package control

import (
	"bytes"
	"compress/gzip"
	"testing"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
)

func TestDecodeForDisplay(t *testing.T) {
	const plain = "<html><body>warpstar secret response body</body></html>"

	gz := func(s string) []byte {
		var b bytes.Buffer
		w := gzip.NewWriter(&b)
		w.Write([]byte(s))
		w.Close()
		return b.Bytes()
	}
	br := func(s string) []byte {
		var b bytes.Buffer
		w := brotli.NewWriter(&b)
		w.Write([]byte(s))
		w.Close()
		return b.Bytes()
	}
	zs := func(s string) []byte {
		var b bytes.Buffer
		w, _ := zstd.NewWriter(&b)
		w.Write([]byte(s))
		w.Close()
		return b.Bytes()
	}

	for _, c := range []struct {
		enc  string
		body []byte
	}{
		{"gzip", gz(plain)},
		{"br", br(plain)},
		{"zstd", zs(plain)},
	} {
		hdr := map[string][]string{"Content-Encoding": {c.enc}, "Content-Type": {"text/html"}, "Content-Length": {"999"}}
		outHdr, outBody := decodeForDisplay(hdr, c.body)
		if string(outBody) != plain {
			t.Fatalf("%s: body not decoded: got %q", c.enc, outBody)
		}
		if _, ok := outHdr["Content-Encoding"]; ok {
			t.Fatalf("%s: Content-Encoding should be dropped after decode", c.enc)
		}
		if outHdr["Content-Length"][0] != "55" { // len(plain)
			t.Fatalf("%s: Content-Length not corrected: %v", c.enc, outHdr["Content-Length"])
		}
		if outHdr["X-Interseptor-Decoded"][0] != c.enc {
			t.Fatalf("%s: missing decoded marker", c.enc)
		}
	}

	// A plain (uncompressed) body is passed through untouched.
	hdr := map[string][]string{"Content-Type": {"text/html"}}
	gotH, gotB := decodeForDisplay(hdr, []byte(plain))
	if string(gotB) != plain || len(gotH) != 1 {
		t.Fatalf("plain body should pass through: %q %v", gotB, gotH)
	}

	// Corrupt compressed data must not break display — original returned.
	bad := map[string][]string{"Content-Encoding": {"gzip"}}
	_, gotBad := decodeForDisplay(bad, []byte("not actually gzip"))
	if string(gotBad) != "not actually gzip" {
		t.Fatalf("corrupt body should fall back to original, got %q", gotBad)
	}
}
