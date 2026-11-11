//go:build !windows

package android

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// adb starts a background daemon on first use. If the adb client is killed at
// the deadline while a descendant still holds the output pipe, CombinedOutput
// keeps waiting for that pipe to close: the "30 s bound" became unbounded.
func TestAdbExecReturnsWhenADescendantHoldsThePipe(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "adb")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nsleep 20 &\nwait\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	origName, origTimeout, origDelay := adbCommandName, adbTimeout, adbWaitDelay
	adbCommandName, adbTimeout, adbWaitDelay = bin, 300*time.Millisecond, 300*time.Millisecond
	t.Cleanup(func() { adbCommandName, adbTimeout, adbWaitDelay = origName, origTimeout, origDelay })

	start := time.Now()
	_, err := adbExec("devices")
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("adbExec took %v: it waited for the orphaned descendant's pipe", elapsed)
	}
	if err == nil {
		t.Fatal("expected a timeout error")
	}
}
