package control

import (
	"io/fs"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// WP0 (UI overhaul foundations): tokens, high-contrast theme, density, utility
// layer, icon sprite, region markers and the new empty stylesheets. The UI has no
// build step, so these static assertions are the compiler for that scaffolding.

// foundationStylesheets are the stylesheets WP0 links from the index head. The
// first four are pre-existing; the rest are scaffolded empty and filled by the
// package that owns each of them.
var foundationStylesheets = []string{
	"app.css", "surfaces.css", "findings.css",
	"workbench.css", "primitives.css", "shell.css", "flow.css",
	"panel-proxy.css", "panel-tools.css", "panel-scan.css", "panel-misc.css",
	"settings.css", "report.css", "mobile.css",
}

// newFoundationStylesheets are the scaffolded files that later packages fill.
var newFoundationStylesheets = foundationStylesheets[3:]

var foundationRegions = []string{
	"shell", "proxy", "intercept", "repeater", "intruder", "scanner", "findings",
	"map", "notes", "activity", "settings", "modals-findings", "modals-tools",
	"mobile", "flowdrawer",
}

// foundationIcons are the sprite symbols added for the overhaul. close, chevron,
// copy, columns, panel and scope replace Unicode glyphs in static markup; the
// rest are reserved for the shared components that adopt them.
var foundationIcons = []string{
	"scope", "identity", "evidence", "paperclip", "check-circle", "ring", "alert-tri",
	"drawer", "link", "split-h", "split-v", "diff", "copy", "columns", "chevron", "panel",
	"close", "plus", "flag", "pin", "target",
}

// foundationReservedIcons may be defined without a use site until the owning
// shared component lands (TestUIUsesVectorIconsNotEmoji otherwise forbids this).
var foundationReservedIcons = map[string]bool{
	"identity": true, "evidence": true, "paperclip": true, "check-circle": true,
	"ring": true, "alert-tri": true, "drawer": true, "link": true,
	"split-h": true, "split-v": true, "diff": true,
}

func foundationReadAll(t *testing.T, names []string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, n := range names {
		out[n] = readUIAsset(t, n)
	}
	return out
}

// foundationTokens merges every :root-level custom property declaration found in
// app.css (dark base first) so a lookup sees the union a browser would.
func foundationTheme(t *testing.T, css, prefix string) map[string]string {
	t.Helper()
	vars := parseThemeBlock(t, css, prefix)
	if prefix != ":root{" {
		for k, v := range parseThemeBlock(t, css, ":root{") {
			if _, ok := vars[k]; !ok {
				vars[k] = v
			}
		}
	}
	return vars
}

func TestUIFoundationLayoutTokens(t *testing.T) {
	css := readUIAsset(t, "app.css")
	vars := parseThemeBlock(t, css, ":root{")
	want := map[string]string{
		"--ctxbar-h":         "40px",
		"--topbar-h":         "48px",
		"--drawer-w":         "420px",
		"--drawer-min":       "320px",
		"--drawer-max":       "640px",
		"--bottomnav-h":      "56px",
		"--sheet-peek":       "72px",
		"--sheet-half":       "50dvh",
		"--rail-w-collapsed": "56px",
		"--split-gutter":     "8px",
		"--safe-t":           "env(safe-area-inset-top,0px)",
		"--safe-b":           "env(safe-area-inset-bottom,0px)",
		"--safe-l":           "env(safe-area-inset-left,0px)",
		"--safe-r":           "env(safe-area-inset-right,0px)",
		"--sticky-top":       "calc(var(--topbar-h) + var(--ctxbar-h))",
		"--z-sticky":         "10",
		"--z-sash":           "15",
		"--z-dock":           "20",
		"--z-strip":          "25",
		"--z-drawer":         "30",
		"--z-popover":        "40",
		"--z-sheet":          "60",
		"--z-modal":          "70",
		"--z-cmdk":           "75",
		"--z-toast":          "80",
		"--motion-press":     "90ms",
		"--motion-pop":       "150ms",
	}
	for name, expected := range want {
		got := strings.ReplaceAll(vars[name], " ", "")
		if got != strings.ReplaceAll(expected, " ", "") {
			t.Errorf("token %s = %q, want %q", name, vars[name], expected)
		}
	}
	for _, name := range []string{"--motion-drawer", "--motion-sheet", "--focus-outline", "--drag-over",
		"--scrim", "--sheet-radius", "--drawer-bg", "--drawer-edge", "--sel-cursor"} {
		if _, ok := vars[name]; !ok {
			t.Errorf("token %s is not declared in :root", name)
		}
	}
	// The focus-ring colour token predates the overhaul and several existing rules
	// read it as a colour; the spec's composite value lives in --focus-outline.
	if !regexp.MustCompile(`^#[0-9a-fA-F]{6}$`).MatchString(vars["--focus-ring"]) {
		t.Errorf("--focus-ring must stay a colour, got %q", vars["--focus-ring"])
	}
	// The radius scale is capped at five steps; the sheet radius must not be a sixth.
	for name := range vars {
		if name == "--r-sheet" {
			t.Error("--r-sheet would be a sixth radius step; compose --sheet-radius from --r-xl")
		}
	}
	if !strings.Contains(css, "scroll-padding-top:var(--sticky-top)") {
		t.Error("app.css must apply --sticky-top as scroll-padding-top (WCAG 2.4.11)")
	}
}

func TestUIFoundationZIndexScaleIsOrdered(t *testing.T) {
	vars := parseThemeBlock(t, readUIAsset(t, "app.css"), ":root{")
	order := []string{"--z-sticky", "--z-sash", "--z-dock", "--z-strip", "--z-drawer", "--z-popover", "--z-sheet", "--z-modal", "--z-cmdk", "--z-toast"}
	prev := -1
	for _, name := range order {
		n := int(atof(t, vars[name]))
		if n <= prev {
			t.Errorf("%s = %d must be greater than the previous layer (%d)", name, n, prev)
		}
		prev = n
	}
}

func TestUIFoundationDensityTokens(t *testing.T) {
	css := readUIAsset(t, "app.css")
	base := parseThemeBlock(t, css, ":root{")
	for name, want := range map[string]string{"--row-h": "30px", "--toolbar-h": "44px", "--hit-min": "32px", "--cell-pad-x": "6px"} {
		if base[name] != want {
			t.Errorf("default density %s = %q, want %q", name, base[name], want)
		}
	}
	for density, want := range map[string]map[string]string{
		"compact":     {"--row-h": "24px", "--toolbar-h": "36px", "--hit-min": "24px", "--cell-pad-x": "4px"},
		"comfortable": {"--row-h": "40px", "--toolbar-h": "48px", "--hit-min": "40px", "--cell-pad-x": "8px"},
	} {
		block := parseThemeBlock(t, css, `:root[data-density="`+density+`"]{`)
		for name, v := range want {
			if block[name] != v {
				t.Errorf("density %s: %s = %q, want %q", density, name, block[name], v)
			}
		}
	}
	// The default tier is selectable explicitly so the pre-paint script can stamp it.
	if !strings.Contains(css, `:root[data-density="default"]{`) {
		t.Error(`app.css must declare :root[data-density="default"]`)
	}
	coarse := mediaBlock(t, css, "@media (pointer:coarse)")
	for _, want := range []string{":root{--row-h:44px}", ":root[data-density]{--row-h:44px", "--toolbar-h:48px", "--hit-min:44px", "--cell-pad-x:8px"} {
		if !strings.Contains(coarse, want) {
			t.Errorf("pointer:coarse block must force the touch density tier; missing %q", want)
		}
	}
}

// TestUIFoundationNewTokensAreThemedInAllThemes checks every colour family
// exists in the dark base, and that the families whose value must differ between
// light and dark are overridden in the light and high-contrast blocks.
func TestUIFoundationNewTokensAreThemedInAllThemes(t *testing.T) {
	css := readUIAsset(t, "app.css")
	dark := parseThemeBlock(t, css, ":root{")
	light := parseThemeBlock(t, css, `:root[data-theme="light"]{`)
	hc := parseThemeBlock(t, css, `:root[data-theme="hc"]{`)
	families := []string{
		"--rd-pass", "--rd-gap", "--rd-block", "--rd-na",
		"--rd-pass-dim", "--rd-gap-dim", "--rd-block-dim", "--rd-na-dim",
		"--scope-on", "--scope-off",
		"--idn-1", "--idn-2", "--idn-3", "--idn-4", "--idn-5", "--idn-6", "--idn-fg",
		"--diff-add-bg", "--diff-add-fg", "--diff-del-bg", "--diff-del-fg",
		"--diff-word-add", "--diff-word-del", "--diff-gutter",
		"--find-bg", "--find-active-bg", "--find-fg",
		"--kbd-bg", "--kbd-border", "--kbd-fg",
	}
	for _, name := range families {
		if _, ok := dark[name]; !ok {
			t.Errorf("%s is not declared in the dark :root block", name)
		}
	}
	mustOverrideLight := []string{
		"--rd-pass", "--rd-gap", "--rd-block", "--rd-na",
		"--idn-1", "--idn-2", "--idn-3", "--idn-4", "--idn-5", "--idn-6", "--idn-fg",
		"--diff-add-bg", "--diff-add-fg", "--diff-del-bg", "--diff-del-fg",
		"--diff-word-add", "--diff-word-del",
		"--find-bg", "--find-active-bg", "--find-fg", "--kbd-bg", "--kbd-fg",
	}
	for _, name := range mustOverrideLight {
		if _, ok := light[name]; !ok {
			t.Errorf("light theme does not override %s", name)
		}
	}
	for _, name := range []string{"--bg", "--bg1", "--bg2", "--bg3", "--fg", "--fg2", "--fg3", "--line", "--line2", "--focus-ring",
		"--accent", "--accentSolid", "--onAccent", "--red", "--redSolid", "--onDanger", "--amber", "--blue", "--violet", "--cyan",
		"--rd-pass", "--rd-gap", "--rd-block", "--rd-na"} {
		if _, ok := hc[name]; !ok {
			t.Errorf("high-contrast theme does not override %s", name)
		}
	}
	for name, want := range map[string]string{"--bg": "#000", "--bg1": "#000", "--bg2": "#0a0a0a", "--bg3": "#1a1a1a", "--fg": "#fff", "--fg2": "#fff", "--fg3": "#e5e5e5", "--line": "#fff", "--line2": "#fff", "--focus-ring": "#ffe14d"} {
		if !strings.EqualFold(hc[name], want) {
			t.Errorf("high-contrast %s = %q, want %q", name, hc[name], want)
		}
	}
}

// TestUIFoundationContrastAcrossThemes validates the new token pairs in light,
// dark and high-contrast: 4.5:1 for text, 3:1 for non-text, and 7:1 for every
// high-contrast text pair.
func TestUIFoundationContrastAcrossThemes(t *testing.T) {
	css := readUIAsset(t, "app.css")
	surfaces := []string{"--bg", "--bg1", "--bg2", "--bg3"}
	for _, theme := range []struct {
		name, prefix string
		text         float64
	}{
		{"dark", ":root{", 4.5},
		{"light", `:root[data-theme="light"]{`, 4.5},
		{"hc", `:root[data-theme="hc"]{`, 7},
	} {
		t.Run(theme.name, func(t *testing.T) {
			vars := foundationTheme(t, css, theme.prefix)
			page := resolve(t, vars, vars["--bg"], rgb{255, 255, 255}, 0)
			surf := func(name string) rgb { return resolve(t, vars, vars[name], page, 0) }
			check := func(label string, fg, bg rgb, min float64) {
				t.Helper()
				if got := contrastRatio(fg, bg); got < min {
					t.Errorf("%s theme: %s = %.2f:1, want >= %.1f:1", theme.name, label, got, min)
				}
			}
			// Status colours are text on every surface and on their own dim fill.
			for _, tok := range []string{"--rd-pass", "--rd-gap", "--rd-block", "--rd-na", "--scope-on", "--scope-off"} {
				for _, s := range surfaces {
					bg := surf(s)
					check(tok+" on "+s, resolve(t, vars, vars[tok], bg, 0), bg, theme.text)
				}
			}
			for _, pair := range [][2]string{{"--rd-pass", "--rd-pass-dim"}, {"--rd-gap", "--rd-gap-dim"}, {"--rd-block", "--rd-block-dim"}, {"--rd-na", "--rd-na-dim"}} {
				for _, s := range surfaces {
					under := surf(s)
					fill := resolve(t, vars, vars[pair[1]], under, 0)
					check(pair[0]+" on "+pair[1]+" over "+s, resolve(t, vars, vars[pair[0]], fill, 0), fill, theme.text)
				}
			}
			// Identity initials sit on a solid hue; the hue itself is a non-text
			// shape that must separate from the surface it is drawn on.
			for i := 1; i <= 6; i++ {
				tok := "--idn-" + string(rune('0'+i))
				fill := resolve(t, vars, vars[tok], page, 0)
				check("--idn-fg on "+tok, resolve(t, vars, vars["--idn-fg"], fill, 0), fill, theme.text)
				for _, s := range surfaces {
					check(tok+" shape on "+s, fill, surf(s), 3)
				}
			}
			// Diff: glyph and text colour on row fill, and on the word-level mark.
			for _, pair := range [][3]string{
				{"--diff-add-fg", "--diff-add-bg", "row"}, {"--diff-del-fg", "--diff-del-bg", "row"},
				{"--diff-add-fg", "--diff-word-add", "word"}, {"--diff-del-fg", "--diff-word-del", "word"},
			} {
				bg := resolve(t, vars, vars[pair[1]], page, 0)
				check(pair[0]+" on "+pair[1], resolve(t, vars, vars[pair[0]], bg, 0), bg, theme.text)
			}
			for _, pair := range [][2]string{{"--find-fg", "--find-bg"}, {"--find-fg", "--find-active-bg"}, {"--kbd-fg", "--kbd-bg"}} {
				bg := resolve(t, vars, vars[pair[1]], page, 0)
				check(pair[0]+" on "+pair[1], resolve(t, vars, vars[pair[0]], bg, 0), bg, theme.text)
			}
			check("--diff-gutter on --bg", resolve(t, vars, vars["--diff-gutter"], page, 0), page, theme.text)
			// Non-text indicators: keyboard cursor bar, focus ring, kbd keycap edge.
			for _, s := range surfaces {
				bg := surf(s)
				check("--sel-cursor on "+s, resolve(t, vars, vars["--sel-cursor"], bg, 0), bg, 3)
				check("--focus-ring on "+s, resolve(t, vars, vars["--focus-ring"], bg, 0), bg, 3)
			}
			if theme.name == "hc" {
				for _, tok := range []string{"--fg", "--fg2", "--fg3", "--accent", "--amber", "--red", "--blue", "--violet", "--cyan"} {
					for _, s := range surfaces {
						bg := surf(s)
						check(tok+" on "+s, resolve(t, vars, vars[tok], bg, 0), bg, 7)
					}
				}
				for _, pair := range [][2]string{{"--onAccent", "--accentSolid"}, {"--onDanger", "--redSolid"}} {
					fill := resolve(t, vars, vars[pair[1]], page, 0)
					check(pair[0]+" on "+pair[1], resolve(t, vars, vars[pair[0]], fill, 0), fill, 7)
				}
			}
		})
	}
}

func TestUIFoundationHighContrastAndForcedColors(t *testing.T) {
	css := readUIAsset(t, "app.css")
	hcStart := strings.Index(css, `:root[data-theme="hc"]{`)
	if hcStart < 0 {
		t.Fatal(`app.css is missing :root[data-theme="hc"]`)
	}
	for _, want := range []string{
		`:root[data-theme="hc"] :where(button,input,select,textarea,.btn,.panel-card,.modal-shell){border-width:2px`,
		`:root[data-theme="hc"] :where(button,a[href],input,select,textarea,summary,[tabindex]):focus-visible{outline:3px solid var(--focus-ring)`,
	} {
		if !strings.Contains(css, want) {
			t.Errorf("high-contrast theme rules missing %q", want)
		}
	}
	forced := mediaBlock(t, css, "@media (forced-colors:active)")
	for _, want := range []string{"CanvasText", "Highlight", "outline:", ".trow.sel", ".tab.active"} {
		if !strings.Contains(forced, want) {
			t.Errorf("forced-colors block must map %q", want)
		}
	}
}

func TestUIFoundationUtilityLayer(t *testing.T) {
	css := readUIAsset(t, "app.css") + readUIAsset(t, "surfaces.css")
	for _, sel := range []string{".u-flex{", ".u-grow{", ".u-gap-1{", ".u-gap-2{", ".u-gap-3{", ".u-gap-4{", ".u-mono{", ".u-truncate{", ".u-sr{"} {
		if !strings.Contains(css, sel) {
			t.Errorf("utility %s is not defined", strings.TrimSuffix(sel, "{"))
		}
	}
}

func TestUIFoundationReducedMotionCoversNewMotionTokens(t *testing.T) {
	css := readUIAsset(t, "app.css")
	at := strings.Index(css, "@media (prefers-reduced-motion:reduce)")
	if at < 0 {
		t.Fatal("reduced-motion block missing")
	}
	reduced := mediaBlock(t, css, "@media (prefers-reduced-motion:reduce)")
	for _, tok := range []string{"--motion-press", "--motion-pop", "--motion-drawer", "--motion-sheet"} {
		if !regexp.MustCompile(regexp.QuoteMeta(tok) + `\s*:\s*0(ms)?\b`).MatchString(reduced) {
			t.Errorf("reduced-motion block must zero %s", tok)
		}
	}
}

// TestUIFoundationScaffoldedStylesheetsAnimateOnlyCompositorProperties covers the
// files later packages fill: they may only transition or animate transform and
// opacity, and every keyframes/transition they add must be reachable by the
// global reduced-motion block (which targets every element).
func TestUIFoundationScaffoldedStylesheetsAnimateOnlyCompositorProperties(t *testing.T) {
	css := foundationReadAll(t, newFoundationStylesheets)
	for name, body := range css {
		body = regexp.MustCompile(`(?s)/\*.*?\*/`).ReplaceAllString(body, "")
		for _, m := range regexp.MustCompile(`transition(?:-property)?\s*:\s*([^;}]+)`).FindAllStringSubmatch(body, -1) {
			for _, part := range strings.Split(m[1], ",") {
				prop := strings.Fields(strings.TrimSpace(part))
				if len(prop) == 0 {
					continue
				}
				if p := prop[0]; p != "transform" && p != "opacity" && p != "none" {
					t.Errorf("%s transitions %q; only transform and opacity may animate", name, p)
				}
			}
		}
		for _, m := range regexp.MustCompile(`@keyframes\s+[\w-]+\s*\{((?:[^{}]|\{[^{}]*\})*)\}`).FindAllStringSubmatch(body, -1) {
			for _, d := range regexp.MustCompile(`([a-z-]+)\s*:`).FindAllStringSubmatch(m[1], -1) {
				if d[1] != "transform" && d[1] != "opacity" {
					t.Errorf("%s keyframes animate %q; only transform and opacity may animate", name, d[1])
				}
			}
		}
	}
}

func TestUIFoundationStylesheetsAreLinkedAndEmbedded(t *testing.T) {
	index := readUIAsset(t, "index.html")
	var linked []string
	for _, m := range regexp.MustCompile(`<link rel="stylesheet" href="/([a-z-]+\.css)">`).FindAllStringSubmatch(index, -1) {
		linked = append(linked, m[1])
	}
	if strings.Join(linked, ",") != strings.Join(foundationStylesheets, ",") {
		t.Errorf("index.html stylesheet links = %v, want %v (mobile.css last so it wins the cascade)", linked, foundationStylesheets)
	}
	for _, name := range linked {
		if _, err := fs.Stat(uiFS, "ui/"+name); err != nil {
			t.Errorf("linked stylesheet %s is not embedded: %v", name, err)
		}
	}
	if !strings.Contains(readUIAsset(t, "workbench.css"), "720") {
		t.Error("workbench.css must document the 1100/900/720/480 breakpoint model at its top")
	}
	for _, bp := range []string{"1100", "900", "720", "480"} {
		if !strings.Contains(readUIAsset(t, "workbench.css"), bp) {
			t.Errorf("workbench.css does not document the %spx breakpoint", bp)
		}
	}
}

// TestUIFoundationEveryVarIsDeclared scans every linked stylesheet for var()
// reads of an undeclared custom property. A property counts as declared if any
// stylesheet declares it, or it is one of the documented runtime-set values.
func TestUIFoundationEveryVarIsDeclared(t *testing.T) {
	css := foundationReadAll(t, foundationStylesheets)
	declared := map[string]bool{"--flow-cols": true, "--split-size": true}
	for _, body := range css {
		for _, m := range regexp.MustCompile(`(--[a-zA-Z0-9-]+)\s*:`).FindAllStringSubmatch(body, -1) {
			declared[m[1]] = true
		}
	}
	var missing []string
	for name, body := range css {
		for _, m := range regexp.MustCompile(`var\((--[a-zA-Z0-9-]+)`).FindAllStringSubmatch(body, -1) {
			if !declared[m[1]] {
				missing = append(missing, name+": "+m[1])
			}
		}
	}
	sort.Strings(missing)
	for _, m := range missing {
		t.Errorf("undeclared custom property read: %s", m)
	}
}

func TestUIFoundationRegionMarkersAppearExactlyOnce(t *testing.T) {
	index := readUIAsset(t, "index.html")
	for _, name := range foundationRegions {
		start, end := "<!-- region:"+name+" -->", "<!-- /region:"+name+" -->"
		if n := strings.Count(index, start); n != 1 {
			t.Errorf("%s appears %d times, want exactly 1", start, n)
		}
		if n := strings.Count(index, end); n != 1 {
			t.Errorf("%s appears %d times, want exactly 1", end, n)
		}
		if strings.Index(index, start) >= strings.Index(index, end) {
			t.Errorf("region %s ends before it starts", name)
		}
	}
	// Panel regions wrap their panel; modal regions wrap the modal run.
	for _, name := range []string{"proxy", "intercept", "repeater", "intruder", "scanner", "findings", "map", "notes", "activity", "settings"} {
		start := strings.Index(index, "<!-- region:"+name+" -->")
		end := strings.Index(index, "<!-- /region:"+name+" -->")
		if !strings.Contains(index[start:end], `data-panel="`+strings.TrimSuffix(name, "")+`"`) {
			t.Errorf("region %s does not contain its panel", name)
		}
	}
	shell := index[strings.Index(index, "<!-- region:shell -->"):strings.Index(index, "<!-- /region:shell -->")]
	for _, want := range []string{`id="bar"`, `id="crumb"`, `id="cmdkBtn"`, `id="humanInputBar"`} {
		if !strings.Contains(shell, want) {
			t.Errorf("shell region must contain %s", want)
		}
	}
	if strings.Contains(shell, `id="tabs"`) {
		t.Error("shell region must stop before the nav rail")
	}
	// modals-findings nests inside modals-tools; both leave the overlays in place.
	mt := index[strings.Index(index, "<!-- region:modals-tools -->"):strings.Index(index, "<!-- /region:modals-tools -->")]
	mf := index[strings.Index(index, "<!-- region:modals-findings -->"):strings.Index(index, "<!-- /region:modals-findings -->")]
	for _, id := range []string{"findGuideModal", "findExportModal", "findCreateModal", "findPickModal", "findFlowPickModal"} {
		if !strings.Contains(mf, `id="`+id+`"`) {
			t.Errorf("modals-findings must contain %s", id)
		}
		if !strings.Contains(mt, `id="`+id+`"`) {
			t.Errorf("modals-tools must enclose %s", id)
		}
	}
	for _, id := range []string{"flowModal", "shortcutsModal", "checksModal", "compareModal", "decModal"} {
		if !strings.Contains(mt, `id="`+id+`"`) {
			t.Errorf("modals-tools must contain %s", id)
		}
		if strings.Contains(mf, `id="`+id+`"`) {
			t.Errorf("modals-findings must not contain %s", id)
		}
	}
}

func TestUIFoundationIndexHasNoStyleAttributes(t *testing.T) {
	index := readUIAsset(t, "index.html")
	if m := regexp.MustCompile(`\sstyle=`).FindAllString(index, -1); len(m) > 0 {
		t.Fatalf("index.html has %d style= attributes; initial hidden state must come from data-init-hidden or classes", len(m))
	}
	// JavaScript toggles these elements through el.style.display, so the initial
	// hidden state is stamped through the CSSOM (never a style attribute) before
	// the application module loads; that keeps '' / 'none' / 'block' toggling intact.
	hidden := regexp.MustCompile(`data-init-hidden`).FindAllString(index, -1)
	if len(hidden) < 17 {
		t.Errorf("expected the 17 formerly inline-hidden elements to carry data-init-hidden, found %d", len(hidden))
	}
	script := regexp.MustCompile(`querySelectorAll\('\[data-init-hidden\]'\)[^;]*style\.display\s*=\s*'none'`)
	if !script.MatchString(index) {
		t.Error("index.html must stamp data-init-hidden elements with style.display='none' through the CSSOM")
	}
	if strings.Index(index, "data-init-hidden]'") > strings.Index(index, `<script type="module" src="/js/app.js">`) {
		t.Error("the data-init-hidden stamp must run before the app module loads")
	}
	for _, id := range []string{"repHistory", "repCodecBadge", "intrListMode", "intrHistory", "mapFit", "mapWarn", "deviceProxyManualField",
		"androidAdbSection", "androidLanHint", "iosLanHint", "iosSshLanHint", "sessionExpiry", "pmNote", "authzMax",
		"authzScopePanel", "authzCookieHint", "setupBack"} {
		if !regexp.MustCompile(`id="` + id + `"[^>]*data-init-hidden`).MatchString(index) {
			t.Errorf("#%s must carry data-init-hidden", id)
		}
	}
}

func TestUIFoundationNoUnicodeGlyphIconsInStaticMarkup(t *testing.T) {
	for _, name := range []string{"index.html", "login.html"} {
		body := readUIAsset(t, name)
		for _, glyph := range []string{"⧉", "◎", "▦", "＋", "◧", "▾", "✕"} {
			if strings.Contains(body, glyph) {
				t.Errorf("%s still uses the Unicode glyph %s as an icon; use the sprite", name, glyph)
			}
		}
	}
	index := readUIAsset(t, "index.html")
	for _, id := range foundationIcons {
		if !strings.Contains(index, `<symbol id="i-`+id+`"`) {
			t.Errorf("sprite is missing icon i-%s", id)
		}
	}
}

func TestUIFoundationPrePaintAppliesThemeAndDensityTogether(t *testing.T) {
	index := readUIAsset(t, "index.html")
	head := index[:strings.Index(index, "</head>")]
	for _, want := range []string{"localStorage.getItem('theme')", "localStorage.getItem('density')", "'data-density'", "'data-theme'", "'hc'"} {
		if !strings.Contains(head, want) {
			t.Errorf("pre-paint head script missing %s", want)
		}
	}
	if !regexp.MustCompile(`(?s)<script>\s*(?://[^\n]*\n)+\(function\(\)\{try\{.*?data-density.*?\}catch\(e\)\{\}\}\)\(\);`).MatchString(head) {
		t.Error("pre-paint script must stay a try/catch guarded IIFE (storage can throw)")
	}
}

func TestUIFoundationLoginTokensAlignOnly(t *testing.T) {
	login := readUIAsset(t, "login.html")
	// Locked-state privacy and the six-digit PIN flow are out of scope for WP0;
	// the sign-in page must keep reading the shared tokens and nothing more.
	if !strings.Contains(login, `href="/app.css"`) {
		t.Error("login.html must keep reading the shared token stylesheet")
	}
	if !strings.Contains(login, "'hc'") {
		t.Error("login.html pre-paint script must honour the high-contrast theme like the workspace")
	}
	if strings.Contains(login, "data-init-hidden") {
		t.Error("login.html must not gain the workspace init-hidden contract")
	}
}
