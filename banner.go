// banner.go 只管终端输出的排版：启动 logo、隧道就绪后的状态框、
// 代理/token 信息怎么显示。代理探测本身的逻辑在 internal/tunnel 里，
// 这里只负责把 tunnel.ProxyStatus() 报出来的值打印成用户能看懂的样子。
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"trynet/internal/buildinfo"
	"trynet/internal/tunnel"
)

var version = buildinfo.Version

// slant 字体的 trynet logo，注意里面全是 / \ _ .，没有反引号，可以用原始字符串
const logo = `   __                        __
  / /________  ______  ___  / /_
 / __/ ___/ / / / __ \/ _ \/ __/
/ /_/ /  / /_/ / / / /  __/ /_
\__/_/   \__, /_/ /_/\___/\__/
        /____/`

// ANSI 颜色。橙色用 256 色的 208，呼应图标；NO_COLOR 或非终端时全部降级为空串
var (
	noColor = os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb"

	orange = colorize("\033[38;5;208m")
	green  = colorize("\033[32m")
	yellow = colorize("\033[33m")
	dim    = colorize("\033[2m")
	bold   = colorize("\033[1m")
	reset  = colorize("\033[0m")
)

func colorize(code string) string {
	if noColor {
		return ""
	}
	return code
}

// banner 打印 logo 和一行副标题
func banner() {
	fmt.Println()
	fmt.Println(orange + logo + reset)
	fmt.Printf("  %smini cloudflare tunnel%s  %sv%s%s\n\n", dim, reset, dim, version, reset)
}

// printProxyInfo 只显示当前生效的代理状态，不带任何"你可能配错了"的提示。
// 程序会在多条边缘路径之间自动回退，成功连上时打印这行纯属误导——明明能连上，
// 却让用户以为出了问题要去改配置，所以启动流程不再调用它。
// 只在 printProxyHint 里、所有路径都失败之后才用它把"我配的代理是什么"报出来。
func printProxyInfo() {
	st := tunnel.ProxyStatus()
	value, source := st.Value, st.Source
	if value == "" {
		fmt.Printf("  %sproxy%s   %snone — direct connection%s\n\n", bold, reset, yellow, reset)
		return
	}
	fmt.Printf("  %sproxy%s   %s%s%s  %s(from %s)%s\n\n", bold, reset, green, value, reset, dim, source, reset)
}

// proxyHintOnce 保证 printProxyHint 整个进程最多打一次：重连失败会反复触发，
// 没有这个每次都刷一遍会把日志刷屏。测试里可以重新赋值一个新的 sync.Once
// 来让各用例互不影响。
var proxyHintOnce = &sync.Once{}

// printProxyHint 只在所有边缘路径都连不上时调用，打在错误日志之后。
// 启动时不再打印代理状态了，所以这里先把"我配的代理是什么"报一遍，
// 再给一条"接下来可以怎么办"的建议：
//   - 没配代理：提示设一个再跑
//   - 配了但不是 socks5：http 代理常连不上边缘 7844，建议换 socks5
//   - 已经是 socks5 还失败：后面两条建议都没用，只留代理状态那行
func printProxyHint() {
	proxyHintOnce.Do(printProxyHintNow)
}

func printProxyHintNow() {
	printProxyInfo()

	st := tunnel.ProxyStatus()
	if st.Value == "" {
		fmt.Printf("  %sIf the edge is unreachable, set a proxy and rerun:%s\n", dim, reset)
		fmt.Printf("    %s%s%s\n\n", green, runWithProxyCmd("socks5://127.0.0.1:1080"), reset)
		return
	}

	if st.IsSocks {
		return
	}
	suggest := st.Suggest
	if suggest == "" {
		suggest = "socks5://127.0.0.1:1080"
	}
	fmt.Printf("  %s! http proxies often can't reach the edge (port 7844); try socks5:%s\n", yellow, reset)
	fmt.Printf("    %s%s%s\n\n", green, runWithProxyCmd(suggest), reset)
}

// runWithProxyCmd 按当前平台拼出"设代理 + 运行"的一行命令，方便复制
func runWithProxyCmd(proxy string) string {
	bin := filepath.Base(os.Args[0])
	if runtime.GOOS == "windows" {
		return fmt.Sprintf(`$env:HTTPS_PROXY="%s"; .\%s`, proxy, bin)
	}
	return fmt.Sprintf("HTTPS_PROXY=%s ./%s", proxy, bin)
}

// printReady 高亮打印隧道就绪后的公网地址，方便直接复制。
// 主机名是纯 ASCII，直接按字节数算框宽即可，不用担心中文宽度。
// 前后都不留空行——banner() 已经空过一行了，框后面紧接着的 printServing
// 才是真正的收尾，不需要再各自留白。
func printReady(location, hostname string) {
	url := "https://" + hostname
	inner := len(url) + 2 // 左右各留一个空格
	fmt.Printf("  %s✓ tunnel ready%s  %s[%s]%s\n", green+bold, reset, dim, location, reset)
	fmt.Printf("  %s┌%s┐%s\n", dim, strings.Repeat("─", inner), reset)
	fmt.Printf("  %s│%s %s%s%s %s│%s\n", dim, reset, bold+orange, url, reset, dim, reset)
	fmt.Printf("  %s└%s┘%s\n", dim, strings.Repeat("─", inner), reset)
}

// printServing 紧跟在 printReady 的框后面打一行，回答两个问题："分享的是哪个
// 目录/端口"和"这次走的是哪条边缘路径"——原来分散在两条带时间戳的日志里，
// 现在折进这一行；结尾的空行代替了原来 printReady 框后面的那一行。
// target 由调用方按 -dir/-port 模式预先拼好（"serving <path>" 或 "forwarding to host:port"）。
func printServing(target, route string) {
	fmt.Printf("  %s%s  ·  via %s%s\n\n", dim, target, route, reset)
}

// fileServiceStatusLine 拼一行登录信息，跟在 printServing 后面打："login user / pass"，
// upload 开启时在同一行后面追加一个标记，保持"行就行不啰嗦"的风格。
func fileServiceStatusLine(user, pass string, upload bool) string {
	line := fmt.Sprintf("  %slogin%s  %s / %s", bold, reset, user, pass)
	if upload {
		line += fmt.Sprintf("  %s·%s  upload enabled", dim, reset)
	}
	return line
}

// uploadOnlyStatusLine 是 -public 时（没有登录信息可贴）单独标一行 upload 状态用的。
// -public 和 -upload 同时开着是这个工具能摆出的最开放的姿势：公网上任何拿到
// 链接的人都能往这个目录里写东西。这一行必须说得比"upload enabled"重，
// 不然它看上去跟有鉴权时的那行一模一样。
func uploadOnlyStatusLine() string {
	return fmt.Sprintf("  %spublic%s  anyone with the url can read %sand upload%s",
		bold, reset, bold, reset)
}

// printStoreHint 只在这次启动新建了快速隧道凭据存档时打一行提示，紧跟在
// printServing 后面。复用已有存档时不再重复说——见 tunnel.StoreFileName 的调用方。
func printStoreHint() {
	fmt.Printf("  %scredentials saved to%s %s%s%s %s— restart reuses them, add it to .gitignore%s\n\n",
		bold, reset, green, tunnel.StoreFileName, reset, dim, reset)
}

// printTokenInfo 打印脱敏后的 API token 来源，跟上面 printProxyInfo 是同一视觉风格，
// 只在 -domain 模式下调用，给用户一个"读到的是哪个值"的锚点。
// 调用方负责告诉我们 token 的实际来源（-api-token 或环境变量名），避免函数内部猜测。
func printTokenInfo(token, source string) {
	if token == "" {
		fmt.Printf("  %scf token%s  %snone%s — set %sCLOUDFLARE_API_TOKEN%s (or -api-token) to use -domain\n\n",
			bold, reset, yellow, reset, green, reset)
		return
	}
	fmt.Printf("  %scf token%s  %s%s%s  %s(from %s)%s\n\n",
		bold, reset, green, maskSecret(token), reset, dim, source, reset)
}

// maskSecret 只在展示时用：8 位以内全部打星号，更长的留头尾各 4 位、中间用 … 隐藏。
// 不追求安全强度（前后各 4 位不影响一个高熵随机串的安全性），纯粹给用户一个
// "是不是我以为的那个值"的视觉锚点。
func maskSecret(s string) string {
	if len(s) <= 8 {
		return strings.Repeat("*", len(s))
	}
	return s[:4] + "…" + s[len(s)-4:]
}
