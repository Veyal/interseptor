//go:build unix

package mcp

import (
	"encoding/json"
	"net"
	"net/url"
	"os"
	"path/filepath"
)

// ActivitySocketReporter returns a best-effort process-local activity reporter.
func ActivitySocketReporter(path string) func(Activity) {
	return func(activity Activity) {
		if path == "" {
			return
		}
		conn, err := net.Dial("unix", path)
		if err != nil {
			return
		}
		defer conn.Close()
		_ = json.NewEncoder(conn).Encode(activity)
	}
}

// ActivitySocketPath returns process-local activity socket path for base URL.
func ActivitySocketPath(base string) string {
	u, err := url.Parse(base)
	if err != nil || u.Port() == "" {
		return ""
	}
	return filepath.Join(os.TempDir(), "interseptor-activity-"+u.Port()+".sock")
}
