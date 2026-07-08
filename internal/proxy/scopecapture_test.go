package proxy

import (
	"io"
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/store"
)

type capScope struct{ in bool }

func (c capScope) InScope(*store.Flow) bool { return c.in }

// Scope-only capture drops out-of-scope flows before they touch the DB; capture-all
// keeps everything; and with no scope configured everything is in scope.
func TestPersistableScopeOnly(t *testing.T) {
	s := &Server{}
	flow := &store.Flow{Host: "example.com", Port: 443}

	// Default (capture all): persist regardless of scope.
	s.Scope = capScope{in: false}
	if !s.persistable(flow) {
		t.Fatal("capture-all should persist even out-of-scope flows")
	}

	// Scope-only + out of scope: dropped.
	s.SetCaptureScopeOnly(true)
	if s.persistable(flow) {
		t.Fatal("scope-only must drop out-of-scope flows")
	}

	// Scope-only + in scope: kept.
	s.Scope = capScope{in: true}
	if !s.persistable(flow) {
		t.Fatal("scope-only must keep in-scope flows")
	}

	// Scope-only but no scope configured: everything is in scope, so kept.
	s.Scope = nil
	if !s.persistable(flow) {
		t.Fatal("scope-only with no scope should persist (no rules = all in scope)")
	}
}

// When a flow isn't persistable, teeBody streams the body through but stores
// nothing — so out-of-scope bodies (the bulk of disk use) never hit disk.
func TestTeeBodyPassthroughWhenNotPersistable(t *testing.T) {
	s := &Server{}
	s.Scope = capScope{in: false}
	s.SetCaptureScopeOnly(true)
	flow := &store.Flow{Host: "example.com", Port: 443}

	r, finalize, err := s.teeBody(flow, strings.NewReader("hello body"))
	if err != nil {
		t.Fatalf("teeBody: %v", err)
	}
	b, _ := io.ReadAll(r)
	if string(b) != "hello body" {
		t.Fatalf("body passthrough = %q, want %q", b, "hello body")
	}
	if h, n, _ := finalize(); h != "" || n != 0 {
		t.Fatalf("out-of-scope body must not be stored, got hash=%q len=%d", h, n)
	}
}
