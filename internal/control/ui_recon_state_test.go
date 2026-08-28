package control

import (
	"strings"
	"testing"
)

// These contracts cover the race-prone parts of the Findings and Map panes.
// The UI is embedded JavaScript, so a small source-level test catches accidental
// regressions without introducing a frontend build dependency.
func TestFindingsMapStateContracts(t *testing.T) {
	findings := readUIAsset(t, "js/findings.js")
	mapJS := readUIAsset(t, "js/map.js")

	if strings.Index(findings, "const edit = findEditMode") > strings.Index(findings, "const verifBanner") {
		t.Fatal("findings detail must establish edit mode before building verification banner")
	}
	for _, name := range []string{"loadEndpointsEpoch", "loadParamsEpoch"} {
		if !strings.Contains(mapJS, name) {
			t.Errorf("map must use latest-request-wins epoch %s", name)
		}
	}
	for _, contract := range []string{
		"let mapEndpointDataMode='unknown'",
		"let mapEndpointRequestMode='full'",
		"function setMapSearchState(search,scope=mapState.searchScope)",
		"if(loadEndpointsPending&&(wasServer||nextServer))invalidateEndpointLoad()",
		"mapEndpointDataMode=serverSearch?'server':'full'",
		"mapEndpointRequestMode=serverSearch?'server':'full'",
		"if(mapUsesServerSearch()||mapEndpointDataMode!=='full'||mapEndpointRequestMode==='server')",
		"function refreshMapDomainSelection()",
		"if(mapState.view==='params'){",
		"invalidateEndpointLoad();loadParams();return",
		"else mapApplySearch()",
	} {
		if !strings.Contains(mapJS, contract) {
			t.Errorf("Map search/domain transition contract missing %q", contract)
		}
	}
	if !strings.Contains(mapJS, "if(v!=='params')loadParamsEpoch++") {
		t.Error("leaving Map parameter view must invalidate its pending response")
	}
	if strings.Count(mapJS, "refreshMapDomainSelection()") < 3 {
		t.Error("Map domain selector and both breadcrumb paths must share the view-aware refresh boundary")
	}
	for _, key := range []string{"MAP_VIEW_KEY", "MAP_DOMAIN_KEY", "MAP_HIDE_NOISE_KEY", "MAP_COLLAPSE_IDENTICAL_KEY"} {
		if !strings.Contains(mapJS, "projectStorageKey("+key+")") {
			t.Errorf("map preference %s must be project scoped", key)
		}
	}
	if !strings.Contains(mapJS, "renderLoadError, projectStorageKey") {
		t.Fatal("map must import the shared project storage-key helper")
	}
}

func TestFindingsLoadsAndSavesKeepAuthoritativeState(t *testing.T) {
	findings := readUIAsset(t, "js/findings.js")
	for _, contract := range []string{
		"let findingsLoadEpoch=0",
		"let findingWritesInFlight = 0",
		"let findingDetailRefreshDeferred = false",
		"const epoch=++findingsLoadEpoch",
		"if(epoch!==findingsLoadEpoch)return false",
		"function findingDetailEditPending()",
		"findingWritesInFlight > 0",
		"bodySaveTimers.has(selFinding)",
		"findingDetailRefreshDeferred = true",
		"const authoritative = acknowledgedFindingValue(f.id, key, previous)",
		"if (el.value === v) el.value = authoritative",
		"toast(err.message); return;",
	} {
		if !strings.Contains(findings, contract) {
			t.Errorf("Findings authoritative-state contract missing %q", contract)
		}
	}
	patchFailure := strings.Index(findings, "const authoritative = acknowledgedFindingValue(f.id, key, previous)")
	if patchFailure < 0 {
		t.Fatal("Finding blur save must return after a failed PATCH")
	}
	reload := strings.Index(findings[patchFailure:], "await loadFindings()")
	if reload < 0 {
		t.Fatal("Finding blur save must separate PATCH rollback from the later refresh")
	}
}

func TestRepeaterDecodeCompletionDistinguishesStaleFromFallback(t *testing.T) {
	tools := readUIAsset(t, "js/tools.js")
	for _, contract := range []string{
		"if(!current())return null",
		"if(repCur()!==t||ok===null)return",
		"if(!ok){$('#repBody').value=repBodyForDisplay(t.body,'pretty');}",
	} {
		if !strings.Contains(tools, contract) {
			t.Errorf("Repeater decoded-view stale result contract missing %q", contract)
		}
	}
}

func TestProjectIdentityResolvesBeforeSavedTabRestore(t *testing.T) {
	app := executableJS(readUIAsset(t, "js/app.js"))
	projectJS := executableJS(readUIAsset(t, "js/project.js"))
	proxy := executableJS(readUIAsset(t, "js/proxy.js"))
	for _, contract := range []string{
		"const projectStorageReady=activeProjectIdentity().then(name=>setStorageProject(name))",
		"return projectStorageReady.then(()=>mapMod||(mapMod=import('./map.js')))",
	} {
		if !strings.Contains(projectJS, contract) {
			t.Errorf("project-ready Map contract missing %q", contract)
		}
	}
	if !strings.Contains(app, "await projectStorageReady") {
		t.Error("application boot must await the shared project-ready boundary")
	}
	if !strings.Contains(app, "from './project.js'") || !strings.Contains(proxy, "from './project.js'") {
		t.Error("application navigation and Proxy Map search must share the project-ready Map loader")
	}
	if strings.Contains(proxy, "import('./map.js')") {
		t.Error("Proxy must not bypass the project-ready Map loader")
	}
	start := strings.Index(app, "async function bootFirstRunUI()")
	if start < 0 {
		t.Fatal("bootFirstRunUI not found")
	}
	body := app[start:]
	project := strings.Index(body, "await bootProjectScopedUI()")
	restore := strings.Index(body, "restoreTab()")
	load := strings.Index(body, "await loadFlows()")
	if project < 0 || restore < 0 || load < 0 || !(project < restore && restore < load) {
		t.Errorf("project identity must resolve before saved Map/tab preferences restore (project=%d restore=%d load=%d)", project, restore, load)
	}
	if strings.Contains(app, "connectEvents();restoreTab()") {
		t.Error("boot tail must not restore a project-scoped tab before project identity resolves")
	}
	if !strings.Contains(app, "await Promise.all([repInit(),intrInit()])") {
		t.Error("Repeater and Intruder hydration must finish before a saved tab becomes editable")
	}
}

func TestProjectUIHydrationCannotBlockStartupIndefinitely(t *testing.T) {
	tools := readUIAsset(t, "js/tools.js")
	for _, contract := range []string{
		"const UI_HYDRATE_TIMEOUT_MS=2500",
		"async function readBoundedUIState(panel)",
		"const controller=new AbortController()",
		"signal:controller.signal",
		"controller.abort()",
		"clearTimeout(timer)",
		"return {status:'success',value:d.value}",
		"return {status:'empty'}",
		"return {status:'error',error:e}",
		"const result=await readBoundedUIState(panel)",
		"const result=await readBoundedUIState('intruder-presets')",
		"const [hydration]=await Promise.all([hydrateUIState('intruder','intr.tabs'),hydrateIntrPresets()])",
		"if(repTabs.tabs.length&&hydration!=='error')repTabs.persist()",
		"if(intrTabs.tabs.length&&hydration!=='error')intrTabs.persist()",
	} {
		if !strings.Contains(tools, contract) {
			t.Errorf("bounded project UI hydration contract missing %q", contract)
		}
	}
}
