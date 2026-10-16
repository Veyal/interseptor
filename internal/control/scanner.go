package control

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"sort"

	"github.com/Veyal/interseptor/internal/checkscript"
	"github.com/Veyal/interseptor/internal/plugin"
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
	flows, err := h.st.QueryFlowsFilter(store.FlowFilter{Limit: limit, Host: host, Search: search, ExcludeFlags: store.FlagIntruder})
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

	// The scan runs across one goroutine per CPU. It is registered so
	// POST /api/scanner/cancel can stop it, and inherits the request context
	// so a disconnected client no longer leaves a scan running into a dead
	// ResponseWriter.
	ctx, scan := h.scanJob.begin(r.Context())
	defer scan.release()

	h.broadcast(scanProgressEvent(scanner.Progress{Total: len(flows)}, false))
	result, err := scanner.RunPassive(ctx, scanner.Request{
		Flows:    flows,
		Disabled: disabled,
		// The reconciliation universe includes every flow matching the
		// requested host/path, even if scope changed since its old issue was
		// created — so scope filters what is analysed, not what is considered.
		InScope: h.sc.InScope,
		Bodies:  h.scanBodies,
		Custom:  h.customChecksRunner(checks, disabled),
		OnProgress: func(p scanner.Progress) {
			h.broadcast(scanProgressEvent(p, false))
		},
		OnIssue: func(is store.Issue) { h.broadcast(scanIssueEvent(is)) },
	})
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			h.broadcast(scanProgressEvent(scanner.Progress{Total: len(flows), Done: true}, true))
			httpErr(w, http.StatusConflict, "passive scan cancelled before it finished; nothing was persisted")
			return
		}
		httpFileNotFoundOrInternal(w, err, "flow body not found")
		return
	}
	// beforeScanCommit is a test seam that fires in the commit window — after
	// RunPassive has returned a complete result but before anything is written.
	// It is nil in production.
	if h.beforeScanCommit != nil {
		h.beforeScanCommit()
	}
	// Point of no return. A cancel can land between RunPassive finishing and
	// this write even though every worker completed — the context is no longer
	// consulted, so the only thing standing between a cancelled scan and a
	// committed one is this atomic claim. If a cancel won it, persist nothing
	// and report the conflict, so a scan never both persists and is reported
	// cancelled.
	if !scan.claimCommit() {
		h.broadcast(scanProgressEvent(scanner.Progress{Total: len(flows), Done: true}, true))
		httpErr(w, http.StatusConflict, "passive scan cancelled before it finished; nothing was persisted")
		return
	}
	all := result.Issues
	scannedFlowIDs := result.FlowIDs
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
	for _, is := range all {
		plugin.EmitScanIssue(is.FlowID, is.Severity, is.Title) // extension hooks (best-effort)
	}
	h.broadcast(map[string]any{"type": "scanner.update"})
	h.scannerIssues(w, r)
}

// scanBodies is the scan engine's body loader. It reports a missing blob as
// os.ErrNotExist so the engine skips that one flow, and anything else as a
// real error that ends the scan.
func (h *Hub) scanBodies(f *store.Flow) (reqBody, resBody []byte, err error) {
	if reqBody, err = h.bodyBytesResult(f.ReqBodyHash); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			log.Printf("scanner: skip flow %d: request body blob %q missing", f.ID, f.ReqBodyHash)
		}
		return nil, nil, err
	}
	if resBody, err = h.bodyBytesResult(f.ResBodyHash); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			log.Printf("scanner: skip flow %d: response body blob %q missing", f.ID, f.ResBodyHash)
		}
		return nil, nil, err
	}
	return reqBody, resBody, nil
}

// customChecksRunner adapts the compiled Starlark checks to the scan engine, so
// internal/scanner never imports internal/checkscript. A check that fails at
// runtime is logged and skipped: one broken user script must not void a scan.
// The compiled checks are frozen, so running them from several goroutines is
// safe; each call still gets its own Starlark thread.
func (h *Hub) customChecksRunner(checks []*checkscript.Check, disabled map[string]bool) scanner.CustomChecks {
	if len(checks) == 0 {
		return nil
	}
	return func(f *store.Flow, reqBody, resBody []byte) []store.Issue {
		cf := checkscript.Flow{
			ID: f.ID, Method: f.Method, Scheme: f.Scheme, Host: f.Host, Port: f.Port,
			Path: f.Path, Status: f.Status, Mime: f.Mime,
			ReqHeaders: f.ReqHeaders, ResHeaders: f.ResHeaders,
			ReqBody: string(reqBody), ResBody: string(resBody),
		}
		var out []store.Issue
		for _, c := range checks {
			if disabled[c.ID] {
				continue
			}
			iss, err := c.Run(cf)
			if err != nil {
				log.Printf("scanner: custom check %s on flow %d: %v", c.ID, f.ID, err)
				continue
			}
			out = append(out, iss...)
		}
		return out
	}
}

// scannerCancel stops every running passive scan. The scan handler blocks
// until the scan finishes, so cancelling it takes a second request.
func (h *scannerAPI) scannerCancel(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"cancelled": h.scanJob.cancelAll()})
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
		ExcludeFlags: store.FlagIntruder,
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
