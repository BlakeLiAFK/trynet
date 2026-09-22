package fileshare

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------
// 上传：功能性
// ---------------------------------------------------------------------

// multipartUploadRequest 拼一个 multipart/form-data 的上传请求，file 字段名
// 固定叫 "file"，跟 renderDir 里的表单一致。
func multipartUploadRequest(t *testing.T, url, filename string, content []byte) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatalf("write part: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, url, &buf)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	return req
}

func TestUploadDisabledByDefault(t *testing.T) {
	dir := t.TempDir()
	addr, err := Start(Config{Dir: dir, Public: true})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	req := multipartUploadRequest(t, "http://"+addr+"/data/", "a.txt", []byte("hi"))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusSeeOther {
		t.Errorf("upload should be rejected when -upload is not set, got %d", resp.StatusCode)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("no file should have been written: %v", entries)
	}
}

func TestUploadWritesFile(t *testing.T) {
	dir := t.TempDir()
	addr, err := Start(Config{Dir: dir, Upload: true, Public: true})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	req := multipartUploadRequest(t, "http://"+addr+"/data/", "hello.txt", []byte("hello world"))
	resp, err := noRedirectClient.Do(req)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Errorf("got status %d, want 303", resp.StatusCode)
	}

	got, err := os.ReadFile(filepath.Join(dir, "hello.txt"))
	if err != nil {
		t.Fatalf("uploaded file missing: %v", err)
	}
	if string(got) != "hello world" {
		t.Errorf("uploaded content = %q, want %q", got, "hello world")
	}
}

// 上传要求整个服务都过鉴权，不是只保护上传口——没带凭据的上传请求也要 401，
// 而且必须真的没有写文件，不能返回 401 但背地里已经落盘了。
func TestUploadRequiresAuthWhenEnabled(t *testing.T) {
	dir := t.TempDir()
	addr, err := Start(Config{Dir: dir, Upload: true, User: "admin", Pass: "s3cret1"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	req := multipartUploadRequest(t, "http://"+addr+"/data/", "sneaky.txt", []byte("hi"))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("got status %d, want 401", resp.StatusCode)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("no file should have been written without credentials: %v", entries)
	}
}

// 同名文件不覆盖：原文件内容不变，新上传的改名
func TestUploadDoesNotOverwriteExistingFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.txt"), "original")

	addr, err := Start(Config{Dir: dir, Upload: true, Public: true})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	req := multipartUploadRequest(t, "http://"+addr+"/data/", "a.txt", []byte("uploaded"))
	resp, err := noRedirectClient.Do(req)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("got status %d, want 303", resp.StatusCode)
	}

	original, err := os.ReadFile(filepath.Join(dir, "a.txt"))
	if err != nil {
		t.Fatalf("original file missing: %v", err)
	}
	if string(original) != "original" {
		t.Errorf("original file content changed: %q", original)
	}

	renamed, err := os.ReadFile(filepath.Join(dir, "a (1).txt"))
	if err != nil {
		t.Fatalf("renamed upload missing: %v", err)
	}
	if string(renamed) != "uploaded" {
		t.Errorf("renamed upload content = %q, want %q", renamed, "uploaded")
	}
}

// 点开头的上传文件名要拒绝，和现有的点文件拦截规则保持一致
func TestUploadRejectsDotfileName(t *testing.T) {
	dir := t.TempDir()
	addr, err := Start(Config{Dir: dir, Upload: true, Public: true})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	req := multipartUploadRequest(t, "http://"+addr+"/data/", ".bashrc", []byte("evil"))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("got status %d, want 400 for dotfile upload", resp.StatusCode)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("dotfile should not have been written: %v", entries)
	}
}

// 敏感名单里的名字（不以点开头的那批）上传同样要拒绝——只挡下载不挡上传的话，
// 别人还是能往分享目录里塞一个 id_rsa 或 x.pem。
func TestUploadRejectsSensitiveName(t *testing.T) {
	dir := t.TempDir()
	addr, err := Start(Config{Dir: dir, Upload: true, Public: true})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	for _, name := range []string{"id_rsa", "x.pem", "credentials"} {
		t.Run(name, func(t *testing.T) {
			req := multipartUploadRequest(t, "http://"+addr+"/data/", name, []byte("evil"))
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("upload: %v", err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusBadRequest {
				t.Errorf("got status %d, want 400 for sensitive name %q", resp.StatusCode, name)
			}
			if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
				t.Errorf("%q should not have been written to disk", name)
			}
		})
	}
}

// -bypass 打开之后写侧也放开：敏感文件名允许上传
func TestUploadBypassAllowsSensitiveName(t *testing.T) {
	dir := t.TempDir()
	addr, err := Start(Config{Dir: dir, Upload: true, Bypass: true, Public: true})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	req := multipartUploadRequest(t, "http://"+addr+"/data/", "id_rsa", []byte("private key"))
	resp, err := noRedirectClient.Do(req)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Errorf("got status %d, want 303 with -bypass", resp.StatusCode)
	}

	got, err := os.ReadFile(filepath.Join(dir, "id_rsa"))
	if err != nil {
		t.Fatalf("uploaded file missing: %v", err)
	}
	if string(got) != "private key" {
		t.Errorf("uploaded content = %q, want %q", got, "private key")
	}
}

// 单文件大小上限：用一个很小的 maxBytes 直接构造 fileService，不用真的发 100MB
func TestUploadRejectsOversizedFile(t *testing.T) {
	dir := t.TempDir()
	fs := &fileService{dir: dir, upload: true, maxBytes: 4}
	srv := httptest.NewServer(fs)
	defer srv.Close()

	req := multipartUploadRequest(t, srv.URL+"/", "big.txt", []byte("this is way more than 4 bytes"))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("got status %d, want 400 for oversized upload", resp.StatusCode)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("oversized upload should not have been written: %v", entries)
	}
}

// ---------------------------------------------------------------------
// 上传：路径穿越——安全测试的重点，真去检查分享目录外面没有多出文件，
// 不能只看返回码。
// ---------------------------------------------------------------------

// snapshotFilesOutside 列出 root 下、shareDir 之外的所有文件的相对路径（排过序），
// 用来在攻击前后做对比：分享目录外面一个文件都不该多出来。
func snapshotFilesOutside(t *testing.T, root, shareDir string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == shareDir || strings.HasPrefix(p, shareDir+string(filepath.Separator)) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(root, p)
		if relErr != nil {
			return relErr
		}
		out = append(out, rel)
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	sort.Strings(out)
	return out
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// 上传文件名里的各种穿越花招：全部必须被挡住（要么整个请求被拒，要么文件名
// 被安全地收窄成一个不带分隔符的裸文件名），关键断言是分享目录外面
// 没有多出任何文件——不是看返回码。
func TestUploadFilenameTraversalNeverEscapesShareDir(t *testing.T) {
	root := t.TempDir()
	shareDir := filepath.Join(root, "share")
	if err := os.Mkdir(shareDir, 0o755); err != nil {
		t.Fatal(err)
	}

	addr, err := Start(Config{Dir: shareDir, Upload: true, Public: true})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	names := []string{
		"../x",
		"..\\x",
		"../../../.ssh/authorized_keys",
		"..\\..\\windows\\system32\\evil.dll",
		"%2e%2e%2fx",
		"....//x",
		"/etc/passwd",
		"C:\\Windows\\System32\\evil.dll",
		"a/../../b",
		"..",
		".",
	}

	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			before := snapshotFilesOutside(t, root, shareDir)

			req := multipartUploadRequest(t, "http://"+addr+"/data/", name, []byte("payload"))
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("upload: %v", err)
			}
			resp.Body.Close()

			after := snapshotFilesOutside(t, root, shareDir)
			if !sameStrings(before, after) {
				t.Fatalf("filename %q let a file escape the shared directory: before=%v after=%v", name, before, after)
			}
		})
	}
}

// 上传目标目录（URL 路径本身）也可能带穿越花招，指向分享目录外的一个真实
// 存在的目录——这是 ensureWithinDir/filepath.Rel 真正把关的地方，不是靠
// 文件名里的 Base 提取能挡住的。
func TestUploadURLPathCannotTargetDirectoryOutsideShareDir(t *testing.T) {
	root := t.TempDir()
	shareDir := filepath.Join(root, "share")
	evilDir := filepath.Join(root, "evil")
	if err := os.Mkdir(shareDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(evilDir, 0o755); err != nil {
		t.Fatal(err)
	}

	addr, err := Start(Config{Dir: shareDir, Upload: true, Public: true})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	req := multipartUploadRequest(t, "http://"+addr+"/data/../evil/", "pwned.txt", []byte("payload"))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	resp.Body.Close()

	entries, err := os.ReadDir(evilDir)
	if err != nil {
		t.Fatalf("read evilDir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("upload escaped the shared directory into %s: %v", evilDir, entries)
	}
	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusSeeOther {
		t.Errorf("escaping upload target should not succeed, got %d", resp.StatusCode)
	}
}

// GET 也要挡住穿越：请求分享目录外的一个真实文件不能读到内容
func TestDownloadTraversalCannotEscapeShareDir(t *testing.T) {
	root := t.TempDir()
	shareDir := filepath.Join(root, "share")
	if err := os.Mkdir(shareDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "secret.txt"), "top secret outside the share dir")

	addr, err := Start(Config{Dir: shareDir, Public: true})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	resp, err := http.Get("http://" + addr + "/data/../secret.txt")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if strings.Contains(string(body), "top secret") {
		t.Fatalf("download traversal leaked a file outside the shared directory: %q", body)
	}
	if resp.StatusCode == http.StatusOK {
		t.Errorf("traversal download should not return 200, got body %q", body)
	}
}
