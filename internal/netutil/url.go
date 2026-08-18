package netutil

import (
	"net"
	"strconv"
	"strings"
)

// URLAuthority formats a host and optional port for use after scheme://.
// Stored flow hosts do not include brackets, so IPv6 literals need them even
// when the scheme's default port is omitted.
func URLAuthority(scheme, host string, port int) string {
	host = strings.TrimSpace(host)
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	}
	defaultPort := port == 0 || (strings.EqualFold(scheme, "http") && port == 80) ||
		(strings.EqualFold(scheme, "https") && port == 443)
	if !defaultPort {
		return net.JoinHostPort(host, strconv.Itoa(port))
	}
	if strings.Contains(host, ":") {
		return "[" + host + "]"
	}
	return host
}
