package sender

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"sort"
	"strings"
)

// ErrGuardBlocked is wrapped by every IPGuard refusal so callers can tell a
// guard decision from an ordinary network failure.
var ErrGuardBlocked = errors.New("destination blocked by IP guard")

var cgnatNet = &net.IPNet{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)}

// IPGuard vets the post-resolution destination of a dial. Because the check
// runs on the address that is actually connected to (and the connection is
// pinned to that address), DNS rebinding cannot swap in a forbidden IP between
// check and use. The zero value permits everything.
type IPGuard struct {
	// BlockPrivate denies loopback, link-local (incl. 169.254.169.254),
	// RFC1918/ULA, CGNAT 100.64.0.0/10, unspecified and multicast addresses,
	// unless the exact destination host is listed in AllowHosts.
	BlockPrivate bool
	// AllowHosts are exact (case-insensitive) hostnames or IP literals exempt
	// from BlockPrivate, e.g. hosts that are in scope.
	AllowHosts []string
	// OwnPorts and OwnIPs identify the tool's own listeners. A destination on
	// one of OwnPorts that is loopback/unspecified/in OwnIPs is refused
	// unconditionally, even for an allowed host.
	OwnPorts []int
	OwnIPs   []net.IP
}

// Check returns nil when connecting to host (resolved to ip) on port is allowed.
func (g *IPGuard) Check(host string, ip net.IP, port int) error {
	if g == nil {
		return nil
	}
	if ip4 := ip.To4(); ip4 != nil {
		ip = ip4
	}
	if g.isOwn(ip, port) {
		return fmt.Errorf("%w: %s resolves to the tool's own listener %s:%d", ErrGuardBlocked, host, ip, port)
	}
	if !g.BlockPrivate || g.allowed(host) {
		return nil
	}
	if restrictedIP(ip) {
		return fmt.Errorf("%w: %s resolves to non-public address %s", ErrGuardBlocked, host, ip)
	}
	return nil
}

// CheckHost resolves host and checks every address. It is used when the
// connection itself goes through a proxy and so cannot be vetted at dial time.
func (g *IPGuard) CheckHost(ctx context.Context, host string, port int) error {
	if g == nil {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil {
		return g.Check(host, ip, port)
	}
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return err
	}
	for _, a := range addrs {
		if err := g.Check(host, a.IP, port); err != nil {
			return err
		}
	}
	return nil
}

func (g *IPGuard) isOwn(ip net.IP, port int) bool {
	onPort := false
	for _, p := range g.OwnPorts {
		if p == port {
			onPort = true
			break
		}
	}
	if !onPort {
		return false
	}
	if ip.IsLoopback() || ip.IsUnspecified() {
		return true
	}
	for _, o := range g.OwnIPs {
		if o.Equal(ip) {
			return true
		}
	}
	return false
}

func (g *IPGuard) allowed(host string) bool {
	for _, h := range g.AllowHosts {
		if strings.EqualFold(strings.Trim(h, "[]"), host) {
			return true
		}
	}
	return false
}

func restrictedIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() || cgnatNet.Contains(ip)
}

// fingerprint is a stable cache key for the guard configuration.
func (g *IPGuard) fingerprint() string {
	if g == nil {
		return "-"
	}
	hosts := make([]string, len(g.AllowHosts))
	for i, h := range g.AllowHosts {
		hosts[i] = strings.ToLower(h)
	}
	sort.Strings(hosts)
	ports := append([]int(nil), g.OwnPorts...)
	sort.Ints(ports)
	ips := make([]string, len(g.OwnIPs))
	for i, ip := range g.OwnIPs {
		ips[i] = ip.String()
	}
	sort.Strings(ips)
	return fmt.Sprintf("%t|%v|%v|%v", g.BlockPrivate, hosts, ports, ips)
}

// guardedDial resolves addr (applying dns overrides), vets every candidate with
// g, and dials the vetted IP directly so the checked address is the connected
// one. dial performs the actual connection (it may tunnel through SOCKS).
func guardedDial(ctx context.Context, g *IPGuard, dns map[string]string, network, addr string,
	dial func(ctx context.Context, network, addr string) (net.Conn, error)) (net.Conn, error) {
	return guardedDialVia(ctx, g, dns, false, network, addr, dial)
}

// guardedDialVia is guardedDial for a dial whose proxy may resolve names itself
// (remote: a socks5h upstream, see resolvesRemotely). There, a name the local resolver cannot find is
// handed to the proxy by name, under the same rule as checkProxiedHost. A direct
// dial never does this: a second lookup could be rebound past the checks.
func guardedDialVia(ctx context.Context, g *IPGuard, dns map[string]string, remote bool, network, addr string,
	dial func(ctx context.Context, network, addr string) (net.Conn, error)) (net.Conn, error) {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	var port int
	if _, err := fmt.Sscanf(portStr, "%d", &port); err != nil {
		return nil, fmt.Errorf("invalid port %q", portStr)
	}
	var ips []net.IP
	if o, ok := dns[strings.ToLower(host)]; ok {
		ip := net.ParseIP(o)
		if ip == nil {
			return nil, fmt.Errorf("dns override for %s is not an IP: %q", host, o)
		}
		ips = []net.IP{ip}
	} else if ip := net.ParseIP(host); ip != nil {
		ips = []net.IP{ip}
	} else {
		addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			if !remote || !hostNotFound(err) {
				return nil, err
			}
			if err := unverifiableHost(g, host, port); err != nil {
				return nil, err
			}
			return dial(ctx, network, addr)
		}
		for _, a := range addrs {
			ips = append(ips, a.IP)
		}
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("resolve %q: no addresses", host)
	}
	// Every address must pass: a mixed answer is refused rather than raced.
	for _, ip := range ips {
		if err := g.Check(host, ip, port); err != nil {
			return nil, err
		}
	}
	var lastErr error
	for _, ip := range ips {
		c, err := dial(ctx, network, net.JoinHostPort(ip.String(), portStr))
		if err == nil {
			return c, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

// checkProxiedHost vets a destination that is reached through the upstream proxy,
// where the dial itself cannot be checked. A DNS override is checked, but the
// proxy still connects to the real name, so the real resolution is checked too.
// When the local resolver cannot find the host, unverifiableHost decides.
func checkProxiedHost(ctx context.Context, g *IPGuard, dns map[string]string, host string, port int) error {
	if g == nil {
		return nil
	}
	if o, ok := dns[strings.ToLower(host)]; ok {
		ip := net.ParseIP(o)
		if ip == nil {
			return fmt.Errorf("dns override for %s is not an IP: %q", host, o)
		}
		if err := g.Check(host, ip, port); err != nil {
			return err
		}
	}
	err := g.CheckHost(ctx, host, port)
	if err == nil || !hostNotFound(err) {
		return err
	}
	return unverifiableHost(g, host, port)
}

// hostNotFound reports a lookup that found no such host, as opposed to a
// resolver failure that should surface unchanged.
func hostNotFound(err error) bool {
	var dnsErr *net.DNSError
	return errors.As(err, &dnsErr) && dnsErr.IsNotFound
}

// unverifiableHost decides a destination the local resolver cannot find but an
// upstream proxy will resolve. It is refused on one of our own ports, because
// another resolver may map such a name (127.1, foo.localhost) to loopback, and
// under a guard that blocks private destinations, since the upstream might
// resolve it to one. Otherwise the upstream is left to resolve it.
func unverifiableHost(g *IPGuard, host string, port int) error {
	if g == nil {
		return nil
	}
	for _, p := range g.OwnPorts {
		if p == port {
			return fmt.Errorf("%w: %s cannot be resolved here to rule out the tool's own listener on port %d", ErrGuardBlocked, host, port)
		}
	}
	if !g.BlockPrivate || g.allowed(host) {
		return nil
	}
	return fmt.Errorf("%w: %s cannot be resolved here to confirm it is not a private address before sending it through the upstream proxy", ErrGuardBlocked, host)
}

// resolvesRemotely reports an upstream that resolves names itself. Only socks5h
// does: plain socks5 resolves locally again before connecting.
func resolvesRemotely(up *url.URL) bool {
	return up != nil && strings.EqualFold(up.Scheme, "socks5h")
}
