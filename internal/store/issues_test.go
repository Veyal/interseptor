package store

import "testing"

func TestReconcileIssuesRemovesOnlyStaleIssuesInScannedFlows(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if _, err := s.InsertFlow(&Flow{Method: "GET", Host: "example.com", Path: "/a"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.InsertFlow(&Flow{Method: "GET", Host: "other.example", Path: "/b"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveIssues([]Issue{
		{FlowID: 1, Severity: "High", Title: "stale", Target: "GET example.com/a"},
		{FlowID: 2, Severity: "Low", Title: "outside scan", Target: "GET other.example/b"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.ReconcileIssuesForScan([]int64{1}, nil, []Issue{
		{FlowID: 1, Severity: "Medium", Title: "current", Target: "GET example.com/a"},
	}); err != nil {
		t.Fatal(err)
	}

	got, err := s.ListIssues()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("issues = %+v, want current plus outside-scan issue", got)
	}
	seen := map[string]bool{}
	for _, issue := range got {
		seen[issue.Title] = true
	}
	if seen["stale"] || !seen["current"] || !seen["outside scan"] {
		t.Fatalf("unexpected reconciliation result: %+v", got)
	}
}

func TestReconcileIssuesPreservesUnscannedInScopeAndDropsOutOfScopeOrOrphaned(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	scannedID, _ := s.InsertFlow(&Flow{Method: "GET", Host: "new.example", Path: "/new"})
	oldInScopeID, _ := s.InsertFlow(&Flow{Method: "GET", Host: "old.example", Path: "/old"})
	outOfScopeID, _ := s.InsertFlow(&Flow{Method: "GET", Host: "gone.example", Path: "/gone"})
	if err := s.SaveIssues([]Issue{
		{FlowID: scannedID, Severity: "High", Title: "scanned stale", Target: "GET new.example/new"},
		{FlowID: oldInScopeID, Severity: "Low", Title: "older unscanned in scope", Target: "GET old.example/old"},
		{FlowID: outOfScopeID, Severity: "Low", Title: "now out of scope", Target: "GET gone.example/gone"},
		{FlowID: 999999, Severity: "Low", Title: "deleted flow", Target: "GET deleted.example/x"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.ReconcileIssuesForScan([]int64{scannedID}, []int64{outOfScopeID}, nil); err != nil {
		t.Fatal(err)
	}
	got, err := s.ListIssues()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Title != "older unscanned in scope" {
		t.Fatalf("reconciliation did not preserve only the older in-scope issue: %+v", got)
	}
}

func TestReconcileIssuesForTargetedScanPreservesUnrelatedTargets(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.InsertFlow(&Flow{Method: "GET", Host: "api.example", Path: "/a"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.InsertFlow(&Flow{Method: "GET", Host: "other.example", Path: "/b"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveIssues([]Issue{
		{FlowID: 1, Severity: "High", Title: "targeted stale", Target: "GET api.example/a"},
		{FlowID: 2, Severity: "Low", Title: "unrelated", Target: "GET other.example/b"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.ReconcileIssuesForScan([]int64{1}, nil, nil); err != nil {
		t.Fatal(err)
	}
	got, err := s.ListIssues()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Title != "unrelated" {
		t.Fatalf("targeted rescan changed unrelated issues: %+v", got)
	}
}

func TestIssueFlowsPageIsBoundedAndResumable(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var issues []Issue
	for i, host := range []string{"a.example", "b.example", "c.example"} {
		id, err := s.InsertFlow(&Flow{Method: "GET", Host: host, Path: "/"})
		if err != nil {
			t.Fatal(err)
		}
		issues = append(issues, Issue{FlowID: id, Severity: "Low", Title: host, Target: host})
		if i == 0 {
			issues = append(issues, Issue{FlowID: id, Severity: "Info", Title: host + " duplicate", Target: host + "/two"})
		}
	}
	if err := s.SaveIssues(issues); err != nil {
		t.Fatal(err)
	}
	first, err := s.IssueFlowsPage(0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 2 {
		t.Fatalf("first page len = %d, want 2", len(first))
	}
	second, err := s.IssueFlowsPage(first[len(first)-1].ID, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 1 || second[0].Host != "c.example" {
		t.Fatalf("second page = %+v", second)
	}
}
