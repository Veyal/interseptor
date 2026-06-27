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
	jwtRe          = regexp.MustCompile(`eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{6,}\.[A-Za-z0-9_-]{4,}`)
	passwordRe     = regexp.MustCompile(`(?i)"?password"?\s*[:=]\s*"?[^"&\s,}]{3,}`)
	tokenRe        = regexp.MustCompile(`(?i)"(access_?token|token|session|secret|api_?key)"\s*:\s*"[^"]{8,}"`)
	versionRe      = regexp.MustCompile(`\d+\.\d+`)
	urlSensitiveRe = regexp.MustCompile(`(?i)[?&](access_?token|api_?key|token|session|password|secret|passwd|auth)=([^&\s]{6,})`)

	// mixedContentRe matches http:// scheme references inside common HTML resource-loading attributes.
	// We look for src= or href= (for link/script/iframe/img) followed immediately by http://.
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
)

const maxScanBytes = 256 * 1024 // cap how much of a body we inspect

// Analyze runs all passive checks against one flow and its (optional) bodies.
func Analyze(f *store.Flow, reqBody, resBody []byte) []store.Issue {
	res := http.Header(f.ResHeaders)
	target := f.Method + " " + f.Host + f.Path
	req := clip(reqBody)
	resp := clip(resBody)

	var out []store.Issue
	add := func(sev, title, detail, evidence, fix string) {
		out = append(out, store.Issue{
			FlowID: f.ID, Severity: sev, Title: title, Target: target,
			Detail: detail, Evidence: evidence, Fix: fix,
		})
	}

	// 1. Password in the request body.
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

	// 2. Session token / JWT in the response body.
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

	// 3. Verbose error disclosure.
	if f.Status >= 500 && containsAny(resp, "traceId", "trace_id", "stacktrace", "stack", "exception", " at ") {
		add("Medium", "Verbose error discloses internal details",
			"A server error response leaks internal diagnostics (trace identifiers / stack frames) that aid reconnaissance of the backend.",
			trunc(firstMatch(resp, "traceId", "trace_id", "exception", "stack"), 80),
			"Return a generic error to clients and keep trace identifiers and stack traces server-side in logs only.")
	}

	// 4. Missing Content-Security-Policy on HTML.
	if isHTML(res, f.Mime) && res.Get("Content-Security-Policy") == "" {
		add("Medium", "Missing Content-Security-Policy header",
			"The HTML document is served without a Content-Security-Policy, increasing the blast radius of any XSS.",
			"(no Content-Security-Policy response header)",
			"Add a restrictive policy such as default-src 'self'; roll it out in report-only mode first.")
	}

	// 5. Missing HSTS on HTTPS.
	if f.Scheme == "https" && res.Get("Strict-Transport-Security") == "" {
		add("Medium", "Missing Strict-Transport-Security (HSTS)",
			"No HSTS header was observed; connections could be downgraded to plaintext HTTP via an active MITM.",
			"(no Strict-Transport-Security response header)",
			"Send Strict-Transport-Security: max-age=63072000; includeSubDomains; preload on every HTTPS response.")
	}

	// 6. Wildcard CORS.
	if res.Get("Access-Control-Allow-Origin") == "*" {
		add("Medium", "Overly permissive CORS policy",
			"Access-Control-Allow-Origin: * lets any origin read this resource.",
			"Access-Control-Allow-Origin: *",
			"Replace the wildcard with an explicit allow-list of trusted origins.")
	}

	// 7. Insecure cookies.
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

	// 8. Server software version disclosure.
	for _, h := range []string{"Server", "X-Powered-By", "X-AspNet-Version"} {
		if v := res.Get(h); v != "" && versionRe.MatchString(v) {
			add("Low", "Server software version disclosed",
				"A response header reveals the server software and version, aiding targeted exploitation.",
				h+": "+v,
				"Suppress or genericize version-bearing headers ("+h+") at the edge.")
			break
		}
	}

	// 9. Request parameter reflected verbatim in an HTML response (possible XSS sink).
	if isHTML(res, f.Mime) {
		if name, val, ok := reflectedParam(f.Path, req, resp); ok {
			add("Low", "Request parameter reflected in HTML response",
				"A request parameter is echoed verbatim into an HTML response. If it is not contextually output-encoded this is a reflected-XSS sink — confirm by sending a marker payload.",
				trunc(name+"="+val, 80),
				"HTML-encode user input on output (and set a Content-Security-Policy); verify the value cannot break out of its HTML/JS/attribute context.")
		}
	}

	// 10. HTTP Basic authentication (credentials are only base64-encoded).
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

	// 11. Missing X-Content-Type-Options on scriptable responses (MIME sniffing).
	if isHTML(res, f.Mime) || containsAny(f.Mime, "javascript") {
		if !strings.Contains(strings.ToLower(res.Get("X-Content-Type-Options")), "nosniff") {
			add("Low", "Missing X-Content-Type-Options: nosniff",
				"The response omits X-Content-Type-Options: nosniff, so a browser may MIME-sniff the body and execute it as a different content type.",
				"(no X-Content-Type-Options response header)",
				"Send X-Content-Type-Options: nosniff on responses.")
		}
	}

	// 12. Missing clickjacking protection on HTML.
	if isHTML(res, f.Mime) {
		csp := strings.ToLower(res.Get("Content-Security-Policy"))
		if res.Get("X-Frame-Options") == "" && !strings.Contains(csp, "frame-ancestors") {
			add("Low", "Missing clickjacking protection",
				"The HTML document can be framed by any origin: neither X-Frame-Options nor a CSP frame-ancestors directive is set, enabling clickjacking.",
				"(no X-Frame-Options or CSP frame-ancestors)",
				"Send X-Frame-Options: DENY (or SAMEORIGIN) or a CSP frame-ancestors 'none' directive.")
		}
	}

	// 13. CORS with credentials — wildcard or reflected origin combined with Allow-Credentials: true.
	// Wildcard + credentials is spec-prohibited but is still a misconfiguration worth flagging.
	// Reflected origin + credentials is exploitable: the browser will honour the reflected ACAO and
	// forward cookies/auth to a cross-origin attacker page.
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

	// 14. Sensitive token or credential in the request URL query string.
	// Tokens in URLs are logged by proxies, servers, and appear in Referer headers, making them
	// high-risk even over HTTPS.
	if m := urlSensitiveRe.FindString(f.Path); m != "" {
		// Extract just the parameter name for a cleaner evidence string.
		kv := strings.SplitN(strings.TrimLeft(m, "?&"), "=", 2)
		paramName := kv[0]
		add("Medium", "Sensitive token or credential in URL",
			"A credential-like parameter ("+paramName+") is present in the request URL query string. "+
				"Query parameters are recorded in server access logs, browser history, proxy logs, and Referer headers sent to third parties.",
			trunc(m, 80),
			"Pass credentials in the request body (POST) or as Authorization/custom headers, never in the URL.")
	}

	// 15. Cookie missing SameSite attribute.
	// The existing check (7) catches missing Secure/HttpOnly. This check focuses on the distinct
	// CSRF-related gap: a cookie that is Secure and HttpOnly but lacks SameSite is still vulnerable
	// to cross-site request forgery in browsers that do not enforce SameSite=Lax by default.
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

	// 16. Sensitive response cached without Cache-Control: no-store.
	// Responses that set authentication cookies or carry a private payload should not be stored by
	// shared caches (CDNs, forward proxies). We flag only responses that set a cookie AND lack an
	// appropriate Cache-Control directive to keep false-positive rate low.
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

	// 17. Missing Referrer-Policy on HTML document responses.
	// Without this header the browser sends the full URL (including path and query string) in the
	// Referer header to any third-party resource loaded by the page, leaking sensitive path
	// parameters and session state.
	if isHTML(res, f.Mime) && res.Get("Referrer-Policy") == "" {
		add("Low", "Missing Referrer-Policy header",
			"The HTML document is served without a Referrer-Policy header. "+
				"The browser will send the full request URL (path + query string) as the Referer to "+
				"any third-party resource referenced by the page, potentially disclosing session "+
				"identifiers, user-specific paths, or other sensitive URL components.",
			"(no Referrer-Policy response header)",
			"Send Referrer-Policy: strict-origin-when-cross-origin (or stricter) on HTML responses.")
	}

	// 18. Mixed content — HTTPS page that references HTTP resources.
	// An HTTPS page loading scripts, stylesheets, iframes, or images over HTTP allows a
	// network-level attacker to inject malicious content or degrade the security of the page.
	// We only inspect HTML responses received over HTTPS to keep noise low.
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

	// 19. Open redirect — 3xx Location contains a request parameter that resolves off-host.
	// We require: (a) a 3xx status, (b) a Location header is present, (c) a request query or
	// body parameter value (≥8 chars, containing "://" or starting with "//" or a known redirect
	// param name) appears verbatim in the Location value, and (d) the Location value differs from
	// the request host. This conservative gate is intentional to minimise false positives from
	// legitimate same-host redirects.
	if f.Status >= 300 && f.Status < 400 {
		if loc := res.Get("Location"); loc != "" {
			if name, val, ok := openRedirectParam(f.Host, f.Path, req, loc); ok {
				add("Medium", "Potential open redirect via request parameter",
					"A redirect response ("+string(rune('0'+f.Status/100))+"xx) sets a Location header "+
						"whose value is influenced by the request parameter '"+name+"'. "+
						"If the server does not validate the destination, an attacker can craft a link "+
						"that redirects victims to an attacker-controlled site after they visit the "+
						"legitimate application URL.",
					trunc(name+"="+val+" → Location: "+loc, 120),
					"Validate redirect destinations against an explicit allow-list of trusted URLs; "+
						"never accept full URLs from user-controlled input as redirect targets.")
			}
		}
	}

	// 20. Directory listing exposure.
	// Web-server auto-index pages expose the directory structure and file names of the server,
	// aiding reconnaissance. The match is conservative: we require both the characteristic
	// <title>Index of /… pattern and a follow-on <a href= link typical of listing pages.
	if strings.Contains(resp, "<a href=") && dirListingRe.MatchString(resp) {
		add("Low", "Directory listing enabled",
			"The response appears to be an auto-generated directory index (e.g. Apache/nginx "+
				"autoindex). Directory listings expose file and directory names, software paths, "+
				"and may reveal sensitive files such as backups, configuration files, or source code.",
			trunc(dirListingRe.FindString(resp), 80),
			"Disable directory listing in the web-server configuration (e.g. Options -Indexes in "+
				"Apache, autoindex off in nginx) and ensure sensitive files are not web-accessible.")
	}

	return out
}

// openRedirectParam checks whether any request query or body parameter value
// appears verbatim in the redirect Location header AND points off-host.
// We restrict to values ≥8 characters that either start with "http", "//",
// or match a known redirect-parameter name — keeping false-positive rate low.
func openRedirectParam(host, path, body, location string) (name, val string, ok bool) {
	// Build a set of candidate parameter (name, value) pairs from query string and body.
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
			// Restrict to values that look like URLs, or parameter names that
			// are commonly used as redirect targets.
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
		// Confirm the destination is off-host by checking that the Location
		// does not simply start with / (same-origin) or contain our host.
		isAbsolute := strings.HasPrefix(locLower, "http") || strings.HasPrefix(locLower, "//")
		if !isAbsolute {
			continue
		}
		// If the host appears in the Location it is likely a same-site redirect.
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
