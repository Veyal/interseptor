package control

import (
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// WP9 (UI overhaul): Repeater and Intruder. Pure logic runs under node; DOM-shaped
// behaviour is pinned statically against the embedded assets.

func regionOf(t *testing.T, name string) string {
	t.Helper()
	index := readUIAsset(t, "index.html")
	start := strings.Index(index, "<!-- region:"+name+" -->")
	end := strings.Index(index, "<!-- /region:"+name+" -->")
	if start < 0 || end < start {
		t.Fatalf("region:%s markers missing", name)
	}
	return index[start:end]
}

func TestUIToolsPureLogicUnderNode(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not on PATH: repeater/intruder logic tests were NOT run; this is a coverage gap, not a pass")
	}
	cmd := exec.Command(node, "--test", "_js-tests/tools-wp9.test.mjs")
	cmd.Dir = "ui"
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("tools node tests failed: %v\n%s", err, out)
	}
}

func TestUIToolsModulesStayLoadableAndInlineStyleFree(t *testing.T) {
	for _, name := range []string{"js/repeater.js", "js/intruder.js"} {
		src := executableJS(readUIAsset(t, name))
		if regexp.MustCompile(`(?m)^import .* from './core\.js'`).MatchString(src) {
			t.Errorf("%s statically imports core.js; take helpers through deps so it loads under node", name)
		}
		if strings.Contains(src, `style="`) || strings.Contains(src, "style='") || strings.Contains(src, "cssText") || strings.Contains(src, ".style.") {
			t.Errorf("%s writes inline style; use classes and custom properties", name)
		}
		if regexp.MustCompile(`\.(innerHTML|outerHTML)\s*=|insertAdjacentHTML`).MatchString(src) {
			t.Errorf("%s assigns HTML; build nodes with textContent", name)
		}
		if regexp.MustCompile(`[\x{25B8}\x{27F2}\x{2197}\x{25A6}\x{FF0B}\x{25A7}\x{25BE}\x{2715}]`).MatchString(src) {
			t.Errorf("%s uses a Unicode glyph instead of a sprite icon", name)
		}
	}
}

func TestUIToolsReuseExistingEndpointsOnly(t *testing.T) {
	for _, name := range []string{"js/repeater.js", "js/intruder.js"} {
		src := executableJS(readUIAsset(t, name))
		for _, m := range regexp.MustCompile(`'(/api/[^'?]*)`).FindAllStringSubmatch(src, -1) {
			if m[1] != "/api/flows/" {
				t.Errorf("%s calls %s; only the existing raw flow endpoint may be read directly (evidence goes through attachEvidence)", name, m[1])
			}
		}
	}
	rep := executableJS(readUIAsset(t, "js/repeater.js"))
	requireUIContains(t, rep, "import('./evidence-attach.js')", "attachEvidence(", "import('./copyas.js')", "side=res")
	// The send API has no identity parameter: the control must not be faked.
	if regexp.MustCompile(`(?i)sendAs|send_as|identityOverride`).MatchString(rep) {
		t.Error("repeater.js references a send-as parameter the API does not have")
	}
	tools := readUIAsset(t, "js/tools.js") // raw: the comment stripper mis-handles /* inside tools.js strings
	if strings.Contains(tools, "sendAs") {
		t.Error("tools.js must not add an unsupported send-as field to the Repeater payload")
	}
}

func TestUIToolsRepeaterContracts(t *testing.T) {
	region := regionOf(t, "repeater")
	requireUIContains(t, region, `id="repTabs"`, `id="repMethod"`, `id="repUrl"`, `id="repSend"`, `id="repHistory"`, `id="repHistToggle"`, `id="repResView"`, `id="repStatus"`, `role="status"`, `id="repResSeg"`, `id="repReqSeg"`)
	rep := executableJS(readUIAsset(t, "js/repeater.js"))
	requireUIContains(t, rep,
		"'repAttach'", "'repProof'", "'repDiffBtn'", "'repCopyAs'", "repAttachedChip",
		"repPhoneSeg", "phoneViewAfterSend()", "createDiffView(", "createFinder(", "buildDiffPanel(",
		"aria-pressed", "Send the request first", "MAX_DIFF_BYTES",
	)
	tools := readUIAsset(t, "js/tools.js") // raw: the comment stripper mis-handles /* inside tools.js strings
	requireUIContains(t, tools,
		"import { wireRepeaterExtras } from './repeater.js'",
		"wireRepeaterExtras({",
		"repExtras.afterSend()",
		`data-evidence-flow="${f.id}"`,
		"createTabManager({", // multi-tab persistence untouched
	)
	// Compare modal stays reachable for the palette.
	requireUIContains(t, readUIAsset(t, "index.html"), `id="compareModal"`)
}

func TestUIToolsIntruderContracts(t *testing.T) {
	region := regionOf(t, "intruder")
	requireUIContains(t, region,
		`id="intrTemplate"`, `id="intrPayloadsWrap"`, `id="intrResults"`, `id="intrToFinding"`, `id="intrStart"`, `id="intrStop"`,
		`data-step-pane="positions"`, `data-step-pane="payloads"`, `data-step-pane="results"`, `id="intrPollStatus"`, `role="status"`,
	)
	// The payload list lives in its own pane so the stepper can show it alone.
	payload := region[strings.Index(region, `data-step-pane="payloads"`):strings.Index(region, `data-step-pane="results"`)]
	requireUIContains(t, payload, `id="intrPayloadsWrap"`, `id="intrPayHint"`)

	intr := executableJS(readUIAsset(t, "js/intruder.js"))
	requireUIContains(t, intr,
		"createElement('progress')", "aria-valuetext", "'intrSummary'", "role', 'status'", "aria-live",
		"'Status differs'", "'Longer'", "'Shorter'", "'Slower'", "'Flagged'", "'Matched'", "'Error'",
		"intruder.attach", "intruder.diff", "scope: 'intruder-results'", "contextmenu", "buildDiffPanel(",
		"LARGE_RUN = 1000", "role', 'alert'",
	)
	// Large runs confirm inline, never through a modal or native dialog.
	if regexp.MustCompile(`openModal|uiConfirm|confirm\(`).MatchString(intr) {
		t.Error("intruder.js must confirm large runs inline, not with a modal or native dialog")
	}
	tools := readUIAsset(t, "js/tools.js") // raw: the comment stripper mis-handles /* inside tools.js strings
	requireUIContains(t, tools,
		"wireIntruderExtras({", "intrExtras.onRender(", "chipsHTML(r,intrBaseline,esc)",
		"needsLargeRunConfirm(reqs)", "needsLargeRunConfirm(repeatValue)",
		"export async function intrStart()", "threads must be between 1 and 64", "repeat must be between 1 and 2000",
		"mark at least one § injection point", "too many requests",
		`data-evidence-flow="${fid}"`,
	)
}

// Single-key shortcuts are gated: the registry refuses duplicates per scope, honours
// typing targets and the Settings switch, and every key has a visible button.
func TestUIToolsKeyBindingsAreScopedAndHaveButtons(t *testing.T) {
	intr := executableJS(readUIAsset(t, "js/intruder.js"))
	scopes := regexp.MustCompile(`keys: '([a-z])', scope: '([a-z-]+)'`).FindAllStringSubmatch(intr, -1)
	seen := map[string]bool{}
	for _, m := range scopes {
		k := m[2] + ":" + m[1]
		if seen[k] {
			t.Errorf("duplicate binding %s in intruder.js", k)
		}
		seen[k] = true
		if m[2] == "global" {
			t.Errorf("single key %q must not be global", m[1])
		}
	}
	if len(scopes) != 2 {
		t.Fatalf("expected 2 intruder single-key bindings, got %d", len(scopes))
	}
	requireUIContains(t, intr, "createKeyRegistry(", "'intrAttach'", "'intrDiffBtn'", "registry.handle(")
}

func TestUIToolsStylesheetContracts(t *testing.T) {
	css := readUIAsset(t, "panel-tools.css")
	requireUIContains(t, css,
		`@media (max-width:899px)`, `@media (max-width:720px)`,
		`.rep-work[data-phone-view="req"]>.rep-res`, `.rep-work[data-phone-view="res"]>.rep-req`,
		`.intr-work[data-step="positions"]`, `.intr-chip`, `.rep-diff[hidden]`, `#repSend{min-height:48px}`,
		`position:sticky`, `var(--hit-min)`,
	)
	if regexp.MustCompile(`(?i)\btransition\b|@keyframes|animation`).MatchString(regexp.MustCompile(`(?s)/\*.*?\*/`).ReplaceAllString(css, "")) {
		t.Error("panel-tools.css must not animate; WP9 has no motion to cover with prefers-reduced-motion")
	}
	for _, m := range regexp.MustCompile(`min-width:\s*(\d+)px`).FindAllStringSubmatch(css, -1) {
		if n, _ := strconv.Atoi(m[1]); n > 375 {
			t.Errorf("panel-tools.css min-width %spx exceeds the 375px floor", m[1])
		}
	}
	if strings.Contains(css, "style=") {
		t.Error("panel-tools.css must not contain inline style markup")
	}
	// Every custom property used here is defined by the token layer.
	tokens := readUIAsset(t, "app.css") + readUIAsset(t, "workbench.css") + readUIAsset(t, "primitives.css") + readUIAsset(t, "shell.css")
	for _, m := range regexp.MustCompile(`var\((--[a-z0-9-]+)`).FindAllStringSubmatch(css, -1) {
		if m[1] == "--chip-c" { // rule-local property, defined on .intr-chip itself
			continue
		}
		if !strings.Contains(tokens, m[1]+":") {
			t.Errorf("panel-tools.css uses undefined token %s", m[1])
		}
	}
	// Chips never rely on colour alone: icon plus visible label.
	requireUIContains(t, executableJS(readUIAsset(t, "js/intruder.js")), `<svg class="icon"`, "esc(c.label)")
}
