package main

import (
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/Veyal/interseptor/internal/launcher"
)

// A second `interseptor launcher` that cannot bind its address must fail
// without touching the state the running launcher depends on: its dashboard
// token (the running one keeps checking the old value) and the registry.
func TestLauncherFailedBindLeavesTokenAndRegistryAlone(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	globalDir := filepath.Join(home, newDataDirName)
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	tokenPath := filepath.Join(globalDir, "launcher.token")
	const running = "token-of-the-running-launcher"
	if err := os.WriteFile(tokenPath, []byte(running), 0o600); err != nil {
		t.Fatal(err)
	}
	// The registry holds an entry the failing launcher would prune (its PID is
	// not an Interseptor process).
	regPath := filepath.Join(globalDir, "instances.json")
	reg, _ := launcher.Open(regPath)
	if err := reg.Upsert(launcher.Instance{Project: "acme", PID: 1 << 30}); err != nil {
		t.Fatal(err)
	}

	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()

	if err := runLauncher([]string{"-addr", busy.Addr().String()}); err == nil {
		t.Fatal("launcher started on an address that is already in use")
	}
	got, err := os.ReadFile(tokenPath)
	if err != nil || string(got) != running {
		t.Fatalf("launcher.token = %q (%v), want the running launcher's token untouched", got, err)
	}
	fresh, _ := launcher.Open(regPath)
	if _, ok := fresh.Get("acme"); !ok {
		t.Error("a launcher that never started rewrote the instance registry")
	}
}
