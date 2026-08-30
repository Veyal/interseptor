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

	saved := strings.Index(handler, "await saveSessionAll(submitted)")
	acknowledged := strings.Index(handler, "saveAcknowledged=true")
	run := strings.Index(handler, "await api('/api/session/login/run'")
	if saved < 0 || acknowledged < 0 || run < 0 || !(saved < acknowledged && acknowledged < run) {
		t.Error("Session reconciliation must distinguish an acknowledged save from a failed login run")
	}
}
