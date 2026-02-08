# Cloudflare Quick Tunnels (TryCloudflare) 技术原理深度调研

> 调研日期: 2026-02-08
> 调研范围: cloudflared 客户端、Cloudflare 边缘网络、Quick Tunnel 完整生命周期

---

## 一、执行摘要

Cloudflare Quick Tunnels 是一种无需账户认证的临时隧道服务，用户只需执行 `cloudflared tunnel --url localhost:PORT` 即可将本地服务暴露到公网，获得一个随机的 `xxx.trycloudflare.com` 域名。

核心架构：cloudflared 客户端通过 QUIC（首选）或 HTTP/2 协议向 Cloudflare 边缘网络发起出站连接，Cloudflare Worker 负责隧道生命周期管理和随机子域名分配，Cloudflare 权威 DNS 负责域名解析映射。

---

## 二、Quick Tunnel 创建流程详解

### 2.1 触发条件

当用户执行 `cloudflared tunnel --url http://localhost:8080` 且未提供账户凭证时，cloudflared 检测到以下条件成立，进入 Quick Tunnel 流程：

1. 未设置 `proxy-dns` 标志
2. `quick-service` 字符串非空（默认为 `https://api.trycloudflare.com`）
3. `shouldRunQuickTunnel` 返回 true（未提供 tunnel credentials）

### 2.2 API 请求（客户端侧）

源码位置：`cmd/cloudflared/tunnel/quick_tunnel.go`

```
RunQuickTunnel(sc *subcommandContext) error
```

关键步骤：

1. 创建 HTTP 客户端（15 秒超时）
2. 发送 POST 请求到 `https://api.trycloudflare.com/tunnel`
3. 解析 JSON 响应，获取 `QuickTunnelResponse` 结构体

响应包含的数据结构：

```
QuickTunnel {
    ID        string   // 隧道 UUID
    Name      string   // 隧道名称
    Hostname  string   // 随机子域名，如 seasonal-deck-organisms-sf.trycloudflare.com
    AccountTag string  // Cloudflare 内部账户标识
    Secret    []byte   // 隧道密钥（用于后续连接认证）
}
```

4. 将 ID 解析为 UUID，构建连接凭证 (credentials)
5. 强制设置协议为 `quic`，连接数限制为 1
6. 启动隧道服务器

### 2.3 服务端处理（Cloudflare Worker）

API 端点 `api.trycloudflare.com` 背后运行的是一个 Cloudflare Worker，处理流程：

1. **接收请求**：Worker 收到创建 Quick Tunnel 的 POST 请求
2. **生成子域名**：Worker 在服务端生成随机子域名（格式为多个英文单词用连字符连接，如 `seasonal-deck-organisms-sf`）
3. **创建隧道记录**：分配 Tunnel UUID、生成认证密钥
4. **返回凭证**：将完整的隧道信息返回给 cloudflared 客户端

关键点：**随机子域名是在 Cloudflare 服务端生成的，不是客户端生成的。**

### 2.4 DNS 记录创建

Worker 创建隧道后，一个运行在 Cloudflare 边缘的互补服务接收到子域名和 cloudflared 实例的标识号，然后：

1. 在 Cloudflare 权威 DNS 中创建 DNS 记录
2. 将随机子域名映射到特定的 Tunnel（类似于 `<UUID>.cfargotunnel.com` 的 CNAME 映射）
3. DNS 记录立即生效（因为 Cloudflare 同时是 trycloudflare.com 的权威 DNS 服务器）

### 2.5 清理机制

Quick Tunnel Worker 使用 **Workers Cron Trigger** 定期执行清理：

- 定时扫描所有 Quick Tunnel
- 发现断开连接超过 5 分钟的隧道，标记为废弃
- 删除废弃隧道及其关联的 DNS 记录

---

## 三、网络连接架构

### 3.1 边缘发现（Edge Discovery）

cloudflared 启动后首先执行边缘 IP 发现：

**SRV 记录查询：**

```
查询: _v2-origintunneld._tcp.argotunnel.com
返回:
  - region1.v2.argotunnel.com (优先级 1, 端口 7844)
  - region2.v2.argotunnel.com (优先级 2, 端口 7844)
```

源码常量（`edgediscovery/allregions/discovery.go`）：

```
srvService = "v2-origintunneld"
srvProto   = "tcp"
srvName    = "argotunnel.com"
```

**A 记录解析：**

- `region1.v2.argotunnel.com` 解析为多个 IPv4 地址（198.41.200.x 范围）
- `region2.v2.argotunnel.com` 解析为另一组 IPv4 地址
- 同时解析 IPv6 地址（2606:4700:a0::* 和 2606:4700:a8::* 范围）

**DNS-over-TLS 回退：**

当标准 DNS 解析失败时，cloudflared 使用 DoT 回退机制：
- 连接 `1.1.1.1:853`（TLS，server name: `cloudflare-dns.com`）
- 15 秒超时

### 3.2 协议选择

cloudflared 支持两种边缘连接协议：

| 特性 | QUIC | HTTP/2 |
|------|------|--------|
| 传输层 | UDP | TCP |
| 端口 | 7844 | 7844 |
| 队头阻塞 | 无 | 有 |
| 默认优先级 | 首选 | 回退 |
| 后量子加密 | 支持 | 不支持 |
| UDP/ICMP 代理 | 支持（Datagram） | 不支持 |
| 连接迁移 | 支持 | 不支持 |

**Quick Tunnel 强制使用 QUIC 协议，连接数限制为 1。**

普通 Named Tunnel 默认也优先 QUIC，但可配置为 HTTP/2，且默认建立 4 个连接。

### 3.3 连接建立过程

```
                         端口 7844 (QUIC/UDP)
[cloudflared] ---------> [Cloudflare Edge DC-1]
                    \
                     `--> [Cloudflare Edge DC-2]
                         端口 7844 (QUIC/UDP)
```

**Named Tunnel 高可用设计：**
- Supervisor 管理 4 个并发连接
- 连接分布到 2 个不同的 Cloudflare 数据中心
- 交错启动（1 秒延迟），防止惊群效应
- 独立退避策略，某个连接失败不影响其他连接

**Quick Tunnel 简化设计：**
- 仅建立 1 个连接
- QUIC 协议
- 无高可用保障

### 3.4 QUIC 连接细节

每个 QUIC 连接包含：

1. **控制流（Control Stream）**：连接建立后的第一个 QUIC 流
   - 通过 RPC 注册隧道
   - 支持动态配置更新（无需重连）
   - 协调优雅关闭

2. **数据流（Data Streams）**：承载实际流量
   - HTTP/WebSocket 请求通过独立的 QUIC 流传输
   - 每个流独立，互不阻塞
   - TCP 流量通过 WARP routing 传输

3. **数据报（Datagrams）**：用于 UDP/ICMP 代理
   - V2 协议：通过 RPC 注册 UDP 会话，用 session ID 跟踪
   - V3 协议：在数据报头部直接嵌入会话信息，减少延迟

---

## 四、流量路由完整链路

### 4.1 请求流程（从用户到本地服务）

```
[用户浏览器]
    |
    | HTTPS 请求 seasonal-deck-organisms-sf.trycloudflare.com
    v
[Cloudflare DNS]
    |
    | 解析到最近的 Cloudflare 边缘节点 (Anycast)
    v
[Cloudflare Edge PoP] (全球 300+ 数据中心)
    |
    | 1. TLS 终止
    | 2. WAF/DDoS 防护
    | 3. 查找隧道路由映射
    | 4. 通过已建立的 QUIC 连接转发请求
    v
[cloudflared 客户端]
    |
    | 1. 接收 QUIC 流
    | 2. Ingress 路由匹配（hostname + path）
    | 3. 代理到本地服务
    v
[本地服务 localhost:8080]
```

### 4.2 Ingress 路由

cloudflared 内部的请求处理管道：

1. **流接收**：从 QUIC 流或 HTTP/2 流接收入站请求
2. **Ingress 匹配**：根据 hostname 和 path 规则选择目标 origin 服务
3. **Origin 代理**：协议特定的处理器管理 HTTP、WebSocket、TCP、UDP、ICMP 流量
4. **响应回传**：通过同一连接将响应发送回边缘

### 4.3 DNS 处理机制

**trycloudflare.com 域名：**
- Cloudflare 自身是 trycloudflare.com 的权威 DNS 服务器
- Quick Tunnel Worker 创建隧道时，通过内部 API 直接写入 DNS 记录
- 无需等待 DNS 传播，记录即时生效
- 清理 Worker 定期删除废弃隧道的 DNS 记录

**Named Tunnel 的 cfargotunnel.com：**
- Named Tunnel 创建时生成 `<UUID>.cfargotunnel.com` 记录
- 用户通过 CNAME 将自定义域名指向 `<UUID>.cfargotunnel.com`
- DNS 记录独立于隧道状态存在（隧道停止，DNS 记录不自动删除）
- 隧道停止时访问会显示 1016 错误

---

## 五、Quick Tunnel vs Named Tunnel 对比

| 维度 | Quick Tunnel | Named Tunnel |
|------|-------------|--------------|
| **账户要求** | 无需账户 | 需要 Cloudflare 账户 |
| **认证方式** | 无 | cert.pem + UUID.json 双文件 |
| **域名** | 随机 trycloudflare.com 子域名 | 自定义域名 |
| **持久性** | 临时（每次重启域名变化） | 永久（UUID 不变） |
| **并发请求限制** | 200 个在途请求 | 无硬性限制 |
| **连接数** | 1 个 QUIC 连接 | 4 个连接（2 个数据中心） |
| **协议** | 强制 QUIC | QUIC/HTTP2 可选 |
| **SLA** | 无保障 | 生产级保障 |
| **SSE 支持** | 不支持 | 支持 |
| **Zero Trust/Access** | 不可用 | 完整支持 |
| **负载均衡** | 不可用 | 支持 |
| **Argo Smart Routing** | 使用 | 使用 |
| **自动清理** | 断开 5 分钟后自动清理 | 手动删除 |
| **用途** | 测试和开发 | 开发到生产全场景 |
| **后量子加密** | 支持（QUIC 默认） | QUIC 模式支持 |

---

## 六、安全性分析

### 6.1 Quick Tunnel 的安全特性

- **出站连接**：cloudflared 仅发起出站连接，无需开放任何入站端口
- **TLS 加密**：所有边缘连接均通过 TLS 加密（QUIC 自带 TLS 1.3）
- **后量子加密**：QUIC 连接默认启用后量子密码学
- **临时凭证**：API 返回的 Secret 仅在隧道生命周期内有效

### 6.2 Quick Tunnel 的安全风险

- **无访问控制**：任何人知道 URL 即可访问
- **无 WAF 高级规则**：无法配置自定义安全规则
- **被滥用风险**：安全研究已发现 Quick Tunnel 被恶意软件用于 C2 通信（因为无需认证）
- **无审计日志**：无法追踪访问记录

### 6.3 Named Tunnel 的安全增强

- **Zero Trust 集成**：支持 Cloudflare Access 身份验证
- **双文件凭证系统**：
  - `cert.pem`：账户级证书，用于创建/删除隧道
  - `<UUID>.json`：隧道级凭证，仅允许运行特定隧道
- **RBAC**：管理员可仅分享隧道凭证而非账户证书

---

## 七、cloudflared 与 Cloudflare Edge 的关系

### 7.1 架构分层

```
+------------------------------------------+
|           cloudflared 客户端              |
+------------------------------------------+
| CLI 层     | 命令解析、参数处理           |
| 守护进程层  | Supervisor、连接管理         |
| 连接层     | QUIC/HTTP2 协议抽象          |
| 数据面     | HTTP/WS/TCP/UDP/ICMP 代理    |
+------------------------------------------+
        |  出站连接 (端口 7844)
        v
+------------------------------------------+
|         Cloudflare Edge Network          |
+------------------------------------------+
| 边缘接入   | Anycast、TLS 终止、DDoS 防护 |
| 隧道路由   | UUID 映射、Ingress 匹配      |
| DNS 服务   | 权威 DNS、记录管理           |
| Worker 运行时 | Quick Tunnel 生命周期管理    |
| Smart Routing | Argo 智能路由优化           |
+------------------------------------------+
```

### 7.2 Supervisor 核心职责

- 管理所有到边缘的 HA 连接
- 协议选择和回退（QUIC -> HTTP/2）
- 协议粘性（一旦某协议成功，后续连接优先使用同一协议）
- 连接失败时的退避重连
- 边缘 IP 轮换（特定失败条件下切换边缘 IP）
- 优雅关闭协调

### 7.3 TunnelConnection 接口

cloudflared 通过 `TunnelConnection` 接口抽象连接处理：

- QUIC 实现和 HTTP/2 实现均遵循相同接口
- 支持统一的请求处理流程
- 控制流和数据流分离
- 协议无关的 Ingress 路由

---

## 八、技术实现细节补充

### 8.1 QUIC 的 UDP 源地址问题

Cloudflare 在实现 QUIC 支持时遇到的关键问题：

当边缘服务器有多个 IP 绑定到同一网络接口时，UDP 的无连接特性导致内核可能选择错误的源 IP 发送响应。

解决方案：使用 `recvmsg()` 和 `sendmsg()` 系统调用，通过 `IP_PKTINFO` 控制消息显式指定源地址。

### 8.2 h2mux 的历史

cloudflared 早期使用自定义的 h2mux 协议（基于 HTTP/2 帧的多路复用），后来先迁移到标准 HTTP/2，再迁移到 QUIC。h2mux 已被废弃。

协议演进路线：h2mux -> HTTP/2 -> QUIC（当前默认）

### 8.3 连接预检

cloudflared 在建立隧道前执行连接预检：
- 验证到边缘的网络连通性
- 检测 UDP（QUIC）是否可达
- 如果 UDP 被阻断，自动回退到 HTTP/2（TCP）

---

## 九、总结

Cloudflare Quick Tunnels 的核心设计哲学是**零摩擦接入**：无账户、无配置、单命令即可将本地服务暴露到公网。其技术实现依赖以下关键组件的协作：

1. **cloudflared 客户端**：Go 语言实现的轻量级守护进程，通过 QUIC 建立出站隧道连接
2. **Cloudflare Worker**：无服务器计算，处理隧道创建、子域名生成、生命周期管理
3. **Cloudflare 权威 DNS**：即时创建和清理 trycloudflare.com 子域名记录
4. **Cloudflare 边缘网络**：全球 300+ PoP，Anycast 路由，流量代理和安全防护
5. **Argo Smart Routing**：优化流量在 Cloudflare 骨干网内的路由路径

Quick Tunnel 适合开发测试场景，生产环境应使用 Named Tunnel 以获得持久域名、高可用连接、访问控制和 SLA 保障。

---

## 参考来源

- Cloudflare 官方文档：Quick Tunnels
- Cloudflare 官方博客：Quick Tunnels Anytime Anywhere
- Cloudflare 官方博客：Getting Tunnels to Connect with QUIC
- cloudflared 源码：quick_tunnel.go
- cloudflared 源码：edgediscovery/allregions/discovery.go
- DeepWiki：cloudflared Architecture Overview
- Cloudflare 官方文档：Tunnel DNS Records
- Cloudflare 官方文档：Tunnel Run Parameters
