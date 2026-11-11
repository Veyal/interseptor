package control

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"strings"

	"github.com/Veyal/interseptor/internal/codec"
	"github.com/Veyal/interseptor/internal/store"
)

var authzStaticExt = map[string]bool{
	".js": true, ".css": true, ".map": true, ".png": true, ".jpg": true, ".jpeg": true,
	".gif": true, ".webp": true, ".svg": true, ".ico": true, ".woff": true, ".woff2": true,
	".ttf": true, ".eot": true, ".mp4": true, ".webm": true, ".mp3": true, ".pdf": true,
}

func authzSkipStatic(f *store.Flow) bool {
	if f == nil {
		return true
	}
	path := strings.ToLower(f.Path)
	if i := strings.LastIndex(path, "."); i >= 0 {
		if authzStaticExt[path[i:]] {
			return true
		}
	}
	if strings.HasPrefix(path, "/static/") || strings.HasPrefix(path, "/assets/") {
		return true
	}
	return false
}

func isAuthHeaderKey(k string) bool {
	kl := strings.ToLower(strings.TrimSpace(k))
	switch kl {
	case "cookie", "authorization", "proxy-authorization", "x-api-key", "x-auth-token",
		"x-access-token", "x-csrf-token", "x-xsrf-token":
		return true
	}
	return strings.HasPrefix(kl, "x-api-") || strings.HasPrefix(kl, "x-auth-")
}

func extractAuthHeaders(h map[string][]string) string {
	if h == nil {
		return ""
	}
	var lines []string
	for k, vs := range h {
		if !isAuthHeaderKey(k) || len(vs) == 0 || strings.TrimSpace(vs[0]) == "" {
			continue
		}
		lines = append(lines, http.CanonicalHeaderKey(k)+": "+strings.TrimSpace(vs[0]))
	}
	// stable order: cookie/auth first
	sortAuthLines(lines)
	return strings.Join(lines, "\n")
}

func sortAuthLines(lines []string) {
	priority := func(s string) int {
		l := strings.ToLower(s)
		if strings.HasPrefix(l, "cookie:") {
			return 0
		}
		if strings.HasPrefix(l, "authorization:") {
			return 1
		}
		return 2
	}
	for i := 0; i < len(lines); i++ {
		for j := i + 1; j < len(lines); j++ {
			if priority(lines[j]) < priority(lines[i]) {
				lines[i], lines[j] = lines[j], lines[i]
			}
		}
	}
}

func stripAuthHeaders(h map[string][]string) {
	for k := range h {
		if isAuthHeaderKey(k) {
			delete(h, k)
		}
	}
}

func bodySHA256(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:8]) // 16 hex chars — enough to compare
}

// comparableBodyHash is bodySHA256 of a stored response body in the form two
// replays are compared in: Content-Encoding removed when it decodes within the
// display bound, the stored bytes otherwise. An identity-encoded body is hashed
// from the stream, so its size never reaches memory; a compressed one is
// decoded into at most the display bound.
func (h *Hub) comparableBodyHash(hash string, headers map[string][]string) (string, error) {
	if hash == "" {
		return "", nil
	}
	rc, err := h.st.OpenBody(hash)
	if err != nil {
		return "", err
	}
	defer rc.Close()
	if enc := strings.ToLower(strings.TrimSpace(firstHeader(headers, "Content-Encoding"))); enc != "" && enc != "identity" && supportsDisplayEncoding(enc) {
		dec, ok, truncated := codec.DecompressReaderLimit(enc, rc, decodeMax)
		if ok && !truncated {
			return bodySHA256(dec), nil
		}
		// Not decodable (or larger than the bound): compare the stored bytes.
		_ = rc.Close()
		if rc, err = h.st.OpenBody(hash); err != nil {
			return "", err
		}
		defer rc.Close()
	}
	sum := sha256.New()
	n, err := io.Copy(sum, rc)
	if err != nil {
		return "", err
	}
	if n == 0 {
		return "", nil
	}
	return hex.EncodeToString(sum.Sum(nil)[:8]), nil
}

// maxAuthzJWTSourceBytes bounds how much of a captured body is searched for a
// JWT. A token sits in a header, the URL or a small JSON document (a login
// response); a larger body is not read at all.
var maxAuthzJWTSourceBytes = 1 << 20

// jwtSourceBody returns a captured body for the JWT search, decoded from its
// Content-Encoding. skipped reports a body over the bound, which is not
// searched (a JSON prefix cannot be parsed).
func (h *Hub) jwtSourceBody(hash string, headers map[string][]string) (body []byte, skipped bool, err error) {
	if hash == "" {
		return nil, false, nil
	}
	_, body, truncated, err := h.bodyForDisplayLimit(hash, headers, maxAuthzJWTSourceBytes)
	if err != nil || truncated {
		return nil, truncated, err
	}
	return body, false, nil
}

func authzSameAccess(baseStatus int, baseLen int64, baseHash, baseMime string, rr authzResult) bool {
	if rr.Error != "" || rr.Status == 0 || rr.Status != baseStatus {
		return false
	}
	if rr.Status >= 400 {
		return false
	}
	if baseHash != "" && rr.BodyHash != "" {
		return baseHash == rr.BodyHash
	}
	if baseMime != "" && rr.Mime != "" && baseMime != rr.Mime {
		return false
	}
	return abs64(rr.Length-baseLen) <= max64(64, baseLen/20)
}
