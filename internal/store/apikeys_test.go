package store

import (
	"strings"
	"testing"
	"time"
)

func TestAPIKeyLifecycle(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	token, key, err := s.CreateAPIKey("ci-runner", ScopeFull, 0)
	if err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}
	if !strings.HasPrefix(token, "ick_") || len(token) != 52 {
		t.Fatalf("unexpected token format: %q", token)
	}
	if key.Label != "ci-runner" || !strings.HasPrefix(token, key.Prefix) {
		t.Fatalf("unexpected key meta: %+v", key)
	}
	if key.Scope != ScopeFull || key.Expires != 0 {
		t.Fatalf("expected full/never-expire defaults: %+v", key)
	}

	keys, err := s.ListAPIKeys()
	if err != nil || len(keys) != 1 || keys[0].Label != "ci-runner" {
		t.Fatalf("ListAPIKeys: %v %+v", err, keys)
	}

	if ok, _ := s.VerifyAPIKey(token); !ok {
		t.Fatal("expected the issued token to verify")
	}
	if ok, _ := s.VerifyAPIKey("ick_deadbeef"); ok {
		t.Fatal("expected a bogus token to fail verification")
	}

	if err := s.DeleteAPIKey(key.ID); err != nil {
		t.Fatalf("DeleteAPIKey: %v", err)
	}
	if keys, _ := s.ListAPIKeys(); len(keys) != 0 {
		t.Fatalf("expected key revoked, got %d", len(keys))
	}
}

func TestAPIKeyScopeAndExpiry(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	// A read-scoped key verifies with scope="read".
	rtok, _, err := s.CreateAPIKey("viewer", ScopeRead, 0)
	if err != nil {
		t.Fatalf("CreateAPIKey read: %v", err)
	}
	ok, scope, err := s.VerifyAPIKeyScope(rtok)
	if err != nil || !ok || scope != ScopeRead {
		t.Fatalf("read key verify = %v %q %v; want true read nil", ok, scope, err)
	}

	// An unknown scope normalizes to full.
	ftok, _, _ := s.CreateAPIKey("agent", "bogus", 0)
	if ok, scope, _ := s.VerifyAPIKeyScope(ftok); !ok || scope != ScopeFull {
		t.Fatalf("bogus scope should normalize to full; got ok=%v scope=%q", ok, scope)
	}

	// An already-expired key does not verify.
	etok, _, err := s.CreateAPIKey("temp", ScopeFull, time.Now().UnixMilli()-1000)
	if err != nil {
		t.Fatalf("CreateAPIKey expired: %v", err)
	}
	if ok, _, _ := s.VerifyAPIKeyScope(etok); ok {
		t.Fatal("expected an expired key to fail verification")
	}

	// A future expiry still verifies.
	vtok, _, _ := s.CreateAPIKey("soon", ScopeFull, time.Now().UnixMilli()+60_000)
	if ok, _, _ := s.VerifyAPIKeyScope(vtok); !ok {
		t.Fatal("expected a not-yet-expired key to verify")
	}

	// Empty token is a fast false.
	if ok, _, _ := s.VerifyAPIKeyScope(""); ok {
		t.Fatal("empty token must not verify")
	}
}
