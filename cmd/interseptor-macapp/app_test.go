package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeExe(t *testing.T, dir, name string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
	return p
}

func TestResolveBinaryPrefersSibling(t *testing.T) {
	dir := t.TempDir()
	want := writeExe(t, dir, serverBinaryName)

	got, err := resolveBinary(dir, func(string) (string, error) {
		t.Fatal("PATH lookup must not run when a sibling binary exists")
		return "", nil
	})
	if err != nil {
		t.Fatalf("resolveBinary: %v", err)
	}
	if got != want {
		t.Errorf("got %q, want sibling %q", got, want)
	}
}

func TestResolveBinaryIgnoresNonExecutableSibling(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, serverBinaryName)
	if err := os.WriteFile(p, []byte("not executable"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := resolveBinary(dir, func(name string) (string, error) {
		return "/usr/local/bin/" + name, nil
	})
	if err != nil {
		t.Fatalf("resolveBinary: %v", err)
	}
	if got != "/usr/local/bin/"+serverBinaryName {
		t.Errorf("got %q, want the PATH fallback", got)
	}
}

func TestResolveBinaryFallsBackToPATH(t *testing.T) {
	got, err := resolveBinary(t.TempDir(), func(name string) (string, error) {
		return "/opt/homebrew/bin/" + name, nil
	})
	if err != nil {
		t.Fatalf("resolveBinary: %v", err)
	}
	if got != "/opt/homebrew/bin/"+serverBinaryName {
		t.Errorf("got %q, want the PATH fallback", got)
	}
}

func TestResolveBinaryErrorsWhenMissingEverywhere(t *testing.T) {
	_, err := resolveBinary(t.TempDir(), func(string) (string, error) {
		return "", errors.New("not found")
	})
	if err == nil {
		t.Fatal("expected an error when the binary is neither a sibling nor on PATH")
	}
}

func TestControlURL(t *testing.T) {
	tests := []struct {
		name string
		env  string
		want string
	}{
		{"default when unset", "", "http://127.0.0.1:9966"},
		{"host and port", "127.0.0.1:9100", "http://127.0.0.1:9100"},
		{"bare port keeps loopback host", ":9100", "http://127.0.0.1:9100"},
		{"whitespace is trimmed", "  127.0.0.1:9200  ", "http://127.0.0.1:9200"},
		{"wildcard bind is reachable via loopback", "0.0.0.0:9300", "http://127.0.0.1:9300"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := controlURL(tt.env); got != tt.want {
				t.Errorf("controlURL(%q) = %q, want %q", tt.env, got, tt.want)
			}
		})
	}
}

func interseptorJSON(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"version":"1.7.2","project":"p","repo":"Veyal/interseptor"}`))
}

func TestIsInterseptorUpAcceptsRealControlPlane(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/version" {
			t.Errorf("probed %s, want /api/version", r.URL.Path)
		}
		interseptorJSON(w)
	}))
	defer srv.Close()
	if !isInterseptorUp(srv.URL, srv.Client()) {
		t.Error("a real control plane must be recognised")
	}
}

// With local auth on, /api/version is gated — but the control plane's own 401
// realm still proves Interseptor is the thing listening.
func TestIsInterseptorUpAcceptsAuthGatedControlPlane(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="interseptor"`)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	if !isInterseptorUp(srv.URL, srv.Client()) {
		t.Error("an auth-gated control plane must still count as up")
	}
}

// Regression: any HTTP response used to count as "Interseptor is already
// running", so an unrelated service on the default port made the launcher open a
// browser at it and never start the real server.
func TestIsInterseptorUpRejectsUnrelatedServices(t *testing.T) {
	cases := map[string]http.HandlerFunc{
		"plain text": func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("hello")) },
		"other json": func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`{"status":"ok"}`))
		},
		"bare 401": func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusUnauthorized) },
		"404":      func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNotFound) },
		"foreign realm": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("WWW-Authenticate", `Basic realm="grafana"`)
			w.WriteHeader(http.StatusUnauthorized)
		},
	}
	for name, h := range cases {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(h)
			defer srv.Close()
			if isInterseptorUp(srv.URL, srv.Client()) {
				t.Errorf("%s must not be mistaken for Interseptor", name)
			}
		})
	}
}

func TestIsInterseptorUpDetectsDeadServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url, client := srv.URL, srv.Client()
	srv.Close()
	if isInterseptorUp(url, client) {
		t.Error("a closed server must not be reported as up")
	}
}

func TestWaitReadyReturnsOnceReadySucceeds(t *testing.T) {
	calls := 0
	err := waitReady(context.Background(), func() bool {
		calls++
		return calls >= 3
	}, time.Millisecond)
	if err != nil {
		t.Fatalf("waitReady: %v", err)
	}
	if calls != 3 {
		t.Errorf("probed %d times, want 3", calls)
	}
}

func TestWaitReadyStopsWhenContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := waitReady(ctx, func() bool { return false }, time.Millisecond); err == nil {
		t.Fatal("expected an error once the context is cancelled")
	}
}

func TestWaitReadyStopsWhenChildExitsEarly(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := waitReady(ctx, func() bool { return false }, time.Millisecond)
	if err == nil {
		t.Fatal("expected a timeout error")
	}
	if time.Since(start) > time.Second {
		t.Error("waitReady should give up promptly when the context deadline passes")
	}
}
