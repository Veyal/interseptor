package control

import (
	"strings"
	"testing"
)

func requireUIContracts(t *testing.T, asset string, contracts ...string) string {
	t.Helper()
	src := readUIAsset(t, asset)
	for _, contract := range contracts {
		if !strings.Contains(src, contract) {
			t.Errorf("%s is missing UI contract %q", asset, contract)
		}
	}
	return src
}

func TestUIProxyInspectorNeverShowsTheWrongSelectedFlow(t *testing.T) {
	requireUIContracts(t, "js/proxy.js",
		"function clearMissingInspectorSelection()",
		"if(state.selId!=null&&!flowStore.byId.has(state.selId))closeInspector()",
		"function showInspectorLoading(id)",
		"if(reqDecode)reqDecode.hidden=true",
		"if(resDecode)resDecode.hidden=true",
		"setInspectorActionState(true)",
		"function showInspectorLoadError(id,error)",
	)
}

func TestUISettingsRefreshPreservesDirtyFields(t *testing.T) {
	src := requireUIContracts(t, "js/settings.js",
		"function snapshotDirtySettings()",
		"function restoreDirtySettings(snapshot)",
		"data-settings-dirty",
		"if (el.type === 'file') return",
		"snapshot.proxyListeners=collectProxyAddrs()",
		"snapshot.hostHeaders=collectHostHeaderRows()",
		"snapshot.deviceProxyMode=selected.dataset.mode",
		"deferRender",
		"setSeg(button,button.dataset.mode===snapshot.deviceProxyMode)",
		"function restoreDirtySettingsDerived(snapshot)",
		"applyOobDisabledUI()",
		"renderUpstreamProxyFields($('#setUpstreamScheme').value)",
	)
	if got := strings.Count(src, "const dirty=snapshotDirtySettings()"); got < 2 {
		t.Fatalf("settings and session loaders must both snapshot dirty fields; found %d snapshots", got)
	}
	if got := strings.Count(src, "restoreDirtySettings(dirty)"); got < 2 {
		t.Fatalf("settings and session loaders must both restore dirty fields; found %d restores", got)
	}
}

func TestUISessionSettingsLoadFailureIsPersistentAndRetryable(t *testing.T) {
	requireUIContracts(t, "index.html",
		`id="sessionLoadState"`,
		`role="status"`,
		`aria-live="polite"`,
	)
	requireUIContracts(t, "js/settings.js",
		"const loadState=$('#sessionLoadState')",
		"renderLoadError(loadState,'Session settings',e,loadSession",
	)
}

func TestUIHistoryPagingAndNarrowScrollingStayUnderstandable(t *testing.T) {
	requireUIContracts(t, "index.html",
		`id="flowCapBanner"`,
		`id="flowCapRetry"`,
		`aria-live="polite"`,
	)
	requireUIContracts(t, "js/proxy.js",
		"let flowPageError=null",
		"let flowLoadError=null",
		"function syncFlowHorizontalScroll()",
		"head.scrollLeft=box.scrollLeft",
		"flowPageError=e",
		"flowLoadError=e",
		"retry.onclick=()=>loadFlows()",
	)
	requireUIContracts(t, "app.css",
		"#flowHead{overflow-x:auto",
		"#flowCapBanner",
	)
}

func TestUIMobileKeepsReconnectStateVisible(t *testing.T) {
	requireUIContracts(t, "index.html",
		`id="sseStatus" role="status" aria-live="polite"`,
	)
	requireUIContracts(t, "js/app.js",
		"wrap.setAttribute('aria-label'",
		"wrap.classList.toggle('reconnecting'",
	)
	css := requireUIContracts(t, "app.css", "#sseStatus{display:inline-flex")
	if !strings.Contains(css, "#sseStatus.reconnecting #sseLabel") {
		t.Error("phone reconnect label must become visible when the control stream is reconnecting")
	}
}

func TestUIScopeLoadFailureIsPersistentAndRetryable(t *testing.T) {
	requireUIContracts(t, "index.html", `id="scopeLoadState"`)
	requireUIContracts(t, "js/proxy.js",
		"renderLoadError(loadState,'Target scope',e,loadScope",
		"loadState.style.display='none'",
	)
}

func TestUIActivityRowsExposeOutcomeAndRetainFocus(t *testing.T) {
	requireUIContracts(t, "js/activity.js",
		"data-activity-id=",
		`tabindex="0"`,
		"const status=it.ok?'Success':'Error'",
		"aria-hidden=\"true\"",
		"function restoreActivityFocus",
		"restoreActivityFocus(box,focusedId)",
	)
}

func TestUIEvidenceImagesAreKeyboardOperable(t *testing.T) {
	for _, asset := range []string{"js/core.js", "js/findings.js"} {
		requireUIContracts(t, asset,
			`tabindex="0"`,
			`role="button"`,
			"Open screenshot",
		)
	}
	requireUIContracts(t, "js/core.js",
		"function openImageLightboxFromTarget(target)",
		"if(e.key!=='Enter'&&e.key!==' ')return",
	)
	findings := readUIAsset(t, "js/findings.js")
	if got := strings.Count(findings, `class="md-img find-doc-img" tabindex="0" role="button"`); got < 2 {
		t.Errorf("edit and report finding screenshots must both be keyboard operable; found %d", got)
	}
}

func TestUIFindingFlowEvidenceHasOneExplicitInspectAction(t *testing.T) {
	findings := requireUIContracts(t, "js/findings.js", `<div class="find-report-flow"`, "find-open-flow")
	if strings.Contains(findings, `<button type="button" class="find-report-flow"`) {
		t.Error("finding flow summary duplicates the adjacent Inspect request action")
	}
}

func TestUIMapCollapseAndGraphLabelsMatchVisibleState(t *testing.T) {
	mapJS := requireUIContracts(t, "js/map.js",
		"const hostOpen=open||!mapState.collapsed.has(h.key)",
		"status ${n.ep.lastStatus||'unknown'}",
		"${n.ep.hits||0} hit${n.ep.hits===1?'':'s'}",
		"function mapGraphFiltered(eps)",
		"eps=mapGraphFiltered(eps)",
		"mapState.searchScope + '|' + mapState.search",
		"mapState.collapseIdentical?'1':'0'",
		`data-host-key="${escAttr(h.key)}"`,
		"function wireMapHostDetails(box)",
		"hydrateMapTreeNode(body)",
	)
	if strings.Count(mapJS, "wireMapHostDetails(box)") < 2 {
		t.Error("Map tree must define and invoke host detail persistence wiring")
	}
}

func TestUIContextMenuMovesAndRestoresFocus(t *testing.T) {
	requireUIContracts(t, "js/core.js",
		"ctx._returnFocus=document.activeElement",
		`role="menuitem" tabindex="-1"`,
		"items[0].focus()",
		"ctx._returnFocus?.focus",
	)
}

func TestUIInspectorEscapeDismissesFindBeforeInspector(t *testing.T) {
	requireUIContracts(t, "js/proxy.js",
		"if(e.key==='Escape'&&inspectFindBar.style.display==='flex')",
		"e.stopImmediatePropagation()",
	)
}

func TestUIToolEditorsKeepAllActionsReachableOnPhones(t *testing.T) {
	index := requireUIContracts(t, "index.html",
		`class="row checks-editor-actions"`,
		`class="btn btn-field checks-editor-id"`,
	)
	if got := strings.Count(index, `class="row checks-editor-actions"`); got < 2 {
		t.Fatalf("checks and codecs editors both need the responsive action layout; found %d", got)
	}
	requireUIContracts(t, "app.css",
		".checks-editor-actions",
		".checks-editor-id",
		"flex-wrap:wrap",
	)
}

func TestUIAPIReferenceFailuresStayVisibleAndRetryable(t *testing.T) {
	requireUIContracts(t, "index.html", `id="restLoadState"`)
	requireUIContracts(t, "js/apipanel.js",
		"renderLoadError($('#restLoadState'),'REST reference',e,loadReference,hadData)",
		"renderLoadError($('#mcpBody'),'MCP reference',e,loadMCP,false)",
	)
}

func TestUIRESTAndProjectRefreshesMarkPreviousDataStale(t *testing.T) {
	requireUIContracts(t, "js/apipanel.js",
		"let restReferenceLoaded=false",
		"let restReferenceLoadEpoch=0",
		"const epoch=++restReferenceLoadEpoch",
		"if(epoch!==restReferenceLoadEpoch)return",
		"function markRESTReferenceStale(stale)",
		"markRESTReferenceStale(true)",
		"renderLoadError($('#restLoadState'),'REST reference',e,loadReference,hadData)",
		"markRESTReferenceStale(false)",
		"base.removeAttribute('data-stale')",
	)
	apipanel := readUIAsset(t, "js/apipanel.js")
	start := strings.Index(apipanel, "export async function loadReference()")
	end := strings.Index(apipanel, "export async function loadMCP()")
	if start < 0 || end <= start {
		t.Fatal("REST reference loader not found")
	}
	loader := apipanel[start:end]
	guard := strings.Index(loader, "if(epoch!==restReferenceLoadEpoch)return")
	paint := strings.Index(loader, "$('#apiBase').textContent")
	if guard < 0 || paint < 0 || guard > paint {
		t.Error("REST reference load epoch must be checked before any response paints into the UI")
	}
	requireUIContracts(t, "js/settings.js",
		"let projectDataLoaded=false",
		"let projectLoadEpoch=0",
		"const epoch=++projectLoadEpoch",
		"if(epoch!==projectLoadEpoch)return",
		"function setProjectControlsDisabled(",
		"function markProjectDataStale(stale)",
		"markProjectDataStale(true)",
		"renderLoadError($('#projectLoadState'),'Projects',e,loadProject,hadData)",
		"markProjectDataStale(false)",
	)
}

func TestUIProjectModalDisablesExistingRowsDuringRefresh(t *testing.T) {
	settings := readUIAsset(t, "js/settings.js")
	start := strings.Index(settings, "async function renderProjModal()")
	end := strings.Index(settings, "export async function openProjectModal()")
	if start < 0 || end <= start {
		t.Fatal("project modal renderer not found")
	}
	renderer := settings[start:end]
	for _, contract := range []string{
		"list.setAttribute('aria-busy','true')",
		"list.querySelectorAll('.pm-row').forEach(button=>button.disabled=true)",
		"setProjectModalActionsDisabled(true)",
		"list.removeAttribute('aria-busy')",
	} {
		if !strings.Contains(renderer, contract) {
			t.Errorf("project modal pending-state contract missing %q", contract)
		}
	}
	disable := strings.Index(renderer, "list.querySelectorAll('.pm-row').forEach(button=>button.disabled=true)")
	request := strings.Index(renderer, "await api('/api/project')")
	if disable < 0 || request < 0 || disable > request {
		t.Error("existing project modal rows must be disabled before the refresh request starts")
	}
}

func TestUIBulkScopeReportsPartialFailures(t *testing.T) {
	requireUIContracts(t, "js/proxy.js",
		"const failed=[]",
		"failed.push(host)",
		"failed.slice(0,3).join(', ')",
		"failed.length>3?' +'+(failed.length-3)+' more':''",
		"button.setAttribute('aria-busy','true')",
		"button.removeAttribute('aria-busy')",
	)
}

func TestUICheckToggleWaitsForAcknowledgementAndRollsBack(t *testing.T) {
	requireUIContracts(t, "js/scanner.js",
		"let checkToggleEpoch=0",
		"async function saveCheckToggle(cb,box)",
		"toggle.disabled=true",
		"cb.checked=previous",
		"if(epoch!==checkToggleEpoch)",
	)
}

func TestUIIntruderHistoryRestoresTheWholeAttackConfiguration(t *testing.T) {
	requireUIContracts(t, "js/tools.js",
		"intrApply(h.cfg)",
		"intrState.sniperSource=t.sniperSource||'list'",
		"$('#intrGrep').value=t.grep||''",
	)
}

func TestUIOOBAndCodecCopyDescribeOnlyExistingActions(t *testing.T) {
	index := requireUIContracts(t, "index.html", `id="oobBtn" data-oob-ui aria-label="OOB">OOB</button>`)
	if strings.Contains(index, ">` OOB</button>") {
		t.Error("OOB label must not expose a literal backtick")
	}
	requireUIContracts(t, "app.css", ".oob-disabled .scan-tools-bar{display:none")
	codecs := requireUIContracts(t, "js/codecs.js", "consult <b>Docs</b>")
	if strings.Contains(codecs, "use <b>Describe</b>") {
		t.Error("codec guidance must not reference the removed Describe action")
	}
}

func TestUIVaultImportUsesTheNonBlockingProductPrompt(t *testing.T) {
	requireUIContracts(t, "js/apipanel.js",
		"uiPrompt",
		"const name=await uiPrompt({title:'Import vault project'",
	)
	requireUIContracts(t, "js/settings.js",
		"uiPrompt",
		"const name=await uiPrompt({title:'Import full project'",
	)
}

func TestUISecurityRelevantReferenceFailuresStayVisibleAndRetryable(t *testing.T) {
	requireUIContracts(t, "index.html", `id="projectLoadState"`)
	requireUIContracts(t, "js/authz.js",
		"renderLoadError(panel,'Authorization scope',e,renderAuthzScopePanel",
		"renderLoadError(el,'In-scope traffic',e,renderAuthzScopePanel",
		"renderLoadError($('#authzIds'),'Saved identities',e,loadAuthzIdentities",
		"renderLoadError(box,'Captured authentication',e,()=>loadFlowAuthHint(flowId)",
	)
	requireUIContracts(t, "js/settings.js",
		"renderLoadError($('#projectLoadState'),'Projects',e,loadProject",
		"renderLoadError(list,'Projects',e,renderProjModal",
		"renderLoadError($('#sysProxyHint'),'System proxy',e,loadSysProxy",
	)
	requireUIContracts(t, "js/proxy.js",
		"let viewsLoadError=null",
		"Retry loading saved views",
	)
	requireUIContracts(t, "js/tags.js",
		"let tagLoadError=null",
		"renderLoadError(bar,'Tags',tagLoadError,loadTags",
	)
	scanner := requireUIContracts(t, "js/scanner.js",
		"renderLoadError(box,'Check packs',e,loadPacksPanel,false)",
	)
	if strings.Contains(scanner, "api('/api/packs/catalog').catch") || strings.Contains(scanner, "api('/api/packs').catch") {
		t.Error("check-pack requests must reach the shared retryable failure state instead of becoming fake empty catalogs")
	}
	requireUIContracts(t, "js/apipanel.js",
		"renderLoadError($('#shareStatus'),'Share status',e,loadShare,false)",
		"renderLoadError(el,'Peer sync status',e,loadMergeStatus,false)",
	)
}
