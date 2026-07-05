package verify

import (
	"context"
	"fmt"
)

// DiffSpec describes a differential-reproduction attempt (Gate 1). It carries the
// requests needed by the chosen oracle plus how many consecutive passes are
// required. The set of requests used depends on Class:
//
//   - ClassReflected / ClassError: Baseline + Payload (Marker for reflected).
//   - ClassBoolean:                Baseline + PayloadTrue + PayloadFalse.
//   - ClassTiming:                 Baseline + Payload + Control (zero-delay).
type DiffSpec struct {
	Class    VulnClass
	Baseline Request
	Payload  Request // the confirming payload (reflected / error / timing)

	// Boolean-length variant.
	PayloadTrue  Request
	PayloadFalse Request

	// Timing variant: a zero-delay control that must return fast.
	Control Request

	// Marker is the unique string the reflected-marker oracle looks for.
	Marker string

	// N is how many consecutive times the oracle must hold. Defaults to 3 when
	// <= 0 (see effectiveN).
	N int
}

func (s DiffSpec) effectiveN() int {
	if s.N <= 0 {
		return 3
	}
	return s.N
}

// DiffResult is the outcome of ReproduceDifferential.
type DiffResult struct {
	Reproduced   bool    // oracle held N consecutive times
	Times        int     // how many consecutive passes were observed (== N on success)
	Baseline     []int64 // recorded baseline/control flow ids gathered across attempts
	PayloadFlows []int64 // recorded payload flow ids gathered across attempts
	Detail       string  // human/AI-readable explanation
}

// ReproduceDifferential re-issues the class's requests up to N times and requires
// the class oracle to hold on *every* attempt; the first failure short-circuits
// and rejects the candidate (fluke rejection). It respects ctx cancellation: if
// ctx is done, it stops and returns a not-reproduced result. Deterministic given
// a deterministic Sender — no real network, no sleeps.
func ReproduceDifferential(ctx context.Context, s Sender, spec DiffSpec) DiffResult {
	n := spec.effectiveN()
	res := DiffResult{}
	for i := 0; i < n; i++ {
		if err := ctx.Err(); err != nil {
			res.Detail = fmt.Sprintf("cancelled after %d/%d passes: %v", res.Times, n, err)
			return res
		}
		held, detail := runOnce(ctx, s, spec, &res)
		if !held {
			res.Reproduced = false
			res.Detail = fmt.Sprintf("oracle %s failed on attempt %d/%d: %s", spec.Class, i+1, n, detail)
			return res
		}
		res.Times++
	}
	res.Reproduced = true
	res.Detail = fmt.Sprintf("oracle %s held %d/%d consecutive times", spec.Class, res.Times, n)
	return res
}

// runOnce issues the requests for one attempt, records the flow ids on res, and
// evaluates the class oracle. It returns whether the oracle held plus a short
// per-attempt detail string. Unknown classes never reproduce.
func runOnce(ctx context.Context, s Sender, spec DiffSpec, res *DiffResult) (bool, string) {
	switch spec.Class {
	case ClassReflected:
		base := s.Send(ctx, spec.Baseline)
		pl := s.Send(ctx, spec.Payload)
		recordBaseline(res, base)
		recordPayload(res, pl)
		if !pl.ok() {
			return false, "payload exchange did not complete"
		}
		return ReflectedMarkerHeld(base, pl, spec.Marker),
			fmt.Sprintf("marker %q present=%v", spec.Marker, ReflectedMarkerHeld(base, pl, spec.Marker))

	case ClassError:
		base := s.Send(ctx, spec.Baseline)
		pl := s.Send(ctx, spec.Payload)
		recordBaseline(res, base)
		recordPayload(res, pl)
		return ErrorSignatureHeld(base, pl), "db/interpreter error signature"

	case ClassBoolean:
		base := s.Send(ctx, spec.Baseline)
		tru := s.Send(ctx, spec.PayloadTrue)
		fls := s.Send(ctx, spec.PayloadFalse)
		recordBaseline(res, base)
		recordPayload(res, tru)
		recordPayload(res, fls)
		return BooleanLengthHeld(base, tru, fls),
			fmt.Sprintf("len base=%d true=%d false=%d", len(base.Body), len(tru.Body), len(fls.Body))

	case ClassTiming:
		base := s.Send(ctx, spec.Baseline)
		pl := s.Send(ctx, spec.Payload)
		ctrl := s.Send(ctx, spec.Control)
		recordBaseline(res, base)
		recordBaseline(res, ctrl)
		recordPayload(res, pl)
		return TimingHeld(base, pl, ctrl),
			fmt.Sprintf("ms base=%d payload=%d control=%d", base.DurMs, pl.DurMs, ctrl.DurMs)

	default:
		return false, "unknown vuln class"
	}
}

func recordBaseline(res *DiffResult, e Exchange) {
	if e.FlowID != 0 {
		res.Baseline = append(res.Baseline, e.FlowID)
	}
}

func recordPayload(res *DiffResult, e Exchange) {
	if e.FlowID != 0 {
		res.PayloadFlows = append(res.PayloadFlows, e.FlowID)
	}
}
