package control

import (
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

// WP6 (UI overhaul Proxy panel): single-tier toolbar with a Filters popover,
// id-keyed selection and bulk bar, paperclip column, guided empty states,
// inspector docking, phone cards. The UI has no browser in this session, so
// DOM behaviour is pinned statically and pure logic runs under node.

func proxyRegion(t *testing.T) string {
	t.Helper()
	index := readUIAsset(t, "index.html")
	start := strings.Index(index, "<!-- region:proxy -->")
	end := strings.Index(index, "<!-- /region:proxy -->")
	if start < 0 || end < start {
		t.Fatal("region:proxy markers missing")
	}
	return index[start:end]
}

func TestUIProxyPureLogicUnderNode(t *testing.T) {
	node := requireNode(t)
	cmd := exec.Command(node, "--test", "_js-tests/proxy-selection.test.mjs", "_js-tests/proxy-filters.test.mjs")
	cmd.Dir = "ui"
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("node --test failed: %v\n%s", err, out)
	}
}

func TestUIProxyPureModulesStayDOMFree(t *testing.T) {
	for _, name := range []string{"js/proxy-selection.js", "js/proxy-filters.js"} {
		src := executableJS(readUIAsset(t, name))
		if regexp.MustCompile(`(?m)^import\s`).MatchString(src) {
			t.Errorf("%s must not import anything so it stays loadable under node", name)
		}
		if regexp.MustCompile(`\bdocument\b|\bwindow\b|\.innerHTML`).MatchString(src) {
			t.Errorf("%s must stay DOM-free", name)
		}
	}
}

func TestUIProxyKeepsEveryLegacyID(t *testing.T) {
	region := proxyRegion(t)
	for _, id := range []string{
		"fSearchScope", "fSearch", "fMethod", "fStatus", "viewsBtn", "colPickerBtn", "colPicker",
		"notesFilter", "hideTlsFilter", "scopeToggle", "manualFilter", "aiFilter",
		"flowSearchScripts", "flowSearchScriptList", "flowSearchScriptName", "flowSearchScriptTest", "flowSearchScriptSave",
		"flowSearchScriptDelete", "flowSearchScriptStatus", "flowSearchScriptEditor", "flowSearchScriptError",
		"chips", "tagBar", "tlsDiagBanner", "selBar", "selCount", "selCompare", "selScope", "selAddFinding", "selDelete", "selClear",
		"flowHead", "rows", "flowCapBanner", "flowCapMessage", "flowCapRetry", "inspectSplitter", "inspect",
		"inspectSendRepeater", "inspectAddFinding", "inspectSendIntruder", "inspectMoreActions",
		"reqDecode", "reqView", "resStatus", "inspectFind", "inspectFindIn", "inspectFindStat", "inspectFindClose",
		"resDecode", "resView", "noteBar", "noteInput", "noteSaved", "noteRetry",
	} {
		if !strings.Contains(region, `id="`+id+`"`) {
			t.Errorf("proxy region lost id %q", id)
		}
	}
}

func TestUIProxyToolbarIsOneTierPlusFiltersPopover(t *testing.T) {
	region := proxyRegion(t)
	toolbar := regexp.MustCompile(`(?s)<div class="toolbar proxy-toolbar">.*?<div id="proxyFilters"`).FindString(region)
	if toolbar == "" {
		t.Fatal("proxy toolbar must precede the Filters popover and carry the proxy-toolbar class")
	}
	for _, want := range []string{`id="fSearch"`, `id="fMethod"`, `id="fStatus"`, `id="viewsBtn"`, `id="colPickerBtn"`, `id="filtersBtn"`, `id="scopeToggle"`, "In scope only"} {
		if !strings.Contains(toolbar, want) {
			t.Errorf("single toolbar tier is missing %s", want)
		}
	}
	for _, moved := range []string{`id="notesFilter"`, `id="hideTlsFilter"`, `id="manualFilter"`, `id="aiFilter"`, `id="flowSearchScripts"`, `id="tagBar"`} {
		if strings.Contains(toolbar, moved) {
			t.Errorf("%s belongs in the Filters popover, not the toolbar", moved)
		}
	}
	btn := regexp.MustCompile(`<button[^>]*id="filtersBtn"[^>]*>`).FindString(region)
	requireUIContains(t, btn, `aria-expanded="false"`, `aria-controls="proxyFilters"`, `aria-haspopup="true"`)
	pop := regexp.MustCompile(`<div id="proxyFilters"[^>]*>`).FindString(region)
	requireUIContains(t, pop, `role="group"`, `aria-label="Filters"`, "hidden")
	popBody := region[strings.Index(region, `<div id="proxyFilters"`):strings.Index(region, `id="selBar"`)]
	for _, want := range []string{`id="notesFilter"`, `id="hideTlsFilter"`, `id="manualFilter"`, `id="aiFilter"`, `id="flowSearchScripts"`, `id="tagBar"`} {
		if !strings.Contains(popBody, want) {
			t.Errorf("Filters popover is missing %s", want)
		}
	}
	// The active-filter chip line only exists while filters are active.
	requireUIContains(t, region, `id="activeFilterBar"`)
	if !regexp.MustCompile(`(?s)id="activeFilterBar"[^>]*hidden[^>]*>.*?id="chips"`).MatchString(region) {
		t.Error("#chips must live in the initially hidden #activeFilterBar")
	}
	// app.css still styles the second tier at the narrow breakpoint by this selector.
	if !regexp.MustCompile(`class="toolbar toolbar-secondary proxy-filter-bar"[^>]*id="activeFilterBar"|id="activeFilterBar"[^>]*class="toolbar toolbar-secondary proxy-filter-bar"`).MatchString(region) {
		t.Error("the chip line keeps the toolbar-secondary proxy-filter-bar classes")
	}
}

func TestUIProxyBulkBarCarriesTheSpecVerbs(t *testing.T) {
	region := proxyRegion(t)
	bar := region[strings.Index(region, `id="selBar"`):]
	bar = bar[:strings.Index(bar, `id="flowHead"`)]
	requireUIContains(t, bar, `role="toolbar"`, `aria-label="Bulk actions`,
		`id="selSendTo"`, `id="selTag"`, `id="selAddFinding"`, `id="selCopyAs"`, `id="selCompare"`, `id="selDelete"`, `id="selClear"`, `id="selProgress"`)
	requireUIContains(t, bar, `aria-haspopup="menu"`, "Add to finding")
	if !regexp.MustCompile(`id="selProgress"[^>]*aria-live="polite"`).MatchString(bar) && !regexp.MustCompile(`aria-live="polite"[^>]*id="selProgress"`).MatchString(bar) {
		t.Error("bulk progress must be announced through a polite live region")
	}
	src := executableJS(readUIAsset(t, "js/proxy.js"))
	requireUIContains(t, src, "chunkIds(", "bulkProgressText(", "BULK_CHUNK", "bulkVerbs(", "uiConfirm(")
	if strings.Contains(src, "_delArm=true") {
		t.Error("bulk delete must use uiConfirm instead of the two-click arm pattern")
	}
	requireUIContains(t, src, "if(btn.disabled)return;", "btn.setAttribute('aria-busy','true')", "Deleting…", "toastError('Delete failed',e)")
}

func TestUIProxyInspectorActionsAndDock(t *testing.T) {
	region := proxyRegion(t)
	requireUIContains(t, region, `id="inspectCopyAs"`, `id="inspectDock"`, "Attach as evidence")
	if !regexp.MustCompile(`<button[^>]*id="inspectDock"[^>]*aria-pressed="`).MatchString(region) {
		t.Error("the dock toggle must expose aria-pressed")
	}
	src := executableJS(readUIAsset(t, "js/proxy.js"))
	requireUIContains(t, src, "parseDockPref(", "resolveDock(", "proxy.dock", "openFlowDrawer(", "dock-drawer", "getHook('attachEvidence')")
	css := readUIAsset(t, "panel-proxy.css")
	requireUIContains(t, css, ".dock-drawer", "#inspectSplitter")
}

func TestUIProxySelectionIsIDKeyedAndFilteredAware(t *testing.T) {
	src := executableJS(readUIAsset(t, "js/proxy.js"))
	requireUIContains(t, src, "from './proxy-selection.js'", "applyRowClick(", "toggleAllIds(", "state.selAnchorId")
	for _, fn := range []string{"export function flowRowClick(", "export function walkFlowNav(", "export function toggleSelectAllShown("} {
		if !strings.Contains(src, fn) {
			t.Errorf("proxy.js must keep exporting %s", fn)
		}
	}
	if regexp.MustCompile(`for\(let i=a;i<=b;i\+\+\)state\.selected\.add`).MatchString(src) {
		t.Error("range selection must go through rangeBetween via applyRowClick, not inline index loops")
	}
	requireUIContains(t, src, "aria-pressed", "msel")
}

func TestUIProxyKeepsLegacyHandlers(t *testing.T) {
	// Raw source: executableJS would strip from the "codecs/*.star" hint to the next block comment.
	src := readUIAsset(t, "js/proxy.js")
	requireUIContains(t, src,
		"(e.ctrlKey||e.metaKey)&&e.key.toLowerCase()==='f'", "e.key==='Escape'", "export function openInspectFind(",
		"e.key==='ContextMenu'", "(e.shiftKey&&e.key==='F10')", "function selectFlow(", "function loadFlows(",
		"export function handleFlowNew(", "export function handleFlowUpdate(", "export function renderRows(",
	)
	app := executableJS(readUIAsset(t, "js/app.js"))
	requireUIContains(t, app, "toggleScopeFilter:()=>$('#scopeToggle')?.click()")
}

func TestUIProxyReusesSharedFlowHelpers(t *testing.T) {
	src := executableJS(readUIAsset(t, "js/proxy.js"))
	requireUIContains(t, src, "from './flowbody.js'", "wsOpcodeName", "flowUrl")
	if strings.Contains(src, "function wsOpcode(") {
		t.Error("the websocket opcode table now lives in flowbody.js")
	}
}

func TestUIProxyEmptyStatesUseStatePanelAndChecklist(t *testing.T) {
	src := executableJS(readUIAsset(t, "js/proxy.js"))
	requireUIContains(t, src, "from './statepanel.js'", "renderState(", "emptyStateModel(", "'empty-first'", "'empty-filtered'",
		"getHook('mountChecklist')", "proxyChecklistMount", "filterCount")
	if strings.Contains(src, "No flows match the current filters.") {
		t.Error("the filtered empty state must come from emptyStateModel and name the active filter count")
	}
	if !strings.Contains(src, "gsSettings") || !strings.Contains(src, "gsTLS") {
		t.Error("the Connection settings and HTTPS setup actions must stay reachable from the first-run state")
	}
}

func TestUIProxyPaperclipColumnReflectsLinkedFindings(t *testing.T) {
	src := executableJS(readUIAsset(t, "js/proxy.js"))
	requireUIContains(t, src, "{key:'attached'", "attachedLabel(", "flowFindings(", "icon('paperclip')", "tr-att")
	css := readUIAsset(t, "panel-proxy.css")
	requireUIContains(t, css, ".tr-att")
	if !strings.Contains(src, "visually") && !strings.Contains(src, "u-sr") {
		t.Error("the paperclip cell must carry screen-reader text, not an icon alone")
	}
}

func TestUIProxyCopyAsAndDiffAreWired(t *testing.T) {
	src := executableJS(readUIAsset(t, "js/proxy.js"))
	requireUIContains(t, src, "from './copyas.js'", "COPY_AS_KINDS", "copyAs(", "inspectCopyAs", "selCopyAs", "keys:'d'", "openCompare(")
	requireUIContains(t, src, "'proxy-list'")
}

func TestUIProxyHistoryReMeasuresOnDensityChange(t *testing.T) {
	src := executableJS(readUIAsset(t, "js/proxy.js"))
	requireUIContains(t, src, "densitychange", "refreshRowHeight")
}

func TestUIProxyGlyphsAreSpriteIcons(t *testing.T) {
	region := proxyRegion(t)
	src := readUIAsset(t, "js/proxy.js")
	for _, glyph := range []string{"⇄", "◎", "◉", "⧉", "▦", "＋", "◧", "✕"} {
		if strings.Contains(region, glyph) {
			t.Errorf("proxy region still uses the Unicode glyph %q as an icon", glyph)
		}
	}
	for _, glyph := range []string{"◎ in scope", "◉", "⇄"} {
		if strings.Contains(src, glyph) {
			t.Errorf("proxy.js still writes the Unicode glyph %q as an icon", glyph)
		}
	}
	if regexp.MustCompile(`\sstyle=`).MatchString(region) {
		t.Error("proxy region must not use inline style attributes")
	}
}

func TestUIProxyNewCodeWritesNoInlineStyles(t *testing.T) {
	for _, name := range []string{"js/proxy-selection.js", "js/proxy-filters.js"} {
		src := executableJS(readUIAsset(t, name))
		if strings.Contains(src, `style="`) || strings.Contains(src, "cssText") || strings.Contains(src, ".style.") {
			t.Errorf("%s writes inline styles; use classes", name)
		}
	}
}

func TestUIProxyPhoneCardsLongPressAndTouchTargets(t *testing.T) {
	css := readUIAsset(t, "panel-proxy.css")
	requireUIContains(t, css, "@media (max-width:720px)", "@media (pointer:coarse)", "grid-template-areas", "var(--hit-min)", "var(--z-popover)", "var(--safe-b)")
	if regexp.MustCompile(`min-width:\s*[4-9][0-9]{2}px|min-width:\s*[0-9]{4}px`).MatchString(css) {
		t.Error("nothing may set a min-width wider than 375px")
	}
	if regexp.MustCompile(`font-size:\s*[0-9.]+px`).MatchString(css) {
		t.Error("panel-proxy.css must use the --fs-* scale, not px font sizes")
	}
	src := executableJS(readUIAsset(t, "js/proxy.js"))
	requireUIContains(t, src, "createLongPress(", "from './proxy-selection.js'", "rowOverflow")
}

func TestUIProxyStyleStaysCompositorOnlyAndFocusVisible(t *testing.T) {
	css := readUIAsset(t, "panel-proxy.css")
	requireUIContains(t, css, ":focus-visible", "var(--focus-ring)")
}

func TestUIProxyModuleImportsResolve(t *testing.T) {
	src := readUIAsset(t, "js/proxy.js")
	for _, m := range regexp.MustCompile(`from '\./([a-z-]+\.js)'`).FindAllStringSubmatch(src, -1) {
		readUIAsset(t, "js/"+m[1])
	}
}
