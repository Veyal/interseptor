package pmsandbox

import (
	"context"
	"runtime/metrics"
	"time"
)

const heapMetric = "/memory/classes/heap/objects:bytes"

func heapBytes() uint64 {
	s := []metrics.Sample{{Name: heapMetric}}
	metrics.Read(s)
	if s[0].Value.Kind() != metrics.KindUint64 {
		return 0
	}
	return s[0].Value.Uint64()
}

// watchHeap calls onBreach once if live heap grows by more than limit bytes
// above its level at start. goja has no heap cap, so this is the in-process
// backstop against memory bombs until the worker subprocess (WP9) lands.
func watchHeap(ctx context.Context, limit uint64, onBreach func()) (stop func()) {
	base := heapBytes()
	done := make(chan struct{})
	go func() {
		t := time.NewTicker(10 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-t.C:
				if cur := heapBytes(); cur > base && cur-base > limit {
					onBreach()
					return
				}
			}
		}
	}()
	return func() { close(done) }
}
