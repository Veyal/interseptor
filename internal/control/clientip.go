package control

import (
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
)

// clientIP returns the best-effort original client IP for a request that is not
// bound to a Hub (tests, helpers). When the immediate peer is loopback
// (Tailscale Serve / Cloudflare tunnel proxying to 127.0.0.1) and the Host names
// such a tunnel, trust CF-Connecting-IP, then X-Real-IP, then the right-most
// X-Forwarded-For hop — the one the local forwarder appended. Earlier hops in
// that header are client-supplied and spoofable, so yielding one would let any
// caller claim an allowlisted source IP. Otherwise use RemoteAddr: forwarded
// headers from a non-loopback peer are spoofable, and so are they from a
// DNS-rebinding page whose own domain resolves to 127.0.0.1 (its Host is its
// domain, which is not a tunnel name).
func clientIP(r *http.Request) string {
	return clientIPFrom(r, isLoopbackIPString(remoteIP(r)) && trustedForwardHost(r.Host))
}

// clientIP (method form) resolves the caller for the allowlist and login rate
// limiter, honouring the running share tunnel's hostname as a trusted
// forwarder. Guard and allowlist handlers use this form.
func (h *Hub) clientIP(r *http.Request) string {
	return clientIPFrom(r, h.forwardedTrusted(r))
}

// forwardedTrusted reports whether r was relayed by a known local forwarder: a
// loopback TCP peer AND a Host naming the running share tunnel, a Tailscale
// Serve / Cloudflare name, or a configured trusted proxy host.
func (h *Hub) forwardedTrusted(r *http.Request) bool {
	return isLoopbackIPString(remoteIP(r)) && h.trustedForwardHost(r.Host)
}

// trustedForwardHost adds the live share tunnel to the package-level rules: the
// hostname is only trusted while that tunnel is actually serving, and an
// attacker cannot point their own Host name at it.
func (h *Hub) trustedForwardHost(hostport string) bool {
	if t, _ := h.tunnelHost.Load().(string); t != "" && hostNameOf(hostport) == t {
		return true
	}
	return trustedForwardHost(hostport)
}

// setTunnelHost records the share tunnel's public hostname (empty when stopped).
func (h *Hub) setTunnelHost(rawURL string) {
	host := ""
	if u, err := url.Parse(rawURL); err == nil {
		host = strings.ToLower(u.Hostname())
	}
	h.tunnelHost.Store(host)
}

// clientIPFrom resolves the caller when trustForwarded says the forwarded
// headers came from a trusted local forwarder. The right-most X-Forwarded-For
// hop outranks the single-value headers: a forwarder that appends to the chain
// proves that hop, while CF-Connecting-IP / X-Real-IP are just as forgeable as
// any earlier hop and must not beat it.
func clientIPFrom(r *http.Request, trustForwarded bool) string {
	peer := remoteIP(r)
	if !trustForwarded || !isLoopbackIPString(peer) {
		return peer
	}
	if hops := r.Header.Values("X-Forwarded-For"); len(hops) > 0 {
		v := hops[len(hops)-1] // last header line, appended by the closest proxy
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
	return peer
}

// hostNameOf lower-cases a Host value and drops any port and trailing dot.
func hostNameOf(hostport string) string {
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		hostport = h
	}
	return strings.TrimSuffix(strings.ToLower(hostport), ".")
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
// forwarded client-IP headers: a Tailscale Serve (*.ts.net) name, a host listed
// in INTERSEPTOR_TRUSTED_PROXY_HOSTS (comma-separated, for a named tunnel on a
// custom domain), or the live share tunnel (see Hub.trustedForwardHost).
//
// A plain loopback Host and an arbitrary *.trycloudflare.com name are NOT
// trusted: neither proves a forwarder is in front of us. A loopback peer with a
// Host it chose can be a DNS-rebinding page or a local process forging the
// header, and any quick tunnel shares the same suffix, so a stranger's tunnel
// would otherwise be trusted too. A local reverse proxy on loopback can be
// named explicitly in INTERSEPTOR_TRUSTED_PROXY_HOSTS.
func trustedForwardHost(host string) bool {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if host == "" {
		return false
	}
	if strings.HasSuffix(host, ".ts.net") {
		return true
	}
	for _, h := range strings.Split(os.Getenv("INTERSEPTOR_TRUSTED_PROXY_HOSTS"), ",") {
		if h = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(h)), "."); h != "" && h == host {
			return true
		}
	}
	return false
}
