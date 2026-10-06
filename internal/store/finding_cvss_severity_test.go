package store

import (
	"errors"
	"strings"
	"testing"
)

func TestCreateFindingBlocksSeverityMismatch(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_, err = s.CreateFinding(&Finding{Title: "mismatch", Severity: "Low", Cvss: testCVSS4})
	if !errors.Is(err, ErrInvalidFinding) || !strings.Contains(err.Error(), "severity") || !strings.Contains(err.Error(), "Critical") || !strings.Contains(err.Error(), "severityOverride") {
		t.Fatalf("want ErrInvalidFinding naming severity, calculated rating and override field, got %v", err)
	}
}

func TestCreateFindingDerivesSeverityFromVector(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	id, err := s.CreateFinding(&Finding{Title: "derived", Cvss: testCVSS4})
	if err != nil {
		t.Fatalf("omitted severity must follow the vector: %v", err)
	}
	got, _ := s.GetFinding(id)
	if got.Severity != "Critical" {
		t.Fatalf("severity=%q, want Critical", got.Severity)
	}
}

func TestSeverityOverrideAllowsDocumentedMismatch(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	f := &Finding{Title: "override", Severity: "Low", Cvss: testCVSS4, ProofReview: FindingProofReview{SeverityOverride: "Compensating control limits exposure to a lab network"}}
	id, err := s.CreateFinding(f)
	if err != nil {
		t.Fatalf("documented override rejected: %v", err)
	}
	got, _ := s.GetFinding(id)
	if got.Severity != "Low" || got.CvssRating != "CRITICAL" || got.ProofReview.SeverityOverride == "" {
		t.Fatalf("override not preserved: sev=%s rating=%s review=%+v", got.Severity, got.CvssRating, got.ProofReview)
	}
	for _, g := range got.Readiness.Gaps {
		if g == "severity" {
			t.Fatalf("overridden severity must not be a readiness gap: %+v", got.Readiness)
		}
	}
}

func TestUpdateFindingSeverityMismatchBlockedOnlyWhenChanged(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	id, err := s.CreateFinding(&Finding{Title: "legacy mismatch", Severity: "Low"})
	if err != nil {
		t.Fatal(err)
	}
	// A pre-existing mismatched finding stays editable for unrelated fields.
	seedLegacyCVSS(t, s, id, testCVSS4)
	title := "renamed"
	if err := s.UpdateFinding(id, nil, nil, &title, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("unrelated edit blocked: %v", err)
	}
	sev := "High"
	if err := s.UpdateFinding(id, &sev, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil); !errors.Is(err, ErrInvalidFinding) {
		t.Fatalf("changing severity to a mismatching value must be blocked, got %v", err)
	}
	sev = "Critical"
	if err := s.UpdateFinding(id, &sev, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("aligned severity rejected: %v", err)
	}
	other := "CVSS:4.0/AV:N/AC:H/AT:N/PR:H/UI:N/VC:N/VI:N/VA:N/SC:N/SI:N/SA:N"
	if err := s.UpdateFinding(id, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, &other, nil); !errors.Is(err, ErrInvalidFinding) {
		t.Fatalf("changing vector so severity mismatches must be blocked, got %v", err)
	}
	info := "Info"
	if err := s.UpdateFinding(id, &info, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, &other, nil); err != nil {
		t.Fatalf("0.0/NONE must map to Info severity: %v", err)
	}
	got, _ := s.GetFinding(id)
	if got.Severity != "Info" || got.CvssRating != "INFO" || got.CvssScore == nil || *got.CvssScore != 0 {
		t.Fatalf("NONE not rendered as Info: %+v", got)
	}
}
