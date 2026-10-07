package collrun

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Veyal/interseptor/internal/collexec"
	"github.com/Veyal/interseptor/internal/store"
)

// runState is the working state of one Run.
type runState struct {
	r          *Runner
	o          *Options
	rep        *Report
	coll       store.Collection
	items      []store.Item
	plan       []store.Item
	ov         *Overlay
	iterations int
	steps      int
	seq        int
	nextAt     time.Time // rate limiter
	markerSent bool
}

func (s *runState) maxSteps() int {
	n := s.o.MaxSteps
	if n <= 0 {
		n = StepsPerPlan * len(s.plan) * s.iterations
	}
	if n > HardMaxSteps {
		n = HardMaxSteps
	}
	return n
}

func (s *runState) maxVisits() int {
	if s.o.MaxVisits > 0 {
		return s.o.MaxVisits
	}
	return DefaultMaxVisit
}

func (s *runState) maxRows() int {
	if s.o.MaxRows > 0 {
		return s.o.MaxRows
	}
	return MaxRunRows
}

// stop records why the run ended (first reason wins).
func (s *runState) stop(status, reason string) {
	if s.rep.Status == StatusRunning {
		s.rep.Status, s.rep.StopReason = status, reason
	}
}

func (s *runState) loop(ctx context.Context) {
	maxSteps := s.maxSteps()
	for iter := 0; iter < s.iterations; iter++ {
		if !s.iteration(ctx, iter, maxSteps) {
			break
		}
	}
	if s.rep.Status == StatusRunning {
		s.rep.Status = StatusDone
	}
}

// iteration runs the plan once; false ends the whole run.
func (s *runState) iteration(ctx context.Context, iter, maxSteps int) bool {
	visits := map[string]int{}
	for i := 0; i < len(s.plan); {
		s.r.waitPaused(ctx)
		if ctx.Err() != nil {
			s.stop(StatusAborted, "run aborted")
			return false
		}
		if s.steps >= maxSteps {
			s.stop(StatusLoopGuard, fmt.Sprintf("step limit %d reached (setNextRequest loop guard)", maxSteps))
			return false
		}
		it := s.plan[i]
		visits[it.UID]++
		if visits[it.UID] > s.maxVisits() {
			s.stop(StatusLoopGuard, fmt.Sprintf("request %q visited more than %d times in one iteration", it.Name, s.maxVisits()))
			return false
		}
		s.steps++
		s.throttle(ctx)
		if ctx.Err() != nil {
			s.stop(StatusAborted, "run aborted")
			return false
		}
		res := s.execute(ctx, it, iter)
		item := s.record(it, iter, res)
		if s.shouldBail(item) {
			s.stop(StatusBailed, "bail: "+s.o.Bail)
			return false
		}
		next, cont := s.advance(res, i)
		if !cont {
			return false
		}
		i = next
		if i < len(s.plan) {
			s.r.sleep(ctx, s.o.Delay)
		}
	}
	return true
}

// advance applies setNextRequest. It returns the next plan index, or
// cont=false when the run must end.
func (s *runState) advance(res *collexec.StepResult, i int) (int, bool) {
	if res == nil || !res.Flow.HasNext {
		return i + 1, true
	}
	if res.Flow.NextRequest == "" {
		s.stop(StatusStopped, "setNextRequest(null)")
		return 0, false
	}
	j, ok := nextIndex(s.plan, res.Flow.NextRequest)
	if !ok {
		s.stop(StatusBadTarget, fmt.Sprintf("setNextRequest target %q is not in the run", s.r.Backend.Scrub(res.Flow.NextRequest)))
		return 0, false
	}
	return j, true
}

func (s *runState) shouldBail(it ItemResult) bool {
	switch s.o.Bail {
	case BailOnFailure:
		return it.Problem()
	case BailOnError:
		return it.Outcome == collexec.OutcomeError || it.Outcome == collexec.OutcomeBlocked
	}
	return false
}

// throttle enforces the requests-per-second limit.
func (s *runState) throttle(ctx context.Context) {
	if s.o.RPS <= 0 {
		return
	}
	interval := time.Duration(float64(time.Second) / s.o.RPS)
	now := s.r.now()
	if s.nextAt.After(now) {
		s.r.sleep(ctx, s.nextAt.Sub(now))
		s.nextAt = s.nextAt.Add(interval)
		return
	}
	s.nextAt = now.Add(interval)
}

// execute runs one step. A nil result means the step could not even start.
func (s *runState) execute(ctx context.Context, it store.Item, iter int) *collexec.StepResult {
	chain, err := collexec.ChainFromItems(s.coll, s.items, it.UID)
	if err != nil {
		return &collexec.StepResult{Outcome: collexec.OutcomeError, Error: "chain: " + err.Error()}
	}
	layers, local, err := s.r.Backend.Layers(chain, s.o.EnvUID)
	if err != nil {
		return &collexec.StepResult{Outcome: collexec.OutcomeError, Error: "variables: " + err.Error()}
	}
	layers = s.ov.Apply(layers)
	if row := s.o.Data.Row(iter); row != nil {
		layers = append(layers, dataLayer(row))
	}
	local = mergeLocal(local, s.o.Local)
	in := collexec.StepInput{
		Chain: chain, Layers: layers, Local: local, Source: s.o.Source, AI: s.o.AI,
		ScopePolicy: s.o.ScopePolicy, NoScripts: s.o.NoScripts, FailOnQuarantine: s.o.FailOnQuarantine,
		RunID: s.rep.RunUID, Iteration: iter, EnvUID: s.o.EnvUID, EnvPin: s.r.Backend.EnvPin(s.o.EnvUID), Identity: s.o.Identity,
	}
	res, err := s.r.Backend.Step(ctx, in, StepMeta{IterationCount: s.iterations})
	if err != nil || res == nil {
		msg := "step failed"
		if err != nil {
			msg += ": " + err.Error()
		}
		return &collexec.StepResult{Outcome: collexec.OutcomeError, Error: msg}
	}
	return res
}

func mergeLocal(base, over map[string]string) map[string]string {
	if len(over) == 0 {
		return base
	}
	out := make(map[string]string, len(base)+len(over))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range over {
		out[k] = v
	}
	return out
}

// record builds the masked item, applies persist=keep, stores the row and
// notifies listeners. Listeners run with no lock held.
func (s *runState) record(it store.Item, iter int, res *collexec.StepResult) ItemResult {
	s.ov.Record(res.VarChanges)
	if s.o.Persist == PersistKeep && len(res.VarChanges) > 0 {
		sk := s.r.Backend.Commit(s.coll, s.o.EnvUID, res.VarChanges)
		s.rep.Persist.Skipped = append(s.rep.Persist.Skipped, sk...)
		s.rep.Persist.Committed += len(res.VarChanges) - len(sk)
	}
	s.seq++
	item := s.buildItem(it, iter, res)
	s.rep.Totals.add(item)
	switch {
	case len(s.rep.Items) < s.maxRows():
		s.rep.Items = append(s.rep.Items, item)
		s.storeRow(item)
	case !s.markerSent:
		s.markerSent = true
		s.rep.Truncated = true
		s.storeMarker()
	}
	copyItem := item
	s.r.emit(s.o, Event{Type: "item", RunUID: s.rep.RunUID, Item: &copyItem, Totals: s.rep.Totals})
	return item
}

func (s *runState) buildItem(it store.Item, iter int, res *collexec.StepResult) ItemResult {
	sc := s.r.Backend.Scrub
	out := ItemResult{
		Seq: s.seq, Iteration: iter, ItemUID: it.UID, Name: sc(it.Name), Path: sc(FolderPath(s.items, it.UID)),
		Method: res.Method, URL: sc(res.URL), Outcome: res.Outcome, BlockReason: res.BlockReason,
		FlowID: res.FlowID, Error: sc(res.Error), Unresolved: res.Unresolved,
	}
	if out.Method == "" {
		out.Method = it.Method
	}
	if r := res.Response; r != nil {
		out.HTTPStatus, out.StatusText, out.DurationMs, out.Size = r.Status, r.StatusText, r.TimeMs, r.Size
		if out.Error == "" {
			out.Error = sc(r.Error)
		}
	}
	for _, t := range res.Tests {
		t.Name, t.Message, t.Subject, t.Expected, t.Actual = sc(t.Name), sc(t.Message), sc(t.Subject), sc(t.Expected), sc(t.Actual)
		out.Tests = append(out.Tests, t)
	}
	for _, c := range res.Console {
		c.Text = sc(c.Text)
		out.Console = append(out.Console, c)
	}
	out.VarChanges = s.r.views(res.VarChanges)
	for _, n := range res.Scripts {
		n.Reason = sc(n.Reason)
		out.Scripts = append(out.Scripts, n)
	}
	for _, w := range res.Warnings {
		out.Warnings = append(out.Warnings, sc(w))
	}
	if res.Flow.HasNext {
		n := sc(res.Flow.NextRequest)
		out.NextRequest = &n
	}
	return out
}

func (s *runState) storeRow(item ItemResult) {
	if s.r.Store == nil {
		return
	}
	raw, err := json.Marshal(item)
	if err != nil {
		return
	}
	_, _ = s.r.Store.AddRunResult(store.CollRunResult{RunUID: s.rep.RunUID, ItemUID: item.ItemUID, Iteration: item.Iteration,
		FlowID: item.FlowID, Status: string(item.Outcome), DurationMs: item.DurationMs, ResultJSON: string(raw)})
}

func (s *runState) storeMarker() {
	if s.r.Store == nil {
		return
	}
	_, _ = s.r.Store.AddRunResult(store.CollRunResult{RunUID: s.rep.RunUID, Status: "truncated",
		ResultJSON: fmt.Sprintf(`{"truncated":true,"maxRows":%d}`, s.maxRows())})
}
