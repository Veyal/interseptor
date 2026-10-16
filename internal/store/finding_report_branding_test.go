package store

import (
	"errors"
	"strings"
	"testing"
)

const testPNG = "data:image/png;base64,iVBORw0KGgo="

func brandingStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestReportBrandingDefaultsToNothing(t *testing.T) {
	s := brandingStore(t)
	b, err := s.GetReportBranding()
	if err != nil {
		t.Fatalf("GetReportBranding: %v", err)
	}
	if !b.IsEmpty() || b.Version != 0 {
		t.Fatalf("unset branding should be empty at version 0: %+v", b)
	}
}

func TestReportBrandingVersionsOnlyOnChange(t *testing.T) {
	s := brandingStore(t)
	in := ReportBranding{CompanyName: "Example Security", ClientName: "Example Corp", LeadAssessor: "A. Assessor"}
	first, err := s.SetReportBranding(in)
	if err != nil {
		t.Fatalf("set: %v", err)
	}
	if first.Version != 1 || first.UpdatedAt == 0 {
		t.Fatalf("first save: %+v", first)
	}
	same, err := s.SetReportBranding(in)
	if err != nil {
		t.Fatalf("resave: %v", err)
	}
	if same.Version != 1 {
		t.Fatalf("an unchanged save must not bump the version: %d", same.Version)
	}
	in.ClientName = "Example Holdings"
	changed, err := s.SetReportBranding(in)
	if err != nil {
		t.Fatalf("change: %v", err)
	}
	if changed.Version != 2 {
		t.Fatalf("changed save: version %d, want 2", changed.Version)
	}
	read, err := s.GetReportBranding()
	if err != nil {
		t.Fatal(err)
	}
	if read.ClientName != "Example Holdings" || read.Version != 2 {
		t.Fatalf("read back: %+v", read)
	}
}

func TestReportBrandingIgnoresCallerVersion(t *testing.T) {
	s := brandingStore(t)
	out, err := s.SetReportBranding(ReportBranding{Version: 99, UpdatedAt: 12345, CompanyName: "Example Security"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Version != 1 || out.UpdatedAt == 12345 {
		t.Fatalf("caller controlled the version: %+v", out)
	}
}

func TestReportBrandingAcceptsEmbeddedLogos(t *testing.T) {
	s := brandingStore(t)
	out, err := s.SetReportBranding(ReportBranding{CompanyLogo: testPNG, ClientLogo: "data:image/jpeg;base64,/9j/"})
	if err != nil {
		t.Fatalf("data: logos must be accepted: %v", err)
	}
	if out.CompanyLogo != testPNG {
		t.Fatalf("logo not stored: %+v", out)
	}
}

func TestReportBrandingRejectsUnsafeInput(t *testing.T) {
	s := brandingStore(t)
	cases := []struct {
		name string
		in   ReportBranding
		want string
	}{
		{"remote logo", ReportBranding{CompanyLogo: "https://example.com/logo.png"}, "data: image URI"},
		{"svg logo", ReportBranding{CompanyLogo: "data:image/svg+xml;base64,PHN2Zz4="}, "is not one of"},
		{"unencoded logo", ReportBranding{CompanyLogo: "data:image/png,raw"}, "base64"},
		{"oversized logo", ReportBranding{CompanyLogo: "data:image/png;base64," + strings.Repeat("A", MaxReportLogoBytes)}, "exceeds"},
		{"oversized field", ReportBranding{CompanyName: strings.Repeat("x", MaxReportBrandingFieldBytes+1)}, "exceeds"},
		{"multiline field", ReportBranding{ClientName: "Example\nCorp"}, "single line"},
		{"oversized cover", ReportBranding{CoverPage: strings.Repeat("x", MaxReportCoverPageBytes+1)}, "exceeds"},
		{"bad start date", ReportBranding{AssessmentStart: "March 2026"}, "ISO date"},
		{"bad end date", ReportBranding{AssessmentEnd: "2026-13-40"}, "ISO date"},
		{"reversed window", ReportBranding{AssessmentStart: "2026-03-14", AssessmentEnd: "2026-03-03"}, "before assessmentStart"},
	}
	for _, c := range cases {
		_, err := s.SetReportBranding(c.in)
		if err == nil {
			t.Errorf("%s: accepted", c.name)
			continue
		}
		if !errors.Is(err, ErrInvalidEngagement) {
			t.Errorf("%s: %v is not ErrInvalidEngagement", c.name, err)
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: error %q does not explain %q", c.name, err, c.want)
		}
	}
	// A rejected write must leave nothing behind.
	if b, _ := s.GetReportBranding(); !b.IsEmpty() {
		t.Fatalf("a rejected branding write persisted: %+v", b)
	}
}

func TestReportBrandingAcceptsAnISOWindow(t *testing.T) {
	s := brandingStore(t)
	out, err := s.SetReportBranding(ReportBranding{AssessmentStart: "2026-03-03", AssessmentEnd: "2026-03-14"})
	if err != nil {
		t.Fatalf("ISO window rejected: %v", err)
	}
	if out.AssessmentStart != "2026-03-03" || out.AssessmentEnd != "2026-03-14" {
		t.Fatalf("dates altered: %+v", out)
	}
}
