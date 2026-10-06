package control

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/mcp"
	"github.com/Veyal/interseptor/internal/store"
)

const gateCVSS4 = "CVSS:4.0/AV:N/AC:L/AT:N/PR:N/UI:N/VC:H/VI:H/VA:H/SC:N/SI:N/SA:N"

// gateFixture seeds one report-ready Critical finding, one empty High draft,
// and one otherwise ready Low finding that still carries a CVSS 3.1 vector.
func gateFixture(t *testing.T, st *store.Store) (ready, draft, legacy int64) {
	t.Helper()
	flows := []int64{}
	for range 6 {
		id, err := st.InsertFlow(&store.Flow{Method: "GET", Scheme: "https", Host: "example.com", Path: "/", Status: 200})
		if err != nil {
			t.Fatal(err)
		}
		flows = append(flows, id)
	}
	body := func(a, b, c int64) string {
		return `[{"type":"flow","flowId":` + strconv.FormatInt(a, 10) + `,"role":"action","proof":"Recorded action"},{"type":"flow","flowId":` + strconv.FormatInt(b, 10) + `,"role":"result","proof":"Observed result"},{"type":"flow","flowId":` + strconv.FormatInt(c, 10) + `,"role":"control","proof":"Expected control"}]`
	}
	mk := func(title, sev, cvss, b string) int64 {
		id, err := st.CreateFinding(&store.Finding{Title: title, Summary: "Observed behavior", Target: "https://example.com/a", Severity: sev, Status: "verified", Impact: "Bounded impact", Why: "Expected boundary", Fix: "Correct boundary", Retest: "Confirm secure behavior", Confidence: "certain", Cvss: cvss, ProofReview: store.FindingProofReview{Execution: "demonstrated"}, Body: b})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	ready = mk("Ready finding", "Critical", gateCVSS4, body(flows[0], flows[1], flows[2]))
	legacy = mk("Legacy score finding", "Critical", "CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H", body(flows[3], flows[4], flows[5]))
	var err error
	draft, err = st.CreateFinding(&store.Finding{Title: "Empty draft", Severity: "High"})
	if err != nil {
		t.Fatal(err)
	}
	return ready, draft, legacy
}

func TestReadinessReturnsProjectWideBoardSortedBySeverity(t *testing.T) {
	h, st, _ := newHub(t)
	ready, draft, _ := gateFixture(t, st)
	if _, err := st.CreateFinding(&store.Finding{Title: "Low note", Severity: "Low"}); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	resp, err := http.Get(ts.URL + "/api/findings/readiness?statuses=all")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out struct {
		Board []struct {
			ID       int64    `json:"id"`
			Title    string   `json:"title"`
			Severity string   `json:"severity"`
			Status   string   `json:"status"`
			Ready    bool     `json:"ready"`
			Gaps     []string `json:"gaps"`
		} `json:"board"`
		Summary struct{ Ready, Blocked int } `json:"summary"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if len(out.Board) != 4 {
		t.Fatalf("board rows: %+v", out.Board)
	}
	rank := map[string]int{"Critical": 0, "High": 1, "Medium": 2, "Low": 3, "Info": 4}
	for i := 1; i < len(out.Board); i++ {
		if rank[out.Board[i-1].Severity] > rank[out.Board[i].Severity] {
			t.Fatalf("board not sorted by severity: %+v", out.Board)
		}
	}
	for _, row := range out.Board {
		switch row.ID {
		case ready:
			if !row.Ready || len(row.Gaps) != 0 {
				t.Fatalf("ready row: %+v", row)
			}
		case draft:
			if row.Ready || len(row.Gaps) == 0 || row.Status == "" || row.Title == "" {
				t.Fatalf("draft row: %+v", row)
			}
		}
	}
	if out.Summary.Ready != 1 || out.Summary.Blocked != 3 {
		t.Fatalf("summary: %+v", out.Summary)
	}
}

func TestReportGateIssuesNameRuleAndFieldAndRequireCVSS4(t *testing.T) {
	h, st, _ := newHub(t)
	_, _, legacy := gateFixture(t, st)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	resp, err := http.Get(ts.URL + "/api/finding-quality/" + strconv.FormatInt(legacy, 10))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var q struct {
		Ready  bool                                                `json:"ready"`
		Issues []struct{ Rule, Field, Capability, Message string } `json:"issues"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&q); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, is := range q.Issues {
		if is.Rule == "" || is.Field == "" || is.Message == "" {
			t.Fatalf("incomplete issue %+v", is)
		}
		found = found || (is.Rule == "cvss_version" && is.Field == "cvss")
	}
	if q.Ready || !found {
		t.Fatalf("CVSS 3.1 vector passed the final gate: %+v", q)
	}
}

// API, MCP and the per-finding endpoint must agree on the gate result.
func TestReportGateParityAcrossAPIAndMCP(t *testing.T) {
	h, st, _ := newHub(t)
	_, draft, legacy := gateFixture(t, st)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	get := func(path string) string {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return strings.TrimSpace(string(b))
	}
	srv := mcp.New(ts.URL)
	viaMCP, err := srv.Call("finding_readiness", map[string]any{"statuses": "all"})
	if err != nil {
		t.Fatal(err)
	}
	if want := get("/api/findings/readiness?statuses=all&tag="); strings.TrimSpace(viaMCP) != want {
		t.Fatalf("MCP and API readiness differ:\nmcp %s\napi %s", viaMCP, want)
	}
	for _, id := range []int64{draft, legacy} {
		path := "/api/finding-quality/" + strconv.FormatInt(id, 10)
		one, err := srv.Call("finding_readiness", map[string]any{"id": id})
		if err != nil {
			t.Fatal(err)
		}
		if strings.TrimSpace(one) != get(path) {
			t.Fatalf("MCP and API per-finding gate differ for #%d", id)
		}
		var list struct {
			Findings []struct {
				ID     int64           `json:"id"`
				Issues json.RawMessage `json:"issues"`
			} `json:"findings"`
		}
		_ = json.Unmarshal([]byte(get("/api/findings/readiness?statuses=all")), &list)
		var single struct {
			Issues json.RawMessage `json:"issues"`
		}
		_ = json.Unmarshal([]byte(one), &single)
		for _, f := range list.Findings {
			if f.ID == id && string(f.Issues) != string(single.Issues) {
				t.Fatalf("project list and per-finding issues differ for #%d: %s vs %s", id, f.Issues, single.Issues)
			}
		}
	}
}
