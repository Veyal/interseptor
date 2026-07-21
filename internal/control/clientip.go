package control

import (
	"net"
	"net/http"
	"strings"
)

// clientIP returns the best-effort original client IP for auth allowlisting.
// When the immediate peer is loopback (Tailscale Serve / Cloudflare tunnel
// proxying to 127.0.0.1), trust CF-Connecting-IP, then the first X-Forwarded-For
// hop, then X-Real-IP. Otherwise use RemoteAddr (never trust forwarded headers
// from a non-loopback peer — they are spoofable).
func clientIP(r *http.Request) string {
	peer := remoteIP(r)
	if isLoopbackIPString(peer) {
		if v := strings.TrimSpace(r.Header.Get("CF-Connecting-IP")); v != "" {
			if ip := hostOnlyIP(v); ip != "" {
				return ip
			}
		}
		if v := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); v != "" {
			if i := strings.IndexByte(v, ','); i >= 0 {
				v = v[:i]
			}
			if ip := hostOnlyIP(strings.TrimSpace(v)); ip != "" {
				return ip
			}
		}
		if v := strings.TrimSpace(r.Header.Get("X-Real-IP")); v != "" {
			if ip := hostOnlyIP(v); ip != "" {
				return ip
			}
		}
	}
	return peer
}

func hostOnlyIP(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if host, _, err := net.SplitHostPort(s); err == nil {
		s = host
	}
	s = strings.TrimPrefix(strings.TrimSuffix(s, "]"), "[")
	if net.ParseIP(s) == nil {
		return ""
	}
	return s
}

func isLoopbackIPString(s string) bool {
	ip := net.ParseIP(strings.TrimPrefix(strings.TrimSuffix(s, "]"), "["))
	return ip != nil && ip.IsLoopback()
}
