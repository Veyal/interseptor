package collrun

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/collexec"
	"github.com/Veyal/interseptor/internal/store"
	"github.com/Veyal/interseptor/internal/varstore"
)

func TestRunSequentialAndTotals(t *testing.T) {
	f := newFake("login", "list", "logout")
	rep := mustRun(t, f, nil, Options{})
	if names(rep) != "login,list,logout" || rep.Status != StatusDone {
		t.Fatalf("items=%s status=%s", names(rep), rep.Status)
	}
	if rep.Totals.Requests != 3 || rep.Totals.Sent != 3 || rep.Totals.Pass != 3 || rep.ExitCode() != ExitPass {
		t.Fatalf("totals %+v exit %d", rep.Totals, rep.ExitCode())
	}
	if rep.CollectionName != "Demo API" || rep.EnvName != "staging" || rep.Iterations != 1 || rep.Source != "runner" {
		t.Fatalf("header %+v", rep)
	}
	if f.calls[0].Policy != store.ScopePolicyBlock {
		t.Fatalf("runs must default to scope policy block, got %q", f.calls[0].Policy)
	}
}

func TestRunDefaultsAreHeadlessSafe(t *testing.T) {
	rep := mustRun(t, newFake("a"), nil, Options{})
	if rep.Persist.Mode != PersistDiscard || rep.Bail != BailNone {
		t.Fatalf("defaults: %+v bail=%s", rep.Persist, rep.Bail)
	}
}

func TestRunIterationsWithDataAndWrap(t *testing.T) {
	f := newFake("a", "b")
	ds := &Dataset{Format: "csv", Columns: []string{"user"}, Hash: "h", Rows: []map[string]string{{"user": "alice@example.com"}, {"user": "bob@example.com"}}}
	rep := mustRun(t, f, nil, Options{Data: ds})
	if rep.Iterations != 2 || len(rep.Items) != 4 || rep.Data == nil || rep.Data.Rows != 2 || rep.Data.Hash != "h" {
		t.Fatalf("iterations %d items %d data %+v", rep.Iterations, len(rep.Items), rep.Data)
	}
	data := func(c stepCall) string {
		v, _, _ := varstore.NewStack(c.Layers...).Lookup("user")
		return v.Value
	}
	if data(f.calls[0]) != "alice@example.com" || data(f.calls[2]) != "bob@example.com" || f.calls[2].Iter != 1 {
		t.Fatalf("data rows not bound per iteration: %q %q", data(f.calls[0]), data(f.calls[2]))
	}
	if f.calls[0].Meta.IterationCount != 2 {
		t.Fatalf("iteration count meta: %+v", f.calls[0].Meta)
	}

	f2 := newFake("a")
	rep = mustRun(t, f2, nil, Options{Data: ds, Iterations: 5})
	if len(rep.Items) != 5 || data(f2.calls[2]) != "alice@example.com" || data(f2.calls[4]) != "alice@example.com" {
		t.Fatalf("rows must wrap with -n > rows: %d items", len(rep.Items))
	}
}

func TestRunIterationsWithoutData(t *testing.T) {
	rep := mustRun(t, newFake("a"), nil, Options{Iterations: 3})
	if len(rep.Items) != 3 || rep.Iterations != 3 {
		t.Fatalf("items %d", len(rep.Items))
	}
}

func TestRunValidation(t *testing.T) {
	r := New(newFake("a"), nil)
	for _, o := range []Options{
		{CollectionUID: "c1", Bail: "sometimes"},
		{CollectionUID: "c1", Persist: "maybe"},
		{CollectionUID: "c1", Iterations: -1},
		{CollectionUID: "c1", Iterations: MaxIterations + 1},
		{CollectionUID: "c1", Delay: -time.Second},
		{CollectionUID: "c1", Delay: 24 * time.Hour},
		{CollectionUID: "c1", RPS: -1},
		{CollectionUID: "c1", ItemUIDs: []string{"nope"}},
	} {
		if _, err := r.Run(context.Background(), o); err == nil {
			t.Errorf("expected error for %+v", o)
		}
	}
}

// ---- flow control and loop guard ----

func TestSetNextRequestJumpsAndSkips(t *testing.T) {
	f := newFake("a", "b", "c")
	f.step = func(in collexec.StepInput, _ int) *collexec.StepResult {
		if in.Chain.Item.Name == "a" {
			r := ok200("a")
			r.Flow = collexec.FlowControl{HasNext: true, NextRequest: "c"}
			return r
		}
		return nil
	}
	rep := mustRun(t, f, nil, Options{})
	if names(rep) != "a,c" || rep.Status != StatusDone {
		t.Fatalf("got %s status %s", names(rep), rep.Status)
	}
	if rep.Items[0].NextRequest == nil || *rep.Items[0].NextRequest != "c" {
		t.Fatalf("next request not recorded: %+v", rep.Items[0].NextRequest)
	}
}

func TestSetNextRequestNullStopsRun(t *testing.T) {
	f := newFake("a", "b")
	f.step = func(in collexec.StepInput, _ int) *collexec.StepResult {
		r := ok200("a")
		r.Flow = collexec.FlowControl{HasNext: true}
		return r
	}
	rep := mustRun(t, f, nil, Options{Iterations: 3})
	if names(rep) != "a" || rep.Status != StatusStopped {
		t.Fatalf("got %s status %s", names(rep), rep.Status)
	}
	if rep.ExitCode() != ExitPass {
		t.Fatalf("an intentional stop is not an error: %d", rep.ExitCode())
	}
}

func TestSetNextRequestUnknownTargetEndsRunLoudly(t *testing.T) {
	f := newFake("a", "b")
	f.step = func(in collexec.StepInput, _ int) *collexec.StepResult {
		r := ok200("a")
		r.Flow = collexec.FlowControl{HasNext: true, NextRequest: "does not exist"}
		return r
	}
	rep := mustRun(t, f, nil, Options{})
	if rep.Status != StatusBadTarget || rep.ExitCode() != ExitRuntime || !strings.Contains(rep.StopReason, "does not exist") {
		t.Fatalf("status %s reason %q exit %d", rep.Status, rep.StopReason, rep.ExitCode())
	}
}

func TestLoopGuardStepLimit(t *testing.T) {
	f := newFake("a", "b")
	// a -> b -> a -> b ... forever, visiting each item once per cycle.
	f.step = func(in collexec.StepInput, _ int) *collexec.StepResult {
		r := ok200(in.Chain.Item.Name)
		if in.Chain.Item.Name == "b" {
			r.Flow = collexec.FlowControl{HasNext: true, NextRequest: "a"}
		}
		return r
	}
	rep := mustRun(t, f, nil, Options{MaxVisits: 1 << 20})
	if rep.Status != StatusLoopGuard {
		t.Fatalf("status %s", rep.Status)
	}
	want := StepsPerPlan * 2 * 1
	if len(f.calls) != want {
		t.Fatalf("loop guard must stop at 10 x plan x iterations = %d steps, ran %d", want, len(f.calls))
	}
	if rep.ExitCode() != ExitRuntime {
		t.Fatalf("exit %d", rep.ExitCode())
	}
}

func TestLoopGuardVisitCap(t *testing.T) {
	f := newFake("a")
	f.step = func(in collexec.StepInput, _ int) *collexec.StepResult {
		r := ok200("a")
		r.Flow = collexec.FlowControl{HasNext: true, NextRequest: "a"} // self loop
		return r
	}
	rep := mustRun(t, f, nil, Options{MaxVisits: 3, MaxSteps: 1000})
	if rep.Status != StatusLoopGuard || len(f.calls) != 3 || !strings.Contains(rep.StopReason, "more than 3 times") {
		t.Fatalf("status %s calls %d reason %q", rep.Status, len(f.calls), rep.StopReason)
	}
}

func TestLoopGuardHardCeiling(t *testing.T) {
	f := newFake("a")
	r := &runState{o: &Options{MaxSteps: 5_000_000}, plan: f.items, iterations: 1}
	if got := r.maxSteps(); got != HardMaxSteps {
		t.Fatalf("maxSteps %d must be capped at %d", got, HardMaxSteps)
	}
	r = &runState{o: &Options{}, plan: make([]store.Item, 50), iterations: 5000}
	if got := r.maxSteps(); got != HardMaxSteps {
		t.Fatalf("derived maxSteps %d must be capped at %d", got, HardMaxSteps)
	}
}

func TestVisitsResetEachIteration(t *testing.T) {
	f := newFake("a")
	rep := mustRun(t, f, nil, Options{Iterations: 5, MaxVisits: 1})
	if rep.Status != StatusDone || len(rep.Items) != 5 {
		t.Fatalf("visit cap is per iteration: status %s items %d", rep.Status, len(rep.Items))
	}
}

// ---- bail ----

func failing(name string) *collexec.StepResult {
	r := ok200(name)
	r.Tests = []collexec.TestResult{{Name: "t", Status: collexec.TestFail, Message: "expected 200 got 500"}}
	return r
}

func TestBailModes(t *testing.T) {
	cases := []struct {
		bail   string
		result func(string) *collexec.StepResult
		want   string
		status string
	}{
		{BailOnFailure, failing, "a", StatusBailed},
		{BailOnError, failing, "a,b,c", StatusDone},
		{BailOnError, func(n string) *collexec.StepResult {
			return &collexec.StepResult{Outcome: collexec.OutcomeError, Error: "dial tcp: refused"}
		}, "a", StatusBailed},
		{BailNone, failing, "a,b,c", StatusDone},
	}
	for _, c := range cases {
		f := newFake("a", "b", "c")
		f.step = func(in collexec.StepInput, _ int) *collexec.StepResult { return c.result(in.Chain.Item.Name) }
		rep := mustRun(t, f, nil, Options{Bail: c.bail})
		if names(rep) != c.want || rep.Status != c.status {
			t.Errorf("bail=%s: ran %s status %s, want %s/%s", c.bail, names(rep), rep.Status, c.want, c.status)
		}
	}
}

func TestBailStopsAllIterations(t *testing.T) {
	f := newFake("a")
	f.step = func(in collexec.StepInput, _ int) *collexec.StepResult { return failing("a") }
	rep := mustRun(t, f, nil, Options{Bail: BailOnFailure, Iterations: 5})
	if len(rep.Items) != 1 {
		t.Fatalf("bail must end the run, ran %d", len(rep.Items))
	}
}

// ---- persist modes ----

func writer(in collexec.StepInput, _ int) *collexec.StepResult {
	r := ok200(in.Chain.Item.Name)
	if in.Chain.Item.Name == "login" {
		r.VarChanges = []collexec.VarChange{{Scope: "environment", Key: "token", Value: "tok-12345678", Display: "tok-12345678"}}
	}
	return r
}

func tokenSeen(c stepCall) string {
	v, _, _ := varstore.NewStack(c.Layers...).Lookup("token")
	return v.Value
}

func TestPersistDiscardIsExactButChainsWithinRun(t *testing.T) {
	f := newFake("login", "use")
	f.baseVars = map[string]string{"token": "stored-old"}
	f.step = writer
	rep := mustRun(t, f, nil, Options{Persist: PersistDiscard})
	if tokenSeen(f.calls[0]) != "stored-old" || tokenSeen(f.calls[1]) != "tok-12345678" {
		t.Fatalf("token chain broken: %q then %q", tokenSeen(f.calls[0]), tokenSeen(f.calls[1]))
	}
	if len(f.commits) != 0 {
		t.Fatalf("discard must never commit: %v", f.commits)
	}
	if f.baseVars["token"] != "stored-old" || rep.Persist.Committed != 0 || len(rep.Persist.Pending) != 1 {
		t.Fatalf("persist info %+v", rep.Persist)
	}
}

func TestPersistKeepCommitsEachStep(t *testing.T) {
	f := newFake("login", "use")
	f.step = writer
	rep := mustRun(t, f, nil, Options{Persist: PersistKeep})
	if len(f.commits) != 1 || f.commits[0][0].Key != "token" || rep.Persist.Committed != 1 {
		t.Fatalf("commits %v persist %+v", f.commits, rep.Persist)
	}
}

func TestPersistAskDecides(t *testing.T) {
	for _, yes := range []bool{true, false} {
		f := newFake("login", "use")
		f.step = writer
		var asked []VarChangeView
		rep := mustRun(t, f, nil, Options{Persist: PersistAsk, Decide: func(p []VarChangeView) bool { asked = p; return yes }})
		if len(asked) != 1 || asked[0].Key != "token" {
			t.Fatalf("decide not asked with pending writes: %+v", asked)
		}
		if yes && (len(f.commits) != 1 || rep.Persist.Decision != PersistKeep) {
			t.Fatalf("yes: commits %v decision %q", f.commits, rep.Persist.Decision)
		}
		if !yes && (len(f.commits) != 0 || rep.Persist.Decision != PersistDiscard) {
			t.Fatalf("no: commits %v decision %q", f.commits, rep.Persist.Decision)
		}
	}
}

func TestPersistAskWithoutDeciderDiscards(t *testing.T) {
	f := newFake("login")
	f.step = writer
	rep := mustRun(t, f, nil, Options{Persist: PersistAsk})
	if len(f.commits) != 0 || rep.Persist.Decision != PersistDiscard {
		t.Fatalf("headless ask must discard: %v %q", f.commits, rep.Persist.Decision)
	}
}

func TestPersistAskNothingToAskNoDecision(t *testing.T) {
	asked := false
	rep := mustRun(t, newFake("a"), nil, Options{Persist: PersistAsk, Decide: func([]VarChangeView) bool { asked = true; return true }})
	if asked || rep.Persist.Decision != "" {
		t.Fatal("must not ask when nothing was written")
	}
}

func TestLocalOverridesAndData(t *testing.T) {
	f := newFake("a")
	mustRun(t, f, nil, Options{Local: map[string]string{"host": "stg.example.com"}})
	if f.calls[0].Local["host"] != "stg.example.com" {
		t.Fatalf("local overrides not passed: %v", f.calls[0].Local)
	}
}

func TestOptionsReachPipeline(t *testing.T) {
	f := newFake("a")
	mustRun(t, f, nil, Options{NoScripts: true, ScopePolicy: store.ScopePolicyWarn})
	if !f.calls[0].NoScr || f.calls[0].Policy != store.ScopePolicyWarn {
		t.Fatalf("%+v", f.calls[0])
	}
}

// ---- selection ----

func TestPlanFolderPickAndOrder(t *testing.T) {
	f := newFake("a", "b", "c")
	f.items = append(f.items, store.Item{UID: "f1", CollectionUID: "c1", Kind: "folder", Name: "Auth", Rank: "0"})
	f.items[1].ParentUID = "f1"
	f.items[2].ParentUID = "f1"
	rep := mustRun(t, f, nil, Options{FolderUID: "f1"})
	if names(rep) != "b,c" || rep.Items[0].Path != "Auth" {
		t.Fatalf("folder plan: %s path %q", names(rep), rep.Items[0].Path)
	}
	// Picked items run in tree order, not click order.
	rep = mustRun(t, f, nil, Options{ItemUIDs: []string{"i-a", "i-c"}})
	if names(rep) != "c,a" { // c lives in the first folder
		t.Fatalf("pick order = rank: %s", names(rep))
	}
}

// ---- pause / resume / abort ----

func TestAbortCancelsInFlightRequest(t *testing.T) {
	f := newFake("a", "b")
	started := make(chan struct{})
	f.step = func(in collexec.StepInput, n int) *collexec.StepResult {
		if n == 1 {
			close(started)
			time.Sleep(50 * time.Millisecond)
		}
		return nil
	}
	r := New(f, nil)
	done := make(chan *Report)
	go func() {
		rep, _ := r.Run(context.Background(), Options{CollectionUID: "c1", Delay: time.Second})
		done <- rep
	}()
	<-started
	r.Abort()
	select {
	case rep := <-done:
		if rep.Status != StatusAborted || len(rep.Items) != 1 || rep.ExitCode() != ExitRuntime {
			t.Fatalf("status %s items %d", rep.Status, len(rep.Items))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("abort did not stop the run (delay not interrupted)")
	}
}

func TestPauseResume(t *testing.T) {
	f := newFake("a", "b", "c")
	paused := make(chan struct{})
	r := New(f, nil)
	f.step = func(in collexec.StepInput, n int) *collexec.StepResult {
		if n == 1 {
			r.Pause()
			close(paused)
		}
		return nil
	}
	done := make(chan *Report)
	go func() {
		rep, _ := r.Run(context.Background(), Options{CollectionUID: "c1"})
		done <- rep
	}()
	<-paused
	time.Sleep(60 * time.Millisecond)
	f.mu.Lock()
	n := len(f.calls)
	f.mu.Unlock()
	if n != 1 {
		t.Fatalf("run advanced while paused: %d calls", n)
	}
	r.Resume()
	select {
	case rep := <-done:
		if len(rep.Items) != 3 || rep.Status != StatusDone {
			t.Fatalf("items %d status %s", len(rep.Items), rep.Status)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("resume did not continue the run")
	}
}

func TestAbortWhilePausedReleasesRun(t *testing.T) {
	f := newFake("a", "b")
	r := New(f, nil)
	r.Pause()
	done := make(chan *Report)
	go func() {
		rep, _ := r.Run(context.Background(), Options{CollectionUID: "c1"})
		done <- rep
	}()
	time.Sleep(30 * time.Millisecond)
	r.Abort()
	select {
	case rep := <-done:
		if rep.Status != StatusAborted {
			t.Fatalf("status %s", rep.Status)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("abort while paused hung")
	}
}

func TestParentContextCancelAborts(t *testing.T) {
	f := newFake("a", "b")
	ctx, cancel := context.WithCancel(context.Background())
	f.step = func(in collexec.StepInput, n int) *collexec.StepResult { cancel(); return nil }
	rep, err := New(f, nil).Run(ctx, Options{CollectionUID: "c1"})
	if err != nil || rep.Status != StatusAborted {
		t.Fatalf("err %v status %s", err, rep.Status)
	}
}

func TestRateLimitSpacesRequests(t *testing.T) {
	f := newFake("a", "b", "c")
	start := time.Now()
	mustRun(t, f, nil, Options{RPS: 20}) // 50ms apart
	if el := time.Since(start); el < 90*time.Millisecond {
		t.Fatalf("3 requests at 20 rps took only %s", el)
	}
}

func TestDelayBetweenRequests(t *testing.T) {
	f := newFake("a", "b")
	start := time.Now()
	mustRun(t, f, nil, Options{Delay: 60 * time.Millisecond})
	if el := time.Since(start); el < 55*time.Millisecond {
		t.Fatalf("delay not applied: %s", el)
	}
}

// ---- events, truncation, errors ----

func TestEventsAreEmitted(t *testing.T) {
	f := newFake("a", "b")
	var types []string
	var lastTotals Totals
	mustRun(t, f, nil, Options{OnEvent: func(e Event) { types = append(types, e.Type); lastTotals = e.Totals }})
	if strings.Join(types, ",") != "start,item,item,done" || lastTotals.Requests != 2 {
		t.Fatalf("events %v totals %+v", types, lastTotals)
	}
}

func TestTruncationMarker(t *testing.T) {
	f := newFake("a")
	rep := mustRun(t, f, nil, Options{Iterations: 10, MaxRows: 4})
	if !rep.Truncated || len(rep.Items) != 4 || rep.Totals.Requests != 10 {
		t.Fatalf("truncated=%v items=%d requests=%d", rep.Truncated, len(rep.Items), rep.Totals.Requests)
	}
}

func TestBackendStepErrorBecomesErrorItem(t *testing.T) {
	f := newFake("a")
	f.step = func(collexec.StepInput, int) *collexec.StepResult {
		return &collexec.StepResult{Outcome: collexec.OutcomeError, Error: "boom"}
	}
	rep := mustRun(t, f, nil, Options{})
	if rep.Totals.Errors != 1 || rep.ExitCode() != ExitRuntime {
		t.Fatalf("%+v exit %d", rep.Totals, rep.ExitCode())
	}
}

// ---- quarantine preflight ----

func quarantinedFake() *fakeBackend {
	f := newFake("a", "b")
	f.items[0].Events = json.RawMessage(`[{"listen":"test","script":{"exec":["pm.test('x',()=>{})"]}}]`)
	return f
}

func TestFailOnQuarantineStopsBeforeAnySend(t *testing.T) {
	f := quarantinedFake()
	rep := mustRun(t, f, nil, Options{FailOnQuarantine: true})
	if len(f.calls) != 0 {
		t.Fatalf("no request may be sent when scripts are unapproved, sent %d", len(f.calls))
	}
	if rep.Status != StatusNotApproved || rep.ExitCode() != ExitNotApproved || len(rep.Quarantined) != 1 || len(rep.Quarantined[0].Hash) != 64 {
		t.Fatalf("status %s exit %d quarantined %+v", rep.Status, rep.ExitCode(), rep.Quarantined)
	}
}

func TestFailOnQuarantinePassesWhenTrustedOrDisabled(t *testing.T) {
	f := quarantinedFake()
	f.trusted[CollectionScripts(f.coll, f.items)[0].Hash] = true
	rep := mustRun(t, f, nil, Options{FailOnQuarantine: true})
	if rep.Status != StatusDone || len(f.calls) != 2 {
		t.Fatalf("trusted script must run: %s %d", rep.Status, len(f.calls))
	}
	f2 := quarantinedFake()
	rep = mustRun(t, f2, nil, Options{FailOnQuarantine: true, NoScripts: true})
	if rep.Status != StatusDone || len(f2.calls) != 2 {
		t.Fatalf("--no-scripts must not trip the gate: %s", rep.Status)
	}
}

func TestDisabledAndEmptyScriptsAreNotQuarantined(t *testing.T) {
	f := newFake("a")
	f.items[0].Events = json.RawMessage(`[{"listen":"test","disabled":true,"script":{"exec":["x()"]}},{"listen":"test","script":{"exec":["  "]}}]`)
	rep := mustRun(t, f, nil, Options{FailOnQuarantine: true})
	if rep.Status != StatusDone {
		t.Fatalf("status %s", rep.Status)
	}
}

// ---- exit codes ----

func TestExitCodes(t *testing.T) {
	cases := []struct {
		name string
		rep  Report
		want int
	}{
		{"pass", Report{Status: StatusDone, Totals: Totals{Pass: 3}}, ExitPass},
		{"test failure", Report{Status: StatusDone, Totals: Totals{Fail: 1}}, ExitTestFail},
		{"transport error", Report{Status: StatusDone, Totals: Totals{Errors: 1, Fail: 1}}, ExitRuntime},
		{"unresolved", Report{Status: StatusDone, Totals: Totals{Unresolved: 1}}, ExitRuntime},
		{"script error test", Report{Status: StatusDone, Totals: Totals{TestError: 1}}, ExitRuntime},
		{"unsupported api is never a pass", Report{Status: StatusDone, Totals: Totals{Unsupported: 1}}, ExitRuntime},
		{"loop guard", Report{Status: StatusLoopGuard}, ExitRuntime},
		{"scope blocked beats runtime", Report{Status: StatusDone, Totals: Totals{ScopeBlocks: 1, Errors: 2}}, ExitScope},
		{"not approved beats scope", Report{Status: StatusNotApproved, Totals: Totals{ScopeBlocks: 1}}, ExitNotApproved},
		{"bailed on failure", Report{Status: StatusBailed, Totals: Totals{Fail: 1}}, ExitTestFail},
		{"intentional stop", Report{Status: StatusStopped, Totals: Totals{Pass: 1}}, ExitPass},
	}
	for _, c := range cases {
		if got := c.rep.ExitCode(); got != c.want {
			t.Errorf("%s: exit %d, want %d", c.name, got, c.want)
		}
	}
}

func TestBlockClassification(t *testing.T) {
	f := newFake("a", "b", "c")
	f.step = func(in collexec.StepInput, _ int) *collexec.StepResult {
		switch in.Chain.Item.Name {
		case "a":
			return &collexec.StepResult{Outcome: collexec.OutcomeBlocked, BlockReason: collexec.BlockScope, Error: "host out of scope"}
		case "b":
			return &collexec.StepResult{Outcome: collexec.OutcomeBlocked, BlockReason: collexec.BlockUnresolved, Unresolved: []string{"x"}}
		}
		return nil
	}
	rep := mustRun(t, f, nil, Options{})
	if rep.Totals.ScopeBlocks != 1 || rep.Totals.Unresolved != 1 || rep.Totals.Blocked != 2 || rep.ExitCode() != ExitScope {
		t.Fatalf("%+v exit %d", rep.Totals, rep.ExitCode())
	}
}
