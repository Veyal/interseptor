package control

import (
	"strings"
	"testing"
)

func TestUILoginMacroRunReconcilesSessionAfterAcknowledgedSaveFailure(t *testing.T) {
	src := readUIAsset(t, "js/settings.js")
	start := strings.Index(src, "if($('#loginMacroRun'))")
	end := strings.Index(src, "// Test = dry-run")
	if start < 0 || end <= start {
		t.Fatal("login macro run handler not found")
	}
	handler := src[start:end]
	for _, contract := range []string{
		"let saveAcknowledged=false",
		"saveAcknowledged=true",
		"finally",
		"if(saveAcknowledged)await loadSession();",
	} {
		if !strings.Contains(handler, contract) {
			t.Errorf("login macro run must reconcile Session after a saved run fails: missing %q", contract)
		}
	}

	saved := strings.Index(handler, "await runLoginMacroWithSession(submitted")
	acknowledged := strings.Index(handler, "saveAcknowledged=true")
	if saved < 0 || acknowledged < 0 || saved > acknowledged {
		t.Error("Session reconciliation must be notified when the queued save is acknowledged")
	}
}
