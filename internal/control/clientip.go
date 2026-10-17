package control

import (
	"net"
	"net/http"
	"net/url"
	"strings"
)

// clientIP returns the best-effort original client IP for auth allowlisting
// and login rate limiting. Forwarded headers are honoured only when the request
// came through a trusted local forwarder (see forwardedTrusted): then the
// RIGHT-MOST X-Forwarded-For hop (the one the local proxy appended; every
// earlier hop is client-supplied and spoofable), then CF-Connecting-IP, then
// X-Real-IP. Otherwise the TCP peer is used — a loopback peer with an arbitrary
// Host may be a DNS-rebinding page or a local process, whose headers are forged.
func (h *Hub) clientIP(r *http.Request) string {
	return clientIPFrom(r, h.forwardedTrusted(r))
}

// forwardedTrusted reports whether r was relayed by a known local forwarder:
// a loopback TCP peer AND a Host naming the running share tunnel or a
// Tailscale Serve (*.ts.net) name — hostnames an attacker cannot point at us.
func (h *Hub) forwardedTrusted(r *http.Request) bool {
	return loopbackPeer(r) && h.trustedForwardHost(r.Host)
}

func (h *Hub) trustedForwardHost(hostport string) bool {
	host := hostport
	if hostOnly, _, err := net.SplitHostPort(hostport); err == nil {
		host = hostOnly
	}
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if host == "" {
		return false
	}
	if strings.HasSuffix(host, ".ts.net") {
		return true
	}
	t, _ := h.tunnelHost.Load().(string)
	return t != "" && host == t
}

// setTunnelHost records the share tunnel's public hostname (empty when stopped).
func (h *Hub) setTunnelHost(rawURL string) {
	host := ""
	if u, err := url.Parse(rawURL); err == nil {
		host = strings.ToLower(u.Hostname())
	}
	h.tunnelHost.Store(host)
}

func clientIPFrom(r *http.Request, trustForwarded bool) string {
	peer := remoteIP(r)
	if trustForwarded && isLoopbackIPString(peer) {
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
