package control

import (
	"regexp"
	"strings"
	"testing"
)

// Desktop and tablet already have the left rail; the phone-only navigation
// (dock sub-control, "All tools" select) and the Settings "Section" picker must
// not show up next to it.

func TestUIDockSegIsPhoneOnly(t *testing.T) {
	css := readUIAsset(t, "mobile.css")
	if !regexp.MustCompile(`(?m)^\.seg\.dock-seg\{display:none`).MatchString(css) {
		t.Error(".dock-seg must default to display:none so the rail is the only desktop nav")
	}
	requireUIRegex(t, css, `(?s)@media \(max-width:720px\)\{.*\.seg\.dock-seg:not\(\[hidden\]\)\{display:flex`)
	requireUIContains(t, css, ".dock-seg[hidden]{display:none}")
	// The base rule must come before the phone block that re-enables it.
	if strings.Index(css, ".seg.dock-seg{display:none") > strings.Index(css, "@media (max-width:720px)") {
		t.Error("base .dock-seg rule must precede the phone media block")
	}
}

func TestUISettingsSectionPickerIsGone(t *testing.T) {
	for _, name := range []string{"index.html", "app.css", "settings.css", "mobile.css", "js/settings.js", "js/app.js", "js/core.js"} {
		src := readUIAsset(t, name)
		for _, gone := range []string{"settings-picker", "settingsSectionSelect", "syncSettingsPicker"} {
			if strings.Contains(src, gone) {
				t.Errorf("%s still references the removed Settings section picker (%s)", name, gone)
			}
		}
	}
}

func TestUISettingsNavStillNavigatesWithoutPicker(t *testing.T) {
	index := readUIAsset(t, "index.html")
	for _, sec := range settingsSections {
		// Real buttons: Tab/Enter/Space work natively, no custom key handling needed.
		if !regexp.MustCompile(`<button[^>]*data-sec="` + sec + `"`).MatchString(index) {
			t.Errorf("#setNav has no <button> for %q", sec)
		}
	}
	js := executableJS(readUIAsset(t, "js/settings.js"))
	requireUIContains(t, js,
		"$$('#setNav button[data-sec]').forEach(b=>b.onclick=",
		"s.hidden=s.dataset.sec!==b.dataset.sec",
		"syncSettingsNavA11y(b)",
		"document.querySelector('#setNav button[data-sec=\"'+sec+'\"]')?.click()",
	)
	// Below the tablet breakpoint the groups must stay visible (as chips), since
	// the picker no longer substitutes for them.
	css := readUIAsset(t, "app.css")
	if regexp.MustCompile(`(?s)@media \(max-width:900px\)\{.*?\.settings-nav-group\{display:none\}`).MatchString(css) {
		t.Error("tablet Settings must keep the nav buttons visible")
	}
	requireUIContains(t, css, ".settings-wrap:not(.split) .settings-nav-group{display:contents}")
}

// The "< Sections" back button is a phone-only control. .btn's display rule
// (specificity 0-1-3) used to outrank `.split-back{display:none}`, so it showed
// beside the always-visible section list on desktop (Settings, Scanner, Intercept).
func TestUISplitBackButtonIsStackModeOnly(t *testing.T) {
	css := readUIAsset(t, "primitives.css")
	requireUIContains(t, css,
		`.split:not([data-mode="stack"]) .split-back{display:none}`,
		`.split[data-mode="stack"] .split-back{display:inline-flex`,
	)
}
