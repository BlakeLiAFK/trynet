# 系统托盘最小化功能

> 创建: 2026-02-08
> 状态: 进行中

## 目标

实现系统托盘功能：关闭窗口仅隐藏到托盘，右键托盘菜单可退出应用。

## 技术方案

- 库: `ra1phdd/systray-on-wails`（修复了 macOS Cocoa 符号冲突）
- 使用 `systray.Register()` 非阻塞模式，不与 Wails 主线程冲突
- `OnBeforeClose` 拦截关闭，调用 `WindowHide()`
- 托盘菜单: 显示窗口 / 退出

## 步骤

- [x] 1. 安装依赖 `efeenesc/systray`（ra1phdd 版本有 .h/.m 函数名不匹配 bug）
- [x] 2. 准备 22x22 托盘图标（从 appicon.png 缩放，嵌入 internal/tray/icon.png）
- [x] 3. internal/tray/tray.go - 托盘初始化模块（Register 非阻塞模式）
- [x] 4. app.go - startup 中初始化托盘 + beforeClose 拦截关闭
- [x] 5. main.go - OnBeforeClose + HideWindowOnClose
- [x] 6. 编译通过，wails dev 运行成功

## 完成标准

- [ ] 关闭窗口后应用不退出，图标在系统托盘
- [ ] 点击托盘图标可恢复窗口
- [ ] 右键菜单可退出应用
