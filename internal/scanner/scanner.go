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

	return out
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
