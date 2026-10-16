// Package redact describes secrets by length and digest so findings can prove
// equality without publishing the value, and lints free text for probable
// secrets. It never stores or returns a raw value.
package redact

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
)

// PrefixLen is the number of hex characters of the SHA-256 digest that are kept.
const PrefixLen = 12

// Result is the safe description of a value.
type Result struct {
	Len          int    `json:"len"`
	SHA256Prefix string `json:"sha256_prefix"`
	Kind         string `json:"kind"`
}

// Placeholder is the redacted form to write into finding text.
func (r Result) Placeholder() string {
	return fmt.Sprintf("[redacted %s len=%d sha256=%s]", r.Kind, r.Len, r.SHA256Prefix)
}

// Hit is a probable secret found in text. It carries no raw value.
type Hit struct {
	Kind    string `json:"kind"`
	Len     int    `json:"len"`
	Suggest string `json:"suggest"`
}

type pattern struct {
	kind string
	re   *regexp.Regexp
}

// Order matters: a bearer header wrapping a JWT reports the bearer once, the
// JWT inside is skipped by overlap tracking in Scan.
var patterns = []pattern{
	{"bearer_token", regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9._~+/=-]{16,}`)},
	{"jwt", regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{5,}\.eyJ[A-Za-z0-9_-]{5,}\.[A-Za-z0-9_-]*`)},
	{"google_api_key", regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{35}`)},
	{"bcrypt_hash", regexp.MustCompile(`\$2[abxy]\$\d{2}\$[./A-Za-z0-9]{53}`)},
}

// Describe returns the length, digest prefix and kind of value. The value is
// only hashed in memory; nothing is retained.
func Describe(value string) Result {
	sum := sha256.Sum256([]byte(value))
	return Result{Len: len(value), SHA256Prefix: hex.EncodeToString(sum[:])[:PrefixLen], Kind: kindOf(value)}
}

func kindOf(value string) string {
	for _, p := range patterns {
		if loc := p.re.FindStringIndex(value); loc != nil && loc[0] == 0 && loc[1] == len(value) {
			return p.kind
		}
	}
	return "secret"
}

// Scan reports probable secrets in text, each with its redacted replacement.
func Scan(text string) []Hit {
	var hits []Hit
	var taken [][2]int
	overlaps := func(a, b int) bool {
		for _, t := range taken {
			if a < t[1] && b > t[0] {
				return true
			}
		}
		return false
	}
	for _, p := range patterns {
		for _, loc := range p.re.FindAllStringIndex(text, -1) {
			if overlaps(loc[0], loc[1]) {
				continue
			}
			taken = append(taken, [2]int{loc[0], loc[1]})
			r := Describe(text[loc[0]:loc[1]])
			r.Kind = p.kind
			hits = append(hits, Hit{Kind: p.kind, Len: r.Len, Suggest: r.Placeholder()})
		}
	}
	return hits
}
