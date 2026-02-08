package autostart

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// appBundlePath 获取当前应用的 .app 路径
// Wails 打包后结构: TryNet.app/Contents/MacOS/trynet
func appBundlePath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return "", err
	}
	// 向上找到 .app 目录
	dir := exe
	for i := 0; i < 5; i++ {
		dir = filepath.Dir(dir)
		if strings.HasSuffix(dir, ".app") {
			return dir, nil
		}
	}
	return "", fmt.Errorf("not running from .app bundle: %s", exe)
}

// Enable 添加到 macOS Login Items
func Enable() error {
	appPath, err := appBundlePath()
	if err != nil {
		return err
	}
	script := fmt.Sprintf(
		`tell application "System Events" to make login item at end with properties {path:"%s", hidden:false}`,
		escapeAS(appPath),
	)
	return exec.Command("/usr/bin/osascript", "-e", script).Run()
}

// Disable 从 macOS Login Items 移除
func Disable() error {
	appPath, err := appBundlePath()
	if err != nil {
		return err
	}
	appName := strings.TrimSuffix(filepath.Base(appPath), ".app")
	script := fmt.Sprintf(
		`tell application "System Events" to delete login item "%s"`,
		escapeAS(appName),
	)
	// 不存在时忽略错误
	exec.Command("/usr/bin/osascript", "-e", script).Run()
	return nil
}

// IsEnabled 检查是否已添加到 Login Items
func IsEnabled() bool {
	appPath, err := appBundlePath()
	if err != nil {
		return false
	}
	appName := strings.TrimSuffix(filepath.Base(appPath), ".app")
	script := `tell application "System Events" to get the name of every login item`
	out, err := exec.Command("/usr/bin/osascript", "-e", script).Output()
	if err != nil {
		return false
	}
	// osascript 返回逗号分隔列表: "item1, item2, item3"
	for _, item := range strings.Split(string(out), ",") {
		if strings.TrimSpace(item) == appName {
			return true
		}
	}
	return false
}

// escapeAS 转义 AppleScript 字符串
func escapeAS(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return s
}
