package scanner

import (
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/store"
)

// BenchmarkAnalyzeLargeHTML measures the built-in checks on one large HTML
// response (the checks read the first MaxScanBytes of it).
func BenchmarkAnalyzeLargeHTML(b *testing.B) {
	body := []byte(strings.Repeat("<p>lorem ipsum example text 0123456789</p>\n", 4*1024*1024/44))
	f := &store.Flow{ID: 1, Method: "GET", Scheme: "https", Host: "big.example.com", Path: "/report/1?q=lorem", Status: 200, Mime: "text/html",
		ResHeaders: map[string][]string{"Content-Type": {"text/html"}}}
	b.SetBytes(MaxScanBytes)
	for i := 0; i < b.N; i++ {
		Analyze(f, nil, body)
	}
}
