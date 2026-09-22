package fileshare

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// okHandler 只用来确认请求有没有被 blockSensitive 放行到下一层
var okHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
})

// noRedirectClient 不跟随重定向——上传成功后 handleUpload 会发一个 303，
// 默认的 http.DefaultClient 会自动跟过去，测试要看的是 303 本身，不是跟完之后的结果。
var noRedirectClient = &http.Client{
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// ---------------------------------------------------------------------
// 鉴权：覆盖 /ui 和 /data 全部路径
// ---------------------------------------------------------------------

// "/" 只负责跳到 "/ui/"，本身不含数据，鉴权边界调整之后它也是公开的——
// 这条测试改盯 /data，那才是真正要保护的东西。鉴权现在只认会话 cookie，
// 401 响应不带 WWW-Authenticate——带了浏览器会弹原生 Basic Auth 对话框，
// 那正是登录弹窗要取代的东西，失败统一交给前端 JS 自己处理。
func TestBasicAuthRejectsNoCredentials(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir+"/hello.txt", "hi")
	addr, err := Start(Config{Dir: dir, User: "admin", Pass: "s3cret1"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	resp, err := http.Get("http://" + addr + "/data/hello.txt")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("got status %d, want 401", resp.StatusCode)
	}
	if resp.Header.Get("WWW-Authenticate") != "" {
		t.Errorf("401 response should not carry WWW-Authenticate (would trigger the native Basic Auth dialog), got %q", resp.Header.Get("WWW-Authenticate"))
	}
}

// /ui 是登录弹窗所在的页面本身，鉴权边界调整之后它改成公开、不要求任何
// 凭据——页面是不含真实数据的空壳，如果鉴权套在它上面，浏览器连页面都加载
// 不到，也就没法在页面里弹出登录框了。真实数据仍然全部在 /data 一侧受保护，
// 见 TestBasicAuthCoversDataRoute。
func TestUIRouteIsPublicWithoutCredentials(t *testing.T) {
	dir := t.TempDir()
	addr, err := Start(Config{Dir: dir, User: "admin", Pass: "s3cret1"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	resp, err := http.Get("http://" + addr + "/ui/")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("/ui/ without credentials: got status %d, want 200 (this route is public)", resp.StatusCode)
	}
}

// 鉴权要覆盖 /data
func TestBasicAuthCoversDataRoute(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir+"/hello.txt", "hi")
	addr, err := Start(Config{Dir: dir, User: "admin", Pass: "s3cret1"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	resp, err := http.Get("http://" + addr + "/data/hello.txt")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("/data/hello.txt without credentials: got status %d, want 401", resp.StatusCode)
	}
}

// Basic Auth 这条路已经完全关掉，不再是鉴权的备用手段——即使带的是正确的
// 用户名密码，/data 也必须照样回 401。这条测试专门守住"没有留一个后门"，
// 是从最初"Basic Auth 正确 → 200"反过来改的，回归意义比看起来更大。
func TestBasicAuthIsFullyRejected(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir+"/hello.txt", "hi")
	addr, err := Start(Config{Dir: dir, User: "admin", Pass: "s3cret1"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	cases := []struct {
		name, user, pass string
	}{
		{"wrong user", "nobody", "s3cret1"},
		{"wrong pass", "admin", "wrong"},
		{"correct credentials", "admin", "s3cret1"},
	}
	for _, c := range cases {
		req, _ := http.NewRequest(http.MethodGet, "http://"+addr+"/data/hello.txt", nil)
		req.SetBasicAuth(c.user, c.pass)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s: get: %v", c.name, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s: got status %d, want 401 (Basic Auth must not grant access anymore)", c.name, resp.StatusCode)
		}
	}
}

// -public 时无凭据也能访问，回到纯公开只读（或公开上传）
func TestNoAuthAllowsAnonymousAccess(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir+"/hello.txt", "hi")
	addr, err := Start(Config{Dir: dir, Public: true})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	resp, err := http.Get("http://" + addr + "/data/hello.txt")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("-public: got status %d, want 200 without credentials", resp.StatusCode)
	}
}

// "用户名错但密码对" 和 "用户名对但密码错" 必须返回一样的结果，这条不变量
// 现在守在 /auth/login 上（唯一还会真正校验用户名密码的入口），见
// session_test.go 的 TestLoginWrongUserAndWrongPassLookIdentical。

// 静态检查：凭据比较必须走 subtle.ConstantTimeCompare，不能退化成 ==。
// 这份逻辑现在收拢进 session.go 的 credentialsMatch，Basic Auth 和会话
// 登录表单共用同一份，所以检查目标从 fileserver.go 换成了 session.go。
func TestBasicAuthSourceUsesConstantTimeCompare(t *testing.T) {
	src, err := os.ReadFile("session.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), "subtle.ConstantTimeCompare") {
		t.Error("credentialsMatch must compare credentials with subtle.ConstantTimeCompare, not ==")
	}
}

// ---------------------------------------------------------------------
// 密码生成
// ---------------------------------------------------------------------

// 密码是纯数字，方便在手机上用数字键盘敲、也方便念给别人听。
// 每一位都必须是数字——混进一个字母，整个"数字键盘就够了"的前提就没了。
func TestGeneratePasswordIsAllDigits(t *testing.T) {
	pass, err := GeneratePassword(PasswordDigits)
	if err != nil {
		t.Fatalf("GeneratePassword: %v", err)
	}
	if len(pass) != PasswordDigits {
		t.Errorf("got length %d, want %d: %q", len(pass), PasswordDigits, pass)
	}
	for _, c := range pass {
		if c < '0' || c > '9' {
			t.Errorf("password contains a non-digit %q: %q", c, pass)
		}
	}
}

// 纯数字每位只有 3.3 bit，位数就是全部的强度来源。8 位是刻意选的（好念、
// 跟验证码一个长度），这条守住的是"别再往下掉"——降到 6 位就只剩 20 bit，
// 几分钟就能爆完。
func TestPasswordHasAFloorOnLength(t *testing.T) {
	if PasswordDigits < 8 {
		t.Errorf("PasswordDigits = %d; an all-digit password below 8 characters "+
			"is brute-forceable in minutes", PasswordDigits)
	}
}

func TestGeneratePasswordIsNotDeterministic(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 20; i++ {
		pass, err := GeneratePassword(PasswordDigits)
		if err != nil {
			t.Fatalf("GeneratePassword: %v", err)
		}
		seen[pass] = true
	}
	if len(seen) < 15 {
		t.Errorf("20 generated passwords only produced %d distinct values, looks non-random", len(seen))
	}
}

func TestBasicAuthSourceUsesCryptoRandForPassword(t *testing.T) {
	src, err := os.ReadFile("fileserver.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), `"crypto/rand"`) {
		t.Error("password generation must import crypto/rand")
	}
	if strings.Contains(string(src), `"math/rand"`) {
		t.Error("password generation must not use math/rand, it is predictable")
	}
}

// ---------------------------------------------------------------------
// JSON 转义 / renderDirJSON 渲染细节
// ---------------------------------------------------------------------

// 文件名直接拿危险字符串构造 dirListing 编码，不落盘——"<script>" 这种字符串
// 在 Windows 上根本没法创建成真实文件名，这样测才能跨平台跑到。
// encoding/json 默认会把 < > & 转成 \u003c 之类的转义序列，文件名混进 JSON
// 响应体里不会被当成 HTML 标签解析。
func TestDirListingJSONEscapesFileNameForSafeEmbedding(t *testing.T) {
	listing := dirListing{
		Path:    "/",
		Entries: []dirEntry{{Name: `<script>alert(1)</script>`, IsDir: false, Size: 3}},
	}
	data, err := json.Marshal(listing)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	body := string(data)
	if strings.Contains(body, "<script>") {
		t.Errorf("file name must be escaped, got raw tag: %q", body)
	}
	if !strings.Contains(body, `\u003cscript\u003e`) {
		t.Errorf("expected escaped script tag in JSON output: %q", body)
	}
}

func TestRenderDirJSONEscapesPathForSafeEmbedding(t *testing.T) {
	dir := t.TempDir()
	fs := &fileService{dir: dir}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.URL.Path = "/<script>alert(1)</script>/"
	rec := httptest.NewRecorder()
	fs.renderDirJSON(rec, req, dir)

	body := rec.Body.String()
	if strings.Contains(body, "<script>alert(1)</script>") {
		t.Errorf("path must be escaped, got raw tag: %q", body)
	}
	if !strings.Contains(body, `\u003cscript\u003e`) {
		t.Errorf("expected escaped path in JSON output: %q", body)
	}
}

func TestRenderDirJSONReportsUploadEnabled(t *testing.T) {
	dir := t.TempDir()
	fs := &fileService{dir: dir, upload: true}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	fs.renderDirJSON(rec, req, dir)

	if !strings.Contains(rec.Body.String(), `"uploadEnabled":true`) {
		t.Errorf("listing should report uploadEnabled=true, got: %s", rec.Body.String())
	}
}

func TestRenderDirJSONReportsUploadDisabled(t *testing.T) {
	dir := t.TempDir()
	fs := &fileService{dir: dir, upload: false}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	fs.renderDirJSON(rec, req, dir)

	if !strings.Contains(rec.Body.String(), `"uploadEnabled":false`) {
		t.Errorf("listing should report uploadEnabled=false, got: %s", rec.Body.String())
	}
}
