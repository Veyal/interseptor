package control

import (
	"os/exec"
	"strings"
	"testing"
)

// Issue #104 — structured inspector sub-tabs (Headers grid + cookies, JSON tree +
// JSONPath, Query/Form params, GraphQL), inline image preview, ergonomic intercept
// hotkeys and the live-stream freeze. The parsers are unit-tested under node
// (ui/_js-tests/inspector-structure.test.mjs); these assert the wiring.

func TestUIInspectorStructuredSubTabsPresent(t *testing.T) {
	index := readUIAsset(t, "index.html")
	// Request side gets every structured view; response side gets Headers + JSON.
	for _, v := range []string{"headers", "json", "params", "gql"} {
		if !strings.Contains(index, `data-view="`+v+`"`) {
			t.Errorf("request inspector segmented control missing data-view=%q", v)
		}
	}
	reqSeg := index[strings.Index(index, `data-side="req"`):]
	reqSeg = reqSeg[:strings.Index(reqSeg, "</div>")]
	for _, v := range []string{"headers", "json", "params", "gql"} {
		if !strings.Contains(reqSeg, `data-view="`+v+`"`) {
			t.Errorf("request seg missing %q", v)
		}
	}
	proxy := executableJS(readUIAsset(t, "js/proxy.js"))
	for _, fn := range []string{"function inspHeadersHTML(", "function inspJsonHTML(", "function inspParamsHTML(", "function inspGraphQLHTML(", "function inspImageHTML(", "const STRUCT_VIEWS="} {
		if !strings.Contains(proxy, fn) {
			t.Errorf("proxy.js missing structured view builder %q", fn)
		}
	}
	// Inline image preview replaces the "binary, not rendered" note for images.
	if !strings.Contains(proxy, "inspImageHTML(el,flowId,side,mime,len)") {
		t.Error("image bodies must render an inline preview, not just the binary note")
	}
	// The pure parser model is DOM-free and exports the parsers.
	model := readUIAsset(t, "js/inspector-structure.js")
	requireUIContains(t, model,
		"export function flattenJson(", "export function detectGraphQL(",
		"export function parseFormEncoded(", "export function parseCookies(",
		"export function headerRows(", "export function splitHeadBody(",
		"export function inspectorViewAvailability(", "export function isJsonInspectorBody(",
		"export function isDecodedAvailable(", "export function needsBackgroundBodyProbe(",
		"export function fallbackInspectorView(",
	)
}

func TestUIInspectorEmptyViewButtonsHidden(t *testing.T) {
	proxy := executableJS(readUIAsset(t, "js/proxy.js"))
	requireUIContains(t, proxy,
		"inspectorViewAvailability(",
		"isDecodedAvailable(",
		"needsBackgroundBodyProbe(",
		"fallbackInspectorView(",
		"applyInspectorViewButtons(",
		"hideInspectorSeg(",
		"registerSseHandler('codecs.update'",
	)
	// Hide both the button and, on non-HTTP response states, the segmented strip itself.
	requireUIContains(t, proxy, "b.hidden=!avail.visible[b.dataset.view]", "seg.hidden=!avail.anyVisible")
}

func TestUIInspectorHexHiddenByDefault(t *testing.T) {
	index := readUIAsset(t, "index.html")
	for _, spec := range []struct {
		marker string
		view   string
	}{
		{`data-side="req"`, "hex"},
		{`data-side="req"`, "json"},
		{`data-side="req"`, "params"},
		{`data-side="res"`, "hex"},
		{`data-side="res"`, "json"},
		{`id="repResSeg"`, "hex"},
	} {
		idx := strings.Index(index, spec.marker)
		if idx < 0 {
			t.Fatalf("index.html missing %s", spec.marker)
		}
		seg := index[idx:]
		seg = seg[:strings.Index(seg, "</div>")]
		viewAttr := `data-view="` + spec.view + `"`
		viewIdx := strings.Index(seg, viewAttr)
		if viewIdx < 0 {
			t.Fatalf("%s missing %s button", spec.marker, viewAttr)
		}
		endBtn := strings.Index(seg[viewIdx:], ">")
		if endBtn < 0 || !strings.Contains(seg[viewIdx:viewIdx+endBtn], " hidden") {
			t.Errorf("%s %s button must be hidden by default in index.html", spec.marker, viewAttr)
		}
	}
	proxy := readUIAsset(t, "js/proxy.js")
	requireUIContains(t, proxy,
		"hexEnabled:readHexViewPref()",
		"jsonEnabled:readJsonViewPref()",
		"paramsEnabled:readParamsViewPref()",
		"const hexEnabled=readHexViewPref()",
		"window.addEventListener('hexviewchange',refreshSelectedInspectorSides)",
		"window.addEventListener('inspectorviewchange',refreshSelectedInspectorSides)",
	)
	toolsRaw := readUIAsset(t, "js/tools.js")
	tools := executableJS(toolsRaw)
	requireUIContains(t, tools,
		"import { readHexViewPref } from './settings-model.js';",
		"if(t.resView==='hex'&&!hexAllowed)t.resView='pretty';",
		"if(button.dataset.view==='hex'){button.hidden=!hexAllowed;}",
		"window.addEventListener('hexviewchange',",
	)
	script := `let hexSetting=false; const readHexViewPref=()=>hexSetting;
const buttons=['raw','pretty','decoded','hex','render'].map(v=>({dataset:{view:v},hidden:v==='hex'||v==='render',disabled:false,attrs:{},on:v==='pretty',classList:{toggle(c,on){if(c==='on')this._on=on;}},setAttribute(k,val){this.attrs[k]=val;}}));
const $=()=>({querySelectorAll:()=>buttons});
` + repeaterRenderJS(t, toolsRaw, "function repSyncResView(t)") + `
const tab={resId:1,resMime:'application/json',resView:'hex'};
repSyncResView(tab);
if(tab.resView!=='pretty')throw Error('expected hex to fall back to pretty when disabled, got '+tab.resView);
if(!buttons.find(b=>b.dataset.view==='hex').hidden)throw Error('hex button must stay hidden when disabled');
hexSetting=true;
tab.resView='hex';
repSyncResView(tab);
if(tab.resView!=='hex')throw Error('expected hex to stay active when enabled, got '+tab.resView);
if(buttons.find(b=>b.dataset.view==='hex').hidden)throw Error('hex button must be visible when enabled');
`
	if out, err := exec.Command("node", "--input-type=module", "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("Repeater hex view toggle behavior failed: %v\n%s", err, out)
	}
}

func TestUIInterceptModifierHotkeys(t *testing.T) {
	app := executableJS(readUIAsset(t, "js/app.js"))
	// Forward/Drop modifier hotkeys are wired before the typing guard so they work
	// with the caret inside the #heldRaw editor.
	forward := strings.Index(app, "isModShortcut(e,'Enter')){e.preventDefault();$('#forwardBtn')")
	drop := strings.Index(app, "isModShiftShortcut(e,'d')){e.preventDefault();$('#dropBtn')")
	typing := strings.Index(app, "if(typing)return;")
	if forward < 0 || drop < 0 || typing < 0 {
		t.Fatal("intercept Forward/Drop modifier hotkeys or the typing guard are missing")
	}
	if forward > typing || drop > typing {
		t.Error("intercept modifier hotkeys must be bound before the typing guard so they work inside #heldRaw")
	}
	requireUIContains(t, app, "function isModShiftShortcut(e,key)")
}

func TestUILiveStreamFreeze(t *testing.T) {
	requireUIContains(t, readUIAsset(t, "index.html"), `id="freezeBtn"`, `id="freezeCount"`)
	proxy := executableJS(readUIAsset(t, "js/proxy.js"))
	requireUIContains(t, proxy,
		"export function toggleLiveFreeze()",
		"export function setLiveFrozen(on)",
		"if(liveFrozen){frozenNew++;updateFreezeBadge();return;}",
	)
	// Freeze must hold the viewport only — capture still upserts the flow first.
	idxUpsert := strings.Index(proxy, "upsertFlow(f);")
	idxFreeze := strings.Index(proxy, "if(liveFrozen){frozenNew++")
	if idxUpsert < 0 || idxFreeze < 0 || idxUpsert > idxFreeze {
		t.Error("handleFlowNew must upsert (capture) the flow before the freeze check holds the viewport")
	}
	app := executableJS(readUIAsset(t, "js/app.js"))
	requireUIContains(t, app, "toggleLiveFreeze()")
	if !strings.Contains(app, "(e.ctrlKey||e.metaKey)&&e.altKey&&!e.shiftKey&&(e.code==='KeyP'||e.key.toLowerCase()==='p')") {
		t.Error("Ctrl/Cmd+Alt+P must toggle the live-stream freeze in Proxy (the sheet lists Ctrl/⌘)")
	}
}
