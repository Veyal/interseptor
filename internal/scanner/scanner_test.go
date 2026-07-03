package scanner

import (
	"net/http"
	"strings"
	"testing"

	"github.com/Veyal/interceptor/internal/store"
)

func titles(issues []store.Issue) string {
	var b []string
	for _, i := range issues {
		b = append(b, i.Severity+":"+i.Title)
	}
	return strings.Join(b, " | ")
}

func has(issues []store.Issue, title string) bool {
	for _, i := range issues {
		if i.Title == title {
			return true
		}
	}
	return false
}

func TestDBErrorPossibleSQLi(t *testing.T) {
	// A database error string in the response body is a strong passive SQLi signal.
	for _, body := range []string{
		`{"error":"You have an error in your SQL syntax near '1'"}`,
		`ORA-00942: table or view does not exist`,
		`SQLSTATE[42000]: Syntax error or access violation`,
		`sqlite3.OperationalError: near "x": syntax error`,
	} {
		flow := &store.Flow{Scheme: "https", Method: "GET", Host: "shop.example", Path: "/p?id=1", Status: 500,
			ResHeaders: map[string][]string(http.Header{"Content-Type": {"application/json"}, "Strict-Transport-Security": {"max-age=1"}})}
		got := Analyze(flow, nil, []byte(body))
		if !has(got, "Possible SQL injection (DB error in response)") {
			t.Fatalf("expected SQLi finding for %q; got: %s", body, titles(got))
		}
	}
	// Negative: a benign response with no DB error signature must not flag.
	benign := &store.Flow{Scheme: "https", Method: "GET", Host: "shop.example", Path: "/p?id=1", Status: 200,
		ResHeaders: map[string][]string(http.Header{"Content-Type": {"application/json"}, "Strict-Transport-Security": {"max-age=1"}})}
	got := Analyze(benign, nil, []byte(`{"product":"widget","price":9.99}`))
	if has(got, "Possible SQL injection (DB error in response)") {
		t.Fatalf("benign response should not flag SQLi; got: %s", titles(got))
	}
}

func htmlFlow(body string) *store.Flow {
	return &store.Flow{Scheme: "https", Method: "GET", Host: "app.example", Path: "/", Status: 200, Mime: "text/html",
		ResHeaders: map[string][]string(http.Header{"Content-Type": {"text/html"}, "Strict-Transport-Security": {"max-age=63072000; includeSubDomains"}})}
}

func TestCloudKeyExposure(t *testing.T) {
	f := &store.Flow{Scheme: "https", Method: "GET", Host: "api.example", Path: "/config", Status: 200,
		ResHeaders: map[string][]string(http.Header{"Content-Type": {"application/json"}, "Strict-Transport-Security": {"max-age=63072000; includeSubDomains"}})}
	for _, secret := range []string{
		`{"aws":"AKIAIOSFODNN7EXAMPLE"}`,
		`token=ghp_1234567890abcdefghijklmnopqrstuvwxyz`,
		`-----BEGIN RSA PRIVATE KEY-----\nMIIE...`,
		`{"g":"AIzaSyA1234567890abcdefghijklmnopqrstuv"}`,
	} {
		got := Analyze(f, nil, []byte(secret))
		if !has(got, "Cloud/API credential or private key exposed in response") {
			t.Fatalf("expected cloud-key finding for %q; got %s", secret, titles(got))
		}
	}
	// Evidence must be redacted (not the full secret).
	got := Analyze(f, nil, []byte(`AKIAIOSFODNN7EXAMPLE`))
	for _, is := range got {
		if is.Title == "Cloud/API credential or private key exposed in response" && strings.Contains(is.Evidence, "N7EXAMPLE") {
			t.Fatalf("secret must be redacted in evidence, got %q", is.Evidence)
		}
	}
	// Negative: an ordinary token-shaped-but-not-matching string does not flag.
	benign := Analyze(f, nil, []byte(`{"id":"abc123","name":"widget"}`))
	if has(benign, "Cloud/API credential or private key exposed in response") {
		t.Fatalf("benign body must not flag cloud key; got %s", titles(benign))
	}
}

func TestPIIExposure(t *testing.T) {
	f := &store.Flow{Scheme: "https", Method: "GET", Host: "api.example", Path: "/u", Status: 200,
		ResHeaders: map[string][]string(http.Header{"Content-Type": {"application/json"}, "Strict-Transport-Security": {"max-age=63072000; includeSubDomains"}})}
	// 4111111111111111 is the canonical Luhn-valid Visa test PAN.
	if !has(Analyze(f, nil, []byte(`{"card":"4111111111111111"}`)), "Payment card number exposed in response") {
		t.Fatal("expected payment-card finding for a Luhn-valid Visa PAN")
	}
	// A 16-digit run that fails Luhn must NOT flag (false-positive guard).
	if has(Analyze(f, nil, []byte(`{"order":"4111111111111112"}`)), "Payment card number exposed in response") {
		t.Fatal("Luhn-invalid digit run must not flag as a card")
	}
	// Valid-format SSN flags; an obviously-invalid area (000) does not.
	if !has(Analyze(f, nil, []byte(`ssn: 123-45-6789`)), "US Social Security Number exposed in response") {
		t.Fatal("expected SSN finding")
	}
	if has(Analyze(f, nil, []byte(`ref 000-12-3456`)), "US Social Security Number exposed in response") {
		t.Fatal("invalid SSN area 000 must not flag")
	}
}

func TestConfigAndHeaderPassiveChecks(t *testing.T) {
	// Weak CSP + weak HSTS on an HTML page.
	f := &store.Flow{Scheme: "https", Method: "GET", Host: "app.example", Path: "/", Status: 200, Mime: "text/html",
		ResHeaders: map[string][]string(http.Header{
			"Content-Type":              {"text/html"},
			"Content-Security-Policy":   {"default-src 'self'; script-src 'self' 'unsafe-inline'"},
			"Strict-Transport-Security": {"max-age=3600"},
			"Set-Cookie":                {"sid=1; SameSite=None"},
		})}
	got := Analyze(f, nil, []byte(`<html><body>
	  <form action="http://insecure.example/login"></form>
	  <a href="https://evil.example" target="_blank">x</a>
	  <script>var w = new WebSocket("ws://app.example/live");</script>
	</body></html>`))
	for _, want := range []string{
		"Weak Content-Security-Policy",
		"Weak HSTS policy (short max-age)",
		"Form submits over plaintext HTTP",
		"Reverse tabnabbing (target=_blank without noopener)",
		"Cookie SameSite=None without Secure",
		"Insecure WebSocket (ws://) reference",
	} {
		if !has(got, want) {
			t.Fatalf("expected %q; got %s", want, titles(got))
		}
	}
	// noopener present → no tabnabbing finding.
	safe := Analyze(htmlFlow(""), nil, []byte(`<a href="https://evil.example" target="_blank" rel="noopener">x</a>`))
	if has(safe, "Reverse tabnabbing (target=_blank without noopener)") {
		t.Fatalf("rel=noopener must suppress tabnabbing; got %s", titles(safe))
	}
}

func TestDisclosurePassiveChecks(t *testing.T) {
	// Framework debug page (any status).
	dbg := &store.Flow{Scheme: "https", Method: "GET", Host: "app.example", Path: "/x", Status: 200, Mime: "text/html",
		ResHeaders: map[string][]string(http.Header{"Content-Type": {"text/html"}, "Strict-Transport-Security": {"max-age=63072000; includeSubDomains"}})}
	if !has(Analyze(dbg, nil, []byte(`<h1>Traceback (most recent call last)</h1>`)), "Framework debug page or stack trace disclosed") {
		t.Fatal("expected framework debug finding")
	}
	// Source map reference in JS.
	js := &store.Flow{Scheme: "https", Method: "GET", Host: "app.example", Path: "/a.js", Status: 200, Mime: "application/javascript",
		ResHeaders: map[string][]string(http.Header{"Content-Type": {"application/javascript"}, "Strict-Transport-Security": {"max-age=63072000; includeSubDomains"}})}
	if !has(Analyze(js, nil, []byte("var x=1;\n//# sourceMappingURL=a.js.map")), "Source map reference exposed") {
		t.Fatal("expected source-map finding")
	}
	// GraphQL introspection.
	gql := &store.Flow{Scheme: "https", Method: "POST", Host: "app.example", Path: "/graphql", Status: 200,
		ResHeaders: map[string][]string(http.Header{"Content-Type": {"application/json"}, "Strict-Transport-Security": {"max-age=63072000; includeSubDomains"}})}
	if !has(Analyze(gql, nil, []byte(`{"data":{"__schema":{"queryType":{"name":"Query"}}}}`)), "GraphQL introspection enabled") {
		t.Fatal("expected GraphQL introspection finding")
	}
	// JSONP reflection.
	jsonp := &store.Flow{Scheme: "https", Method: "GET", Host: "app.example", Path: "/api?callback=renderData", Status: 200, Mime: "application/javascript",
		ResHeaders: map[string][]string(http.Header{"Content-Type": {"application/javascript"}, "Strict-Transport-Security": {"max-age=63072000; includeSubDomains"}})}
	if !has(Analyze(jsonp, nil, []byte(`renderData({"secret":42})`)), "JSONP endpoint reflects callback") {
		t.Fatal("expected JSONP finding")
	}
}

func TestDisabledBuiltinSkipped(t *testing.T) {
	// Disabling the security-headers check must suppress the merged finding.
	flow := &store.Flow{Scheme: "https", Method: "GET", Host: "app.example", Path: "/", Status: 200, Mime: "text/html",
		ResHeaders: map[string][]string(http.Header{"Content-Type": {"text/html"}})}
	on := Analyze(flow, nil, []byte("<html></html>"))
	if !has(on, "Missing security response headers") {
		t.Fatalf("expected merged header finding when enabled; got: %s", titles(on))
	}
	off := AnalyzeWithDisabled(flow, nil, []byte("<html></html>"), map[string]bool{"security-headers": true})
	if has(off, "Missing security response headers") {
		t.Fatalf("disabled check must be skipped; got: %s", titles(off))
	}
}

func TestAnalyzeHeaderHygiene(t *testing.T) {
	flow := &store.Flow{
		Scheme: "https", Method: "GET", Host: "app.example.com", Path: "/", Status: 200, Mime: "text/html",
		ResHeaders: map[string][]string(http.Header{
			"Content-Type":                {"text/html; charset=utf-8"},
			"Access-Control-Allow-Origin": {"*"},
			"Server":                      {"nginx/1.21.0"},
		}),
	}
	got := Analyze(flow, nil, []byte("<html></html>"))
	for _, want := range []string{
		"Missing security response headers",
		"Overly permissive CORS policy",
		"Server software version disclosed",
	} {
		if !has(got, want) {
			t.Fatalf("expected %q; got: %s", want, titles(got))
		}
	}
}

func TestAnalyzeSecretsInBodies(t *testing.T) {
	flow := &store.Flow{
		Scheme: "https", Method: "POST", Host: "api.example.com", Path: "/login", Status: 200,
		ResHeaders: map[string][]string(http.Header{"Content-Type": {"application/json"}, "Strict-Transport-Security": {"max-age=1"}}),
	}
	got := Analyze(flow,
		[]byte(`{"email":"a@b.com","password":"hunter2-correct-horse"}`),
		[]byte(`{"token":"eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxIn0.abc123"}`))
	if !has(got, "Password transmitted in request body") {
		t.Fatalf("expected password finding; got: %s", titles(got))
	}
	if !has(got, "Session token leaked in response body") {
		t.Fatalf("expected token finding; got: %s", titles(got))
	}
}

func TestAnalyzeInsecureCookieAndVerboseError(t *testing.T) {
	flow := &store.Flow{
		Scheme: "https", Method: "GET", Host: "api.example.com", Path: "/reports", Status: 500,
		ResHeaders: map[string][]string(http.Header{
			"Set-Cookie":                {"session=abc; Path=/"},
			"Strict-Transport-Security": {"max-age=1"},
			"Content-Type":              {"application/json"},
		}),
	}
	got := Analyze(flow, nil, []byte(`{"error":"boom","traceId":"3f9a-22b1","stack":"..."}`))
	if !has(got, "Cookie set without Secure and HttpOnly") {
		t.Fatalf("expected cookie finding; got: %s", titles(got))
	}
	if !has(got, "Verbose error discloses internal details") {
		t.Fatalf("expected verbose-error finding; got: %s", titles(got))
	}
}

func TestAnalyzeReflectionAuthAndFraming(t *testing.T) {
	flow := &store.Flow{
		Scheme: "https", Method: "GET", Host: "app.test", Path: "/search?q=hello<scriptmark>",
		Status: 200, Mime: "text/html",
		ReqHeaders: map[string][]string(http.Header{"Authorization": {"Basic dXNlcjpwYXNz"}}),
		ResHeaders: map[string][]string(http.Header{
			"Content-Type":              {"text/html; charset=utf-8"},
			"Content-Security-Policy":   {"default-src 'self'"}, // present, but no frame-ancestors
			"Strict-Transport-Security": {"max-age=63072000"},
		}),
	}
	got := Analyze(flow, nil, []byte("<html>results for hello<scriptmark> ...</html>"))
	for _, want := range []string{
		"Request parameter reflected in HTML response",
		"HTTP Basic authentication in use",
		"Missing security response headers",
	} {
		if !has(got, want) {
			t.Fatalf("expected %q; got: %s", want, titles(got))
		}
	}
}

func TestAnalyzeReflectionAvoidsTrivialValues(t *testing.T) {
	// Short / non-alpha values should not be flagged as reflections (noise control).
	flow := &store.Flow{
		Scheme: "https", Method: "GET", Host: "app.test", Path: "/p?id=12345&ok=1", Status: 200, Mime: "text/html",
		ResHeaders: map[string][]string(http.Header{
			"Content-Type": {"text/html"}, "X-Frame-Options": {"DENY"},
			"X-Content-Type-Options": {"nosniff"}, "Strict-Transport-Security": {"max-age=1"},
		}),
	}
	got := Analyze(flow, nil, []byte("<html>id 12345 ok 1</html>"))
	if has(got, "Request parameter reflected in HTML response") {
		t.Fatalf("trivial values should not flag reflection; got: %s", titles(got))
	}
}

func TestAnalyzeCleanFlowHasNoIssues(t *testing.T) {
	flow := &store.Flow{
		Scheme: "https", Method: "GET", Host: "api.example.com", Path: "/health", Status: 200,
		ResHeaders: map[string][]string(http.Header{
			"Content-Type":              {"application/json"},
			"Strict-Transport-Security": {"max-age=63072000"},
		}),
	}
	if got := Analyze(flow, nil, []byte(`{"ok":true}`)); len(got) != 0 {
		t.Fatalf("expected no issues, got: %s", titles(got))
	}
}

// --- Check 13: CORS with credentials ---

func TestCORSCredentialsWildcard(t *testing.T) {
	// Positive: ACAO=* + Allow-Credentials: true → High severity
	flow := &store.Flow{
		Scheme: "https", Method: "GET", Host: "api.example.com", Path: "/data", Status: 200,
		ReqHeaders: map[string][]string(http.Header{"Origin": {"https://attacker.example"}}),
		ResHeaders: map[string][]string(http.Header{
			"Content-Type":                       {"application/json"},
			"Strict-Transport-Security":          {"max-age=1"},
			"Access-Control-Allow-Origin":        {"*"},
			"Access-Control-Allow-Credentials":   {"true"},
		}),
	}
	got := Analyze(flow, nil, []byte(`{"data":"secret"}`))
	if !has(got, "CORS wildcard with credentials enabled") {
		t.Fatalf("expected CORS wildcard+credentials finding; got: %s", titles(got))
	}
	// verify severity is High
	for _, i := range got {
		if i.Title == "CORS wildcard with credentials enabled" && i.Severity != "High" {
			t.Fatalf("expected High severity, got %s", i.Severity)
		}
	}
}

func TestCORSCredentialsReflectedOrigin(t *testing.T) {
	// Positive: ACAO reflects request Origin + Allow-Credentials: true → High severity
	flow := &store.Flow{
		Scheme: "https", Method: "GET", Host: "api.example.com", Path: "/data", Status: 200,
		ReqHeaders: map[string][]string(http.Header{"Origin": {"https://attacker.example"}}),
		ResHeaders: map[string][]string(http.Header{
			"Content-Type":                     {"application/json"},
			"Strict-Transport-Security":        {"max-age=1"},
			"Access-Control-Allow-Origin":      {"https://attacker.example"},
			"Access-Control-Allow-Credentials": {"true"},
		}),
	}
	got := Analyze(flow, nil, []byte(`{"data":"secret"}`))
	if !has(got, "CORS reflects request Origin with credentials enabled") {
		t.Fatalf("expected CORS reflected-origin+credentials finding; got: %s", titles(got))
	}
}

func TestCORSCredentialsNegative(t *testing.T) {
	// Negative: Allow-Credentials present but ACAO is an explicit non-reflected trusted origin → no issue.
	flow := &store.Flow{
		Scheme: "https", Method: "GET", Host: "api.example.com", Path: "/data", Status: 200,
		ReqHeaders: map[string][]string(http.Header{"Origin": {"https://app.example.com"}}),
		ResHeaders: map[string][]string(http.Header{
			"Content-Type":                     {"application/json"},
			"Strict-Transport-Security":        {"max-age=1"},
			"Access-Control-Allow-Origin":      {"https://trusted.example.com"},
			"Access-Control-Allow-Credentials": {"true"},
		}),
	}
	got := Analyze(flow, nil, []byte(`{"ok":true}`))
	if has(got, "CORS wildcard with credentials enabled") || has(got, "CORS reflects request Origin with credentials enabled") {
		t.Fatalf("should not flag CORS when ACAO is a fixed trusted origin; got: %s", titles(got))
	}
}

// --- Check 14: Sensitive token in URL ---

func TestSensitiveTokenInURL(t *testing.T) {
	cases := []struct {
		name string
		path string
	}{
		{"access_token", "/api/resource?access_token=eyJhbGciOiJIUzI1NiJ9.payload.sig"},
		{"api_key", "/v1/data?api_key=supersecretkey123"},
		{"token", "/callback?token=verylongtoken12345"},
		{"session", "/profile?session=sess_abc123xyz"},
		{"password", "/reset?password=newPass123!"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			flow := &store.Flow{
				Scheme: "https", Method: "GET", Host: "api.example.com", Path: tc.path, Status: 200,
				ResHeaders: map[string][]string(http.Header{
					"Content-Type":              {"application/json"},
					"Strict-Transport-Security": {"max-age=1"},
				}),
			}
			got := Analyze(flow, nil, []byte(`{"ok":true}`))
			if !has(got, "Sensitive token or credential in URL") {
				t.Fatalf("path %q: expected token-in-URL finding; got: %s", tc.path, titles(got))
			}
		})
	}
}

func TestSensitiveTokenInURLNegative(t *testing.T) {
	// Negative: normal query parameters should not fire.
	flow := &store.Flow{
		Scheme: "https", Method: "GET", Host: "api.example.com", Path: "/search?q=hello&page=2", Status: 200,
		ResHeaders: map[string][]string(http.Header{
			"Content-Type":              {"application/json"},
			"Strict-Transport-Security": {"max-age=1"},
		}),
	}
	got := Analyze(flow, nil, []byte(`{"results":[]}`))
	if has(got, "Sensitive token or credential in URL") {
		t.Fatalf("should not flag benign query params; got: %s", titles(got))
	}
}

// --- Check 15: Cookie missing SameSite ---

func TestCookieMissingSameSite(t *testing.T) {
	// Positive: cookie has Secure and HttpOnly but no SameSite → Low finding.
	flow := &store.Flow{
		Scheme: "https", Method: "POST", Host: "app.example.com", Path: "/login", Status: 200,
		ResHeaders: map[string][]string(http.Header{
			"Content-Type":              {"application/json"},
			"Strict-Transport-Security": {"max-age=1"},
			// Secure + HttpOnly present, SameSite absent.
			"Set-Cookie": {"session=abc123; Secure; HttpOnly; Path=/"},
		}),
	}
	got := Analyze(flow, nil, []byte(`{"ok":true}`))
	if !has(got, "Cookie missing SameSite attribute") {
		t.Fatalf("expected SameSite finding; got: %s", titles(got))
	}
}

func TestCookieMissingSameSiteNegative(t *testing.T) {
	// Negative: cookie has SameSite=Strict → no SameSite finding.
	flow := &store.Flow{
		Scheme: "https", Method: "POST", Host: "app.example.com", Path: "/login", Status: 200,
		ResHeaders: map[string][]string(http.Header{
			"Content-Type":              {"application/json"},
			"Strict-Transport-Security": {"max-age=1"},
			"Set-Cookie":                {"session=abc123; Secure; HttpOnly; SameSite=Strict; Path=/"},
		}),
	}
	got := Analyze(flow, nil, []byte(`{"ok":true}`))
	if has(got, "Cookie missing SameSite attribute") {
		t.Fatalf("should not flag cookie that has SameSite; got: %s", titles(got))
	}
}

// --- Check 16: Authenticated response not marked no-store / private ---

func TestAuthenticatedResponseCacheable(t *testing.T) {
	// Positive: response sets cookie but Cache-Control is absent → Low finding.
	flow := &store.Flow{
		Scheme: "https", Method: "POST", Host: "app.example.com", Path: "/login", Status: 200,
		ResHeaders: map[string][]string(http.Header{
			"Content-Type":              {"application/json"},
			"Strict-Transport-Security": {"max-age=1"},
			"Set-Cookie":                {"session=abc123; Secure; HttpOnly; SameSite=Strict; Path=/"},
			// No Cache-Control header at all.
		}),
	}
	got := Analyze(flow, nil, []byte(`{"ok":true}`))
	if !has(got, "Authenticated response may be cached by shared proxies") {
		t.Fatalf("expected cache-control finding; got: %s", titles(got))
	}
}

func TestAuthenticatedResponseCacheableNoStore(t *testing.T) {
	// Negative: no-store present → no finding.
	flow := &store.Flow{
		Scheme: "https", Method: "POST", Host: "app.example.com", Path: "/login", Status: 200,
		ResHeaders: map[string][]string(http.Header{
			"Content-Type":              {"application/json"},
			"Strict-Transport-Security": {"max-age=1"},
			"Set-Cookie":                {"session=abc123; Secure; HttpOnly; SameSite=Strict; Path=/"},
			"Cache-Control":             {"no-store"},
		}),
	}
	got := Analyze(flow, nil, []byte(`{"ok":true}`))
	if has(got, "Authenticated response may be cached by shared proxies") {
		t.Fatalf("should not flag when Cache-Control: no-store is present; got: %s", titles(got))
	}
}

func TestAuthenticatedResponseCacheablePrivate(t *testing.T) {
	// Negative: Cache-Control: private is also acceptable.
	flow := &store.Flow{
		Scheme: "https", Method: "POST", Host: "app.example.com", Path: "/login", Status: 200,
		ResHeaders: map[string][]string(http.Header{
			"Content-Type":              {"application/json"},
			"Strict-Transport-Security": {"max-age=1"},
			"Set-Cookie":                {"session=abc123; Secure; HttpOnly; SameSite=Strict; Path=/"},
			"Cache-Control":             {"private, max-age=0"},
		}),
	}
	got := Analyze(flow, nil, []byte(`{"ok":true}`))
	if has(got, "Authenticated response may be cached by shared proxies") {
		t.Fatalf("should not flag when Cache-Control: private is present; got: %s", titles(got))
	}
}

func TestNoCookieNoCacheIssue(t *testing.T) {
	// Negative: response has no Set-Cookie → no cache-control finding regardless of Cache-Control value.
	flow := &store.Flow{
		Scheme: "https", Method: "GET", Host: "api.example.com", Path: "/public", Status: 200,
		ResHeaders: map[string][]string(http.Header{
			"Content-Type":              {"application/json"},
			"Strict-Transport-Security": {"max-age=1"},
			// No Set-Cookie, no Cache-Control — cache finding should not fire.
		}),
	}
	got := Analyze(flow, nil, []byte(`{"items":[]}`))
	if has(got, "Authenticated response may be cached by shared proxies") {
		t.Fatalf("should not flag when no cookie is set; got: %s", titles(got))
	}
}

// --- Merged security-headers check (was check 17: Missing Referrer-Policy) ---

func TestMissingReferrerPolicy(t *testing.T) {
	// Positive: HTML response with every other security header present but no
	// Referrer-Policy → one consolidated "Missing security response headers"
	// finding at Low (CSP & HSTS are present, so the bundle isn't Medium).
	flow := &store.Flow{
		Scheme: "https", Method: "GET", Host: "app.example.com", Path: "/page", Status: 200, Mime: "text/html",
		ResHeaders: map[string][]string(http.Header{
			"Content-Type":              {"text/html; charset=utf-8"},
			"Strict-Transport-Security": {"max-age=63072000"},
			"X-Content-Type-Options":    {"nosniff"},
			"X-Frame-Options":           {"DENY"},
			"Content-Security-Policy":   {"default-src 'self'"},
			// No Referrer-Policy.
		}),
	}
	got := Analyze(flow, nil, []byte("<html><body>hello</body></html>"))
	if !has(got, "Missing security response headers") {
		t.Fatalf("expected consolidated security-headers finding; got: %s", titles(got))
	}
	for _, i := range got {
		if i.Title == "Missing security response headers" && i.Severity != "Low" {
			t.Fatalf("expected Low severity (only Referrer-Policy missing); got %s", i.Severity)
		}
	}
}

func TestMissingReferrerPolicyNegative(t *testing.T) {
	// Negative: HTML response with ALL security headers present → no finding.
	flow := &store.Flow{
		Scheme: "https", Method: "GET", Host: "app.example.com", Path: "/page", Status: 200, Mime: "text/html",
		ResHeaders: map[string][]string(http.Header{
			"Content-Type":              {"text/html; charset=utf-8"},
			"Strict-Transport-Security": {"max-age=63072000"},
			"X-Content-Type-Options":    {"nosniff"},
			"X-Frame-Options":           {"DENY"},
			"Content-Security-Policy":   {"default-src 'self'"},
			"Referrer-Policy":           {"strict-origin-when-cross-origin"},
		}),
	}
	got := Analyze(flow, nil, []byte("<html><body>hello</body></html>"))
	if has(got, "Missing security response headers") {
		t.Fatalf("should not flag when all security headers are present; got: %s", titles(got))
	}
}

func TestMissingReferrerPolicyNonHTML(t *testing.T) {
	// Negative: JSON API response without Referrer-Policy → no finding (only HTML is in scope).
	flow := &store.Flow{
		Scheme: "https", Method: "GET", Host: "api.example.com", Path: "/data", Status: 200,
		ResHeaders: map[string][]string(http.Header{
			"Content-Type":              {"application/json"},
			"Strict-Transport-Security": {"max-age=1"},
			// No Referrer-Policy — but not HTML, so should not fire.
		}),
	}
	got := Analyze(flow, nil, []byte(`{"ok":true}`))
	if has(got, "Missing Referrer-Policy header") {
		t.Fatalf("should not flag Referrer-Policy on non-HTML response; got: %s", titles(got))
	}
}

// --- Check 18: Mixed content ---

func TestMixedContentDetected(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"script src", `<html><head><script src="http://cdn.example.com/lib.js"></script></head></html>`},
		{"link href", `<html><head><link href="http://cdn.example.com/style.css"></head></html>`},
		{"img src", `<html><body><img src="http://images.cdn.net/photo.jpg"></body></html>`},
		{"iframe src", `<html><body><iframe src="http://ads.example.net/"></iframe></body></html>`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			flow := &store.Flow{
				Scheme: "https", Method: "GET", Host: "secure.example.com", Path: "/page", Status: 200, Mime: "text/html",
				ResHeaders: map[string][]string(http.Header{
					"Content-Type":              {"text/html; charset=utf-8"},
					"Strict-Transport-Security": {"max-age=1"},
					"Referrer-Policy":           {"no-referrer"},
				}),
			}
			got := Analyze(flow, nil, []byte(tc.body))
			if !has(got, "Mixed content: HTTPS page loads HTTP resource") {
				t.Fatalf("%s: expected mixed-content finding; got: %s", tc.name, titles(got))
			}
			for _, i := range got {
				if i.Title == "Mixed content: HTTPS page loads HTTP resource" && i.Severity != "Medium" {
					t.Fatalf("expected Medium severity, got %s", i.Severity)
				}
			}
		})
	}
}

func TestMixedContentNegativeHTTPS(t *testing.T) {
	// Negative: all resources are HTTPS → no mixed-content finding.
	flow := &store.Flow{
		Scheme: "https", Method: "GET", Host: "secure.example.com", Path: "/page", Status: 200, Mime: "text/html",
		ResHeaders: map[string][]string(http.Header{
			"Content-Type":              {"text/html; charset=utf-8"},
			"Strict-Transport-Security": {"max-age=1"},
			"Referrer-Policy":           {"no-referrer"},
		}),
	}
	body := `<html><head><script src="https://cdn.example.com/lib.js"></script></head></html>`
	got := Analyze(flow, nil, []byte(body))
	if has(got, "Mixed content: HTTPS page loads HTTP resource") {
		t.Fatalf("should not flag when resources use HTTPS; got: %s", titles(got))
	}
}

func TestMixedContentNegativeHTTPPage(t *testing.T) {
	// Negative: request is plain HTTP, not HTTPS → check does not fire (no downgrade risk).
	flow := &store.Flow{
		Scheme: "http", Method: "GET", Host: "insecure.example.com", Path: "/page", Status: 200, Mime: "text/html",
		ResHeaders: map[string][]string(http.Header{
			"Content-Type": {"text/html; charset=utf-8"},
			"Referrer-Policy": {"no-referrer"},
		}),
	}
	body := `<html><head><script src="http://cdn.example.com/lib.js"></script></head></html>`
	got := Analyze(flow, nil, []byte(body))
	if has(got, "Mixed content: HTTPS page loads HTTP resource") {
		t.Fatalf("should not flag mixed content on plain-HTTP page; got: %s", titles(got))
	}
}

// --- Check 19: Open redirect via request parameter ---

func TestOpenRedirectDetected(t *testing.T) {
	cases := []struct {
		name   string
		path   string
		loc    string
	}{
		{
			"next param with full URL",
			"/login?next=https://attacker.example.com/steal",
			"https://attacker.example.com/steal",
		},
		{
			"redirect param protocol-relative",
			"/logout?redirect=//evil.net/phish",
			"//evil.net/phish",
		},
		{
			"url param absolute",
			"/sso?url=https://phisher.org/&state=xyz",
			"https://phisher.org/",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			flow := &store.Flow{
				Scheme: "https", Method: "GET", Host: "app.example.com", Path: tc.path, Status: 302,
				ResHeaders: map[string][]string(http.Header{
					"Location":                  {tc.loc},
					"Strict-Transport-Security": {"max-age=1"},
				}),
			}
			got := Analyze(flow, nil, nil)
			if !has(got, "Potential open redirect via request parameter") {
				t.Fatalf("%s: expected open-redirect finding; got: %s", tc.name, titles(got))
			}
			for _, i := range got {
				if i.Title == "Potential open redirect via request parameter" && i.Severity != "Medium" {
					t.Fatalf("expected Medium severity, got %s", i.Severity)
				}
			}
		})
	}
}

func TestOpenRedirectSameHostNotFlagged(t *testing.T) {
	// Negative: redirect target contains our own host → not an open redirect.
	flow := &store.Flow{
		Scheme: "https", Method: "GET", Host: "app.example.com",
		Path:   "/login?next=https://app.example.com/dashboard",
		Status: 302,
		ResHeaders: map[string][]string(http.Header{
			"Location":                  {"https://app.example.com/dashboard"},
			"Strict-Transport-Security": {"max-age=1"},
		}),
	}
	got := Analyze(flow, nil, nil)
	if has(got, "Potential open redirect via request parameter") {
		t.Fatalf("should not flag same-host redirects; got: %s", titles(got))
	}
}

func TestOpenRedirectRelativeNotFlagged(t *testing.T) {
	// Negative: redirect is a relative path (no host) → not an open redirect.
	flow := &store.Flow{
		Scheme: "https", Method: "GET", Host: "app.example.com",
		Path:   "/login?next=/dashboard",
		Status: 302,
		ResHeaders: map[string][]string(http.Header{
			"Location":                  {"/dashboard"},
			"Strict-Transport-Security": {"max-age=1"},
		}),
	}
	got := Analyze(flow, nil, nil)
	if has(got, "Potential open redirect via request parameter") {
		t.Fatalf("should not flag relative-path redirects; got: %s", titles(got))
	}
}

func TestOpenRedirectNon3xx(t *testing.T) {
	// Negative: 200 response with Location-looking content → not flagged.
	flow := &store.Flow{
		Scheme: "https", Method: "GET", Host: "app.example.com",
		Path:   "/page?next=https://attacker.example.com/",
		Status: 200,
		ResHeaders: map[string][]string(http.Header{
			"Content-Type":              {"application/json"},
			"Strict-Transport-Security": {"max-age=1"},
		}),
	}
	got := Analyze(flow, nil, []byte(`{"ok":true}`))
	if has(got, "Potential open redirect via request parameter") {
		t.Fatalf("should not flag non-3xx responses; got: %s", titles(got))
	}
}

// --- Check 20: Directory listing ---

func TestDirectoryListingDetected(t *testing.T) {
	apacheBody := `<!DOCTYPE HTML PUBLIC "-//W3C//DTD HTML 3.2 Final//EN">
<html>
 <head>
  <title>Index of /var/www/uploads</title>
 </head>
 <body>
<h1>Index of /var/www/uploads</h1>
<ul><li><a href="backup.tar.gz"> backup.tar.gz</a></li>
<li><a href="passwords.txt"> passwords.txt</a></li>
</ul>
</body></html>`
	flow := &store.Flow{
		Scheme: "https", Method: "GET", Host: "app.example.com", Path: "/uploads/", Status: 200, Mime: "text/html",
		ResHeaders: map[string][]string(http.Header{
			"Content-Type":              {"text/html; charset=utf-8"},
			"Strict-Transport-Security": {"max-age=1"},
			"Referrer-Policy":           {"no-referrer"},
		}),
	}
	got := Analyze(flow, nil, []byte(apacheBody))
	if !has(got, "Directory listing enabled") {
		t.Fatalf("expected directory-listing finding; got: %s", titles(got))
	}
	for _, i := range got {
		if i.Title == "Directory listing enabled" && i.Severity != "Low" {
			t.Fatalf("expected Low severity, got %s", i.Severity)
		}
	}
}

func TestDirectoryListingNegativeNormalPage(t *testing.T) {
	// Negative: a normal HTML page that happens to mention "index" → not flagged.
	body := `<html><head><title>Welcome to My Site</title></head>
<body><h1>Site Index</h1><p>Browse our content below.</p>
<a href="/about">About</a>
</body></html>`
	flow := &store.Flow{
		Scheme: "https", Method: "GET", Host: "app.example.com", Path: "/", Status: 200, Mime: "text/html",
		ResHeaders: map[string][]string(http.Header{
			"Content-Type":              {"text/html; charset=utf-8"},
			"Strict-Transport-Security": {"max-age=1"},
			"Referrer-Policy":           {"no-referrer"},
		}),
	}
	got := Analyze(flow, nil, []byte(body))
	if has(got, "Directory listing enabled") {
		t.Fatalf("should not flag a normal HTML page; got: %s", titles(got))
	}
}

func TestDirectoryListingNegativeTitleOnlyNoLinks(t *testing.T) {
	// Negative: title matches but there are no <a href= links → conservative gate requires both.
	body := `<html><head><title>Index of /</title></head><body><p>Nothing here.</p></body></html>`
	flow := &store.Flow{
		Scheme: "https", Method: "GET", Host: "app.example.com", Path: "/", Status: 200, Mime: "text/html",
		ResHeaders: map[string][]string(http.Header{
			"Content-Type":              {"text/html; charset=utf-8"},
			"Strict-Transport-Security": {"max-age=1"},
			"Referrer-Policy":           {"no-referrer"},
		}),
	}
	got := Analyze(flow, nil, []byte(body))
	if has(got, "Directory listing enabled") {
		t.Fatalf("should not flag when <a href= links are absent; got: %s", titles(got))
	}
}
