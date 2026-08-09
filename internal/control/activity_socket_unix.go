//go:build unix

package control

import (
	"errors"
	"net"
	"os"
)

// StartActivitySocket accepts activity reports from trusted process-local MCP clients.
func (h *Hub) StartActivitySocket(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		return err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = listener.Close()
		_ = os.Remove(path)
		return err
	}
	h.activitySocketPath = path
	h.startActivityListener(listener, "")
	return nil
}

func (h *Hub) removeActivitySocket() {
	_ = os.Remove(h.activitySocketPath)
}
