// tunnel.go 管一条边缘连接的生命周期：在已建立的 TLS 连接上跑 HTTP/2 服务端、
// 用控制流完成注册、把边缘转进来的请求反代到本地。
// main.go 负责"拿到凭据、决定连哪里"，这里负责"连上之后怎么办"。
package tunnel

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"runtime"
	"strings"
	"sync"
	"time"
	"trynet/internal/buildinfo"

	"github.com/google/uuid"
	"golang.org/x/net/http2"

	"github.com/cloudflare/cloudflared/tunnelrpc"
	"github.com/cloudflare/cloudflared/tunnelrpc/pogs"
)

const (
	// 边缘用这个头区分控制流、websocket、配置下发等内部连接类型
	headerUpgrade        = "Cf-Cloudflared-Proxy-Connection-Upgrade"
	headerTCPProxySrc    = "Cf-Cloudflared-Proxy-Src"
	upgradeControlStream = "control-stream"

	// 声明了 serialized_headers 特性后，业务响应头要序列化进这个头里回给边缘
	headerRespHeaders = "cf-cloudflared-response-headers"
	headerRespMeta    = "cf-cloudflared-response-meta"
	respMetaOrigin    = `{"src":"origin"}`
)

// Result 是一次连接尝试的结果，run 用它决定要不要换隧道、要不要重置退避
type Result struct {
	Err        error
	Connected  bool // 曾经注册成功过，说明这次不是"连不上"，重试退避该归零
	Rejected   bool // 边缘明确拒绝了注册，值得换一个新隧道再试
	DialFailed bool // 拨号本身就没成功（含代理不可用），跟注册被拒、连接正常断开是两回事
}

// Serve 建立一条到边缘的 TLS 连接，然后在这条连接上跑 HTTP/2 服务端。
// 注意角色是反过来的：我们主动拨号，但由边缘向我们发请求。
//
// reconnect 说明这是不是断线之后的重连，只决定回调走 OnReady 还是 OnReconnect，
// 不影响连接和注册本身的逻辑。
//
// 注册成功之后要给用户看什么，这个包不参与决定——它只在该说话的时候调 cb，
// 由调用方去拼那些字。这里曾经直接调 banner 的打印函数，还顺带存着
// target/extraLine/announceStore 三个纯展示字段，等于让一个管连接的类型兼职
// 排版；拆包之后这条依赖会成环，正好把它倒过来。
// EdgeHosts 是 SRV 记录 _v2-origintunneld._tcp.argotunnel.com 的固定结果，
// 直接写死省掉一次查询。调用方按顺序轮换，一个连不上就换下一个。
var EdgeHosts = []string{
	"region1.v2.argotunnel.com:7844",
	"region2.v2.argotunnel.com:7844",
}

func Serve(ctx context.Context, info *Info, origin, edgeHost string, reconnect bool, cb Callbacks) Result {
	conn, err := Dial(ctx, edgeHost)
	if err != nil {
		return Result{Err: err, DialFailed: true}
	}
	defer conn.Close()

	// Dial 成功之后会把走通的那条路记下来，直接读出来标注这次连接用的是哪条，
	// 不用再单独把路径标签从 Dial 一路传出来
	route := "unknown"
	if r := rememberedEdgeRoute(); r != nil {
		route = r.label
	}

	// 连接本身用独立的 context，不直接被外层 ctx（Ctrl+C）打断：收到停止信号时
	// 先在这条连接上跑一次优雅退出 RPC 通知边缘注销，跑完/超时了再真正断开，
	// 不然边缘那边会留一条僵尸注册直到它自己超时踢掉。
	connCtx, cancelConn := context.WithCancel(context.Background())
	defer cancelConn()

	t := &tunnel{
		info:      info,
		origin:    origin,
		localIP:   addrIP(conn.LocalAddr()),
		edgeIP:    addrIP(conn.RemoteAddr()), // 走代理时这里是代理地址，边缘只拿它做日志，不影响功能
		route:     route,
		reconnect: reconnect,
		cb:        cb,
	}
	t.proxy = &httputil.ReverseProxy{
		Rewrite:        t.rewrite,
		ModifyResponse: modifyResponse,
		ErrorLog:       log.Default(),
	}

	go func() {
		select {
		case <-ctx.Done():
			t.gracefulShutdown()
		case <-connCtx.Done():
		}
		cancelConn()
	}()

	(&http2.Server{}).ServeConn(conn, &http2.ServeConnOpts{Context: connCtx, Handler: t})

	t.mu.Lock()
	defer t.mu.Unlock()
	if t.result.Err == nil {
		t.result.Err = errors.New("edge closed the connection")
	}
	return t.result
}

// Callbacks 是注册成功后通知调用方的两个钩子。都可以为 nil。
//
// 这个包不知道也不该知道用户看到的是一个方框还是一行日志，它只负责在对的时机
// 把事实报出去：连上了哪个边缘机房、主机名是什么、走的哪条路。
type Callbacks struct {
	OnReady     func(location, hostname, route string) // 首次注册成功
	OnReconnect func(route string)                     // 断线之后恢复
}

type tunnel struct {
	info    *Info
	origin  string // 127.0.0.1:port
	localIP net.IP
	edgeIP  net.IP
	proxy   *httputil.ReverseProxy

	route     string // 这次连接走的边缘路径标签，比如 "direct" 或 "proxy http://..."
	reconnect bool   // 是不是断线之后的重连，决定回调走 OnReady 还是 OnReconnect
	cb        Callbacks

	mu     sync.Mutex
	client tunnelrpc.RegistrationClient // 注册成功后记下来，收到停止信号时用它发优雅退出
	result Result
}

func (t *tunnel) setResult(r Result) {
	t.mu.Lock()
	t.result = r
	t.mu.Unlock()
}

// gracefulShutdown 在停止信号到达时尝试通知边缘注销这条连接。
// 用独立的、还没被取消的 context，因为外层 ctx 这时已经 canceled 了，没法再发 RPC。
func (t *tunnel) gracefulShutdown() {
	t.mu.Lock()
	client := t.client
	t.mu.Unlock()
	if client == nil {
		return // 还没注册成功，没什么好通知边缘的
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.GracefulShutdown(ctx, 3*time.Second); err != nil {
		log.Printf("graceful shutdown: %v", err)
	}
}

func addrIP(a net.Addr) net.IP {
	if t, ok := a.(*net.TCPAddr); ok {
		return t.IP
	}
	return net.IPv4zero
}

func (t *tunnel) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Header.Get(headerUpgrade) == upgradeControlStream:
		t.serveControlStream(w, r)
	case r.Header.Get(headerUpgrade) != "" || r.Header.Get(headerTCPProxySrc) != "":
		// websocket / TCP / 配置下发，本版本不支持
		http.Error(w, "trynet only supports plain HTTP requests", http.StatusNotImplemented)
	default:
		t.proxy.ServeHTTP(w, r)
	}
}

func (t *tunnel) rewrite(pr *httputil.ProxyRequest) {
	pr.Out.URL.Scheme = "http"
	pr.Out.URL.Host = t.origin
	pr.Out.Host = pr.In.Host
	pr.Out.Header.Del(headerUpgrade)
	pr.SetXForwarded()
}

// serveControlStream 把这条 HTTP/2 流当成双向管道，在上面跑 Cap'n Proto 注册 RPC。
// 注册成功之前，公网域名是不通的。
func (t *tunnel) serveControlStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "no flusher", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	tunnelID, err := uuid.Parse(t.info.ID)
	if err != nil {
		// 理论上不会发生（ID 是我们自己刚从 JSON 解出来的），但万一发生了，
		// 标记 rejected 让 run() 试着换一个新隧道，属于自愈而不是卡死。
		t.setResult(Result{Err: fmt.Errorf("invalid tunnel id: %w", err), Rejected: true})
		return
	}

	client := tunnelrpc.NewRegistrationClient(r.Context(), &streamRWC{r: r.Body, w: w, f: flusher}, 15*time.Second)
	defer client.Close()

	details, err := client.RegisterConnection(
		r.Context(),
		pogs.TunnelAuth{AccountTag: t.info.AccountTag, TunnelSecret: t.info.Secret},
		tunnelID,
		t.connOptions(),
		0, // connIndex：trynet 不管快速隧道还是固定域名都只维持一条连接，固定传 0
		t.edgeIP,
	)
	if err != nil {
		t.setResult(Result{Err: fmt.Errorf("register connection: %w", err), Rejected: true})
		return
	}

	t.mu.Lock()
	t.client = client
	t.result.Connected = true
	t.mu.Unlock()

	// 首次连接和重连分成两个回调：首次要把完整的地址展示出来，重连时主机名
	// 没变，再刷一遍整个框只是噪音。具体打成什么样由调用方决定。
	//
	// hostname 作为参数传出去而不是让调用方从自己手里的 info 读：注册被拒时
	// 外层会换一份新凭据重试，闭包捕获的那个 info 就过期了——传出去的这份
	// 永远是这条连接实际用的。
	if t.reconnect {
		if t.cb.OnReconnect != nil {
			t.cb.OnReconnect(t.route)
		}
	} else if t.cb.OnReady != nil {
		t.cb.OnReady(details.Location, t.info.Hostname, t.route)
	}
	<-r.Context().Done()
}

func (t *tunnel) connOptions() *pogs.ConnectionOptions {
	id := uuid.New()
	return &pogs.ConnectionOptions{
		Client: pogs.ClientInfo{
			ClientID: id[:],
			Features: []string{"serialized_headers"},
			Version:  "trynet/" + buildinfo.Version,
			Arch:     runtime.GOOS + "_" + runtime.GOARCH,
		},
		OriginLocalIP:   t.localIP,
		ReplaceExisting: true, // 重连时顶掉边缘上的旧连接，省掉重复注册的错误处理
	}
}

// streamRWC 把 HTTP/2 的请求体 + 响应体拼成一个双向流，每次写完都要 flush，
// 否则 http2 会攒着不发，RPC 直接卡死。
type streamRWC struct {
	r io.Reader
	w io.Writer
	f http.Flusher
}

func (s *streamRWC) Read(p []byte) (int, error) { return s.r.Read(p) }

func (s *streamRWC) Write(p []byte) (int, error) {
	n, err := s.w.Write(p)
	if err == nil {
		s.f.Flush()
	}
	return n, err
}

func (s *streamRWC) Close() error { return nil }

// modifyResponse 把业务响应头按 serialized_headers 约定塞进一个头里。
// 边缘拿到的是 HTTP/2 帧，直接透传原始头会被 HTTP/2 的头部校验规则挡掉。
func modifyResponse(resp *http.Response) error {
	user := make(http.Header, len(resp.Header))
	for name, values := range resp.Header {
		if isControlResponseHeader(strings.ToLower(name)) {
			continue
		}
		user[name] = values
	}

	// content-length 对边缘本身有意义，所以序列化之外还要原样留一份
	contentLength := resp.Header.Get("Content-Length")

	resp.Header = http.Header{}
	if contentLength != "" {
		resp.Header.Set("Content-Length", contentLength)
	}
	resp.Header.Set(headerRespHeaders, serializeHeaders(user))
	resp.Header.Set(headerRespMeta, respMetaOrigin)
	return nil
}

func isControlResponseHeader(name string) bool {
	return strings.HasPrefix(name, ":") ||
		strings.HasPrefix(name, "cf-int-") ||
		strings.HasPrefix(name, "cf-cloudflared-") ||
		strings.HasPrefix(name, "cf-proxy-")
}

var headerEncoding = base64.RawStdEncoding

// serializeHeaders 把头名和头值分别 base64，拼成 name:value;name:value
func serializeHeaders(h http.Header) string {
	var buf strings.Builder
	for name, values := range h {
		for _, v := range values {
			if buf.Len() > 0 {
				buf.WriteByte(';')
			}
			buf.WriteString(headerEncoding.EncodeToString([]byte(name)))
			buf.WriteByte(':')
			buf.WriteString(headerEncoding.EncodeToString([]byte(v)))
		}
	}
	return buf.String()
}
