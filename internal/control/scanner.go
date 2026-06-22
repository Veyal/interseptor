package control

import (
	"net/http"

	"github.com/Veyal/interceptor/internal/scanner"
	"github.com/Veyal/interceptor/internal/store"
)

// scannerRun runs the passive scanner over all captured flows (excluding the
// Intruder's attack traffic) and persists the deduplicated findings.
func (h *Hub) scannerRun(w http.ResponseWriter, r *http.Request) {
	flows, err := h.st.QueryFlowsFilter(store.FlowFilter{Limit: 5000, ExcludeFlags: store.FlagIntruder})
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	var all []store.Issue
	for _, f := range flows {
		all = append(all, scanner.Analyze(f, h.bodyBytes(f.ReqBodyHash), h.bodyBytes(f.ResBodyHash))...)
	}
	if err := h.st.SaveIssues(all); err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	h.broadcast(map[string]any{"type": "scanner.update"})
	h.scannerIssues(w, r)
}

func (h *Hub) scannerIssues(w http.ResponseWriter, r *http.Request) {
	issues, err := h.st.ListIssues()
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if issues == nil {
		issues = []store.Issue{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"issues": issues})
}
