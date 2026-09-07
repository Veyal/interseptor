package cvss

import "testing"

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
	for _, vector := range []string{"", "9.8", "CVSS:4.0/AV:N"} {
		if _, err := Evaluate(vector); err == nil {
			t.Errorf("Evaluate(%q) accepted invalid vector", vector)
		}
	}
}
