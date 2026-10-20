package control

import (
	"net/http"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

// The desktop skin (docs/desktop-design.md) restyles the embedded UI inside a
// desktop host only. These contracts keep a browser tab byte-for-byte on the web
// look: every desktop rule is scoped under html.desktop-app, the desktop chrome
// is built only when a host is detected, and the web DOM is never rewritten.

var desktopSkinSheets = []string{
	"css/desktop/tokens.css", "css/desktop/type.css", "css/desktop/states.css",
	"css/desktop/components.css", "css/desktop/shell.css",
}

func TestUIDesktopSkinIsLinkedAfterTheWebStylesheets(t *testing.T) {
	index := readUIAsset(t, "index.html")
	last := strings.Index(index, `<link rel="stylesheet" href="/mobile.css">`)
	if last < 0 {
		t.Fatal("index.html lost mobile.css")
	}
	at := last
	for _, name := range desktopSkinSheets {
		link := `<link rel="stylesheet" href="/` + name + `">`
		i := strings.Index(index, link)
		if i < 0 {
			t.Fatalf("index.html does not link %s", name)
		}
		if i < at {
			t.Errorf("%s is linked before a web stylesheet or out of order; the skin must load last", name)
		}
		at = i
	}
	// Served like every embedded asset: strong validator and gzip.
	for _, name := range desktopSkinSheets {
		rec := uiGet(t, "/"+name, map[string]string{"Accept-Encoding": "gzip"})
		if rec.Code != http.StatusOK || rec.Header().Get("ETag") == "" || rec.Header().Get("Content-Encoding") != "gzip" {
			t.Errorf("/%s: status %d ETag %q encoding %q", name, rec.Code, rec.Header().Get("ETag"), rec.Header().Get("Content-Encoding"))
		}
	}
}

func TestUIDesktopSkinRulesAreScopedToDesktopHosts(t *testing.T) {
	ruleRE := regexp.MustCompile(`([^{}]+)\{[^{}]*\}`)
	for _, name := range desktopSkinSheets {
		css := stripCSSComments(readUIAsset(t, name))
		rules := ruleRE.FindAllStringSubmatch(css, -1)
		if len(rules) == 0 {
			t.Fatalf("%s has no rules", name)
		}
		for _, r := range rules {
			for _, sel := range splitTopLevelCommas(r[1]) {
				sel = strings.TrimSpace(sel)
				if !strings.HasPrefix(sel, "html.desktop-app") && !strings.HasPrefix(sel, ":where(html.desktop-app)") {
					t.Errorf("%s: selector %q is not scoped under html.desktop-app; a browser tab would change", name, sel)
				}
			}
		}
		if strings.Contains(css, "@import") || strings.Contains(css, "url(http") {
			t.Errorf("%s loads an external resource", name)
		}
	}
}

func TestUIDesktopSkinFollowsTheDesignSystemRules(t *testing.T) {
	hex := regexp.MustCompile(`#[0-9a-fA-F]{3,8}\b`)
	for _, name := range desktopSkinSheets {
		css := stripCSSComments(readUIAsset(t, name))
		// Colours live in the token blocks only.
		if name != "css/desktop/tokens.css" {
			for _, m := range hex.FindAllString(css, -1) {
				t.Errorf("%s: literal colour %s outside desktop/tokens.css", name, m)
			}
		}
		// Only transform and opacity animate.
		for _, m := range regexp.MustCompile(`transition(?:-property)?\s*:\s*([^;}]+)`).FindAllStringSubmatch(css, -1) {
			for _, part := range strings.Split(m[1], ",") {
				if f := strings.Fields(part); len(f) > 0 && f[0] != "transform" && f[0] != "opacity" && f[0] != "none" {
					t.Errorf("%s transitions %q; only transform and opacity may animate", name, f[0])
				}
			}
		}
		if strings.Contains(css, "@keyframes") {
			t.Errorf("%s declares keyframes", name)
		}
		// A removed outline always comes with a visible replacement.
		for _, m := range regexp.MustCompile(`([^{}]+)\{([^{}]*)\}`).FindAllStringSubmatch(css, -1) {
			if regexp.MustCompile(`outline\s*:\s*(none|0)\b`).MatchString(m[2]) && !regexp.MustCompile(`(box-shadow|border(-color)?|background)\s*:`).MatchString(m[2]) {
				t.Errorf("%s removes the outline for %q without a replacement", name, strings.TrimSpace(m[1]))
			}
		}
	}
	// Weights 400/500/600 only.
	for _, name := range []string{"css/desktop/tokens.css", "css/desktop/states.css", "css/desktop/components.css", "css/desktop/shell.css"} {
		for _, m := range regexp.MustCompile(`font-weight\s*:\s*(\d+)|font\s*:\s*(\d{3})\s`).FindAllStringSubmatch(readUIAsset(t, name), -1) {
			w := m[1] + m[2]
			if w != "400" && w != "500" && w != "600" {
				t.Errorf("%s uses font-weight %s; the desktop skin allows 400, 500 and 600", name, w)
			}
		}
	}
	// The generated weight and case map is current with the web stylesheets.
	node := requireNode(t)
	cmd := exec.Command(node, "../../scripts/desktop-type-overrides.mjs", "--check")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("css/desktop/type.css is stale: %v\n%s", err, out)
	}
}

func TestUIDesktopSkinTokensRemapTheWebNames(t *testing.T) {
	css := stripCSSComments(readUIAsset(t, "css/desktop/tokens.css"))
	block := func(prefix string) map[string]string {
		i := strings.Index(css, prefix)
		if i < 0 {
			t.Fatalf("tokens.css has no %s block", prefix)
		}
		body := css[i+len(prefix):]
		body = body[:strings.Index(body, "}")]
		out := map[string]string{}
		for _, m := range regexp.MustCompile(`(--[\w-]+)\s*:\s*([^;]+)`).FindAllStringSubmatch(body, -1) {
			out[m[1]] = strings.TrimSpace(m[2])
		}
		return out
	}
	dark, light := block("html.desktop-app{"), block(`html.desktop-app[data-theme="light"]{`)
	for name, want := range map[string]string{
		"--surface-0": "#16171a", "--surface-1": "#1c1d21", "--surface-2": "#25262b", "--surface-inset": "#141518",
		"--text-primary": "#ececee", "--accent": "#3ec98e", "--warning": "#e2a336", "--danger": "#ef6262",
		"--fs-xs": "13px", "--text-ui": "13px", "--text-code": "12.5px", "--text-caption": "11px",
		"--control-h": "28px", "--statusbar-h": "24px", "--titlebar-h": "38px", "--rail-w": "184px",
	} {
		if dark[name] != want {
			t.Errorf("desktop dark %s = %q, want %q", name, dark[name], want)
		}
	}
	// Every existing name the web themes define is remapped, so all panels move.
	for _, name := range []string{"--bg", "--bg1", "--bg2", "--bg3", "--fg", "--fg2", "--fg3", "--line", "--line2", "--accent", "--accentSolid", "--onAccent", "--sel", "--accentDim", "--focus-ring", "--ui", "--mono", "--r-sm", "--r-md", "--r-lg", "--r-xl"} {
		if _, ok := dark[name]; !ok {
			t.Errorf("desktop tokens do not remap %s", name)
		}
	}
	// Every colour the dark block sets is set again for light, or the web light
	// block (higher specificity than the dark desktop block) would leak through.
	for name, v := range dark {
		if strings.HasPrefix(v, "#") || strings.HasPrefix(v, "rgba(") || strings.HasPrefix(v, "var(--") && strings.Contains(name, "bg") {
			if _, ok := light[name]; !ok {
				t.Errorf("desktop light block does not set %s", name)
			}
		}
	}
	// Density blocks out-rank :root[data-density=…].
	requireUIContains(t, css, `html.desktop-app[data-density]{`, `html.desktop-app[data-density="compact"]{`, `html.desktop-app[data-density="comfortable"]{`)
}

func TestUIDesktopSkinFocusStates(t *testing.T) {
	states := stripCSSComments(readUIAsset(t, "css/desktop/states.css"))
	// The keyboard ring is keyboard-only and never touches high contrast.
	requireUIContains(t, states,
		`html.desktop-app.dsk-kbd:not([data-theme="hc"]):not(#dsk-x) :focus-visible{outline-color:var(--focus-ring)}`,
		`html.desktop-app:not([data-theme="hc"]):not(#dsk-x) :focus-visible{outline:2px solid transparent;outline-offset:2px;box-shadow:none}`,
	)
	for _, m := range regexp.MustCompile(`([^{}]+)\{[^{}]*outline[^{}]*\}`).FindAllStringSubmatch(states, -1) {
		if strings.Contains(m[1], ":focus-visible") && !strings.Contains(m[1], `:not([data-theme="hc"])`) && !strings.Contains(m[1], "transparent") && !strings.Contains(m[1], "#main") {
			t.Errorf("focus rule %q does not exclude high contrast", strings.TrimSpace(m[1]))
		}
	}
	// Input focus: lift, accent border, soft halo.
	requireUIRegex(t, states, `textarea,select,\[contenteditable="true"\],\.ui-select-trigger\):focus-visible\{outline:none;background-color:var\(--surface-1\);border-color:color-mix\(in srgb,var\(--accent\) 55%,transparent\);box-shadow:0 0 0 3px`)
	// desktop.js owns the modality: navigation keys set it, pointer and commands clear it.
	js := readUIAsset(t, "js/desktop.js")
	requireUIContains(t, js, "if (isNavigationKey(e)) setKeyboardMode(true);", "window.addEventListener('pointerdown', () => setKeyboardMode(false), true);",
		"if (!parsed) return false;\n  setKeyboardMode(false);")
	// No yellow ring on desktop: the ring colour is the accent.
	requireUIContains(t, readUIAsset(t, "css/desktop/tokens.css"), "--focus-ring:color-mix(in srgb,var(--accent) 60%,transparent);")
}

func TestUIDesktopShellIsBuiltOnlyInADesktopHost(t *testing.T) {
	app := readUIAsset(t, "js/app.js")
	requireUIContains(t, app,
		"import { initDesktopShell } from './desktop-shell.js';",
		"if(isDesktopApp()){",
		// A native menu panel switch closes the palette first.
		"if(isCommandPaletteOpen())cmdkClose();activateTab(tab);return true;",
	)
	i := strings.Index(app, "if(isDesktopApp()){")
	if j := strings.Index(app, "initDesktopShell({"); j < i {
		t.Fatal("initDesktopShell must run only inside the isDesktopApp() guard")
	}
	shell := readUIAsset(t, "js/desktop-shell.js")
	requireUIContains(t, shell, "if (!isDesktop()) return false;")
	for _, m := range staticImportRE.FindAllStringSubmatch(shell, -1) {
		if m[1] != "desktop-model.js" && m[1] != "desktop-panel-kit.js" {
			t.Errorf("desktop-shell.js imports %s; it may import only desktop-model.js and the desktop kit", m[1])
		}
	}
	// The web markup keeps the controls the desktop chrome hides or moves.
	index := readUIAsset(t, "index.html")
	for _, id := range []string{"ctxScope", "paneActions", "connChip", "themeToggle", "icptStat", "mobileProjectChip", "verBadge", "projBadge"} {
		if !strings.Contains(index, `id="`+id+`"`) {
			t.Errorf("index.html lost #%s", id)
		}
	}
	if strings.Contains(index, "dskStatus") || strings.Contains(index, "dsk-") {
		t.Error("the desktop status bar must be built by desktop-shell.js, never shipped in the web markup")
	}
	// The controls that leave the title toolbar stay reachable from the palette.
	for _, want := range []string{"View: Split left and right", "View: Split top and bottom", "View: Close this document", "History: In-scope only filter", "Connection details"} {
		if !strings.Contains(app, want) {
			t.Errorf("app.js does not register the palette entry %q", want)
		}
	}
}

func TestUIDesktopShellUnderNode(t *testing.T) {
	node := requireNode(t)
	cmd := exec.Command(node, "--test", "_js-tests/desktop-shell.test.mjs")
	cmd.Dir = "ui"
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("node --test failed: %v\n%s", err, out)
	}
}

func TestUIDesktopStatus2xxUsesSuccessColor(t *testing.T) {
	// Status 2xx codes (rendered with statusColor() using var(--accent)) must be
	// visibly green (var(--success)) rather than muted/secondary text.
	checks := []struct {
		sheet string
		want  string
	}{
		{"css/desktop/components.css", "html.desktop-app .tr-st[style*=\"--accent\"]{color:var(--success)!important}"},
		{"css/desktop/panel-intruder.css", "html.desktop-app #panel-intruder .intr-st[style*=\"--accent\"]{color:var(--success)!important}"},
		{"css/desktop/panel-tools.css", "html.desktop-app :is(.rep-res,.intr-results) .statusline[style*=\"--accent\"]{color:var(--success)!important}"},
		{"css/desktop/panel-map.css", "html.desktop-app .map-sts span[style*=\"--accent\"],html.desktop-app .map-tbl-sts span[style*=\"--accent\"]{color:var(--success)!important}"},
	}
	for _, tc := range checks {
		css := readUIAsset(t, tc.sheet)
		if !strings.Contains(css, tc.want) {
			t.Errorf("%s missing expected 2xx success color rule %q", tc.sheet, tc.want)
		}
		if strings.Contains(css, `[style*="--accent"]{color:var(--text-secondary)`) {
			t.Errorf("%s still overrides --accent status to var(--text-secondary)", tc.sheet)
		}
	}
}
