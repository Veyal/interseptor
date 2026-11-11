package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// Two launchers starting at the same moment used to share one temp file name
// ("launcher.token.tmp"): one renamed it away while the other was still
// working on it, so a start failed, or a token was published that no launcher
// held. Every start must succeed, the file must hold one complete token that
// a caller was given, and no temp file may be left behind.
func TestLauncherTokenConcurrentStartsDoNotCollide(t *testing.T) {
	for round := 0; round < 20; round++ {
		dir := t.TempDir()
		const n = 8
		toks := make([]string, n)
		errs := make([]error, n)
		var wg sync.WaitGroup
		start := make(chan struct{})
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				toks[i], errs[i] = loadOrCreateLauncherToken(dir)
			}()
		}
		close(start)
		wg.Wait()
		for i, err := range errs {
			if err != nil {
				t.Fatalf("round %d: start %d failed: %v", round, i, err)
			}
		}
		got, err := os.ReadFile(filepath.Join(dir, "launcher.token"))
		if err != nil {
			t.Fatal(err)
		}
		held := false
		for _, tok := range toks {
			held = held || tok == string(got)
		}
		if !held || len(got) != 64 {
			t.Fatalf("round %d: launcher.token = %q, not a token any start returned", round, got)
		}
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			if e.Name() != "launcher.token" {
				t.Fatalf("round %d: leftover file %q", round, e.Name())
			}
		}
		if runtime.GOOS != "windows" {
			info, _ := os.Stat(filepath.Join(dir, "launcher.token"))
			if perm := info.Mode().Perm(); perm != 0o600 {
				t.Fatalf("launcher.token perms = %o, want 600", perm)
			}
		}
	}
}

// A project instance's log carries the hosts and request paths of the
// engagement (paths include query strings, which can hold tokens). It is the
// operator's alone: created 0600, and a log an older release left 0644 is
// tightened when it is reopened.
func TestInstanceLogIsPrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits")
	}
	old := syscallUmask(0) // a permissive umask must not be what keeps it private
	defer syscallUmask(old)
	dir := t.TempDir()
	path := filepath.Join(dir, "acme.log")
	f, err := openInstanceLog(path)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("first\n")
	f.Close()
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Fatalf("new log perms = %o, want 600", info.Mode().Perm())
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	f, err = openInstanceLog(path)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("second\n")
	f.Close()
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Fatalf("existing log perms = %o, want it tightened to 600", info.Mode().Perm())
	}
	if b, _ := os.ReadFile(path); !strings.HasSuffix(string(b), "first\nsecond\n") {
		t.Fatalf("log was not appended to: %q", b)
	}
}
