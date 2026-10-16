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
		"localStorage.setItem(projectStorageKey(MAP_DOMAIN_KEY)",
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
	requireUIRegex(t, activity, `(?s)if\(id\)\{.*?selectFlow\(id\);return;\}.*?row\.onclick=open;wireRowKey\(row,open\)`)
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

func TestUIJourneyAsyncEditorStateSurvivesLateCompletions(t *testing.T) {
	core := executableJS(readUIAsset(t, "js/core.js"))
	notes := executableJS(readUIAsset(t, "js/notes.js"))
	tools := executableJS(readUIAsset(t, "js/tools.js"))
	codecs := executableJS(readUIAsset(t, "js/codecs.js"))
	mapJS := executableJS(readUIAsset(t, "js/map.js"))

	requireUIContains(t, core,
		"while(current!==lastSaved)",
		"let submitted=current",
		"lastSaved=submitted",
		"preserveCurrent=false",
	)
	requireUIContains(t, notes,
		"let notesEditGeneration=0",
		"notesEditGeneration!==generation",
		"preserveCurrent:preserveLocal",
	)
	requireUIContains(t, tools,
		"sendError:''",
		"t.sendError=msg",
		"else if(t.sendError)setRepSendState('error','Send failed')",
	)
	recordError := strings.Index(tools, "t.sendError=msg")
	backgroundGuard := -1
	if recordError >= 0 {
		backgroundGuard = strings.Index(tools[recordError:], "if(repCur()!==t)")
	}
	if recordError < 0 || backgroundGuard < 0 {
		t.Error("Repeater must record a source-tab error before guarding active-tab paint")
	}
	requireUIContains(t, codecs,
		"function codecEditorMatches(epoch, id, source)",
		"if (!codecEditorMatches(epoch, id, source)) return",
	)
	requireUIContains(t, mapJS,
		"fillMapDomains(mapState.noiseHiddenCount>0?mapState.domain:'')",
		"mapState.domain !== preserveMissing",
	)
}

func TestUIJourneySettingsUpstreamProxyCredentialsAreOptional(t *testing.T) {
	index := readUIAsset(t, "index.html")
	settings := executableJS(readUIAsset(t, "js/settings.js"))
	css := readUIAsset(t, "app.css")
	requireUIContains(t, index,
		`id="upstreamProxySection"`,
		`id="setUpstreamScheme"`,
		`value="direct"`,
		`value="http"`,
		`value="https"`,
		`value="socks5"`,
		`value="socks5h"`,
		`id="setUpstreamHost"`,
		`id="setUpstreamPort"`,
		`id="setUpstreamUser"`,
		`id="setUpstreamPassword"`,
		`id="setUpstreamCA"`,
		`id="upstreamSchemeHelp" hidden`,
		`id="upstreamProxySummary"`,
	)
	requireUIContains(t, settings,
		"parseUpstreamProxyURL(",
		"renderUpstreamProxyFields(",
		"buildUpstreamProxyURL(",
		"setUpstreamScheme",
		"setUpstreamHost",
		"setUpstreamPort",
		"setUpstreamUser",
		"setUpstreamPassword",
		"setUpstreamCA",
		"upstreamProxyCA",
		"new URL(raw)",
	)
	if strings.Contains(settings, "encodeURIComponent($('#setUpstreamUser').value.trim())") {
		t.Error("upstream proxy URL must be built with URL.username instead of manual userinfo encoding")
	}
	requireUIContains(t, css,
		`.settings-wrap:not(.split) .settings-nav-group{display:contents}`,
	)
}

func TestUIJourneySettingsRetainsNonAIControls(t *testing.T) {
	settings := executableJS(readUIAsset(t, "js/settings.js"))

	requireUIContains(t, settings,
		"document.querySelector('#panel-settings .settings-body')",
		"function setCapScope(",
		"function setSuppressTelemetry(",
		"function setSuppressAndroidTelemetry(",
		"function setInvisibleProxy(",
		"function setFindingsUIEditing(",
		"function setAutoBypass(",
		"saveBooleanSetting('captureScopeOnly',on",
		"saveBooleanSetting('suppressBrowserTelemetry',on",
		"saveBooleanSetting('suppressAndroidTelemetry',on",
		"saveBooleanSetting('invisibleProxy',on",
		"saveBooleanSetting('findingsUIEditing',on",
		"saveBooleanSetting('autoBypassOnPinFailure',on",
		"saveSettingsPatch({tlsBypassHosts:next})",
		"buildUpstreamProxyURL()",
		"upstreamProxyCA",
	)
}

func TestUIBrowserBackgroundSuppressionExplainsScope(t *testing.T) {
	index := readUIAsset(t, "index.html")
	settings := executableJS(readUIAsset(t, "js/settings.js"))
	requireUIContains(t, index,
		`Browser background traffic`,
		`What is hidden?`,
		`New traffic only; forwarding and existing History are unchanged`,
		`Normal website and application traffic remains visible`,
	)
	requireUIContains(t, settings,
		`Suppressing browser background traffic`,
		`Capturing browser background traffic`,
		`New browser background traffic will be forwarded without capture`,
		`Browser background suppression disabled; other capture policies still apply`,
		`Android telemetry suppression disabled; other capture policies still apply`,
		`document.activeElement===control`,
		`focusReturn.focus({preventScroll:true})`,
	)
	if strings.Contains(settings, "Allowing browser telemetry") {
		t.Error("suppression-off label must describe capture visibility, not imply that suppression blocks network traffic")
	}
}

func TestUIJourneyOriginTLSVerificationToggle(t *testing.T) {
	index := readUIAsset(t, "index.html")
	settings := executableJS(readUIAsset(t, "js/settings.js"))
	requireUIContains(t, index,
		`id="originTLSVerifyMode"`,
		`Compatibility — accept test certificates`,
		`Strict — verify origin certificates`,
		`id="originTLSVerifyBypassHost"`,
		`id="originTLSVerifyBypassAdd"`,
		`id="originTLSVerifyBypassSelected"`,
		`doesn't contain any IP SANs`,
	)
	requireUIContains(t, settings,
		"s.originTLSVerify",
		"saveBooleanSetting('originTLSVerify',on",
		"setOriginTLSVerify(",
		"addOriginTLSVerifyException(",
		"selectedOriginHost(",
		"loadSettings();",
		"document.activeElement!==ol",
	)
}

func TestUIJourneyFindingAttachedFlowRepeaterAction(t *testing.T) {
	findings := executableJS(readUIAsset(t, "js/findings.js"))
	tools := readUIAsset(t, "js/tools.js")
	css := readUIAsset(t, "app.css")
	requireUIContains(t, findings,
		`import { sendToRepeater } from './tools.js';`,
		`find-send-repeater`,
		`aria-label="Send attached flow #`,
		`sendToRepeater({ id });`,
		`!block.missing`,
		`find-report-flow`,
		`find-evidence-actions`,
		`wireSendToRepeaterButtons`,
	)
	requireUIContains(t, tools,
		`sendToRepeater`,
		`'/api/flows/'`,
		`raw?side=req`,
		`repFlowEndpoint(d)`,
		`method=d.method`,
		`headersToText(d.reqHeaders)`,
		`sourceFlowId=f.id`,
	)
	// Resolve the flow before choosing a Repeater tab. Findings intentionally
	// pass only {id}; using the partial object makes every action target an
	// "undefined" endpoint tab. Do not navigate away until both reads succeed.
	loadFlow := strings.Index(tools, "const d=await api('/api/flows/'+f.id)")
	chooseTab := strings.Index(tools, "repFlowEndpoint(d)")
	navigate := strings.Index(tools, `document.querySelector('.tab[data-tab="repeater"]').click()`)
	if loadFlow < 0 || chooseTab < loadFlow || navigate < chooseTab {
		t.Error("Send to Repeater must load metadata, select the real endpoint tab, then navigate")
	}
	requireUIContains(t, css,
		`[hidden]{display:none!important}`,
		`.find-evidence-actions`,
		`.find-report-stepbody`,
	)
	// Copy belongs to reader navigation and captured evidence; replay continues
	// to use the shared helper rather than reconstructing a request here.
	if strings.Contains(findings, "fetch('/api/flows") || strings.Contains(findings, "api('/api/flows/'+id+'/raw") {
		t.Error("finding flow action reconstructs request instead of using Repeater helper")
	}
}

func TestUIJourneyRepeaterRoutingUsesFullEndpointIdentity(t *testing.T) {
	tools := readUIAsset(t, "js/tools.js")

	// Cross-tool routing may reuse an unedited Repeater tab for the same
	// endpoint, so that identity must distinguish scheme, host, port, and
	// queryless path. The tab's send history itself is deliberately not keyed by
	// this mutable endpoint; that contract lives in ui_repeater_tab_history_test.
	requireUIContains(t, tools,
		"function repEndpointParts(",
		"function repEndpointAuthority(",
		"authority.replace(/%/g,'%25')",
		"export function repTabEndpoint(",
		"export function repFlowEndpoint(",
	)
	if strings.Count(tools, "repEndpointAuthority(d.scheme,d.host,d.port)") != 2 {
		t.Error("Repeater flow-loading paths must use the shared endpoint authority formatter")
	}
}

func TestUIJourneyToolTabsExposeUnambiguousTabSemantics(t *testing.T) {
	tools := readUIAsset(t, "js/tools.js")
	core := readUIAsset(t, "js/core.js")
	// Repeater and Intruder tab items include a close control. Explicit tab
	// semantics belong in the shared renderer so re-renders cannot restore a
	// button-like parent around the nested Close action.
	requireUIContains(t, core,
		"tablistLabel='Tabs'",
		"class=\"rt-select\"",
		"role','tablist'",
		"role=\"tab\"",
		"aria-selected=",
		"ArrowRight",
		"Home",
		"mgr._focusTab",
		"mgr.switchTo=function(tid,restoreFocus=false)",
		"mgr.close=function(tid,restoreFocus=false)",
	)
	if strings.Contains(core, "wireRowKey(el,()=>mgr.switchTo") {
		t.Fatal("tool tabs must not wrap their Close button in a button-like tab row")
	}
	requireUIContains(t, tools,
		"tablistLabel:'Repeater tabs'",
		"tablistLabel:'Intruder tabs'",
		"function wireButtonGroupKeys(",
		"intrResFilter",
	)
}

func TestUIJourneyRepeaterDoesNotPaintAResponseIntoAnotherTab(t *testing.T) {
	tools := readUIAsset(t, "js/tools.js")
	start := strings.Index(tools, "const flow=await api('/api/repeater/send'")
	if start < 0 {
		t.Fatal("Repeater send request is missing")
	}
	guard := strings.Index(tools[start:], "if(repCur()!==t||!current())return;")
	assign := strings.Index(tools[start:], "t.resId=flow.id")
	paint := strings.Index(tools[start:], "$('#repStatus').textContent=t.status")
	if guard < 0 || assign < 0 || paint < 0 || guard < assign || guard > paint {
		t.Fatal("Repeater must guard shared response panes when the active tab changes mid-send")
	}
}

func TestUIJourneyRepeaterPendingStateFollowsItsSourceTab(t *testing.T) {
	tools := readUIAsset(t, "js/tools.js")
	requireUIContains(t, tools,
		"t.sendPending=true",
		"t.sendPending=false",
		"if(t.sendPending)setRepSendState('pending','Sending…')",
		"else setRepSendState('idle','Send ▸')",
	)
}

func TestUIJourneyIntruderValidatesNumericLimitsBeforeStart(t *testing.T) {
	tools := readUIAsset(t, "js/tools.js")
	start := strings.Index(tools, "export async function intrStart()")
	if start < 0 {
		t.Fatal("Intruder start handler is missing")
	}
	section := tools[start:]
	if !strings.Contains(section, "threads must be between 1 and 64") ||
		!strings.Contains(section, "repeat must be between 1 and 2000") {
		t.Fatal("Intruder must reject out-of-range numeric inputs before starting an attack")
	}
}

func TestUIJourneyFindingsHasWritingGuideAndResponsiveReadingLayout(t *testing.T) {
	index := readUIAsset(t, "index.html")
	findings := executableJS(readUIAsset(t, "js/findings.js"))
	css := readUIAsset(t, "app.css")
	requireUIContains(t, index,
		`id="findGuide"`,
		`id="findGuideModal"`,
		`Technical finding template`,
		`Reproduction`,
		`Evidence`,
		`Remediation`,
	)
	requireUIContains(t, findings,
		`findGuideModal`,
		`findGuideClose`,
	)
	requireUIContains(t, css,
		`@media (max-width:900px)`,
		`.find-view{flex-direction:column}`,
		`.find-view .scan-list{width:100%`,
		`@media (max-width:720px)`,
		`#appRow{flex-direction:column}`,
		`#tabs{display:none}`,
	)
}

func TestUIJourneyReadinessProjectScannerReportInterceptAndShareContracts(t *testing.T) {
	setup := executableJS(readUIAsset(t, "js/setup.js"))
	scanner := executableJS(readUIAsset(t, "js/scanner.js"))
	settings := executableJS(readUIAsset(t, "js/settings.js"))
	app := executableJS(readUIAsset(t, "js/app.js"))
	projectJS := executableJS(readUIAsset(t, "js/project.js"))
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
	requireUIContains(t, app, "await projectStorageReady", "await bootProjectScopedUIWithDeadline(bootProjectScopedUI())", "await loadFlows()", "maybeShowSetup()")
	requireUIContains(t, projectJS, "'/api/project'", "'/api/version'", "Promise.allSettled", "throw new Error('active project unavailable')")
	requireUIRegex(t, app, `(?s)await bootProjectScopedUIWithDeadline\(bootProjectScopedUI\(\)\).*?await loadFlows\(\).*?maybeShowSetup\(\)`)
	if strings.Contains(projectJS, "return 'default'") {
		t.Error("project identity must fail closed instead of selecting an unverified default workspace")
	}
	if strings.Contains(app, "setTimeout(()=>{if(state.flows&&!state.flows.length)maybeShowSetup()") {
		t.Error("first-run setup still depends on an arbitrary timer")
	}
	requireUIContains(t, executableJS(readUIAsset(t, "js/report-preflight.js")), `id="reportStatuses"`)
	requireUIContains(t, intercept, "intercept-danger", "held")
	requireUIContains(t, index,
		`id="interceptWarning"`,
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

func TestUIJourneyToolsAndScannerAsyncActionContracts(t *testing.T) {
	tools := readUIAsset(t, "js/tools.js")
	scanner := readUIAsset(t, "js/scanner.js")
	index := readUIAsset(t, "index.html")

	// The shortcut hint must survive Repeater's transient Send states.
	requireUIContains(t, index, `id="repSendLabel"`)
	requireUIContains(t, tools, "repSendLabel", "setRepSendState", "intrStartPending", "intrPollError", "data-intr-poll-retry")
	if !strings.Contains(tools, "if(running&&!st.pollFailed)scheduleIntr()") {
		t.Error("Intruder polling must pause after an explicit poll failure until Retry")
	}

	// Checks actions must call the string endpoint, expose pending state, and
	// prevent a second Test/Save while the first request is in flight.
	if strings.Contains(scanner, "checkEndpoint()") {
		t.Error("Checks Test/Save must not call the string endpoint as a function")
	}
	requireUIContains(t, scanner, "setCheckActionState", "checkActionEpoch", "aria-busy")

	// OOB clear/load and scanner promotion are destructive or duplicate-prone;
	// their contracts require visible retry/pending state.
	requireUIContains(t, index, `id="oobLoadState"`)
	requireUIContains(t, scanner,
		"data-oob-retry",
		"oobClearEpoch",
		"oobGenerateEpoch",
		"promoteFindingPending",
		"setPromoteFindingState",
	)

	// Scanner controls expose their selection and target to assistive tech.
	requireUIContains(t, index, `aria-label="Scanner target host"`, `aria-selected="false"`)
}
