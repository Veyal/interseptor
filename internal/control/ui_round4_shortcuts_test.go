package control

import (
	"strings"
	"testing"
)

// Round 4: modifier chords must never reach the plain-letter handlers, Cmd/Ctrl+I
// is the Proxy menu's "Intercept Requests" (not send-to-Intruder), and a plain
// j/k keeps the rows marked with x.
func TestUIShortcutModifierContracts(t *testing.T) {
	app := executableJS(readUIAsset(t, "js/app.js"))
	requireUIContains(t, app, "if((e.key==='j'||e.key==='k')&&(e.ctrlKey||e.metaKey||e.altKey))return;")
	if strings.Contains(app, "isModShortcut(e,'i')") {
		t.Error("Cmd/Ctrl+I belongs to the native Intercept Requests menu item; History must not claim it")
	}
	requireUIContains(t, app, "flowSendShortcutAllowed()&&isPlainShortcut(e,'i')")
	table := readUIAsset(t, "js/shortcuts-table.js")
	if strings.Contains(table, "'Ctrl+I'") {
		t.Error("the shortcut sheet must not list Ctrl+I for Intruder")
	}
	proxy := executableJS(readUIAsset(t, "js/proxy.js"))
	requireUIContains(t, proxy, "const keepMarks=(e.key==='j'||e.key==='k')&&!e.ctrlKey&&!e.metaKey&&!e.shiftKey&&state.selected.size>0;")
	requireUIContains(t, executableJS(readUIAsset(t, "js/repeater.js")), "if (e.ctrlKey || e.metaKey || e.altKey) return;")
}

func TestUIVersionBadgeRetriesOnReconnect(t *testing.T) {
	app := executableJS(readUIAsset(t, "js/app.js"))
	requireUIContains(t, app, "if($('#verBadge')?.dataset.failed==='true')loadVersion(false);")
}

func TestUIClientAbortRendersAsAbortedNot502(t *testing.T) {
	proxy := executableJS(readUIAsset(t, "js/proxy.js"))
	requireUIContains(t, proxy, "const CLIENT_ABORT_ERROR='client disconnected';", ">ABRT</span>", "No response — aborted:")
}

func TestUIChecklistDoItPerformsItsStep(t *testing.T) {
	cl := executableJS(readUIAsset(t, "js/checklist.js"))
	requireUIContains(t, cl, "openSetup({ step: 1 })", "openSetup({ step: 0 })")
	css := readUIAsset(t, "settings.css")
	requireUIContains(t, css, ".checklist-progress::-webkit-progress-bar", ".checklist-progress::-webkit-progress-value")
}
