// Package autopwn is the autonomous-pentest ("Autopilot") run engine: the Phase-2
// orchestration core that reads captured in-scope history, plans and executes
// active security testing using Interseptor's own ~84 tools, verifies every
// candidate through the 4-gate verifier, and files ONLY machine-proven findings.
//
// The engine composes the Phase-0/1 building blocks (internal/mcp tool bus,
// internal/verify's 4 gates, internal/aiagent's budgeted loop, internal/sender,
// internal/oob) behind a small set of injected interfaces so the whole pipeline
// is unit-testable end-to-end WITHOUT a live LLM or network:
//
//   - the planning + adversarial-verifier agents run over an injected
//     aiagent.ToolCaller (a fake in tests scripts the turns) and an
//     aiagent.ToolExecutor (a fake in tests returns canned tool responses; in
//     production it is mcp.Server.Call);
//   - Gate 1 / Gate 3 drive an injected verify.Sender / verify.OOBPoller (real
//     adapters over sender/oob in production; scripted fakes in tests);
//   - Gate 4 calls an injected AskHuman gate (control provides one wrapping its
//     humaninput surface; a fake in tests).
//
// Safety rails (docs/AUTONOMOUS-PENTEST.md §8): the run refuses to start without
// scope rules, snapshots the scope it is bound to, enforces request/token/
// wall-clock budgets as hard kill switches, and Stop() cancels the run ctx
// immediately. Every phase transition, tool batch, candidate, and gate verdict is
// written to Activity (glass box) and broadcast over SSE.
package autopwn

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/Veyal/interseptor/internal/aiagent"
	"github.com/Veyal/interseptor/internal/mcp"
	"github.com/Veyal/interseptor/internal/oob"
	"github.com/Veyal/interseptor/internal/sender"
	"github.com/Veyal/interseptor/internal/store"
	"github.com/Veyal/interseptor/internal/verify"
)

// ErrRunActive is returned by Start when a run is already in flight.
var ErrRunActive = errors.New("autopwn: a run is already active")

// ErrNoScope is returned by Start when no scope rules exist — the run refuses to
// start (mirrors the bulk active-scan gate: never probe without a scope boundary).
var ErrNoScope = errors.New("autopwn: refusing to start with no scope rules; define scope first")

// Budget bounds one autonomous run. Zero/negative means that dimension is
// unbounded. Requests caps real HTTP sends (verifier re-sends + tool-driven
// probes tracked by the engine); Tokens is the advisory LLM ceiling; WallMs is a
// hard wall-clock kill.
type Budget struct {
	MaxRequests int   `json:"maxRequests"`
	MaxTokens   int   `json:"maxTokens"`
	MaxWallMs   int64 `json:"maxWallMs"`
}

// StartOpts configures one run.
type StartOpts struct {
	Budget     Budget
	TargetHint string // optional operator steer for the planning phase
}

// Deps injects everything the engine needs. In production, control wires the
// concrete infra; tests inject fakes at each seam. The two agent seams
// (NewToolCaller + ToolExecutor) are separate so a test can script model turns
// (fake ToolCaller) while returning canned tool results (fake ToolExecutor)
// without standing up an mcp.Server.
type Deps struct {
	Store  *store.Store
	Sender *sender.Sender
	OOB    *oob.Catcher
	Tools  *mcp.Server

	// NewToolCaller builds a fresh ToolCaller from the configured aiassist
	// provider (each agent phase gets its own caller/context). Required.
	NewToolCaller func() (aiagent.ToolCaller, error)

	// ToolExecutor runs tool calls for the planning + verifier agents. When nil,
	// the engine builds one over Tools (mcp.Server.Call). Tests inject a fake.
	ToolExecutor aiagent.ToolExecutor

	// VerifySender / VerifyOOB override the Gate-1 / Gate-3 collaborators. When
	// nil, the engine builds real adapters over Sender / OOB. Tests inject fakes
	// so gate outcomes are scripted deterministically.
	VerifySender verify.Sender
	VerifyOOB    verify.OOBPoller

	// CollectCandidates produces the run's candidates from the executed plan. When
	// nil, the engine uses its default collector (active-scan issues + agent
	// observations). Tests inject a fake to script the candidate set directly.
	CollectCandidates func(ctx context.Context, e *Engine, plan Plan) []Candidate

	Broadcast      func(v any)          // SSE fan-out (autopwn.update); may be nil
	RecordActivity func(store.Activity) // glass-box audit; may be nil
	AskHuman       func(ctx context.Context, message string, options []string) (string, error)

	// OOBBaseURL is the public OOB callback base the engine injects into blind
	// probes (built by control from the request host + persisted oob base).
	OOBBaseURL string

	// IsOwnListener reports whether a raw URL targets one of Interseptor's own
	// listeners (proxy / control). Control passes a predicate wrapping its
	// isOwnListener; the verify phase never probes a target this returns true for.
	// When nil, no URL is treated as an own listener (tests may pass a simple
	// loopback-port check or leave it nil).
	IsOwnListener func(rawURL string) bool

	// OOBWindow / OOBInterval bound Gate-3's callback poll loop. Zero uses the
	// verify defaults (30s window, 500ms interval). Tests shrink them so a
	// no-callback blind candidate rejects fast without a real 30s wait.
	OOBWindow   time.Duration
	OOBInterval time.Duration

	Clock aiagent.Clock // real in prod, fake in tests

	// AgentBudget bounds each planning/verifier agent run (steps/tool-calls). When
	// zero, sensible small defaults apply.
	PlanBudget     aiagent.Budget
	VerifierBudget aiagent.Budget
}

// Run status constants (mirror pentest_run.status).
const (
	StatusPlanning  = "planning"
	StatusExecuting = "executing"
	StatusVerifying = "verifying"
	StatusDone      = "done"
	StatusStopped   = "stopped"
	StatusError     = "error"
)

// RunState is a live snapshot of the active (or last) run.
type RunState struct {
	RunID    int64  `json:"runId"`
	Active   bool   `json:"active"`
	Status   string `json:"status"`
	Phase    string `json:"phase"`
	Budget   Budget `json:"budget"`
	Consumed struct {
		Requests int   `json:"requests"`
		Tokens   int   `json:"tokens"`
		WallMs   int64 `json:"wallMs"`
	} `json:"consumed"`
	Candidates int    `json:"candidates"`
	Verified   int    `json:"verified"`
	Filed      int    `json:"filed"`
	Rejected   int    `json:"rejected"`
	Error      string `json:"error,omitempty"`
}

// Engine drives autonomous runs. At most one run is active at a time; its state
// is guarded by mu. Broadcast / RecordActivity are always fired OUTSIDE the lock.
type Engine struct {
	d Deps

	mu      sync.Mutex
	active  bool
	state   RunState
	cancel  context.CancelFunc
	startMs int64
}

// New constructs an Engine. Clock defaults to the real clock when unset.
func New(d Deps) *Engine {
	if d.Clock == nil {
		d.Clock = aiagent.RealClock{}
	}
	return &Engine{d: d}
}

// State returns a live snapshot of the current/last run.
func (e *Engine) State() RunState {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.state
}

// ListRuns returns the persisted run history newest-first.
func (e *Engine) ListRuns() ([]store.PentestRun, error) {
	if e.d.Store == nil {
		return nil, errors.New("autopwn: no store configured")
	}
	return e.d.Store.ListPentestRuns()
}

// Stop cancels the active run's context immediately (the kill switch). It is a
// no-op when no run is active.
func (e *Engine) Stop() {
	e.mu.Lock()
	cancel := e.cancel
	e.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}
