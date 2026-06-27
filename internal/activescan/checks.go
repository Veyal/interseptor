package activescan

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// Checks is the built-in active-check set (all 7 classes from PRD-0002).
var Checks = []Check{
	xssCheck, sqliErrorCheck, sqliBooleanCheck, sstiCheck,
	openRedirectCheck, pathTraversalCheck, cmdInjectionCheck,
}

// Reflected XSS: inject a marker wrapped in angle brackets/quotes and confirm it
// comes back unencoded (i.e. `<marker>` survives in the response).
var xssCheck = Check{
	Class: "xss", Severity: "High", Title: "Reflected cross-site scripting (XSS)",
	Fix: "Contextually output-encode user input and set a Content-Security-Policy.",
	Run: func(p Point, base Response, probe Prober) *Hit {
		m := mark()
		r := probe(`'"><` + m + `>`)
		if r.Status != 0 && strings.Contains(r.Body, "<"+m+">") {
			return &Hit{Evidence: "marker `<" + m + ">` reflected unencoded", FlowID: r.FlowID}
		}
		return nil
	},
}

var sqlErrRe = regexp.MustCompile(`(?i)(SQL syntax|mysql_fetch|valid MySQL result|ORA-\d{5}|PostgreSQL.{0,40}ERROR|SQLite[/.].{0,20}error|Unclosed quotation mark|quoted string not properly terminated|SQLSTATE\[|near ".{0,30}": syntax error|System\.Data\.SqlClient)`)

// Error-based SQL injection: append a quote and look for a DB error that wasn't
// already present in the baseline.
var sqliErrorCheck = Check{
	Class: "sqli", Severity: "High", Title: "SQL injection (error-based)",
	Fix: "Use parameterized queries / prepared statements; never concatenate input into SQL.",
	Run: func(p Point, base Response, probe Prober) *Hit {
		if sqlErrRe.MatchString(base.Body) {
			return nil // already errors without us — can't attribute
		}
		for _, q := range []string{"'", "\"", "')", "'\""} {
			r := probe(p.Value + q)
			if r.Status != 0 {
				if m := sqlErrRe.FindString(r.Body); m != "" {
					return &Hit{Evidence: "DB error after `" + q + "`: " + trunc(m, 80), FlowID: r.FlowID}
				}
			}
		}
		return nil
	},
}

// Boolean-based SQL injection: a true condition should match the baseline length
// while a false condition diverges noticeably.
var sqliBooleanCheck = Check{
	Class: "sqli", Severity: "High", Title: "SQL injection (boolean-based)",
	Fix: "Use parameterized queries / prepared statements.",
	Run: func(p Point, base Response, probe Prober) *Hit {
		tru := probe(p.Value + "' AND '1'='1")
		fls := probe(p.Value + "' AND '1'='2")
		if tru.Status == 0 || fls.Status == 0 || base.Status == 0 {
			return nil
		}
		lb, lt, lf := len(base.Body), len(tru.Body), len(fls.Body)
		// Length-based boolean detection is unreliable on tiny responses (a few
		// bytes' natural variation reads as a large relative divergence → false
		// positives), so require a non-trivial baseline.
		if lb < 64 {
			return nil
		}
		// true ≈ baseline, false clearly different from true
		if lt > 0 && absdiff(lt, lb) <= lb/20+8 && absdiff(lf, lt) >= lt/10+24 {
			return &Hit{
				Evidence: fmt.Sprintf("`AND 1=1` len=%d ≈ baseline %d, `AND 1=2` len=%d diverges", lt, lb, lf),
				FlowID:   fls.FlowID,
			}
		}
		return nil
	},
}

// Server-side template injection: 7*731 = 5117 is distinctive enough to avoid
// natural collisions.
var sstiCheck = Check{
	Class: "ssti", Severity: "High", Title: "Server-side template injection (SSTI)",
	Fix: "Never render user input as a template; if unavoidable, use a sandboxed engine.",
	Run: func(p Point, base Response, probe Prober) *Hit {
		if strings.Contains(base.Body, "5117") {
			return nil
		}
		for _, pl := range []string{"{{7*731}}", "${7*731}", "<%= 7*731 %>", "#{7*731}"} {
			r := probe(pl)
			if r.Status != 0 && strings.Contains(r.Body, "5117") {
				return &Hit{Evidence: "`" + pl + "` evaluated to 5117", FlowID: r.FlowID}
			}
		}
		return nil
	},
}

const redirectCanary = "interceptor-oob.example"

// Open redirect: confirm a 3xx Location (or it) points at our off-host canary.
var openRedirectCheck = Check{
	Class: "redirect", Severity: "Medium", Title: "Open redirect",
	Fix: "Validate redirect targets against an allow-list of known hosts/paths.",
	Run: func(p Point, base Response, probe Prober) *Hit {
		for _, pl := range []string{"https://" + redirectCanary + "/x", "//" + redirectCanary + "/x"} {
			r := probe(pl)
			if r.Status >= 300 && r.Status < 400 && r.Headers != nil {
				if loc := r.Headers.Get("Location"); loc != "" {
					if u, err := url.Parse(loc); err == nil && strings.EqualFold(u.Hostname(), redirectCanary) {
						return &Hit{Evidence: "redirects off-host to " + loc, FlowID: r.FlowID}
					}
				}
			}
		}
		return nil
	},
}

var passwdRe = regexp.MustCompile(`root:.*?:0:0:`)

// Path traversal / LFI: pull /etc/passwd via several traversal encodings.
var pathTraversalCheck = Check{
	Class: "lfi", Severity: "High", Title: "Path traversal / local file inclusion",
	Fix: "Reject `..` and absolute paths; resolve against a fixed base and allow-list filenames.",
	Run: func(p Point, base Response, probe Prober) *Hit {
		if passwdRe.MatchString(base.Body) {
			return nil
		}
		for _, pl := range []string{
			"../../../../../../etc/passwd",
			"....//....//....//....//etc/passwd",
			"..%2f..%2f..%2f..%2f..%2f..%2fetc%2fpasswd",
		} {
			r := probe(pl)
			if r.Status != 0 && passwdRe.MatchString(r.Body) {
				return &Hit{Evidence: "/etc/passwd returned via `" + trunc(pl, 28) + "`", FlowID: r.FlowID}
			}
		}
		return nil
	},
}

// OS command injection (timing): a `sleep` payload delays the response, confirmed
// against the baseline and a sleep-0 control. Lower confidence by nature.
var cmdInjectionCheck = Check{
	Class: "cmdi", Severity: "High", Title: "OS command injection (timing)",
	Fix: "Never pass input to a shell; use exec with an argument array and validate input.",
	Run: func(p Point, base Response, probe Prober) *Hit {
		if base.Duration > 3*time.Second {
			return nil // baseline already slow; timing is unreliable
		}
		for _, pl := range []string{";sleep 6", "|sleep 6", "`sleep 6`", "$(sleep 6)"} {
			r := probe(p.Value + pl)
			if r.Status != 0 && r.Duration >= 5*time.Second {
				// The sleep-0 control must actually run (Status!=0); a budget-
				// exhausted or errored control returns Duration 0, which would
				// otherwise pass `< 3s` and falsely "confirm" a slow endpoint.
				if c := probe(p.Value + ";sleep 0"); c.Status != 0 && c.Duration < 3*time.Second {
					return &Hit{
						Evidence: fmt.Sprintf("`sleep 6` delayed the response to %.1fs (baseline %.1fs)", r.Duration.Seconds(), base.Duration.Seconds()),
						FlowID:   r.FlowID,
					}
				}
			}
		}
		return nil
	},
}
