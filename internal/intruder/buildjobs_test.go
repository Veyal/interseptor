package intruder

import "testing"

// A pathological repeat count must be represented arithmetically, never built:
// the plan holds the total and generates each job on demand, so a repeat of 2^30
// costs the same memory as a repeat of 3.
func TestPlanRepresentsHugeRepeatWithoutBuilding(t *testing.T) {
	plan := planJobs(Spec{AttackType: "repeat", Repeat: 1 << 30}, 1, []string{"x"})
	if plan.total != 1<<30 {
		t.Fatalf("huge repeat: total = %d, want %d", plan.total, 1<<30)
	}
	// Enumerating must be interruptible at any point (the run stops on Stop).
	var n int
	plan.each(func(job) bool {
		n++
		return n < 4
	})
	if n != 4 {
		t.Fatalf("huge repeat: yielded %d jobs before stopping, want 4", n)
	}

	small := planJobs(Spec{AttackType: "repeat", Repeat: 3}, 1, []string{"x"})
	if small.total != 3 {
		t.Fatalf("small repeat: total = %d, want 3", small.total)
	}
}

func TestPitchforkIgnoresPayloadListsWithoutMatchingPositions(t *testing.T) {
	plan := planJobs(Spec{
		AttackType: "pitchfork",
		Payloads:   [][]string{{"one", "two"}, {}},
	}, 1, []string{"base"})
	if plan.total != 2 {
		t.Fatalf("total = %d, want 2; an extra unmatched list must not suppress valid positions", plan.total)
	}
	var n int
	plan.each(func(job) bool {
		n++
		return true
	})
	if n != 2 {
		t.Fatalf("jobs = %d, want 2", n)
	}
}
