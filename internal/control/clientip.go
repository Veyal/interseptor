package control

import (
	"net"
	"net/http"
	"os"
	"strings"
)

// clientIP returns the best-effort original client IP for auth allowlisting.
// When the immediate peer is loopback (Tailscale Serve / Cloudflare tunnel
// proxying to 127.0.0.1) and the Host names such a tunnel, trust
// CF-Connecting-IP, then the first X-Forwarded-For hop, then X-Real-IP.
// Otherwise use RemoteAddr: forwarded headers from a non-loopback peer are
// spoofable, and so are they from a DNS-rebinding page whose own domain
// resolves to 127.0.0.1 (its Host is its domain, which is not a tunnel name).
func clientIP(r *http.Request) string {
	peer := remoteIP(r)
	if isLoopbackIPString(peer) && trustedForwardHost(r.Host) {
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

// trustedForwardHost reports whether a request with this Host may carry
// forwarded client-IP headers: loopback (a local reverse proxy), a Tailscale
// Serve (*.ts.net) or Cloudflare quick-tunnel (*.trycloudflare.com) name, or a
// host listed in INTERSEPTOR_TRUSTED_PROXY_HOSTS (comma-separated, for a named
// tunnel on a custom domain).
func trustedForwardHost(host string) bool {
	if isLoopbackHost(host) {
		return true
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if host == "" {
		return false
	}
	if strings.HasSuffix(host, ".ts.net") || strings.HasSuffix(host, ".trycloudflare.com") {
		return true
	}
	for _, h := range strings.Split(os.Getenv("INTERSEPTOR_TRUSTED_PROXY_HOSTS"), ",") {
		if h = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(h)), "."); h != "" && h == host {
			return true
		}
	}
	return false
}
