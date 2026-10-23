package redact

import (
	"strings"
	"testing"
)

func TestCredentialHeadersMasksKnownHeaders(t *testing.T) {
	raw := "GET /account HTTP/1.1\r\n" +
		"Host: example.com\r\n" +
		"Authorization: Bearer abcdefghijklmnop\r\n" +
		"Cookie: sid=0123456789abcdef; theme=dark\r\n" +
		"Proxy-Authorization: Basic ZXhhbXBsZTpzZWNyZXQ=\r\n" +
		"Accept: application/json\r\n" +
		"\r\n" +
		"body stays"
	got := CredentialHeaders(raw)
	for _, secret := range []string{"abcdefghijklmnop", "0123456789abcdef", "ZXhhbXBsZTpzZWNyZXQ="} {
		if strings.Contains(got, secret) {
			t.Errorf("leaked %q:\n%s", secret, got)
		}
	}
	for _, keep := range []string{"GET /account HTTP/1.1", "Host: example.com", "Accept: application/json", "body stays"} {
		if !strings.Contains(got, keep) {
			t.Errorf("dropped %q:\n%s", keep, got)
		}
	}
	for _, name := range []string{"Authorization: [redacted", "Cookie: [redacted", "Proxy-Authorization: [redacted"} {
		if !strings.Contains(got, name) {
			t.Errorf("header name or placeholder missing (%q):\n%s", name, got)
		}
	}
	if strings.Count(got, "\r\n") != strings.Count(raw, "\r\n") {
		t.Errorf("line endings changed:\n%q", got)
	}
}

func TestCredentialHeadersMasksResponseSetCookie(t *testing.T) {
	raw := "HTTP/1.1 200 OK\r\n" +
		"Set-Cookie: sid=zzzz1111yyyy2222; HttpOnly\r\n" +
		"Set-Cookie: csrf=aaaa3333bbbb4444; Path=/\r\n" +
		"Content-Type: text/html\r\n" +
		"\r\n" +
		"<html>Set-Cookie: not-a-header-here</html>"
	got := CredentialHeaders(raw)
	for _, secret := range []string{"zzzz1111yyyy2222", "aaaa3333bbbb4444"} {
		if strings.Contains(got, secret) {
			t.Errorf("leaked %q:\n%s", secret, got)
		}
	}
	if !strings.Contains(got, "<html>Set-Cookie: not-a-header-here</html>") {
		t.Errorf("body was rewritten:\n%s", got)
	}
	if strings.Count(got, "Set-Cookie: [redacted") != 2 {
		t.Errorf("want both Set-Cookie values masked:\n%s", got)
	}
}

func TestCredentialHeadersLeavesBodyAndOtherHeaders(t *testing.T) {
	raw := "POST /login HTTP/1.1\nContent-Type: application/json\n\n{\"password\":\"hunter2hunter2\"}"
	if got := CredentialHeaders(raw); got != raw {
		t.Fatalf("body credentials are out of scope for this pass:\n%q", got)
	}
}

func TestCredentialHeadersHandlesLFAndMissingBody(t *testing.T) {
	raw := "GET / HTTP/1.1\nCookie: sid=abcdef123456\n"
	got := CredentialHeaders(raw)
	if strings.Contains(got, "abcdef123456") {
		t.Fatalf("leaked over LF endings:\n%q", got)
	}
	if !strings.HasSuffix(got, "\n") {
		t.Fatalf("trailing newline lost: %q", got)
	}
}

func TestCredentialHeadersMasksFoldedContinuation(t *testing.T) {
	raw := "GET / HTTP/1.1\r\nCookie: sid=abcdef123456;\r\n\tcarried=fedcba654321\r\n\r\nbody"
	got := CredentialHeaders(raw)
	for _, secret := range []string{"abcdef123456", "fedcba654321"} {
		if strings.Contains(got, secret) {
			t.Errorf("folded continuation leaked %q:\n%q", secret, got)
		}
	}
}

func TestCredentialHeadersIsIdempotentAndStable(t *testing.T) {
	raw := "GET / HTTP/1.1\r\nAuthorization: Bearer abcdefghijklmnop\r\n\r\n"
	once := CredentialHeaders(raw)
	if twice := CredentialHeaders(once); twice != once {
		t.Fatalf("not idempotent:\n%q\n%q", once, twice)
	}
	if CredentialHeaders(raw) != once {
		t.Fatal("not deterministic")
	}
	other := CredentialHeaders("GET / HTTP/1.1\r\nAuthorization: Bearer abcdefghijklmnoq\r\n\r\n")
	if other == once {
		t.Fatal("distinct credentials must produce distinct placeholders")
	}
}

func TestCredentialHeadersEmptyAndHeaderOnly(t *testing.T) {
	if CredentialHeaders("") != "" {
		t.Fatal("empty input")
	}
	if got := CredentialHeaders("not an http message at all"); got != "not an http message at all" {
		t.Fatalf("single line altered: %q", got)
	}
}

func TestCredentialHeadersMasksCredentialLikeNames(t *testing.T) {
	secrets := map[string]string{
		"X-Amz-Security-Token": "amzsecret111", "PRIVATE-TOKEN": "glpat222", "X-Session-Token": "sess333",
		"Api-Key": "apikey444", "X-Goog-Api-Key": "goog555", "X-Vault-Token": "vault666",
		"X-CSRF-Token": "csrf777", "X-Amz-Credential": "cred888", "X-Client-Secret": "sec999",
	}
	raw := "GET /x HTTP/1.1\r\nHost: example.com\r\n"
	for name, v := range secrets {
		raw += name + ": " + v + "\r\n"
	}
	keep := []string{"Content-Length: 12", "Sec-Fetch-Mode: cors", "Sec-Fetch-Site: same-origin", "Accept-Encoding: gzip", "Cache-Control: no-cache"}
	raw += strings.Join(keep, "\r\n") + "\r\n\r\nbody"
	got := CredentialHeaders(raw)
	for name, v := range secrets {
		if strings.Contains(got, v) {
			t.Errorf("%s leaked %q", name, v)
		}
	}
	for _, k := range keep {
		if !strings.Contains(got, k) {
			t.Errorf("harmless header altered: %q\n%s", k, got)
		}
	}
}

func TestCredentialHeadersMasksRequestLineQuerySecrets(t *testing.T) {
	raw := "GET /cb?access_token=tok1234abcd&api_key=key5678efgh&page=2 HTTP/1.1\r\nHost: example.com\r\n\r\n"
	got := CredentialHeaders(raw)
	for _, v := range []string{"tok1234abcd", "key5678efgh"} {
		if strings.Contains(got, v) {
			t.Errorf("request line leaked %q: %q", v, got)
		}
	}
	if !strings.HasPrefix(got, "GET /cb?access_token=[redacted") || !strings.Contains(got, "&page=2 HTTP/1.1\r\n") {
		t.Errorf("request line structure lost: %q", got)
	}
	if status := "HTTP/1.1 200 OK\r\n\r\n"; CredentialHeaders(status) != status {
		t.Error("status line altered")
	}
}

func TestCredentialHeadersTreatsBareCRAsLineEnd(t *testing.T) {
	raw := "GET / HTTP/1.1\r\nX-Foo: a\rAuthorization: Bearer SECRETabcdef123\r\n\r\nbody"
	got := CredentialHeaders(raw)
	if strings.Contains(got, "SECRETabcdef123") {
		t.Fatalf("bare CR smuggled a credential past masking: %q", got)
	}
	if !strings.Contains(got, "X-Foo: a\rAuthorization: [redacted") || !strings.HasSuffix(got, "\r\n\r\nbody") {
		t.Fatalf("line endings not preserved: %q", got)
	}
}

// Headers that merely mention a credential word are evidence, not secrets:
// a CORS finding rests on Access-Control-Allow-Credentials, an auth finding on
// WWW-Authenticate. Masking keys on whole name segments.
func TestCredentialHeaderNamesAvoidEvidenceHeaders(t *testing.T) {
	for _, name := range []string{
		"WWW-Authenticate", "Proxy-Authenticate", "Authentication-Info",
		"Access-Control-Allow-Credentials", "Access-Control-Allow-Headers",
		"X-Author", "X-Session-Count", "Sec-WebSocket-Key", "Content-Length", "Sec-Fetch-Mode",
	} {
		if isCredentialHeaderName(strings.ToLower(name)) {
			t.Errorf("%s must stay clear", name)
		}
	}
	for _, name := range []string{
		"Authorization", "X-Amz-Security-Token", "PRIVATE-TOKEN", "X-Session-Token", "Api-Key",
		"X-Goog-Api-Key", "X-Vault-Token", "X-Amz-Credential", "X-Hub-Signature-256", "X-Auth",
		"X-Session-Id", "X-Client-Secret", "X-Access-Key", "X-Csrf-Token", "Cookie",
	} {
		if !isCredentialHeaderName(strings.ToLower(name)) {
			t.Errorf("%s must be masked", name)
		}
	}
}
