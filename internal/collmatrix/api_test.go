package collmatrix

import (
	"bytes"
	"context"
	"encoding/json"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/collexec"
	"github.com/Veyal/interseptor/internal/collrun"
	"github.com/Veyal/interseptor/internal/store"
	"github.com/Veyal/interseptor/internal/varstore"
)

type harness struct {
	t    *testing.T
	be   *fakeBackend
	runs *fakeRuns
	sink *fakeSink
	svc  *Service
	srv  *httptest.Server
}

func newHarness(t *testing.T, human bool) *harness {
	t.Helper()
	be := newFake("list", "admin")
	be.respond = func(in collexec.StepInput) *collexec.StepResult {
		switch in.Identity {
		case "admin":
			return stepResp(200, 500, 9, 11)
		case "user":
			return stepResp(200, 500, 8, 12)
		}
		return stepResp(401, 20, 3, 13)
	}
	runs := &fakeRuns{runs: map[string][]store.CollRun{}, rows: map[string][]store.CollRunResult{}}
	sink := &fakeSink{}
	svc := New(Deps{
		Backend: be, Runs: runs, Evidence: sink,
		Identities: staticIdentities{
			{Name: "admin", Headers: []Header{{"Authorization", "Bearer a"}}},
			{Name: "user", Headers: []Header{{"Authorization", "Bearer u"}}, Expect: ExpectDeny},
		},
		Bodies: fakeBodies{},
	})
	mux := http.NewServeMux()
	svc.Mount(mux, Callers{IsAI: func(*http.Request) bool { return !human }})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return &harness{t: t, be: be, runs: runs, sink: sink, svc: svc, srv: srv}
}

func (h *harness) do(method, path, body string) (*http.Response, []byte) {
	h.t.Helper()
	req, _ := http.NewRequest(method, h.srv.URL+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer resp.Body.Close()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(resp.Body)
	return resp, buf.Bytes()
}

func TestRoutesRunGetRenderAttach(t *testing.T) {
	h := newHarness(t, true)
	resp, body := h.do("POST", "/api/collmatrix/run", `{"collectionUid":"c1"}`)
	if resp.StatusCode != 200 {
		t.Fatalf("run = %d %s", resp.StatusCode, body)
	}
	var out struct {
		Matrix Matrix `json:"matrix"`
	}
	if err := json.Unmarshal(body, &out); err != nil || out.Matrix.ID == "" || out.Matrix.Summary.Violations != 2 {
		t.Fatalf("out = %+v err=%v body=%s", out, err, body)
	}
	id := out.Matrix.ID
	if resp, _ := h.do("GET", "/api/collmatrix/"+id, ""); resp.StatusCode != 200 {
		t.Fatalf("get = %d", resp.StatusCode)
	}
	if resp, _ := h.do("GET", "/api/collmatrix/nope", ""); resp.StatusCode != 404 {
		t.Fatalf("get missing = %d", resp.StatusCode)
	}
	resp, pngBytes := h.do("GET", "/api/collmatrix/"+id+"/render.png?width=800", "")
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "image/png" {
		t.Fatalf("render = %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	if _, err := png.Decode(bytes.NewReader(pngBytes)); err != nil {
		t.Fatal(err)
	}
	resp, body = h.do("POST", "/api/collmatrix/"+id+"/attach", `{"findingId":3,"onlyFlagged":true}`)
	if resp.StatusCode != 200 || len(h.sink.calls) == 0 {
		t.Fatalf("attach = %d %s calls=%d", resp.StatusCode, body, len(h.sink.calls))
	}
	if resp, _ := h.do("POST", "/api/collmatrix/"+id+"/attach", `{"findingId":0}`); resp.StatusCode != 400 {
		t.Fatalf("attach zero finding = %d", resp.StatusCode)
	}
}

func TestRunRouteIgnoresCallerScopePolicy(t *testing.T) {
	h := newHarness(t, false)
	var pol []string
	h.be.respond = func(in collexec.StepInput) *collexec.StepResult { pol = append(pol, in.ScopePolicy); return nil }
	resp, body := h.do("POST", "/api/collmatrix/run", `{"collectionUid":"c1","scopePolicy":"off"}`)
	if resp.StatusCode != 200 {
		t.Fatalf("run = %d %s", resp.StatusCode, body)
	}
	for _, p := range pol {
		if p != "block" {
			t.Fatalf("policy = %q", p)
		}
	}
}

func TestRouteErrorsAndLimits(t *testing.T) {
	h := newHarness(t, true)
	for _, c := range []struct{ method, path, body string }{
		{"POST", "/api/collmatrix/run", `{`},
		{"POST", "/api/collmatrix/run", `{}`},
		{"POST", "/api/collmatrix/run", `{"collectionUid":"c1","identities":["ghost"]}`},
		{"POST", "/api/collmatrix/handoff", `{"collectionUid":"c1","itemUid":"missing"}`},
		{"POST", "/api/collmatrix/diff-example", `{"collectionUid":"c1","itemUid":"i-list","example":"x","flowId":1}`},
	} {
		if resp, _ := h.do(c.method, c.path, c.body); resp.StatusCode < 400 {
			t.Errorf("%s %s %s = %d", c.method, c.path, c.body, resp.StatusCode)
		}
	}
	big := `{"collectionUid":"` + strings.Repeat("a", maxBody+10) + `"}`
	if resp, _ := h.do("POST", "/api/collmatrix/run", big); resp.StatusCode != 400 {
		t.Errorf("oversized body = %d", resp.StatusCode)
	}
}

func TestHandoffSecretsOnlyForHumans(t *testing.T) {
	for _, human := range []bool{false, true} {
		h := newHarness(t, human)
		h.be.items[0] = orderItem()
		h.be.items[0].CollectionUID = "c1"
		h.be.layers = []varstore.Layer{{Scope: varstore.ScopeEnvironment, Vars: map[string]varstore.Var{
			"baseUrl": {Value: "https://api.example.com"}, "apiToken": {Value: "canary-handoff-1", Secret: true},
		}}}
		resp, body := h.do("POST", "/api/collmatrix/handoff", `{"collectionUid":"c1","itemUid":"i1","positions":["orderId"],"includeSecrets":true}`)
		if resp.StatusCode != 200 {
			t.Fatalf("human=%v handoff = %d %s", human, resp.StatusCode, body)
		}
		if got := strings.Contains(string(body), "canary-handoff-1"); got != human {
			t.Fatalf("human=%v secret in body = %v: %s", human, got, body)
		}
	}
}

func TestToolsNeverLeakAndAreAIScoped(t *testing.T) {
	h := newHarness(t, true)
	tools := h.svc.Tools()
	names := map[string]bool{}
	for _, tl := range tools {
		names[tl.Name] = true
		if tl.Description == "" || tl.InputSchema["type"] != "object" || tl.Call == nil {
			t.Fatalf("bad tool %+v", tl)
		}
	}
	for _, want := range []string{"collection_identity_matrix", "collection_openapi_coverage", "collection_intruder_handoff", "collection_example_diff", "collection_run_timing", "collection_attach_run_evidence"} {
		if !names[want] {
			t.Fatalf("missing tool %s", want)
		}
	}
	var matrix Tool
	for _, tl := range tools {
		if tl.Name == "collection_identity_matrix" {
			matrix = tl
		}
	}
	var pol []string
	var ai []bool
	h.be.respond = func(in collexec.StepInput) *collexec.StepResult {
		pol = append(pol, in.ScopePolicy)
		ai = append(ai, in.AI)
		return nil
	}
	if _, err := matrix.Call(context.Background(), json.RawMessage(`{"collectionUid":"c1","scopePolicy":"off"}`)); err != nil {
		t.Fatal(err)
	}
	if len(pol) == 0 {
		t.Fatal("no steps ran")
	}
	for i := range pol {
		if pol[i] != "block" || !ai[i] {
			t.Fatalf("step %d policy=%q ai=%v", i, pol[i], ai[i])
		}
	}
	if _, err := matrix.Call(context.Background(), json.RawMessage(`not json`)); err == nil {
		t.Fatal("bad arguments must error")
	}
}

func TestStoredRunTimingAndEvidenceRoutes(t *testing.T) {
	h := newHarness(t, true)
	h.runs.runs["c1"] = []store.CollRun{{UID: "r9", CollectionUID: "c1", StartedTS: 100, FinishedTS: 900, Status: collrun.StatusDone, SummaryJSON: `{"iterations":1}`}}
	h.runs.rows["r9"] = []store.CollRunResult{
		row("r9", "i-list", collrun.ItemResult{Seq: 1, ItemUID: "i-list", Name: "list", Outcome: collexec.OutcomeSent, DurationMs: 120, FlowID: 31, HTTPStatus: 200}),
		row("r9", "i-admin", collrun.ItemResult{Seq: 2, ItemUID: "i-admin", Name: "admin", Outcome: collexec.OutcomeSent, DurationMs: 80, FlowID: 32, HTTPStatus: 500}),
	}
	resp, body := h.do("GET", "/api/collmatrix/timing?collection=c1&run=r9", "")
	var tr TimingReport
	if resp.StatusCode != 200 || json.Unmarshal(body, &tr) != nil || tr.WallMs != 800 || tr.RequestMs != 200 || len(tr.Items) != 2 {
		t.Fatalf("timing = %d %s", resp.StatusCode, body)
	}
	if resp, _ := h.do("GET", "/api/collmatrix/timing?collection=c1&run=nope", ""); resp.StatusCode != 404 {
		t.Fatalf("missing run = %d", resp.StatusCode)
	}
	resp, body = h.do("POST", "/api/collmatrix/attach-run", `{"collectionUid":"c1","runUid":"r9","itemUids":["i-admin"],"findingId":5}`)
	if resp.StatusCode != 200 || len(h.sink.calls) != 1 || h.sink.calls[0].flow != 32 || h.sink.calls[0].finding != 5 {
		t.Fatalf("attach-run = %d %s %+v", resp.StatusCode, body, h.sink.calls)
	}
}

func TestCoverageRoute(t *testing.T) {
	h := newHarness(t, true)
	h.be.items = []store.Item{specItem("a", "List", "GET", "/users", "")}
	resp, body := h.do("GET", "/api/collmatrix/coverage?collection=c1", "")
	var cr CoverageReport
	if resp.StatusCode != 200 || json.Unmarshal(body, &cr) != nil || cr.Total != 1 || cr.Exercised != 0 {
		t.Fatalf("coverage = %d %s", resp.StatusCode, body)
	}
}

func TestEveryRouteIsDocumented(t *testing.T) {
	h := newHarness(t, true)
	seen := map[string]bool{}
	for _, r := range h.svc.Routes(Callers{}) {
		k := r.Method + " " + r.Path
		if seen[k] || r.Desc == "" || r.Handler == nil || !strings.HasPrefix(r.Path, "/api/collmatrix/") {
			t.Fatalf("bad route %+v", r)
		}
		seen[k] = true
	}
}
