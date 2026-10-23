package store

import (
	"testing"
)

const cvss69 = "CVSS:4.0/AV:N/AC:L/AT:N/PR:N/UI:N/VC:N/VI:L/VA:L/SC:N/SI:N/SA:N"

// forgetSeverityAlignment simulates a project written before the one-time
// severity alignment ran.
func forgetSeverityAlignment(t *testing.T, s *Store) {
	t.Helper()
	if _, err := s.db.Exec(`DELETE FROM settings WHERE key=?`, severityAlignmentSetting); err != nil {
		t.Fatal(err)
	}
}

func findingRevisionCount(t *testing.T, s *Store, id int64) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`SELECT count(*) FROM finding_revisions WHERE finding_id=?`, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func reopen(t *testing.T, s *Store, dir string) *Store {
	t.Helper()
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	return s2
}

func TestSeverityAlignmentRunsOnceWithRevision(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.createFindingPreserving(&Finding{Title: "merged", Severity: "Critical", Cvss: cvss69})
	if err != nil {
		t.Fatal(err)
	}
	override, err := s.createFindingPreserving(&Finding{Title: "overridden", Severity: "Critical", Cvss: cvss69,
		ProofReview: FindingProofReview{SeverityOverride: "chained with a pre-auth bug"}})
	if err != nil {
		t.Fatal(err)
	}
	forgetSeverityAlignment(t, s)
	revs := findingRevisionCount(t, s, id)
	var updated int64
	_ = s.db.QueryRow(`SELECT updated_ts FROM findings WHERE id=?`, id).Scan(&updated)
	if _, err := s.db.Exec(`UPDATE findings SET updated_ts=1 WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}

	s = reopen(t, s, dir)
	var sev string
	if err := s.db.QueryRow(`SELECT severity, updated_ts FROM findings WHERE id=?`, id).Scan(&sev, &updated); err != nil {
		t.Fatal(err)
	}
	if sev != "Medium" {
		t.Fatalf("severity %q, want Medium", sev)
	}
	if updated == 1 {
		t.Fatal("alignment did not bump updated_ts")
	}
	after := findingRevisionCount(t, s, id)
	if after <= revs {
		t.Fatalf("revisions %d -> %d: alignment must be audited", revs, after)
	}
	var source string
	_ = s.db.QueryRow(`SELECT source FROM finding_revisions WHERE finding_id=? ORDER BY id DESC LIMIT 1`, id).Scan(&source)
	if source != "migration" {
		t.Fatalf("latest revision source %q, want migration", source)
	}
	if got := dbSeverity(t, s, override); got != "Critical" {
		t.Fatalf("overridden finding realigned to %q", got)
	}

	// Later merges keep their historical severity: the alignment never reruns.
	if _, err := s.db.Exec(`UPDATE findings SET severity='Critical' WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	s = reopen(t, s, dir)
	defer s.Close()
	if n := findingRevisionCount(t, s, id); n != after {
		t.Fatalf("second reopen added revisions: %d -> %d", after, n)
	}
	_ = s.db.QueryRow(`SELECT severity FROM findings WHERE id=?`, id).Scan(&sev)
	if sev != "Critical" {
		t.Fatalf("second reopen rewrote severity to %q", sev)
	}
}

func TestUpdateCVSSOnlyDerivesSeverity(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	id, err := s.CreateFinding(&Finding{Title: "medium", Severity: "Medium"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateFinding(id, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, ptr(testCVSS4), nil); err != nil {
		t.Fatalf("cvss-only update rejected: %v", err)
	}
	if got := dbSeverity(t, s, id); got != "Critical" {
		t.Fatalf("severity %q, want Critical from the new vector", got)
	}

	// A documented override keeps the stored severity.
	oid, err := s.CreateFinding(&Finding{Title: "override", Severity: "Low", ProofReview: FindingProofReview{SeverityOverride: "lab only"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateFinding(oid, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, ptr(testCVSS4), nil); err != nil {
		t.Fatal(err)
	}
	if got := dbSeverity(t, s, oid); got != "Low" {
		t.Fatalf("override severity changed to %q", got)
	}
}

func dbSeverity(t *testing.T, s *Store, id int64) string {
	t.Helper()
	var sev string
	if err := s.db.QueryRow(`SELECT severity FROM findings WHERE id=?`, id).Scan(&sev); err != nil {
		t.Fatal(err)
	}
	return sev
}

func ptr[T any](v T) *T { return &v }
