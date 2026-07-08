package autopwn

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Veyal/interseptor/internal/activescan/breaker"
	"github.com/Veyal/interseptor/internal/aiagent"
	"github.com/Veyal/interseptor/internal/store"
	"github.com/Veyal/interseptor/internal/verify"
)

const updateType = "autopwn.update"

// executor returns the ToolExecutor for agent phases: the injected one (tests) or
// a real one over the mcp.Server tool bus.
func (e *Engine) executor() aiagent.ToolExecutor {
	if e.d.ToolExecutor != nil {
		return e.d.ToolExecutor
	}
	return newToolExecutor(e.d.Tools)
}

// verifyDeps assembles the 4 gate collaborators. Sender/OOB come from the injected
// overrides (tests) or real adapters (production); Agent is the adversarial
// verifier over a fresh ToolCaller; Human wraps the injected AskHuman gate.
func (e *Engine) verifyDeps() verify.VerifyDeps {
	sender := e.d.VerifySender
	if sender == nil && e.d.Store != nil && e.d.Sender != nil {
		sender = newSenderAdapter(e.d.Store, e.d.Sender)
	}
	oobPoll := e.d.VerifyOOB
	if oobPoll == nil && e.d.OOB != nil {
		oobPoll = newOOBAdapter(e.d.OOB)
	}
	return verify.VerifyDeps{
		Sender: sender,
		OOB:    oobPoll,
		Agent: &agentVerifier{
			newCaller: e.d.NewToolCaller,
			exec:      e.executor(),
			budget:    e.d.VerifierBudget,
			clock:     e.d.Clock,
		},
		Human: &humanConfirmer{ask: e.d.AskHuman},
	}
}

// planBudget bounds the planning agent (steps/tool-calls + the run's token/wall
// budget). Sensible small defaults when unset.
func (e *Engine) planBudget(opts StartOpts) aiagent.Budget {
	b := e.d.PlanBudget
	if b.MaxSteps <= 0 {
		b.MaxSteps = 12
	}
	if b.MaxToolCalls <= 0 {
		b.MaxToolCalls = 24
	}
	if b.MaxTokens <= 0 {
		b.MaxTokens = opts.Budget.MaxTokens
	}
	if b.MaxWallMs <= 0 {
		b.MaxWallMs = opts.Budget.MaxWallMs
	}
	return b
}

// --- budget accounting (mu-guarded) ---

func (e *Engine) addRequests(n int) {
	if n <= 0 {
		return
	}
	e.mu.Lock()
	e.state.Consumed.Requests += n
	e.mu.Unlock()
}

func (e *Engine) addTokens(n int) {
	if n <= 0 {
		return
	}
	e.mu.Lock()
	e.state.Consumed.Tokens += n
	e.mu.Unlock()
}

// budgetExceeded reports whether any hard budget cap is now exceeded.
func (e *Engine) budgetExceeded() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	b := e.state.Budget
	if b.MaxRequests > 0 && e.state.Consumed.Requests >= b.MaxRequests {
		return true
	}
	if b.MaxTokens > 0 && e.state.Consumed.Tokens >= b.MaxTokens {
		return true
	}
	if b.MaxWallMs > 0 && e.d.Clock.Now().UnixMilli()-e.startMs >= b.MaxWallMs {
		return true
	}
	return false
}

// budgetOrCtxDone is the common hard-stop check before spending budget.
func (e *Engine) budgetOrCtxDone(ctx context.Context) bool {
	return ctx.Err() != nil || e.budgetExceeded()
}

func ctxOrBudgetErr(ctx context.Context) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return fmt.Errorf("budget exhausted")
}

// --- state updates (mu-guarded) + glass-box emit ---

func (e *Engine) setPhase(runID int64, phase string) {
	e.mu.Lock()
	e.state.Status = phase
	e.state.Phase = phase
	e.mu.Unlock()
	e.emit(store.Activity{Tool: "autopwn", Summary: "phase: " + phase, OK: true, Intent: "phase transition"}, phaseUpdate(phase, runID))
}

func (e *Engine) setCounts(fn func(*RunState)) {
	e.mu.Lock()
	fn(&e.state)
	e.mu.Unlock()
}

// emit records an Activity and broadcasts an SSE update — both OUTSIDE the lock,
// per the mutex discipline (notifiers may run concurrently / re-enter).
func (e *Engine) emit(a store.Activity, update any) {
	if e.d.RecordActivity != nil {
		if a.TS == 0 {
			a.TS = e.d.Clock.Now().UnixMilli()
		}
		e.d.RecordActivity(a)
	}
	if e.d.Broadcast != nil && update != nil {
		e.d.Broadcast(update)
	}
}

func phaseUpdate(phase string, runID int64) map[string]any {
	return map[string]any{"type": updateType, "runId": runID, "phase": phase, "status": phase}
}

func candidateUpdate(runID int64, c Candidate, proof verify.Proof, findingID int64) map[string]any {
	return map[string]any{
		"type": updateType, "runId": runID, "phase": StatusVerifying,
		"candidate": map[string]any{
			"vulnClass":  c.VulnClass,
			"severity":   c.Severity.String(),
			"target":     c.Target,
			"proven":     proof.Proven,
			"rejectedAt": proof.RejectedAt,
			"confidence": proof.Confidence,
			"findingId":  findingID,
		},
	}
}

// persistPlan writes the plan JSON to the run row.
func (e *Engine) persistPlan(runID int64, plan Plan) {
	if e.d.Store == nil {
		return
	}
	b, _ := json.Marshal(plan)
	pj := string(b)
	_ = e.d.Store.UpdatePentestRun(runID, nil, nil, &pj, nil, nil, nil)
}

// --- execution: drive the plan's tools via the in-process tool bus ---

// driveTools runs each plan step's primary tool via the tool bus, respecting a
// per-host circuit breaker (anomalous/blocking hosts are abandoned) and the
// request budget. Every call already lands in History (FlagAI) + Activity.
func (e *Engine) driveTools(ctx context.Context, plan Plan) {
	exec := e.executor()
	brk := breaker.New()
	for _, step := range plan.Steps {
		if e.budgetOrCtxDone(ctx) {
			return
		}
		tool := step.Tool
		if tool == "" && len(step.Tools) > 0 {
			tool = step.Tools[0]
		}
		if tool == "" {
			continue
		}
		host := hostOf(step.Target)
		key := breaker.Key(tool, host, step.Target)
		if skip, _ := brk.ShouldSkip(key); skip {
			continue
		}
		args := step.Args
		if args == nil {
			args = map[string]any{}
		}
		if step.FlowID != 0 {
			if _, ok := args["flowId"]; !ok {
				args["flowId"] = step.FlowID
			}
		}
		out, err := exec.Exec(ctx, aiagent.ToolCall{Name: tool, Args: args})
		e.addRequests(1)
		ok := err == nil
		brk.Record(key, tool, host, step.Target, statusFromToolResult(out), err != nil)
		e.emit(store.Activity{
			Tool: tool, Summary: fmt.Sprintf("drive %s @ %s", tool, step.Target), OK: ok,
			Result: firstLine(out), Intent: "execute plan step",
		}, map[string]any{"type": updateType, "phase": StatusExecuting, "tool": tool, "target": step.Target, "ok": ok})
	}
}

// hostOf extracts a host label from a target URL (best-effort, breaker keying).
func hostOf(target string) string {
	t := strings.TrimPrefix(strings.TrimPrefix(target, "https://"), "http://")
	if i := strings.IndexAny(t, "/?#"); i >= 0 {
		t = t[:i]
	}
	return t
}

// statusFromToolResult best-effort parses a "status" field from a JSON tool
// result so the breaker sees blocking statuses; 0 when absent.
func statusFromToolResult(out string) int {
	var m map[string]any
	if json.Unmarshal([]byte(out), &m) == nil {
		if v, ok := m["status"].(float64); ok {
			return int(v)
		}
	}
	return 0
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}
