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
		"authzIdentityLoadEpoch",
		"authzIdentityEditEpoch",
		"authzIdentityMutationTail",
		"onEscape:closeAuthz,onDismiss:closeAuthz",
		"function authzActionCurrent(epoch,mode,target,requiresTarget=true)",
		"$('#authzIds')?.querySelectorAll('input,textarea,button')",
		"finally{if(epoch===authzRunEpoch)setAuthzActionBusy(false);}",
	)
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
