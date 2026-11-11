package sysproxy

import (
	"os"
	"strings"
	"testing"
	"time"
)

// A wedged networksetup/gsettings (an authorization prompt nobody answers, a
// hung settings daemon) must not block the settings handler, startup recovery
// or shutdown forever.
func TestRunCommandTimesOutOnHang(t *testing.T) {
	if os.Getenv("SYSPROXY_HELPER_HANG") == "1" {
		time.Sleep(2 * time.Minute)
		os.Exit(0)
	}
	orig := commandTimeout
	commandTimeout = 300 * time.Millisecond
	t.Cleanup(func() { commandTimeout = orig })
	t.Setenv("SYSPROXY_HELPER_HANG", "1")

	start := time.Now()
	_, err := runCommand(os.Args[0], "-test.run=TestRunCommandTimesOutOnHang")
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("want a timeout error, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("runCommand took %v with a %v bound", elapsed, commandTimeout)
	}
}

func TestRunCommandReturnsOutput(t *testing.T) {
	if os.Getenv("SYSPROXY_HELPER_ECHO") == "1" {
		os.Stdout.WriteString("hello")
		os.Exit(0)
	}
	t.Setenv("SYSPROXY_HELPER_ECHO", "1")
	out, err := runCommand(os.Args[0], "-test.run=TestRunCommandReturnsOutput")
	if err != nil || string(out) != "hello" {
		t.Fatalf("got %q, %v", out, err)
	}
}
