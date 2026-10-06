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

func TestUIViewportExtendsIntoSafeArea(t *testing.T) {
	for _, name := range []string{"index.html", "login.html"} {
		if !strings.Contains(readUIAsset(t, name), "viewport-fit=cover") {
			t.Errorf("%s viewport meta must declare viewport-fit=cover so env(safe-area-inset-*) applies", name)
		}
	}
	css := readUIAsset(t, "app.css")
	for _, want := range []string{"env(safe-area-inset-top", "env(safe-area-inset-left", "env(safe-area-inset-right", "env(safe-area-inset-bottom"} {
		if !strings.Contains(css, want) {
			t.Errorf("app.css never reads %s", want)
		}
	}
}

// mediaBlock returns the concatenated bodies of every @media block whose
// prelude equals q.
func mediaBlock(t *testing.T, css, q string) string {
	t.Helper()
	var out strings.Builder
	for from := 0; ; {
		rel := strings.Index(css[from:], q+"{")
		if rel < 0 {
			break
		}
		at := from + rel
		depth, end := 0, -1
		for i := at + len(q); i < len(css) && end < 0; i++ {
			switch css[i] {
			case '{':
				depth++
			case '}':
				if depth--; depth == 0 {
					end = i
				}
			}
		}
		if end < 0 {
			t.Fatalf("media block %q is unterminated", q)
		}
		out.WriteString(css[at+len(q)+1 : end])
		from = end
	}
	if out.Len() == 0 {
		t.Fatalf("media block %q not found", q)
	}
	return out.String()
}

// .trow height is shared with the virtualized History list (ROW_H in proxy.js),
// so it is exposed as --row-h for that module instead of being resized here.
func TestUIFlowRowHeightIsATokenForVirtualization(t *testing.T) {
	if !strings.Contains(readUIAsset(t, "app.css"), ".trow{height:var(--row-h,30px)") {
		t.Error(".trow height must read --row-h so the virtual list and CSS can agree")
	}
}

func TestUICoarsePointerGetsFortyFourPixelTargets(t *testing.T) {
	css := readUIAsset(t, "app.css")
	at := strings.LastIndex(css, "@media (pointer:coarse){")
	if at < 0 {
		t.Fatal("no @media (pointer:coarse) block")
	}
	if strings.Count(css, "pointer:coarse") != 1 {
		t.Error("touch sizing must live in a single pointer:coarse block")
	}
	block := mediaBlock(t, css, "@media (pointer:coarse)")
	for _, want := range []string{
		".btn,", ".tab,", ".search,", ".ui-select-trigger", ".seg button", ".chip .x", ".btn.xs", "min-height:44px",
		"input[type=checkbox],input[type=radio]", "width:20px",
	} {
		if !strings.Contains(block, want) {
			t.Errorf("pointer:coarse block missing %q", want)
		}
	}
	for _, tail := range regexp.MustCompile(`@media`).FindAllStringIndex(css[at+10:], -1) {
		t.Errorf("another @media block follows the pointer:coarse block at offset %d", tail[0])
	}
}

func TestUIResponsiveCollapseAt900720And600(t *testing.T) {
	css := readUIAsset(t, "app.css")
	tablet := mediaBlock(t, css, "@media (min-width:721px) and (max-width:900px)")
	for _, want := range []string{"--nav-rail-w:56px", ".nav-rail-group-label", ".nav-rail-foot", "font-size:0"} {
		if !strings.Contains(tablet, want) {
			t.Errorf("tablet rail collapse missing %q", want)
		}
	}
	m900 := mediaBlock(t, css, "@media (max-width:900px)")
	if !strings.Contains(m900, ".toolbar{") || !strings.Contains(m900, "flex-wrap:wrap") || !strings.Contains(m900, "overflow-x:visible") {
		t.Error("900px block must let .toolbar wrap instead of scrolling sideways")
	}
	m720 := mediaBlock(t, css, "@media (max-width:720px)")
	for _, want := range []string{
		".authz-row{grid-template-columns:minmax(0,1fr) 56px minmax(0,auto)",
		"overflow-wrap:anywhere", "#appRow{", "env(safe-area-inset-bottom",
	} {
		if !strings.Contains(m720, want) {
			t.Errorf("720px block missing %q", want)
		}
	}
	m600 := mediaBlock(t, css, "@media (max-width:600px)")
	for _, want := range []string{
		".oob-row{grid-template-columns:44px minmax(0,1fr) auto", ".authz-id{flex-wrap:wrap", ".act-filter{width:100%",
		".hi-prompt .hi-input{width:100%", "min-width:0",
	} {
		if !strings.Contains(m600, want) {
			t.Errorf("600px block missing %q", want)
		}
	}
}
