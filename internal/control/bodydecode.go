package control

import (
	"io"
	"strconv"
	"strings"

	"github.com/Veyal/interseptor/internal/codec"
)

// decodeMax caps decompressed output so a compression bomb (tiny body → huge
// expansion) can't exhaust memory when a flow is opened for inspection.
const decodeMax = 24 << 20 // 24 MiB (matches codec.decompressMax)

// decodeForDisplay returns headers and body suitable for human inspection. When
// the body carries a recognized Content-Encoding (gzip / deflate / br / zstd) it
// is decompressed, the encoding header dropped, Content-Length corrected, and an
// X-Interseptor-Decoded marker added so the reader knows it was unpacked — so
// the inspector shows readable text instead of compressed bytes (which look like
// undecrypted garbage). On any failure the originals are returned unchanged;
// display must never break, and a non-compressed body passes through untouched.
func decodeForDisplay(headers map[string][]string, body []byte) (map[string][]string, []byte) {
	if len(body) == 0 {
		return headers, body
	}
	enc := strings.ToLower(strings.TrimSpace(firstHeader(headers, "Content-Encoding")))
	if enc == "" || enc == "identity" {
		return headers, body
	}
	dec, ok := codec.DecompressBody(enc, body)
	if !ok {
		return headers, body
	}
	return decodedDisplayHeaders(headers, enc, len(dec)), dec
}

// decodeForDisplayLimit is the report-safe variant: decoded output never
// exceeds max, even when a small compressed capture expands dramatically.
func decodeForDisplayLimit(headers map[string][]string, body []byte, max int) (map[string][]string, []byte, bool) {
	if len(body) == 0 {
		return headers, body, false
	}
	enc := strings.ToLower(strings.TrimSpace(firstHeader(headers, "Content-Encoding")))
	if enc == "" || enc == "identity" {
		return headers, body, false
	}
	dec, ok, truncated := codec.DecompressBodyLimit(enc, body, max)
	if !ok {
		return headers, body, truncated
	}
	return decodedDisplayHeaders(headers, enc, len(dec)), dec, truncated
}

func (h *Hub) bodyForDisplayLimit(hash string, headers map[string][]string, max int) (map[string][]string, []byte, bool, error) {
	if hash == "" || max <= 0 {
		return headers, nil, false, nil
	}
	enc := strings.ToLower(strings.TrimSpace(firstHeader(headers, "Content-Encoding")))
	if enc != "" && enc != "identity" && supportsDisplayEncoding(enc) {
		rc, err := h.st.OpenBody(hash)
		if err != nil {
			return headers, nil, false, err
		}
		decoded, ok, truncated := codec.DecompressReaderLimit(enc, rc, max)
		_ = rc.Close()
		if ok {
			return decodedDisplayHeaders(headers, enc, len(decoded)), decoded, truncated, nil
		}
	}
	rc, err := h.st.OpenBody(hash)
	if err != nil {
		return headers, nil, false, err
	}
	defer rc.Close()
	body, err := io.ReadAll(io.LimitReader(rc, int64(max)+1))
	if err != nil {
		return headers, nil, false, err
	}
	if len(body) > max {
		return headers, body[:max], true, nil
	}
	return headers, body, false, nil
}

func decodedDisplayHeaders(headers map[string][]string, enc string, length int) map[string][]string {
	out := make(map[string][]string, len(headers)+1)
	for k, v := range headers {
		switch strings.ToLower(k) {
		case "content-encoding", "content-length":
		default:
			out[k] = v
		}
	}
	out["Content-Length"] = []string{strconv.Itoa(length)}
	out["X-Interseptor-Decoded"] = []string{enc}
	return out
}

func firstHeader(h map[string][]string, key string) string {
	for k, v := range h {
		if strings.EqualFold(k, key) && len(v) > 0 {
			return v[0]
		}
	}
	return ""
}

func supportsDisplayEncoding(header string) bool {
	seen := false
	for _, part := range strings.Split(header, ",") {
		switch strings.ToLower(strings.TrimSpace(part)) {
		case "identity":
			continue
		case "gzip", "x-gzip", "deflate", "br", "zstd":
			seen = true
		default:
			return false
		}
	}
	return seen
}
