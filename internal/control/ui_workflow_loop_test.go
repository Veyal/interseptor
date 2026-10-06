package control

import "testing"

// Workflow-loop contracts: moving a request between Intercept, Repeater,
// Intruder and Findings must never destroy work or strand the operator.
func TestUISendToIntruderPreservesConfiguredAttackTabs(t *testing.T) {
	tools := readUIAsset(t, "js/tools.js")
	requireUIContains(t, tools,
		"function intrTabPristine(t)",
		"function intrTabForLoad(forceNew=false)",
		"if(!forceNew&&intrTabPristine(cur))return cur;",
		"const tab=intrTabs.create();",
		"const loadTab=intrTabForLoad(editorMoved);",
		"let intrLoadActionEpoch=0",
		"toastError('Send to Intruder failed',e)",
	)
}

func TestUIRepeaterResponsePaneOffersIntruderFindingAndCurl(t *testing.T) {
	tools := readUIAsset(t, "js/tools.js")
	requireUIContains(t, tools,
		"function wireRepeaterActions()",
		"id=\"repToIntruder\"",
		"id=\"repAddFinding\"",
		"id=\"repCopyCurl\"",
		"function repBuildRaw(t)",
		"function repCurlCommand(built)",
		"btn.disabled=!t||!t.resId||!!t.sendPending",
		"m.addFlowToFinding(t.resId)",
		"wireRepeaterActions();\n  resolveRepeaterReady(hydration)",
	)
	css := readUIAsset(t, "app.css")
	requireUIContains(t, css, ".rep-actions{", ".rep-res .pane-head{")
}

func TestUIInterceptHeldRequestCanOpenInRepeaterWithoutForwarding(t *testing.T) {
	intercept := readUIAsset(t, "js/intercept.js")
	requireUIContains(t, intercept,
		"import { sendRawToRepeater } from './tools.js'",
		"id='heldRepeaterBtn'",
		"sendRawToRepeater({scheme:h.scheme,host:h.host,raw:$('#heldRaw').value})",
		"b.hidden=side==='resp'",
		"'#heldRepeaterBtn']",
	)
	if containsAny(intercept, "style.cssText", "style.marginLeft") {
		t.Error("intercept.js must use shared classes instead of inline style writes for held-load state")
	}
	tools := readUIAsset(t, "js/tools.js")
	requireUIContains(t, tools,
		"export function parseRawRequest(raw)",
		"export async function sendRawToRepeater({scheme,host,raw,label})",
		"const t=repNewTab();if(!t)return false;",
	)
}
