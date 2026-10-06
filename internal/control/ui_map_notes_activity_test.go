package control

import (
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

// WP11 (UI overhaul): Map coverage, Notes references and the Activity timeline.
// Pure logic runs under node; DOM-shaped behaviour is pinned against the embedded
// assets, as for the other ui_* tests.

func TestUIMapNotesActivityPureLogicUnderNode(t *testing.T) {
	node := requireNode(t)
	cmd := exec.Command(node, "--test", "_js-tests/activity-model.test.mjs", "_js-tests/notes-model.test.mjs", "_js-tests/map-coverage.test.mjs")
	cmd.Dir = "ui"
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("map/notes/activity node tests failed: %v\n%s", err, out)
	}
}

// The pure view-models must stay loadable under node: no imports, no DOM, no network.
func TestUIMapNotesActivityModelsArePure(t *testing.T) {
	for _, name := range []string{"js/activity-model.js", "js/notes-model.js", "js/map-coverage.js"} {
		src := executableJS(readUIAsset(t, name))
		if regexp.MustCompile(`(?m)^import `).MatchString(src) {
			t.Errorf("%s must have no imports", name)
		}
		for _, banned := range []string{"document.", "window.", "fetch(", "api(", "localStorage", "innerHTML"} {
			if strings.Contains(src, banned) {
				t.Errorf("%s must stay pure, found %q", name, banned)
			}
		}
	}
}

func TestUIMapNotesActivityMarkupKeepsIdsAndAddsControls(t *testing.T) {
	m := uiRegion(t, "map")
	for _, want := range []string{`id="mapDomain"`, `id="mapSearch"`, `id="mapViewSeg"`, `id="mapTree"`, `id="mapTable"`, `id="mapWarn" class="map-warn" data-init-hidden role="status" aria-live="polite"`,
		`id="mapCoverage"`, `id="mapUnlinked"`, `aria-pressed="false"`} {
		if !strings.Contains(m, want) {
			t.Errorf("region:map missing %s", want)
		}
	}
	n := uiRegion(t, "notes")
	for _, want := range []string{`id="notesEdit"`, `id="notesPreview"`, `id="notesSeg"`, `id="notesStatus"`, `aria-live="polite"`, `id="notesPromote"`} {
		if !strings.Contains(n, want) {
			t.Errorf("region:notes missing %s", want)
		}
	}
	a := uiRegion(t, "activity")
	for _, want := range []string{`id="actFeed" class="scroll"></div>`, `id="actCount" role="status" aria-live="polite" aria-atomic="true"`,
		`id="actChips" role="group"`, `id="actAttention"`, `id="actNewPill"`, `id="actAppendix"`, `id="actClear"`, `id="actIntentFilter"`} {
		if !strings.Contains(a, want) {
			t.Errorf("region:activity missing %s", want)
		}
	}
	// The high-volume feed itself is never a live region; only the pill-free count is.
	if strings.Contains(a, `id="actFeed" class="scroll" aria-live`) {
		t.Error("the Activity feed must not be a live region")
	}
	for _, region := range []string{m, n, a} {
		if strings.Contains(region, ` style="`) {
			t.Error("new panel markup must not use inline styles")
		}
	}
}

func TestUIMapUsesOnlyRealDataAndDrawer(t *testing.T) {
	mapJS := executableJS(readUIAsset(t, "js/map.js"))
	requireUIContains(t, mapJS,
		"from './map-coverage.js'",
		"openFlow(id, { source: 'map' })",
		"flowPopup(id)",
		"api('/api/findings')",
		"renderState(host, 'error'",
		"renderState(host, 'loading'",
		"onRetry: loadEndpoints",
		"wireMapContextMenus()",
	)
	// Coverage the API does not expose must never be invented.
	for _, fake := range []string{"Tested in Repeater", "Scanned in", "tested:"} {
		if strings.Contains(mapJS, fake) {
			t.Errorf("map.js claims coverage the API does not provide: %q", fake)
		}
	}
	// The lazy module boundary stays in project.js; nothing else imports map.js statically.
	requireUIContains(t, readUIAsset(t, "js/project.js"), "import('./map.js')")
	if strings.Contains(executableJS(readUIAsset(t, "js/map-coverage.js")), "api(") {
		t.Error("map-coverage.js must not fetch")
	}
}

func TestUINotesChipsResolveOnlyExistingIdsAndNeverEditSavedText(t *testing.T) {
	notes := executableJS(readUIAsset(t, "js/notes.js"))
	requireUIContains(t, notes,
		"from './notes-model.js'",
		"resolveRefs(parseNoteRefs(node.nodeValue),known)",
		"api('/api/findings')",
		"api('/api/flows/'+id)",
		"closest('code,pre,a,button')",
		"document.createTextNode(t.text)",
		"promoteRequest(noteSelectionText())",
		"void decorateNoteRefs(box)",
	)
	// Status colour and visibility come from CSS, not inline style writes.
	status := notes[strings.Index(notes, "function setNotesStatus"):strings.Index(notes, "const notesAutosave=")]
	if strings.Contains(status, ".style.") {
		t.Error("setNotesStatus must not write inline styles")
	}
	// The post-pass operates on the preview DOM only.
	decorate := notes[strings.Index(notes, "export async function decorateNoteRefs"):strings.Index(notes, "$('#notesPreview')&&")]
	if strings.Contains(decorate, "notesEdit") || strings.Contains(decorate, "notesAutosave") {
		t.Error("reference chips must never touch the editor or the saved note")
	}
}

func TestUIActivityTimelineKeepsPersistenceContract(t *testing.T) {
	activity := executableJS(readUIAsset(t, "js/activity.js"))
	requireUIContains(t, activity,
		"from './activity-model.js'",
		"groupByDay(a)",
		"act-day-h",
		"aria-pressed=",
		"openFlow(id,{source:'activity'})",
		"selectFlow(id)",
		"shouldDeferRender($('#actFeed')?.scrollTop)",
		"activityAppendix(rows)",
		"#humanInputBar .hi-prompt",
		// Unchanged reconciliation of live events and snapshots.
		"let activityPendingEvents=[]",
		"state.activity=mergeActivitySnapshot(d.activity||[])",
		"activityPendingEvents.unshift(it)",
	)
	// The actor is always the AI/MCP: the badge carries text, not only an icon.
	requireUIContains(t, activity, `class="act-actor"`, ">AI</span>")
	// Only the flow-opening rows are actionable; expandable rows keep their ARIA contract.
	requireUIContains(t, activity, `aria-controls="actDetail-${i}"`, `aria-expanded="${expanded}"`)
	// Event types the API does not record are not fabricated.
	for _, fake := range []string{"evidence attached", "identity switched", "scope toggled"} {
		if strings.Contains(strings.ToLower(activity), fake) {
			t.Errorf("activity.js renders an event type the activity API does not record: %q", fake)
		}
	}
}

func TestUIPanelMiscStylesAreTokenOnlyAndMotionFree(t *testing.T) {
	css := readUIAsset(t, "panel-misc.css")
	defined := map[string]bool{}
	for _, name := range []string{"app.css", "workbench.css", "primitives.css", "shell.css", "surfaces.css", "mobile.css"} {
		for _, m := range regexp.MustCompile(`(--[A-Za-z0-9_-]+)\s*:`).FindAllStringSubmatch(readUIAsset(t, name), -1) {
			defined[m[1]] = true
		}
	}
	for _, m := range regexp.MustCompile(`var\((--[A-Za-z0-9_-]+)`).FindAllStringSubmatch(css, -1) {
		if !defined[m[1]] {
			t.Errorf("panel-misc.css uses undefined token %s", m[1])
		}
	}
	if regexp.MustCompile(`@keyframes|animation\s*:|transition\s*:`).MatchString(css) {
		t.Error("panel-misc.css must not animate; reduced-motion coverage assumes no motion here")
	}
	if strings.Contains(css, "style=") {
		t.Error("panel-misc.css must not reference inline styles")
	}
	for _, want := range []string{".act-chip", ".act-day-h{position:sticky", ".note-chip", ".notes-wide", ".map-auth", ".map-pip", "max-width:720px", "pointer:coarse"} {
		if !strings.Contains(css, want) {
			t.Errorf("panel-misc.css missing %q", want)
		}
	}
	// Status is icon + text: the auth/pip markup in map.js carries a text label.
	mapJS := readUIAsset(t, "js/map.js")
	if !strings.Contains(mapJS, "esc(auth.label)") || !strings.Contains(mapJS, `class="u-sr">${esc(linked.label)}`) {
		t.Error("map coverage markup must carry text for every icon")
	}
}
