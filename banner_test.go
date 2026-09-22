package main

import (
	"bytes"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
)

func TestMaskSecret(t *testing.T) {
	cases := map[string]string{
		"":                 "",
		"short":            "*****",
		"12345678":         "********",
		"123456789":        "1234…6789",
		"AbCdEfGhIjKlMnOp": "AbCd…MnOp",
	}
	for in, want := range cases {
		if got := maskSecret(in); got != want {
			t.Errorf("maskSecret(%q) = %q, want %q", in, got, want)
		}
	}
}

// captureStdout 跑 f 的时候把 os.Stdout 换成一根管道，收集写出来的内容。
// banner.go 里的打印函数都是直接往 os.Stdout 写，这是验证它们输出内容的唯一办法。
func captureStdout(t *testing.T, f func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = old }()

	f()

	_ = w.Close()
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	return buf.String()
}

func TestPrintTokenInfoMissing(t *testing.T) {
	out := captureStdout(t, func() { printTokenInfo("", "") })
	if !strings.Contains(out, "none") || !strings.Contains(out, "CLOUDFLARE_API_TOKEN") {
		t.Errorf("missing-token banner line looks wrong: %q", out)
	}
}

func TestPrintTokenInfoSourceEnv(t *testing.T) {
	out := captureStdout(t, func() { printTokenInfo("AbCdEfGhIjKlMnOp", "CLOUDFLARE_API_TOKEN") })
	if !strings.Contains(out, "AbCd…MnOp") {
		t.Errorf("token should be masked in output: %q", out)
	}
	if strings.Contains(out, "AbCdEfGhIjKlMnOp") {
		t.Errorf("full token must never be printed: %q", out)
	}
	if !strings.Contains(out, "CLOUDFLARE_API_TOKEN") {
		t.Errorf("source should be in output: %q", out)
	}
}

func TestPrintTokenInfoSourceFlag(t *testing.T) {
	out := captureStdout(t, func() { printTokenInfo("AbCdEfGhIjKlMnOp", "-api-token") })
	if !strings.Contains(out, "AbCd…MnOp") {
		t.Errorf("token should be masked in output: %q", out)
	}
	if strings.Contains(out, "AbCdEfGhIjKlMnOp") {
		t.Errorf("full token must never be printed: %q", out)
	}
	if !strings.Contains(out, "-api-token") {
		t.Errorf("source should be in output: %q", out)
	}
}

// printProxyInfo 现在只报状态，不该再带"去改配置"的提示措辞——
// 程序会自动在多条边缘路径之间回退，启动时打这种提示纯属误导
func TestPrintProxyInfoHasNoHint(t *testing.T) {
	clearProxyEnv(t)
	t.Setenv("HTTPS_PROXY", "http://192.168.3.181:2080")

	out := captureStdout(t, printProxyInfo)
	if !strings.Contains(out, "192.168.3.181:2080") {
		t.Errorf("状态行里应该有代理值: %q", out)
	}
	if strings.Contains(out, "try socks5") || strings.Contains(out, "unreachable") {
		t.Errorf("printProxyInfo 不该再带提示措辞: %q", out)
	}
}

func TestPrintProxyInfoNoProxyHasNoHint(t *testing.T) {
	clearProxyEnv(t)

	out := captureStdout(t, printProxyInfo)
	if !strings.Contains(out, "none") {
		t.Errorf("没代理时应该显示 none: %q", out)
	}
	if strings.Contains(out, "try socks5") || strings.Contains(out, "unreachable") {
		t.Errorf("printProxyInfo 不该再带提示措辞: %q", out)
	}
}

// 没配代理时，printProxyHint 应该提示设一个再跑
func TestPrintProxyHintNoProxy(t *testing.T) {
	clearProxyEnv(t)
	proxyHintOnce = &sync.Once{}

	out := captureStdout(t, printProxyHint)
	if !strings.Contains(out, "none") {
		t.Errorf("应该先报一遍代理状态（没配）: %q", out)
	}
	if !strings.Contains(out, "unreachable") {
		t.Errorf("没代理时应提示设代理: %q", out)
	}
	if !strings.Contains(out, "socks5://127.0.0.1:1080") {
		t.Errorf("应该给出默认 socks5 建议命令: %q", out)
	}
}

// 配了 http 代理还失败时，printProxyHint 应该建议换 socks5
func TestPrintProxyHintHTTPProxy(t *testing.T) {
	clearProxyEnv(t)
	t.Setenv("HTTPS_PROXY", "http://192.168.3.181:2080")
	proxyHintOnce = &sync.Once{}

	out := captureStdout(t, printProxyHint)
	if !strings.Contains(out, "192.168.3.181:2080") || !strings.Contains(out, "HTTPS_PROXY") {
		t.Errorf("应该先报一遍当前代理的值和来源: %q", out)
	}
	if !strings.Contains(out, "try socks5") {
		t.Errorf("http 代理应建议换 socks5: %q", out)
	}
	if !strings.Contains(out, "socks5://127.0.0.1:1080") {
		t.Errorf("没有备用 socks5 时应给默认建议: %q", out)
	}
}

// 已经是 socks5 还失败，说明换代理这条建议没用了，但代理状态本身还是要报——
// 启动时不再打印它，失败时这是用户唯一能看到"我配的代理是什么"的地方
func TestPrintProxyHintSocks5AlreadyOnlyShowsStatus(t *testing.T) {
	clearProxyEnv(t)
	t.Setenv("HTTPS_PROXY", "socks5://192.168.3.181:1080")
	proxyHintOnce = &sync.Once{}

	out := captureStdout(t, printProxyHint)
	if !strings.Contains(out, "socks5://192.168.3.181:1080") || !strings.Contains(out, "HTTPS_PROXY") {
		t.Errorf("应该报出当前代理的值和来源: %q", out)
	}
	if strings.Contains(out, "try socks5") {
		t.Errorf("已经是 socks5 了，不该再建议换 socks5: %q", out)
	}
}

// printServing 在 -dir 模式下应该显示目录路径，并带上路由标签
func TestPrintServingDirMode(t *testing.T) {
	out := captureStdout(t, func() { printServing("serving C:\\Users\\blake\\tmp\\trynet", "direct") })
	if !strings.Contains(out, "serving C:\\Users\\blake\\tmp\\trynet") {
		t.Errorf("应该显示目录绝对路径: %q", out)
	}
	if !strings.Contains(out, "via direct") {
		t.Errorf("应该带上路由标签: %q", out)
	}
}

// printServing 在 -port 模式下应该显示转发地址，走了代理时标签要带上代理值
func TestPrintServingPortModeWithProxy(t *testing.T) {
	out := captureStdout(t, func() {
		printServing("forwarding to 127.0.0.1:8080", "proxy http://192.168.3.181:2080")
	})
	if !strings.Contains(out, "forwarding to 127.0.0.1:8080") {
		t.Errorf("应该显示转发地址: %q", out)
	}
	if !strings.Contains(out, "via proxy http://192.168.3.181:2080") {
		t.Errorf("应该带上代理路由标签: %q", out)
	}
}

func TestRunWithProxyCmd(t *testing.T) {
	got := runWithProxyCmd("socks5://h:1080")
	if !strings.Contains(got, "socks5://h:1080") {
		t.Errorf("命令里没带上代理值: %q", got)
	}
}

// printProxyHint 整个进程最多打一次：重连反复失败也不能每次都刷一遍
func TestPrintProxyHintOnlyOnce(t *testing.T) {
	clearProxyEnv(t)
	t.Setenv("HTTPS_PROXY", "http://192.168.3.181:2080")
	proxyHintOnce = &sync.Once{}

	first := captureStdout(t, printProxyHint)
	if first == "" {
		t.Fatal("第一次调用应该有输出")
	}
	second := captureStdout(t, printProxyHint)
	if second != "" {
		t.Errorf("第二次调用不该再有输出: %q", second)
	}
}

// clearProxyEnv 清空所有代理环境变量，让每个用例从确定的状态开始。
//
// 和 internal/tunnel 的测试里那个同名 helper 是两份一样的代码。Go 的测试
// helper 不跨包共享，要共享就得把它挪进非测试文件再导出——为了五行代码
// 把测试脚手架塞进生产代码，不划算。
func clearProxyEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"http_proxy", "HTTP_PROXY", "https_proxy", "HTTPS_PROXY", "no_proxy", "NO_PROXY", "ALL_PROXY", "all_proxy"} {
		t.Setenv(k, "")
	}
}
