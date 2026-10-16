package sender

import (
	"context"
	"errors"
	"fmt"
	"net"
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
			return nil, err
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
