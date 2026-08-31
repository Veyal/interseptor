package control

import (
	"strings"
	"testing"
)

func TestUISavedSearchSourceRejectsStaleSelection(t *testing.T) {
	proxy := readUIAsset(t, "js/proxy.js")
	for _, contract := range []string{
		"let flowSearchSourceEpoch=0",
		"const epoch=++flowSearchSourceEpoch",
		"const current=()=>epoch===flowSearchSourceEpoch&&$('#flowSearchScriptList')?.value===name",
		"if(!current())return",
		"if(current())flowSearchStatus(e.message,true)",
		"addEventListener('input',()=>{flowSearchSourceEpoch++;flowSearchEditEpoch++;",
		"++flowSearchSourceEpoch",
	} {
		if !strings.Contains(proxy, contract) {
			t.Errorf("saved-search selection ownership contract missing %q", contract)
		}
	}
}

func TestUIRuleAndScopeAddsAreAcknowledgementGated(t *testing.T) {
	for _, tc := range []struct {
		name      string
		path      string
		contracts []string
	}{
		{"interception rules", "js/intercept.js", []string{"let ruleAddInFlight=false", "if(ruleAddInFlight)return", "b.disabled=stateName==='pending'", "b.setAttribute('aria-busy'", "setRuleAddState('pending')"}},
		{"scope rules", "js/proxy.js", []string{"let scopeAddInFlight=false", "if(scopeAddInFlight)return", "b.disabled=stateName==='pending'", "b.setAttribute('aria-busy'", "setScopeAddState('pending')"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := readUIAsset(t, tc.path)
			for _, contract := range tc.contracts {
				if !strings.Contains(src, contract) {
					t.Errorf("duplicate-submit guard missing %q", contract)
				}
			}
		})
	}
}

func TestUIInterceptDecodeKeepsSelectionAndRawSnapshot(t *testing.T) {
	intercept := readUIAsset(t, "js/intercept.js")
	for _, contract := range []string{
		"let heldDecodeEpoch=0",
		"const decodeEpoch=++heldDecodeEpoch",
		"const selectionKey=heldKey(sel.side,sel.id)",
		"heldDecodeCurrent(decodeEpoch,selectionKey,raw)",
		"heldDecodeEpoch++",
	} {
		if !strings.Contains(intercept, contract) {
			t.Errorf("intercept decode ownership contract missing %q", contract)
		}
	}
}
