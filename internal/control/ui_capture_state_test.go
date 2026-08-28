package control

import (
	"strings"
	"testing"
)

func TestUIProxyInspectorRejectsStaleAsyncBodies(t *testing.T) {
	proxy := readUIAsset(t, "js/proxy.js")
	for _, contract := range []string{
		"const renderSideEpoch={req:0,res:0}",
		"const flowId=state.selId",
		"const epoch=++renderSideEpoch[side]",
		"renderSideEpoch[side]===epoch",
		"state.selId===flowId",
		"'/api/flows/'+flowId+'/decoded?side='",
		"'/api/flows/'+flowId+'/raw?side='",
	} {
		if !strings.Contains(proxy, contract) {
			t.Errorf("Proxy stale-render contract missing %q", contract)
		}
	}
	if !strings.Contains(proxy, "if(state.selId!==id)return") {
		t.Error("WebSocket/flow detail rendering must stop after the selected flow changes")
	}
}

func TestUIHistoryLoadsRejectStaleFilterAndPageResponses(t *testing.T) {
	proxy := executableJS(readUIAsset(t, "js/proxy.js"))
	for _, contract := range []string{
		"let flowLoadEpoch=0,flowPageEpoch=0",
		"const epoch=++flowLoadEpoch",
		"if(epoch!==flowLoadEpoch)return",
		"const loadEpoch=flowLoadEpoch,pageEpoch=++flowPageEpoch",
		"if(loadEpoch!==flowLoadEpoch||pageEpoch!==flowPageEpoch)return",
	} {
		if !strings.Contains(proxy, contract) {
			t.Errorf("History latest-filter contract missing %q", contract)
		}
	}
}

func TestUIHistoryLiveFilterRemovalClosesSelectedInspector(t *testing.T) {
	proxy := executableJS(readUIAsset(t, "js/proxy.js"))
	remove := strings.Index(proxy, "if(!flowMatchesFilters(f))")
	if remove < 0 {
		t.Fatal("History live-update filter-removal branch not found")
	}
	branch := proxy[remove:]
	for _, contract := range []string{
		"const removedSelected=state.selId===f.id",
		"if(removedSelected){closeInspector();return;}",
	} {
		if !strings.Contains(branch, contract) {
			t.Errorf("History selected-removal contract missing %q", contract)
		}
	}
}

func TestUIResponseRenderFallbackKeepsARIAStateInSync(t *testing.T) {
	proxy := executableJS(readUIAsset(t, "js/proxy.js"))
	for _, contract := range []string{
		"const on=b.dataset.view==='pretty'",
		"b.classList.toggle('on',on)",
		"b.setAttribute('aria-pressed',on?'true':'false')",
	} {
		if !strings.Contains(proxy, contract) {
			t.Errorf("response Render fallback ARIA contract missing %q", contract)
		}
	}
}

func TestUIFlowPopupLatestRequestWins(t *testing.T) {
	modal := executableJS(readUIAsset(t, "js/flowmodal.js"))
	for _, contract := range []string{
		"let fmOpenEpoch=0",
		"const epoch=++fmOpenEpoch",
		"if(epoch!==fmOpenEpoch)return",
		"const renderEpoch=++fmSideEpoch[side]",
		"fmSideEpoch[side]===renderEpoch",
	} {
		if !strings.Contains(modal, contract) {
			t.Errorf("flow popup latest-request contract missing %q", contract)
		}
	}
}

func TestUIWorkflowShortcutsCannotActThroughModalOrTextField(t *testing.T) {
	app := executableJS(readUIAsset(t, "js/app.js"))
	blocked := strings.Index(app, "if(workflowShortcutBlocked())return")
	repeater := strings.Index(app, "if(activePanel()==='repeater'&&(isModSpace(e)||isModShortcut(e,'Enter')))")
	typing := strings.Index(app, "if(typing)return")
	intercept := strings.Index(app, "if(activePanel()==='intercept'&&state.heldSel")
	if blocked < 0 || repeater < 0 || typing < 0 || intercept < 0 {
		t.Fatal("workflow shortcut guards not found")
	}
	if !(blocked < repeater && repeater < typing && typing < intercept) {
		t.Errorf("shortcut guard order must be modal → explicit modified shortcut → typing guard → destructive Intercept shortcut (got %d, %d, %d, %d)", blocked, repeater, typing, intercept)
	}
}

func TestUIInterceptPreservesIntentionalEmptyDraft(t *testing.T) {
	intercept := executableJS(readUIAsset(t, "js/intercept.js"))
	for _, contract := range []string{
		"heldRawCache.has(cacheKey)?heldRawCache.get(cacheKey)",
		"heldRawCache.set(heldKey(sel.side,sel.id),$('#heldRaw').value)",
	} {
		if !strings.Contains(intercept, contract) {
			t.Errorf("Intercept empty-draft contract missing %q", contract)
		}
	}
}

func TestUIInterceptAcknowledgementReconcilesLocalQueue(t *testing.T) {
	intercept := executableJS(readUIAsset(t, "js/intercept.js"))
	for _, contract := range []string{
		"function reconcileHeldRemoval(sel)",
		"filter(h=>h.id!==sel.id)",
		"state.heldSel=null",
		"reconcileHeldRemoval(sel)",
	} {
		if !strings.Contains(intercept, contract) {
			t.Errorf("Intercept acknowledgement reconciliation missing %q", contract)
		}
	}
}

func TestUIRepeaterGuardsPendingAndDecodedTabIdentity(t *testing.T) {
	tools := executableJS(readUIAsset(t, "js/tools.js"))
	for _, contract := range []string{
		"if(t.sendPending)return",
		"t.reqDecodeEpoch=(t.reqDecodeEpoch||0)+1",
		"t.reqDecodeEpoch===decodeEpoch",
		"if(repCur()!==t)return",
	} {
		if !strings.Contains(tools, contract) {
			t.Errorf("Repeater async identity contract missing %q", contract)
		}
	}
}

func TestUIIntruderLocksRunToOriginatingTab(t *testing.T) {
	tools := executableJS(readUIAsset(t, "js/tools.js"))
	for _, contract := range []string{
		"let intrRunTabId=null",
		"function syncIntrTabLock(locked)",
		"intrRunTabId=intrTabs.cur()?.tid??null",
		"syncIntrTabLock(true)",
		"aria-busy",
	} {
		if !strings.Contains(tools, contract) {
			t.Errorf("Intruder run/tab ownership contract missing %q", contract)
		}
	}
}

func TestUIWebSocketReplayRowsAreKeyboardOperable(t *testing.T) {
	proxy := executableJS(readUIAsset(t, "js/proxy.js"))
	if !strings.Contains(proxy, "wireRowKey(el,activate)") {
		t.Error("replayable WebSocket frames must use the shared keyboard activation contract")
	}
}

func TestUIPanelTransitionCommitsStateSynchronously(t *testing.T) {
	motion := executableJS(readUIAsset(t, "js/motion.js"))
	start := strings.Index(motion, "export function transitionView(updateFunction)")
	if start < 0 {
		t.Fatal("shared panel transition helper not found")
	}
	body := motion[start:]
	update := strings.Index(body, "updateFunction()")
	animate := strings.Index(body, ".animate(")
	if update < 0 || animate < 0 || update > animate {
		t.Error("panel state must commit synchronously before its optional entrance animation starts")
	}
	if strings.Contains(body, "document.startViewTransition") {
		t.Error("deferred View Transition callbacks race rapid tab/keyboard activation")
	}
}

func TestUIInterceptInitialLoadFailureIsExplicitAndRetryable(t *testing.T) {
	app := executableJS(readUIAsset(t, "js/app.js"))
	for _, contract := range []string{
		"function renderInterceptUnavailable(error)",
		"Intercept state unavailable:",
		"data-intercept-retry",
		"retry.onclick=refreshIntercept",
		"button.disabled=true",
		"textContent='Unknown'",
	} {
		if !strings.Contains(app, contract) {
			t.Errorf("Intercept initial error-state contract missing %q", contract)
		}
	}
}
