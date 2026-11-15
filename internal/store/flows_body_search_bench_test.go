package store

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"strings"
	"testing"
	"time"
)

func seedSearchBenchmarkStore(b *testing.B, flows int, bodySize int, gzipBodies bool) *Store {
	b.Helper()
	s, err := Open(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = s.Close() })

	pad := strings.Repeat("Lorem ipsum dolor sit amet, consectetur adipiscing elit. 0123456789ABCDEF ", max(1, bodySize/80))
	for i := 0; i < flows; i++ {
		var body bytes.Buffer
		body.WriteString(pad)
		fmt.Fprintf(&body, " flow-marker-%d", i)
		if i == flows/2 {
			body.WriteString(" RareSearchNeedleToken ")
		}
		raw := body.Bytes()
		var resHeaders map[string][]string
		if gzipBodies && i%2 == 0 {
			var gzBuf bytes.Buffer
			zw := gzip.NewWriter(&gzBuf)
			if _, err := zw.Write(raw); err != nil {
				b.Fatal(err)
			}
			if err := zw.Close(); err != nil {
				b.Fatal(err)
			}
			raw = gzBuf.Bytes()
			resHeaders = map[string][]string{"Content-Encoding": {"gzip"}, "Content-Type": {"text/html"}}
		} else {
			resHeaders = map[string][]string{"Content-Type": {"application/json"}, "X-Request-Id": {fmt.Sprintf("req-%d", i)}}
		}
		w, err := s.NewBodyWriter()
		if err != nil {
			b.Fatal(err)
		}
		if _, err := w.Write(raw); err != nil {
			b.Fatal(err)
		}
		hash, _, err := w.Finalize()
		if err != nil {
			b.Fatal(err)
		}
		if _, err := s.InsertFlow(&Flow{
			TS:          time.UnixMilli(int64(i + 1)),
			Method:      "GET",
			Scheme:      "https",
			Host:        "app.example.com",
			Path:        fmt.Sprintf("/api/v1/items/%d", i),
			HTTPVersion: "HTTP/1.1",
			Status:      200,
			Mime:        "application/json",
			ReqHeaders:  map[string][]string{"Host": {"app.example.com"}, "Accept": {"application/json"}},
			ResHeaders:  resHeaders,
			ResBodyHash: hash,
			ResLen:      int64(len(raw)),
		}); err != nil {
			b.Fatal(err)
		}
	}
	return s
}

func BenchmarkBodyAndAnywhereSearch(b *testing.B) {
	plain := seedSearchBenchmarkStore(b, 300, 64<<10, false)
	mixed := seedSearchBenchmarkStore(b, 200, 64<<10, true)

	b.Run("body-plain-300x64k", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			ids, _, _, err := plain.FlowIDsBodySearch(FlowFilter{Search: "raresearchneedletoken -AnalyticsNoise"}, 8000)
			if err != nil || len(ids) != 1 {
				b.Fatalf("ids=%v err=%v", ids, err)
			}
		}
	})

	b.Run("anywhere-plain-300x64k", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			f, _, err := plain.AnywhereSearchFilter(FlowFilter{Search: "raresearchneedletoken"}, 8000)
			if err != nil || len(f.AnyTextIDs) != 1 {
				b.Fatalf("anyIDs=%v err=%v", f.AnyTextIDs, err)
			}
		}
	})

	b.Run("body-gzip-mixed-200x64k", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			ids, _, _, err := mixed.FlowIDsBodySearch(FlowFilter{Search: "raresearchneedletoken"}, 8000)
			if err != nil || len(ids) != 1 {
				b.Fatalf("ids=%v err=%v", ids, err)
			}
		}
	})

	b.Run("endpoint-body-300x64k", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			eps, _, err := plain.Endpoints(EndpointFilter{Search: "raresearchneedletoken", SearchScope: EndpointSearchBody})
			if err != nil || len(eps) != 1 {
				b.Fatalf("eps=%d err=%v", len(eps), err)
			}
		}
	})

	b.Run("endpoints-soft404-300x64k", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			eps, _, err := plain.Endpoints(EndpointFilter{})
			if err != nil || len(eps) != 300 {
				b.Fatalf("eps=%d err=%v", len(eps), err)
			}
		}
	})
}
