package store

import (
	"errors"
	"testing"
)

// Bug-bounty platforms still score in CVSS v3.1 while the finding contract
// requires a v4.0 vector in cvss. A v3.1 vector is kept beside it in cvssV3.
func TestFindingStoresCVSSV31BesideV40(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	id, err := s.CreateFinding(&Finding{Title: "IDOR", Severity: "High",
		Cvss:   "CVSS:4.0/AV:N/AC:L/AT:N/PR:L/UI:N/VC:H/VI:N/VA:N/SC:N/SI:N/SA:N",
		CvssV3: "CVSS:3.1/AV:N/AC:L/PR:L/UI:N/S:U/C:H/I:N/A:N"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.GetFinding(id)
	if err != nil {
		t.Fatal(err)
	}
	if got.CvssV3 != "CVSS:3.1/AV:N/AC:L/PR:L/UI:N/S:U/C:H/I:N/A:N" || got.CvssV3Score == nil || *got.CvssV3Score != 6.5 || got.CvssV3Rating != "MEDIUM" {
		t.Fatalf("v3.1 vector not kept/scored: %q %v %q", got.CvssV3, got.CvssV3Score, got.CvssV3Rating)
	}
	if got.Cvss == "" || got.CvssScore == nil {
		t.Fatalf("v4.0 vector disturbed: %+v", got)
	}

	// Patch, clear, and reject anything that is not a v3.1 vector.
	v := "CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H"
	if err := s.UpdateFindingMetadata(id, FindingMetadataPatch{CvssV3: &v}); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetFinding(id)
	if got.CvssV3 != v || got.CvssV3Rating != "CRITICAL" {
		t.Fatalf("patch = %q %q", got.CvssV3, got.CvssV3Rating)
	}
	for _, bad := range []string{"CVSS:4.0/AV:N/AC:L/AT:N/PR:L/UI:N/VC:H/VI:N/VA:N/SC:N/SI:N/SA:N", "CVSS:3.1/AV:N", "high"} {
		b := bad
		if err := s.UpdateFindingMetadata(id, FindingMetadataPatch{CvssV3: &b}); !errors.Is(err, ErrInvalidFinding) {
			t.Errorf("cvssV3 %q accepted: %v", bad, err)
		}
	}
	empty := ""
	if err := s.UpdateFindingMetadata(id, FindingMetadataPatch{CvssV3: &empty}); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetFinding(id)
	if got.CvssV3 != "" || got.CvssV3Score != nil {
		t.Fatalf("clear left %q", got.CvssV3)
	}
}
