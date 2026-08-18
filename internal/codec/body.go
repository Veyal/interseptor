// Package codec — body.go: HTTP response-body decompression helpers shared by
// control (display decoding) and intruder (grep decoding).
package codec

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"io"
	"strings"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
)

// decompressMax caps decompressed output so a compression bomb cannot exhaust
// memory. 24 MiB matches the limit used by the control display decoder.
const decompressMax = 24 << 20 // 24 MiB

// DecompressBody inflates body according to the Content-Encoding header value.
// It supports gzip, deflate, br (brotli), and zstd. On success it returns the
// decompressed bytes and true. On any failure (unknown encoding, corrupt data,
// empty result) it returns nil, false — callers must fall back to the raw body.
//
// Comma-separated encoding chains are decoded in reverse application order.
func DecompressBody(contentEncoding string, body []byte) ([]byte, bool) {
	if len(body) == 0 || contentEncoding == "" {
		return nil, false
	}
	parts := strings.Split(contentEncoding, ",")
	current := body
	decoded := false
	for i := len(parts) - 1; i >= 0; i-- {
		enc := strings.ToLower(strings.TrimSpace(parts[i]))
		if enc == "identity" {
			continue
		}
		if enc == "" {
			return nil, false
		}
		var ok bool
		current, ok = decompressOne(enc, current)
		if !ok {
			return nil, false
		}
		decoded = true
	}
	if !decoded {
		return nil, false
	}
	return current, true
}

func decompressOne(enc string, body []byte) ([]byte, bool) {
	var rc io.Reader
	var closer io.Closer
	switch enc {
	case "gzip", "x-gzip":
		zr, err := gzip.NewReader(bytes.NewReader(body))
		if err != nil {
			return nil, false
		}
		rc = zr
		closer = zr
	case "br":
		rc = brotli.NewReader(bytes.NewReader(body))
	case "zstd":
		zr, err := zstd.NewReader(bytes.NewReader(body))
		if err != nil {
			return nil, false
		}
		rc = zr
		closer = zr.IOReadCloser()
	case "deflate":
		// "deflate" is ambiguous: usually zlib-wrapped, sometimes raw DEFLATE.
		if zr, err := zlib.NewReader(bytes.NewReader(body)); err == nil {
			rc = zr
			closer = zr
		} else {
			fr := flate.NewReader(bytes.NewReader(body))
			rc = fr
			closer = fr
		}
	default:
		return nil, false
	}

	out, err := io.ReadAll(io.LimitReader(rc, decompressMax+1))
	if closer != nil {
		_ = closer.Close()
	}
	if (err != nil && err != io.ErrUnexpectedEOF) || len(out) == 0 || len(out) > decompressMax {
		return nil, false
	}
	return out, true
}

// IsBinaryContentType returns true when the Content-Type header value indicates
// binary content (image, audio, video, font, octet-stream, zip, wasm, pdf)
// that cannot be meaningfully grepped as text.
func IsBinaryContentType(contentType string) bool {
	ct := strings.ToLower(strings.TrimSpace(contentType))
	// Strip parameters like "; charset=utf-8"
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = strings.TrimSpace(ct[:i])
	}
	switch {
	case strings.HasPrefix(ct, "image/"),
		strings.HasPrefix(ct, "audio/"),
		strings.HasPrefix(ct, "video/"),
		strings.HasPrefix(ct, "font/"),
		ct == "application/octet-stream",
		ct == "application/zip",
		ct == "application/x-zip-compressed",
		ct == "application/gzip",
		ct == "application/x-gzip",
		ct == "application/wasm",
		ct == "application/pdf":
		return true
	}
	return false
}
