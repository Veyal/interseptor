package control

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The UI ships as embedded static assets with no build step, so the design
// system has no compiler behind it. These tests are that compiler: they parse
// app.css and enforce the contracts a stylesheet can otherwise drift away from
// silently — theme contrast, token discipline, and dead-selector rot.

type rgb struct{ r, g, b float64 }

var (
	reHex      = regexp.MustCompile(`^#([0-9a-fA-F]{3}|[0-9a-fA-F]{6})$`)
	reRGBA     = regexp.MustCompile(`^rgba?\(\s*([0-9.]+)\s*,\s*([0-9.]+)\s*,\s*([0-9.]+)\s*(?:,\s*([0-9.]+)\s*)?\)$`)
	reVarRef   = regexp.MustCompile(`^var\(\s*(--[a-zA-Z0-9-]+)\s*\)$`)
	reDeclLine = regexp.MustCompile(`(--[a-zA-Z0-9-]+)\s*:\s*([^;}]+)`)
)

// parseThemeBlock pulls the custom-property declarations out of a single CSS
// rule block whose selector is given by prefix.
func parseThemeBlock(t *testing.T, css, prefix string) map[string]string {
	t.Helper()
	start := strings.Index(css, prefix)
	if start < 0 {
		t.Fatalf("theme block %q not found in app.css", prefix)
	}
	open := strings.Index(css[start:], "{")
	if open < 0 {
		t.Fatalf("theme block %q has no opening brace", prefix)
	}
	open += start
	depth, end := 0, -1
	for i := open; i < len(css); i++ {
		switch css[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				end = i
			}
		}
		if end >= 0 {
			break
		}
	}
	if end < 0 {
		t.Fatalf("theme block %q is unterminated", prefix)
	}
	out := map[string]string{}
	for _, m := range reDeclLine.FindAllStringSubmatch(css[open:end], -1) {
		out[m[1]] = strings.TrimSpace(m[2])
	}
	return out
}

// resolve turns a token value into a solid colour, compositing any alpha over
// the supplied backdrop. Nested var() references are followed.
func resolve(t *testing.T, vars map[string]string, value string, backdrop rgb, depth int) rgb {
	t.Helper()
	if depth > 8 {
		t.Fatalf("var() reference cycle resolving %q", value)
	}
	value = strings.TrimSpace(value)
	if m := reVarRef.FindStringSubmatch(value); m != nil {
		next, ok := vars[m[1]]
		if !ok {
			t.Fatalf("token %s referenced but never defined", m[1])
		}
		return resolve(t, vars, next, backdrop, depth+1)
	}
	if m := reHex.FindStringSubmatch(value); m != nil {
		h := m[1]
		if len(h) == 3 {
			h = string([]byte{h[0], h[0], h[1], h[1], h[2], h[2]})
		}
		n, err := strconv.ParseUint(h, 16, 32)
		if err != nil {
			t.Fatalf("bad hex %q: %v", value, err)
		}
		return rgb{float64(n >> 16 & 0xff), float64(n >> 8 & 0xff), float64(n & 0xff)}
	}
	if m := reRGBA.FindStringSubmatch(value); m != nil {
		c := rgb{atof(t, m[1]), atof(t, m[2]), atof(t, m[3])}
		alpha := 1.0
		if m[4] != "" {
			alpha = atof(t, m[4])
		}
		return rgb{
			c.r*alpha + backdrop.r*(1-alpha),
			c.g*alpha + backdrop.g*(1-alpha),
			c.b*alpha + backdrop.b*(1-alpha),
		}
	}
	t.Fatalf("cannot resolve colour value %q", value)
	return rgb{}
}

func atof(t *testing.T, s string) float64 {
	t.Helper()
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		t.Fatalf("bad number %q: %v", s, err)
	}
	return f
}

func channelLuminance(c float64) float64 {
	c /= 255
	if c <= 0.04045 {
		return c / 12.92
	}
	return math.Pow((c+0.055)/1.055, 2.4)
}

func relativeLuminance(c rgb) float64 {
	return 0.2126*channelLuminance(c.r) + 0.7152*channelLuminance(c.g) + 0.0722*channelLuminance(c.b)
}

func contrastRatio(a, b rgb) float64 {
	la, lb := relativeLuminance(a), relativeLuminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// wcagAA is the WCAG 2.2 minimum for body text and UI labels.
const wcagAA = 4.5

// TestUIThemeContrastMeetsWCAGAA proves every foreground token stays legible on
// every surface token it is actually painted on, in BOTH themes. The light
// theme was previously a mechanical inversion of the dark one and failed this
// on secondary text and every accent colour.
func TestUIThemeContrastMeetsWCAGAA(t *testing.T) {
	css := readUIAsset(t, "app.css")

	// Foreground tokens that carry text, and the surfaces they sit on.
	foregrounds := []string{"--fg", "--fg2", "--fg3", "--accent", "--amber", "--red", "--blue", "--violet", "--cyan"}
	surfaces := []string{"--bg", "--bg1", "--bg2", "--bg3"}

	for _, theme := range []struct {
		name   string
		prefix string
	}{
		{"dark", ":root{"},
		{"light", `:root[data-theme="light"]{`},
	} {
		t.Run(theme.name, func(t *testing.T) {
			vars := parseThemeBlock(t, css, theme.prefix)
			if theme.name == "light" {
				// The light block only overrides; inherit the rest from :root.
				base := parseThemeBlock(t, css, ":root{")
				for k, v := range base {
					if _, ok := vars[k]; !ok {
						vars[k] = v
					}
				}
			}
			page := resolve(t, vars, vars["--bg"], rgb{255, 255, 255}, 0)
			for _, sName := range surfaces {
				surface := resolve(t, vars, vars[sName], page, 0)
				for _, fName := range foregrounds {
					fg := resolve(t, vars, vars[fName], surface, 0)
					if got := contrastRatio(fg, surface); got < wcagAA {
						t.Errorf("%s theme: %s on %s = %.2f:1, want >= %.2f:1", theme.name, fName, sName, got, wcagAA)
					}
				}
			}
			// Filled controls: the label colour must be legible on the fill.
			for _, pair := range [][2]string{
				{"--onAccent", "--accentSolid"},
				{"--onDanger", "--redSolid"},
			} {
				fill := resolve(t, vars, vars[pair[1]], page, 0)
				label := resolve(t, vars, vars[pair[0]], fill, 0)
				if got := contrastRatio(label, fill); got < wcagAA {
					t.Errorf("%s theme: %s on %s = %.2f:1, want >= %.2f:1", theme.name, pair[0], pair[1], got, wcagAA)
				}
			}
		})
	}
}

// TestUIStylesheetHasNoUntokenizedColours keeps theme-blind literals out of the
// component layer. Colours declared outside the two :root blocks cannot respond
// to the theme toggle, which is how the error/warning toasts became unreadable
// in light mode.
func TestUIStylesheetHasNoUntokenizedColours(t *testing.T) {
	css := readUIAsset(t, "app.css")
	// Everything after the light-theme block is component CSS.
	lightEnd := strings.Index(css, `:root[data-theme="light"]{`)
	if lightEnd < 0 {
		t.Fatal("light theme block not found")
	}
	closing := strings.Index(css[lightEnd:], "}")
	components := css[lightEnd+closing:]

	// The image lightbox is deliberately theme-independent: it is a blackout
	// viewer for evidence screenshots and must look identical in both themes.
	allowed := regexp.MustCompile(`(?m)^\s*\.img-lightbox`)

	hex := regexp.MustCompile(`#[0-9a-fA-F]{3,8}\b`)
	for _, line := range strings.Split(components, "\n") {
		if allowed.MatchString(line) || strings.HasPrefix(strings.TrimSpace(line), "/*") {
			continue
		}
		if m := hex.FindString(line); m != "" {
			t.Errorf("untokenized colour %s outside :root — cannot follow the theme: %s", m, strings.TrimSpace(line))
		}
	}
}

// TestUIStylesheetIsSelfContained keeps the control UI off the network. This is
// a localhost security tool: a webfont fetch leaks that the operator is running
// it, and breaks the UI entirely on air-gapped engagements.
func TestUIStylesheetIsSelfContained(t *testing.T) {
	for _, name := range []string{"app.css", "index.html", "login.html"} {
		asset := readUIAsset(t, name)
		for _, host := range []string{"fonts.googleapis.com", "fonts.gstatic.com", "cdn.jsdelivr.net", "unpkg.com", "cdnjs.cloudflare.com"} {
			if strings.Contains(asset, host) {
				t.Errorf("%s loads an external asset from %s; the UI must be fully self-contained", name, host)
			}
		}
		if strings.Contains(asset, "@import url(") {
			t.Errorf("%s uses a render-blocking @import", name)
		}
	}
}

// TestUIStylesheetHasNoDeadSelectors guards against styling classes that no
// longer exist in the markup — the silent failure mode where a rule looks
// applied but never matches.
func TestUIStylesheetHasNoDeadSelectors(t *testing.T) {
	css := readUIAsset(t, "app.css")
	markup := readUIAsset(t, "index.html")
	for _, name := range []string{"js/core.js", "js/tools.js", "js/proxy.js", "js/findings.js", "js/scanner.js", "js/app.js", "js/settings.js", "js/map.js", "js/intercept.js"} {
		markup += readUIAsset(t, name)
	}
	// Class names the stylesheet styles as a state modifier on a component.
	for _, dead := range []struct{ selector, mustExist string }{
		{".rep-tab.active", "rep-tab"},
		{".find-list", "find-list"},
	} {
		if strings.Contains(css, dead.selector) && !strings.Contains(markup, dead.mustExist+`"`) && !strings.Contains(markup, dead.mustExist+` `) {
			t.Errorf("stylesheet targets %s but no markup or JS ever emits it", dead.selector)
		}
	}
	if strings.Contains(css, ".rep-tab.active") {
		t.Error("stylesheet styles .rep-tab.active, but core.js emits .rep-tab.on — the rule never applies")
	}
	if strings.Contains(css, ".find-list") {
		t.Error("stylesheet styles .find-list, which appears nowhere in markup or JS")
	}
	// Every var() the component layer reads must be declared. --flow-cols is the
	// documented exception: proxy.js writes it onto #flowHead/#rows at runtime to
	// drive the resizable history grid, and the rule carries its own fallback.
	runtimeSet := map[string]bool{"--flow-cols": true}
	declared := parseThemeBlock(t, css, ":root{")
	for _, m := range regexp.MustCompile(`var\((--[a-zA-Z0-9-]+)([,)])`).FindAllStringSubmatch(css, -1) {
		if _, ok := declared[m[1]]; !ok && !runtimeSet[m[1]] {
			t.Errorf("var(%s) is read but never declared in :root", m[1])
		}
	}
}

// TestUIUsesVectorIconsNotEmoji keeps pictographic emoji out of the interface.
// Emoji are font-dependent, render differently on macOS/Linux/Windows, cannot
// take a colour token, and cannot follow the theme — and this tool's screens get
// screenshotted into client reports. Monochrome typographic symbols (arrows,
// checks, the ✕ close glyph, disclosure triangles) are deliberately still
// allowed: they are plain text glyphs that inherit colour correctly.
func TestUIUsesVectorIconsNotEmoji(t *testing.T) {
	assets := []string{"index.html", "login.html", "js/core.js", "js/app.js", "js/proxy.js",
		"js/tools.js", "js/findings.js", "js/scanner.js", "js/map.js", "js/settings.js",
		"js/activity.js", "js/authz.js", "js/codecs.js",
		"js/humaninput.js", "js/tlsdiag.js", "js/intercept.js", "js/notes.js", "js/tags.js",
		"js/apipanel.js", "js/setup.js", "js/flowmodal.js"}

	// These ranges were originally too narrow and let ⚡ (U+26A1, which has
	// Emoji_Presentation=Yes and therefore renders in full colour), ➕ (U+2795),
	// ⏱, ⏸, ♻, ⌨, ☀ and ☾ through. Cover the pictographic blocks wholesale and
	// carve out the monochrome typographic symbols that are deliberately kept.
	keep := map[rune]bool{
		0x2713: true, 0x2714: true, 0x2715: true, 0x2717: true, // ✓ ✔ ✕ ✗
		0x2605: true, 0x2606: true, // ★ ☆ — used as a "suggested" marker
		0x2630: true, // ☰ menu bars (text presentation)
	}
	pictographic := func(r rune) bool {
		if keep[r] {
			return false
		}
		switch {
		case r >= 0x1F300 && r <= 0x1FAFF: // Misc Symbols & Pictographs, Emoticons, Transport, Supplemental
			return true
		case r >= 0x1F000 && r <= 0x1F0FF: // playing cards / mahjong
			return true
		case r >= 0x2600 && r <= 0x27BF: // Misc Symbols + Dingbats (⚡ ➕ ♻ ☀ ☾ ✨ ⚠ …)
			return true
		case r >= 0x2300 && r <= 0x23FF: // Misc Technical (⏱ ⏸ ⌨ ⏎ …)
			// ⌘ (U+2318) and ⌥/⇧ are keyboard legends shown in <kbd>, not icons.
			return r != 0x2318 && r != 0x2325 && r != 0x21E7 && r != 0x23CE
		case r >= 0x1F1E6 && r <= 0x1F1FF: // regional indicators (flags)
			return true
		}
		return false
	}
	for _, name := range assets {
		for _, r := range readUIAsset(t, name) {
			if pictographic(r) {
				t.Errorf("%s contains emoji %q used as an icon; use the sprite via <use href=\"#i-…\"> or icon() from core.js", name, string(r))
				break
			}
		}
	}

	index := readUIAsset(t, "index.html")
	if !strings.Contains(index, `<svg id="iconSprite"`) {
		t.Fatal("index.html is missing the inline icon sprite")
	}
	// Every icon referenced anywhere must exist in the sprite.
	defined := map[string]bool{}
	for _, m := range regexp.MustCompile(`<symbol id="i-([a-z-]+)"`).FindAllStringSubmatch(index, -1) {
		defined[m[1]] = true
	}
	if len(defined) == 0 {
		t.Fatal("icon sprite defines no symbols")
	}
	used := map[string]bool{}
	for _, name := range assets {
		asset := readUIAsset(t, name)
		for _, m := range regexp.MustCompile(`href="#i-([a-z-]+)"`).FindAllStringSubmatch(asset, -1) {
			used[m[1]] = true
		}
		// icon('name') / icon("name") calls in JS.
		for _, m := range regexp.MustCompile(`\bicon\(['"]([a-z-]+)['"]`).FindAllStringSubmatch(asset, -1) {
			used[m[1]] = true
		}
		// ctx-menu items declaring icon:'name'
		for _, m := range regexp.MustCompile(`icon:\s*['"]([a-z-]+)['"]`).FindAllStringSubmatch(asset, -1) {
			used[m[1]] = true
		}
	}
	for name := range used {
		if !defined[name] {
			t.Errorf("icon %q is referenced but not defined in the sprite", name)
		}
	}
	for name := range defined {
		if !used[name] {
			t.Errorf("icon %q is defined in the sprite but never used — drop it", name)
		}
	}
	// Icon markup must never be built inside an escaping call or an HTML
	// attribute value. esc()/escAttr() turn it into visible literal "<svg …>"
	// text, and an attribute cannot hold markup at all. This is exactly how the
	// HTTPS lock in the Proxy host column broke: the bulk emoji replacement
	// dropped the sprite reference inside an existing esc(...).
	for _, name := range assets {
		asset := readUIAsset(t, name)
		for _, pattern := range []struct{ what, re string }{
			{"an escaping call", `esc(?:Attr)?\([^()]*<svg`},
			{"an HTML attribute", `(?:title|placeholder|aria-label|alt)="[^"]*<svg`},
		} {
			if m := regexp.MustCompile(pattern.re).FindString(asset); m != "" {
				t.Errorf("%s: icon markup inside %s renders as literal text: %s", name, pattern.what, m)
			}
		}
	}

	// Any module calling icon() must import it.
	for _, name := range assets {
		if !strings.HasPrefix(name, "js/") || name == "js/core.js" {
			continue
		}
		asset := readUIAsset(t, name)
		if !regexp.MustCompile(`[^a-zA-Z-]icon\(`).MatchString(executableJS(asset)) {
			continue
		}
		if !regexp.MustCompile(`(?m)^import \{[^}]*\bicon\b`).MatchString(asset) {
			t.Errorf("%s calls icon() but does not import it from core.js", name)
		}
	}
}

// TestUIStylesheetHasNoDuplicateSelectors enforces one source of truth per
// component. The stylesheet previously ended with a "Shared visual system pass"
// that re-declared ~40 primitives with different padding, radii and heights than
// their original definitions, so editing the primitive silently did nothing.
// Declarations inside @media blocks are legitimate overrides and are excluded.
func TestUIStylesheetHasNoDuplicateSelectors(t *testing.T) {
	css := readUIAsset(t, "app.css")
	css = regexp.MustCompile(`(?s)/\*.*?\*/`).ReplaceAllString(css, "")

	// Drop @media blocks — a breakpoint redeclaring a selector is the point.
	var top strings.Builder
	for i := 0; i < len(css); {
		at := strings.Index(css[i:], "@media")
		if at < 0 {
			top.WriteString(css[i:])
			break
		}
		top.WriteString(css[i : i+at])
		j := i + at
		for j < len(css) && css[j] != '{' {
			j++
		}
		depth := 0
		for ; j < len(css); j++ {
			if css[j] == '{' {
				depth++
			} else if css[j] == '}' {
				if depth--; depth == 0 {
					j++
					break
				}
			}
		}
		i = j
	}

	seen := map[string]int{}
	keyframeStop := regexp.MustCompile(`^[\d%,.\s]+$`)
	for _, m := range regexp.MustCompile(`([^{}]+)\{[^{}]*\}`).FindAllStringSubmatch(top.String(), -1) {
		sel := strings.Join(strings.Fields(m[1]), " ")
		if sel == "" || strings.HasPrefix(sel, ":root") || strings.HasPrefix(sel, "@") || keyframeStop.MatchString(sel) {
			continue
		}
		seen[sel]++
	}
	for sel, n := range seen {
		if n > 1 {
			t.Errorf("selector %q is declared %d times outside @media — components must have one source of truth", sel, n)
		}
	}
}

func TestUIStylesheetUsesTechnicalConsoleVisualSystem(t *testing.T) {
	css := readUIAsset(t, "app.css")
	requireUIContains(t, css,
		"--surface-frame:",
		"--signal-grid:",
		"background-image:linear-gradient(90deg,var(--signal-grid)",
		"box-shadow:inset 2px 0 0 var(--accent)",
		".nav-rail-group-label{font-family:var(--mono)",
		".pane-head .lbl,.rep-sub,.icpt-queue-head{font-family:var(--mono)",
	)
}

// TestUIFontRolesAreCorrect keeps the proportional/monospace split honest: chrome
// and prose proportional, machine data monospace. The body default used to be
// monospace, which rendered findings prose — the surface that ends up in client
// reports — in JetBrains Mono.
func TestUIFontRolesAreCorrect(t *testing.T) {
	css := readUIAsset(t, "app.css")
	if !regexp.MustCompile(`body\{[^}]*font-family:var\(--ui\)`).MatchString(css) {
		t.Error("body must default to the proportional --ui face, not --mono")
	}
	if !regexp.MustCompile(`(?m)^pre,code,kbd,samp,textarea\{font-family:var\(--mono\)\}`).MatchString(css) {
		t.Error("raw-data elements (pre/code/kbd/samp/textarea) must be explicitly monospace")
	}
	// The reset that makes form controls inherit must not come after the mono
	// rule, or textareas silently fall back to the proportional face.
	reset := strings.Index(css, "button,input,select,textarea{font-family:inherit")
	mono := strings.Index(css, "pre,code,kbd,samp,textarea{font-family:var(--mono)}")
	if reset < 0 || mono < 0 || reset > mono {
		t.Error("the form-control inherit reset must precede the monospace rule")
	}
}

// TestUIBreakpointsHaveNoDeadZone keeps the minimum width aligned with the
// narrowest breakpoint the stylesheet actually implements. body had
// min-width:1024px while media queries existed at 900px and 720px, so every
// window from 721px to 1023px scrolled the whole app horizontally while those
// queries were firing.
func TestUIBreakpointsHaveNoDeadZone(t *testing.T) {
	css := readUIAsset(t, "app.css")
	m := regexp.MustCompile(`body\{[^}]*min-width:(\d+)px`).FindStringSubmatch(css)
	if m == nil {
		if !regexp.MustCompile(`body\{[^}]*min-width:0`).MatchString(css) {
			t.Fatal("body has no explicit responsive min-width contract")
		}
		return
	}
	minWidth, _ := strconv.Atoi(m[1])
	narrowest := 1 << 30
	for _, q := range regexp.MustCompile(`@media \(max-width:(\d+)px\)`).FindAllStringSubmatch(css, -1) {
		if w, _ := strconv.Atoi(q[1]); w < narrowest {
			narrowest = w
		}
	}
	if narrowest == 1<<30 {
		return // no narrow breakpoints declared; nothing to reconcile
	}
	if minWidth > narrowest {
		t.Errorf("body min-width is %dpx but the stylesheet has a %dpx breakpoint: widths %d-%dpx scroll horizontally while that query is active",
			minWidth, narrowest, narrowest+1, minWidth-1)
	}
}

func TestUIResponsiveShellConstrainsNarrowViewport(t *testing.T) {
	css := readUIAsset(t, "app.css")
	if !strings.Contains(css, "@media (max-width:720px){") {
		t.Fatal("narrow viewport media rule not found")
	}
	for _, contract := range []string{"#bar{min-width:0", "#appRow{flex-direction:column}", "#tabs{width:100%", "#main{width:100%;min-width:0", ".panel{min-width:0"} {
		if !strings.Contains(css, contract) {
			t.Errorf("narrow viewport rule missing %q", contract)
		}
	}
}

// TestUIHoverStatesDoNotShiftLayout keeps dense list and toolbar rows stable
// under the cursor. Transform-based hover makes rows jitter as the pointer
// crosses them, which reads as a rendering bug in a data-dense tool.
func TestUIHoverStatesDoNotShiftLayout(t *testing.T) {
	css := readUIAsset(t, "app.css")
	rule := regexp.MustCompile(`(?m)^([^{\n]*:hover[^{\n]*)\{([^}]*)\}`)
	for _, m := range rule.FindAllStringSubmatch(css, -1) {
		body := m[2]
		if strings.Contains(body, "translateY") || strings.Contains(body, "translateX") {
			t.Errorf("hover rule shifts layout: %s{%s}", strings.TrimSpace(m[1]), strings.TrimSpace(body))
		}
	}
	if strings.Contains(css, "transition:all") {
		t.Error("transition:all animates unintended properties; name the properties explicitly")
	}
}

// TestUITypeScaleIsBounded stops the type ramp from sprawling back into a
// continuum of near-identical sizes, and enforces a legibility floor.
func TestUITypeScaleIsBounded(t *testing.T) {
	css := readUIAsset(t, "app.css")
	vars := parseThemeBlock(t, css, ":root{")
	sizes := map[string]float64{}
	for name, value := range vars {
		if !strings.HasPrefix(name, "--fs-") {
			continue
		}
		px := strings.TrimSuffix(strings.TrimSpace(value), "px")
		f, err := strconv.ParseFloat(px, 64)
		if err != nil {
			t.Errorf("type token %s is not a px value: %q", name, value)
			continue
		}
		sizes[name] = f
		if f < 11 {
			t.Errorf("type token %s = %gpx is below the 11px legibility floor", name, f)
		}
	}
	if len(sizes) > 7 {
		t.Errorf("type scale has %d steps (%s); collapse it to at most 7", len(sizes), fmt.Sprint(sizes))
	}
	// The scale is only real if the markup honours it too. Inline styles in
	// index.html and JS template strings bypass the stylesheet entirely, and
	// that is where 9px and 10px text survived the first cleanup.
	for _, name := range []string{"app.css", "index.html", "login.html", "js/core.js", "js/app.js",
		"js/proxy.js", "js/tools.js", "js/findings.js", "js/scanner.js", "js/map.js", "js/settings.js",
		"js/activity.js", "js/authz.js", "js/codecs.js", "js/apipanel.js",
		"js/humaninput.js", "js/tlsdiag.js", "js/intercept.js", "js/notes.js", "js/tags.js",
		"js/setup.js", "js/flowmodal.js"} {
		for _, m := range regexp.MustCompile(`font-size:\s*([0-9.]+)px`).FindAllStringSubmatch(readUIAsset(t, name), -1) {
			f, _ := strconv.ParseFloat(m[1], 64)
			if f > 0 && f < 11 {
				t.Errorf("%s: font-size:%spx is below the 11px legibility floor", name, m[1])
			} else if f > 0 {
				t.Errorf("%s: hardcoded font-size:%spx bypasses the --fs-* scale", name, m[1])
			}
		}
	}
	radii := 0
	for name := range vars {
		if strings.HasPrefix(name, "--r-") {
			radii++
		}
	}
	if radii > 5 {
		t.Errorf("radius scale has %d steps; collapse it to at most 5", radii)
	}
}
