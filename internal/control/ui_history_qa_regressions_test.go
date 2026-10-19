package control

import (
	"strings"
	"testing"
)

// Contract checks for History/cross-cutting QA fixes that need a real browser to
// exercise; each asserts the source rule that carries the fix.
func TestUIHistoryQARegressionContracts(t *testing.T) {
	proxy := readUIAsset(t, "js/proxy.js")
	core := readUIAsset(t, "js/core.js")
	checks := []struct{ name, src, want string }{
		{"columns picker keeps focus on mousedown", proxy, "m.addEventListener('mousedown',e=>e.preventDefault())"},
		{"views label keeps its chevron", proxy, "function setViewsLabel(btn,txt)"},
		{"method filter lists common verbs", proxy, "methodChoices([...seenMethods],cur)"},
		{"ui-select trigger takes focus on mouse open", core, "trigger.focus({preventScroll:true})"},
		{"desktop menus stay under the title bar", core, "--titlebar-h"},
		{"hex view fits its pane", proxy, "mountHexDump(el,bytes)"},
		{"repeater placeholder button has no id undefined", readUIAsset(t, "js/repeater.js"), "if (id) b.id = id;"},
		{"copy-as menu value column is content sized", readUIAsset(t, "surfaces.css"), "fit-content(42%)"},
		{"desktop ctx rows follow row height", readUIAsset(t, "css/desktop/components.css"), "min-height:var(--row-h)}"},
		{"proxy toolbar tiers come from a ResizeObserver", readUIAsset(t, "js/desktop-panel-proxy.js"), "toolbarWidthTier("},
		{"narrow toolbar rules key off the panel attribute", readUIAsset(t, "css/desktop/panel-proxy.css"), "html.desktop-app #panel-proxy[data-tb-narrow]"},
	}
	for _, c := range checks {
		if !strings.Contains(c.src, c.want) {
			t.Errorf("%s: missing %q", c.name, c.want)
		}
	}
	// A size container on History's panel or toolbar makes desktop WebKit lay the
	// panel out while #rows is half rebuilt: the list snaps to the top after a panel
	// round trip and the rows under the pointer jump while live flows arrive.
	css := readUIAsset(t, "css/desktop/panel-proxy.css")
	for _, bad := range []string{"#panel-proxy{container:", ".proxy-toolbar{container:", "@container dsk-proxy-tb"} {
		if strings.Contains(css, bad) {
			t.Errorf("History must not use a size container around #rows: found %q", bad)
		}
	}
}

// hexDumpTestJS is formatHexDump (js/hexdump.js) plus a mountHexDump stand-in that
// paints the same <pre class="hex-dump"> the browser helper does, for the tests
// that evaluate view code outside a browser.
func hexDumpTestJS(t *testing.T) string {
	t.Helper()
	return "const HEX_LINE_16=16,HEX_LINE_8=8,HEX_LINE_4=4;\nconst sizeLabel=n=>n+' B';\n" +
		repeaterRenderJS(t, readUIAsset(t, "js/hexdump.js"), "export function formatHexDump(strOrBytes, maxBytes = 32768, perLine = HEX_LINE_16)") +
		"const mountHexDump=(host,raw)=>{host.innerHTML='<pre class=\"hex-dump\">'+esc(formatHexDump(raw))+'</pre>';};\n"
}

// Regression: "Scroll down to load older flows." sat in a floating bar under the
// list, over the half-visible last row. The paging hint (and its loading state)
// now ends the scrolled list itself, after the last loaded row; only errors and
// a refresh keep the persistent bar.
func TestUIHistoryPagingHintEndsTheListInsteadOfCoveringRows(t *testing.T) {
	proxy := readUIAsset(t, "js/proxy.js")
	for _, want := range []string{
		"const end=text=>{hideBar();box.dataset.listEnd=text;}",
		"end('Scroll down to load older flows.')",
		"delete box.dataset.listEnd",
	} {
		if !strings.Contains(proxy, want) {
			t.Errorf("proxy.js lacks %q", want)
		}
	}
	if strings.Contains(proxy, "show('Scroll down to load older flows.')") {
		t.Error("the paging hint must not use the floating banner")
	}
	if css := readUIAsset(t, "app.css"); !strings.Contains(css, "#rows[data-list-end]::after{content:attr(data-list-end)") {
		t.Error("app.css must render the list-end hint after the last row")
	}
}

// Regression: End only reached the last loaded row and queued one 300-row page,
// so the oldest of a few thousand flows took a dozen presses. End now pages to
// the true end in large pages (yielding between them, cancelled by any other
// move or a reload) and then lands on the oldest flow.
func TestUIHistoryEndReachesTheOldestFlow(t *testing.T) {
	proxy := readUIAsset(t, "js/proxy.js")
	for _, want := range []string{
		"export async function loadMoreFlows({pageSize=FLOW_FETCH}={})",
		"q.set('limit',String(pageSize+1))",
		"applyPageMore(d,flows.length,pageSize)",
		"async function loadToOldest()",
		"if(e.key==='End'&&flowHasMore)",
		"end(flowSweeping?'Loading to the oldest flow…'",
		// The row End reveals at the bottom must stay in view once the flow loads.
		"if(noteBar&&noteBar.style.display==='flex')noteBar.style.visibility='hidden';",
	} {
		if !strings.Contains(proxy, want) {
			t.Errorf("proxy.js lacks %q", want)
		}
	}
}

// Regression (Chromium): removing the focused row while the window moved let the
// focus change lay the list out with rows gone and the old pads, which clamped
// scrollTop ~48 rows short, so End's target row was unmounted again and focus
// fell to the list. Focus is parked on the list before any row is removed.
func TestUIHistoryReconcileParksFocusBeforeRemovingRows(t *testing.T) {
	proxy := readUIAsset(t, "js/proxy.js")
	park := strings.Index(proxy, "if(plan.remove.some(id=>byId.get(id)?.contains(document.activeElement)))box.focus({preventScroll:true});")
	remove := strings.Index(proxy, "for(const id of plan.remove){")
	if park < 0 || remove < 0 || park > remove {
		t.Fatal("reconcileVirtualRows must park focus on #rows before it removes rows")
	}
}
