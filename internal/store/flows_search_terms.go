package store

import (
	"slices"
	"strings"
	"unicode"
)

// SplitNegativeSearch separates the excluded terms of a History search from the
// terms that must match. A token prefixed with "-" excludes matching flows; the
// other tokens are the positive query, rejoined with one space so callers can
// keep treating it as a single search string. Double quotes group a term that
// contains spaces, with or without the prefix, so -"image/png" and -image/png
// read the same.
//
// Excluded terms are lower-cased and de-duplicated because every store-side
// match is case-insensitive and runs against lower-cased fields. A lone "-" is
// kept as text: it carries no term to exclude.
func SplitNegativeSearch(input string) (positive string, exclude []string) {
	var keep []string
	for _, token := range searchTokens(input) {
		term, ok := excludedTerm(token)
		if !ok {
			keep = append(keep, token)
			continue
		}
		if !slices.Contains(exclude, term) {
			exclude = append(exclude, term)
		}
	}
	return strings.Join(keep, " "), exclude
}

// searchTokens splits on unquoted whitespace, dropping the quotes that grouped a
// term. An unterminated quote keeps the rest of the input as one term.
func searchTokens(input string) []string {
	var (
		tokens  []string
		current strings.Builder
		quoted  bool
	)
	flush := func() {
		if current.Len() > 0 {
			tokens = append(tokens, current.String())
			current.Reset()
		}
	}
	for _, r := range input {
		switch {
		case r == '"':
			quoted = !quoted
		case !quoted && unicode.IsSpace(r):
			flush()
		default:
			current.WriteRune(r)
		}
	}
	flush()
	return tokens
}

// excludedTerm reports whether token is a "-" exclusion and returns its term.
func excludedTerm(token string) (string, bool) {
	if !strings.HasPrefix(token, "-") {
		return "", false
	}
	term := strings.ToLower(strings.TrimSpace(token[1:]))
	return term, term != ""
}
