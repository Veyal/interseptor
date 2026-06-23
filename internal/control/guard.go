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
func (h *Hub) securityGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isLoopbackHost(r.Host) {
			http.Error(w, "forbidden: the control plane only accepts loopback requests (rejected Host)", http.StatusForbidden)
			return
		}
		if o := r.Header.Get("Origin"); o != "" && !isLoopbackOrigin(o) {
			http.Error(w, "forbidden: cross-origin request rejected", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
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
