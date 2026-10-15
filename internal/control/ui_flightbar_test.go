package control

import (
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The Flight Bar (#102): the former topbar (#bar) and Engagement Strip (#ctxbar)
// are one bar of about 42px. The readiness blockers moved off a permanent strip
// into the popover of a readiness ring, and every affordance of both old bars
// stays reachable. The UI has no build step, so the markup, stylesheet and
// script contracts are pinned statically, and the ring and checklist rules run
// under node (_js-tests/ctxbar-model.test.mjs).

var htmlComment = regexp.MustCompile(`(?s)<!--.*?-->`)

// flightBarMarkup returns the markup of the one Flight Bar element, without its
// HTML comments (a commented-out control must not satisfy a contract).
func flightBarMarkup(t *testing.T) string {
	t.Helper()
	shell := shellRegion(t)
	start := strings.Index(shell, `<header id="bar"`)
	end := strings.Index(shell, `</header>`)
	if start < 0 || end < start {
		t.Fatal(`the shell region has no <header id="bar"> Flight Bar`)
	}
	return htmlComment.ReplaceAllString(shell[start:end+len(`</header>`)], "")
}

// section returns s from the first `from` up to the next `to` (or to the end of s
// when `to` is empty). A missing or out-of-order marker fails the one test with a
// message instead of panicking the whole package with a -1 slice index.
func section(t *testing.T, s, from, to string) string {
	t.Helper()
	i := strings.Index(s, from)
	if i < 0 {
		t.Fatalf("marker %q is missing", from)
	}
	if to == "" {
		return s[i:]
	}
	j := strings.Index(s[i:], to)
	if j < 0 {
		t.Fatalf("marker %q does not follow %q", to, from)
	}
	return s[i : i+j]
}

func TestUIFlightBarIsTheOnlyTopBar(t *testing.T) {
	index := readUIAsset(t, "index.html")
	shell := shellRegion(t)
	if n := strings.Count(index, `id="bar"`); n != 1 {
		t.Errorf(`id="bar" appears %d times, want exactly 1`, n)
	}
	requireUIContains(t, shell, `<header id="bar" class="flightbar">`)

	// The second strip and every part that existed only for it are gone.
	for _, gone := range []string{
		`id="ctxbar"`, `id="ctxReady"`, `id="ctxReadyIcon"`, `id="ctxBlockers"`, `id="ctxBlockerPop"`, `id="ctxBlockerTitle"`,
		`id="ctxBlockerClose"`, `id="ctxMore"`, `class="ctx-ready"`, `class="ctx-more"`, `role="region" aria-label="Engagement"`,
	} {
		if strings.Contains(index, gone) {
			t.Errorf("index.html still carries %s from the two-strip layout", gone)
		}
	}
	// Nothing after the bar is a second strip: no second header, and no element
	// carrying one of the bar's own control classes.
	after := section(t, shell, `</header>`, "")
	if strings.Contains(after, "<header") {
		t.Error("a second <header> follows the Flight Bar")
	}
	if m := regexp.MustCompile(`class="[^"]*\b(?:ctx|fb|conn)-[a-z-]+`).FindString(after); m != "" {
		t.Errorf("%s appears after the Flight Bar; the engagement controls live in the one bar", m)
	}
	// The decorative bell had no behaviour and nothing else drew it.
	if strings.Contains(index, "i-bell") {
		t.Error("the unused bell icon is gone with the old topbar")
	}

	// No stylesheet styles the old strip or reads its height token.
	for _, name := range foundationStylesheets {
		css := stripCSSComments(readUIAsset(t, name))
		for _, gone := range []string{"#ctxbar", "--ctxbar-h", ".ctx-more", ".ctx-ready", ".ctx-blockers", ".ctx-sheet", ".ctx-evidence{display:none}"} {
			if strings.Contains(css, gone) {
				t.Errorf("%s still carries %q from the two-strip layout", name, gone)
			}
		}
	}
	// No script looks the old elements up.
	lookup := regexp.MustCompile("['\"`#](ctxbar|ctxBlockers|ctxBlockerPop|ctxBlockerClose|ctxMore|ctxReady|ctxReadyIcon)\\b")
	for _, name := range uiScriptNames(t) {
		if m := lookup.FindString(executableJS(readUIAsset(t, name))); m != "" {
			t.Errorf("%s still looks up the removed element %s", name, m)
		}
	}
}

func TestUIFlightBarHoldsEveryAffordanceInReadingOrder(t *testing.T) {
	bar := flightBarMarkup(t)
	// [Logo] [Project] [Scope] [Omnibar] [intercept status] [Identity] [Readiness ring + popover] [Live status] [Theme].
	order := []string{
		`id="logo"`, `id="ctxProject"`, `id="ctxScope"`, `id="cmdkBtn"`, `id="icptStat"`, `id="icptCompact"`, `id="ctxIdentityWrap"`,
		`id="ctxRing"`, `id="ctxRingPop"`, `id="connChip"`, `id="connPop"`, `id="themeToggle"`,
	}
	prev := -1
	for _, want := range order {
		at := strings.Index(bar, want)
		if at < 0 {
			t.Errorf("the Flight Bar lost %s", want)
			continue
		}
		if at < prev {
			t.Errorf("%s is out of order in the Flight Bar (the tab order follows the DOM)", want)
		}
		prev = at
	}
	// The omnibar sits between the two zones, so it can be centred.
	cmdk, start, end := strings.Index(bar, `id="cmdkBtn"`), strings.Index(bar, `class="fb-zone fb-start"`), strings.Index(bar, `class="fb-zone fb-end"`)
	if !(start >= 0 && start < cmdk && cmdk < end) {
		t.Errorf("#cmdkBtn must sit between the start and end zones (start %d, omnibar %d, end %d)", start, cmdk, end)
	}
	for _, want := range []string{`id="crumb" class="sr-only"`, `class="brand"`, `id="ctxLive"`} {
		if !strings.Contains(bar, want) {
			t.Errorf("the Flight Bar lost %s", want)
		}
	}
	// The popover carries what the strip used to show permanently: the Next
	// action, the target, the evidence counts, the blockers and the scope fix.
	pop := section(t, bar, `id="ctxRingPop"`, `id="connChip"`)
	for _, want := range []string{`id="ctxMeter"`, `id="ctxNext"`, `id="ctxTarget"`, `id="ctxEvidence"`, `id="ctxBlockerList"`, `id="ctxBlockerOpen"`, `id="ctxScopeSettings"`} {
		if !strings.Contains(pop, want) {
			t.Errorf("the readiness popover lost %s", want)
		}
	}
}

func TestUIFlightBarRingIsARealButtonThatOwnsItsPopover(t *testing.T) {
	bar := flightBarMarkup(t)
	ring := regexp.MustCompile(`<button[^>]*id="ctxRing"[^>]*>`).FindString(bar)
	if ring == "" {
		t.Fatal("#ctxRing must be a <button>")
	}
	for _, want := range []string{`type="button"`, `aria-haspopup="dialog"`, `aria-expanded="false"`, `aria-controls="ctxRingPop"`, `aria-label="Readiness: loading"`, `data-state="loading"`, `data-mark="none"`, `data-report="false"`} {
		if !strings.Contains(ring, want) {
			t.Errorf("#ctxRing lacks %s: %s", want, ring)
		}
	}
	// The ring is always on the bar (it is the way into the popover), never hidden or styled inline.
	if regexp.MustCompile(`\shidden[\s>]`).MatchString(ring) || regexp.MustCompile(`\sstyle=`).MatchString(ring) {
		t.Errorf("#ctxRing must be visible by default and carry no inline style: %s", ring)
	}
	// Its drawing is decorative; the visible fraction and badge start hidden and are filled by script.
	if !regexp.MustCompile(`id="ctxRing"[^>]*>\s*<svg class="icon ctx-ring-svg"[^>]*aria-hidden="true" focusable="false"`).MatchString(bar) {
		t.Error("the ring drawing must be a decorative svg (aria-hidden, focusable=false)")
	}
	for _, id := range []string{"ctxRingText", "ctxRingBadge"} {
		if !regexp.MustCompile(`<span id="` + id + `"[^>]*\shidden[\s>]`).MatchString(bar) {
			t.Errorf("#%s must start hidden", id)
		}
	}
	// The popover directly follows its trigger, so the DOM order matches the focus order.
	between := section(t, bar, `id="ctxRing"`, `id="ctxRingPop"`)
	for _, m := range regexp.MustCompile(`\sid="([^"]+)"`).FindAllStringSubmatch(between, -1) {
		switch m[1] {
		case "ctxRing", "ctxRingArc", "ctxRingText", "ctxRingBadge":
		default:
			t.Errorf("#%s sits between the ring and its popover", m[1])
		}
	}

	// Every ring state the model can produce is styled, and nothing relies on colour alone.
	model := executableJS(readUIAsset(t, "js/ctxbar-model.js"))
	css := stripCSSComments(readUIAsset(t, "shell.css"))
	for _, state := range []string{"setup", "blocked", "ready"} {
		if !strings.Contains(model, "'"+state+"'") {
			t.Errorf("ctxbar-model.js no longer produces the %s state", state)
		}
		if !strings.Contains(css, `.ctx-ring[data-state="`+state+`"]`) {
			t.Errorf("shell.css does not style the %s ring state", state)
		}
	}
	requireUIContains(t, css, `.ctx-ring[data-mark="bang"] .ctx-ring-bang`, `.ctx-ring[data-mark="check"] .ctx-ring-check`)
	requireUIContains(t, css, ".ctx-ring:focus-visible", "@media (forced-colors:active)", ".ctx-ring-arc{stroke:Highlight}")
}

func TestUIFlightBarReadinessPopoverCarriesTheBlockerChecklist(t *testing.T) {
	bar := flightBarMarkup(t)
	pop := section(t, bar, `id="ctxRingPop"`, `id="connChip"`)
	// The checklist lives in the popover, once, under its own heading.
	if n := strings.Count(bar, `id="ctxBlockerList"`); n != 1 {
		t.Fatalf("#ctxBlockerList appears %d times, want exactly 1 (inside the popover)", n)
	}
	requireUIContains(t, pop, `<ul id="ctxBlockerList" class="ctx-pop-list" aria-labelledby="ctxBlockerHead">`, `<h3 id="ctxBlockerHead" class="ctx-pop-sub">Report blockers</h3>`)
	if strings.Index(pop, `id="ctxBlockerHead"`) > strings.Index(pop, `id="ctxBlockerList"`) {
		t.Error("the Report blockers heading must come before the checklist it names")
	}
	// ...and the ring opens exactly that popover.
	requireUIContains(t, bar, `aria-controls="ctxRingPop"`)

	// Script: the list is filled from the model, project blockers first, each with a Fix control.
	ctx := executableJS(readUIAsset(t, "js/ctxbar.js"))
	requireUIContains(t, ctx,
		"$('ctxBlockerList')", "blockerChecklist(projectState.get(), POPOVER_ROW_CAP)",
		"model.project.forEach", "model.findings.forEach", "'Fix: ' + b.label", "'Fix F-' + row.id",
		"if (popOpen) fillBlockerList();",
		// Rebuilt only when the content changed, so a live refresh never drops focus from a Fix link.
		"const sig = JSON.stringify(model);", "if (list.dataset.sig === sig) return;", "list.dataset.sig = sig;",
	)
	if strings.Index(ctx, "model.project.forEach") > strings.Index(ctx, "model.findings.forEach") {
		t.Error("project blockers must be listed before per-finding blockers")
	}
	// Opening the popover refreshes the list, so it is never stale.
	open := section(t, ctx, "export function openReadinessPopover", "export function closeReadinessPopover")
	requireUIContains(t, open, "fillBlockerList();", "placePopover();", "pop.hidden = false;", "ring.setAttribute('aria-expanded', 'true')")
	// The checklist styling exists for both row kinds, the empty note and the footer.
	css := stripCSSComments(readUIAsset(t, "shell.css"))
	requireUIContains(t, css, ".ctx-pop-list{", ".ctx-pop-row{", ".ctx-pop-empty{", ".ctx-pop-foot{", ".ctx-gap{")
}

func TestUIFlightBarModelUnderNode(t *testing.T) {
	node := requireNode(t)
	cmd := exec.Command(node, "--test", "_js-tests/ctxbar-model.test.mjs")
	cmd.Dir = "ui"
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("flight bar model node tests failed: %v\n%s", err, out)
	}
}

// The bar is the control height plus ten pixels at the default density and
// follows the density tiers and the touch tier, so a control and its focus ring
// always fit inside it (the bar clips vertically so it can scroll sideways).
func TestUIFlightBarHeightTokens(t *testing.T) {
	app := readUIAsset(t, "app.css")
	root := parseThemeBlock(t, app, ":root{")
	if root["--topbar-h"] != "42px" {
		t.Errorf("--topbar-h = %q, want 42px", root["--topbar-h"])
	}
	if _, ok := root["--ctxbar-h"]; ok {
		t.Error("--ctxbar-h is gone with the second strip")
	}
	if root["--sticky-top"] != "var(--topbar-h)" {
		t.Errorf("--sticky-top = %q, want var(--topbar-h): the Flight Bar is the only sticky chrome", root["--sticky-top"])
	}

	shell := stripCSSComments(readUIAsset(t, "shell.css"))
	height := map[string]float64{"default": 42}
	for density, want := range map[string]string{"compact": "36", "comfortable": "50"} {
		rule := `:root[data-density="` + density + `"]{--topbar-h:` + want + `px}`
		if !strings.Contains(shell, rule) {
			t.Errorf("shell.css missing %s", rule)
		}
		height[density], _ = strconv.ParseFloat(want, 64)
	}
	coarse := mediaBlock(t, shell, "@media (pointer:coarse)")
	requireUIContains(t, coarse, ":root,:root[data-density]{--topbar-h:54px}")
	height["coarse"] = 54

	// Each tier's controls plus four pixels of focus ring on each side fit the bar.
	// The control heights are read from the stylesheets (--hit-min per density, and
	// the 44px touch minimum the coarse tier gives every bar control), not assumed.
	px := func(v string) float64 { f, _ := strconv.ParseFloat(strings.TrimSuffix(v, "px"), 64); return f }
	hit := map[string]float64{"default": px(root["--hit-min"]), "coarse": 44}
	for _, density := range []string{"compact", "comfortable"} {
		hit[density] = px(parseThemeBlock(t, app, `:root[data-density="`+density+`"]{`)["--hit-min"])
	}
	requireUIContains(t, coarse, ".ctx-chip,.ctx-ring,.ctx-row,.conn-chip,#cmdkBtn{min-height:44px}")
	for tier, bar := range height {
		if hit[tier] <= 0 {
			t.Errorf("%s density: no --hit-min control height is set", tier)
			continue
		}
		if bar < hit[tier]+8 {
			t.Errorf("%s density: a %gpx bar cannot hold %gpx controls and their focus ring", tier, bar, hit[tier])
		}
	}
	// Anything pinned under the bar hangs from the token, not a number.
	for _, name := range []string{"shell.css", "flow.css"} {
		css := stripCSSComments(readUIAsset(t, name))
		for _, m := range regexp.MustCompile(`[;{]\s*top\s*:\s*(?:calc\()?\s*(4[2-8]|9[0-9])px`).FindAllString(css, -1) {
			t.Errorf("%s pins %q under the bar; use var(--sticky-top) or var(--topbar-h)", name, m)
		}
	}
}

// No width hides the omnibar, the scope pill, the readiness ring or the live
// status chip: the narrow tiers shorten them and drop the pieces that other
// surfaces cover (the dock and the palette), never the controls themselves.
func TestUIFlightBarNeverHidesAnAffordance(t *testing.T) {
	css := stripCSSComments(readUIAsset(t, "shell.css"))
	always := map[string]bool{"#cmdkBtn": true, ".ctx-ring": true, "#ctxRing": true, ".conn-chip": true, "#connChip": true, ".ctx-scope": true, "#ctxScope": true, "#icptStat": true, "#bar": true, ".fb-zone": true, ".fb-end": true, ".fb-engage": true}
	hides := regexp.MustCompile(`display\s*:\s*none|visibility\s*:\s*hidden|(?:^|[;\s])(?:width|max-width|height|max-height)\s*:\s*0(?:px)?\s*(?:;|$)`)
	lastCompound := regexp.MustCompile(`[^\s>+~]+$`)
	pseudoOrAttr := regexp.MustCompile(`\[[^\]]*\]|::?[a-zA-Z-]+(?:\([^)]*\))?`)
	for _, m := range regexp.MustCompile(`([^{}]+)\{([^{}]*)\}`).FindAllStringSubmatch(css, -1) {
		if !hides.MatchString(m[2]) {
			continue
		}
		for _, sel := range strings.Split(m[1], ",") {
			sel = strings.TrimSpace(sel)
			if i := strings.LastIndex(sel, "}"); i >= 0 {
				sel = strings.TrimSpace(sel[i+1:])
			}
			// A descendant rule hides the control it ends in: `#bar .ctx-ring{display:none}`
			// and `.ctx-ring[data-state="x"]{...}` both hide .ctx-ring. A pseudo-element
			// (`#bar::-webkit-scrollbar`) is a part of the control, not the control.
			compound := lastCompound.FindString(sel)
			if strings.Contains(compound, "::") {
				continue
			}
			if target := pseudoOrAttr.ReplaceAllString(compound, ""); always[target] {
				t.Errorf("shell.css hides %s through %q; narrow widths may shorten it but never remove it", target, sel)
			}
		}
	}
	// The phone tier keeps the pieces that cover what it drops.
	requireUIContains(t, readUIAsset(t, "mobile.css"), "#dock{display:flex")
	requireUIContains(t, executableJS(readUIAsset(t, "js/dock.js")), "'Switch project'", "row('Theme'")
	requireUIContains(t, executableJS(readUIAsset(t, "js/cmdk-logic.js")), "startsWith('@')")
}

// The bar and its popover convey state without motion, so there is nothing to
// switch off under prefers-reduced-motion beyond the global reset.
func TestUIFlightBarAddsNoMotion(t *testing.T) {
	css := stripCSSComments(readUIAsset(t, "shell.css"))
	// scroll-behavior is the smooth-scroll property; overscroll-behavior (which only
	// stops scroll chaining) is not motion, so the match must not be a substring.
	for _, banned := range []*regexp.Regexp{
		regexp.MustCompile(`transition`), regexp.MustCompile(`animation`), regexp.MustCompile(`@keyframes`), regexp.MustCompile(`(?:^|[^a-z-])scroll-behavior`),
	} {
		if m := banned.FindString(css); m != "" {
			t.Errorf("shell.css uses %q; the Flight Bar adds no motion", strings.TrimLeft(m, ";{ "))
		}
	}
	// ...and no other stylesheet gives a bar control a transition or an animation.
	barSelector := regexp.MustCompile(`#bar\b|\.flightbar|\.fb-|\.ctx-(?:chip|ring|pop|row|scope|project|identity|avatar)|\.conn-(?:chip|pop)|#cmdkBtn|#themeToggle`)
	motion := regexp.MustCompile(`(?:transition|animation)(?:-[a-z]+)?\s*:\s*([^;}]+)`)
	for _, name := range foundationStylesheets {
		for _, m := range regexp.MustCompile(`([^{}]+)\{([^{}]*)\}`).FindAllStringSubmatch(stripCSSComments(readUIAsset(t, name)), -1) {
			if !barSelector.MatchString(m[1]) {
				continue
			}
			for _, d := range motion.FindAllStringSubmatch(m[2], -1) {
				if strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(d[1]), "!important")) != "none" {
					t.Errorf("%s animates a Flight Bar control (%s{...%s}); the bar adds no motion", name, strings.TrimSpace(m[1]), d[0])
				}
			}
		}
	}
	reduced := mediaBlock(t, readUIAsset(t, "app.css"), "@media (prefers-reduced-motion:reduce)")
	requireUIContains(t, reduced, "transition:none!important", "animation:none!important")
}

// While interception is on, app.js writes "· REQ intercept ON · 2 held" into
// #icptStat. The bar has no room for that sentence at 900px and below, so the
// stylesheet keeps #icptStat for assistive technology only (clipped, never
// display:none, so the live region keeps announcing) and shows a short aria-hidden
// twin, "REQ · 2 held", that ctxbar.js keeps in step.
func TestUIFlightBarNarrowInterceptStatusKeepsAShortTwin(t *testing.T) {
	bar := flightBarMarkup(t)
	requireUIContains(t, bar,
		`id="icptStat" role="status" aria-live="polite"`,
		`<span id="icptCompact" class="kv state-active icpt-compact" aria-hidden="true" hidden></span>`,
	)
	if strings.Index(bar, `id="icptCompact"`) < strings.Index(bar, `id="icptStat"`) {
		t.Error("the compact twin must follow #icptStat")
	}
	css := stripCSSComments(readUIAsset(t, "shell.css"))
	requireUIContains(t, css, ".icpt-compact{display:none;")
	narrow := mediaBlock(t, css, "@media (max-width:900px)")
	requireUIContains(t, narrow, "#icptStat{position:absolute;width:1px;height:1px;overflow:hidden;clip:rect(0 0 0 0)", ".icpt-compact{display:inline}")
	if regexp.MustCompile(`#icptStat\{[^}]*display\s*:\s*none`).MatchString(css) {
		t.Error("#icptStat must never be display:none: it is the live region for the intercept state")
	}

	ctx := executableJS(readUIAsset(t, "js/ctxbar.js"))
	requireUIContains(t, ctx,
		"function watchIntercept()", "new MutationObserver(sync).observe(stat,", "compactIntercept(full)",
		"getComputedStyle(stat).display === 'none'", "compact.hidden = !text;", "watchIntercept();",
	)
	requireUIContains(t, executableJS(readUIAsset(t, "js/ctxbar-model.js")), "export function compactIntercept(text)")
}

// The saved default identity is applied through the store, which notifies
// synchronously. The callback that applies it must then render the store's current
// state, not the older snapshot it was handed: repainting that snapshot showed "No
// identity" after every reload that had a saved one.
func TestUIFlightBarRestoresTheSavedIdentityIntoTheLiveState(t *testing.T) {
	ctx := executableJS(readUIAsset(t, "js/ctxbar.js"))
	sub := section(t, ctx, "projectState.subscribe((s) => {", "renderCtxbar(projectState.get());")
	requireUIContains(t, sub, "if (!saved || s.identities.some((i) => i.name === saved)) {", "restored = true;", "projectState.setActiveIdentity(saved);")
	if strings.Index(sub, "restored = true;") > strings.Index(sub, "projectState.setActiveIdentity(saved);") {
		t.Error("the restored flag must be set before the identity is applied: applying it notifies synchronously and re-enters the callback")
	}
	if regexp.MustCompile(`renderCtxbar\(s\)`).MatchString(ctx) {
		t.Error("ctxbar.js renders a stale store snapshot; render projectState.get()")
	}
	// The visible label is also the control's accessible name (the custom select is
	// labelled by it), so it says what the control does.
	requireUIContains(t, flightBarMarkup(t), `<label for="ctxIdentity" class="ctx-key">Act as</label>`)
}

// The popovers hang from a bar that never wraps text and sits in short windows too:
// in a window under 560px tall the readiness popover scrolls as a whole and its
// checklist grows to its content (a list of its own would collapse under the fixed
// header, rows and footer), a Fix button's focus ring fits inside the checklist's
// scroll area, and the connection popover wraps long addresses.
func TestUIFlightBarPopoversStayReachableOnShortAndNarrowWindows(t *testing.T) {
	css := stripCSSComments(readUIAsset(t, "shell.css"))
	pop := regexp.MustCompile(`\.ctx-pop\{[^}]*\}`).FindString(css)
	requireUIContains(t, pop, "max-height:min(70vh,480px)", "overflow-y:auto", "white-space:normal")
	requireUIContains(t, css,
		".ctx-pop-list{margin:calc(-1 * var(--sp-1));padding:var(--sp-1);",
		".conn-pop{white-space:normal}",
		// The omnibar and the scope pill keep a hover fill (their own backgrounds out-rank .btn:hover and .ctx-chip:hover).
		"#cmdkBtn:hover{background:var(--bg3)}", ".ctx-scope[data-state]:hover{background:var(--bg3)}",
	)
	requireUIContains(t, mediaBlock(t, css, "@media (max-height:560px)"), ".ctx-pop-list{overflow:visible;flex:none}")
	requireUIContains(t, mediaBlock(t, css, "@media (forced-colors:active)"), ".ctx-ring-track,.ctx-ring-bang,.ctx-ring-check{stroke:CanvasText}")
}
