package tunnel

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/url"
	"sync"
	"testing"
)

// clearProxyEnv 把所有会影响 proxyFor/edgeRoutes 的环境变量先清空，
// 避免测试结果受跑测试的机器上本来就有的代理设置影响。
// 写法照抄 TestProxyFor。
func clearProxyEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"http_proxy", "HTTP_PROXY", "https_proxy", "HTTPS_PROXY", "no_proxy", "NO_PROXY", "ALL_PROXY", "all_proxy"} {
		t.Setenv(k, "")
	}
}

// http 代理不通时，banner 应从环境里挑出 socks5 建议给用户
func TestFindSocksSuggestion(t *testing.T) {
	for _, k := range proxyVars {
		t.Setenv(k, "")
	}
	t.Setenv("HTTPS_PROXY", "http://192.168.3.181:2080") // 生效但连不上边缘
	t.Setenv("ALL_PROXY", "socks5://192.168.3.181:1080") // 环境里备着的可用代理

	value, source := detectProxy()
	if value != "http://192.168.3.181:2080" || source != "HTTPS_PROXY" {
		t.Fatalf("生效代理判断错: %s from %s", value, source)
	}
	if isSocks(value) {
		t.Fatal("http 代理不该被判成 socks5")
	}
	if got := findSocks(); got != "socks5://192.168.3.181:1080" {
		t.Errorf("没挑出 socks5 建议: %q", got)
	}
}

func TestProxyFor(t *testing.T) {
	const edge = "region1.v2.argotunnel.com:7844"

	cases := []struct {
		name string
		env  map[string]string
		want string // 期望的代理 host，空表示直连
	}{
		{"没配代理", nil, ""},
		{"https_proxy 小写", map[string]string{"https_proxy": "http://127.0.0.1:7890"}, "127.0.0.1:7890"},
		{"HTTPS_PROXY 大写", map[string]string{"HTTPS_PROXY": "http://127.0.0.1:8080"}, "127.0.0.1:8080"},
		{"ALL_PROXY 兜底", map[string]string{"ALL_PROXY": "socks5://127.0.0.1:1080"}, "127.0.0.1:1080"},
		{"NO_PROXY 排除", map[string]string{
			"https_proxy": "http://127.0.0.1:7890",
			"no_proxy":    "argotunnel.com",
		}, ""},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for _, k := range []string{"http_proxy", "HTTP_PROXY", "https_proxy", "HTTPS_PROXY", "no_proxy", "NO_PROXY", "ALL_PROXY", "all_proxy"} {
				t.Setenv(k, "")
			}
			for k, v := range c.env {
				t.Setenv(k, v)
			}

			pu, err := proxyFor(edge)
			if err != nil {
				t.Fatal(err)
			}
			got := ""
			if pu != nil {
				got = pu.Host
			}
			if got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestProxyHostPort(t *testing.T) {
	cases := map[string]string{
		"http://p.local":         "p.local:80",
		"https://p.local":        "p.local:443",
		"socks5://p.local":       "p.local:1080",
		"http://p.local:3128":    "p.local:3128",
		"socks5://u:p@p.local:9": "p.local:9",
	}
	for raw, want := range cases {
		pu, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		if got := proxyHostPort(pu); got != want {
			t.Errorf("%s: got %q, want %q", raw, got, want)
		}
	}
}

func TestEdgeRoutesNoProxyConfigured(t *testing.T) {
	clearProxyEnv(t)

	routes, err := edgeRoutes("region1.v2.argotunnel.com:7844")
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 1 {
		t.Fatalf("got %d routes, want 1: %+v", len(routes), routes)
	}
	if routes[0].proxy != nil {
		t.Errorf("第一条应该是直连，got %v", routes[0].proxy)
	}
}

func TestEdgeRoutesExplicitProxyThenDirect(t *testing.T) {
	clearProxyEnv(t)
	t.Setenv("HTTPS_PROXY", "http://192.168.3.181:2080")

	routes, err := edgeRoutes("region1.v2.argotunnel.com:7844")
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 2 {
		t.Fatalf("got %d routes, want 2: %+v", len(routes), routes)
	}
	if routes[0].proxy == nil || routes[0].proxy.Host != "192.168.3.181:2080" {
		t.Errorf("第一条应该是配置的代理, got %+v", routes[0])
	}
	if routes[1].proxy != nil {
		t.Errorf("第二条应该是直连, got %+v", routes[1])
	}
}

func TestEdgeRoutesFallbackFromOtherEnvVar(t *testing.T) {
	clearProxyEnv(t)
	t.Setenv("HTTPS_PROXY", "http://192.168.3.181:2080")
	t.Setenv("ALL_PROXY", "socks5://127.0.0.1:1080")

	routes, err := edgeRoutes("region1.v2.argotunnel.com:7844")
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 3 {
		t.Fatalf("got %d routes, want 3: %+v", len(routes), routes)
	}
	if routes[0].proxy == nil || routes[0].proxy.Host != "192.168.3.181:2080" {
		t.Errorf("第一条应该是 HTTPS_PROXY, got %+v", routes[0])
	}
	if routes[1].proxy != nil {
		t.Errorf("第二条应该是直连, got %+v", routes[1])
	}
	if routes[2].proxy == nil || routes[2].proxy.Host != "127.0.0.1:1080" || routes[2].proxy.Scheme != "socks5" {
		t.Errorf("第三条应该是 ALL_PROXY 的 socks5, got %+v", routes[2])
	}
}

func TestEdgeRoutesDedupSameProxy(t *testing.T) {
	clearProxyEnv(t)
	t.Setenv("HTTPS_PROXY", "http://192.168.3.181:2080")
	t.Setenv("ALL_PROXY", "http://192.168.3.181:2080")

	routes, err := edgeRoutes("region1.v2.argotunnel.com:7844")
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 2 {
		t.Fatalf("同一个代理不该重复出现: got %d routes, want 2: %+v", len(routes), routes)
	}
}

func TestEdgeRoutesNoProxyExcludesExplicit(t *testing.T) {
	clearProxyEnv(t)
	t.Setenv("HTTPS_PROXY", "http://192.168.3.181:2080")
	t.Setenv("NO_PROXY", "argotunnel.com")

	routes, err := edgeRoutes("region1.v2.argotunnel.com:7844")
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) == 0 {
		t.Fatal("候选路径不该是空的")
	}
	if routes[0].proxy != nil {
		t.Errorf("被 NO_PROXY 排除后，直连应该排在最前, got %+v", routes[0])
	}
}

// clearRememberedRoute 保证测试之间不会因为全局的"记住的路"状态互相影响
func clearRememberedRoute(t *testing.T) {
	t.Helper()
	forgetEdgeRoute()
	t.Cleanup(forgetEdgeRoute)
}

func TestRememberEdgeRouteThenRecall(t *testing.T) {
	clearRememberedRoute(t)

	rememberEdgeRoute(edgeRoute{label: "direct"})

	got := rememberedEdgeRoute()
	if got == nil || got.label != "direct" {
		t.Fatalf("rememberedEdgeRoute() = %+v, want direct", got)
	}
}

func TestForgetEdgeRouteClearsMemory(t *testing.T) {
	clearRememberedRoute(t)

	rememberEdgeRoute(edgeRoute{label: "direct"})
	forgetEdgeRoute()

	if got := rememberedEdgeRoute(); got != nil {
		t.Fatalf("forgetEdgeRoute 之后 rememberedEdgeRoute() = %+v, want nil", got)
	}
}

// TestRememberedRouteConcurrentAccess 并发读写"记住的路"这个状态机，
// 用 go test -race 跑才有意义，但没有 -race 也能验证不会 panic 或死锁。
func TestRememberedRouteConcurrentAccess(t *testing.T) {
	clearRememberedRoute(t)

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(3)
		go func(n int) {
			defer wg.Done()
			label := "direct"
			if n%2 == 0 {
				label = "proxy http://example.invalid:1080"
			}
			rememberEdgeRoute(edgeRoute{label: label})
		}(i)
		go func() {
			defer wg.Done()
			_ = rememberedEdgeRoute()
		}()
		go func() {
			defer wg.Done()
			forgetEdgeRoute()
		}()
	}
	wg.Wait()
}

// fakeTLSConn 造一个不联网、也不做真实握手的 *tls.Conn，够 dialEdgeViaFunc
// 的测试替身返回用——Dial 本身只关心拨号成不成功，不会真的收发数据。
func fakeTLSConn(t *testing.T) *tls.Conn {
	t.Helper()
	client, server := net.Pipe()
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})
	return tls.Client(client, &tls.Config{InsecureSkipVerify: true})
}

func TestDialEdgeReusesRememberedRouteWithoutReprobing(t *testing.T) {
	clearProxyEnv(t)
	clearRememberedRoute(t)

	origDial := dialEdgeViaFunc
	t.Cleanup(func() { dialEdgeViaFunc = origDial })

	calls := 0
	dialEdgeViaFunc = func(ctx context.Context, addr string, route edgeRoute) (*tls.Conn, error) {
		calls++
		return fakeTLSConn(t), nil
	}

	rememberEdgeRoute(edgeRoute{label: "direct"})

	conn, err := Dial(context.Background(), "region1.v2.argotunnel.com:7844")
	if err != nil {
		t.Fatalf("Dial failed: %v", err)
	}
	if conn == nil {
		t.Fatal("want non-nil conn")
	}
	if calls != 1 {
		t.Errorf("记住的路能连通时不该重新探路, calls = %d, want 1", calls)
	}
}

func TestDialEdgeForgetsAndReprobesWhenRememberedRouteFails(t *testing.T) {
	clearProxyEnv(t)
	clearRememberedRoute(t)
	t.Setenv("HTTPS_PROXY", "http://192.168.3.181:2080")
	t.Setenv("ALL_PROXY", "socks5://127.0.0.1:1080")

	addr := "region1.v2.argotunnel.com:7844"
	routes, err := edgeRoutes(addr)
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 3 {
		t.Fatalf("测试前置条件：想要 3 条候选路径, got %d: %+v", len(routes), routes)
	}

	// 假装之前记住的是配置的那个代理，换了网络环境后它不通了
	rememberEdgeRoute(routes[0])

	origDial := dialEdgeViaFunc
	t.Cleanup(func() { dialEdgeViaFunc = origDial })

	var attempted []string
	dialEdgeViaFunc = func(ctx context.Context, addr string, route edgeRoute) (*tls.Conn, error) {
		attempted = append(attempted, route.label)
		switch route.label {
		case routes[0].label:
			return nil, errors.New("connection refused")
		case routes[1].label: // 直连，第二优先级
			return fakeTLSConn(t), nil
		default:
			t.Fatalf("不该试到 %s：前面已经有一条成功了", route.label)
			return nil, nil
		}
	}

	conn, err := Dial(context.Background(), addr)
	if err != nil {
		t.Fatalf("Dial failed: %v", err)
	}
	if conn == nil {
		t.Fatal("want non-nil conn")
	}

	wantAttempts := []string{routes[0].label, routes[1].label}
	if len(attempted) != len(wantAttempts) {
		t.Fatalf("attempted = %v, want %v（失效的那条不该在候选列表里被重复试一遍）", attempted, wantAttempts)
	}
	for i, label := range wantAttempts {
		if attempted[i] != label {
			t.Errorf("attempted[%d] = %q, want %q", i, attempted[i], label)
		}
	}

	got := rememberedEdgeRoute()
	if got == nil || got.label != routes[1].label {
		t.Errorf("应该记住新连通的那条路, got %+v, want %+v", got, routes[1])
	}
}

func TestDialEdgeReportsRememberedFailureOnceWhenAllRoutesFail(t *testing.T) {
	clearProxyEnv(t)
	clearRememberedRoute(t)

	addr := "region1.v2.argotunnel.com:7844"
	routes, err := edgeRoutes(addr)
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 1 {
		t.Fatalf("测试前置条件：没配代理时只该有直连这一条, got %+v", routes)
	}

	rememberEdgeRoute(routes[0])

	origDial := dialEdgeViaFunc
	t.Cleanup(func() { dialEdgeViaFunc = origDial })

	calls := 0
	wantErr := errors.New("network unreachable")
	dialEdgeViaFunc = func(ctx context.Context, addr string, route edgeRoute) (*tls.Conn, error) {
		calls++
		return nil, wantErr
	}

	_, err = Dial(context.Background(), addr)
	if err == nil {
		t.Fatal("want error, got nil")
	}
	if !errors.Is(err, wantErr) {
		t.Errorf("最终错误里应该包含底层的失败原因: %v", err)
	}
	if calls != 1 {
		t.Errorf("唯一的候选路径就是刚失败的记忆路径，不该再重复试一遍, calls = %d, want 1", calls)
	}
	if got := rememberedEdgeRoute(); got != nil {
		t.Errorf("失败之后不该继续记着这条路, got %+v", got)
	}
}
