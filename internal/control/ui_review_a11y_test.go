package control

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Review fixes from the WCAG 2.2 AA audit of the shell, top bar, sheets, palette,
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

func TestUIFlightBarLabelsStartWithVisibleText(t *testing.T) {
	src := executableJS(readUIAsset(t, "js/ctxbar.js"))
	requireUIContains(t, src,
		"'Scope ' + m.text + '. ' + m.label", "'Target ' + s.brief.target", "'Target Set target.", "'Evidence ' + t + '. Open Findings.'", "'Next ' + s.nextAction.label")
	index := readUIAsset(t, "index.html")
	requireUIContains(t, index, `<span class="ctx-key">Evidence</span>`)
	if strings.Contains(index, `<span class="ctx-key">Evid</span>`) {
		t.Error("the readiness popover must spell out Evidence")
	}
}

func TestUIReadinessPopoverFollowsItsTrigger(t *testing.T) {
	index := readUIAsset(t, "index.html")
	trigger := strings.Index(index, `id="ctxRing"`)
	pop := strings.Index(index, `id="ctxRingPop"`)
	next := strings.Index(index, `id="connChip"`)
	if !(trigger > 0 && trigger < pop && pop < next) {
		t.Errorf("#ctxRingPop must sit directly after #ctxRing (trigger %d, pop %d, next control %d)", trigger, pop, next)
	}
}

func TestUIFlightBarFocusRingFitsAndNoShorthandFocusRing(t *testing.T) {
	shell := readUIAsset(t, "shell.css")
	// The bar scrolls sideways when a window is narrower than the phone tier, which
	// clips vertically; its height tokens leave room for a control and its focus ring
	// (TestUIFlightBarHeightTokens), and the ring is drawn inside that room.
	if !regexp.MustCompile(`#bar\{[^}]*overflow-x:auto;overflow-y:hidden`).MatchString(shell) {
		t.Error("#bar must scroll sideways instead of clipping when the window is narrower than the phone tier")
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
