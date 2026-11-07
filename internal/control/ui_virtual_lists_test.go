package control

import (
	"regexp"
	"strings"
	"testing"
)

// Issue #105 point 3, the deferred half: the Findings list and the Scanner issue list run on
// the shared windowed renderer (core.js createListView, wrapped by js/windowed-list.js) with
// container-level event delegation, the way Intruder results do. These are structural
// guarantees over the shipped JS and CSS. The behaviour itself is covered under node:
// ui/_js-tests/list-rows-model.test.mjs (index, key and sticky-header math) and
// windowed-list.test.mjs (the glue, run on the real createListView with a fake DOM).

// jsBlock returns the source from the first occurrence of from up to the next top-level
// declaration, so an assertion is scoped to one function. Works for both code styles used here.
func jsBlock(t *testing.T, src, from string) string {
	t.Helper()
	start := strings.Index(src, from)
	if start < 0 {
		t.Fatalf("source has no %q", from)
	}
	rest := src[start+len(from):]
	end := regexp.MustCompile(`\n(function |async function |export |const |let |\$\(|/\*)`).FindStringIndex(rest)
	if end == nil {
		return src[start:]
	}
	return src[start : start+len(from)+end[0]]
}

// nestedBlock returns a function nested one level deep (two-space indent, as in windowed-list.js)
// from its signature to its closing brace.
func nestedBlock(t *testing.T, src, from string) string {
	t.Helper()
	start := strings.Index(src, from)
	if start < 0 {
		t.Fatalf("source has no %q", from)
	}
	end := strings.Index(src[start:], "\n  }\n")
	if end < 0 {
		t.Fatalf("no closing brace after %q", from)
	}
	return src[start : start+end]
}

func TestUIWindowedListHelpersStayLayered(t *testing.T) {
	model := readUIAsset(t, "js/list-rows-model.js")
	requireUIContains(t, model,
		"export function buildRows(", "export function stepIndex(", "export function revealScrollTop(", "export function stickyState(")
	exec := executableJS(model)
	for _, forbidden := range []string{"document", "window.", "querySelector", "./core.js", "import "} {
		if strings.Contains(exec, forbidden) {
			t.Errorf("list-rows-model.js must stay DOM-free and standalone; found %q", forbidden)
		}
	}

	glue := executableJS(readUIAsset(t, "js/windowed-list.js"))
	requireUIContains(t, glue,
		"import { createListView } from './core.js'",
		"import { revealScrollTop } from './list-rows-model.js'",
		"export function createWindowedList(options)",
		// One window renderer: the glue wraps it, it never re-implements the arithmetic.
		"createListView({",
		"new ResizeObserver(",
		"{ passive: true }",
		"focus({ preventScroll: true })",
		"setAttribute('tabindex', '0')",
		// A list painted over by its owner must not be repainted by a resize or scroll.
		"let suspended = false;",
		"if (destroyed || suspended || !view.isVirtual()) return;",
		// A hidden pane measures 0px and must keep the window it had.
		"if (!container.clientHeight) return;",
		// A short list has no window: refresh()/resize must not make createListView repaint every row.
		"refresh: () => { if (!suspended && view.isVirtual()) run(() => view.refresh()); },",
		"if (!view.isVirtual()) return;",
		// The one tab stop of a virtualized list is the open row; failing that the row that has focus (a
		// rebuilt row comes back at tabindex -1), failing that a row in the viewport, never an overscan row above it.
		"indexOf = null, topInset = null",
		"let stop = focused || rows[0];",
		"if (!focused && indexOf && view.isVirtual() && rowHeight() > 0) {",
		"stop = Array.prototype.find.call(rows, (row) => indexOf(row) >= first) || stop;",
		// Focus on another control inside the list (an empty state's Clear filters) is not dropped to <body>:
		// it goes to the open row, or the container; a row that scrolled out goes to the container.
		"return row ? keyOf(row) : '';",
		"const fallback = key ? container : (container.querySelector(rowSelector + '[tabindex=\"0\"]') || container);",
		"restoreFocus(key);",
	)
	if strings.Contains(glue, "computeVirtualWindow") {
		t.Error("windowed-list.js must build on createListView, not recompute the window itself")
	}
	// #105: the scroll path must not force layout. Row height is measured once (calibrate), never per frame.
	onScroll := nestedBlock(t, glue, "function onScroll() {")
	for _, forbidden := range []string{"getBoundingClientRect", "offsetHeight", "offsetTop", "calibrate("} {
		if strings.Contains(onScroll, forbidden) {
			t.Errorf("the scroll handler must not read layout (%s)", forbidden)
		}
	}
	if !strings.Contains(nestedBlock(t, glue, "function calibrate() {"), "if (calibrated || !view.isVirtual()) return false;") {
		t.Error("calibrate() must measure once, and only a virtualized list (a short list keeps natural row heights)")
	}

	// core.js stays the lower layer: createListView is the single windowed renderer and does not know the helpers
	// built on it. (What createListView does is pinned by running the real function in windowed-list.test.mjs.)
	core := readUIAsset(t, "js/core.js")
	if strings.Contains(core, "windowed-list.js") || strings.Contains(core, "list-rows-model") {
		t.Error("core.js must not know about the Findings/Scanner list helpers")
	}
}

func TestUIFindingsListIsWindowedWithDelegation(t *testing.T) {
	raw := readUIAsset(t, "js/findings.js")
	src := executableJS(raw)
	requireUIContains(t, src,
		"import { createWindowedList } from './windowed-list.js'",
		"import { buildRows, stepIndex, stickyState } from './list-rows-model.js'",
		"const FIND_VIRT_MIN =",
		"findListWin = createWindowedList({",
		"rowSelector: '.find-row'",
		"getItems: () => findRowModel.rows",
		"renderRow: findRowHTML",
		"threshold: FIND_VIRT_MIN",
	)

	// Severity headers are rows of the flat list, drawn by the same header renderer as before.
	requireUIContains(t, src,
		"findRowModel = buildRows(list, findGroupBySeverity ? f => severityKey(f.severity) : null)",
		"findingGroupHeaderHTML(row.key, row.count)",
		"findingRowHTML(row.item, row.item.id === findTabId)",
	)

	// Three delegated listeners, no per-row binding.
	on := jsBlock(t, src, "findListWin = createWindowedList({")
	for _, want := range []string{"on: {", "click: (row, event) =>", "contextmenu: (row, event) =>", "keydown: (row, event) =>"} {
		if !strings.Contains(on, want) {
			t.Errorf("the delegated Findings list lost %q", want)
		}
	}
	for _, gone := range []string{"el.oncontextmenu", "el.onclick", "el.onkeydown", ".querySelectorAll('.find-row').forEach"} {
		if strings.Contains(src, gone) {
			t.Errorf("findings.js still binds rows one by one (%s); the container delegates", gone)
		}
	}

	// The context menu survives delegation: the handler claims the event (so core.js's document-level
	// dispatcher does not open the workspace menu on top) and opens the same row menu.
	requireUIRegex(t, src, `contextmenu: \(row, event\) => \{ event\.preventDefault\(\); showFindingRowCtx\(event, Number\(row\.dataset\.id\)\); \}`)

	// Selection survives: a click opens the row and moves focus to the detail, as before.
	sel := jsBlock(t, src, "function selectFindingRow(id, moveToDetail) {")
	requireUIContains(t, sel,
		"selFinding = id;", "rememberFindingRoute(id);", "find-mobile-detail-visible", "renderFindings();",
		"matchMedia('(max-width:600px)').matches ? $('#findBackToList') : $('#findTitleText')")
	// Keys: Enter/Space open; arrows, Home/End and the Page keys move the open finding and the
	// row is scrolled into the window and focused before the next key can arrive.
	key := jsBlock(t, src, "function onFindingRowKey(row, event) {")
	requireUIContains(t, key, "event.key === 'Enter' || event.key === ' '", "selectFindingRow(id, true)", "moveFindingByKey(event, id)")
	move := jsBlock(t, src, "function moveFindingByKey(event, fromId) {")
	requireUIContains(t, move,
		"stepIndex({", "findOrderIndex.get(fromId)", "revealFinding(selFinding, 'nearest')", "renderFindings();", "focusFindingRow(selFinding)",
		"findEditMode = false;", "findSection = 'overview';", "rememberFindingRoute(selFinding)")
	// A modifier chord is left to the browser, as before.
	requireUIContains(t, move, "event.metaKey || event.ctrlKey || event.shiftKey || event.altKey")

	// The roving tab stop is kept (one tab stop for the list, not one per row)...
	requireUIContains(t, raw, `tabindex="${tab ? '0' : '-1'}"`)
	// ...and the focused-row key still rides on the data-id the row markup carries.
	requireUIContains(t, src, "keyOf: row => row.dataset.id", "findRow: id => box.querySelector(`.find-row[data-id=\"${id}\"]`)",
		"indexOf: row =>", "topInset: () => (findGroupBySeverity && findListWin ? findListWin.rowHeight() : 0)")

	// The keyed render still skips an unchanged list; empty states stop the window repainting over them.
	render := jsBlock(t, src, "function renderFindings() {")
	if n := strings.Count(render, "findListWin.suspend()"); n != 2 {
		t.Errorf("renderFindings must suspend the windowed list before both empty states (found %d suspend calls)", n)
	}
	// An empty state also forgets the previous list: the container is a click target and a key handler,
	// and ArrowDown there would otherwise select a finding the filter (or the project) no longer shows.
	if n := strings.Count(render, "setFindingRows([]);"); n != 2 {
		t.Errorf("renderFindings must clear the row model in both empty states (found %d)", n)
	}
	for _, want := range []string{"No matching findings", "findingsEmptyHTML()", "ensureFindListWin(box).render()", "flushFindReveal()"} {
		if !strings.Contains(render, want) {
			t.Errorf("renderFindings lost %q", want)
		}
	}

	// The pinned header is one element drawn from the scroll offset, hidden from assistive tech
	// (the real headers stay in the tree), not a position:sticky inside the translated window.
	sticky := jsBlock(t, src, "function buildFindSticky(box) {")
	requireUIContains(t, sticky, "root.className = 'find-sticky'", "root.setAttribute('aria-hidden', 'true')", "box.insertBefore(root, box.firstChild)")
	requireUIContains(t, jsBlock(t, src, "function syncFindStickyHeader(win, scrollTop) {"), "stickyState({ groupRows: findRowModel.groupRows, scrollTop, rowH: h, stickyH: h })")
	// A filter can hide the open finding (phone Back): focus goes to the tab stop or the list, not a vanishing control.
	requireUIContains(t, jsBlock(t, src, "function focusFindingRow(id) {"),
		"(box?.querySelector(`.find-row[data-id=\"${id}\"]`) || box?.querySelector('.find-row[tabindex=\"0\"]') || box)?.focus({ preventScroll: true });")

	// Back from the phone detail view and deep links reach rows that may not be mounted.
	requireUIContains(t, src, "setTimeout(() => focusFindingRow(selFinding), 0)", "function flushFindReveal() {", "if (revealFinding(selFinding, 'center')) win.refresh();",
		// A reveal waits while a filter hides the open finding, and the wait is a Map lookup (it runs per scroll frame).
		"if (!findRevealPending || !findOrderIndex.has(selFinding)) return;")
}

func TestUIFindingsListStylesFitTheWindowedRenderer(t *testing.T) {
	css := readUIAsset(t, "findings.css")
	requireUIContains(t, css,
		// One declared height for every row; the Go row contract still pins min-height:50px beside it.
		"#findList{--find-row-h:50px}",
		// Rows and headers are fixed only while the list is virtualized; a short list keeps natural rows.
		"#findList.vlist .find-row{height:var(--find-row-h);overflow:hidden}",
		"#findList.vlist .find-group{position:relative;height:var(--find-row-h)}",
		// The pinned copy: a zero-height sticky wrapper keeps it out of the row flow.
		".find-sticky{position:sticky;top:0;z-index:2;height:0;overflow:visible}",
		"#findList.vlist .find-sticky .find-group{position:absolute;left:0;right:0;top:0;height:var(--find-row-h)}",
		// The container is the focus fallback when its row is not mounted, so it needs a visible ring.
		"#findList:focus-visible{outline:3px solid var(--focus-ring);outline-offset:-3px;box-shadow:none}",
		// A key-moved row sits flush against the scroller's edge; the shared outside ring would be clipped there.
		"#findList .find-row:focus-visible{outline-offset:-3px;box-shadow:none}",
	)
	// A short list keeps its natural sticky headers (the rule asserted by the headers contract).
	group := regexp.MustCompile(`\.find-group\{[^}]*\}`).FindString(css)
	requireUIContains(t, group, "position:sticky", "top:0")
	for _, line := range strings.Split(css, "\n") {
		if strings.Contains(line, "find-sticky") && regexp.MustCompile(`transition|animation`).MatchString(line) {
			t.Errorf("the pinned header must not animate: %s", line)
		}
	}
	// The window CSS is not allowed to hard-code a second row height anywhere else.
	if n := strings.Count(css, "--find-row-h:"); n != 1 {
		t.Errorf("--find-row-h must be declared once, found %d", n)
	}
}
