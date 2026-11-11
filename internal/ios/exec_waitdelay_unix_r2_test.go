//go:build !windows

package ios

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func orphaningTool(t *testing.T, name string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nsleep 20 &\nwait\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	origTimeout, origDelay := execTimeout, execWaitDelay
	execTimeout, execWaitDelay = 300*time.Millisecond, 300*time.Millisecond
	t.Cleanup(func() { execTimeout, execWaitDelay = origTimeout, origDelay })
	return bin
}

// A killed tool whose descendant still holds the output pipe must not block the
// caller past the deadline.
func TestIdeviceExecReturnsWhenADescendantHoldsThePipe(t *testing.T) {
	bin := orphaningTool(t, "idevice_id")
	start := time.Now()
	_, err := ideviceExec(bin, "-l")
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("ideviceExec took %v: it waited for the orphaned descendant's pipe", elapsed)
	}
	if err == nil {
		t.Fatal("expected a timeout error")
	}
}

func TestSimctlExecReturnsWhenADescendantHoldsThePipe(t *testing.T) {
	bin := orphaningTool(t, "xcrun")
	orig := simctlCommandName
	simctlCommandName = bin
	t.Cleanup(func() { simctlCommandName = orig })
	start := time.Now()
	_, err := simctlExec("list")
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("simctlExec took %v: it waited for the orphaned descendant's pipe", elapsed)
	}
	if err == nil {
		t.Fatal("expected a timeout error")
	}
}
