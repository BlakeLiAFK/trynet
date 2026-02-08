package cfd

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Manager cloudflared 二进制管理器
type Manager struct {
	dataDir string
}

// New 创建管理器实例
func New(dataDir string) *Manager {
	binDir := filepath.Join(dataDir, "bin")
	os.MkdirAll(binDir, 0755)
	return &Manager{dataDir: dataDir}
}

// BinaryPath 返回 cloudflared 二进制路径
func (m *Manager) BinaryPath() string {
	name := "cloudflared"
	if runtime.GOOS == "windows" {
		name = "cloudflared.exe"
	}
	return filepath.Join(m.dataDir, "bin", name)
}

// IsInstalled 检查 cloudflared 是否已安装
func (m *Manager) IsInstalled() bool {
	info, err := os.Stat(m.BinaryPath())
	return err == nil && !info.IsDir()
}

// GetVersion 获取 cloudflared 版本号
func (m *Manager) GetVersion() string {
	if !m.IsInstalled() {
		return ""
	}
	out, err := exec.Command(m.BinaryPath(), "version").CombinedOutput()
	if err != nil {
		return ""
	}
	s := strings.TrimSpace(string(out))
	if idx := strings.Index(s, "version "); idx >= 0 {
		s = s[idx+8:]
		if idx2 := strings.Index(s, " "); idx2 >= 0 {
			s = s[:idx2]
		}
	}
	return s
}

// downloadURL 根据平台返回下载地址
func (m *Manager) downloadURL() string {
	goos := runtime.GOOS
	arch := runtime.GOARCH
	switch goos {
	case "darwin":
		return fmt.Sprintf("https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-darwin-%s.tgz", arch)
	case "linux":
		return fmt.Sprintf("https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-%s", arch)
	case "windows":
		return fmt.Sprintf("https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-windows-%s.exe", arch)
	}
	return ""
}

// Install 下载并安装 cloudflared
func (m *Manager) Install(onProgress func(string)) error {
	url := m.downloadURL()
	if url == "" {
		return fmt.Errorf("unsupported platform: %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	if onProgress != nil {
		onProgress("downloading")
	}
	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("download failed: HTTP %d", resp.StatusCode)
	}
	dest := m.BinaryPath()
	if strings.HasSuffix(url, ".tgz") {
		if onProgress != nil {
			onProgress("extracting")
		}
		if err := m.extractTgz(resp.Body, dest); err != nil {
			return err
		}
	} else {
		f, err := os.Create(dest)
		if err != nil {
			return err
		}
		if _, err := io.Copy(f, resp.Body); err != nil {
			f.Close()
			return err
		}
		f.Close()
	}
	if runtime.GOOS != "windows" {
		os.Chmod(dest, 0755)
	}
	if onProgress != nil {
		onProgress("done")
	}
	return nil
}

// GetLatestVersion 从 GitHub 获取最新版本号
func (m *Manager) GetLatestVersion() (string, error) {
	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Get("https://github.com/cloudflare/cloudflared/releases/latest")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	loc := resp.Header.Get("Location")
	if loc == "" {
		return "", fmt.Errorf("failed to get redirect URL")
	}
	parts := strings.Split(loc, "/")
	return parts[len(parts)-1], nil
}

// Update 更新 cloudflared 到最新版本（重新下载）
func (m *Manager) Update(onProgress func(string)) error {
	return m.Install(onProgress)
}

// extractTgz 从 tgz 压缩包中提取 cloudflared 二进制
func (m *Manager) extractTgz(r io.Reader, dest string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if header.Typeflag == tar.TypeReg && strings.Contains(header.Name, "cloudflared") {
			f, err := os.Create(dest)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(f, tr)
			f.Close()
			return copyErr
		}
	}
	return fmt.Errorf("cloudflared binary not found in archive")
}
