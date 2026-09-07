// Package cvss contains the shared CVSS v4.0 evaluation contract used by the
// control API and the findings editor.
package cvss

import (
	"fmt"
	"strings"

	cvss40 "github.com/pandatix/go-cvss/40"
)

// Evaluation is the operator-facing result of evaluating a CVSS v4.0 vector.
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

// Evaluate parses and scores one complete CVSS v4.0 vector. A CVSS v4 NONE
// rating is exposed as Info for the finding UI while RawRating retains the
// specification's name.
func Evaluate(vector string) (Evaluation, error) {
	vector = strings.TrimSpace(vector)
	if vector == "" {
		return Evaluation{}, fmt.Errorf("CVSS v4.0 vector is required")
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
