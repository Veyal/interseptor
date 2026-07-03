// Package scanner runs passive security checks over captured flows. It never
// sends traffic — it only inspects request/response metadata and bodies that
// were already recorded, keeping analysis off the proxy hot path.
package scanner

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/Veyal/interceptor/internal/store"
)

var (
	jwtRe      = regexp.MustCompile(`eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{6,}\.[A-Za-z0-9_-]{4,}`)
	passwordRe = regexp.MustCompile(`(?i)"?password"?\s*[:=]\s*"?[^"&\s,}]{3,}`)
	tokenRe    = regexp.MustCompile(`(?i)"(access_?token|token|session|secret|api_?key)"\s*:\s*"[^"]{8,}"`)
	versionRe  = regexp.MustCompile(`\d+\.\d+`)
	urlSensitiveRe = regexp.MustCompile(`(?i)[?&](access_?token|api_?key|token|session|password|secret|passwd|auth)=([^&\s]{6,})`)

	// mixedContentRe matches http:// scheme references inside common HTML resource-loading attributes.
	mixedContentRe = regexp.MustCompile(`(?i)(?:src|href)\s*=\s*["']?http://`)

	// dirListingRe matches the characteristic title of an auto-generated directory listing.
	dirListingRe = regexp.MustCompile(`(?i)<title>\s*index of /`)

	// privateIPRe matches RFC-1918 / loopback / link-local IP addresses disclosed in response text.
	privateIPRe = regexp.MustCompile(`(?:^|[^0-9.])` +
		`(?:127\.0\.0\.\d{1,3}` +
		`|10\.\d{1,3}\.\d{1,3}\.\d{1,3}` +
		`|172\.(?:1[6-9]|2\d|3[01])\.\d{1,3}\.\d{1,3}` +
		`|192\.168\.\d{1,3}\.\d{1,3}` +
		`|169\.254\.\d{1,3}\.\d{1,3})` +
		`(?:[^0-9.]|$)`)

	// dbErrorRe matches high-signal database error strings in a response body — a strong
	// passive SQL-injection indicator (user input reached a query un-parameterized).
	// Restricted to error-message phrasing (not bare function names) to keep false positives low.
	dbErrorRe = regexp.MustCompile(`(?i)(SQL syntax|You have an error in your SQL syntax|mysql_fetch|valid MySQL result|ORA-\d{4,5}|PostgreSQL.{0,40}ERROR|pg_query failed|SQLite[/:.\s].{0,20}error|sqlite3\.OperationalError|SQLSTATE\[|Unclosed quotation mark|quoted string not properly terminated|near ".{0,30}": syntax error|System\.Data\.SqlClient\.SqlException|SqlException)`)

	// cloudKeyRe matches high-confidence, format-distinctive credential/API-key and
	// private-key patterns. Only well-known fixed-shape tokens are included (never a
	// generic "long base64" heuristic) so a match is very unlikely to be noise.
	cloudKeyRe = regexp.MustCompile(
		`A(?:KIA|SIA|GPA|IDA|ROA|IPA|NPA|NVA|CCA)[0-9A-Z]{16}` + // AWS access key id
			`|AIza[0-9A-Za-z_\-]{35}` + // Google API key
			`|gh[posur]_[0-9A-Za-z]{36}` + // GitHub token (ghp_/gho_/ghs_/ghu_/ghr_)
			`|github_pat_[0-9A-Za-z_]{22,}` + // GitHub fine-grained PAT
			`|xox[baprs]-[0-9A-Za-z-]{10,48}` + // Slack token
			`|(?:sk|rk)_live_[0-9A-Za-z]{20,}` + // Stripe live secret / restricted key
			`|ya29\.[0-9A-Za-z_\-]{20,}` + // Google OAuth access token
			`|SG\.[0-9A-Za-z_\-]{22}\.[0-9A-Za-z_\-]{43}` + // SendGrid API key
			`|-----BEGIN (?:RSA |EC |OPENSSH |DSA |PGP )?PRIVATE KEY-----`) // private key block

	// ccCandidateRe finds a run of 13–19 digits (optionally space/dash grouped) that
	// is then Luhn-validated and prefix-checked in code, keeping card false positives low.
	ccCandidateRe = regexp.MustCompile(`(?:\d[ -]?){13,19}`)
	// ssnRe matches a US SSN in the dashed format; range validity is checked in code.
	ssnRe = regexp.MustCompile(`\b\d{3}-\d{2}-\d{4}\b`)

	// sourceMapRe matches an inline source-map reference comment in a JS response.
	sourceMapRe = regexp.MustCompile(`(?m)//[#@] sourceMappingURL=`)

	// debugPageRe matches high-signal framework debug/exception pages and language
	// stack traces (any status code — verbose-error only covers 5xx).
	debugPageRe = regexp.MustCompile(`(?i)(Werkzeug Debugger|Traceback \(most recent call last\)|class="Whoops|Whoops\\Exception|Symfony\\Component\\[A-Za-z]+\\.{0,40}Exception|Action Controller: Exception caught|You're seeing this error because you have DEBUG = True|<title>\s*Runtime Error\s*</title>|Server Error in '/' Application|<b>Fatal error</b>|<b>Parse error</b>|Uncaught (?:Error|Exception|TypeError)|goroutine \d+ \[|\bat (?:System\.[A-Za-z]|java\.[a-z]+\.))`)

	// cspWeakSourceRe matches a wildcard * in a script/default/object source directive.
	cspWeakSourceRe = regexp.MustCompile(`(?:default|script|object)-src[^;]*\*`)
	// hstsMaxAgeRe extracts the max-age seconds from an HSTS header.
	hstsMaxAgeRe = regexp.MustCompile(`(?i)max-age\s*=\s*"?(\d+)`)

	// formActionHTTPRe matches an HTML form whose action posts over plaintext HTTP.
	formActionHTTPRe = regexp.MustCompile(`(?i)<form[^>]+action\s*=\s*["']?http://`)
	// anchorTagRe extracts opening <a> tags for the reverse-tabnabbing check.
	anchorTagRe = regexp.MustCompile(`(?i)<a\b[^>]*>`)
	// insecureWSRe matches a plaintext (ws://) WebSocket URL.
	insecureWSRe = regexp.MustCompile(`(?i)\bws://[a-z0-9.\-]`)
	// jsIdentRe validates a JSONP callback value is a plain JS identifier/path.
	jsIdentRe = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$.]{0,60}$`)
)

const maxScanBytes = 256 * 1024 // cap how much of a body we inspect

// BuiltinCheck is metadata for a built-in passive check — shown in the Checks
// manager so users can see and toggle each one (built-ins can be disabled but
// not deleted; only Starlark checks are user-editable).
type BuiltinCheck struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Category    string `json:"category"`
	Severity    string `json:"severity"`
	Description string `json:"description"`
}

// Stable check IDs (referenced both by the gating logic and by BuiltinChecks).
const (
	checkPasswordInBody    = "password-in-body"
	checkTokenInResp       = "token-in-response"
	checkVerboseError      = "verbose-error"
	checkSecurityHeaders   = "security-headers"
	checkCorsWildcard      = "cors-wildcard"
	checkCorsCreds         = "cors-credentials"
	checkInsecureCookie    = "insecure-cookie"
	checkCookieSameSite    = "cookie-no-samesite"
	checkCacheableAuth     = "cacheable-auth"
	checkVersionDisclosure = "version-disclosure"
	checkReflectedParam    = "reflected-param"
	checkBasicAuth         = "basic-auth"
	checkSensitiveURL      = "sensitive-url-param"
	checkMixedContent      = "mixed-content"
	checkOpenRedirect      = "open-redirect"
	checkDirListing        = "directory-listing"
	checkDBError           = "db-error-sqli"
	checkPrivateIP         = "private-ip-disclosure"
	checkCloudKey          = "cloud-key-exposure"
	checkPII               = "pii-exposure"
	checkSourceMap         = "source-map-disclosure"
	checkDebugPage         = "framework-debug-page"
	checkWeakCSP           = "weak-csp"
	checkWeakHSTS          = "weak-hsts"
	checkInsecureForm      = "insecure-form-action"
	checkTabnabbing        = "reverse-tabnabbing"
	checkGraphQLIntrospect = "graphql-introspection"
	checkSameSiteNone      = "samesite-none-insecure"
	checkInsecureWS        = "insecure-websocket"
	checkJSONP             = "jsonp-endpoint"
)

// BuiltinChecks lists every built-in passive check. The Category groups them in
// the UI; Severity is the default the check emits.
var BuiltinChecks = []BuiltinCheck{
	{checkPasswordInBody, "Password transmitted in request body", "Secrets", "Medium", "A password field is sent in the request body."},
	{checkTokenInResp, "Session token leaked in response body", "Secrets", "High", "A bearer token / credential is returned in the response body."},
	{checkSensitiveURL, "Sensitive token or credential in URL", "Secrets", "Medium", "A credential-like parameter is in the request URL query string."},
	{checkSecurityHeaders, "Missing security response headers", "Headers", "Medium", "Bundles CSP, HSTS, X-Content-Type-Options, clickjacking & Referrer-Policy into one finding listing whichever are missing."},
	{checkCorsWildcard, "Overly permissive CORS policy", "CORS", "Medium", "Access-Control-Allow-Origin: * lets any origin read the resource."},
	{checkCorsCreds, "CORS with credentials enabled", "CORS", "High", "Wildcard or reflected Origin combined with Allow-Credentials: true."},
	{checkInsecureCookie, "Cookie set without Secure and HttpOnly", "Cookies", "Low", "A cookie lacks the Secure and/or HttpOnly attributes."},
	{checkCookieSameSite, "Cookie missing SameSite attribute", "Cookies", "Low", "A cookie is set without a SameSite attribute (CSRF surface)."},
	{checkCacheableAuth, "Authenticated response may be cached", "Cookies", "Low", "A cookie-setting response lacks Cache-Control: no-store/private."},
	{checkVersionDisclosure, "Server software version disclosed", "Disclosure", "Low", "Server / X-Powered-By / X-AspNet-Version reveals a version."},
	{checkVerboseError, "Verbose error discloses internal details", "Disclosure", "Medium", "A 5xx response leaks trace ids / stack frames."},
	{checkPrivateIP, "Internal IP address disclosed", "Disclosure", "Low", "The response body contains a private/loopback IP address."},
	{checkReflectedParam, "Request parameter reflected in HTML", "Injection", "Low", "A parameter is echoed verbatim into HTML — a possible reflected-XSS sink."},
	{checkDBError, "Possible SQL injection (DB error in response)", "Injection", "High", "The response contains a database error string — a strong SQLi signal."},
	{checkBasicAuth, "HTTP Basic authentication in use", "Auth", "Low", "Credentials are sent as reversible base64 (Authorization: Basic)."},
	{checkMixedContent, "Mixed content: HTTPS page loads HTTP resource", "Config", "Medium", "An HTTPS page references a resource over plain HTTP."},
	{checkOpenRedirect, "Potential open redirect via request parameter", "Redirect", "Medium", "A 3xx Location is influenced by a request parameter, off-host."},
	{checkDirListing, "Directory listing enabled", "Config", "Low", "The response looks like an auto-generated directory index."},
	{checkCloudKey, "Cloud/API credential or private key exposed", "Secrets", "High", "A response or request body contains a well-known API key, cloud credential, or private-key block."},
	{checkPII, "Personal data (card/SSN) exposed in response", "Disclosure", "Medium", "A Luhn-valid payment card number or a US SSN appears in the response body."},
	{checkSourceMap, "Source map reference exposed", "Disclosure", "Low", "A JavaScript response references a sourceMappingURL, which may expose original source."},
	{checkDebugPage, "Framework debug / stack-trace page", "Disclosure", "High", "The response is a framework debug page or language stack trace, leaking internals."},
	{checkWeakCSP, "Weak Content-Security-Policy", "Headers", "Medium", "A CSP is present but permits unsafe-inline, unsafe-eval, or a wildcard script source."},
	{checkWeakHSTS, "Weak HSTS policy (short max-age)", "Headers", "Low", "HSTS is set but its max-age is under 180 days, shrinking the HTTPS-enforcement window."},
	{checkInsecureForm, "Form submits over plaintext HTTP", "Config", "Medium", "An HTML form's action posts to an http:// URL, exposing submitted data."},
	{checkTabnabbing, "Reverse tabnabbing (target=_blank without noopener)", "Config", "Low", "An external link opens in a new tab without rel=noopener, exposing window.opener."},
	{checkGraphQLIntrospect, "GraphQL introspection enabled", "Disclosure", "Medium", "The response contains a GraphQL introspection schema, revealing the full API surface."},
	{checkSameSiteNone, "Cookie SameSite=None without Secure", "Cookies", "Medium", "A cookie declares SameSite=None but is not marked Secure."},
	{checkInsecureWS, "Insecure WebSocket (ws://) reference", "Config", "Low", "An HTTPS page references a plaintext ws:// WebSocket endpoint."},
	{checkJSONP, "JSONP endpoint reflects callback", "Disclosure", "Low", "A javascript response wraps data in a caller-supplied callback — cross-origin data theft surface."},
}

// Analyze runs all passive checks (none disabled) — kept for the existing 3-arg
// callers and tests. The real scan path uses AnalyzeWithDisabled so users can
// turn individual built-in checks off.
func Analyze(f *store.Flow, reqBody, resBody []byte) []store.Issue {
	return AnalyzeWithDisabled(f, reqBody, resBody, nil)
}

// AnalyzeWithDisabled runs the built-in passive checks, skipping any whose ID is
// in disabled. disabled may be nil to run everything.
func AnalyzeWithDisabled(f *store.Flow, reqBody, resBody []byte, disabled map[string]bool) []store.Issue {
	res := http.Header(f.ResHeaders)
	target := f.Method + " " + f.Host + f.Path
	req := clip(reqBody)
	resp := clip(resBody)
	on := func(id string) bool { return disabled == nil || !disabled[id] }

	var out []store.Issue
	add := func(sev, title, detail, evidence, fix string) {
		out = append(out, store.Issue{
			FlowID: f.ID, Severity: sev, Title: title, Target: target,
			Detail: detail, Evidence: evidence, Fix: fix,
		})
	}

	// Password in the request body.
	if on(checkPasswordInBody) {
		if m := passwordRe.FindString(req); m != "" {
			sev := "Medium"
			if f.Scheme == "http" {
				sev = "High"
			}
			add(sev, "Password transmitted in request body",
				"The request carries a password field in its body; over plaintext HTTP this is trivially sniffable, and even over TLS it should be kept out of logs.",
				trunc(m, 80),
				"Always submit credentials over HTTPS, keep the body out of access logs, and consider client-side hashing / SRP so the raw secret never transits.")
		}
	}

	// Session token / JWT in the response body.
	if on(checkTokenInResp) {
		if jwt := jwtRe.FindString(resp); jwt != "" {
			add("High", "Session token leaked in response body",
				"A bearer token (JWT) is returned in the response body where intermediaries or caches may retain it.",
				trunc(jwt, 48)+"…",
				"Deliver session tokens via a Set-Cookie with HttpOnly, Secure and SameSite=Strict instead of the JSON body.")
		} else if m := tokenRe.FindString(resp); m != "" {
			add("High", "Session token leaked in response body",
				"A credential-looking field is returned in the response body where intermediaries or caches may retain it.",
				trunc(m, 64),
				"Return session tokens via a Secure, HttpOnly cookie rather than the response body.")
		}
	}

	// Verbose error disclosure.
	if on(checkVerboseError) {
		if f.Status >= 500 && containsAny(resp, "traceId", "trace_id", "stacktrace", "stack", "exception", " at ") {
			add("Medium", "Verbose error discloses internal details",
				"A server error response leaks internal diagnostics (trace identifiers / stack frames) that aid reconnaissance of the backend.",
				trunc(firstMatch(resp, "traceId", "trace_id", "exception", "stack"), 80),
				"Return a generic error to clients and keep trace identifiers and stack traces server-side in logs only.")
		}
	}

	// Security response headers — MERGED into a single finding. The previous
	// behaviour emitted one issue per missing header (CSP, HSTS, nosniff,
	// clickjacking, Referrer-Policy), which drowned the issue list in near-
	// duplicates. Now we collect whichever are missing and emit one finding that
	// lists them, at Medium if CSP or HSTS is among them (they materially raise
	// the XSS / downgrade blast radius) otherwise Low.
	if on(checkSecurityHeaders) {
		var missing []string
		if isHTML(res, f.Mime) && res.Get("Content-Security-Policy") == "" {
			missing = append(missing, "Content-Security-Policy")
		}
		if f.Scheme == "https" && res.Get("Strict-Transport-Security") == "" {
			missing = append(missing, "Strict-Transport-Security (HSTS)")
		}
		if isHTML(res, f.Mime) || containsAny(f.Mime, "javascript") {
			if !strings.Contains(strings.ToLower(res.Get("X-Content-Type-Options")), "nosniff") {
				missing = append(missing, "X-Content-Type-Options: nosniff")
			}
		}
		if isHTML(res, f.Mime) {
			csp := strings.ToLower(res.Get("Content-Security-Policy"))
			if res.Get("X-Frame-Options") == "" && !strings.Contains(csp, "frame-ancestors") {
				missing = append(missing, "X-Frame-Options / CSP frame-ancestors (clickjacking)")
			}
		}
		if isHTML(res, f.Mime) && res.Get("Referrer-Policy") == "" {
			missing = append(missing, "Referrer-Policy")
		}
		if len(missing) > 0 {
			sev := "Low"
			for _, m := range missing {
				if strings.Contains(m, "Content-Security-Policy") || strings.Contains(m, "Strict-Transport-Security") {
					sev = "Medium"
					break
				}
			}
			add(sev, "Missing security response headers",
				"The response is missing one or more standard security response headers ("+strings.Join(missing, ", ")+
					"). Each weakens a different defence-in-depth control (XSS containment, downgrade protection, MIME sniffing, clickjacking, Referer leakage).",
				"Missing: "+strings.Join(missing, ", "),
				"Send the missing headers — CSP, HSTS on HTTPS, X-Content-Type-Options: nosniff, X-Frame-Options (or CSP frame-ancestors), Referrer-Policy: strict-origin-when-cross-origin.")
		}
	}

	// Wildcard CORS.
	if on(checkCorsWildcard) {
		if res.Get("Access-Control-Allow-Origin") == "*" {
			add("Medium", "Overly permissive CORS policy",
				"Access-Control-Allow-Origin: * lets any origin read this resource.",
				"Access-Control-Allow-Origin: *",
				"Replace the wildcard with an explicit allow-list of trusted origins.")
		}
	}

	// CORS with credentials — wildcard or reflected origin combined with Allow-Credentials: true.
	if on(checkCorsCreds) {
		if strings.EqualFold(res.Get("Access-Control-Allow-Credentials"), "true") {
			acao := res.Get("Access-Control-Allow-Origin")
			reqOrigin := http.Header(f.ReqHeaders).Get("Origin")
			switch {
			case acao == "*":
				add("High", "CORS wildcard with credentials enabled",
					"Access-Control-Allow-Origin: * is set alongside Access-Control-Allow-Credentials: true. "+
						"Although browsers block this combination, it is a server-side misconfiguration that signals the developer intended open cross-origin access with credentials.",
					"Access-Control-Allow-Origin: * | Access-Control-Allow-Credentials: true",
					"Restrict Access-Control-Allow-Origin to a specific trusted origin when credentials are required; never use * with credentials.")
			case reqOrigin != "" && acao == reqOrigin:
				add("High", "CORS reflects request Origin with credentials enabled",
					"The server echoes back the caller's Origin header as Access-Control-Allow-Origin and also sets Access-Control-Allow-Credentials: true. "+
						"Any origin — including attacker-controlled pages — can make credentialed cross-origin requests and read the response.",
					"Access-Control-Allow-Origin: "+acao+" | Access-Control-Allow-Credentials: true",
					"Validate the Origin against an explicit server-side allow-list before reflecting it; do not echo arbitrary origins.")
			}
		}
	}

	// Insecure cookies (missing Secure and/or HttpOnly).
	if on(checkInsecureCookie) {
		for _, c := range res.Values("Set-Cookie") {
			lc := strings.ToLower(c)
			if !strings.Contains(lc, "secure") || !strings.Contains(lc, "httponly") {
				add("Low", "Cookie set without Secure and HttpOnly",
					"A cookie is set without both the Secure and HttpOnly attributes, exposing it to plaintext interception or theft via XSS.",
					trunc(c, 80),
					"Set cookies with Secure; HttpOnly; SameSite=Strict (or Lax).")
				break
			}
		}
	}

	// Cookie missing SameSite.
	if on(checkCookieSameSite) {
		for _, c := range res.Values("Set-Cookie") {
			lc := strings.ToLower(c)
			if !strings.Contains(lc, "samesite") {
				add("Low", "Cookie missing SameSite attribute",
					"A cookie is set without a SameSite attribute. Browsers that do not default to Lax will send it on cross-site requests, enabling CSRF attacks.",
					trunc(c, 80),
					"Add SameSite=Strict (or Lax) to all cookies. Use Strict for session tokens.")
				break
			}
		}
	}

	// Authenticated response cached without Cache-Control: no-store / private.
	if on(checkCacheableAuth) {
		if len(res.Values("Set-Cookie")) > 0 {
			cc := strings.ToLower(res.Get("Cache-Control"))
			if !strings.Contains(cc, "no-store") && !strings.Contains(cc, "private") {
				add("Low", "Authenticated response may be cached by shared proxies",
					"The response sets a cookie but does not include Cache-Control: no-store or private. "+
						"A shared proxy or CDN node may cache and serve this response to other users.",
					"Set-Cookie present; Cache-Control: "+res.Get("Cache-Control"),
					"Add Cache-Control: no-store (or at minimum private) to responses that set authentication cookies.")
			}
		}
	}

	// Server software version disclosure.
	if on(checkVersionDisclosure) {
		for _, h := range []string{"Server", "X-Powered-By", "X-AspNet-Version"} {
			if v := res.Get(h); v != "" && versionRe.MatchString(v) {
				add("Low", "Server software version disclosed",
					"A response header reveals the server software and version, aiding targeted exploitation.",
					h+": "+v,
					"Suppress or genericize version-bearing headers ("+h+") at the edge.")
				break
			}
		}
	}

	// Request parameter reflected verbatim in an HTML response (possible XSS sink).
	if on(checkReflectedParam) {
		if isHTML(res, f.Mime) {
			if name, val, ok := reflectedParam(f.Path, req, resp); ok {
				add("Low", "Request parameter reflected in HTML response",
					"A request parameter is echoed verbatim into an HTML response. If it is not contextually output-encoded this is a reflected-XSS sink — confirm by sending a marker payload.",
					trunc(name+"="+val, 80),
					"HTML-encode user input on output (and set a Content-Security-Policy); verify the value cannot break out of its HTML/JS/attribute context.")
			}
		}
	}

	// Possible SQL injection — a database error string in the response body.
	if on(checkDBError) {
		if m := dbErrorRe.FindString(resp); m != "" {
			add("High", "Possible SQL injection (DB error in response)",
				"The response contains a database error message. This strongly suggests user input reached a SQL query without parameterization — inject a single quote and confirm the error changes to validate SQL injection.",
				trunc(m, 80),
				"Use parameterized queries / prepared statements everywhere user input reaches SQL; never string-concatenate. Validate and normalize input, and return generic errors to clients.")
		}
	}

	// Internal IP address disclosed in the response body (topology leak).
	if on(checkPrivateIP) {
		if m := privateIPRe.FindString(resp); m != "" {
			add("Low", "Internal IP address disclosed",
				"The response body contains what looks like a private/internal IP address (RFC1918 / loopback / link-local), revealing internal network topology.",
				strings.TrimSpace(m),
				"Avoid echoing internal hostnames or IP addresses to clients; keep them server-side.")
		}
	}

	// HTTP Basic authentication.
	if on(checkBasicAuth) {
		if av := http.Header(f.ReqHeaders).Get("Authorization"); strings.HasPrefix(strings.ToLower(av), "basic ") {
			sev := "Low"
			if f.Scheme == "http" {
				sev = "High"
			}
			add(sev, "HTTP Basic authentication in use",
				"The request authenticates with HTTP Basic, which transmits credentials as reversible base64. Over plaintext HTTP they are exposed to any on-path observer; even over TLS they are replayable and sent on every request.",
				"Authorization: Basic …",
				"Prefer a token/session-cookie scheme; if Basic is required, enforce HTTPS and short-lived credentials.")
		}
	}

	// Sensitive token or credential in the request URL query string.
	if on(checkSensitiveURL) {
		if m := urlSensitiveRe.FindString(f.Path); m != "" {
			kv := strings.SplitN(strings.TrimLeft(m, "?&"), "=", 2)
			paramName := kv[0]
			add("Medium", "Sensitive token or credential in URL",
				"A credential-like parameter ("+paramName+") is present in the request URL query string. "+
					"Query parameters are recorded in server access logs, browser history, proxy logs, and Referer headers sent to third parties.",
				trunc(m, 80),
				"Pass credentials in the request body (POST) or as Authorization/custom headers, never in the URL.")
		}
	}

	// Mixed content — HTTPS page references HTTP resources.
	if on(checkMixedContent) {
		if f.Scheme == "https" && isHTML(res, f.Mime) {
			if m := mixedContentRe.FindString(resp); m != "" {
				add("Medium", "Mixed content: HTTPS page loads HTTP resource",
					"An HTTPS page references at least one resource (script/style/iframe/image) over "+
						"plain HTTP. Active mixed content (scripts/styles) is blocked by modern browsers, "+
						"but its presence indicates a configuration defect; passive mixed content (images) "+
						"is still loaded and can be replaced by a network attacker.",
					trunc(m, 80),
					"Update all sub-resource URLs to HTTPS, or use protocol-relative URLs (//…).")
			}
		}
	}

	// Open redirect — 3xx Location influenced by a request parameter, off-host.
	if on(checkOpenRedirect) {
		if f.Status >= 300 && f.Status < 400 {
			if loc := res.Get("Location"); loc != "" {
				if name, val, ok := openRedirectParam(f.Host, f.Path, req, loc); ok {
					add("Medium", "Potential open redirect via request parameter",
						"A redirect response sets a Location header whose value is influenced by the request parameter '"+name+"'. "+
							"If the server does not validate the destination, an attacker can craft a link that redirects victims to an attacker-controlled site.",
						trunc(name+"="+val+" → Location: "+loc, 120),
						"Validate redirect destinations against an explicit allow-list of trusted URLs; never accept full URLs from user-controlled input as redirect targets.")
				}
			}
		}
	}

	// Directory listing exposure.
	if on(checkDirListing) {
		if strings.Contains(resp, "<a href=") && dirListingRe.MatchString(resp) {
			add("Low", "Directory listing enabled",
				"The response appears to be an auto-generated directory index (e.g. Apache/nginx autoindex). Directory listings expose file and directory names, software paths, and may reveal sensitive files.",
				trunc(dirListingRe.FindString(resp), 80),
				"Disable directory listing in the web-server configuration (e.g. Options -Indexes in Apache, autoindex off in nginx) and ensure sensitive files are not web-accessible.")
		}
	}

	// Cloud/API credential or private key exposed (in either body).
	if on(checkCloudKey) {
		if m := cloudKeyRe.FindString(resp); m != "" {
			add("High", "Cloud/API credential or private key exposed in response",
				"The response body contains what looks like a live API key, cloud credential, or private-key block. Secrets returned to clients are cached, logged, and trivially exfiltrated.",
				redactSecret(m),
				"Never return secrets to clients. Revoke and rotate the exposed credential, and move it server-side into a secrets manager.")
		} else if m := cloudKeyRe.FindString(req); m != "" {
			add("High", "Cloud/API credential or private key exposed in request",
				"The request body carries what looks like an API key, cloud credential, or private-key block. Even over TLS these end up in proxy/access logs and browser history.",
				redactSecret(m),
				"Send credentials via short-lived tokens in Authorization headers, keep them out of bodies and logs, and rotate anything exposed.")
		}
	}

	// Personal data (payment card / SSN) in the response.
	if on(checkPII) {
		if m := findPaymentCard(resp); m != "" {
			add("Medium", "Payment card number exposed in response",
				"The response body contains a Luhn-valid payment card number. Exposing PAN data is a PCI-DSS violation and a serious privacy risk.",
				m, // already masked
				"Never return full PANs to clients; mask all but the last four digits and keep card data in a PCI-compliant vault.")
		} else if m := findSSN(resp); m != "" {
			add("Medium", "US Social Security Number exposed in response",
				"The response body contains a string matching a valid US Social Security Number format.",
				maskSSN(m),
				"Do not return SSNs to clients; mask or omit them and restrict access server-side.")
		}
	}

	// Source-map reference exposed in a JS response.
	if on(checkSourceMap) {
		if (containsAny(f.Mime, "javascript") || containsAny(res.Get("Content-Type"), "javascript")) && sourceMapRe.MatchString(resp) {
			add("Low", "Source map reference exposed",
				"A JavaScript response references a sourceMappingURL. If the .map file is reachable it exposes original, unminified source (and sometimes comments/paths) to anyone.",
				trunc(sourceMapRe.FindString(resp), 80),
				"Strip sourceMappingURL comments from production bundles, or ensure .map files are not served publicly.")
		}
	}

	// Framework debug page / language stack trace (any status).
	if on(checkDebugPage) {
		if m := debugPageRe.FindString(resp); m != "" {
			add("High", "Framework debug page or stack trace disclosed",
				"The response looks like a framework debug page or a language stack trace. These leak source paths, framework versions, SQL, and sometimes an interactive console — a major reconnaissance and, for some debuggers, RCE surface.",
				trunc(m, 80),
				"Disable debug mode in production (e.g. Flask DEBUG=False, Django DEBUG=False, display_errors=Off, ASP.NET customErrors) and return generic error pages.")
		}
	}

	// Weak Content-Security-Policy (present but permissive).
	if on(checkWeakCSP) {
		if csp := res.Get("Content-Security-Policy"); csp != "" {
			lc := strings.ToLower(csp)
			var weak []string
			if strings.Contains(lc, "unsafe-inline") {
				weak = append(weak, "'unsafe-inline'")
			}
			if strings.Contains(lc, "unsafe-eval") {
				weak = append(weak, "'unsafe-eval'")
			}
			if cspWeakSourceRe.MatchString(lc) {
				weak = append(weak, "a wildcard * source")
			}
			if len(weak) > 0 {
				add("Medium", "Weak Content-Security-Policy",
					"A Content-Security-Policy is set but permits "+strings.Join(weak, ", ")+
						", which largely defeats its XSS-containment purpose (inline/eval'd or arbitrarily-sourced scripts still execute).",
					trunc(csp, 120),
					"Remove 'unsafe-inline'/'unsafe-eval' and wildcard sources; use nonces or hashes for inline scripts and an explicit source allow-list.")
			}
		}
	}

	// Weak HSTS policy — present but with a short max-age. A short window means the
	// browser stops enforcing HTTPS soon after the last visit, re-opening the SSL-
	// strip surface. (Missing includeSubDomains alone is deliberately NOT flagged —
	// it is extremely common and would drown the list; the fix still recommends it.)
	if on(checkWeakHSTS) {
		if f.Scheme == "https" {
			if hsts := res.Get("Strict-Transport-Security"); hsts != "" {
				if secs := hstsMaxAge(hsts); secs > 0 && secs < 15552000 { // under 180 days
					add("Low", "Weak HSTS policy (short max-age)",
						"Strict-Transport-Security is present but its max-age is short, so the browser stops enforcing HTTPS soon after the last visit — re-opening the SSL-strip / downgrade surface.",
						trunc(hsts, 80),
						"Set Strict-Transport-Security: max-age=63072000; includeSubDomains; preload.")
				}
			}
		}
	}

	// Form submitting over plaintext HTTP from an HTTPS page.
	if on(checkInsecureForm) {
		if f.Scheme == "https" && isHTML(res, f.Mime) {
			if m := formActionHTTPRe.FindString(resp); m != "" {
				add("Medium", "Form submits over plaintext HTTP",
					"An HTML form on this HTTPS page posts to an http:// action URL. The submitted data (potentially credentials) is sent in cleartext and can be read or modified by any on-path attacker.",
					trunc(m, 80),
					"Point every form action at an https:// URL.")
			}
		}
	}

	// Reverse tabnabbing — external target=_blank link without rel=noopener.
	if on(checkTabnabbing) {
		if isHTML(res, f.Mime) {
			if m := findTabnabbingAnchor(resp); m != "" {
				add("Low", "Reverse tabnabbing (target=_blank without noopener)",
					"An external link opens in a new tab (target=_blank) without rel=noopener. The opened page can rewrite window.opener.location to redirect this tab to a phishing page.",
					trunc(m, 80),
					"Add rel=\"noopener noreferrer\" to every target=_blank link (modern browsers imply noopener, but set it explicitly for older ones).")
			}
		}
	}

	// GraphQL introspection enabled (schema returned to the client).
	if on(checkGraphQLIntrospect) {
		if strings.Contains(resp, `"__schema"`) && strings.Contains(resp, `"queryType"`) {
			add("Medium", "GraphQL introspection enabled",
				"The response contains a GraphQL introspection result (__schema/queryType). Introspection hands an attacker the entire API surface — every type, field, and mutation.",
				`"__schema" present in response`,
				"Disable introspection in production (e.g. Apollo introspection:false) or restrict it to authenticated internal users.")
		}
	}

	// Cookie with SameSite=None but no Secure.
	if on(checkSameSiteNone) {
		for _, c := range res.Values("Set-Cookie") {
			lc := strings.ToLower(c)
			if strings.Contains(lc, "samesite=none") && !strings.Contains(lc, "secure") {
				add("Medium", "Cookie SameSite=None without Secure",
					"A cookie is set with SameSite=None but without the Secure attribute. Browsers reject this combination (the cookie may be dropped), and it signals the cookie was intended for cross-site use without transport protection.",
					trunc(c, 80),
					"Always pair SameSite=None with Secure; otherwise use SameSite=Lax or Strict.")
				break
			}
		}
	}

	// Insecure WebSocket (ws://) reference on an HTTPS page.
	if on(checkInsecureWS) {
		if f.Scheme == "https" && (isHTML(res, f.Mime) || containsAny(f.Mime, "javascript")) {
			if m := insecureWSRe.FindString(resp); m != "" {
				add("Low", "Insecure WebSocket (ws://) reference",
					"An HTTPS page references a plaintext ws:// WebSocket endpoint. The WebSocket data is unencrypted and readable/modifiable by an on-path attacker (and browsers block ws:// from https pages).",
					trunc(m, 80),
					"Use wss:// (WebSocket over TLS) for all WebSocket connections from secure pages.")
			}
		}
	}

	// JSONP endpoint reflecting a caller-supplied callback.
	if on(checkJSONP) {
		if containsAny(f.Mime, "javascript") || containsAny(res.Get("Content-Type"), "javascript") {
			if name, cb := jsonpCallback(f.Path, resp); cb != "" {
				add("Low", "JSONP endpoint reflects callback",
					"This endpoint returns JavaScript that wraps its data in a caller-supplied callback function ("+name+"). Any site can include it as a <script> and read the data cross-origin, bypassing the same-origin policy.",
					trunc(cb+"(…", 60),
					"Prefer CORS-guarded JSON over JSONP; if JSONP is required, allow-list callback names and never expose sensitive data through it.")
			}
		}
	}

	return out
}

// redactSecret shows only the leading, format-identifying portion of a matched
// secret so the finding is actionable without printing the full credential.
func redactSecret(s string) string {
	if strings.HasPrefix(s, "-----BEGIN") {
		return trunc(s, 40)
	}
	if len(s) <= 10 {
		return s
	}
	return s[:10] + "…(redacted)"
}

// findPaymentCard returns a masked payment card number found in s (Luhn-valid,
// known IIN prefix), or "".
func findPaymentCard(s string) string {
	for _, cand := range ccCandidateRe.FindAllString(s, 32) {
		digits := stripNonDigits(cand)
		if len(digits) < 13 || len(digits) > 19 {
			continue
		}
		if !luhnValid(digits) || !knownCardPrefix(digits) {
			continue
		}
		return digits[:6] + strings.Repeat("•", len(digits)-10) + digits[len(digits)-4:]
	}
	return ""
}

// findSSN returns the first range-valid US SSN in s, or "".
func findSSN(s string) string {
	for _, m := range ssnRe.FindAllString(s, 32) {
		if validSSN(m) {
			return m
		}
	}
	return ""
}

func maskSSN(s string) string {
	if len(s) == 11 { // ddd-dd-dddd
		return "•••-••-" + s[7:]
	}
	return "•••-••-••••"
}

func stripNonDigits(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteByte(byte(r))
		}
	}
	return b.String()
}

func luhnValid(digits string) bool {
	sum, alt := 0, false
	for i := len(digits) - 1; i >= 0; i-- {
		n := int(digits[i] - '0')
		if alt {
			n *= 2
			if n > 9 {
				n -= 9
			}
		}
		sum += n
		alt = !alt
	}
	return sum%10 == 0
}

// knownCardPrefix requires a recognised card IIN so random Luhn-valid digit runs
// (e.g. order ids that happen to pass Luhn) don't get flagged.
func knownCardPrefix(d string) bool {
	switch {
	case d[0] == '4' && (len(d) == 13 || len(d) == 16 || len(d) == 19): // Visa
		return true
	case len(d) == 16 && d[0] == '5' && d[1] >= '1' && d[1] <= '5': // Mastercard 51-55
		return true
	case len(d) == 16 && strings.HasPrefix(d, "2") && d[1] >= '2' && d[1] <= '7': // Mastercard 2221-2720 (approx)
		return true
	case len(d) == 15 && (strings.HasPrefix(d, "34") || strings.HasPrefix(d, "37")): // Amex
		return true
	case len(d) == 16 && (strings.HasPrefix(d, "6011") || strings.HasPrefix(d, "65")): // Discover
		return true
	}
	return false
}

// validSSN rejects the well-known invalid SSN ranges.
func validSSN(s string) bool {
	// s is ddd-dd-dddd
	area, grp, ser := s[0:3], s[4:6], s[7:11]
	if area == "000" || area == "666" || area[0] == '9' {
		return false
	}
	if grp == "00" || ser == "0000" {
		return false
	}
	return true
}

// hstsMaxAge parses the max-age seconds from an HSTS header (0 if absent).
func hstsMaxAge(h string) int {
	m := hstsMaxAgeRe.FindStringSubmatch(h)
	if len(m) < 2 {
		return 0
	}
	n := 0
	for _, r := range m[1] {
		n = n*10 + int(r-'0')
		if n > 1<<30 { // clamp; anything this large is "strong" anyway
			return 1 << 30
		}
	}
	return n
}

// findTabnabbingAnchor returns the first <a> tag that opens an external http(s)
// link in a new tab without rel=noopener/noreferrer, or "".
func findTabnabbingAnchor(html string) string {
	for _, tag := range anchorTagRe.FindAllString(html, 64) {
		lc := strings.ToLower(tag)
		if !strings.Contains(lc, "target=") || !strings.Contains(lc, "_blank") {
			continue
		}
		if !strings.Contains(lc, "href=\"http") && !strings.Contains(lc, "href='http") && !strings.Contains(lc, "href=http") {
			continue
		}
		if strings.Contains(lc, "noopener") || strings.Contains(lc, "noreferrer") {
			continue
		}
		return tag
	}
	return ""
}

// jsonpCallback returns the callback param name and value if the response looks
// like JSONP wrapping data in a caller-supplied callback, else ("", "").
func jsonpCallback(path, resp string) (name, cb string) {
	for _, n := range []string{"callback", "cb", "jsonp", "jsonpcallback", "jsoncallback"} {
		v := queryParam(path, n)
		if v == "" || !jsIdentRe.MatchString(v) {
			continue
		}
		t := strings.TrimLeft(resp, " \t\r\n")
		t = strings.TrimPrefix(t, "/**/")
		t = strings.TrimLeft(t, " \t")
		if strings.HasPrefix(t, v+"(") {
			return n, v
		}
	}
	return "", ""
}

// queryParam extracts a single query parameter value from a request path+query.
func queryParam(path, key string) string {
	i := strings.IndexByte(path, '?')
	if i < 0 {
		return ""
	}
	vals, err := url.ParseQuery(path[i+1:])
	if err != nil {
		return ""
	}
	return vals.Get(key)
}

// openRedirectParam checks whether any request query or body parameter value
// appears verbatim in the redirect Location header AND points off-host.
func openRedirectParam(host, path, body, location string) (name, val string, ok bool) {
	var pairs [][2]string
	collect := func(q string) {
		for _, kv := range strings.Split(q, "&") {
			if kv == "" {
				continue
			}
			k, v, _ := strings.Cut(kv, "=")
			if dec, err := url.QueryUnescape(v); err == nil {
				v = dec
			}
			if len(v) < 8 {
				continue
			}
			lk := strings.ToLower(k)
			looksLikeURL := strings.HasPrefix(v, "http") || strings.HasPrefix(v, "//")
			redirectParamName := lk == "next" || lk == "redirect" || lk == "redirect_uri" ||
				lk == "return" || lk == "returnto" || lk == "url" || lk == "goto" ||
				lk == "continue" || lk == "dest" || lk == "destination" || lk == "target"
			if looksLikeURL || redirectParamName {
				pairs = append(pairs, [2]string{k, v})
			}
		}
	}
	if i := strings.IndexByte(path, '?'); i >= 0 {
		collect(path[i+1:])
	}
	collect(body)

	locLower := strings.ToLower(location)
	for _, p := range pairs {
		if !strings.Contains(location, p[1]) {
			continue
		}
		isAbsolute := strings.HasPrefix(locLower, "http") || strings.HasPrefix(locLower, "//")
		if !isAbsolute {
			continue
		}
		if strings.Contains(locLower, strings.ToLower(host)) {
			continue
		}
		return p[0], p[1], true
	}
	return "", "", false
}

// reflectedParam returns the first query/body parameter whose value (≥6 chars,
// containing a letter) appears verbatim in resp — a candidate reflected-XSS sink.
func reflectedParam(path, body, resp string) (name, val string, ok bool) {
	var pairs [][2]string
	collect := func(q string) {
		for _, kv := range strings.Split(q, "&") {
			if kv == "" {
				continue
			}
			k, v, _ := strings.Cut(kv, "=")
			if dec, err := url.QueryUnescape(v); err == nil {
				v = dec
			}
			if len(v) >= 6 && hasLetter(v) {
				pairs = append(pairs, [2]string{k, v})
			}
		}
	}
	if i := strings.IndexByte(path, '?'); i >= 0 {
		collect(path[i+1:])
	}
	collect(body)
	for _, p := range pairs {
		if strings.Contains(resp, p[1]) {
			return p[0], p[1], true
		}
	}
	return "", "", false
}

func hasLetter(s string) bool {
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			return true
		}
	}
	return false
}

func clip(b []byte) string {
	if len(b) > maxScanBytes {
		b = b[:maxScanBytes]
	}
	return string(b)
}

func isHTML(h http.Header, mime string) bool {
	ct := h.Get("Content-Type")
	return strings.Contains(ct, "text/html") || strings.Contains(mime, "text/html")
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func firstMatch(s string, subs ...string) string {
	for _, sub := range subs {
		if i := strings.Index(s, sub); i >= 0 {
			end := i + 60
			if end > len(s) {
				end = len(s)
			}
			return s[i:end]
		}
	}
	return ""
}

func trunc(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
