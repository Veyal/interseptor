package verify

import (
	"context"
	"time"
)

// OOBSpec describes an out-of-band confirmation attempt (Gate 3 mechanism). The
// Probe request must already carry the token's callback URL injected as its
// payload (the caller mints the token and builds the URL); ConfirmOOB does not
// construct payloads, it only sends and correlates.
type OOBSpec struct {
	Probe    Request       // request with the token URL already injected
	Token    string        // the exact token to correlate an interaction against
	Window   time.Duration // total time to wait for a callback (default 30s)
	Interval time.Duration // gap between polls (default 500ms)

	// sleep is the injectable delay used between polls; nil ⇒ time.Sleep. Tests
	// set a no-op (or a channel-driven) sleep so the poll loop runs instantly.
	sleep func(context.Context, time.Duration)
}

func (s OOBSpec) window() time.Duration {
	if s.Window <= 0 {
		return 30 * time.Second
	}
	return s.Window
}

func (s OOBSpec) interval() time.Duration {
	if s.Interval <= 0 {
		return 500 * time.Millisecond
	}
	return s.Interval
}

// OOBResult is the outcome of ConfirmOOB.
type OOBResult struct {
	Confirmed bool   // a callback for Token arrived within the window
	Token     string // echoed for the proof record
	ProbeFlow int64  // the recorded probe flow id (for PoC attachment)
	Polls     int    // how many times the poller was queried
	Detail    string
}

// ConfirmOOB sends the probe once, then polls poll.HitsForToken(token) until a
// hit arrives or the window elapses. A callback to a globally-unique URL only we
// injected is proof the payload executed server-side, so a single hit confirms.
// No hit within the window ⇒ not confirmed (blind candidates are never filed on
// inference alone). Respects ctx cancellation: a cancelled ctx stops polling and
// returns not-confirmed.
//
// The poll cadence is fully injectable (Window/Interval + a fake sleep), so tests
// drive it without real time.Sleep. The number of poll iterations is
// deterministic: ceil(window/interval)+1 (an immediate check, then one per
// interval across the window).
func ConfirmOOB(ctx context.Context, s Sender, poll OOBPoller, spec OOBSpec) OOBResult {
	res := OOBResult{Token: spec.Token}
	if spec.Token == "" {
		res.Detail = "empty token"
		return res
	}

	probe := s.Send(ctx, spec.Probe)
	res.ProbeFlow = probe.FlowID

	sleep := spec.sleep
	if sleep == nil {
		sleep = defaultSleep
	}
	interval := spec.interval()
	deadline := time.Duration(0)
	window := spec.window()

	for {
		if err := ctx.Err(); err != nil {
			res.Detail = "cancelled: " + err.Error()
			return res
		}
		res.Polls++
		if poll.HitsForToken(spec.Token) > 0 {
			res.Confirmed = true
			res.Detail = "OOB callback received for token"
			return res
		}
		if deadline >= window {
			res.Detail = "no OOB callback within window"
			return res
		}
		sleep(ctx, interval)
		deadline += interval
	}
}

// defaultSleep is the production poll delay: a plain interruptible sleep.
func defaultSleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}
