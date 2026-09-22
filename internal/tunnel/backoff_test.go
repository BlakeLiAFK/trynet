package tunnel

import (
	"context"
	"testing"
	"time"
)

func TestBackoff(t *testing.T) {
	b := NewBackoff()
	got := []time.Duration{b.Next(), b.Next(), b.Next(), b.Next(), b.Next(), b.Next()}
	want := []time.Duration{3, 6, 12, 24, 30, 30}
	for i, w := range want {
		if got[i] != w*time.Second {
			t.Errorf("next() #%d = %v, want %ds", i, got[i], w)
		}
	}

	b.Reset()
	if got := b.Next(); got != 3*time.Second {
		t.Errorf("after reset, next() = %v, want 3s", got)
	}
}

func TestSleepCtx(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if SleepCtx(ctx, time.Second) {
		t.Error("已取消的 ctx 应立刻返回 false")
	}

	start := time.Now()
	if !SleepCtx(context.Background(), 20*time.Millisecond) {
		t.Error("正常等待应返回 true")
	}
	if time.Since(start) < 20*time.Millisecond {
		t.Error("没等够时间就返回了")
	}
}
