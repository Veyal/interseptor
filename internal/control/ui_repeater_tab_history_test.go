package control

import (
	"strings"
	"testing"
)

func TestUIRepeaterHistoryIsOwnedAndPersistedByTab(t *testing.T) {
	tools := readUIAsset(t, "js/tools.js")

	for _, contract := range []string{
		"const REP_HISTORY_RENDER_BATCH=100",
		"const REP_HISTORY_DB_NAME='interseptor-repeater-history'",
		"history:[]",
		"historyKey:newRepHistoryKey()",
		"historyNeedsMigration:false",
		"function normalizeRepHistory(",
		"function normalizeRepeaterTab(t)",
		"function serializeRepeaterTab(t)",
		"async function repRecordHistory(t,flow)",
		"await repRecordHistory(t,flow)",
		"historyKey:t.historyKey",
		"historyNeedsMigration:!!t.historyNeedsMigration",
		"async function repHydrateTabHistory(t)",
		"await repHydrateTabHistory(t)",
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

	serializeStart := strings.Index(tools, "function serializeRepeaterTab(t)")
	serializeEnd := strings.Index(tools, "function repWarningSuffix(t)")
	if serializeStart < 0 || serializeEnd <= serializeStart {
		t.Fatal("Repeater tab serializer not found")
	}
	if strings.Contains(tools[serializeStart:serializeEnd], "history:normalizeRepHistory(t.history)") {
		t.Error("Repeater tab metadata must not inline the unbounded history")
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
		"await repStoreHistoryEntries(t,legacy)",
		"t.history=normalizeRepHistory([...(t.history||[]),...legacy])",
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
		"normalizeRepHistory([entry,...(t.history||[])])",
		"await repStoreHistoryEntries(t,t.historyStoreNeedsMigration?t.history:[entry])",
		"index.getAll(repHistoryTabKey(t))",
		"function repDeleteHistoryKey(tabKey)",
		"index.openCursor(tabKey)",
		"onClose:t=>repDeleteHistory(t)",
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

func TestUIRepeaterHistoryHydratesOnDemandWithoutBlockingStartup(t *testing.T) {
	tools := readUIAsset(t, "js/tools.js")

	for _, contract := range []string{
		"function repHistoryVisible()",
		"function refreshRepHistory(t=repCur())",
		"if(repHistoryVisible())loadRepHistory();else repSetHistoryCount(t)",
		"if(show)loadRepHistory()",
		"t.history=normalizeRepHistory([...(t.history||[]),...embedded,...stored])",
		"repRetryHistoryCleanup(repTabs.tabs).catch(()=>{})",
	} {
		if !strings.Contains(tools, contract) {
			t.Errorf("on-demand Repeater history contract missing %q", contract)
		}
	}

	start := strings.Index(tools, "export async function repInit()")
	end := strings.Index(tools, "export async function intrInit()")
	if start < 0 || end <= start {
		t.Fatal("Repeater initialization boundary not found")
	}
	init := tools[start:end]
	if strings.Contains(init, "await repRetryHistoryCleanup") || strings.Contains(init, "repTabs.tabs.map(repHydrateTabHistory)") {
		t.Error("Repeater initialization must not await cleanup or hydrate every tab history")
	}
}

func TestUIRepeaterCleanupYieldsBetweenBoundedBatches(t *testing.T) {
	tools := readUIAsset(t, "js/tools.js")

	for _, contract := range []string{
		"const REP_HISTORY_DELETE_BATCH=64",
		"function repHistoryCleanupTurn()",
		"globalThis.requestIdleCallback(()=>resolve(),{timeout:250})",
		"async function repDeleteHistoryBatch(tabKey)",
		"if(!cursor||deleted>=REP_HISTORY_DELETE_BATCH)return",
		"await repHistoryCleanupTurn()",
		"deleted=await repDeleteHistoryBatch(tabKey)",
		"while(deleted===REP_HISTORY_DELETE_BATCH)",
	} {
		if !strings.Contains(tools, contract) {
			t.Errorf("bounded Repeater cleanup contract missing %q", contract)
		}
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
		"const actionEpoch=++repRequestActionEpoch",
		"actionEpoch===repRequestActionEpoch",
		"t.historyLoadEpoch=(t.historyLoadEpoch||0)+1",
		"const loadEpoch=t.historyLoadEpoch",
		"const editorEpoch=t.reqEditEpoch||0",
		"const current=()=>repCur()===t&&actionEpoch===repRequestActionEpoch&&t.historyLoadEpoch===loadEpoch&&(t.reqEditEpoch||0)===editorEpoch",
		"t.reqEditEpoch=(t.reqEditEpoch||0)+1",
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

func TestUIRepeaterSendCompletionOwnsItsGeneration(t *testing.T) {
	tools := readUIAsset(t, "js/tools.js")
	start := strings.Index(tools, "export async function repSend()")
	end := strings.Index(tools, "export async function renderRepResponse()")
	if start < 0 || end <= start {
		t.Fatal("Repeater send function not found")
	}
	send := tools[start:end]
	for _, contract := range []string{
		"t.sendEpoch=(t.sendEpoch||0)+1",
		"const sendEpoch=t.sendEpoch",
		"const current=()=>!t._closed&&t.sendEpoch===sendEpoch",
		"await repRecordHistory(t,flow)",
		"resetRepSend(600,t,sendEpoch)",
		"resetRepSend(900,t,sendEpoch)",
	} {
		if !strings.Contains(send, contract) {
			t.Errorf("Repeater send ownership contract missing %q", contract)
		}
	}
	historyWrite := strings.Index(send, "await repRecordHistory(t,flow)")
	clearPending := strings.Index(send, "t.sendPending=false;repPersist()")
	if historyWrite < 0 || clearPending < historyWrite {
		t.Error("Repeater send must remain pending until tab-owned history persistence settles")
	}
	if strings.Count(send, "if(!current())return") < 3 || strings.Count(send, "||!current())return") < 3 {
		t.Error("Repeater send continuations must reject stale generations after every asynchronous boundary")
	}
}

func TestUIRepeaterRequestAdoptionUsesSharedLatestActionOwnership(t *testing.T) {
	tools := readUIAsset(t, "js/tools.js")
	start := strings.Index(tools, "export async function sendToRepeater(f)")
	end := strings.Index(tools, "export async function repInit()")
	if start < 0 || end <= start {
		t.Fatal("send-to-Repeater function not found")
	}
	transfer := tools[start:end]
	for _, contract := range []string{
		"const actionEpoch=++repRequestActionEpoch",
		"if(actionEpoch!==repRequestActionEpoch)return false",
		"t.reqEditEpoch=(t.reqEditEpoch||0)+1",
	} {
		if !strings.Contains(transfer, contract) {
			t.Errorf("send-to-Repeater ownership contract missing %q", contract)
		}
	}
	guard := strings.LastIndex(transfer, "if(actionEpoch!==repRequestActionEpoch)return false")
	paint := strings.Index(transfer, "t.method=d.method")
	if guard < 0 || paint < 0 || guard > paint {
		t.Error("send-to-Repeater must reject stale actions before committing or focusing a request")
	}
}

func TestUIRepeaterCloseAttemptsHistoryDeletionWhenCleanupLedgerIsUnavailable(t *testing.T) {
	tools := readUIAsset(t, "js/tools.js")
	start := strings.Index(tools, "async function repDeleteHistory(t)")
	end := strings.Index(tools, "async function repRetryHistoryCleanup(openTabs)")
	if start < 0 || end <= start {
		t.Fatal("Repeater history deletion function not found")
	}
	cleanup := tools[start:end]
	for _, contract := range []string{
		"const tabKey=repHistoryTabKey(t)",
		"try{repMarkHistoryCleanup(t);}catch(e){}",
		"await repDeleteHistoryKey(tabKey)",
		"try{repClearHistoryCleanup(tabKey);}catch(e){}",
	} {
		if !strings.Contains(cleanup, contract) {
			t.Errorf("Repeater best-effort cleanup-ledger contract missing %q", contract)
		}
	}
	if strings.Contains(cleanup, "const tabKey=repMarkHistoryCleanup(t)") {
		t.Error("cleanup-ledger failure must not prevent the IndexedDB deletion attempt")
	}
	derive := strings.Index(cleanup, "const tabKey=repHistoryTabKey(t)")
	mark := strings.Index(cleanup, "try{repMarkHistoryCleanup(t);}catch(e){}")
	remove := strings.Index(cleanup, "await repDeleteHistoryKey(tabKey)")
	if derive < 0 || mark < derive || remove < mark {
		t.Error("history cleanup must derive the key, attempt the ledger best-effort, then delete IndexedDB rows")
	}
}
