package redact

import (
	"strings"
	"testing"
)

// A bare CR ends a line for the report and preview renderers, so the masker
// treats it as one too. It must not, however, be read as the empty line that
// ends the header block: "GET / HTTP/1.1\r\r\nAuthorization: …" then left every
// later header unmasked, and the renderer drew the credential on its own line.
func TestCredentialHeadersBareCRDoesNotEndTheHeaderBlock(t *testing.T) {
	raw := "GET / HTTP/1.1\r\r\nAuthorization: Bearer SECRETabcdef123\r\n" +
		"Host: example.com\r\nX-Api-Key: key0123456789\r\n\r\nbody"
	got := CredentialHeaders(raw)
	for _, secret := range []string{"SECRETabcdef123", "key0123456789"} {
		if strings.Contains(got, secret) {
			t.Fatalf("credential %q survived masking: %q", secret, got)
		}
	}
	if !strings.HasSuffix(got, "\r\n\r\nbody") {
		t.Fatalf("body or line endings altered: %q", got)
	}
	// Every byte of structure outside the masked values must survive.
	if !strings.HasPrefix(got, "GET / HTTP/1.1\r\r\nAuthorization: [redacted") {
		t.Fatalf("request line or header structure lost: %q", got)
	}
}

// A CRLF blank line still ends the header block, so a body that merely looks
// like a header is left alone (masking must not rewrite evidence).
func TestCredentialHeadersBodyAfterBlankLineIsUntouched(t *testing.T) {
	raw := "GET / HTTP/1.1\r\nHost: example.com\r\n\r\nAuthorization: Bearer SECRETabcdef123"
	if got := CredentialHeaders(raw); !strings.Contains(got, "SECRETabcdef123") {
		t.Fatalf("body was masked: %q", got)
	}
}
