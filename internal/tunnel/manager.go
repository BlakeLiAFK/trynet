package tunnel

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
)

const maxLogLines = 200

// Status 隧道运行状态
type Status struct {
	Running bool   `json:"running"`
	URL     string `json:"url"`
	Error   string `json:"error"`
	LastLog string `json:"lastLog"`
}

// runningTunnel 运行中的隧道实例
type runningTunnel struct {
	cmd         *exec.Cmd
	url         string
	logs        []string
	metricsAddr string // Prometheus metrics 地址 (host:port)
	manualStop  bool   // 是否手动停止
}

// Manager 隧道进程管理器
type Manager struct {
	binaryPath   string
	mu           sync.Mutex
	running      map[int64]*runningTunnel
	lastErrors   map[int64]string   // 持久化错误信息
	lastLogs     map[int64][]string // 持久化日志（进程退出后保留）
	OnTunnelExit func(id int64)     // 隧道非手动退出时的回调

	// 访问隧道（独立 map，避免 ID 与暴露隧道冲突）
	accessRunning    map[int64]*runningTunnel
	accessLastErrors map[int64]string
	accessLastLogs   map[int64][]string
	OnAccessExit     func(id int64) // 访问隧道非手动退出时的回调
}

// urlRegex 匹配 trycloudflare.com 域名
var urlRegex = regexp.MustCompile(`https://[a-zA-Z0-9-]+\.trycloudflare\.com`)

// connRegex 匹配 Named Tunnel 连接成功
var connRegex = regexp.MustCompile(`Registered tunnel connection`)

// errRegex 匹配 ERR 级别日志
var errRegex = regexp.MustCompile(`ERR\s+(.+)`)

// fatalRegex 匹配非日志格式的致命错误
var fatalRegex = regexp.MustCompile(`(?i)^(error|fatal|panic)\b`)

// logCleanRegex 去除日志中的时间戳前缀，只保留关键内容
var logCleanRegex = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T[\d:.]+Z\s+`)

// metricsAddrRegex 匹配 metrics 服务地址
var metricsAddrRegex = regexp.MustCompile(`Starting metrics server on ([^\s/]+)`)

// accessListenRegex 匹配访问隧道监听成功标志
var accessListenRegex = regexp.MustCompile(`Start Websocket listener`)

// New 创建隧道管理器
func New(binaryPath string) *Manager {
	return &Manager{
		binaryPath:       binaryPath,
		running:          make(map[int64]*runningTunnel),
		lastErrors:       make(map[int64]string),
		lastLogs:         make(map[int64][]string),
		accessRunning:    make(map[int64]*runningTunnel),
		accessLastErrors: make(map[int64]string),
		accessLastLogs:   make(map[int64][]string),
	}
}

// SetBinaryPath 设置 cloudflared 二进制路径
func (m *Manager) SetBinaryPath(path string) {
	m.binaryPath = path
}

// Start 启动隧道
// proxyURL 非空时自动强制 HTTP/2 协议并设置代理环境变量
// noTLSVerify 为 true 时传递 --no-tls-verify，跳过后端证书验证
func (m *Manager) Start(id int64, host string, port int, protocol, tunnelType, token, proxyURL string, noTLSVerify bool) error {
	m.mu.Lock()
	if _, ok := m.running[id]; ok {
		m.mu.Unlock()
		return fmt.Errorf("tunnel %d is already running", id)
	}
	delete(m.lastErrors, id)
	delete(m.lastLogs, id)
	m.mu.Unlock()

	var args []string
	args = append(args, "tunnel", "--metrics", "127.0.0.1:0")
	// 代理模式下强制使用 HTTP/2（QUIC 无法通过代理）
	if proxyURL != "" {
		args = append(args, "--protocol", "http2")
	}
	if tunnelType == "named" && token != "" {
		if noTLSVerify {
			args = append(args, "--no-tls-verify")
		}
		args = append(args, "run", "--token", token)
	} else {
		// Quick Tunnel 只支持 http/https，TCP 不被 trycloudflare.com 支持
		if protocol == "tcp" || protocol == "" {
			protocol = "http"
		}
		localURL := fmt.Sprintf("%s://%s:%d", protocol, host, port)
		// 用 /dev/null 作为 config，避免 ~/.cloudflared/config.yml 中的
		// ingress 规则（如 http_status:404）干扰 Quick Tunnel 路由
		args = append(args, "--config", "/dev/null")
		if noTLSVerify {
			args = append(args, "--no-tls-verify")
		}
		args = append(args, "--url", localURL)
	}
	cmd := exec.Command(m.binaryPath, args...)

	// 设置代理环境变量
	if proxyURL != "" {
		cmd.Env = append(os.Environ(),
			"HTTPS_PROXY="+proxyURL,
			"HTTP_PROXY="+proxyURL,
			"ALL_PROXY="+proxyURL,
		)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start cloudflared: %w", err)
	}

	rt := &runningTunnel{cmd: cmd}
	m.mu.Lock()
	m.running[id] = rt
	m.mu.Unlock()

	go func() {
		var lastErrors []string
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			line := scanner.Text()

			// 存储日志行
			m.mu.Lock()
			if t, ok := m.running[id]; ok {
				t.logs = append(t.logs, line)
				if len(t.logs) > maxLogLines {
					t.logs = t.logs[len(t.logs)-maxLogLines:]
				}
			}
			m.mu.Unlock()

			// 解析 metrics 服务地址
			if matches := metricsAddrRegex.FindStringSubmatch(line); len(matches) > 1 {
				m.mu.Lock()
				if t, ok := m.running[id]; ok {
					t.metricsAddr = matches[1]
				}
				m.mu.Unlock()
			}
			// Quick Tunnel: 匹配 trycloudflare.com URL
			if match := urlRegex.FindString(line); match != "" {
				m.mu.Lock()
				if t, ok := m.running[id]; ok {
					t.url = match
				}
				m.mu.Unlock()
			}
			// Named Tunnel: 检测连接成功
			if connRegex.MatchString(line) {
				m.mu.Lock()
				if t, ok := m.running[id]; ok && t.url == "" {
					t.url = "connected"
				}
				m.mu.Unlock()
			}
			// 收集错误信息
			if matches := errRegex.FindStringSubmatch(line); len(matches) > 1 {
				lastErrors = append(lastErrors, strings.TrimSpace(matches[1]))
			} else if fatalRegex.MatchString(line) {
				lastErrors = append(lastErrors, strings.TrimSpace(line))
			}
			if len(lastErrors) > 10 {
				lastErrors = lastErrors[len(lastErrors)-10:]
			}
		}
		// stderr 读完，持久化错误和日志
		m.mu.Lock()
		if len(lastErrors) > 0 {
			m.lastErrors[id] = strings.Join(lastErrors, "\n")
		}
		if t, ok := m.running[id]; ok {
			m.lastLogs[id] = t.logs
		}
		m.mu.Unlock()
	}()

	go func() {
		cmd.Wait()
		m.mu.Lock()
		wasManualStop := false
		if rt, ok := m.running[id]; ok {
			wasManualStop = rt.manualStop
		}
		delete(m.running, id)
		if _, hasErr := m.lastErrors[id]; !hasErr {
			m.lastErrors[id] = "process exited unexpectedly"
		}
		m.mu.Unlock()

		// 非手动停止时触发断连回调
		if !wasManualStop && m.OnTunnelExit != nil {
			m.OnTunnelExit(id)
		}
	}()

	return nil
}

// Stop 停止隧道
func (m *Manager) Stop(id int64) error {
	m.mu.Lock()
	rt, ok := m.running[id]
	delete(m.lastErrors, id)
	delete(m.lastLogs, id)
	if ok {
		rt.manualStop = true
	}
	m.mu.Unlock()
	if !ok {
		return nil
	}
	if rt.cmd.Process != nil {
		rt.cmd.Process.Kill()
	}
	return nil
}

// cleanLog 去除时间戳前缀
func cleanLog(line string) string {
	return logCleanRegex.ReplaceAllString(line, "")
}

// GetStatus 获取单个隧道状态
func (m *Manager) GetStatus(id int64) Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	if rt, ok := m.running[id]; ok {
		var lastLog string
		if len(rt.logs) > 0 {
			lastLog = cleanLog(rt.logs[len(rt.logs)-1])
		}
		return Status{Running: true, URL: rt.url, LastLog: lastLog}
	}
	if errMsg, ok := m.lastErrors[id]; ok {
		return Status{Running: false, Error: errMsg}
	}
	return Status{Running: false}
}

// GetAllStatuses 获取所有隧道状态
func (m *Manager) GetAllStatuses() map[int64]Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make(map[int64]Status)
	for id, rt := range m.running {
		var lastLog string
		if len(rt.logs) > 0 {
			lastLog = cleanLog(rt.logs[len(rt.logs)-1])
		}
		result[id] = Status{Running: true, URL: rt.url, LastLog: lastLog}
	}
	for id, errMsg := range m.lastErrors {
		if _, running := m.running[id]; !running {
			result[id] = Status{Running: false, Error: errMsg}
		}
	}
	return result
}

// GetLogs 获取隧道的完整日志
func (m *Manager) GetLogs(id int64) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if rt, ok := m.running[id]; ok {
		cp := make([]string, len(rt.logs))
		copy(cp, rt.logs)
		return cp
	}
	if logs, ok := m.lastLogs[id]; ok {
		cp := make([]string, len(logs))
		copy(cp, logs)
		return cp
	}
	return nil
}

// GetMetricsAddr 获取隧道的 metrics 地址
func (m *Manager) GetMetricsAddr(id int64) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if rt, ok := m.running[id]; ok {
		return rt.metricsAddr
	}
	return ""
}

// ClearError 清除指定隧道的错误信息
func (m *Manager) ClearError(id int64) {
	m.mu.Lock()
	delete(m.lastErrors, id)
	m.mu.Unlock()
}

// StopAll 停止所有隧道
func (m *Manager) StopAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, rt := range m.running {
		rt.manualStop = true
		if rt.cmd.Process != nil {
			rt.cmd.Process.Kill()
		}
	}
	for _, rt := range m.accessRunning {
		rt.manualStop = true
		if rt.cmd.Process != nil {
			rt.cmd.Process.Kill()
		}
	}
}

// StartAccess 启动访问隧道
// 执行: cloudflared access tcp --hostname {hostname} --url localhost:{port}
// 可选: --id {serviceTokenID} --secret {serviceTokenSecret}
// 代理: 设置 HTTPS_PROXY 等环境变量
func (m *Manager) StartAccess(id int64, hostname string, localPort int, serviceTokenID, serviceTokenSecret, proxyURL string) error {
	m.mu.Lock()
	if _, ok := m.accessRunning[id]; ok {
		m.mu.Unlock()
		return fmt.Errorf("access %d is already running", id)
	}
	delete(m.accessLastErrors, id)
	delete(m.accessLastLogs, id)
	m.mu.Unlock()

	localURL := fmt.Sprintf("localhost:%d", localPort)
	args := []string{"access", "tcp", "--hostname", hostname, "--url", localURL}
	if serviceTokenID != "" && serviceTokenSecret != "" {
		args = append(args, "--id", serviceTokenID, "--secret", serviceTokenSecret)
	}

	cmd := exec.Command(m.binaryPath, args...)
	if proxyURL != "" {
		cmd.Env = append(os.Environ(),
			"HTTPS_PROXY="+proxyURL,
			"HTTP_PROXY="+proxyURL,
			"ALL_PROXY="+proxyURL,
		)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start cloudflared access: %w", err)
	}

	rt := &runningTunnel{cmd: cmd}
	m.mu.Lock()
	m.accessRunning[id] = rt
	m.mu.Unlock()

	go func() {
		var lastErrors []string
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			line := scanner.Text()

			m.mu.Lock()
			if t, ok := m.accessRunning[id]; ok {
				t.logs = append(t.logs, line)
				if len(t.logs) > maxLogLines {
					t.logs = t.logs[len(t.logs)-maxLogLines:]
				}
				// 检测监听成功标志
				if accessListenRegex.MatchString(line) && t.url == "" {
					t.url = "connected"
				}
			}
			m.mu.Unlock()

			// 收集错误信息
			if matches := errRegex.FindStringSubmatch(line); len(matches) > 1 {
				lastErrors = append(lastErrors, strings.TrimSpace(matches[1]))
			} else if fatalRegex.MatchString(line) {
				lastErrors = append(lastErrors, strings.TrimSpace(line))
			}
			if len(lastErrors) > 10 {
				lastErrors = lastErrors[len(lastErrors)-10:]
			}
		}
		m.mu.Lock()
		if len(lastErrors) > 0 {
			m.accessLastErrors[id] = strings.Join(lastErrors, "\n")
		}
		if t, ok := m.accessRunning[id]; ok {
			m.accessLastLogs[id] = t.logs
		}
		m.mu.Unlock()
	}()

	go func() {
		cmd.Wait()
		m.mu.Lock()
		wasManualStop := false
		if rt, ok := m.accessRunning[id]; ok {
			wasManualStop = rt.manualStop
		}
		delete(m.accessRunning, id)
		if _, hasErr := m.accessLastErrors[id]; !hasErr {
			m.accessLastErrors[id] = "process exited unexpectedly"
		}
		m.mu.Unlock()

		if !wasManualStop && m.OnAccessExit != nil {
			m.OnAccessExit(id)
		}
	}()

	return nil
}

// StopAccess 停止访问隧道
func (m *Manager) StopAccess(id int64) error {
	m.mu.Lock()
	rt, ok := m.accessRunning[id]
	delete(m.accessLastErrors, id)
	delete(m.accessLastLogs, id)
	if ok {
		rt.manualStop = true
	}
	m.mu.Unlock()
	if !ok {
		return nil
	}
	if rt.cmd.Process != nil {
		rt.cmd.Process.Kill()
	}
	return nil
}

// GetAccessStatus 获取单个访问隧道状态
func (m *Manager) GetAccessStatus(id int64) Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	if rt, ok := m.accessRunning[id]; ok {
		var lastLog string
		if len(rt.logs) > 0 {
			lastLog = cleanLog(rt.logs[len(rt.logs)-1])
		}
		return Status{Running: true, URL: rt.url, LastLog: lastLog}
	}
	if errMsg, ok := m.accessLastErrors[id]; ok {
		return Status{Running: false, Error: errMsg}
	}
	return Status{Running: false}
}

// GetAllAccessStatuses 获取所有访问隧道状态
func (m *Manager) GetAllAccessStatuses() map[int64]Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make(map[int64]Status)
	for id, rt := range m.accessRunning {
		var lastLog string
		if len(rt.logs) > 0 {
			lastLog = cleanLog(rt.logs[len(rt.logs)-1])
		}
		result[id] = Status{Running: true, URL: rt.url, LastLog: lastLog}
	}
	for id, errMsg := range m.accessLastErrors {
		if _, running := m.accessRunning[id]; !running {
			result[id] = Status{Running: false, Error: errMsg}
		}
	}
	return result
}

// GetAccessLogs 获取访问隧道的完整日志
func (m *Manager) GetAccessLogs(id int64) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if rt, ok := m.accessRunning[id]; ok {
		cp := make([]string, len(rt.logs))
		copy(cp, rt.logs)
		return cp
	}
	if logs, ok := m.accessLastLogs[id]; ok {
		cp := make([]string, len(logs))
		copy(cp, logs)
		return cp
	}
	return nil
}
