package store

import (
	"errors"
	"strings"
	"testing"
)

const legacyCVSS31 = "CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H"

func TestCreateFindingRejectsNonV4Vector(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, vec := range []string{legacyCVSS31, "9.8"} {
		_, err := s.CreateFinding(&Finding{Title: "example", Severity: "Critical", Cvss: vec})
		if !errors.Is(err, ErrInvalidFinding) || !strings.Contains(err.Error(), "cvss") || !strings.Contains(err.Error(), "CVSS:4.0/") {
			t.Fatalf("vector %q: want ErrInvalidFinding naming field and format, got %v", vec, err)
		}
	}
	if _, err := s.CreateFinding(&Finding{Title: "ok", Severity: "Critical", Cvss: testCVSS4}); err != nil {
		t.Fatalf("v4 vector rejected: %v", err)
	}
}

func TestUpdateFindingCVSSVersionOnlyWhenChanged(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	id, err := s.CreateFinding(&Finding{Title: "legacy", Severity: "Critical"})
	if err != nil {
		t.Fatal(err)
	}
	// Seed a legacy 3.1 finding the way an old archive would have stored it.
	if _, err := s.db.Exec(`UPDATE findings SET cvss=? WHERE id=?`, legacyCVSS31, id); err != nil {
		t.Fatal(err)
	}
	title := "renamed"
	if err := s.UpdateFinding(id, nil, nil, &title, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("unrelated edit of a legacy finding must succeed: %v", err)
	}
	got, err := s.GetFinding(id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Cvss != legacyCVSS31 {
		t.Fatalf("legacy vector must never be rewritten silently, got %q", got.Cvss)
	}
	if !strings.Contains(got.CvssWarning, "3.1") {
		t.Fatalf("legacy finding needs a cvssWarning, got %q", got.CvssWarning)
	}
	same := legacyCVSS31
	if err := s.UpdateFinding(id, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, &same, nil); err != nil {
		t.Fatalf("resending the unchanged legacy vector must succeed: %v", err)
	}
	other := "CVSS:3.1/AV:N/AC:L/PR:N/UI:R/S:U/C:H/I:H/A:H"
	if err := s.UpdateFinding(id, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, &other, nil); !errors.Is(err, ErrInvalidFinding) {
		t.Fatalf("changing to another 3.1 vector must be rejected, got %v", err)
	}
	v4 := testCVSS4
	sev := "Critical"
	if err := s.UpdateFinding(id, &sev, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, &v4, nil); err != nil {
		t.Fatalf("moving to a 4.0 vector must succeed: %v", err)
	}
	got, _ = s.GetFinding(id)
	if got.CvssWarning != "" {
		t.Fatalf("v4 finding must not carry a legacy warning: %q", got.CvssWarning)
	}
}
