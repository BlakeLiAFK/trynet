package cfapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeCFServer 包一层 httptest.Server，额外记录 tunnel/DNS 记录的 DELETE 请求
// 有没有被打到——Teardown 该删的场景和不该删的场景都要能断言到，
// 不然"复用场景不删"这种保护很容易在改代码时悄悄回归。
type fakeCFServer struct {
	*httptest.Server
	mu            sync.Mutex
	tunnelDeleted bool
	dnsDeleted    bool
}

func (fs *fakeCFServer) markTunnelDeleted() {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	fs.tunnelDeleted = true
}

func (fs *fakeCFServer) markDNSDeleted() {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	fs.dnsDeleted = true
}

func (fs *fakeCFServer) wasTunnelDeleted() bool {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return fs.tunnelDeleted
}

func (fs *fakeCFServer) wasDNSDeleted() bool {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return fs.dnsDeleted
}

// newFakeCloudflareServer 起一个假的 Cloudflare API，tunnelExists/dnsExists
// 控制它是走"查到已有资源直接复用"还是"什么都没有，从头创建"这条路径。
func newFakeCloudflareServer(t *testing.T, tunnelExists, dnsExists bool) *fakeCFServer {
	t.Helper()
	rawToken := `eyJhIjoiYWNjb3VudC10YWciLCJ0IjoidHVubmVsLWlkIiwicyI6ImMyVmpjbVYwZG5jPSJ9`
	fs := &fakeCFServer{}

	mux := http.NewServeMux()
	mux.HandleFunc("/accounts", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "errors": []any{},
			"result": []map[string]string{{"id": "acct1", "name": "Test"}}})
	})
	mux.HandleFunc("/zones", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "errors": []any{},
			"result": []map[string]string{{"id": "zone1", "name": "example.com"}}})
	})
	mux.HandleFunc("/accounts/acct1/cfd_tunnel", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			if tunnelExists {
				t.Fatal("should not create a tunnel when one already exists")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "errors": []any{},
				"result": map[string]string{"id": "tunnel-id", "name": "trynet-files-example-com"}})
			return
		}
		result := []map[string]string{}
		if tunnelExists {
			result = []map[string]string{{"id": "tunnel-id", "name": "trynet-files-example-com"}}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "errors": []any{}, "result": result})
	})
	mux.HandleFunc("/accounts/acct1/cfd_tunnel/tunnel-id/token", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "errors": []any{}, "result": rawToken})
	})
	mux.HandleFunc("/zones/zone1/dns_records", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			var body cfCreateDNSBody
			_ = json.NewDecoder(r.Body).Decode(&body)
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "errors": []any{},
				"result": map[string]any{"id": "rec1", "name": body.Name, "type": body.Type,
					"content": body.Content, "proxied": body.Proxied}})
			return
		}
		result := []map[string]any{}
		if dnsExists {
			result = []map[string]any{{"id": "rec1", "name": "files.example.com", "type": "CNAME",
				"content": "tunnel-id.cfargotunnel.com", "proxied": true}}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "errors": []any{}, "result": result})
	})
	mux.HandleFunc("/accounts/acct1/cfd_tunnel/tunnel-id", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			fs.markTunnelDeleted()
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "errors": []any{}, "result": map[string]any{}})
	})
	mux.HandleFunc("/zones/zone1/dns_records/rec1", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			fs.markDNSDeleted()
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "errors": []any{}, "result": map[string]any{}})
	})

	fs.Server = httptest.NewServer(mux)
	return fs
}

func TestSetupNamedTunnelCreatesEverything(t *testing.T) {
	srv := newFakeCloudflareServer(t, false, false)
	defer srv.Close()
	useFakeAPI(t, srv.URL)

	info, handle, err := SetupNamedTunnel(context.Background(), "tok", "", "files.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if info.Hostname != "files.example.com" || info.ID != "tunnel-id" {
		t.Errorf("got %+v", info)
	}
	if handle.dnsRecordID == "" {
		t.Error("newly created dns record id should be recorded for Teardown")
	}
}

func TestSetupNamedTunnelReusesEverything(t *testing.T) {
	srv := newFakeCloudflareServer(t, true, true)
	defer srv.Close()
	useFakeAPI(t, srv.URL)

	info, handle, err := SetupNamedTunnel(context.Background(), "tok", "", "files.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if info.ID != "tunnel-id" {
		t.Errorf("got %+v", info)
	}
	if handle.dnsRecordID != "" {
		t.Error("reused dns record should not be marked for Teardown deletion")
	}
}

func TestSetupNamedTunnelConflictingDNS(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/accounts", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "errors": []any{},
			"result": []map[string]string{{"id": "acct1", "name": "Test"}}})
	})
	mux.HandleFunc("/zones", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "errors": []any{},
			"result": []map[string]string{{"id": "zone1", "name": "example.com"}}})
	})
	mux.HandleFunc("/accounts/acct1/cfd_tunnel", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "errors": []any{},
			"result": []map[string]string{{"id": "tunnel-id", "name": "trynet-files-example-com"}}})
	})
	mux.HandleFunc("/accounts/acct1/cfd_tunnel/tunnel-id/token", func(w http.ResponseWriter, r *http.Request) {
		rawToken := `eyJhIjoiYWNjb3VudC10YWciLCJ0IjoidHVubmVsLWlkIiwicyI6ImMyVmpjbVYwZG5jPSJ9`
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "errors": []any{}, "result": rawToken})
	})
	mux.HandleFunc("/zones/zone1/dns_records", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "errors": []any{},
			"result": []map[string]any{{"id": "rec1", "name": "files.example.com", "type": "CNAME",
				"content": "somewhere-else.example.net", "proxied": false}}})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	useFakeAPI(t, srv.URL)

	_, _, err := SetupNamedTunnel(context.Background(), "tok", "", "files.example.com")
	if err == nil {
		t.Fatal("expected an error, dns record points elsewhere and should not be overwritten")
	}
	if !strings.Contains(err.Error(), "somewhere-else.example.net") {
		t.Errorf("error should mention what it currently points to, got: %v", err)
	}
}

// TestSetupNamedTunnelConflictingRecordType 覆盖域名上已经有一条 A 记录的场景：
// cfFindDNSRecord 现在不限类型查询，能发现它，SetupNamedTunnel 应该给出一句
// 用户看得懂的提示（带上记录类型和当前内容），而不是让请求捅到 Cloudflare 那边
// 换回一个生硬的错误码。
func TestSetupNamedTunnelConflictingRecordType(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/accounts", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "errors": []any{},
			"result": []map[string]string{{"id": "acct1", "name": "Test"}}})
	})
	mux.HandleFunc("/zones", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "errors": []any{},
			"result": []map[string]string{{"id": "zone1", "name": "example.com"}}})
	})
	mux.HandleFunc("/accounts/acct1/cfd_tunnel", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "errors": []any{},
			"result": []map[string]string{{"id": "tunnel-id", "name": "trynet-files-example-com"}}})
	})
	mux.HandleFunc("/accounts/acct1/cfd_tunnel/tunnel-id/token", func(w http.ResponseWriter, r *http.Request) {
		rawToken := `eyJhIjoiYWNjb3VudC10YWciLCJ0IjoidHVubmVsLWlkIiwicyI6ImMyVmpjbVYwZG5jPSJ9`
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "errors": []any{}, "result": rawToken})
	})
	mux.HandleFunc("/zones/zone1/dns_records", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "errors": []any{},
			"result": []map[string]any{{"id": "rec1", "name": "files.example.com", "type": "A",
				"content": "203.0.113.10", "proxied": false}}})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	useFakeAPI(t, srv.URL)

	_, _, err := SetupNamedTunnel(context.Background(), "tok", "", "files.example.com")
	if err == nil {
		t.Fatal("expected an error, an existing A record should not be silently overwritten")
	}
	if !strings.Contains(err.Error(), "A record") || !strings.Contains(err.Error(), "203.0.113.10") {
		t.Errorf("error should mention both the record type and its current content, got: %v", err)
	}
}

// TestTeardownDeletesCreatedResources 覆盖"新建"场景：tunnel 和 dns 记录都是
// 这次自己建的，Teardown 应该把两者都删掉。
func TestTeardownDeletesCreatedResources(t *testing.T) {
	srv := newFakeCloudflareServer(t, false, false)
	defer srv.Close()
	useFakeAPI(t, srv.URL)

	_, handle, err := SetupNamedTunnel(context.Background(), "tok", "", "files.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if !handle.createdTunnel {
		t.Fatal("expected createdTunnel to be true after actually creating a new tunnel")
	}

	if err := handle.Teardown(); err != nil {
		t.Fatalf("Teardown: %v", err)
	}
	if !srv.wasTunnelDeleted() {
		t.Error("Teardown should have deleted the tunnel it created")
	}
	if !srv.wasDNSDeleted() {
		t.Error("Teardown should have deleted the dns record it created")
	}
}

// TestTeardownSkipsReusedResources 锁住 Critical 修复：tunnel 和 dns 记录都是
// 复用到的存量资源，Teardown 一个都不能删，否则会留下悬空的 DNS CNAME，
// 把下一次运行卡死在"dns record points elsewhere"那个硬失败上。
func TestTeardownSkipsReusedResources(t *testing.T) {
	srv := newFakeCloudflareServer(t, true, true)
	defer srv.Close()
	useFakeAPI(t, srv.URL)

	_, handle, err := SetupNamedTunnel(context.Background(), "tok", "", "files.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if handle.createdTunnel {
		t.Fatal("expected createdTunnel to be false when the tunnel was found and reused")
	}
	if handle.dnsRecordID != "" {
		t.Fatal("expected dnsRecordID to be empty when the dns record was found and reused")
	}

	if err := handle.Teardown(); err != nil {
		t.Fatalf("Teardown: %v", err)
	}
	if srv.wasTunnelDeleted() {
		t.Error("Teardown must not delete a reused tunnel: it may belong to a previous run or another use, " +
			"and deleting it leaves the dns record dangling")
	}
	if srv.wasDNSDeleted() {
		t.Error("Teardown must not delete a reused dns record")
	}
}

// TestRefreshTokenFillsHostname 确认 RefreshToken 重新取到的 token 上，
// Hostname 被正确填回了域名，不是留着空串——serve() 拿到的 info 要靠它打招呼。
func TestRefreshTokenFillsHostname(t *testing.T) {
	srv := newFakeCloudflareServer(t, true, true)
	defer srv.Close()
	useFakeAPI(t, srv.URL)

	_, handle, err := SetupNamedTunnel(context.Background(), "tok", "", "files.example.com")
	if err != nil {
		t.Fatal(err)
	}

	info, err := handle.RefreshToken(context.Background())
	if err != nil {
		t.Fatalf("RefreshToken: %v", err)
	}
	if info.Hostname != "files.example.com" {
		t.Errorf("expected hostname to be filled in, got %q", info.Hostname)
	}
	if info.ID != "tunnel-id" {
		t.Errorf("got %+v", info)
	}
}
