package control

import (
	"regexp"
	"strings"
	"testing"
)

// findings.css and surfaces.css must follow the same token contract as app.css:
// every var() resolves, no literal type sizes, and no off-scale radii.
func TestUISecondaryStylesheetsUseDeclaredTokens(t *testing.T) {
	app := readUIAsset(t, "app.css")
	declared := parseThemeBlock(t, app, ":root{")
	for _, name := range []string{"findings.css", "surfaces.css"} {
		css := readUIAsset(t, name)
		for _, m := range regexp.MustCompile(`var\((--[a-zA-Z0-9-]+)([,)])`).FindAllStringSubmatch(css, -1) {
			if _, ok := declared[m[1]]; !ok && !strings.Contains(css, m[1]+":") {
				t.Errorf("%s reads undeclared token %s", name, m[1])
			}
		}
		for _, m := range regexp.MustCompile(`font-size:\s*([0-9.]+)px`).FindAllStringSubmatch(css, -1) {
			t.Errorf("%s: literal font-size:%spx bypasses the --fs-* scale", name, m[1])
		}
		for _, m := range regexp.MustCompile(`font:[^;}]*?\b([0-9.]+)px`).FindAllStringSubmatch(css, -1) {
			t.Errorf("%s: literal %spx in font shorthand bypasses the --fs-* scale", name, m[1])
		}
		for _, m := range regexp.MustCompile(`border-radius:\s*([^;}]*)`).FindAllStringSubmatch(css, -1) {
			if regexp.MustCompile(`\b(4|7|8|10)px`).MatchString(m[1]) {
				t.Errorf("%s: off-scale radius %q; use --r-* tokens", name, m[1])
			}
		}
	}
	if _, ok := declared["--fs-3xl"]; !ok {
		t.Error("--fs-3xl token is missing")
	}
}

func TestUIAppStylesheetRadiiAreOnScale(t *testing.T) {
	css := readUIAsset(t, "app.css")
	for _, m := range regexp.MustCompile(`border-radius:\s*([^;}]*)`).FindAllStringSubmatch(css, -1) {
		if regexp.MustCompile(`\b(4|7|8|10)px`).MatchString(m[1]) {
			t.Errorf("app.css: off-scale radius %q; use --r-* tokens", m[1])
		}
	}
}

// One declaration per paired button alias, and a primary button that is
// disabled must stop looking like the live call to action.
func TestUIButtonAliasesShareOneRule(t *testing.T) {
	css := readUIAsset(t, "app.css")
	for _, want := range []string{
		".btn.accent,.btn-primary{",
		".btn.accent:hover,.btn-primary:hover{",
		".btn.danger,.btn-danger{",
		".btn.danger:hover,.btn-danger:hover{",
		".btn.accent:disabled,.btn-primary:disabled{",
	} {
		if !strings.Contains(css, want) {
			t.Errorf("app.css missing merged button rule %q", want)
		}
	}
	for _, stale := range []string{"\n.btn-primary{", "\n.btn-danger{", "Not wired into markup yet"} {
		if strings.Contains(css, stale) {
			t.Errorf("app.css still carries stale duplicate %q", stale)
		}
	}
}

func TestUIRepTabCloseIsLegibleAndFocusable(t *testing.T) {
	css := readUIAsset(t, "app.css")
	rule := regexp.MustCompile(`\.rep-tab \.rt-close\{([^}]*)\}`).FindStringSubmatch(css)
	if rule == nil {
		t.Fatal(".rep-tab .rt-close rule missing")
	}
	for _, want := range []string{"opacity:1", "color:var(--fg3)", "min-width:24px", "min-height:24px"} {
		if !strings.Contains(rule[1], want) {
			t.Errorf(".rep-tab .rt-close missing %q", want)
		}
	}
	if !strings.Contains(css, ".rep-tab .rt-close:focus-visible{") {
		t.Error("rep-tab close needs a :focus-visible rule")
	}
}

func TestUIDecorationUsesTokensNotGlowOrLiterals(t *testing.T) {
	css := readUIAsset(t, "app.css")
	for _, glow := range []string{".nav-dot{width:7px", ".sse-dot{width:7px", "box-shadow:0 -4px 12px rgba(0,0,0,0.2)"} {
		if strings.Contains(css, glow) {
			t.Errorf("app.css still has unshared dot/inspector rule %q", glow)
		}
	}
	if !strings.Contains(css, ".dot,.nav-dot,.sse-dot,.act-row .ok{") {
		t.Error("status dots must share one rule")
	}
	if strings.Contains(css, "box-shadow:0 0 6px var(--accent)") {
		t.Error("status dots must not glow")
	}
	declared := parseThemeBlock(t, css, ":root{")
	for _, tok := range []string{"--lightbox-bg", "--lightbox-bar", "--lightbox-fg", "--lightbox-fg-dim", "--lightbox-line"} {
		if _, ok := declared[tok]; !ok {
			t.Errorf("%s token missing", tok)
		}
	}
	for _, line := range strings.Split(css, "\n") {
		if strings.HasPrefix(line, ".img-lightbox") && (strings.Contains(line, "rgba(") || strings.Contains(line, "#fff")) {
			t.Errorf("lightbox rule uses a literal colour: %s", line)
		}
	}
	if !strings.Contains(css, ".toast-item.info{") {
		t.Error("toast needs info styling")
	}
	if strings.Contains(css, "max-width:80vw") || strings.Contains(css, "0 10px 30px") {
		t.Error("toast must use token elevation and a container-relative width")
	}
	surf := readUIAsset(t, "surfaces.css")
	if strings.Contains(surf, "100vw - 24px") {
		t.Error("toast width must not use 100vw")
	}
}

// Classes other UI modules (palette, skip link, secondary panels) are written
// against must exist in the shared stylesheets, otherwise markup that drops its
// inline styles silently renders unstyled.
func TestUISharedClassesAreDefined(t *testing.T) {
	css := readUIAsset(t, "app.css") + readUIAsset(t, "surfaces.css")
	for _, sel := range []string{
		".cmdk-shell", ".cmdk-input", ".cmdk-list", ".cmdk-row", `.cmdk-row[aria-selected="true"]`,
		".cmdk-foot", ".cmdk-empty", ".skip", ".skip:focus", ".visually-hidden", ".sr-only",
		".cell-empty", ".msg-ok", ".msg-err", ".msg-warn", ".mono-accent", ".is-update",
	} {
		if !strings.Contains(css, sel+"{") && !strings.Contains(css, sel+",") && !strings.Contains(css, sel+" {") {
			t.Errorf("shared class %s is not defined", sel)
		}
	}
	skip := regexp.MustCompile(`\.skip:focus[^{]*\{([^}]*)\}`).FindStringSubmatch(css)
	if skip == nil || !strings.Contains(skip[1], "safe-area-inset-top") {
		t.Error(".skip:focus must reveal the link inside the top safe-area inset")
	}
}
