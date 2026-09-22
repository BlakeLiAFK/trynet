// fileserver.go 提供完整的文件服务：目录列表、下载、上传，外加保护写权限的
// 鉴权。只读时最坏情况是被人读到文件；开了上传之后，鉴权就是唯一挡着"别人往
// 硬盘里写东西"的门，所以这里每一处校验都要经得起推敲：路径穿越用
// filepath.Rel 判断包含关系（不能用字符串前缀匹配，"/data" 和 "/data-evil"
// 会让前缀匹配误判通过）、密码生成用 crypto/rand。会话 cookie 鉴权（登录弹窗
// 背后那套 token 存储、常量时间凭据比较）在 session.go，这里只管把 /data 套
// 进 requireAuth——鉴权只认会话 cookie 这一条路，不解析 Authorization 头。
//
// 路由分几个命名空间：/ui 是页面（embed.FS 打包的静态 index.html/app.css/
// app.js/modal.css/auth.js，目录内容由页面加载后自己去 /data 拿 JSON 渲染），
// /data 才是真正的文件读写，GET 目录返回 JSON、GET 文件返回字节、POST 上传；
// /auth/login、/auth/logout 处理会话登录登出。鉴权只套在 /data 上，/ui 公开
// 不鉴权——页面本身是不含真实数据的空壳，鉴权套在它上面的话浏览器连页面都
// 加载不到，没法在页面里弹登录框，这是 SPA 的标准做法。内容过滤（点文件 +
// 敏感名单）同样只套在 /data 上——套在 /ui 上的话，/ui 自己的 CSS/JS 会被
// 过滤器误伤，症状是"页面打得开但没样式"，很难查。
package fileshare

import (
	"crypto/rand"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// webFS 打包页面的静态资源（index.html/app.css/app.js）。目录必须叫 "web"，
// 不能叫 "_web" 或 ".web"——go:embed 会自动跳过 "_" 或 "." 开头的目录，
// 那样会拿到一个空 FS：编译不报错，运行时全部 404，很难查。
//
//go:embed web
var webFS embed.FS

// maxUploadBytes 是单次上传允许的最大字节数，用 http.MaxBytesReader 强制生效
const maxUploadBytes = 100 << 20 // 100 MB

// passwordAlphabet 用来生成随机密码：纯数字。这串东西经常是念给旁边的人、
// 或者在手机上敲进登录框的，数字键盘一次到位，也不用分辨 0/O、1/l/I。
const passwordAlphabet = "0123456789"

// PasswordDigits 是自动生成的密码位数：8 位，跟手机验证码一个长度，好记好念。
//
// 纯数字 8 位 ≈ 26.6 bit。配合登录失败固定 300ms 的延迟，单条连接爆破平均要
// 半年；几百条连接一起打能压到天级别。对快速隧道来说这远远够用——那个域名
// 本身只活几十分钟。长期挂着的固定域名想要更强的，用 -pass 指定自己的密码。
const PasswordDigits = 8

// Config 收拢一次 Start 调用需要的所有参数
type Config struct {
	Dir    string
	Bypass bool // 为 true 时关闭全部内容过滤（点文件 + 敏感名单），同时影响读和写，是逃生舱
	Upload bool // 是否开启上传表单和写入端点
	Public bool // 为 true 时 /data 完全不鉴权，任何人都能读（upload 开着时也能写）
	User   string
	Pass   string
}

// Start 在随机本地端口起一个文件服务，命名空间分四块：
// /ui 是页面，embed.FS 打包的静态资源，公开、不鉴权——页面本身是不含真实
// 数据的空壳，鉴权套在它上面的话浏览器连页面都加载不到，也就没法在页面里
// 弹登录框了，这是 SPA 的标准做法。/data 是文件读写：目录列表（JSON）、
// 下载、upload 开启时再加上传，鉴权覆盖。/auth 是 login/logout，会话登录
// 登出、以及签发单文件分享链接，见 session.go 和 share.go。public 为 false
// （默认）时 /data 要求有效的会话 cookie 或一张为该路径签发的分享 token；
// bypass 为 false（默认）时内容过滤只套在 /data 上。
func Start(cfg Config) (string, error) {
	if _, err := os.Stat(cfg.Dir); err != nil {
		return "", err
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}

	store := newSessionStore()
	store.startCleanup(sessionCleanupPeriod)
	shares := newShareStore()
	shares.startCleanup(shareCleanupPeriod)

	srv := &http.Server{Handler: buildFileServerHandler(cfg, store, shares)}
	go func() { _ = srv.Serve(ln) }()
	return ln.Addr().String(), nil
}

// buildFileServerHandler 组装完整的路由树，单独拆出来是为了让测试能直接
// 传一个自己造的 sessionStore 进来（比如强行往表里塞一个已过期的 token），
// 不用真的等 12 小时或者搭一整套时钟注入。
func buildFileServerHandler(cfg Config, store *sessionStore, shares *shareStore) http.Handler {
	fs := &fileService{dir: cfg.Dir, public: cfg.Public, upload: cfg.Upload, bypass: cfg.Bypass, maxBytes: maxUploadBytes, urlPrefix: "/data"}

	mux := http.NewServeMux()
	mux.HandleFunc("/", redirectRootToUI)
	mux.HandleFunc("/ui/", serveUIAsset)
	mux.Handle("/auth/login", loginHandler(store, cfg.User, cfg.Pass))
	mux.Handle("/auth/logout", logoutHandler(store))
	// 公开模式下签发受限 token 没有意义（东西本来就都能拿），接口干脆不挂载。
	// 第三个参数传 nil：签发接口只认会话 cookie，拿分享 token 换不出新 token。
	if !cfg.Public {
		mux.Handle("/auth/share", requireAuth(shareHandler(shares, cfg.Dir), store, nil))
	}

	// StripPrefix 让 fs 内部继续按老样子处理相对于分享目录的路径（"/sub/x.txt"
	// 而不是 "/data/sub/x.txt"），resolveRequestPath/renderDirJSON 都不用因为
	// 多了一层前缀而改动；fs.urlPrefix 记着这层前缀，只用于拼重定向的 Location。
	var dataHandler http.Handler = http.StripPrefix("/data", fs)
	if !cfg.Bypass {
		dataHandler = blockSensitive(dataHandler)
	}
	if !cfg.Public {
		dataHandler = requireAuth(dataHandler, store, shares)
	}
	mux.Handle("/data/", dataHandler)

	// /thumb 是网格模式的缩略图，跟 /data 同一套过滤和鉴权，但第三个参数传 nil：
	// 不收分享 token。拿到分享链接的人得到的是一个直接下载地址，从来不会看到
	// 网格界面，给他开这条路等于白白多一个入口。
	var thumbHandler http.Handler = http.StripPrefix("/thumb", http.HandlerFunc(fs.serveThumbnail))
	if !cfg.Bypass {
		thumbHandler = blockSensitive(thumbHandler)
	}
	if !cfg.Public {
		thumbHandler = requireAuth(thumbHandler, store, nil)
	}
	mux.Handle("/thumb/", thumbHandler)

	return mux
}

// redirectRootToUI 把 "/" 跳到 "/ui/"；除了 "/" 本身之外的任何没被 /ui、/data
// 两个子树接住的路径都 404——直接文件链接一律要走 /data，不再在根命名空间里放行。
func redirectRootToUI(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	http.Redirect(w, r, "/ui/", http.StatusFound)
}

// GeneratePassword 用 crypto/rand 生成一个 n 位随机密码（不是 math/rand——那个
// 可预测，密码强度全靠它就等于没有）。用 crypto/rand.Int 逐位从 passwordAlphabet
// 里取样，避免用取模处理随机字节带来的轻微偏差。
func GeneratePassword(n int) (string, error) {
	alphabetLen := big.NewInt(int64(len(passwordAlphabet)))
	buf := make([]byte, n)
	for i := range buf {
		idx, err := rand.Int(rand.Reader, alphabetLen)
		if err != nil {
			return "", err
		}
		buf[i] = passwordAlphabet[idx.Int64()]
	}
	return string(buf), nil
}

// fileService 是 /data 的业务 handler：GET/HEAD 列目录或下载文件，POST 处理上传。
// 点文件/敏感名单拦截、鉴权都是外层套的 handler，这里不用再操心那两件事；
// fs 自己只在渲染目录列表和收上传文件名时，用 bypass 再判一次要不要跳过敏感条目。
type fileService struct {
	dir       string
	public    bool
	upload    bool
	bypass    bool   // 为 true 时关闭内容过滤：目录列表照常显示敏感条目，上传也不再拒绝敏感文件名
	maxBytes  int64  // 单次上传的大小上限，Start 里固定填 maxUploadBytes
	urlPrefix string // fs 挂载在真实 URL 空间里的前缀（"/data"），只用于拼重定向的 Location；
	// 直接拿 fs 构造 httptest.NewServer(fs) 的测试不经过 StripPrefix，这个字段留空即可。
}

func (fs *fileService) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		fs.handleGet(w, r)
	case http.MethodPost:
		fs.handleUpload(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// serveUIAsset 把 embed.FS 里的静态页面资源按精确路径分发出去：index.html、
// app.css、app.js，一一对应，Content-Type 手动指定，不让浏览器瞎猜。页面本身
// 是纯静态文件，不再由 Go 拼 HTML——目录内容改由 app.js 调 /data/ 的 JSON
// 接口渲染，见 renderDirJSON。
//
// 只认这三个路径：/ui 子树下别的路径一律 404，不把整个 web/ 目录当成通用静态
// 文件服务器暴露出去。
func serveUIAsset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var name, contentType string
	switch r.URL.Path {
	case "/ui/":
		name, contentType = "web/index.html", "text/html; charset=utf-8"
	case "/ui/app.css":
		name, contentType = "web/app.css", "text/css; charset=utf-8"
	case "/ui/modal.css":
		name, contentType = "web/modal.css", "text/css; charset=utf-8"
	case "/ui/share.css":
		name, contentType = "web/share.css", "text/css; charset=utf-8"
	case "/ui/grid.css":
		name, contentType = "web/grid.css", "text/css; charset=utf-8"
	case "/ui/app.js":
		name, contentType = "web/app.js", "text/javascript; charset=utf-8"
	case "/ui/auth.js":
		name, contentType = "web/auth.js", "text/javascript; charset=utf-8"
	case "/ui/share.js":
		name, contentType = "web/share.js", "text/javascript; charset=utf-8"
	case "/ui/grid.js":
		name, contentType = "web/grid.js", "text/javascript; charset=utf-8"
	case "/ui/icon.png":
		name, contentType = "web/icon.png", "image/png"
	default:
		http.NotFound(w, r)
		return
	}

	data, err := webFS.ReadFile(name)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", contentType)
	_, _ = w.Write(data)
}

// resolveRequestPath 把 URL 路径映射到分享目录下的本地路径。GET 用它定位要
// 展示的目录/文件，POST 用它定位上传目标目录。filepath.Join 本身会做 Clean，
// 但 Clean 不认识"分享目录的边界在哪"——urlPath 里的 ".." 完全可能把 local
// 算到 dir 外面去，真正挡住穿越的是下面这一步 ensureWithinDir 的包含关系校验，
// 不能省。
func resolveRequestPath(dir, urlPath string) (string, error) {
	local := filepath.Join(dir, filepath.FromSlash(urlPath))
	if err := ensureWithinDir(dir, local); err != nil {
		return "", err
	}
	return local, nil
}

// ensureWithinDir 确认 target 仍然落在 dir 内部。不能用 strings.HasPrefix 判断——
// "/data" 和 "/data-evil" 会让前缀匹配误判通过——必须先转成绝对路径，再用
// filepath.Rel 算出真正的相对关系：结果不能是 ".."、不能以 "../" 开头、
// 也不能本身还是个绝对路径。
func ensureWithinDir(dir, target string) error {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(absDir, absTarget)
	if err != nil {
		return err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return errors.New("path escapes shared directory")
	}
	return nil
}

func (fs *fileService) handleGet(w http.ResponseWriter, r *http.Request) {
	local, err := resolveRequestPath(fs.dir, r.URL.Path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	info, err := os.Stat(local)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if info.IsDir() {
		// 相对链接（比如 "sub/"）要求目录 URL 以 / 结尾，否则浏览器会拿去拼上一级；
		// Location 要带上 fs.urlPrefix，不然重定向会落到真实 URL 空间里少一层 /data 的地方。
		if !strings.HasSuffix(r.URL.Path, "/") {
			http.Redirect(w, r, fs.urlPrefix+r.URL.Path+"/", http.StatusMovedPermanently)
			return
		}
		fs.renderDirJSON(w, r, local)
		return
	}
	http.ServeFile(w, r, local)
}

// dirEntry 是目录列表 JSON 里的单条记录。
type dirEntry struct {
	Name    string `json:"name"`
	IsDir   bool   `json:"isDir"`
	Size    int64  `json:"size"`
	ModTime string `json:"modTime"` // RFC3339，UTC
	// Thumb 说明这个条目值不值得去请求 /thumb。由服务端给答案而不是让前端
	// 按扩展名猜：猜的话两边的支持列表迟早漂移，而且每个非图片条目都会白跑
	// 一次 404 请求——一个几百个文件的目录就是几百次。
	Thumb bool `json:"thumb"`
}

// dirListing 是 GET 一个 /data 目录时返回的 JSON 响应体。
type dirListing struct {
	Path          string     `json:"path"`
	Entries       []dirEntry `json:"entries"`
	UploadEnabled bool       `json:"uploadEnabled"`
	// ShareEnabled 为 false 时页面不渲染分享按钮。-public 下签发接口压根没挂载，
	// 渲染出来只会点一下报错。
	ShareEnabled bool `json:"shareEnabled"`
}

// renderDirJSON 把目录内容编码成 JSON 返回，页面自己用 app.js 渲染成列表。
// encoding/json 默认会把 <、>、& 转成 \u003c 之类的转义序列（HTMLEscape 没有
// 关掉），文件名里带 "<script>" 这类内容原样编码进去也不会被当成标签解析，
// 这是 Go 标准库的默认行为，不用额外处理。
func (fs *fileService) renderDirJSON(w http.ResponseWriter, r *http.Request, local string) {
	entries, err := os.ReadDir(local)
	if err != nil {
		http.Error(w, "cannot list directory", http.StatusInternalServerError)
		return
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsDir() != entries[j].IsDir() {
			return entries[i].IsDir() // 目录排前面
		}
		return entries[i].Name() < entries[j].Name()
	})

	listing := dirListing{
		Path:          r.URL.Path,
		Entries:       []dirEntry{},
		UploadEnabled: fs.upload,
		ShareEnabled:  !fs.public,
	}
	for _, e := range entries {
		name := e.Name()
		if !fs.bypass && isFilteredName(name) {
			continue // 目录列表也要挡，跟下载权限保持一致，不留一个打不开的死链接
		}
		info, err := e.Info()
		if err != nil {
			continue // 拿不到信息就跳过这一条，不让它拖垮整份列表
		}
		listing.Entries = append(listing.Entries, dirEntry{
			Name:    name,
			IsDir:   e.IsDir(),
			Size:    info.Size(),
			ModTime: info.ModTime().UTC().Format(time.RFC3339),
			// 只看扩展名和大小，不在列目录时就去解码——解一整个目录的图
			// 只为了填一个布尔值，代价完全不成比例。解不开的那些，
			// /thumb 会回 404，前端的 onerror 兜底还在
			Thumb: !e.IsDir() && isThumbnailable(name) && info.Size() <= thumbMaxSourceBytes,
		})
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(listing); err != nil {
		log.Printf("encode directory listing: %v", err)
	}
}

// handleUpload 处理 multipart 上传。文件名只取 base name、丢掉任何目录成分，
// 再确认目标仍在分享目录内；同名文件不覆盖，依次加 " (1)"、" (2)" 之类的后缀。
func (fs *fileService) handleUpload(w http.ResponseWriter, r *http.Request) {
	if !fs.upload {
		http.NotFound(w, r)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, fs.maxBytes)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		http.Error(w, "upload too large or malformed", http.StatusBadRequest)
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "missing file", http.StatusBadRequest)
		return
	}
	defer file.Close()

	dirPath, err := resolveRequestPath(fs.dir, r.URL.Path)
	if err != nil {
		http.Error(w, "invalid upload target", http.StatusBadRequest)
		return
	}
	if info, err := os.Stat(dirPath); err != nil || !info.IsDir() {
		http.Error(w, "invalid upload target", http.StatusBadRequest)
		return
	}

	name, err := safeUploadName(header.Filename, fs.bypass)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// safeUploadName 已经把 name 收窄成一个不带分隔符的裸文件名，这里再算一遍
	// 完整目标路径是否仍在分享目录内，属于防御性的第二道检查——按规格要求，
	// 不能只信一层。
	if err := ensureWithinDir(fs.dir, filepath.Join(dirPath, name)); err != nil {
		http.Error(w, "invalid file name", http.StatusBadRequest)
		return
	}

	out, dest, err := createUploadFile(dirPath, name)
	if err != nil {
		http.Error(w, "cannot create destination", http.StatusInternalServerError)
		return
	}
	if _, err := io.Copy(out, file); err != nil {
		out.Close()
		os.Remove(dest)
		http.Error(w, "upload failed", http.StatusInternalServerError)
		return
	}
	out.Close()

	http.Redirect(w, r, fs.urlPrefix+r.URL.Path, http.StatusSeeOther)
}

// safeUploadName 校验并规范化上传文件名：丢掉任何目录成分、拒绝空名、
// "."、".."、以点开头的名字，以及命中敏感名单的名字（除非 bypass 打开）。
// 按 "/" 和 "\" 两种分隔符各自取最后一段，不依赖运行平台判断哪个字符是
// 分隔符——Windows 风格的 "..\..\x" 在 Linux 上不会被当成路径分隔符处理，
// 必须显式转换，否则那台机器上这条防线就是摆设。
//
// 过滤同时管读和写：只挡下载、不挡上传的话，别人还是能把 id_rsa、.bashrc
// 这类文件写进分享目录——bypass 为 true 时两边一起放开，保持行为对称。
func safeUploadName(name string, bypass bool) (string, error) {
	if name == "" {
		return "", errors.New("empty file name")
	}
	normalized := strings.ReplaceAll(name, "\\", "/")
	clean := path.Clean("/" + normalized)
	base := path.Base(clean)
	if base == "" || base == "." || base == ".." || base == "/" {
		return "", errors.New("invalid file name")
	}
	if !bypass {
		if strings.HasPrefix(base, ".") {
			return "", errors.New("dotfile uploads are not allowed")
		}
		if isSensitiveName(base) {
			return "", errors.New("this file name is not allowed")
		}
	}
	return base, nil
}

// createUploadFile 在 dir 下创建一个新文件，不覆盖已有同名文件：先试原名，
// 冲突就依次试 "name (1)"、"name (2)"……用 O_EXCL 独占创建，避免先 Stat
// 再 Open 之间的竞态窗口。
func createUploadFile(dir, name string) (*os.File, string, error) {
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	candidate := name
	for i := 0; i <= 10000; i++ {
		if i > 0 {
			candidate = fmt.Sprintf("%s (%d)%s", base, i, ext)
		}
		dest := filepath.Join(dir, candidate)
		f, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err == nil {
			return f, dest, nil
		}
		if !os.IsExist(err) {
			return nil, "", err
		}
	}
	return nil, "", errors.New("too many name collisions")
}
