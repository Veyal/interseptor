//go:build windows

package control

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"os"
)

// StartActivitySocket accepts authenticated activity reports on Windows loopback TCP.
func (h *Hub) StartActivitySocket(path string) error {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("listen activity loopback: %w", err)
	}
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		_ = listener.Close()
		return fmt.Errorf("create activity token: %w", err)
	}
	token := hex.EncodeToString(tokenBytes)
	descriptor := struct {
		Address string `json:"address"`
		Token   string `json:"token"`
	}{Address: listener.Addr().String(), Token: token}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		_ = listener.Close()
		return fmt.Errorf("create activity descriptor: %w", err)
	}
	if err := json.NewEncoder(file).Encode(descriptor); err != nil {
		_ = file.Close()
		_ = listener.Close()
		_ = os.Remove(path)
		return fmt.Errorf("write activity descriptor: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = listener.Close()
		_ = os.Remove(path)
		return fmt.Errorf("close activity descriptor: %w", err)
	}
	h.activitySocketPath = path
	h.startActivityListener(listener, token)
	return nil
}

func (h *Hub) removeActivitySocket() {
	_ = os.Remove(h.activitySocketPath)
}
