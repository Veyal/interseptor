package control

import (
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/Veyal/interseptor/internal/sender"
	"github.com/Veyal/interseptor/internal/store"
)

// targetsOwnListener reports whether rawURL points at one of Interseptor's
// own listeners by a literal local host. Request-generating tools use it as a
// fast pre-check so a prompt-injected target cannot turn the control API into
// a same-origin SSRF; ownGuard enforces the same rule on the resolved address
// at dial time (DNS names, redirect hops, local interface addresses).
func (h *Hub) targetsOwnListener(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return false
	}
	return h.isOwnListener(&store.Flow{Host: u.Hostname(), Port: atoiOr(u.Port(), defaultPortFor(u.Scheme))})
}

// isOwnListener reports whether a flow targets one of Interseptor's own
// listeners by a literal local host, comparing listener ports.
func (h *Hub) isOwnListener(f *store.Flow) bool {
	if !isLocalLiteralHost(f.Host) {
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

// isLocalLiteralHost reports whether host literally names this machine:
// loopback or unspecified IPs (0.0.0.0, ::), "localhost" and *.localhost,
// with or without a trailing dot.
func isLocalLiteralHost(host string) bool {
	h := strings.TrimSuffix(strings.Trim(strings.TrimSpace(host), "[]"), ".")
	if h == "" {
		return false
	}
	lh := strings.ToLower(h)
	if lh == "localhost" || strings.HasSuffix(lh, ".localhost") {
		return true
	}
	if i := strings.IndexByte(h, '%'); i >= 0 {
		h = h[:i] // drop an IPv6 zone
	}
	ip := net.ParseIP(h)
	return ip != nil && (ip.IsLoopback() || ip.IsUnspecified())
}

// ownGuard is the dial-time guard every control-plane send carries: it
// refuses any resolved destination on one of our listener ports that is
// loopback, unspecified or a local interface address, redirect hops included.
func (h *Hub) ownGuard() *sender.IPGuard {
	ports, ips := h.ownListeners()
	return &sender.IPGuard{OwnPorts: ports, OwnIPs: ips}
}

// isOwnDialTarget reports whether a resolved ip:port is one of our listeners
// (loopback, unspecified or a local interface address on a listener port). The
// shared sender runs it at dial time, so plain sends keep the default client.
func (h *Hub) isOwnDialTarget(ip net.IP, port int) bool {
	return h.ownGuard().Check(ip.String(), ip, port) != nil
}

// guardedOptions gives an existing per-send guard our listener ports too.
// Every other send keeps its transport: the sender's dialer refuses our
// listeners itself (isOwnDialTarget), so the shared client keeps its fast IPv4
// fallback, remote socks5h resolution and connection pool.
func (h *Hub) guardedOptions(o *sender.SendOptions) *sender.SendOptions {
	if o == nil || o.Guard == nil {
		return o
	}
	g := h.ownGuard()
	merged := *o.Guard
	merged.OwnPorts = append(append([]int(nil), merged.OwnPorts...), g.OwnPorts...)
	merged.OwnIPs = append(append([]net.IP(nil), merged.OwnIPs...), g.OwnIPs...)
	out := *o
	out.Guard = &merged
	return &out
}
