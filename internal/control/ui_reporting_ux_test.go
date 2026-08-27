package control

import (
	"regexp"
	"strings"
	"testing"
)

// These tests cover reporting/recon/settings contracts that are easy to break
// in the no-build-step UI: every assertion maps to a real task-completion or
// accessibility failure found during the feature audit.
func TestUIReportingFindingCreateAndDeleteContracts(t *testing.T) {
	findings := executableJS(readUIAsset(t, "js/findings.js"))
	rawFindings := readUIAsset(t, "js/findings.js")

	for _, want := range []string{
		"#fcSave",
		"aria-busy",
		"/api/findings",
		"uiConfirm(",
		"await loadFindings()",
	} {
		if !strings.Contains(findings, want) {
			t.Errorf("findings UI missing create/delete contract %q", want)
		}
	}
	if !strings.Contains(rawFindings, "finding created") {
		t.Error("successful finding creation must provide visible confirmation")
	}
	if !regexp.MustCompile(`(?s)#fcSave.*?/api/findings.*?POST`).MatchString(findings) {
		t.Error("#fcSave must POST the existing finding form through /api/findings")
	}
}

func TestUIDeviceSelectorsAreKeyboardOperable(t *testing.T) {
	settings := executableJS(readUIAsset(t, "js/settings.js"))
	for _, want := range []string{
		"ArrowDown", "ArrowUp", "Home", "End", "role=\"option\"",
		"data-device-option", "focus()", "focus return",
	} {
		if !strings.Contains(settings, want) {
			t.Errorf("custom device listboxes missing keyboard contract %q", want)
		}
	}
}

func TestUIMapReportingContracts(t *testing.T) {
	mapJS := executableJS(readUIAsset(t, "js/map.js"))
	for _, want := range []string{
		"mapHiddenNoiseAction",
		"Show all statuses",
		"aria-sort",
		"preserveRole",
		"localStorage.setItem(MAP_DOMAIN_KEY",
		"localStorage.setItem(MAP_HIDE_NOISE_KEY,'0')",
	} {
		if !strings.Contains(mapJS, want) {
			t.Errorf("map UI missing contract %q", want)
		}
	}
	if strings.Contains(mapJS, "el.setAttribute('role','button')") && !strings.Contains(mapJS, "preserveRole") {
		t.Error("map keyboard wiring must not replace native table row/header semantics")
	}
}

func TestUIFindingsReadModeAndNotesRecoveryContracts(t *testing.T) {
	findings := executableJS(readUIAsset(t, "js/findings.js"))
	if !strings.Contains(findings, "find-verif-read") {
		t.Error("Findings Read mode must render verification instructions as read-only content")
	}
	if regexp.MustCompile(`(?s)const verifBanner.*?\$\('#findVerifInstr'`).MatchString(findings) &&
		!strings.Contains(findings, "edit ?") {
		t.Error("verification textarea must only be emitted in Edit mode")
	}

	notes := readUIAsset(t, "js/notes.js")
	for _, want := range []string{"data-notes-retry", "state-error-msg", "Retry", "load notes"} {
		if !strings.Contains(notes, want) {
			t.Errorf("notes UI missing recovery contract %q", want)
		}
	}
}
