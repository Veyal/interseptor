package control

import (
	"strings"
	"testing"
)

func TestUIRepeaterHistoryIsOwnedAndPersistedByTab(t *testing.T) {
	tools := readUIAsset(t, "js/tools.js")

	for _, contract := range []string{
		"const REP_HISTORY_RENDER_BATCH=100",
		"history:[]",
		"historyNeedsMigration:false",
		"function normalizeRepHistory(",
		"function normalizeRepeaterTab(t)",
		"function serializeRepeaterTab(t)",
		"function repRecordHistory(t,flow)",
		"repRecordHistory(t,flow)",
		"history:normalizeRepHistory(t.history)",
		"historyNeedsMigration:!!t.historyNeedsMigration",
		"const flows=normalizeRepHistory(t.history)",
	} {
		if !strings.Contains(tools, contract) {
			t.Errorf("tab-owned Repeater history contract missing %q", contract)
		}
	}

	loadStart := strings.Index(tools, "export async function loadRepHistory()")
	loadEnd := -1
	if loadStart >= 0 {
		if rel := strings.Index(tools[loadStart:], "// Toggle the per-tab history rail"); rel >= 0 {
			loadEnd = loadStart + rel
		}
	}
	if loadStart < 0 || loadEnd < 0 {
		t.Fatal("Repeater history renderer not found")
	}
	loader := tools[loadStart:loadEnd]
	if strings.Contains(loader, "repTabEndpointParts(t)") || strings.Contains(loader, "new URLSearchParams") {
		t.Error("normal Repeater history rendering must not derive ownership from the mutable request URL")
	}

	urlWireStart := strings.Index(tools, "['#repMethod','#repUrl'].forEach")
	urlWireEnd := -1
	if urlWireStart >= 0 {
		if rel := strings.Index(tools[urlWireStart:], "['#repHeaders','#repBody'].forEach"); rel >= 0 {
			urlWireEnd = urlWireStart + rel
		}
	}
	if urlWireStart < 0 || urlWireEnd < 0 {
		t.Fatal("Repeater URL editor wiring not found")
	}
	if strings.Contains(tools[urlWireStart:urlWireEnd], ".history=") {
		t.Error("editing the Repeater method or URL must not replace the tab's history")
	}
}

func TestUIRepeaterHistoryMigrationIsOneShotAndRaceSafe(t *testing.T) {
	tools := readUIAsset(t, "js/tools.js")

	for _, contract := range []string{
		"async function migrateLegacyRepHistory(t)",
		"if(!t.historyNeedsMigration)return true",
		"if(t.historyMigrationPromise)return t.historyMigrationPromise",
		"const legacyURL=t.historyLegacyURL||t.url",
		"t.historyMigrationPromise=(async()=>",
		"t.history=normalizeRepHistory([...(t.history||[]),...(d.flows||[])])",
		"t.historyNeedsMigration=false",
		"t.historyLegacyURL=''",
		"t.historyMigrationPromise=null",
		"'/api/repeater/history?'+params.toString()",
	} {
		if !strings.Contains(tools, contract) {
			t.Errorf("legacy Repeater history migration contract missing %q", contract)
		}
	}
}

func TestUIRepeaterHistoryRetainsTabLifetimeAndPaginatesRendering(t *testing.T) {
	tools := readUIAsset(t, "js/tools.js")

	for _, contract := range []string{
		"normalizeRepHistory([repHistoryEntry(flow),...(t.history||[])])",
		"const visibleCount=Math.min(flows.length,Math.max(REP_HISTORY_RENDER_BATCH,Number(t.historyVisibleCount)||0))",
		"const visible=flows.slice(0,visibleCount)",
		"data-rep-history-more",
		"t.historyVisibleCount=Math.min(flows.length,visibleCount+REP_HISTORY_RENDER_BATCH)",
		"History item #'+id+' is no longer available",
	} {
		if !strings.Contains(tools, contract) {
			t.Errorf("tab-lifetime Repeater history contract missing %q", contract)
		}
	}
	if strings.Contains(tools, "if(out.length>=REP_TAB_HISTORY_LIMIT)break") {
		t.Error("Repeater history must not silently discard entries while its owning tab remains open")
	}
}

func TestUIRepeaterHistorySelectionRejectsStaleLoads(t *testing.T) {
	tools := readUIAsset(t, "js/tools.js")
	start := strings.Index(tools, "export async function repLoadSend(id)")
	end := strings.Index(tools, "export async function sendToRepeater(f)")
	if start < 0 || end < 0 || end <= start {
		t.Fatal("Repeater history selection loader not found")
	}
	loader := tools[start:end]
	for _, contract := range []string{
		"t.historyLoadEpoch=(t.historyLoadEpoch||0)+1",
		"const loadEpoch=t.historyLoadEpoch",
		"const editorEpoch=t.reqEditEpoch||0",
		"const current=()=>repCur()===t&&t.historyLoadEpoch===loadEpoch&&(t.reqEditEpoch||0)===editorEpoch",
		"if(current())toast('History item #'+id+' is no longer available",
	} {
		if !strings.Contains(loader, contract) {
			t.Errorf("latest Repeater history selection contract missing %q", contract)
		}
	}
	if strings.Count(loader, "if(!current())return") < 2 {
		t.Error("Repeater history selection must reject stale metadata and raw-request completions")
	}
}
