# macOS 系统通知方案调研

> 创建: 2026-02-08
> 状态: 进行中

## 目标

为 Wails v2 桌面应用选择 macOS 原生通知的最佳实现方案，用于 cloudflared 隧道异常退出时通知用户。

## 调研范围

- [ ] Wails v2 内置通知 API 支持情况
- [ ] Go 语言 macOS 通知库对比 (beeep, gosx-notifier, osascript 等)
- [ ] CGO 依赖分析
- [ ] macOS Sequoia 兼容性
- [ ] Wails .app bundle 环境下的行为差异
- [ ] 推荐方案和代码实现思路

## 完成标准

- [ ] 输出完整的技术对比矩阵
- [ ] 给出明确推荐方案
- [ ] 提供可落地的代码实现思路

## 备注

场景: cloudflared 隧道进程异常退出 -> 发送系统通知给用户
