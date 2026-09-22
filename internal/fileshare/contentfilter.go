// contentfilter.go 收拢"这个名字该不该被挡住"的判断，以及套在 /data 上的
// 过滤 handler。fileserver.go 里的 renderDir/serveUI（挡目录列表条目）和
// safeUploadName（挡上传文件名）都复用这里的 isFilteredName，保证读、写、
// 列表三处口径完全一致。
package fileshare

import (
	"net/http"
	"path"
	"path/filepath"
	"strings"
)

// sensitiveNames 是按名字整体匹配的敏感目录/文件名单，抄自项目计划文档，
// 不自行增减。点文件规则（任何以 "." 开头的路径段）挡不住这里非点开头的那批——
// id_rsa、credentials 这些是真正的漏网之鱼。
var sensitiveNames = map[string]struct{}{
	".git": {}, ".svn": {}, ".hg": {}, ".ssh": {}, ".aws": {}, ".gnupg": {}, ".kube": {}, ".docker": {},
	".env": {}, ".netrc": {}, ".npmrc": {}, ".pypirc": {}, ".htpasswd": {}, ".trynet.json": {},
	"id_rsa": {}, "id_dsa": {}, "id_ecdsa": {}, "id_ed25519": {}, "credentials": {}, "secring.gpg": {},
}

// sensitiveExts 是按扩展名匹配的敏感名单，看的是最后一个后缀
// （"key.pem" 命中，"pem.txt" 不命中）。
var sensitiveExts = map[string]struct{}{
	".pem": {}, ".key": {}, ".pfx": {}, ".p12": {}, ".jks": {}, ".keystore": {}, ".kdbx": {}, ".ppk": {},
}

// isSensitiveName 判断单个文件/目录名是否命中敏感名单，不分大小写。
func isSensitiveName(name string) bool {
	lower := strings.ToLower(name)
	if _, ok := sensitiveNames[lower]; ok {
		return true
	}
	_, ok := sensitiveExts[strings.ToLower(filepath.Ext(name))]
	return ok
}

// isFilteredName 判断一个名字该不该被内容过滤挡掉：点开头，或者命中敏感名单。
// blockSensitive（挡请求）和 renderDir/serveUI（挡目录列表里的条目）共用这一份
// 判断，保证"列得出来的"和"点得开的"永远是同一批东西，不会出现点开就 404 的死链接。
func isFilteredName(name string) bool {
	return strings.HasPrefix(name, ".") || isSensitiveName(name)
}

// blockSensitive 包一层 handler，挡掉路径里任何一段命中点文件规则或敏感名单的
// 请求，返回 404。只套在 /data 上（见 Start），不套 /ui——套在 /ui 上
// 的话，下一批 /ui 自己的静态资源会被误伤。
//
// r.URL.Path 是 net/http 已经解码过的路径（%2e 之类会先变回 .），这里再用
// path.Clean 规范化一遍去掉 . 和 .. 之后逐段判断，不能只做字符串前缀匹配。
func blockSensitive(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clean := path.Clean(r.URL.Path)
		for _, seg := range strings.Split(clean, "/") {
			if isFilteredName(seg) {
				http.NotFound(w, r)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
