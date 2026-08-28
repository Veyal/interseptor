package control

import (
	"strings"
	"testing"
)

func TestUIHistorySnapshotReplayCoalescesFlowsAndRecoversOverflow(t *testing.T) {
	proxy := executableJS(readUIAsset(t, "js/proxy.js"))
	for _, contract := range []string{
		"let flowLoadEvents=new Map()",
		"let flowLoadOverflow=false",
		"const previous=flowLoadEvents.get(flow.id)",
		"flowLoadEvents.delete(flow.id)",
		"flowLoadEvents.set(flow.id,{kind:replayKind,flow})",
		"if(flowLoadEvents.size>MAX_LIVE_FLOWS)",
		"flowLoadOverflow=true",
		"flowLoadEvents.delete(flowLoadEvents.keys().next().value)",
		"const replay=Array.from(flowLoadEvents.values())",
		"const replayOverflow=flowLoadOverflow",
		"let replayExact=!replayOverflow",
		"if(!replayExact)scheduleReload()",
	} {
		if !strings.Contains(proxy, contract) {
			t.Errorf("History replay exactness contract missing %q", contract)
		}
	}
	if strings.Contains(proxy, "flowLoadEvents.splice(") {
		t.Error("History replay must not silently discard raw live events without marking the snapshot inexact")
	}
}
