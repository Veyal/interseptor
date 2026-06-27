package control

import (
	"net"
	"net/http"
	"net/url"
	"strings"
)

// securityGuard protects the loopback-only control plane from browser-driven
// attacks. Both listeners bind 127.0.0.1, but a web page the user visits can
// still POST to http://127.0.0.1:9966 (CSRF), and via DNS rebinding a foreign
// origin can be made to resolve to loopback and read responses. Two checks,
// applied to every control request, defeat both:
//
//   - Host must name the loopback interface. A rebinding attack reaches us with
//     the attacker's Host (e.g. evil.com), so rejecting non-loopback Hosts kills
//     it — this is the primary DNS-rebinding defense.
//   - Origin, when present, must be loopback. A cross-site fetch carries the
//     attacker's Origin, so rejecting it kills classic CSRF.
//
// Legitimate clients — the embedded UI, curl, the MCP server — send a loopback
// Host and either no Origin or the loopback Origin, so they pass untouched.
// maxRequestBody bounds every control request body as a DoS backstop: handlers
// decode JSON into memory and several read unbounded string fields (notes, rule
// patterns, repeater bodies, session headers). The largest legitimate body is a
// project import (128 MiB), so the global cap sits there; tighter per-endpoint
// limits (checks 512 KiB, HAR 64 MiB, OOB 512 B) still apply on top. A var (not a
// const) so tests can lower it. Loopback + AI/MCP make an oversized body a real
// local DoS vector despite the loopback bind.
var maxRequestBody int64 = 128 << 20

func (h *Hub) securityGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The OOB interaction catcher is deliberately public: blind callbacks from a
		// target arrive with a foreign Host/Origin and no auth. It only records request
		// metadata (no control actions), so it bypasses the loopback/CSRF checks.
		if strings.HasPrefix(r.URL.Path, "/oob/") {
			next.ServeHTTP(w, r)
			return
		}
		if !isLoopbackHost(r.Host) {
			httpErr(w, http.StatusForbidden, "the control plane only accepts loopback requests (rejected Host)")
			return
		}
		if o := r.Header.Get("Origin"); o != "" && !isLoopbackOrigin(o) {
			httpErr(w, http.StatusForbidden, "cross-origin request rejected")
			return
		}
		// Optional API-key auth for the remote-agent surface: once the operator has
		// created at least one key, the Streamable-HTTP MCP endpoint requires a valid
		// bearer token. Keyless installs stay open (loopback trust); the /api surface
		// and embedded UI remain loopback-only and are not gated here.
		if r.URL.Path == "/mcp" && !h.mcpAuthorized(r) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="interceptor"`)
			httpErr(w, http.StatusUnauthorized, "the MCP endpoint requires Authorization: Bearer <api key> — create one in the API tab, or remove all keys to disable auth")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
		next.ServeHTTP(w, r)
	})
}

// mcpAuthorized reports whether a request to /mcp may proceed. Auth is opt-in:
// with no API keys the endpoint is open (loopback trust); once any key exists a
// valid bearer token is required. On a store error it falls back to the last-known
// key state (mcpKeysSeen) — so a keyless install stays open through a transient DB
// hiccup, but once keys are known to exist the endpoint fails CLOSED rather than
// briefly dropping auth.
func (h *Hub) mcpAuthorized(r *http.Request) bool {
	has, err := h.st.HasAPIKeys()
	if err != nil {
		has = h.mcpKeysSeen.Load()
	} else {
		h.mcpKeysSeen.Store(has)
	}
	if !has {
		return true
	}
	tok := bearerToken(r)
	if tok == "" {
		return false
	}
	ok, _ := h.st.VerifyAPIKey(tok)
	return ok
}

// bearerToken extracts the token from an "Authorization: Bearer <token>" header.
func bearerToken(r *http.Request) string {
	const pfx = "Bearer "
	v := r.Header.Get("Authorization")
	if len(v) >= len(pfx) && strings.EqualFold(v[:len(pfx)], pfx) {
		return strings.TrimSpace(v[len(pfx):])
	}
	return ""
}

// isLoopbackHost reports whether a Host header (host or host:port) names the
// loopback interface: 127.0.0.0/8, ::1, or "localhost". An empty host (e.g. a
// bare ":8080", meaning all interfaces) is not loopback.
func isLoopbackHost(host string) bool {
	if host == "" {
		return false
	}
	h := host
	if hostOnly, _, err := net.SplitHostPort(host); err == nil {
		h = hostOnly
	}
	h = strings.TrimPrefix(strings.TrimSuffix(h, "]"), "[")
	if strings.EqualFold(h, "localhost") {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// isLoopbackOrigin reports whether an Origin header value points at loopback.
// A malformed or opaque origin (e.g. "null") is treated as non-loopback.
func isLoopbackOrigin(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	return isLoopbackHost(u.Host)
}
