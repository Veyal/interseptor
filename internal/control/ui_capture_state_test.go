package control

import (
	"strings"
	"testing"
)

func TestUIProxyInspectorRejectsStaleAsyncBodies(t *testing.T) {
	proxy := readUIAsset(t, "js/proxy.js")
	for _, contract := range []string{
		"let selectFlowEpoch=0",
		"const selectEpoch=++selectFlowEpoch",
		"selectFlowEpoch===selectEpoch",
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
	if strings.Count(proxy, "const selectEpoch=selectFlowEpoch") < 2 {
		t.Error("request, response, and WebSocket rendering must share the selected-flow generation")
	}
}

func TestUIHistoryLoadsRejectStaleFilterAndPageResponses(t *testing.T) {
	proxy := executableJS(readUIAsset(t, "js/proxy.js"))
	for _, contract := range []string{
		"let flowRefreshing=false",
		"let flowLoadEpoch=0,flowPageEpoch=0",
		"let flowLoadEvents=new Map()",
		"let flowLoadOverflow=false",
		"const epoch=++flowLoadEpoch",
		"flowLoadEvents=new Map()",
		"flowRefreshing=true",
		"if(epoch!==flowLoadEpoch)return",
		"const replay=Array.from(flowLoadEvents.values())",
		"if(flowLoadEvents.size>MAX_LIVE_FLOWS)",
		"flowLoadOverflow=true",
		"reconcileFlowLoadEvent(event)",
		"if(!replayExact)scheduleReload()",
		"if(flowRefreshing||loadingMore||!flowHasMore||!state.flows.length)return",
		"const loadEpoch=flowLoadEpoch,pageEpoch=++flowPageEpoch",
		"if(loadEpoch!==flowLoadEpoch||pageEpoch!==flowPageEpoch)return",
		"if(epoch===flowLoadEpoch){flowHasMore=false;toast('flows: '+e.message);}",
		"if(epoch===flowLoadEpoch){flowRefreshing=false;updateTruncBanner();}",
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

func TestUIHistoryLiveBurstActivatesVirtualization(t *testing.T) {
	proxy := executableJS(readUIAsset(t, "js/proxy.js"))
	start := strings.Index(proxy, "function flowRowLiveUpdate(f,isNew)")
	if start < 0 {
		t.Fatal("History live-update renderer not found")
	}
	body := proxy[start:]
	if !strings.Contains(body, "if(isNew&&state.flows.length>=VIRT_MIN){renderRows();return;}") {
		t.Error("History must enter its virtualized render path when a live burst crosses the row threshold")
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

func TestUIIntruderDisplayRendersPreserveAuthoritativeRunState(t *testing.T) {
	tools := executableJS(readUIAsset(t, "js/tools.js"))
	for _, contract := range []string{
		"export function renderIntr(st,{authoritative=true}={})",
		"if(authoritative){",
		"syncIntrTabLock(intrLastRunning||intrStartPending)",
		"intrLastResults=res.slice()",
		"{authoritative:false}",
	} {
		if !strings.Contains(tools, contract) {
			t.Errorf("Intruder display rendering must preserve run lifecycle: missing %q", contract)
		}
	}
	if strings.Contains(tools, "syncIntrTabLock(running||intrStartPending)") {
		t.Error("display-only Intruder data must not control attack tab locking")
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

func TestUIInterceptRefreshRejectsStaleAuthoritativeState(t *testing.T) {
	app := executableJS(readUIAsset(t, "js/app.js"))
	intercept := executableJS(readUIAsset(t, "js/intercept.js"))
	for _, contract := range []string{
		"export function interceptStateGeneration()",
		"export function replaceInterceptState(next)",
		"let interceptMutationTail=Promise.resolve()",
		"let interceptFilterMutationTail=Promise.resolve()",
		"async function applyInterceptMutation(request)",
		"async function applyFilterMutation(request,draft)",
		"const result=interceptMutationTail.then(async()=>",
		"const generation=interceptSummaryEpoch",
		"if(generation!==interceptSummaryEpoch)return false",
		"interceptMutationTail=result.catch(()=>{})",
		"replaceInterceptState(mergeInterceptFilterSince(s,filterGeneration))",
	} {
		if !strings.Contains(intercept, contract) {
			t.Errorf("Intercept authoritative-state contract missing %q", contract)
		}
	}
	for _, contract := range []string{
		"const generation=interceptStateGeneration()",
		"replaceInterceptState(mergeInterceptFilterSince(next,filterGeneration))",
		"replaceInterceptState(m.intercept)",
	} {
		if !strings.Contains(app, contract) {
			t.Errorf("Intercept refresh generation contract missing %q", contract)
		}
	}
	if strings.Count(app, "if(generation!==interceptStateGeneration())return") < 2 {
		t.Error("Intercept refresh success and failure must both reject stale completions")
	}
	if strings.Count(intercept, "await applyInterceptMutation(") != 2 {
		t.Error("request and response safety toggles must reject a response superseded by newer SSE state")
	}
	if strings.Count(intercept, "await applyFilterMutation(") != 1 {
		t.Error("filter persistence must use its independent field-scoped mutation lane")
	}
}
