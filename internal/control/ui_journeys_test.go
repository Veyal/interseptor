package control

import (
	"strings"
	"testing"
)

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

func TestUIJourneyReadinessProjectScannerReportInterceptAndShareContracts(t *testing.T) {
	setup := executableJS(readUIAsset(t, "js/setup.js"))
	autopwn := executableJS(readUIAsset(t, "js/autopwn.js"))
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
	requireUIContains(t, autopwn,
		"'/api/readiness'",
		"'scope'",
		"'ai_provider'",
	)
	requireUIRegex(t, autopwn, `(?s)api\('/api/readiness'\).*?api\('/api/autopwn/start'`)
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
	requireUIContains(t, findings, "findExportStatuses", "statuses=")
	requireUIContains(t, intercept, "intercept-danger", "held")
	requireUIContains(t, index,
		`id="interceptWarning"`,
		`id="findExportStatuses"`,
		`id="scanClear"`,
		`id="scanRescanState"`,
		`id="sharePrereq"`,
	)
}
