package control

import (
	"strings"
	"testing"
)

// A filter reload can replace the History page cache while a selected flow's
// detail request is still in flight. The response belongs to the selection and
// the filter generation that started it; it must not paint after a newer filter
// generation has made that selection stale.
func TestUIProxyInspectorRejectsDetailAfterFilterReload(t *testing.T) {
	source := readUIAsset(t, "js/proxy.js")
	proxy := executableJS(source)
	for _, contract := range []string{
		"let flowFilterEpoch=0",
		"let flowFilterSignature=''",
		"let flowFilterReconciledEpoch=0",
		"function inspectorFilterSignature()",
		"if(signatureChanged){flowFilterSignature=filterSignature;flowFilterEpoch++;}",
		"const filterChanged=filterEpoch!==flowFilterReconciledEpoch",
		"flowFilterReconciledEpoch=filterEpoch",
		"const filterEpoch=flowFilterEpoch",
		"flowFilterEpoch===filterEpoch",
		"function showInspectorFilterLoadError(id,error)",
		"function showInspectorSelectionUnavailable(id)",
		"if(!state.detail&&previousFlow&&canIncremental()&&flowMatchesFilters(previousFlow))",
		"if(!state.detail&&flowStore.byId.has(state.selId)){selectFlow(state.selId);return;}",
		"if(!state.detail&&!flowStore.byId.has(state.selId)&&canIncremental()&&!previousFlow){selectFlow(state.selId);return;}",
		"selectFlow(state.selId)",
		"if(!canIncremental()&&!flowStore.byId.has(state.selId))",
		"showInspectorSelectionUnavailable(state.selId)",
		"showInspectorFilterLoadError(previousSelected,e)",
		"if(filterChanged&&state.detail)selectFlow(state.selId)",
		"else if(previousFlow&&canIncremental()&&filterChanged&&state.detail)selectFlow(state.selId)",
	} {
		if !strings.Contains(proxy, contract) {
			t.Errorf("Inspector/filter ownership contract missing %q", contract)
		}
	}
	if strings.Contains(proxy, "const filterEpoch=++flowFilterEpoch") {
		t.Error("background History refreshes must not invalidate Inspector subloads")
	}
	if !strings.Contains(proxy, "const current=()=>selectFlowEpoch===selectEpoch&&state.selId===id&&flowFilterEpoch===filterEpoch") {
		t.Error("selected-flow detail responses must be rejected after a newer filter reload")
	}
	if !strings.Contains(proxy, "if(canIncremental()&&!flowMatchesFilters(d)){closeInspector();return;}") {
		t.Error("client-decidable filters must be revalidated before painting returned Inspector detail")
	}
	if !strings.Contains(proxy, "if(epoch===flowLoadEpoch&&state.selId===previousSelected&&previousSelected!=null&&!state.detail){state.detail=null;showInspectorFilterLoadError(previousSelected,e);") {
		t.Error("a failed filter reload must replace pending Inspector content with a concrete retry state")
	}
	if !strings.Contains(source, "Preserve\n    // already-loaded detail when a background refresh fails") {
		t.Error("failed background refreshes must not erase already-loaded Inspector detail")
	}
}

func TestUIProxyInspectorDoesNotRepaintForUnknownServerFilterMatch(t *testing.T) {
	proxy := readUIAsset(t, "js/proxy.js")
	for _, contract := range []string{
		"// Server-only filters (text search and in-scope matching) cannot be",
		"If the replacement page",
		"// does not contain the selected flow, do not repaint retained detail as if",
		"showInspectorSelectionUnavailable(state.selId)",
	} {
		if !strings.Contains(proxy, contract) {
			t.Errorf("server-only Inspector ownership contract missing %q", contract)
		}
	}
}
