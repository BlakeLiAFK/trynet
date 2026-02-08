# cloudflared 代理配置深度调研报告

> 调研日期: 2026-02-08
> 调研范围: cloudflared 的 HTTP/SOCKS5 代理支持、环境变量、命令行参数及已知限制

---

## 执行摘要

cloudflared 的代理支持分为两个完全不同的维度，需要严格区分：

1. **出站代理 (Outbound Proxy)** -- cloudflared 自身通过上游代理连接 Cloudflare 边缘节点
2. **入站代理 (Inbound Proxy)** -- cloudflared 作为 SOCKS5 代理服务器，供客户端使用

**核心结论**: 截至 2026-02-08，cloudflared 对出站代理的原生支持极为有限。PR #1514 (2025年8月提交) 试图添加完整的出站代理支持，但尚未合并。社区主要依赖 iptables 透明代理等变通方案。

---

## 一、HTTP 代理支持

### 1.1 出站 HTTP 代理 (cloudflared -> Cloudflare Edge)

**当前状态: 不完整/基本不可用**

cloudflared 代码中存在 `http.ProxyFromEnvironment()` 的调用（主要在 vendored 的 gorilla/websocket 库中），但隧道核心连接逻辑（特别是 QUIC 连接）并不尊重这些代理设置。

| 版本 | 支持情况 | 说明 |
|------|---------|------|
| 2021.3.1 之前 | 完全不支持 | Issue #170 报告 |
| 2021.3.1 (PR #317) | 部分支持 | `cloudflared access` 子命令支持代理；`cloudflared tunnel` 仍不支持 |
| 当前 (2025.x) | 部分支持 | WebSocket 层可能尊重代理设置，但 QUIC (默认协议) 完全忽略代理 |
| PR #1514 (未合并) | 计划完整支持 | HTTP CONNECT + SOCKS4/5 |

**使用 `--protocol http2` 时的代理可能性**:

当强制使用 HTTP/2 协议时，cloudflared 使用 TCP/443，理论上更可能通过企业代理。但即使如此，目前的连接逻辑也不保证会读取代理环境变量。

### 1.2 入站 HTTP 代理 (cloudflared 作为服务端)

cloudflared 本身不提供 HTTP 代理服务器功能。它可以将 HTTP 请求转发到源站服务，但不充当通用 HTTP 代理。

---

## 二、SOCKS5 代理支持

### 2.1 出站 SOCKS5 代理 (cloudflared -> Cloudflare Edge)

**当前状态: 不支持**

Cloudflare 在 Issue #1025 中明确表态：

> "This is something that we don't actually want to support within cloudflared."
> -- Cloudflare 工程师 Joao Oliveirinha

原因：QUIC 协议（cloudflared 的首选传输协议）无法通过 SOCKS 代理工作。

PR #1514 (未合并) 计划通过 `ALL_PROXY` 环境变量和 `golang.org/x/net/proxy` 包支持 SOCKS4/5 出站代理，但仅对 HTTP/2 协议模式有效。

### 2.2 入站 SOCKS5 代理 (cloudflared 作为 SOCKS5 代理服务器)

**当前状态: 支持**

这是 cloudflared 已经正式支持的功能，主要用于 TCP 服务代理（如 kubectl、SSH、RDP）。

**服务端配置方式一: 命令行参数**

```bash
cloudflared tunnel --hostname cluster.site.com \
  --url tcp://kubernetes.docker.internal:6443 \
  --socks5=true
```

**服务端配置方式二: 配置文件 (推荐)**

```yaml
tunnel: <TUNNEL_ID>
credentials-file: /path/to/credentials.json

ingress:
  - hostname: kube.mydomain.com
    service: tcp://kubernetes.internal:6443
    originRequest:
      proxyType: socks
  - service: http_status:404
```

**客户端使用方式:**

```bash
# 启动本地 TCP 转发
cloudflared access tcp --hostname kube.mydomain.com --url 127.0.0.1:1234

# 然后通过 SOCKS5 代理使用
export HTTPS_PROXY=socks5://127.0.0.1:1234
kubectl get pods
```

**Origin 配置参数:**

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `proxyType` | `""` (空) | 代理类型。`"socks"` 启用 SOCKS5 |
| `proxyAddress` | `127.0.0.1` | 代理监听地址（仅本地管理隧道） |
| `proxyPort` | `0` (自动选择) | 代理监听端口（仅本地管理隧道） |

---

## 三、环境变量

### 3.1 当前已有的环境变量 (实际效果有限)

| 环境变量 | 是否被读取 | 实际是否生效 | 说明 |
|----------|-----------|-------------|------|
| `HTTP_PROXY` / `http_proxy` | 代码中有引用 | 对 tunnel 连接不生效 | Go 标准库 `http.ProxyFromEnvironment` 存在于 vendored 代码中 |
| `HTTPS_PROXY` / `https_proxy` | 代码中有引用 | 对 tunnel 连接不生效 | 同上 |
| `ALL_PROXY` / `all_proxy` | 不被读取 | 不生效 | 需要 PR #1514 |
| `NO_PROXY` / `no_proxy` | 不被读取 | 不生效 | 需要 PR #1514 |

### 3.2 PR #1514 规划的环境变量支持 (未合并)

如果 PR #1514 被合并，将支持：

```bash
# HTTP 代理 (支持认证)
export HTTP_PROXY="http://user:pass@proxy.corp.com:8080"
export HTTPS_PROXY="http://user:pass@proxy.corp.com:8080"

# SOCKS 代理
export ALL_PROXY="socks5://proxy.corp.com:1080"

# 启动隧道
cloudflared tunnel run my-tunnel
```

**代理优先级 (PR #1514 设计):**
1. `ALL_PROXY` -- SOCKS 代理（最高优先级）
2. `HTTP_PROXY` / `HTTPS_PROXY` -- HTTP 代理
3. 直连（无代理配置时的回退）

### 3.3 systemd 环境变量变通方案

Issue #87 中有用户报告，对于 `cloudflared proxy-dns` 子命令，通过 systemd 设置环境变量可以工作：

```ini
# /etc/systemd/system/cloudflared.service
[Service]
Environment=http_proxy=[::1]:8080
ExecStart=/usr/bin/cloudflared proxy-dns
```

**注意**: `cloudflared proxy-dns` 命令将在 2026-02-02 之后从新版本中移除。

---

## 四、命令行参数

### 4.1 与代理相关的命令行参数

cloudflared **没有**专门用于配置出站代理的命令行参数。不存在类似 `--proxy`、`--proxy-server`、`--http-proxy` 这样的 flag。

以下是与代理功能间接相关的参数：

| 参数 | 子命令 | 说明 |
|------|--------|------|
| `--protocol http2` | `tunnel run` | 强制使用 HTTP/2 协议，避免 QUIC 的 UDP 限制 |
| `--socks5=true` | `tunnel` (旧版) | 启用 SOCKS5 代理服务器模式 |
| `--edge-ip-version` | `tunnel run` | 指定出站 IP 版本 (4/6/auto) |

### 4.2 配置文件中的代理相关参数

```yaml
# 隧道传输协议（影响是否可通过企业代理）
protocol: http2    # 可选: auto, http2, quic

# 入站 SOCKS5 代理配置
ingress:
  - hostname: app.example.com
    service: tcp://internal-service:port
    originRequest:
      proxyType: socks       # 启用 SOCKS5
      proxyAddress: 127.0.0.1  # 监听地址
      proxyPort: 0             # 监听端口 (0 = 自动)
```

---

## 五、已知限制

### 5.1 核心限制

1. **QUIC 协议不支持代理**: cloudflared 默认使用 QUIC (UDP/7844)，QUIC 无法通过 HTTP CONNECT 或 SOCKS 代理。这是最根本的限制。

2. **无原生出站代理支持**: 没有命令行参数或配置文件选项来指定出站代理。PR #1514 (2025年8月) 试图解决此问题但尚未合并。

3. **Cloudflare 官方立场**: Cloudflare 明确表示不打算在 cloudflared 内部支持出站代理功能。

4. **HTTP/2 回退的局限**: 即使使用 `--protocol http2` 强制 TCP，当前代码也不保证会通过代理环境变量指定的代理服务器连接。

5. **`proxy-dns` 命令移除**: `cloudflared proxy-dns` 将在 2026-02-02 后从新版本移除，原因是底层 DNS 库存在安全漏洞。

6. **DNS 循环依赖**: 如果代理主机名需要 DNS 解析，而 DNS 又需要通过代理，会产生循环依赖。PR #1514 通过让 DNS dialer 使用直连来解决此问题。

### 5.2 网络环境限制

| 网络环境 | QUIC (默认) | HTTP/2 | 通过代理 |
|----------|------------|--------|---------|
| 直连互联网 | 正常 | 正常 | N/A |
| 企业防火墙 (仅 TCP/443) | 失败 | 正常 | 不支持 |
| 需要 HTTP 代理的网络 | 失败 | 可能失败 | 不支持 |
| 大陆网络 (需翻墙) | 不稳定 | 较稳定 | 需变通方案 |

### 5.3 性能限制

- HTTP/2 在 CPU 受限环境中比 QUIC 更快
- QUIC 在非受限环境中平均响应时间优于 HTTP/2 约 16.6%
- 使用透明代理方案会增加额外的延迟和复杂性

---

## 六、变通方案

### 6.1 方案一: iptables 透明代理 (推荐 - 仅 Linux)

这是社区中最成熟的方案，使用 iptables 将 cloudflared 的出站流量重定向到本地代理。

```bash
# 必须使用 HTTP/2 协议
# cloudflared config:
# protocol: http2

# Cloudflare 边缘节点 IP 段
CF_IPS="198.41.192.0/24 198.41.200.0/24"

# 本地透明代理端口 (V2Ray/Xray 监听)
PROXY_PORT=12345

# 添加 iptables 规则
for ip in $CF_IPS; do
  iptables -t nat -A OUTPUT -d $ip -p tcp -j REDIRECT --to-port $PROXY_PORT
done
```

配合 V2Ray/Xray 的 dokodemo-door 入站协议：

```json
{
  "inbounds": [{
    "port": 12345,
    "protocol": "dokodemo-door",
    "settings": {
      "network": "tcp",
      "followRedirect": true
    },
    "streamSettings": {
      "sockopt": {
        "tproxy": "redirect"
      }
    }
  }]
}
```

参考项目: https://github.com/0xMashiro/CF-Tunnel-Transparent-Proxy

### 6.2 方案二: proxychains (简单但不稳定)

```bash
# 安装 proxychains
# macOS: brew install proxychains-ng
# Linux: apt install proxychains4

# 配置 /etc/proxychains.conf
# socks5 127.0.0.1 1080

# 强制使用 HTTP/2 并通过 proxychains 启动
proxychains4 cloudflared tunnel run --protocol http2 my-tunnel
```

**注意**: proxychains 使用 LD_PRELOAD 钩子，对 Go 静态编译的二进制文件可能无效。

### 6.3 方案三: 路由器级代理

在路由器上配置策略路由，将目标为 Cloudflare 边缘节点 IP 的流量转发到代理服务器。这对 cloudflared 完全透明。

### 6.4 方案四: 等待 PR #1514 合并或自行构建

```bash
# 从 PR #1514 分支构建
git clone https://github.com/cloudflare/cloudflared.git
cd cloudflared
git fetch origin pull/1514/head:proxy-support
git checkout proxy-support
make cloudflared
```

---

## 七、对比评估矩阵

| 维度 | 原生支持 (当前) | PR #1514 (未合并) | iptables 透明代理 | proxychains |
|------|----------------|------------------|-------------------|-------------|
| HTTP 代理 | 不支持 | 支持 | 支持 | 不确定 |
| SOCKS5 代理 | 不支持 | 支持 | 支持 | 不确定 |
| QUIC 协议兼容 | N/A | 仅 HTTP/2 | 仅 HTTP/2 | 仅 HTTP/2 |
| 认证支持 | N/A | Basic Auth | 取决于代理 | 支持 |
| 稳定性 | N/A | 未验证 | 高 (社区验证) | 低 |
| 复杂度 | N/A | 低 | 中 | 低 |
| 平台支持 | N/A | 全平台 | 仅 Linux | Linux/macOS |
| 维护成本 | N/A | 跟随官方 | 需维护 IP 列表 | 低 |

---

## 八、关键信息源

### GitHub Issues
- Issue #87: SOCKS5/HTTP 代理配置 (2019, 已关闭)
- Issue #110: 通过 HTTP 代理连接 Cloudflare (2019)
- Issue #170: 不使用代理环境变量 (2020, 已在 2021.3.1 修复 access 子命令)
- Issue #350: 通过代理运行隧道 (2020)
- Issue #1025: 边缘节点代理支持请求 (2023, Cloudflare 拒绝)

### Pull Requests
- PR #317: 为 access 子命令添加代理支持 (2021, 已合并)
- PR #1514: 为 tunnel 连接添加 HTTP 代理支持 (2025年8月, 未合并)

### 官方文档
- Origin 配置参数: proxyType / proxyAddress / proxyPort
- Tunnel run 参数: --protocol
- proxy-dns 命令移除公告 (2026-02-02)

---

## 九、建议

1. **短期方案**: 如果在企业代理环境中，使用 `--protocol http2` 并配合 iptables 透明代理（Linux）或路由器级代理。

2. **持续关注**: 关注 PR #1514 的合并状态。如果被合并，将是最优雅的解决方案。

3. **自行构建**: 如果急需此功能，可从 PR #1514 分支自行构建 cloudflared 二进制文件。

4. **协议选择**: 在受限网络环境中，始终使用 `--protocol http2` 或在配置文件中设置 `protocol: http2`，避免 QUIC 的 UDP 流量被阻断。

5. **风险评估**: cloudflared 隧道流量在 Cloudflare 边缘节点处会被解密，不是端到端加密。敏感应用应在隧道内使用额外的加密层（如 SSH、TLS）。
