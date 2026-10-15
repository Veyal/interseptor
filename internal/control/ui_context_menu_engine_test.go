package control

import (
	"regexp"
	"strings"
	"testing"
)

// The cascading engine lives in core.js and its rules live in ctxmenu-model.js,
// which node tests exercise directly. These tests pin the shipped wiring: that
// the engine renders children, that the native menu has nowhere left to leak,
// and that each surface actually asks for a menu.

func TestUIContextMenuEngineRendersCascadingSubmenus(t *testing.T) {
	core := requireUIContracts(t, "js/core.js",
		// Children become a flyout, never another root-level row.
		"function ctxOpenSubmenu(depth,idx",
		"const sections=childSections(it);",
		"el.className='ctx-submenu';",
		`el.setAttribute('role','menu');`,
		// The chevron is the sprite icon, rotated in CSS — never a Unicode glyph.
		`<span class="ctx-arrow" aria-hidden="true">${icon('chevron')}</span>`,
		`aria-haspopup="menu" aria-expanded="false"`,
		`row.setAttribute('aria-expanded','true')`,
		// Placement, keyboard and the 120 ms rest all come from the model module.
		"submenuPlacement({parent:parent.el.getBoundingClientRect()",
		"el.dataset.ctxSide=place.side;",
		"},SUBMENU_HOVER_MS);",
		"menuKeyAction(e.key,{children:hasChildren(it)",
	)
	if !strings.Contains(core, "from './ctxmenu-model.js'") {
		t.Fatal("core.js must take the menu rules from ctxmenu-model.js, not inline them")
	}
	// The engine is registered once, at module load, so ArrowLeft/Escape inside a
	// flyout is handled before a feature module's own Escape listener sees it.
	if !strings.Contains(core, "document.addEventListener('keydown',ctxKeyHandler);") {
		t.Fatal("the menu key handler must be registered at module load, not per open")
	}
	if !strings.Contains(core, "e.preventDefault();e.stopImmediatePropagation();") {
		t.Fatal("an owned key must not also reach the feature modules' Escape handlers")
	}
	for _, action := range []string{"case 'open':ctxOpenSubmenu(depth,p.sel,{focus:true});break;", "case 'close':", "case 'activate':ctxRunRow(depth,p.sel);break;"} {
		if !strings.Contains(core, action) {
			t.Fatalf("keyboard action missing: %s", action)
		}
	}
	// A disabled row is announced and never runs.
	if !strings.Contains(core, `cls.push('ctx-disabled')`) || !strings.Contains(core, `' aria-disabled="true"'`) {
		t.Fatal("a disabled menu row must carry aria-disabled and its own class")
	}
	if !strings.Contains(core, "if(!it||it.disabled)return;") {
		t.Fatal("a disabled row must not run its action")
	}
}

func TestUIContextMenuSubmenuStylesExist(t *testing.T) {
	css := readUIAsset(t, "surfaces.css")
	for _, want := range []string{
		".ctx-submenu {",
		".ctx-submenu.show { display: block; }",
		".ctx-arrow .icon { transform: rotate(-90deg); }",
		".ctx-item.ctx-open",
		".ctx-item.ctx-disabled",
		".ctx-item:focus-visible",
	} {
		if !strings.Contains(css, want) {
			t.Errorf("surfaces.css missing %q", want)
		}
	}
	// The flyout shares the one floating-surface frame.
	if !regexp.MustCompile(`#ctxmenu,\n\.ctx-submenu,`).MatchString(css) {
		t.Error("the flyout must use the shared --surface-frame rule, not its own border")
	}
	// A flyout is its own fixed panel; nesting it inside the scrolling root clips it.
	if !regexp.MustCompile(`\.ctx-submenu \{[^}]*position: fixed`).MatchString(css) {
		t.Error(".ctx-submenu must be position:fixed")
	}
	// Reduced motion is handled once, globally.
	if !strings.Contains(readUIAsset(t, "app.css"), "@media (prefers-reduced-motion:reduce){") {
		t.Error("the global reduced-motion block must still switch the flyout animation off")
	}
}

func TestUINativeContextMenuHasNowhereLeftToLeak(t *testing.T) {
	core := readUIAsset(t, "js/core.js")
	proxy := readUIAsset(t, "js/proxy.js")
	// The old escape hatch returned early for text fields and selections, which
	// is exactly where the browser menu used to appear.
	for _, gone := range []string{
		"if(tag==='input'||tag==='textarea'||t.isContentEditable)return;",
		"if(sel&&String(sel).length&&!sel.isCollapsed)return;",
	} {
		if strings.Contains(proxy, gone) {
			t.Fatalf("proxy.js still lets the native menu through: %s", gone)
		}
	}
	requireUIContracts(t, "js/core.js",
		"document.addEventListener('contextmenu',e=>{",
		// A feature handler that drew its own menu wins.
		"if(e.defaultPrevented)return;",
		"if(field&&isTextField(field)){e.preventDefault();openTextFieldCtxMenu(e,field);return;}",
		"if(sel){e.preventDefault();openSelectionCtxMenu(e,sel);return;}",
		"openWorkspaceCtxMenu(e);",
	)
	// The fallback is never empty, so no view is a dead right-click.
	if !strings.Contains(core, "export function registerWorkspaceCtxSection(build)") {
		t.Fatal("core.js must let a feature module contribute workspace-menu roots")
	}
	if !strings.Contains(proxy, "registerWorkspaceCtxSection(()=>({head:'WORKSPACE'") {
		t.Fatal("proxy.js must register the workspace fallback roots")
	}
	for _, want := range []string{"Command palette", "New Repeater tab", "Open Decoder", "Keyboard shortcuts"} {
		if !strings.Contains(proxy, want) {
			t.Errorf("workspace menu missing %q", want)
		}
	}
}

func TestUITextFieldClipboardBridgeIsWired(t *testing.T) {
	core := requireUIContracts(t, "js/core.js",
		"export function openTextFieldCtxMenu(e,el,opts={})",
		"export function isTextField(el)",
		"navigator.clipboard.writeText(text)",
		"navigator.clipboard.readText()",
		// Transforms reuse the existing decode endpoint rather than a new one.
		"async function runCodecOp(el,op,label)",
		"api('/api/decode',{method:'POST'",
		"function insertSnippet(el,id)",
		// An edit made by the menu notifies the owning module the way typing does.
		"el.dispatchEvent(new Event('input',{bubbles:true}));",
	)
	for _, action := range []string{"'cut'", "'copy'", "'paste'", "'select-all'", "'clear'", "'decoder'"} {
		if !strings.Contains(core, "if(action==="+action+")") {
			t.Errorf("clipboard bridge is missing the %s action", action)
		}
	}
	// A blocked clipboard says so instead of failing silently.
	for _, msg := range []string{"Clipboard write was blocked by the browser", "Clipboard read was blocked"} {
		if !strings.Contains(core, msg) {
			t.Errorf("missing clipboard failure message %q", msg)
		}
	}
	// The UUID comes from crypto, never Math.random.
	if strings.Contains(core, "Math.random()") && strings.Contains(core, "function randomUUID()") {
		idx := strings.Index(core, "function randomUUID()")
		if strings.Contains(core[idx:idx+600], "Math.random()") {
			t.Error("randomUUID must use crypto.getRandomValues, not Math.random")
		}
	}
}

func TestUIEverySurfaceHasACategorisedMenu(t *testing.T) {
	cases := map[string][]string{
		// History rows: the roots of the categorised tree, not a flat forty.
		"js/proxy.js": {
			"function flowSendRoot(f)", "function flowCopyRoot(f,side='both')", "function flowEvidenceRoot(f)",
			"function flowFilterRoot(f)", "function flowTagRoot(f)", "function flowAnalyseRoot(f)",
			"function flowDeleteRoot(f)", "function flowColumnRoot(f,field)",
			"function showWsFrameCtx(e,frame)",
		},
		// Both lists are windowed, so a row carries no handler: one delegated contextmenu listener on the
		// container opens the menu (ui_virtual_lists_test.go pins the rest of the delegation).
		"js/findings.js": {"function showFindingRowCtx(event, id)", "showFindingRowCtx(event, Number(row.dataset.id))"},
		"js/scanner.js":  {"function showScanIssueCtx(e,g)", "showScanIssueCtx(e,(scanState.groups||[])[i])"},
		"js/intruder.js": {"head: 'ATTEMPT · flow #'", "label: 'Copy attempt as'"},
		"js/repeater.js": {"resView.addEventListener('contextmenu'", "head: 'RESPONSE'"},
		"js/tools.js":    {"function repShowEncodeCtx(e,el)", "openTextFieldCtxMenu(e,el,{payloadMarker:true", "head:'TAB · '"},
	}
	for asset, contracts := range cases {
		requireUIContracts(t, asset, contracts...)
	}
	// Every flow root is actually used by the two flow menus.
	proxy := readUIAsset(t, "js/proxy.js")
	for _, root := range []string{"flowSendRoot(f)", "flowCopyRoot(f,", "flowEvidenceRoot(f)", "flowFilterRoot(f)", "flowAnalyseRoot(f)"} {
		if strings.Count(proxy, root) < 2 {
			t.Errorf("%s is defined but used by only one menu", root)
		}
	}
	// The history menu is the one that used to be 38 flat items; it must be a
	// handful of roots now. Count the top-level entries of showCtx's item list.
	start := strings.Index(proxy, "export function showCtx(x,y,f,field){")
	if start < 0 {
		t.Fatal("showCtx is gone")
	}
	body := proxy[start : start+900]
	roots := strings.Count(body, "Root(f")
	if roots < 5 || roots > 9 {
		t.Errorf("the history menu should be 5-9 categorised roots (the 7-item rule); found %d", roots)
	}
	// The inspector pane menu groups its three text copies and the image behind
	// one root, so it stays inside the rule too.
	if !strings.Contains(proxy, "{label:'Copy this '+pane,icon:'copy',children:[") {
		t.Error("the inspector pane copies must sit behind one categorised root")
	}
}

func TestUIFlowPngCopyIsMountedWhereProofIsPasted(t *testing.T) {
	html := readUIAsset(t, "index.html")
	core := requireUIContracts(t, "js/core.js",
		"export function flowPngMenuItems(id)",
		"export function copyFlowPng(id, opts)",
		"export function copyPreviewPng(src, name)",
	)
	for _, want := range []string{"'Flow as PNG · both stacked'", "'Request as PNG'", "'Response as PNG'", "'Flow as PNG · side by side'"} {
		if !strings.Contains(core, want) {
			t.Errorf("flowPngMenuItems missing %s", want)
		}
	}
	// New mount points exist in the shell.
	for _, id := range []string{"selCopyPng", "inspectCopyPngMore"} {
		if !strings.Contains(html, `id="`+id+`"`) {
			t.Errorf("index.html missing #%s", id)
		}
	}
	requireUIContracts(t, "js/proxy.js",
		"function pngCopySections(id)",
		"function pngSelectionFlowId()",
		"const more=$('#inspectCopyPngMore');",
		"const sel=$('#selCopyPng');",
		// The inspector dropdown carries side, layout and a fixed theme.
		"{label:'Fixed theme',icon:'appearance',children:[",
	)
	// Repeater mounts it on the toolbar and in the response head.
	requireUIContracts(t, "js/repeater.js",
		"pngMount.id = 'repCopyPng';",
		"resPngMount.id = 'repResCopyPng';",
		"mountFlowCopyPng(pngMount, id, 'btn xs');",
		"{ side: 'res', text: '', ariaLabel: 'Copy this response as a PNG image' }",
		"const pngFlowId = (t) => (t && t.resId && !t.sendPending ? t.resId : 0);",
	)
	// The Comparer and the authz matrix copy the renderer's own image.
	requireUIContracts(t, "js/flow-diff.js",
		"copyPng.id = 'proxyDiffCopyPng';",
		"'/api/render/flow-diff.png?a=' + shownIds[0] + '&b=' + shownIds[1]",
	)
	requireUIContracts(t, "js/authz.js",
		`id="authzCopyMatrixPng"`,
		"copyPreviewPng('/api/render/authz/'+encodeURIComponent(d.runId)+'.png'",
	)
	// Those routes have to be on the clipboard allowlist or the copy is blocked.
	img := readUIAsset(t, "js/copy-image.js")
	for _, want := range []string{"const EVIDENCE_RENDER =", "const RENDER_ROUTE =", "const INTRUDER_RENDER ="} {
		if !strings.Contains(img, want) {
			t.Errorf("copy-image.js missing allowlist entry %s", want)
		}
	}
}

func TestUIInterceptHeldMessageHasAReadOnlyPrettyView(t *testing.T) {
	html := readUIAsset(t, "index.html")
	for _, want := range []string{`id="heldViewSeg"`, `id="heldViewRaw"`, `id="heldViewPretty"`, `id="heldPretty"`} {
		if !strings.Contains(html, want) {
			t.Errorf("index.html missing %s", want)
		}
	}
	// The pretty pane is a <pre>, so it cannot be typed into by construction.
	if !regexp.MustCompile(`<pre id="heldPretty"[^>]*hidden>`).MatchString(html) {
		t.Error("#heldPretty must be a <pre> that starts hidden")
	}
	if !strings.Contains(html, `aria-label="Held message, pretty view — read-only, the bytes to forward are unchanged"`) {
		t.Error("the pretty pane must say it is read-only")
	}
	// Beautify still rewrites the forwarded bytes, and now says so.
	if !strings.Contains(html, "Rewrite the bytes that will be forwarded") {
		t.Error("Beautify's title must warn that it changes what gets forwarded")
	}
	js := requireUIContracts(t, "js/intercept.js",
		"function renderHeldPretty()",
		"function setHeldView(view,{focus=false}={})",
		"pre.innerHTML=raw?highlightHTTP(heldBeautified(raw),true,contentTypeFromRaw(raw)):",
		"function resetHeldView()",
		`'#heldViewRaw','#heldViewPretty'`,
		// A textarea's API value normalizes CRLF to LF, so core's CRLF-only
		// prettify() never split this text. Both separators must be accepted.
		"function heldBeautified(raw)",
		`const sep=/\r?\n\r?\n/.exec(raw);`,
	)
	if strings.Contains(js, "prettify(ta.value)") {
		t.Error("Beautify must use heldBeautified, which matches the editor's LF separator")
	}
	// The read-only view must never write back into the editor.
	pretty := js[strings.Index(js, "function renderHeldPretty()"):]
	pretty = pretty[:strings.Index(pretty, "function syncHeldViewSeg()")]
	if strings.Contains(pretty, "ta.value=") {
		t.Fatal("the pretty view must not assign #heldRaw.value — that is what Beautify does")
	}
	if strings.Contains(pretty, "setHeldModified") {
		t.Fatal("reading a held message pretty must not mark it MODIFIED")
	}
	// Raw | Pretty | Decoded stay mutually exclusive.
	if !strings.Contains(js, "if(heldView==='pretty')setHeldView('raw');") {
		t.Error("switching to Decoded must leave the pretty view")
	}
	if !strings.Contains(js, "setHeldView('raw');\n  ta.value=heldBeautified(ta.value);") {
		t.Error("Beautify must return to Raw so the rewritten bytes are visible")
	}
}
