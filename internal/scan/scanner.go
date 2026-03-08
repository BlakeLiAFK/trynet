package scan

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"sort"
	"sync"
	"time"
)

// 扫描的端口列表（仅 HTTP/HTTPS 服务）
var targetPorts = []int{
	// Web / 反代
	80, 443, 8080, 8443, 8000, 8888,
	// 开发服务器
	3000, 4000, 5000, 5173, 7000, 9000,
	// 面板 / 管理
	1000, 9090, 9443, 10000, 19999,
	// 自托管应用 (Jellyfin, Plex, HomeAssistant, Gitea)
	3001, 6875, 8096, 8123, 32400,
	// Git / 代码
	4321, 10080, 10443,
	// DB HTTP API (CouchDB, Elasticsearch, Kibana)
	5601, 5984, 9200,
}

// Result 单个扫描结果
type Result struct {
	IP      string `json:"ip"`
	Port    int    `json:"port"`
	Proto   string `json:"proto"`   // "http" 或 "https"
	Latency int64  `json:"latency"` // 毫秒
}

// tlsClient 复用，跳过证书验证（局域网自签名证书）
var tlsClient = &http.Client{
	Timeout: 800 * time.Millisecond,
	Transport: &http.Transport{
		TLSClientConfig:   &tls.Config{InsecureSkipVerify: true},
		DisableKeepAlives: true,
	},
}

var plainClient = &http.Client{
	Timeout: 800 * time.Millisecond,
	Transport: &http.Transport{
		DisableKeepAlives: true,
	},
}

// Scan 扫描局域网
// subnetBits: 子网大小，支持 24(/24，254 主机) 或 16(/16，65534 主机)，默认 24
func Scan(subnetBits int, onProgress func(scanned, total int)) []Result {
	if subnetBits != 16 && subnetBits != 24 {
		subnetBits = 24
	}
	hosts := localHosts(subnetBits)

	type task struct {
		ip   string
		port int
	}
	var tasks []task
	for _, ip := range hosts {
		for _, port := range targetPorts {
			tasks = append(tasks, task{ip, port})
		}
	}

	total := len(tasks)
	var (
		mu      sync.Mutex
		results []Result
		scanned int
		wg      sync.WaitGroup
		sem     = make(chan struct{}, 200)
	)

	for _, t := range tasks {
		wg.Add(1)
		sem <- struct{}{}
		go func(ip string, port int) {
			defer wg.Done()
			defer func() { <-sem }()

			if r, ok := probe(ip, port); ok {
				mu.Lock()
				results = append(results, r)
				mu.Unlock()
			}
			mu.Lock()
			scanned++
			if onProgress != nil {
				onProgress(scanned, total)
			}
			mu.Unlock()
		}(t.ip, t.port)
	}
	wg.Wait()

	sort.Slice(results, func(i, j int) bool {
		return results[i].Latency < results[j].Latency
	})
	return results
}

// probe 探测单个 ip:port，先尝试 HTTPS 再尝试 HTTP
func probe(ip string, port int) (Result, bool) {
	addr := fmt.Sprintf("%s:%d", ip, port)

	conn, err := net.DialTimeout("tcp", addr, 300*time.Millisecond)
	if err != nil {
		return Result{}, false
	}
	conn.Close()

	// 尝试 HTTPS
	start := time.Now()
	resp, err := tlsClient.Get("https://" + addr)
	if err == nil {
		resp.Body.Close()
		return Result{IP: ip, Port: port, Proto: "https", Latency: time.Since(start).Milliseconds()}, true
	}

	// 尝试 HTTP
	start = time.Now()
	resp, err = plainClient.Get("http://" + addr)
	if err == nil {
		resp.Body.Close()
		return Result{IP: ip, Port: port, Proto: "http", Latency: time.Since(start).Milliseconds()}, true
	}

	return Result{}, false
}

// localHosts 获取本机所有私有网卡 IP，按 subnetBits 扩展枚举
func localHosts(subnetBits int) []string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}

	seen := map[string]bool{}
	var hosts []string

	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP.To4()
			case *net.IPAddr:
				ip = v.IP.To4()
			}
			if ip == nil || !isPrivate(ip) {
				continue
			}

			var subnetKey string
			if subnetBits == 16 {
				subnetKey = fmt.Sprintf("%d.%d.0.0/16", ip[0], ip[1])
			} else {
				subnetKey = fmt.Sprintf("%d.%d.%d.0/24", ip[0], ip[1], ip[2])
			}
			if seen[subnetKey] {
				continue
			}
			seen[subnetKey] = true

			_, network, err := net.ParseCIDR(subnetKey)
			if err != nil {
				continue
			}
			// 枚举子网内所有主机
			cur := cloneIP(network.IP)
			for {
				incrementIP(cur)
				if !network.Contains(cur) {
					break
				}
				// 跳过广播地址
				if isBroadcast(cur, network) {
					break
				}
				h := cur.String()
				if !seen[h] {
					seen[h] = true
					hosts = append(hosts, h)
				}
			}
		}
	}
	return hosts
}

// isPrivate 判断是否为 RFC1918 私有 IP
func isPrivate(ip net.IP) bool {
	for _, cidr := range []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"} {
		_, network, _ := net.ParseCIDR(cidr)
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

func cloneIP(ip net.IP) net.IP {
	c := make(net.IP, len(ip))
	copy(c, ip)
	return c
}

func incrementIP(ip net.IP) {
	for i := len(ip) - 1; i >= 0; i-- {
		ip[i]++
		if ip[i] != 0 {
			break
		}
	}
}

func isBroadcast(ip net.IP, subnet *net.IPNet) bool {
	broadcast := make(net.IP, 4)
	sub4 := subnet.IP.To4()
	for i := range sub4 {
		broadcast[i] = sub4[i] | ^subnet.Mask[i]
	}
	return ip.Equal(broadcast)
}
