// cloudflare_resources_test.go 测的是具体资源操作：account/zone 解析、
// tunnel 查找创建取 token、DNS 记录增删查。传输层与通用工具（cfDo/cfList/cfPath
// 等）的测试留在 cloudflare_api_test.go。
package cfapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCfResolveAccountSingleAccount(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true, "errors": []any{},
			"result": []map[string]string{{"id": "acct1", "name": "My Account"}},
		})
	}))
	defer srv.Close()
	useFakeAPI(t, srv.URL)

	got, err := cfResolveAccount(context.Background(), "tok", "")
	if err != nil {
		t.Fatal(err)
	}
	if got != "acct1" {
		t.Errorf("got %q, want acct1", got)
	}
}

func TestCfResolveAccountMultipleWithoutHint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true, "errors": []any{},
			"result": []map[string]string{
				{"id": "acct1", "name": "First"},
				{"id": "acct2", "name": "Second"},
			},
		})
	}))
	defer srv.Close()
	useFakeAPI(t, srv.URL)

	_, err := cfResolveAccount(context.Background(), "tok", "")
	if err == nil {
		t.Fatal("expected an error asking the user to pick an account")
	}
	if !strings.Contains(err.Error(), "acct1") || !strings.Contains(err.Error(), "acct2") {
		t.Errorf("error should list both accounts, got: %v", err)
	}
}

func TestCfResolveAccountWithHint(t *testing.T) {
	// 给了 hint 就直接用，不应该发任何请求
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("should not call the API when a hint is given")
	}))
	defer srv.Close()
	useFakeAPI(t, srv.URL)

	got, err := cfResolveAccount(context.Background(), "tok", "hinted-acct")
	if err != nil {
		t.Fatal(err)
	}
	if got != "hinted-acct" {
		t.Errorf("got %q, want hinted-acct", got)
	}
}

func TestCfResolveZoneLongestSuffixMatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true, "errors": []any{},
			"result": []map[string]string{
				{"id": "zone1", "name": "example.com"},
				{"id": "zone2", "name": "sub.example.com"},
			},
		})
	}))
	defer srv.Close()
	useFakeAPI(t, srv.URL)

	got, err := cfResolveZone(context.Background(), "tok", "files.sub.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "zone2" {
		t.Errorf("should match the longer zone sub.example.com, got %+v", got)
	}
}

func TestCfResolveZoneNoMatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true, "errors": []any{},
			"result": []map[string]string{{"id": "zone1", "name": "unrelated.com"}},
		})
	}))
	defer srv.Close()
	useFakeAPI(t, srv.URL)

	_, err := cfResolveZone(context.Background(), "tok", "files.example.com")
	if err == nil {
		t.Fatal("expected an error, no zone covers this domain")
	}
}

func TestCfResolveZoneExactMatch(t *testing.T) {
	// domain 与 zone 完全相等的情况（用户想把根域名直接做隧道）
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true, "errors": []any{},
			"result": []map[string]string{
				{"id": "zone-exact-match", "name": "example.com"},
			},
		})
	}))
	defer srv.Close()
	useFakeAPI(t, srv.URL)

	got, err := cfResolveZone(context.Background(), "tok", "example.com")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "zone-exact-match" {
		t.Errorf("exact match failed, got %+v, want zone-exact-match", got)
	}
}

func TestCfResolveZoneRejectsLookalike(t *testing.T) {
	// notexample.com 看起来像但其实不是 example.com 的子域，不能误匹配
	// 这是最关键的防线，防止把 DNS 记录建到别人的域名
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true, "errors": []any{},
			"result": []map[string]string{
				{"id": "zone-lookalike", "name": "example.com"},
			},
		})
	}))
	defer srv.Close()
	useFakeAPI(t, srv.URL)

	_, err := cfResolveZone(context.Background(), "tok", "notexample.com")
	if err == nil {
		t.Fatal("expected an error; notexample.com should not match example.com")
	}
}

func TestCfFindTunnelFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("name"); got != "trynet-files-example-com" {
			t.Errorf("wrong name filter: %q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true, "errors": []any{},
			"result": []map[string]string{{"id": "tun1", "name": "trynet-files-example-com"}},
		})
	}))
	defer srv.Close()
	useFakeAPI(t, srv.URL)

	got, err := cfFindTunnel(context.Background(), "tok", "acct1", "trynet-files-example-com")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.ID != "tun1" {
		t.Errorf("got %+v", got)
	}
}

func TestCfFindTunnelNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true, "errors": []any{}, "result": []map[string]string{},
		})
	}))
	defer srv.Close()
	useFakeAPI(t, srv.URL)

	got, err := cfFindTunnel(context.Background(), "tok", "acct1", "trynet-nope")
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("expected nil for not-found, got %+v", got)
	}
}

func TestCfGetTunnelToken(t *testing.T) {
	// 真实 token 是 base64({"a":accountTag,"t":tunnelID,"s":base64(secret)})
	rawToken := `eyJhIjoiYWNjb3VudC10YWciLCJ0IjoidHVubmVsLWlkIiwicyI6ImMyVmpjbVYwZG5jPSJ9`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true, "errors": []any{}, "result": rawToken,
		})
	}))
	defer srv.Close()
	useFakeAPI(t, srv.URL)

	info, err := cfGetTunnelToken(context.Background(), "tok", "acct1", "tun1")
	if err != nil {
		t.Fatal(err)
	}
	if info.AccountTag != "account-tag" || info.ID != "tunnel-id" {
		t.Errorf("got %+v", info)
	}
	if string(info.Secret) != "secretvw" {
		t.Errorf("secret decoded wrong: %q", info.Secret)
	}
}

func TestCfFindDNSRecord(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("type"); got != "" {
			t.Errorf("should query by name only, not restrict by type: %q", got)
		}
		if got := r.URL.Query().Get("name"); got != "files.example.com" {
			t.Errorf("wrong name filter: %q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true, "errors": []any{},
			"result": []map[string]any{
				{"id": "rec1", "name": "files.example.com", "type": "CNAME",
					"content": "tun1.cfargotunnel.com", "proxied": true},
			},
		})
	}))
	defer srv.Close()
	useFakeAPI(t, srv.URL)

	got, err := cfFindDNSRecord(context.Background(), "tok", "zone1", "files.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.Content != "tun1.cfargotunnel.com" {
		t.Errorf("got %+v", got)
	}
}

func TestCfFindDNSRecordNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true, "errors": []any{}, "result": []map[string]any{},
		})
	}))
	defer srv.Close()
	useFakeAPI(t, srv.URL)

	got, err := cfFindDNSRecord(context.Background(), "tok", "zone1", "nope.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("expected nil for not-found, got %+v", got)
	}
}

func TestCfCreateDNSRecordIsProxied(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body cfCreateDNSBody
		_ = json.NewDecoder(r.Body).Decode(&body)
		if !body.Proxied {
			t.Error("dns record must be proxied (orange cloud), otherwise the tunnel doesn't work")
		}
		if body.Type != "CNAME" {
			t.Errorf("wrong type: %q", body.Type)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true, "errors": []any{},
			"result": map[string]any{"id": "rec1", "name": body.Name, "type": body.Type,
				"content": body.Content, "proxied": body.Proxied},
		})
	}))
	defer srv.Close()
	useFakeAPI(t, srv.URL)

	got, err := cfCreateDNSRecord(context.Background(), "tok", "zone1", "files.example.com", "tun1.cfargotunnel.com")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "rec1" {
		t.Errorf("got %+v", got)
	}
}

func TestCfDeleteDNSRecord(t *testing.T) {
	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "errors": []any{}, "result": map[string]any{}})
	}))
	defer srv.Close()
	useFakeAPI(t, srv.URL)

	if err := cfDeleteDNSRecord(context.Background(), "tok", "zone1", "rec1"); err != nil {
		t.Fatal(err)
	}
	if gotMethod != http.MethodDelete || gotPath != "/zones/zone1/dns_records/rec1" {
		t.Errorf("got %s %s", gotMethod, gotPath)
	}
}

func TestCfDeleteTunnel(t *testing.T) {
	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "errors": []any{}, "result": map[string]any{}})
	}))
	defer srv.Close()
	useFakeAPI(t, srv.URL)

	if err := cfDeleteTunnel(context.Background(), "tok", "acct1", "tun1"); err != nil {
		t.Fatal(err)
	}
	if gotMethod != http.MethodDelete || gotPath != "/accounts/acct1/cfd_tunnel/tun1" {
		t.Errorf("got %s %s", gotMethod, gotPath)
	}
}

func TestCfDeleteDNSRecordAlreadyGoneIsIdempotent(t *testing.T) {
	// Cloudflare 删除一条已经不存在的 DNS 记录会返回 81044，不是静默成功；
	// -Teardown 的语义是"确保这东西没了"，本来就没有等于目标已经达成，不该报错。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": false,
			"errors": []map[string]any{
				{"code": 81044, "message": "Record does not exist."},
			},
			"result": nil,
		})
	}))
	defer srv.Close()
	useFakeAPI(t, srv.URL)

	if err := cfDeleteDNSRecord(context.Background(), "tok", "zone1", "rec1"); err != nil {
		t.Errorf("expected nil error for an already-gone record, got: %v", err)
	}
}

func TestCfDeleteDNSRecordHTTP404IsIdempotent(t *testing.T) {
	// 不是所有资源的"不存在"都用同一个错误码，所以 HTTP 404 也要认
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": false,
			"errors":  []any{},
			"result":  nil,
		})
	}))
	defer srv.Close()
	useFakeAPI(t, srv.URL)

	if err := cfDeleteDNSRecord(context.Background(), "tok", "zone1", "rec1"); err != nil {
		t.Errorf("expected nil error for HTTP 404, got: %v", err)
	}
}

func TestCfDeleteDNSRecordOtherErrorIsNotSwallowed(t *testing.T) {
	// 关键回归点：只吞"不存在"，不能把所有错误都吞掉
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": false,
			"errors": []map[string]any{
				{"code": 10000, "message": "Authentication error"},
			},
			"result": nil,
		})
	}))
	defer srv.Close()
	useFakeAPI(t, srv.URL)

	err := cfDeleteDNSRecord(context.Background(), "tok", "zone1", "rec1")
	if err == nil {
		t.Fatal("expected an error for a non-not-found failure, got nil")
	}
	if !strings.Contains(err.Error(), "Authentication error") {
		t.Errorf("error should surface the Cloudflare message, got: %v", err)
	}
}
