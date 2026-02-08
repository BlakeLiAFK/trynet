# cloudflared Prometheus Metrics 深度调研报告

> 调研时间: 2026-02-08
> 信息来源: cloudflare/cloudflared GitHub 源码 + 官方文档

---

## 一、Metrics 端点概述

cloudflared 启动隧道时会自动启动一个 HTTP 服务器，暴露 Prometheus 格式的指标。

### 1.1 端点地址

| 场景 | 默认地址 |
|------|---------|
| 非容器环境 | `127.0.0.1:<PORT>/metrics` |
| 容器环境(Docker/K8s) | `0.0.0.0:<PORT>/metrics` |
| 指定 `--metrics 127.0.0.1:0` | 绑定随机可用端口 |

**端口选择逻辑**(源码 `metrics/metrics.go` - `CreateMetricsListener`):
1. 默认地址时，依次尝试 20241, 20242, 20243, 20244, 20245
2. 全部占用则 fallback 到随机端口
3. 指定 `--metrics` 时直接绑定指定地址

### 1.2 端点路由

| 路径 | 功能 |
|------|------|
| `/metrics` | Prometheus 指标 |
| `/healthcheck` | 健康检查 |
| `/ready` | 就绪检查(如配置) |
| `/debug/` | pprof 调试 |
| `/quicktunnel` | Quick tunnel 主机名 |
| `/config` | Orchestrator 配置(如可用) |

### 1.3 启动日志格式

cloudflared 使用 zerolog，启动时输出到 stderr:

**文本格式(默认控制台)**:
```
2024-12-19T21:17:58Z INF Starting metrics server on 127.0.0.1:20241/metrics
```

**JSON 格式(logfile 或配置 JSON 输出时)**:
```json
{"level":"info","time":"2024-12-19T21:17:58Z","message":"Starting metrics server on 127.0.0.1:20241/metrics"}
```

源码格式化方式:
```go
log.Info().Msgf("Starting metrics server on %s/metrics", l.Addr())
```

---

## 二、完整指标列表

以下基于 cloudflared 源码中 4 个核心指标文件整理。

### 2.1 隧道核心指标 (proxy/metrics.go)

| 完整名称 | 类型 | 标签 | 说明 |
|----------|------|------|------|
| `cloudflared_tunnel_total_requests` | Counter | - | 所有隧道代理的请求总数 |
| `cloudflared_tunnel_concurrent_requests_per_tunnel` | Gauge | - | 当前每条隧道的并发请求数 |
| `cloudflared_tunnel_response_by_code` | Counter | `status_code` | 按 HTTP 状态码统计的响应数 |
| `cloudflared_tunnel_request_errors` | Counter | - | 代理到源站的错误总数 |

### 2.2 TCP 会话指标 (proxy/metrics.go)

| 完整名称 | 类型 | 标签 | 说明 |
|----------|------|------|------|
| `cloudflared_tcp_active_sessions` | Gauge | - | 当前活跃 TCP 会话数 |
| `cloudflared_tcp_total_sessions` | Counter | - | TCP 会话累计总数 |

### 2.3 代理连接指标 (proxy/metrics.go)

| 完整名称 | 类型 | 标签 | 说明 |
|----------|------|------|------|
| `cloudflared_proxy_connect_latency` | Histogram | - | 连接建立和确认延迟(ms)，桶: 1,10,25,50,100,500,1000,5000 |
| `cloudflared_proxy_connect_streams_errors` | Counter | - | 连接建立失败总数 |

### 2.4 连接管理指标 (connection/metrics.go)

| 完整名称 | 类型 | 标签 | 说明 |
|----------|------|------|------|
| `cloudflared_tunnel_max_concurrent_requests_per_tunnel` | Gauge | `connection_id` | 每条隧道历史最大并发请求数 |
| `cloudflared_tunnel_server_locations` | Gauge | `connection_id`, `edge_location` | 隧道连接的边缘节点位置(1=当前, 0=历史) |
| `cloudflared_tunnel_tunnel_rpc_fail` | Counter | `error`, `rpcName` | RPC 连接错误数(按类型) |
| `cloudflared_tunnel_tunnel_register_fail` | Counter | `error`, `rpcName` | 隧道注册错误数(按类型) |
| `cloudflared_tunnel_user_hostnames_counts` | Counter | `userHostname` | 服务的用户主机名统计 |
| `cloudflared_tunnel_tunnel_register_success` | Counter | `rpcName` | 隧道注册成功数 |

### 2.5 配置推送指标 (connection/metrics.go)

| 完整名称 | 类型 | 标签 | 说明 |
|----------|------|------|------|
| `cloudflared_config_local_config_pushes` | Counter | - | 本地配置推送到边缘的次数 |
| `cloudflared_config_local_config_pushes_errors` | Counter | - | 本地配置推送错误次数 |

### 2.6 HA 连接指标 (supervisor/metrics.go)

| 完整名称 | 类型 | 标签 | 说明 |
|----------|------|------|------|
| `cloudflared_tunnel_ha_connections` | Gauge | - | 当前活跃的高可用连接数 |

### 2.7 QUIC 传输指标 (quic/metrics.go)

| 完整名称 | 类型 | 标签 | 说明 |
|----------|------|------|------|
| `quic_client_total_connections` | Counter | - | 发起的连接总数 |
| `quic_client_closed_connections` | Counter | - | 已关闭的连接数 |
| `quic_client_max_udp_payload` | Gauge | `conn_index` | 最大 UDP 负载大小(bytes) |
| `quic_client_sent_frames` | Counter | `conn_index`, `frame_type` | 发送的帧数 |
| `quic_client_sent_bytes` | Counter | `conn_index` | 发送的字节数 |
| `quic_client_received_frames` | Counter | `conn_index`, `frame_type` | 接收的帧数 |
| `quic_client_receive_bytes` | Counter | `conn_index` | 接收的字节数 |
| `quic_client_buffered_packets` | Counter | `conn_index`, `packet_type` | 缓冲的包数 |
| `quic_client_dropped_packets` | Counter | `conn_index`, `packet_type`, `reason` | 丢弃的包数 |
| `quic_client_lost_packets` | Counter | `conn_index`, `reason` | 丢失的包数 |
| `quic_client_min_rtt` | Gauge | `conn_index` | 最小 RTT(ms) |
| `quic_client_latest_rtt` | Gauge | `conn_index` | 最新 RTT(ms) |
| `quic_client_smoothed_rtt` | Gauge | `conn_index` | 平滑 RTT(ms) |
| `quic_client_mtu` | Gauge | `conn_index` | 当前 MTU |
| `quic_client_congestion_window` | Gauge | `conn_index` | 拥塞窗口大小 |
| `quic_client_congestion_state` | Gauge | `conn_index` | 拥塞状态 |
| `quic_client_packet_too_big_dropped` | Counter | - | 因过大被丢弃的源站包数 |

### 2.8 构建信息 (metrics/metrics.go)

| 完整名称 | 类型 | 标签 | 说明 |
|----------|------|------|------|
| `build_info` | Gauge | `goversion`, `type`, `revision`, `version` | 构建版本信息(固定值 1) |

### 2.9 Prometheus 内置指标

Metrics 端点还会自动暴露:

| 名称前缀 | 说明 |
|----------|------|
| `go_*` | Go 运行时指标(goroutine 数、GC、内存等) |
| `process_*` | 进程级指标(CPU、内存、文件描述符) |
| `promhttp_*` | Prometheus HTTP handler 自身指标 |

---

## 三、从 stderr 解析 Metrics 端口

### 3.1 日志行匹配

cloudflared 启动时在 stderr 输出:
```
2026-02-08T12:00:00Z INF Starting metrics server on 127.0.0.1:38291/metrics
```

使用 `--metrics 127.0.0.1:0` 时端口由操作系统分配，必须从日志中解析。

### 3.2 Go 解析方案

```go
package tunnel

import (
    "bufio"
    "fmt"
    "io"
    "net"
    "strings"
)

// ParseMetricsAddr 从 cloudflared 的 stderr 流中实时解析 metrics 地址。
// 返回 "host:port" 格式的地址。
func ParseMetricsAddr(stderr io.Reader) (string, error) {
    const prefix = "Starting metrics server on "
    scanner := bufio.NewScanner(stderr)
    for scanner.Scan() {
        line := scanner.Text()
        idx := strings.Index(line, prefix)
        if idx == -1 {
            continue
        }
        // 提取 "127.0.0.1:38291/metrics" 部分
        addr := line[idx+len(prefix):]
        // 去掉 "/metrics" 后缀
        addr = strings.TrimSuffix(addr, "/metrics")
        // 验证地址格式
        _, _, err := net.SplitHostPort(addr)
        if err != nil {
            return "", fmt.Errorf("invalid metrics address %q: %w", addr, err)
        }
        return addr, nil
    }
    if err := scanner.Err(); err != nil {
        return "", fmt.Errorf("reading stderr: %w", err)
    }
    return "", fmt.Errorf("metrics server address not found in output")
}
```

### 3.3 启动 cloudflared 并捕获端口的完整流程

```go
package tunnel

import (
    "context"
    "fmt"
    "io"
    "os/exec"
)

// StartWithMetrics 启动 cloudflared 并返回 metrics 地址。
func StartWithMetrics(ctx context.Context, args []string) (metricsAddr string, cmd *exec.Cmd, err error) {
    fullArgs := append([]string{"tunnel", "--metrics", "127.0.0.1:0"}, args...)
    cmd = exec.CommandContext(ctx, "cloudflared", fullArgs...)

    stderr, err := cmd.StderrPipe()
    if err != nil {
        return "", nil, fmt.Errorf("creating stderr pipe: %w", err)
    }

    if err := cmd.Start(); err != nil {
        return "", nil, fmt.Errorf("starting cloudflared: %w", err)
    }

    // 在另一个 goroutine 中持续读取 stderr，避免阻塞
    addrCh := make(chan string, 1)
    errCh := make(chan error, 1)
    go func() {
        addr, parseErr := ParseMetricsAddr(stderr)
        if parseErr != nil {
            errCh <- parseErr
            return
        }
        addrCh <- addr
        // 继续消费 stderr 避免进程阻塞
        io.Copy(io.Discard, stderr)
    }()

    select {
    case addr := <-addrCh:
        return addr, cmd, nil
    case parseErr := <-errCh:
        cmd.Process.Kill()
        return "", nil, parseErr
    case <-ctx.Done():
        cmd.Process.Kill()
        return "", nil, ctx.Err()
    }
}
```

---

## 四、Go 代码获取和解析 Prometheus 指标

### 4.1 依赖

```
go get github.com/prometheus/common@latest
go get github.com/prometheus/client_model@latest
```

### 4.2 HTTP 获取 + 解析

```go
package tunnel

import (
    "fmt"
    "net/http"
    "time"

    dto "github.com/prometheus/client_model/go"
    "github.com/prometheus/common/expfmt"
)

// MetricsClient 用于获取和解析 cloudflared 的 Prometheus 指标。
type MetricsClient struct {
    metricsURL string
    httpClient *http.Client
}

func NewMetricsClient(metricsAddr string) *MetricsClient {
    return &MetricsClient{
        metricsURL: fmt.Sprintf("http://%s/metrics", metricsAddr),
        httpClient: &http.Client{Timeout: 5 * time.Second},
    }
}

// Fetch 获取并解析所有指标，返回 map[指标名]*MetricFamily。
func (c *MetricsClient) Fetch() (map[string]*dto.MetricFamily, error) {
    resp, err := c.httpClient.Get(c.metricsURL)
    if err != nil {
        return nil, fmt.Errorf("fetching metrics: %w", err)
    }
    defer resp.Body.Close()

    if resp.StatusCode != http.StatusOK {
        return nil, fmt.Errorf("metrics returned status %d", resp.StatusCode)
    }

    // 根据 Content-Type 选择解析器
    mediaType, params := expfmt.ResponseFormat(resp)
    decoder := expfmt.NewDecoder(resp.Body, expfmt.NewFormat(mediaType))

    families := make(map[string]*dto.MetricFamily)
    for {
        var mf dto.MetricFamily
        if err := decoder.Decode(&mf); err != nil {
            break // EOF 或解析结束
        }
        families[mf.GetName()] = &mf
    }
    // 注意: params 可能用于未来的版本协商
    _ = params

    return families, nil
}
```

**备注**: 上面的 `expfmt.ResponseFormat` 是较新版本的 API。如果使用旧版本，可以用更简单的 TextParser:

```go
// 简化版：直接使用 TextParser（兼容性更好）
func (c *MetricsClient) FetchSimple() (map[string]*dto.MetricFamily, error) {
    resp, err := c.httpClient.Get(c.metricsURL)
    if err != nil {
        return nil, fmt.Errorf("fetching metrics: %w", err)
    }
    defer resp.Body.Close()

    var parser expfmt.TextParser
    families, err := parser.TextToMetricFamilies(resp.Body)
    if err != nil {
        return nil, fmt.Errorf("parsing metrics: %w", err)
    }
    return families, nil
}
```

### 4.3 提取具体指标值

```go
package tunnel

import (
    dto "github.com/prometheus/client_model/go"
)

// TunnelStats 从 Prometheus 指标中提取的隧道状态摘要。
type TunnelStats struct {
    TotalRequests   float64 // 总请求数
    ActiveRequests  float64 // 当前并发请求
    RequestErrors   float64 // 请求错误数
    HAConnections   float64 // HA 连接数
    ActiveSessions  float64 // TCP 活跃会话
    TotalSessions   float64 // TCP 会话总数
    LatestRTT       map[string]float64 // conn_index -> RTT(ms)
    SentBytes       map[string]float64 // conn_index -> bytes
    ReceivedBytes   map[string]float64 // conn_index -> bytes
    ResponseCodes   map[string]float64 // status_code -> count
    ServerLocations map[string]string  // connection_id -> edge_location
}

// ParseTunnelStats 从指标族中提取关键隧道状态。
func ParseTunnelStats(families map[string]*dto.MetricFamily) *TunnelStats {
    stats := &TunnelStats{
        LatestRTT:       make(map[string]float64),
        SentBytes:       make(map[string]float64),
        ReceivedBytes:   make(map[string]float64),
        ResponseCodes:   make(map[string]float64),
        ServerLocations: make(map[string]string),
    }

    // Counter 值
    stats.TotalRequests = getCounterValue(families, "cloudflared_tunnel_total_requests")
    stats.RequestErrors = getCounterValue(families, "cloudflared_tunnel_request_errors")
    stats.TotalSessions = getCounterValue(families, "cloudflared_tcp_total_sessions")

    // Gauge 值
    stats.ActiveRequests = getGaugeValue(families, "cloudflared_tunnel_concurrent_requests_per_tunnel")
    stats.HAConnections = getGaugeValue(families, "cloudflared_tunnel_ha_connections")
    stats.ActiveSessions = getGaugeValue(families, "cloudflared_tcp_active_sessions")

    // 带标签的 Counter: response_by_code
    if mf, ok := families["cloudflared_tunnel_response_by_code"]; ok {
        for _, m := range mf.GetMetric() {
            code := getLabelValue(m, "status_code")
            if code != "" {
                stats.ResponseCodes[code] = m.GetCounter().GetValue()
            }
        }
    }

    // 带标签的 Gauge: QUIC RTT
    if mf, ok := families["quic_client_latest_rtt"]; ok {
        for _, m := range mf.GetMetric() {
            connIdx := getLabelValue(m, "conn_index")
            stats.LatestRTT[connIdx] = m.GetGauge().GetValue()
        }
    }

    // 带标签的 Counter: QUIC 发送字节
    if mf, ok := families["quic_client_sent_bytes"]; ok {
        for _, m := range mf.GetMetric() {
            connIdx := getLabelValue(m, "conn_index")
            stats.SentBytes[connIdx] = m.GetCounter().GetValue()
        }
    }

    // 带标签的 Counter: QUIC 接收字节
    if mf, ok := families["quic_client_receive_bytes"]; ok {
        for _, m := range mf.GetMetric() {
            connIdx := getLabelValue(m, "conn_index")
            stats.ReceivedBytes[connIdx] = m.GetCounter().GetValue()
        }
    }

    // 带标签的 Gauge: 服务器位置
    if mf, ok := families["cloudflared_tunnel_server_locations"]; ok {
        for _, m := range mf.GetMetric() {
            if m.GetGauge().GetValue() == 1 { // 1 表示当前位置
                connID := getLabelValue(m, "connection_id")
                location := getLabelValue(m, "edge_location")
                stats.ServerLocations[connID] = location
            }
        }
    }

    return stats
}

// getCounterValue 获取无标签 Counter 的值。
func getCounterValue(families map[string]*dto.MetricFamily, name string) float64 {
    mf, ok := families[name]
    if !ok || len(mf.GetMetric()) == 0 {
        return 0
    }
    return mf.GetMetric()[0].GetCounter().GetValue()
}

// getGaugeValue 获取无标签 Gauge 的值。
func getGaugeValue(families map[string]*dto.MetricFamily, name string) float64 {
    mf, ok := families[name]
    if !ok || len(mf.GetMetric()) == 0 {
        return 0
    }
    return mf.GetMetric()[0].GetGauge().GetValue()
}

// getLabelValue 从 Metric 中获取指定标签的值。
func getLabelValue(m *dto.Metric, name string) string {
    for _, lp := range m.GetLabel() {
        if lp.GetName() == name {
            return lp.GetValue()
        }
    }
    return ""
}
```

### 4.4 定时轮询示例

```go
package tunnel

import (
    "context"
    "log"
    "time"
)

// PollMetrics 定时轮询指标并通过回调通知。
func PollMetrics(ctx context.Context, client *MetricsClient, interval time.Duration, callback func(*TunnelStats)) {
    ticker := time.NewTicker(interval)
    defer ticker.Stop()

    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            families, err := client.Fetch()
            if err != nil {
                log.Printf("metrics fetch error: %v", err)
                continue
            }
            stats := ParseTunnelStats(families)
            callback(stats)
        }
    }
}
```

---

## 五、指标分类用途总结

### 5.1 隧道健康监控（最常用）

| 指标 | 监控用途 |
|------|---------|
| `cloudflared_tunnel_ha_connections` | 隧道是否存活，正常值为连接数(通常 4) |
| `cloudflared_tunnel_server_locations` | 连接到哪些边缘节点 |
| `cloudflared_tunnel_total_requests` | 请求吞吐量(rate) |
| `cloudflared_tunnel_request_errors` | 错误率监控 |
| `cloudflared_tunnel_concurrent_requests_per_tunnel` | 负载压力 |

### 5.2 网络质量监控

| 指标 | 监控用途 |
|------|---------|
| `quic_client_latest_rtt` | 实时延迟 |
| `quic_client_smoothed_rtt` | 平滑延迟趋势 |
| `quic_client_min_rtt` | 基线延迟 |
| `quic_client_lost_packets` | 丢包率 |
| `quic_client_congestion_window` | 网络拥塞状况 |

### 5.3 流量统计

| 指标 | 监控用途 |
|------|---------|
| `quic_client_sent_bytes` | 上行流量 |
| `quic_client_receive_bytes` | 下行流量 |
| `cloudflared_tunnel_response_by_code` | HTTP 响应分布 |
| `cloudflared_tcp_active_sessions` | TCP 并发连接 |

### 5.4 连接稳定性

| 指标 | 监控用途 |
|------|---------|
| `quic_client_total_connections` | 连接重建次数(频繁重建说明不稳定) |
| `quic_client_closed_connections` | 关闭的连接数 |
| `cloudflared_tunnel_tunnel_rpc_fail` | RPC 错误 |
| `cloudflared_proxy_connect_latency` | 源站连接延迟分布 |

---

## 六、实际 /metrics 输出示例

访问 `http://127.0.0.1:<PORT>/metrics` 返回的文本格式(截取):

```
# HELP build_info Build and version information
# TYPE build_info gauge
build_info{goversion="go1.22.5",revision="...",type="nightly",version="2024.8.2"} 1

# HELP cloudflared_tunnel_ha_connections Number of active ha connections
# TYPE cloudflared_tunnel_ha_connections gauge
cloudflared_tunnel_ha_connections 4

# HELP cloudflared_tunnel_total_requests Amount of requests proxied through all the tunnels
# TYPE cloudflared_tunnel_total_requests counter
cloudflared_tunnel_total_requests 1523

# HELP cloudflared_tunnel_response_by_code Count of responses by HTTP status code
# TYPE cloudflared_tunnel_response_by_code counter
cloudflared_tunnel_response_by_code{status_code="200"} 1400
cloudflared_tunnel_response_by_code{status_code="304"} 100
cloudflared_tunnel_response_by_code{status_code="404"} 23

# HELP cloudflared_tunnel_server_locations Where each tunnel is connected to
# TYPE cloudflared_tunnel_server_locations gauge
cloudflared_tunnel_server_locations{connection_id="0",edge_location="NRT"} 1
cloudflared_tunnel_server_locations{connection_id="1",edge_location="KIX"} 1

# HELP quic_client_latest_rtt Latest RTT measured on a connection
# TYPE quic_client_latest_rtt gauge
quic_client_latest_rtt{conn_index="0"} 45.2
quic_client_latest_rtt{conn_index="1"} 52.1

# HELP quic_client_sent_bytes Number of bytes that have been sent through a connection
# TYPE quic_client_sent_bytes counter
quic_client_sent_bytes{conn_index="0"} 1048576
quic_client_sent_bytes{conn_index="1"} 524288

# HELP cloudflared_proxy_connect_latency Time to establish and acknowledge connections in ms
# TYPE cloudflared_proxy_connect_latency histogram
cloudflared_proxy_connect_latency_bucket{le="1"} 50
cloudflared_proxy_connect_latency_bucket{le="10"} 200
cloudflared_proxy_connect_latency_bucket{le="25"} 350
cloudflared_proxy_connect_latency_bucket{le="50"} 420
cloudflared_proxy_connect_latency_bucket{le="100"} 450
cloudflared_proxy_connect_latency_bucket{le="500"} 460
cloudflared_proxy_connect_latency_bucket{le="1000"} 460
cloudflared_proxy_connect_latency_bucket{le="5000"} 460
cloudflared_proxy_connect_latency_bucket{le="+Inf"} 460
cloudflared_proxy_connect_latency_sum 8500
cloudflared_proxy_connect_latency_count 460
```

---

## 七、注意事项

1. **指标可用性**: 部分指标(如 `user_hostnames_counts`)在源码中定义但实际可能不出现在输出中(GitHub Issue #850)
2. **QUIC 指标前缀**: QUIC 指标的 namespace 是 `quic`，subsystem 是 `client`，与隧道指标的 `cloudflared` namespace 不同
3. **无流量统计**: cloudflared 目前不提供按域名的字节级流量统计(GitHub Issue #1001)，但 QUIC 层的 `sent_bytes` 和 `receive_bytes` 可以反映总体流量
4. **连接索引**: QUIC 指标的 `conn_index` 标签对应 cloudflared 的多条连接(默认 4 条)，值为 "0" 到 "3"
5. **Histogram 解析**: `connect_latency` 是 Histogram 类型，Prometheus 会自动生成 `_bucket`、`_sum`、`_count` 后缀指标

---

## 八、推荐监控方案

对于 trynet 项目，建议重点关注以下指标:

| 优先级 | 指标 | 理由 |
|--------|------|------|
| P0 | `ha_connections` | 判断隧道是否存活 |
| P0 | `total_requests` / `request_errors` | 请求成功率 |
| P1 | `latest_rtt` / `smoothed_rtt` | 用户感知延迟 |
| P1 | `sent_bytes` / `receive_bytes` | 流量消耗 |
| P2 | `server_locations` | 连接节点分布 |
| P2 | `response_by_code` | HTTP 错误分析 |
| P2 | `connect_latency` | 源站性能 |

轮询间隔建议: 5-10 秒适合实时监控面板，30-60 秒适合后台统计。
