// session_test.go 覆盖会话 cookie 鉴权：登录发 cookie、cookie 换来 /data
// 访问权、登出真删会话、伪造/过期 token 被拒、登录失败的暴力破解防护，
// 以及 Basic Auth 彻底失效之后 /data 只认这一条路。
package fileshare

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// doLogin 往 /auth/login 发一次登录请求，返回响应本身（调用方自己关 Body）。
func doLogin(t *testing.T, ts *httptest.Server, user, pass string) *http.Response {
	t.Helper()
	body, err := json.Marshal(loginRequest{Username: user, Password: pass})
	if err != nil {
		t.Fatalf("marshal login request: %v", err)
	}
	resp, err := http.Post(ts.URL+"/auth/login", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("post /auth/login: %v", err)
	}
	return resp
}

// sessionCookieFrom 从响应里按名字找会话 cookie，找不到就让测试失败。
func sessionCookieFrom(t *testing.T, resp *http.Response) *http.Cookie {
	t.Helper()
	for _, c := range resp.Cookies() {
		if c.Name == sessionCookieName {
			return c
		}
	}
	t.Fatalf("response did not set a %q cookie", sessionCookieName)
	return nil
}

// ---------------------------------------------------------------------
// 登录：成功发 cookie，cookie 带齐四个安全标志
// ---------------------------------------------------------------------

func TestLoginWithCorrectCredentialsSetsSessionCookie(t *testing.T) {
	dir := t.TempDir()
	ts := httptest.NewServer(buildFileServerHandler(Config{Dir: dir, User: "admin", Pass: "s3cret1"}, newSessionStore(), newShareStore()))
	defer ts.Close()

	resp := doLogin(t, ts, "admin", "s3cret1")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login: got status %d, want 200", resp.StatusCode)
	}

	// 直接查 Set-Cookie 头本身的属性，不依赖客户端的 cookie jar——jar 只关心
	// 名字和值，HttpOnly/Secure/SameSite 这些标志早在解析阶段就被吞掉了。
	raw := resp.Header.Get("Set-Cookie")
	if raw == "" {
		t.Fatal("login response did not carry a Set-Cookie header")
	}
	for _, want := range []string{"HttpOnly", "Secure", "SameSite=Strict", "Path=/"} {
		if !strings.Contains(raw, want) {
			t.Errorf("Set-Cookie missing %q: %s", want, raw)
		}
	}
	if !strings.HasPrefix(raw, sessionCookieName+"=") {
		t.Errorf("Set-Cookie should start with %q, got: %s", sessionCookieName+"=", raw)
	}
}

// 登录失败：不发 Set-Cookie，401，body 是统一的错误文案
func TestLoginWithWrongCredentialsDoesNotSetCookie(t *testing.T) {
	dir := t.TempDir()
	ts := httptest.NewServer(buildFileServerHandler(Config{Dir: dir, User: "admin", Pass: "s3cret1"}, newSessionStore(), newShareStore()))
	defer ts.Close()

	resp := doLogin(t, ts, "admin", "wrong")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("got status %d, want 401", resp.StatusCode)
	}
	if resp.Header.Get("Set-Cookie") != "" {
		t.Error("a failed login must not set a session cookie")
	}
}

// "用户名错" 和 "密码错" 必须返回完全相同的状态码和响应体，不能让人靠这个
// 区分出"用户名对不对"。
func TestLoginWrongUserAndWrongPassLookIdentical(t *testing.T) {
	dir := t.TempDir()
	ts := httptest.NewServer(buildFileServerHandler(Config{Dir: dir, User: "admin", Pass: "s3cret1"}, newSessionStore(), newShareStore()))
	defer ts.Close()

	readBody := func(resp *http.Response) string {
		defer resp.Body.Close()
		buf, _ := io.ReadAll(resp.Body)
		return string(buf)
	}

	wrongUserResp := doLogin(t, ts, "nobody", "s3cret1")
	wrongUserCode, wrongUserBody := wrongUserResp.StatusCode, readBody(wrongUserResp)

	wrongPassResp := doLogin(t, ts, "admin", "wrong")
	wrongPassCode, wrongPassBody := wrongPassResp.StatusCode, readBody(wrongPassResp)

	if wrongUserCode != wrongPassCode || wrongUserBody != wrongPassBody {
		t.Errorf("wrong-user and wrong-pass responses differ: (%d,%q) vs (%d,%q)",
			wrongUserCode, wrongUserBody, wrongPassCode, wrongPassBody)
	}
	if wrongUserCode != http.StatusUnauthorized {
		t.Errorf("got status %d, want 401", wrongUserCode)
	}
	if !strings.Contains(wrongUserBody, "Incorrect user name or password") {
		t.Errorf("expected the generic error text, got: %q", wrongUserBody)
	}
}

// 登录失败要有固定延迟，减缓在线暴力破解；宽松地断言 >= 250ms（留一点
// 调度抖动的余量），不直接比 300ms 整，避免测试在慢的 CI 机器上偶发抖动。
func TestLoginFailureHasFixedDelay(t *testing.T) {
	dir := t.TempDir()
	ts := httptest.NewServer(buildFileServerHandler(Config{Dir: dir, User: "admin", Pass: "s3cret1"}, newSessionStore(), newShareStore()))
	defer ts.Close()

	start := time.Now()
	resp := doLogin(t, ts, "admin", "wrong")
	resp.Body.Close()
	elapsed := time.Since(start)

	if elapsed < 250*time.Millisecond {
		t.Errorf("login failure returned in %s, want at least ~%s (fixed delay)", elapsed, loginFailDelay)
	}
}

// ---------------------------------------------------------------------
// 会话 cookie 换 /data 访问权
// ---------------------------------------------------------------------

func TestSessionCookieGrantsDataAccess(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir+"/hello.txt", "hi")
	ts := httptest.NewServer(buildFileServerHandler(Config{Dir: dir, User: "admin", Pass: "s3cret1"}, newSessionStore(), newShareStore()))
	defer ts.Close()

	loginResp := doLogin(t, ts, "admin", "s3cret1")
	cookie := sessionCookieFrom(t, loginResp)
	loginResp.Body.Close()

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/data/hello.txt", nil)
	req.AddCookie(cookie)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("got status %d, want 200 with a valid session cookie", resp.StatusCode)
	}
}

// ---------------------------------------------------------------------
// 登出：服务端真删 token，不是只清 cookie
// ---------------------------------------------------------------------

func TestLogoutInvalidatesSession(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir+"/hello.txt", "hi")
	ts := httptest.NewServer(buildFileServerHandler(Config{Dir: dir, User: "admin", Pass: "s3cret1"}, newSessionStore(), newShareStore()))
	defer ts.Close()

	loginResp := doLogin(t, ts, "admin", "s3cret1")
	cookie := sessionCookieFrom(t, loginResp)
	loginResp.Body.Close()

	logoutReq, _ := http.NewRequest(http.MethodPost, ts.URL+"/auth/logout", nil)
	logoutReq.AddCookie(cookie)
	logoutResp, err := http.DefaultClient.Do(logoutReq)
	if err != nil {
		t.Fatalf("post /auth/logout: %v", err)
	}
	logoutResp.Body.Close()
	if logoutResp.StatusCode != http.StatusOK {
		t.Errorf("logout: got status %d, want 200", logoutResp.StatusCode)
	}

	// 拿登出之前那份 cookie 值再去访问 /data——服务端必须已经把它从存储里
	// 删掉了，不能只是让浏览器那份 cookie 过期。
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/data/hello.txt", nil)
	req.AddCookie(cookie)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("got status %d, want 401 for a token revoked by logout", resp.StatusCode)
	}
}

// ---------------------------------------------------------------------
// 伪造 / 过期 token
// ---------------------------------------------------------------------

func TestForgedSessionTokenRejected(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir+"/hello.txt", "hi")
	ts := httptest.NewServer(buildFileServerHandler(Config{Dir: dir, User: "admin", Pass: "s3cret1"}, newSessionStore(), newShareStore()))
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/data/hello.txt", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "this-is-not-a-real-token-AAAAAAAAAAAAAAAAAAAA"})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("got status %d, want 401 for a forged token", resp.StatusCode)
	}
}

// 过期 token 要能测，但不能真等 12 小时——测试直接往 store 的表里塞一个
// 过期时间在过去的 token，等价于"这个 token 本来就该已经过期了"，不用引入
// 时钟注入之类的额外机制。
func TestExpiredSessionTokenRejected(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir+"/hello.txt", "hi")
	store := newSessionStore()
	ts := httptest.NewServer(buildFileServerHandler(Config{Dir: dir, User: "admin", Pass: "s3cret1"}, store, newShareStore()))
	defer ts.Close()

	token, err := store.create()
	if err != nil {
		t.Fatalf("store.create: %v", err)
	}
	store.mu.Lock()
	store.tokens[token] = time.Now().Add(-time.Minute) // 强行改成一分钟前就过期了
	store.mu.Unlock()

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/data/hello.txt", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("got status %d, want 401 for an expired token", resp.StatusCode)
	}
}

// ---------------------------------------------------------------------
// sessionStore 单元测试：滑动过期、清理、随机性
// ---------------------------------------------------------------------

func TestSessionStoreValidateSlidesExpiry(t *testing.T) {
	store := newSessionStore()
	token, err := store.create()
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	store.mu.Lock()
	original := store.tokens[token]
	store.mu.Unlock()

	time.Sleep(5 * time.Millisecond)
	if !store.validate(token) {
		t.Fatal("validate should accept a freshly created token")
	}

	store.mu.Lock()
	renewed := store.tokens[token]
	store.mu.Unlock()

	if !renewed.After(original) {
		t.Errorf("a successful validate should push the expiry forward: original=%v renewed=%v", original, renewed)
	}
}

func TestSessionStoreCleanupExpiredRemovesOnlyExpiredEntries(t *testing.T) {
	store := newSessionStore()
	fresh, err := store.create()
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	stale, err := store.create()
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	store.mu.Lock()
	store.tokens[stale] = time.Now().Add(-time.Hour)
	store.mu.Unlock()

	store.cleanupExpired()

	store.mu.Lock()
	_, freshStillThere := store.tokens[fresh]
	_, staleStillThere := store.tokens[stale]
	store.mu.Unlock()

	if !freshStillThere {
		t.Error("cleanupExpired should not touch a token that has not expired yet")
	}
	if staleStillThere {
		t.Error("cleanupExpired should remove an expired token")
	}
}

func TestSessionTokenIsRandomAndURLSafe(t *testing.T) {
	store := newSessionStore()
	a, err := store.create()
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	b, err := store.create()
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if a == b {
		t.Fatal("two generated tokens must not collide")
	}
	for _, tok := range []string{a, b} {
		if strings.ContainsAny(tok, "+/=") {
			t.Errorf("token should be base64url without padding, got characters not in that alphabet: %q", tok)
		}
	}
}

// ---------------------------------------------------------------------
// -public：会话/Basic Auth 都不再拦
// ---------------------------------------------------------------------

func TestNoAuthSkipsSessionCheckToo(t *testing.T) {
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
		t.Errorf("-public: got status %d, want 200 without any cookie", resp.StatusCode)
	}
}
