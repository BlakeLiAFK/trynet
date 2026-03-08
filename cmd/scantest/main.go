package main

import (
	"fmt"
	"trynet/internal/scan"
)

func main() {
	fmt.Println("开始扫描...")
	results := scan.Scan(24, func(scanned, total int) {
		if total > 0 && scanned%500 == 0 {
			fmt.Printf("进度: %d / %d\n", scanned, total)
		}
	})
	fmt.Printf("\n发现 %d 个服务:\n", len(results))
	for _, r := range results {
		fmt.Printf("  [%s] %s:%d  %dms\n", r.Proto, r.IP, r.Port, r.Latency)
	}
}
