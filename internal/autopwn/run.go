package autopwn

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Veyal/interseptor/internal/aiagent"
	"github.com/Veyal/interseptor/internal/store"
	"github.com/Veyal/interseptor/internal/verify"
)

// Start launches an autonomous run in a background goroutine and returns the new
// pentest_run id. It refuses (without spawning) when a run is already active or
// when no enabled include scope rule exists. The whole run shares one cancellable ctx derived
// from the caller's ctx = the kill switch (Stop cancels it).
func (e *Engine) Start(ctx context.Context, opts StartOpts) (int64, error) {
	if e.d.Store == nil {
		return 0, fmt.Errorf("autopwn: no store configured")
	}

	// Scope gate: refuse without an enabled include rule (snapshot rules for the run row).
	rules, err := e.d.Store.ListScopeRules()
	if err != nil {
		return 0, fmt.Errorf("autopwn: load scope: %w", err)
	}
	hasEnabledInclude := false
	for _, rule := range rules {
		if rule.Enabled && rule.Action == "include" {
			hasEnabledInclude = true
			break
		}
	}
	if !hasEnabledInclude {
		return 0, ErrNoScope
	}
	scopeJSON, _ := json.Marshal(rules)
	budgetJSON, _ := json.Marshal(opts.Budget)

	e.mu.Lock()
	if e.active {
		e.mu.Unlock()
		return 0, ErrRunActive
	}

	run := &store.PentestRun{
		Status: StatusPlanning,
		Scope:  string(scopeJSON),
		Budget: string(budgetJSON),
	}
	runID, err := e.d.Store.CreatePentestRun(run)
	if err != nil {
		e.mu.Unlock()
		return 0, fmt.Errorf("autopwn: create run: %w", err)
	}

	runCtx, cancel := context.WithCancel(ctx)
	e.active = true
	e.cancel = cancel
	e.startMs = e.d.Clock.Now().UnixMilli()
	e.state = RunState{
		RunID:  runID,
		Active: true,
		Status: StatusPlanning,
		Phase:  StatusPlanning,
		Budget: opts.Budget,
	}
	e.mu.Unlock()

	e.emit(store.Activity{Tool: "autopwn", Summary: fmt.Sprintf("run %d started", runID), OK: true, Intent: "begin autonomous pentest"}, phaseUpdate(StatusPlanning, runID))

	go e.run(runCtx, cancel, runID, string(scopeJSON), opts)
	return runID, nil
}

// run is the whole pipeline: plan → execute → verify → finish. It always
// finalizes the run row + engine state and releases the active slot, whatever
// path it exits — including a panic in any phase. finalization runs exactly once,
// via the recover-guarded closure below: without this, a panic would unwind run,
// leave active=true forever, and brick every future Start.
func (e *Engine) run(ctx context.Context, cancel context.CancelFunc, runID int64, scopeJSON string, opts StartOpts) {
	defer cancel()

	summary := runSummary{}
	finalStatus := StatusDone
	var runErr string
	finished := false

	// finalize releases the active slot + writes the terminal state exactly once.
	// Deferred so it fires on a normal return AND on a panic recovered below.
	finalize := func() {
		if finished {
			return
		}
		finished = true
		e.finish(runID, finalStatus, runErr, summary)
	}
	defer finalize()
	defer func() {
		if r := recover(); r != nil {
			finalStatus = StatusError
			runErr = fmt.Sprintf("panic: %v", r)
			finalize()
		}
	}()

	// --- Plan phase ---
	plan, err := e.planPhase(ctx, runID, opts)
	if err != nil {
		finalStatus, runErr = e.classifyExit(ctx, err)
		return
	}
	e.persistPlan(runID, plan)

	// Build the safety boundary from the run's scope SNAPSHOT (not live rules) +
	// the injected own-listener predicate. The verify phase re-checks every probe
	// URL against it before sending (the execute phase is already gated by the bus).
	guard := newBoundaryGuard(scopeJSON, e.d.IsOwnListener)

	// --- Execute phase ---
	e.setPhase(runID, StatusExecuting)
	candidates := e.executePhase(ctx, plan)
	summary.Candidates = len(candidates)
	e.setCounts(func(s *RunState) { s.Candidates = len(candidates) })

	// --- Verify phase ---
	if !e.budgetOrCtxDone(ctx) {
		e.setPhase(runID, StatusVerifying)
		e.verifyPhase(ctx, runID, guard, candidates, &summary)
	}

	if e.budgetExceeded() {
		finalStatus = StatusStopped
		runErr = "budget exhausted"
	} else if ctxErr := ctx.Err(); ctxErr != nil {
		finalStatus = StatusStopped
		runErr = ctxErr.Error()
	}
}

// classifyExit maps a phase error to a final status: a cancelled/budget-stopped
// ctx yields "stopped", any other error yields "error".
func (e *Engine) classifyExit(ctx context.Context, err error) (status, errText string) {
	if ctx.Err() != nil || e.budgetExceeded() {
		return StatusStopped, "stopped: " + err.Error()
	}
	return StatusError, err.Error()
}

// runSummary is the JSON rollup written to pentest_run.summary.
type runSummary struct {
	Candidates int `json:"candidates"`
	Verified   int `json:"verified"`
	Filed      int `json:"filed"`
	Rejected   int `json:"rejected"`
	Consumed   struct {
		Requests int   `json:"requests"`
		Tokens   int   `json:"tokens"`
		WallMs   int64 `json:"wallMs"`
	} `json:"consumed"`
}

// planPhase runs the recon/planning agent over read-only tools and parses its
// answer into a Plan. Budget/ctx are checked before spending the LLM turn.
func (e *Engine) planPhase(ctx context.Context, runID int64, opts StartOpts) (Plan, error) {
	if e.budgetOrCtxDone(ctx) {
		return Plan{}, ctxOrBudgetErr(ctx)
	}
	tc, err := e.d.NewToolCaller()
	if err != nil || tc == nil {
		return Plan{}, fmt.Errorf("autopwn: build planner: %w", errOrNil(err))
	}
	// Gather recon deterministically over the tool bus and inject it into the task,
	// so a model that will not (or cannot) call tools still plans from real context.
	digest := e.reconDigest(ctx)
	task := buildPlanTask(opts.TargetHint, digest)
	res, err := aiagent.Run(ctx, tc, e.executor(), planSystem, task, planTools, e.planBudget(opts), e.d.Clock)
	if err != nil {
		e.addTokens(res.Tokens)
		return Plan{}, err
	}
	e.addTokens(res.Tokens)
	plan := parsePlan(res.FinalText)
	if len(plan.Steps) == 0 {
		// A zero-step plan means nothing will be executed or verified. Surface it
		// loudly (not a silent "done"): usually the model returned no structured
		// steps, or there is no in-scope history to plan over.
		e.emit(store.Activity{
			Tool: "autopwn", OK: false,
			Summary: "planning produced 0 steps — no in-scope history, or the model returned no structured plan",
			Intent:  "planning diagnostic",
		}, map[string]any{"type": updateType, "runId": runID, "phase": StatusPlanning, "steps": 0})
		return plan, nil
	}
	e.emit(store.Activity{
		Tool: "autopwn", Summary: fmt.Sprintf("plan: %d step(s)", len(plan.Steps)), OK: true,
		Intent: "recon: history → prioritized attack plan",
	}, map[string]any{"type": updateType, "runId": runID, "phase": StatusPlanning, "steps": len(plan.Steps)})
	return plan, nil
}

// executePhase drives the plan's tools via the tool bus, then collects candidates.
// Each tool call already lands in History (FlagAI) + Activity; the engine tracks a
// per-host circuit breaker and honors the request budget as a hard stop.
func (e *Engine) executePhase(ctx context.Context, plan Plan) []Candidate {
	e.driveTools(ctx, plan)
	collect := e.d.CollectCandidates
	if collect == nil {
		collect = defaultCollectCandidates
	}
	if e.budgetOrCtxDone(ctx) {
		return nil
	}
	return collect(ctx, e, plan)
}

// verifyPhase runs the 4-gate verifier over each candidate; a candidate that
// clears every applicable gate becomes a filed Finding with its PoC flows and a
// finding_verification proof-record. Unproven candidates are NOT filed.
//
// Before any probe, each candidate is checked against the run's scope snapshot +
// own-listener predicate (the safety boundary): a candidate whose Target, Gate-1
// URLs, or Gate-3 probe URL is out of scope or an own listener is SKIPPED (never
// probed) and counted as rejected. A blind candidate whose callback URL could not
// be built (OOBBaseURL unset) is likewise skipped — an unconfirmable blind probe
// is a config error, surfaced loudly, not a silent Gate-3 rejection.
func (e *Engine) verifyPhase(ctx context.Context, runID int64, guard *boundaryGuard, candidates []Candidate, summary *runSummary) {
	deps := e.verifyDeps()
	for _, c := range candidates {
		if e.budgetOrCtxDone(ctx) {
			return
		}

		vc, token, skip := e.prepareCandidate(runID, c)
		if skip != "" {
			e.skipCandidate(runID, c, skip)
			summary.Rejected++
			e.setCounts(func(s *RunState) { s.Rejected++ })
			continue
		}

		// Safety boundary: re-check every URL the verifier would probe against the
		// run's scope snapshot + own-listener predicate BEFORE sending anything.
		if ok, reason := guard.candidateAllowed(candidateURLs(c, vc)); !ok {
			e.skipCandidate(runID, c, reason)
			summary.Rejected++
			e.setCounts(func(s *RunState) { s.Rejected++ })
			continue
		}

		proof := verify.Verify(ctx, vc, deps)
		// Count the verifier's re-sends against the request budget.
		e.addRequests(proof.ReproCount + boolInt(vc.Blind))

		if !proof.Proven {
			summary.Rejected++
			e.setCounts(func(s *RunState) { s.Rejected++ })
			e.emit(store.Activity{
				Tool: "autopwn", Summary: fmt.Sprintf("rejected %s @ %s (gate %s)", c.VulnClass, c.Target, proof.RejectedAt),
				OK: true, Intent: "verifier rejected candidate",
			}, candidateUpdate(runID, c, proof, 0))
			continue
		}

		summary.Verified++
		e.setCounts(func(s *RunState) { s.Verified++ })
		findingID, ferr := e.fileFinding(runID, c, proof, token)
		if ferr != nil {
			e.emit(store.Activity{
				Tool: "autopwn", Summary: fmt.Sprintf("verified but file failed: %s", ferr), OK: false,
				Intent: "file verified finding",
			}, candidateUpdate(runID, c, proof, 0))
			continue
		}
		summary.Filed++
		e.setCounts(func(s *RunState) { s.Filed++ })
		e.emit(store.Activity{
			Tool: "autopwn", Summary: fmt.Sprintf("filed %s finding: %s (conf %d)", c.Severity, c.VulnClass, proof.Confidence),
			OK: true, Intent: "verified-only filing",
		}, candidateUpdate(runID, c, proof, findingID))
	}
}

// prepareCandidate builds the verify.Candidate, minting + injecting a correlated
// OOB token for blind classes so Gate 3 has live ground truth. It returns the
// token (empty for non-blind), plus a non-empty skip reason when the candidate is
// inert and must NOT be probed:
//
//   - a blind candidate with no OOB catcher configured, or
//   - a blind candidate whose callback URL is empty (OOBBaseURL unset) — minting +
//     polling a token without injecting the URL would silently reject every blind
//     finding at Gate 3 on a mere config omission, so we surface it as a skip.
func (e *Engine) prepareCandidate(runID int64, c Candidate) (vc verify.Candidate, token, skip string) {
	if !c.Blind {
		return c.toVerifyCandidate(nil), "", ""
	}
	if e.d.OOB == nil {
		return c.toVerifyCandidate(nil), "", "blind candidate skipped: OOB catcher not configured"
	}
	token, url := e.d.OOB.MintCorrelatedURL(runID, c.VulnClass+"@"+c.Target, c.StepFlow, e.d.OOBBaseURL)
	if url == "" {
		return c.toVerifyCandidate(nil), token, "blind candidate skipped: OOBBaseURL not configured (cannot inject callback URL)"
	}
	probe := c.OOBProbe
	if c.OOBPlaceholder != "" {
		probe.URL = replaceAll(probe.URL, c.OOBPlaceholder, url)
		probe.Body = replaceBytes(probe.Body, c.OOBPlaceholder, url)
	}
	oobSpec := &verify.OOBSpec{Probe: probe, Token: token, Window: e.d.OOBWindow, Interval: e.d.OOBInterval}
	return c.toVerifyCandidate(oobSpec), token, ""
}

// skipCandidate records a loud Activity + SSE update for a candidate skipped by
// the safety boundary or an inert-config guard (never probed).
func (e *Engine) skipCandidate(runID int64, c Candidate, reason string) {
	e.emit(store.Activity{
		Tool: "autopwn", OK: false,
		Summary: fmt.Sprintf("candidate skipped: %s (%s @ %s)", reason, c.VulnClass, c.Target),
		Intent:  "safety boundary / config guard",
	}, map[string]any{
		"type": updateType, "runId": runID, "phase": StatusVerifying,
		"candidate": map[string]any{
			"vulnClass": c.VulnClass, "target": c.Target,
			"skipped": true, "reason": reason,
		},
	})
}

// fileFinding creates the verified Finding, attaches its PoC flows, and saves the
// machine proof-record. Returns the new finding id.
func (e *Engine) fileFinding(runID int64, c Candidate, proof verify.Proof, token string) (int64, error) {
	f := &store.Finding{
		Severity: c.Severity.String(),
		Status:   "verified",
		Source:   "ai",
		Title:    findingTitle(c),
		Target:   c.Target,
		Detail:   buildFindingNarrative(c, proof),
	}
	findingID, err := e.d.Store.CreateFinding(f)
	if err != nil {
		return 0, err
	}
	// Attach PoC flows: baseline, payload, and (blind) the OOB probe flow.
	attach := func(flowID int64, note string) {
		if flowID != 0 {
			_ = e.d.Store.AttachFlow(findingID, flowID, note, -1)
		}
	}
	attach(proof.BaselineFlow, "baseline / control")
	attach(proof.PayloadFlow, "confirming payload")

	v := proof.Verification(findingID, runID, c.VulnClass, token)
	if _, err := e.d.Store.SaveFindingVerification(&store.FindingVerification{
		FindingID:    v.FindingID,
		RunID:        v.RunID,
		VulnClass:    v.VulnClass,
		Gates:        v.Gates,
		ReproCount:   v.ReproCount,
		OOBToken:     v.OOBToken,
		BaselineFlow: v.BaselineFlow,
		PayloadFlow:  v.PayloadFlow,
		Confidence:   v.Confidence,
		TS:           e.d.Clock.Now().UnixMilli(),
	}); err != nil {
		return findingID, err
	}
	return findingID, nil
}

// finish finalizes the run: persist summary + status, flip engine state inactive,
// and broadcast a final autopwn.update.
func (e *Engine) finish(runID int64, status, errText string, summary runSummary) {
	e.mu.Lock()
	summary.Verified = e.state.Verified
	summary.Filed = e.state.Filed
	summary.Rejected = e.state.Rejected
	summary.Candidates = e.state.Candidates
	summary.Consumed.Requests = e.state.Consumed.Requests
	summary.Consumed.Tokens = e.state.Consumed.Tokens
	summary.Consumed.WallMs = e.d.Clock.Now().UnixMilli() - e.startMs
	e.state.Consumed.WallMs = summary.Consumed.WallMs
	e.state.Status = status
	e.state.Phase = status
	e.state.Active = false
	e.state.Error = errText
	e.active = false
	e.cancel = nil
	stateCopy := e.state
	e.mu.Unlock()

	summaryJSON, _ := json.Marshal(summary)
	st := status
	sj := string(summaryJSON)
	var ep *string
	if errText != "" {
		ep = &errText
	}
	if e.d.Store != nil {
		_ = e.d.Store.UpdatePentestRun(runID, &st, nil, nil, nil, &sj, ep)
	}

	e.emit(store.Activity{
		Tool: "autopwn", OK: status == StatusDone,
		Summary: fmt.Sprintf("run %d %s: %d filed / %d verified / %d rejected", runID, status, summary.Filed, summary.Verified, summary.Rejected),
		Intent:  "run complete",
	}, map[string]any{
		"type": updateType, "runId": runID, "status": status, "phase": status,
		"summary": summary, "state": stateCopy,
	})
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func errOrNil(err error) error {
	if err == nil {
		return fmt.Errorf("nil tool caller")
	}
	return err
}
