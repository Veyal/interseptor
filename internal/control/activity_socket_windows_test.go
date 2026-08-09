//go:build windows

package control

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestActivitySocketBindsLoopbackAndWritesAuthenticatedDescriptor(t *testing.T) {
	// Given
	h, _, _ := newHub(t)
	path := filepath.Join(t.TempDir(), "activity.json")

	// When
	if err := h.StartActivitySocket(path); err != nil {
		t.Fatalf("StartActivitySocket: %v", err)
	}
	defer h.closeActivitySocket()

	// Then
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer file.Close()
	var descriptor struct {
		Address string `json:"address"`
		Token   string `json:"token"`
	}
	if err := json.NewDecoder(file).Decode(&descriptor); err != nil {
		t.Fatalf("Decode: %v", err)
	}
	host, _, err := net.SplitHostPort(descriptor.Address)
	if err != nil {
		t.Fatalf("SplitHostPort: %v", err)
	}
	if host != "127.0.0.1" {
		t.Fatalf("host = %q, want 127.0.0.1", host)
	}
	if descriptor.Token == "" {
		t.Fatal("token is empty")
	}
}
