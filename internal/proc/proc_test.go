package proc_test

import (
	"os"
	"os/exec"
	"runtime"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/proc"
)

func TestListExcludesSelf(t *testing.T) {
	procs, err := proc.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	self := os.Getpid()
	for _, p := range procs {
		if p.PID == self {
			t.Fatalf("List included caller PID %d", self)
		}
	}
}

func TestAliveCurrentProcess(t *testing.T) {
	if !proc.Alive(os.Getpid()) {
		t.Fatal("Alive(self) = false, want true")
	}
}

func TestAliveNonExistentProcess(t *testing.T) {
	const deadPID = 99999999
	if proc.Alive(deadPID) {
		t.Fatalf("Alive(%d) = true, want false", deadPID)
	}
}

func TestAliveInitProcess(t *testing.T) {
	switch runtime.GOOS {
	case "linux", "darwin", "freebsd":
		if !proc.Alive(1) {
			t.Fatal("Alive(1) = false on Unix, want true")
		}
	case "windows":
		// PID 4 (System) is always present on Windows.
		if !proc.Alive(4) {
			t.Fatal("Alive(4) = false on Windows, want true")
		}
	}
}

func TestAliveInterseptorNonExistentProcess(t *testing.T) {
	const deadPID = 99999999
	if proc.AliveInterseptor(deadPID) {
		t.Fatalf("AliveInterseptor(%d) = true, want false", deadPID)
	}
}

func TestAliveInterseptorRejectsNonInterseptorProcess(t *testing.T) {
	// The test binary itself (or its "go test" host process) is alive but is
	// not an "interseptor"/"interseptor.exe" image — AliveInterseptor must
	// say false even though the generic Alive(pid) says true, which is
	// exactly the PID-reuse scenario it exists to guard against.
	self := os.Getpid()
	if !proc.Alive(self) {
		t.Fatal("Alive(self) = false, want true (sanity check)")
	}
	if proc.AliveInterseptor(self) {
		t.Fatalf("AliveInterseptor(self=%d) = true, want false — the test process is not an interseptor binary", self)
	}
}

func TestForceReapsChild(t *testing.T) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("powershell", "-NoProfile", "-Command", "Start-Sleep -Seconds 30")
	default:
		cmd = exec.Command("sleep", "30")
	}
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot start child: %v", err)
	}
	pid := cmd.Process.Pid
	t.Cleanup(func() {
		_ = proc.Force(pid)
	})

	if !proc.Alive(pid) {
		t.Fatal("child not alive after start")
	}
	if err := proc.Force(pid); err != nil {
		t.Fatalf("Force: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for proc.Alive(pid) {
		if time.Now().After(deadline) {
			t.Fatalf("child PID %d still alive after Force", pid)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
