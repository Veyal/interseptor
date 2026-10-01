package control

import (
	"strings"
	"testing"
)

func TestFindingsEvidenceFirstWorkspaceContracts(t *testing.T) {
	index := readUIAsset(t, "index.html")
	findings := readUIAsset(t, "js/findings.js")
	app := readUIAsset(t, "js/app.js")
	css := readUIAsset(t, "app.css")
	if strings.Contains(index, `<option value="pdf">`) {
		t.Error("findings export must not advertise PDF when the browser cannot produce it")
	}
	if !strings.Contains(index, `<option value="html">HTML with images</option>`) {
		t.Error("findings export must identify self-contained HTML as the image-preserving handoff")
	}
	for _, want := range []string{`<nav id="findList"`, `id="findEvidenceRail"`, `id="findNarrativePreset"`, `id="findBackToList"`} {
		if !strings.Contains(index, want) && !strings.Contains(findings, want) {
			t.Errorf("missing findings evidence-first contract %q", want)
		}
	}
	for _, want := range []string{"aria-current", "Differential proof", "Input → Result", "Exposure proof", "Control failure", "find-evidence-screenshot", "find-attach-flow", "find-save-state", "paste", "What this proves", "Proof annotation needed", "sourceFlowId", "find-block-role"} {
		if !strings.Contains(findings, want) {
			t.Errorf("findings UI missing %q", want)
		}
	}
	for _, want := range []string{"function findingReadiness", "f?.readiness", "report_ready", "Add outline", "role: 'result', source: 'operator_upload'", "Choose role…", "role: 'observation'", "role: 'result', source: 'captured_flow'"} {
		if !strings.Contains(findings, want) {
			t.Errorf("findings UI missing structured workflow contract %q", want)
		}
	}
	if strings.Contains(findings, "r === (selected || 'observation')") {
		t.Error("missing evidence roles must not be rendered as if observation were persisted")
	}
	if !strings.Contains(findings, "async function settleFindingBodyBeforeEvidence") || strings.Count(findings, "await settleFindingBodyBeforeEvidence(") < 3 {
		t.Error("evidence attachment paths must flush pending body snapshots before mutating the finding")
	}
	if !strings.Contains(findings, "function captureActiveFindingTextEditor") ||
		!strings.Contains(findings, "captureActiveFindingTextEditor(fid);\n  await flushPendingBodySave(fid)") {
		t.Error("evidence attachment must capture the focused reproduction textarea before replacing the editor")
	}
	if !strings.Contains(findings, "function applyFindingEvidenceResponse") || strings.Count(findings, "applyFindingEvidenceResponse(") < 4 {
		t.Error("evidence attachment paths must apply the authoritative response even while editor focus defers SSE refreshes")
	}
	if !strings.Contains(findings, "if(el.tagName==='SELECT')el.addEventListener('change',commit)") {
		t.Error("finding select fields must persist on change without depending on a later focus transition")
	}
	if !strings.Contains(findings, ".find-block-proof').forEach(inp => inp.addEventListener('input'") {
		t.Error("evidence proof must update the pending body snapshot while typing so Done cannot miss the final focused value")
	}
	if strings.Contains(findings, "const gaps = f.missing || []") {
		t.Error("findings UI must render canonical structured readiness, not legacy before/after completeness")
	}
	if strings.Contains(findings, "Label Before → Action → After") {
		t.Error("findings empty states must not present differential proof as the universal format")
	}
	if strings.Contains(findings, "const verifBanner = (f.status === 'needs_verification' || f.verificationInstructions)") {
		t.Error("verification instructions must not label an open finding as needing verification")
	}
	flowPickerStart := strings.Index(findings, "function renderFlowPickList")
	flowPickerEnd := strings.Index(findings, "async function openFlowPickForFinding")
	if flowPickerStart < 0 || flowPickerEnd <= flowPickerStart {
		t.Fatal("missing flow picker renderer")
	}
	flowPicker := findings[flowPickerStart:flowPickerEnd]
	if strings.Contains(flowPicker, "el.onclick") {
		t.Error("flow picker labels must rely on the checkbox change event so row text does not toggle twice")
	}
	if !strings.Contains(flowPicker, "el.querySelector('input').onchange = () => toggle()") {
		t.Error("flow picker selection must remain available to pointer and keyboard users through checkbox change")
	}
	if !strings.Contains(findings, "export function handleAppHash") || !strings.Contains(app, "restoreTab();\n    handleAppHash();") {
		t.Error("finding deep links must be replayed after project-scoped UI hydration")
	}
	reportStart := strings.Index(findings, "function renderFindReportBody")
	if reportStart < 0 {
		t.Fatal("missing finding report renderer")
	}
	textBranch := findings[reportStart:]
	imageStart := strings.Index(textBranch, "if (b.type === 'image')")
	if imageStart < 0 {
		t.Fatal("missing image report branch")
	}
	flowStart := strings.Index(textBranch[imageStart:], "if (b.type === 'flow')")
	if flowStart < 0 || !strings.Contains(textBranch[imageStart:imageStart+flowStart], "b.source") {
		t.Error("screenshot read view must preserve visible evidence provenance")
	}
	if strings.Contains(textBranch[:imageStart], "Proof annotation needed") {
		t.Error("text reproduction steps must not demand proof annotations")
	}
	for _, want := range []string{".find-evidence-rail", ".find-mobile-back", ".find-readiness", ".find-row.sel"} {
		if !strings.Contains(css, want) {
			t.Errorf("findings CSS missing %q", want)
		}
	}
	compactDesktopStart := strings.Index(css, "@media (min-width:901px) and (max-width:1100px)")
	if compactDesktopStart < 0 {
		t.Fatal("findings CSS must define a compact-desktop breakpoint")
	}
	compactDesktop := css[compactDesktopStart:]
	if compactDesktopEnd := strings.Index(compactDesktop[1:], "\n@media "); compactDesktopEnd >= 0 {
		compactDesktop = compactDesktop[:compactDesktopEnd+1]
	}
	if !strings.Contains(compactDesktop, ".findings-toolbar-actions{order:2;flex:1 1 100%;width:100%;flex-wrap:wrap}") {
		t.Error("findings toolbar must wrap its actions before they collide at compact desktop widths")
	}
	if !strings.Contains(compactDesktop, ".find-header-top{display:grid;grid-template-columns:auto auto 1fr auto auto") ||
		!strings.Contains(compactDesktop, ".find-header-top .find-title-text{grid-column:1/-1") ||
		!strings.Contains(compactDesktop, ".find-header-top #findToggleEdit{grid-column:5;grid-row:1}") {
		t.Error("finding titles must receive a full header row at compact desktop widths")
	}
}
