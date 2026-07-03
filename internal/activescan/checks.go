package activescan

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// Checks is the built-in active-check set.
var Checks = []Check{
	xssCheck, sqliErrorCheck, sqliBooleanCheck, sqliTimeCheck, sstiCheck,
	openRedirectCheck, pathTraversalCheck, cmdInjectionCheck,
	xxeCheck, crlfCheck,
	nosqlCheck, ldapCheck, xpathCheck,
	hostHeaderCheck, corsReflectionCheck,
}

// Reflected XSS: inject a marker wrapped in angle brackets/quotes and confirm it
// comes back unencoded (i.e. `<marker>` survives in the response).
var xssCheck = Check{
	ID: "active-xss", Class: "xss", Severity: "High", Title: "Reflected cross-site scripting (XSS)",
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
	ID: "active-sqli-error", Class: "sqli", Severity: "High", Title: "SQL injection (error-based)",
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
	ID: "active-sqli-boolean", Class: "sqli", Severity: "High", Title: "SQL injection (boolean-based)",
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
	ID: "active-ssti", Class: "ssti", Severity: "High", Title: "Server-side template injection (SSTI)",
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
	ID: "active-open-redirect", Class: "redirect", Severity: "Medium", Title: "Open redirect",
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
	ID: "active-lfi", Class: "lfi", Severity: "High", Title: "Path traversal / local file inclusion",
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
	ID: "active-cmdi", Class: "cmdi", Severity: "High", Title: "OS command injection (timing)",
	Fix: "Never pass input to a shell; use exec with an argument array and validate input.",
	Run: func(p Point, base Response, probe Prober) *Hit {
		if base.Status == 419 || base.Status == 401 || base.Status == 403 || base.Status == 502 {
			return nil
		}
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

// crlfHeaderName is the injected header the CRLF check looks for in response headers.
// Using a distinctive, unlikely-to-collide name minimises false positives.
const crlfHeaderName = "Interceptorcrlfcanary"

// crlfPayloads are the CRLF sequences tried, in several encodings, since web
// servers and reverse proxies normalise input differently:
//
//   - raw:            literal CR+LF (caught by servers that do no URL-decoding before header splitting)
//   - URL-encoded:    %0d%0a  (the most common server normalisation path)
//   - double-encoded: %250d%250a (bypasses a single decode layer)
//   - mixed:          %0d%0a mixed-case variants (case-insensitive hex decode)
//
// Each entry appends the injected header in the form "\r\nName: value" so that a
// vulnerable server inserts it as an extra response header.
var crlfPayloads = []string{
	"x\r\n" + crlfHeaderName + ": canary",
	"x\r\n" + strings.ToLower(crlfHeaderName) + ": canary",
	"x%0d%0a" + crlfHeaderName + "%3a%20canary",
	"x%0D%0A" + crlfHeaderName + "%3A%20canary",
	"x%250d%250a" + crlfHeaderName + "%253a%2520canary",
}

// crlfCheck detects HTTP response-splitting / header injection by injecting
// CR/LF sequences into query and body parameters and checking whether the
// injected header (crlfHeaderName) appears in the *response headers* — the
// high-signal, low-false-positive confirmation.  Body reflection alone is NOT
// treated as a hit; it could be benign text echoing.
//
// A baseline guard prevents mis-flagging an endpoint that already emits the
// canary header for some unrelated reason.
var crlfCheck = Check{
	ID: "active-crlf", Class: "crlf", Severity: "High", Title: "CRLF injection / HTTP response splitting",
	Fix: "Strip or reject CR (\\r, %0d) and LF (\\n, %0a) characters from any input that is reflected into HTTP response headers. Use a framework that sets headers via a typed API rather than string concatenation.",
	Run: func(p Point, base Response, probe Prober) *Hit {
		// Baseline guard: if the canary header is already present in the
		// baseline response headers we cannot attribute a later hit to us.
		if base.Headers != nil && base.Headers.Get(crlfHeaderName) != "" {
			return nil
		}

		for _, pl := range crlfPayloads {
			r := probe(pl)
			if r.Status == 0 {
				continue // probe did not execute (budget or error)
			}
			if r.Headers == nil {
				continue
			}
			// Primary signal: the injected header appears in the response headers.
			if r.Headers.Get(crlfHeaderName) != "" {
				return &Hit{
					Evidence: fmt.Sprintf("injected header %q appeared in response headers after payload %q", crlfHeaderName, trunc(pl, 60)),
					FlowID:   r.FlowID,
				}
			}
			// Secondary signal: a Set-Cookie we injected (some servers split on
			// the Set-Cookie header name rather than a generic header).
			if sc := r.Headers.Get("Set-Cookie"); strings.Contains(sc, crlfHeaderName) {
				return &Hit{
					Evidence: fmt.Sprintf("injected Set-Cookie containing %q appeared in response headers", crlfHeaderName),
					FlowID:   r.FlowID,
				}
			}
		}
		return nil
	},
}

// xxeCanary is the string the server echoes back if it resolves the internal entity.
const xxeCanary = "INTERCEPTOR_XXE_CANARY"

// xxeInjectDoctype rewrites an XML body to prepend a DOCTYPE declaration that
// defines an internal entity mapping "xxe" → xxeCanary, then injects &xxe; as
// the text of the first non-prolog element so that a resolving parser echoes the
// canary in its output.  Returns "" if the body cannot be injected safely.
func xxeInjectDoctype(body string) string {
	// We need a root element name to anchor the DOCTYPE. Find the first tag by
	// scanning for '<' followed by a letter (skips the XML declaration, comments,
	// and processing instructions).
	rootName := xmlFirstElement(body)
	if rootName == "" {
		return ""
	}

	doctype := fmt.Sprintf(
		`<!DOCTYPE %s [<!ENTITY xxe "%s">]>`,
		rootName, xxeCanary,
	)

	// Strip any existing DOCTYPE so we don't produce a second one.
	body = xmlStripDoctype(body)

	// Insert the DOCTYPE immediately after the XML declaration (if present) or
	// before the root element.
	declEnd := strings.Index(body, "?>")
	if declEnd >= 0 {
		pos := declEnd + 2
		return body[:pos] + "\n" + doctype + "\n" + body[pos:]
	}
	return doctype + "\n" + body
}

// xmlFirstElement returns the tag name of the first XML element in body
// (skipping the XML declaration, PIs, and comments).
func xmlFirstElement(body string) string {
	s := body
	for {
		idx := strings.Index(s, "<")
		if idx < 0 {
			return ""
		}
		rest := s[idx+1:]
		s = rest
		if len(rest) == 0 {
			return ""
		}
		// Skip XML declaration, processing instructions, comments, CDATA
		if strings.HasPrefix(rest, "?") || strings.HasPrefix(rest, "!") {
			continue
		}
		// Skip closing tags
		if strings.HasPrefix(rest, "/") {
			continue
		}
		// We found an opening tag; extract the name (up to whitespace, '>', or '/')
		end := strings.IndexAny(rest, " \t\r\n>/")
		if end < 0 {
			end = len(rest)
		}
		name := rest[:end]
		if name == "" {
			continue
		}
		return name
	}
}

// xmlStripDoctype removes an existing <!DOCTYPE ...> declaration from body so
// we can safely insert our own.
func xmlStripDoctype(body string) string {
	lower := strings.ToLower(body)
	start := strings.Index(lower, "<!doctype")
	if start < 0 {
		return body
	}
	// Find matching '>' — need to handle nested '[...]' internal subset.
	depth := 0
	for i := start; i < len(body); i++ {
		switch body[i] {
		case '[':
			depth++
		case ']':
			depth--
		case '>':
			if depth == 0 {
				return body[:start] + body[i+1:]
			}
		}
	}
	return body
}

// ---- time-based blind SQL injection ----

// sqliTimePayloads pair a 6-second delay payload with a 0-second control across
// the major dialects (MySQL, MSSQL, PostgreSQL), in both string and numeric
// contexts. A hit requires the slow payload to delay AND the matching control to
// return fast — the same differential guard the timing cmd-injection check uses.
var sqliTimePayloads = []struct{ slow, ctrl string }{
	{"' AND SLEEP(6)-- -", "' AND SLEEP(0)-- -"},                            // MySQL, string context
	{" AND SLEEP(6)", " AND SLEEP(0)"},                                      // MySQL, numeric context
	{"'; WAITFOR DELAY '0:0:6'-- ", "'; WAITFOR DELAY '0:0:0'-- "},          // MSSQL
	{"' || pg_sleep(6)-- ", "' || pg_sleep(0)-- "},                          // PostgreSQL, string context
	{"'||(SELECT 1 FROM PG_SLEEP(6))||'", "'||(SELECT 1 FROM PG_SLEEP(0))||'"}, // PostgreSQL, subselect
}

// Time-based blind SQL injection: when error- and boolean-based signals are absent
// (blind endpoint), a database sleep is the last reliable confirmation. Lower
// confidence by nature (timing), so it is differentially confirmed against a
// 0-delay control and a fast baseline.
var sqliTimeCheck = Check{
	ID: "active-sqli-time", Class: "sqli", Severity: "High", Title: "SQL injection (time-based blind)",
	Fix: "Use parameterized queries / prepared statements; never concatenate input into SQL.",
	Run: func(p Point, base Response, probe Prober) *Hit {
		switch base.Status {
		case 0, 401, 403, 419, 502:
			return nil
		}
		if base.Duration > 3*time.Second {
			return nil // baseline already slow; timing is unreliable
		}
		for _, pl := range sqliTimePayloads {
			r := probe(p.Value + pl.slow)
			if r.Status != 0 && r.Duration >= 5*time.Second {
				if c := probe(p.Value + pl.ctrl); c.Status != 0 && c.Duration < 3*time.Second {
					return &Hit{
						Evidence: fmt.Sprintf("`%s` delayed the response to %.1fs (baseline %.1fs, 0-delay control %.1fs)",
							trunc(pl.slow, 32), r.Duration.Seconds(), base.Duration.Seconds(), c.Duration.Seconds()),
						FlowID: r.FlowID,
					}
				}
			}
		}
		return nil
	},
}

// ---- NoSQL injection (error-based) ----

var nosqlErrRe = regexp.MustCompile(`(?i)(MongoError|MongoServerError|E11000 duplicate key|com\.mongodb|BSONObj|BSONError|CouchDB.{0,40}error|CastError|failed to parse.{0,40}(query|bson)|unexpected token.{0,20}in JSON|SyntaxError: Unexpected (token|end of))`)

// NoSQL injection: break the surrounding query/JSON with quote, backslash, and
// operator payloads and look for a MongoDB/driver parse error that wasn't already
// in the baseline. High-signal, low false positive (driver-specific error text).
var nosqlCheck = Check{
	ID: "active-nosql", Class: "nosqli", Severity: "High", Title: "NoSQL injection (error-based)",
	Fix: "Type-check and validate input; use a typed query builder and never pass raw user input into query operators (e.g. $where, $gt).",
	Run: func(p Point, base Response, probe Prober) *Hit {
		if nosqlErrRe.MatchString(base.Body) {
			return nil
		}
		for _, q := range []string{"'", "\"", "'\"", "\\", "'||'1'=='1", `{"$gt":""}`} {
			r := probe(p.Value + q)
			if r.Status != 0 {
				if m := nosqlErrRe.FindString(r.Body); m != "" {
					return &Hit{Evidence: "NoSQL/driver error after `" + trunc(q, 16) + "`: " + trunc(m, 80), FlowID: r.FlowID}
				}
			}
		}
		return nil
	},
}

// ---- LDAP injection (error-based) ----

var ldapErrRe = regexp.MustCompile(`(?i)(javax\.naming\.|LDAPException|com\.sun\.jndi\.ldap|LDAP: error code \d+|Invalid DN syntax|supplied argument is not a valid ldap|Bad search filter|ldap_search\(\)|A supplied argument to a function was invalid)`)

// LDAP injection: inject filter-breaking metacharacters and look for an LDAP
// library error absent from the baseline.
var ldapCheck = Check{
	ID: "active-ldap", Class: "ldapi", Severity: "High", Title: "LDAP injection (error-based)",
	Fix: "Escape LDAP filter metacharacters (RFC 4515) or use a parameterized directory API; never build filters via string concatenation.",
	Run: func(p Point, base Response, probe Prober) *Hit {
		if ldapErrRe.MatchString(base.Body) {
			return nil
		}
		for _, q := range []string{"*)(&", "*))(|(cn=*", "*)(uid=*))(|(uid=*", "admin*)((|userPassword=*)"} {
			r := probe(p.Value + q)
			if r.Status != 0 {
				if m := ldapErrRe.FindString(r.Body); m != "" {
					return &Hit{Evidence: "LDAP error after `" + trunc(q, 24) + "`: " + trunc(m, 80), FlowID: r.FlowID}
				}
			}
		}
		return nil
	},
}

// ---- XPath injection (error-based) ----

var xpathErrRe = regexp.MustCompile(`(?i)(XPathException|Expression must evaluate to a node-set|xmlXPathEval|SimpleXMLElement::xpath|System\.Xml\.XPath|MS\.Internal\.Xml|xpath_eval|Empty Path Expression|A closing bracket expected|Invalid predicate|org\.jaxen|net\.sf\.saxon|Unfinished qualified name)`)

// XPath injection: inject expression-breaking characters and look for an XPath
// engine error absent from the baseline.
var xpathCheck = Check{
	ID: "active-xpath", Class: "xpathi", Severity: "High", Title: "XPath injection (error-based)",
	Fix: "Use parameterized XPath (variable binding) or escape input; never concatenate user input into XPath expressions.",
	Run: func(p Point, base Response, probe Prober) *Hit {
		if xpathErrRe.MatchString(base.Body) {
			return nil
		}
		for _, q := range []string{"'", "\"", "']", "\"]", "' or '1'='1", "count(//*)"} {
			r := probe(p.Value + q)
			if r.Status != 0 {
				if m := xpathErrRe.FindString(r.Body); m != "" {
					return &Hit{Evidence: "XPath error after `" + q + "`: " + trunc(m, 80), FlowID: r.FlowID}
				}
			}
		}
		return nil
	},
}

// ---- Host header injection ----

const hostHeaderCanary = "interceptor-host.example"

// hostCanaryAbsRe matches the canary host in an absolute-URL context (//host or
// http(s)://host) — the strict signal that a spoofable X-Forwarded-Host was used
// to build a link, rather than merely echoed as harmless text.
var hostCanaryAbsRe = regexp.MustCompile(`(?i)(?:https?:)?//` + regexp.QuoteMeta(hostHeaderCanary))

// Host header injection: spoof X-Forwarded-Host and confirm the app builds an
// absolute URL from it — the root cause of password-reset poisoning and web cache
// poisoning. Fires only on the X-Forwarded-Host header point and only when the
// canary lands in a redirect Location or an absolute URL in the body.
var hostHeaderCheck = Check{
	ID: "active-host-header", Class: "host-header", Severity: "Medium", Title: "Host header injection (X-Forwarded-Host reflected into a URL)",
	Fix: "Build absolute URLs from a fixed, configured canonical hostname; never trust the Host / X-Forwarded-Host headers. Validate against an allow-list.",
	Run: func(p Point, base Response, probe Prober) *Hit {
		if p.Kind != "header" || !strings.EqualFold(p.Name, "X-Forwarded-Host") {
			return nil
		}
		if strings.Contains(base.Body, hostHeaderCanary) {
			return nil
		}
		r := probe(hostHeaderCanary)
		if r.Status == 0 {
			return nil
		}
		if r.Headers != nil {
			if loc := r.Headers.Get("Location"); strings.Contains(loc, hostHeaderCanary) {
				return &Hit{Evidence: "X-Forwarded-Host reflected into Location: " + trunc(loc, 80), FlowID: r.FlowID}
			}
		}
		if hostCanaryAbsRe.MatchString(r.Body) {
			return &Hit{Evidence: "X-Forwarded-Host reflected into an absolute URL in the response body", FlowID: r.FlowID}
		}
		return nil
	},
}

// ---- CORS misconfiguration (active) ----

const corsCanaryOrigin = "https://interceptor-cors.example"

// CORS misconfiguration: send an arbitrary attacker Origin and confirm the server
// reflects it into Access-Control-Allow-Origin (optionally with credentials).
// Fires only on the Origin header point.
var corsReflectionCheck = Check{
	ID: "active-cors-reflect", Class: "cors", Severity: "Medium", Title: "CORS misconfiguration (arbitrary Origin reflected)",
	Fix: "Validate the Origin header against a server-side allow-list before echoing it into Access-Control-Allow-Origin; never reflect arbitrary origins, especially with Allow-Credentials: true.",
	Run: func(p Point, base Response, probe Prober) *Hit {
		if p.Kind != "header" || !strings.EqualFold(p.Name, "Origin") {
			return nil
		}
		r := probe(corsCanaryOrigin)
		if r.Status == 0 || r.Headers == nil {
			return nil
		}
		if !strings.EqualFold(r.Headers.Get("Access-Control-Allow-Origin"), corsCanaryOrigin) {
			return nil
		}
		if strings.EqualFold(r.Headers.Get("Access-Control-Allow-Credentials"), "true") {
			return &Hit{
				Severity: "High",
				Title:    "CORS misconfiguration (arbitrary Origin reflected with credentials)",
				Evidence: "Access-Control-Allow-Origin reflects " + corsCanaryOrigin + " with Access-Control-Allow-Credentials: true",
				FlowID:   r.FlowID,
			}
		}
		return &Hit{Evidence: "Access-Control-Allow-Origin reflects arbitrary Origin " + corsCanaryOrigin, FlowID: r.FlowID}
	},
}

// XXE (XML External Entity) injection: inject a DOCTYPE with an internal entity
// and confirm entity resolution by checking whether the canary string is reflected
// in the response.  Only fires on "body" points whose value is XML-shaped (i.e.
// requests that carry an XML body, as detected by Points()).  The payload is
// deliberately safe — it uses an internal entity only; no SYSTEM/file reads.
var xxeCheck = Check{
	ID: "active-xxe", Class: "xxe", Severity: "High", Title: "XML External Entity injection (XXE)",
	Fix: "Disable DTD processing and external entity resolution in your XML parser (e.g. set FEATURE_SECURE_PROCESSING, disallow DOCTYPE declarations, or use a DTD-rejecting library).",
	Run: func(p Point, base Response, probe Prober) *Hit {
		// Only act on whole-body XML points produced by Points().
		if p.Kind != "body" || p.Name != "_xml" {
			return nil
		}
		// Guard: if the canary is already in the baseline the check is unreliable.
		if strings.Contains(base.Body, xxeCanary) {
			return nil
		}
		injected := xxeInjectDoctype(p.Value)
		if injected == "" {
			return nil // could not construct a safe payload
		}
		r := probe(injected)
		if r.Status != 0 && strings.Contains(r.Body, xxeCanary) {
			return &Hit{
				Evidence: fmt.Sprintf("internal entity &xxe; resolved: canary %q reflected in response", xxeCanary),
				FlowID:   r.FlowID,
			}
		}
		return nil
	},
}
