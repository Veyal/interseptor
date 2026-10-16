package control

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/store"
)

func getProjectReadiness(t *testing.T, h *Hub) (int, projectReadiness, string) {
	t.Helper()
	req := httptest.NewRequest("GET", "/api/project/readiness", nil)
	req.Host = "localhost"
	req.RemoteAddr = "127.0.0.1:12345"
	rec := httptest.NewRecorder()
	h.Handler().ServeHTTP(rec, req)
	var out projectReadiness
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode: %v body=%s", err, rec.Body.String())
		}
	}
	return rec.Code, out, rec.Body.String()
}

func TestProjectReadinessEmptyProjectReturnsEmptyArraysNotNull(t *testing.T) {
	h, _, _ := newHub(t)
	code, out, raw := getProjectReadiness(t, h)
	if code != http.StatusOK {
		t.Fatalf("status = %d body=%s", code, raw)
	}
	for _, bad := range []string{`"items":null`, `"blockers":null`} {
		if strings.Contains(raw, bad) {
			t.Fatalf("response contains %s: %s", bad, raw)
		}
	}
	if out.Scope.Enabled || out.Scope.In != 0 || out.Scope.Out != 0 {
		t.Fatalf("scope = %+v, want all zero", out.Scope)
	}
	if out.Brief.OK || out.Brief.Target != "" {
		t.Fatalf("brief = %+v, want empty", out.Brief)
	}
	if out.Findings.Total != 0 || out.Findings.Truncated || len(out.Findings.Items) != 0 {
		t.Fatalf("findings = %+v, want empty", out.Findings)
	}
	want := []string{blockerNoTarget, blockerNoScope}
	if strings.Join(out.Blockers, ",") != strings.Join(want, ",") {
		t.Fatalf("blockers = %v, want %v", out.Blockers, want)
	}
}

func TestProjectReadinessAggregatesScopeBriefEvidenceAndFindings(t *testing.T) {
	h, st, _ := newHub(t)
	st.CreateScopeRule(&store.ScopeRule{Action: "include", Host: "example.com", Enabled: true})
	st.CreateScopeRule(&store.ScopeRule{Action: "include", Host: "*.example.com", Enabled: true})
	st.CreateScopeRule(&store.ScopeRule{Action: "include", Host: "off.example.com", Enabled: false})
	st.CreateScopeRule(&store.ScopeRule{Action: "exclude", Host: "admin.example.com", Enabled: true})
	if _, err := st.SetEngagementBrief(store.EngagementBrief{Scope: "https://app.example.com", Authorisation: "signed"}); err != nil {
		t.Fatal(err)
	}
	fid, err := st.InsertFlow(&store.Flow{Method: "GET", Scheme: "https", Host: "example.com", Path: "/", Status: 200})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SaveWSFrame(&store.WSFrame{FlowID: fid, TS: time.Now(), Dir: "in", Opcode: 1, Length: 1}); err != nil {
		t.Fatal(err)
	}
	if err := st.FlushWSFrames(); err != nil {
		t.Fatal(err)
	}
	id, err := st.CreateFinding(&store.Finding{Title: "Draft finding"})
	if err != nil {
		t.Fatal(err)
	}

	code, out, raw := getProjectReadiness(t, h)
	if code != http.StatusOK {
		t.Fatalf("status = %d body=%s", code, raw)
	}
	if !out.Scope.Enabled || out.Scope.In != 2 || out.Scope.Out != 1 {
		t.Fatalf("scope = %+v, want enabled in=2 out=1", out.Scope)
	}
	if !out.Brief.OK || out.Brief.Target != "https://app.example.com" {
		t.Fatalf("brief = %+v", out.Brief)
	}
	if out.Evidence.Flows != 1 || out.Evidence.WS != 1 {
		t.Fatalf("evidence = %+v, want flows=1 ws=1", out.Evidence)
	}
	if out.Findings.Total != 1 || out.Findings.Ready != 0 || len(out.Findings.Items) != 1 {
		t.Fatalf("findings = %+v", out.Findings)
	}
	item := out.Findings.Items[0]
	if item.ID != id || item.Stage != "draft" || len(item.Gaps) == 0 {
		t.Fatalf("item = %+v, want draft finding with gaps", item)
	}
	// Server finding gap codes must surface as blockers, project-level ones must not.
	for _, g := range item.Gaps {
		if !containsString(out.Blockers, g) {
			t.Errorf("blockers %v missing finding gap %q", out.Blockers, g)
		}
	}
	if containsString(out.Blockers, blockerNoTarget) || containsString(out.Blockers, blockerNoScope) {
		t.Errorf("blockers %v include project-level codes though target and scope are set", out.Blockers)
	}
}

func TestProjectReadinessBlockerCodesMatchReadinessChecklist(t *testing.T) {
	h, _, _ := newHub(t)
	rep := (&authzAPI{h}).buildReadiness()
	if !containsString(rep.Blockers, blockerNoScope) {
		t.Fatalf("buildReadiness blockers = %v; project blocker %q must equal readiness.go's scope check id", rep.Blockers, blockerNoScope)
	}
}

func TestProjectReadinessCapsItemsAndTruncatesWithin1000Findings(t *testing.T) {
	h, st, _ := newHub(t)
	for i := 0; i < 1000; i++ {
		if _, err := st.CreateFinding(&store.Finding{Title: fmt.Sprintf("Finding %d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	start := time.Now()
	code, out, raw := getProjectReadiness(t, h)
	elapsed := time.Since(start)
	if code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	if out.Findings.Total != 1000 || len(out.Findings.Items) != maxProjectReadinessItems || !out.Findings.Truncated {
		t.Fatalf("total=%d items=%d truncated=%v", out.Findings.Total, len(out.Findings.Items), out.Findings.Truncated)
	}
	if elapsed > projectReadinessBudget {
		t.Fatalf("1000 findings took %v, budget %v", elapsed, projectReadinessBudget)
	}
	if len(raw) > 256<<10 {
		t.Fatalf("response is %d bytes, want bounded under 256 KiB", len(raw))
	}
}

func TestProjectReadinessSharesGuardWithOtherAPIRoutes(t *testing.T) {
	h, _, _ := newHub(t)
	status := func(path string) int {
		req := httptest.NewRequest("GET", path, nil)
		req.Host = "attacker.example.com" // rejected by the host guard
		req.RemoteAddr = "127.0.0.1:12345"
		rec := httptest.NewRecorder()
		h.Handler().ServeHTTP(rec, req)
		return rec.Code
	}
	want := status("/api/findings")
	if want == http.StatusOK {
		t.Fatalf("guard probe is ineffective: /api/findings status = %d", want)
	}
	if got := status("/api/project/readiness"); got != want {
		t.Fatalf("/api/project/readiness status = %d, want %d like /api/findings", got, want)
	}
}

func TestProjectReadinessRouteIsCataloged(t *testing.T) {
	for _, r := range apiRoutes {
		if r.Method == "GET" && r.Path == "/api/project/readiness" {
			return
		}
	}
	t.Fatal("GET /api/project/readiness missing from apiRoutes catalog")
}
