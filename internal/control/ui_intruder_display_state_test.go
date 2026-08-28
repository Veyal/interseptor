package control

import (
	"strings"
	"testing"
)

func TestUIIntruderHistoryOwnsDisplayedResultsSeparatelyFromActiveRun(t *testing.T) {
	tools := executableJS(readUIAsset(t, "js/tools.js"))
	for _, contract := range []string{
		"let intrFilter='all', intrLastResults=[], intrDisplayedResults=[], intrDisplayOwner='live', intrDisplayedTarget=''",
		"intrDisplayOwner='history'",
		"intrDisplayedTarget=h.target||''",
		"intrDisplayedResults=h.results.slice()",
		"if(intrDisplayOwner==='live'){",
		"intrDisplayedResults=res.slice()",
		"const pool=intrApplyFilter(intrDisplayedResults)",
		"const displayTarget=intrDisplayedTarget||$('#intrTarget').value||''",
		"target:displayTarget",
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
	if !strings.Contains(historyLoader, "if(h.cfg&&!intrLastRunning&&!intrStartPending)") {
		t.Error("opening Intruder history during a live run must not persist historical configuration into its locked tab")
	}
	if strings.Contains(historyLoader, "if(h.cfg){") {
		t.Error("Intruder history configuration restoration must be gated by active-run ownership")
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

func TestUIIntruderPollingRejectsOutOfOrderResponses(t *testing.T) {
	tools := executableJS(readUIAsset(t, "js/tools.js"))
	for _, contract := range []string{
		"let intrPollEpoch=0",
		"let intrPollInFlight=false,intrPollQueued=false",
		"if(intrPollInFlight){intrPollQueued=true;return;}",
		"const epoch=++intrPollEpoch",
		"if(epoch!==intrPollEpoch)return",
		"const started=await api('/api/intruder/start'",
		"if(pollEpoch===intrPollEpoch)renderIntr(started)",
	} {
		if !strings.Contains(tools, contract) {
			t.Errorf("Intruder response-order contract missing %q", contract)
		}
	}
	if strings.Count(tools, "if(epoch!==intrPollEpoch)return") < 2 {
		t.Error("Intruder poll success and failure must both reject stale responses")
	}
}
