package store

import (
	"testing"
	"time"
)

func seedPruneFlows(t *testing.T, s *Store) {
	t.Helper()
	now := time.Now()
	rows := []*Flow{
		{TS: now, Method: "GET", Scheme: "https", Host: "app.example", Port: 443, Path: "/index.html", Status: 200, Mime: "text/html", ReqLen: 100, ResLen: 900},
		{TS: now, Method: "GET", Scheme: "https", Host: "app.example", Port: 443, Path: "/app.js", Status: 200, Mime: "application/javascript", ReqLen: 50, ResLen: 5000},
		{TS: now, Method: "GET", Scheme: "https", Host: "app.example", Port: 443, Path: "/logo.png", Status: 200, Mime: "image/png", ReqLen: 50, ResLen: 2000},
		{TS: now, Method: "POST", Scheme: "https", Host: "api.example", Port: 443, Path: "/v1/login", Status: 401, Mime: "application/json", ReqLen: 80, ResLen: 120},
		{TS: now, Method: "GET", Scheme: "https", Host: "cdn.example", Port: 443, Path: "/lib.css", Status: 200, Mime: "text/css", ReqLen: 40, ResLen: 300},
	}
	for _, f := range rows {
		if _, err := s.InsertFlow(f); err != nil {
			t.Fatalf("InsertFlow: %v", err)
		}
	}
}

func TestPruneDryRunReportsAndDeletesNothing(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	seedPruneFlows(t, s)

	res, err := s.PruneFlows(PruneOptions{Hosts: []string{"app.example"}, DryRun: true})
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if !res.DryRun || res.Matched != 3 || res.Deleted != 0 || res.PruneID != 0 {
		t.Fatalf("dry run result = %+v", res)
	}
	if res.Bytes != 100+900+50+5000+50+2000 {
		t.Fatalf("bytes = %d", res.Bytes)
	}
	if len(res.PerHost) != 1 || res.PerHost[0].Host != "app.example" || res.PerHost[0].Flows != 3 {
		t.Fatalf("perHost = %+v", res.PerHost)
	}
	if n, _ := s.FlowCount(); n != 5 {
		t.Fatalf("dry run deleted flows: count=%d", n)
	}
}

func TestPruneFiltersNarrowTheSelection(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	seedPruneFlows(t, s)

	res, err := s.PruneFlows(PruneOptions{
		Hosts:        []string{"app.example"},
		Methods:      []string{"GET"},
		Statuses:     []int{200},
		MimePrefixes: []string{"image/", "application/javascript"},
		DryRun:       true,
	})
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if res.Matched != 2 {
		t.Fatalf("matched = %d, want the js and png only (%+v)", res.Matched, res.PerHost)
	}

	res, err = s.PruneFlows(PruneOptions{Hosts: []string{"app.example"}, PathPrefixes: []string{"/app."}, DryRun: true})
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if res.Matched != 1 {
		t.Fatalf("path filter matched = %d, want 1", res.Matched)
	}
}

func TestPruneUndoRestoresTheBatch(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	seedPruneFlows(t, s)

	res, err := s.PruneFlows(PruneOptions{Hosts: []string{"app.example"}, UndoWindow: time.Hour})
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if res.Deleted != 3 || res.PruneID == 0 {
		t.Fatalf("prune result = %+v", res)
	}
	if n, _ := s.FlowCount(); n != 2 {
		t.Fatalf("after prune count=%d, want 2", n)
	}

	pending, err := s.PendingPrunes()
	if err != nil || len(pending) != 1 || pending[0].Flows != 3 {
		t.Fatalf("pending = %+v, err=%v", pending, err)
	}

	restored, err := s.RestorePrune(res.PruneID)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if restored != 3 {
		t.Fatalf("restored = %d", restored)
	}
	if n, _ := s.FlowCount(); n != 5 {
		t.Fatalf("after restore count=%d, want 5", n)
	}
	// The restored rows are searchable again.
	got, err := s.QueryFlowsFilter(FlowFilter{Host: "app.example", Limit: 10})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("restored flows are missing from the FTS index")
	}
	// The snapshot is consumed: a second restore must fail.
	if _, err := s.RestorePrune(res.PruneID); err == nil {
		t.Fatal("a consumed snapshot must not restore twice")
	}
}

func TestPruneUndoWindowExpires(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	seedPruneFlows(t, s)

	res, err := s.PruneFlows(PruneOptions{Hosts: []string{"cdn.example"}, UndoWindow: time.Millisecond})
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	time.Sleep(5 * time.Millisecond)
	if _, err := s.RestorePrune(res.PruneID); err == nil {
		t.Fatal("an expired snapshot must not restore")
	}
	if pending, _ := s.PendingPrunes(); len(pending) != 0 {
		t.Fatalf("expired snapshots must not be pending: %+v", pending)
	}
}

func TestPruneKeepOnlyWithFilters(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	seedPruneFlows(t, s)

	// Keep app.example but drop its static assets: keepOnly cannot express that,
	// so it is a delete with the asset filters instead.
	res, err := s.PruneFlows(PruneOptions{
		Hosts:        []string{"app.example"},
		MimePrefixes: []string{"image/", "text/css", "application/javascript"},
		DryRun:       true,
	})
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if res.Matched != 2 {
		t.Fatalf("matched = %d, want js and png", res.Matched)
	}
}
