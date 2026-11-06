package control

import (
	"strings"
	"testing"
)

// A response diff panel belongs to the tab/attempt it was opened for. Leaving it
// mounted across a tab switch hid the response pane while the status line
// described the new tab's response, so both loaders must close it first.
func TestUIDiffPanelsCloseOnTabSwitch(t *testing.T) {
	tools := readUIAsset(t, "js/tools.js")
	intr := readUIAsset(t, "js/intruder.js")

	for _, want := range []string{
		"if(repExtras)repExtras.closeDiff();",
		"intrExtras?.closeDiff?.();",
	} {
		if !strings.Contains(tools, want) {
			t.Errorf("tools.js is missing the diff-panel reset %q", want)
		}
	}
	if !strings.Contains(intr, "closeDiff: () => diffPanel.close(") {
		t.Error("intruder.js must expose closeDiff so the loader can reset its diff panel")
	}
}

// The "severity matches score" gap used to focus #findSeverity, an element the
// detail pane no longer renders (severity follows the CVSS editor), so the link
// focused nothing. It now points at the CVSS editor, and the dead blur binding
// is gone.
func TestUIFindingSeverityGapTargetsAnExistingControl(t *testing.T) {
	findings := readUIAsset(t, "js/findings.js")
	if strings.Contains(findings, "'#findSeverity'") {
		t.Error("findings.js still binds or targets the removed #findSeverity control")
	}
	if !strings.Contains(findings, "severity:'findCvss'") {
		t.Error("the severity gap must point at the CVSS editor, the only severity control in the detail pane")
	}
}
