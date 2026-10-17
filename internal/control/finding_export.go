package control

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/Veyal/interseptor/internal/report"
	"github.com/Veyal/interseptor/internal/store"
	"github.com/Veyal/interseptor/internal/version"
)

// findingExportFormats are the accepted ?format= values of
// GET /api/finding-export/{id}. There is no default: a one-click export to a
// bug-bounty platform should never land in the wrong shape because a parameter
// was forgotten.
var findingExportFormats = map[string]bool{
	"hackerone": true, "bugcrowd": true, "jira": true, "sarif": true,
}

const findingExportFormatError = "format must be hackerone, bugcrowd, jira, or sarif"

// exportFinding shapes one finding for an issue tracker or bug-bounty platform.
//
// The payload is the request body for that system minus the identifiers only the
// caller holds (a Jira project key, a HackerOne weakness id); internal/report
// documents what each adapter leaves out and why. Raw evidence bodies are
// included by default with credential headers masked; ?maskHeaders=0 keeps
// them verbatim.
func (h *findingsAPI) exportFinding(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	format := strings.ToLower(strings.TrimSpace(q.Get("format")))
	if !findingExportFormats[format] {
		httpErr(w, http.StatusBadRequest, findingExportFormatError)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpErr(w, http.StatusBadRequest, "bad finding id")
		return
	}
	f, err := h.st.GetFinding(id)
	if err != nil {
		httpNotFoundOrInternal(w, err, "finding not found")
		return
	}
	if f == nil {
		httpErr(w, http.StatusNotFound, "finding not found")
		return
	}
	// A finding the reviewer dismissed is excluded from the report body, and
	// filing it with a platform would be worse than printing it.
	if f.Status == "false_positive" {
		httpErr(w, http.StatusConflict, "finding is marked false_positive and is excluded from exports")
		return
	}

	mode := q.Get("mode")
	if mode == "" {
		mode = "draft"
	}
	if mode != "draft" && mode != "final" {
		httpErr(w, http.StatusBadRequest, "mode must be draft or final")
		return
	}
	fs := []store.Finding{*f}
	quality := assessReportQuality(fs)
	if mode == "final" && !quality.Ready {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":   "Final export needs review. Resolve the listed checks or export a draft.",
			"quality": quality,
		})
		return
	}
	w.Header().Set("X-Interseptor-Report-Mode", mode)

	if v := q.Get("includeBodies"); !(v == "0" || strings.EqualFold(v, "false")) {
		h.enrichFindingReportBodies(fs)
	}
	// A ticket leaves the engagement for a third-party platform, so credential
	// headers are masked unless the caller explicitly asks for maskHeaders=0.
	if v := strings.TrimSpace(q.Get("maskHeaders")); v == "" || boolParam(v) {
		fs = report.MaskSensitiveHeaders(fs)
		w.Header().Set("X-Interseptor-Report-Masked", "1")
	}
	titles := h.findingTitlesForExport()

	w.Header().Set("Content-Disposition", `attachment; filename="interseptor-finding-`+
		strconv.FormatInt(id, 10)+"-"+format+`.json"`)
	switch format {
	case "hackerone":
		writeJSON(w, http.StatusOK, report.HackerOne(fs[0], titles))
	case "bugcrowd":
		writeJSON(w, http.StatusOK, report.Bugcrowd(fs[0], titles))
	case "jira":
		writeJSON(w, http.StatusOK, report.Jira(fs[0], titles))
	case "sarif":
		writeSARIF(w, report.SARIF(fs, version.String()))
	default:
		httpErr(w, http.StatusBadRequest, findingExportFormatError)
	}
}

// findingTitlesForExport maps finding ids to titles so a related-finding link
// reads as "#12 Session fixation" rather than a bare id. A lookup failure
// degrades to bare ids rather than failing the export.
func (h *findingsAPI) findingTitlesForExport() map[int64]string {
	all, err := h.st.ListFindings("", "", "")
	if err != nil {
		return nil
	}
	titles := make(map[int64]string, len(all))
	for _, f := range all {
		titles[f.ID] = f.Title
	}
	return titles
}
