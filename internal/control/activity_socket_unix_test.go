//go:build unix

package control

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestActivitySocketCloseReturnsWhenClientSendsNoJSON(t *testing.T) {
	// Given
	h, _, _ := newHub(t)
	path := filepath.Join(os.TempDir(), "interseptor-activity-close-"+strconv.Itoa(os.Getpid())+".sock")
	t.Cleanup(func() { _ = os.Remove(path) })
	if err := h.StartActivitySocket(path); err != nil {
		t.Fatalf("StartActivitySocket: %v", err)
	}
	client, err := net.Dial("unix", path)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer client.Close()

	// When
	closed := make(chan struct{})
	go func() {
		h.closeActivitySocket()
		close(closed)
	}()

	// Then
	select {
	case <-closed:
	case <-time.After(time.Second):
		_ = client.Close()
		<-closed
		t.Fatal("closeActivitySocket blocked on idle client")
	}
}

func TestActivitySocketUsesOwnerOnlyModeAndRemovesPathOnClose(t *testing.T) {
	// Given
	h, _, _ := newHub(t)
	path := filepath.Join(os.TempDir(), "interseptor-activity-close-"+strconv.Itoa(os.Getpid())+".sock")
	t.Cleanup(func() { _ = os.Remove(path) })
	if err := h.StartActivitySocket(path); err != nil {
		t.Fatalf("StartActivitySocket: %v", err)
	}

	// When
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	h.closeActivitySocket()

	// Then
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("socket mode = %o, want 600", got)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("socket path remains after close: %v", err)
	}
}
