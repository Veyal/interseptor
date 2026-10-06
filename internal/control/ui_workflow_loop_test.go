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
