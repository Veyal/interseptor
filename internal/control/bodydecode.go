package control

import (
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
	out := make(map[string][]string, len(headers)+1)
	for k, v := range headers {
		switch strings.ToLower(k) {
		case "content-encoding", "content-length":
			// dropped/replaced below so the displayed message stays coherent
		default:
			out[k] = v
		}
	}
	out["Content-Length"] = []string{strconv.Itoa(len(dec))}
	out["X-Interseptor-Decoded"] = []string{enc}
	return out, dec
}

func firstHeader(h map[string][]string, key string) string {
	for k, v := range h {
		if strings.EqualFold(k, key) && len(v) > 0 {
			return v[0]
		}
	}
	return ""
}
