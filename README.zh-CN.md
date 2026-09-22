# trynet

**通过 Cloudflare 边缘网络，把本地端口或目录发布到一个公网 HTTPS 地址——无需 Cloudflare 账号、无需注册、无需开放入站端口。**

[English](README.md)

```
$ trynet

   __                        __
  / /________  ______  ___  / /_
 / __/ ___/ / / / __ \/ _ \/ __/
/ /_/ /  / /_/ / / / /  __/ /_
\__/_/   \__, /_/ /_/\___/\__/
        /____/
  mini cloudflare tunnel  v1.0.0

  ✓ tunnel ready  [lax08]
  ┌───────────────────────────────────────────────────────────┐
  │ https://hydraulic-colleagues-mirror-avi.trycloudflare.com │
  └───────────────────────────────────────────────────────────┘
  serving /home/you/project  ·  via direct

  login  admin / 48310726  ·  upload enabled
```

trynet 是单个静态二进制文件，用 Go 编写，不依赖 CGO。它直接实现了 Cloudflare
快速隧道协议，因此既不调用 `cloudflared`，也不要求系统上装有它。

---

## 快速开始

把当前目录发布到公网：

```sh
trynet
```

发布指定目录：

```sh
trynet -dir ~/Downloads
```

把公网地址转发到本地 HTTP 服务：

```sh
trynet -port 3000
```

每条命令都会输出一个 `https://<随机>.trycloudflare.com` 地址，进程退出前一直可用。

## 安装

从 [releases 页面](https://github.com/BlakeLiAFK/trynet/releases/tag/v1.0.0)下载二进制文件，或从源码构建：

```sh
git clone https://github.com/BlakeLiAFK/trynet.git
cd trynet
go run make.go
```

需要 Go 1.26.8 或更新版本（见 `go.mod`）。`make.go` 使用
`CGO_ENABLED=0`、`-trimpath` 和 `-ldflags="-s -w"`，无需另装 `cloudflared`。
手工交叉编译时，除 `GOOS`、`GOARCH` 外也请设置 `CGO_ENABLED=0`。

一次性构建全部平台到 `dist/`：

```sh
go run make.go -all
```

`make.go` 可选地为 Windows、Linux 加 UPX 壳；压缩后执行 `upx -t`，
加壳失败则回退到未加壳版本并说明原因。macOS 不加 UPX 壳。
体积与平台、架构和编译器有关，实际发布数据写入 `BUILDINFO.json`，不承诺统一的 13 MB → 4 MB。

### v1.0.0 下载与校验

Windows、macOS、Linux 均提供 **amd64（x86-64）** 和 **arm64** 两种构建。
Windows 使用 `.zip`，macOS/Linux 使用 `.tar.gz`。成功加壳的平台额外提供
未加壳的兼容包；各平台的压缩状态、压缩前后实际体积见 `BUILDINFO.json`。
每个包均包含中英文 README、MIT 许可证和第三方依赖许可证。

解压后运行 `./trynet -version`，Windows 运行 `trynet.exe -version`。
下载校验表是 `SHA256SUMS.txt`：Linux 使用 `sha256sum -c SHA256SUMS.txt`，
macOS 使用 `shasum -a 256 <下载的文件>`，PowerShell 使用
`Get-FileHash <下载的文件> -Algorithm SHA256`。Linux 的批量校验命令要求
本地已下载表中全部文件；只下载一个包时，对照该文件对应的校验值即可。

GitHub Actions 在发布前执行代码测试、六平台构建、UPX 完整性检查，
并在对应系统和架构的原生运行器上验证交付的可执行文件。以后发布新版本，
推送 `v主版本.次版本.补丁版本` 标签，或在 **Release** 工作流中手工填写标签。
工作流不会静默移动或覆盖已经发布的标签。

新的 CLI 从 v1.0.0 重新开始；旧的 v1.0.7 等 Wails 应用版本属于此前项目，
不是当前 CLI。请使用上面的 v1.0.0 下载链接。

## 功能

### 目录分享（`-dir`，默认模式）

一套内嵌在二进制里的文件服务：

- 浏览目录、下载文件
- 拖拽上传，逐文件显示进度
- 列表和网格两种排布，图片由服务端生成缩略图
- 单文件分享链接：一个限定范围、会过期的 token，只授权读取某一个文件
- 默认要求登录，密码随机生成
- 适配手机：响应式布局、44px 点击目标、不存在只靠 hover 才出现的功能

### 端口转发（`-port`）

到 `127.0.0.1:<端口>` 的反向代理。请求和响应头、流式响应体均透传。

**只支持普通 HTTP。** WebSocket 升级、TCP 代理和 SSH 均不支持，这类请求会收到
`501 Not Implemented`。

---

## 工作原理

Cloudflare 隧道把通常的客户端/服务端角色反了过来。trynet 主动向 Cloudflare
边缘节点的 7844 端口发起 TLS 连接，然后**在这条出站连接上运行一个 HTTP/2
服务端**。边缘沿着它收到的这条连接下发请求，trynet 从本地源站取数据应答。
没有任何东西监听公网端口，因此不需要入站防火墙规则，也不需要端口映射。

注册过程走 Cap'n Proto RPC 控制流，与 `cloudflared` 使用的是同一套协议。
快速隧道的凭据来自 `https://api.trycloudflare.com/tunnel`，该接口无需认证。

### 代理处理

很多网络环境无法直连边缘。trynet 会探测可用路径，按以下顺序取第一条能通的：

1. 直连
2. `HTTPS_PROXY` / `ALL_PROXY` / `HTTP_PROXY`（及其小写形式）
3. 环境变量里找到的 SOCKS5 代理

走通的那条路会被记住，重连时优先使用。**只有在所有路径都失败时才输出代理诊断
信息**——能正常工作时保持安静。全部失败时会打印当前平台下"设置代理再运行"的
完整命令。

HTTP `CONNECT` 代理经常无法到达 7844 端口，有些甚至返回
`200 Connection established` 之后并不真正转发。这种情况换 SOCKS5 代理通常可解。

### 断线重连

连接总会断。trynet 在多个边缘区域之间轮换，采用指数退避（3 秒起步、每次翻倍、
封顶 30 秒，任意一次注册成功后归零）。**公网主机名在重连过程中保持不变**，
断线不会导致地址变化。

### 快速隧道的有效期

**进程退出后，快速隧道的主机名大约还能被重新认领 12–16 分钟。** 这是实测结果，
上游并未公开：该主机名在第 12 分钟时仍返回 HTTP 530，到第 16 分钟已变为
NXDOMAIN。

trynet 把快速隧道凭据保存在工作目录下的 `.trynet.json`（权限 0600），下次启动
优先复用，因此在这个窗口内重启可以保持同一个地址。实现上并未写死这个时间：
先拿存档去连，只有被边缘拒绝才申请新隧道。`-new` 可强制申请新的。

记得把 `.trynet.json` 加进 `.gitignore`。

---

## 与 cloudflared 的区别

| | trynet | cloudflared | ngrok | localtunnel |
|---|---|---|---|---|
| 是否需要账号 | 否 | 快速隧道不需要 | 需要 | 否 |
| 内置文件服务 | 有 | 无 | 无 | 无 |
| 文件上传 | 支持 | 不支持 | 不支持 | 不支持 |
| 文件服务鉴权 | 默认开启 | 不适用 | 不适用 | 不适用 |
| 固定自有域名 | 支持，全自动（未验证） | 支持，需手工配置 | 支持 | 不支持 |
| 代理自动探测 | 支持 | 仅读环境变量 | 仅读环境变量 | 不支持 |
| WebSocket / TCP / SSH | 不支持 | 支持 | 支持 | 支持 |

*其它项目那几列描述的是它们公开文档里的行为，免费额度也会随时间变化；
在依赖表格里某一格之前，请以它们自己的文档为准。*

trynet 不是 `cloudflared` 的替代品。`cloudflared` 是官方支持的生产级工具，
具备访问策略、负载均衡、ingress 规则、监控指标和托管控制台。trynet 只用尽可能
少的代码实现了其中一条窄路——快速隧道，外加可选的命名隧道——并在上面加了一套
文件服务。

**要跑一个服务，用 `cloudflared`；要把一个地址递给别人，用 trynet。**

---

## 需要 Cloudflare 账号吗

默认模式不需要，快速隧道是匿名的。

只有 `-domain`（发布到你自己的域名）才需要账号和 API token。

---

## 文件服务

### 鉴权

默认开启登录。用户名是 `admin`，密码是 8 位随机数字，启动时打印出来。用数字是
为了方便念给别人听，以及在手机数字键盘上输入。

```sh
trynet -dir ~/share -user alice -pass hunter2
```

会话在服务端保存：登录时用凭据换一个 32 字节的不透明 token，存在
`HttpOnly; Secure; SameSite=Strict` 的 cookie 里，有效期 12 小时且滑动续期。
**不接受 HTTP Basic 鉴权。**

`-public` 完全关闭鉴权，任何拿到地址的人都能访问。

### 分享链接

分享链接只授权读取**一个文件**、有效期**一小时**，且无法用于写入：

```sh
curl -O "https://<主机名>/data/report.pdf?t=<token>"
```

token 与签发时的路径绑定，拿它请求任何其它路径都返回 401。它只对 `GET` 和
`HEAD` 有效，因此分享链接永远不会变成上传入口，而且使用时不会续期。

token 保存在内存中，进程退出即全部失效。目录、被过滤的路径、以及分享目录之外
的路径，一律拒绝签发。

### 上传

开启鉴权时默认接受上传，单文件上限 100 MB。**同名文件不会被覆盖。**

`-read-only` 关闭上传。在 `-public` 下上传默认关闭，必须同时显式给出
`-upload` 才开启——公网可写的目录不该是敲一个参数的副作用。

### 内容过滤

读和写两侧都会过滤，目录列表和下载口径一致，因此被过滤的文件既不会被列出、
也不会被下载，不存在点开就 404 的死链接。

所有以 `.` 开头的路径段都会被过滤，此外还有以下名称（不分大小写）：

```
.git  .svn  .hg  .ssh  .aws  .gnupg  .kube  .docker
.env  .netrc  .npmrc  .pypirc  .htpasswd  .trynet.json
id_rsa  id_dsa  id_ecdsa  id_ed25519  credentials  secring.gpg
```

以及以下扩展名：

```
.pem  .key  .pfx  .p12  .jks  .keystore  .kdbx  .ppk
```

这份名单是**刻意保持简短的**。名单越长越像回事，但它只能挡住有人想得到的东西；
真正的防线是不要把含有密钥的目录分享出去，列太长反而给人虚假的安全感。
`-bypass` 会完全关闭过滤，并打印警告。

### 缩略图

JPEG、PNG、GIF 图片会由服务端生成缩略图：最长边 160px，盒式降采样后重新编码为
JPEG。超过 12 MB 的源文件直接跳过——解码占用的内存是按**像素数**而非文件大小
计算的，而这个进程可能同时还在转发隧道流量。

HEIC、WebP、AVIF 不支持：Go 标准库解不了，而为这几个格式引入第三方依赖不划算。
这些文件会回退显示文件类型图标。

---

## 固定域名（`-domain`）

> **未验证。** 这个模式有单元测试，也有对着假 API 服务器的测试，但**从未对真实
> 的 Cloudflare API 运行过**。请当作实验性功能使用，并到控制台核对它创建了什么。

发布到你自己的域名，而不是随机的 `trycloudflare.com` 主机名：

```sh
export CLOUDFLARE_API_TOKEN=...
trynet -dir ~/share -domain files.example.com
```

trynet 会创建一条命名隧道和一条开启代理的 CNAME 记录，若已存在则复用。
`-teardown` 在退出时删除两者——但**只删 trynet 自己创建的资源，绝不删除它发现
并复用的已有资源**。

API token 需要以下权限：

| 范围 | 权限 |
|---|---|
| 账户 | `Cloudflare Tunnel: Edit` |
| 区域 | `DNS: Edit` |

只有当 token 能访问多个账户时才需要 `-account-id`。

---

## 参数

服务参数同时支持命令行 flag 和环境变量，**flag 优先**。
`-version` 和 `-h` 仅输出本地信息并退出，不会打开隧道。

| Flag | 环境变量 | 默认值 | 说明 |
|---|---|---|---|
| `-version` | — | `false` | 输出版本及提交号后退出 |
| `-dir` | `TRYNET_DIR` | `.` | 要分享的目录 |
| `-port` | `TRYNET_PORT` | — | 转发本地端口，与 `-dir` 二选一 |
| `-user` | `TRYNET_USER` | `admin` | 文件服务用户名 |
| `-pass` | `TRYNET_PASS` | 随机 | 文件服务密码 |
| `-public` | `TRYNET_PUBLIC` | `false` | 完全关闭登录 |
| `-read-only` | `TRYNET_READ_ONLY` | `false` | 拒绝上传 |
| `-upload` | `TRYNET_UPLOAD` | `false` | 在 `-public` 下仍接受上传 |
| `-bypass` | `TRYNET_BYPASS` | `false` | 关闭全部内容过滤 |
| `-new` | `TRYNET_NEW` | `false` | 忽略存档，申请新隧道 |
| `-domain` | `TRYNET_DOMAIN` | — | 要发布到的固定域名 |
| `-api-token` | `CLOUDFLARE_API_TOKEN` | — | Cloudflare API token，`-domain` 必需 |
| `-account-id` | `CLOUDFLARE_ACCOUNT_ID` | — | 账户 id，仅当 token 跨多账户时需要 |
| `-teardown` | `TRYNET_TEARDOWN` | `false` | 退出时删除 `-domain` 创建的资源 |

`-dir` 和 `-port` 互斥。两个都不给时，trynet 分享当前目录。

---

## 常见问题

**把它开到公网上安全吗？**

地址是不可猜测的，文件服务也默认要求登录，但这是一个临时分享工具，不是加固过的
公共服务。不要把它指向含有密钥的目录，也不要让它长时间无人值守地运行。

**为什么我的 HTTP 代理连不上？**

HTTP `CONNECT` 代理经常拒绝转发 7844 端口，或者默默地转发失败——有些会返回
`200 Connection established` 然后什么都不转。改用 SOCKS5 代理即可；所有路径都
失败时，trynet 会从环境变量里找一个 SOCKS5 地址并建议给你。

**能永久保持同一个地址吗？**

快速隧道不行。在约 12–16 分钟内重启会复用存档、保持同一主机名，超过之后该主机名
就被释放了。需要固定地址请用 `-domain` 配合你自己的域名。

**分享链接能配合 curl 和 wget 用吗？**

可以。它就是一个带查询参数的普通 URL，不需要 cookie、请求头或登录。它是只读的，
一小时后失效。

**为什么未加壳的可执行文件较大？**

可执行文件包含 Go 运行时、内嵌 Web UI 和 Cloudflare 隧道相关依赖。
构建时用 `-trimpath -ldflags="-s -w"` 去掉调试信息，再按平台选择是否加 UPX 壳。
具体大小及压缩比例以 Release 附带的 `BUILDINFO.json` 实测值为准。

**支持 WebSocket、TCP、UDP 或 SSH 吗？**

不支持。trynet 只处理普通 HTTP 请求。边缘会用自己的请求头标记 WebSocket 升级和
TCP 代理，trynet 对这类请求返回 `501 Not Implemented`。需要这些能力请用
`cloudflared`。

---

## 致谢

隧道协议的实现参照
[cloudflare/cloudflared](https://github.com/cloudflare/cloudflared)（Apache-2.0），
它仍然是理解快速隧道如何工作的权威参考。

## 许可证

MIT，见 [LICENSE](LICENSE)。
