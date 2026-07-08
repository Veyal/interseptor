package control

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strconv"

	"github.com/Veyal/interseptor/internal/activescan"
	"github.com/Veyal/interseptor/internal/activescan/breaker"
	"github.com/Veyal/interseptor/internal/activescript"
	"github.com/Veyal/interseptor/internal/store"
)

// Custom ACTIVE-check management: list / read / save / delete user Starlark
// active checks in ActiveChecksDir, plus a test endpoint that compiles a check
// and runs it against one injection point of a captured flow (sending real
// probes) — the active twin of the passive /api/checks surface.

func (h *checksAPI) listActiveChecks(w http.ResponseWriter, r *http.Request) {
	checks := []activescript.Source{}
	if h.ActiveChecksDir != "" {
		if got := activescript.List(h.ActiveChecksDir); got != nil {
			checks = got
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"checks":   checks,
		"dir":      h.ActiveChecksDir,
		"disabled": h.checksDisabledList(),
	})
}

func (h *checksAPI) getActiveCheck(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if src, builtin, overridden, err := h.readActiveCheckSource(id); err == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"id": id, "source": src, "builtin": builtin, "overridden": overridden,
		})
		return
	}
	httpErr(w, http.StatusNotFound, "active check not found")
}

func (h *checksAPI) readActiveCheckSource(id string) (src string, builtin, overridden bool, err error) {
	if h.ActiveChecksDir != "" && activescript.Exists(h.ActiveChecksDir, id) {
		src, err = activescript.Read(h.ActiveChecksDir, id)
		return src, activescan.IsBuiltinID(id), activescan.IsBuiltinID(id), err
	}
	if tpl, ok := activescan.BuiltinTemplate(id); ok {
		return tpl, true, false, nil
	}
	return "", false, false, os.ErrNotExist
}

func (h *checksAPI) saveActiveCheck(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	body, _ := io.ReadAll(http.MaxBytesReader(w, r.Body, maxCheckSource))
	var in struct{ Source string }
	if err := json.Unmarshal(body, &in); err != nil {
		httpErr(w, http.StatusBadRequest, "bad json")
		return
	}
	if err := activescript.Save(h.ActiveChecksDir, id, in.Source); err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	h.broadcast(map[string]any{"type": "checks.update"})
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "saved": true})
}

func (h *checksAPI) deleteActiveCheck(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := activescript.Delete(h.ActiveChecksDir, id); err != nil {
		httpErr(w, http.StatusNotFound, err.Error())
		return
	}
	h.broadcast(map[string]any{"type": "checks.update"})
	w.WriteHeader(http.StatusNoContent)
}

// testActiveCheck compiles the supplied source and runs it against the first
// injectable point of the given flow (or the latest in-scope flow). It sends real
// probes (bounded to a handful) so the user can iterate before saving. A runtime
// error is returned as 200 + {error} (never 500) to match the passive test path.
func (h *checksAPI) testActiveCheck(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(http.MaxBytesReader(w, r.Body, maxCheckSource))
	var in struct {
		Source string `json:"source"`
		FlowID int64  `json:"flowId"`
	}
	if err := json.Unmarshal(body, &in); err != nil {
		httpErr(w, http.StatusBadRequest, "bad json")
		return
	}
	c, err := activescript.Compile("test", in.Source)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"error": err.Error()})
		return
	}

	// Resolve a target flow with at least one injectable point.
	f := h.resolveActiveTestFlow(in.FlowID)
	if f == nil {
		writeJSON(w, http.StatusOK, map[string]any{"note": "no captured flow with an injectable parameter to test against yet"})
		return
	}
	target := activescan.Target{Method: f.Method, URL: analyzeURL(f), Headers: http.Header(f.ReqHeaders), Body: string(h.bodyBytes(f.ReqBodyHash))}
	pts := activescan.Points(target)
	if len(pts) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"note": "flow #" + strconv.FormatInt(f.ID, 10) + " has no query/form/json parameter to inject into"})
		return
	}

	// Build a real, tightly-bounded probe path through the active sender.
	ctx := r.Context()
	send := h.activeSender(ctx, 0, true, breaker.New())
	baseline := send(target)
	var budget int = 6
	probe := func(payload string) activescan.Response {
		if budget <= 0 {
			return activescan.Response{}
		}
		budget--
		return send(target.With(pts[0], payload))
	}
	hit := c.Run(pts[0], baseline, probe)
	if hit == nil {
		writeJSON(w, http.StatusOK, map[string]any{"note": "no finding on flow #" + strconv.FormatInt(f.ID, 10) + " (check compiles & runs)."})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"finding": map[string]any{
			"severity": hit.Severity, "title": hit.Title, "detail": hit.Detail,
			"evidence": hit.Evidence, "fix": hit.Fix, "flowId": hit.FlowID,
		},
	})
}

// resolveActiveTestFlow returns the requested flow, else the latest in-scope flow
// that has an injectable point; nil if none.
func (h *checksAPI) resolveActiveTestFlow(id int64) *store.Flow {
	if id > 0 {
		f, err := h.st.GetFlow(id)
		if err == nil {
			return f
		}
	}
	d, err := h.st.QueryFlows(200)
	if err != nil {
		return nil
	}
	for i := len(d) - 1; i >= 0; i-- { // latest first
		f := d[i]
		if !h.sc.InScope(f) || h.isOwnListener(f) {
			continue
		}
		t := activescan.Target{Method: f.Method, URL: analyzeURL(f), Headers: http.Header(f.ReqHeaders), Body: string(h.bodyBytes(f.ReqBodyHash))}
		if len(activescan.Points(t)) > 0 {
			return f
		}
	}
	return nil
}
