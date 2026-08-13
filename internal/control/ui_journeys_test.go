package control

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUIJourneyProxyAnywhereAndSavedScriptSearchContracts(t *testing.T) {
	// Given
	index := readUIAsset(t, "index.html")
	proxy := executableJS(readUIAsset(t, "js/proxy.js"))

	// When / Then
	requireUIContains(t, index,
		`<option value="anywhere" selected>Anywhere</option>`,
		`<option value="body">Body</option>`,
		`<option value="id">ID</option>`,
		`<option value="script">Script</option>`,
		`id="flowSearchScriptEditor"`,
		`aria-label="Saved search Starlark editor"`,
		`id="flowSearchScriptSave"`,
		`id="flowSearchScriptError"`,
	)
	requireUIContains(t, proxy,
		"searchScope:'anywhere'",
		"'/api/flow-searches'",
		"flowSearchScriptEditor",
		"flowSearchScriptSave",
		"flowSearchScriptError",
		"state.filters.searchScope",
		"savedSearch",
		"loadFlows();",
	)
}

func TestUIJourneyProxyActionsAndKeyboardContextMenu(t *testing.T) {
	index := readUIAsset(t, "index.html")
	proxy := executableJS(readUIAsset(t, "js/proxy.js"))
	requireUIContains(t, index,
		`id="inspectSendRepeater"`,
		`id="inspectSendIntruder"`,
		`id="inspectMoreActions"`,
		`aria-label="More flow actions"`,
	)
	requireUIContains(t, proxy,
		"inspectSendRepeater",
		"inspectSendIntruder",
		"inspectMoreActions",
		"e.key==='ContextMenu'",
		"(e.shiftKey&&e.key==='F10')",
		"showCtx(",
	)
}

func TestUIJourneyMapActivityLabelsAndRetryStates(t *testing.T) {
	index := readUIAsset(t, "index.html")
	mapJS := executableJS(readUIAsset(t, "js/map.js"))
	activity := executableJS(readUIAsset(t, "js/activity.js"))
	settings := executableJS(readUIAsset(t, "js/settings.js"))
	scanner := executableJS(readUIAsset(t, "js/scanner.js"))

	requireUIContains(t, mapJS,
		"const MAP_DOMAIN_KEY =",
		"restoreMapDomain()",
		"localStorage.setItem(MAP_DOMAIN_KEY",
		"renderLoadError(",
		"finally",
	)
	if strings.Contains(mapJS, "mapState.domain = hosts[0]") {
		t.Error("Map still auto-selects the first host instead of All domains")
	}
	requireUIContains(t, activity,
		"wireRowKey",
		"aria-label",
		"renderLoadError(",
		"finally",
	)
	requireUIRegex(t, activity, `if\(row\.classList\.contains\('act-jump'\)\)\{.*?wireRowKey\(row,open\)`)
	requireUIContains(t, settings, "renderLoadError(", "finally")
	requireUIContains(t, scanner, "renderLoadError(", "finally")
	requireUIContains(t, index,
		`aria-label="Repeater request headers"`,
		`aria-label="Repeater request body"`,
		`aria-label="Intruder request template"`,
		`aria-label="Engagement notes editor"`,
		`aria-label="Decoder input"`,
		`aria-label="Decoder output"`,
	)
}

func TestUIJourneySettingsUpstreamProxyCredentialsAreOptional(t *testing.T) {
	index := readUIAsset(t, "index.html")
	settings := executableJS(readUIAsset(t, "js/settings.js"))
	requireUIContains(t, index,
		`id="setUpstream"`,
		`id="setUpstreamUser"`,
		`id="setUpstreamPassword"`,
		`id="setUpstreamCA"`,
		`Optional; leave blank for no proxy authentication`,
	)
	requireUIContains(t, settings,
		"parseUpstreamProxyCredentials(",
		"upstreamProxy:buildUpstreamProxyURL(",
		"setUpstreamUser",
		"setUpstreamPassword",
		"setUpstreamCA",
		"upstreamProxyCA",
		"new URL(raw)",
	)
	if strings.Contains(settings, "encodeURIComponent($('#setUpstreamUser').value.trim())") {
		t.Error("upstream proxy URL must be built with URL.username instead of manual userinfo encoding")
	}
}

func TestUIJourneySettingsRetainsNonAIControls(t *testing.T) {
	settings := executableJS(readUIAsset(t, "js/settings.js"))

	requireUIContains(t, settings,
		"document.querySelector('#panel-settings .settings-body')",
		"function setCapScope(",
		"function setSuppressTelemetry(",
		"function setSuppressAndroidTelemetry(",
		"function setInvisibleProxy(",
		"function setAutoBypass(",
		"captureScopeOnly:on",
		"suppressBrowserTelemetry:on",
		"suppressAndroidTelemetry:on",
		"invisibleProxy:on",
		"autoBypassOnPinFailure:on",
		"tlsBypassHosts:hosts",
		"upstreamProxy:buildUpstreamProxyURL()",
		"upstreamProxyCA",
	)
}

func TestUIJourneyFindingAttachedFlowRepeaterAction(t *testing.T) {
	findings := executableJS(readUIAsset(t, "js/findings.js"))
	tools := readUIAsset(t, "js/tools.js")
	requireUIContains(t, findings,
		`import { sendToRepeater } from './tools.js';`,
		`find-send-repeater`,
		`aria-label="Send attached flow #`,
		`sendToRepeater({ id });`,
		`!block.missing`,
		`find-report-flow`,
	)
	requireUIContains(t, tools,
		`sendToRepeater`,
		`'/api/flows/'`,
		`raw?side=req`,
		`method=d.method`,
		`headersToText(d.reqHeaders)`,
		`sourceFlowId=f.id`,
	)
	if strings.Contains(findings, "Copy URL") || strings.Contains(findings, "Copy link") {
		t.Error("finding flow actions retain copy controls")
	}
	if strings.Contains(findings, "fetch('/api/flows") || strings.Contains(findings, "api('/api/flows/'+id+'/raw") {
		t.Error("finding flow action reconstructs request instead of using Repeater helper")
	}
}

func TestUIJourneyReadinessProjectScannerReportInterceptAndShareContracts(t *testing.T) {
	setup := executableJS(readUIAsset(t, "js/setup.js"))
	scanner := executableJS(readUIAsset(t, "js/scanner.js"))
	settings := executableJS(readUIAsset(t, "js/settings.js"))
	app := executableJS(readUIAsset(t, "js/app.js"))
	intercept := executableJS(readUIAsset(t, "js/intercept.js"))
	findings := executableJS(readUIAsset(t, "js/findings.js"))
	index := readUIAsset(t, "index.html")

	requireUIContains(t, setup,
		"projectStorageKey(",
		"'/api/readiness'",
		"tls_intercept",
		"traffic",
	)
	for _, asset := range []string{"js/ai.js", "js/autopwn.js"} {
		if _, err := os.Stat(filepath.Join("ui", asset)); err == nil {
			t.Errorf("removed UI asset still exists: %s", asset)
		}
	}
	requireUIContains(t, scanner, "'/api/readiness'", "in-scope")
	requireUIContains(t, scanner,
		"'/api/scanner/targets'",
		"d.hosts",
		"d.truncated",
		"uiConfirm(",
		"'/api/scanner/issues',{method:'DELETE'}",
	)
	if strings.Contains(scanner, "'/api/flows?inScope=1&limit=2000'") || strings.Contains(scanner, "'/api/flows/inscope") {
		t.Error("Scanner target loader still uses a capped/boolean flow endpoint")
	}
	requireUIContains(t, settings,
		"mobileReadiness",
		"'/api/tls-diagnosis'",
		"'/api/readiness'",
		"const accepted=await api('/api/project/switch'",
	)
	requireUIContains(t, settings,
		"Project-wide readiness",
		"does not verify the selected device",
		"send a new HTTPS request from this device",
	)
	if strings.Contains(settings, "Traffic and TLS interception are ready") {
		t.Error("mobile setup still claims the selected device is ready from historical project evidence")
	}
	requireUIContains(t, app, "'/api/project'", "await bootProjectScopedUI()", "await loadFlows()", "maybeShowSetup()")
	requireUIRegex(t, app, `(?s)await bootProjectScopedUI\(\).*?await loadFlows\(\).*?maybeShowSetup\(\)`)
	requireUIRegex(t, app, `(?s)api\('/api/project'\).*?api\('/api/version'\).*?return 'default'`)
	if strings.Contains(app, "setTimeout(()=>{if(state.flows&&!state.flows.length)maybeShowSetup()") {
		t.Error("first-run setup still depends on an arbitrary timer")
	}
	requireUIContains(t, index, `id="findExportStatuses"`)
	requireUIContains(t, intercept, "intercept-danger", "held")
	requireUIContains(t, index,
		`id="interceptWarning"`,
		`id="findExportStatuses"`,
		`id="scanClear"`,
		`id="scanRescanState"`,
		`id="sharePrereq"`,
	)
	requireUIContains(t, index, `id="findEmptyNew"`, `find-view is-empty`, `class="state-empty find-empty"`)
	requireUIContains(t, findings, "findingsEmptyHTML(", "setFindingsViewEmpty(", "findEmptyNew", "is-empty")
}

func TestUIHasNoBuiltInProviderSurfaces(t *testing.T) {
	assets := []string{"index.html", "app.css", "js/app.js", "js/settings.js", "js/findings.js", "js/scanner.js", "js/codecs.js", "js/notes.js", "js/tools.js", "js/setup.js", "js/proxy.js"}
	for _, asset := range assets {
		body := readUIAsset(t, asset)
		for _, forbidden := range []string{"/api/ai", "/api/autopwn", "Ask AI", "Autopilot", "aiProvider", "setAiProvider", "autopwn.update"} {
			if strings.Contains(body, forbidden) {
				t.Errorf("%s retains removed built-in provider surface %q", asset, forbidden)
			}
		}
	}
	for _, asset := range []string{"js/ai.js", "js/autopwn.js"} {
		if _, err := os.Stat(filepath.Join("ui", asset)); !os.IsNotExist(err) {
			t.Errorf("removed UI asset exists: %s", asset)
		}
	}
}

func TestUIJourneyCodecsListUsesChecksRowLayout(t *testing.T) {
	index := readUIAsset(t, "index.html")
	codecs := executableJS(readUIAsset(t, "js/codecs.js"))
	css := readUIAsset(t, "app.css")
	requireUIContains(t, index,
		`id="codecsList"`,
		`class="codecs-list"`,
		`id="codecsDirHint"`,
		`id="codecModeSeg"`,
		`id="codecPaneCode"`,
		`id="codecTest"`,
		`id="codecSave"`,
		`id="codecPaneDocs"`,
		`id="codecDocs"`,
		`id="codecOut"`,
		`id="codecsSearch"`,
	)
	requireUIContains(t, codecs,
		"checks-row checks-pick codecs-row",
		"checks-title",
		"checks-meta",
		"wireRowKey(",
		"re-encode on send",
		"codecsDirLabel(",
		"codecSetMode(",
		"/api/codecs/reference",
		"loadCodecDocs(",
	)
	if strings.Contains(codecs, `class="h${`) || strings.Contains(codecs, `".h${codecSel`) {
		t.Error("codecs list still renders unstyled history .h rows")
	}
	if strings.Contains(index, `id="codecTestOut"`) {
		t.Error("codecs modal still uses legacy codecTestOut instead of Checks-style panes")
	}
	requireUIContains(t, css, ".codecs-list .codecs-row", ".codecs-dir-hint", "#codecOut")
}
