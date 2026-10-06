package control

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Review fixes from the WCAG 2.2 AA audit of the shell, strip, sheets, palette,
// readiness meter and toasts.

func TestUIToastSitsAboveTheModalStack(t *testing.T) {
	css := readUIAsset(t, "app.css")
	requireUIContains(t, css, "--z-toast:100000;", "z-index:var(--z-toast)}")
	if strings.Contains(css, "pointer-events:none;z-index:100}") {
		t.Error("#toast must not keep the hard-coded z-index below the 400+ modal stack")
	}
	core := executableJS(readUIAsset(t, "js/core.js"))
	m := regexp.MustCompile(`MODAL_Z_BASE=(\d+)`).FindStringSubmatch(core)
	if n, err := strconv.Atoi(m[1]); m == nil || err != nil || n >= 100000 {
		t.Errorf("modal z-index base must stay below the toast layer, got %v", m)
	}
}

func TestUIErrorToastsPersistAndCanBeDismissed(t *testing.T) {
	core := executableJS(readUIAsset(t, "js/core.js"))
	requireUIContains(t, core,
		"if (sev !== 'error') armToastTimer(",
		"x.setAttribute('aria-label', 'Dismiss')",
		"e.key === 'Escape') dismissToast(t)",
	)
	if strings.Contains(core, "sev === 'error' ? 8000") {
		t.Error("error toasts must not auto-dismiss")
	}
	index := readUIAsset(t, "index.html")
	requireUIContains(t, index, `<div id="toast" role="status" aria-live="polite" aria-atomic="false">`)
}

func TestUIReadinessMeterIsNamedAndLinkFree(t *testing.T) {
	src := executableJS(readUIAsset(t, "js/readiness-meter.js"))
	requireUIContains(t, src, "'aria-label': 'Report readiness'", "role: 'group'")
	if regexp.MustCompile(`role:\s*'meter'[^}]*\}`).FindString(src) == "" {
		t.Error("the plain meter keeps role=meter")
	}
}

func TestUIPaletteTriggerAndComboboxState(t *testing.T) {
	index := readUIAsset(t, "index.html")
	btn := regexp.MustCompile(`<button id="cmdkBtn"[^>]*>`).FindString(index)
	if strings.Contains(btn, "aria-label=") {
		t.Errorf("#cmdkBtn must be named by its visible text (WCAG 2.5.3): %s", btn)
	}
	requireUIContains(t, btn, `aria-keyshortcuts="Control+K Meta+K"`)
}

func TestUIStripLabelsStartWithVisibleText(t *testing.T) {
	src := executableJS(readUIAsset(t, "js/ctxbar.js"))
	requireUIContains(t, src,
		"'Target ' + s.brief.target", "'Target Set target.", "'Evidence ' + t + '. Open Findings.'", "'Next ' + s.nextAction.label")
	index := readUIAsset(t, "index.html")
	requireUIContains(t, index, `<span class="ctx-key">Evidence</span>`)
	if strings.Contains(index, `<span class="ctx-key">Evid</span>`) {
		t.Error("the strip must spell out Evidence")
	}
}

func TestUIBlockerPopoverFollowsItsTrigger(t *testing.T) {
	index := readUIAsset(t, "index.html")
	trigger := strings.Index(index, `id="ctxBlockers"`)
	pop := strings.Index(index, `id="ctxBlockerPop"`)
	next := strings.Index(index, `id="ctxNext"`)
	if !(trigger > 0 && trigger < pop && pop < next) {
		t.Errorf("#ctxBlockerPop must sit directly after #ctxBlockers (trigger %d, pop %d, next %d)", trigger, pop, next)
	}
}

func TestUIStripFocusRingFitsAndNoShorthandFocusRing(t *testing.T) {
	shell := readUIAsset(t, "shell.css")
	if !regexp.MustCompile(`#ctxbar\{[^}]*padding:var\(--sp-2\) max`).MatchString(shell) {
		t.Error("#ctxbar needs vertical padding so the focus ring is not clipped by overflow-x:auto")
	}
	for _, name := range []string{"app.css", "shell.css", "mobile.css", "findings.css", "panel-proxy.css", "panel-scan.css", "settings.css", "report.css", "surfaces.css", "flow.css", "primitives.css"} {
		css := readUIAsset(t, name)
		if regexp.MustCompile(`outline:\s*var\(--focus-ring\)`).MatchString(css) {
			t.Errorf("%s uses outline:var(--focus-ring); --focus-ring is a colour, so that draws no ring", name)
		}
	}
}

func TestUIThemeToggleExposesState(t *testing.T) {
	app := executableJS(readUIAsset(t, "js/app.js"))
	requireUIContains(t, app, "'Theme: '", "b.setAttribute('aria-label',l)")
}
