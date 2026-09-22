# plan_MiniTunnel —— 迷你版 cloudflared 快速隧道

## 0. 调研结论（已验证）

| 问题 | 结论 | 证据 |
| --- | --- | --- |
| cloudflared 是否开源 Go 项目 | 是，`github.com/cloudflare/cloudflared`，**Apache-2.0** | 已 clone 校验 LICENSE |
| 能否提取算法自己实现 | 能。协议全部在开源仓库里，无私有加密 | 见下文协议拆解 |
| trycloudflare 申请隧道接口 | `POST https://api.trycloudflare.com/tunnel`，空 body，无需任何认证 | 实测返回 `{"success":true,"result":{id,name,hostname,account_tag,secret}}` |
| 边缘地址 | SRV `_v2-origintunneld._tcp.argotunnel.com` → `region1.v2.argotunnel.com:7844` / `region2...:7844` | DoH 实测确认，可直接硬编码省掉 SRV 查询 |
| 传输协议 | QUIC（默认）或 HTTP/2 over TLS（fallback）。**HTTP/2 路径实现量小一个数量级** | `connection/protocol.go`、`connection/http2.go` |

### HTTP/2 隧道协议拆解（我们要复刻的部分）

1. `tls.Dial("tcp", "region1.v2.argotunnel.com:7844")`，`ServerName="h2.cftunnel.com"`，`NextProtos=["h2"]`
2. **角色反转**：客户端拨号后，自己跑 `http2.Server.ServeConn(tlsConn)`，由边缘向我们发 HTTP/2 请求
3. 请求按 `Cf-Cloudflared-Proxy-Connection-Upgrade` 头分流：
   - `control-stream` → 这条流的 body/response 当成双向管道，跑 Cap'n Proto RPC `RegistrationServer.registerConnection(auth{accountTag,secret}, tunnelID, connIndex=0, options)`，注册成功后域名才生效
   - `websocket` → WebSocket
   - 其余 → 普通 HTTP 请求，直接反代到本地
4. 响应头约定（因为声明了 `serialized_headers` 特性）：
   - `cf-cloudflared-response-headers`: 所有业务响应头 base64(RawStd) 后拼成 `k:v;k:v`
   - `cf-cloudflared-response-meta`: `{"src":"origin"}`
   - `content-length` 额外原样保留一份

唯一"啃不动自己写"的是 Cap'n Proto 注册 RPC（生成代码 4800 行），因此直接复用 cloudflared 的 `tunnelrpc` 包。

## 1. 方案选择

- **方案 A（采用）**：新建独立 module，只 import `cloudflared/tunnelrpc` + `tunnelrpc/pogs` 做注册，HTTP/2 反代自己写。代码 ~250 行，不碰 QUIC/capnp 细节。
- 方案 B：自己手搓 capnp RPC —— 工作量数千行，否决。
- 方案 C：把 `tunnelrpc/` 目录拷进来 —— 多 6600 行生成代码，否决。

## 2. 交付物

```
trynet/
├─ go.mod            # module trynet，go 1.26.8
├─ main.go           # 隧道主逻辑
├─ proxy.go          # https_proxy 拨号
├─ main_test.go      # 自检：头部序列化往返 + 代理地址解析
└─ plans/plan_MiniTunnel.md
```

## 3. 功能设计

CLI 两个用法，对应用户要的两个功能：

```bash
trynet                     # 不带参数 = 分享当前目录（最常用，等价于 -dir .）
trynet -dir ./public       # 分享指定目录，内置 fileserver 监听随机端口
trynet -port 3000          # 把本机已有的 3000 端口映射到公网
```

`-dir` 与 `-port` 同时给会报错；都不给则默认 `-dir .`。
走 `-dir` 时 `net.Listen("tcp","127.0.0.1:0")` 拿随机端口起 `http.FileServer`，之后走同一条隧道逻辑。

**运行期输出一律英文**（日志、错误、`-h` 帮助），代码注释仍用中文。

### 代理支持（https_proxy）

出站有两处，且**第二处标准库管不了**：

| 出站 | 代理方式 |
| --- | --- |
| `POST api.trycloudflare.com/tunnel` | `http.Transport{Proxy: http.ProxyFromEnvironment}`，自动识别 `https_proxy` |
| `TLS 拨号 region1.v2.argotunnel.com:7844` | **裸 TCP，标准库不管**。手写：向代理发 `CONNECT host:7844 HTTP/1.1`，读 `200 Connection established`，再在这条 TCP 上做 TLS 握手 |

规则：
- 读取顺序 `HTTPS_PROXY` → `https_proxy` → `ALL_PROXY` → `all_proxy`，遵守 `NO_PROXY`
- 支持代理 URL 里的 `user:pass@`（发 `Proxy-Authorization: Basic ...`）
- 支持 `http://` / `https://` / `socks5://`（socks5 用 `golang.org/x/net/proxy`，随 x/net 一起进来，零新增依赖）
- 未设置代理时直连，行为不变

## 4. 实现步骤

1. `go mod init trynet`，`go get github.com/cloudflare/cloudflared`、`golang.org/x/net/http2`
   - 工具链：已下载 **go1.26.8**（见第 7 节），默认 `go` 仍是 1.24.4，不受影响
2. `requestQuickTunnel()` —— POST 拿 hostname / id / account_tag / secret，走 `ProxyFromEnvironment`
3. `startLocalFileServer(dir)` —— 随机端口，返回 `127.0.0.1:port`
4. `serializeHeaders()` —— 30 行，照搬 `connection/header.go` 的 base64 约定
5. `dialEdge()` —— 按 `https_proxy` 决定直连 / HTTP CONNECT / socks5，再套 TLS(ALPN=h2)
6. `proxyHandler(origin)` —— `httputil.ReverseProxy` 到本地，`ModifyResponse` 里写序列化响应头
7. `controlStream()` —— `tunnelrpc.NewRegistrationClient(...).RegisterConnection(...)`，成功后打印公网 URL，阻塞到 ctx 结束
8. `connect()` —— 拨号 + `http2.Server.ServeConn`；断线后外层 for 循环重连（region1 / region2 轮换）
9. 自检：`main_test.go` 验证 `serializeHeaders` 与 cloudflared 同款输出、代理 URL 解析分支
10. 实测：`-dir` 跑起来，curl 公网域名拿到文件列表

## 5. 明确不做（精简取舍）

- QUIC 传输：HTTP/2 已够用，且 QUIC 需额外处理 capnp 数据流签名 `0x0A36CD12A13E`
- WebSocket / TCP(warp-routing) / UDP datagram / ICMP
- 多 HA 连接（quick tunnel 本来就只允许 1 条）
- 指标、优雅退出、远程配置下发

## 6. 风险

| 风险 | 应对 |
| --- | --- |
| ~~本机 go 1.24 < cloudflared 要求的 1.26~~ | 已解决：go1.26.8 装进 module cache，不改默认 go |
| 国内网络直连 `*.argotunnel.com:7844` 可能不稳 | 支持 `https_proxy` 走代理；另加重试与 region 轮换 |
| 代理不允许 CONNECT 到 7844 这种非 443 端口 | 明确报错提示，让用户换代理或直连 |
| 响应头序列化细节出错 → 浏览器拿不到 Content-Type | 单测 + 实测 `curl -v` 校验 |
| trycloudflare 隧道是临时的，会被回收 | 属预期行为，不处理 |

## 7. Go 1.26 工具链（已完成，零污染）

用 Go 官方的 toolchain 分发机制装的，不是装第二套 SDK：

```bash
GOTOOLCHAIN=go1.26.8 go version   # -> go version go1.26.8 windows/amd64
```

- 落点：`C:\Users\blake\go\pkg\mod\golang.org\toolchain@v0.0.1-go1.26.8.windows-amd64\`
- 走 `GOPROXY=https://goproxy.cn` 拉取，国内可达
- **未改动**：`PATH`、`GOROOT`（仍 `C:\sdk\go`）、默认 `go`（仍 1.24.4）、任何全局配置
- 本项目 `go.mod` 写 `go 1.26.8` 后，现有默认值 `GOTOOLCHAIN=auto` 会自动切到它；其它项目完全不受影响
- 卸载 = 删掉上面那个目录

## 8. 实现过程中发现的两个坑（调研阶段没料到）

### 坑 1：边缘证书不是公共 CA 签的

`region1.v2.argotunnel.com:7844` 返回的是 **CloudFlare Origin Certificate**，
签发者是 Cloudflare 自己的 Origin CA，系统根证书库里没有，直连报
`x509: certificate signed by unknown authority`。

解决：`tlsconfig.CreateTunnelConfig("", "h2.cftunnel.com")` —— cloudflared 把这张根证书
硬编码在 `tlsconfig/cloudflare_ca.go` 里，直接复用，不用自己抄 PEM。

顺带一提：边缘在 7844 上**不回 ALPN**（`NegotiatedProtocol` 是空串），
但直接在这条连接上跑 HTTP/2 是通的，不用管。

### 坑 2：HTTP 代理能 CONNECT 成功，但隧道是哑的

本机环境里 `ALL_PROXY=http://192.168.3.181:2080`，CONNECT 返回 200，
之后 TLS 握手直接 EOF —— 代理接了但没真正转发 7844。
换成 `socks5://192.168.3.181:1080` 后一次通过。

所以报错信息要能区分"连不上代理"和"代理接了但不通"，不能笼统说"连接失败"。

## 9. 实测结果

```
16:49:33 已连到边缘 192.168.3.181:1080 (本地 192.168.3.181:65309)
16:49:34 隧道已就绪 [lax11]  ->  https://signature-powers-manhattan-biol.trycloudflare.com
```

```
$ curl -si --socks5-hostname 192.168.3.181:1080 https://<hostname>/hello.txt
HTTP/1.1 200 OK
Content-Type: text/plain; charset=utf-8    <- 头部序列化正确
Content-Length: 18
CF-Ray: a3e7d23f8e4bd12a-LAX
Server: cloudflare

hello from trynet
```

`-port` 与 `-dir` 两种模式都已实测通过。

## 10. 使用方式

```bash
# 国内环境基本都要走代理，程序启动时也会按平台提示这条命令
export ALL_PROXY=socks5://192.168.3.181:1080   # Windows PowerShell: $env:ALL_PROXY="..."

GOTOOLCHAIN=go1.26.8 go build -trimpath -ldflags="-s -w" -o trynet-raw.exe .
upx --best -o trynet.exe trynet-raw.exe

./trynet.exe                          # 不带参数 = 分享当前目录
./trynet.exe -dir ./testdata/public   # 分享指定目录
./trynet.exe -port 8099               # 映射已有端口
```

`go.mod` 里写了 `go 1.26.8`，`GOTOOLCHAIN=auto`（系统默认）会自动切到它，日常 `go build` 也不用带前缀。

## 11. 体积优化

| 构建方式 | 体积 | 启动耗时 |
| --- | --- | --- |
| 默认 `go build` | 18.45 MB | — |
| `-trimpath -ldflags="-s -w"` | **12.91 MB** | 161 ms |
| 再过一遍 `upx --best` | **4.09 MB** | 324 ms |

按包统计过一遍，剩下的体积基本都是 Go runtime + `net/http` + `crypto/tls`，
第三方里最大的是 capnp 674 KB、protobuf 602 KB（被 prometheus 带进来的）、prometheus 111 KB。
就算把 `tunnelrpc/metrics` 抠掉也只省 1.3 MB 左右，不值当，没做。

**UPX 的代价（自己权衡）**：
- 启动慢一倍（161 ms → 324 ms），自解压开销，长期驻留的隧道进程无所谓
- Windows 杀软对加壳程序误报率高，要分发给别人的话建议用不加壳的 12.91 MB 版本
- 已实测：加壳后隧道功能正常、图标资源也完好

命名约定：**原始编译产物 = `trynet-raw.exe`，UPX 压缩后 = `trynet.exe`**（对外交付的就是压缩版）。

```bash
GOTOOLCHAIN=go1.26.8 go build -trimpath -ldflags="-s -w" -o trynet-raw.exe .
upx --best -o trynet.exe trynet-raw.exe
```

**多层加壳？不行，实测过：** UPX 套不了自己，`upx --best --force` 一检测到文件已经是 UPX 壳就
`Packed 1 file: 0 ok, 1 error`，体积一个字节没变。要多层得换不同的壳工具链串起来，但 UPX 压完
数据已接近熵上限，再套一层几乎必然变大 + 启动更慢 + 杀软误报更凶，收益为负，没做。

## 12. 图标

`assets/trynet.ico`（256/128/64/48/32/16 六个尺寸）+ `assets/trynet-icon.png`。
imgen 生图 → 裁掉白边 → 自己画圆角遮罩做透明角 → 打包 ico。

资源通过 `rsrc_windows_amd64.syso` 嵌进 exe，**不需要改任何代码**，`go build` 会自动链接。
换图标时重新生成一次即可：

```bash
rsrc -ico assets/trynet.ico -arch amd64 -o rsrc_windows_amd64.syso
```

文件名带 `_windows_amd64` 后缀是故意的，这样交叉编译到 Linux/macOS 时不会被链接进去。

## 13. 默认行为与日志语言

- 无参数 = `-dir .`，直接分享当前目录，省掉最常见场景的打字
- `-port` 和 `-dir` 同时出现才报错，二者都缺不再是错误
- 所有 `log.Printf` / `fmt.Errorf` / flag usage 改为英文，避免终端、日志文件、CI 里出现乱码；
  报错里回显的路径除外，那是用户自己传进来的

```
17:03:30 serving C:/Users/blake/tmp/trynet/testdata/public on http://127.0.0.1:54012
17:03:34 connected to edge 192.168.3.181:1080 (local 192.168.3.181:54015)
17:03:35 tunnel ready [lax12]  ->  https://specification-lit-fantasy-cities.trycloudflare.com
```

## 14. 启动 banner 与代理提示

`banner.go`（89 行）：slant 字体 logo + 副标题 + 代理状态块，橙色呼应图标。

- 检测到代理：绿色显示值和来源（`socks5://... (from ALL_PROXY)`）
- 没检测到：黄色提示 + **按当前平台**打印一条可复制的运行命令
- 检测到但不是 socks5：http 代理常连不上边缘 7844，程序**自动从环境里挑出 socks5 那条**
  （如用户的 `ALL_PROXY`），拼成可直接复制的命令 —— 不用用户自己记
- 命令按平台生成：Windows 用 PowerShell 的 `$env:HTTPS_PROXY="..."; .\\trynet.exe`，
  mac/linux 用 `HTTPS_PROXY=... ./trynet`；二进制名取自 `os.Args[0]`，跟实际文件名一致
- 隧道就绪后用一个 box 框住公网 URL，橙色加粗，方便直接复制
- 尊重 `NO_COLOR` 惯例，非终端 / `TERM=dumb` 时自动去色

```
   __                        __
  / /________  ______  ___  / /_
 / __/ ___/ / / / __ \\/ _ \\/ __/
/ /_/ /  / /_/ / / / /  __/ /_
\\__/_/   \\__, /_/ /_/\\___/\\__/
        /____/
  mini cloudflare tunnel  v0.1

  proxy   socks5://192.168.3.181:1080  (from ALL_PROXY)

  ✓ tunnel ready  [lax09]
  ┌───────────────────────────────────────────────────────┐
  │ https://roses-shareware-appears-ons.trycloudflare.com │
  └───────────────────────────────────────────────────────┘
```

http 代理不通时的自动建议（实测，环境里 `HTTPS_PROXY=http:2080` + `ALL_PROXY=socks5:1080`）：

```
  proxy   http://192.168.3.181:2080  (from HTTPS_PROXY)
  ! http proxies often can't reach the edge (port 7844); try socks5:
    $env:HTTPS_PROXY="socks5://192.168.3.181:1080"; .\\trynet.exe
```

## 15. 三处健壮性修复（review 后按要求全部修复）

review 时发现三个真实问题，用户要求全部修复：

### 15.1 凭据失效不会自愈

**问题**：`requestQuickTunnel()` 只在启动时调一次，注册被边缘拒绝后只会带着同一份
失效凭据无限重试，永远连不上。

**修复**：`serve()` 返回值从 `error` 改成 `serveResult{err, connected, rejected}`。
只有 `RegisterConnection` 明确报错（`rejected=true`）才重新申请一个新隧道；
单纯的拨号/TLS 失败不换隧道——否则网络抖动一下公网地址就变了，用户还得重新复制链接。

### 15.2 没有优雅退出

**问题**：Ctrl+C 直接关 TCP，不会告诉边缘注销这条注册，边缘要等自己的超时才会踢掉。

**修复**：连接本身现在用独立的 `connCtx`（不直接绑外层的信号 ctx）。收到 Ctrl+C 时，
先在这条还活着的连接上跑 `client.GracefulShutdown()` RPC（用一个全新的、没被取消的
5 秒超时 context——外层 ctx 这时已经 canceled，没法复用来发 RPC），跑完或超时了再
真正 `cancelConn()` 断开。`tunnel` 结构体新增 `client`/`result` 字段，用 `sync.Mutex`
保护（写者是 `serveControlStream` 的 goroutine，读者是收到信号后的另一个 goroutine）。

**验证方式的取舍**：Windows 上从工具环境里给一个后台进程投递真正的 Ctrl+C
（`GenerateConsoleCtrlEvent`）需要攻击自己的控制台，有误杀当前工具会话的风险，
沙盒里没法安全做。改用注入假 `RegistrationClient` 的单测直接验证 `gracefulShutdown()`
这个函数本身：确认 client 非空时真的调用了 `GracefulShutdown(ctx, 3s)`，ctx 带
≤5s 超时；client 为空（还没注册成功就收到信号）时是安全的空操作。函数逻辑本身
验证过，但"OS 信号能否触发到这个函数"这一层没有做真实信号的端到端测试，仅依赖
Go 标准库 `signal.NotifyContext` 的既有可靠性。

### 15.3 重连没有退避

**修复**：新增 `backoff` 小结构，3s 起步每次翻倍，封顶 30s。`run()` 里只要这次
连接成功注册过（`result.connected`）就 `reset()` 回 3s——长期挂着运行时，偶尔一次
网络抖动不会被罚等 30s；真的持续连不上时才会逐步拉长间隔。

### 新增测试

| 测试 | 覆盖点 |
| --- | --- |
| `TestBackoff` | 3/6/12/24/30/30 的翻倍+封顶序列，reset 后回到 3s |
| `TestSleepCtx` | ctx 已取消时立刻返回 false；正常等待返回 true 且确实等够了时间 |
| `TestGracefulShutdownCallsRPC` | 假 client 断言 `GracefulShutdown` 被调用、grace=3s、ctx 有 ≤5s 超时 |
| `TestGracefulShutdownNoopWithoutClient` | client 为 nil 时不 panic |

全部 11 个测试通过；改动后重新跑了一次真实网络端到端（拨号/TLS/注册/`curl` 取内容），
确认重构没有破坏原有链路。

## 16. 进度

- [x] 协议调研与验证
- [x] go1.26.8 工具链就位
- [x] 代码实现（main.go 341 行 + proxy.go 188 行 + main_test.go 145 行）
- [x] `go vet` / `gofmt` / `go test` 全绿
- [x] 两种模式实测跑通
- [x] 体积优化 18.45 MB -> 12.91 MB（-s -w）-> 4.09 MB（UPX）
- [x] 橙色图标嵌入，加壳后依然完好
- [x] 日志全英文 + 无参数默认分享当前目录，已实测
- [x] 产物命名 trynet-raw.exe（原始）/ trynet.exe（压缩）
- [x] 多层加壳实测无效，结论存档
- [x] 启动 banner + 按平台代理提示 + URL 高亮框，已实测
- [x] 三处健壮性修复：凭据自愈 / 优雅退出 / 重连退避，11 个测试全绿，真实网络回归通过
- [x] http 代理不通时自动从环境挑 socks5 建议、按平台拼运行命令，已实测
