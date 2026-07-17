package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/intercept"
	"github.com/Veyal/interseptor/internal/store"
)

func TestScannerTargetsReturnsAllDistinctInScopeHosts(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.CreateScopeRule(&store.ScopeRule{Enabled: true, Action: "include", Host: "api.example.com"}); err != nil {
		t.Fatal(err)
	}
	for _, host := range []string{"api.example.com", "other.example.com"} {
		if _, err := st.InsertFlow(&store.Flow{TS: time.Now(), Method: "GET", Scheme: "https", Host: host, Path: "/", Status: 200}); err != nil {
			t.Fatal(err)
		}
	}
	h := New(st, intercept.New(), nil, nil, nil)
	defer h.Close()
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/scanner/targets")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body struct {
		Hosts []struct {
			Host  string `json:"host"`
			Count int64  `json:"count"`
		} `json:"hosts"`
		Truncated bool `json:"truncated"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Truncated || len(body.Hosts) != 1 || body.Hosts[0].Host != "api.example.com" || body.Hosts[0].Count != 1 {
		t.Fatalf("unexpected complete in-scope host response: %+v", body)
	}
}

func TestScannerTargetsIncludesHostsBeyondFirstPage(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.CreateScopeRule(&store.ScopeRule{Enabled: true, Action: "include", Host: "*.example.com"}); err != nil {
		t.Fatal(err)
	}
	for _, host := range []string{"old.example.com", "two.example.com", "three.example.com", "four.example.com", "new.example.com"} {
		if _, err := st.InsertFlow(&store.Flow{TS: time.Now(), Method: "GET", Scheme: "https", Host: host, Path: "/", Status: 200}); err != nil {
			t.Fatal(err)
		}
	}
	h := New(st, intercept.New(), nil, nil, nil)
	defer h.Close()
	rr := httptest.NewRecorder()
	(&scannerAPI{h}).scannerTargetsWithBatch(rr, httptest.NewRequest(http.MethodGet, "/api/scanner/targets", nil), 2)

	var body struct {
		Hosts []struct {
			Host string `json:"host"`
		} `json:"hosts"`
		Truncated bool `json:"truncated"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, host := range body.Hosts {
		seen[host.Host] = true
	}
	if body.Truncated || len(body.Hosts) != 5 || !seen["old.example.com"] {
		t.Fatalf("older in-scope hosts were truncated: %+v", body)
	}
}

func TestScannerFullRescanDropsOutOfScopeIssuesButNotCuratedFindings(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.CreateScopeRule(&store.ScopeRule{Enabled: true, Action: "include", Host: "kept.example.com"}); err != nil {
		t.Fatal(err)
	}
	oldID, _ := st.InsertFlow(&store.Flow{TS: time.Now(), Method: "GET", Scheme: "https", Host: "old.example.com", Path: "/old", Status: 200})
	keptID, _ := st.InsertFlow(&store.Flow{TS: time.Now(), Method: "GET", Scheme: "https", Host: "kept.example.com", Path: "/kept", Status: 200})
	if err := st.SaveIssues([]store.Issue{
		{FlowID: oldID, Severity: "High", Title: "out of scope stale", Target: "GET old.example.com/old"},
		{FlowID: keptID, Severity: "Low", Title: "not reproduced", Target: "GET kept.example.com/kept"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateFinding(&store.Finding{Title: "curated", Severity: "High", Status: "open"}); err != nil {
		t.Fatal(err)
	}
	h := New(st, intercept.New(), nil, nil, nil)
	defer h.Close()
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/api/scanner/run", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	issues, _ := st.ListIssues()
	for _, issue := range issues {
		if issue.Title == "out of scope stale" || issue.Title == "not reproduced" {
			t.Fatalf("full scan retained stale issue after scope change: %+v", issues)
		}
	}
	findings, err := st.ListFindings("", "", "")
	if err != nil || len(findings) != 1 || findings[0].Title != "curated" {
		t.Fatalf("curated findings changed: %+v, %v", findings, err)
	}
}

func TestScannerTargetedRescanPreservesUnrelatedIssues(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.CreateScopeRule(&store.ScopeRule{Enabled: true, Action: "include", Host: "*.example.com"}); err != nil {
		t.Fatal(err)
	}
	targetID, _ := st.InsertFlow(&store.Flow{TS: time.Now(), Method: "GET", Scheme: "https", Host: "api.example.com", Path: "/target", Status: 200})
	otherID, _ := st.InsertFlow(&store.Flow{TS: time.Now(), Method: "GET", Scheme: "https", Host: "other.example.com", Path: "/other", Status: 200})
	if err := st.SaveIssues([]store.Issue{
		{FlowID: targetID, Severity: "High", Title: "target stale", Target: "GET api.example.com/target"},
		{FlowID: otherID, Severity: "Low", Title: "unrelated", Target: "GET other.example.com/other"},
	}); err != nil {
		t.Fatal(err)
	}
	h := New(st, intercept.New(), nil, nil, nil)
	defer h.Close()
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/scanner/run?host=api.example.com&search=%2Ftarget", strings.NewReader(""))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	issues, _ := st.ListIssues()
	seenUnrelated := false
	for _, issue := range issues {
		if issue.Title == "target stale" {
			t.Fatalf("targeted scan retained stale issue: %+v", issues)
		}
		if issue.Title == "unrelated" {
			seenUnrelated = true
		}
	}
	if !seenUnrelated {
		t.Fatalf("targeted scan removed unrelated target: %+v", issues)
	}
}

func TestScannerFullScanCapPreservesOlderInScopeIssues(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.CreateScopeRule(&store.ScopeRule{Enabled: true, Action: "include", Host: "*.example.com"}); err != nil {
		t.Fatal(err)
	}
	oldID, _ := st.InsertFlow(&store.Flow{TS: time.UnixMilli(1), Method: "GET", Scheme: "https", Host: "old.example.com", Path: "/old", Status: 200})
	outID, _ := st.InsertFlow(&store.Flow{TS: time.UnixMilli(2), Method: "GET", Scheme: "https", Host: "outside.test", Path: "/gone", Status: 200})
	newID, _ := st.InsertFlow(&store.Flow{TS: time.UnixMilli(3), Method: "GET", Scheme: "https", Host: "new.example.com", Path: "/new", Status: 200})
	if err := st.SaveIssues([]store.Issue{
		{FlowID: oldID, Severity: "Low", Title: "older in-scope", Target: "GET old.example.com/old"},
		{FlowID: outID, Severity: "Low", Title: "now out-of-scope", Target: "GET outside.test/gone"},
		{FlowID: newID, Severity: "Low", Title: "new stale", Target: "GET new.example.com/new"},
	}); err != nil {
		t.Fatal(err)
	}
	h := New(st, intercept.New(), nil, nil, nil)
	defer h.Close()
	rr := httptest.NewRecorder()
	(&scannerAPI{h}).scannerRunWithLimit(rr, httptest.NewRequest(http.MethodPost, "/api/scanner/run", nil), 1)

	issues, _ := st.ListIssues()
	seen := map[string]bool{}
	for _, issue := range issues {
		seen[issue.Title] = true
	}
	if !seen["older in-scope"] || seen["now out-of-scope"] || seen["new stale"] {
		t.Fatalf("cap-aware reconciliation is wrong: %+v", issues)
	}
}
