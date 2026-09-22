// session.go 提供会话 cookie 鉴权：浏览器登录一次换一个不透明 token，存进
// 服务端内存表，往后请求靠 Cookie 免登录。/data 只认这一条路径——不再解析
// Authorization: Basic，401 响应也不带 WWW-Authenticate，避免浏览器弹出
// 原生 Basic Auth 对话框（那正是登录弹窗要取代的东西），失败统一交给前端
// JS 自己弹登录框。credentialsMatch 是登录接口校验用户名密码时用的常量
// 时间比较。
package fileshare

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

const (
	sessionCookieName    = "trynet_session"
	sessionTTL           = 12 * time.Hour
	sessionCleanupPeriod = time.Hour
	sessionTokenBytes    = 32 // crypto/rand 生成的原始字节数，base64url 编码后进 cookie
	loginFailDelay       = 300 * time.Millisecond
)

// sessionStore 是登录会话的服务端存储：token 到过期时间的映射，一把互斥锁
// 保护并发读写。token 本身是 32 字节 crypto/rand 随机数，猜中它的概率跟
// 猜中一把 256 位密钥一样低，不需要额外签名或加密。
type sessionStore struct {
	mu     sync.Mutex
	tokens map[string]time.Time
}

func newSessionStore() *sessionStore {
	return &sessionStore{tokens: make(map[string]time.Time)}
}

// create 生成一个新 token 并记入存储，过期时间是当前时间 + sessionTTL。
func (s *sessionStore) create() (string, error) {
	buf := make([]byte, sessionTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(buf)

	s.mu.Lock()
	s.tokens[token] = time.Now().Add(sessionTTL)
	s.mu.Unlock()
	return token, nil
}

// validate 检查 token 是否存在且未过期；命中时顺带续期（滑动过期），只要
// 用户还在活动，会话就不会中途过期。空 token 直接判无效，不用进锁。
func (s *sessionStore) validate(token string) bool {
	if token == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	exp, ok := s.tokens[token]
	if !ok || time.Now().After(exp) {
		delete(s.tokens, token) // 顺手清掉已过期的条目，不用等下一轮 cleanup
		return false
	}
	s.tokens[token] = time.Now().Add(sessionTTL)
	return true
}

// revoke 从存储里真正删除 token——登出必须是服务端主动失效，只清浏览器那份
// cookie 没用，token 还留在服务端就还能被人拿着旧值继续访问。
func (s *sessionStore) revoke(token string) {
	s.mu.Lock()
	delete(s.tokens, token)
	s.mu.Unlock()
}

// cleanupExpired 扫一遍表，删掉所有已过期的条目。不靠这个也不会有正确性
// 问题（validate 命中过期条目时已经会删），纯粹是防止长期挂着的进程里一堆
// 从没再被访问过的过期 token 一直占着内存不释放。
func (s *sessionStore) cleanupExpired() {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for token, exp := range s.tokens {
		if now.After(exp) {
			delete(s.tokens, token)
		}
	}
}

// startCleanup 起一个后台协程按固定周期清理过期条目，跟 Start 里
// srv.Serve 那个协程一样常驻到进程退出，不需要显式停止。
func (s *sessionStore) startCleanup(period time.Duration) {
	go func() {
		ticker := time.NewTicker(period)
		defer ticker.Stop()
		for range ticker.C {
			s.cleanupExpired()
		}
	}()
}

// credentialsMatch 常量时间比较用户名和密码，loginHandler 校验凭据用这一份：
// 用户名和密码都必须比较完才返回结果，提前返回会用响应时间泄漏"用户名对不对"。
func credentialsMatch(gotUser, gotPass, user, pass string) bool {
	userOK := subtle.ConstantTimeCompare([]byte(gotUser), []byte(user)) == 1
	passOK := subtle.ConstantTimeCompare([]byte(gotPass), []byte(pass)) == 1
	return userOK && passOK
}

// requireAuth 包一层鉴权，认两条路：
//
//   - 有效的会话 cookie——浏览器登录一次换来的，覆盖 /data 的全部读写
//   - GET/HEAD 请求上带的分享 token（?t=），且这张 token 就是为本次请求的
//     路径签发的。范围窄得多：一张 token 只开一个文件，而且只读
//
// shares 为 nil 表示这条路由不接受分享 token。签发接口 /auth/share 自己就走
// 这条——否则拿一张分享 token 就能换出新的分享 token，token 的范围限制形同虚设。
//
// 两条都不通就回 401，而且不带 WWW-Authenticate——带了浏览器会弹原生 Basic
// Auth 对话框，那正是登录弹窗要取代的东西。前端 JS 拿到 401 之后自己弹登录框，
// 见 app.js/auth.js。
func requireAuth(next http.Handler, store *sessionStore, shares *shareStore) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if cookie, err := r.Cookie(sessionCookieName); err == nil && store.validate(cookie.Value) {
			next.ServeHTTP(w, r)
			return
		}
		// 方法限制写在这里而不是交给下游：分享链接只能用来取文件，带着 token
		// 发 POST 必须挡在鉴权层，不能让它流到上传逻辑门口再指望那边拦住
		if shares != nil && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
			if shares.validate(r.URL.Query().Get(shareQueryParam), r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}
		}
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	})
}

// setSessionCookie 把新 token 写成 Set-Cookie。四个安全标志缺一不可：
// HttpOnly 挡住 JS 读走它（XSS 兜底），SameSite=Strict 挡跨站请求带上它，
// Path=/ 保证整个应用共用同一份会话。
//
// Secure 这里无条件设置为 true，即使 r.TLS 是 nil——trynet 跑在 cloudflared
// 隧道后面，隧道在边缘把 TLS 解密之后再用明文转发给本地进程，本地这边永远
// 看不到 r.TLS，但用户浏览器访问的确实是 https://xxx.trycloudflare.com，
// 无条件设置才是对的。副作用：本地端口直接用 http 访问时登录状态存不住
// （浏览器不会把 Secure cookie 存到明文连接上），这是可以接受的边缘情况。
func setSessionCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(sessionTTL.Seconds()),
	})
}

// clearSessionCookie 让浏览器丢掉本地那份 cookie；服务端那份 token 由
// logoutHandler 另外调用 revoke 真正删除，两步缺一不可。
func clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
	})
}

// loginRequest 是 /auth/login 的请求体：登录弹窗用 fetch 发 JSON。
type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// loginHandler 校验凭据、发会话 cookie。失败路径统一延迟 loginFailDelay
// 再回 401，响应体和状态码不区分"用户名错"还是"密码错"——这两件事在
// credentialsMatch 里已经合并成一个布尔值，这里天然做不到区分。
func loginHandler(store *sessionStore, user, pass string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req loginRequest
		_ = json.NewDecoder(r.Body).Decode(&req) // 解析失败就留空字段，走跟凭据错误一样的失败路径，不额外分支

		if !credentialsMatch(req.Username, req.Password, user, pass) {
			time.Sleep(loginFailDelay) // 固定延迟，减缓在线暴力破解
			writeLoginError(w)
			return
		}

		token, err := store.create()
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		setSessionCookie(w, token)
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
}

func writeLoginError(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(`{"error":"Incorrect user name or password"}`))
}

// logoutHandler 让会话真正失效：先从存储里删掉 token（服务端主动失效，不是
// 只清浏览器那份 cookie），再让浏览器丢掉本地的 cookie。没有 cookie，或者
// token 本来就不存在，也照常返回 200——登出本来就该是幂等操作。
func logoutHandler(store *sessionStore) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if cookie, err := r.Cookie(sessionCookieName); err == nil {
			store.revoke(cookie.Value)
		}
		clearSessionCookie(w)
		w.WriteHeader(http.StatusOK)
	})
}
