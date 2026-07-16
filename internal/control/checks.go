package control

import (
	"encoding/json"
	"io"
	"net/http"
	"os"

	"github.com/Veyal/interseptor/internal/activescan"
	"github.com/Veyal/interseptor/internal/activescript"
	"github.com/Veyal/interseptor/internal/checkscript"
	"github.com/Veyal/interseptor/internal/scanner"
	"github.com/Veyal/interseptor/internal/store"
)

// maxCheckSource bounds a user/AI-supplied Starlark check source before it is
// decoded and handed to the parser — a multi-hundred-MB body would otherwise be
// lexed in full (memory/CPU exhaustion on the control goroutine). 512 KiB is far
// larger than any real check.
const maxCheckSource = 512 << 10

// Custom-check management: list / read / save / delete user Starlark checks in
// ChecksDir, plus a test endpoint that compiles + runs a check against a flow
// without saving — so a human (or the AI) can iterate before committing it.

func (h *checksAPI) listChecks(w http.ResponseWriter, r *http.Request) {
	checks := []checkscript.Source{}
	if h.ChecksDir != "" {
		if got := checkscript.List(h.ChecksDir); got != nil {
			checks = got
		}
	}
	builtin := make([]map[string]any, 0, len(scanner.BuiltinChecks))
	for _, b := range scanner.BuiltinChecks {
		m := map[string]any{
			"id": b.ID, "title": b.Title, "category": b.Category,
			"severity": b.Severity, "description": b.Description,
			"editable": true,
		}
		if h.ChecksDir != "" && checkscript.Exists(h.ChecksDir, b.ID) {
			m["overridden"] = true
		}
		builtin = append(builtin, m)
	}
	active := activeCheckList()
	if h.ActiveChecksDir != "" {
		for i := range active {
			id, _ := active[i]["id"].(string)
			if activescript.Exists(h.ActiveChecksDir, id) {
				active[i]["overridden"] = true
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"checks":    checks,
		"builtin":   builtin,
		"active":    active,
		"dir":       h.ChecksDir,
		"activeDir": h.ActiveChecksDir,
		"disabled":  h.checksDisabledList(),
	})
}

// activeCheckList exposes the built-in active-scan probes for the Checks manager.
func activeCheckList() []map[string]any {
	out := make([]map[string]any, 0, len(activescan.Checks))
	for _, c := range activescan.Checks {
		out = append(out, map[string]any{
			"id": c.ID, "class": c.Class, "severity": c.Severity, "title": c.Title, "fix": c.Fix,
			"editable": true,
		})
	}
	return out
}

func (h *Hub) checksDisabledList() []string {
	raw, ok, _ := h.st.GetSetting("checks.disabled")
	if !ok || raw == "" {
		return nil
	}
	var ids []string
	_ = json.Unmarshal([]byte(raw), &ids)
	return ids
}

func (h *Hub) checksDisabledSet() map[string]bool {
	dis := map[string]bool{}
	for _, id := range h.checksDisabledList() {
		dis[id] = true
	}
	return dis
}

func (h *checksAPI) setChecksDisabled(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Disabled []string `json:"disabled"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, maxCheckSource)).Decode(&in); err != nil {
		httpErr(w, http.StatusBadRequest, "bad json")
		return
	}
	b, _ := json.Marshal(in.Disabled)
	if err := h.st.SetSetting("checks.disabled", string(b)); err != nil {
		httpInternalErr(w, err)
		return
	}
	h.broadcast(map[string]any{"type": "checks.update"})
	writeJSON(w, http.StatusOK, map[string]any{"disabled": in.Disabled})
}

func (h *checksAPI) getCheck(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if src, builtin, overridden, err := h.readCheckSource(id); err == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"id": id, "source": src, "builtin": builtin, "overridden": overridden,
		})
		return
	}
	httpErr(w, http.StatusNotFound, "check not found")
}

func (h *checksAPI) readCheckSource(id string) (src string, builtin, overridden bool, err error) {
	if h.ChecksDir != "" && checkscript.Exists(h.ChecksDir, id) {
		src, err = checkscript.Read(h.ChecksDir, id)
		return src, scanner.IsBuiltinID(id), scanner.IsBuiltinID(id), err
	}
	if tpl, ok := scanner.BuiltinTemplate(id); ok {
		return tpl, true, false, nil
	}
	return "", false, false, os.ErrNotExist
}

func (h *checksAPI) saveCheck(w http.ResponseWriter, r *http.Request) {
	if h.ChecksDir == "" {
		httpErr(w, http.StatusBadRequest, "checks directory not configured")
		return
	}
	var in struct {
		Source string `json:"source"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, maxCheckSource)).Decode(&in); err != nil {
		httpErr(w, http.StatusBadRequest, "bad json")
		return
	}
	if err := checkscript.Save(h.ChecksDir, r.PathValue("id"), in.Source); err != nil {
		httpErr(w, http.StatusBadRequest, err.Error()) // includes compile errors
		return
	}
	h.broadcast(map[string]any{"type": "checks.update"})
	writeJSON(w, http.StatusOK, map[string]any{"id": r.PathValue("id"), "saved": true})
}

func (h *checksAPI) deleteCheck(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := checkscript.Delete(h.ChecksDir, id); err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	h.broadcast(map[string]any{"type": "checks.update"})
	w.WriteHeader(http.StatusNoContent)
}

// testCheck compiles source and runs it against a flow (the given id, else the
// most recent flow), returning findings or the compile/runtime error — never 500
// for a bad check, so callers can show the error inline.
func (h *checksAPI) testCheck(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Source string `json:"source"`
		FlowID int64  `json:"flowId"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, maxCheckSource)).Decode(&in); err != nil {
		httpErr(w, http.StatusBadRequest, "bad json")
		return
	}
	c, err := checkscript.Compile("test", in.Source)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"error": err.Error()})
		return
	}
	var f *store.Flow
	if in.FlowID > 0 {
		if f, err = h.st.GetFlow(in.FlowID); err != nil {
			httpErr(w, http.StatusNotFound, "flow not found")
			return
		}
	} else if flows, _ := h.st.QueryFlowsFilter(store.FlowFilter{Limit: 1}); len(flows) > 0 {
		f = flows[0]
	}
	if f == nil {
		writeJSON(w, http.StatusOK, map[string]any{"findings": []store.Issue{}, "note": "no captured flow to test against yet"})
		return
	}
	issues, rerr := c.Run(h.flowForCheck(f))
	if rerr != nil {
		writeJSON(w, http.StatusOK, map[string]any{"error": rerr.Error()})
		return
	}
	if issues == nil {
		issues = []store.Issue{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"findings": issues, "flowId": f.ID})
}

func (h *checksAPI) flowForCheck(f *store.Flow) checkscript.Flow {
	return checkscript.Flow{
		ID: f.ID, Method: f.Method, Scheme: f.Scheme, Host: f.Host, Port: f.Port,
		Path: f.Path, Status: f.Status, Mime: f.Mime,
		ReqHeaders: f.ReqHeaders, ResHeaders: f.ResHeaders,
		ReqBody: string(h.bodyBytes(f.ReqBodyHash)), ResBody: string(h.bodyBytes(f.ResBodyHash)),
	}
}
