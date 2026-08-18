package codec

import (
	"bytes"
	"compress/gzip"
	"testing"

	"github.com/andybalholm/brotli"
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
