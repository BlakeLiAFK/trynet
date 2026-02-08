# TryNet

Cloudflare Tunnel 桌面管理工具，支持一键创建和管理多个隧道，将本地服务快速暴露到公网。

## 功能

- 一键安装和更新 cloudflared
- Quick Tunnel：零配置创建临时隧道，自动分配 `*.trycloudflare.com` 域名
- Named Tunnel：使用 Cloudflare Zero Trust Token，支持自定义域名
- 支持 HTTP / HTTPS / TCP 协议
- 同时运行多个隧道
- 中文 / English 双语界面

## 技术栈

- 后端：Go + [Wails v2](https://wails.io)
- 前端：Vue 3 + TypeScript + vue-i18n
- 数据库：SQLite (纯 Go 实现，无 CGO 依赖)

## 环境要求

- Go 1.22+
- Node.js 18+
- [Wails CLI v2](https://wails.io/docs/gettingstarted/installation)

```bash
go install github.com/wailsapp/wails/v2/cmd/wails@latest
```

## 开发

```bash
wails dev
```

## 构建

```bash
wails build
```

构建产物位于 `build/bin/` 目录。

### macOS 首次打开

由于应用未经 Apple 签名，macOS 会阻止直接打开。有以下几种方式解决：

**方式一：右键打开（推荐）**

右键点击 `trynet.app` -> 选择"打开" -> 在弹窗中点击"打开"

**方式二：命令行移除隔离属性**

```bash
xattr -cr trynet.app
```

**方式三：系统设置**

打开"系统设置" -> "隐私与安全性" -> 找到被阻止的应用 -> 点击"仍要打开"

## 项目结构

```
trynet/
├── app.go                  # Wails API 绑定层
├── main.go                 # 应用入口
├── internal/
│   ├── db/db.go            # SQLite 数据库
│   ├── cfd/manager.go      # cloudflared 安装与管理
│   └── tunnel/manager.go   # 隧道进程管理
└── frontend/
    └── src/
        ├── App.vue
        ├── style.css
        ├── i18n/           # 国际化
        └── components/
            ├── TunnelList.vue    # 隧道管理
            └── SettingsView.vue  # 设置页面
```

## License

MIT
