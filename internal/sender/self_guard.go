package sender

import (
	"context"
	"net"
	"net/http"
	"slices"
	"strconv"
)

// SetOwnPorts installs the provider of the tool's own listener ports (control
// plane and proxy). Every destination dial on one of those ports is then vetted
// after DNS resolution by the same IPGuard the collection pipeline uses, so no
// send path (Repeater, Intruder, replay, retest, authz, macros, MCP) can reach
// the unauthenticated loopback control API through an alias such as 0.0.0.0,
// [::], ::ffff:127.0.0.1, "localhost." or a public name that resolves to
// loopback. Dials to any other port take the historical path untouched.
func (s *Sender) SetOwnPorts(fn func() []int) {
	if fn == nil {
		s.ownPorts.Store(nil)
		return
	}
	s.ownPorts.Store(&fn)
}

// selfGuardFor returns the own-listener guard for a destination port, or nil
// when port is not one of the tool's own ports (the common case, which costs a
// single slice scan).
func (s *Sender) selfGuardFor(port int) *IPGuard {
	fn := s.ownPorts.Load()
	if fn == nil {
		return nil
	}
	ports := (*fn)()
	if !slices.Contains(ports, port) {
		return nil
	}
	return &IPGuard{OwnPorts: ports, OwnIPs: localInterfaceIPs()}
}

// selfCheckAddr vets a host:port that is about to be reached through an
// upstream proxy (which resolves and connects on our behalf).
func (s *Sender) selfCheckAddr(ctx context.Context, host string, port int) error {
	g := s.selfGuardFor(port)
	if g == nil {
		return nil
	}
	return g.CheckHost(ctx, host, port)
}

// selfCheckRequest vets the destination of req when it is sent through an
// HTTP(S) upstream proxy, where the dial itself only reaches the proxy.
func (s *Sender) selfCheckRequest(req *http.Request) error {
	port, _ := strconv.Atoi(req.URL.Port())
	if port == 0 {
		port = defaultPort(req.URL.Scheme)
	}
	return s.selfCheckAddr(req.Context(), req.URL.Hostname(), port)
}

// DialTarget dials a send destination directly (no upstream proxy) with the
// own-listener guard applied. It is for send paths that speak their own wire
// protocol, such as the WebSocket repeater.
func (s *Sender) DialTarget(ctx context.Context, network, addr string) (net.Conn, error) {
	return s.dialVia(nil, ctx, network, addr)
}

// localInterfaceIPs lists every local interface address: a listener bound to
// all interfaces answers on each of them.
func localInterfaceIPs() []net.IP {
	as, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	ips := make([]net.IP, 0, len(as))
	for _, a := range as {
		if ipn, ok := a.(*net.IPNet); ok {
			ips = append(ips, ipn.IP)
		}
	}
	return ips
}
