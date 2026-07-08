package capture

import (
	"bytes"
	"io"
	"testing"

	"github.com/Veyal/interseptor/internal/store"
)

// BenchmarkTeeBody measures the capture hot path: streaming a body through the
// tee to the content-addressed store. It validates that capture cost scales with
// throughput, not with a copy held in memory.
func BenchmarkTeeBody(b *testing.B) {
	s, err := store.Open(b.TempDir())
	if err != nil {
		b.Fatalf("Open: %v", err)
	}
	defer s.Close()
	c := New(s)

	payload := bytes.Repeat([]byte("interseptor-streaming-body-"), 4096) // ~108 KB
	b.SetBytes(int64(len(payload)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tee, finalize, err := c.TeeBody(bytes.NewReader(payload))
		if err != nil {
			b.Fatalf("TeeBody: %v", err)
		}
		if _, err := io.Copy(io.Discard, tee); err != nil {
			b.Fatalf("copy: %v", err)
		}
		if _, _, err := finalize(); err != nil {
			b.Fatalf("finalize: %v", err)
		}
	}
}
