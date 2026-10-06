package control

import (
	"strings"
	"testing"
)

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

func TestUIFindingCreationStaysInPlaceAndOffersOpen(t *testing.T) {
	findings := readUIAsset(t, "js/findings.js")
	requireUIContains(t, findings,
		"export function flowFindingDefaults()",
		"export function toastOpenFinding(message, sev, findingId)",
		"open.onclick = () => { el.remove(); openFinding(findingId); };",
		"export function pickFindingForFlows(ids, opts = {})",
		"＋ Add to a new finding",
		"Add ${ids.length} flow",
		"if (!target) { try { target = flowOrigin(await api('/api/flows/' + ids[0]));",
		"if (result && result.attached) addOpenFindingAction(id);",
	)
	start := strings.Index(findings, "async function createFindingFromFlows(")
	end := strings.Index(findings, "export function pickFindingForFlows(")
	if start < 0 || end < start {
		t.Fatal("createFindingFromFlows must precede pickFindingForFlows")
	}
	if strings.Contains(findings[start:end], "data-tab=\"findings\"") {
		t.Error("creating a finding from flows must not jump to the Findings tab")
	}
	pickStart := end
	pickEnd := strings.Index(findings[pickStart:], "$('#fpClose')")
	if strings.Contains(findings[pickStart:pickStart+pickEnd], "data-tab=\"findings\"") {
		t.Error("adding flows to a finding must not jump to the Findings tab")
	}
}

func TestUIIntruderToFindingSharesThePickerAndExplainsSelection(t *testing.T) {
	tools := readUIAsset(t, "js/tools.js")
	requireUIContains(t, tools,
		"function intrFindingSelection(pool)",
		"Nothing was flagged or interesting, so the first",
		"only the first ${INTR_FINDING_FLOW_CAP} are attached",
		"m.pickFindingForFlows(flowIds,{",
		"button.textContent='→ Finding'",
	)
	if strings.Contains(tools, "button.textContent='To Finding'") || strings.Contains(tools, "Create finding from Intruder") {
		t.Error("Intruder must use the shared Add-to-finding picker and the → Finding label")
	}
}
