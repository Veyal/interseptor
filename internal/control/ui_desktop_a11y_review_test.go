package control

import (
	"regexp"
	"strings"
	"testing"
)

// Desktop-skin review fixes that are pure stylesheet / markup contracts.
func TestUIDesktopSkinKeepsUnavailableStateHighContrastAndAnnouncements(t *testing.T) {
	icpt := stripCSSComments(readUIAsset(t, "css/desktop/panel-intercept.css"))
	if !strings.Contains(icpt, "#interceptWarning:not(.is-unavailable){display:none!important}") {
		t.Fatal("the desktop Intercept panel must keep the 'state unavailable' error (with Retry) visible")
	}
	app := readUIAsset(t, "js/app.js")
	if !strings.Contains(app, "classList.add('is-unavailable')") || !strings.Contains(app, "classList.remove('is-unavailable')") {
		t.Fatal("app.js must add is-unavailable on failure and remove it when the state loads")
	}
	shell := stripCSSComments(readUIAsset(t, "css/desktop/shell.css"))
	for _, m := range regexp.MustCompile(`:is\(([^)]*)\)\{display:none!important\}`).FindAllStringSubmatch(shell, -1) {
		if strings.Contains(m[1], "#icptStat") {
			t.Fatal("#icptStat is an aria-live status: visually hide it, never display:none it")
		}
	}
	if !strings.Contains(shell, "html.desktop-app.desktop-macos #cmdkBtn kbd+kbd") || strings.Contains(shell, "html.desktop-app #cmdkBtn kbd+kbd") {
		t.Fatal("the palette chord's negative margin belongs to the macOS glyph chord only")
	}
	for name, needles := range map[string][]string{
		"css/desktop/states.css":     {`html.desktop-app:not([data-theme="hc"]):not(#dsk-x) :is(#main,.panel`},
		"css/desktop/components.css": {`html.desktop-app:not([data-theme="hc"]):not(#dsk-x) .cmdk-input:focus-visible`, `html.desktop-app:not([data-theme="hc"]) .ui-select-opt.active`},
	} {
		css := readUIAsset(t, name)
		for _, n := range needles {
			if !strings.Contains(css, n) {
				t.Errorf("%s: high contrast must keep its focus ring: missing %q", name, n)
			}
		}
	}
	if !strings.Contains(readUIAsset(t, "js/desktop-shell.js"), "mac ? '' : '+'") {
		t.Fatal("the palette hint joins Ctrl and K with + on Windows/Linux")
	}
}
