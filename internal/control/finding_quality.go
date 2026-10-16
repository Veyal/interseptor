package control

import (
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/Veyal/interseptor/internal/cvss"
	"github.com/Veyal/interseptor/internal/store"
)

// reportGateIssue is one failed final-report rule. Rule is stable and
// machine-readable; Field (and Capability for claim checks) says what to fix.
type reportGateIssue struct {
	Rule       string `json:"rule"`
	Field      string `json:"field"`
	Capability string `json:"capability,omitempty"`
	Message    string `json:"message"`
}
type reportFindingQuality struct {
	ID     int64                       `json:"id"`
	Title  string                      `json:"title"`
	Ready  bool                        `json:"ready"`
	Checks []store.FindingQualityCheck `json:"checks"`
	Issues []reportGateIssue           `json:"issues"`
}

// reportBoardRow is one line of the project-wide "what blocks the report" table.
type reportBoardRow struct {
	ID       int64    `json:"id"`
	Title    string   `json:"title"`
	Severity string   `json:"severity"`
	Status   string   `json:"status"`
	Ready    bool     `json:"ready"`
	Gaps     []string `json:"gaps"`
}
type reportQuality struct {
	Ready    bool                   `json:"ready"`
	Total    int                    `json:"total"`
	Summary  reportBoardSummary     `json:"summary"`
	Board    []reportBoardRow       `json:"board"`
	Findings []reportFindingQuality `json:"findings"`
	Message  string                 `json:"message,omitempty"`
}
type reportBoardSummary struct {
	Ready   int `json:"ready"`
	Blocked int `json:"blocked"`
}

var reportSeverityRank = map[string]int{"critical": 0, "high": 1, "medium": 2, "low": 3, "info": 4}

func severityRank(s string) int {
	if r, ok := reportSeverityRank[strings.ToLower(strings.TrimSpace(s))]; ok {
		return r
	}
	return len(reportSeverityRank)
}

// reportGateIssues is the final report-quality gate for one enriched finding.
// It maps every readiness check to {rule, field/capability, message} and adds
// the final-report-only rules. It never modifies the finding or its evidence.
func reportGateIssues(f *store.Finding) []reportGateIssue {
	issues := []reportGateIssue{}
	if f.Readiness != nil {
		for _, c := range f.Readiness.Checks {
			issues = append(issues, reportGateIssue{Rule: c.Code, Field: c.Field, Capability: c.Capability, Message: c.Message})
		}
	}
	if raw := strings.TrimSpace(f.Cvss); raw != "" {
		// A parseable but non-4.0 vector is reported here; unparseable ones are
		// already reported by the "cvss" readiness check.
		if ev, err := cvss.Evaluate(raw); err == nil && !strings.HasPrefix(strings.ToUpper(ev.CanonicalVector), "CVSS:4.0") {
			issues = append(issues, reportGateIssue{Rule: "cvss_version", Field: "cvss", Message: "Final reports require a CVSS v4.0 vector; re-score this finding with CVSS:4.0."})
		}
	}
	return issues
}

func assessFindingQuality(f store.Finding) (reportFindingQuality, reportBoardRow) {
	f.EnrichCompleteness()
	issues := reportGateIssues(&f)
	ready := f.Ready && len(issues) == 0
	checks := []store.FindingQualityCheck{}
	if f.Readiness != nil {
		checks = f.Readiness.Checks
	}
	gaps := make([]string, 0, len(issues))
	for _, is := range issues {
		gaps = append(gaps, is.Rule)
	}
	return reportFindingQuality{f.ID, f.Title, ready, checks, issues},
		reportBoardRow{ID: f.ID, Title: f.Title, Severity: f.Severity, Status: f.Status, Ready: ready, Gaps: gaps}
}

func assessReportQuality(findings []store.Finding) reportQuality {
	out := reportQuality{Ready: len(findings) > 0, Total: len(findings), Findings: []reportFindingQuality{}, Board: []reportBoardRow{}}
	if len(findings) == 0 {
		out.Message = "Select at least one finding for a final report."
	}
	for _, f := range findings {
		q, row := assessFindingQuality(f)
		if !q.Ready {
			out.Ready = false
			out.Summary.Blocked++
		} else {
			out.Summary.Ready++
		}
		out.Findings = append(out.Findings, q)
		out.Board = append(out.Board, row)
	}
	sort.SliceStable(out.Board, func(i, j int) bool {
		a, b := out.Board[i], out.Board[j]
		if ra, rb := severityRank(a.Severity), severityRank(b.Severity); ra != rb {
			return ra < rb
		}
		return a.ID < b.ID
	})
	return out
}
func (h *findingsAPI) reportReadiness(w http.ResponseWriter, r *http.Request) {
	fs, err := h.st.ListFindings("", "", r.URL.Query().Get("tag"))
	if err != nil {
		httpInternalErr(w, err)
		return
	}
	writeJSON(w, 200, assessReportQuality(filterReportFindings(fs, r.URL.Query().Get("statuses"))))
}

// findingQuality returns the final-gate result for a single finding, with the
// same shape as one entry of the project-wide readiness response.
func (h *findingsAPI) findingQuality(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		httpErr(w, http.StatusBadRequest, "invalid finding id")
		return
	}
	f, err := h.st.GetFinding(id)
	if err != nil {
		httpNotFoundOrInternal(w, err, "finding not found")
		return
	}
	q, _ := assessFindingQuality(*f)
	writeJSON(w, http.StatusOK, q)
}
