package control

import (
	"strings"
	"testing"
)

func TestUIProxyResourceOwnershipContracts(t *testing.T) {
	proxyRaw := readUIAsset(t, "js/proxy.js")
	proxy := executableJS(proxyRaw)
	intercept := executableJS(readUIAsset(t, "js/intercept.js"))
	authz := executableJS(readUIAsset(t, "js/authz.js"))
	contracts := map[string][]string{
		"proxy":     {"scopeLoadEpoch", "scopeMutationLanes", "scopeDrafts", "viewsLoadEpoch", "wsReplayEpoch", "compareEpoch", "t._closed"},
		"intercept": {"rulesLoadEpoch", "ruleMutationLanes"},
		"authz":     {"authzScopeEpoch", "authzHintEpoch", "authzRunEpoch", "authzIdentityLoadEpoch", "authzIdentityEditEpoch", "authzIdentityMutationTail"},
	}
	for name, src := range map[string]string{"proxy": proxy, "intercept": intercept, "authz": authz} {
		for _, contract := range contracts[name] {
			if !strings.Contains(src, contract) {
				t.Errorf("%s missing ownership contract %q", name, contract)
			}
		}
	}
	if !strings.Contains(proxyRaw, "flowSearchLoadEpoch") {
		t.Error("proxy missing saved-search load epoch")
	}
}

func TestUIRejectedScopeAndRuleDraftsRestoreAuthoritativeState(t *testing.T) {
	for _, tc := range []struct {
		asset, start, end, owner string
		contracts                []string
	}{
		{"js/proxy.js", "async function updateScope(id,tr)", "async function deleteScope(id)", "scopeDrafts.get(id)===upd", []string{"scopeDrafts.delete(id)", "renderScope()", "toast(e.message,'error')"}},
		{"js/intercept.js", "export async function updateRule(id,tr)", "export async function deleteRule(id)", "ruleDrafts.get(id)===upd", []string{"ruleDrafts.delete(id)", "renderRules()", "toast(e.message,'error')"}},
	} {
		src := readUIAsset(t, tc.asset)
		start := strings.Index(src, tc.start)
		end := strings.Index(src, tc.end)
		if start < 0 || end <= start {
			t.Errorf("%s mutation boundary not found", tc.asset)
			continue
		}
		mutation := src[start:end]
		catch := strings.Index(mutation, "catch(e)")
		if catch < 0 {
			t.Errorf("%s mutation rejection boundary not found", tc.asset)
			continue
		}
		failure := mutation[catch:]
		if strings.Count(mutation, tc.owner) != 2 {
			t.Errorf("%s must preserve newer drafts across both acknowledgement paths", tc.asset)
		}
		for _, contract := range tc.contracts {
			if !strings.Contains(failure, contract) {
				t.Errorf("%s rejected draft reconciliation missing %q", tc.asset, contract)
			}
		}
	}
}

func TestUIRejectedScopeAndRuleDeletesReconcileOnlyTheirOwnedDraft(t *testing.T) {
	for _, tc := range []struct {
		asset, start, end, drafts, revision, render string
	}{
		{"js/proxy.js", "async function deleteScope(id)", "let scopeAddInFlight", "scopeDrafts", "scopeMutationRevision", "renderScope()"},
		{"js/intercept.js", "export async function deleteRule(id)", "let ruleAddInFlight", "ruleDrafts", "ruleMutationRevision", "renderRules()"},
	} {
		src := readUIAsset(t, tc.asset)
		start := strings.Index(src, tc.start)
		end := strings.Index(src, tc.end)
		if start < 0 || end <= start {
			t.Errorf("%s delete mutation boundary not found", tc.asset)
			continue
		}
		mutation := src[start:end]
		for _, contract := range []string{
			"const hadDraft=" + tc.drafts + ".has(id),draftAtDelete=" + tc.drafts + ".get(id)",
			"revision===" + tc.revision + ".get(id)&&hadDraft&&" + tc.drafts + ".get(id)===draftAtDelete",
			tc.drafts + ".delete(id)",
			tc.render,
		} {
			if !strings.Contains(mutation, contract) {
				t.Errorf("%s rejected delete ownership missing %q", tc.asset, contract)
			}
		}
	}
}

func TestUIRejectedScopeAndRuleDeletesRestoreFocusAndAnnounceFailure(t *testing.T) {
	for _, tc := range []struct {
		asset, renderStart, renderEnd, deleteStart, deleteEnd string
	}{
		{"js/proxy.js", "export function renderScope()", "function scopeMutation", "async function deleteScope(id)", "let scopeAddInFlight"},
		{"js/intercept.js", "export function renderRules()", "export async function loadRules", "export async function deleteRule(id)", "let ruleAddInFlight"},
	} {
		src := readUIAsset(t, tc.asset)
		renderStart := strings.Index(src, tc.renderStart)
		renderEnd := strings.Index(src, tc.renderEnd)
		deleteStart := strings.Index(src, tc.deleteStart)
		deleteEnd := strings.Index(src, tc.deleteEnd)
		if renderStart < 0 || renderEnd <= renderStart || deleteStart < 0 || deleteEnd <= deleteStart {
			t.Errorf("%s focus/error mutation boundaries not found", tc.asset)
			continue
		}
		requireUIContains(t, src[renderStart:renderEnd],
			`data-k="delete"`,
			"input[data-k],select[data-k]",
		)
		requireUIContains(t, src[deleteStart:deleteEnd], "toast(e.message,'error')")
	}
}

func TestUIProxySavedViewsRestoreEveryVisibleFilter(t *testing.T) {
	src := executableJS(readUIAsset(t, "js/proxy.js"))
	for _, contract := range []string{
		"searchScope:f.searchScope||'anywhere'",
		"tag:f.tag||''",
		"notesOnly:state.notesOnly",
		"showManual:state.showManual",
		"showAI:state.showAI",
		"hideTlsFailed:state.hideTlsFailed",
		"renderChips();renderTagBar();loadFlows()",
	} {
		if !strings.Contains(src, contract) {
			t.Errorf("saved views missing restoration contract %q", contract)
		}
	}
}

func TestUIProxyAsyncSurfacesRetainExactOwners(t *testing.T) {
	proxy := requireUIContracts(t, "js/proxy.js",
		"const current=()=>epoch===wsReplayEpoch&&selectFlowEpoch===selectionEpoch&&state.selId===flowId&&state.detail===detail&&out.isConnected&&button.isConnected&&$('#wsReplayOut')===out&&$('#wsSendBtn')===button",
		"if(!out||!button||button.disabled)return",
		"if(!current())return",
		"function compareModalOpen(modal,box)",
		"state.selected.size===2",
		"onEscape:closeCompare,onDismiss:closeCompare",
		"aria-pressed=\"${mode==='words'?'true':'false'}\"",
		"if(restoreModeFocus)requestAnimationFrame",
		"flowSearchEditEpoch",
		"flowSearchTestEpoch",
		"flowSearchTestPending",
		"edited while save pending — save again",
	)
	if strings.Count(proxy, "if(!current())return") < 3 {
		t.Error("compare and WebSocket replay must guard every awaited ownership boundary")
	}
}

func TestUIAuthzModalOwnsLoadsEditsAndActions(t *testing.T) {
	authz := requireUIContracts(t, "js/authz.js",
		"let rules=[]",
		"rules=d.rules||[]",
		"let authzActionFocus=null",
		"authzIdentityLoadEpoch",
		"authzIdentityEditEpoch",
		"authzIdentityMutationTail",
		"onEscape:closeAuthz,onDismiss:closeAuthz",
		"'#authzRun', '#authzCheck', '#authzSave', '#authzFromFlow', '#authzAdd', '#authzClose', '#authzScopeEdit'",
		"status.focus({preventScroll:true})",
		"restore.focus({preventScroll:true})",
		"function setAuthzStatus(message,kind='status')",
		"kind==='error'?'alert':'status'",
		"kind==='error'?'assertive':'polite'",
		"function openSettingsScope(){\n  if(authzActionBusy)return",
		"setAuthzActionBusy(authzActionBusy)",
		"setAuthzStatus('Loading captured authentication…')",
		"function closeAuthz(){\n  if(authzActionBusy)return",
		"function authzActionCurrent(epoch,mode,target,requiresTarget=true)",
		"$('#authzIds')?.querySelectorAll('input,textarea,button')",
		"finally{if(epoch===authzRunEpoch)setAuthzActionBusy(false);}",
	)
	for _, message := range []string{
		"Loading captured authentication failed: ",
		"Session check failed: ",
		"Cross-host replay failed: ",
		"Saving identities failed: ",
		"Authorization replay failed: ",
	} {
		if !strings.Contains(authz, "setAuthzStatus('"+message+"'+e.message,'error')") {
			t.Errorf("Authz failure must use assertive status semantics: missing %q", message)
		}
	}
	if strings.Contains(authz, "state.scope=d.rules||[]") {
		t.Fatal("authorization scope preview must not overwrite Settings target-scope state")
	}
	if strings.Count(authz, "finally{if(epoch===authzRunEpoch)setAuthzActionBusy(false);}") < 5 {
		t.Error("every Authz action must keep stale completions from enabling a newer action")
	}
	if strings.Count(authz, "if(!authzActionCurrent(") < 8 {
		t.Error("Authz actions must guard intermediate, success, and error effects")
	}
}

func TestUIClearAllResetsEveryVisibleHistoryFilter(t *testing.T) {
	requireUIContracts(t, "js/proxy.js",
		"state.inScopeOnly=false;state.hideTlsFailed=false",
		"localStorage.setItem(HIDE_TLS_KEY,'0')",
		"syncSourceFilters();syncHideTlsFilter()",
		"syncControls();renderChips();renderTagBar();loadFlows()",
	)
	if !strings.Contains(executableJS(readUIAsset(t, "js/proxy.js")), "state.notesOnly||state.inScopeOnly||!state.showManual||!state.showAI") {
		t.Fatal("history filter detection must include the scope-only filter")
	}
}

func TestUIWebSocketFramesUseEachEndpointContract(t *testing.T) {
	// Captured frames come from store.WSFrame (length/preview), while the
	// one-shot replay endpoint returns wsrepeater.Frame (len/text). Keeping the
	// two mappings explicit prevents a superficially tempting but breaking
	// normalization in the inspector.
	requireUIContracts(t, "js/proxy.js",
		"frames.map(f=>wsFrameRow(f.dir,f.opcode,f.length,f.preview))",
		"frames.map(f=>wsFrameRow(f.dir,f.opcode,f.len,f.text))",
	)
}
