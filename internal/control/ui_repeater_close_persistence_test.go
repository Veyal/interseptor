package control

import (
	"strings"
	"testing"
)

func TestUIRepeaterCloseSuppressesLateHistoryWrites(t *testing.T) {
	core := readUIAsset(t, "js/core.js")
	tools := readUIAsset(t, "js/tools.js")

	for _, contract := range []string{
		"closed._closed=true",
		"try{closeResult=onClose(closed);}",
		"mgr.tabs.splice(i,1)",
	} {
		if !strings.Contains(core, contract) {
			t.Errorf("tab close lifecycle contract missing %q", contract)
		}
	}
	closedMark := strings.Index(core, "closed._closed=true")
	closeHook := strings.Index(core, "closeResult=onClose(closed)")
	removeTab := strings.Index(core, "mgr.tabs.splice(i,1)")
	if closedMark < 0 || closeHook < 0 || removeTab < 0 || closedMark > closeHook || closeHook > removeTab {
		t.Error("tab close must durably register cleanup before discarding tab metadata")
	}

	for _, contract := range []string{
		"function repHistoryOperation(t,work,allowClosed=false)",
		"const previous=t.historyOperation||Promise.resolve()",
		"if(!t||(!allowClosed&&t._closed))return Promise.resolve(false)",
		"t.historyOperation=next.catch(()=>{})",
		"return repHistoryOperation(t,async()=>",
		"if(t._closed)return false",
		"return repHistoryOperation(t,async()=>{",
		"if(!t._closed)return;",
		"function repMarkHistoryCleanup(t)",
		"function repRetryHistoryCleanup(openTabs)",
		"const tabKey=repMarkHistoryCleanup(t)",
		"await repRetryHistoryCleanup(repTabs.tabs)",
		"repClearHistoryCleanup(tabKey)",
	} {
		if !strings.Contains(tools, contract) {
			t.Errorf("Repeater close-vs-late-write contract missing %q", contract)
		}
	}

	if !strings.Contains(tools, "return repHistoryOperation(t,async()=>{") {
		t.Error("Repeater history cleanup must be serialized with outstanding writers")
	}
}

func TestUIRepeaterCleanupLedgerFailureDoesNotBlockInit(t *testing.T) {
	tools := readUIAsset(t, "js/tools.js")
	start := strings.Index(tools, "async function repRetryHistoryCleanup(openTabs)")
	end := strings.Index(tools, "async function repHydrateTabHistory(t)")
	if start < 0 || end <= start {
		t.Fatal("Repeater cleanup retry function not found")
	}
	retry := tools[start:end]
	for _, contract := range []string{
		"let cleanupKeys",
		"try{cleanupKeys=repHistoryCleanupKeys();}catch(e){return;}",
		"try{repClearHistoryCleanup(tabKey);}catch(e){}",
	} {
		if !strings.Contains(retry, contract) {
			t.Errorf("Repeater cleanup retry must tolerate storage failure: missing %q", contract)
		}
	}
}
