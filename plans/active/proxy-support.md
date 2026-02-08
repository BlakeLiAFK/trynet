# cloudflared 代理支持

> 创建: 2026-02-08
> 状态: 进行中

## 目标

为 cloudflared 进程添加 HTTP/SOCKS5 代理支持，解决受限网络环境下的连接问题。

## 调研结论

- cloudflared 默认使用 QUIC (UDP/7844)，**无法通过代理**
- 强制 `--protocol http2` 后连接变为 TCP/443，可通过代理
- 设置 `HTTPS_PROXY`/`HTTP_PROXY`/`ALL_PROXY` 环境变量到子进程
- Go 标准库 `net/http` 会读取这些环境变量
- 这是最佳实践方案，官方 PR #1514 也采用类似思路

## 步骤

- [ ] 1. 后端: manager.go - Start() 方法支持代理参数
  - 接收 proxyURL 参数
  - 代理启用时自动添加 `--protocol http2`
  - 通过 cmd.Env 设置 HTTPS_PROXY/HTTP_PROXY/ALL_PROXY 环境变量
- [ ] 2. 后端: app.go - StartTunnel() 读取代理配置传递给 manager
  - 从 DB 读取 proxy_enabled, proxy_url 设置
  - 传递给 tunnels.Start()
- [ ] 3. 前端: SettingsView.vue - 添加代理设置 UI
  - 开关: 启用/禁用代理
  - 代理类型: HTTP / SOCKS5
  - 代理地址和端口
  - 可选: 用户名/密码
  - 提示: 启用代理会自动强制 HTTP/2 协议
- [ ] 4. 前端: i18n - 添加代理相关翻译
- [ ] 5. 测试验证

## 完成标准

- [ ] 设置页面可配置代理
- [ ] 启动隧道时正确传递代理环境变量
- [ ] 代理启用时自动使用 HTTP/2 协议
- [ ] UI 有清晰的提示说明

## 备注

- QUIC 协议不支持代理，必须强制 HTTP/2
- SOCKS5 代理通过 ALL_PROXY 环境变量传递
- HTTP 代理通过 HTTPS_PROXY/HTTP_PROXY 传递
