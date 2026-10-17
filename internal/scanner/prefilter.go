package scanner

import (
	"regexp"
	"strings"
)

// A few checks use large case-insensitive alternations with no literal prefix
// (database errors, framework debug pages, cloud keys). Go's regexp runs those
// on its slow NFA path: about 1 MB/s, so a 256 KiB HTML body cost ~280 ms of
// regex work and a scan over a few hundred large pages took tens of seconds.
//
// Every alternative of those patterns contains a fixed substring, so a body
// that contains none of them cannot match. gatedRe checks those substrings
// first (fast) and runs the regex only when one is present; the result is
// identical to the regex alone.
type gatedRe struct {
	re     *regexp.Regexp
	tokens []string // necessary substrings; at least one occurs in any match
	fold   bool     // tokens are lower-case and the pattern is (?i)
}

// find returns re.FindString(s). folded is s as foldForGate returns it; it is
// only read when g.fold is set.
func (g gatedRe) find(s, folded string) string {
	hay := s
	if g.fold {
		hay = folded
	}
	for _, t := range g.tokens {
		if strings.Contains(hay, t) {
			return g.re.FindString(s)
		}
	}
	return ""
}

// foldForGate lower-cases s for a token test against a (?i) pattern whose
// tokens are ASCII. (?i) also folds U+017F (long s) to "s" and U+212A (Kelvin
// sign) to "k"; strings.ToLower maps the Kelvin sign to "k" but leaves the
// long s alone, so it is mapped here — the gate must never reject a body the
// regex would match.
func foldForGate(s string) string {
	l := strings.ToLower(s)
	if strings.Contains(l, "ſ") {
		l = strings.ReplaceAll(l, "ſ", "s")
	}
	return l
}

var (
	dbErrorGate = gatedRe{re: dbErrorRe, fold: true, tokens: []string{
		"sql syntax", "mysql_fetch", "valid mysql result", "ora-", "postgresql", "pg_query failed",
		"sqlite", "sqlstate[", "unclosed quotation mark", "quoted string not properly terminated",
		"syntax error", "sqlexception",
	}}
	debugPageGate = gatedRe{re: debugPageRe, fold: true, tokens: []string{
		"werkzeug debugger", "traceback (most recent call last)", `class="whoops`, `whoops\exception`,
		`symfony\component\`, "action controller: exception caught",
		"you're seeing this error because you have debug = true", "runtime error",
		"server error in '/' application", "<b>fatal error</b>", "<b>parse error</b>",
		"uncaught ", "goroutine ", "at system.", "at java.",
	}}
	privateIPGate    = gatedRe{re: privateIPRe, tokens: []string{"127.", "10.", "172.", "192.168.", "169.254."}}
	mixedContentGate = gatedRe{re: mixedContentRe, fold: true, tokens: []string{"http://"}}
	cloudKeyGate     = gatedRe{re: cloudKeyRe, tokens: []string{
		"AKIA", "ASIA", "AGPA", "AIDA", "AROA", "AIPA", "ANPA", "ANVA", "ACCA",
		"AIza", "ghp_", "gho_", "ghs_", "ghu_", "ghr_", "github_pat_", "xox",
		"sk_live_", "rk_live_", "ya29.", "SG.", "-----BEGIN ",
	}}
)

// hasDigitRun reports whether s holds n ASCII digits in a row, each pair at most
// one space or hyphen apart: the shortest text ccCandidateRe, (?:\d[ -]?){13,19},
// can match. It is a single pass, so bodies with no such run skip the regex.
func hasDigitRun(s string, n int) bool {
	run, sep := 0, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
			run++
			sep = false
			if run >= n {
				return true
			}
		case (c == ' ' || c == '-') && run > 0 && !sep:
			sep = true
		default:
			run, sep = 0, false
		}
	}
	return false
}
