package control

import (
	"net/http"

	"github.com/Veyal/interseptor/internal/store"
)

type reportFindingQuality struct {
	ID     int64                       `json:"id"`
	Title  string                      `json:"title"`
	Ready  bool                        `json:"ready"`
	Checks []store.FindingQualityCheck `json:"checks"`
}
type reportQuality struct {
	Ready    bool                   `json:"ready"`
	Total    int                    `json:"total"`
	Findings []reportFindingQuality `json:"findings"`
	Message  string                 `json:"message,omitempty"`
}

func assessReportQuality(findings []store.Finding) reportQuality {
	out := reportQuality{Ready: len(findings) > 0, Total: len(findings), Findings: []reportFindingQuality{}}
	if len(findings) == 0 {
		out.Message = "Select at least one finding for a final report."
	}
	for _, f := range findings {
		f.EnrichCompleteness()
		if !f.Ready {
			out.Ready = false
		}
		out.Findings = append(out.Findings, reportFindingQuality{f.ID, f.Title, f.Ready, f.Readiness.Checks})
	}
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
