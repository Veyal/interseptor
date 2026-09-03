package control

import (
	"strings"
	"testing"
)

// Persisted workspace data is user-authored. These contracts ensure a damaged
// or unexpectedly large browser blob cannot block boot or be replaced by the
// blank seed tab during automatic hydration.
func TestPersistedTabStateHasNonDestructiveSizeGuard(t *testing.T) {
	core := readUIAsset(t, "js/core.js")
	tools := readUIAsset(t, "js/tools.js")
	if maxUIStateBytes != 4<<20 {
		t.Fatalf("server UI-state limit = %d, want 4 MiB", maxUIStateBytes)
	}

	for _, contract := range []string{
		"export const MAX_PROJECT_UI_STATE_BYTES=4*1024*1024",
		"export function persistedStateByteLength(value)",
		"new TextEncoder().encode(String(value)).byteLength",
		"const MAX_PERSISTED_TAB_COUNT=200",
		"export function isSafePersistedTabState(value)",
		"Number.isSafeInteger(tab.tid)",
		"const persistedSeq=Number.isSafeInteger(d.seq)&&d.seq>0?d.seq:0",
		"mgr.init=function(barSel,fallbackState=null)",
		"persistedStateByteLength(raw)>MAX_PROJECT_UI_STATE_BYTES",
		"d.tabs.length>MAX_PERSISTED_TAB_COUNT",
		"storageMigrationWarnings.set(scoped",
		"return scoped",
		"mgr.storageWarning=true",
		"onStorageWarning",
		"return false;",
	} {
		if !strings.Contains(core, contract) {
			t.Errorf("tab persistence must guard oversized state without overwriting it: missing %q", contract)
		}
	}
	for _, contract := range []string{
		"const ignoredPendingStateKeys=new Set()",
		"persistedStateByteLength(raw)>MAX_PROJECT_UI_STATE_BYTES",
		"ignoredPendingStateKeys.add(key)",
		"ignoredPendingStateKeys.delete(key)",
		"if(ignoredPendingStateKeys.has(key))",
		"guardedHydratedTabStates.set(storageBase,result.value)",
		"Saved workspace exceeds the project storage limit.",
		"queue.pending=null",
		"queue.version++",
		"if(queue.version!==version)continue",
		"const canReplaceInvalidServer=pending!==null&&result.status==='success'",
		"validServer||canReplaceInvalidServer",
		"if(!validServer&&!canReplaceInvalidServer)return 'error'",
		"Saved workspace pending state is not recognized. It was kept for recovery; your next edit will replace it.",
		"Saved workspace pending state is too large to restore safely",
		"return null;",
		"if(repTabs.storageWarning)hydration='error'",
		"if(intrTabs.storageWarning)hydration='error'",
		"if(repTabs.tabs.length&&hydration!=='error'&&!repTabs.storageWarning)repTabs.persist()",
		"if(intrTabs.tabs.length&&hydration!=='error'&&!intrTabs.storageWarning)intrTabs.persist()",
		"hydrateUIState('repeater','rep.tabs',isSafePersistedTabState)",
		"hydrateUIState('intruder','intr.tabs',isSafePersistedTabState)",
		"const guardedHydratedTabStates=new Map()",
		"function persistedTabStateIsUnsafe(raw)",
		"guardedHydratedTabStates.set(storageBase,result.value)",
		"repTabs.init('#repTabs',guardedHydratedTabStates.get('rep.tabs'))",
		"intrTabs.init('#intrTabs',guardedHydratedTabStates.get('intr.tabs'))",
	} {
		if !strings.Contains(tools, contract) {
			t.Errorf("pending workspace state must have a non-destructive size guard: missing %q", contract)
		}
	}
	readStart := strings.Index(tools, "function readPendingUIState(panel)")
	readEnd := strings.Index(tools[readStart:], "function uiPersistenceQueue(panel)")
	if readStart < 0 || readEnd < 0 {
		t.Fatal("pending UI-state reader not found")
	}
	if strings.Contains(tools[readStart:readStart+readEnd], "localStorage.removeItem") {
		t.Fatal("unreadable pending drafts must remain available for recovery")
	}
	if strings.Contains(core, "storageWarningPending") {
		t.Fatal("the first operator edit after guarded recovery must be persisted")
	}
	if strings.Contains(core+tools, "MAX_PERSISTED_TAB_STATE_CHARS") || strings.Contains(core+tools, "MAX_PENDING_UI_STATE_CHARS") {
		t.Fatal("UI-state guards must use the server byte limit, not an independent character limit")
	}
}

func TestPersistedTabStateRejectsAmbiguousIDsAndCapsCreation(t *testing.T) {
	core := readUIAsset(t, "js/core.js")
	for _, contract := range []string{
		"const seenTabIDs=new Set()",
		"tab.tid>=Number.MAX_SAFE_INTEGER",
		"seenTabIDs.has(tab.tid)",
		"seenTabIDs.add(tab.tid)",
		"mgr.tabs.length>=MAX_PERSISTED_TAB_COUNT",
		"aria-disabled=\"${atTabLimit?'true':'false'}\"",
		"supports up to ${MAX_PERSISTED_TAB_COUNT} tabs",
	} {
		if !strings.Contains(core, contract) {
			t.Errorf("tab workspaces must reject ambiguous IDs and remain reloadable at the creation cap: missing %q", contract)
		}
	}
}

func TestProjectPersistenceContinuesWhenBrowserStorageIsUnavailable(t *testing.T) {
	core := readUIAsset(t, "js/core.js")
	persistStart := strings.Index(core, "mgr.persist=function()")
	persistEnd := strings.Index(core[persistStart:], "mgr.persistDebounced=function()")
	if persistStart < 0 || persistEnd < 0 {
		t.Fatal("tab persistence implementation not found")
	}
	persist := core[persistStart : persistStart+persistEnd]
	for _, contract := range []string{
		"let storedLocally=true",
		"storedLocally=false",
		"could not be saved in this browser; project storage will still be updated",
		"if(typeof onPersist==='function')",
		"return storedLocally",
	} {
		if !strings.Contains(persist, contract) {
			t.Errorf("a localStorage failure must not suppress project persistence: missing %q", contract)
		}
	}
	if strings.Index(persist, "if(typeof onPersist==='function')") < strings.Index(persist, "catch(e)") {
		t.Error("project persistence must run after handling a browser-local storage failure")
	}
}

func TestRepeaterCreationPathsShareReloadSafeLimit(t *testing.T) {
	core := readUIAsset(t, "js/core.js")
	tools := readUIAsset(t, "js/tools.js")
	proxy := readUIAsset(t, "js/proxy.js")
	for _, contract := range []string{
		"mgr.availableSlots=()=>MAX_PERSISTED_TAB_COUNT-mgr.tabs.length",
		"mgr.create=function()",
		"const t=repTabs.create()",
		"const available=repTabs.availableSlots()",
		"requests.length>available",
		"if(!t)return false",
	} {
		if !strings.Contains(core+tools+proxy, contract) {
			t.Errorf("every Repeater creation route must share the reload-safe tab allocator: missing %q", contract)
		}
	}
	if strings.Contains(tools, "repTabs.seq++") {
		t.Error("Repeater creation must not bypass the bounded tab-ID allocator")
	}
	if strings.Contains(tools, "created.push(tab);repTabs.tabs.push(tab)") {
		t.Error("Postman import must not append a tab twice after the shared allocator already owns insertion")
	}
}

func TestProjectStorageKeysPreserveTheFullProjectIdentity(t *testing.T) {
	core := readUIAsset(t, "js/core.js")
	project := readUIAsset(t, "js/project.js")
	if !strings.Contains(core, "encodeURIComponent(String(storageProject))") {
		t.Error("project-local browser keys must encode the complete identity without collisions")
	}
	if strings.Contains(core, "String(storageProject).replace(/[^A-Za-z0-9._-]+/g,'_')") {
		t.Error("lossy project-name replacement can leak drafts between distinct projects")
	}
	for _, contract := range []string{
		"export function setStorageProject(name,knownProjects=[],legacyName=name)",
		"export function projectStorageLegacyKeys(base)",
		"const legacyMatches=storageProjectNames.filter",
		"legacyMatches.length===1",
		"localStorage.removeItem(legacyScoped)",
		"ambiguous legacy project state",
		"let migrationComplete=true",
		"migrationComplete=false",
		"legacy project state could not be migrated",
		"if(migrationComplete)migratedStorageBases.add(base)",
		"setStorageProject(identity.key,identity.projects,identity.name)",
		"key:project.dir",
		"key:version.projectDir",
	} {
		if !strings.Contains(core+project, contract) {
			t.Errorf("legacy scoped drafts need an unambiguous migration or explicit recovery warning: missing %q", contract)
		}
	}
}

func TestPersistedTabNormalizationCannotCrashOrShareRepeaterHistory(t *testing.T) {
	tools := readUIAsset(t, "js/tools.js")
	core := readUIAsset(t, "js/core.js")
	for _, contract := range []string{
		"function persistedText(value,fallback='')",
		"function normalizeRepeaterTab(t,normalizedTabs=[])",
		"const historyKeys=new Set(normalizedTabs.map(tab=>tab.historyKey))",
		"while(historyKeys.has(historyKey))historyKey=newRepHistoryKey()",
		"function normalizeIntruderTab(t)",
		"for(const rawTab of d.tabs)normalizedTabs.push(normalize(rawTab,normalizedTabs))",
	} {
		if !strings.Contains(tools+core, contract) {
			t.Errorf("persisted tab normalization must coerce malformed fields and isolate history identities: missing %q", contract)
		}
	}
}

func TestIntruderPresetNormalizationCannotBlockWorkspaceBoot(t *testing.T) {
	tools := readUIAsset(t, "js/tools.js")
	for _, contract := range []string{
		"function normalizeIntruderPreset(p)",
		"function normalizeIntruderPresets(value)",
		"if(!p||typeof p!=='object'||Array.isArray(p))return null",
		"value.slice(0,20).map(normalizeIntruderPreset).filter(Boolean)",
		"hydrateUIState('intruder-presets','intruder.presets',value=>normalizeIntruderPresets(value)!==null)",
		"let list=normalizeIntruderPresets(fallback)",
		"list=normalizeIntruderPresets(JSON.parse(localStorage.getItem(intrPresetsKey())||'null'))",
		"list=normalizeIntruderPresets(list)||[]",
	} {
		if !strings.Contains(tools, contract) {
			t.Errorf("malformed Intruder presets must be normalized before rendering or reuse: missing %q", contract)
		}
	}
}

func TestHydrationWriteFailureUsesInMemoryStateInsteadOfBlankReplacement(t *testing.T) {
	tools := readUIAsset(t, "js/tools.js")
	for _, contract := range []string{
		"guardedHydratedTabStates.set(storageBase,pending)",
		"guardedHydratedTabStates.set(storageBase,result.value)",
		"could not be copied into browser storage; it was restored in memory",
		"loadIntrPresets(guardedHydratedTabStates.get('intruder.presets'))",
		"let list=normalizeIntruderPresets(fallback)",
		"if(ignoredPendingStateKeys.has(key))",
	} {
		if !strings.Contains(tools, contract) {
			t.Errorf("a failed hydration write must retain authoritative state in memory: missing %q", contract)
		}
	}
	if strings.Contains(tools, "uiPersistenceReady.set(panel,false)") {
		t.Error("an ignored browser draft must not disable later project storage synchronization")
	}
}

func TestIntruderPresetSaveKeepsInMemoryFallbackAndTruthfulFeedback(t *testing.T) {
	tools := readUIAsset(t, "js/tools.js")
	for _, contract := range []string{
		"let storedLocally=true",
		"catch(e){storedLocally=false;}",
		"loadIntrPresets(list)",
		"preset kept in this session · server sync queued",
		"preset kept in this session · storage unavailable",
	} {
		if !strings.Contains(tools, contract) {
			t.Errorf("Intruder preset save fallback missing %q", contract)
		}
	}
}

func TestIntruderHydrationKeepsBoundedPayloadPreviews(t *testing.T) {
	tools := readUIAsset(t, "js/tools.js")
	for _, contract := range []string{
		"const INTR_PERSISTED_PAYLOAD_MAX=500",
		"o.sniperLines?.length>INTR_PERSISTED_PAYLOAD_MAX",
		"o.posLines.map(a=>(a&&a.length<=INTR_PERSISTED_PAYLOAD_MAX)?a:null)",
		"o.sniperLarge=true",
		"not restored after reload",
	} {
		if !strings.Contains(tools, contract) {
			t.Errorf("Intruder persisted payload safeguard missing %q", contract)
		}
	}
}

func TestPersistedStateRecoveryWarningIsPersistentAndActionable(t *testing.T) {
	core := readUIAsset(t, "js/core.js")
	tools := readUIAsset(t, "js/tools.js")
	app := readUIAsset(t, "js/app.js")
	for _, contract := range []string{
		"export function workspaceStorageWarningMessage()",
		"workspaceStorageWarningMessage",
		"data-workspace-warning-dismiss",
		">Continue</button>",
		"Editing this replacement tab will replace it.",
		"mgr.persist=function()",
		"document.activeElement===dismiss",
		"document.querySelector('.tab.active:not(:disabled)')?.focus()",
	} {
		if !strings.Contains(core+tools+app, contract) {
			t.Errorf("guarded saved-state recovery needs persistent, truthful feedback: missing %q", contract)
		}
	}
}
