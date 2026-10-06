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
	// Severity is the product severity (Critical/High/Medium/Low/Info) that a
	// finding must carry for this vector unless an override is documented.
	// CVSS NONE (0.0) maps to Info; RawRating keeps the specification's name.
	Severity    string `json:"severity"`
	Explanation string `json:"explanation,omitempty"`
	Legacy      bool   `json:"legacy,omitempty"`
}

// ProductSeverity maps a CVSS rating name to the finding severity. It is the
// single mapping shared by the UI preview, API, MCP and store validation.
func ProductSeverity(rating string) string {
	switch strings.ToUpper(strings.TrimSpace(rating)) {
	case "CRITICAL":
		return "Critical"
	case "HIGH":
		return "High"
	case "MEDIUM":
		return "Medium"
	case "LOW":
		return "Low"
	case "NONE", "INFO":
		return "Info"
	}
	return ""
}

var raisingMetrics = map[string]string{
	"AV:N": "network-reachable", "AC:L": "low attack complexity", "AT:N": "no attack requirements",
	"PR:N": "no privileges required", "UI:N": "no user interaction",
	"VC:H": "high confidentiality impact", "VI:H": "high integrity impact", "VA:H": "high availability impact",
	"SC:H": "high subsequent-system confidentiality impact", "SI:H": "high subsequent-system integrity impact", "SA:H": "high subsequent-system availability impact",
}

var loweringMetrics = map[string]string{
	"AV:P": "physical access needed", "AV:L": "local access needed", "AC:H": "high attack complexity", "AT:P": "attack requirements present",
	"PR:H": "high privileges required", "UI:A": "active user interaction", "VC:N": "no confidentiality impact", "VI:N": "no integrity impact", "VA:N": "no availability impact",
}

// explain lists the metrics that most move a v4.0 base score, in vector order.
func explain(vector string) string {
	var raise, lower []string
	for _, part := range strings.Split(vector, "/")[1:] {
		if text, ok := raisingMetrics[part]; ok {
			raise = append(raise, text)
		}
		if text, ok := loweringMetrics[part]; ok {
			lower = append(lower, text)
		}
	}
	var out []string
	if len(raise) > 0 {
		out = append(out, "Raises score: "+strings.Join(raise, ", "))
	}
	if len(lower) > 0 {
		out = append(out, "Lowers score: "+strings.Join(lower, ", "))
	}
	return strings.Join(out, ". ")
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
			Severity:        ProductSeverity(rawRating),
			Legacy:          true,
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
		Severity:        ProductSeverity(rawRating),
		Explanation:     explain(v.Vector()),
	}, nil
}
