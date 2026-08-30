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
		"if(typeof onClose==='function')Promise.resolve().then(()=>onClose(closed)).catch(()=>{});",
	} {
		if !strings.Contains(core, contract) {
			t.Errorf("tab close lifecycle contract missing %q", contract)
		}
	}
	closedMark := strings.Index(core, "closed._closed=true")
	closeHook := strings.Index(core, "onClose(closed)")
	if closedMark < 0 || closeHook < 0 || closedMark > closeHook {
		t.Error("tab close must mark the detached tab before invoking asynchronous cleanup")
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
	} {
		if !strings.Contains(tools, contract) {
			t.Errorf("Repeater close-vs-late-write contract missing %q", contract)
		}
	}

	if !strings.Contains(tools, "return await repHistoryOperation(t,async()=>{") {
		t.Error("Repeater history cleanup must be serialized with outstanding writers")
	}
}
