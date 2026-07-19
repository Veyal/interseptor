package intruder

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const lifecycleTestTimeout = 2 * time.Second

func awaitLifecycle(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(lifecycleTestTimeout):
		t.Fatalf("timed out waiting for %s", what)
	}
}

func awaitEngine(t *testing.T, e *Engine) {
	t.Helper()
	e.mu.Lock()
	done := e.doneCh
	e.mu.Unlock()
	awaitLifecycle(t, done, "Intruder run")
}

func closeEngine(t *testing.T, e *Engine) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		e.Close()
		close(done)
	}()
	awaitLifecycle(t, done, "Intruder shutdown")
}

func lifecycleSpec(target string) Spec {
	return Spec{
		Target:     target,
		Template:   "GET / HTTP/1.1\r\nHost: example.com\r\n\r\n",
		AttackType: "repeat",
		Repeat:     1,
		Threads:    1,
	}
}

func TestCloseZeroStateRejectsEveryFutureStart(t *testing.T) {
	e := newEngine(t)
	closeEngine(t, e)

	err := e.Start(Spec{})
	if !errors.Is(err, ErrClosed) {
		t.Fatalf("Start after Close error = %v, want ErrClosed", err)
	}
	if st := e.State(); st.Running {
		t.Fatalf("closed zero-state engine is running: %+v", st)
	}
}

func TestCloseCompletedRunPreservesState(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer target.Close()
	e := newEngine(t)

	if err := e.Start(lifecycleSpec(target.URL)); err != nil {
		t.Fatalf("Start: %v", err)
	}
	awaitEngine(t, e)
	before := e.State()
	closeEngine(t, e)
	after := e.State()

	if after.Running || after.Total != before.Total || after.Done != before.Done || len(after.Results) != len(before.Results) {
		t.Fatalf("Close changed completed state: before=%+v after=%+v", before, after)
	}
}

func TestCloseAfterRestartRejectsAnotherRestart(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer target.Close()
	e := newEngine(t)
	spec := lifecycleSpec(target.URL)

	for run := 1; run <= 2; run++ {
		if err := e.Start(spec); err != nil {
			t.Fatalf("Start run %d: %v", run, err)
		}
		awaitEngine(t, e)
	}
	closeEngine(t, e)

	if err := e.Start(spec); !errors.Is(err, ErrClosed) {
		t.Fatalf("Start after restarted engine was closed = %v, want ErrClosed", err)
	}
}

func TestCloseCancelsDispatchDelay(t *testing.T) {
	firstRequest := make(chan struct{}, 1)
	var hits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		if hits.Add(1) == 1 {
			firstRequest <- struct{}{}
		}
	}))
	defer target.Close()
	e := newEngine(t)
	spec := lifecycleSpec(target.URL)
	spec.Repeat = 2
	spec.DelayMs = 60_000

	if err := e.Start(spec); err != nil {
		t.Fatalf("Start: %v", err)
	}
	awaitLifecycle(t, firstRequest, "first delayed-run request")
	closeEngine(t, e)

	if got := hits.Load(); got != 1 {
		t.Fatalf("requests dispatched after Close: got %d, want 1", got)
	}
}

func TestCloseCancelsInFlightSend(t *testing.T) {
	requestStarted := make(chan struct{})
	requestCanceled := make(chan struct{})
	forceRelease := make(chan struct{})
	target := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		close(requestStarted)
		select {
		case <-r.Context().Done():
			close(requestCanceled)
		case <-forceRelease:
		}
	}))
	defer target.Close()
	e := newEngine(t)

	if err := e.Start(lifecycleSpec(target.URL)); err != nil {
		t.Fatalf("Start: %v", err)
	}
	awaitLifecycle(t, requestStarted, "in-flight request")
	closeDone := make(chan struct{})
	go func() {
		e.Close()
		close(closeDone)
	}()
	select {
	case <-closeDone:
	case <-time.After(lifecycleTestTimeout):
		close(forceRelease)
		awaitLifecycle(t, closeDone, "shutdown after forced request release")
		t.Fatal("Close did not cancel the in-flight send")
	}
	awaitLifecycle(t, requestCanceled, "request context cancellation")

	if st := e.State(); st.Running {
		t.Fatalf("engine still running after Close: %+v", st)
	}
}

func TestConcurrentStartAndCloseCannotEscapeBarrier(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer target.Close()
	e := newEngine(t)
	gate := make(chan struct{})
	startResult := make(chan error, 1)
	closeDone := make(chan struct{})

	go func() {
		<-gate
		startResult <- e.Start(lifecycleSpec(target.URL))
	}()
	go func() {
		<-gate
		e.Close()
		close(closeDone)
	}()
	close(gate)

	var startErr error
	select {
	case startErr = <-startResult:
	case <-time.After(lifecycleTestTimeout):
		t.Fatal("timed out waiting for concurrent Start")
	}
	awaitLifecycle(t, closeDone, "concurrent Close")
	if startErr != nil && !errors.Is(startErr, ErrClosed) {
		t.Fatalf("concurrent Start error = %v, want nil or ErrClosed", startErr)
	}
	if err := e.Start(lifecycleSpec(target.URL)); !errors.Is(err, ErrClosed) {
		t.Fatalf("Start escaped Close barrier: %v", err)
	}
	if st := e.State(); st.Running {
		t.Fatalf("engine running after concurrent Start/Close: %+v", st)
	}
}

func TestConcurrentCloseIsIdempotent(t *testing.T) {
	e := newEngine(t)
	const closers = 16
	gate := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(closers)
	for range closers {
		go func() {
			defer wg.Done()
			<-gate
			e.Close()
		}()
	}
	close(gate)
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	awaitLifecycle(t, done, "concurrent Close calls")

	if err := e.Start(Spec{}); !errors.Is(err, ErrClosed) {
		t.Fatalf("Start after concurrent Close = %v, want ErrClosed", err)
	}
}
