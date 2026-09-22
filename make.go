//go:build ignore

// make.go 是 trynet 的打包脚本：编译 + 加壳。
//
//	go run make.go        只编当前平台，产物留在仓库根目录（trynet-raw / trynet）
//	go run make.go -all   交叉编译全部平台，产物放进 dist/
//
// 用 Go 写而不是 shell/bat：构建这个项目本来就得有 Go，不用再额外装东西，
// Windows / macOS / Linux 上行为一致。文件头的 ignore 标签让它不进 ./... 的构建范围。
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

type target struct {
	goos   string
	goarch string
}

// crossTargets 是 -all 要编的平台
var crossTargets = []target{
	{"windows", "amd64"},
	{"windows", "arm64"},
	{"linux", "amd64"},
	{"linux", "arm64"},
	{"darwin", "amd64"},
	{"darwin", "arm64"},
}

var releaseVersion = "1.0.0"
var sourceCommit = "dev"

func main() {
	flag.StringVar(&releaseVersion, "version", releaseVersion, "version embedded in binaries")
	flag.StringVar(&sourceCommit, "commit", sourceCommit, "source commit embedded in binaries")
	all := flag.Bool("all", false, "cross-compile every target into dist/ instead of just this machine")
	flag.Parse()

	upx := findUPX()
	if upx == "" {
		fmt.Println("upx not found on PATH, binaries will be left uncompressed")
		fmt.Println("  get it from https://upx.github.io to cut roughly two thirds off the size")
		fmt.Println()
	}

	if !*all {
		if err := build(target{runtime.GOOS, runtime.GOARCH}, "", upx); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	if err := os.MkdirAll("dist", 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	failed := 0
	for _, t := range crossTargets {
		if err := build(t, "dist", upx); err != nil {
			fmt.Fprintf(os.Stderr, "%s/%s: %v\n", t.goos, t.goarch, err)
			failed++
		}
	}
	if failed > 0 {
		os.Exit(1)
	}
}

// build 编一个平台，能加壳就加壳。
// dir 为空表示编到仓库根目录、沿用 trynet-raw / trynet 这套名字（本机日常用）；
// 否则编到 dir 下，文件名带平台后缀，且不保留未压缩的中间产物。
func build(t target, dir, upx string) error {
	ext := ""
	if t.goos == "windows" {
		ext = ".exe"
	}

	var raw, packed string
	if dir == "" {
		raw, packed = "trynet-raw"+ext, "trynet"+ext
	} else {
		packed = filepath.Join(dir, fmt.Sprintf("trynet-%s-%s%s", t.goos, t.goarch, ext))
		raw = packed + ".raw"
	}
	// 删不掉旧产物是真正的构建错误，不能当成"压不了"降级处理——
	// Windows 上跑着的程序会锁住自己的文件，这时候硬往下走只会交付一个旧二进制，
	// 而且 UPX 报出来的是一句指错方向的 "File exists"。
	for _, path := range []string{raw, packed} {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove stale %s: %w (still running?)", path, err)
		}
	}

	// -trimpath 去掉构建机的绝对路径，-s -w 去掉符号表和调试信息，
	// 这两项加起来就能省掉三成体积，且不影响运行。
	cmd := exec.Command("go", "build", "-trimpath", "-buildvcs=false", "-ldflags=-s -w -X trynet/internal/buildinfo.Version="+releaseVersion+" -X trynet/internal/buildinfo.Commit="+sourceCommit, "-o", raw, ".")
	cmd.Env = append(os.Environ(), "GOOS="+t.goos, "GOARCH="+t.goarch, "CGO_ENABLED=0")
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("go build: %w", err)
	}
	rawSize := fileSize(raw)

	// Skip macOS: UPX disables modern macOS support because of compatibility issues.
	if t.goos == "darwin" || upx == "" {
		note := "upx unavailable"
		if t.goos == "darwin" {
			note = "not packed: macOS compatibility policy"
		}
		return ship(raw, packed, rawSize, note)
	}

	// 剩下的情况让 UPX 自己说话。它压不了的平台会干脆报错（比如目前的 win64/arm64），
	// 这时交付未压缩版就行——加壳是优化不是交付条件，不该因为优化失败就让构建失败。
	// 这样以后 UPX 支持了新平台也不用回来改这里。
	if out, err := exec.Command(upx, "--best", "-o", packed, raw).CombinedOutput(); err != nil {
		_ = os.Remove(packed) // 失败时 UPX 可能留下半成品
		return ship(raw, packed, rawSize, "not packed: "+upxReason(out))
	}
	if out, err := exec.Command(upx, "-t", packed).CombinedOutput(); err != nil {
		_ = os.Remove(packed)
		return ship(raw, packed, rawSize, "not packed: integrity check failed: "+upxReason(out))
	}
	if fileSize(packed) >= rawSize {
		_ = os.Remove(packed)
		return ship(raw, packed, rawSize, "not packed: no size saving")
	}
	fmt.Printf("%-34s %6.2f MB  (packed from %.2f MB)\n", packed, toMB(fileSize(packed)), toMB(rawSize))

	if dir != "" {
		_ = os.Remove(raw)
	}
	return nil
}

// ship 把未压缩的二进制当作最终产物交付，并说明为什么没加壳。
func ship(raw, packed string, rawSize int64, note string) error {
	if err := os.Rename(raw, packed); err != nil {
		return err
	}
	fmt.Printf("%-34s %6.2f MB  (%s)\n", packed, toMB(rawSize), note)
	return nil
}

// upxReason 从 UPX 的输出里挑出真正说明原因的那一行，
// 免得把版权声明和表头一起打出来。
func upxReason(out []byte) string {
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.Contains(line, "Exception:") {
			if _, reason, ok := strings.Cut(line, "Exception:"); ok {
				return strings.TrimSpace(reason)
			}
		}
	}
	return "upx failed"
}

func findUPX() string {
	p, err := exec.LookPath("upx")
	if err != nil {
		return ""
	}
	return p
}

func fileSize(path string) int64 {
	fi, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return fi.Size()
}

func toMB(n int64) float64 { return float64(n) / (1 << 20) }
