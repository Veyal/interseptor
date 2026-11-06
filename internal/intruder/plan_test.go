package intruder

import (
	"math"
	"testing"
)

// A payload list longer than the old maxRequests cap must schedule one request
// per payload. The run is bounded by the Stop button, not by a constant.
func TestPlanJobsIsUncappedAndLazy(t *testing.T) {
	const n = 250000
	list := make([]string, n)
	for i := range list {
		list[i] = "p"
	}

	plan := planJobs(Spec{AttackType: "sniper", Payloads: [][]string{list}}, 1, []string{"base"})
	if plan.total != n {
		t.Fatalf("sniper total = %d, want %d", plan.total, n)
	}

	// The plan must emit jobs one at a time: yielding false has to stop the
	// generator immediately, without walking (or holding) the rest.
	got := 0
	plan.each(func(job) bool {
		got++
		return got < 3
	})
	if got != 3 {
		t.Fatalf("yield stop: got %d jobs, want 3", got)
	}

	// A pathological repeat count is represented arithmetically, never built.
	const huge = 1 << 40
	rep := planJobs(Spec{AttackType: "repeat", Repeat: huge}, 1, []string{"x"})
	if rep.total != huge {
		t.Fatalf("repeat total = %d, want %d", rep.total, huge)
	}
	got = 0
	rep.each(func(job) bool {
		got++
		return got < 2
	})
	if got != 2 {
		t.Fatalf("repeat yield stop: got %d jobs, want 2", got)
	}
}

// total must equal the number of jobs the generator really emits, for every
// attack type, so progress and paging stay honest.
func TestPlanJobsCountsMatchEnumeration(t *testing.T) {
	cases := []struct {
		name string
		spec Spec
		pos  int
		base []string
		want int64
	}{
		{"sniper-1pos", Spec{AttackType: "sniper", Payloads: [][]string{{"a", "b", "c"}}}, 1, []string{"base"}, 3},
		{"sniper-3pos", Spec{AttackType: "sniper", Payloads: [][]string{{"a", "b"}}}, 3, []string{"x", "y", "z"}, 6},
		{"battering", Spec{AttackType: "battering", Payloads: [][]string{{"a", "b", "c", "d"}}}, 2, []string{"x", "y"}, 4},
		{"pitchfork", Spec{AttackType: "pitchfork", Payloads: [][]string{{"a", "b", "c"}, {"1", "2"}}}, 2, []string{"x", "y"}, 2},
		{"cluster", Spec{AttackType: "cluster", Payloads: [][]string{{"a", "b"}, {"1", "2", "3"}}}, 2, []string{"x", "y"}, 6},
		{"repeat", Spec{AttackType: "repeat", Repeat: 7}, 1, []string{"x"}, 7},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			plan := planJobs(c.spec, c.pos, c.base)
			if plan.total != c.want {
				t.Fatalf("total = %d, want %d", plan.total, c.want)
			}
			var n int64
			plan.each(func(job) bool {
				n++
				return true
			})
			if n != c.want {
				t.Fatalf("enumerated %d jobs, want %d", n, c.want)
			}
		})
	}
}

// A Cluster bomb over several long lists overflows int64. The total saturates
// instead of wrapping negative, which would make the run look empty.
func TestMulSatSaturatesInsteadOfOverflowing(t *testing.T) {
	if got := mulSat(0, math.MaxInt64); got != 0 {
		t.Fatalf("mulSat(0, MaxInt64) = %d, want 0", got)
	}
	if got := mulSat(3, 4); got != 12 {
		t.Fatalf("mulSat(3,4) = %d, want 12", got)
	}
	if got := mulSat(math.MaxInt64, 2); got != math.MaxInt64 {
		t.Fatalf("mulSat(MaxInt64,2) = %d, want saturation", got)
	}
	if got := mulSat(1<<40, 1<<40); got != math.MaxInt64 {
		t.Fatalf("mulSat(2^40,2^40) = %d, want saturation", got)
	}
}

// The anomaly accumulator keeps a bounded sample of response lengths: an
// unbounded run must not grow this slice with the request count.
func TestAnomalyAccBoundsLengthSample(t *testing.T) {
	var acc anomalyAcc
	for i := 0; i < anomalySampleCap*20; i++ {
		acc.add(Result{Status: 200, Length: int64(i % 100)})
	}
	if len(acc.lengths) > anomalySampleCap {
		t.Fatalf("kept %d lengths, want at most %d", len(acc.lengths), anomalySampleCap)
	}
	if acc.valid != anomalySampleCap*20 {
		t.Fatalf("valid = %d, want %d", acc.valid, anomalySampleCap*20)
	}
}
