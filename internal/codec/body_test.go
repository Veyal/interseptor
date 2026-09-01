package codec

import (
	"bytes"
	"compress/gzip"
	"testing"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
)

func TestDecompressBodyDecodesChainedEncodings(t *testing.T) {
	plain := []byte("chained response body")
	var gzipBuf bytes.Buffer
	gzipWriter := gzip.NewWriter(&gzipBuf)
	if _, err := gzipWriter.Write(plain); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	var brotliBuf bytes.Buffer
	brotliWriter := brotli.NewWriter(&brotliBuf)
	if _, err := brotliWriter.Write(gzipBuf.Bytes()); err != nil {
		t.Fatalf("brotli write: %v", err)
	}
	if err := brotliWriter.Close(); err != nil {
		t.Fatalf("brotli close: %v", err)
	}

	got, ok := DecompressBody("gzip, br", brotliBuf.Bytes())
	if !ok || !bytes.Equal(got, plain) {
		t.Fatalf("decoded ok=%v body=%x, want %q", ok, got, plain)
	}
}

func TestDecompressBodyLimitBoundsExpandedOutput(t *testing.T) {
	plain := bytes.Repeat([]byte("x"), 256<<10)
	var compressed bytes.Buffer
	w := gzip.NewWriter(&compressed)
	if _, err := w.Write(plain); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}

	const limit = 64 << 10
	got, ok, truncated := DecompressBodyLimit("gzip", compressed.Bytes(), limit)
	if !ok || !truncated {
		t.Fatalf("bounded decode ok=%v truncated=%v, want true/true", ok, truncated)
	}
	if len(got) != limit || !bytes.Equal(got, plain[:limit]) {
		t.Fatalf("bounded decode length=%d, want %d-byte prefix", len(got), limit)
	}
}

func TestDecompressBodyLimitRejectsZstdWindowAboveLimit(t *testing.T) {
	plain := bytes.Repeat([]byte("z"), 2<<20)
	enc, err := zstd.NewWriter(nil, zstd.WithWindowSize(2<<20), zstd.WithSingleSegment(true))
	if err != nil {
		t.Fatalf("zstd writer: %v", err)
	}
	compressed := enc.EncodeAll(plain, nil)
	enc.Close()

	if got, ok, truncated := DecompressBodyLimit("zstd", compressed, 64<<10); ok || truncated || len(got) != 0 {
		t.Fatalf("oversized-window zstd decode=(%d,%v,%v), want bounded rejection", len(got), ok, truncated)
	}
}
