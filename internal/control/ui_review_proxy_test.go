package control

import (
	"strings"
	"testing"
)

// Review fixes (WP6): the Proxy Diff verb and the `d` key open the shared
// DiffView, and the inspector and Flow Drawer use the shared Finder.

func TestUIProxyDiffVerbAndKeyOpenSharedDiffView(t *testing.T) {
	proxy := executableJS(readUIAsset(t, "js/proxy.js"))
	requireUIContains(t, proxy,
		"import { openFlowDiff } from './flow-diff.js'",
		"$('#selCompare').onclick=()=>openFlowDiff()",
		"id:'proxy.diff',keys:'d',scope:'proxy-list'",
	)
	if strings.Contains(proxy, "$('#selCompare').onclick=()=>openCompare()") {
		t.Error("the bulk Diff verb must open the DiffView, not the legacy compare modal")
	}
	diff := executableJS(readUIAsset(t, "js/flow-diff.js"))
	requireUIContains(t, diff,
		"import { buildDiffPanel } from './repeater.js'",
		"selectedPair(state.selected)",
		"select exactly two flows to diff",
		"openModal(modal,",
	)
	repeater := executableJS(readUIAsset(t, "js/repeater.js"))
	requireUIContains(t, repeater, "if (e.key === 'n') { e.preventDefault(); view.next(); } else if (e.key === 'N') { e.preventDefault(); view.prev(); }")
	requireUIContains(t, executableJS(readUIAsset(t, "js/core.js")), "'flowDiffModal'")
	// The legacy modal stays reachable from the palette.
	app := executableJS(readUIAsset(t, "js/app.js"))
	requireUIContains(t, app, "Compare selected flows (legacy word diff)", "run:()=>openCompare()", "Diff selected flows", "run:()=>openFlowDiff()")
	requireUIContains(t, executableJS(readUIAsset(t, "js/cmdk-logic.js")), "how: { cmd: 'Compare selected flows (legacy word diff)' }")
}

func TestUIInspectorAndDrawerUseSharedFinder(t *testing.T) {
	// Raw source: executableJS would treat the '/' key literal as a comment start.
	proxy := readUIAsset(t, "js/proxy.js")
	requireUIContains(t, proxy,
		"import { createFinder } from './finder.js'",
		"refreshInspectFinder(side)",
		"export function openInspectFind(",
		"keys:'/',scope:'proxy-inspector'",
	)
	for _, gone := range []string{"markFindInHtml", "inspectFindIn", "inspectFindBar"} {
		if strings.Contains(proxy, gone) {
			t.Errorf("proxy.js still references the retired inspector find bar %q", gone)
		}
	}
	if strings.Contains(readUIAsset(t, "index.html"), `id="inspectFind"`) {
		t.Error("the old inspector find bar markup must be gone")
	}
	drawer := executableJS(readUIAsset(t, "js/flowdrawer.js"))
	requireUIContains(t, drawer,
		"import { createFinder } from './finder.js'",
		"createFinder($('#fdPanel'), { root: $('#fdBody')",
		"if (finder.isOpen()) finder.refresh();",
		"finder.close();",
		"e.key === '/'",
	)
}

func TestUIProxyEditScopeAndCoachTip(t *testing.T) {
	index := readUIAsset(t, "index.html")
	requireUIContains(t, index, `id="scopeEdit"`)
	proxy := readUIAsset(t, "js/proxy.js")
	requireUIContains(t, proxy,
		"getShellApi().openSettings",
		"open('scope')",
		"interseptor.coach.scopeSwitch",
		"tip.id='scopeCoach'",
		"The scope switch moved to the Scope chip in the strip above.",
	)
	// storage can throw: the tip must render and dismiss without it
	if n := strings.Count(proxy[strings.Index(proxy, "interseptor.coach.scopeSwitch"):], "try{"); n < 2 {
		t.Errorf("coach tip storage access must be guarded, found %d try blocks", n)
	}
}

func TestUITerminologyIsConsistent(t *testing.T) {
	index := readUIAsset(t, "index.html")
	for _, gone := range []string{"Target scope</", "Attach as PoC", "attach captured flows from History", `<span class="ctx-key">Evid</span>`} {
		if strings.Contains(index, gone) {
			t.Errorf("index.html still uses the old term %q", gone)
		}
	}
	requireUIContains(t, index, `<button data-sec="scope">Scope</button>`, "attach captured flows from Proxy.", `id="ffpAttach" disabled>Attach as evidence</button>`)
	requireUIContains(t, readUIAsset(t, "js/project-state.js"), "'Attach evidence to F-'")
}
