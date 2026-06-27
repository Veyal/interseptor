package intruder

import "testing"

// buildJobs must enforce the request cap DURING accumulation, not after — a huge
// repeat count (or a sniper attack with thousands of positions×payloads) would
// otherwise materialize billions of job structs and OOM before any truncation.
func TestBuildJobsCapsDuringAccumulation(t *testing.T) {
	// A pathological repeat count returns at most maxRequests jobs, fast.
	jobs, capped := buildJobs(Spec{AttackType: "repeat", Repeat: 1 << 30}, 1, []string{"x"})
	if len(jobs) != maxRequests {
		t.Fatalf("huge repeat: got %d jobs, want cap %d", len(jobs), maxRequests)
	}
	if !capped {
		t.Fatal("huge repeat: expected capped=true")
	}

	// A small repeat is returned in full and not flagged capped.
	jobs, capped = buildJobs(Spec{AttackType: "repeat", Repeat: 3}, 1, []string{"x"})
	if len(jobs) != 3 || capped {
		t.Fatalf("small repeat: got %d jobs capped=%v, want 3/false", len(jobs), capped)
	}
}
