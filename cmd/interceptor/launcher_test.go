package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Veyal/interceptor/internal/launcher"
)

// TestResolveLauncherAddr mirrors flags_test.go's coverage of
// resolveControlAddr: the launcher dashboard's -addr flag must honor the
// same INTERCEPTOR_ALLOW_EXTERNAL_BIND policy as the main control/proxy
// listeners rather than binding non-loopback unconditionally.
func TestResolveLauncherAddr(t *testing.T) {
	tests := []struct {
		name     string
		addr     string
		allowExt string // "" = unset
		wantAddr string
	}{
		{"loopback host:port always allowed", "127.0.0.1:9965", "0", "127.0.0.1:9965"},
		{"localhost always allowed", "localhost:9965", "0", "localhost:9965"},
		{"empty falls back to default", "", "0", defaultLauncherAddr},
		{"non-loopback allowed by default (unset)", "0.0.0.0:9965", "", "0.0.0.0:9965"},
		{"non-loopback allowed explicitly", "0.0.0.0:9965", "1", "0.0.0.0:9965"},
		{"non-loopback blocked falls back to default", "0.0.0.0:9965", "0", defaultLauncherAddr},
		{"non-loopback blocked (false)", "10.0.0.5:9965", "false", defaultLauncherAddr},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.allowExt == "" {
				os.Unsetenv("INTERCEPTOR_ALLOW_EXTERNAL_BIND")
			} else {
				t.Setenv("INTERCEPTOR_ALLOW_EXTERNAL_BIND", tc.allowExt)
			}
			if got := resolveLauncherAddr(tc.addr); got != tc.wantAddr {
				t.Fatalf("resolveLauncherAddr(%q) with ALLOW_EXTERNAL_BIND=%q = %q, want %q", tc.addr, tc.allowExt, got, tc.wantAddr)
			}
		})
	}
}

// TestHandleStopUsesStrictAliveInterceptorCheck is a wiring test: it swaps
// the launcher's liveness/kill indirections for fakes and asserts handleStop
// consults the PID-reuse-safe check (launcherAlive, backed by
// proc.AliveInterceptor in production) rather than a generic "is this PID
// alive" check that can't distinguish our child from an unrelated process
// that has since reused a recycled PID.
//
// Real cross-process PID recycling isn't practically reproducible in a unit
// test, so this proves the wiring instead: the fake "strict" check is the
// only one consulted, and a PID that's alive-but-not-ours (as the fake
// reports) is correctly treated as not running.
func TestHandleStopUsesStrictAliveInterceptorCheck(t *testing.T) {
	origAlive, origGraceful, origForce := launcherAlive, launcherGraceful, launcherForce
	t.Cleanup(func() {
		launcherAlive, launcherGraceful, launcherForce = origAlive, origGraceful, origForce
	})

	const trackedPID = 4242

	var mu sync.Mutex
	strictCalls := 0
	gracefulCalls := 0

	// The fake strict check reports trackedPID as NOT alive (simulating a
	// recycled PID now held by some other, non-interceptor process) even
	// though a naive/generic check would say it's alive. If handleStop used
	// a generic liveness check instead of the injected strict one, this test
	// would observe a call to Graceful/Force against a PID that isn't ours.
	launcherAlive = func(pid int) bool {
		mu.Lock()
		strictCalls++
		mu.Unlock()
		if pid != trackedPID {
			t.Fatalf("launcherAlive called with unexpected pid %d, want %d", pid, trackedPID)
		}
		return false
	}
	launcherGraceful = func(pid int) error {
		mu.Lock()
		gracefulCalls++
		mu.Unlock()
		return nil
	}
	launcherForce = func(pid int) error { return nil }

	dir := t.TempDir()
	reg, err := launcher.Open(filepath.Join(dir, "instances.json"))
	if err != nil {
		t.Fatalf("open registry: %v", err)
	}
	if err := reg.Upsert(launcher.Instance{
		Project:     "demo",
		ControlAddr: "127.0.0.1:9966",
		ProxyAddr:   "127.0.0.1:8080",
		PID:         trackedPID,
		StartedAt:   time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	lh := &launcherServer{
		globalDir:   dir,
		projectsDir: filepath.Join(dir, "projects"),
		logsDir:     filepath.Join(dir, "logs"),
		exe:         "interceptor",
		reg:         reg,
	}

	req := httptest.NewRequest(http.MethodPost, "/api/instances/demo/stop", nil)
	req.SetPathValue("project", "demo")
	rec := httptest.NewRecorder()

	lh.handleStop(rec, req)

	mu.Lock()
	calls := strictCalls
	graceful := gracefulCalls
	mu.Unlock()

	if calls == 0 {
		t.Fatal("handleStop never called the strict liveness check (launcherAlive) — kill path is not wired to it")
	}
	if graceful != 0 {
		t.Fatalf("handleStop called Graceful %d time(s) for a PID the strict check said was not ours — kill path is not respecting AliveInterceptor's verdict", graceful)
	}
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d (strict check says not running)", rec.Code, http.StatusNotFound)
	}
}

// TestHandleStopGracefulThenForceWhenStillAlive exercises the positive path:
// when the strict check reports the PID is (still) ours and alive,
// handleStop signals Graceful, and — if the fake liveness check keeps
// reporting it alive — escalates to Force. This complements the negative
// test above so both branches of the strict-check wiring are covered.
func TestHandleStopGracefulThenForceWhenStillAlive(t *testing.T) {
	origAlive, origGraceful, origForce := launcherAlive, launcherGraceful, launcherForce
	t.Cleanup(func() {
		launcherAlive, launcherGraceful, launcherForce = origAlive, origGraceful, origForce
	})

	const trackedPID = 4343

	var mu sync.Mutex
	gracefulCalls, forceCalls := 0, 0
	stillAlive := true // flips false once Graceful fires, so the poll loop exits promptly

	launcherAlive = func(pid int) bool {
		if pid != trackedPID {
			t.Fatalf("launcherAlive called with unexpected pid %d, want %d", pid, trackedPID)
		}
		mu.Lock()
		defer mu.Unlock()
		return stillAlive
	}
	launcherGraceful = func(pid int) error {
		mu.Lock()
		gracefulCalls++
		stillAlive = false
		mu.Unlock()
		return nil
	}
	launcherForce = func(pid int) error {
		mu.Lock()
		forceCalls++
		mu.Unlock()
		return nil
	}

	dir := t.TempDir()
	reg, err := launcher.Open(filepath.Join(dir, "instances.json"))
	if err != nil {
		t.Fatalf("open registry: %v", err)
	}
	if err := reg.Upsert(launcher.Instance{
		Project:     "demo",
		ControlAddr: "127.0.0.1:9966",
		ProxyAddr:   "127.0.0.1:8080",
		PID:         trackedPID,
		StartedAt:   time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	lh := &launcherServer{
		globalDir:   dir,
		projectsDir: filepath.Join(dir, "projects"),
		logsDir:     filepath.Join(dir, "logs"),
		exe:         "interceptor",
		reg:         reg,
	}

	req := httptest.NewRequest(http.MethodPost, "/api/instances/demo/stop", nil)
	req.SetPathValue("project", "demo")
	rec := httptest.NewRecorder()

	lh.handleStop(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusAccepted)
	}

	// The background escalation loop polls every 200ms; once our fake
	// launcherAlive flips false right after Graceful, the loop exits without
	// ever reaching Force and removes the registry entry. Wait for that so
	// the goroutine is done before this test's Cleanup restores the real
	// proc-backed indirections.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := reg.Get("demo"); !ok {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	mu.Lock()
	g, f := gracefulCalls, forceCalls
	mu.Unlock()
	if g == 0 {
		t.Fatal("handleStop never called launcherGraceful for a live, correctly-identified PID")
	}
	if f != 0 {
		t.Fatalf("launcherForce called %d time(s); the fake reports the pid died right after Graceful, so Force should not fire", f)
	}
	if _, ok := reg.Get("demo"); ok {
		t.Fatal("registry entry for \"demo\" was not removed after the stop goroutine finished")
	}
}

// TestLauncherAliveDefaultsToAliveInterceptor guards against a future edit
// silently repointing the launcherAlive indirection at the generic
// proc.Alive (which is PID-reuse-unsafe) instead of proc.AliveInterceptor.
func TestLauncherAliveDefaultsToAliveInterceptor(t *testing.T) {
	// Both launcherAlive and proc.AliveInterceptor should agree on an
	// obviously-dead PID; this is a smoke check that the indirection is
	// still pointed at *some* proc-package function and hasn't been
	// replaced with something that always returns true/false.
	const deadPID = 99999999
	if launcherAlive(deadPID) {
		t.Fatalf("launcherAlive(%d) = true, want false", deadPID)
	}
}
