package control

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/Veyal/interseptor/internal/report"
	"github.com/Veyal/interseptor/internal/store"
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
		// Image blocks must reference a content hash — never embed base64/path
		// (use POST /api/findings/{id}/images instead).
		var blocks []struct {
			Type string `json:"type"`
			MD   string `json:"md,omitempty"`
			Data string `json:"data,omitempty"`
			Path string `json:"path,omitempty"`
			Hash string `json:"hash,omitempty"`
		}
		if err := json.Unmarshal([]byte(body), &blocks); err == nil {
			for _, b := range blocks {
				if b.Type == "text" && len(b.MD) > maxFindingTextBlock {
					return "finding text block too large (max 256 KiB per block)"
				}
				if b.Type == "image" {
					if b.Data != "" || b.Path != "" {
						return "image blocks must not include data or path — use POST /api/findings/{id}/images"
					}
					if b.Hash == "" {
						return "image blocks require a content hash"
					}
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

func (h *findingsAPI) listFindings(w http.ResponseWriter, r *http.Request) {
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

// findingsReport renders the curated findings (with PoC flows) as a downloadable
// engagement report. Passive-scan issues are omitted by default; pass ?issues=1 to
// append the passive-scan appendix. ?format=html returns a standalone HTML document.
// Full reconstructed request/response bodies for PoC flows are included by default
// (?includeBodies=0 to omit — useful for huge projects).
func (h *findingsAPI) findingsReport(w http.ResponseWriter, r *http.Request) {
	fs, err := h.st.ListFindings("", "")
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	var issues []store.Issue
	if r.URL.Query().Get("issues") == "1" {
		if iss, err := h.st.ListIssues(); err == nil {
			issues = iss
		}
	}
	includeBodies := true
	if v := r.URL.Query().Get("includeBodies"); v == "0" || strings.EqualFold(v, "false") {
		includeBodies = false
	}
	if includeBodies {
		h.enrichFindingReportBodies(fs)
	}
	format := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("format")))
	switch format {
	case "html":
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="interseptor-report.html"`)
		w.Write([]byte(report.ProjectHTML(fs, issues)))
	default:
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="interseptor-report.md"`)
		w.Write([]byte(report.Project(fs, issues)))
	}
}

// reportBodyCap bounds each reconstructed req/res in the export so huge downloads
// cannot blow up the report. Truncation is marked explicitly.
const reportBodyCap = 64 << 10 // 64 KiB

// enrichFindingReportBodies attaches reconstructed HTTP req/res to each PoC flow
// block (and legacy Flows list) for offline report handoff.
func (h *findingsAPI) enrichFindingReportBodies(fs []store.Finding) {
	for i := range fs {
		for j := range fs[i].Blocks {
			bl := &fs[i].Blocks[j]
			if bl.Type != "flow" || bl.Missing || bl.FlowID == 0 {
				continue
			}
			bl.ReqRaw, bl.ResRaw = h.flowRawForReport(bl.FlowID)
		}
		for j := range fs[i].Flows {
			fl := &fs[i].Flows[j]
			if fl.Missing || fl.FlowID == 0 {
				continue
			}
			fl.ReqRaw, fl.ResRaw = h.flowRawForReport(fl.FlowID)
		}
	}
}

func (h *findingsAPI) flowRawForReport(id int64) (req, res string) {
	f, err := h.st.GetFlow(id)
	if err != nil || f == nil {
		return "", ""
	}
	return truncateReportRaw(string(h.rawRequest(f)), reportBodyCap),
		truncateReportRaw(string(h.rawResponse(f)), reportBodyCap)
}

func truncateReportRaw(s string, cap int) string {
	if cap <= 0 || len(s) <= cap {
		return s
	}
	return s[:cap] + "\n\n… [truncated]"
}

func (h *findingsAPI) createFinding(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Severity                 string  `json:"severity"`
		Status                   string  `json:"status"`
		Source                   string  `json:"source"`
		Title                    string  `json:"title"`
		Target                   string  `json:"target"`
		Detail                   string  `json:"detail"`
		Evidence                 string  `json:"evidence"`
		Fix                      string  `json:"fix"`    // back-compat: still accepted
		Impact                   string  `json:"impact"` // security impact — what an attacker gains / business consequence
		Cvss                     string  `json:"cvss"`   // CVSS score or vector string
		VerificationInstructions string  `json:"verificationInstructions"`
		Body                     string  `json:"body"`    // JSON blocks (new format)
		FlowIDs                  []int64 `json:"flowIds"` // optional: attach these PoC flows on create
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
		Impact: in.Impact, Cvss: in.Cvss, VerificationInstructions: in.VerificationInstructions, Body: in.Body,
	}
	id, err := h.st.CreateFinding(f)
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Attach any PoC flows passed at create time. A bad flowId (e.g. a typo, or
	// a flow that was since purged) must not fail the whole finding — the
	// finding is the durable record and should still be created — but it also
	// must not be silently dropped, so failures are collected and surfaced to
	// the caller as warnings instead.
	var warnings []string
	for _, fid := range in.FlowIDs {
		if err := h.st.AttachFlow(id, fid, "", -1); err != nil {
			warnings = append(warnings, fmt.Sprintf("failed to attach flow %d: %v", fid, err))
		}
	}
	h.broadcast(map[string]any{"type": "findings.update"})
	out, err := h.st.GetFinding(id)
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	// AttachFlow rejects unknown flowIds; any Missing rows here mean a PoC was
	// purged after attach — still surface that so create_finding callers notice.
	for _, fl := range out.Flows {
		if fl.Missing {
			warnings = append(warnings, fmt.Sprintf("attached flow %d not found — PoC will show as missing", fl.FlowID))
		}
	}
	writeJSON(w, http.StatusOK, findingWithWarnings(out, warnings))
}

// findingWithWarnings renders a finding as JSON with an additional "warnings"
// field listing any non-fatal problems from the request (e.g. a PoC flow that
// failed to attach). warnings is omitted entirely when empty, so existing
// callers see byte-identical responses to before this field existed.
func findingWithWarnings(f *store.Finding, warnings []string) map[string]any {
	b, _ := json.Marshal(f)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	if m == nil {
		m = map[string]any{}
	}
	if len(warnings) > 0 {
		m["warnings"] = warnings
	}
	return m
}

func (h *findingsAPI) getFinding(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	f, err := h.st.GetFinding(id)
	if err != nil {
		httpErr(w, http.StatusNotFound, "finding not found")
		return
	}
	writeJSON(w, http.StatusOK, f)
}

func (h *findingsAPI) updateFinding(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	var in struct {
		Severity                 *string `json:"severity"`
		Status                   *string `json:"status"`
		Title                    *string `json:"title"`
		Target                   *string `json:"target"`
		Detail                   *string `json:"detail"`
		Evidence                 *string `json:"evidence"`
		Fix                      *string `json:"fix"`    // back-compat: still accepted
		Impact                   *string `json:"impact"` // security impact — what an attacker gains / business consequence
		Cvss                     *string `json:"cvss"`   // CVSS score or vector string
		VerificationInstructions *string `json:"verificationInstructions"`
		Body                     *string `json:"body"` // JSON blocks
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
	if err := h.st.UpdateFinding(id, in.Severity, in.Status, in.Title, in.Target, in.Detail, in.Evidence, in.Fix, in.Body, in.Impact, in.Cvss, in.VerificationInstructions); err != nil {
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

func (h *findingsAPI) deleteFinding(w http.ResponseWriter, r *http.Request) {
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
func (h *findingsAPI) attachFindingFlow(w http.ResponseWriter, r *http.Request) {
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
		if errors.Is(err, store.ErrFlowNotFound) {
			httpErr(w, http.StatusNotFound, err.Error())
			return
		}
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	h.broadcast(map[string]any{"type": "findings.update"})
	out, err := h.st.GetFinding(id)
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *findingsAPI) detachFindingFlow(w http.ResponseWriter, r *http.Request) {
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

// attachFindingImage uploads screenshot/evidence bytes and inserts an image
// block into the finding narrative. Body: {data, mime?, caption?, position?}.
func (h *findingsAPI) attachFindingImage(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	var in struct {
		Mime     string `json:"mime"`
		Data     string `json:"data"` // raw base64 or data: URL
		Caption  string `json:"caption"`
		Position *int   `json:"position"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpErr(w, http.StatusBadRequest, "bad json")
		return
	}
	mime, raw, err := store.DecodeNotesImagePayload(in.Mime, in.Data)
	if err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	hash, _, err := h.st.PutImageBytes(mime, raw)
	if err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	pos := -1
	if in.Position != nil {
		pos = *in.Position
	}
	if err := h.st.AttachImage(id, hash, mime, in.Caption, pos); err != nil {
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

// getFindingImage serves a content-addressed finding screenshot by hash.
func (h *findingsAPI) getFindingImage(w http.ResponseWriter, r *http.Request) {
	hash := r.PathValue("hash")
	rc, err := h.st.OpenBody(hash)
	if err != nil {
		httpErr(w, http.StatusNotFound, "image not found")
		return
	}
	defer rc.Close()
	// Prefer MIME from a finding that references this hash; never sniff — serve
	// as allowlisted raster or inert application/octet-stream.
	mime := h.st.FindingImageMIME(hash)
	w.Header().Set("Content-Type", store.SanitizeNotesImageMIME(mime))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	_, _ = io.Copy(w, rc)
}
