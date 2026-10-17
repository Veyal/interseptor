package control

import (
	"net"
	"net/url"
	"slices"
	"strings"

	"github.com/Veyal/interseptor/internal/store"
)

// targetsOwnListener reports whether rawURL points at one of Interseptor's
// own loopback listeners. Request-generating tools use this guard so a
// prompt-injected target cannot turn the control API into a same-origin SSRF.
// It is the early, literal check that yields a clear 403; the sender's
// dial-time guard (sender.SetOwnPorts) is the authoritative one and also
// covers names that only resolve to loopback.
func (h *Hub) targetsOwnListener(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return false
	}
	return h.isOwnListener(&store.Flow{Host: u.Hostname(), Port: atoiOr(u.Port(), defaultPortFor(u.Scheme))})
}

// isOwnListener reports whether a flow targets one of Interseptor's own
// listeners: a loopback alias (see isSelfAliasHost) on the control port or any
// proxy listener port.
func (h *Hub) isOwnListener(f *store.Flow) bool {
	if !isSelfAliasHost(f.Host) {
		return false
	}
	return slices.Contains(h.ownListenerPorts(), f.Port)
}

// isSelfAliasHost reports whether host names this machine without DNS:
// loopback (incl. IPv4-mapped ::ffff:127.0.0.1), the unspecified address
// (0.0.0.0 / ::, which connect to loopback), and "localhost" in any case,
// with a trailing dot, or as a *.localhost subdomain (RFC 6761).
func isSelfAliasHost(host string) bool {
	h := strings.TrimSuffix(strings.Trim(host, "[]"), ".")
	if h == "" {
		return false
	}
	if ip := net.ParseIP(h); ip != nil {
		return ip.IsLoopback() || ip.IsUnspecified()
	}
	h = strings.ToLower(h)
	return h == "localhost" || strings.HasSuffix(h, ".localhost")
}
