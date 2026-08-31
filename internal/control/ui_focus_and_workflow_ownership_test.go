package control

import (
	"strings"
	"testing"
)

func TestUIHighFrequencyListsPreserveKeyboardFocus(t *testing.T) {
	requireUIContracts(t, "js/proxy.js",
		"function captureFlowListFocus(box)",
		"function restoreFlowListFocus(box,focus)",
		"restoreFlowListFocus(box,focus)",
	)
	requireUIContracts(t, "js/intercept.js",
		"const focusedHeld=document.activeElement?.closest?.('#heldList .icpt-item[data-id][data-side]')",
		"target?.focus({preventScroll:true})",
	)
	requireUIContracts(t, "js/scanner.js",
		"const previousSelectedTitle=(scanState.groups||[])[scanState.sel]?.title||''",
		"const focusedTitle=",
		"groups.findIndex(group=>group.title===previousSelectedTitle)",
		"focus({preventScroll:true})",
	)
	requireUIContracts(t, "js/tools.js",
		"const focusedHistoryID=focusedHistory?.dataset.id||''",
		"if(restoreHistoryFocus)",
		"data-hid=\"${h.id}\"",
		"const focusedHistoryID=document.activeElement?.closest?.('#intrHistory .h[data-hid]')?.dataset.hid||''",
	)
}

func TestUIHistoryFilterDismissalsAreKeyboardControls(t *testing.T) {
	proxy := requireUIContracts(t, "js/proxy.js",
		`<button type="button" class="x" data-clear=`,
		`aria-label="Show TLS failures"`,
		`aria-label="Remove ${escAttr(e.field)} exclusion"`,
	)
	if strings.Contains(proxy, `<span class="x"`) {
		t.Fatal("filter dismissals must not regress to mouse-only spans")
	}
	requireUIContracts(t, "app.css", ".chip .x{", "border:0", "font:inherit")
}

func TestUIDynamicRemoveControlsHaveSpecificAccessibleNames(t *testing.T) {
	requireUIContracts(t, "js/authz.js",
		"aria-label=\"Remove authorization identity ${i+1}\"",
		"authzIdentityEditEpoch++;\n    renderIdentities(ids)",
	)
	requireUIContracts(t, "js/settings.js", "aria-label=\"Remove host header override for ${escAttr(host||'new host')}\"")
	requireUIContracts(t, "js/findings.js",
		"aria-label=\"Move evidence block ${i+1} up\"",
		"aria-label=\"Move evidence block ${i+1} down\"",
		"aria-label=\"Remove evidence block ${i+1}\"",
	)
}

func TestUIFindingAttachmentsReportAuthoritativePartialResults(t *testing.T) {
	findings := requireUIContracts(t, "js/findings.js",
		"const findingAttachPending=new Set()",
		"let attached=0",
		"failed.push({id:fid,error})",
		"await loadFindings()",
		"'attached '+attached+' of '+ids.length+' flows · '+failed.length+' failed'",
		"await attachFlowsToFinding(Number(b.dataset.id),ids)",
	)
	if strings.Contains(findings, "toast('attached ' + ids.length") {
		t.Fatal("bulk finding attachments must not claim every flow succeeded")
	}
}

func TestUISSEHeavyPanelUpdatesAreVisibilityGated(t *testing.T) {
	app := requireUIContracts(t, "js/app.js",
		"function onPanelUpdate(panelName,reloadFn,badgeId)",
		"'intruder.update':{contract:'panel-gated nudge'",
		"onPanelUpdate('intruder',scheduleIntr,'intrBadge')",
		"'scanner.update':{contract:'panel-gated nudge'",
		"onPanelUpdate('scanner',loadIssues,'scanBadge')",
		"if(t.dataset.tab==='intruder'){clearNavDot('intrBadge');scheduleIntr();}",
		"if(t.dataset.tab==='scanner'){clearNavDot('scanBadge');loadScanTargets();loadIssues();}",
	)
	if strings.Contains(app, "else if(m.type==='intruder.update')scheduleIntr()") || strings.Contains(app, "else if(m.type==='scanner.update')loadIssues()") {
		t.Fatal("scanner and Intruder SSE nudges must not do heavy work off-panel")
	}
	requireUIContracts(t, "index.html", `id="intrBadge"`, `id="scanBadge"`)
}

func TestUIProjectSwitchWaitsForTheAcceptedIdentity(t *testing.T) {
	settings := requireUIContracts(t, "js/settings.js",
		"let projectSwitchEpoch=0,projectSwitchTimer=null,projectSwitchPending=false",
		"expected=String(accepted.switching)",
		"const reached=path?projectPathKey(d.dir)===projectPathKey(path):String(d.current||'')===expected",
		"if(reached){projectSwitchPending=false;projectSwitchTimer=null;location.reload();return;}",
		"Project switch was not confirmed within 30 seconds",
	)
	if strings.Contains(settings, "graceTries") {
		t.Fatal("project switching must never accept an old-project response after a grace period")
	}
}

func TestUIIntruderPresetSnapshotsBeforePrompt(t *testing.T) {
	tools := requireUIContracts(t, "js/tools.js",
		"const snapshot=intrReadEditor()",
		"target:snapshot.target",
		"template:snapshot.template",
		"posNums:snapshot.posNums.map",
	)
	snapshot := strings.Index(tools, "const snapshot=intrReadEditor()")
	prompt := strings.Index(tools[snapshot:], "await uiPrompt({title:'Save attack preset'")
	if snapshot < 0 || prompt < 0 {
		t.Fatal("Intruder preset snapshot/prompt sequence not found")
	}
}

func TestUIIntruderHistoryBelongsToItsOriginatingTab(t *testing.T) {
	requireUIContracts(t, "js/tools.js",
		"function activeIntrHistory()",
		"h.tid===activeId",
		"tid:intrRunCfg?.tid??intrRunTabId",
		"intrRunCfg={...intrReadEditor(),tid:intrTabs.cur()?.tid??null}",
		"const h=intrHistory.find(item=>item.id===id)",
	)
}

func TestUICreatedFindingsSurfacePartialAttachmentWarnings(t *testing.T) {
	for _, asset := range []string{"js/findings.js", "js/scanner.js", "js/tools.js"} {
		requireUIContracts(t, asset,
			"Array.isArray(f.warnings)",
			"PoC attachment",
		)
	}
}

func TestUISendToRepeaterOnlyReusesPreexistingUneditedTabs(t *testing.T) {
	requireUIContracts(t, "js/tools.js",
		"const tabSnapshots=repTabs.tabs.map",
		"repTabs.tabs.includes(snapshot.tab)",
		"snapshot.endpoint===fep",
		"snapshot.editEpoch===(snapshot.tab.reqEditEpoch||0)",
	)
}

func TestUIAuthzContextActionRetainsItsExplicitFlow(t *testing.T) {
	requireUIContracts(t, "js/authz.js",
		"let authzSelectionAtOpen=null",
		"let authzSelectionChanged=false",
		"authzSelectionChanged?(state.selId||authzFlowId):authzFlowId",
		"authzSelectionAtOpen=state.selId||null",
		"authzSelectionChanged=false",
		"authzSelectionChanged=true",
		"authzFlowId=flowId||authzSelectionAtOpen||null",
	)
	proxy := readUIAsset(t, "js/proxy.js")
	start := strings.Index(proxy, "export async function selectFlow(id)")
	end := strings.Index(proxy, "export async function renderSide(side)")
	if start < 0 || end <= start {
		t.Fatal("selected-flow handler not found")
	}
	handler := proxy[start:end]
	if !strings.Contains(handler, "state.selId=id;\n  if(switching)onAuthzSelectionChanged();") {
		t.Error("same-flow refreshes must not retarget an explicit Authz context action")
	}
}

func TestUIIntruderClearsOldEvidenceBeforeStartingAnotherRun(t *testing.T) {
	requireUIContracts(t, "js/tools.js",
		"intrLastResults=[];intrDisplayedResults=[]",
		"intrLastRunning=false;intrLastTotal=0;intrLastDone=0",
		"showIntrLiveResults()",
	)
}

func TestUISelectableRowsExposeTheirCurrentState(t *testing.T) {
	requireUIContracts(t, "js/proxy.js",
		"aria-current=\"${f.id===state.selId?'true':'false'}\"",
		"aria-pressed=\"${state.selected.has(f.id)?'true':'false'}\"",
	)
	requireUIContracts(t, "js/intercept.js",
		"aria-current=\"${(state.heldSel&&state.heldSel.id===h.id&&state.heldSel.side===h.side)?'true':'false'}\"",
		"el.setAttribute('aria-current',selected?'true':'false')",
	)
	requireUIContracts(t, "js/findings.js", "aria-current=\"${f.id === selFinding ? 'true' : 'false'}\"")
	requireUIContracts(t, "js/tools.js", "aria-current=\"${f.id===t.resId?'true':'false'}\"")
}

func TestUIActivityOnlyMakesActionableRowsKeyboardStops(t *testing.T) {
	activity := requireUIContracts(t, "js/activity.js",
		"${fid?' tabindex=\"0\" role=\"button\"':''}",
	)
	if strings.Contains(activity, `class="act-row${fid?' act-jump':''}${grp}" tabindex="0"`) {
		t.Fatal("non-actionable Activity entries must not be keyboard stops")
	}
}

func TestUIInnerToolTabpanelsAreLabelledByTheirOwnTabs(t *testing.T) {
	index := requireUIContracts(t, "index.html",
		`id="repTabPanel" role="tabpanel"`,
		`id="intrTabPanel" role="tabpanel"`,
	)
	for _, stale := range []string{
		`id="repTabPanel" role="tabpanel" aria-labelledby="tab-repeater"`,
		`id="intrTabPanel" role="tabpanel" aria-labelledby="tab-intruder"`,
	} {
		if strings.Contains(index, stale) {
			t.Errorf("inner task tabpanel must not reuse the top-level navigation label: %s", stale)
		}
	}
}

func TestUICrossFeatureTransfersDoNotOverwriteNewerEditorWork(t *testing.T) {
	tools := requireUIContracts(t, "js/tools.js",
		"const targetEditEpoch=target._editEpoch||0",
		"(target._editEpoch||0)!==targetEditEpoch",
	)
	if strings.Index(tools, "const targetEditEpoch=target._editEpoch||0") > strings.Index(tools, "await Promise.all([api('/api/flows/'+f.id)") {
		t.Fatal("Intruder transfer must snapshot editor ownership before requesting flow data")
	}

	proxy := requireUIContracts(t, "js/proxy.js",
		"const editEpoch=t.reqEditEpoch||0",
		"const current=()=>!t._closed&&(t.reqEditEpoch||0)===editEpoch",
		"await Promise.all([api('/api/flows/'+f.id)",
	)
	check := strings.Index(proxy, "if(!current())")
	paint := strings.Index(proxy, "t.method=d.method")
	if check < 0 || paint < 0 || check > paint {
		t.Fatal("Send as identity must validate tab ownership before painting fetched data")
	}
}

func TestUIIntruderFilePickersBelongToTheirStartingTab(t *testing.T) {
	tools := requireUIContracts(t, "js/tools.js",
		"const ownerTab=intrTabs.cur(),ownerEditEpoch=intrTabs.cur()?._editEpoch||0",
		"intrTabs.cur()!==ownerTab||(ownerTab._editEpoch||0)!==ownerEditEpoch||!ta.isConnected",
		"const template= $('#intrTemplate')",
		"intrTabs.cur()!==ownerTab||(ownerTab._editEpoch||0)!==ownerEditEpoch||template!==$('#intrTemplate')||!template.isConnected",
	)
	if strings.Count(tools, "const got=await pickTextFile") < 2 {
		t.Fatal("expected both Intruder file picker paths")
	}
}

func TestUITabFocusCallbacksBelongToTheLatestSelection(t *testing.T) {
	requireUIContracts(t, "js/core.js",
		"focusEpoch:0",
		"const epoch=++mgr.focusEpoch",
		"if(epoch!==mgr.focusEpoch||mgr.active!==tid)return",
	)
}

func TestUIAuthzHintTracksTheEffectiveFlowTarget(t *testing.T) {
	requireUIContracts(t, "js/authz.js",
		"let authzHintTarget=null",
		"if(authzModalOpen()&&f!==authzHintTarget)",
		"loadFlowAuthHint(f)",
		"export function onAuthzSelectionChanged()",
	)
	requireUIContracts(t, "js/proxy.js",
		"openAuthz, onAuthzSelectionChanged",
		"onAuthzSelectionChanged()",
	)
}
