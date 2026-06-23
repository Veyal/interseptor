package store

import (
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
