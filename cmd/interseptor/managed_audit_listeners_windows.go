//go:build windows

package main

import (
	"fmt"
	"os"
)

func loadManagedUIAuditListeners(string, []string) (*managedUIAuditListeners, error) {
	if os.Getenv(managedUIAuditEnv) != "1" {
		return nil, nil
	}
	return nil, fmt.Errorf("managed UI audits require inherited POSIX listeners on this platform")
}
