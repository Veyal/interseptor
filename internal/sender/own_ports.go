package sender

import (
	"context"
	"net"
	"slices"
)

// SetOwnPorts installs the provider of the tool's own listener ports (the
// control plane and every proxy listener). Dial-time protection from
// SetOwnListenerCheck cannot see a destination that an HTTP upstream proxy
// resolves and connects to on our behalf — the dial itself only reaches the
// proxy — so that hop is vetted with these ports instead.
func (s *Sender) SetOwnPorts(fn func() []int) {
	if fn == nil {
		s.ownPorts.Store(nil)
		return
	}
	s.ownPorts.Store(&fn)
}

// ownPortList reports the tool's own listener ports, or nil when unset.
func (s *Sender) ownPortList() []int {
	fn := s.ownPorts.Load()
	if fn == nil {
		return nil
	}
	return (*fn)()
}

// checkProxiedOwnPort refuses a proxied destination that lands on one of the
// tool's own listeners: loopback, the unspecified address or a local interface
// address on an own port. Dials to any other port are untouched.
func checkProxiedOwnPort(ctx context.Context, ports []int, dns map[string]string, host string, port int) error {
	if len(ports) == 0 || !slices.Contains(ports, port) {
		return nil
	}
	return checkProxiedHost(ctx, &IPGuard{OwnPorts: ports, OwnIPs: localInterfaceIPs()}, dns, host, port)
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
