//go:build windows

package proc_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/Veyal/interceptor/internal/proc"
)

// spawnNamed copies the current test binary to dir under name and starts it
// re-invoking only TestHelperProcessSleep (guarded by GO_WANT_HELPER_PROCESS,
// below), turning it into a long-lived process under a chosen image name —
// letting these tests exercise AliveInterceptor's image-name check against a
// real "interceptor.exe"-named process without needing the full production
// binary.
func spawnNamed(t *testing.T, name string) (pid int, cleanup func()) {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	dir := t.TempDir()
	dst := filepath.Join(dir, name)
	data, err := os.ReadFile(self)
	if err != nil {
		t.Fatalf("read self: %v", err)
	}
	if err := os.WriteFile(dst, data, 0o755); err != nil {
		t.Fatalf("write copy: %v", err)
	}

	cmd := exec.Command(dst, "-test.run", "^TestHelperProcessSleep$", "-test.v")
	cmd.Env = append(os.Environ(), "GO_WANT_HELPER_PROCESS=1")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start %s: %v", dst, err)
	}
	return cmd.Process.Pid, func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }
}

// TestHelperProcessSleep is not a real test: it's invoked as a subprocess
// (see spawnNamed) purely so the copied binary stays alive under its
// renamed image long enough for the parent test to observe it via
// AliveInterceptor. It exits immediately unless GO_WANT_HELPER_PROCESS=1.
func TestHelperProcessSleep(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	time.Sleep(5 * time.Second)
}

func TestAliveInterceptorAcceptsInterceptorImage(t *testing.T) {
	pid, cleanup := spawnNamed(t, "interceptor.exe")
	defer cleanup()

	deadline := time.Now().Add(2 * time.Second)
	for !proc.AliveInterceptor(pid) && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if !proc.AliveInterceptor(pid) {
		t.Fatalf("AliveInterceptor(%d) = false, want true for a process named interceptor.exe", pid)
	}
}

func TestAliveInterceptorRejectsOtherImage(t *testing.T) {
	pid, cleanup := spawnNamed(t, "not-interceptor.exe")
	defer cleanup()

	// Give the process a moment to actually start before asserting on it.
	deadline := time.Now().Add(2 * time.Second)
	for !proc.Alive(pid) && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if !proc.Alive(pid) {
		t.Fatalf("helper process %d never came up", pid)
	}

	if proc.AliveInterceptor(pid) {
		t.Fatalf("AliveInterceptor(%d) = true, want false for a process not named interceptor(.exe)", pid)
	}
}

// TestAliveInterceptorRejectsSystemProcess confirms the PID-reuse guard:
// PID 4 (System) always exists on Windows but is never an Interceptor
// process, so AliveInterceptor must say false even though generic Alive
// (tested separately in proc_test.go) says true for the same PID.
func TestAliveInterceptorRejectsSystemProcess(t *testing.T) {
	if proc.AliveInterceptor(4) {
		t.Fatal("AliveInterceptor(4) = true, want false (PID 4 is System, not an Interceptor process)")
	}
}

func TestAliveInterceptorRejectsNonExistentPID(t *testing.T) {
	const deadPID = 99999999
	if proc.AliveInterceptor(deadPID) {
		t.Fatalf("AliveInterceptor(%d) = true, want false", deadPID)
	}
}
