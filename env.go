// env.go 把环境变量接进命令行 flag 的默认值，并在 -domain 模式下告诉横幅
// API token 到底是从哪来的（环境变量还是显式的 -api-token）。
package main

import (
	"flag"
	"os"
	"strconv"
)

// envOr 返回环境变量的值，没设置（或是空串）就用 def。
func envOr(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

func envOrInt(name string, def int) int {
	if v := os.Getenv(name); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func envOrBool(name string, def bool) bool {
	if v := os.Getenv(name); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return def
}

// tokenSource 说明 API token 实际是从哪来的，给启动横幅显示用。
// 环境变量的值是作为 flag 默认值注入的，parse 完就分不出来源了，
// 只能靠 flag.Visit——它只遍历命令行上显式给过的 flag。
func tokenSource() string {
	source := "CLOUDFLARE_API_TOKEN"
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "api-token" {
			source = "-api-token"
		}
	})
	return source
}
