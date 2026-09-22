package tunnel

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/cloudflare/cloudflared/tunnelrpc/pogs"
)

// 头部序列化是隧道能不能正常返回内容的关键，格式错了浏览器就拿不到 Content-Type。
// 这里按边缘的解码方式反解一遍，确认能原样还原。
func TestSerializeHeaders(t *testing.T) {
	in := http.Header{}
	in.Set("Content-Type", "text/html; charset=utf-8")
	in.Set("X-Empty", "")
	in.Add("Set-Cookie", "a=1")
	in.Add("Set-Cookie", "b=2")

	got, err := deserializeHeaders(serializeHeaders(in))
	if err != nil {
		t.Fatalf("反解失败: %v", err)
	}
	if len(got) != len(in) {
		t.Fatalf("头数量不对: got %d, want %d", len(got), len(in))
	}
	for name, values := range in {
		if strings.Join(got[name], "|") != strings.Join(values, "|") {
			t.Errorf("%s: got %v, want %v", name, got[name], values)
		}
	}
}

// 按 cloudflared 边缘的约定反解：base64(name):base64(value) 用 ; 分隔
func deserializeHeaders(s string) (http.Header, error) {
	h := http.Header{}
	for _, pair := range strings.Split(s, ";") {
		if pair == "" {
			continue
		}
		name, value, ok := strings.Cut(pair, ":")
		if !ok {
			return nil, errors.New("头部格式不对")
		}
		n, err := headerEncoding.DecodeString(name)
		if err != nil {
			return nil, err
		}
		v, err := headerEncoding.DecodeString(value)
		if err != nil {
			return nil, err
		}
		h[string(n)] = append(h[string(n)], string(v))
	}
	return h, nil
}

func TestModifyResponseKeepsContentLength(t *testing.T) {
	resp := &http.Response{Header: http.Header{}}
	resp.Header.Set("Content-Type", "application/json")
	resp.Header.Set("Content-Length", "42")
	resp.Header.Set("Cf-Cloudflared-Response-Meta", "应该被丢掉")

	if err := modifyResponse(resp); err != nil {
		t.Fatal(err)
	}
	if got := resp.Header.Get("Content-Length"); got != "42" {
		t.Errorf("Content-Length 丢了: %q", got)
	}
	if resp.Header.Get(headerRespMeta) != respMetaOrigin {
		t.Errorf("meta 头不对: %q", resp.Header.Get(headerRespMeta))
	}

	user, err := deserializeHeaders(resp.Header.Get(headerRespHeaders))
	if err != nil {
		t.Fatal(err)
	}
	if user.Get("Content-Type") != "application/json" {
		t.Errorf("Content-Type 没被序列化: %v", user)
	}
	if len(user["Cf-Cloudflared-Response-Meta"]) != 0 {
		t.Errorf("控制头不该被序列化进去: %v", user)
	}
}

// fakeRegClient 是 tunnelrpc.RegistrationClient 的假实现，
// 用来单测 gracefulShutdown() 的逻辑，不需要真的网络连接
type fakeRegClient struct {
	shutdownCalled bool
	shutdownCtx    context.Context
	shutdownGrace  time.Duration
	shutdownErr    error
}

func (f *fakeRegClient) RegisterConnection(context.Context, pogs.TunnelAuth, uuid.UUID, *pogs.ConnectionOptions, uint8, net.IP) (*pogs.ConnectionDetails, error) {
	return nil, nil
}
func (f *fakeRegClient) SendLocalConfiguration(context.Context, []byte) error { return nil }
func (f *fakeRegClient) GracefulShutdown(ctx context.Context, gracePeriod time.Duration) error {
	f.shutdownCalled = true
	f.shutdownCtx = ctx
	f.shutdownGrace = gracePeriod
	return f.shutdownErr
}
func (f *fakeRegClient) Close() {}

func TestGracefulShutdownCallsRPC(t *testing.T) {
	fake := &fakeRegClient{}
	tun := &tunnel{client: fake}

	tun.gracefulShutdown()

	if !fake.shutdownCalled {
		t.Fatal("GracefulShutdown 没被调用")
	}
	if fake.shutdownGrace != 3*time.Second {
		t.Errorf("grace period = %v, want 3s", fake.shutdownGrace)
	}
	if dl, ok := fake.shutdownCtx.Deadline(); !ok || time.Until(dl) > 5*time.Second {
		t.Errorf("ctx 应该带一个 <=5s 的超时，避免 Ctrl+C 卡死: deadline ok=%v", ok)
	}
}

// 还没注册成功就收到停止信号时，client 是 nil，不该 panic 也不该瞎调 RPC
func TestGracefulShutdownNoopWithoutClient(t *testing.T) {
	tun := &tunnel{}
	tun.gracefulShutdown() // 不 panic 就算过
}

// Dial 本身失败（含代理不可用）时，Serve() 应该把 dialFailed 标出来，
// 这样 run() 才知道该在重试提示之后打一条代理建议
func TestServeSetsDialFailedOnDialError(t *testing.T) {
	clearProxyEnv(t)
	clearRememberedRoute(t)

	origDial := dialEdgeViaFunc
	t.Cleanup(func() { dialEdgeViaFunc = origDial })
	dialEdgeViaFunc = func(ctx context.Context, addr string, route edgeRoute) (*tls.Conn, error) {
		return nil, errors.New("connection refused")
	}

	result := Serve(context.Background(), nil, "", "region1.v2.argotunnel.com:7844", false, Callbacks{})
	if !result.DialFailed {
		t.Error("拨号失败应该置 dialFailed = true")
	}
	if result.Rejected {
		t.Error("拨号失败不是注册被拒，rejected 不该是 true")
	}
	if result.Connected {
		t.Error("拨号失败不该算 connected")
	}
	if result.Err == nil {
		t.Error("应该带上拨号失败的原因")
	}
}
