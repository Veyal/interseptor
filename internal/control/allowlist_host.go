package control

import (
	"net"
	"net/http"
	"os"
	"strings"
)

// loopbackPeer reports whether the TCP peer is loopback, i.e. the request was
// relayed by a local reverse proxy or tunnel (which routes by its own
// configured hostname, so the Host it forwards is not attacker-chosen DNS).
func loopbackPeer(r *http.Request) bool { return isLoopbackIPString(remoteIP(r)) }

// localNameSuffixes are DNS suffixes that cannot be registered publicly, so an
// attacker's rebinding domain can never use them.
var localNameSuffixes = []string{".local", ".ts.net", ".home.arpa", ".lan", ".internal", ".localdomain"}

// directHostNameOK reports whether a Host header on a DIRECT (non-proxied)
// connection names this machine in a way a DNS-rebinding page cannot: an IP
// literal, a single-label or non-public local name (mini, mini.local, MagicDNS
// *.ts.net), or the machine's own hostname.
func directHostNameOK(hostport string) bool {
	h := hostport
	if hostOnly, _, err := net.SplitHostPort(hostport); err == nil {
		h = hostOnly
	}
	h = strings.ToLower(strings.TrimSuffix(strings.Trim(h, "[]"), "."))
	if h == "" {
		return false
	}
	if net.ParseIP(h) != nil || !strings.Contains(h, ".") {
		return true
	}
	for _, sfx := range localNameSuffixes {
		if strings.HasSuffix(h, sfx) {
			return true
		}
	}
	if name, err := os.Hostname(); err == nil && name != "" {
		name = strings.ToLower(strings.TrimSuffix(name, "."))
		if h == name || h == strings.SplitN(name, ".", 2)[0] {
			return true
		}
	}
	return false
}
