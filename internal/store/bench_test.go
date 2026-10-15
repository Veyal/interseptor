package store

import (
	"strconv"
	"testing"
	"time"
)

// BenchmarkInsertFlow measures the metadata write rate on the hot path (one flow
// row per proxied request). A regression here would mean capture is no longer
// cheap relative to forwarding.
func BenchmarkInsertFlow(b *testing.B) {
	s, err := Open(b.TempDir())
	if err != nil {
		b.Fatalf("Open: %v", err)
	}
	defer s.Close()
	f := &Flow{
		TS: time.UnixMilli(1), Method: "GET", Scheme: "https", Host: "victim.test", Path: "/api/users?id=1",
		Status: 200, ReqHeaders: map[string][]string{"Accept": {"*/*"}}, ResHeaders: map[string][]string{"Content-Type": {"text/html"}},
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		f.ID = 0
		if _, err := s.InsertFlow(f); err != nil {
			b.Fatalf("InsertFlow: %v", err)
		}
	}
}

func benchFlow(i int) *Flow {
	return &Flow{
		TS:     time.UnixMilli(int64(i) + 1),
		Method: "GET", Scheme: "https", Host: "victim.test",
		Path:       "/api/users?id=" + strconv.Itoa(i),
		ReqHeaders: map[string][]string{"Accept": {"*/*"}},
		ClientAddr: "127.0.0.1:50000",
	}
}

// BenchmarkFlowDrain measures the write-behind flusher's apply rate for a
// queued batch — the ceiling on proxy ingress before flowPending fills and
// capture starts dropping flows. One iteration is one queued operation.
//
// The flusher goroutine is paused so this goroutine is the only drainer and
// the measurement covers nothing but the SQLite write path.
func BenchmarkFlowDrain(b *testing.B) {
	for _, batch := range []int{64, 512, 4096} {
		b.Run("batch"+strconv.Itoa(batch), func(b *testing.B) {
			s, err := Open(b.TempDir())
			if err != nil {
				b.Fatalf("Open: %v", err)
			}
			defer s.Close()
			s.flowMu.Lock()
			s.flowPaused = true
			s.flowMu.Unlock()

			b.ResetTimer()
			for i := 0; i < b.N; i += batch {
				n := batch
				if i+n > b.N {
					n = b.N - i
				}
				b.StopTimer()
				for j := 0; j < n; j++ {
					if ok, err := s.EnqueueInsertFlow(benchFlow(i + j)); err != nil || !ok {
						b.Fatalf("enqueue: ok=%v err=%v", ok, err)
					}
				}
				b.StartTimer()
				if err := s.drainOnce(); err != nil {
					b.Fatalf("drainOnce: %v", err)
				}
			}
			b.StopTimer()
			if n := s.FlowsDropped(); n != 0 {
				b.Fatalf("dropped %d", n)
			}
		})
	}
}

// BenchmarkFlowCaptureIngest measures the whole capture path a proxied request
// pays: enqueue the request-side insert, enqueue the response-side update, and
// let the flusher apply both. One iteration is one captured flow (two queued
// operations), so ns/op converts directly to a sustainable flows/sec figure.
func BenchmarkFlowCaptureIngest(b *testing.B) {
	s, err := Open(b.TempDir())
	if err != nil {
		b.Fatalf("Open: %v", err)
	}
	defer s.Close()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		f := benchFlow(i)
		if ok, err := s.EnqueueInsertFlow(f); err != nil || !ok {
			b.Fatalf("enqueue insert: ok=%v err=%v", ok, err)
		}
		f.Status = 200
		f.ResLen = 1024
		if ok, err := s.EnqueueUpdateFlow(f); err != nil || !ok {
			b.Fatalf("enqueue update: ok=%v err=%v", ok, err)
		}
		// Keep the queue well under flowBufCap so the measurement is throughput,
		// not the drop path.
		if (i+1)%1024 == 0 {
			if err := s.FlushFlows(); err != nil {
				b.Fatalf("FlushFlows: %v", err)
			}
		}
	}
	if err := s.FlushFlows(); err != nil {
		b.Fatalf("FlushFlows: %v", err)
	}
	b.StopTimer()
	if n := s.FlowsDropped(); n != 0 {
		b.Fatalf("dropped %d flows", n)
	}
}
