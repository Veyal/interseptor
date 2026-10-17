package scanner

import (
	"math/rand"
	"strings"
	"testing"
)

var allGates = map[string]gatedRe{
	"dbError": dbErrorGate, "debugPage": debugPageGate, "cloudKey": cloudKeyGate,
	"privateIP": privateIPGate, "mixedContent": mixedContentGate,
}

// One sample per alternative of every gated pattern, in odd casing where the
// pattern is case-insensitive: the gate must let each through.
func TestGatedPatternsStillMatchEveryAlternative(t *testing.T) {
	samples := map[string][]string{
		"dbError": {
			"You have an error in your SQL SYNTAX", "mysql_fetch_array()", "not a valid MySQL result", "ORA-01756",
			"PostgreSQL query failed: ERROR", "pg_query failed", "SQLite/JDBCDriver error", "sqlite3.OperationalError",
			"SQLSTATE[42000]", "Unclosed quotation mark after", "quoted string not properly terminated",
			`near "foo": syntax error`, "System.Data.SqlClient.SqlException", "SqlException",
			"ſql ſyntax", // (?i) folds the long s
		},
		"debugPage": {
			"Werkzeug Debugger", "Traceback (most recent call last):", `<div class="Whoops container">`, `Whoops\Exception\Handler`,
			`Symfony\Component\HttpKernel\Exception\NotFoundHttpException`, "Action Controller: Exception caught",
			"You're seeing this error because you have DEBUG = True", "<title> Runtime Error </title>",
			"Server Error in '/' Application", "<b>Fatal error</b>", "<b>Parse error</b>", "Uncaught TypeError",
			"goroutine 1 [running]", " at System.Web.Mvc", " at java.lang.Thread.run",
		},
		"cloudKey": {
			"AKIAABCDEFGHIJKLMNOP", "ASIAABCDEFGHIJKLMNOP", "AIza" + strings.Repeat("a", 35), "ghp_" + strings.Repeat("a", 36),
			"github_pat_" + strings.Repeat("a", 22), "xoxb-1234567890", "sk_live_" + strings.Repeat("a", 20),
			"ya29." + strings.Repeat("a", 20), "SG." + strings.Repeat("a", 22) + "." + strings.Repeat("a", 43),
			"-----BEGIN RSA PRIVATE KEY-----", "-----BEGIN PRIVATE KEY-----",
		},
		"privateIP":    {"host 127.0.0.1 down", "at 10.1.2.3:", "172.16.0.9", "192.168.1.1", "169.254.169.254"},
		"mixedContent": {`<img SRC="HTTP://example.com/a.png">`, "<a href=http://example.com/>"},
	}
	for name, list := range samples {
		g := allGates[name]
		for _, s := range list {
			want := g.re.FindString(s)
			if want == "" {
				t.Fatalf("%s: sample %q does not match the pattern itself", name, s)
			}
			if got := g.find(s, foldForGate(s)); got != want {
				t.Errorf("%s: gate on %q = %q, want %q", name, s, got, want)
			}
		}
	}
}

// Random text built from pieces of the patterns: the gated result must always
// equal the regex alone, and hasDigitRun must agree with the card pattern.
func TestGatedPatternsAgreeWithTheRegexOnRandomText(t *testing.T) {
	pieces := []string{
		"sql", " syntax", "SQL ", "ORA-", "1234", "postgreSQL", " ERROR", "sqlite", "/", ".", "SQLSTATE[", "near \"", "\": ",
		"syntax error", "SqlException", "Werkzeug", " Debugger", "Traceback (most recent call last)", "goroutine ", "[", "Uncaught ",
		"Error", " at ", "System.", "java.", "lang.", "AKIA", "ABCDEFGHIJKLMNOP", "AIza", "ghp_", "xoxb-", "sk_live_", "SG.",
		"-----BEGIN ", "PRIVATE KEY-----", "127.", "10.", "0.", "172.", "16.", "192.168.", "169.254.", "src=", "href=\"", "HTTP://",
		"http://", "ſ", "K", "<b>Fatal error</b>", "<title>", "Runtime Error", "</title>", " ", "-", "a", "Z", "\n",
	}
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 20000; i++ {
		var b strings.Builder
		for n := rng.Intn(12); n >= 0; n-- {
			b.WriteString(pieces[rng.Intn(len(pieces))])
		}
		s := b.String()
		for name, g := range allGates {
			if got, want := g.find(s, foldForGate(s)), g.re.FindString(s); got != want {
				t.Fatalf("%s: gate on %q = %q, regex = %q", name, s, got, want)
			}
		}
	}
	digits := []string{"1", "23", "456", " ", "-", "  ", "--", "x", "7890"}
	for i := 0; i < 20000; i++ {
		var b strings.Builder
		for n := rng.Intn(16); n >= 0; n-- {
			b.WriteString(digits[rng.Intn(len(digits))])
		}
		s := b.String()
		if got, want := hasDigitRun(s, 13), ccCandidateRe.MatchString(s); got != want {
			t.Fatalf("hasDigitRun(%q) = %v, ccCandidateRe matches = %v", s, got, want)
		}
	}
}
