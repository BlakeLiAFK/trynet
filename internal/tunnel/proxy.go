// proxy.go 负责到边缘的连接怎么走：从环境变量探测该用哪个代理、组装候选
// 路径、记住走通的那条、以及实际的拨号（直连 / HTTP CONNECT / socks5）。
package tunnel

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/http/httpproxy"
	"golang.org/x/net/proxy"

	"github.com/cloudflare/cloudflared/tlsconfig"
)

// 与边缘建立 TLS 时用的 SNI，和实际连的 IP 无关
const edgeTLSName = "h2.cftunnel.com"

// edgeRoute 是一条到边缘的可能路径。proxy 为 nil 表示直连。
type edgeRoute struct {
	proxy *url.URL
	label string // 打日志用，比如 "direct" 或 "proxy http://host:port"
}

// edgeRouteTimeout 是探路阶段单条路径的限时，避免某条卡住（比如代理接了
// CONNECT 却不转发数据）把整体拖垮
const edgeRouteTimeout = 12 * time.Second

// 拨通过一次之后就记住那条路，后面重连不用每次都从头探。
// 但记住的路只是优先尝试，不是唯一尝试——它失效了（比如换了网络环境，
// 原来通的代理或直连不再可用）就得忘掉它，回去把候选列表重新探一遍，
// 不然会守着一条死路永远连不上，明明环境里还有能用的路。
// 边缘拨号目前是串行调用的，但这里仍然加锁，不依赖这个隐含前提。
var (
	rememberedRouteMu sync.Mutex
	rememberedRoute   *edgeRoute
)

func rememberedEdgeRoute() *edgeRoute {
	rememberedRouteMu.Lock()
	defer rememberedRouteMu.Unlock()
	return rememberedRoute
}

func rememberEdgeRoute(route edgeRoute) {
	rememberedRouteMu.Lock()
	defer rememberedRouteMu.Unlock()
	rememberedRoute = &route
}

func forgetEdgeRoute() {
	rememberedRouteMu.Lock()
	defer rememberedRouteMu.Unlock()
	rememberedRoute = nil
}

// dialEdgeViaFunc 是 dialEdgeVia 的一层间接调用，生产环境里就是它本身；
// 测试用来换成假拨号，不用真的连网络也能测到"记住的路失败后会回退"这条逻辑。
var dialEdgeViaFunc = dialEdgeVia

// Dial 连到边缘并完成 TLS 握手。ALPN 必须是 h2，SNI 必须是 h2.cftunnel.com。
// 边缘用的是 Cloudflare Origin CA 签的证书，公共根证书验不过，所以要把它的根证书加进去。
//
// 到边缘不止一条路可走：用户配的代理可能只对 443 端口生效，CONNECT 到边缘的
// 7844 端口时接了却不转发，握手直接 EOF；这种情况下直连或者环境里备着的其它
// 代理反而通。所以这里按优先级依次试，认死一条路只会白白重试到天荒地老。
func Dial(ctx context.Context, addr string) (*tls.Conn, error) {
	var errs []error
	var deadRoute *edgeRoute

	// 已经探出能用的路了，先试它，不用每次重连都重新探一遍；
	// 一旦它不通了，忘掉它，往下走完整的候选列表重新探
	if route := rememberedEdgeRoute(); route != nil {
		conn, err := dialEdgeViaFunc(ctx, addr, *route)
		if err == nil {
			return conn, nil
		}
		forgetEdgeRoute()
		deadRoute = route
		errs = append(errs, fmt.Errorf("%s: %w", route.label, err))
	}

	routes, err := edgeRoutes(addr)
	if err != nil {
		return nil, err
	}

	for _, route := range routes {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if deadRoute != nil && sameEdgeRoute(route, *deadRoute) {
			continue // 刚试过且失败了，不用再来一遍
		}

		routeCtx, cancel := context.WithTimeout(ctx, edgeRouteTimeout)
		conn, err := dialEdgeViaFunc(routeCtx, addr, route)
		cancel()
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", route.label, err))
			continue
		}

		rememberEdgeRoute(route)
		return conn, nil
	}

	return nil, errors.Join(errs...)
}

// sameEdgeRoute 判断两条路径是不是同一条，用来在重探时跳过刚失败过的那条
func sameEdgeRoute(a, b edgeRoute) bool {
	if (a.proxy == nil) != (b.proxy == nil) {
		return false
	}
	if a.proxy == nil {
		return true
	}
	return routeKey(a.proxy) == routeKey(b.proxy)
}

// dialEdgeVia 沿着一条具体路径建立裸连接并完成 TLS 握手
func dialEdgeVia(ctx context.Context, addr string, route edgeRoute) (*tls.Conn, error) {
	cfg, err := tlsconfig.CreateTunnelConfig("", edgeTLSName)
	if err != nil {
		return nil, err
	}
	cfg.NextProtos = []string{"h2"}
	cfg.MinVersion = tls.VersionTLS12

	raw, err := dialRaw(ctx, addr, route.proxy)
	if err != nil {
		return nil, err
	}
	conn := tls.Client(raw, cfg)
	if err := conn.HandshakeContext(ctx); err != nil {
		_ = raw.Close()
		return nil, fmt.Errorf("tls handshake: %w", err)
	}
	return conn, nil
}

// edgeRoutes 按优先级组装到 addr 的候选路径：
//  1. proxyFor 认为该走的显式代理（用户配了就该先试它）
//  2. 直连（最常见的兜底）
//  3. 环境里其它代理变量指向的代理（比如 HTTPS_PROXY 连不通，但 ALL_PROXY
//     备着能用的 socks5）
//
// 同一个代理 URL 只出现一次。proxyFor 因为 NO_PROXY 之类原因返回 nil 时，
// 直连自然排在最前，后面仍可以继续尝试环境里的其它代理。
func edgeRoutes(addr string) ([]edgeRoute, error) {
	explicit, err := proxyFor(addr)
	if err != nil {
		return nil, err
	}

	seen := map[string]bool{}
	var routes []edgeRoute

	if explicit != nil {
		routes = append(routes, edgeRoute{proxy: explicit, label: "proxy " + explicit.String()})
		seen[routeKey(explicit)] = true
	}

	routes = append(routes, edgeRoute{label: "direct"})

	for _, name := range proxyVars {
		pu, err := parseProxyURL(os.Getenv(name))
		if err != nil || pu == nil {
			continue
		}
		key := routeKey(pu)
		if seen[key] {
			continue
		}
		seen[key] = true
		routes = append(routes, edgeRoute{proxy: pu, label: "proxy " + pu.String()})
	}

	return routes, nil
}

// routeKey 是用来判断两个代理是不是同一个的去重键，忽略大小写和认证信息
func routeKey(pu *url.URL) string {
	return strings.ToLower(pu.Scheme) + "://" + strings.ToLower(pu.Host)
}

// parseProxyURL 解析一个代理地址，容错规则跟 httpproxy 包内部一致：
// 没写 scheme（比如只有 "host:port"）时按 http 处理
func parseProxyURL(raw string) (*url.URL, error) {
	if raw == "" {
		return nil, nil
	}
	pu, err := url.Parse(raw)
	if err == nil && pu.Scheme != "" && pu.Host != "" {
		return pu, nil
	}
	if fallback, ferr := url.Parse("http://" + raw); ferr == nil && fallback.Host != "" {
		return fallback, nil
	}
	if err != nil {
		return nil, err
	}
	return pu, nil
}

// dialRaw 沿着一条具体路径（pu 为 nil 表示直连）建立裸 TCP 连接。
// 这一步 http.ProxyFromEnvironment 帮不上忙 —— 它只作用于 http.Transport，
// 对我们这种自己拨号的场景无效，所以 CONNECT 得手写。
func dialRaw(ctx context.Context, addr string, pu *url.URL) (net.Conn, error) {
	d := &net.Dialer{Timeout: 15 * time.Second}

	if pu == nil {
		return d.DialContext(ctx, "tcp", addr)
	}

	switch pu.Scheme {
	case "socks5", "socks5h":
		return dialSocks5(ctx, pu, addr, d)
	case "http", "https", "":
		return dialHTTPConnect(ctx, pu, addr, d)
	default:
		return nil, fmt.Errorf("unsupported proxy scheme: %s", pu.Scheme)
	}
}

// proxyFor 决定 addr 该走哪个代理，没有就返回 nil。
// httpproxy 负责 HTTPS_PROXY / HTTP_PROXY / NO_PROXY 的标准语义，
// ALL_PROXY 不在它的范围内，这里补上。
func proxyFor(addr string) (*url.URL, error) {
	cfg := httpproxy.FromEnvironment()
	if cfg.HTTPSProxy == "" {
		cfg.HTTPSProxy = firstEnv("ALL_PROXY", "all_proxy")
	}
	// 目标是裸 TCP，这里假装成 https 好让 HTTPS_PROXY 生效
	return cfg.ProxyFunc()(&url.URL{Scheme: "https", Host: addr})
}

func firstEnv(names ...string) string {
	for _, n := range names {
		if v := os.Getenv(n); v != "" {
			return v
		}
	}
	return ""
}

// 代理环境变量的检查顺序，和 proxyFor 保持一致
var proxyVars = []string{"HTTPS_PROXY", "https_proxy", "ALL_PROXY", "all_proxy", "HTTP_PROXY", "http_proxy"}

// detectProxy 按优先级找出实际生效的代理环境变量，供 banner.go 展示用
func detectProxy() (value, source string) {
	for _, name := range proxyVars {
		if v := os.Getenv(name); v != "" {
			return v, name
		}
	}
	return "", ""
}

// findSocks 从所有代理变量里挑出第一个 socks5 的值，用于在 http 代理不通时给建议
func findSocks() string {
	for _, name := range proxyVars {
		if v := os.Getenv(name); isSocks(v) {
			return v
		}
	}
	return ""
}

func isSocks(v string) bool {
	return strings.HasPrefix(strings.ToLower(v), "socks5")
}

// ProxyState 是当前生效的代理状况，给调用方拼提示文案用。
//
// 导出这一个只读快照，而不是把 detectProxy/findSocks/isSocks 三个内部函数
// 各自导出：外面要的是"现在什么情况"，不是"你怎么查出来的"。查法以后要改
// （加一个环境变量、支持 pac），改这一个函数就够，调用方一个字不用动。
type ProxyState struct {
	Value   string // 生效的代理地址，空表示没配
	Source  string // 来自哪个环境变量
	IsSocks bool   // 是不是 socks5
	Suggest string // 环境里能找到的一个 socks5 地址，用来给建议；没有则为空
}

// ProxyStatus 读一遍环境变量，报告当前的代理状况。
func ProxyStatus() ProxyState {
	value, source := detectProxy()
	return ProxyState{
		Value:   value,
		Source:  source,
		IsSocks: isSocks(value),
		Suggest: findSocks(),
	}
}

// dialHTTPConnect 走 HTTP 代理的 CONNECT 隧道
func dialHTTPConnect(ctx context.Context, pu *url.URL, addr string, d *net.Dialer) (net.Conn, error) {
	conn, err := d.DialContext(ctx, "tcp", proxyHostPort(pu))
	if err != nil {
		return nil, fmt.Errorf("dial proxy %s: %w", pu.Host, err)
	}

	// 代理本身是 https 的，先跟代理做一层 TLS
	if pu.Scheme == "https" {
		tc := tls.Client(conn, &tls.Config{ServerName: pu.Hostname(), MinVersion: tls.VersionTLS12})
		if err := tc.HandshakeContext(ctx); err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("tls handshake with proxy %s: %w", pu.Host, err)
		}
		conn = tc
	}

	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}

	var req strings.Builder
	fmt.Fprintf(&req, "CONNECT %s HTTP/1.1\r\nHost: %s\r\nUser-Agent: trynet\r\n", addr, addr)
	if auth := basicAuth(pu.User); auth != "" {
		fmt.Fprintf(&req, "Proxy-Authorization: Basic %s\r\n", auth)
	}
	req.WriteString("\r\n")

	if _, err := conn.Write([]byte(req.String())); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("send CONNECT: %w", err)
	}

	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, &http.Request{Method: http.MethodConnect})
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("read proxy response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		_ = conn.Close()
		return nil, fmt.Errorf("proxy refused CONNECT %s: %s (many proxies only allow port 443, so 7844 gets blocked)", addr, resp.Status)
	}
	_ = conn.SetDeadline(time.Time{})

	// 正常情况下 br 里不会有剩余数据，但别赌，包一层保险
	return &bufConn{Conn: conn, r: br}, nil
}

func dialSocks5(ctx context.Context, pu *url.URL, addr string, d *net.Dialer) (net.Conn, error) {
	var auth *proxy.Auth
	if pu.User != nil {
		pw, _ := pu.User.Password()
		auth = &proxy.Auth{User: pu.User.Username(), Password: pw}
	}
	dialer, err := proxy.SOCKS5("tcp", proxyHostPort(pu), auth, d)
	if err != nil {
		return nil, err
	}
	cd, ok := dialer.(proxy.ContextDialer)
	if !ok {
		return nil, fmt.Errorf("socks5 dialer does not support context")
	}
	conn, err := cd.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("dial %s via socks5 proxy %s: %w", addr, pu.Host, err)
	}
	return conn, nil
}

// proxyHostPort 补齐代理地址里省略的端口
func proxyHostPort(pu *url.URL) string {
	if pu.Port() != "" {
		return pu.Host
	}
	switch pu.Scheme {
	case "https":
		return net.JoinHostPort(pu.Hostname(), "443")
	case "socks5", "socks5h":
		return net.JoinHostPort(pu.Hostname(), "1080")
	default:
		return net.JoinHostPort(pu.Hostname(), "80")
	}
}

func basicAuth(u *url.Userinfo) string {
	if u == nil {
		return ""
	}
	pw, _ := u.Password()
	return base64.StdEncoding.EncodeToString([]byte(u.Username() + ":" + pw))
}

// bufConn 让读操作走已经预读过的 bufio.Reader，避免丢掉缓冲里的字节
type bufConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *bufConn) Read(p []byte) (int, error) { return c.r.Read(p) }
