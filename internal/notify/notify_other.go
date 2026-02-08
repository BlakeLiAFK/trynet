//go:build !darwin

package notify

// Send 非 macOS 平台暂不支持通知
func Send(title, message string) error {
	return nil
}
