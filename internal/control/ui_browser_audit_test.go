package control

import (
	"os"
	"strings"
	"testing"
)

func TestUIBrowserAuditDigestExcludesIgnoredWorkspaceState(t *testing.T) {
	source, err := os.ReadFile("../../scripts/ui_browser_audit.py")
	if err != nil {
		t.Fatalf("read UI browser audit: %v", err)
	}
	text := string(source)
	for _, want := range []string{
		`"git",`,
		`"ls-files",`,
		`"--cached"`,
		`"--others"`,
		`"--exclude-standard"`,
		`"-z"`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("runtime identity must enumerate tracked or nonignored source files: missing %s", want)
		}
	}

	identityStart := strings.Index(text, "def runtime_source_identity()")
	if identityStart < 0 {
		t.Fatal("runtime_source_identity function not found")
	}
	identityEnd := strings.Index(text[identityStart:], "\ndef percentile(")
	if identityEnd < 0 {
		t.Fatal("runtime_source_identity function boundary not found")
	}
	identity := text[identityStart : identityStart+identityEnd]
	if strings.Contains(identity, ".rglob(") {
		t.Error("runtime identity must not hash ignored workspace files from a recursive filesystem walk")
	}
}

func TestUIBrowserAuditExpectedConsoleErrorsAreOneShot(t *testing.T) {
	source, err := os.ReadFile("../../scripts/ui_browser_audit.py")
	if err != nil {
		t.Fatalf("read UI browser audit: %v", err)
	}
	if !strings.Contains(string(source), "result.expected_console_request_urls.remove(request_url)") {
		t.Error("an expected injected console error must not mask later failures at the same URL")
	}
}
