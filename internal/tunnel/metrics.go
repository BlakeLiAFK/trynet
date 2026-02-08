package tunnel

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Metrics 隧道运行指标
type Metrics struct {
	HAConnections      int     `json:"haConnections"`      // HA 活跃连接数
	TotalRequests      int64   `json:"totalRequests"`      // 请求总数
	RequestErrors      int64   `json:"requestErrors"`      // 代理错误数
	LatestRTT          float64 `json:"latestRtt"`          // 最新 RTT (ms)
	SentBytes          int64   `json:"sentBytes"`          // 发送字节
	ReceivedBytes      int64   `json:"receivedBytes"`      // 接收字节
	ConcurrentRequests int     `json:"concurrentRequests"` // 当前并发请求
}

// metricsClient 复用 HTTP client，短超时
var metricsClient = &http.Client{
	Timeout: 2 * time.Second,
}

// FetchMetrics 从 Prometheus endpoint 获取并解析隧道指标
func FetchMetrics(addr string) (*Metrics, error) {
	if addr == "" {
		return nil, fmt.Errorf("no metrics address")
	}
	resp, err := metricsClient.Get("http://" + addr + "/metrics")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return parseMetrics(resp.Body)
}

// parseMetrics 从 Prometheus text 格式解析关键指标
func parseMetrics(r io.Reader) (*Metrics, error) {
	m := &Metrics{}
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		// 跳过注释和空行
		if len(line) == 0 || line[0] == '#' {
			continue
		}
		// 解析简单 gauge/counter 格式: metric_name{labels} value
		name, value := parsePromLine(line)
		switch name {
		case "cloudflared_tunnel_ha_connections":
			m.HAConnections = int(value)
		case "cloudflared_tunnel_total_requests":
			m.TotalRequests = int64(value)
		case "cloudflared_tunnel_request_errors":
			m.RequestErrors = int64(value)
		case "cloudflared_tunnel_concurrent_requests_per_tunnel":
			m.ConcurrentRequests = int(value)
		case "quic_client_latest_rtt":
			m.LatestRTT = value
		case "quic_client_sent_bytes":
			m.SentBytes = int64(value)
		case "quic_client_receive_bytes":
			m.ReceivedBytes = int64(value)
		}
	}
	return m, nil
}

// parsePromLine 解析 Prometheus 格式行，返回 metric name 和 value
// 格式: metric_name 1.23 或 metric_name{label="val"} 1.23
func parsePromLine(line string) (string, float64) {
	// 去掉 label 部分
	name := line
	if idx := strings.IndexByte(line, '{'); idx > 0 {
		name = line[:idx]
		// 找到 } 后面的值
		if end := strings.IndexByte(line, '}'); end > idx {
			line = name + line[end+1:]
		}
	}
	// 分割 name 和 value
	parts := strings.Fields(line)
	if len(parts) < 2 {
		return name, 0
	}
	v, _ := strconv.ParseFloat(parts[1], 64)
	return parts[0], v
}
