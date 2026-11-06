package proxy

import (
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/scope"
	"github.com/Veyal/interseptor/internal/store"
)

// With scope-only capture on, an out-of-scope request must be counted and named,
// not silently discarded: "the app sent nothing" and "we filtered it" have to
// look different.
func TestOutOfScopeDropIsRecorded(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()
	sc := scope.New()
	sc.SetRules([]store.ScopeRule{{Action: "include", Host: "app.example", Enabled: true}})
	s := New(st, nil, nil, nil, nil)
	s.Scope = sc
	s.SetCaptureScopeOnly(true)

	flow := s.newFlow(&store.Flow{Host: "pos.example", Method: "GET", Path: "/screen"})
	if s.persistable(flow) {
		t.Fatal("an out-of-scope flow must not be persisted with scope-only capture on")
	}
	rep := s.RecentSuppressed(15 * time.Minute)
	if rep.Total != 1 {
		t.Fatalf("suppressed total = %d, want 1", rep.Total)
	}
	if len(rep.Hosts) != 1 || rep.Hosts[0].Host != "pos.example" || rep.Hosts[0].Reason != SuppressedOutOfScope {
		t.Fatalf("hosts = %+v", rep.Hosts)
	}
	if len(rep.Samples) != 1 || rep.Samples[0].Path != "/screen" {
		t.Fatalf("samples = %+v", rep.Samples)
	}

	// An in-scope request is persisted and adds nothing to the ring.
	inScope := s.newFlow(&store.Flow{Host: "app.example", Method: "GET", Path: "/me"})
	if !s.persistable(inScope) {
		t.Fatal("an in-scope flow must be persisted")
	}
	if got := s.RecentSuppressed(15 * time.Minute).Total; got != 1 {
		t.Fatalf("in-scope traffic must not be counted as suppressed: %d", got)
	}
}

func TestSuppressedWindowExcludesOldDrops(t *testing.T) {
	s := New(nil, nil, nil, nil, nil)
	s.recordSuppressed(SuppressedRequest{Host: "old.example", Method: "GET", Path: "/"})
	s.suppressed.mu.Lock()
	s.suppressed.ring[0].TS = time.Now().Add(-time.Hour).UnixMilli()
	s.suppressed.mu.Unlock()

	if got := s.RecentSuppressed(15 * time.Minute).Total; got != 0 {
		t.Fatalf("a drop older than the window must not count: %d", got)
	}
	if got := s.RecentSuppressed(2 * time.Hour).Total; got != 1 {
		t.Fatalf("a wider window must include it: %d", got)
	}
}
