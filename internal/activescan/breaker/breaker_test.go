package breaker

import (
	"sync"
	"testing"
)

func TestTrackerSkipsAfterRepeated403(t *testing.T) {
	tr := New()
	key := Key("GET", "app.test", "/api")
	for i := 0; i < SkipThreshold; i++ {
		tr.Record(key, "GET", "app.test", "/api", 403, false)
	}
	skip, reason := tr.ShouldSkip(key)
	if !skip || reason == "" {
		t.Fatalf("ShouldSkip = %v, %q — want skip after %d 403s", skip, reason, SkipThreshold)
	}
}

func TestTrackerSkipsAfterTransportErrors(t *testing.T) {
	tr := New()
	key := Key("POST", "app.test", "/x")
	for i := 0; i < TimeoutThreshold; i++ {
		tr.Record(key, "POST", "app.test", "/x", 0, true)
	}
	skip, _ := tr.ShouldSkip(key)
	if !skip {
		t.Fatal("expected skip after repeated transport errors")
	}
}

func TestTrackerConcurrent(t *testing.T) {
	tr := New()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			key := Key("GET", "host.test", "/p")
			for j := 0; j < 50; j++ {
				tr.Record(key, "GET", "host.test", "/p", 403+(j%3), j%5 == 0)
				tr.ShouldSkip(key)
				_ = tr.SkippedList()
			}
		}(i)
	}
	wg.Wait()
}
