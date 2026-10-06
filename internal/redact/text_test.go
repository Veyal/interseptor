package redact

import (
	"strings"
	"testing"
)

func TestTextMasksKeyedSecrets(t *testing.T) {
	cases := []struct{ name, in, secret string }{
		{"camelCase json", `{"accessToken":"abc123def456","csrfToken":"Zx9Qw81"}`, "abc123def456"},
		{"camelCase csrf", `{"accessToken":"abc123def456","csrfToken":"Zx9Qw81"}`, "Zx9Qw81"},
		{"snake otp", "otp=482913&next=/home", "482913"},
		{"reset code key", "reset_code=48291", "48291"},
		{"query code", "GET /reset?code=482913 HTTP/1.1", "482913"},
		{"refresh", `{"refreshToken": "r-9f8e7d6c"}`, "r-9f8e7d6c"},
		{"password", "password=Summer2024!&user=bob", "Summer2024!"},
		{"api key kebab", "x-api-key: k_live_12345", "k_live_12345"},
		{"client secret", `client_secret="s3cr3t-value"`, "s3cr3t-value"},
		{"session id", "sessionId=ab12cd34", "ab12cd34"},
		{"bearer", "Authorization: Bearer abcdefghijkl", "abcdefghijkl"},
		{"cookie header", "Cookie: sid=abcd1234; theme=dark", "abcd1234"},
		{"jwt", "t=eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJleGFtcGxlIn0.c2lnbmF0dXJl", "eyJhbGciOiJIUzI1NiJ9"},
		{"long opaque", "id 0123456789abcdef0123456789abcdefXYZ9", "0123456789abcdef0123456789abcdefXYZ9"},
		{"pin", "pin=123456", "123456"},
	}
	for _, c := range cases {
		got := Text(c.in)
		if strings.Contains(got, c.secret) {
			t.Errorf("%s: leaked %q in %q", c.name, c.secret, got)
		}
		if !strings.Contains(got, "[redacted") {
			t.Errorf("%s: no placeholder in %q", c.name, got)
		}
	}
}

func TestTextKeepsEvidenceThatIsNotSecret(t *testing.T) {
	keep := []string{
		"status_code=200", "GET /api/orders?page=2&sort=asc", "HTTP/1.1 429 Too Many Requests",
		"Retry-After: 30", "x-ratelimit-remaining: 0", "error=forbidden", "Content-Type: application/json",
		"pinned=true", "session expired", "consider=yes", "Keyboard=us",
	}
	for _, s := range keep {
		if got := Text(s); got != s {
			t.Errorf("Text(%q) altered non-secret text: %q", s, got)
		}
	}
}

func TestTextEqualSecretsStayComparable(t *testing.T) {
	a, b := Text("otp=482913"), Text("otp=482913")
	c := Text("otp=482914")
	if a != b || a == c {
		t.Fatalf("placeholders must be deterministic and distinct: %q %q %q", a, b, c)
	}
}

func TestTextIdempotentAndEmpty(t *testing.T) {
	if Text("") != "" {
		t.Fatal("empty")
	}
	once := Text("password=hunter2hunter2")
	if Text(once) != once {
		t.Fatalf("not idempotent: %q -> %q", once, Text(once))
	}
}
