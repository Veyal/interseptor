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
