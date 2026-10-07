// Package collrun is the collection runner: one library behind the UI runner
// view, the `interseptor run` CLI and MCP. It plans a run (collection, folder,
// picked items or failed-from-last-run), drives every request through the
// shared collexec pipeline, and produces one masked Report that the
// collreport package renders as CLI text, JSON, JUnit or HTML.
//
// Features: iterations and CSV/JSON data files, delay and rate limit, bail
// modes, setNextRequest flow control with a loop guard, a copy-on-write
// variable overlay with ask/keep/discard persistence, pause/resume/abort and
// crash-safe incremental result rows.
//
// Secrets never leave through a run: every string recorded passes through the
// Backend's single scrubber, variable writes are shown only as masked
// displays, and response bodies are never copied (rows link to flows).
package collrun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Veyal/interseptor/internal/collection"
	"github.com/Veyal/interseptor/internal/collexec"
	"github.com/Veyal/interseptor/internal/store"
)

// Bounds and defaults.
const (
	MaxIterations   = 10000
	HardMaxSteps    = 100000
	StepsPerPlan    = 10  // maxSteps = StepsPerPlan x plan x iterations (capped at HardMaxSteps)
	DefaultMaxVisit = 100 // per item, per iteration
	MaxRunRows      = 20000
	MaxDelay        = 10 * time.Minute
)

// Bail modes.
const (
	BailNone      = "none"
	BailOnFailure = "on-failure"
	BailOnError   = "on-error"
)

// Persist modes for script variable writes.
const (
	PersistAsk     = "ask"
	PersistKeep    = "keep"
	PersistDiscard = "discard"
)

// Options configure one run.
type Options struct {
	CollectionUID string
	FolderUID     string
	ItemUIDs      []string
	// FailedFromRun, when set, restricts the plan to the items of that stored
	// run that had a problem.
	FailedFromRun string
	EnvUID        string

	// Iterations is the iteration count. 0 means one per data row, or 1 when
	// there is no data. With data and N > rows, rows wrap around.
	Iterations int
	Data       *Dataset

	Delay time.Duration // between requests
	RPS   float64       // max requests per second (0 = unlimited)
	Bail  string

	// Persist is ask | keep | discard (empty means discard, the headless
	// default). In ask mode Decide chooses at the end; a nil Decide discards.
	Persist string
	Decide  func(pending []VarChangeView) bool

	NoScripts        bool
	FailOnQuarantine bool // headless: untrusted scripts stop the run before any send
	ScopePolicy      string
	Source           collexec.Source
	AI               bool
	Identity         string
	Local            map[string]string // highest-precedence overrides (CLI --env-var)

	MaxSteps  int // 0 = StepsPerPlan x plan x iterations
	MaxVisits int // per item per iteration; 0 = DefaultMaxVisit
	MaxRows   int // stored/reported rows; 0 = MaxRunRows

	OnEvent func(Event)
}

// Event is a progress notification for SSE or the CLI.
type Event struct {
	Type   string      `json:"type"` // start | item | paused | resumed | done
	RunUID string      `json:"runUid"`
	Item   *ItemResult `json:"item,omitempty"`
	Totals Totals      `json:"totals"`
	Status string      `json:"status,omitempty"`
	Total  int         `json:"plannedSteps,omitempty"`
}

// Runner executes one run at a time. Use a new Runner per run; Pause, Resume
// and Abort may be called from any goroutine.
type Runner struct {
	Backend Backend
	Store   RunStore // optional
	Clock   func() time.Time

	mu      sync.Mutex
	cancel  context.CancelFunc
	paused  bool
	resume  chan struct{}
	aborted bool
}

// New returns a Runner.
func New(b Backend, rs RunStore) *Runner { return &Runner{Backend: b, Store: rs, Clock: time.Now} }

// Pause holds the run before its next request.
func (r *Runner) Pause() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.paused {
		r.paused, r.resume = true, make(chan struct{})
	}
}

// Resume continues a paused run.
func (r *Runner) Resume() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.paused {
		r.paused = false
		close(r.resume)
	}
}

// Abort cancels the run, including an in-flight request.
func (r *Runner) Abort() {
	r.mu.Lock()
	r.aborted = true
	cancel := r.cancel
	if r.paused {
		r.paused = false
		close(r.resume)
	}
	r.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (r *Runner) waitPaused(ctx context.Context) {
	r.mu.Lock()
	if !r.paused {
		r.mu.Unlock()
		return
	}
	ch := r.resume
	r.mu.Unlock()
	select {
	case <-ch:
	case <-ctx.Done():
	}
}

func (r *Runner) now() time.Time {
	if r.Clock != nil {
		return r.Clock()
	}
	return time.Now()
}

// sleep waits d, honoring cancellation and pause.
func (r *Runner) sleep(ctx context.Context, d time.Duration) {
	if d <= 0 {
		return
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
	case <-ctx.Done():
	}
}

var errNoBackend = errors.New("collrun: no backend")

func (o *Options) validate() error {
	switch o.Bail {
	case "", BailNone, BailOnFailure, BailOnError:
	default:
		return fmt.Errorf("collrun: bail must be none, on-failure or on-error")
	}
	switch o.Persist {
	case "", PersistAsk, PersistKeep, PersistDiscard:
	default:
		return fmt.Errorf("collrun: persist must be ask, keep or discard")
	}
	if o.Iterations < 0 || o.Iterations > MaxIterations {
		return fmt.Errorf("collrun: iterations must be between 0 and %d", MaxIterations)
	}
	if o.Delay < 0 || o.Delay > MaxDelay {
		return fmt.Errorf("collrun: delay must be between 0 and %s", MaxDelay)
	}
	if o.RPS < 0 || o.RPS > 10000 {
		return fmt.Errorf("collrun: rps must be between 0 and 10000")
	}
	return nil
}

// iterationCount applies the iteration rules.
func (o *Options) iterationCount() int {
	switch {
	case o.Iterations > 0:
		return o.Iterations
	case o.Data.Len() > 0:
		return o.Data.Len()
	}
	return 1
}

// Run executes the plan and returns the masked report. The error is reserved
// for misuse (bad options, unknown collection, empty plan); runtime problems
// are in the report and its ExitCode.
func (r *Runner) Run(ctx context.Context, o Options) (*Report, error) {
	if r == nil || r.Backend == nil {
		return nil, errNoBackend
	}
	if err := o.validate(); err != nil {
		return nil, err
	}
	coll, items, err := r.Backend.Load(o.CollectionUID)
	if err != nil {
		return nil, err
	}
	pick := o.ItemUIDs
	if o.FailedFromRun != "" {
		if r.Store == nil {
			return nil, errors.New("collrun: failed-from-run needs a run store")
		}
		failed, err := FailedItemUIDs(r.Store, o.FailedFromRun)
		if err != nil {
			return nil, err
		}
		if len(failed) == 0 {
			return nil, errors.New("collrun: that run had no failed requests")
		}
		pick = failed
	}
	plan := PlanItems(items, o.FolderUID, pick)
	if len(plan) == 0 {
		return nil, errors.New("collrun: nothing to run: no requests selected")
	}
	if o.Source == "" {
		o.Source = collexec.SourceRunner
	}
	if o.ScopePolicy == "" {
		o.ScopePolicy = store.ScopePolicyBlock
	}
	if o.Persist == "" {
		o.Persist = PersistDiscard
	}
	if o.Bail == "" {
		o.Bail = BailNone
	}
	iterations := o.iterationCount()
	rep := &Report{
		CollectionUID: coll.UID, CollectionName: coll.Name, EnvUID: o.EnvUID, EnvName: r.Backend.EnvName(o.EnvUID),
		Source: string(o.Source), Status: StatusRunning, StartedMs: r.now().UnixMilli(), Iterations: iterations,
		Bail: o.Bail, ScopePolicy: o.ScopePolicy, Items: []ItemResult{}, Persist: PersistInfo{Mode: o.Persist},
	}
	if o.Data.Len() > 0 {
		rep.Data = &DataInfo{Format: o.Data.Format, Rows: o.Data.Len(), Columns: o.Data.Columns, Hash: o.Data.Hash}
	}

	if o.FailOnQuarantine && !o.NoScripts {
		if q := r.untrustedInPlan(coll, items, plan); len(q) > 0 {
			rep.Quarantined = q
			rep.Totals.Quarantined = len(q)
			rep.Status, rep.StopReason = StatusNotApproved, "scripts are not approved; trust them in the UI, pin their hashes or run with --no-scripts"
			rep.FinishedMs = r.now().UnixMilli()
			rep.RunUID = r.persistHeader(rep, nil)
			return rep, nil
		}
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	r.mu.Lock()
	r.cancel = cancel
	pre := r.aborted
	r.mu.Unlock()
	if pre {
		cancel()
	}

	rep.RunUID = r.persistHeader(rep, nil)
	r.emit(&o, Event{Type: "start", RunUID: rep.RunUID, Status: StatusRunning, Total: len(plan) * iterations})

	ov := NewOverlay()
	st := &runState{r: r, o: &o, rep: rep, coll: coll, items: items, plan: plan, ov: ov, iterations: iterations}
	st.loop(runCtx)

	r.finishPersist(&o, rep, coll, ov)
	rep.FinishedMs = r.now().UnixMilli()
	r.persistHeader(rep, rep)
	r.emit(&o, Event{Type: "done", RunUID: rep.RunUID, Totals: rep.Totals, Status: rep.Status})
	return rep, nil
}

func (r *Runner) emit(o *Options, e Event) {
	if o.OnEvent != nil {
		o.OnEvent(e)
	}
}

// persistHeader writes the run header (running at start, final at the end).
// It returns the run uid; a store failure never fails the run.
func (r *Runner) persistHeader(rep *Report, final *Report) string {
	if r.Store == nil {
		if rep.RunUID != "" {
			return rep.RunUID
		}
		return collection.NewID()
	}
	h := store.CollRun{UID: rep.RunUID, CollectionUID: rep.CollectionUID, EnvUID: rep.EnvUID, Source: rep.Source,
		Status: rep.Status, StartedTS: rep.StartedMs}
	if final != nil {
		h.FinishedTS = final.FinishedMs
		h.SummaryJSON = summaryJSON(final)
	}
	if rep.Status == StatusNotApproved {
		h.FinishedTS, h.SummaryJSON = rep.FinishedMs, summaryJSON(rep)
	}
	saved, err := r.Store.PutRun(h)
	if err != nil || saved == nil {
		if rep.RunUID != "" {
			return rep.RunUID
		}
		return collection.NewID()
	}
	return saved.UID
}

// RunSummary is the compact summary stored in ix_runs.summary_json.
type RunSummary struct {
	Status     string    `json:"status"`
	StopReason string    `json:"stopReason,omitempty"`
	Iterations int       `json:"iterations"`
	Totals     Totals    `json:"totals"`
	Data       *DataInfo `json:"data,omitempty"`
	Persist    string    `json:"persist"`
	ExitCode   int       `json:"exitCode"`
	Truncated  bool      `json:"truncated,omitempty"`
}

func summaryJSON(rep *Report) string {
	b, err := json.Marshal(RunSummary{Status: rep.Status, StopReason: rep.StopReason, Iterations: rep.Iterations,
		Totals: rep.Totals, Data: rep.Data, Persist: rep.Persist.Mode, ExitCode: rep.ExitCode(), Truncated: rep.Truncated})
	if err != nil {
		return ""
	}
	return string(b)
}

// finishPersist applies the persist policy to the writes of the run.
func (r *Runner) finishPersist(o *Options, rep *Report, coll store.Collection, ov *Overlay) {
	pending := ov.Pending()
	rep.Persist.Pending = r.views(pending)
	if o.Persist != PersistAsk || len(pending) == 0 {
		return
	}
	if o.Decide != nil && o.Decide(rep.Persist.Pending) {
		rep.Persist.Decision = PersistKeep
		rep.Persist.Skipped = append(rep.Persist.Skipped, r.Backend.Commit(coll, o.EnvUID, pending)...)
		rep.Persist.Committed = len(pending) - len(rep.Persist.Skipped)
		return
	}
	rep.Persist.Decision = PersistDiscard
}

func (r *Runner) views(changes []collexec.VarChange) []VarChangeView {
	var out []VarChangeView
	for _, c := range changes {
		out = append(out, VarChangeView{Scope: c.Scope, Key: c.Key, Display: r.displayValue(c), Unset: c.Unset, Secret: c.Secret})
	}
	return out
}

// displayValue is the only form a variable write is shown in. Secrets are
// never shown; other values are scrubbed together with their key, so a
// secret-looking name (token, password, api_key) masks its value even when the
// variable was not marked secret.
func (r *Runner) displayValue(c collexec.VarChange) string {
	if c.Secret {
		return "[secret]"
	}
	if c.Unset || c.Display == "" {
		return ""
	}
	prefix := c.Key + "="
	return strings.TrimPrefix(r.Backend.Scrub(prefix+c.Display), prefix)
}
