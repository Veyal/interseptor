package control

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"

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

func authzSameAccess(baseStatus int, baseLen int64, baseHash, baseMime string, rr authzResult) bool {
	if rr.Status == 0 || rr.Status != baseStatus {
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
