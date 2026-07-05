package control

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Veyal/interceptor/internal/store"
)

// POST /api/autopwn/start refuses cleanly (400) when no scope rules exist — the
// autonomous run must never probe without a scope boundary (mirrors the bulk
// active-scan gate). The error text is the engine's ErrNoScope message.
func TestAutopwnStartRefusesWithoutScope(t *testing.T) {
	h, _, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	body, _ := json.Marshal(map[string]any{"budget": map[string]any{"maxRequests": 10}})
	resp, err := http.Post(ts.URL+"/api/autopwn/start", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("no-scope start: got %d, want 400", resp.StatusCode)
	}
	var out struct {
		Error string `json:"error"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	if out.Error == "" {
		t.Fatalf("expected an error message, got empty body")
	}
}

// With scope rules defined, POST /api/autopwn/start returns a runId. No AI
// provider is configured, so the run's plan phase errors out on its own goroutine
// (asserted indirectly: the HTTP contract still returns the runId + a state), but
// the wiring must compile and the no-scope gate must be satisfied here.
func TestAutopwnStartReturnsRunID(t *testing.T) {
	h, s, _ := newHub(t)
	if _, err := s.CreateScopeRule(&store.ScopeRule{Enabled: true, Action: "include", Host: "example.com"}); err != nil {
		t.Fatalf("create scope rule: %v", err)
	}
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	body, _ := json.Marshal(map[string]any{
		"budget":     map[string]any{"maxRequests": 5, "maxWallMs": 500},
		"targetHint": "example.com login",
	})
	resp, err := http.Post(ts.URL+"/api/autopwn/start", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("start with scope: got %d, want 200", resp.StatusCode)
	}
	var out struct {
		RunID int64          `json:"runId"`
		State map[string]any `json:"state"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.RunID == 0 {
		t.Fatalf("expected a non-zero runId, got %d", out.RunID)
	}
	if out.State == nil {
		t.Fatalf("expected a state snapshot in the response")
	}
	// Stop it so the background goroutine unwinds promptly under the test store.
	h.autopwn().Stop()
}

// GET /api/autopwn/state and /runs return JSON without a run ever having started.
func TestAutopwnStateAndRunsJSON(t *testing.T) {
	h, _, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	stResp, err := http.Get(ts.URL + "/api/autopwn/state")
	if err != nil {
		t.Fatalf("get state: %v", err)
	}
	defer stResp.Body.Close()
	if stResp.StatusCode != http.StatusOK {
		t.Fatalf("state: got %d, want 200", stResp.StatusCode)
	}
	var st map[string]any
	if err := json.NewDecoder(stResp.Body).Decode(&st); err != nil {
		t.Fatalf("decode state: %v", err)
	}
	if _, ok := st["active"]; !ok {
		t.Fatalf("state JSON missing 'active' field: %v", st)
	}

	runsResp, err := http.Get(ts.URL + "/api/autopwn/runs")
	if err != nil {
		t.Fatalf("get runs: %v", err)
	}
	defer runsResp.Body.Close()
	if runsResp.StatusCode != http.StatusOK {
		t.Fatalf("runs: got %d, want 200", runsResp.StatusCode)
	}
	var runs struct {
		Runs []store.PentestRun `json:"runs"`
	}
	if err := json.NewDecoder(runsResp.Body).Decode(&runs); err != nil {
		t.Fatalf("decode runs: %v", err)
	}
	if runs.Runs == nil {
		t.Fatalf("expected a (possibly empty) runs array, got null")
	}
}

// POST /api/autopwn/stop is a no-op when nothing is running and returns the state.
func TestAutopwnStopNoRun(t *testing.T) {
	h, _, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/api/autopwn/stop", "application/json", nil)
	if err != nil {
		t.Fatalf("post stop: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("stop with no run: got %d, want 200", resp.StatusCode)
	}
}
