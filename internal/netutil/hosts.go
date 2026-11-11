// Package netutil provides helpers for enumerating local network addresses.
package netutil

import (
	"net"
	"sort"
	"strings"
)

// Host describes a bindable listen address with a human label.
type Host struct {
	Address   string `json:"address"`
	Label     string `json:"label"`
	Kind      string `json:"kind"` // loopback, any, lan, link_local, other
	Suggested bool   `json:"suggested,omitempty"`
}

// HostsResult is returned by ListListenHosts.
type HostsResult struct {
	Hosts        []Host `json:"hosts"`
	Suggested    string `json:"suggested"`              // best default listen host for mobile Wi‑Fi proxy
	SuggestedLAN string `json:"suggestedLAN,omitempty"` // first private LAN IPv4, if any
}

// ListListenHosts returns loopback, all-interfaces, and interface IPs suitable for
// proxy/control bind pickers. Suggested is the best LAN IPv4 for device Wi‑Fi setup.
func ListListenHosts() HostsResult {
	seen := make(map[string]struct{})
	var out []Host

	add := func(addr, label, kind string) {
		addr = strings.TrimSpace(addr)
		if addr == "" {
			return
		}
		if _, ok := seen[addr]; ok {
			return
		}
		seen[addr] = struct{}{}
		out = append(out, Host{Address: addr, Label: label, Kind: kind})
	}

	add("127.0.0.1", "Loopback (localhost only)", "loopback")
	add("::1", "IPv6 loopback", "loopback")
	add("0.0.0.0", "All IPv4 interfaces", "any")
	add("::", "All IPv6 interfaces", "any")

	var suggestedLAN string
	ifaces, err := net.Interfaces()
	if err == nil {
		type ifaceHost struct {
			host Host
			prio int
		}
		var ifaceHosts []ifaceHost
		for _, iface := range ifaces {
			if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
				continue
			}
			addrs, err := iface.Addrs()
			if err != nil {
				continue
			}
			ifaceLabel := iface.Name
			if len(ifaceLabel) > 24 {
				ifaceLabel = ifaceLabel[:24] + "…"
			}
			for _, addr := range addrs {
				ipnet, ok := addr.(*net.IPNet)
				if !ok {
					continue
				}
				ip := ipnet.IP
				if ip4 := ip.To4(); ip4 != nil {
					if ip4.IsLoopback() {
						continue
					}
					addrStr := ip4.String()
					kind := "lan"
					var label string
					var prio int
					if ip4.IsLinkLocalUnicast() {
						kind = "link_local"
						label = ifaceLabel + " (link-local IPv4)"
						prio = 50
					} else if isPrivateIPv4(ip4) {
						label = ifaceLabel + " (LAN IPv4)"
						prio = 1
						if suggestedLAN == "" {
							suggestedLAN = addrStr
						}
					} else {
						kind = "other"
						label = ifaceLabel + " (public IPv4)"
						prio = 30
					}
					ifaceHosts = append(ifaceHosts, ifaceHost{
						host: Host{Address: addrStr, Label: label, Kind: kind},
						prio: prio,
					})
					continue
				}
				if ip.IsLoopback() || ip.IsLinkLocalUnicast() {
					continue
				}
				addrStr := ip.String()
				ifaceHosts = append(ifaceHosts, ifaceHost{
					host: Host{Address: addrStr, Label: ifaceLabel + " (IPv6)", Kind: "other"},
					prio: 40,
				})
			}
		}
		sort.SliceStable(ifaceHosts, func(i, j int) bool {
			if ifaceHosts[i].prio != ifaceHosts[j].prio {
				return ifaceHosts[i].prio < ifaceHosts[j].prio
			}
			return ifaceHosts[i].host.Address < ifaceHosts[j].host.Address
		})
		for _, ih := range ifaceHosts {
			add(ih.host.Address, ih.host.Label, ih.host.Kind)
		}
	}

	suggested := suggestedLAN
	if suggested == "" {
		suggested = "127.0.0.1"
	}
	for i := range out {
		if out[i].Address == suggested {
			out[i].Suggested = true
		}
	}

	return HostsResult{Hosts: out, Suggested: suggested, SuggestedLAN: suggestedLAN}
}

// SuggestedLAN returns the first private LAN IPv4 on an up, non-loopback interface.
func SuggestedLAN() (string, error) {
	r := ListListenHosts()
	if r.SuggestedLAN == "" {
		return "", errNoLAN
	}
	return r.SuggestedLAN, nil
}

var errNoLAN = &lanErr{"no LAN IPv4 address found — connect to Wi‑Fi/Ethernet or pass wifiHost explicitly"}

type lanErr struct{ msg string }

func (e *lanErr) Error() string { return e.msg }

func isPrivateIPv4(ip net.IP) bool {
	// RFC 1918 + CGNAT carrier-grade NAT range often used on tethering.
	return ip.IsPrivate() || ip[0] == 100 && ip[1] >= 64 && ip[1] <= 127
}
