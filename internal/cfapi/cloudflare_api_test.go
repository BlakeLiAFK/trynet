package cfapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// useFakeAPI 把 API 地址临时指向假服务器，测试结束自动还原成原值。
// 比每个测试手写 defer 恢复成硬编码字面量安全：默认值改了不用动这些测试，
// 也不会因为漏写 defer 污染到后面的测试。
//
// 注意：cfAPIBase 是包级变量，所以这些测试不能加 t.Parallel()，会互相串。
func useFakeAPI(t *testing.T, url string) {
	t.Helper()
	old := cfAPIBase
	cfAPIBase = url
	t.Cleanup(func() { cfAPIBase = old })
}

func TestSanitizeTunnelName(t *testing.T) {
	// 长域名用例特意让第 63 字节落在连字符上（前缀 7 字节 + 55 个 a + 一个折叠出来的
	// 连字符，第 63 个字符正好是那个连字符），确保截断后去掉末尾连字符的分支真的被走到。
	longDomain := strings.Repeat("a", 55) + "." + strings.Repeat("b", 10)
	cases := map[string]string{
		"files.example.com": "trynet-files-example-com",
		"Files.EXAMPLE.com": "trynet-files-example-com",
		"a..b---c.com":      "trynet-a-b-c-com",
		// 域名以非法字符开头，前缀本身已经以连字符结尾，不应该产生双连字符
		".foo.com": "trynet-foo-com",
		longDomain: "trynet-" + strings.Repeat("a", 55),
	}
	for in, want := range cases {
		got := sanitizeTunnelName(in)
		if want != "" && got != want {
			t.Errorf("sanitizeTunnelName(%q) = %q, want %q", in, got, want)
		}
		if len(got) > 63 {
			t.Errorf("sanitizeTunnelName(%q) too long: %d chars", in, len(got))
		}
		if got[0] == '-' || got[len(got)-1] == '-' {
			t.Errorf("sanitizeTunnelName(%q) = %q has leading/trailing dash", in, got)
		}
		for _, r := range got {
			if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
				t.Errorf("sanitizeTunnelName(%q) = %q has illegal char %q", in, got, r)
			}
		}
	}
}

func TestCfDoSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("missing/wrong Authorization header: %q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"errors":  []any{},
			"result":  map[string]string{"id": "abc123"},
		})
	}))
	defer srv.Close()

	useFakeAPI(t, srv.URL)

	type result struct {
		ID string `json:"id"`
	}
	got, err := cfDo[result](context.Background(), http.MethodGet, "/whatever", "test-token", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "abc123" {
		t.Errorf("got %+v", got)
	}
}

func TestCfDoAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": false,
			"errors": []map[string]any{
				{"code": 9109, "message": "Invalid access token"},
			},
			"result": nil,
		})
	}))
	defer srv.Close()

	useFakeAPI(t, srv.URL)

	_, err := cfDo[map[string]any](context.Background(), http.MethodGet, "/whatever", "bad-token", nil)
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "Invalid access token") {
		t.Errorf("error should surface the Cloudflare message, got: %v", err)
	}
}

func TestCfDoTransportFailure(t *testing.T) {
	// 起一个 server 然后立刻关掉，端口上没有任何东西在监听，
	// 这样 tunnel.APIClient.Do 一定会失败，用来验证传输层错误也被正确包装并返回。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srv.Close()

	useFakeAPI(t, srv.URL)

	_, err := cfDo[map[string]any](context.Background(), http.MethodGet, "/whatever", "test-token", nil)
	if err == nil {
		t.Fatal("expected an error when the server is unreachable")
	}
}

func TestCfDoDecodeFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("<html>oops</html>"))
	}))
	defer srv.Close()

	useFakeAPI(t, srv.URL)

	_, err := cfDo[map[string]any](context.Background(), http.MethodGet, "/whatever", "test-token", nil)
	if err == nil {
		t.Fatal("expected an error when the response body is not JSON")
	}
}

func TestCfListPagination(t *testing.T) {
	// 假服务器按 page 参数分两页返回，第一页 total_pages=2，
	// 用来证明 cfList 真的把两页都翻完了，不是只拿第一页就收手。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pageParam := r.URL.Query().Get("page")
		var result []string
		var page int
		switch pageParam {
		case "1":
			result = []string{"a"}
			page = 1
		case "2":
			result = []string{"b"}
			page = 2
		default:
			t.Errorf("unexpected page param: %q", pageParam)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"errors":  []any{},
			"result":  result,
			"result_info": map[string]any{
				"page":        page,
				"per_page":    50,
				"count":       len(result),
				"total_count": 2,
				"total_pages": 2,
			},
		})
	}))
	defer srv.Close()

	useFakeAPI(t, srv.URL)

	got, err := cfList[string](context.Background(), "/whatever", "test-token", nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"a", "b"}
	if len(got) != len(want) {
		t.Fatalf("cfList returned %d items, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("item %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestCfPath(t *testing.T) {
	if got := cfPath("/zones", nil); got != "/zones" {
		t.Errorf("cfPath with nil query = %q, want %q", got, "/zones")
	}
	if got := cfPath("/zones", url.Values{}); got != "/zones" {
		t.Errorf("cfPath with empty query = %q, want %q", got, "/zones")
	}

	q := url.Values{}
	q.Set("name", "files.example.com")
	q.Set("status", "a&b c")
	got := cfPath("/zones", q)
	want := "/zones?" + q.Encode()
	if got != want {
		t.Errorf("cfPath with query = %q, want %q", got, want)
	}
	if !strings.Contains(got, "name=files.example.com") {
		t.Errorf("cfPath did not preserve name param: %q", got)
	}
	if !strings.Contains(got, "status=a%26b+c") {
		t.Errorf("cfPath did not escape special characters: %q", got)
	}
}
