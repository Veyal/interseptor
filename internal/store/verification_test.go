package store

import (
	"database/sql"
	"errors"
	"testing"
)

// TestFindingVerificationUpsert covers save → get and the upsert-by-finding_id
// semantics (one proof-record per finding, re-save overwrites in place).
func TestFindingVerificationUpsert(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	run, err := s.CreatePentestRun(&PentestRun{Status: "verifying"})
	if err != nil {
		t.Fatalf("CreatePentestRun: %v", err)
	}
	fid, err := s.CreateFinding(&Finding{Severity: "High", Title: "SQLi", Source: "ai", Status: "verified"})
	if err != nil {
		t.Fatalf("CreateFinding: %v", err)
	}

	v := &FindingVerification{
		FindingID:    fid,
		RunID:        run,
		VulnClass:    "sqli-boolean",
		Gates:        `{"diff":{"reproN":3}}`,
		ReproCount:   3,
		BaselineFlow: 11,
		PayloadFlow:  12,
		Confidence:   85,
		TS:           1000,
	}
	id, err := s.SaveFindingVerification(v)
	if err != nil || id == 0 {
		t.Fatalf("SaveFindingVerification: id=%d err=%v", id, err)
	}
	if v.ID != id {
		t.Fatalf("v.ID not set: %d vs %d", v.ID, id)
	}

	got, err := s.GetFindingVerification(fid)
	if err != nil {
		t.Fatalf("GetFindingVerification: %v", err)
	}
	if got.VulnClass != "sqli-boolean" || got.ReproCount != 3 || got.Confidence != 85 ||
		got.RunID != run || got.BaselineFlow != 11 || got.PayloadFlow != 12 || got.TS != 1000 {
		t.Fatalf("round-trip wrong: %+v", got)
	}

	// Re-save for the same finding: upsert overwrites in place, same row id, no dup.
	v2 := &FindingVerification{
		FindingID:  fid,
		RunID:      run,
		VulnClass:  "ssrf-blind",
		Gates:      `{"oob":{"token":"abc"}}`,
		ReproCount: 1,
		OOBToken:   "abc123",
		Confidence: 100,
		TS:         2000,
	}
	id2, err := s.SaveFindingVerification(v2)
	if err != nil {
		t.Fatalf("SaveFindingVerification re-save: %v", err)
	}
	if id2 != id {
		t.Fatalf("upsert should reuse row id: was %d now %d", id, id2)
	}
	got2, _ := s.GetFindingVerification(fid)
	if got2.VulnClass != "ssrf-blind" || got2.OOBToken != "abc123" || got2.Confidence != 100 || got2.TS != 2000 {
		t.Fatalf("upsert did not overwrite: %+v", got2)
	}

	// Exactly one row for this finding.
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM finding_verification WHERE finding_id=?`, fid).Scan(&n)
	if n != 1 {
		t.Fatalf("expected exactly 1 proof-record, got %d", n)
	}
}

// TestGetFindingVerificationMissing verifies a finding with no proof-record yields
// sql.ErrNoRows (so callers can distinguish machine-proven from hand-set verified).
func TestGetFindingVerificationMissing(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	if _, err := s.GetFindingVerification(42); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected sql.ErrNoRows, got %v", err)
	}
}
