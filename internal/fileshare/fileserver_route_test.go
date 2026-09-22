package fileshare

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------
// Start：路由—— "/" 跳 "/ui/"，"/ui/*" 是页面，"/data/*" 是文件
// ---------------------------------------------------------------------

func TestRootRedirectsToUI(t *testing.T) {
	dir := t.TempDir()
	addr, err := Start(Config{Dir: dir, Public: true})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	client := &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.Get("http://" + addr + "/")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Errorf("got status %d, want 302", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/ui/" {
		t.Errorf("Location = %q, want %q", loc, "/ui/")
	}
}

// /ui/ 现在是 embed.FS 里的静态 index.html：不再由 Go 拼目录内容进 HTML，
// 目录列表改成页面加载后由 app.js 去 /data/ 拿 JSON 自己渲染，所以这里只
// 断言页面本身是合法的 HTML、Content-Type 对、并且引用了 app.js/app.css。
func TestUIServesLandingPage(t *testing.T) {
	dir := t.TempDir()
	addr, err := Start(Config{Dir: dir, Public: true})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	resp, err := http.Get("http://" + addr + "/ui/")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("got status %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
	body, _ := io.ReadAll(resp.Body)
	page := string(body)
	if !strings.Contains(page, "<!doctype html>") {
		t.Errorf("landing page should be an HTML document, got: %s", page)
	}
	if !strings.Contains(page, `href="app.css"`) || !strings.Contains(page, `src="app.js"`) {
		t.Errorf("landing page should reference app.css and app.js, got: %s", page)
	}
}

// /ui/app.css 和 /ui/app.js 要能取到，Content-Type 要对，不能让浏览器瞎猜
func TestUIServesAppCSS(t *testing.T) {
	dir := t.TempDir()
	addr, err := Start(Config{Dir: dir, Public: true})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	resp, err := http.Get("http://" + addr + "/ui/app.css")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("got status %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/css") {
		t.Errorf("Content-Type = %q, want text/css", ct)
	}
	body, _ := io.ReadAll(resp.Body)
	if len(body) == 0 {
		t.Error("app.css should not be empty")
	}
}

func TestUIServesAppJS(t *testing.T) {
	dir := t.TempDir()
	addr, err := Start(Config{Dir: dir, Public: true})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	resp, err := http.Get("http://" + addr + "/ui/app.js")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("got status %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/javascript") {
		t.Errorf("Content-Type = %q, want text/javascript", ct)
	}
	body, _ := io.ReadAll(resp.Body)
	if len(body) == 0 {
		t.Error("app.js should not be empty")
	}
}

// 页面里引用的每个静态资源都得真能取到。加一个新的 css/js 要同时改三处：
// 建文件、在 serveUIAsset 里加一条 case、在 index.html 里加标签——漏掉中间那条
// 就是线上 404，而且页面不会报错，只是功能静悄悄没了（少挂一个 window.trynetXxx
// 就够了）。这条测试直接顺着 index.html 的引用走一遍，以后加文件不用再回来改它。
func TestUIServesEveryAssetThePageReferences(t *testing.T) {
	dir := t.TempDir()
	addr, err := Start(Config{Dir: dir, Public: true})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	page, err := webFS.ReadFile("web/index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}

	refs := regexp.MustCompile(`(?:src|href)="([^"]+)"`).FindAllStringSubmatch(string(page), -1)
	found := 0
	for _, m := range refs {
		ref := m[1]
		// 只看页面自己的相对资源：跳过 # 锚点、外链，以及 /ui/icon.png 那种
		// 已经有专门测试的绝对路径
		if strings.ContainsAny(ref, "#:") || strings.HasPrefix(ref, "/") {
			continue
		}
		found++
		resp, err := http.Get("http://" + addr + "/ui/" + ref)
		if err != nil {
			t.Fatalf("get %s: %v", ref, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("%s: got status %d, want 200 -- missing a case in serveUIAsset?", ref, resp.StatusCode)
		}
		if len(body) == 0 {
			t.Errorf("%s: served an empty body", ref)
		}
	}
	// 一个引用都没匹配上说明正则或页面结构变了，别让这条测试静悄悄地什么都不测
	if found == 0 {
		t.Fatal("index.html referenced no relative assets; this test stopped testing anything")
	}
}

// /ui/icon.png 是页面的 favicon，浏览器控制台不该再报 favicon.ico 404
func TestUIServesIcon(t *testing.T) {
	dir := t.TempDir()
	addr, err := Start(Config{Dir: dir, Public: true})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	resp, err := http.Get("http://" + addr + "/ui/icon.png")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("got status %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "image/png" {
		t.Errorf("Content-Type = %q, want image/png", ct)
	}
	body, _ := io.ReadAll(resp.Body)
	if len(body) == 0 {
		t.Error("icon.png should not be empty")
	}
}

// /ui 子树下没定义的路径一律 404，不把整个 web/ 目录当通用静态文件服务器暴露出去
func TestUIRejectsUnknownAsset(t *testing.T) {
	dir := t.TempDir()
	addr, err := Start(Config{Dir: dir, Public: true})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	resp, err := http.Get("http://" + addr + "/ui/does-not-exist.txt")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("got status %d, want 404", resp.StatusCode)
	}
}

// 内容过滤只作用于 /data/*：/ui 本身不能因为分享目录里全是敏感文件就打不开，
// 否则下一批换成 embed.FS 的静态资源也会被同一层过滤器误伤。
func TestUIRouteNotContentFiltered(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir+"/id_rsa", "private key")
	addr, err := Start(Config{Dir: dir, Public: true})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	resp, err := http.Get("http://" + addr + "/ui/")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("/ui/ got status %d, want 200 even when the shared directory only has sensitive files", resp.StatusCode)
	}
}

// GET 一个 /data 目录现在返回 JSON，不是 HTML——页面自己用 app.js 渲染。
// curl /data/ 直接拿到 JSON 也是合理的行为。
func TestDataServesDirectoryListing(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir+"/hello.txt", "hi")
	if err := os.Mkdir(dir+"/sub", 0o755); err != nil {
		t.Fatal(err)
	}
	addr, err := Start(Config{Dir: dir, Public: true})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	resp, err := http.Get("http://" + addr + "/data/")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("got status %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), `"name":"hello.txt"`) || !strings.Contains(string(body), `"name":"sub"`) {
		t.Errorf("directory listing missing expected entries: %s", body)
	}
}

func TestDataServesSubdirectoryListing(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(dir+"/sub", 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir+"/sub/inner.txt", "hi")
	addr, err := Start(Config{Dir: dir, Public: true})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	resp, err := http.Get("http://" + addr + "/data/sub/")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("got status %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), `"name":"inner.txt"`) {
		t.Errorf("subdirectory listing missing expected entry: %s", body)
	}
}

// /data/ 的 JSON 字段要齐全：名字、是否目录、大小、修改时间都要有，而且
// 类型、取值都要对得上磁盘上的真实文件。
func TestDataDirectoryListingHasFullJSONFields(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir+"/hello.txt", "hello world")
	if err := os.Mkdir(dir+"/sub", 0o755); err != nil {
		t.Fatal(err)
	}
	addr, err := Start(Config{Dir: dir, Upload: true, Public: true})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	resp, err := http.Get("http://" + addr + "/data/")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var listing dirListing
	if err := json.Unmarshal(body, &listing); err != nil {
		t.Fatalf("decode listing: %v; body=%s", err, body)
	}
	if !listing.UploadEnabled {
		t.Error("listing should report uploadEnabled=true when -upload is set")
	}
	if len(listing.Entries) != 2 {
		t.Fatalf("got %d entries, want 2: %+v", len(listing.Entries), listing.Entries)
	}
	for _, e := range listing.Entries {
		switch e.Name {
		case "hello.txt":
			if e.IsDir {
				t.Error("hello.txt should not be reported as a directory")
			}
			if e.Size != int64(len("hello world")) {
				t.Errorf("hello.txt size = %d, want %d", e.Size, len("hello world"))
			}
			if e.ModTime == "" {
				t.Error("hello.txt should carry a non-empty modTime")
			}
		case "sub":
			if !e.IsDir {
				t.Error("sub should be reported as a directory")
			}
		default:
			t.Errorf("unexpected entry %q", e.Name)
		}
	}
}

// 文件名里带 "<script>" 一类内容，返回的 JSON 里必须是转义过的，不能原样
// 输出——这是 XSS。真去磁盘上创建这个文件名：Windows 的 NTFS 不允许文件名
// 里出现 "<" ">"，创建不了就跳过，留给 TestDirListingJSONEscapesFileNameForSafeEmbedding
// 在不依赖真实文件系统的情况下覆盖同一件事。
func TestDataListingEscapesFileNameXSS(t *testing.T) {
	dir := t.TempDir()
	maliciousName := `<script>alert(1)</script>.txt`
	if err := os.WriteFile(filepath.Join(dir, maliciousName), []byte("x"), 0o600); err != nil {
		t.Skipf("this filesystem does not allow a file named %q: %v", maliciousName, err)
	}

	addr, err := Start(Config{Dir: dir, Public: true})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	resp, err := http.Get("http://" + addr + "/data/")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if strings.Contains(string(body), "<script>") {
		t.Errorf("file name must be escaped, got raw tag: %s", body)
	}
}

func TestDataServesFileBytes(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir+"/report.pdf", "%PDF-fake-content")
	addr, err := Start(Config{Dir: dir, Public: true})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	resp, err := http.Get("http://" + addr + "/data/report.pdf")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "%PDF-fake-content" {
		t.Errorf("got body %q, want file content", body)
	}
}
