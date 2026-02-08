# Wails v2 系统托盘（System Tray）深度调研报告

> 调研日期: 2026-02-08
> 适用项目: TryNet (Wails v2.10.2 + Go 1.24)
> 目标平台: macOS (主要), Windows/Linux (兼顾)

---

## 一、执行摘要

Wails v2 **没有**内置系统托盘支持，官方明确表示不会为 v2 添加此功能（仅 v3 支持）。但社区提供了多种第三方方案，其中 **`ra1phdd/systray-on-wails`** 是目前最适合 Wails v2 + macOS 的方案，它通过 `Register()` 函数避免了主线程竞争问题，并修复了 macOS 上的 Objective-C 链接冲突。

**推荐方案**: `ra1phdd/systray-on-wails` + `HideWindowOnClose` + `OnBeforeClose`

---

## 二、Wails v2 官方支持情况

### 2.1 官方立场

Wails 维护者 @leaanthony 明确声明:
> "I'm not saying you couldn't get it working if you tried hard enough but we aren't supporting it in v2."

原因:
- 同时维护 v2 和 v3 太困难
- 系统托盘是 v3 的核心功能之一
- v2 的架构不适合原生托盘集成

### 2.2 Wails v3 的系统托盘

Wails v3（目前仍为 alpha，已开发 3+ 年）提供了完整的原生系统托盘支持:
- 自适应图标（支持 light/dark 模式）
- macOS 模板图标支持
- 内置窗口附着和切换
- `systray-basic`、`systray-menu`、`systray-clock` 等示例

**但 v3 尚未稳定，不建议生产使用。**

---

## 三、第三方库对比评估

### 3.1 对比矩阵

| 库 | macOS 兼容 | Wails v2 兼容 | 主线程处理 | 维护状态 | 推荐度 |
|---|---|---|---|---|---|
| `getlantern/systray` | 链接冲突 | 不兼容 | Run() 阻塞 | 维护中 | 不推荐 |
| `energye/systray` | 链接冲突 | Linux/Win 可用 | Run() 阻塞 | 活跃 | 仅 Linux/Win |
| `efeenesc/systray` | 修复链接 | 部分兼容 | Run() 阻塞 | 低维护 | 备选 |
| `ra1phdd/systray-on-wails` | 修复链接 | 完全兼容 | Register() 非阻塞 | 2024.11 发布 | 推荐 |
| 自定义 CGO/ObjC | 完全兼容 | 需手动集成 | 完全可控 | 需自维护 | 高级方案 |

### 3.2 各库详细分析

#### getlantern/systray（原始库）

**问题**: 在 macOS 上与 Wails v2 存在 Objective-C 符号链接冲突。`systray_darwin.m` 中的变量名与 Wails 内部使用的 Cocoa 符号冲突，导致 duplicate symbol 或 undefined symbol 错误。

```
# 典型错误
ld: duplicate symbol '_xxx' in:
    /path/to/systray_darwin.o
    /path/to/wails_darwin.o
```

此外，`systray.Run()` 会阻塞主线程运行事件循环，与 Wails 的主线程事件循环产生冲突。

#### energye/systray（getlantern 的 fork）

移除了 GTK 依赖，增加了点击事件支持，但 **macOS 链接问题未修复**。仅适用于 Linux 和 Windows 平台。

#### efeenesc/systray（修复链接的 fork）

通过重命名 `systray_darwin.m` 中冲突的变量名解决了链接问题。但仍使用 `Run()` 阻塞模式，与 Wails 主线程集成存在风险。作者本人建议使用其他库。

#### ra1phdd/systray-on-wails（推荐）

**核心优势**:
- 修复了 macOS Objective-C 链接冲突
- 提供 `Register()` 函数 -- 初始化 GUI 但不启动事件循环，让 Wails 保持对主线程的控制
- 专门为 Wails 集成设计
- 2024 年 11 月发布，相对较新

```go
// Register 初始化 systray 但不阻塞主线程
// 适合与 Wails 等拥有自己事件循环的框架配合使用
systray.Register(onReady, onExit)

// Run 初始化 systray 并阻塞主线程
// 不适合与 Wails 配合使用
systray.Run(onReady, onExit)
```

**注意**: macOS Catalina (10.15) 之前的版本，`Register()` 行为等同于 `Run()`。

#### 自定义 CGO/Objective-C 方案

GitHub Discussion #4514 中有社区成员分享了完整的 macOS 原生实现:
- 直接使用 CGO 绑定 Cocoa 框架
- 通过 `NSStatusBar`、`NSMenu`、`NSMenuItem` 创建托盘
- 完全可控，但需要自行维护
- 限制: 不能同时设置点击处理器和菜单（Darwin 限制）

---

## 四、窗口关闭拦截方案

### 4.1 HideWindowOnClose（最简方案）

```go
err := wails.Run(&options.App{
    HideWindowOnClose: true,  // 关闭窗口时隐藏而非退出
    // ...
})
```

**行为**: 点击窗口关闭按钮时，窗口隐藏但应用继续运行。

**问题**: 隐藏后没有原生方式重新显示窗口（macOS dock 点击无法恢复，这是 v2 的已知缺陷，v3 已修复）。

### 4.2 OnBeforeClose 回调

```go
err := wails.Run(&options.App{
    OnBeforeClose: func(ctx context.Context) (prevent bool) {
        // 隐藏窗口而非关闭
        runtime.WindowHide(ctx)
        return true  // true = 阻止关闭
    },
})
```

**已知 macOS 问题**（Issue #2572）:
- 使用 `WindowHide` 隐藏后，窗口可能无法通过 `WindowShow` 正确恢复
- 应用图标仍在 Dock 中，但点击无响应
- 建议配合系统托盘的 "显示窗口" 菜单项使用

### 4.3 窗口操作 API

```go
import runtime "github.com/wailsapp/wails/v2/pkg/runtime"

// 显示窗口（如果当前隐藏）
runtime.WindowShow(ctx)

// 隐藏窗口（如果当前可见）
runtime.WindowHide(ctx)

// 最小化窗口
runtime.WindowMinimise(ctx)

// 取消最小化
runtime.WindowUnminimise(ctx)

// 窗口是否最小化
isMin := runtime.WindowIsMinimised(ctx)

// 设置窗口置顶
runtime.WindowSetAlwaysOnTop(ctx, true)
```

**重要**: 这些 API 需要在 `OnDomReady` 之后使用，不能在 `OnStartup` 中直接调用（窗口可能尚未初始化）。

---

## 五、macOS 特殊考量

### 5.1 主线程竞争

macOS 要求所有 UI 操作在主线程执行。Wails 和 systray 都需要主线程:
- Wails: WebView 渲染和事件循环
- systray: NSStatusBar 和 NSMenu 操作

**解决方案**: 使用 `Register()` 而非 `Run()`，让 Wails 保持主线程控制权。

### 5.2 Dock 图标行为

- `HideWindowOnClose: true` 隐藏窗口后，Dock 图标仍然存在
- 点击 Dock 图标 **不会** 自动恢复隐藏的窗口（v2 缺陷）
- 需要通过系统托盘菜单提供 "显示窗口" 选项

如需隐藏 Dock 图标:
- 在 `Info.plist` 中设置 `LSUIElement` 为 `true`
- 但这会导致应用没有 Dock 图标和应用菜单

### 5.3 应用签名

使用 CGO 的第三方库需要确保应用签名正确，否则 macOS Gatekeeper 可能阻止运行。

### 5.4 图标规格

- 系统托盘图标推荐尺寸: 22x22 像素（@1x），44x44 像素（@2x）
- 格式: PNG（推荐）或 ICO
- macOS 支持模板图标（单色，系统自动适配 light/dark 模式）

---

## 六、推荐实现方案

### 6.1 技术选型

**选择**: `ra1phdd/systray-on-wails` + Wails v2 内置选项

**理由**:
1. 专门为 Wails 设计，解决了 macOS 链接问题
2. `Register()` 模式不阻塞主线程
3. API 简洁，与 `getlantern/systray` 兼容
4. 跨平台支持（Windows/macOS/Linux）

### 6.2 集成架构

```
main.go
  |
  +-- wails.Run()          # Wails 主事件循环
  |     |
  |     +-- OnStartup      # 应用启动回调
  |     |     |
  |     |     +-- systray.Register(onReady, onExit)  # 注册托盘（非阻塞）
  |     |
  |     +-- OnBeforeClose   # 窗口关闭拦截
  |           |
  |           +-- runtime.WindowHide()  # 隐藏窗口
  |           +-- return true           # 阻止退出
  |
  +-- onReady()            # 托盘就绪回调
        |
        +-- SetIcon()       # 设置托盘图标
        +-- AddMenuItem("显示窗口")  --> runtime.WindowShow()
        +-- AddMenuItem("退出")      --> systray.Quit() + runtime.Quit()
```

### 6.3 伪代码示例

```go
package main

import (
    "context"

    "github.com/wailsapp/wails/v2"
    "github.com/wailsapp/wails/v2/pkg/options"
    wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
    "github.com/ra1phdd/systray-on-wails"
)

func main() {
    app := NewApp()
    err := wails.Run(&options.App{
        Title:             "TryNet",
        Width:             960,
        Height:            640,
        HideWindowOnClose: true,
        OnStartup:        app.startup,
        OnShutdown:       app.shutdown,
        OnBeforeClose:    app.beforeClose,
        Bind:             []interface{}{app},
    })
}

func (a *App) startup(ctx context.Context) {
    a.ctx = ctx
    // ... 原有初始化逻辑 ...

    // 注册系统托盘（非阻塞）
    systray.Register(a.onTrayReady, a.onTrayExit)
}

func (a *App) beforeClose(ctx context.Context) bool {
    wailsRuntime.WindowHide(ctx)
    return true  // 阻止关闭，仅隐藏
}

func (a *App) onTrayReady() {
    systray.SetIcon(iconData)
    systray.SetTitle("TryNet")
    systray.SetTooltip("TryNet 隧道管理")

    mShow := systray.AddMenuItem("显示窗口", "显示主窗口")
    systray.AddSeparator()
    mQuit := systray.AddMenuItem("退出", "完全退出应用")

    go func() {
        for {
            select {
            case <-mShow.ClickedCh:
                wailsRuntime.WindowShow(a.ctx)
            case <-mQuit.ClickedCh:
                systray.Quit()
                wailsRuntime.Quit(a.ctx)
            }
        }
    }()
}

func (a *App) onTrayExit() {
    // 清理资源
}
```

### 6.4 依赖安装

```bash
go get github.com/ra1phdd/systray-on-wails
```

go.mod 中将添加:
```
require github.com/ra1phdd/systray-on-wails v1.x.x
```

---

## 七、风险评估与缓解

| 风险 | 等级 | 缓解策略 |
|---|---|---|
| macOS WindowHide 后无法恢复 | 中 | 通过托盘菜单 WindowShow 恢复；测试验证 |
| ra1phdd 库维护停滞 | 低 | 代码量小，可 fork 自维护 |
| macOS Catalina 以下不兼容 | 极低 | 目标用户群体基本不使用旧版 macOS |
| CGO 交叉编译复杂度 | 中 | 仅在目标平台本地编译 |
| Dock 图标点击不恢复窗口 | 中 | 用户通过托盘菜单操作，可接受 |

---

## 八、备选方案

### 方案 B: 自定义 CGO/Objective-C

如果 `ra1phdd/systray-on-wails` 不满足需求，可以参考 Discussion #4514 中的自定义实现:
- 完全控制 NSStatusBar 行为
- 无第三方依赖
- 但需要 Objective-C 知识和自行维护

### 方案 C: 升级到 Wails v3

如果 Wails v3 在近期发布稳定版，可以考虑迁移:
- 原生系统托盘支持
- Dock 点击恢复窗口已修复
- 但 v3 仍为 alpha，迁移成本高

### 方案 D: 独立 systray 进程 + IPC

运行独立的 systray 小程序，通过 IPC（Unix Socket / Named Pipe）与 Wails 主程序通信:
- 完全避免主线程冲突
- 架构复杂度高
- 需要管理两个进程的生命周期

---

## 九、参考资料

- Wails v2 官方文档 - Options: https://wails.io/docs/reference/options/
- Wails v2 官方文档 - Window API: https://wails.io/docs/reference/runtime/window/
- Wails v3 系统托盘: https://v3alpha.wails.io/features/menus/systray/
- GitHub Issue #1010 - macOS tray menu: https://github.com/wailsapp/wails/issues/1010
- GitHub Discussion #4514 - V2 SysTray: https://github.com/wailsapp/wails/discussions/4514
- GitHub Issue #2572 - close window logic on mac: https://github.com/wailsapp/wails/issues/2572
- GitHub Issue #3003 - Linking problems on macOS: https://github.com/wailsapp/wails/issues/3003
- ra1phdd/systray-on-wails: https://pkg.go.dev/github.com/ra1phdd/systray-on-wails
- energye/systray: https://github.com/energye/systray
- efeenesc/systray: https://pkg.go.dev/github.com/efeenesc/systray
