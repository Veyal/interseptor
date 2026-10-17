package control

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/store"
)

func seedNoisyIssues(t *testing.T, st *store.Store) {
	t.Helper()
	var issues []store.Issue
	for i := 1; i <= 66; i++ {
		issues = append(issues, store.Issue{FlowID: int64(i), Severity: "Low", Title: "Server version disclosed",
			Target: fmt.Sprintf("http://app.example.com/p%d", i), Evidence: strings.Repeat("x", 2000)})
	}
	for i := 1; i <= 65; i++ {
		issues = append(issues, store.Issue{FlowID: int64(100 + i), Severity: "Medium", Title: "Missing security headers",
			Target: fmt.Sprintf("http://app.example.com/q%d", i)})
	}
	issues = append(issues, store.Issue{FlowID: 500, Severity: "High", Title: "Missing security headers", Target: "https://api.example.com/x"})
	if err := st.SaveIssues(issues); err != nil {
		t.Fatal(err)
	}
}

func getIssues(t *testing.T, base, query string) (int, map[string]any, int) {
	t.Helper()
	resp, err := http.Get(base + "/api/scanner/issues?" + query)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return resp.StatusCode, m, len(b)
}

func TestScannerIssuesGroupedView(t *testing.T) {
	h, st, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	seedNoisyIssues(t, st)

	_, full, fullBytes := getIssues(t, ts.URL, "")
	if n := len(full["issues"].([]any)); n != 132 {
		t.Fatalf("unchanged default view = %d issues", n)
	}
	code, g, groupedBytes := getIssues(t, ts.URL, "view=grouped")
	groups, _ := g["groups"].([]any)
	if code != 200 || len(groups) != 3 || g["totalIssues"].(float64) != 132 {
		t.Fatalf("grouped = %d %v", code, g)
	}
	first := groups[0].(map[string]any)
	if first["severity"] != "High" || first["host"] != "api.example.com" {
		t.Fatalf("most severe group first: %v", first)
	}
	second := groups[1].(map[string]any)
	if second["count"].(float64) != 65 || len(second["exampleFlowIds"].([]any)) != 3 {
		t.Fatalf("group counts/examples: %v", second)
	}
	if groupedBytes*20 > fullBytes {
		t.Fatalf("grouped view %d B is not much smaller than the full list %d B", groupedBytes, fullBytes)
	}
}

func TestScannerIssuesFilterPageAndSummary(t *testing.T) {
	h, st, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	seedNoisyIssues(t, st)

	_, l, _ := getIssues(t, ts.URL, "view=list&severity=low&limit=10&offset=60")
	issues := l["issues"].([]any)
	if l["total"].(float64) != 66 || len(issues) != 6 || l["nextOffset"] != nil {
		t.Fatalf("filtered last page = total %v len %d next %v", l["total"], len(issues), l["nextOffset"])
	}
	if ev := issues[0].(map[string]any)["evidence"].(string); len([]rune(ev)) > issueEvidenceRunes+1 {
		t.Fatalf("list evidence not bounded: %d runes", len([]rune(ev)))
	}
	_, l, _ = getIssues(t, ts.URL, "view=list&host=api.example&limit=1")
	if l["total"].(float64) != 1 {
		t.Fatalf("host filter = %v", l["total"])
	}
	_, l, _ = getIssues(t, ts.URL, "view=list&limit=1")
	if l["nextOffset"].(float64) != 1 {
		t.Fatalf("nextOffset = %v", l["nextOffset"])
	}
	_, sm, size := getIssues(t, ts.URL, "view=summary")
	by := sm["bySeverity"].(map[string]any)
	if sm["total"].(float64) != 132 || sm["groups"].(float64) != 3 || by["low"].(float64) != 66 || size > 1024 {
		t.Fatalf("summary = %v (%d B)", sm, size)
	}
	for _, q := range []string{"view=bogus", "view=list&limit=0", "view=list&offset=-1"} {
		if code, _, _ := getIssues(t, ts.URL, q); code != http.StatusBadRequest {
			t.Errorf("%s = %d", q, code)
		}
	}
}
