package tlsca

import (
	"strconv"
	"sync/atomic"
	"testing"
)

// BenchmarkLeafForHostDistinctHosts measures the cost the TLS interception
// handshake pays on a cache miss — a fresh SNI the proxy has not seen. Run in
// parallel, because that is what a browser opening a page full of third-party
// hosts, or a subdomain sweep through the proxy, actually does.
func BenchmarkLeafForHostDistinctHosts(b *testing.B) {
	ca, err := LoadOrCreate(b.TempDir())
	if err != nil {
		b.Fatalf("LoadOrCreate: %v", err)
	}
	old := leafCacheMax
	leafCacheMax = 1 << 30 // keep FIFO eviction out of the measurement
	b.Cleanup(func() { leafCacheMax = old })

	var seq atomic.Int64
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			host := "h" + strconv.FormatInt(seq.Add(1), 10) + ".example.com"
			if _, err := ca.LeafForHost(host); err != nil {
				b.Fatalf("LeafForHost: %v", err)
			}
		}
	})
}

// BenchmarkLeafForHostCached measures the hit path, which every handshake after
// the first to a given host takes.
func BenchmarkLeafForHostCached(b *testing.B) {
	ca, err := LoadOrCreate(b.TempDir())
	if err != nil {
		b.Fatalf("LoadOrCreate: %v", err)
	}
	if _, err := ca.LeafForHost("api.example.com"); err != nil {
		b.Fatalf("warm: %v", err)
	}
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if _, err := ca.LeafForHost("api.example.com"); err != nil {
				b.Fatalf("LeafForHost: %v", err)
			}
		}
	})
}

// BenchmarkLeafForHostSameHostBurst measures the burst a single new host
// produces: a page's worth of parallel connections all arriving with the same
// SNI before any certificate is cached.
func BenchmarkLeafForHostSameHostBurst(b *testing.B) {
	var seq atomic.Int64
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		ca, err := LoadOrCreate(b.TempDir())
		if err != nil {
			b.Fatalf("LoadOrCreate: %v", err)
		}
		host := "burst" + strconv.FormatInt(seq.Add(1), 10) + ".example.com"
		done := make(chan error, 16)
		b.StartTimer()
		for g := 0; g < 16; g++ {
			go func() {
				_, err := ca.LeafForHost(host)
				done <- err
			}()
		}
		for g := 0; g < 16; g++ {
			if err := <-done; err != nil {
				b.Fatalf("LeafForHost: %v", err)
			}
		}
	}
}
