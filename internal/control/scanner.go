package control

import (
	"log"
	"net/http"

	"github.com/Veyal/interceptor/internal/checkscript"
	"github.com/Veyal/interceptor/internal/report"
	"github.com/Veyal/interceptor/internal/scanner"
	"github.com/Veyal/interceptor/internal/store"
)

// scannerRun runs the passive scanner over all captured flows (excluding the
// Intruder's attack traffic) and persists the deduplicated findings. Both the
// built-in checks and any user-authored Starlark checks (ChecksDir) run.
func (h *Hub) scannerRun(w http.ResponseWriter, r *http.Request) {
	// Optional ?host= and ?search= focus the scan on one target instead of all
	// captured traffic (host is a substring match; search matches path/host/method).
	host := r.URL.Query().Get("host")
	search := r.URL.Query().Get("search")
	flows, err := h.st.QueryFlowsFilter(store.FlowFilter{Limit: 5000, Host: host, Search: search, ExcludeFlags: store.FlagIntruder | store.FlagActiveScan})
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Compile user checks once; surface compile failures without aborting.
	var checks []*checkscript.Check
	disabled := h.checksDisabledSet()
	if h.ChecksDir != "" {
		var cerrs map[string]error
		checks, cerrs = checkscript.LoadDir(h.ChecksDir)
		for name, e := range cerrs {
			log.Printf("scanner: custom check %s failed to compile: %v", name, e)
		}
		// A Starlark file with the same id as a built-in overrides it.
		for _, c := range checks {
			if scanner.IsBuiltinID(c.ID) {
				disabled[c.ID] = true
			}
		}
	}

	var all []store.Issue
	for _, f := range flows {
		if !h.sc.InScope(f) { // focus the scanner on in-scope traffic only
			continue
		}
		req, res := h.bodyBytes(f.ReqBodyHash), h.bodyBytes(f.ResBodyHash)
		all = append(all, scanner.AnalyzeWithDisabled(f, req, res, disabled)...)

		if len(checks) > 0 {
			cf := checkscript.Flow{
				ID: f.ID, Method: f.Method, Scheme: f.Scheme, Host: f.Host, Port: f.Port,
				Path: f.Path, Status: f.Status, Mime: f.Mime,
				ReqHeaders: f.ReqHeaders, ResHeaders: f.ResHeaders,
				ReqBody: string(req), ResBody: string(res),
			}
			for _, c := range checks {
				if disabled[c.ID] {
					continue
				}
				iss, err := c.Run(cf)
				if err != nil {
					log.Printf("scanner: custom check %s on flow %d: %v", c.ID, f.ID, err)
					continue
				}
				all = append(all, iss...)
			}
		}
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

// scannerReport renders the current findings as a downloadable Markdown report.
func (h *Hub) scannerReport(w http.ResponseWriter, r *http.Request) {
	issues, err := h.st.ListIssues()
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="interceptor-findings.md"`)
	w.Write([]byte(report.Findings(issues)))
}
