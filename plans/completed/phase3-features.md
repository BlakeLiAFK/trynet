# Phase 3: 新功能批量实现

> 创建: 2026-02-08
> 状态: 已完成

## 目标

实现4个新功能: 开机自启动、隧道断连通知、隧道搜索/排序、流量/连接统计

---

## 功能1: 开机自启动应用

### 技术方案
- macOS: 使用 `osascript` 调用 AppleScript System Events 管理 Login Items
- 对应 Wails v3 官方选用的方案，纯 Go，无 CGO
- 命令: `tell application "System Events" to make login item at end with properties {path:"/path/to/App.app", hidden:false}`

### 实现步骤
- [x] 新建 `internal/autostart/autostart_darwin.go`
  - `Enable(appPath)` 添加到 Login Items
  - `Disable(appName)` 移除
  - `IsEnabled(appName)` 检查状态
- [x] `app.go` 暴露 `SetAutoStart(enabled bool) error` 和 `IsAutoStartEnabled() bool`
- [x] `SettingsView.vue` 添加"开机自启动"开关
- [x] i18n 添加翻译

---

## 功能2: 隧道断连通知

### 技术方案
- 使用 `/usr/bin/osascript` 调用 `display notification`
- 零依赖，纯 Go `os/exec`
- 转义特殊字符防注入

### 实现步骤
- [x] 新建 `internal/notify/notify_darwin.go`
  - `Send(title, message)` 发送系统通知
- [x] `internal/tunnel/manager.go` 添加 `OnTunnelExit` 回调
  - 进程非手动停止时触发回调（携带隧道 ID）
- [x] `app.go` 注入通知回调
  - 查数据库拿到隧道名称
  - 发送通知: "隧道 XXX 已断开"
- [x] 设置页面添加"断连通知"开关
- [x] i18n 添加翻译

---

## 功能3: 隧道搜索/排序

### 技术方案
- 纯前端实现，不涉及后端
- 搜索: 按名称/地址模糊匹配
- 排序: 按名称、状态(运行中优先)、创建时间

### 实现步骤
- [x] `TunnelList.vue` 工具栏添加搜索输入框
- [x] 添加排序下拉选择: 默认(创建时间) / 名称 / 状态
- [x] computed 属性 `filteredTunnels` 处理过滤和排序逻辑
- [x] i18n 添加翻译

---

## 功能4: 流量/连接统计

### 技术方案
- cloudflared `--metrics 127.0.0.1:0` 暴露 Prometheus 指标
- 从 stderr 解析 metrics 服务地址 `Starting metrics server on 127.0.0.1:PORT/metrics`
- Go HTTP GET 获取 `/metrics`，简单文本解析（不引入 prometheus 库，手动解析关键指标即可，保持零依赖）
- 关键指标:
  - `cloudflared_tunnel_ha_connections` (活跃连接数)
  - `cloudflared_tunnel_total_requests` (请求总数)
  - `cloudflared_tunnel_request_errors` (错误数)
  - `quic_client_latest_rtt` (RTT延迟，仅 QUIC 模式)
  - `quic_client_sent_bytes` / `quic_client_receive_bytes` (流量)

### 实现步骤
- [x] `internal/tunnel/manager.go` 解析 metrics 地址并存储
  - 新增 `metricsAddr` 字段到 `runningTunnel`
  - 在 stderr 扫描中匹配 `Starting metrics server on`
- [x] 新建 `internal/tunnel/metrics.go`
  - `FetchMetrics(addr)` HTTP GET + 文本解析
  - 返回 `TunnelMetrics` 结构体
- [x] `app.go` 暴露 `GetTunnelMetrics(id int64) TunnelMetrics`
- [x] `TunnelList.vue` 运行中的隧道卡片显示简要统计
  - 连接数、请求数、错误数
  - 点击展开详情弹窗显示更多指标
- [x] i18n 添加翻译

---

## 完成标准

- [x] 4个功能全部实现
- [x] `wails dev` 编译通过
- [x] 在浏览器中验证各功能正常
- [x] i18n 中英文完整

## 文件变更清单

### 新建文件
- `internal/autostart/autostart_darwin.go`
- `internal/notify/notify_darwin.go`
- `internal/tunnel/metrics.go`

### 修改文件
- `internal/tunnel/manager.go` - metrics地址解析 + 断连回调
- `app.go` - 新增 API 方法
- `frontend/src/components/TunnelList.vue` - 搜索/排序 + 指标展示
- `frontend/src/components/SettingsView.vue` - 开机自启 + 通知开关
- `frontend/src/i18n/zh.ts` - 中文翻译
- `frontend/src/i18n/en.ts` - 英文翻译
