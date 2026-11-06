package store

import "testing"

func TestPlaybookCoverageUpsertKeepsFlowUnion(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	if _, err := s.RecordPlaybookCoverage(PlaybookCoverage{
		PlaybookID: "object-authz", Host: "api.example", Endpoint: "GET /orders/{id}", Status: CoverageConsidered, FlowIDs: []int64{7},
	}); err != nil {
		t.Fatalf("record: %v", err)
	}
	if _, err := s.RecordPlaybookCoverage(PlaybookCoverage{
		PlaybookID: "object-authz", Host: "api.example", Endpoint: "GET /orders/{id}", Status: CoverageRun, FlowIDs: []int64{9},
	}); err != nil {
		t.Fatalf("record: %v", err)
	}
	rows, err := s.ListPlaybookCoverage(PlaybookCoverageFilter{PlaybookID: "object-authz"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want one upserted row", len(rows))
	}
	if rows[0].Status != CoverageRun {
		t.Fatalf("status = %s, want the latest", rows[0].Status)
	}
	if len(rows[0].FlowIDs) != 2 {
		t.Fatalf("flow ids = %v, want both", rows[0].FlowIDs)
	}
}

func TestPlaybookCoverageRejectsUnknownStatus(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	if _, err := s.RecordPlaybookCoverage(PlaybookCoverage{PlaybookID: "x", Host: "h", Status: "maybe"}); err == nil {
		t.Fatal("an unknown status must be rejected")
	}
}

func TestCoverageSummaryRollsUp(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	for _, rec := range []PlaybookCoverage{
		{PlaybookID: "a", Host: "h1", Endpoint: "/x", Status: CoverageRun},
		{PlaybookID: "a", Host: "h2", Endpoint: "/x", Status: CoverageFound},
		{PlaybookID: "b", Host: "h1", Endpoint: "/y", Status: CoverageDismissed},
	} {
		if _, err := s.RecordPlaybookCoverage(rec); err != nil {
			t.Fatalf("record: %v", err)
		}
	}
	sum, err := s.CoverageSummary()
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	byID := map[string]PlaybookCoverageCounts{}
	for _, c := range sum {
		byID[c.PlaybookID] = c
	}
	if byID["a"].Run != 1 || byID["a"].Found != 1 {
		t.Fatalf("a = %+v", byID["a"])
	}
	if byID["b"].Dismissed != 1 {
		t.Fatalf("b = %+v", byID["b"])
	}
}
