package control

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/Veyal/interceptor/internal/report"
	"github.com/Veyal/interceptor/internal/store"
)

// maxFindingBodyBytes is the maximum total byte size of a finding's narrative
// body (the body JSON or the combined detail+evidence+fix text). Capped at 1 MiB
// to prevent storage DoS and UI hangs from runaway AI loops or malicious clients.
const maxFindingBodyBytes = 1 << 20 // 1 MiB

// maxFindingTextBlock is the maximum byte size of a single text block's markdown
// content within a finding body. Mirrors the reMaxText cap used elsewhere.
const maxFindingTextBlock = 256 << 10 // 256 KiB

// checkFindingBodySize validates body-content fields from an incoming write
// request. It returns a non-empty error message (suitable for httpErr) if any
// limit is exceeded. The checks are:
//   - If body (pre-serialised JSON blocks) is non-empty: its raw byte length must
//     not exceed maxFindingBodyBytes, and each text block's MD must not exceed
//     maxFindingTextBlock.
//   - Otherwise: the sum of detail + evidence + fix must not exceed maxFindingBodyBytes.
//
// Reads of pre-existing large findings are never blocked; this only guards writes.
func checkFindingBodySize(body, detail, evidence, fix string) string {
	if body != "" {
		if len(body) > maxFindingBodyBytes {
			return "finding body too large (max 1 MiB)"
		}
		// Validate individual text block sizes within the body JSON.
		var blocks []struct {
			Type string `json:"type"`
			MD   string `json:"md,omitempty"`
		}
		if err := json.Unmarshal([]byte(body), &blocks); err == nil {
			for _, b := range blocks {
				if b.Type == "text" && len(b.MD) > maxFindingTextBlock {
					return "finding text block too large (max 256 KiB per block)"
				}
			}
		}
		return ""
	}
	// Legacy fields path: detail + evidence + fix combined.
	if len(detail)+len(evidence)+len(fix) > maxFindingBodyBytes {
		return "finding body too large (max 1 MiB)"
	}
	return ""
}

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
	var in struct {
		Severity string  `json:"severity"`
		Status   string  `json:"status"`
		Source   string  `json:"source"`
		Title    string  `json:"title"`
		Target   string  `json:"target"`
		Detail   string  `json:"detail"`
		Evidence string  `json:"evidence"`
		Fix      string  `json:"fix"`    // back-compat: still accepted
		Impact   string  `json:"impact"` // security impact — what an attacker gains / business consequence
		Cvss     string  `json:"cvss"`   // CVSS score or vector string
		Body     string  `json:"body"`    // JSON blocks (new format)
		FlowIDs  []int64 `json:"flowIds"` // optional: attach these PoC flows on create
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpErr(w, http.StatusBadRequest, "bad json")
		return
	}
	if in.Title == "" {
		httpErr(w, http.StatusBadRequest, "title required")
		return
	}
	if msg := checkFindingBodySize(in.Body, in.Detail, in.Evidence, in.Fix); msg != "" {
		httpErr(w, http.StatusRequestEntityTooLarge, msg)
		return
	}
	f := &store.Finding{
		Severity: in.Severity, Status: in.Status, Source: orVal(in.Source, "human"),
		Title: in.Title, Target: in.Target, Detail: in.Detail, Evidence: in.Evidence, Fix: in.Fix,
		Impact: in.Impact, Cvss: in.Cvss, Body: in.Body,
	}
	id, err := h.st.CreateFinding(f)
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	for _, fid := range in.FlowIDs {
		_ = h.st.AttachFlow(id, fid, "", -1)
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
		Fix      *string `json:"fix"`    // back-compat: still accepted
		Impact   *string `json:"impact"` // security impact — what an attacker gains / business consequence
		Cvss     *string `json:"cvss"`   // CVSS score or vector string
		Body     *string `json:"body"`   // JSON blocks
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpErr(w, http.StatusBadRequest, "bad json")
		return
	}
	// Dereference optional pointers for the size check; nil means "not being updated".
	bodyStr, detailStr, evidenceStr, fixStr := "", "", "", ""
	if in.Body != nil {
		bodyStr = *in.Body
	}
	if in.Detail != nil {
		detailStr = *in.Detail
	}
	if in.Evidence != nil {
		evidenceStr = *in.Evidence
	}
	if in.Fix != nil {
		fixStr = *in.Fix
	}
	if msg := checkFindingBodySize(bodyStr, detailStr, evidenceStr, fixStr); msg != "" {
		httpErr(w, http.StatusRequestEntityTooLarge, msg)
		return
	}
	if err := h.st.UpdateFinding(id, in.Severity, in.Status, in.Title, in.Target, in.Detail, in.Evidence, in.Fix, in.Body, in.Impact, in.Cvss); err != nil {
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
// Optional "position" (0-based block index) controls where the flow block is
// inserted in the narrative body; omit or -1 to append at the end.
func (h *Hub) attachFindingFlow(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	var in struct {
		FlowID   int64  `json:"flowId"`
		Note     string `json:"note"`
		Position *int   `json:"position"` // optional 0-based block index; omit = append
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpErr(w, http.StatusBadRequest, "bad json")
		return
	}
	if in.FlowID == 0 {
		httpErr(w, http.StatusBadRequest, "flowId required")
		return
	}
	pos := -1
	if in.Position != nil {
		pos = *in.Position
	}
	if err := h.st.AttachFlow(id, in.FlowID, in.Note, pos); err != nil {
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
