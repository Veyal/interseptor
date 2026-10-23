package control

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Veyal/interseptor/internal/store"
)

// authzRunTarget200 answers 200 with the same body for every identity, so a
// lower-privileged identity looks exactly like the baseline — the "same"
// verdict the matrix must flag as suspicious.
func authzRunTarget200(t *testing.T) *store.Flow {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "every identity sees this")
	}))
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	port, _ := strconv.Atoi(u.Port())
	return &store.Flow{Method: "GET", Scheme: u.Scheme, Host: u.Hostname(), Port: port, Path: "/api/items"}
}

func runIDOf(t *testing.T, out map[string]any) int64 {
	t.Helper()
	raw, ok := out["runId"].(float64)
	if !ok || raw <= 0 {
		t.Fatalf("response carries no numeric runId: %+v", out)
	}
	return int64(raw)
}

func TestAuthzRunPersistsHistoryAndNormalizesMatrix(t *testing.T) {
	f := authzRunTarget200(t)
	h, st, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	flowID, err := st.InsertFlow(f)
	if err != nil {
		t.Fatal(err)
	}
	authzPost(t, ts.URL+"/api/authz",
		`{"identities":[{"name":"admin","headers":"Authorization: Bearer admin"},{"name":"user","headers":"Authorization: Bearer user"}]}`)

	sink, _ := authzEventSink(t, h)
	code, out := authzReq(t, http.MethodPost, ts.URL+"/api/authz/run", `{"flowId":`+strconv.FormatInt(flowID, 10)+`}`, nil)
	if code != http.StatusOK {
		t.Fatalf("run status = %d: %+v", code, out)
	}
	runID := runIDOf(t, out)
	if _, ok := out["runs"]; !ok {
		t.Fatalf("run response lost its runs field: %+v", out)
	}
	msg := waitForEvent(t, sink, `"what":"runs"`)
	if !strings.Contains(msg, `"runId":`+strconv.FormatInt(runID, 10)) {
		t.Fatalf("run event = %s", msg)
	}

	_, list := authzReq(t, http.MethodGet, ts.URL+"/api/authz/runs?limit=10", "", nil)
	runs, _ := list["runs"].([]any)
	if len(runs) != 1 {
		t.Fatalf("runs listing = %+v", runs)
	}
	row, _ := runs[0].(map[string]any)
	if int64(row["id"].(float64)) != runID || row["kind"] != authzRunKindSingle || row["actor"] != "you" {
		t.Fatalf("run row = %+v", row)
	}
	if row["ts"] == "" || int(row["endpoints"].(float64)) != 1 || int(row["suspicious"].(float64)) != 1 {
		t.Fatalf("run row summary = %+v", row)
	}
	target, _ := row["target"].(map[string]any)
	if target == nil || int64(target["flowId"].(float64)) != flowID || target["path"] != "/api/items" {
		t.Fatalf("run target = %+v", target)
	}
	names, _ := row["identities"].([]any)
	if len(names) != 2 || names[0] != "admin" {
		t.Fatalf("run identities = %+v", names)
	}

	code, detail := authzReq(t, http.MethodGet, ts.URL+"/api/authz/runs/"+strconv.FormatInt(runID, 10), "", nil)
	if code != http.StatusOK {
		t.Fatalf("run detail = %d: %+v", code, detail)
	}
	matrix, _ := detail["matrix"].(map[string]any)
	rows, _ := matrix["rows"].([]any)
	if len(rows) != 1 {
		t.Fatalf("matrix rows = %+v", rows)
	}
	mrow, _ := rows[0].(map[string]any)
	baseline, _ := mrow["baseline"].(map[string]any)
	if int(baseline["status"].(float64)) != 200 {
		t.Fatalf("baseline = %+v", baseline)
	}
	cells, _ := mrow["cells"].([]any)
	if len(cells) != 2 {
		t.Fatalf("cells = %+v", cells)
	}
	first, _ := cells[0].(map[string]any)
	second, _ := cells[1].(map[string]any)
	if first["identity"] != "admin" || first["verdict"] != authzVerdictAllowed {
		t.Fatalf("baseline cell = %+v", first)
	}
	if second["identity"] != "user" || second["verdict"] != authzVerdictSame {
		t.Fatalf("lower-priv cell = %+v", second)
	}
	if _, ok := detail["raw"]; !ok {
		t.Fatalf("run detail lost raw: %+v", detail)
	}

	// The run id also addresses the matrix renderer.
	resp, err := http.Get(ts.URL + "/api/render/authz/" + strconv.FormatInt(runID, 10) + ".png")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("render by persisted run id = %d", resp.StatusCode)
	}

	if code, _ := authzReq(t, http.MethodDelete, ts.URL+"/api/authz/runs/"+strconv.FormatInt(runID, 10), "", nil); code != http.StatusNoContent {
		t.Fatalf("delete run = %d, want 204", code)
	}
	if code, _ := authzReq(t, http.MethodGet, ts.URL+"/api/authz/runs/"+strconv.FormatInt(runID, 10), "", nil); code != http.StatusNotFound {
		t.Fatalf("deleted run detail = %d, want 404", code)
	}
	if code, _ := authzReq(t, http.MethodDelete, ts.URL+"/api/authz/runs/"+strconv.FormatInt(runID, 10), "", nil); code != http.StatusNotFound {
		t.Fatalf("double delete = %d, want 404", code)
	}
}

func TestAuthzBulkDifferentialAndCrossHostRunsPersist(t *testing.T) {
	h, st, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	var state atomic.Int64
	_, diffFlow := differentialTarget(t, &state)
	flowID, err := st.InsertFlow(diffFlow)
	if err != nil {
		t.Fatal(err)
	}
	diffFlow.ID = flowID
	authzPost(t, ts.URL+"/api/authz",
		`{"identities":[{"name":"admin","headers":"Authorization: Bearer admin"},{"name":"user","headers":"Authorization: Bearer user"}]}`)
	// Scope the differential target in so the bulk run has something to test.
	h.sc.SetRules([]store.ScopeRule{{ID: 1, Enabled: true, Action: "include", Host: diffFlow.Host}})

	kinds := map[string]string{}

	_, out := authzReq(t, http.MethodPost, ts.URL+"/api/authz/run", `{"inScope":true}`, nil)
	kinds[authzRunKindBulk] = strconv.FormatInt(runIDOf(t, out), 10)

	_, out = authzReq(t, http.MethodPost, ts.URL+"/api/authz/differential",
		`{"flowId":`+strconv.FormatInt(flowID, 10)+`,"invalidBody":"bad"}`, nil)
	if _, ok := out["contexts"]; !ok {
		t.Fatalf("differential response lost contexts: %+v", out)
	}
	kinds[authzRunKindDifferential] = strconv.FormatInt(runIDOf(t, out), 10)

	_, out = authzReq(t, http.MethodPost, ts.URL+"/api/authz/cross-host-replay",
		`{"flowId":`+strconv.FormatInt(flowID, 10)+`,"jwt":"a.b.c"}`, nil)
	if _, ok := out["results"]; !ok {
		t.Fatalf("cross-host response lost results: %+v", out)
	}
	kinds[authzRunKindCrossHost] = strconv.FormatInt(runIDOf(t, out), 10)

	for kind, id := range kinds {
		code, detail := authzReq(t, http.MethodGet, ts.URL+"/api/authz/runs/"+id, "", nil)
		if code != http.StatusOK {
			t.Fatalf("%s detail = %d", kind, code)
		}
		run, _ := detail["run"].(map[string]any)
		if run["kind"] != kind {
			t.Fatalf("%s run kind = %+v", kind, run)
		}
		matrix, _ := detail["matrix"].(map[string]any)
		rows, _ := matrix["rows"].([]any)
		if len(rows) == 0 {
			t.Fatalf("%s matrix has no rows: %+v", kind, matrix)
		}
		for _, raw := range rows {
			row, _ := raw.(map[string]any)
			cells, _ := row["cells"].([]any)
			if len(cells) == 0 {
				t.Fatalf("%s row without cells: %+v", kind, row)
			}
			for _, c := range cells {
				cell, _ := c.(map[string]any)
				switch cell["verdict"] {
				case authzVerdictAllowed, authzVerdictDenied, authzVerdictError,
					authzVerdictSame, authzVerdictDifferent, authzVerdictExpired:
				default:
					t.Fatalf("%s cell verdict = %+v", kind, cell)
				}
			}
		}
	}

	_, list := authzReq(t, http.MethodGet, ts.URL+"/api/authz/runs", "", nil)
	runs, _ := list["runs"].([]any)
	if len(runs) != 3 {
		t.Fatalf("history = %d runs, want 3", len(runs))
	}
	newest, _ := runs[0].(map[string]any)
	if newest["kind"] != authzRunKindCrossHost {
		t.Fatalf("history not newest-first: %+v", runs)
	}
}

func TestAuthzRunActorLabelsAISource(t *testing.T) {
	f := authzRunTarget200(t)
	h, st, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	flowID, err := st.InsertFlow(f)
	if err != nil {
		t.Fatal(err)
	}
	authzPost(t, ts.URL+"/api/authz", `{"identities":[{"name":"admin","headers":"Authorization: Bearer admin"}]}`)
	_, out := authzReq(t, http.MethodPost, ts.URL+"/api/authz/run", `{"flowId":`+strconv.FormatInt(flowID, 10)+`}`,
		map[string]string{"X-Interseptor-Source": "ai"})
	id := runIDOf(t, out)
	_, detail := authzReq(t, http.MethodGet, ts.URL+"/api/authz/runs/"+strconv.FormatInt(id, 10), "", nil)
	run, _ := detail["run"].(map[string]any)
	if run["actor"] != identitySourceAgent {
		t.Fatalf("AI run actor = %+v, want agent", run)
	}
}

func TestAuthzCellVerdictMapping(t *testing.T) {
	cases := []struct {
		name string
		in   cellFacts
		want string
	}{
		{"transport error", cellFacts{err: "send failed"}, authzVerdictError},
		{"no response", cellFacts{status: 0}, authzVerdictError},
		{"expired", cellFacts{status: 401, expired: true}, authzVerdictExpired},
		{"denied", cellFacts{status: 403}, authzVerdictDenied},
		{"same", cellFacts{status: 200, same: true}, authzVerdictSame},
		{"baseline", cellFacts{status: 200, baseline: true}, authzVerdictAllowed},
		{"different", cellFacts{status: 200}, authzVerdictDifferent},
		{"no access signal", cellFacts{status: 500}, authzVerdictError},
	}
	for _, tc := range cases {
		if got := authzCellVerdict(tc.in); got != tc.want {
			t.Errorf("%s: verdict = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestAuthzRunSummaryFromStoredRow(t *testing.T) {
	sum := authzRunSummaryFrom(store.AuthzRun{
		ID: 7, TS: 1_700_000_000_000, Kind: "single", Actor: "you",
		Summary: json.RawMessage(`{"kind":"single","endpoints":2,"suspicious":1}`),
	})
	if sum.ID != 7 || sum.Endpoints != 2 || sum.Suspicious != 1 || sum.Kind != "single" {
		t.Fatalf("summary = %+v", sum)
	}
	if !strings.HasPrefix(sum.TS, "2023-11-14T") {
		t.Fatalf("ts = %q", sum.TS)
	}
	if sum.Identities == nil {
		t.Fatal("identities must never be null")
	}
}
