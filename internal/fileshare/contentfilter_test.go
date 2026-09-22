package fileshare

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------
// blockSensitive：点文件规则
// ---------------------------------------------------------------------

func TestBlockSensitiveBlocksDotSegments(t *testing.T) {
	handler := blockSensitive(okHandler)

	paths := []string{
		"/.trynet.json",
		"/.git/config",
		"/.env",
		"/sub/.hidden/file.txt",
	}
	for _, p := range paths {
		req := httptest.NewRequest(http.MethodGet, p, nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Errorf("path %q: got status %d, want 404", p, rec.Code)
		}
	}
}

func TestBlockSensitiveAllowsNormalPaths(t *testing.T) {
	handler := blockSensitive(okHandler)

	paths := []string{"/", "/index.html", "/sub/dir/file.txt", "/a.b.c", "/report.pdf"}
	for _, p := range paths {
		req := httptest.NewRequest(http.MethodGet, p, nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("path %q: got status %d, want 200", p, rec.Code)
		}
	}
}

// URL 编码绕过：%2e 会被 net/http 解码成 .，blockSensitive 应该照样挡住
func TestBlockSensitiveBlocksPercentEncodedDot(t *testing.T) {
	handler := blockSensitive(okHandler)

	req := httptest.NewRequest(http.MethodGet, "/%2etrynet.json", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("got status %d, want 404 for percent-encoded dot", rec.Code)
	}
}

// ---------------------------------------------------------------------
// blockSensitive：敏感名单（不以点开头的那批漏网之鱼）
// ---------------------------------------------------------------------

// 敏感名单里按名字整体匹配的那批，不分大小写；命中的话不管出现在路径哪一段都要挡
func TestBlockSensitiveBlocksSensitiveNames(t *testing.T) {
	handler := blockSensitive(okHandler)

	paths := []string{
		"/id_rsa",
		"/ID_RSA",
		"/credentials",
		"/secring.gpg",
		"/sub/id_ed25519",
		"/deep/nested/credentials",
	}
	for _, p := range paths {
		req := httptest.NewRequest(http.MethodGet, p, nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Errorf("path %q: got status %d, want 404", p, rec.Code)
		}
	}
}

// 敏感名单里按扩展名匹配的那批：看最后一个后缀，不分大小写
func TestBlockSensitiveBlocksSensitiveExtensions(t *testing.T) {
	handler := blockSensitive(okHandler)

	paths := []string{
		"/x.pem",
		"/X.PEM",
		"/server.key",
		"/bundle.pfx",
		"/site.p12",
		"/store.jks",
		"/vault.keystore",
		"/db.kdbx",
		"/host.ppk",
		"/sub/key.pem", // 敏感名单要挡任何一段，不只是最后一段
	}
	for _, p := range paths {
		req := httptest.NewRequest(http.MethodGet, p, nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Errorf("path %q: got status %d, want 404", p, rec.Code)
		}
	}
}

// 只看最后一个后缀：pem.txt 不该被当成 .pem 挡住
func TestBlockSensitiveOnlyMatchesFinalExtension(t *testing.T) {
	handler := blockSensitive(okHandler)

	req := httptest.NewRequest(http.MethodGet, "/pem.txt", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("pem.txt: got status %d, want 200 (only the final extension should match)", rec.Code)
	}
}

// ---------------------------------------------------------------------
// Start：内容过滤（点文件 + 敏感名单）只套在 /data 上，-bypass 关掉它
// ---------------------------------------------------------------------

// -bypass 打开之后 /data 应该完全放行，不再挡点文件也不再挡敏感名单
func TestStartFileServerBypassAllowsFilteredNames(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir+"/.secret", "top secret")
	writeFile(t, dir+"/id_rsa", "private key")

	addr, err := Start(Config{Dir: dir, Bypass: true, Public: true})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	for _, name := range []string{".secret", "id_rsa"} {
		resp, err := http.Get("http://" + addr + "/data/" + name)
		if err != nil {
			t.Fatalf("get %s: %v", name, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("%s: got status %d, want 200 with -bypass", name, resp.StatusCode)
		}
	}
}

// 默认（不给 -bypass）应该挡住点文件和敏感名单，正常文件照常能访问
func TestStartFileServerDefaultBlocksFilteredNames(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir+"/.secret", "top secret")
	writeFile(t, dir+"/id_rsa", "private key")
	writeFile(t, dir+"/x.pem", "cert")
	if err := os.Mkdir(dir+"/.git", 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir+"/.git/config", "[core]")
	writeFile(t, dir+"/public.txt", "hello")

	addr, err := Start(Config{Dir: dir, Public: true})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	blocked := []string{".secret", "id_rsa", "x.pem", ".git/config"}
	for _, name := range blocked {
		resp, err := http.Get("http://" + addr + "/data/" + name)
		if err != nil {
			t.Fatalf("get %s: %v", name, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s: got status %d, want 404", name, resp.StatusCode)
		}
	}

	resp, err := http.Get("http://" + addr + "/data/public.txt")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("public.txt: got status %d, want 200", resp.StatusCode)
	}
}

// ---------------------------------------------------------------------
// renderDirJSON 的目录列表要用同一份过滤口径，不能列出点得开的东西之外的条目
// ---------------------------------------------------------------------

// renderDirJSON 的目录列表也要跳过敏感条目，不然会留下一堆点开就 404 的死链接
func TestRenderDirJSONSkipsFilteredEntries(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir+"/id_rsa", "private key")
	writeFile(t, dir+"/public.txt", "hello")
	fs := &fileService{dir: dir}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	fs.renderDirJSON(rec, req, dir)

	body := rec.Body.String()
	if strings.Contains(body, "id_rsa") {
		t.Errorf("listing should skip id_rsa, got: %s", body)
	}
	if !strings.Contains(body, "public.txt") {
		t.Errorf("listing should still show public.txt, got: %s", body)
	}
}

// bypass 打开之后 renderDirJSON 也应该照常列出敏感条目
func TestRenderDirJSONBypassShowsFilteredEntries(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir+"/id_rsa", "private key")
	fs := &fileService{dir: dir, bypass: true}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	fs.renderDirJSON(rec, req, dir)

	if !strings.Contains(rec.Body.String(), "id_rsa") {
		t.Error("listing should show id_rsa when bypass is enabled")
	}
}
