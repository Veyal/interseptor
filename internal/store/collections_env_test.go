package store

import (
	"errors"
	"testing"
)

// An update that also replaces variables is one operation: when the variable
// set is rejected, the environment must be untouched (not renamed, not bumped).
func TestUpdateEnvironmentWithVariablesIsAtomic(t *testing.T) {
	s := newTestStore(t)
	e, err := s.CreateEnvironment(Environment{Name: "dev", Kind: "env"})
	if err != nil {
		t.Fatal(err)
	}
	next := Environment{UID: e.UID, Name: "prod", Rev: e.Rev}
	if _, err := s.UpdateEnvironmentWithVariables(next, &[]Variable{{Key: "a"}, {Key: "a"}}); !errors.Is(err, ErrCollInvalid) {
		t.Fatalf("duplicate keys: %v, want ErrCollInvalid", err)
	}
	cur, err := s.GetEnvironment(e.UID)
	if err != nil {
		t.Fatal(err)
	}
	if cur.Name != "dev" || cur.Rev != e.Rev {
		t.Fatalf("rejected update changed the environment: name=%q rev=%d, want dev/%d", cur.Name, cur.Rev, e.Rev)
	}
	if vars, err := s.ListVariables(VarOwnerEnvironment, e.UID); err != nil || len(vars) != 0 {
		t.Fatalf("rejected variables were written: %v %v", vars, err)
	}

	// The accepted path applies both, in one revision.
	up, err := s.UpdateEnvironmentWithVariables(next, &[]Variable{{Key: "a", Enabled: true}})
	if err != nil {
		t.Fatalf("valid update: %v", err)
	}
	if up.Name != "prod" || up.Rev != e.Rev+1 {
		t.Fatalf("update = %+v, want prod rev %d", up, e.Rev+1)
	}
	if vars, _ := s.ListVariables(VarOwnerEnvironment, e.UID); len(vars) != 1 || vars[0].Key != "a" {
		t.Fatalf("variables not applied: %+v", vars)
	}
	// A nil variable set leaves the existing variables alone.
	if _, err := s.UpdateEnvironmentWithVariables(Environment{UID: e.UID, Name: "prod2", Rev: up.Rev}, nil); err != nil {
		t.Fatal(err)
	}
	if vars, _ := s.ListVariables(VarOwnerEnvironment, e.UID); len(vars) != 1 {
		t.Fatalf("nil variable set dropped variables: %+v", vars)
	}
	// A stale revision is a conflict and writes nothing.
	if _, err := s.UpdateEnvironmentWithVariables(Environment{UID: e.UID, Name: "x", Rev: e.Rev}, nil); !errors.Is(err, ErrCollConflict) {
		t.Fatalf("stale rev: %v, want ErrCollConflict", err)
	}
	if cur, _ := s.GetEnvironment(e.UID); cur.Name != "prod2" {
		t.Fatalf("conflicting update renamed to %q", cur.Name)
	}
}
