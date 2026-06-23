package control

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/Veyal/interceptor/internal/activescan"
	"github.com/Veyal/interceptor/internal/sender"
	"github.com/Veyal/interceptor/internal/store"
)

// asState is the live active-scan state (one run at a time). "armed" is the
// session-level consent gate: active scans refuse to run until the operator
// arms it (asserting they're authorized to test the in-scope targets).
type asState struct {
	mu       sync.Mutex
	armed    bool
	running  bool
	cancel   context.CancelFunc
	targets  int
	scanned  int
	requests int
	findings []activescan.Finding
}

func (h *Hub) asWriteState(w http.ResponseWriter) {
	h.as.mu.Lock()
	defer h.as.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{
		"armed": h.as.armed, "running": h.as.running,
		"targets": h.as.targets, "scanned": h.as.scanned, "requests": h.as.requests,
		"findings": append([]activescan.Finding{}, h.as.findings...),
	})
}

func (h *Hub) asGet(w http.ResponseWriter, r *http.Request) { h.asWriteState(w) }

func (h *Hub) asArm(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Armed bool `json:"armed"`
	}
	json.NewDecoder(r.Body).Decode(&in)
	h.as.mu.Lock()
	h.as.armed = in.Armed
	h.as.mu.Unlock()
	h.broadcast(map[string]any{"type": "activescan.update"})
	h.asWriteState(w)
}

func (h *Hub) asStop(w http.ResponseWriter, r *http.Request) {
	h.as.mu.Lock()
	if h.as.cancel != nil {
		h.as.cancel()
	}
	h.as.mu.Unlock()
	h.asWriteState(w)
}

func (h *Hub) asStart(w http.ResponseWriter, r *http.Request) {
	var in struct {
		FlowID      int64 `json:"flowId"`
		InScope     bool  `json:"inScope"`
		Arm         bool  `json:"arm"` // arm-and-run (the AI/API consent path)
		MaxRequests int   `json:"maxRequests"`
	}
	json.NewDecoder(r.Body).Decode(&in)

	if in.FlowID == 0 && !in.InScope {
		httpErr(w, http.StatusBadRequest, "specify a flowId or inScope:true")
		return
	}
	// Refuse a bulk "all in-scope" run when scope is unrestricted — otherwise it
	// would actively attack every host in the capture history. (Single-flow scans
	// are explicit, so they don't require scope rules.)
	if in.InScope && !h.sc.HasIncludes() {
		httpErr(w, http.StatusBadRequest, "define a target-scope include rule before an “all in-scope” active scan — with no scope it would attack every captured host. (Scanning a single selected flow doesn’t need scope rules.)")
		return
	}

	// Claim the run under one lock acquisition (check arm + running, then set
	// running) so two concurrent starts can't both launch or orphan the kill switch.
	h.as.mu.Lock()
	if in.Arm {
		h.as.armed = true
	}
	if !h.as.armed {
		h.as.mu.Unlock()
		httpErr(w, http.StatusForbidden, "active scanning is disarmed — arm it first (you confirm you're authorized to test the in-scope targets). It sends crafted/attack requests.")
		return
	}
	if h.as.running {
		h.as.mu.Unlock()
		httpErr(w, http.StatusConflict, "an active scan is already running")
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	h.as.running, h.as.cancel = true, cancel
	h.as.targets, h.as.scanned, h.as.requests, h.as.findings = 0, 0, 0, nil
	h.as.mu.Unlock()

	// release rolls the claim back if we bail before launching the runner.
	release := func() {
		cancel()
		h.as.mu.Lock()
		h.as.running, h.as.cancel = false, nil
		h.as.mu.Unlock()
		h.broadcast(map[string]any{"type": "activescan.update"})
	}

	var flows []*store.Flow
	if in.FlowID > 0 {
		f, err := h.st.GetFlow(in.FlowID)
		if err != nil {
			release()
			httpErr(w, http.StatusNotFound, "flow not found")
			return
		}
		flows = []*store.Flow{f}
	} else {
		flows, _ = h.st.QueryFlowsFilter(store.FlowFilter{Limit: 500, ExcludeFlags: store.FlagRepeater | store.FlagIntruder | store.FlagActiveScan})
	}
	targets := h.asTargets(flows)
	if len(targets) == 0 {
		release()
		httpErr(w, http.StatusBadRequest, "no in-scope targets with injectable (query/body) parameters")
		return
	}
	h.as.mu.Lock()
	h.as.targets = len(targets)
	h.as.mu.Unlock()
	h.broadcast(map[string]any{"type": "activescan.update"})

	go h.asRun(ctx, targets, in.MaxRequests)
	h.asWriteState(w)
}

// asRun executes the scan across targets within a shared request budget.
func (h *Hub) asRun(ctx context.Context, targets []activescan.Target, budget int) {
	if budget <= 0 {
		budget = 2000
	}
	send := h.activeSender(ctx)
	for _, t := range targets {
		if ctx.Err() != nil || budget <= 0 {
			break
		}
		fs, n := activescan.Run(ctx, t, send, activescan.Options{MaxRequests: budget, Concurrency: 6})
		budget -= n
		h.as.mu.Lock()
		h.as.scanned++
		h.as.requests += n
		h.as.findings = append(h.as.findings, fs...)
		h.as.mu.Unlock()
		if len(fs) > 0 {
			h.st.SaveIssues(asIssues(fs))
			h.broadcast(map[string]any{"type": "scanner.update"})
		}
		h.broadcast(map[string]any{"type": "activescan.update"})
	}
	h.as.mu.Lock()
	h.as.running, h.as.cancel = false, nil
	h.as.mu.Unlock()
	h.broadcast(map[string]any{"type": "activescan.update"})
}

// asTargets keeps in-scope flows whose endpoint has injection points, deduped.
func (h *Hub) asTargets(flows []*store.Flow) []activescan.Target {
	seen := map[string]bool{}
	var out []activescan.Target
	for _, f := range flows {
		if !h.sc.InScope(f) {
			continue
		}
		// Never active-scan our own listeners (control plane or proxy) — they can be
		// captured if the UI is reached through the proxy (e.g. system proxy on).
		if h.isOwnListener(f) {
			continue
		}
		key := f.Method + " " + f.Host + f.Path
		if seen[key] {
			continue
		}
		t := activescan.Target{
			Method:  f.Method,
			URL:     analyzeURL(f),
			Headers: http.Header(f.ReqHeaders),
			Body:    string(h.bodyBytes(f.ReqBodyHash)),
		}
		if len(activescan.Points(t)) == 0 {
			continue
		}
		seen[key] = true
		out = append(out, t)
	}
	return out
}

// isOwnListener reports whether a flow targets one of our own loopback listeners
// (control plane or proxy), so the scanner never attacks itself. Compares with
// loopback normalization (127.x / ::1 / localhost) rather than a literal string.
func (h *Hub) isOwnListener(f *store.Flow) bool {
	if !isLoopbackHost(f.Host) {
		return false
	}
	for _, addr := range []string{h.SelfAddr, h.currentProxyAddr()} {
		if _, p, err := net.SplitHostPort(addr); err == nil {
			if n, e := strconv.Atoi(p); e == nil && n == f.Port {
				return true
			}
		}
	}
	return false
}

// activeSender bridges the engine to the real sender (records each probe as a
// flagged flow, applies session auth) and reads the response back for detection.
// The ctx is threaded into each send so the kill switch aborts in-flight probes.
func (h *Hub) activeSender(ctx context.Context) activescan.SendFunc {
	return func(t activescan.Target) activescan.Response {
		flow, err := h.snd.Send(sender.Request{
			Method:  t.Method,
			URL:     t.URL,
			Headers: map[string][]string(t.Headers),
			Body:    []byte(t.Body),
			Flags:   store.FlagActiveScan,
			Context: ctx,
		})
		if err != nil || flow == nil {
			return activescan.Response{}
		}
		return activescan.Response{
			FlowID:   flow.ID,
			Status:   flow.Status,
			Headers:  http.Header(flow.ResHeaders),
			Body:     string(h.bodyBytes(flow.ResBodyHash)),
			Duration: time.Duration(flow.DurationMs) * time.Millisecond,
		}
	}
}

func asIssues(fs []activescan.Finding) []store.Issue {
	out := make([]store.Issue, 0, len(fs))
	for _, f := range fs {
		out = append(out, store.Issue{
			FlowID:   f.FlowID,
			Severity: f.Severity,
			Title:    "[active] " + f.Title,
			Target:   f.Point.Kind + " param: " + f.Point.Name,
			Detail:   "Confirmed by active probing of the " + f.Point.Kind + " parameter `" + f.Point.Name + "`. The confirming request/response is flow #" + strconv.FormatInt(f.FlowID, 10) + ".",
			Evidence: f.Evidence,
			Fix:      f.Fix,
		})
	}
	return out
}
