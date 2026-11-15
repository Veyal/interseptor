package store

import (
	"slices"
	"strings"
	"unicode"
)

// SplitNegativeSearch separates the excluded terms of a History search from the
// terms that must match. A token starting with unquoted "-" excludes matching
// flows; the other tokens are the positive query, rejoined with one space so
// callers can keep treating it as a single search string. Double quotes group a
// term that contains spaces or a leading literal dash ("-admin"), while a "-"
// before an opening quote (-"image/png") excludes that phrase.
//
// Excluded terms are lower-cased and de-duplicated because every store-side
// match is case-insensitive and runs against lower-cased fields. A lone "-" is
// kept as text: it carries no term to exclude; empty exclusions such as -"   "
// and whitespace-only quoted tokens are ignored.
func SplitNegativeSearch(input string) (positive string, exclude []string) {
	var keep []string
	for _, tok := range searchTokens(input) {
		term, isExcl := excludedToken(tok)
		if isExcl {
			if term != "" && !slices.Contains(exclude, term) {
				exclude = append(exclude, term)
			}
			continue
		}
		if strings.TrimSpace(tok.text) == "" {
			continue
		}
		keep = append(keep, tok.text)
	}
	return strings.Join(keep, " "), exclude
}

type searchToken struct {
	text       string
	canExclude bool
	hadQuote   bool
}

// searchTokens splits on unquoted whitespace, dropping the quotes that grouped a
// term. An unterminated quote keeps the rest of the input as one term.
func searchTokens(input string) []searchToken {
	var (
		tokens     []searchToken
		current    strings.Builder
		quoted     bool
		hadQuote   bool
		canExclude bool
		started    bool
	)
	flush := func() {
		if started {
			tokens = append(tokens, searchToken{
				text:       current.String(),
				canExclude: canExclude,
				hadQuote:   hadQuote,
			})
			current.Reset()
			hadQuote = false
			canExclude = false
			started = false
		}
	}
	for _, r := range input {
		switch {
		case r == '"':
			if !started {
				started = true
				canExclude = false
			}
			hadQuote = true
			quoted = !quoted
		case !quoted && unicode.IsSpace(r):
			flush()
		default:
			if !started {
				started = true
				canExclude = true
			}
			current.WriteRune(r)
		}
	}
	flush()
	return tokens
}

// excludedToken reports whether tok is a "-" exclusion and returns its lower-cased term.
func excludedToken(tok searchToken) (string, bool) {
	if !tok.canExclude || !strings.HasPrefix(tok.text, "-") {
		return "", false
	}
	if len(tok.text) == 1 && !tok.hadQuote {
		return "", false
	}
	return strings.ToLower(strings.TrimSpace(tok.text[1:])), true
}
