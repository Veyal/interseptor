package control

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestUIBrowserAuditEvidenceMatchesCurrentRuntime(t *testing.T) {
	repoRoot := filepath.Clean("../..")
	reportBytes, err := os.ReadFile(filepath.Join(repoRoot, "docs/ui-audit/browser-audit.json"))
	if err != nil {
		t.Fatalf("read retained UI browser audit: %v", err)
	}
	var report struct {
		ApplicationSource struct {
			RuntimeSHA256      string `json:"runtime_sha256"`
			RuntimeFiles       int    `json:"runtime_files"`
			AuditHarnessSHA256 string `json:"audit_harness_sha256"`
		} `json:"application_source"`
	}
	if err := json.Unmarshal(reportBytes, &report); err != nil {
		t.Fatalf("decode retained UI browser audit: %v", err)
	}

	cmd := exec.Command("git", "ls-files", "--cached", "--others", "--exclude-standard", "-z", "--", "go.mod", "go.sum", "cmd", "internal")
	cmd.Dir = repoRoot
	listed, err := cmd.Output()
	if err != nil {
		t.Fatalf("enumerate runtime source: %v", err)
	}
	unique := make(map[string]struct{})
	for _, raw := range bytes.Split(listed, []byte{0}) {
		if len(raw) == 0 {
			continue
		}
		relative := string(raw)
		if strings.HasSuffix(relative, "_test.go") {
			continue
		}
		info, err := os.Stat(filepath.Join(repoRoot, filepath.FromSlash(relative)))
		if err != nil {
			t.Fatalf("stat runtime source %s: %v", relative, err)
		}
		if info.Mode().IsRegular() {
			unique[relative] = struct{}{}
		}
	}
	paths := make([]string, 0, len(unique))
	for relative := range unique {
		paths = append(paths, relative)
	}
	sort.Strings(paths)
	digest := sha256.New()
	for _, relative := range paths {
		content, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(relative)))
		if err != nil {
			t.Fatalf("read runtime source %s: %v", relative, err)
		}
		_, _ = digest.Write([]byte(relative))
		_, _ = digest.Write([]byte{0})
		fileDigest := sha256.Sum256(content)
		_, _ = digest.Write(fileDigest[:])
	}

	gotDigest := hex.EncodeToString(digest.Sum(nil))
	if gotDigest != report.ApplicationSource.RuntimeSHA256 || len(paths) != report.ApplicationSource.RuntimeFiles {
		t.Fatalf("retained UI audit source identity is stale: got %s across %d files, evidence records %s across %d files", gotDigest, len(paths), report.ApplicationSource.RuntimeSHA256, report.ApplicationSource.RuntimeFiles)
	}
	harness, err := os.ReadFile(filepath.Join(repoRoot, "scripts/ui_browser_audit.py"))
	if err != nil {
		t.Fatalf("read UI audit harness: %v", err)
	}
	harnessDigest := sha256.Sum256(harness)
	if got := hex.EncodeToString(harnessDigest[:]); got != report.ApplicationSource.AuditHarnessSHA256 {
		t.Fatalf("retained UI audit harness identity is stale: got %s, evidence records %s", got, report.ApplicationSource.AuditHarnessSHA256)
	}
}

func TestUIBrowserAuditRetainsCoreAndFindingsScreenshots(t *testing.T) {
	repoRoot := filepath.Clean("../..")
	reportBytes, err := os.ReadFile("../../docs/ui-audit/browser-audit.json")
	if err != nil {
		t.Fatalf("read retained UI browser audit: %v", err)
	}
	var report struct {
		Metrics struct {
			Screenshots map[string]struct {
				Path   string `json:"path"`
				Width  int    `json:"width"`
				Height int    `json:"height"`
			} `json:"screenshots"`
		} `json:"metrics"`
	}
	if err := json.Unmarshal(reportBytes, &report); err != nil {
		t.Fatalf("decode retained UI browser audit: %v", err)
	}
	want := map[string]struct {
		path   string
		width  int
		height int
	}{
		"1440x900":          {"docs/ui-audit/after-1440x900-proxy.png", 1440, 900},
		"1024x768":          {"docs/ui-audit/after-1024x768-map.png", 1024, 768},
		"390x844":           {"docs/ui-audit/after-390x844-scanner.png", 390, 844},
		"findings-1440x900": {"docs/ui-audit/findings-after-1440x900.png", 1440, 900},
		"findings-1024x768": {"docs/ui-audit/findings-after-1024x768.png", 1024, 768},
		"findings-390x844":  {"docs/ui-audit/findings-after-390x844.png", 390, 844},
	}
	for key, expected := range want {
		got, ok := report.Metrics.Screenshots[key]
		if !ok {
			t.Errorf("retained UI audit is missing %s screenshot", key)
			continue
		}
		if got.Path != expected.path || got.Width != expected.width || got.Height != expected.height {
			t.Errorf("%s screenshot = (%q, %dx%d), want (%q, %dx%d)", key, got.Path, got.Width, got.Height, expected.path, expected.width, expected.height)
			continue
		}
		png, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(got.Path)))
		if err != nil {
			t.Errorf("read %s screenshot: %v", key, err)
			continue
		}
		if len(png) < 24 || !bytes.Equal(png[:8], []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}) || string(png[12:16]) != "IHDR" {
			t.Errorf("%s screenshot is not a valid PNG with an IHDR", key)
			continue
		}
		width := int(binary.BigEndian.Uint32(png[16:20]))
		height := int(binary.BigEndian.Uint32(png[20:24]))
		if width != expected.width || height != expected.height {
			t.Errorf("%s PNG is %dx%d, want %dx%d", key, width, height, expected.width, expected.height)
		}
	}
}

func TestUIBrowserAuditDigestExcludesIgnoredWorkspaceState(t *testing.T) {
	source, err := os.ReadFile("../../scripts/ui_browser_audit.py")
	if err != nil {
		t.Fatalf("read UI browser audit: %v", err)
	}
	text := string(source)
	for _, want := range []string{
		`"git",`,
		`"ls-files",`,
		`"--cached"`,
		`"--others"`,
		`"--exclude-standard"`,
		`"-z"`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("runtime identity must enumerate tracked or nonignored source files: missing %s", want)
		}
	}

	identityStart := strings.Index(text, "def runtime_source_identity()")
	if identityStart < 0 {
		t.Fatal("runtime_source_identity function not found")
	}
	identityEnd := strings.Index(text[identityStart:], "\ndef percentile(")
	if identityEnd < 0 {
		t.Fatal("runtime_source_identity function boundary not found")
	}
	identity := text[identityStart : identityStart+identityEnd]
	if strings.Contains(identity, ".rglob(") {
		t.Error("runtime identity must not hash ignored workspace files from a recursive filesystem walk")
	}
}

func TestUIBrowserAuditExpectedConsoleErrorsAreOneShot(t *testing.T) {
	source, err := os.ReadFile("../../scripts/ui_browser_audit.py")
	if err != nil {
		t.Fatalf("read UI browser audit: %v", err)
	}
	if !strings.Contains(string(source), "result.expected_console_request_urls.remove(request_url)") {
		t.Error("an expected injected console error must not mask later failures at the same URL")
	}
}

func TestUIBrowserAuditFullModeRequiresAndVerifiesIsolatedTarget(t *testing.T) {
	source, err := os.ReadFile("../../scripts/ui_browser_audit.py")
	if err != nil {
		t.Fatalf("read UI browser audit: %v", err)
	}
	text := string(source)
	for _, want := range []string{
		"--expected-project",
		"--expected-data-dir",
		"--expected-data-dir must",
		".interseptor-ui-audit-sentinel",
		"sentinel",
		"project=",
		"ui-audit-",
		"disposable_project",
		"project_switch_locked",
		`project.get("canSwitch") is not False`,
		"/api/version",
		"/api/project",
		"/api/settings",
		"settings.proxyAddr",
		"upstreamProxy",
		"preflight",
		"data_dir_match",
		"commonpath",
		"projects",
		"safe bare project",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("full audit must verify isolated server identity: missing %s", want)
		}
	}
	if strings.Contains(text, `server project {observed_project!r}`) {
		t.Error("full audit must not expose a live project name in diagnostics")
	}
	if strings.Contains(text, `server project {observed_project}`) || strings.Contains(text, `project {observed_project`) {
		t.Error("full audit must not interpolate live project identity in diagnostics")
	}
	if strings.Contains(text, `server proxy {observed_proxy`) {
		t.Error("full audit must not interpolate a live proxy address in diagnostics")
	}
}

func TestUIBrowserAuditDirectProxyDiagnosticsAreBounded(t *testing.T) {
	source, err := os.ReadFile("../../scripts/ui_browser_audit.py")
	if err != nil {
		t.Fatalf("read UI browser audit: %v", err)
	}
	text := string(source)
	if !strings.Contains(text, "len(body)") || !strings.Contains(text, "sha256(body)") {
		t.Error("direct proxy failures must include bounded response metadata")
	}
	if strings.Contains(text, "response body {sample") {
		t.Error("direct proxy diagnostics must use a digest, never echo response content")
	}
}

func TestUIBrowserAuditDefaultProxyGuardChecksHostAndPortTogether(t *testing.T) {
	source, err := os.ReadFile("../../scripts/ui_browser_audit.py")
	if err != nil {
		t.Fatalf("read UI browser audit: %v", err)
	}
	text := string(source)
	if !strings.Contains(text, "--full requires --managed") || !strings.Contains(text, `"127.0.0.1"`) {
		t.Error("full audit must use the managed loopback candidate")
	}
}

func TestUIBrowserAuditFullPreflightRequiresFreshEmptyCollections(t *testing.T) {
	source, err := os.ReadFile("../../scripts/ui_browser_audit.py")
	if err != nil {
		t.Fatalf("read UI browser audit: %v", err)
	}
	text := string(source)
	for _, want := range []string{"/api/flows?limit=1&includeTools=1", "/api/findings?view=summary", "isinstance(flows.get(\"flows\"), list)", "isinstance(findings.get(\"findings\"), list)", "fresh"} {
		if !strings.Contains(text, want) {
			t.Errorf("full audit preflight must verify fresh empty data: missing %s", want)
		}
	}
}

func TestUIBrowserAuditDataRootIsStrictTemporaryDescendant(t *testing.T) {
	source, err := os.ReadFile("../../scripts/ui_browser_audit.py")
	if err != nil {
		t.Fatalf("read UI browser audit: %v", err)
	}
	text := string(source)
	for _, want := range []string{"tempfile.gettempdir()", "candidate.is_symlink()", "interseptor-ui-audit-", "projects_dir.is_symlink()", "expected_project_path.is_symlink()", "observed_path == expected_project_dir", "sentinel.stat().st_size"} {
		if !strings.Contains(text, want) {
			t.Errorf("full audit data root safety missing %s", want)
		}
	}
}

func TestUIBrowserAuditSmokeDoesNotToggleTelemetry(t *testing.T) {
	source, err := os.ReadFile("../../scripts/ui_browser_audit.py")
	if err != nil {
		t.Fatalf("read UI browser audit: %v", err)
	}
	text := string(source)
	settingsStart := strings.Index(text, "def inner_tabs_and_settings()")
	if settingsStart < 0 {
		t.Fatal("settings audit function not found")
	}
	settingsEnd := strings.Index(text[settingsStart:], "\n        result.run(\"Repeater/Intruder inner tabs")
	if settingsEnd < 0 {
		t.Fatal("settings audit function not found")
	}
	settings := text[settingsStart : settingsStart+settingsEnd]
	guard := strings.Index(settings, "if args.full:")
	mutation := strings.Index(settings, `toggle.press("Enter")`)
	if guard < 0 || mutation < 0 || guard > mutation {
		t.Fatal("telemetry mutation must remain guarded by full mode")
	}
	preflight := strings.Index(settings[guard:mutation], "full_audit_preflight(")
	if preflight < 0 {
		t.Fatal("full mode must revalidate the disposable project immediately before the telemetry mutation")
	}
}

func TestUIBrowserAuditRefreshesPreflightBeforeMutationBlocks(t *testing.T) {
	source, err := os.ReadFile("../../scripts/ui_browser_audit.py")
	if err != nil {
		t.Fatalf("read UI browser audit: %v", err)
	}
	text := string(source)
	if strings.Count(text, "full_audit_preflight(") < 4 { // definition + initial + two mutation-boundary checks
		t.Error("full audit must preflight initially and before both mutation blocks")
	}
}

func TestUIBrowserAuditFlowNoteJourneyClosesItsModal(t *testing.T) {
	source, err := os.ReadFile("../../scripts/ui_browser_audit.py")
	if err != nil {
		t.Fatalf("read UI browser audit: %v", err)
	}
	text := string(source)
	start := strings.Index(text, "def flow_note_read_waits_for_save()")
	if start < 0 {
		t.Fatal("flow-note audit journey not found")
	}
	end := strings.Index(text[start:], `result.run("Inspector reads wait for their flow's acknowledged note save"`)
	if end < 0 {
		t.Fatal("flow-note audit journey boundary not found")
	}
	journey := text[start : start+end]
	if strings.Count(journey, "invalidate_and_close_flow_popup(page)") < 2 {
		t.Fatal("flow-note journey must invalidate and close a pre-existing popup and guarantee the same during teardown")
	}
	closeStart := strings.Index(text, "def invalidate_and_close_flow_popup(page: Page)")
	if closeStart < 0 {
		t.Fatal("flow-popup invalidation helper not found")
	}
	closeEnd := strings.Index(text[closeStart:], "\n\ndef ")
	if closeEnd < 0 {
		t.Fatal("flow-popup invalidation helper boundary not found")
	}
	closeHelper := text[closeStart : closeStart+closeEnd]
	if !strings.Contains(closeHelper, `button.click()`) {
		t.Fatal("flow-popup cleanup must click the close control even while hidden so an in-flight popup is invalidated")
	}
	for _, want := range []string{"deadline = time.monotonic() + 15", "hidden_since", "time.monotonic() - hidden_since >= 2"} {
		if !strings.Contains(closeHelper, want) {
			t.Fatalf("flow-popup cleanup must require a stable hidden interval: missing %s", want)
		}
	}
}

func TestUIBrowserAuditLateJourneysCloseTransientFlowPopup(t *testing.T) {
	source, err := os.ReadFile("../../scripts/ui_browser_audit.py")
	if err != nil {
		t.Fatalf("read UI browser audit: %v", err)
	}
	text := string(source)
	for _, marker := range []string{
		"def burst_and_map_performance()",
		"def mobile_dense_reachability()",
		"def screenshots()",
	} {
		start := strings.Index(text, marker)
		if start < 0 {
			t.Fatalf("late audit journey %q not found", marker)
		}
		end := start + 360
		if end > len(text) {
			end = len(text)
		}
		if !strings.Contains(text[start:end], "invalidate_and_close_flow_popup(page)") {
			t.Errorf("late audit journey %q must close a transient popup before navigation", marker)
		}
	}
}

func TestUIBrowserAuditPerformanceRetriesOnlyVisibleFlowPopup(t *testing.T) {
	source, err := os.ReadFile("../../scripts/ui_browser_audit.py")
	if err != nil {
		t.Fatalf("read UI browser audit: %v", err)
	}
	text := string(source)
	start := strings.Index(text, "def burst_and_map_performance()")
	if start < 0 {
		t.Fatal("performance journey not found")
	}
	end := strings.Index(text[start:], `result.run("high-volume History, Map render/Fit, scroll, and CDP performance"`)
	if end < 0 {
		t.Fatal("performance journey boundary not found")
	}
	journey := text[start : start+end]
	for _, want := range []string{
		`page.locator('.tab[data-tab="proxy"]').click(timeout=2_000)`,
		`if not page.locator("#flowModal").is_visible():`,
		`page.locator('.tab[data-tab="proxy"]').click(force=True)`,
		`invalidate_and_close_flow_popup(page)`,
	} {
		if !strings.Contains(journey, want) {
			t.Errorf("performance popup retry contract missing %s", want)
		}
	}
	if strings.Count(journey, "invalidate_and_close_flow_popup(page)") < 4 {
		t.Error("performance journey must re-establish popup quiescence after the burst and before later panel navigation")
	}
}

func TestUIBrowserAuditAuthzContextMenuRetriesLiveDismissal(t *testing.T) {
	source, err := os.ReadFile("../../scripts/ui_browser_audit.py")
	if err != nil {
		t.Fatalf("read UI browser audit: %v", err)
	}
	text := string(source)
	start := strings.Index(text, "def authz_context_target()")
	if start < 0 {
		t.Fatal("Authz context-target journey not found")
	}
	end := strings.Index(text[start:], `result.run("Authz explicit context and A-to-B-to-A retargeting"`)
	if end < 0 {
		t.Fatal("Authz context-target journey boundary not found")
	}
	journey := text[start : start+end]
	if !strings.Contains(journey, "for attempt in range(2)") || !strings.Contains(journey, "if attempt == 1:") {
		t.Fatal("Authz context-target journey must retry once when live History refresh dismisses its context menu")
	}
}

func TestUIBrowserAuditFullModeOwnsDisposableProcess(t *testing.T) {
	source, err := os.ReadFile("../../scripts/ui_browser_audit.py")
	if err != nil {
		t.Fatalf("read UI browser audit: %v", err)
	}
	text := string(source)
	for _, want := range []string{
		"--managed",
		"--full requires --managed",
		"arbitrary existing servers are not accepted",
		"--full --managed chooses its own disposable server and data root",
		"CGO_ENABLED",
		"INTERSEPTOR_UI_AUDIT_MANAGED",
		"INTERSEPTOR_NO_UPDATE_CHECK",
		"INTERSEPTOR_NO_BROWSER",
		`not key.startswith("INTERSEPTOR_")`,
		"env=managed_candidate_env(",
		"subprocess.Popen",
		"--data-dir",
		"--project",
		"terminate()",
		"shutil.rmtree",
		"sentinel.is_file()",
		"sentinel.read_text",
		"remove_managed_root(root, project)",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("managed full audit ownership missing %s", want)
		}
	}
}

func TestUIBrowserAuditManagedChildOwnsReservedListeners(t *testing.T) {
	source, err := os.ReadFile("../../scripts/ui_browser_audit.py")
	if err != nil {
		t.Fatalf("read UI browser audit: %v", err)
	}
	text := string(source)
	for _, want := range []string{
		"def reserve_loopback_listener()",
		"INTERSEPTOR_UI_AUDIT_CONTROL_FD",
		"INTERSEPTOR_UI_AUDIT_PROXY_FD",
		"pass_fds=",
		"reservations",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("managed full audit must retain and pass owned listeners: missing %s", want)
		}
	}
	if strings.Contains(text, "def free_loopback_port()") {
		t.Error("managed full audit must not select ports by closing free-port probes")
	}
	reserveStart := strings.Index(text, "def reserve_loopback_listener()")
	if reserveStart < 0 {
		t.Fatal("managed listener reservation helper not found")
	}
	reserveEnd := strings.Index(text[reserveStart:], "\n\ndef ")
	if reserveEnd < 0 {
		t.Fatal("managed listener reservation helper boundary not found")
	}
	reserve := text[reserveStart : reserveStart+reserveEnd]
	for _, want := range []string{"listener.bind", "listener.listen", "listener.set_inheritable"} {
		if !strings.Contains(reserve, want) {
			t.Errorf("managed listener reservation is incomplete: missing %s", want)
		}
	}
	cleanupStart := strings.Index(text, "def cleanup_managed_audit(")
	if cleanupStart < 0 {
		t.Fatal("managed cleanup helper not found")
	}
	cleanupEnd := strings.Index(text[cleanupStart:], "\n\ndef ")
	if cleanupEnd < 0 {
		t.Fatal("managed cleanup helper boundary not found")
	}
	cleanup := text[cleanupStart : cleanupStart+cleanupEnd]
	stopAt := strings.Index(cleanup, "stop_managed_process(process)")
	closeAt := strings.Index(cleanup, "close_managed_reservations(reservations)")
	removeAt := strings.Index(cleanup, "remove_managed_root(root, project)")
	if stopAt < 0 || closeAt < stopAt || removeAt < closeAt {
		t.Error("managed cleanup must stop the child, close retained listeners, then remove its root")
	}
}

func TestUIBrowserAuditManagedBuildUsesVerifiedSourceSnapshot(t *testing.T) {
	source, err := os.ReadFile("../../scripts/ui_browser_audit.py")
	if err != nil {
		t.Fatalf("read UI browser audit: %v", err)
	}
	text := string(source)
	for _, want := range []string{
		"def create_runtime_source_snapshot(",
		`root / "runtime-source"`,
		"runtime_source_digest(snapshot, source_paths)",
		`"GOENV": "off"`,
		`"GOWORK": "off"`,
		`"GOFLAGS": ""`,
		`"GOTOOLCHAIN": "local"`,
		`"GOMODCACHE": str(module_cache)`,
		`"GOCACHE": str(build_cache)`,
		`"-mod=readonly"`,
		`"-modcacherw"`,
		"cwd=snapshot",
		"run_audit(args, managed_source)",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("managed full audit source binding missing %s", want)
		}
	}
}
