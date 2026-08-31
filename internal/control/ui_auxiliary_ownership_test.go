package control

import (
	"strings"
	"testing"
)

func TestUIAPIResourceLoadsRejectStaleResponses(t *testing.T) {
	requireUIContracts(t, "js/apipanel.js",
		"let allowlistLoadEpoch=0",
		"const epoch=++allowlistLoadEpoch",
		"if(epoch!==allowlistLoadEpoch)return",
		"let apiKeysLoadEpoch=0",
		"const epoch=++apiKeysLoadEpoch",
		"if(epoch!==apiKeysLoadEpoch)return",
		"let shareLoadEpoch=0",
		"const epoch=++shareLoadEpoch",
		"if(epoch!==shareLoadEpoch)return",
		"let mergeStatusLoadEpoch=0",
		"const epoch=++mergeStatusLoadEpoch",
		"if(epoch!==mergeStatusLoadEpoch)return",
		"let vaultPanelLoadEpoch=0",
		"let vaultListLoadEpoch=0",
	)
}

func TestUIAPIPeerAndVaultActionsRejectDuplicateSubmissions(t *testing.T) {
	requireUIContracts(t, "js/apipanel.js",
		"let allowlistMutationPending=false",
		"if(allowlistMutationPending)return",
		"function setAllowlistMutationPending(pending)",
		"let peerMergePending=false",
		"if(peerMergePending)return",
		"function setPeerMergePending(pending)",
		"let vaultActionPending=false",
		"if(vaultActionPending)return",
		"function setVaultActionPending(pending)",
		"async function saveVaultCfg(){\n  if(vaultActionPending)return",
		"finally{setPeerMergePending(false);}",
		"finally{setVaultActionPending(false);}",
	)
}

func TestUIVaultDoesNotProbeRemoteBeforeItIsConfigured(t *testing.T) {
	panel := requireUIContracts(t, "js/apipanel.js",
		"let vaultConfigured=false",
		"vaultConfigured=!!(c.url&&c.hasKey)",
		"if(!vaultConfigured)",
		"Configure vault URL and token to list backups.",
		"pending||(id!=='vaultSaveCfg'&&!vaultConfigured)",
	)
	guard := strings.Index(panel, "if(!vaultConfigured)")
	remote := strings.Index(panel, "await api('/api/vault/remote')")
	if guard < 0 || remote < 0 || guard > remote {
		t.Fatal("vault configuration must be checked before probing the remote endpoint")
	}
	requireUIContracts(t, "index.html",
		`id="vaultBackup" disabled`,
		`id="vaultRefresh" disabled`,
	)
}

func TestUIVaultSaveDoesNotClearANewerTokenDraft(t *testing.T) {
	panel := requireUIContracts(t, "js/apipanel.js",
		"const key=$('#vaultKey').value.trim()",
		"if(key&&$('#vaultKey').value.trim()===key)$('#vaultKey').value=''",
	)
	if strings.Contains(panel, "if(key)$('#vaultKey').value=''") {
		t.Fatal("a completed vault save must not clear a token typed during the request")
	}
}

func TestUIShareDelayedRefreshBelongsToTheStartingAction(t *testing.T) {
	requireUIContracts(t, "js/apipanel.js",
		"let shareActionEpoch=0,shareRefreshTimer=null",
		"clearTimeout(shareRefreshTimer)",
		"const epoch=++shareActionEpoch",
		"if(epoch!==shareActionEpoch)return",
		"shareRefreshTimer=setTimeout",
	)
}

func TestUITagLoadsAndColorWritesHaveLatestOwners(t *testing.T) {
	tags := requireUIContracts(t, "js/tags.js",
		"let tagLoadEpoch=0",
		"const epoch=++tagLoadEpoch",
		"if(epoch!==tagLoadEpoch)return",
		"const tagColorLanes=new Map()",
		"lane.next={color,revision:++lane.revision}",
		"while(lane.next)",
		"tagColorLanes.delete(tag)",
	)
	if strings.Count(tags, "if(epoch!==tagLoadEpoch)return") < 2 {
		t.Error("tag loading must guard both success and failure effects")
	}
}

func TestUIHumanInputRejectsStaleLoadsAndDuplicateResponses(t *testing.T) {
	index := requireUIContracts(t, "index.html",
		`id="humanInputBar"`,
		`role="region"`,
		`aria-live="polite"`,
		"hidden",
	)
	if strings.Count(index, `id="humanInputBar"`) != 1 {
		t.Fatal("AI-to-human input needs exactly one persistent live region")
	}
	human := requireUIContracts(t, "js/humaninput.js",
		"let humanInputLoadEpoch=0",
		"const humanInputPending=new Set()",
		"const epoch=++humanInputLoadEpoch",
		"if(epoch!==humanInputLoadEpoch)return",
		"if(humanInputPending.has(id))return",
		"humanInputPending.add(id)",
		"row.setAttribute('aria-busy','true')",
		"humanInputLoadEpoch++",
		"await loadHumanInput()",
		"bar.hidden = true",
		"bar.hidden = false",
	)
	if strings.Count(human, "if(epoch!==humanInputLoadEpoch)return") < 2 {
		t.Error("human-input loading must guard both success and failure effects")
	}
}

func TestUIActivityReconcilesLiveEventsAcrossLoadsAndClears(t *testing.T) {
	requireUIContracts(t, "js/activity.js",
		"let activityPendingEvents=[]",
		"function activityEventKey(it)",
		"activityPendingEvents.unshift(it)",
		"if(activityPendingEvents.length>ACT_MAX)activityPendingEvents.length=ACT_MAX",
		"function mergeActivitySnapshot(snapshot)",
		"state.activity=mergeActivitySnapshot(d.activity||[])",
		"activityPendingEvents=[]",
		"activityLoadGeneration++",
	)
	requireUIContracts(t, "js/app.js",
		"if(t.dataset.tab==='activity'){clearActSeen();loadActivity();}",
		".classList.contains('active'))loadActivity()",
	)
}

func TestUITLSDiagnosisOnlyRendersTheLatestRequest(t *testing.T) {
	tls := requireUIContracts(t, "js/tlsdiag.js",
		"let trafficDiagnosisEpoch=0",
		"const epoch=++trafficDiagnosisEpoch",
		"if(epoch!==trafficDiagnosisEpoch)return null",
	)
	if strings.Count(tls, "if(epoch!==trafficDiagnosisEpoch)return null") < 2 {
		t.Error("TLS diagnosis must guard both success and error effects")
	}
}

func TestUISingletonPromptAndConfirmSettleSupersededCallers(t *testing.T) {
	requireUIContracts(t, "js/core.js",
		"let activePromptFinish=null",
		"let activeConfirmFinish=null",
		"if(activePromptFinish)activePromptFinish(null)",
		"if(activeConfirmFinish)activeConfirmFinish(false)",
		"if(activePromptFinish===finish)activePromptFinish=null",
		"if(activeConfirmFinish===finish)activeConfirmFinish=null",
	)
}

func TestUIFindingCreateAndAsyncEditorActionsRetainTheirOwners(t *testing.T) {
	requireUIContracts(t, "js/findings.js",
		"let findingCreateEpoch=0",
		"const createEpoch=++findingCreateEpoch",
		"if(createEpoch!==findingCreateEpoch",
		"findingCreateEpoch++",
	)
	requireUIContracts(t, "js/tools.js",
		"const ownerTab=repCur()",
		"const ownerEditEpoch=ownerTab.reqEditEpoch||0",
		"repCur()!==ownerTab",
		"ownerTab.reqEditEpoch!==ownerEditEpoch",
		"let intrToFindingPending=false",
		"if(intrToFindingPending)return",
	)
}

func TestUIFlowSearchTestValidatesTheRequiredNameBeforeRequesting(t *testing.T) {
	proxy := requireUIContracts(t, "js/proxy.js",
		"async function testFlowSearch()",
		"if(!p.name){flowSearchStatus('name required',true);return;}",
		"await api('/api/flow-searches/test'",
	)
	guard := strings.Index(proxy, "if(!p.name){flowSearchStatus('name required',true);return;}")
	request := strings.Index(proxy, "await api('/api/flow-searches/test'")
	if guard < 0 || request < 0 || guard > request {
		t.Fatal("flow-search Test must validate its API-required name before issuing a request")
	}
}
