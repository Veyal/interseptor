// Package bind holds listen-address policy shared by the CLI and control plane.
package bind

import (
	"os"
	"strings"
)

// ExternalBindAllowed reports whether non-loopback proxy/control binds are permitted.
// Allowed by default; set INTERSEPTOR_ALLOW_EXTERNAL_BIND=0 (or false/no/off) to
// lock down to loopback-only rebinding via Settings or CLI.
func ExternalBindAllowed() bool {
	v := strings.TrimSpace(os.Getenv("INTERSEPTOR_ALLOW_EXTERNAL_BIND"))
	if v == "" {
		return true
	}
	switch strings.ToLower(v) {
	case "0", "false", "no", "off":
		return false
	default:
		return true
	}
}
