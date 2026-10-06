package control

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Veyal/interseptor/internal/report"
	"github.com/Veyal/interseptor/internal/store"
)

// maxFindingBodyBytes is the maximum byte size of both the canonical block body
// and, separately, the aggregate scalar report envelope. These caps prevent
// storage/UI DoS from runaway AI loops or malicious clients while leaving the
// full body allowance available for structured reproduction and evidence.
const maxFindingBodyBytes = 1 << 20 // 1 MiB

const maxFindingMutationRequestBytes int64 = 16 << 20

// maxFindingTextBlock is the maximum byte size of a single text block's markdown
// content within a finding body. Mirrors the reMaxText cap used elsewhere.
const maxFindingTextBlock = 256 << 10 // 256 KiB

// checkFindingBodySize validates the content supplied by one incoming write.
// The store repeats the aggregate check against retained fields so a client
// cannot bypass it with several individually small PATCH requests.
//
// Reads of pre-existing large findings are never blocked; this only guards writes.
func checkFindingBodySize(f store.Finding) string {
	if f.Body != "" {
		if len(f.Body) > maxFindingBodyBytes {
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
		if err := json.Unmarshal([]byte(f.Body), &blocks); err == nil {
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
	}
	if f.Body == "" && len(f.Detail)+len(f.Evidence)+len(f.Fix) > maxFindingBodyBytes {
		return "finding body too large (max 1 MiB)"
	}
	scalarSize := len(f.Title) + len(f.Summary) + len(f.Target) + len(f.Fix) +
		len(f.Impact) + len(f.Why) + len(f.Cwe) + len(f.Cvss) +
		len(f.VerificationInstructions) + len(f.Retest) + len(f.Detail) + len(f.Evidence)
	if scalarSize > maxFindingBodyBytes {
		return "finding narrative too large (max 1 MiB across report fields)"
	}
	return ""
}

// Findings: a curated, persistent vulnerability store for a project (distinct from
// the ephemeral passive-scanner issues). A finding can have multiple request/response
// flows attached as PoC evidence — the human (or AI) selects them from History. The
// AI records findings here as structured memory; the human reviews/curates them.

func (h *findingsAPI) listFindings(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	fs, err := h.st.ListFindings(q.Get("severity"), q.Get("status"), q.Get("tag"))
	if err != nil {
		httpInternalErr(w, err)
		return
	}
	if fs == nil {
		fs = []store.Finding{}
	}
	if strings.EqualFold(q.Get("view"), "summary") {
		summaries, total, truncated := findingListSummaries(fs)
		writeJSON(w, http.StatusOK, map[string]any{"findings": summaries, "total": total, "truncated": truncated})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"findings": fs})
}

const (
	maxFindingListSummaries   = 500
	maxFindingSummaryText     = 512
	maxFindingSummaryTags     = 20
	maxFindingSummaryTagBytes = 128
	maxFindingSummaryMissing  = 50
)

type findingListSummary struct {
	ID               int64                   `json:"id"`
	Severity         string                  `json:"severity"`
	Status           string                  `json:"status"`
	Title            string                  `json:"title"`
	Summary          string                  `json:"summary,omitempty"`
	Target           string                  `json:"target,omitempty"`
	TargetCount      int                     `json:"targetCount"`
	Confidence       string                  `json:"confidence,omitempty"`
	Tags             []string                `json:"tags"`
	TagCount         int                     `json:"tagCount"`
	Ready            bool                    `json:"ready"`
	Missing          []string                `json:"missing"`
	Readiness        *store.FindingReadiness `json:"readiness,omitempty"`
	MissingFlowIDs   []int64                 `json:"missingFlowIds"`
	MissingFlowCount int                     `json:"missingFlowCount"`
}

func findingListSummaries(fs []store.Finding) ([]findingListSummary, int, bool) {
	total := len(fs)
	if len(fs) > maxFindingListSummaries {
		fs = fs[:maxFindingListSummaries]
	}
	out := make([]findingListSummary, 0, len(fs))
	for i := range fs {
		f := &fs[i]
		tags := append([]string(nil), f.Tags...)
		if len(tags) > maxFindingSummaryTags {
			tags = tags[:maxFindingSummaryTags]
		}
		for j := range tags {
			tags[j] = truncateFindingSummary(tags[j], maxFindingSummaryTagBytes)
		}
		missingFlows := missingFlowIDs(f)
		missingFlowCount := len(missingFlows)
		if len(missingFlows) > maxFindingSummaryMissing {
			missingFlows = missingFlows[:maxFindingSummaryMissing]
		}
		out = append(out, findingListSummary{
			ID: f.ID, Severity: f.Severity, Status: f.Status,
			Title:   truncateFindingSummary(f.Title, maxFindingSummaryText),
			Summary: truncateFindingSummary(f.Summary, maxFindingSummaryText),
			Target:  truncateFindingSummary(f.Target, maxFindingSummaryText), TargetCount: len(f.Targets),
			Confidence: f.Confidence, Tags: tags, TagCount: len(f.Tags), Ready: f.Ready,
			Missing: append([]string(nil), f.Missing...), Readiness: f.Readiness,
			MissingFlowIDs: missingFlows, MissingFlowCount: missingFlowCount,
		})
	}
	return out, total, total > len(out)
}

func truncateFindingSummary(value string, max int) string {
	if len(value) <= max {
		return value
	}
	value = value[:max]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value + "…"
}

func (h *findingsAPI) listFindingTags(w http.ResponseWriter, r *http.Request) {
	tags, err := h.st.DistinctFindingTags()
	if err != nil {
		httpInternalErr(w, err)
		return
	}
	if tags == nil {
		tags = []store.TagCount{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"tags": tags})
}

// findingsReport renders the curated findings (with PoC flows) as a downloadable
// engagement report. Passive-scan issues are omitted by default; pass ?issues=1 to
// append the passive-scan appendix. ?format=html|json returns HTML or JSON;
// ?tag= filters; ?groupBy=tag sections by finding tag; ?omitTags=a,b excludes
// those tags from a grouped export (findings carrying only omitted tags drop out).
// Full reconstructed request/response bodies for PoC flows are included by default
// (?includeBodies=0 to omit — useful for huge projects).
func (h *findingsAPI) findingsReport(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	format := strings.ToLower(strings.TrimSpace(q.Get("format")))
	if format != "" && format != "md" && format != "markdown" && format != "html" && format != "json" {
		httpErr(w, http.StatusBadRequest, "format must be md, html, or json")
		return
	}
	fs, err := h.st.ListFindings("", "", q.Get("tag"))
	if err != nil {
		httpInternalErr(w, err)
		return
	}
	fs = filterReportFindings(fs, q.Get("statuses"))
	mode := q.Get("mode")
	if mode == "" {
		mode = "draft"
	}
	if mode != "draft" && mode != "final" {
		httpErr(w, 400, "mode must be draft or final")
		return
	}
	groupByTag := strings.EqualFold(q.Get("groupBy"), "tag")
	omitTags := splitCSV(q.Get("omitTags"))
	if groupByTag && format != "json" {
		fs = report.FilterGroupedFindings(fs, omitTags)
	}
	quality := assessReportQuality(fs)
	if mode == "final" && !quality.Ready {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "Final report needs review. Resolve the listed checks or export a draft.", "quality": quality})
		return
	}
	w.Header().Set("X-Interseptor-Report-Mode", mode)

	var issues []store.Issue
	if q.Get("issues") == "1" {
		if iss, err := h.st.ListIssues(); err == nil {
			issues = iss
		}
	}
	includeBodies := true
	if v := q.Get("includeBodies"); v == "0" || strings.EqualFold(v, "false") {
		includeBodies = false
	}
	if includeBodies {
		h.enrichFindingReportBodies(fs)
	}
	tagOrder := splitCSV(q.Get("tagOrder"))
	audit := ""
	if q.Get("audit") == "1" {
		revisions := map[int64][]store.FindingRevision{}
		for _, f := range fs {
			if revs, err := h.st.ListFindingRevisions(f.ID, 100); err == nil {
				revisions[f.ID] = revs
			}
		}
		audit = report.AuditTrail(fs, revisions)
	}
	switch format {
	case "json":
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="interseptor-report.json"`)
		writeJSON(w, http.StatusOK, map[string]any{"findings": fs, "issues": issues, "quality": quality, "mode": mode})
	case "html":
		h.enrichFindingReportImages(fs)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="interseptor-report.html"`)
		if groupByTag {
			w.Write([]byte(report.HTMLFromMarkdown(report.ProjectGroupedByTag(fs, issues, tagOrder, omitTags) + audit)))
		} else {
			w.Write([]byte(report.HTMLFromMarkdown(report.Project(fs, issues) + audit)))
		}
	case "", "md", "markdown":
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="interseptor-report.md"`)
		if groupByTag {
			w.Write([]byte(report.ProjectGroupedByTag(fs, issues, tagOrder, omitTags) + audit))
		} else {
			w.Write([]byte(report.Project(fs, issues) + audit))
		}
	default:
		httpErr(w, http.StatusBadRequest, "format must be md, html, or json")
	}
}

func filterReportFindings(fs []store.Finding, raw string) []store.Finding {
	raw = strings.TrimSpace(strings.ToLower(raw))
	if raw == "all" {
		return fs
	}
	if raw == "" {
		raw = "open,verified,fixed"
	}
	allowed := make(map[string]bool)
	for _, status := range splitCSV(raw) {
		allowed[strings.ToLower(status)] = true
	}
	out := make([]store.Finding, 0, len(fs))
	for _, f := range fs {
		if allowed[strings.ToLower(f.Status)] {
			out = append(out, f)
		}
	}
	return out
}

func splitCSV(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// reportBodyCap bounds each reconstructed req/res in the export so huge downloads
// cannot blow up the report. Truncation is marked explicitly.
const reportBodyCap = 64 << 10 // 64 KiB

// reportImageEmbedCap matches the supported finding-image upload size; the
// separate total cap keeps offline exports bounded when several images exist.
const reportImageEmbedCap = 5 << 20      // 5 MiB
const reportImageEmbedTotalCap = 8 << 20 // 8 MiB per offline HTML report

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

// enrichFindingReportImages rewrites image block URLs to data: URIs so a
// downloaded HTML report shows screenshots without the control API.
func (h *findingsAPI) enrichFindingReportImages(fs []store.Finding) {
	total := 0
	for i := range fs {
		for j := range fs[i].Blocks {
			bl := &fs[i].Blocks[j]
			if bl.Type != "image" || bl.Missing || bl.Hash == "" {
				continue
			}
			rc, err := h.st.OpenBody(bl.Hash)
			if err != nil {
				bl.URL = ""
				bl.Missing = true
				continue
			}
			data, err := io.ReadAll(io.LimitReader(rc, reportImageEmbedCap+1))
			rc.Close()
			if err != nil || len(data) == 0 {
				bl.URL = ""
				bl.Missing = true
				continue
			}
			if len(data) > reportImageEmbedCap || total+len(data) > reportImageEmbedTotalCap {
				bl.URL = ""
				continue
			}
			mime := store.SanitizeNotesImageMIME(bl.Mime)
			if mime == "" || mime == "application/octet-stream" {
				mime = "image/png"
			}
			bl.URL = "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)
			total += len(data)
		}
	}
}

func (h *findingsAPI) flowRawForReport(id int64) (req, res string) {
	f, err := h.st.GetFlow(id)
	if err != nil || f == nil {
		return "", ""
	}
	return h.flowRawSideForReport(f, true), h.flowRawSideForReport(f, false)
}

const reportBodyTruncationMarker = "\n\n… [body truncated at 64 KiB]"

// flowRawSideForReport reconstructs one side of a captured exchange while
// bounding only the body read. Headers remain intact so an exported report is
// still useful for reproducing the request/response, even when a payload is
// large. The body is read through LimitReader rather than bodyBytesResult,
// which would load the complete content-addressed body before truncating it.
func (h *findingsAPI) flowRawSideForReport(f *store.Flow, request bool) string {
	var b bytes.Buffer
	var headers map[string][]string
	var hash, host string
	if request {
		fmt.Fprintf(&b, "%s %s %s\r\n", f.Method, orVal(f.Path, "/"), orVal(f.HTTPVersion, "HTTP/1.1"))
		headers, hash, host = f.ReqHeaders, f.ReqBodyHash, f.Host
	} else {
		fmt.Fprintf(&b, "%s %d %s\r\n", orVal(f.HTTPVersion, "HTTP/1.1"), f.Status, http.StatusText(f.Status))
		headers, hash = f.ResHeaders, f.ResBodyHash
	}

	displayHeaders, body, truncated := h.reportBody(hash, headers)
	writeHeaders(&b, displayHeaders, host)
	b.WriteString("\r\n")
	b.Write(body)
	if truncated {
		b.WriteString(reportBodyTruncationMarker)
	}
	return b.String()
}

// reportBody bounds the returned raw or decoded representation while allowing
// supported compression streams to consume enough encoded input to produce it.
func (h *findingsAPI) reportBody(hash string, headers map[string][]string) (map[string][]string, []byte, bool) {
	displayHeaders, body, truncated, err := h.bodyForDisplayLimit(hash, headers, reportBodyCap)
	if err != nil {
		return headers, nil, false
	}
	return displayHeaders, body, truncated
}

func (h *findingsAPI) createFinding(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Severity                 string                     `json:"severity"`
		Status                   string                     `json:"status"`
		Source                   string                     `json:"source"`
		Title                    string                     `json:"title"`
		Summary                  string                     `json:"summary"`
		Target                   string                     `json:"target"`
		Targets                  store.FindingTargets       `json:"targets"`
		ProofReview              store.FindingProofReview   `json:"proofReview"`
		Claims                   []store.FindingClaim       `json:"claims"`
		NotExecuted              []store.FindingNotExecuted `json:"notExecuted"`
		RelatedFindings          []store.FindingRelation    `json:"relatedFindings"`
		Confidence               string                     `json:"confidence"`
		Detail                   string                     `json:"detail"`
		Evidence                 string                     `json:"evidence"`
		Fix                      string                     `json:"fix"`    // remediation — optional
		Impact                   string                     `json:"impact"` // what an attacker gains / business consequence
		Why                      string                     `json:"why"`    // why this is a vulnerability
		Cwe                      string                     `json:"cwe"`
		Environment              string                     `json:"environment"` // production | staging | development | testing | local; legacy prod
		Cvss                     string                     `json:"cvss"`
		VerificationInstructions string                     `json:"verificationInstructions"`
		Retest                   string                     `json:"retest"`
		Body                     string                     `json:"body"`    // JSON blocks (PoC timeline)
		Blocks                   *[]store.FindingBlock      `json:"blocks"`  // canonical structured alternative to body
		FlowIDs                  []int64                    `json:"flowIds"` // optional: attach these PoC flows on create
		Tags                     []string                   `json:"tags"`    // report-scoping labels (cms, api, …)
	}
	if !decodeLimitedJSON(w, r, maxFindingMutationRequestBytes, &in) {
		return
	}
	if in.Title == "" {
		httpErr(w, http.StatusBadRequest, "title required")
		return
	}
	if in.Body != "" && in.Blocks != nil {
		httpErr(w, http.StatusBadRequest, "send either body or blocks, not both")
		return
	}
	if in.Blocks != nil {
		normBlocks, err := store.NormalizeFindingBlocks(*in.Blocks)
		if err != nil {
			httpErr(w, http.StatusBadRequest, err.Error())
			return
		}
		in.Body, err = store.MarshalFindingBlocks(normBlocks)
		if err != nil {
			httpErr(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if in.Body != "" {
		norm, err := store.NormalizeFindingBody(in.Body)
		if err != nil {
			httpErr(w, http.StatusBadRequest, err.Error())
			return
		}
		in.Body = norm
	}
	f := &store.Finding{
		Severity: in.Severity, Status: in.Status, Source: orVal(in.Source, "human"),
		Title: in.Title, Summary: in.Summary, Target: in.Target, Targets: in.Targets, ProofReview: in.ProofReview, Claims: in.Claims, NotExecuted: in.NotExecuted, RelatedFindings: in.RelatedFindings, Confidence: in.Confidence, Detail: in.Detail, Evidence: in.Evidence, Fix: in.Fix, Retest: in.Retest,
		Impact: in.Impact, Why: in.Why, Cwe: in.Cwe, Environment: in.Environment,
		Cvss: in.Cvss, VerificationInstructions: in.VerificationInstructions, Body: in.Body,
		Tags: in.Tags,
	}
	if msg := checkFindingBodySize(*f); msg != "" {
		httpErr(w, http.StatusRequestEntityTooLarge, msg)
		return
	}
	id, err := h.st.CreateFinding(f, findingAPIChange(""))
	if err != nil {
		if errors.Is(err, store.ErrInvalidFinding) || errors.Is(err, store.ErrFlowNotFound) || strings.Contains(err.Error(), "type must be") || strings.Contains(err.Error(), "body must be") {
			httpErr(w, http.StatusBadRequest, err.Error())
			return
		}
		httpInternalErr(w, err)
		return
	}
	// Attach any PoC flows passed at create time. A bad flowId (e.g. a typo, or
	// a flow that was since purged) must not fail the whole finding — the
	// finding is the durable record and should still be created — but it also
	// must not be silently dropped, so failures are collected and surfaced to
	// the caller as warnings instead.
	var warnings []string
	for _, fid := range in.FlowIDs {
		if err := h.st.AttachFlow(id, fid, "", -1, findingAPIChange("")); err != nil {
			warnings = append(warnings, fmt.Sprintf("failed to attach flow %d: %v", fid, err))
		}
	}
	h.broadcast(map[string]any{"type": "findings.update"})
	out, err := h.st.GetFinding(id)
	if err != nil {
		httpInternalErr(w, err)
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
	return findingAPIResponse(f, warnings)
}

// findingAPIResponse renders a finding plus optional warnings and missingFlowIds.
func findingAPIResponse(f *store.Finding, warnings []string) map[string]any {
	b, _ := json.Marshal(f)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	if m == nil {
		m = map[string]any{}
	}
	if len(warnings) > 0 {
		m["warnings"] = warnings
	}
	if missing := missingFlowIDs(f); len(missing) > 0 {
		m["missingFlowIds"] = missing
	} else {
		m["missingFlowIds"] = []int64{}
	}
	return m
}

func missingFlowIDs(f *store.Finding) []int64 {
	if f == nil {
		return nil
	}
	var out []int64
	seen := map[int64]bool{}
	for _, bl := range f.Blocks {
		if bl.Type == "flow" && bl.Missing && bl.FlowID > 0 && !seen[bl.FlowID] {
			seen[bl.FlowID] = true
			out = append(out, bl.FlowID)
		}
	}
	for _, fl := range f.Flows {
		if fl.Missing && fl.FlowID > 0 && !seen[fl.FlowID] {
			seen[fl.FlowID] = true
			out = append(out, fl.FlowID)
		}
	}
	return out
}

func (h *findingsAPI) getFinding(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	f, err := h.st.GetFinding(id)
	if err != nil {
		httpNotFoundOrInternal(w, err, "finding not found")
		return
	}
	writeJSON(w, http.StatusOK, findingAPIResponse(f, nil))
}

func (h *findingsAPI) requireFinding(w http.ResponseWriter, id int64) bool {
	if id <= 0 {
		httpErr(w, http.StatusBadRequest, "bad id")
		return false
	}
	if _, err := h.st.GetFinding(id); err != nil {
		httpNotFoundOrInternal(w, err, "finding not found")
		return false
	}
	return true
}

func (h *findingsAPI) updateFinding(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	var in struct {
		Severity                 *string                     `json:"severity"`
		Status                   *string                     `json:"status"`
		Title                    *string                     `json:"title"`
		Summary                  *string                     `json:"summary"`
		Target                   *string                     `json:"target"`
		Targets                  *store.FindingTargets       `json:"targets"`
		ProofReview              *store.FindingProofReview   `json:"proofReview"`
		Claims                   *[]store.FindingClaim       `json:"claims"`
		NotExecuted              *[]store.FindingNotExecuted `json:"notExecuted"`
		RelatedFindings          *[]store.FindingRelation    `json:"relatedFindings"`
		Confidence               *string                     `json:"confidence"`
		Detail                   *string                     `json:"detail"`
		Evidence                 *string                     `json:"evidence"`
		Fix                      *string                     `json:"fix"`
		Impact                   *string                     `json:"impact"`
		Why                      *string                     `json:"why"`
		Cwe                      *string                     `json:"cwe"`
		Environment              *string                     `json:"environment"`
		Cvss                     *string                     `json:"cvss"`
		VerificationInstructions *string                     `json:"verificationInstructions"`
		Retest                   *string                     `json:"retest"`
		Body                     *string                     `json:"body"`   // JSON blocks (PoC timeline)
		Blocks                   *[]store.FindingBlock       `json:"blocks"` // canonical structured alternative to body
		Tags                     *[]string                   `json:"tags"`   // when present (incl. []), replaces the tag set
	}
	if !decodeLimitedJSON(w, r, maxFindingMutationRequestBytes, &in) {
		return
	}
	if in.Body != nil && in.Blocks != nil {
		httpErr(w, http.StatusBadRequest, "send either body or blocks, not both")
		return
	}
	if in.Blocks != nil {
		normBlocks, err := store.NormalizeFindingBlocks(*in.Blocks)
		if err != nil {
			httpErr(w, http.StatusBadRequest, err.Error())
			return
		}
		normBody, err := store.MarshalFindingBlocks(normBlocks)
		if err != nil {
			httpErr(w, http.StatusBadRequest, err.Error())
			return
		}
		in.Body = &normBody
	}
	if in.Body != nil {
		norm, err := store.NormalizeFindingBody(*in.Body)
		if err != nil {
			httpErr(w, http.StatusBadRequest, err.Error())
			return
		}
		*in.Body = norm
	}
	value := func(p *string) string {
		if p == nil {
			return ""
		}
		return *p
	}
	mutation := store.Finding{
		Title: value(in.Title), Summary: value(in.Summary), Target: value(in.Target),
		Detail: value(in.Detail), Evidence: value(in.Evidence), Fix: value(in.Fix), Body: value(in.Body),
		Impact: value(in.Impact), Why: value(in.Why), Cwe: value(in.Cwe), Cvss: value(in.Cvss),
		VerificationInstructions: value(in.VerificationInstructions), Retest: value(in.Retest),
	}
	if msg := checkFindingBodySize(mutation); msg != "" {
		httpErr(w, http.StatusRequestEntityTooLarge, msg)
		return
	}
	if err := h.st.UpdateFindingCanonical(id, in.Severity, in.Status, in.Title, in.Target, in.Detail, in.Evidence, in.Fix, in.Body, in.Impact, in.Why, in.Cwe, in.Environment, in.Cvss, in.VerificationInstructions, in.Summary, in.Confidence, in.Retest, in.Tags, store.FindingMetadataPatch{Change: findingAPIChange(""), Targets: in.Targets, ProofReview: in.ProofReview, Claims: in.Claims, NotExecuted: in.NotExecuted, RelatedFindings: in.RelatedFindings}); err != nil {
		if errors.Is(err, store.ErrInvalidFinding) || errors.Is(err, store.ErrFlowNotFound) || strings.Contains(err.Error(), "type must be") || strings.Contains(err.Error(), "body must be") || strings.Contains(err.Error(), "flow block") {
			httpErr(w, http.StatusBadRequest, err.Error())
			return
		}
		httpInternalErr(w, err)
		return
	}
	h.broadcast(map[string]any{"type": "findings.update"})
	out, err := h.st.GetFinding(id)
	if err != nil {
		httpNotFoundOrInternal(w, err, "finding not found")
		return
	}
	writeJSON(w, http.StatusOK, findingAPIResponse(out, nil))
}

func (h *findingsAPI) deleteFinding(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err := h.st.DeleteFinding(id, findingAPIChange("")); err != nil {
		httpInternalErr(w, err)
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
	if !h.requireFinding(w, id) {
		return
	}
	var in struct {
		FlowID   int64  `json:"flowId"`
		Note     string `json:"note"`
		Position *int   `json:"position"` // optional 0-based block index; omit = append
		Role     string `json:"role"`
		Proof    string `json:"proof"`
	}
	if !decodeLimitedJSON(w, r, maxFindingMutationRequestBytes, &in) {
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
	if err := h.st.AttachFlowWithMetadata(id, in.FlowID, in.Note, pos, in.Role, in.Proof, "captured_flow", in.FlowID, findingAPIChange("")); err != nil {
		if errors.Is(err, store.ErrFlowNotFound) {
			httpErr(w, http.StatusNotFound, err.Error())
			return
		}
		if errors.Is(err, store.ErrInvalidFinding) {
			httpErr(w, http.StatusBadRequest, err.Error())
			return
		}
		httpInternalErr(w, err)
		return
	}
	h.broadcast(map[string]any{"type": "findings.update"})
	out, err := h.st.GetFinding(id)
	if err != nil {
		httpInternalErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, findingAPIResponse(out, nil))
}

func (h *findingsAPI) detachFindingFlow(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	flowID, _ := strconv.ParseInt(r.PathValue("flowId"), 10, 64)
	if !h.requireFinding(w, id) {
		return
	}
	if err := h.st.DetachFlow(id, flowID, findingAPIChange("")); err != nil {
		httpInternalErr(w, err)
		return
	}
	h.broadcast(map[string]any{"type": "findings.update"})
	out, _ := h.st.GetFinding(id)
	writeJSON(w, http.StatusOK, findingAPIResponse(out, nil))
}

// attachFindingImage uploads screenshot/evidence bytes and inserts an image
// block into the finding narrative. Body: {data, mime?, caption?, position?}.
func (h *findingsAPI) attachFindingImage(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if !h.requireFinding(w, id) {
		return
	}
	var in struct {
		Mime         string `json:"mime"`
		Data         string `json:"data"` // raw base64 or data: URL
		Caption      string `json:"caption"`
		Position     *int   `json:"position"`
		Role         string `json:"role"`
		Proof        string `json:"proof"`
		Source       string `json:"source"`
		SourceFlowID int64  `json:"sourceFlowId"`
	}
	if !decodeLimitedJSON(w, r, maxFindingMutationRequestBytes, &in) {
		return
	}
	mime, raw, err := store.DecodeNotesImagePayload(in.Mime, in.Data)
	if err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	pos := -1
	if in.Position != nil {
		pos = *in.Position
	}
	role, source, proof := in.Role, in.Source, in.Proof
	_, _, err = h.st.PutAndAttachImage(id, mime, raw, in.Caption, pos, role, proof, source, in.SourceFlowID, findingAPIChange(""))
	if err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	h.broadcast(map[string]any{"type": "findings.update"})
	out, err := h.st.GetFinding(id)
	if err != nil {
		httpNotFoundOrInternal(w, err, "finding not found")
		return
	}
	writeJSON(w, http.StatusOK, findingAPIResponse(out, nil))
}

// classifyFindingImage lets a reviewer relabel an attached image (for example
// an operator upload that is a real browser capture) without re-uploading it.
// Body: {source, reason?}. Ingestion provenance is preserved.
func (h *findingsAPI) classifyFindingImage(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if !h.requireFinding(w, id) {
		return
	}
	var in struct {
		Source string `json:"source"`
		Reason string `json:"reason"`
	}
	if !decodeLimitedJSON(w, r, maxFindingMutationRequestBytes, &in) {
		return
	}
	if err := h.st.ClassifyFindingImage(id, r.PathValue("hash"), in.Source, findingAPIChange(in.Reason)); err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	h.broadcast(map[string]any{"type": "findings.update"})
	out, err := h.st.GetFinding(id)
	if err != nil {
		httpNotFoundOrInternal(w, err, "finding not found")
		return
	}
	writeJSON(w, http.StatusOK, findingAPIResponse(out, nil))
}

// getFindingImage serves a content-addressed finding screenshot by hash.
func (h *findingsAPI) getFindingImage(w http.ResponseWriter, r *http.Request) {
	hash := r.PathValue("hash")
	// Content-addressed bodies are shared with flow captures. Only a hash
	// referenced by a finding image block is authorized on this endpoint.
	mime := h.st.FindingImageMIME(hash)
	if mime == "" {
		httpErr(w, http.StatusNotFound, "image not found")
		return
	}
	rc, err := h.st.OpenBody(hash)
	if err != nil {
		httpErr(w, http.StatusNotFound, "image not found")
		return
	}
	defer rc.Close()
	// Prefer MIME from a finding that references this hash; never sniff — serve
	// as allowlisted raster or inert application/octet-stream.
	w.Header().Set("Content-Type", store.SanitizeNotesImageMIME(mime))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	_, _ = io.Copy(w, rc)
}
