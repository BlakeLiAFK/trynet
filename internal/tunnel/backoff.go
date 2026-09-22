// backoff.go 提供重连/重试要用的等待小工具：main.go 的重连循环和
// named_tunnel.go 的 teardown 重试都靠它们。
package tunnel

import (
	"context"
	"time"
)

// SleepCtx 等待 d 或 ctx 被取消，返回 false 表示是被取消打断的，调用方应立即退出
func SleepCtx(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

// backoff 是最简单的指数退避：从 3s 起步每次翻倍，封顶 30s；连上一次就 reset 回起点，
// 这样长期挂着运行时偶尔一次网络抖动不会被罚等 30s
type backoff struct{ d time.Duration }

func NewBackoff() *backoff { return &backoff{d: 3 * time.Second} }

func (b *backoff) Next() time.Duration {
	d := b.d
	b.d *= 2
	if b.d > 30*time.Second {
		b.d = 30 * time.Second
	}
	return d
}

func (b *backoff) Reset() { b.d = 3 * time.Second }
