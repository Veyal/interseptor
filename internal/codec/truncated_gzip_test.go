package codec

import (
	"bytes"
	"compress/gzip"
	"testing"
)

func gzipFixture(t *testing.T, plain []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write(plain); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}

// A gzip body cut mid-stream (capture truncation, a failed transfer, a hostile
// Content-Encoding) is not a complete decoding. It used to be accepted because
// io.ErrUnexpectedEOF was whitelisted: callers then displayed/searched a short
// prefix as if it were the whole body, while br/zstd/deflate correctly failed.
// A partial decode must report failure so the caller falls back to the raw body.
func TestTruncatedGzipIsNotAcceptedAsACompleteDecode(t *testing.T) {
	plain := bytes.Repeat([]byte("0123456789"), 5000)
	full := gzipFixture(t, plain)
	for name, body := range map[string][]byte{
		"cut in the middle": full[:len(full)/2],
		"missing trailer":   full[:len(full)-8],
	} {
		t.Run(name, func(t *testing.T) {
			if got, ok, truncated := DecompressBodyLimit("gzip", body, 1<<20); ok || truncated || got != nil {
				t.Fatalf("DecompressBodyLimit accepted a truncated gzip stream: ok=%v truncated=%v len=%d", ok, truncated, len(got))
			}
			if got, ok, truncated := DecompressReaderLimit("gzip", bytes.NewReader(body), 1<<20); ok || truncated || got != nil {
				t.Fatalf("DecompressReaderLimit accepted a truncated gzip stream: ok=%v truncated=%v len=%d", ok, truncated, len(got))
			}
			if got, ok := DecompressBody("gzip", body); ok || got != nil {
				t.Fatalf("DecompressBody accepted a truncated gzip stream: ok=%v len=%d", ok, len(got))
			}
		})
	}
}

// A complete stream whose decoded output hits the cap is a different case: the
// prefix is real and callers must still get it, flagged as truncated.
func TestCompleteGzipOverTheOutputCapStillDecodes(t *testing.T) {
	plain := bytes.Repeat([]byte("x"), 128<<10)
	got, ok, truncated := DecompressBodyLimit("gzip", gzipFixture(t, plain), 32<<10)
	if !ok || !truncated || !bytes.Equal(got, plain[:32<<10]) {
		t.Fatalf("capped decode ok=%v truncated=%v len=%d", ok, truncated, len(got))
	}
}
