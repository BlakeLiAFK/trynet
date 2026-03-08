package main

import (
	"context"
	"fmt"

	"trynet/internal/autostart"
	"trynet/internal/scan"
	"trynet/internal/cfd"
	"trynet/internal/db"
	"trynet/internal/notify"
	"trynet/internal/tray"
	"trynet/internal/tunnel"

	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// App 应用主结构
type App struct {
	ctx       context.Context
	db        *db.DB
	cfd       *cfd.Manager
	tunnels   *tunnel.Manager
	forceQuit bool
}

// NewApp 创建应用实例
func NewApp() *App {
	return &App{}
}

// startup 应用启动时初始化
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	database, err := db.New()
	if err != nil {
		wailsRuntime.LogFatal(ctx, "database init failed: "+err.Error())
		return
	}
	a.db = database
	dataDir := db.DataDir()
	a.cfd = cfd.New(dataDir)
	a.tunnels = tunnel.New(a.cfd.BinaryPath())

	// 注册访问隧道断连回调
	a.tunnels.OnAccessExit = func(id int64) {
		if a.db.GetSetting("notify_disconnect") != "true" {
			return
		}
		name := fmt.Sprintf("Access #%d", id)
		list, err := a.db.ListAccesses()
		if err == nil {
			for _, acc := range list {
				if acc.ID == id {
					name = acc.Name
					break
				}
			}
		}
		notify.Send("TryNet", name+" disconnected")
	}

	// 注册隧道断连回调
	a.tunnels.OnTunnelExit = func(id int64) {
		// 检查用户是否启用了断连通知
		if a.db.GetSetting("notify_disconnect") != "true" {
			return
		}
		// 查询隧道名称
		name := fmt.Sprintf("Tunnel #%d", id)
		list, err := a.db.ListTunnels()
		if err == nil {
			for _, t := range list {
				if t.ID == id {
					name = t.Name
					break
				}
			}
		}
		notify.Send("TryNet", name+" disconnected")
	}

	// 初始化系统托盘
	tray.Init(tray.Callbacks{
		OnShow: func() {
			wailsRuntime.WindowShow(ctx)
		},
		OnQuit: func() {
			a.forceQuit = true
			wailsRuntime.Quit(ctx)
		},
	})

	// 自动启动标记了 auto_start 的隧道
	go a.autoStartTunnels()
}

// autoStartTunnels 启动所有标记了自启动的隧道和访问隧道
func (a *App) autoStartTunnels() {
	if !a.cfd.IsInstalled() {
		return
	}
	proxyURL := ""
	if a.db.GetSetting("proxy_enabled") == "true" {
		proxyURL = a.db.GetSetting("proxy_url")
	}
	// 启动暴露服务隧道
	list, err := a.db.ListTunnels()
	if err == nil {
		for _, t := range list {
			if t.AutoStart {
				a.tunnels.Start(t.ID, t.LocalHost, t.LocalPort, t.Protocol, t.TunnelType, t.Token, proxyURL, t.NoTLSVerify)
			}
		}
	}
	// 启动访问隧道
	accesses, err := a.db.ListAccesses()
	if err == nil {
		for _, acc := range accesses {
			if acc.AutoStart {
				a.tunnels.StartAccess(acc.ID, acc.Hostname, acc.LocalPort, acc.ServiceTokenID, acc.ServiceTokenSecret, proxyURL)
			}
		}
	}
}

// beforeClose 窗口关闭时隐藏到托盘而非退出
func (a *App) beforeClose(ctx context.Context) bool {
	if a.forceQuit {
		return false // 允许真正退出
	}
	wailsRuntime.WindowHide(ctx)
	return true // 阻止关闭，仅隐藏窗口
}

// shutdown 应用关闭时清理资源
func (a *App) shutdown(ctx context.Context) {
	if a.tunnels != nil {
		a.tunnels.StopAll()
	}
	if a.db != nil {
		a.db.Close()
	}
}

// IsCloudflaredInstalled 检查 cloudflared 是否已安装
func (a *App) IsCloudflaredInstalled() bool {
	return a.cfd.IsInstalled()
}

// GetCloudflaredVersion 获取 cloudflared 版本
func (a *App) GetCloudflaredVersion() string {
	return a.cfd.GetVersion()
}

// InstallCloudflared 安装 cloudflared
func (a *App) InstallCloudflared() error {
	return a.cfd.Install(func(status string) {
		wailsRuntime.EventsEmit(a.ctx, "install-progress", status)
	})
}

// GetTunnels 获取所有隧道配置
func (a *App) GetTunnels() ([]db.Tunnel, error) {
	return a.db.ListTunnels()
}

// CreateTunnel 创建隧道配置
func (a *App) CreateTunnel(name, host string, port int, protocol, tunnelType, token, customDomain string, autoStart, noTLSVerify bool) (*db.Tunnel, error) {
	return a.db.CreateTunnel(name, host, port, protocol, tunnelType, token, customDomain, autoStart, noTLSVerify)
}

// UpdateTunnel 更新隧道配置
func (a *App) UpdateTunnel(id int64, name, host string, port int, protocol, tunnelType, token, customDomain string, autoStart, noTLSVerify bool) error {
	return a.db.UpdateTunnel(id, name, host, port, protocol, tunnelType, token, customDomain, autoStart, noTLSVerify)
}

// DeleteTunnel 删除隧道配置
func (a *App) DeleteTunnel(id int64) error {
	a.tunnels.Stop(id)
	return a.db.DeleteTunnel(id)
}

// StartTunnel 启动隧道
func (a *App) StartTunnel(id int64) error {
	list, err := a.db.ListTunnels()
	if err != nil {
		return err
	}
	// 读取代理配置
	proxyURL := ""
	if a.db.GetSetting("proxy_enabled") == "true" {
		proxyURL = a.db.GetSetting("proxy_url")
	}
	for _, t := range list {
		if t.ID == id {
			return a.tunnels.Start(id, t.LocalHost, t.LocalPort, t.Protocol, t.TunnelType, t.Token, proxyURL, t.NoTLSVerify)
		}
	}
	return fmt.Errorf("tunnel not found: %d", id)
}

// CheckCloudflaredUpdate 检查 cloudflared 更新
func (a *App) CheckCloudflaredUpdate() (map[string]string, error) {
	current := a.cfd.GetVersion()
	latest, err := a.cfd.GetLatestVersion()
	if err != nil {
		return nil, err
	}
	result := map[string]string{
		"current":    current,
		"latest":     latest,
		"needUpdate": "false",
	}
	if current != latest {
		result["needUpdate"] = "true"
	}
	return result, nil
}

// UpdateCloudflared 更新 cloudflared 到最新版本
func (a *App) UpdateCloudflared() error {
	return a.cfd.Update(func(status string) {
		wailsRuntime.EventsEmit(a.ctx, "install-progress", status)
	})
}

// StopTunnel 停止隧道
func (a *App) StopTunnel(id int64) error {
	return a.tunnels.Stop(id)
}

// TunnelStatus 隧道状态
type TunnelStatus struct {
	Running bool   `json:"running"`
	URL     string `json:"url"`
	Error   string `json:"error"`
	LastLog string `json:"lastLog"`
}

// GetTunnelStatus 获取单个隧道状态
func (a *App) GetTunnelStatus(id int64) TunnelStatus {
	s := a.tunnels.GetStatus(id)
	return TunnelStatus{Running: s.Running, URL: s.URL, Error: s.Error, LastLog: s.LastLog}
}

// GetAllStatuses 获取所有隧道状态
func (a *App) GetAllStatuses() map[int64]TunnelStatus {
	raw := a.tunnels.GetAllStatuses()
	result := make(map[int64]TunnelStatus)
	for id, s := range raw {
		result[id] = TunnelStatus{Running: s.Running, URL: s.URL, Error: s.Error, LastLog: s.LastLog}
	}
	return result
}

// GetTunnelLogs 获取隧道完整日志
func (a *App) GetTunnelLogs(id int64) []string {
	return a.tunnels.GetLogs(id)
}

// GetTunnelMetrics 获取隧道运行指标
func (a *App) GetTunnelMetrics(id int64) *tunnel.Metrics {
	addr := a.tunnels.GetMetricsAddr(id)
	if addr == "" {
		return nil
	}
	m, err := tunnel.FetchMetrics(addr)
	if err != nil {
		return nil
	}
	return m
}

// OpenURL 用系统浏览器打开链接
func (a *App) OpenURL(url string) {
	wailsRuntime.BrowserOpenURL(a.ctx, url)
}

// GetSetting 获取设置项
func (a *App) GetSetting(key string) string {
	return a.db.GetSetting(key)
}

// SetSetting 保存设置项
func (a *App) SetSetting(key, value string) error {
	return a.db.SetSetting(key, value)
}

// SetAutoStart 设置开机自启动
func (a *App) SetAutoStart(enabled bool) error {
	if enabled {
		return autostart.Enable()
	}
	return autostart.Disable()
}

// IsAutoStartEnabled 检查是否已设置开机自启动
func (a *App) IsAutoStartEnabled() bool {
	return autostart.IsEnabled()
}

// ScanResult 局域网扫描结果
type ScanResult struct {
	IP      string `json:"ip"`
	Port    int    `json:"port"`
	Proto   string `json:"proto"`
	Latency int64  `json:"latency"`
}

// AccessStatus 访问隧道状态
type AccessStatus struct {
	Running bool   `json:"running"`
	Error   string `json:"error"`
	LastLog string `json:"lastLog"`
}

// GetAccesses 获取所有访问隧道配置
func (a *App) GetAccesses() ([]db.Access, error) {
	return a.db.ListAccesses()
}

// CreateAccess 创建访问隧道配置
func (a *App) CreateAccess(name, hostname string, localPort int, serviceTokenID, serviceTokenSecret string, autoStart bool) (*db.Access, error) {
	return a.db.CreateAccess(name, hostname, localPort, serviceTokenID, serviceTokenSecret, autoStart)
}

// UpdateAccess 更新访问隧道配置
func (a *App) UpdateAccess(id int64, name, hostname string, localPort int, serviceTokenID, serviceTokenSecret string, autoStart bool) error {
	return a.db.UpdateAccess(id, name, hostname, localPort, serviceTokenID, serviceTokenSecret, autoStart)
}

// DeleteAccess 删除访问隧道配置
func (a *App) DeleteAccess(id int64) error {
	a.tunnels.StopAccess(id)
	return a.db.DeleteAccess(id)
}

// StartAccess 启动访问隧道
func (a *App) StartAccess(id int64) error {
	list, err := a.db.ListAccesses()
	if err != nil {
		return err
	}
	proxyURL := ""
	if a.db.GetSetting("proxy_enabled") == "true" {
		proxyURL = a.db.GetSetting("proxy_url")
	}
	for _, acc := range list {
		if acc.ID == id {
			return a.tunnels.StartAccess(id, acc.Hostname, acc.LocalPort, acc.ServiceTokenID, acc.ServiceTokenSecret, proxyURL)
		}
	}
	return fmt.Errorf("access not found: %d", id)
}

// StopAccess 停止访问隧道
func (a *App) StopAccess(id int64) error {
	return a.tunnels.StopAccess(id)
}

// GetAccessStatus 获取单个访问隧道状态
func (a *App) GetAccessStatus(id int64) AccessStatus {
	s := a.tunnels.GetAccessStatus(id)
	return AccessStatus{Running: s.Running, Error: s.Error, LastLog: s.LastLog}
}

// GetAllAccessStatuses 获取所有访问隧道状态
func (a *App) GetAllAccessStatuses() map[int64]AccessStatus {
	raw := a.tunnels.GetAllAccessStatuses()
	result := make(map[int64]AccessStatus)
	for id, s := range raw {
		result[id] = AccessStatus{Running: s.Running, Error: s.Error, LastLog: s.LastLog}
	}
	return result
}

// GetAccessLogs 获取访问隧道完整日志
func (a *App) GetAccessLogs(id int64) []string {
	return a.tunnels.GetAccessLogs(id)
}

// ScanLAN 扫描局域网内可用的 HTTP/HTTPS 服务
func (a *App) ScanLAN(subnetBits int) []ScanResult {
	raw := scan.Scan(subnetBits, func(scanned, total int) {
		wailsRuntime.EventsEmit(a.ctx, "scan-progress", scanned, total)
	})
	var results []ScanResult
	for _, r := range raw {
		results = append(results, ScanResult{
			IP:      r.IP,
			Port:    r.Port,
			Proto:   r.Proto,
			Latency: r.Latency,
		})
	}
	if results == nil {
		results = []ScanResult{}
	}
	return results
}
