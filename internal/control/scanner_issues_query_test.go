package control

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/store"
)

func getIssues(t *testing.T, h *Hub, query string) (int, map[string]json.RawMessage) {
	t.Helper()
	rec := httptest.NewRecorder()
	(&scannerAPI{h}).scannerIssues(rec, httptest.NewRequest(http.MethodGet, "/api/scanner/issues"+query, nil))
	var out map[string]json.RawMessage
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode %q: %v", rec.Body.String(), err)
		}
	}
	return rec.Code, out
}

// seedRepeatedIssues stores what a short capture of one site produces: the same
// two checks firing once per URL, plus one finding on a second host.
func seedRepeatedIssues(t *testing.T, st *store.Store) {
	t.Helper()
	var issues []store.Issue
	for i := 1; i <= 66; i++ {
		issues = append(issues, store.Issue{FlowID: int64(i), Severity: "Low", Title: "Server software version disclosed", Target: fmt.Sprintf("GET app.example.com/p/%d", i)})
	}
	for i := 1; i <= 65; i++ {
		issues = append(issues, store.Issue{FlowID: int64(100 + i), Severity: "Medium", Title: "Missing security response headers", Target: fmt.Sprintf("GET app.example.com/p/%d", i)})
	}
	issues = append(issues, store.Issue{FlowID: 500, Severity: "Medium", Title: "Overly permissive CORS policy", Target: "GET accounts.example.net/o", Detail: "d", Fix: "f"})
	if err := st.SaveIssues(issues); err != nil {
		t.Fatal(err)
	}
}

func TestScannerIssuesPlainListIsUnchanged(t *testing.T) {
	h, st, _ := newHub(t)
	seedRepeatedIssues(t, st)
	code, out := getIssues(t, h, "")
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	var issues []store.Issue
	_ = json.Unmarshal(out["issues"], &issues)
	if len(issues) != 132 || len(out) != 1 {
		t.Fatalf("plain list = %d issues, keys %v; want all 132 under issues only", len(issues), keysOf(out))
	}
}

func TestScannerIssuesGroupByHostFoldsRepeats(t *testing.T) {
	h, st, _ := newHub(t)
	seedRepeatedIssues(t, st)
	code, out := getIssues(t, h, "?group=host")
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	var groups []issueGroup
	_ = json.Unmarshal(out["groups"], &groups)
	if len(groups) != 3 || string(out["total"]) != "132" || string(out["groupCount"]) != "3" {
		t.Fatalf("groups=%+v total=%s groupCount=%s", groups, out["total"], out["groupCount"])
	}
	// Most severe first, then the largest group.
	if groups[0].Title != "Missing security response headers" || groups[0].Count != 65 || groups[0].Host != "app.example.com" {
		t.Fatalf("first group = %+v", groups[0])
	}
	if len(groups[0].ExampleFlowIDs) != 3 || len(groups[0].ExampleTargets) != 3 {
		t.Fatalf("examples not bounded to 3: %+v", groups[0])
	}
	if groups[2].Title != "Server software version disclosed" || groups[2].Count != 66 {
		t.Fatalf("last group = %+v", groups[2])
	}
	if _, ok := out["nextOffset"]; ok {
		t.Fatal("nextOffset set without a limit")
	}
}

func TestScannerIssuesFiltersAndPages(t *testing.T) {
	h, st, _ := newHub(t)
	seedRepeatedIssues(t, st)

	_, out := getIssues(t, h, "?severity=medium&host=ACCOUNTS.example.net")
	var issues []store.Issue
	_ = json.Unmarshal(out["issues"], &issues)
	if len(issues) != 1 || issues[0].Title != "Overly permissive CORS policy" {
		t.Fatalf("severity+host filter = %+v", issues)
	}

	_, out = getIssues(t, h, "?title=version&limit=10&offset=60")
	issues = nil
	_ = json.Unmarshal(out["issues"], &issues)
	if len(issues) != 6 || string(out["total"]) != "66" {
		t.Fatalf("last page = %d rows, total %s; want 6 of 66", len(issues), out["total"])
	}
	if _, ok := out["nextOffset"]; ok {
		t.Fatal("last page must not carry nextOffset")
	}
	_, out = getIssues(t, h, "?group=host&limit=1")
	if string(out["nextOffset"]) != "1" {
		t.Fatalf("first grouped page nextOffset = %s, want 1", out["nextOffset"])
	}

	for _, bad := range []string{"?limit=-1", "?offset=x", "?group=title"} {
		if code, _ := getIssues(t, h, bad); code != http.StatusBadRequest {
			t.Fatalf("%s → %d, want 400", bad, code)
		}
	}
}

func TestIssueHostReadsEveryTargetShape(t *testing.T) {
	for target, want := range map[string]string{
		"GET app.example.com/p?q=1":      "app.example.com",
		"POST API.example.com":           "api.example.com",
		"https://www.example.com:8443/x": "www.example.com",
		"cdn.example.com/a.js":           "cdn.example.com",
		"":                               "",
	} {
		if got := issueHost(target); got != want {
			t.Errorf("issueHost(%q) = %q, want %q", target, got, want)
		}
	}
}

// A scan reads at most `limit` of the newest flows. It used to say nothing when
// older traffic was left out, so a partial scan read as a complete one.
func TestScannerRunReportsCappedCoverageAndSummary(t *testing.T) {
	h, st, _ := newHub(t)
	for i := 0; i < 3; i++ {
		if _, err := st.InsertFlow(&store.Flow{TS: time.UnixMilli(int64(i + 1)), Method: "GET", Scheme: "https", Host: "app.example.com", Path: fmt.Sprintf("/p/%d", i), Status: 200, Mime: "text/html",
			ResHeaders: map[string][]string{"Server": {"nginx/1.18.0"}}}); err != nil {
			t.Fatal(err)
		}
	}
	run := func(query string, limit int) map[string]json.RawMessage {
		rec := httptest.NewRecorder()
		(&scannerAPI{h}).scannerRunWithLimit(rec, httptest.NewRequest(http.MethodPost, "/api/scanner/run"+query, nil), limit)
		if rec.Code != http.StatusOK {
			t.Fatalf("run%s: %d %s", query, rec.Code, rec.Body.String())
		}
		var out map[string]json.RawMessage
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return out
	}
	out := run("", 2)
	var scan struct {
		Flows  int  `json:"flows"`
		Limit  int  `json:"limit"`
		Capped bool `json:"capped"`
	}
	_ = json.Unmarshal(out["scan"], &scan)
	if scan.Flows != 2 || scan.Limit != 2 || !scan.Capped {
		t.Fatalf("capped scan = %+v, want 2 of limit 2, capped", scan)
	}
	if _, ok := out["issues"]; !ok {
		t.Fatal("the full run response must still carry issues")
	}
	out = run("?summary=1", 3)
	_ = json.Unmarshal(out["scan"], &scan)
	if scan.Flows != 3 || scan.Capped {
		t.Fatalf("whole scan = %+v, want 3 uncapped", scan)
	}
	if _, ok := out["issues"]; ok {
		t.Fatal("summary must not carry the issue rows")
	}
	if string(out["groupCount"]) == "" || string(out["total"]) == "0" {
		t.Fatalf("summary = %v", keysOf(out))
	}
}

func keysOf(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
