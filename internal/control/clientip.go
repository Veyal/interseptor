package control

import (
	"net"
	"net/http"
	"strings"
)

// clientIP returns the best-effort original client IP for auth allowlisting.
// When the immediate peer is loopback (Tailscale Serve / Cloudflare tunnel
// proxying to 127.0.0.1), trust the RIGHT-MOST X-Forwarded-For hop (the one the
// local proxy appended; every earlier hop is client-supplied and spoofable),
// then CF-Connecting-IP, then X-Real-IP. Otherwise use RemoteAddr (never trust
// forwarded headers from a non-loopback peer — they are spoofable).
func clientIP(r *http.Request) string {
	peer := remoteIP(r)
	if isLoopbackIPString(peer) {
		if hops := r.Header.Values("X-Forwarded-For"); len(hops) > 0 {
			v := hops[len(hops)-1]
			if i := strings.LastIndexByte(v, ','); i >= 0 {
				v = v[i+1:]
			}
			if ip := hostOnlyIP(strings.TrimSpace(v)); ip != "" {
				return ip
			}
		}
		if v := strings.TrimSpace(r.Header.Get("CF-Connecting-IP")); v != "" {
			if ip := hostOnlyIP(v); ip != "" {
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
