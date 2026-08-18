package control

import (
	"net"
	"net/url"
	"strconv"

	"github.com/Veyal/interseptor/internal/store"
)

// targetsOwnListener reports whether rawURL points at one of Interseptor's
// own loopback listeners. Request-generating tools use this guard so a
// prompt-injected target cannot turn the control API into a same-origin SSRF.
func (h *Hub) targetsOwnListener(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return false
	}
	return h.isOwnListener(&store.Flow{Host: u.Hostname(), Port: atoiOr(u.Port(), defaultPortFor(u.Scheme))})
}

// isOwnListener reports whether a flow targets one of Interseptor's own
// loopback listeners, comparing normalized loopback hosts and listener ports.
func (h *Hub) isOwnListener(f *store.Flow) bool {
	if !isLoopbackHost(f.Host) {
		return false
	}
	for _, addr := range []string{h.GetSelfAddr(), h.currentProxyAddr()} {
		if _, port, err := net.SplitHostPort(addr); err == nil {
			if n, err := strconv.Atoi(port); err == nil && n == f.Port {
				return true
			}
		}
	}
	return false
}
