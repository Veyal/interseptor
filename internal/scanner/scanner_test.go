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
		"Missing Content-Security-Policy header",
		"Missing Strict-Transport-Security (HSTS)",
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
		"Missing X-Content-Type-Options: nosniff",
		"Missing clickjacking protection",
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
