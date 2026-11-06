package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/store"
)

func playbookServer(t *testing.T) (*httptest.Server, *store.Store) {
	t.Helper()
	h, s, _ := newHub(t)
	h.ProjectDir = t.TempDir()
	return httptest.NewServer(h.Handler()), s
}

func TestPlaybooksEndpointListsTheCorpus(t *testing.T) {
	ts, _ := playbookServer(t)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/playbooks")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	var out struct {
		Count     int `json:"count"`
		Bundled   int `json:"bundled"`
		Untrusted int `json:"untrusted"`
		Playbooks []struct {
			ID        string `json:"id"`
			AppliesTo []string
			Trusted   bool `json:"trusted"`
		} `json:"playbooks"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Count < 5 || out.Bundled != out.Count || out.Untrusted != 0 {
		t.Fatalf("catalogue = %d bundled=%d untrusted=%d", out.Count, out.Bundled, out.Untrusted)
	}
	if out.Playbooks[0].ID == "" || len(out.Playbooks[0].AppliesTo) == 0 {
		t.Fatalf("first playbook = %+v", out.Playbooks[0])
	}
}

func TestPlaybookFiltersReportWhatApplies(t *testing.T) {
	ts, _ := playbookServer(t)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/playbooks?path=/checkout/pay&param=price&method=POST")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	var out struct {
		Matched []struct {
			ID string `json:"id"`
		} `json:"matched"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if len(out.Matched) == 0 {
		t.Fatal("the checkout signals should match at least one playbook")
	}
}

func TestPlaybookGetRendersMarkdown(t *testing.T) {
	ts, _ := playbookServer(t)
	defer ts.Close()
	resp, err := http.Get(ts.URL + "/api/playbooks/object-authz")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var out struct {
		Markdown string `json:"markdown"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if !strings.Contains(out.Markdown, "# Object-level authorization") {
		t.Fatalf("markdown = %q", out.Markdown[:min(len(out.Markdown), 120)])
	}
	resp2, _ := http.Get(ts.URL + "/api/playbooks/nope-nope")
	if resp2.StatusCode != http.StatusNotFound {
		t.Fatalf("missing playbook status = %d", resp2.StatusCode)
	}
	resp2.Body.Close()
}

func TestPlaybookMatchOnACapturedFlow(t *testing.T) {
	ts, s := playbookServer(t)
	defer ts.Close()

	if _, err := s.InsertFlow(&store.Flow{
		TS: time.Now(), Method: "POST", Scheme: "https", Host: "api.example", Port: 443,
		Path: "/checkout/pay?price=1", Status: 200, Mime: "application/json",
		ReqHeaders: map[string][]string{"Content-Type": {"application/json"}},
	}); err != nil {
		t.Fatalf("InsertFlow: %v", err)
	}
	flows, err := s.QueryFlows(10)
	if err != nil || len(flows) == 0 {
		t.Fatalf("flows: %v", err)
	}
	resp, err := http.Get(ts.URL + "/api/playbooks/match?flowId=" + itoaTest(flows[0].ID))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	var out struct {
		Count     int `json:"count"`
		Playbooks []struct {
			ID        string `json:"id"`
			MatchedOn string `json:"matchedOn"`
		} `json:"playbooks"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if out.Count == 0 {
		t.Fatalf("no playbook matched a checkout flow: %+v", out)
	}
	if out.Playbooks[0].MatchedOn == "" {
		t.Fatal("a match must name the signal that matched")
	}
}

// A playbook the project adds is untrusted, and the API says so.
func TestProjectPlaybookIsReportedUntrusted(t *testing.T) {
	h, _, _ := newHub(t)
	h.ProjectDir = t.TempDir()
	writeProjectPlaybook(t, h.ProjectDir, "house.md", "---\nid: house\ntitle: House rule\napplies_to:\n  - path:/admin\n---\n\nCheck the admin panel.\n")
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/playbooks")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	var out struct {
		Untrusted int `json:"untrusted"`
		Project   int `json:"project"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if out.Project != 1 || out.Untrusted != 1 {
		t.Fatalf("project=%d untrusted=%d", out.Project, out.Untrusted)
	}
}

func TestCoverageRoundTrip(t *testing.T) {
	ts, _ := playbookServer(t)
	defer ts.Close()

	body := `{"playbookId":"object-authz","host":"api.example","endpoint":"GET /orders/{id}","status":"run","flowIds":[3]}`
	resp, err := http.Post(ts.URL+"/api/playbooks/coverage", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	resp.Body.Close()

	list, err := http.Get(ts.URL + "/api/playbooks/coverage?host=api.example")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer list.Body.Close()
	var out struct {
		Coverage []store.PlaybookCoverage `json:"coverage"`
		Summary  []store.PlaybookCoverageCounts
	}
	_ = json.NewDecoder(list.Body).Decode(&out)
	if len(out.Coverage) != 1 || out.Coverage[0].Status != "run" {
		t.Fatalf("coverage = %+v", out.Coverage)
	}
	if len(out.Summary) != 1 || out.Summary[0].Run != 1 {
		t.Fatalf("summary = %+v", out.Summary)
	}
}
