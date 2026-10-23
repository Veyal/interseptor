package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// launcherTestOrigin is the loopback origin tests address the launcher on.
const launcherTestOrigin = "http://127.0.0.1:9965"

// A DNS-rebinding page reaches the launcher with its own Host header; the
// dashboard (which embeds the token) and the API must refuse it.
func TestLauncherRejectsNonLoopbackHost(t *testing.T) {
	lh, tok := newTestLauncherServer(t)
	mux := lh.routes()
	for _, path := range []string{"/", "/api/instances"} {
		req := httptest.NewRequest("GET", "http://attacker.example:9965"+path, nil)
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		if rr.Code != http.StatusForbidden {
			t.Fatalf("GET %s with Host attacker.example: status = %d, want 403", path, rr.Code)
		}
		if strings.Contains(rr.Body.String(), tok) {
			t.Fatalf("GET %s leaked the launcher token to a foreign Host", path)
		}
	}
	for _, host := range []string{"127.0.0.1:9965", "localhost:9965", "[::1]:9965"} {
		req := httptest.NewRequest("GET", launcherTestOrigin+"/api/instances", nil)
		req.Host = host
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("Host %s: status = %d, want 200", host, rr.Code)
		}
	}
}

func TestLauncherRejectsCrossOriginMutation(t *testing.T) {
	lh, tok := newTestLauncherServer(t)
	mux := lh.routes()
	req := httptest.NewRequest("POST", launcherTestOrigin+"/api/instances/acme/stop", nil)
	req.Header.Set(launcherTokenHeader, tok)
	req.Header.Set("Origin", "http://attacker.example")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("cross-origin stop: status = %d, want 403", rr.Code)
	}

	same := httptest.NewRequest("POST", launcherTestOrigin+"/api/instances/acme/stop", nil)
	same.Header.Set(launcherTokenHeader, tok)
	same.Header.Set("Origin", launcherTestOrigin)
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, same)
	if rr.Code == http.StatusForbidden || rr.Code == http.StatusUnauthorized {
		t.Fatalf("same-origin stop: status = %d, want auth to pass", rr.Code)
	}
}
