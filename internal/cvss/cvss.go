// Package cvss contains the shared CVSS v3.1 and v4.0 evaluation contract used by the
// control API and the findings editor.
package cvss

import (
	"fmt"
	"strings"

	cvss31 "github.com/pandatix/go-cvss/31"
	cvss40 "github.com/pandatix/go-cvss/40"
)

// Evaluation is the operator-facing result of evaluating a CVSS vector.
// Vector preserves the submitted spelling; CanonicalVector is available for a
// deliberate Apply operation without rewriting an existing finding on preview.
type Evaluation struct {
	Vector          string  `json:"vector"`
	CanonicalVector string  `json:"canonicalVector"`
	Score           float64 `json:"score"`
	Rating          string  `json:"rating"`
	RawRating       string  `json:"rawRating"`
	Nomenclature    string  `json:"nomenclature"`
}

// ExpectedVectorFormat is the only vector shape accepted for new or changed findings.
const ExpectedVectorFormat = "CVSS:4.0/AV:N/AC:L/AT:N/PR:N/UI:N/VC:H/VI:H/VA:H/SC:N/SI:N/SA:N"

// IsLegacy reports whether vector is a CVSS v3.1 vector. Existing findings with
// such vectors are preserved and warned about, never rewritten.
func IsLegacy(vector string) bool {
	return strings.HasPrefix(strings.ToUpper(strings.TrimSpace(vector)), "CVSS:3.1")
}

// ValidateForWrite enforces the finding contract for a vector being written:
// empty clears the field, anything else must be a complete CVSS:4.0 vector.
// The error names the field and the expected format.
func ValidateForWrite(vector string) error {
	vector = strings.TrimSpace(vector)
	if vector == "" {
		return nil
	}
	if !strings.HasPrefix(strings.ToUpper(vector), "CVSS:4.0/") {
		return fmt.Errorf("cvss must be a CVSS v4.0 vector starting with \"CVSS:4.0/\" (expected format %s); got %q", ExpectedVectorFormat, truncate(vector, 80))
	}
	if _, err := cvss40.ParseVector(vector); err != nil {
		return fmt.Errorf("cvss is not a valid CVSS v4.0 vector (expected format %s): %v", ExpectedVectorFormat, err)
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// Evaluate parses and scores one complete CVSS v3.1 or v4.0 vector. A CVSS NONE
// rating is exposed as Info for the finding UI while RawRating retains the
// specification's name.
func Evaluate(vector string) (Evaluation, error) {
	vector = strings.TrimSpace(vector)
	if vector == "" {
		return Evaluation{}, fmt.Errorf("CVSS vector is required")
	}
	if strings.HasPrefix(strings.ToUpper(vector), "CVSS:3.1") {
		v, err := cvss31.ParseVector(vector)
		if err != nil {
			return Evaluation{}, fmt.Errorf("invalid CVSS v3.1 vector: %w", err)
		}
		score := v.BaseScore()
		rawRating, err := cvss31.Rating(score)
		if err != nil {
			return Evaluation{}, fmt.Errorf("evaluate CVSS v3.1 vector: %w", err)
		}
		rating := rawRating
		if rating == "NONE" {
			rating = "INFO"
		}
		return Evaluation{
			Vector:          vector,
			CanonicalVector: v.Vector(),
			Score:           score,
			Rating:          rating,
			RawRating:       rawRating,
			Nomenclature:    "CVSS-3.1",
		}, nil
	}
	v, err := cvss40.ParseVector(vector)
	if err != nil {
		return Evaluation{}, fmt.Errorf("invalid CVSS v4.0 vector: %w", err)
	}
	score := v.Score()
	rawRating, err := cvss40.Rating(score)
	if err != nil {
		return Evaluation{}, fmt.Errorf("evaluate CVSS v4.0 vector: %w", err)
	}
	rating := rawRating
	if rating == "NONE" {
		rating = "INFO"
	}
	return Evaluation{
		Vector:          vector,
		CanonicalVector: v.Vector(),
		Score:           score,
		Rating:          rating,
		RawRating:       rawRating,
		Nomenclature:    v.Nomenclature(),
	}, nil
}
