package control

import (
	"strings"
	"testing"
)

func TestUIIntruderHistoryOwnsDisplayedResultsSeparatelyFromActiveRun(t *testing.T) {
	tools := executableJS(readUIAsset(t, "js/tools.js"))
	for _, contract := range []string{
		"let intrFilter='all', intrLastResults=[], intrDisplayedResults=[]",
		"intrDisplayedResults=h.results.slice()",
		"intrDisplayedResults=res.slice()",
		"const pool=intrApplyFilter(intrDisplayedResults)",
	} {
		if !strings.Contains(tools, contract) {
			t.Errorf("Intruder display ownership contract missing %q", contract)
		}
	}
}

func TestUIIntruderHistoryDoesNotOverwriteAuthoritativeRunLifecycle(t *testing.T) {
	tools := executableJS(readUIAsset(t, "js/tools.js"))
	start := strings.Index(tools, "function intrLoadHistory(i)")
	end := strings.Index(tools[start:], "$('#intrHistToggle')")
	if start < 0 || end < 0 {
		t.Fatal("Intruder history loader not found")
	}
	historyLoader := tools[start : start+end]
	if strings.Contains(historyLoader, "intrLastResults=") || strings.Contains(historyLoader, "intrLastRunning=") {
		t.Error("opening Intruder history must not replace the active run snapshot")
	}
	if !strings.Contains(historyLoader, "intrDisplayedResults=h.results.slice()") {
		t.Error("opening Intruder history must establish the displayed result snapshot")
	}
}

func TestUIIntruderPollFailurePreservesDisplayedResultOwnership(t *testing.T) {
	tools := executableJS(readUIAsset(t, "js/tools.js"))
	if !strings.Contains(tools, "results:intrDisplayedResults,pollFailed:true") {
		t.Fatal("a failed active-run poll must preserve the result snapshot currently displayed to the operator")
	}
	if strings.Contains(tools, "results:intrLastResults,pollFailed:true") {
		t.Fatal("a failed active-run poll must not replace a selected history view with authoritative run rows")
	}
}
