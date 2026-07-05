package verify

import (
	"context"
	"testing"
	"time"
)

// arrivingPoller reports 0 hits until the arriveAt-th poll (1-based), then 1.
// arriveAt <= 0 means it never arrives.
type arrivingPoller struct {
	arriveAt int
	polls    int
}

func (p *arrivingPoller) HitsForToken(_ string) int {
	p.polls++
	if p.arriveAt > 0 && p.polls >= p.arriveAt {
		return 1
	}
	return 0
}

// probeSender records a fixed probe flow id and counts sends.
type probeSender struct {
	flow int64
	sent int
}

func (s *probeSender) Send(_ context.Context, _ Request) Exchange {
	s.sent++
	return Exchange{Status: 200, FlowID: s.flow}
}

// noopSleep drives the poll loop instantly (no real time.Sleep).
func noopSleep(_ context.Context, _ time.Duration) {}

func TestConfirmOOBArrives(t *testing.T) {
	s := &probeSender{flow: 42}
	poll := &arrivingPoller{arriveAt: 3} // arrives on the 3rd poll
	spec := OOBSpec{
		Probe:    req("probe"),
		Token:    "tok-abc",
		Window:   10 * time.Second,
		Interval: time.Second,
		sleep:    noopSleep,
	}
	res := ConfirmOOB(context.Background(), s, poll, spec)
	if !res.Confirmed {
		t.Fatalf("expected confirmed, got %+v", res)
	}
	if res.ProbeFlow != 42 {
		t.Fatalf("expected probe flow 42, got %d", res.ProbeFlow)
	}
	if res.Token != "tok-abc" {
		t.Fatalf("token not echoed: %q", res.Token)
	}
	if s.sent != 1 {
		t.Fatalf("probe should be sent exactly once, got %d", s.sent)
	}
	if res.Polls != 3 {
		t.Fatalf("expected 3 polls, got %d", res.Polls)
	}
}

func TestConfirmOOBImmediateHit(t *testing.T) {
	s := &probeSender{flow: 1}
	poll := &arrivingPoller{arriveAt: 1} // present on the very first check
	spec := OOBSpec{Probe: req("probe"), Token: "t", Window: time.Second, Interval: 100 * time.Millisecond, sleep: noopSleep}
	res := ConfirmOOB(context.Background(), s, poll, spec)
	if !res.Confirmed || res.Polls != 1 {
		t.Fatalf("expected confirmed on first poll, got %+v", res)
	}
}

func TestConfirmOOBNeverArrives(t *testing.T) {
	s := &probeSender{flow: 7}
	poll := &arrivingPoller{arriveAt: 0} // never
	spec := OOBSpec{
		Probe:    req("probe"),
		Token:    "tok",
		Window:   2 * time.Second,
		Interval: time.Second,
		sleep:    noopSleep,
	}
	res := ConfirmOOB(context.Background(), s, poll, spec)
	if res.Confirmed {
		t.Fatalf("expected not confirmed, got %+v", res)
	}
	if res.ProbeFlow != 7 {
		t.Fatalf("probe flow should still be recorded, got %d", res.ProbeFlow)
	}
	// window/interval = 2/1 → checks at t=0,1,2 → 3 polls.
	if res.Polls != 3 {
		t.Fatalf("expected 3 polls across the window, got %d", res.Polls)
	}
}

func TestConfirmOOBEmptyToken(t *testing.T) {
	s := &probeSender{flow: 1}
	poll := &arrivingPoller{arriveAt: 1}
	res := ConfirmOOB(context.Background(), s, poll, OOBSpec{Token: "", sleep: noopSleep})
	if res.Confirmed {
		t.Fatalf("empty token must not confirm, got %+v", res)
	}
	if s.sent != 0 {
		t.Fatalf("empty token should not send a probe, sent %d", s.sent)
	}
}

func TestConfirmOOBContextCancelled(t *testing.T) {
	s := &probeSender{flow: 1}
	poll := &arrivingPoller{arriveAt: 0} // never arrives
	ctx, cancel := context.WithCancel(context.Background())
	// Cancel from within the sleep so the loop observes ctx.Done on the next check.
	spec := OOBSpec{
		Probe:    req("probe"),
		Token:    "tok",
		Window:   time.Hour, // would otherwise poll for an hour
		Interval: time.Second,
		sleep: func(_ context.Context, _ time.Duration) {
			cancel()
		},
	}
	res := ConfirmOOB(ctx, s, poll, spec)
	if res.Confirmed {
		t.Fatalf("cancelled ctx must not confirm, got %+v", res)
	}
	// One initial check (miss), one sleep (cancels), then ctx.Err stops it.
	if res.Polls != 1 {
		t.Fatalf("expected 1 poll before cancellation stop, got %d", res.Polls)
	}
}

func TestConfirmOOBDefaultsWindowInterval(t *testing.T) {
	// With no Window/Interval set, defaults (30s/500ms) apply. Using a poller that
	// arrives immediately keeps this fast while exercising the default path.
	s := &probeSender{flow: 1}
	poll := &arrivingPoller{arriveAt: 1}
	res := ConfirmOOB(context.Background(), s, &fakeOnce{poll}, OOBSpec{Probe: req("p"), Token: "t", sleep: noopSleep})
	if !res.Confirmed {
		t.Fatalf("expected confirmed with defaults, got %+v", res)
	}
}

// fakeOnce wraps a poller unchanged; present to document the defaults path.
type fakeOnce struct{ inner OOBPoller }

func (f *fakeOnce) HitsForToken(tok string) int { return f.inner.HitsForToken(tok) }
