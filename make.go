//go:build ignore

// make.go builds and optionally UPX-packs the CLI.
// go run make.go builds the host target; -all builds all targets in dist/.
package main

import (
	_ "embed"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

type target struct {
	goos string
	goarch string
}

var crossTargets = []target{
	{"windows", "amd64"}, {"windows", "arm64"},
	{"linux", "amd64"}, {"linux", "arm64"},
	{"darwin", "amd64"}, {"darwin", "arm64"},
}

//go:embed VERSION
var versionFile string
var releaseVersion = strings.TrimSpace(versionFile)
var sourceCommit = "dev"

func main() {
	flag.StringVar(&releaseVersion, "version", releaseVersion, "version embedded in binaries")
	flag.StringVar(&sourceCommit, "commit", sourceCommit, "source commit embedded in binaries")
	all := flag.Bool("all", false, "cross-compile every target into dist/ instead of just this machine")
	flag.Parse()
	upx := findUPX()
	if upx == "" {
		fmt.Println("upx not found on PATH, binaries will be left uncompressed")
		fmt.Println("  get it from https://upx.github.io; savings depend on target and compiler")
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
	if failed > 0 { os.Exit(1) }
}

func build(t target, dir, upx string) error {
	ext := ""
	if t.goos == "windows" { ext = ".exe" }
	var raw, packed string
	if dir == "" {
		raw, packed = "trynet-raw"+ext, "trynet"+ext
	} else {
		packed = filepath.Join(dir, fmt.Sprintf("trynet-%s-%s%s", t.goos, t.goarch, ext))
		raw = packed + ".raw"
	}
	// Never silently ship an old executable if removal fails (e.g. Windows
	// locks a still-running executable). Packing fallback is separate.
	for _, path := range []string{raw, packed} {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove stale %s: %w (still running?)", path, err)
		}
	}
	cmd := exec.Command("go", "build", "-trimpath", "-buildvcs=false", "-ldflags=-s -w -X trynet/internal/buildinfo.Version="+releaseVersion+" -X trynet/internal/buildinfo.Commit="+sourceCommit, "-o", raw, ".")
	cmd.Env = append(os.Environ(), "GOOS="+t.goos, "GOARCH="+t.goarch, "CGO_ENABLED=0")
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil { return fmt.Errorf("go build: %w", err) }
	rawSize := fileSize(raw)
	if t.goos == "darwin" || upx == "" {
		note := "upx unavailable"
		if t.goos == "darwin" { note = "not packed: macOS compatibility policy" }
		return ship(raw, packed, rawSize, note)
	}
	if out, err := exec.Command(upx, "--best", "-o", packed, raw).CombinedOutput(); err != nil {
		_ = os.Remove(packed)
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
	fmt.Printf("%-34s %6.2f MiB  (packed from %.2f MiB)\n", packed, toMB(fileSize(packed)), toMB(rawSize))
	if dir != "" { _ = os.Remove(raw) }
	return nil
}

func ship(raw, packed string, rawSize int64, note string) error {
	if err := os.Rename(raw, packed); err != nil { return err }
	fmt.Printf("%-34s %6.2f MiB  (%s)\n", packed, toMB(rawSize), note)
	return nil
}

func upxReason(out []byte) string {
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.Contains(line, "Exception:") {
			if _, reason, ok := strings.Cut(line, "Exception:"); ok { return strings.TrimSpace(reason) }
		}
	}
	return "upx failed"
}

func findUPX() string {
	p, err := exec.LookPath("upx")
	if err != nil { return "" }
	return p
}

func fileSize(path string) int64 {
	fi, err := os.Stat(path)
	if err != nil { return 0 }
	return fi.Size()
}

func toMB(n int64) float64 { return float64(n) / (1 << 20) }
