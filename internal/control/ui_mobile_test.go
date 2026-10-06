package control

import (
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

// WP13 (UI overhaul mobile shell): bottom navigation, destination memory,
// badges, hidden-select sync, soft-keyboard handling and phone safe areas. The
// UI has no browser in this session, so DOM behaviour is pinned statically and
// pure logic runs under node.

func mobileRegion(t *testing.T) string {
	t.Helper()
	index := readUIAsset(t, "index.html")
	start := strings.Index(index, "<!-- region:mobile -->")
	end := strings.Index(index, "<!-- /region:mobile -->")
	if start < 0 || end < start {
		t.Fatal("region:mobile markers missing")
	}
	return index[start:end]
}

func TestUIMobileDockModelUnderNode(t *testing.T) {
	node := requireNode(t)
	cmd := exec.Command(node, "--test", "_js-tests/dock-model.test.mjs")
	cmd.Dir = "ui"
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("node --test failed: %v\n%s", err, out)
	}
}

func TestUIMobileDockMarkup(t *testing.T) {
	region := mobileRegion(t)
	requireUIContains(t, region, `<nav id="dock"`, `aria-label="Main"`)
	buttons := regexp.MustCompile(`<button[^>]*data-dock="(\w+)"`).FindAllStringSubmatch(region, -1)
	var got []string
	for _, m := range buttons {
		got = append(got, m[1])
	}
	if strings.Join(got, ",") != "capture,test,recon,report,more" {
		t.Fatalf("dock destinations = %v", got)
	}
	if n := strings.Count(region, `class="dock-label"`); n != 5 {
		t.Errorf("want 5 visible labels, got %d", n)
	}
	if strings.Contains(region, "style=") {
		t.Error("dock markup must not use inline styles")
	}
	for _, tab := range []string{"Capture", "Test", "Recon", "Report", "More"} {
		if !strings.Contains(region, ">"+tab+"<") {
			t.Errorf("dock label %q missing", tab)
		}
	}
}

func TestUIMobileRailAndDockNeverBothExposed(t *testing.T) {
	css := readUIAsset(t, "mobile.css")
	app := readUIAsset(t, "app.css")
	// The rail is display:none (not offscreen) in the phone model.
	if !regexp.MustCompile(`(?s)@media \(max-width:720px\)\{[^@]*#tabs\{display:none`).MatchString(app) &&
		!regexp.MustCompile(`(?s)@media \(max-width:720px\)\s*\{[^@]*#tabs\{display:none`).MatchString(css) {
		t.Error("#tabs must be display:none at <=720px")
	}
	// The dock itself is hidden above the phone breakpoint.
	if !regexp.MustCompile(`(?m)^#dock\{display:none`).MatchString(css) {
		t.Error("#dock must default to display:none")
	}
	requireUIRegex(t, css, `(?s)@media \(max-width:720px\)\{.*#dock\{display:flex`)
	// The legacy select stays in the DOM but is not a second visible nav.
	requireUIRegex(t, css, `(?s)@media \(max-width:720px\)\{.*\.mobile-tool-nav\{display:none`)
	if !strings.Contains(readUIAsset(t, "index.html"), `id="mobileToolSelect"`) {
		t.Error("#mobileToolSelect must stay in the DOM")
	}
}

func TestUIMobileDockTargetsAndSafeArea(t *testing.T) {
	css := readUIAsset(t, "mobile.css")
	requireUIContains(t, css, "var(--bottomnav-h)", "var(--safe-b)", "min-height:44px")
	requireUIRegex(t, css, `html\[data-soft-keyboard="true"\]\s*#dock\{display:none`)
	requireUIRegex(t, css, `\.dock-label\{[^}]*font-size:var\(--fs-xs\)`)
	requireUIRegex(t, css, `\.dock-btn:focus-visible\{[^}]*outline`)
	requireUIRegex(t, css, `\.dock-btn\[aria-current="page"\]`)
	requireUIRegex(t, css, `#appRow\{padding-bottom:0`)
}

func TestUIMobileCSSRules(t *testing.T) {
	css := regexp.MustCompile(`(?s)/\*.*?\*/`).ReplaceAllString(readUIAsset(t, "mobile.css"), "")
	for _, m := range regexp.MustCompile(`min-width\s*:\s*(\d+)px`).FindAllStringSubmatch(css, -1) {
		if m[1] != "0" && len(m[1]) >= 3 && m[1] > "375" {
			t.Errorf("mobile.css sets min-width %spx, wider than 375px", m[1])
		}
	}
	for _, m := range regexp.MustCompile(`transition(?:-property)?\s*:\s*([^;}]+)`).FindAllStringSubmatch(css, -1) {
		for _, part := range strings.Split(m[1], ",") {
			if p := strings.Fields(strings.TrimSpace(part)); len(p) > 0 && p[0] != "transform" && p[0] != "opacity" && p[0] != "none" {
				t.Errorf("mobile.css transitions %q; only transform and opacity may animate", p[0])
			}
		}
	}
	// Dock indicator animates scaleX and must be covered by reduced motion.
	requireUIContains(t, css, "scaleX(")
	requireUIRegex(t, css, `@media \(prefers-reduced-motion:\s*reduce\)\s*\{[^@]*\.dock-btn`)
	for _, v := range regexp.MustCompile(`var\((--[\w-]+)`).FindAllStringSubmatch(css, -1) {
		if !strings.Contains(readUIAsset(t, "app.css"), v[1]+":") {
			t.Errorf("mobile.css uses undeclared token %s", v[1])
		}
	}
}

func TestUIMobileDockModuleUsesActivateTab(t *testing.T) {
	src := executableJS(readUIAsset(t, "js/dock.js"))
	requireUIRegex(t, src, `import\s*\{[^}]*getShellApi[^}]*\}\s*from\s*'\./shell-hooks\.js'`)
	requireUIRegex(t, src, `getShellApi\(\)\.activateTab\(`)
	requireUIContains(t, src, "TAB_CHANGE_EVENT", "aria-current", "moreSheet", "shouldHideDock", "dockLast", "projectStorageKey")
	// every storage touch is guarded
	if n, g := strings.Count(src, "localStorage."), len(regexp.MustCompile(`try\s*\{`).FindAllString(src, -1)); g < 2 || n < 2 {
		t.Errorf("localStorage use (%d) must sit in try/catch (%d try blocks)", n, g)
	}
	if strings.Contains(src, ".innerHTML") || strings.Contains(src, ".style.") || strings.Contains(src, "style=") {
		t.Error("dock.js must build nodes without innerHTML or inline styles")
	}
	// Dock must not reimplement tab switching.
	if strings.Contains(src, "classList.add('active')") || strings.Contains(src, `aria-selected`) {
		t.Error("dock.js must delegate to activateTab, not touch tab state")
	}
}

func TestUIMobileDockIsAnOptionalModuleAndMoreSheetIsRegistered(t *testing.T) {
	requireUIContains(t, readUIAsset(t, "js/shell-hooks.js"), "'dock'")
	requireUIContains(t, readUIAsset(t, "js/core.js"), "'moreSheet'")
}

func TestUIMobileDockModelStaysDOMFree(t *testing.T) {
	if regexp.MustCompile(`(?m)^import `).MatchString(readUIAsset(t, "js/dock-model.js")) {
		t.Error("dock-model.js must be import-free so node can load it")
	}
}

func TestUIMobilePaletteSheetShapeOnPhones(t *testing.T) {
	css := readUIAsset(t, "mobile.css")
	requireUIRegex(t, css, `\.cmdk-input\{[^}]*min-height:48px`)
	requireUIRegex(t, css, `\.cmdk-row\{[^}]*min-height:48px`)
	requireUIRegex(t, css, `\.cmdk-shell\{[^}]*margin-top:0`)
}
