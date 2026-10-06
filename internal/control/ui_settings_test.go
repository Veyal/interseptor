package control

import (
	"io/fs"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// WP12 (UI overhaul): Settings, the engagement sheet, the setup wizard and the
// first-run checklist. Pure logic runs under node (ui/_js-tests); everything
// DOM-shaped is asserted statically against the embedded assets.

var wp12Modules = []string{
	"js/settings-model.js", "js/checklist-model.js", "js/settings-appearance.js", "js/settings-health.js",
	"js/settings-confirm.js", "js/checklist.js", "js/engagement.js",
}

var settingsSections = []string{"proxy", "tls", "devices", "scope", "scanner", "session", "project", "api", "appearance"}

func TestUISettingsPureLogicUnderNode(t *testing.T) {
	node := requireNode(t)
	cmd := exec.Command(node, "--test", "_js-tests/settings-model.test.mjs", "_js-tests/checklist-model.test.mjs")
	cmd.Dir = "ui"
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("node --test failed: %v\n%s", err, out)
	}
}

func TestUISettingsPureModulesStayDOMFree(t *testing.T) {
	for _, name := range []string{"js/settings-model.js", "js/checklist-model.js"} {
		src := executableJS(readUIAsset(t, name))
		if regexp.MustCompile(`(?m)^import `).MatchString(src) {
			t.Errorf("%s imports a module; keep it loadable under node", name)
		}
		if regexp.MustCompile(`\b(document|window)\.`).MatchString(src) {
			t.Errorf("%s touches the DOM", name)
		}
	}
}

func TestUISettingsModulesResolveTheirImports(t *testing.T) {
	imp := regexp.MustCompile(`(?:import|from)\s*\(?\s*'\./([a-z-]+\.js)'`)
	for _, name := range append([]string{"js/settings.js", "js/setup.js", "js/apipanel.js"}, wp12Modules...) {
		for _, m := range imp.FindAllStringSubmatch(readUIAsset(t, name), -1) {
			if _, err := fs.Stat(uiFS, "ui/js/"+m[1]); err != nil {
				t.Errorf("%s imports ./%s which is not embedded", name, m[1])
			}
		}
	}
}

func TestUISettingsNewModulesWriteNoInlineStyleOrHTMLStrings(t *testing.T) {
	for _, name := range wp12Modules {
		src := executableJS(readUIAsset(t, name))
		if strings.Contains(src, `style="`) || strings.Contains(src, "style='") || strings.Contains(src, "cssText") || regexp.MustCompile(`\.style\.[a-zA-Z]+\s*=`).MatchString(src) {
			t.Errorf("%s writes inline styles; use classes and the hidden attribute", name)
		}
		if regexp.MustCompile(`\.(innerHTML|outerHTML)\s*=|insertAdjacentHTML`).MatchString(src) {
			t.Errorf("%s assigns HTML strings; build nodes with textContent", name)
		}
	}
}

func TestUISettingsKeepsSectionIdsAndNavLinks(t *testing.T) {
	index := readUIAsset(t, "index.html")
	requireUIContains(t, index, `id="setNav"`, `id="setSearch"`, `id="settingsSectionSelect"`, `id="setNavEmpty"`)
	for _, sec := range settingsSections {
		if !regexp.MustCompile(`<section class="set-sec" data-sec="` + sec + `"`).MatchString(index) {
			t.Errorf("Settings section %q is missing", sec)
		}
		if !regexp.MustCompile(`<button[^>]*data-sec="` + sec + `"`).MatchString(index) {
			t.Errorf("#setNav has no button for %q", sec)
		}
		if !strings.Contains(index, `<option value="`+sec+`">`) {
			t.Errorf("section picker has no option for %q", sec)
		}
	}
}

func TestUISettingsAppearanceSection(t *testing.T) {
	index := readUIAsset(t, "index.html")
	for _, id := range []string{"themeSeg", "densitySeg", "singleKeySwitch", "hintsSwitch"} {
		if !strings.Contains(index, `id="`+id+`"`) {
			t.Errorf("Appearance control #%s missing", id)
		}
	}
	for _, c := range []string{"system", "dark", "light", "hc"} {
		if !strings.Contains(index, `data-theme-choice="`+c+`"`) {
			t.Errorf("theme choice %s missing", c)
		}
	}
	for _, c := range []string{"compact", "default", "comfortable"} {
		if !strings.Contains(index, `data-density-choice="`+c+`"`) {
			t.Errorf("density choice %s missing", c)
		}
	}
	src := executableJS(readUIAsset(t, "js/settings-appearance.js"))
	requireUIContains(t, src, "setDensity(", "writeSingleKeyPref(", "readSingleKeyPref(", "themeStoragePlan(", "writeHintsPref(",
		"prefers-color-scheme", "aria-pressed", "data-hints", "'densitychange'")
}

func TestUISettingsEngagementUsesOneRenderer(t *testing.T) {
	index := readUIAsset(t, "index.html")
	if !strings.Contains(index, `id="briefFormMount"`) {
		t.Fatal("Settings must mount the engagement brief through #briefFormMount")
	}
	if strings.Contains(index, `<textarea id="brief-`) {
		t.Error("the brief textareas must come from the single renderer, not static markup")
	}
	src := executableJS(readUIAsset(t, "js/engagement.js"))
	requireUIContains(t, src, "renderBriefForm(", "createAutosave(", "openSheet(", "'engagementSheet'", "registerHook('openEngagementSheet'",
		"aria-describedby", "'briefSaveBtn'", "'briefVersion'", "validateTargetLines(", "saveStatusText(", "'ctxScope'", "openAuthz")
	if n := strings.Count(src, "function renderBriefForm("); n != 1 {
		t.Errorf("renderBriefForm defined %d times, want one shared renderer", n)
	}
	for _, f := range []string{"scope", "authorisation", "conductRules", "rateLimits", "doNotTouch", "credentialPolicy"} {
		if !strings.Contains(src, "'"+f+"'") {
			t.Errorf("brief field %s missing from the renderer", f)
		}
	}
	css := readUIAsset(t, "settings.css")
	requireUIContains(t, css, ".brief-form", ".field-error")
}

func TestUISettingsHealthChipsPairIconAndText(t *testing.T) {
	src := executableJS(readUIAsset(t, "js/settings-health.js"))
	requireUIContains(t, src, "sectionHealth(", "'/api/readiness'", "check-circle", "alert-tri", "ring", "projectState.subscribe(", "settings-health")
	css := readUIAsset(t, "settings.css")
	requireUIContains(t, css, ".settings-health", `[data-state="warn"]`)
}

func TestUISettingsSearchHighlightsAndAnnouncesCount(t *testing.T) {
	src := executableJS(readUIAsset(t, "js/settings.js"))
	requireUIContains(t, src, "matchSections(", "searchSummary(", "highlightRanges(")
	index := readUIAsset(t, "index.html")
	if !regexp.MustCompile(`id="setNavEmpty"[^>]*role="status"`).MatchString(index) && !regexp.MustCompile(`role="status"[^>]*id="setNavEmpty"`).MatchString(index) {
		t.Error("#setNavEmpty must be a polite status region so the match count is announced")
	}
}

func TestUISettingsPhoneUsesPushedPages(t *testing.T) {
	src := executableJS(readUIAsset(t, "js/settings.js"))
	requireUIContains(t, src, "createSplitPane(", "stackBelow:720", "showDetail(", "scopeKey:projectStorageKey")
	css := readUIAsset(t, "settings.css")
	requireUIContains(t, css, "@media (max-width:720px)", ".settings-wrap.split")
	// The compact picker would duplicate the grouped list.
	if !regexp.MustCompile(`(?s)@media \(max-width:720px\).*\.settings-picker\{display:none`).MatchString(css) {
		t.Error("phone Settings must hide the section picker in favour of the grouped list")
	}
}

func TestUISettingsDestructiveActionsNeedTypedConfirm(t *testing.T) {
	settings := executableJS(readUIAsset(t, "js/settings.js"))
	for _, id := range []string{"Delete selected hosts", "Keep only selected", "Purge by pattern"} {
		at := strings.Index(settings, "confirmTyped(uiConfirm,'"+id+"'")
		if at < 0 {
			t.Errorf("%q must use confirmTyped", id)
		}
	}
	confirm := executableJS(readUIAsset(t, "js/settings-confirm.js"))
	requireUIContains(t, confirm, "typedConfirmMatches(", "confirmOk", "disabled", "aria-describedby", "export function confirmTyped(")
	if got := strings.Count(settings, "registerProjectSwitchGuard("); got != 1 {
		t.Errorf("registerProjectSwitchGuard registrations changed: %d, want the single existing one", got)
	}
}

func TestUISetupHasFinalTargetAndScopeStep(t *testing.T) {
	src := executableJS(readUIAsset(t, "js/setup.js"))
	requireUIContains(t, src, "const LAST = 4", "Set target and enable scope", "setupTargetInput", "'/api/engagement-brief'")
}

func TestUIChecklistIsDerivedNeverTicked(t *testing.T) {
	src := executableJS(readUIAsset(t, "js/checklist.js"))
	requireUIContains(t, src, "export function mountChecklist(", "deriveChecklist(", "'progress'", "projectStorageKey(", "registerHook('mountChecklist'",
		"projectState.subscribe(", "'/api/readiness'", "Do it", "aria-live")
	if strings.Contains(src, `type="checkbox"`) || strings.Contains(src, "'checkbox'") {
		t.Error("checklist steps must be derived from state, not manual checkboxes")
	}
}

func TestUISettingsStylesheetContract(t *testing.T) {
	css := readUIAsset(t, "settings.css")
	requireUIContains(t, css, ":focus-visible", "@media (pointer:coarse)", "min-height:44px", ".checklist", ".settings-appearance")
	if m := regexp.MustCompile(`min-width:\s*(\d+)px`).FindAllStringSubmatch(css, -1); m != nil {
		for _, x := range m {
			if px, _ := strconv.Atoi(x[1]); px > 375 {
				t.Errorf("settings.css sets min-width %spx; nothing may be wider than the 375px floor", x[1])
			}
		}
	}
}

func TestUIEngagementSheetIsRegisteredAsModal(t *testing.T) {
	core := readUIAsset(t, "js/core.js")
	if !strings.Contains(core, "'engagementSheet'") {
		t.Error("engagementSheet must stay in MODAL_IDS so shortcut gating and the focus trap see it")
	}
}
