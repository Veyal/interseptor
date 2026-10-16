package mcp

import (
	"fmt"
	"strings"
	"testing"
)

const lintJWT = "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJleGFtcGxlIn0.c2lnbmF0dXJlZXhhbXBsZQ"

func TestSecretLintWarnsAndSuggestsRedactedFormWithoutEchoingValue(t *testing.T) {
	body := fmt.Sprintf(`[{"type":"text","role":"observation","md":"Request carries Authorization: Bearer abcdefghijklmnopqrstuvwxyz012345 in the header."}]`)
	err, warns := validateFindingFormat(findingFormatInput{
		Severity: "Low", Summary: "A session token " + lintJWT + " appears in the URL query string.",
		Impact: "Token leaks to logs.", Why: "Credentials in URLs.", Target: "wss://api.example.com/ws", Body: body,
	})
	if err != nil {
		t.Fatalf("a secret is a warning, not a rejection: %v", err)
	}
	joined := strings.Join(warns, "\n")
	for _, want := range []string{"jwt", "bearer_token", "[redacted jwt", "redact_value", "summary"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("warnings missing %q:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, lintJWT) || strings.Contains(joined, "abcdefghijklmnopqrstuvwxyz012345") {
		t.Fatalf("warning leaked the secret:\n%s", joined)
	}
	if block := formatWarningsBlock(warns); strings.Contains(block, lintJWT) {
		t.Fatal("rendered block leaked the secret")
	}
}

func TestSecretLintQuietForRedactedText(t *testing.T) {
	_, warns := validateFindingFormat(findingFormatInput{
		Severity: "Low", Summary: "A session token [redacted jwt len=71 sha256=0123456789ab] appears in the URL.",
		Impact: "Leak.", Why: "Credentials in URLs.", Target: "example.com", Partial: true,
	})
	if strings.Contains(strings.Join(warns, "\n"), "redact_value") {
		t.Fatalf("redacted text must not be flagged: %v", warns)
	}
}
