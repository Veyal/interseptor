package control

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/Veyal/interceptor/internal/report"
	"github.com/Veyal/interceptor/internal/store"
)

// Findings: a curated, persistent vulnerability store for a project (distinct from
// the ephemeral passive-scanner issues). A finding can have multiple request/response
// flows attached as PoC evidence — the human (or AI) selects them from History. The
// AI records findings here as structured memory; the human reviews/curates them.

func (h *Hub) listFindings(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	fs, err := h.st.ListFindings(q.Get("severity"), q.Get("status"))
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if fs == nil {
		fs = []store.Finding{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"findings": fs})
}

// findingsReport renders the curated findings (with PoC flows) plus the passive-scan
// issues as a downloadable Markdown engagement report. Pass ?issues=0 to omit the
// passive-scan appendix.
func (h *Hub) findingsReport(w http.ResponseWriter, r *http.Request) {
	fs, err := h.st.ListFindings("", "")
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	var issues []store.Issue
	if r.URL.Query().Get("issues") != "0" {
		if iss, err := h.st.ListIssues(); err == nil {
			issues = iss
		}
	}
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="interceptor-report.md"`)
	w.Write([]byte(report.Project(fs, issues)))
}

func (h *Hub) createFinding(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Severity string  `json:"severity"`
		Status   string  `json:"status"`
		Source   string  `json:"source"`
		Title    string  `json:"title"`
		Target   string  `json:"target"`
		Detail   string  `json:"detail"`
		Evidence string  `json:"evidence"`
		Fix      string  `json:"fix"`
		FlowIDs  []int64 `json:"flowIds"` // optional: attach these PoC flows on create
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpErr(w, http.StatusBadRequest, "bad json")
		return
	}
	if body.Title == "" {
		httpErr(w, http.StatusBadRequest, "title required")
		return
	}
	f := &store.Finding{
		Severity: body.Severity, Status: body.Status, Source: orVal(body.Source, "human"),
		Title: body.Title, Target: body.Target, Detail: body.Detail, Evidence: body.Evidence, Fix: body.Fix,
	}
	id, err := h.st.CreateFinding(f)
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	for _, fid := range body.FlowIDs {
		_ = h.st.AttachFlow(id, fid, "")
	}
	h.broadcast(map[string]any{"type": "findings.update"})
	out, _ := h.st.GetFinding(id)
	writeJSON(w, http.StatusOK, out)
}

func (h *Hub) getFinding(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	f, err := h.st.GetFinding(id)
	if err != nil {
		httpErr(w, http.StatusNotFound, "finding not found")
		return
	}
	writeJSON(w, http.StatusOK, f)
}

func (h *Hub) updateFinding(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	var in struct {
		Severity *string `json:"severity"`
		Status   *string `json:"status"`
		Title    *string `json:"title"`
		Target   *string `json:"target"`
		Detail   *string `json:"detail"`
		Evidence *string `json:"evidence"`
		Fix      *string `json:"fix"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpErr(w, http.StatusBadRequest, "bad json")
		return
	}
	if err := h.st.UpdateFinding(id, in.Severity, in.Status, in.Title, in.Target, in.Detail, in.Evidence, in.Fix); err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	h.broadcast(map[string]any{"type": "findings.update"})
	out, err := h.st.GetFinding(id)
	if err != nil {
		httpErr(w, http.StatusNotFound, "finding not found")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Hub) deleteFinding(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err := h.st.DeleteFinding(id); err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	h.broadcast(map[string]any{"type": "findings.update"})
	w.WriteHeader(http.StatusNoContent)
}

// attachFindingFlow records a flow as PoC evidence for a finding.
func (h *Hub) attachFindingFlow(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	var in struct {
		FlowID int64  `json:"flowId"`
		Note   string `json:"note"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpErr(w, http.StatusBadRequest, "bad json")
		return
	}
	if in.FlowID == 0 {
		httpErr(w, http.StatusBadRequest, "flowId required")
		return
	}
	if err := h.st.AttachFlow(id, in.FlowID, in.Note); err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	h.broadcast(map[string]any{"type": "findings.update"})
	out, _ := h.st.GetFinding(id)
	writeJSON(w, http.StatusOK, out)
}

func (h *Hub) detachFindingFlow(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	flowID, _ := strconv.ParseInt(r.PathValue("flowId"), 10, 64)
	if err := h.st.DetachFlow(id, flowID); err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	h.broadcast(map[string]any{"type": "findings.update"})
	out, _ := h.st.GetFinding(id)
	writeJSON(w, http.StatusOK, out)
}
