// share.go 提供单文件临时分享链接：登录用户对某个文件签发一个带过期时间的
// token，拿到链接的人不用登录就能下载**那一个文件**。
//
// 和会话 cookie 的区别是授权范围，不是实现方式：cookie 是整个分享目录的钥匙，
// 分享 token 只对签发它的那一个路径有效，拿去请求别的文件一律 401。这样"把
// 一份报告发给同事"不用连带交出整个目录的访问权。
//
// 三条刻意的约束：
//   - 只对 GET/HEAD 有效，带 token 发 POST 是 401——分享链接永远不能变成写入口
//   - 固定 1 小时过期，不做滑动续期。滑动续期用在会话上是"人还在用就别踢他"，
//     用在分享链接上就成了"只要有人一直下载就永不过期"，恰好是它该避免的
//   - 只存在内存里，进程重启全部失效，符合"临时"的定位
package fileshare

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"sync"
	"time"
)

const (
	shareTTL           = time.Hour
	shareCleanupPeriod = 10 * time.Minute
	shareTokenBytes    = 16 // 128 位随机。会话那边的 32 字节在这里是浪费——这串东西要塞进 URL 里给人复制
	shareQueryParam    = "t"
	dataPrefix         = "/data/"
)

// shareGrant 是一张 token 对应的授权：能访问哪个路径、什么时候作废。
// path 存的是解码后的绝对 URL 路径（"/data/my report.pdf"），跟
// net/http 交给 handler 的 r.URL.Path 口径一致，比较时不用再解码一次。
type shareGrant struct {
	path   string
	expiry time.Time
}

// shareStore 是分享 token 的服务端存储，结构和 sessionStore 一样：一张表加
// 一把互斥锁。区别在于值不只是过期时间，还带着这张 token 能碰的那个路径。
type shareStore struct {
	mu     sync.Mutex
	tokens map[string]shareGrant
}

func newShareStore() *shareStore {
	return &shareStore{tokens: make(map[string]shareGrant)}
}

// create 为 urlPath 签发一张新 token。urlPath 必须是已经校验过的、解码并
// 规范化之后的路径——校验在 shareHandler 里做，这里只管生成和记账。
func (s *shareStore) create(urlPath string) (string, error) {
	buf := make([]byte, shareTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(buf)

	s.mu.Lock()
	s.tokens[token] = shareGrant{path: urlPath, expiry: time.Now().Add(shareTTL)}
	s.mu.Unlock()
	return token, nil
}

// validate 检查 token 有效、未过期，**并且**它绑定的路径就是本次请求的路径。
// 路径不匹配和 token 不存在同样返回 false：一张 token 不是通行证，它只开一扇门。
//
// 命中时不续期——这是和 sessionStore.validate 最要紧的区别，别照着那边抄。
func (s *shareStore) validate(token, urlPath string) bool {
	if token == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	grant, ok := s.tokens[token]
	if !ok {
		return false
	}
	if time.Now().After(grant.expiry) {
		delete(s.tokens, token) // 顺手清掉，不用等下一轮 cleanup
		return false
	}
	return grant.path == path.Clean(urlPath)
}

// cleanupExpired 扫一遍表删掉过期条目。和 sessionStore 那边一样，不靠它也不会
// 有正确性问题（validate 命中过期条目时已经会删），纯粹是防止长期挂着的进程里
// 一堆没人再碰的过期 token 占着内存。
func (s *shareStore) cleanupExpired() {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for token, grant := range s.tokens {
		if now.After(grant.expiry) {
			delete(s.tokens, token)
		}
	}
}

func (s *shareStore) startCleanup(period time.Duration) {
	go func() {
		ticker := time.NewTicker(period)
		defer ticker.Stop()
		for range ticker.C {
			s.cleanupExpired()
		}
	}()
}

// shareRequest 是 POST /auth/share 的请求体，path 是页面上那个下载链接原样
// 回传（"/data/sub/report.pdf"，各段是 encodeURIComponent 编码过的）。
type shareRequest struct {
	Path string `json:"path"`
}

type shareResponse struct {
	URL       string `json:"url"`
	ExpiresIn int    `json:"expiresIn"`
}

// shareHandler 签发分享链接。调用方必须已经有有效会话（挂载时套了 requireAuth，
// 且不接受分享 token——token 签不出新 token）。
//
// 签发前拒绝三种路径，宁可签不出来也不签出一个越界或者用不了的 token：
//  1. 目录：用户要分享的是文件。给目录签 token 会漏一份文件名清单出去（里面的
//     文件路径不同、token 对不上，下不动，但名字已经漏了）
//  2. 被内容过滤挡掉的路径（.env、id_rsa 这些）：不拦就会签出一个永远 404 的
//     token，用户以为分享成功了，比直接失败更糟
//  3. 分享目录之外的路径：复用 resolveRequestPath 那套包含性判断，不另写一遍
//
// 三种拒绝共用同一句错误文案。区分开来等于告诉调用方"这个路径存在但被过滤了"，
// 而被过滤的东西本来就不该出现在他看得到的列表里。
func shareHandler(shares *shareStore, dir string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req shareRequest
		_ = json.NewDecoder(r.Body).Decode(&req) // 解析失败就留空，跟非法路径走同一条拒绝分支

		clean, ok := shareablePath(dir, req.Path)
		if !ok {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"That path can't be shared"}`))
			return
		}

		token, err := shares.create(clean)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		// 用 url.URL 拼而不是字符串相加：文件名里的空格和中文要重新转义回去，
		// 拼出来的链接才能直接粘到浏览器或 curl 里
		link := (&url.URL{Path: clean, RawQuery: shareQueryParam + "=" + token}).String()
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(shareResponse{URL: link, ExpiresIn: int(shareTTL.Seconds())})
	})
}

// shareablePath 把页面回传的 /data 链接校验并规范化成可以签发的路径，
// 返回解码、Clean 之后的绝对 URL 路径。任何一步不过关都返回 false。
func shareablePath(dir, raw string) (string, bool) {
	if !strings.HasPrefix(raw, dataPrefix) {
		return "", false
	}
	decoded, err := url.PathUnescape(raw)
	if err != nil {
		return "", false
	}
	// 先解码再 Clean：顺序反过来的话 "%2e%2e" 会在 Clean 之后才变成 ".."，
	// 逃出 /data 前缀的检查
	clean := path.Clean(decoded)
	if !strings.HasPrefix(clean, dataPrefix) {
		return "", false
	}

	for _, seg := range strings.Split(clean, "/") {
		if isFilteredName(seg) {
			return "", false
		}
	}

	local, err := resolveRequestPath(dir, strings.TrimPrefix(clean, "/data"))
	if err != nil {
		return "", false
	}
	fi, err := os.Stat(local)
	if err != nil || !fi.Mode().IsRegular() {
		return "", false // 目录、设备文件、不存在的路径，都不签
	}
	return clean, true
}
