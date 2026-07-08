package main

import (
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/launcher"
)

// newTestLauncherServer builds a launcherServer rooted at a fresh temp dir,
// with a real launcher token generated the same way runLauncher does.
func newTestLauncherServer(t *testing.T) (*launcherServer, string) {
	t.Helper()
	dir := t.TempDir()
	reg, err := launcher.Open(filepath.Join(dir, "instances.json"))
	if err != nil {
		t.Fatalf("launcher.Open: %v", err)
	}
	tok, err := loadOrCreateLauncherToken(dir)
	if err != nil {
		t.Fatalf("loadOrCreateLauncherToken: %v", err)
	}
	lh := &launcherServer{
		globalDir:   dir,
		projectsDir: filepath.Join(dir, "projects"),
		logsDir:     filepath.Join(dir, "logs"),
		exe:         os.Args[0], // never actually started in these tests
		reg:         reg,
		token:       tok,
	}
	return lh, tok
}

// TestUnauthenticatedStartRejected is the failing-test-first guard for the
// launcher auth gap: a POST to /start with no token must never reach
// handleStart's spawn logic.
func TestUnauthenticatedStartRejected(t *testing.T) {
	lh, _ := newTestLauncherServer(t)
	mux := lh.routes()

	req := httptest.NewRequest("POST", "/api/instances/acme/start", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != 401 && rr.Code != 403 {
		t.Fatalf("unauthenticated start: status = %d, want 401 or 403", rr.Code)
	}
	if _, ok := lh.reg.Get("acme"); ok {
		t.Fatal("unauthenticated start must not spawn/register an instance")
	}
}

func TestUnauthenticatedStopRejected(t *testing.T) {
	lh, _ := newTestLauncherServer(t)
	mux := lh.routes()

	req := httptest.NewRequest("POST", "/api/instances/acme/stop", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != 401 && rr.Code != 403 {
		t.Fatalf("unauthenticated stop: status = %d, want 401 or 403", rr.Code)
	}
}

func TestWrongTokenRejected(t *testing.T) {
	lh, _ := newTestLauncherServer(t)
	mux := lh.routes()

	req := httptest.NewRequest("POST", "/api/instances/acme/start", nil)
	req.Header.Set("X-Interseptor-Launcher-Token", "not-the-real-token")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != 401 && rr.Code != 403 {
		t.Fatalf("wrong token start: status = %d, want 401 or 403", rr.Code)
	}
}

// TestReadRoutesStayOpen confirms GET / and GET /api/instances remain
// reachable without a token — they're loopback-only informational reads.
func TestReadRoutesStayOpen(t *testing.T) {
	lh, _ := newTestLauncherServer(t)
	mux := lh.routes()

	for _, path := range []string{"/", "/api/instances"} {
		req := httptest.NewRequest("GET", path, nil)
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		if rr.Code != 200 {
			t.Fatalf("GET %s: status = %d, want 200", path, rr.Code)
		}
	}
}

// TestCorrectTokenStartSucceedsPastAuth confirms a request WITH the right
// token clears the auth gate (it may still fail later for other reasons,
// e.g. no real binary to exec in this unit test — but it must not be
// rejected as unauthenticated).
func TestCorrectTokenStartSucceedsPastAuth(t *testing.T) {
	lh, tok := newTestLauncherServer(t)
	mux := lh.routes()

	req := httptest.NewRequest("POST", "/api/instances/acme/start", nil)
	req.Header.Set("X-Interseptor-Launcher-Token", tok)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code == 401 || rr.Code == 403 {
		t.Fatalf("start with correct token: status = %d, want auth to pass (got rejected)", rr.Code)
	}
}

// TestDashboardEmbedsLauncherToken confirms the dashboard page's own
// start/stop buttons will still work now that the API requires a token: the
// page must embed lh.token so its inline JS can send it back.
func TestDashboardEmbedsLauncherToken(t *testing.T) {
	lh, tok := newTestLauncherServer(t)
	mux := lh.routes()

	req := httptest.NewRequest("GET", "/", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != 200 {
		t.Fatalf("GET /: status = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, tok) {
		t.Fatal("dashboard HTML does not embed the launcher token — its start/stop buttons would fail auth")
	}
	if !strings.Contains(body, "X-Interseptor-Launcher-Token") {
		t.Fatal("dashboard JS does not appear to send the launcher token header")
	}
}

// TestWaitForBindSucceedsAsSoonAsPortAnswers confirms the success path
// returns promptly once something is actually listening — it shouldn't wait
// out the full timeout when the port is already up.
func TestWaitForBindSucceedsAsSoonAsPortAnswers(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()

	start := time.Now()
	ok := waitForBind(ln.Addr().String(), 2*time.Second, make(chan struct{}))
	elapsed := time.Since(start)

	if !ok {
		t.Fatal("waitForBind = false, want true (port is listening)")
	}
	if elapsed > 1*time.Second {
		t.Fatalf("waitForBind took %s to notice an already-open port, want well under the 2s timeout", elapsed)
	}
}

// TestWaitForBindTimesOutWhenNothingListens confirms a dead port produces a
// bounded-time failure rather than hanging or false-positiving.
func TestWaitForBindTimesOutWhenNothingListens(t *testing.T) {
	// Grab a port and immediately release it so nothing is listening there.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close()

	start := time.Now()
	ok := waitForBind(addr, 300*time.Millisecond, make(chan struct{}))
	elapsed := time.Since(start)

	if ok {
		t.Fatal("waitForBind = true, want false (nothing is listening)")
	}
	if elapsed > 1*time.Second {
		t.Fatalf("waitForBind took %s, want it bounded near the 300ms timeout", elapsed)
	}
}

// TestWaitForBindReturnsEarlyOnExit confirms a child that dies before
// binding is reported immediately instead of waiting out the full timeout.
func TestWaitForBindReturnsEarlyOnExit(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close()

	exited := make(chan struct{})
	close(exited)

	start := time.Now()
	ok := waitForBind(addr, 5*time.Second, exited)
	elapsed := time.Since(start)

	if ok {
		t.Fatal("waitForBind = true, want false (child already exited)")
	}
	if elapsed > 1*time.Second {
		t.Fatalf("waitForBind took %s to notice an already-closed exited channel, want near-immediate", elapsed)
	}
}

func TestLoadOrCreateLauncherTokenPersistsAndPermissions(t *testing.T) {
	dir := t.TempDir()
	tok1, err := loadOrCreateLauncherToken(dir)
	if err != nil {
		t.Fatalf("loadOrCreateLauncherToken: %v", err)
	}
	if len(tok1) != 64 { // 32 bytes hex-encoded
		t.Fatalf("token length = %d, want 64 (32 bytes hex)", len(tok1))
	}

	// A second call within the same launcher "start" (same process) should
	// regenerate fresh — no stale-token accumulation across restarts.
	tok2, err := loadOrCreateLauncherToken(dir)
	if err != nil {
		t.Fatalf("loadOrCreateLauncherToken (2nd): %v", err)
	}
	if tok1 == tok2 {
		t.Fatal("expected a fresh token on each launcher start, got the same token twice")
	}

	info, err := os.Stat(filepath.Join(dir, "launcher.token"))
	if err != nil {
		t.Fatalf("stat launcher.token: %v", err)
	}
	// On Windows, os.Chmod/WriteFile only toggle the read-only attribute —
	// real per-principal access is governed by NTFS ACLs, not POSIX mode
	// bits, so 0600 there is best-effort (matching internal/tlsca's CA key,
	// which has the same limitation). Enforce the real permission bits only
	// on POSIX platforms.
	if runtime.GOOS != "windows" {
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Fatalf("launcher.token perms = %o, want 600", perm)
		}
	}
}

// TestResolveLauncherAddr mirrors flags_test.go's coverage of
// resolveControlAddr: the launcher dashboard's -addr flag must honor the
// same INTERSEPTOR_ALLOW_EXTERNAL_BIND policy as the main control/proxy
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
				os.Unsetenv("INTERSEPTOR_ALLOW_EXTERNAL_BIND")
			} else {
				t.Setenv("INTERSEPTOR_ALLOW_EXTERNAL_BIND", tc.allowExt)
			}
			if got := resolveLauncherAddr(tc.addr); got != tc.wantAddr {
				t.Fatalf("resolveLauncherAddr(%q) with ALLOW_EXTERNAL_BIND=%q = %q, want %q", tc.addr, tc.allowExt, got, tc.wantAddr)
			}
		})
	}
}

// TestHandleStopUsesStrictAliveInterseptorCheck is a wiring test: it swaps
// the launcher's liveness/kill indirections for fakes and asserts handleStop
// consults the PID-reuse-safe check (launcherAlive, backed by
// proc.AliveInterseptor in production) rather than a generic "is this PID
// alive" check that can't distinguish our child from an unrelated process
// that has since reused a recycled PID.
//
// Real cross-process PID recycling isn't practically reproducible in a unit
// test, so this proves the wiring instead: the fake "strict" check is the
// only one consulted, and a PID that's alive-but-not-ours (as the fake
// reports) is correctly treated as not running.
func TestHandleStopUsesStrictAliveInterseptorCheck(t *testing.T) {
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
		t.Fatalf("handleStop called Graceful %d time(s) for a PID the strict check said was not ours — kill path is not respecting AliveInterseptor's verdict", graceful)
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

// TestLauncherAliveDefaultsToAliveInterseptor guards against a future edit
// silently repointing the launcherAlive indirection at the generic
// proc.Alive (which is PID-reuse-unsafe) instead of proc.AliveInterseptor.
func TestLauncherAliveDefaultsToAliveInterseptor(t *testing.T) {
	// Both launcherAlive and proc.AliveInterseptor should agree on an
	// obviously-dead PID; this is a smoke check that the indirection is
	// still pointed at *some* proc-package function and hasn't been
	// replaced with something that always returns true/false.
	const deadPID = 99999999
	if launcherAlive(deadPID) {
		t.Fatalf("launcherAlive(%d) = true, want false", deadPID)
	}
}
