// share_test.go 覆盖单文件分享链接：token 只对签发它的那一个路径有效、只读、
// 到点就失效，以及签发接口拒绝目录和敏感文件。
//
// 这批测试真正守住的是"授权范围"——一张分享 token 如果能拿去下载别的文件、
// 或者能拿去上传，整个功能就退化成了"把登录密码换了个名字发出去"，正是它要
// 避免的东西。
package fileshare

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"
	"time"
)

// newShareServer 起一个带鉴权的文件服务，返回服务本身和登录后的会话 cookie。
func newShareServer(t *testing.T, dir string) (*httptest.Server, *http.Cookie) {
	t.Helper()
	ts := httptest.NewServer(buildFileServerHandler(
		Config{Dir: dir, User: "admin", Pass: "s3cret1"}, newSessionStore(), newShareStore()))
	t.Cleanup(ts.Close)

	resp := doLogin(t, ts, "admin", "s3cret1")
	defer resp.Body.Close()
	return ts, sessionCookieFrom(t, resp)
}

// issueShare 用会话 cookie 为 dataPath 申请一个分享链接，返回响应状态码和
// 响应体里的 url 字段（失败时 url 为空）。
func issueShare(t *testing.T, ts *httptest.Server, cookie *http.Cookie, dataPath string) (int, string) {
	t.Helper()
	body, err := json.Marshal(shareRequest{Path: dataPath})
	if err != nil {
		t.Fatalf("marshal share request: %v", err)
	}
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/auth/share", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if cookie != nil {
		req.AddCookie(cookie)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post /auth/share: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, ""
	}
	var out shareResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode share response: %v", err)
	}
	return resp.StatusCode, out.URL
}

// getNoCookie 不带任何凭据发一个 GET，模拟"收到链接的人"。
func getNoCookie(t *testing.T, rawurl string) (int, string) {
	t.Helper()
	resp, err := http.Get(rawurl)
	if err != nil {
		t.Fatalf("get %s: %v", rawurl, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(data)
}

// ---------------------------------------------------------------------
// 正路：签发 → 无凭据下载
// ---------------------------------------------------------------------

func TestShareLinkDownloadsWithoutCredentials(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir+"/report.pdf", "pdf bytes")
	ts, cookie := newShareServer(t, dir)

	status, link := issueShare(t, ts, cookie, "/data/report.pdf")
	if status != http.StatusOK {
		t.Fatalf("issue share: got status %d, want 200", status)
	}

	gotStatus, body := getNoCookie(t, ts.URL+link)
	if gotStatus != http.StatusOK {
		t.Fatalf("share link: got status %d, want 200", gotStatus)
	}
	if body != "pdf bytes" {
		t.Errorf("share link served %q, want %q", body, "pdf bytes")
	}
}

// 文件名带空格和中文时链接照样能用——签发时要重新转义回去，否则粘出去的
// 链接一到浏览器就对不上路径
func TestShareLinkHandlesNamesNeedingEscaping(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir+"/my report 报告.txt", "hello")
	ts, cookie := newShareServer(t, dir)

	status, link := issueShare(t, ts, cookie, "/data/"+url.PathEscape("my report 报告.txt"))
	if status != http.StatusOK {
		t.Fatalf("issue share: got status %d, want 200", status)
	}
	gotStatus, body := getNoCookie(t, ts.URL+link)
	if gotStatus != http.StatusOK || body != "hello" {
		t.Errorf("share link: got status %d body %q, want 200 %q", gotStatus, body, "hello")
	}
}

// ---------------------------------------------------------------------
// 授权范围：一张 token 只开一扇门
// ---------------------------------------------------------------------

// 这条是整个功能的核心约束。拿 a.txt 的 token 去下 b.txt 必须失败，否则
// 分享链接就等于把整个目录交出去了。
func TestShareTokenDoesNotUnlockOtherFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir+"/a.txt", "file a")
	writeFile(t, dir+"/b.txt", "file b")
	if err := os.MkdirAll(dir+"/sub", 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir+"/sub/c.txt", "file c")
	ts, cookie := newShareServer(t, dir)

	_, link := issueShare(t, ts, cookie, "/data/a.txt")
	token := tokenFromLink(t, link)

	for _, path := range []string{"/data/b.txt", "/data/sub/c.txt", "/data/", "/data/sub/"} {
		status, _ := getNoCookie(t, ts.URL+path+"?"+shareQueryParam+"="+token)
		if status != http.StatusUnauthorized {
			t.Errorf("%s with a.txt's token: got status %d, want 401", path, status)
		}
	}
}

// 分享链接只能取文件，不能变成写入口：带 token 发上传必须被鉴权层挡下。
func TestShareTokenCannotUpload(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir+"/a.txt", "file a")
	ts := httptest.NewServer(buildFileServerHandler(
		Config{Dir: dir, Upload: true, User: "admin", Pass: "s3cret1"}, newSessionStore(), newShareStore()))
	defer ts.Close()
	resp := doLogin(t, ts, "admin", "s3cret1")
	cookie := sessionCookieFrom(t, resp)
	resp.Body.Close()

	_, link := issueShare(t, ts, cookie, "/data/a.txt")
	token := tokenFromLink(t, link)

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", "evil.txt")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := fw.Write([]byte("planted")); err != nil {
		t.Fatalf("write form file: %v", err)
	}
	mw.Close()

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/data/?"+shareQueryParam+"="+token, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	up, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post upload: %v", err)
	}
	defer up.Body.Close()
	if up.StatusCode != http.StatusUnauthorized {
		t.Errorf("upload with a share token: got status %d, want 401", up.StatusCode)
	}
}

// 分享 token 换不出新的分享 token，否则范围限制就绕开了。
func TestShareTokenCannotMintAnotherToken(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir+"/a.txt", "file a")
	writeFile(t, dir+"/b.txt", "file b")
	ts, cookie := newShareServer(t, dir)

	_, link := issueShare(t, ts, cookie, "/data/a.txt")
	token := tokenFromLink(t, link)

	body, _ := json.Marshal(shareRequest{Path: "/data/b.txt"})
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/auth/share?"+shareQueryParam+"="+token, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post /auth/share: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("minting with a share token: got status %d, want 401", resp.StatusCode)
	}
}

func TestIssueShareRequiresSession(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir+"/a.txt", "file a")
	ts, _ := newShareServer(t, dir)

	if status, _ := issueShare(t, ts, nil, "/data/a.txt"); status != http.StatusUnauthorized {
		t.Errorf("issue share without a session: got status %d, want 401", status)
	}
}

// ---------------------------------------------------------------------
// 签发前的拒绝：目录、敏感文件、目录外路径
// ---------------------------------------------------------------------

func TestIssueShareRejectsUnshareablePaths(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir+"/ok.txt", "fine")
	if err := os.MkdirAll(dir+"/sub", 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir+"/sub/nested.txt", "nested")
	writeFile(t, dir+"/.env", "SECRET=1")
	writeFile(t, dir+"/id_rsa", "key")
	writeFile(t, dir+"/server.pem", "cert")
	ts, cookie := newShareServer(t, dir)

	cases := []struct {
		name, path string
	}{
		{"directory", "/data/sub/"},
		{"directory without slash", "/data/sub"},
		{"root", "/data/"},
		{"dotfile", "/data/.env"},
		{"sensitive name", "/data/id_rsa"},
		{"sensitive extension", "/data/server.pem"},
		{"missing file", "/data/nope.txt"},
		{"escapes the share dir", "/data/../../etc/passwd"},
		// Clean 之后变成 "/ok.txt"，文件真实存在。不拦的话会签出一张绑在
		// "/ok.txt" 上的 token——请求永远以 /data 开头，这张 token 谁也用不了，
		// 用户却以为分享成功了
		{"climbs out of the data prefix onto a real file", "/data/../ok.txt"},
		{"encoded escape", "/data/%2e%2e/%2e%2e/etc/passwd"},
		{"outside the data namespace", "/ui/app.js"},
		{"empty", ""},
	}
	for _, c := range cases {
		status, link := issueShare(t, ts, cookie, c.path)
		if status != http.StatusBadRequest {
			t.Errorf("%s (%q): got status %d, want 400 (link %q)", c.name, c.path, status, link)
		}
	}

	// 对照组：正常文件确实签得出来，免得上面那批因为"全都失败"而假通过
	if status, _ := issueShare(t, ts, cookie, "/data/ok.txt"); status != http.StatusOK {
		t.Errorf("control case /data/ok.txt: got status %d, want 200", status)
	}
}

// ---------------------------------------------------------------------
// 过期：到点失效，而且用了也不续期
// ---------------------------------------------------------------------

func TestExpiredShareTokenRejected(t *testing.T) {
	shares := newShareStore()
	token, err := shares.create("/data/a.txt")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// 直接把过期时间拨到过去，不用真等一小时
	shares.mu.Lock()
	shares.tokens[token] = shareGrant{path: "/data/a.txt", expiry: time.Now().Add(-time.Second)}
	shares.mu.Unlock()

	if shares.validate(token, "/data/a.txt") {
		t.Error("expired share token was accepted")
	}
}

// 分享 token 不滑动续期——这是和会话 cookie 最容易抄错的一处。用一次之后
// 过期时间必须原封不动，否则"只要有人一直下载就永不过期"。
//
// 过期时间刻意拨成"还有 5 分钟"，不用 create 给的那个整点 1 小时：Windows 上
// 连续两次 time.Now() 会返回逐位相同的值（时钟分辨率就那么粗），拿
// now+shareTTL 跟 now+shareTTL 比是比不出差别的，续期了也看不见。错开成一个
// 明显不同的值，一旦有人加了续期，它就会跳到 1 小时后，藏不住。
func TestShareTokenDoesNotSlideExpiry(t *testing.T) {
	shares := newShareStore()
	token, err := shares.create("/data/a.txt")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	before := time.Now().Add(5 * time.Minute)
	shares.mu.Lock()
	shares.tokens[token] = shareGrant{path: "/data/a.txt", expiry: before}
	shares.mu.Unlock()

	if !shares.validate(token, "/data/a.txt") {
		t.Fatal("share token with time left was rejected")
	}

	shares.mu.Lock()
	after := shares.tokens[token].expiry
	shares.mu.Unlock()
	if !after.Equal(before) {
		t.Errorf("expiry moved from %v to %v; share tokens must not slide", before, after)
	}
}

// ---------------------------------------------------------------------
// 中间件本身：方法限制和 shares==nil 这两道，在端到端路径上被"路径对不上"
// 抢先挡掉了，考验不到，只能直接打 requireAuth
// ---------------------------------------------------------------------

func TestRequireAuthShareTokenScope(t *testing.T) {
	const shared = "/data/a.txt"
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })

	shares := newShareStore()
	token, err := shares.create(shared)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	cases := []struct {
		name   string
		shares *shareStore
		method string
		path   string
		want   int
	}{
		{"get on the shared path", shares, http.MethodGet, shared, http.StatusOK},
		{"head on the shared path", shares, http.MethodHead, shared, http.StatusOK},
		// 上传是 POST。token 只能取文件，不能变写入口，这道必须挡在鉴权层，
		// 不能指望下游的上传逻辑替它拦
		{"post on the shared path", shares, http.MethodPost, shared, http.StatusUnauthorized},
		{"put on the shared path", shares, http.MethodPut, shared, http.StatusUnauthorized},
		{"delete on the shared path", shares, http.MethodDelete, shared, http.StatusUnauthorized},
		{"get on another path", shares, http.MethodGet, "/data/b.txt", http.StatusUnauthorized},
		// nil 表示这条路由压根不收分享 token，/auth/share 就是这么挂的——
		// 否则拿一张 token 就能换出新的，范围限制形同虚设
		{"route that takes no share tokens", nil, http.MethodGet, shared, http.StatusUnauthorized},
	}

	for _, c := range cases {
		h := requireAuth(ok, newSessionStore(), c.shares)
		req := httptest.NewRequest(c.method, c.path+"?"+shareQueryParam+"="+token, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != c.want {
			t.Errorf("%s: got status %d, want %d", c.name, rec.Code, c.want)
		}
	}
}

func TestShareStoreRejectsUnknownToken(t *testing.T) {
	shares := newShareStore()
	if shares.validate("", "/data/a.txt") {
		t.Error("empty token was accepted")
	}
	if shares.validate("not-a-real-token", "/data/a.txt") {
		t.Error("forged token was accepted")
	}
}

func TestShareStoreCleanupDropsExpired(t *testing.T) {
	shares := newShareStore()
	live, _ := shares.create("/data/live.txt")
	dead, _ := shares.create("/data/dead.txt")
	shares.mu.Lock()
	shares.tokens[dead] = shareGrant{path: "/data/dead.txt", expiry: time.Now().Add(-time.Second)}
	shares.mu.Unlock()

	shares.cleanupExpired()

	shares.mu.Lock()
	defer shares.mu.Unlock()
	if _, ok := shares.tokens[dead]; ok {
		t.Error("cleanup left an expired token behind")
	}
	if _, ok := shares.tokens[live]; !ok {
		t.Error("cleanup dropped a live token")
	}
}

// 每次签发都是新 token，不会因为同一个文件就复用同一串
func TestShareTokensAreDistinct(t *testing.T) {
	shares := newShareStore()
	seen := make(map[string]bool)
	for i := 0; i < 50; i++ {
		token, err := shares.create("/data/a.txt")
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		if seen[token] {
			t.Fatalf("duplicate share token %q", token)
		}
		seen[token] = true
	}
}

// ---------------------------------------------------------------------
// -public：东西都公开了，签发接口就不该存在
// ---------------------------------------------------------------------

func TestPublicModeHasNoShareEndpoint(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir+"/a.txt", "file a")
	ts := httptest.NewServer(buildFileServerHandler(
		Config{Dir: dir, Public: true}, newSessionStore(), newShareStore()))
	defer ts.Close()

	body, _ := json.Marshal(shareRequest{Path: "/data/a.txt"})
	resp, err := http.Post(ts.URL+"/auth/share", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("post /auth/share: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		t.Error("/auth/share answered in -public mode; it should not be mounted")
	}

	// 对照：-public 下文件本来就无需凭据
	if status, body := getNoCookie(t, ts.URL+"/data/a.txt"); status != http.StatusOK || body != "file a" {
		t.Errorf("-public: got status %d body %q, want 200 %q", status, body, "file a")
	}
}

// tokenFromLink 从签发接口返回的 "/data/x.txt?t=XXX" 里取出 token。
func tokenFromLink(t *testing.T, link string) string {
	t.Helper()
	u, err := url.Parse(link)
	if err != nil {
		t.Fatalf("parse share link %q: %v", link, err)
	}
	token := u.Query().Get(shareQueryParam)
	if token == "" {
		t.Fatalf("share link %q carries no %q param", link, shareQueryParam)
	}
	return token
}
