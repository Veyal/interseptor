//go:build !windows

package desktopinstall

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A tool that is killed at the context's end while a descendant still holds
// its output pipe must not keep the runner waiting for that pipe: Ctrl-C on
// the installer has to return.
func TestR3ExecRunnerReturnsWhenADescendantHoldsThePipe(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "tool")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nsleep 20 &\nwait\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	old := execWaitDelay
	execWaitDelay = 300 * time.Millisecond
	t.Cleanup(func() { execWaitDelay = old })
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, code, err := execRunner(ctx, bin)
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("execRunner took %v: it waited for the orphaned descendant's pipe", elapsed)
	}
	if err == nil && code == 0 {
		t.Fatal("a killed tool was reported as a success")
	}
}

// An ordinary run is unchanged: output and exit code are reported.
func TestR3ExecRunnerReportsOutputAndExitCode(t *testing.T) {
	out, code, err := execRunner(context.Background(), "/bin/sh", "-c", "echo hi; exit 3")
	if err != nil || code != 3 || string(out) != "hi\n" {
		t.Fatalf("out=%q code=%d err=%v", out, code, err)
	}
}
