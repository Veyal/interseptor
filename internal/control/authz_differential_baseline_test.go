package control

import "testing"

// When the first context errors, the differential's real baseline is the
// first context that answered (authzDifferential compares against it). The
// matrix must mark that context as the baseline, not flag it "same" against
// the errored one, and mark the errored context as an error.
func TestAuthzMatrixFromDifferentialUsesFirstNonErrorBaseline(t *testing.T) {
	ev := authzDifferentialEvidence{FlowID: 7, Method: "GET", Host: "example.com", Path: "/a", Contexts: []differentialContext{
		{Name: "admin", Error: "dial tcp: refused"},
		{Name: "user", Status: 200, Length: 10, SameAsBaseline: true},
		{Name: "guest", Status: 200, Length: 10, SameAsBaseline: true},
		{Name: anonymousContextName, Anonymous: true, Status: 401},
	}}
	m := authzMatrixFromDifferential(ev)
	row := m.Rows[0]
	if row.Baseline.Status != 200 || row.Baseline.Length != 10 {
		t.Fatalf("row baseline = %+v, want the user context", row.Baseline)
	}
	want := []string{authzVerdictError, authzVerdictAllowed, authzVerdictSame, authzVerdictDenied}
	for i, c := range row.Cells {
		if c.Verdict != want[i] {
			t.Fatalf("cell %d (%s) verdict = %q, want %q", i, c.Identity, c.Verdict, want[i])
		}
	}
	if m.suspicious() != 1 {
		t.Fatalf("suspicious = %d, want 1 (guest only)", m.suspicious())
	}
}

// AuthNotEnforced compares the anonymous replay with the real (first
// non-error) baseline too.
func TestFinishDifferentialUsesFirstNonErrorBaseline(t *testing.T) {
	ev := authzDifferentialEvidence{Contexts: []differentialContext{
		{Name: "admin", Error: "timeout", Outcome: outcomeAuthFailure},
		{Name: "user", Status: 200, Outcome: outcomeSuccess, SameAsBaseline: true},
		{Name: anonymousContextName, Anonymous: true, Status: 200, Outcome: outcomeSuccess, SameAsBaseline: true},
	}}
	(&authzAPI{}).finishDifferential(&ev, nil, nil, differentialOpts{})
	if !ev.AuthNotEnforced {
		t.Fatal("anonymous success identical to the user baseline was not flagged")
	}
}
