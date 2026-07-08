package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/store"
)

// insertScannableFlow inserts a flow that targets upstream (an httptest server)
// with a query parameter, so activescan.Points finds an injectable point and
// resolveActiveTestFlow can pick it up.
func insertScannableFlow(t *testing.T, s *store.Store, upstream *httptest.Server, path string) int64 {
	t.Helper()
	u, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatalf("parse upstream url: %v", err)
	}
	host := u.Hostname()
	port, _ := strconv.Atoi(u.Port())
	id, err := s.InsertFlow(&store.Flow{
		TS: time.Now(), Method: "GET", Scheme: "http", Host: host, Port: port, Path: path, Status: 200,
	})
	if err != nil {
		t.Fatalf("InsertFlow: %v", err)
	}
	return id
}

// TestActiveChecksListEmpty covers the "no dir configured" default: listing
// must return an empty (not nil/error) checks array.
func TestActiveChecksListEmpty(t *testing.T) {
	h, _, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/active-checks")
	if err != nil {
		t.Fatalf("GET active-checks: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var out struct {
		Checks []map[string]any `json:"checks"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	if out.Checks == nil || len(out.Checks) != 0 {
		t.Fatalf("expected empty checks array, got %v", out.Checks)
	}
}

// TestActiveChecksSaveGetListDeleteRoundTrip mirrors the passive /api/checks
// round trip: save a custom active check, read it back, see it listed, then
// delete it and confirm it's gone.
func TestActiveChecksSaveGetListDeleteRoundTrip(t *testing.T) {
	h, _, _ := newHub(t)
	h.ActiveChecksDir = t.TempDir()
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	const src = `def check(point, baseline, probe):
    r = probe("'")
    if re_search("(?i)SQL syntax", r.body):
        return [finding("High", "SQLi (custom)")]
    return []
`
	body, _ := json.Marshal(map[string]string{"source": src})
	saveReq, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/active-checks/my-sqli", strings.NewReader(string(body)))
	saveResp, err := http.DefaultClient.Do(saveReq)
	if err != nil {
		t.Fatalf("PUT active-checks: %v", err)
	}
	defer saveResp.Body.Close()
	if saveResp.StatusCode != http.StatusOK {
		t.Fatalf("save status %d", saveResp.StatusCode)
	}

	getResp, err := http.Get(ts.URL + "/api/active-checks/my-sqli")
	if err != nil {
		t.Fatalf("GET active-check: %v", err)
	}
	defer getResp.Body.Close()
	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("get status %d", getResp.StatusCode)
	}
	var got struct {
		Source     string `json:"source"`
		Builtin    bool   `json:"builtin"`
		Overridden bool   `json:"overridden"`
	}
	json.NewDecoder(getResp.Body).Decode(&got)
	if got.Source != src {
		t.Fatalf("source round-trip mismatch: got %q", got.Source)
	}
	if got.Builtin || got.Overridden {
		t.Fatalf("a novel id should not be builtin/overridden: %+v", got)
	}

	listResp, err := http.Get(ts.URL + "/api/active-checks")
	if err != nil {
		t.Fatalf("GET list: %v", err)
	}
	defer listResp.Body.Close()
	var list struct {
		Checks []struct {
			ID string `json:"id"`
		} `json:"checks"`
	}
	json.NewDecoder(listResp.Body).Decode(&list)
	found := false
	for _, c := range list.Checks {
		if c.ID == "my-sqli" {
			found = true
		}
	}
	if !found {
		t.Fatalf("saved check missing from list: %+v", list.Checks)
	}

	delReq, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/active-checks/my-sqli", nil)
	delResp, err := http.DefaultClient.Do(delReq)
	if err != nil {
		t.Fatalf("DELETE active-check: %v", err)
	}
	defer delResp.Body.Close()
	if delResp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status %d", delResp.StatusCode)
	}

	getAfter, err := http.Get(ts.URL + "/api/active-checks/my-sqli")
	if err != nil {
		t.Fatalf("GET after delete: %v", err)
	}
	defer getAfter.Body.Close()
	if getAfter.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 after delete, got %d", getAfter.StatusCode)
	}
}

// TestActiveChecksSaveRejectsBadID confirms save validates the id before
// touching the filesystem (mirrors checkscript's slug rule).
func TestActiveChecksSaveRejectsBadID(t *testing.T) {
	h, _, _ := newHub(t)
	h.ActiveChecksDir = t.TempDir()
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	body, _ := json.Marshal(map[string]string{"source": "def check(point, baseline, probe):\n    return []\n"})
	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/active-checks/..%2Fevil", strings.NewReader(string(body)))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest && resp.StatusCode != http.StatusNotFound {
		t.Fatalf("path-traversal id: expected 400/404, got %d", resp.StatusCode)
	}
}

// TestActiveChecksTestEndpointFindsVuln runs testActiveCheck against a real
// flow through a real (loopback) upstream: the check's probe() must actually
// traverse the active sender and the handler must report a finding when the
// upstream signals a vulnerability.
func TestActiveChecksTestEndpointFindsVuln(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Target.With URL-encodes the payload before it ever hits the wire (a
		// literal "'" becomes "%27" in RawQuery, as it should for a well-formed
		// request), so check the decoded value, not the still-encoded RawQuery.
		if strings.Contains(r.URL.Query().Get("q"), "'") {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("You have an error in your SQL syntax near 'x'"))
			return
		}
		w.Write([]byte("ok"))
	}))
	defer upstream.Close()

	h, s, _ := newHub(t)
	flowID := insertScannableFlow(t, s, upstream, "/search?q=1")

	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	const src = `def check(point, baseline, probe):
    r = probe("'")
    if re_search("(?i)SQL syntax", r.body):
        return [finding("High", "SQL injection (custom)", evidence=r.body[:40])]
    return []
`
	body, _ := json.Marshal(map[string]any{"source": src, "flowId": flowID})
	resp, err := http.Post(ts.URL+"/api/active-checks/test", "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatalf("POST test: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var out struct {
		Finding *struct {
			Severity string `json:"severity"`
			Title    string `json:"title"`
		} `json:"finding"`
		Note  string `json:"note"`
		Error string `json:"error"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	if out.Finding == nil {
		t.Fatalf("expected a finding, got note=%q error=%q", out.Note, out.Error)
	}
	if out.Finding.Severity != "High" || out.Finding.Title != "SQL injection (custom)" {
		t.Fatalf("unexpected finding: %+v", out.Finding)
	}
}

// TestActiveChecksTestEndpointNoFinding confirms a clean upstream produces the
// "no finding" note rather than a false positive or an error.
func TestActiveChecksTestEndpointNoFinding(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	}))
	defer upstream.Close()

	h, s, _ := newHub(t)
	flowID := insertScannableFlow(t, s, upstream, "/search?q=1")

	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	const src = `def check(point, baseline, probe):
    r = probe("'")
    if re_search("(?i)SQL syntax", r.body):
        return [finding("High", "SQLi")]
    return []
`
	body, _ := json.Marshal(map[string]any{"source": src, "flowId": flowID})
	resp, err := http.Post(ts.URL+"/api/active-checks/test", "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatalf("POST test: %v", err)
	}
	defer resp.Body.Close()
	var out struct {
		Finding any    `json:"finding"`
		Note    string `json:"note"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	if out.Finding != nil {
		t.Fatalf("expected no finding, got %+v", out.Finding)
	}
	if !strings.Contains(out.Note, "no finding") {
		t.Fatalf("expected a 'no finding' note, got %q", out.Note)
	}
}

// TestActiveChecksTestEndpointNoInjectableFlow confirms the "nothing to test
// against" branch when no flow with an injection point exists yet.
func TestActiveChecksTestEndpointNoInjectableFlow(t *testing.T) {
	h, _, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	const src = `def check(point, baseline, probe):
    return []
`
	body, _ := json.Marshal(map[string]any{"source": src})
	resp, err := http.Post(ts.URL+"/api/active-checks/test", "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatalf("POST test: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var out struct {
		Note string `json:"note"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	if out.Note == "" {
		t.Fatalf("expected a note explaining there's nothing to test against")
	}
}

// TestActiveChecksTestEndpointBadJSON confirms malformed request bodies are
// rejected with 400, not a panic or 500.
func TestActiveChecksTestEndpointBadJSON(t *testing.T) {
	h, _, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/api/active-checks/test", "application/json", strings.NewReader("{not json"))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", resp.StatusCode)
	}
}

// TestActiveChecksTestEndpointCompileError confirms a check that fails to
// compile is reported as a 200 + {"error": ...} — never a 500 — so the UI can
// show it inline while iterating (matches testCheck's contract).
func TestActiveChecksTestEndpointCompileError(t *testing.T) {
	h, _, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	body, _ := json.Marshal(map[string]any{"source": "this is not starlark("})
	resp, err := http.Post(ts.URL+"/api/active-checks/test", "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200 (compile errors are reported inline)", resp.StatusCode)
	}
	var out struct {
		Error string `json:"error"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	if out.Error == "" {
		t.Fatalf("expected a compile error message")
	}
}

// TestActiveChecksTestEndpointRunawayCompileFailsFast pins the fix for the
// previously-fixed Critical DoS: /api/active-checks/test is the reachable HTTP
// surface for activescript.Compile, which used to let a runaway module-level
// Starlark comprehension hang (or exhaust memory on) the control goroutine
// because compilation ran without a step bound. It must now fail fast with a
// clear error instead of hanging. The comprehension range is kept small enough
// that a regression would still be caught (it never finishes on the maxSteps
// budget) without slowing down the suite when the fix holds.
func TestActiveChecksTestEndpointRunawayCompileFailsFast(t *testing.T) {
	h, _, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	const runaway = `x = [i * i for i in range(1000000000)]
def check(point, baseline, probe):
    return []
`
	body, _ := json.Marshal(map[string]any{"source": runaway})

	done := make(chan *http.Response, 1)
	errCh := make(chan error, 1)
	go func() {
		resp, err := http.Post(ts.URL+"/api/active-checks/test", "application/json", strings.NewReader(string(body)))
		if err != nil {
			errCh <- err
			return
		}
		done <- resp
	}()

	select {
	case err := <-errCh:
		t.Fatalf("POST: %v", err)
	case resp := <-done:
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status %d, want 200 (runaway compile reported as {error}, not 500)", resp.StatusCode)
		}
		var out struct {
			Error string `json:"error"`
		}
		json.NewDecoder(resp.Body).Decode(&out)
		if out.Error == "" {
			t.Fatal("expected a compile error for the runaway module-level comprehension")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("POST /api/active-checks/test did not return within 5s — the Starlark compile step bound regressed (DoS)")
	}
}
