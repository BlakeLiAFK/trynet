//go:build !darwin

package autostart

import "fmt"

// Enable 非 macOS 平台暂不支持
func Enable() error {
	return fmt.Errorf("autostart not supported on this platform")
}

// Disable 非 macOS 平台暂不支持
func Disable() error {
	return nil
}

// IsEnabled 非 macOS 平台返回 false
func IsEnabled() bool {
	return false
}
