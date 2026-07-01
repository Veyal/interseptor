package ios

import (
	"net"
)

func lanHost() (string, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return "", err
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ipnet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			ip4 := ipnet.IP.To4()
			if ip4 == nil || ip4.IsLoopback() || ip4.IsLinkLocalUnicast() {
				continue
			}
			return ip4.String(), nil
		}
	}
	return "", errNoLAN
}

var errNoLAN = &lanErr{"no LAN IPv4 address found — connect to Wi‑Fi/Ethernet or pass wifiHost explicitly"}

type lanErr struct{ msg string }

func (e *lanErr) Error() string { return e.msg }
