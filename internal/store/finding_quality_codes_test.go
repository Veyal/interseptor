package store

import "testing"

// Every gap the readiness summary can emit has a hint, and FindingGapCodes
// lists them for the documentation generator.
func TestFindingGapCodesCoverReadinessGaps(t *testing.T) {
	codes := map[string]bool{}
	for _, c := range FindingGapCodes() {
		codes[c] = true
	}
	for _, want := range []string{"summary", "target", "impact", "why", "evidence", "proof", "action", "result", "control", "execution", "execution_reason", "visual", "cvss", "severity", "target_evidence", "fix", "retest", "confidence", "evidence_missing", "verification"} {
		if !codes[want] {
			t.Errorf("FindingGapCodes missing %q", want)
		}
	}
	f := &Finding{}
	for _, gap := range f.ReadinessSummary().Gaps {
		if !codes[gap] {
			t.Errorf("readiness gap %q has no hint", gap)
		}
	}
}
