// Package verify holds the deterministic, LLM-free primitives of the autonomous
// pentester's 4-gate verifier (see docs/AUTONOMOUS-PENTEST.md §5): Gate 1,
// differential reproduction, and the mechanism behind Gate 3, out-of-band (OOB)
// confirmation.
//
// Both primitives turn a *candidate* vulnerability into machine-proven ground
// truth without any LLM in the loop:
//
//   - Gate 1 re-issues a payload request and a matched baseline/control N times
//     and requires a class-specific oracle (reflection, DB error, boolean-length
//     split, or timing) to hold on every attempt — a single flaky observation
//     rejects the candidate.
//   - Gate 3's mechanism sends a probe carrying a globally-unique OOB token and
//     polls for a callback to that exact token; a hit is proof the payload
//     executed server-side.
//
// The package is deliberately decoupled from the concrete sender/OOB catcher: it
// depends only on the small Sender / OOBPoller interfaces defined here, injected
// by the caller. Real adapters over internal/sender and internal/oob are wired in
// Phase 2; the unit tests inject scripted fakes, so nothing here touches the
// network or the wall clock. Every class oracle is an exported pure function.
package verify

import "context"

// Exchange is a single HTTP exchange the verifier can reason about, kept minimal
// and provider-agnostic so a real sender adapter and a test fake can both produce
// it. It captures only what the oracles need (status, headers, body, duration)
// plus the recorded FlowID for later PoC attachment and any transport Err.
type Exchange struct {
	Status  int
	Headers map[string][]string
	Body    []byte
	DurMs   int64
	FlowID  int64 // the recorded flow, for PoC attachment later
	Err     error
}

// ok reports whether the exchange completed at the transport level (a real
// response arrived). Timing/length oracles treat a failed exchange as unusable.
func (e Exchange) ok() bool { return e.Err == nil && e.Status != 0 }

// Request describes a request variant to (re-)issue. It mirrors the shape of
// internal/sender.Request but is redeclared here to keep the package free of a
// hard dependency on that concrete type; the Phase-2 adapter maps between them.
type Request struct {
	Method  string
	URL     string
	Headers map[string][]string
	Body    []byte
}

// Sender re-issues a request variant and returns the recorded exchange. Phase 2
// injects a real adapter over internal/sender; tests inject a scripted fake.
type Sender interface {
	Send(ctx context.Context, req Request) Exchange
}

// OOBPoller answers "did a callback arrive for this token?". Phase 2 injects an
// adapter over internal/oob.Catcher (counting interactions whose token matches);
// tests inject a fake that "arrives" after k polls.
type OOBPoller interface {
	HitsForToken(token string) int
}
