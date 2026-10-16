package control

import (
	"net"
	"strconv"

	"github.com/Veyal/interseptor/internal/collrun"
)

// ownListeners returns the ports and addresses of Interseptor's own
// listeners (proxy and control) so collection sends can never reach them,
// whatever the scope policy: every local interface address is "own".
func (h *Hub) ownListeners() ([]int, []net.IP) {
	var ports []int
	seen := map[int]bool{}
	addrs := append([]string{h.GetSelfAddr()}, h.currentProxyAddrs()...)
	for _, a := range addrs {
		_, p, err := net.SplitHostPort(a)
		if err != nil {
			continue
		}
		if n, err := strconv.Atoi(p); err == nil && n > 0 && !seen[n] {
			seen[n] = true
			ports = append(ports, n)
		}
	}
	var ips []net.IP
	if as, err := net.InterfaceAddrs(); err == nil {
		for _, a := range as {
			if ipn, ok := a.(*net.IPNet); ok {
				ips = append(ips, ipn.IP)
			}
		}
	}
	return ports, ips
}

// backend returns the process-wide collrun backend: the same store-backed
// pipeline, variable layers, persistence and script-engine routing that the
// headless CLI uses, bound to this Hub's sender, scope engine, cookie jars and
// masking registry. It is built once so variable commits stay serialized.
func (c *collectionsAPI) backend() *collrun.StoreBackend {
	c.beOnce.Do(func() {
		c.be = collrun.NewStoreBackend(collrun.StoreConfig{
			Store: c.h.st, Sender: c.h.snd, Scope: c.h.sc, Registry: c.reg, Jars: c.jars,
			Own: c.h.ownListeners, Scripts: c.h.ScriptRouter,
		})
	})
	return c.be
}
