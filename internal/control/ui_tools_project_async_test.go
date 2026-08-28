package control

import (
	"strings"
	"testing"
)

func TestProjectIdentityIsBoundedAndFailsClosed(t *testing.T) {
	project := executableJS(readUIAsset(t, "js/project.js"))
	app := executableJS(readUIAsset(t, "js/app.js"))

	for _, contract := range []string{
		"const PROJECT_IDENTITY_TIMEOUT_MS=2500",
		"const controller=new AbortController()",
		"cache:'no-store'",
		"Promise.allSettled",
		"controller.abort()",
		"throw new Error('active project unavailable')",
	} {
		if !strings.Contains(project, contract) {
			t.Errorf("project identity fail-closed contract missing %q", contract)
		}
	}
	if strings.Contains(project, "return 'default'") {
		t.Error("project identity must never silently select the default project")
	}
	for _, contract := range []string{
		"data-workspace-retry",
		"location.reload()",
		"Active project unavailable",
	} {
		if !strings.Contains(app, contract) {
			t.Errorf("project identity recovery UI contract missing %q", contract)
		}
	}
}

func TestProjectDraftWritesAreOrderedAndRecoverDirtyLocalState(t *testing.T) {
	tools := readUIAsset(t, "js/tools.js")
	app := executableJS(readUIAsset(t, "js/app.js"))
	for _, contract := range []string{
		"const uiPersistenceQueues=new Map()",
		"function uiPendingStateKey(panel)",
		"localStorage.setItem(uiPendingStateKey(panel),body)",
		"async function drainUIState(panel)",
		"while(queue.pending!==null)",
		"await api('/api/ui/'+panel",
		"if(queue.pending===null)queue.pending=body",
		"export function uiStateSyncPending()",
		"new CustomEvent('interseptor:ui-state-sync'",
		"function readPendingUIState(panel)",
		"let pending=readPendingUIState(panel)",
		"if(pending!==null)",
		"persistUIState(panel,pending)",
		"detail:{pending:true,error:true",
		"export async function retryUIStateSync()",
	} {
		if !strings.Contains(tools, contract) {
			t.Errorf("durable ordered UI persistence contract missing %q", contract)
		}
	}
	for _, contract := range []string{
		"Local draft · server sync pending",
		"retryUIStateSync()",
		"event.detail?.pending",
	} {
		if !strings.Contains(app, contract) {
			t.Errorf("current-session persistence failure feedback missing %q", contract)
		}
	}
}

func TestRepeaterCommitsDecodedModeOnlyAfterCurrentDecodeSucceeds(t *testing.T) {
	tools := readUIAsset(t, "js/tools.js")
	for _, contract := range []string{
		"const startView=t.reqView||'pretty'",
		"t.reqView===startView",
		"t.reqView='decoded';t.codecId=d.codecId||''",
		"if(next==='decoded')",
		"const ok=await repEnterDecoded(t)",
		"if(ok===null)return",
	} {
		if !strings.Contains(tools, contract) {
			t.Errorf("Repeater decoded transition ownership contract missing %q", contract)
		}
	}
	transitionStart := strings.Index(tools, "if(next==='decoded')")
	if transitionStart < 0 {
		return
	}
	transition := tools[transitionStart:]
	awaitAt := strings.Index(transition, "await repEnterDecoded(t)")
	commitAt := strings.Index(transition, "t.reqView='decoded'")
	if commitAt >= 0 && commitAt < awaitAt {
		t.Error("Repeater must not enter decoded mode before the decode is acknowledged")
	}
}

func TestSendToRepeaterRejectsStaleEditorOwnership(t *testing.T) {
	tools := readUIAsset(t, "js/tools.js")
	for _, contract := range []string{
		"const tabEditEpochs=new Map(repTabs.tabs.map",
		"tabEditEpochs.get(t.tid)!==(t.reqEditEpoch||0)",
	} {
		if !strings.Contains(tools, contract) {
			t.Errorf("async editor ownership contract missing %q", contract)
		}
	}
}

func TestIntruderPresetHydrationAndSaveFeedbackStayTruthful(t *testing.T) {
	tools := readUIAsset(t, "js/tools.js")
	for _, contract := range []string{
		"const [tabHydration,presetHydration]=await Promise.all",
		"const hydration=[tabHydration,presetHydration].includes('error')?'error'",
		"return result.status",
		"const serverSyncQueued=persistUIState('intruder-presets',list)",
		"preset saved locally",
		"server sync unavailable",
	} {
		if !strings.Contains(tools, contract) {
			t.Errorf("Intruder preset truthfulness contract missing %q", contract)
		}
	}
}
