# 快速隧道凭据存档复用

## 背景

快速隧道模式每次启动都申请新隧道，域名跟着变。但实测隧道断开后服务端注册
不会立刻失效，所以把凭据存到当前目录、重启时优先复用，大概率能拿回同一个
域名。有效期没有文档说明，不猜 TTL，注册被拒就当过期、走已有的 refresh 逻辑
换新的。

## 设计

### 存档（quick_tunnel.go）

- 文件名 `.trynet.json`，路径 `os.Getwd()`（当前工作目录，不是 `-dir`）
- 权限 `0600`
- 内容是 `tunnelInfo` 的 JSON
- `loadTunnelStore() (*tunnelInfo, error)`：读不到、解析失败、字段不全都返回
  error，调用方一律当"没有存档"处理，不报错打扰用户
- `saveTunnelStore(info *tunnelInfo) error`：写存档

### main.go 改动

- 新增 flag `-new` / `TRYNET_NEW`：忽略存档强制申请新隧道
- 新增 flag `-hidden` / `TRYNET_HIDDEN`：文件服务放行点开头路径
- 快速隧道分支：
  - `refresh` 闭包包一层 `requestQuickTunnel`，成功后覆盖写存档（这样
    `run()` 里已有的 rejected → refresh 分支自动会把新凭据存下）
  - 没给 `-new` 且存档能解析 → 直接用，不调 API
  - 否则调 `refresh` 申请新的，标记 `announceStore = true`
- `run()` / `serve()` / `tunnel` 结构体新增 `announceStore bool`，只在
  首次成功注册（非重连）且这次是新建存档时，在 `printServing` 之后打一行
  英文提示（存档路径 + 建议加入 .gitignore）
- `startFileServer` 新增 `hidden bool` 参数，包一层 `blockDotfiles`
  handler：路径按 `/` 切开，`path.Clean` 规范化后任何一段以 `.` 开头就
  404。默认开启，`-hidden` 关闭

### 不改的部分

- `-domain` 固定域名模式的建隧道/DNS/teardown 逻辑
- 隧道协议、连接、代理回退

## 测试

- 存档读写往返（`t.TempDir()` + `os.Chdir`，defer 还原）
- 存档损坏 → 当无存档
- `-new` 跳过存档（走 main 里的分支逻辑，用小函数单测覆盖）
- 点文件拦截：`.trynet.json` / `.git/config` / `.env` 404，正常文件 200，
  `-hidden` 放行，`%2e` 编码绕过也要挡住

## 验证

```
GOTOOLCHAIN=go1.26.8 gofmt -l .
GOTOOLCHAIN=go1.26.8 go vet ./...
GOTOOLCHAIN=go1.26.8 go test -count=1 ./...
GOTOOLCHAIN=go1.26.8 go test -count=2 ./...
```

## 状态

- [x] 计划
- [x] 实现
- [x] 测试（新增 13 个，总数 69 → 82，`-count=1`/`-count=2` 均过）
- [x] 验证 + 提交

## 执行记录

- 存档读写、`acquireQuickTunnel`（复用/`-new`/无存档/refresh 报错四种分支）
  放进 `quick_tunnel.go` + `quick_tunnel_test.go`
- `blockDotfiles` 放进 `main.go`（跟 `startFileServer` 挨着），测试在
  `main_test.go`，覆盖普通拦截、放行、`-hidden`、`%2e` 编码绕过
- `run()`/`serve()`/`tunnel` 结构体多穿一个 `announceStore bool`，只在首次
  非重连注册成功、且这次是新申请（不是复用存档）时，在 `printServing` 后面
  打一行提示，文案在 `banner.go` 的 `printStoreHint()`
- 实测：`gofmt -l .` 干净，`go vet ./...` 干净，`go test -count=1/2 ./...`
  全过，`wc -l` 最大文件 420 行（`cloudflare_api.go`，未改动），本次新增/
  改动文件都在 500 行以内
