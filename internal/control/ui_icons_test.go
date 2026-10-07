package control

import (
	"math"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// The icon family (docs/ui-icons.md) is one inline sprite in index.html. These
// tests keep it a closed system: every reference resolves, nothing is dead,
// every symbol obeys the grid/stroke/colour contract, and glyphs or emoji never
// stand in for icons.

var (
	iconSymbolRe = regexp.MustCompile(`(?s)<symbol id="i-([a-z0-9-]+)"([^>]*)>(.*?)</symbol>`)
	iconHrefRe   = regexp.MustCompile(`(?:xlink:)?href="#i-([a-z0-9-]+)"`)
	iconQuotedRe = regexp.MustCompile(`'([a-z][a-z0-9-]*)'`)
	iconTokenRe  = regexp.MustCompile(`'i-([a-z0-9-]+)'`)
	// Call shapes whose first argument (or ternary branches) is a sprite name.
	iconCallRes = []*regexp.Regexp{
		regexp.MustCompile(`\bicon:\s*([^,}\n]*)`),
		regexp.MustCompile(`\biconNode\(([^)\n]*)`),
		regexp.MustCompile(`\bsetUse\(([^\n]*)`),
		regexp.MustCompile(`\b(?:mkButton|iconButton)\(([^)\n]*)`),
	}
	iconMapRe      = regexp.MustCompile(`\b(?:SEV_ICON|CHIP_ICON|DEFAULT_ICON|ICONS)\s*=\s*\{([^}]*)\}`)
	iconMapValueRe = regexp.MustCompile(`:\s*'([a-z][a-z0-9-]*)'`)
	iconBranchRe   = regexp.MustCompile(`(?:^|[?:(,])\s*'([a-z][a-z0-9-]*)'`)
	// Names that appear quoted inside those calls but are not sprite ids.
	iconNotNames = map[string]bool{"doc": true, "document": true}
)

func uiJSAssets(t *testing.T) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join("ui", "js", "*.js"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no UI scripts found: %v", err)
	}
	out := make([]string, 0, len(files))
	for _, f := range files {
		out = append(out, "js/"+filepath.Base(f))
	}
	return out
}

// iconCallArgs returns the full argument text of every icon(...) call, parens
// balanced, so chained ternaries between the parentheses are seen whole.
func iconCallArgs(src string) []string {
	var out []string
	call := regexp.MustCompile(`(?:^|[^\w.])icon\(`)
	for _, loc := range call.FindAllStringIndex(src, -1) {
		depth, i := 1, loc[1]
		for ; i < len(src) && depth > 0; i++ {
			switch src[i] {
			case '(':
				depth++
			case ')':
				depth--
			}
		}
		if depth == 0 {
			out = append(out, src[loc[1]:i-1])
		}
	}
	return out
}

func uiIconDefinitions(t *testing.T) map[string]string {
	t.Helper()
	defs := map[string]string{}
	for _, m := range iconSymbolRe.FindAllStringSubmatch(readUIAsset(t, "index.html"), -1) {
		if _, dup := defs[m[1]]; dup {
			t.Errorf("symbol i-%s is defined twice", m[1])
		}
		defs[m[1]] = m[0]
	}
	if len(defs) == 0 {
		t.Fatal("icon sprite defines no symbols")
	}
	return defs
}

// uiIconReferences returns name -> first referencing asset.
func uiIconReferences(t *testing.T) map[string]string {
	t.Helper()
	refs := map[string]string{}
	add := func(name, asset string) {
		name = strings.TrimPrefix(name, "i-")
		if name != "" && !iconNotNames[name] {
			if _, ok := refs[name]; !ok {
				refs[name] = asset
			}
		}
	}
	assets := append([]string{"index.html", "login.html"}, uiJSAssets(t)...)
	for _, name := range assets {
		src := readUIAsset(t, name)
		if strings.HasPrefix(name, "js/") {
			src = executableJS(src)
		}
		for _, m := range iconHrefRe.FindAllStringSubmatch(src, -1) {
			add(m[1], name)
		}
		if !strings.HasPrefix(name, "js/") {
			continue
		}
		for _, m := range iconTokenRe.FindAllStringSubmatch(src, -1) {
			add(m[1], name)
		}
		for _, arg := range iconCallArgs(src) {
			for _, q := range iconBranchRe.FindAllStringSubmatch(arg, -1) {
				add(q[1], name)
			}
		}
		for _, re := range iconCallRes {
			for _, call := range re.FindAllStringSubmatch(src, -1) {
				for _, q := range iconQuotedRe.FindAllStringSubmatch(call[1], -1) {
					add(q[1], name)
				}
			}
		}
		for _, m := range iconMapRe.FindAllStringSubmatch(src, -1) {
			for _, q := range iconMapValueRe.FindAllStringSubmatch(m[1], -1) {
				add(q[1], name)
			}
		}
	}
	return refs
}

func TestUIIconReferencesResolveAndNoDeadSymbols(t *testing.T) {
	defs := uiIconDefinitions(t)
	refs := uiIconReferences(t)
	var problems []string
	for name, asset := range refs {
		if _, ok := defs[name]; !ok {
			problems = append(problems, "icon \""+name+"\" referenced in "+asset+" is not defined in the sprite")
		}
	}
	for name := range defs {
		if _, ok := refs[name]; !ok && !iconReserved[name] {
			problems = append(problems, "symbol i-"+name+" is defined but never used - drop it (or add it to iconReserved with a reason)")
		}
	}
	sort.Strings(problems)
	for _, p := range problems {
		t.Error(p)
	}
}

// iconReserved is deliberately empty: a symbol with no call site is dead weight
// inside the boot-critical index.html.
var iconReserved = map[string]bool{}

func TestUIIconSymbolContract(t *testing.T) {
	defs := uiIconDefinitions(t)
	shape := regexp.MustCompile(`<(path|circle|rect|ellipse|line|polyline)\b`)
	anyTag := regexp.MustCompile(`<([a-zA-Z]+)\b`)
	colour := regexp.MustCompile(`(?i)#[0-9a-f]{3,8}\b|rgb|hsl|url\(|style=|class=|opacity|fill="(?:[^n"][^"]*)"|stroke(?:-width)?=`)
	geometry := map[string]string{}
	for name, def := range defs {
		m := iconSymbolRe.FindStringSubmatch(def)
		attrs, body := m[2], m[3]
		if strings.TrimSpace(attrs) != `viewBox="0 0 24 24"` {
			t.Errorf("i-%s: symbol attributes must be exactly viewBox=\"0 0 24 24\", got %q", name, attrs)
		}
		if len(body) > 700 {
			t.Errorf("i-%s: %d bytes of shape data; keep icons simple (<= 700)", name, len(body))
		}
		if c := colour.FindString(body); c != "" {
			t.Errorf("i-%s: hard-coded colour/style/stroke override %q; icons are stroke-only currentColor (stroke width comes from .icon)", name, c)
		}
		for _, tag := range anyTag.FindAllStringSubmatch(body, -1) {
			if !shape.MatchString("<" + tag[1] + " ") {
				t.Errorf("i-%s: element <%s> is not allowed in an icon (path/circle/rect/ellipse/line/polyline only)", name, tag[1])
			}
		}
		if n := len(shape.FindAllString(body, -1)); n == 0 || n > 6 {
			t.Errorf("i-%s: %d sub-shapes; an icon has 1-6 (target is <= 3)", name, n)
		}
		if other, dup := geometry[body]; dup {
			t.Errorf("i-%s duplicates the geometry of i-%s; one meaning, one icon (no aliases)", name, other)
		}
		geometry[body] = name
	}
}

// retiredIcons are ids whose meaning changed or that were duplicates of another
// symbol. They must not come back as aliases.
var retiredIcons = []string{
	"toolbox", "sliders", "traffic", "gate", "warning", "alert-tri", "robot", "thought",
	"gear", "bolt", "recycle", "nodes", "layers", "pencil", "paperclip", "check-circle",
	"ring", "pause", "notebook", "dock-more", "panel", "drawer", "split-h", "split-v",
	"flag", "pin", "scan", "repeat", "dock-sprite",
}

func TestUIRetiredIconIDsAreGone(t *testing.T) {
	defs := uiIconDefinitions(t)
	refs := uiIconReferences(t)
	for _, id := range retiredIcons {
		if _, ok := defs[id]; ok {
			t.Errorf("retired icon i-%s is still defined", id)
		}
		if asset, ok := refs[id]; ok {
			t.Errorf("retired icon %q is still referenced by %s", id, asset)
		}
	}
	index := readUIAsset(t, "index.html")
	if n := strings.Count(index, "<symbol "); n != len(defs) {
		t.Errorf("%d <symbol> elements but %d inside the single iconSprite", n, len(defs))
	}
	if strings.Contains(index, `class="dock-sprite"`) {
		t.Error("the dock must share the main sprite, not carry its own")
	}
}

// TestUIIconsFixPreviouslyWrongMetaphors pins the audit's headline findings:
// Mobile devices used a toolbox and API & MCP used a settings slider.
func TestUIIconsFixPreviouslyWrongMetaphors(t *testing.T) {
	index := readUIAsset(t, "index.html")
	header := func(title string) string {
		re := regexp.MustCompile(`<header class="settings-section-heading"><svg class="icon"[^>]*><use href="#(i-[a-z0-9-]+)"/></svg><h2>` + regexp.QuoteMeta(title) + `</h2>`)
		m := re.FindStringSubmatch(index)
		if m == nil {
			t.Fatalf("settings section heading %q not found", title)
		}
		return m[1]
	}
	want := map[string]string{
		"Proxy &amp; network":          "i-proxy",
		"TLS &amp; certificates":       "i-tls",
		"Mobile devices":               "i-device-mobile",
		"Scope":                        "i-scope",
		"Scanner &amp; OOB":            "i-scanner",
		"API &amp; MCP":                "i-api-mcp",
		"Session &amp; authentication": "i-key",
		"Appearance":                   "i-appearance",
		"Project &amp; data":           "i-folder",
	}
	seen := map[string]string{}
	for title, id := range want {
		got := header(title)
		if got != id {
			t.Errorf("settings heading %q uses %s, want %s", title, got, id)
		}
		if prev, dup := seen[got]; dup {
			t.Errorf("settings headings %q and %q share %s", prev, title, got)
		}
		seen[got] = title
	}
	// Each panel's navigation tab has its own icon, and each is unique.
	tabs := map[string]string{
		"proxy": "i-proxy", "intercept": "i-intercept", "repeater": "i-repeater", "intruder": "i-intruder",
		"scanner": "i-scanner", "map": "i-map", "findings": "i-finding", "notes": "i-notes",
		"activity": "i-activity", "settings": "i-settings",
	}
	used := map[string]string{}
	for tab, id := range tabs {
		re := regexp.MustCompile(`id="tab-` + tab + `"[^>]*>\s*<svg class="icon nav-icon"[^>]*><use href="#(i-[a-z0-9-]+)"/>`)
		m := re.FindStringSubmatch(index)
		if m == nil {
			t.Errorf("tab %s has no nav icon", tab)
			continue
		}
		if m[1] != id {
			t.Errorf("tab %s uses %s, want %s", tab, m[1], id)
		}
		if prev, dup := used[m[1]]; dup {
			t.Errorf("tabs %s and %s share %s", prev, tab, m[1])
		}
		used[m[1]] = tab
	}
	// The mobile dock reuses the nav family (single deduplicated sprite).
	for dock, id := range map[string]string{"capture": "i-proxy", "test": "i-repeater", "recon": "i-scanner", "report": "i-report", "more": "i-more"} {
		if !regexp.MustCompile(`data-dock="` + dock + `"[^>]*><svg class="icon"[^>]*><use href="#` + id + `"/>`).MatchString(index) {
			t.Errorf("dock button %s should use %s", dock, id)
		}
	}
	// Context bar chips.
	for el, id := range map[string]string{"ctxProject": "i-folder", "ctxScope": "i-scope", "ctxTarget": "i-target", "ctxEvidence": "i-evidence", "ctxMore": "i-info"} {
		if !regexp.MustCompile(`id="` + el + `"[^>]*><svg class="icon"[^>]*><use href="#` + id + `"/>`).MatchString(index) {
			t.Errorf("context chip %s should use %s", el, id)
		}
	}
}

// TestUIIconMarkupIsAccessible: decorative icons are hidden from assistive
// technology and icon-only buttons carry an accessible name (never title alone).
func TestUIIconMarkupIsAccessible(t *testing.T) {
	index := readUIAsset(t, "index.html")
	svgRe := regexp.MustCompile(`<svg class="icon[^"]*"([^>]*)>`)
	for _, m := range svgRe.FindAllStringSubmatch(index, -1) {
		attrs := m[1]
		decorative := strings.Contains(attrs, `aria-hidden="true"`) && strings.Contains(attrs, `focusable="false"`)
		labelled := strings.Contains(attrs, `role="img"`) && strings.Contains(attrs, "aria-label=")
		hiddenByParent := strings.Contains(attrs, "focusable=\"false\"") // #logo wrapper is aria-hidden itself
		if !decorative && !labelled && !hiddenByParent {
			t.Errorf("icon svg without aria-hidden+focusable=false or role=img+aria-label: %q", attrs)
		}
	}
	iconOnly := regexp.MustCompile(`(?s)<button\b([^>]*)>\s*<svg class="icon[^>]*><use [^>]*/></svg>\s*</button>`)
	for _, m := range iconOnly.FindAllStringSubmatch(index, -1) {
		if !strings.Contains(m[1], "aria-label=") {
			t.Errorf("icon-only button has no aria-label (title is not an accessible name): <button%s>", m[1])
		}
	}
	login := readUIAsset(t, "login.html")
	if iconHrefRe.MatchString(login) {
		t.Error("login.html has no sprite; it must not reference #i-* symbols")
	}
}

// TestUIIconsNeverUseGlyphsOrEmoji: icons are sprite symbols. Pictographic and
// dingbat glyphs, arrows used as buttons and CSS content: glyphs are out. The
// allowlist is intentionally tiny and each entry is a plain-text affordance.
func TestUIIconsNeverUseGlyphsOrEmoji(t *testing.T) {
	banned := "✓✔✕✗✘⤓⇉⤢↻⌄⌃▲▼▴▾☰⊞⚠⚡⏸⏱♻⌨☀☾○●⋯↗↘↙⇄⇅◂◀▶⧉◎▦＋◧🔒🔓🛡⚙🔑📎🧰📱🌐"
	// Allowed: ▸ marks "run/next" in text button labels (Send ▸, Start ▸) and is
	// not a standalone icon; ★ is the "suggested" marker inside native <option>
	// text, which cannot contain SVG.
	allowed := "▸★"
	files := append([]string{"index.html", "login.html"}, uiJSAssets(t)...)
	for _, css := range []string{"app.css", "findings.css", "flow.css", "mobile.css", "panel-misc.css", "panel-proxy.css", "panel-scan.css", "panel-tools.css", "primitives.css", "report.css", "settings.css", "shell.css", "surfaces.css", "workbench.css"} {
		files = append(files, css)
	}
	for _, name := range files {
		src := readUIAsset(t, name)
		for _, r := range src {
			if strings.ContainsRune(allowed, r) {
				continue
			}
			if strings.ContainsRune(banned, r) || (r >= 0x1F300 && r <= 0x1FAFF) {
				t.Errorf("%s uses the glyph %q as an icon; use <use href=\"#i-...\"> / icon() from core.js", name, string(r))
				break
			}
		}
		if strings.HasSuffix(name, ".css") {
			for _, m := range regexp.MustCompile(`content:\s*['"]([^'"]*)['"]`).FindAllStringSubmatch(src, -1) {
				for _, r := range m[1] {
					if r > 0x7e {
						t.Errorf("%s draws %q with CSS content:; icons must be sprite markup", name, m[1])
						break
					}
				}
			}
		}
	}
}

// ---- Construction spec (docs/ui-icons.md, "Gate & Lane") ----------------------
//
// Every symbol is built from straight runs only: path data uses M/L/H/V/Z, every
// segment is horizontal, vertical or exactly 45 degrees, every vertex sits in
// the 2..22 safe box. There are no arcs, curves, circles or rects; "round"
// things are chamfered octagons and dots are square pads.

var (
	iconPathRe  = regexp.MustCompile(`<path d="([^"]*)"/>`)
	iconDataRe  = regexp.MustCompile(`[MmLlHhVvZz]|-?\d*\.?\d+`)
	iconBadCmd  = regexp.MustCompile(`[A-Za-z]`)
	iconBadCmds = regexp.MustCompile(`[^MmLlHhVvZz0-9.\s,-]`)
)

type iconPoint struct {
	x, y float64
	move bool // starts a new subpath (no segment leads to it)
}

// iconWalk parses one path's data into its vertices (M/L/H/V/Z, abs and rel).
func iconWalk(t *testing.T, id, d string) []iconPoint {
	t.Helper()
	if bad := iconBadCmds.FindString(d); bad != "" {
		t.Errorf("i-%s: path command/character %q is outside the Gate & Lane set M L H V Z (no arcs or curves)", id, bad)
		return nil
	}
	toks := iconDataRe.FindAllString(d, -1)
	var pts []iconPoint
	var x, y, sx, sy float64
	cmd := byte(0)
	i := 0
	num := func() float64 {
		if i >= len(toks) {
			t.Errorf("i-%s: truncated path data %q", id, d)
			return 0
		}
		v, err := strconv.ParseFloat(toks[i], 64)
		if err != nil {
			t.Errorf("i-%s: bad number %q in %q", id, toks[i], d)
		}
		i++
		return v
	}
	for i < len(toks) {
		if iconBadCmd.MatchString(toks[i]) {
			cmd = toks[i][0]
			i++
		}
		rel := cmd >= 'a'
		moved := false
		switch strings.ToUpper(string(cmd)) {
		case "M", "L":
			nx, ny := num(), num()
			if rel {
				nx, ny = nx+x, ny+y
			}
			x, y = nx, ny
			if strings.ToUpper(string(cmd)) == "M" {
				sx, sy = x, y
				moved = true
				if rel {
					cmd = 'l'
				} else {
					cmd = 'L'
				}
			}
		case "H":
			nx := num()
			if rel {
				nx += x
			}
			x = nx
		case "V":
			ny := num()
			if rel {
				ny += y
			}
			y = ny
		case "Z":
			x, y = sx, sy
		default:
			t.Errorf("i-%s: number with no command in %q", id, d)
			return pts
		}
		pts = append(pts, iconPoint{x, y, moved})
	}
	return pts
}

func TestUIIconsFollowGateAndLaneConstruction(t *testing.T) {
	const eps = 0.011
	for name, def := range uiIconDefinitions(t) {
		body := iconSymbolRe.FindStringSubmatch(def)[3]
		if strings.Count(body, "<") != len(iconPathRe.FindAllString(body, -1)) {
			t.Errorf("i-%s: only <path> elements are allowed (octagons and boxes are chamfered paths, not circle/rect)", name)
			continue
		}
		for _, m := range iconPathRe.FindAllStringSubmatch(body, -1) {
			pts := iconWalk(t, name, m[1])
			for i, p := range pts {
				if p.x < 2-eps || p.x > 22+eps || p.y < 2-eps || p.y > 22+eps {
					t.Errorf("i-%s: vertex (%g,%g) leaves the 2..22 safe box in %q", name, p.x, p.y, m[1])
				}
				if i == 0 || p.move {
					continue
				}
				dx, dy := math.Abs(p.x-pts[i-1].x), math.Abs(p.y-pts[i-1].y)
				if dx > eps && dy > eps && math.Abs(dx-dy) > eps {
					t.Errorf("i-%s: segment (%g,%g) is not horizontal, vertical or 45 degrees in %q", name, dx, dy, m[1])
				}
			}
		}
	}
}

// The signature motifs live where the spec says they must.
func TestUIIconsCarrySignatureMotifs(t *testing.T) {
	defs := uiIconDefinitions(t)
	gate := regexp.MustCompile(`d="M\d+(?:\.\d+)? \d+(?:\.\d+)?v\d+(?:\.\d+)?"`)
	pad := regexp.MustCompile(`h\.01`)
	// A hold bar (a lone full-height vertical stroke) stops the lane.
	for _, id := range []string{"intercept", "intruder"} {
		if !gate.MatchString(defs[id]) {
			t.Errorf("i-%s is an in-path icon and must carry a hold bar (single vertical gate stroke)", id)
		}
	}
	// The proxy mark is two opposed lanes with the tap pad between them.
	if !strings.Contains(defs["proxy"], "M3 8h16l-4-4M21 16H5l4 4") || !pad.MatchString(defs["proxy"]) {
		t.Error("i-proxy must be the two opposed lanes with the tap pad")
	}
	// Severity is also shape-coded (never colour alone): four distinct outlines.
	shapes := map[string]string{}
	for _, id := range []string{"sev-critical", "alert", "alert-circle", "sev-low"} {
		first := iconPathRe.FindStringSubmatch(defs[id])[1]
		if other, dup := shapes[first]; dup {
			t.Errorf("severity icons i-%s and i-%s share an outline", id, other)
		}
		shapes[first] = id
	}
	// The same lanes are the logo, favicon and login mark.
	for _, f := range []string{"index.html", "login.html"} {
		if !strings.Contains(readUIAsset(t, f), "M3 8h16l-4-4M21 16H5l4 4") && !strings.Contains(readUIAsset(t, f), "M5%208.5h12.5l-3-3M19%2015.5H6.5l3%203") {
			t.Errorf("%s does not carry the two-lane mark", f)
		}
	}
}

// Square caps and mitre joins are what make pads square and corners crisp.
func TestUIIconStrokeStyleIsSquareMitre(t *testing.T) {
	css := readUIAsset(t, "app.css")
	m := regexp.MustCompile(`(?m)^\.icon\{[^}]*\}`).FindString(css)
	if !strings.Contains(m, "stroke-linecap:square") || !strings.Contains(m, "stroke-linejoin:miter") || !strings.Contains(m, "stroke-width:1.75") {
		t.Errorf(".icon must set stroke-width:1.75, stroke-linecap:square and stroke-linejoin:miter, got %q", m)
	}
	for _, f := range []string{"index.html", "login.html"} {
		if strings.Contains(readUIAsset(t, f), "stroke-linecap=\"round\"") || strings.Contains(readUIAsset(t, f), "stroke-linecap:round") {
			t.Errorf("%s still draws an icon with round caps", f)
		}
	}
}
