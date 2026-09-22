# plan_Restructure —— 按职责拆包，清空根目录

## 现状

38 个 `.go` 文件全在根目录、全是 `package main`。功能上没问题，但看不出哪些
东西是一组的，改一处不知道会波及谁——因为同一个包里所有标识符互相可见，
边界只存在于命名习惯里。

## 1. 目标结构

```
main.go                     入口：flag 解析、装配、重连循环
env.go                      环境变量读取（只有 main 用）
banner.go                   启动横幅和状态行
make.go                     打包脚本（//go:build ignore，不进构建）
rsrc_windows_amd64.syso     Windows 资源，必须留在 main 包目录

internal/tunnel/            隧道本体
  tunnel.go                 单条边缘连接：HTTP/2 角色反转 + Cap'n Proto 注册
  quick_tunnel.go           快速隧道申请 + .trynet.json 存档
  proxy.go                  代理探测、多路径拨号、记住可用路线
  backoff.go                重连退避

internal/cfapi/             Cloudflare 管理 API
  cloudflare_api.go         泛型请求封装 + 各资源路径
  named_tunnel.go           固定域名：创建/复用 tunnel 和 DNS 记录、清理

internal/fileshare/         文件服务
  fileserver.go             路由树、目录列表、下载、上传
  session.go                会话 cookie 鉴权
  share.go                  单文件临时分享链接
  thumbnail.go              缩略图
  contentfilter.go          点文件 + 敏感名单过滤
  web/                      内嵌页面（embed.FS 必须和它的包在一起）

assets/  plans/
```

根目录的 `.go` 从 38 个降到 8 个（含 4 个测试）。

## 2. 依赖方向

```
main ──→ tunnel
     ├─→ cfapi ──→ tunnel      （cfapi 产出 tunnel.Info）
     └─→ fileshare
```

无环。`fileshare` 谁也不依赖，`tunnel` 谁也不依赖。

`cfapi` 依赖 `tunnel` 是因为 `tunnelInfo`（连接器凭据）定义在那边，
而 `cfGetTunnelToken` 要产出一个。这个方向是对的：cfapi 负责"怎么从管理 API
拿到凭据"，tunnel 负责"拿凭据去连"。不为这一个类型再开一个包。

## 3. 唯一的真实改动：把打印从 tunnel 里倒出来

现在 `tunnel.go` 直接调 `printReady` / `printServing` / `printStoreHint`。
banner 留在 main 包，tunnel 就不可能再调它——会成环。

改成回调：

```go
type Callbacks struct {
    OnReady     func(location, hostname, route string) // 首次连上
    OnReconnect func(route string)                     // 断线恢复
}
```

`tunnel` 结构体随之丢掉 `target`、`extraLine`、`announceStore` 三个字段——
它们是纯展示数据，本来就不该待在一个管连接的类型里，只是同包时看不出来。
main 提供闭包，想打什么自己决定。

这不是为了拆包硬凑的适配层，是拆包把一个原本就存在的职责混淆暴露了出来。

## 4. 导出规则

**只导出跨包真正需要的**，其余一律保持小写。

拆包的收益就在这里：`isFilteredName`、`downsample`、`cfPath` 这些留在包内，
以后改它们不用再担心"根目录某个文件是不是在用"。

预计的导出面：

- `tunnel`：`Info`、`Serve`、`EdgeHosts`、`AcquireQuick`、`RequestQuick`、
  `StoreFileName`、`Backoff`、`Callbacks`、`Dial`（给 main 做连通性探测）
- `cfapi`：`SetupNamedTunnel`、`Handle`（含 `RefreshToken`、`Teardown`）、
  `TokenSource`
- `fileshare`：`Start`、`Config`、`GeneratePassword`、`PasswordDigits`

## 5. 不做的事

- **不建 `cmd/trynet/`**。单二进制项目，`main.go` 放根目录是 Go 的常规做法，
  多一层目录只是仪式。
- **不把 `banner.go` / `env.go` 也拆出去**。它们只有 main 用，48 和 177 行，
  单独开包就是一文件一包。
- **不动任何测试的断言**。这次只搬家和改包名/大小写，行为一个字节都不变——
  测试全绿是这次重构唯一的验收标准，改了断言就没有验收标准了。

## 6. 步骤

一个包一个提交，坏了好定位。每步跑全量测试。

- [x] `internal/fileshare`（不依赖任何内部包，先搬最独立的）
- [x] `internal/tunnel` + 回调倒置
- [x] `internal/cfapi`
- [x] 根目录收尾：main.go / env.go / banner.go 的导入和调用改掉
- [x] 全量测试 + `go vet` + 真机跑一次（快速隧道 + 文件服务 + 分享 + 网格）

## 8. 执行记录

根目录 `.go` 从 **38 个降到 8 个**。165 个测试全过，`go vet` 干净，没有文件超 500 行。
**没有改动任何测试断言**——只改了因签名变化而必须改的调用处。

### 计划里没预见到的四处

**`apiHTTPClient` 原本在 main.go**，而 `quick_tunnel.go`（tunnel 包）和
`cloudflare_api.go`（cfapi 包）都在用它。依赖方向是 cfapi→tunnel，所以它落在
`tunnel` 里导出成 `APIClient`。不为 4 行配置单开一个包。

**`edgeHosts` 也在 main.go**，但它是边缘主机列表，跟 main 没关系，一并移进
`tunnel` 导出成 `EdgeHosts`。

**banner 需要 `detectProxy`/`isSocks`/`findSocks` 三个内部函数**来拼提示文案。
没有把三个都导出，而是给了一个只读快照 `tunnel.ProxyStatus() ProxyState`：
外面要的是"现在什么情况"，不是"你怎么查出来的"。以后探测逻辑要改（加环境变量、
支持 pac），改这一个函数就够，调用方一个字不用动。

**两处刻意的重复**，都在注释里写明了原因：

- `sleepCtx` 在 cfapi 里另写了一份（7 行）。它跟隧道毫无关系，为省 7 行去
  import 隧道包拿一个 sleep，读的人会先愣一下"删 DNS 记录的重试为什么要从
  隧道包拿东西"。
- `clearProxyEnv` 测试 helper 在根目录 `banner_test.go` 里另写一份（5 行）。
  Go 的测试 helper 不跨包共享，要共享就得挪进非测试文件再导出——为了五行
  把测试脚手架塞进生产代码，不划算。

### 回调倒置的实际收益

`tunnel` 结构体去掉了 `target`、`extraLine`、`announceStore` 三个字段，
`Serve()` 的参数从 8 个减到 6 个。这三个是纯展示数据，本来就不该待在一个管
连接的类型里；同包时看不出来，拆包一逼就现形了。

回调签名 `OnReady(location, hostname, route)` 把 hostname 传了出去，而不是让
调用方从自己手里的 `info` 读：注册被拒时外层会换一份新凭据重试，闭包捕获的
那个 `info` 就过期了。这是本项目之前踩过一次的坑（见 plan_NamedTunnel.md 里
teardown 闭包那条），这次直接在签名上堵死。

### 真机验证

走 trycloudflare 隧道：`/ui/` 200、未登录 `/data/` 401、登录后 200、
分享链接无凭据取回内容、9 个静态资源全 200。

## 9. 待确认

`testdata/public/hello.txt` 没有任何代码引用，是早期手工验证的残留，
建议删。等确认，不自行删除。
