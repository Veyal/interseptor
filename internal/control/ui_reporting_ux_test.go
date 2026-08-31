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
		"data-device-option", "focus()", "focus return", "Android device",
		"iOS device", "Remove proxy listener", "Proxy listener host",
		"Control UI bind host", "Control UI bind port",
		"aria-controls", "aria-current", "settings-section-",
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
		"localStorage.setItem(projectStorageKey(MAP_DOMAIN_KEY)",
		"localStorage.setItem(projectStorageKey(MAP_HIDE_NOISE_KEY),'0')",
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
	for _, want := range []string{"renderLoadError", "data-findings-retry", "loadFindings"} {
		if !strings.Contains(findings, want) {
			t.Errorf("findings load failure must remain visible and retryable: missing %q", want)
		}
	}
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

func TestUIReportingAsyncActionsHaveTruthfulFeedback(t *testing.T) {
	codecs := executableJS(readUIAsset(t, "js/codecs.js"))
	for _, want := range []string{"role=\"option\"", "wireCodecRow", "codecBusy", "aria-busy", "ArrowDown", "tabindex=", "codecLoadEpoch", "epoch!==codecLoadEpoch", "codecSel!==id", "codecDocsLoadEpoch", "codecListLoadEpoch"} {
		if !strings.Contains(codecs, want) {
			t.Errorf("codec list/editor must keep listbox semantics and expose pending state: missing %q", want)
		}
	}
	if strings.Contains(codecs, "aria-activedescendant") {
		t.Error("codec options use roving DOM focus and must not also put aria-activedescendant on the listbox")
	}
	scanner := executableJS(readUIAsset(t, "js/scanner.js"))
	if !strings.Contains(scanner, "checks-edit-target") {
		t.Error("check enable toggles must have a separate edit control, not a nested interactive row")
	}
	if strings.Contains(scanner, "box.querySelectorAll('.checks-pick[data-id]')") && strings.Contains(scanner, "wireRowKey(el,open)") {
		t.Error("check rows must not be promoted to role=button around their enable checkbox")
	}
	for _, want := range []string{"ArrowDown", "ArrowUp", "tabindex=", "scan-issue-"} {
		if !strings.Contains(scanner, want) {
			t.Errorf("scanner issue list needs roving keyboard selection: missing %q", want)
		}
	}
	if strings.Contains(scanner, "aria-activedescendant") {
		t.Error("scanner issues use roving DOM focus and must not also put aria-activedescendant on the listbox")
	}
	for _, want := range []string{"checkLoadEpoch", "epoch!==checkLoadEpoch", "checkSelId!==id"} {
		if !strings.Contains(scanner, want) {
			t.Errorf("check editor must ignore stale selection responses: missing %q", want)
		}
	}
	mapJS := executableJS(readUIAsset(t, "js/map.js"))
	for _, want := range []string{"renderLoadError(warn,'Parameters'", "data-map-params-retry", "aria-live", "Map domain", "Map method", "Map status", "Map tag"} {
		if !strings.Contains(mapJS, want) {
			t.Errorf("Map parameter mining must expose an actionable error state: missing %q", want)
		}
	}

	authz := executableJS(readUIAsset(t, "js/authz.js"))
	for _, want := range []string{"authzActionBusy", "aria-busy", "Run failed"} {
		if !strings.Contains(authz, want) {
			t.Errorf("authorization actions must prevent duplicate runs and expose failures: missing %q", want)
		}
	}
	index := readUIAsset(t, "index.html")
	for _, want := range []string{`id="authzMode" role="group"`, `id="authzStatus"`, `role="status" aria-live="polite"`} {
		if !strings.Contains(index, want) {
			t.Errorf("authorization mode/status semantics missing %q", want)
		}
	}
	if strings.Contains(authz, "$('#authzResults').setAttribute('role','status')") {
		t.Error("interactive authorization results must not be placed inside role=status")
	}

	setup := executableJS(readUIAsset(t, "js/setup.js"))
	for _, want := range []string{"setupActionBusy", "aria-busy", "Adding…"} {
		if !strings.Contains(setup, want) {
			t.Errorf("setup actions must expose pending state: missing %q", want)
		}
	}
	activity := executableJS(readUIAsset(t, "js/activity.js"))
	if !strings.Contains(activity, "uiConfirm(") {
		t.Error("clearing the activity evidence must require confirmation")
	}
}
