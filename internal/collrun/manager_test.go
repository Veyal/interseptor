package collrun

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/collexec"
)

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestManagerRunsToCompletionAndStreamsEvents(t *testing.T) {
	m := NewManager()
	defer m.Close()
	f := newFake("a", "b")
	lr, err := m.Start(f, nil, Options{CollectionUID: "c1"})
	if err != nil {
		t.Fatal(err)
	}
	if lr.UID == "" {
		t.Fatal("Start must return a run with an id")
	}
	ch, cancel := lr.Subscribe()
	defer cancel()
	rep, err := lr.Wait(context.Background())
	if err != nil || rep.Status != StatusDone {
		t.Fatalf("err %v rep %+v", err, rep)
	}
	for range ch { // channel closes when the run ends
	}
	p := lr.Snapshot(0)
	if !p.Finished || p.Count != 2 || len(p.Items) != 2 || p.Report == nil || p.Status != StatusDone {
		t.Fatalf("snapshot %+v", p)
	}
	if tail := lr.Snapshot(1); len(tail.Items) != 1 || tail.Items[0].Name != "b" || tail.Since != 1 {
		t.Fatalf("since offset: %+v", tail)
	}
	if got, ok := m.Get(lr.UID); !ok || got != lr {
		t.Fatal("finished run must stay queryable")
	}
	if len(m.Active()) != 0 {
		t.Fatalf("active %v", m.Active())
	}
}

func TestManagerPauseResumeAbort(t *testing.T) {
	m := NewManager()
	defer m.Close()
	f := newFake("a", "b", "c")
	gate := make(chan struct{})
	f.step = func(in collexec.StepInput, n int) *collexec.StepResult {
		if n == 1 {
			<-gate
		}
		return nil
	}
	lr, err := m.Start(f, nil, Options{CollectionUID: "c1"})
	if err != nil {
		t.Fatal(err)
	}
	lr.Pause()
	if lr.Snapshot(0).Status != LivePaused {
		t.Fatalf("status %s", lr.Snapshot(0).Status)
	}
	close(gate)
	time.Sleep(60 * time.Millisecond)
	if n := lr.Snapshot(0).Count; n != 1 {
		t.Fatalf("paused run advanced to %d items", n)
	}
	lr.Resume()
	rep, _ := lr.Wait(context.Background())
	if rep.Status != StatusDone || len(rep.Items) != 3 {
		t.Fatalf("status %s items %d", rep.Status, len(rep.Items))
	}

	f2 := newFake("a", "b")
	lr2, _ := m.Start(f2, nil, Options{CollectionUID: "c1", Delay: time.Hour - time.Minute*50})
	waitFor(t, "first item", func() bool { return lr2.Snapshot(0).Count == 1 })
	lr2.Abort()
	rep2, _ := lr2.Wait(context.Background())
	if rep2.Status != StatusAborted {
		t.Fatalf("status %s", rep2.Status)
	}
}

func TestManagerPersistAskWaitsForDecision(t *testing.T) {
	for _, keep := range []bool{true, false} {
		m := NewManager()
		f := newFake("login")
		f.step = writer
		lr, err := m.Start(f, nil, Options{CollectionUID: "c1", Persist: PersistAsk})
		if err != nil {
			t.Fatal(err)
		}
		waitFor(t, "awaiting_persist", func() bool { return lr.Snapshot(0).Status == LiveAwaitingPersist })
		p := lr.Snapshot(0)
		if p.Finished || len(p.Pending) != 1 || p.Pending[0].Key != "token" {
			t.Fatalf("pending writes must be visible while waiting: %+v", p)
		}
		if !lr.Decide(keep) {
			t.Fatal("Decide refused while waiting")
		}
		rep, _ := lr.Wait(context.Background())
		wantCommits := 0
		if keep {
			wantCommits = 1
		}
		if len(f.commits) != wantCommits || rep.Persist.Decision == "" {
			t.Fatalf("keep=%v commits %d decision %q", keep, len(f.commits), rep.Persist.Decision)
		}
		if lr.Decide(true) {
			t.Fatal("Decide must refuse once the run is over")
		}
		m.Close()
	}
}

func TestManagerAbortWhileAwaitingDecisionDiscards(t *testing.T) {
	m := NewManager()
	f := newFake("login")
	f.step = writer
	lr, _ := m.Start(f, nil, Options{CollectionUID: "c1", Persist: PersistAsk})
	waitFor(t, "awaiting_persist", func() bool { return lr.Snapshot(0).Status == LiveAwaitingPersist })
	m.Close()
	rep, _ := lr.Wait(context.Background())
	if len(f.commits) != 0 || rep.Persist.Decision != PersistDiscard {
		t.Fatalf("no answer must mean discard: commits %d decision %q", len(f.commits), rep.Persist.Decision)
	}
}

func TestManagerLimitsActiveRuns(t *testing.T) {
	m := NewManager()
	m.MaxActive = 1
	defer m.Close()
	f := newFake("a")
	gate := make(chan struct{})
	f.step = func(collexec.StepInput, int) *collexec.StepResult { <-gate; return nil }
	lr, err := m.Start(f, nil, Options{CollectionUID: "c1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Start(newFake("b"), nil, Options{CollectionUID: "c1"}); err != ErrTooManyRuns {
		t.Fatalf("err %v", err)
	}
	close(gate)
	_, _ = lr.Wait(context.Background())
	if _, err := m.Start(newFake("c"), nil, Options{CollectionUID: "c1"}); err != nil {
		t.Fatalf("slot must free up: %v", err)
	}
}

func TestManagerStartErrorsAndQuarantine(t *testing.T) {
	m := NewManager()
	defer m.Close()
	if _, err := m.Start(newFake("a"), nil, Options{CollectionUID: "c1", Bail: "never"}); err == nil {
		t.Fatal("invalid options must fail Start")
	}
	f := quarantinedFake()
	lr, err := m.Start(f, nil, Options{CollectionUID: "c1", FailOnQuarantine: true})
	if err != nil {
		t.Fatal(err)
	}
	rep, _ := lr.Wait(context.Background())
	if rep.Status != StatusNotApproved || len(f.calls) != 0 {
		t.Fatalf("status %s calls %d", rep.Status, len(f.calls))
	}
}

func TestManagerKeepsOnlyRecentFinishedRuns(t *testing.T) {
	m := NewManager()
	m.Keep = 2
	defer m.Close()
	var uids []string
	for i := 0; i < 4; i++ {
		lr, err := m.Start(newFake("a"), nil, Options{CollectionUID: "c1"})
		if err != nil {
			t.Fatal(err)
		}
		_, _ = lr.Wait(context.Background())
		uids = append(uids, lr.UID)
		time.Sleep(5 * time.Millisecond)
	}
	waitFor(t, "trim", func() bool { _, ok := m.Get(uids[0]); return !ok })
	if _, ok := m.Get(uids[3]); !ok {
		t.Fatal("newest run must stay")
	}
}

func TestSubscriberNeverBlocksRunner(t *testing.T) {
	m := NewManager()
	defer m.Close()
	f := newFake("a")
	lr, err := m.Start(f, nil, Options{CollectionUID: "c1", Iterations: 400})
	if err != nil {
		t.Fatal(err)
	}
	_, cancel := lr.Subscribe() // never read from it
	defer cancel()
	ctx, stop := context.WithTimeout(context.Background(), 10*time.Second)
	defer stop()
	rep, err := lr.Wait(ctx)
	if err != nil || len(rep.Items) != 400 {
		t.Fatalf("a stuck subscriber stalled the run: %v", err)
	}
}

// Pause, resume and the persist-ask prompt are visible to event consumers
// (the control layer forwards them over SSE).
func TestManagerEmitsPauseResumeAndPersistAskEvents(t *testing.T) {
	m := NewManager()
	defer m.Close()
	f := newFake("login")
	f.step = writer
	var mu sync.Mutex
	var types []string
	var asked []VarChangeView
	lr, err := m.Start(f, nil, Options{CollectionUID: "c1", Persist: PersistAsk, OnEvent: func(e Event) {
		mu.Lock()
		defer mu.Unlock()
		types = append(types, e.Type)
		if e.Type == "awaiting_persist" {
			asked = e.Pending
		}
	}})
	if err != nil {
		t.Fatal(err)
	}
	lr.Pause()
	lr.Resume()
	waitFor(t, "awaiting_persist", func() bool { return lr.Snapshot(0).Status == LiveAwaitingPersist })
	lr.Decide(true)
	if _, err := lr.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, want := range []string{"start", "paused", "resumed", "awaiting_persist", "done"} {
		found := false
		for _, g := range types {
			found = found || g == want
		}
		if !found {
			t.Fatalf("missing %q event in %v", want, types)
		}
	}
	if len(asked) != 1 || asked[0].Key != "token" {
		t.Fatalf("awaiting_persist must carry the pending writes: %+v", asked)
	}
}
