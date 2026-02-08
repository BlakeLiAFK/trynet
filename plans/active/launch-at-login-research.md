# macOS 开机自启动 (Launch at Login) 方案调研

> 创建: 2026-02-08
> 状态: 已完成

## 目标

调研 Wails v2 桌面应用在 macOS 上实现"开机自启动"的最佳方案（非 App Store 分发）

## 步骤

- [x] 调研 Wails v2 是否有内置支持
- [x] 调研 macOS 开机自启动的各种现代方法
- [x] 调研 Go 语言中可用的库和方法
- [x] 分析沙盒/非沙盒环境差异
- [x] 给出推荐方案和代码实现思路

## 调研结论

### Wails v2 没有内置支持

功能在 Wails v3 中实现（Issue #1789），v3 选用 AppleScript + System Events 方案。

### 推荐方案：AppleScript + System Events

- Wails v3 官方验证的方案
- 纯 Go 实现，无 cgo 依赖
- macOS 10.x ~ 15.x 全版本兼容
- 代码量小（<80行），维护成本低

### 方案对比

| 方案 | 兼容性 | 复杂度 | 需要cgo | 推荐 |
|------|--------|--------|---------|------|
| AppleScript | 10.x~15.x | 极低 | 否 | 首选 |
| LaunchAgent plist | 10.x~15.x | 低 | 否 | 备选 |
| SMAppService | 13.0+ | 高 | 是 | 不推荐 |

### 实现路径

文件: `internal/autostart/autostart_darwin.go`
三个方法: Enable() / Disable() / IsEnabled()
通过 osascript 调用 AppleScript 操作 System Events 登录项

### 注意事项

1. 首次调用需要用户授权"自动化"权限
2. 每次查询实时读取状态，不缓存
3. 开发模式非 .app 运行时功能不可用
4. Info.plist 需添加 NSAppleEventsUsageDescription

## 完成标准

- [x] 输出完整调研报告，包含方案对比
- [x] 给出具体的代码实现思路
- [x] 考虑兼容性和用户体验
