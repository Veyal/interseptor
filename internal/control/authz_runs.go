package control

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/Veyal/interseptor/internal/store"
)

// Authorization-run history. Every run endpoint (/api/authz/run single and
// bulk, /api/authz/differential, /api/authz/cross-host-replay) persists its
// result here and returns the new run's id, so the workspace can show what was
// tested, when, by whom and what looked wrong — long after the response is
// gone. Each run kind is also normalized into ONE matrix shape
// (identity x endpoint) so a single view renders all of them.

// Run kinds.
const (
	authzRunKindSingle       = "single"
	authzRunKindBulk         = "bulk"
	authzRunKindDifferential = "differential"
	authzRunKindCrossHost    = "crosshost"
)

// Normalized cell verdicts.
const (
	authzVerdictAllowed   = "allowed"   // 2xx/3xx for the baseline identity
	authzVerdictDenied    = "denied"    // 401/403
	authzVerdictError     = "error"     // transport failure (or no access signal at all)
	authzVerdictSame      = "same"      // same access as the baseline — suspicious for a lower-privileged identity
	authzVerdictDifferent = "different" // 2xx but a different response than the baseline
	authzVerdictExpired   = "expired"   // the session/credential looks invalid
)

type authzRunTarget struct {
	FlowID int64  `json:"flowId"`
	Method string `json:"method"`
	Host   string `json:"host"`
	Path   string `json:"path"`
}

// authzRunSummary is one row of GET /api/authz/runs.
type authzRunSummary struct {
	ID         int64           `json:"id"`
	TS         string          `json:"ts"`
	Kind       string          `json:"kind"`
	Actor      string          `json:"actor"`
	Target     *authzRunTarget `json:"target"`
	Endpoints  int             `json:"endpoints"`
	Identities []string        `json:"identities"`
	Suspicious int             `json:"suspicious"`
}

type authzMatrixBaseline struct {
	Status int   `json:"status"`
	Length int64 `json:"length"`
}

type authzMatrixCell struct {
	Identity string `json:"identity"`
	Status   int    `json:"status"`
	Length   int64  `json:"length"`
	Verdict  string `json:"verdict"`
	FlowID   int64  `json:"flowId"`
	Note     string `json:"note"`
}

type authzMatrixRow struct {
	FlowID   int64               `json:"flowId"`
	Method   string              `json:"method"`
	Host     string              `json:"host"`
	Path     string              `json:"path"`
	Baseline authzMatrixBaseline `json:"baseline"`
	Cells    []authzMatrixCell   `json:"cells"`
}

type authzMatrix struct {
	Identities []string         `json:"identities"`
	Rows       []authzMatrixRow `json:"rows"`
}

// suspicious counts the cells where a non-baseline identity got the same
// access as the baseline. authzVerdictSame is only ever assigned to
// non-baseline cells, so the count needs no second baseline lookup.
func (m authzMatrix) suspicious() int {
	n := 0
	for _, row := range m.Rows {
		for _, c := range row.Cells {
			if c.Verdict == authzVerdictSame {
				n++
			}
		}
	}
	return n
}

// cellFacts is the evidence one normalized verdict is derived from.
type cellFacts struct {
	err      string
	status   int
	expired  bool
	same     bool
	baseline bool
}

// authzCellVerdict maps one replay result onto the normalized verdict set.
// A non-2xx that is neither 401/403 nor an expiry signal (404, 500, …) carries
// no access signal at all and is reported as "error" with the status in note.
func authzCellVerdict(f cellFacts) string {
	switch {
	case f.err != "" || f.status == 0:
		return authzVerdictError
	case f.expired:
		return authzVerdictExpired
	case accessDeniedStatus(f.status):
		return authzVerdictDenied
	case f.same:
		return authzVerdictSame
	case f.status >= 200 && f.status < 400:
		if f.baseline {
			return authzVerdictAllowed
		}
		return authzVerdictDifferent
	}
	return authzVerdictError
}

func cellNote(errText string, status int) string {
	if errText != "" {
		return errText
	}
	if status != 0 && (status < 200 || status >= 400) && !accessDeniedStatus(status) {
		return "status " + strconv.Itoa(status)
	}
	return ""
}

// ---- normalization per run kind -------------------------------------------

// authzMatrixFromRuns normalizes a single or bulk /api/authz/run result.
func authzMatrixFromRuns(runs []authzRunOut) authzMatrix {
	m := authzMatrix{Identities: []string{}, Rows: []authzMatrixRow{}}
	seen := map[string]bool{}
	for _, run := range runs {
		row := authzMatrixRow{FlowID: run.FlowID, Method: run.Method, Host: run.Host, Path: run.Path, Cells: []authzMatrixCell{}}
		baseline := ""
		for _, rr := range run.Results {
			if rr.Error == "" {
				baseline = rr.Name
				row.Baseline = authzMatrixBaseline{Status: rr.Status, Length: rr.Length}
				break
			}
		}
		for _, rr := range run.Results {
			if !seen[rr.Name] {
				seen[rr.Name] = true
				m.Identities = append(m.Identities, rr.Name)
			}
			row.Cells = append(row.Cells, authzMatrixCell{
				Identity: rr.Name, Status: rr.Status, Length: rr.Length, FlowID: rr.FlowID,
				Verdict: authzCellVerdict(cellFacts{
					err: rr.Error, status: rr.Status, expired: rr.SessionInvalid,
					same: rr.Same, baseline: rr.Name == baseline,
				}),
				Note: cellNote(rr.Error, rr.Status),
			})
		}
		m.Rows = append(m.Rows, row)
	}
	return m
}

// authzMatrixFromDifferential normalizes one /api/authz/differential result.
// The differential tests ONE endpoint, so the matrix has exactly one row and
// one cell per context (the saved identities plus anonymous).
func authzMatrixFromDifferential(ev authzDifferentialEvidence) authzMatrix {
	m := authzMatrix{Identities: []string{}, Rows: []authzMatrixRow{}}
	row := authzMatrixRow{FlowID: ev.FlowID, Method: ev.Method, Host: ev.Host, Path: ev.Path, Cells: []authzMatrixCell{}}
	for i, c := range ev.Contexts {
		m.Identities = append(m.Identities, c.Name)
		if i == 0 {
			row.Baseline = authzMatrixBaseline{Status: c.Status, Length: c.Length}
		}
		row.Cells = append(row.Cells, authzMatrixCell{
			Identity: c.Name, Status: c.Status, Length: c.Length, FlowID: c.FlowID,
			Verdict: authzCellVerdict(cellFacts{
				err: c.Error, status: c.Status, same: c.SameAsBaseline && i > 0, baseline: i == 0,
			}),
			Note: c.Outcome,
		})
	}
	m.Rows = append(m.Rows, row)
	return m
}

// authzMatrixFromCrossHost normalizes one /api/authz/cross-host-replay result.
// There is one identity (the token) and one row per host the token was replayed
// to; the reference host is the baseline, so a 2xx on any OTHER host is the
// suspicious "same access" cell — that is exactly the token confusion the run
// looks for.
func authzMatrixFromCrossHost(refHost, method, path string, results []crossHostResult) authzMatrix {
	m := authzMatrix{Identities: []string{"token"}, Rows: []authzMatrixRow{}}
	var baseline authzMatrixBaseline
	for _, res := range results {
		if res.Host == refHost {
			baseline = authzMatrixBaseline{Status: res.Status, Length: res.Length}
			break
		}
	}
	for _, res := range results {
		isRef := res.Host == refHost
		m.Rows = append(m.Rows, authzMatrixRow{
			FlowID: res.FlowID, Method: method, Host: res.Host, Path: path, Baseline: baseline,
			Cells: []authzMatrixCell{{
				Identity: "token", Status: res.Status, Length: res.Length, FlowID: res.FlowID,
				Verdict: authzCellVerdict(cellFacts{
					err: res.Error, status: res.Status, same: res.Accepted && !isRef, baseline: isRef,
				}),
				Note: cellNote(res.Error, res.Status),
			}},
		})
	}
	return m
}

// ---- persistence ----------------------------------------------------------

// persistAuthzRun stores one finished run and returns its id (0 when the run
// could not be persisted — the caller still answers the client, history is a
// convenience and never a reason to fail a completed test).
func (h *authzAPI) persistAuthzRun(r *http.Request, kind string, target *authzRunTarget, m authzMatrix, raw any) int64 {
	sum := authzRunSummary{
		Kind: kind, Actor: identityActor(r, ""), Target: target,
		Endpoints: len(m.Rows), Identities: m.Identities, Suspicious: m.suspicious(),
	}
	if sum.Identities == nil {
		sum.Identities = []string{}
	}
	sumJSON, err := json.Marshal(sum)
	if err != nil {
		log.Printf("control: encode authz run summary: %v", err)
		return 0
	}
	matrixJSON, err := json.Marshal(m)
	if err != nil {
		log.Printf("control: encode authz run matrix: %v", err)
		return 0
	}
	rawJSON, err := json.Marshal(raw)
	if err != nil {
		log.Printf("control: encode authz run raw: %v", err)
		return 0
	}
	id, err := h.st.PutAuthzRun(store.AuthzRun{
		Kind: kind, Actor: sum.Actor, Summary: sumJSON, Matrix: matrixJSON, Raw: rawJSON,
	})
	if err != nil {
		log.Printf("control: persist authz run: %v", err)
		return 0
	}
	h.broadcastAuthzRun(id)
	return id
}

// flowTarget describes a run's single target endpoint.
func flowTarget(f *store.Flow) *authzRunTarget {
	if f == nil {
		return nil
	}
	return &authzRunTarget{FlowID: f.ID, Method: f.Method, Host: f.Host, Path: f.Path}
}

// writeJSONWithRunID writes v with an extra top-level "runId" field. v must
// encode to a JSON object; a zero id adds nothing.
func writeJSONWithRunID(w http.ResponseWriter, v any, runID int64) {
	if runID == 0 {
		writeJSON(w, http.StatusOK, v)
		return
	}
	b, err := json.Marshal(v)
	if err != nil {
		httpInternalErr(w, err)
		return
	}
	var doc map[string]json.RawMessage
	if json.Unmarshal(b, &doc) != nil {
		writeJSON(w, http.StatusOK, v)
		return
	}
	doc["runId"], _ = json.Marshal(runID)
	writeJSON(w, http.StatusOK, doc)
}

// ---- read/delete handlers -------------------------------------------------

// authzRunSummaryFrom rebuilds a summary from a stored row: the id and
// timestamp always come from the columns, the rest from the stored blob.
func authzRunSummaryFrom(row store.AuthzRun) authzRunSummary {
	var sum authzRunSummary
	if len(row.Summary) > 0 {
		_ = json.Unmarshal(row.Summary, &sum)
	}
	sum.ID = row.ID
	sum.TS = time.UnixMilli(row.TS).UTC().Format(time.RFC3339)
	if sum.Kind == "" {
		sum.Kind = row.Kind
	}
	if sum.Actor == "" {
		sum.Actor = row.Actor
	}
	if sum.Identities == nil {
		sum.Identities = []string{}
	}
	return sum
}

// listAuthzRuns is GET /api/authz/runs?limit=50.
func (h *authzAPI) listAuthzRuns(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if v, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && v > 0 {
		limit = v
	}
	rows, err := h.st.ListAuthzRuns(limit)
	if err != nil {
		httpInternalErr(w, err)
		return
	}
	out := make([]authzRunSummary, 0, len(rows))
	for _, row := range rows {
		out = append(out, authzRunSummaryFrom(row))
	}
	writeJSON(w, http.StatusOK, map[string]any{"runs": out})
}

// getAuthzRun is GET /api/authz/runs/{id}.
func (h *authzAPI) getAuthzRun(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		httpErr(w, http.StatusBadRequest, "bad id")
		return
	}
	row, ok, err := h.st.GetAuthzRun(id)
	if err != nil {
		httpInternalErr(w, err)
		return
	}
	if !ok {
		httpErr(w, http.StatusNotFound, "run not found")
		return
	}
	matrix := json.RawMessage(`{"identities":[],"rows":[]}`)
	if len(row.Matrix) > 0 {
		matrix = row.Matrix
	}
	raw := json.RawMessage(`{}`)
	if len(row.Raw) > 0 {
		raw = row.Raw
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"run":    authzRunSummaryFrom(*row),
		"matrix": matrix,
		"raw":    raw,
	})
}

// deleteAuthzRun is DELETE /api/authz/runs/{id}.
func (h *authzAPI) deleteAuthzRun(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		httpErr(w, http.StatusBadRequest, "bad id")
		return
	}
	ok, err := h.st.DeleteAuthzRun(id)
	if err != nil {
		httpInternalErr(w, err)
		return
	}
	if !ok {
		httpErr(w, http.StatusNotFound, "run not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
	h.broadcastAuthzRun(id)
}
