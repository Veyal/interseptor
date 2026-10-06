package control

import (
	"encoding/json"
	"os/exec"
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
		"const focusedIssue=!!document.activeElement?.closest?.('#scanList .scan-item')",
		"const focusedTitle=",
		"groups.findIndex(group=>group.title===previousSelectedTitle)",
		"focus({preventScroll:true})",
		"if(focusedIssue)requestAnimationFrame(()=>$('#scanRun')?.focus({preventScroll:true}))",
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
		"await attachFlowsToFinding(id, ids)",
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
		"projectPathFlavor=projectPathFlavorFor(d.dir)",
		"expected=String(accepted.switching)",
		"expectedPath=path?projectPathKey(path):''",
		"Custom save location must be an absolute folder path.",
		"const reached=path?projectPathKey(d.dir)===expectedPath:String(d.current||'')===expected",
		"if(reached){projectSwitchPending=false;projectSwitchTimer=null;location.reload();return;}",
		"Project switch was not confirmed within 30 seconds",
	)
	if strings.Contains(settings, "accepted.path") {
		t.Fatal("custom project switching must not expand the switch API response contract")
	}
	if strings.Contains(settings, "graceTries") {
		t.Fatal("project switching must never accept an old-project response after a grace period")
	}

	start := strings.Index(settings, "function projectPathFlavorFor(value)")
	end := strings.Index(settings, "export async function doSwitchProject")
	if start < 0 || end <= start {
		t.Fatal("project path normalizer not found")
	}
	cases := [][3]string{
		{"/tmp/engagement/../acme/", "posix", "/tmp/acme"},
		{"/tmp//acme/./", "posix", "/tmp/acme"},
		{"//tmp/../acme", "posix", "/acme"},
		{`\tmp\acme`, "posix", ""},
		{`C:\engagements\draft\..\acme`, "windows", "C:/engagements/acme"},
		{`\\server\share\draft\..\acme`, "windows", "//server/share/acme"},
		{`\\server\share\..\..\acme`, "windows", "//server/share/acme"},
		{"relative/acme", "posix", ""},
		{"../acme", "posix", ""},
	}
	encoded, err := json.Marshal(cases)
	if err != nil {
		t.Fatal(err)
	}
	script := settings[start:end] + "\nconst cases=" + string(encoded) + "; for (const [input,flavor,want] of cases) { const got=projectPathKey(input,flavor); if (got!==want) throw new Error(JSON.stringify({input,flavor,want,got})); }"
	if out, err := exec.Command("node", "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("project path normalization contract failed: %v\n%s", err, out)
	}
}

func TestUIProjectPathFailuresAreOwnedByBothProjectSurfaces(t *testing.T) {
	settings := readUIAsset(t, "js/settings.js")
	requireUIContains(t, settings,
		"function setProjectSwitchFeedback(text,kind='status')",
		"['projNewPath','pmNewPath']",
		"field.setAttribute('aria-invalid','true')",
		"setProjectSwitchFeedback(message,'error')",
		"toast(message,'error')",
		"#pmSwitchNote",
	)

	start := strings.Index(settings, "async function renderProjModal()")
	end := strings.Index(settings, "export async function openProjectModal")
	if start < 0 || end <= start {
		t.Fatal("project modal render boundary not found")
	}
	requireUIContains(t, settings[start:end],
		"projectPathFlavor=projectPathFlavorFor(d.dir)",
		"if(epoch!==projectModalLoadEpoch)return false",
	)

	requireUIContracts(t, "index.html",
		`id="projNewPath" aria-describedby="projSwitchNote"`,
		`id="projSwitchNote" role="status" aria-live="polite"`,
		`id="pmNewPath" aria-describedby="pmSwitchNote"`,
		`id="pmSwitchNote" role="status" aria-live="polite"`,
	)
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
	// Intruder creates findings through the shared findings.js picker.
	for _, asset := range []string{"js/findings.js", "js/scanner.js"} {
		requireUIContracts(t, asset,
			"Array.isArray(f.warnings)",
			"PoC attachment",
		)
	}
}

func TestUISendToRepeaterOnlyReusesPreexistingUneditedTabs(t *testing.T) {
	tools := requireUIContracts(t, "js/tools.js",
		"requestAdoptionPristine:false",
		"requestAdoptionPristine:t.requestAdoptionPristine===true",
		"requestAdoptionPristine:!!t.requestAdoptionPristine",
		"repSaveEditor({operatorEdit:true})",
		"if(changed&&operatorEdit)t.requestAdoptionPristine=false",
		"t.requestAdoptionPristine=false",
		"const tabSnapshots=repTabs.tabs.map",
		"pristine:tab.requestAdoptionPristine===true",
		"repTabs.tabs.includes(snapshot.tab)",
		"snapshot.endpoint===fep",
		"snapshot.pristine",
		"snapshot.tab.requestAdoptionPristine===true",
		"snapshot.editEpoch===(snapshot.tab.reqEditEpoch||0)",
		"snapshot.tab.sendPending!==true",
		"t.requestAdoptionPristine=true",
	)
	if strings.Count(tools, "repSaveEditor({operatorEdit:true})") < 3 {
		t.Error("every direct Repeater request edit path must clear adoption-pristine state")
	}
	reuseStart := strings.Index(tools, "const reusable=tabSnapshots.find")
	reuseEnd := strings.Index(tools, "let t=reusable?.tab||null")
	if reuseStart < 0 || reuseEnd <= reuseStart || !strings.Contains(tools[reuseStart:reuseEnd], "snapshot.tab.sendPending!==true") {
		t.Error("send-to-Repeater must not reuse a tab with an owned send in flight")
	}
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
	// Findings rows are real links, so "current" there stays aria-current.
	requireUIContracts(t, "js/findings.js", "aria-current=\"${f.id === selFinding ? 'true' : 'false'}\"")
	// Single-select JS-rendered lists are listboxes: role=option + aria-selected,
	// not role=button + aria-current. They share js/listbox.js so every one of
	// them keeps a single Tab stop and the same Arrow/Home/End contract.
	requireUIContracts(t, "js/intercept.js",
		"aria-selected=\"${(state.heldSel&&state.heldSel.id===h.id&&state.heldSel.side===h.side)?'true':'false'}\"",
		"wireListbox(list,list.querySelectorAll('.icpt-item')",
		"selectionFollowsFocus:true",
		"setListboxSelection($$('#heldList .icpt-item')",
	)
	requireUIContracts(t, "js/tools.js",
		"aria-selected=\"${f.id===t.resId?'true':'false'}\"",
		"aria-selected=\"${h===intrDisplayedHistory?'true':'false'}\"",
		"wireListbox(box.querySelector('[data-rep-history-rows]'),box.querySelectorAll('.h')",
		"wireListbox(box,box.querySelectorAll('.h')",
	)
	for _, asset := range []string{"js/intercept.js", "js/tools.js"} {
		if strings.Contains(readUIAsset(t, asset), "aria-current=") {
			t.Errorf("%s: a single-select list must expose aria-selected, not aria-current", asset)
		}
	}
	listbox := requireUIContracts(t, "js/listbox.js",
		"container.setAttribute('role', 'listbox')",
		"el.setAttribute('role', 'option')",
		"const NAV_KEYS = ['ArrowDown', 'ArrowUp', 'Home', 'End']",
		"el.tabIndex = i === stop ? 0 : -1",
		"if (selectionFollowsFocus && to !== from && activate) activate(els[to], e)",
		"export function setListboxSelection(rows, isSelected)",
	)
	// One Tab stop: every option but the current one must be removed from the
	// tab order, and Enter/Space activation stays owned by core.js wireRowKey.
	if !strings.Contains(listbox, "wireRowKey(el,") {
		t.Fatal("listbox options must reuse core.js wireRowKey for Enter/Space activation")
	}
}

func TestUIActivityOnlyMakesActionableRowsKeyboardStops(t *testing.T) {
	activity := requireUIContracts(t, "js/activity.js",
		"act-expandable", "aria-controls=\"actDetail-${i}\"", "aria-expanded=\"${expanded}\"", "detail.hidden=expanded",
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
