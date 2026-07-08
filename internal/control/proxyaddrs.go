package control

import (
	"fmt"
	"net"
	"slices"
	"strings"

	"github.com/Veyal/interseptor/internal/bind"
	"github.com/Veyal/interseptor/internal/store"
)

// LoadProxyAddrs reads persisted proxy listeners for process startup.
func LoadProxyAddrs(st *store.Store) []string {
	return loadProxyAddrs(st)
}

const defaultProxyAddr = "127.0.0.1:8080"

func loadProxyAddrs(st *store.Store) []string {
	if v, ok, _ := st.GetSetting("proxy.addrs"); ok && strings.TrimSpace(v) != "" {
		return normalizeProxyAddrs(parseProxyAddrsRaw(v))
	}
	if v, ok, _ := st.GetSetting("proxy.addr"); ok && strings.TrimSpace(v) != "" {
		return []string{strings.TrimSpace(v)}
	}
	return []string{defaultProxyAddr}
}

func parseProxyAddrsRaw(raw string) []string {
	var out []string
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func normalizeProxyAddrs(addrs []string) []string {
	seen := make(map[string]struct{})
	var out []string
	for _, a := range addrs {
		a = strings.TrimSpace(a)
		if a == "" {
			continue
		}
		if _, ok := seen[a]; ok {
			continue
		}
		seen[a] = struct{}{}
		out = append(out, a)
	}
	if len(out) == 0 {
		return []string{defaultProxyAddr}
	}
	return out
}

func formatProxyAddrs(addrs []string) string {
	return strings.Join(normalizeProxyAddrs(addrs), "\n")
}

func displayProxyAddrs(addrs []string) string {
	addrs = normalizeProxyAddrs(addrs)
	if len(addrs) == 1 {
		return addrs[0]
	}
	return strings.Join(addrs, ", ")
}

func validateProxyAddrs(addrs []string) error {
	if len(addrs) == 0 {
		return fmt.Errorf("at least one proxy listen address required")
	}
	for _, addr := range addrs {
		if err := validateListenAddr(addr); err != nil {
			return err
		}
		if !isLoopbackHost(proxyListenHost(addr)) && !bind.ExternalBindAllowed() {
			return fmt.Errorf("proxy bind %s must be loopback (127.0.0.1/localhost/::1); external bind is disabled (INTERSEPTOR_ALLOW_EXTERNAL_BIND=0)", addr)
		}
	}
	return nil
}

func validateListenAddr(addr string) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("invalid listen address %q: %w", addr, err)
	}
	if strings.TrimSpace(port) == "" {
		return fmt.Errorf("invalid listen address %q: missing port", addr)
	}
	_ = host // empty host is valid (all interfaces)
	return nil
}

// proxyListenHost returns the host portion of a listen address without client-side rewriting.
func proxyListenHost(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return host
}

// isExternalProxyBind reports whether addr listens on a non-loopback interface.
func isExternalProxyBind(addr string) bool {
	host := proxyListenHost(addr)
	if host == "" || host == "0.0.0.0" || host == "::" {
		return true
	}
	return !isLoopbackHost(host)
}

func proxyAddrsEqual(a, b []string) bool {
	return slices.Equal(normalizeProxyAddrs(a), normalizeProxyAddrs(b))
}
