package control

import (
	"fmt"
	"net"
	"strings"

	"github.com/Veyal/interseptor/internal/netutil"
	"github.com/Veyal/interseptor/internal/store"
)

const (
	settingDeviceProxyMode = "proxy.deviceMode"
	settingDeviceProxyHost = "proxy.deviceHost"
)

// DeviceEndpoint is the host:port clients on the LAN should use for proxy setup.
type DeviceEndpoint struct {
	Mode         string `json:"mode"` // auto | manual
	Host         string `json:"host"`
	Port         int    `json:"port"`
	Endpoint     string `json:"endpoint"`
	ManualHost   string `json:"manualHost,omitempty"`
	SuggestedLAN string `json:"suggestedLAN,omitempty"`
	Source       string `json:"source"` // manual, lan_listener, all_interfaces, external_listener, loopback, default
}

func loadDeviceProxyMode(st *store.Store) string {
	if v, ok, _ := st.GetSetting(settingDeviceProxyMode); ok {
		v = strings.ToLower(strings.TrimSpace(v))
		if v == "manual" {
			return "manual"
		}
	}
	return "auto"
}

func loadDeviceProxyHost(st *store.Store) string {
	if v, ok, _ := st.GetSetting(settingDeviceProxyHost); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

func (h *Hub) resolveDeviceEndpoint() DeviceEndpoint {
	return ResolveDeviceProxyEndpoint(h.currentProxyAddrs(), loadDeviceProxyMode(h.st), loadDeviceProxyHost(h.st))
}

// deviceProxyHostPort returns the address mobile devices should use (Wi‑Fi proxy,
// physical iPhone profiles, etc.). override wins when non-empty.
func (h *Hub) deviceProxyHostPort(override string) (string, int) {
	if host := strings.TrimSpace(override); host != "" {
		_, port := proxyHostPort(h.currentProxyAddrs()[0])
		if port <= 0 {
			port = 8080
		}
		if h, p, err := net.SplitHostPort(host); err == nil {
			return h, atoiOr(p, port)
		}
		return host, port
	}
	ep := h.resolveDeviceEndpoint()
	return ep.Host, ep.Port
}

// ResolveDeviceProxyEndpoint picks the best client-facing proxy address from active
// listeners. Auto mode prefers a LAN listener on the suggested interface IP,
// then all-interfaces binds mapped to that LAN IP, then any external listener,
// then loopback for simulator-only setups.
func ResolveDeviceProxyEndpoint(addrs []string, mode, manualHost string) DeviceEndpoint {
	addrs = normalizeProxyAddrs(addrs)
	lan := netutil.ListListenHosts()

	port := 8080
	if len(addrs) > 0 {
		_, port = proxyHostPort(addrs[0])
	}
	if port <= 0 {
		port = 8080
	}

	manualHost = strings.TrimSpace(manualHost)
	if mode == "manual" && manualHost != "" {
		host := manualHost
		if h, p, err := net.SplitHostPort(manualHost); err == nil {
			host = h
			if p != "" {
				port = atoiOr(p, port)
			}
		}
		return DeviceEndpoint{
			Mode:         "manual",
			Host:         host,
			Port:         port,
			Endpoint:     fmt.Sprintf("%s:%d", host, port),
			ManualHost:   manualHost,
			SuggestedLAN: lan.SuggestedLAN,
			Source:       "manual",
		}
	}

	var lanExact, allIfaces, externalSpecific, loopback string

	for _, addr := range addrs {
		listenHost := proxyListenHost(addr)
		_, p := proxyHostPort(addr)
		if p <= 0 {
			p = port
		}

		if isLoopbackHost(listenHost) {
			if loopback == "" {
				loopback = fmt.Sprintf("127.0.0.1:%d", p)
			}
			continue
		}

		if listenHost == "" || listenHost == "0.0.0.0" || listenHost == "::" {
			clientHost := lan.Suggested
			if clientHost == "" {
				clientHost = "127.0.0.1"
			}
			if allIfaces == "" {
				allIfaces = fmt.Sprintf("%s:%d", clientHost, p)
			}
			continue
		}

		if lan.SuggestedLAN != "" && listenHost == lan.SuggestedLAN {
			lanExact = fmt.Sprintf("%s:%d", listenHost, p)
			continue
		}

		if externalSpecific == "" {
			externalSpecific = fmt.Sprintf("%s:%d", listenHost, p)
		}
	}

	chosen := ""
	source := "default"
	switch {
	case lanExact != "":
		chosen, source = lanExact, "lan_listener"
	case allIfaces != "":
		chosen, source = allIfaces, "all_interfaces"
	case externalSpecific != "":
		chosen, source = externalSpecific, "external_listener"
	case loopback != "":
		chosen, source = loopback, "loopback"
	default:
		chosen = fmt.Sprintf("127.0.0.1:%d", port)
	}

	host, p, err := net.SplitHostPort(chosen)
	if err != nil {
		host, p = "127.0.0.1", "8080"
	}
	resolvedPort := atoiOr(p, port)

	return DeviceEndpoint{
		Mode:         "auto",
		Host:         host,
		Port:         resolvedPort,
		Endpoint:     fmt.Sprintf("%s:%d", host, resolvedPort),
		SuggestedLAN: lan.SuggestedLAN,
		Source:       source,
	}
}
