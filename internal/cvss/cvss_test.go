package cvss

import (
	"strings"
	"testing"

	cvss40 "github.com/pandatix/go-cvss/40"
)

func TestValidateForWrite(t *testing.T) {
	v4 := "CVSS:4.0/AV:N/AC:L/AT:N/PR:N/UI:N/VC:H/VI:H/VA:H/SC:N/SI:N/SA:N"
	if err := ValidateForWrite(""); err != nil {
		t.Fatalf("empty vector must be allowed (clearing): %v", err)
	}
	if err := ValidateForWrite(v4); err != nil {
		t.Fatalf("valid 4.0 vector rejected: %v", err)
	}
	for _, bad := range []string{"CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H", "9.8", "AV:N/AC:L", "CVSS:4.0/AV:N"} {
		err := ValidateForWrite(bad)
		if err == nil {
			t.Fatalf("ValidateForWrite(%q) accepted", bad)
		}
		if !strings.Contains(err.Error(), "cvss") || !strings.Contains(err.Error(), "CVSS:4.0/") {
			t.Fatalf("error must name the field and expected format: %v", err)
		}
	}
}

// TestRatingBandEdges pins the score edges of every CVSS 4.0 band and the one
// documented mapping from CVSS NONE to the product severity Info.
func TestRatingBandEdges(t *testing.T) {
	for _, tt := range []struct {
		score    float64
		raw, sev string
	}{
		{0.0, "NONE", "Info"}, {0.1, "LOW", "Low"}, {3.9, "LOW", "Low"}, {4.0, "MEDIUM", "Medium"},
		{6.9, "MEDIUM", "Medium"}, {7.0, "HIGH", "High"}, {8.9, "HIGH", "High"}, {9.0, "CRITICAL", "Critical"}, {10.0, "CRITICAL", "Critical"},
	} {
		raw, err := cvss40.Rating(tt.score)
		if err != nil || raw != tt.raw {
			t.Fatalf("score %v: raw=%q err=%v, want %s", tt.score, raw, err, tt.raw)
		}
		if got := ProductSeverity(raw); got != tt.sev {
			t.Fatalf("ProductSeverity(%s)=%s, want %s", raw, got, tt.sev)
		}
	}
	if ProductSeverity("INFO") != "Info" || ProductSeverity("bogus") != "" {
		t.Fatal("INFO maps to Info; unknown ratings map to nothing")
	}
}

func TestEvaluateExplainsInfluentialMetrics(t *testing.T) {
	got, err := Evaluate("CVSS:4.0/AV:N/AC:L/AT:N/PR:N/UI:N/VC:H/VI:H/VA:H/SC:N/SI:N/SA:N")
	if err != nil {
		t.Fatal(err)
	}
	if got.Severity != "Critical" || !strings.Contains(got.Explanation, "network") || !strings.Contains(got.Explanation, "no privileges") {
		t.Fatalf("explanation/severity missing: %+v", got)
	}
	none, _ := Evaluate("CVSS:4.0/AV:N/AC:H/AT:N/PR:H/UI:N/VC:N/VI:N/VA:N/SC:N/SI:N/SA:N")
	if none.Severity != "Info" || none.RawRating != "NONE" || none.Rating != "INFO" {
		t.Fatalf("0.0 must be Info while RawRating keeps NONE: %+v", none)
	}
}

func TestIsLegacy(t *testing.T) {
	if !IsLegacy("cvss:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H") || IsLegacy("CVSS:4.0/AV:N") || IsLegacy("") {
		t.Fatal("IsLegacy only flags 3.1 vectors")
	}
}

func TestEvaluateRatingBoundaries(t *testing.T) {
	tests := []struct {
		name, vector, rating, raw string
		score                     float64
	}{
		{"none", "CVSS:4.0/AV:N/AC:H/AT:N/PR:H/UI:N/VC:N/VI:N/VA:N/SC:N/SI:N/SA:N", "INFO", "NONE", 0},
		{"low", "CVSS:4.0/AV:P/AC:H/AT:P/PR:H/UI:A/VC:L/VI:N/VA:N/SC:N/SI:N/SA:N", "LOW", "LOW", 1},
		{"medium", "CVSS:4.0/AV:L/AC:L/AT:N/PR:L/UI:P/VC:N/VI:H/VA:H/SC:N/SI:L/SA:L", "MEDIUM", "MEDIUM", 5.2},
		{"high", "CVSS:4.0/AV:N/AC:L/AT:N/PR:N/UI:N/VC:N/VI:N/VA:N/SC:H/SI:H/SA:H", "HIGH", "HIGH", 7.9},
		{"critical", "CVSS:4.0/AV:N/AC:L/AT:N/PR:N/UI:N/VC:H/VI:H/VA:H/SC:N/SI:N/SA:N", "CRITICAL", "CRITICAL", 9.3},
		{"cvss31_critical", "CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H", "CRITICAL", "CRITICAL", 9.8},
		{"cvss31_medium", "CVSS:3.1/AV:N/AC:L/PR:N/UI:R/S:C/C:L/I:L/A:N", "MEDIUM", "MEDIUM", 6.1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Evaluate(tt.vector)
			if err != nil {
				t.Fatal(err)
			}
			if got.Score != tt.score || got.Rating != tt.rating || got.RawRating != tt.raw {
				t.Fatalf("evaluation=%+v, want score=%v rating=%s raw=%s", got, tt.score, tt.rating, tt.raw)
			}
			if got.Vector != tt.vector || got.CanonicalVector == "" || got.Nomenclature == "" {
				t.Fatalf("evaluation lost vector metadata: %+v", got)
			}
		})
	}
}

func TestEvaluateRejectsInvalidVector(t *testing.T) {
	for _, vector := range []string{"", "9.8", "CVSS:4.0/AV:N", "CVSS:3.1/AV:N"} {
		if _, err := Evaluate(vector); err == nil {
			t.Errorf("Evaluate(%q) accepted invalid vector", vector)
		}
	}
}
