package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"
)

// freeAddr returns a currently-unused loopback address. There is an inherent
// race between closing and re-binding, but it is fine for a local unit test.
func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("freeAddr: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close()
	return addr
}

func dialOK(t *testing.T, addr string) bool {
	t.Helper()
	c, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
	if err != nil {
		return false
	}
	c.Close()
	return true
}

// Regression: adding a second proxy listener must not fail by re-binding the
// port the first (still-running) listener already holds. Before the reconcile
// fix, RebindAddrs re-bound the entire set and errored with "address already in
// use" on the existing port.
func TestRebindAddrsAddsWithoutRebindingExisting(t *testing.T) {
	a, b := freeAddr(t), freeAddr(t)
	m := &proxyManager{handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})}
	if err := m.StartAddrs([]string{a}); err != nil {
		t.Fatalf("StartAddrs: %v", err)
	}
	defer m.Shutdown(context.Background())

	// Add b while keeping a — the failing case in the bug report.
	if err := m.RebindAddrs([]string{a, b}); err != nil {
		t.Fatalf("adding a second listener failed: %v", err)
	}
	if got := m.Addrs(); len(got) != 2 || got[0] != a || got[1] != b {
		t.Fatalf("Addrs = %v, want [%s %s]", got, a, b)
	}
	if !dialOK(t, a) || !dialOK(t, b) {
		t.Fatalf("both listeners should accept connections: a=%v b=%v", dialOK(t, a), dialOK(t, b))
	}

	// Idempotent re-apply of the same set must also succeed (nothing to rebind).
	if err := m.RebindAddrs([]string{a, b}); err != nil {
		t.Fatalf("re-applying the same set failed: %v", err)
	}
}

// Dropping an address drains that listener while the retained one keeps serving.
func TestRebindAddrsDropsRemoved(t *testing.T) {
	a, b := freeAddr(t), freeAddr(t)
	m := &proxyManager{handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})}
	if err := m.StartAddrs([]string{a, b}); err != nil {
		t.Fatalf("StartAddrs: %v", err)
	}
	defer m.Shutdown(context.Background())

	if err := m.RebindAddrs([]string{b}); err != nil {
		t.Fatalf("RebindAddrs drop: %v", err)
	}
	if got := m.Addrs(); len(got) != 1 || got[0] != b {
		t.Fatalf("Addrs = %v, want [%s]", got, b)
	}
	if !dialOK(t, b) {
		t.Fatal("retained listener b should still serve")
	}
	// a should be drained shortly (graceful shutdown is async).
	deadline := time.Now().Add(2 * time.Second)
	for dialOK(t, a) {
		if time.Now().After(deadline) {
			t.Fatal("dropped listener a is still accepting connections")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// A bad address in the desired set must not tear down the live listeners.
func TestRebindAddrsBadAddressKeepsExisting(t *testing.T) {
	a := freeAddr(t)
	m := &proxyManager{handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})}
	if err := m.StartAddrs([]string{a}); err != nil {
		t.Fatalf("StartAddrs: %v", err)
	}
	defer m.Shutdown(context.Background())

	bad := fmt.Sprintf("127.0.0.1:%d", 1<<20) // invalid port → bind fails
	if err := m.RebindAddrs([]string{a, bad}); err == nil {
		t.Fatal("expected error binding an invalid address")
	}
	if got := m.Addrs(); len(got) != 1 || got[0] != a {
		t.Fatalf("live set changed after a failed rebind: %v", got)
	}
	if !dialOK(t, a) {
		t.Fatal("existing listener a should be untouched after a failed rebind")
	}
}
