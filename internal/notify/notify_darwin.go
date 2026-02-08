package notify

import (
	"fmt"
	"os/exec"
	"strings"
)

// Send 发送 macOS 系统通知
func Send(title, message string) error {
	script := fmt.Sprintf(
		`display notification "%s" with title "%s"`,
		escapeAS(message), escapeAS(title),
	)
	return exec.Command("/usr/bin/osascript", "-e", script).Run()
}

// escapeAS 转义 AppleScript 字符串中的特殊字符
func escapeAS(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return s
}
