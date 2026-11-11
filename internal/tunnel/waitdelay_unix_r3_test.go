//go:build !windows

package tunnel

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// Close does not wait for a descendant of the tunnel process that inherited
// its stderr: the child is killed and reaped, and Close returns. (The child's
// stderr is a StderrPipe read by the manager, so cmd.Wait has no copy
// goroutine to wait for and needs no WaitDelay; this pins that.)
func TestR3CloseReturnsWhenADescendantHoldsStderr(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "cloudflared")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nsleep 20 &\nwait\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	m := New(func() string { return "9966" })
	m.lookPath = func(string) (string, error) { return bin, nil }
	m.commandContext = exec.CommandContext
	if _, err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond) // let the shell fork its sleeper
	done := make(chan struct{})
	go func() { m.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Close is still waiting 5s after the tunnel process was killed")
	}
}
