package control

import (
	"log"
	"net/http"
	"sort"

	"github.com/Veyal/interseptor/internal/checkscript"
	"github.com/Veyal/interseptor/internal/report"
	"github.com/Veyal/interseptor/internal/scanner"
	"github.com/Veyal/interseptor/internal/store"
)

// scannerRun runs the passive scanner over all captured flows (excluding the
// Intruder's attack traffic) and persists the deduplicated findings. Both the
// built-in checks and any user-authored Starlark checks (ChecksDir) run.
func (h *scannerAPI) scannerRun(w http.ResponseWriter, r *http.Request) {
	h.scannerRunWithLimit(w, r, 5000)
}

func (h *scannerAPI) scannerRunWithLimit(w http.ResponseWriter, r *http.Request, limit int) {
	if limit < 1 || limit > 5000 {
		limit = 5000
	}
	// Optional ?host= and ?search= focus the scan on one target instead of all
	// captured traffic (host is a substring match; search matches path/host/method).
	host := r.URL.Query().Get("host")
	search := r.URL.Query().Get("search")
	flows, err := h.st.QueryFlowsFilter(store.FlowFilter{Limit: limit, Host: host, Search: search, ExcludeFlags: store.FlagIntruder | store.FlagActiveScan})
	if err != nil {
		httpInternalErr(w, err)
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
	scannedFlowIDs := make([]int64, 0, len(flows))
	for _, f := range flows {
		// The reconciliation universe includes every flow matching the requested
		// host/path, even if scope changed since its old issue was created.
		scannedFlowIDs = append(scannedFlowIDs, f.ID)
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
	fullScan := host == "" && search == ""
	if fullScan {
		scanned := make(map[int64]struct{}, len(scannedFlowIDs))
		for _, id := range scannedFlowIDs {
			scanned[id] = struct{}{}
		}
		var afterID int64
		for {
			issueFlows, err := h.st.IssueFlowsPage(afterID, 500)
			if err != nil {
				httpInternalErr(w, err)
				return
			}
			if len(issueFlows) == 0 {
				break
			}
			staleFlowIDs := make([]int64, 0, len(issueFlows))
			for _, f := range issueFlows {
				if _, ok := scanned[f.ID]; !ok && !h.sc.InScope(f) {
					staleFlowIDs = append(staleFlowIDs, f.ID)
				}
			}
			if len(staleFlowIDs) > 0 {
				if err := h.st.ReconcileIssuesForScan(nil, staleFlowIDs, nil); err != nil {
					httpInternalErr(w, err)
					return
				}
			}
			afterID = issueFlows[len(issueFlows)-1].ID
		}
	}
	if err := h.st.ReconcileIssuesForScan(scannedFlowIDs, nil, all); err != nil {
		httpInternalErr(w, err)
		return
	}
	h.broadcast(map[string]any{"type": "scanner.update"})
	h.scannerIssues(w, r)
}

func (h *scannerAPI) scannerIssues(w http.ResponseWriter, r *http.Request) {
	issues, err := h.st.ListIssues()
	if err != nil {
		httpInternalErr(w, err)
		return
	}
	if issues == nil {
		issues = []store.Issue{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"issues": issues})
}

func (h *scannerAPI) clearScannerIssues(w http.ResponseWriter, r *http.Request) {
	if err := h.st.ClearIssues(); err != nil {
		httpInternalErr(w, err)
		return
	}
	h.broadcast(map[string]any{"type": "scanner.update"})
	w.WriteHeader(http.StatusNoContent)
}

type scannerTargetHost struct {
	Host  string `json:"host"`
	Count int64  `json:"count"`
}

func (h *scannerAPI) scannerTargets(w http.ResponseWriter, r *http.Request) {
	h.scannerTargetsWithBatch(w, r, 2000)
}

func (h *scannerAPI) scannerTargetsWithBatch(w http.ResponseWriter, _ *http.Request, batch int) {
	if batch < 1 || batch > 5000 {
		batch = 2000
	}
	filter := store.FlowFilter{
		Limit:        batch + 1,
		SortKey:      "id",
		SortDir:      -1,
		ExcludeFlags: store.FlagIntruder | store.FlagActiveScan,
	}
	counts := make(map[string]int64)
	for {
		rows, err := h.st.QueryFlowsListFilter(filter)
		if err != nil {
			httpInternalErr(w, err)
			return
		}
		more := len(rows) > batch
		if more {
			rows = rows[:batch]
		}
		for _, flow := range rows {
			if h.sc.InScope(flow) {
				counts[flow.Host]++
			}
		}
		if !more || len(rows) == 0 {
			break
		}
		last := rows[len(rows)-1]
		filter.CursorID = last.ID
		filter.CursorVal = store.FlowSortValue(last, filter.SortKey)
	}
	hosts := make([]scannerTargetHost, 0, len(counts))
	for host, count := range counts {
		hosts = append(hosts, scannerTargetHost{Host: host, Count: count})
	}
	sort.Slice(hosts, func(i, j int) bool {
		if hosts[i].Count != hosts[j].Count {
			return hosts[i].Count > hosts[j].Count
		}
		return hosts[i].Host < hosts[j].Host
	})
	writeJSON(w, http.StatusOK, map[string]any{"hosts": hosts, "truncated": false})
}

// scannerReport renders the current findings as a downloadable Markdown report.
func (h *scannerAPI) scannerReport(w http.ResponseWriter, r *http.Request) {
	issues, err := h.st.ListIssues()
	if err != nil {
		httpInternalErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="interseptor-findings.md"`)
	w.Write([]byte(report.Findings(issues)))
}
