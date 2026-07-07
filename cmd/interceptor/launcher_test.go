package main

import (
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Veyal/interceptor/internal/launcher"
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
	req.Header.Set("X-Interceptor-Launcher-Token", "not-the-real-token")
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
	req.Header.Set("X-Interceptor-Launcher-Token", tok)
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
	if !strings.Contains(body, "X-Interceptor-Launcher-Token") {
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
