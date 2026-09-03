package control

import (
	"os"
	"strings"
	"testing"
)

func TestUIWorkspaceRequestsSettleEvenWhenAbortIsIgnored(t *testing.T) {
	project := executableJS(readUIAsset(t, "js/project.js"))
	tools := executableJS(readUIAsset(t, "js/tools.js"))

	for _, contract := range []string{
		"async function boundedProjectIdentity(path,select)",
		"Promise.race([api(path",
		"Promise.allSettled([",
		"controller.abort()",
		"reject(new Error('active project timed out'))",
		"key:project.dir",
		"key:version.projectDir",
	} {
		if !strings.Contains(project, contract) {
			t.Errorf("project identity needs a settling deadline independent of fetch abort: missing %q", contract)
		}
	}
	for _, contract := range []string{
		"Promise.race([request,deadline])",
		"controller.abort()",
		"resolve({status:'error',error:new Error('saved workspace request timed out')})",
	} {
		if !strings.Contains(tools, contract) {
			t.Errorf("saved workspace hydration needs a settling deadline independent of fetch abort: missing %q", contract)
		}
	}
}

func TestUIBrowserAuditExercisesWorkspaceStartupRecovery(t *testing.T) {
	source, err := os.ReadFile("../../scripts/ui_browser_audit.py")
	if err != nil {
		t.Fatalf("read browser audit: %v", err)
	}
	audit := string(source)
	for _, contract := range []string{
		`"--startup-engines"`,
		`def workspace_module_fetch_recovery`,
		`def workspace_module_evaluation_recovery`,
		`def workspace_hydration_recovery`,
		`def project_identity_sibling_recovery`,
		`def pathological_persisted_state_recovery`,
		`def invalid_pending_state_recovery`,
		`def oversized_pending_state_is_not_retried`,
		`def valid_pending_replaces_invalid_server_state`,
		`def browser_storage_failure_keeps_project_sync`,
		`def persisted_tab_identity_and_creation_guards`,
		`def postman_import_preserves_unique_repeater_tabs`,
		`def malformed_tab_fields_and_history_keys_recover`,
		`def delayed_module_eventually_recovers`,
		`state_page.route(`,
		`"**/api/ui/repeater"`,
		`module_page.route("**/js/tools.js"`,
		`injected module evaluation failure`,
		`workspaceStorageWarningMessage=()=>''`,
		"if(url.includes('/api/ui/repeater')){",
		"return new Promise(()=>{})",
		"Workspace scripts could not start",
		"Saved workspace unavailable",
		`reduced_motion="reduce"`,
		`document.activeElement?.hasAttribute('data-workspace-retry')`,
		`module_status.locator("[data-workspace-retry]").focus()`,
		`get_attribute("aria-live") == "assertive"`,
		`get_attribute("aria-live") == "polite"`,
		`for engine_name in args.startup_engines`,
		`f"workspace module fetch failure becomes actionable ({engine_name})"`,
		`f"workspace module evaluation failure becomes actionable ({engine_name})"`,
		`f"workspace hydration timeout falls back locally ({engine_name})"`,
		`f"project identity accepts a valid sibling ({engine_name})"`,
		`"pathological browser-local workspace state remains recoverable"`,
		`"invalid pending workspace state is preserved"`,
		`"oversized pending workspace state is retained without retry"`,
		`"valid pending workspace state replaces invalid server state"`,
		`"browser storage failure keeps project workspace sync"`,
		`"persisted tab identity and creation limits remain safe"`,
		`"Postman import creates unique reload-safe Repeater tabs"`,
		`"malformed tab and preset fields recover with isolated Repeater history"`,
		`[data-workspace-warning-dismiss]`,
		`https://example.com/recovered`,
		`https://example.com/server`,
		`"seq":"not-a-number"`,
		`"late module completion restores the guarded workspace"`,
	} {
		if !strings.Contains(audit, contract) {
			t.Errorf("browser audit does not exercise workspace startup recovery: missing %q", contract)
		}
	}
}

func TestUIWorkspaceBootHasModuleAndTopLevelWatchdogs(t *testing.T) {
	index := readUIAsset(t, "index.html")
	app := executableJS(readUIAsset(t, "js/app.js"))

	for _, contract := range []string{
		"window.__interseptorWorkspaceBoot",
		"Workspace scripts could not start",
		"Workspace initialization is taking longer than expected",
		"window.addEventListener('error', onWorkspaceScriptError, true)",
		"event&&event.error",
		"data-workspace-retry",
		"tabs[i].disabled=true",
		"surface.inert=true",
		"guardedSurfaces[i].inert=false",
		"guardedSurfaces",
		"status.setAttribute('aria-live','assertive')",
	} {
		if !strings.Contains(index, contract) {
			t.Errorf("pre-module workspace watchdog missing %q", contract)
		}
	}
	moduleAt := strings.Index(index, `<script type="module" src="/js/app.js"></script>`)
	watchdogAt := strings.Index(index, "window.__interseptorWorkspaceBoot")
	if watchdogAt < 0 || moduleAt < 0 || watchdogAt > moduleAt {
		t.Error("workspace watchdog must be installed before the application module loads")
	}
	if strings.Contains(index, "controls[k].disabled=true") {
		t.Error("pre-module watchdog must not permanently overwrite feature-owned disabled states")
	}

	for _, contract := range []string{
		"const WORKSPACE_BOOT_TIMEOUT_MS=7000",
		"Promise.race([work,deadline])",
		"error.name='WorkspaceBootTimeout'",
		"settleWorkspaceBootWatchdog()",
		"Workspace initialization timed out",
	} {
		if !strings.Contains(app, contract) {
			t.Errorf("top-level workspace boot deadline missing %q", contract)
		}
	}
	if !strings.Contains(app, "settleWorkspaceBootWatchdog();\nbootFirstRunUI();") {
		t.Error("module watchdog must settle as soon as evaluation completes, before asynchronous data loads")
	}
}

func TestUIWorkspaceRecoveryAlwaysSettlesFeedback(t *testing.T) {
	app := executableJS(readUIAsset(t, "js/app.js"))
	for _, contract := range []string{
		"try{await retryUIStateSync();}",
		"finally{",
		"retry.setAttribute('aria-busy','false')",
		"status.classList.remove('is-error')",
		"status.setAttribute('role','status')",
		"status.setAttribute('aria-live','polite')",
	} {
		if !strings.Contains(app, contract) {
			t.Errorf("workspace recovery feedback can remain stale: missing %q", contract)
		}
	}
}
