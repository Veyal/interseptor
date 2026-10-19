package control

import (
	"os/exec"
	"strings"
	"testing"
)

// A live flow that arrives out of id order (concurrent clients) must land at its
// id position under the live "# ↓" sort, not at the top in arrival order.
func TestUIHistoryLiveInsertKeepsIdOrder(t *testing.T) {
	core := readUIAsset(t, "js/core.js")
	script := repeaterRenderJS(t, core, "export function createFlowStore(order)") +
		repeaterRenderJS(t, core, "export function upsertFlow(store,f)") + `
const s=createFlowStore([{id:15}]);
for(const id of [17,16,18,14])upsertFlow(s,{id});
const got=s.order.map(f=>f.id).join(',');
if(got!=='18,17,16,15,14')throw Error('live order '+got);
const r=upsertFlow(s,{id:16,status:200});
if(r.isNew||s.order.length!==5||s.byId.get(16).status!==200)throw Error('merge changed shape');
`
	if out, err := exec.Command("node", "--input-type=module", "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("live insert order: %v\n%s", err, out)
	}
}

// A dropped held request (flag 4) never gets a status; History must show it as
// dropped, not as an in-flight "pending" row — including rows stored before the
// server started recording an error for drops.
func TestUIHistoryDroppedFlowIsNotPending(t *testing.T) {
	core := readUIAsset(t, "js/core.js")
	script := "const FLAG_DROPPED=4;\n" + repeaterRenderJS(t, core, "export function flowPending(f)") + `
if(!flowPending({status:0,error:'',flags:0}))throw Error('in-flight flow is not pending');
if(flowPending({status:0,error:'',flags:5}))throw Error('legacy dropped flow (flags 5, no error) shows as pending');
if(flowPending({status:0,error:'request dropped by interseptor',flags:5}))throw Error('dropped flow shows as pending');
if(flowPending({status:200,flags:0}))throw Error('completed flow shows as pending');
`
	if out, err := exec.Command("node", "--input-type=module", "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("dropped flow pending state: %v\n%s", err, out)
	}
	proxy := readUIAsset(t, "js/proxy.js")
	for _, want := range []string{"const pending=flowPending(f);", "f.flags&FLAG_DROPPED", "}else if(flowPending(d)){"} {
		if !strings.Contains(proxy, want) {
			t.Errorf("proxy.js missing %q", want)
		}
	}
}

// Context-menu shortcut hints name this platform's modifier (⌘K on macOS,
// Ctrl+K elsewhere), and "New Repeater tab" actually opens Repeater.
func TestUIWorkspaceMenuShortcutsArePlatformCorrect(t *testing.T) {
	kit := readUIAsset(t, "js/desktop-panel-kit.js")
	script := "let mac=true;const isMac=()=>mac;\n" + repeaterRenderJS(t, kit, "export function modShortcut(key)") + `
if(modShortcut('K')!=='⌘K')throw Error('mac label '+modShortcut('K'));
mac=false;
if(modShortcut('K')!=='Ctrl+K')throw Error('other label '+modShortcut('K'));
`
	if out, err := exec.Command("node", "--input-type=module", "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("modShortcut: %v\n%s", err, out)
	}
	proxy := readUIAsset(t, "js/proxy.js")
	for _, bad := range []string{"Ctrl/Cmd K", "Ctrl/Cmd T", "Ctrl/Cmd J", `data-tab="tools"`} {
		if strings.Contains(proxy, bad) {
			t.Errorf("proxy.js still contains %q", bad)
		}
	}
	tools := readUIAsset(t, "js/tools.js")
	if !strings.Contains(tools, `export function openNewRepeaterTab(){document.querySelector('.tab[data-tab="repeater"]')?.click();`) {
		t.Error("openNewRepeaterTab must switch to the Repeater panel")
	}
	app := readUIAsset(t, "js/app.js")
	if !strings.Contains(app, "if(isModShortcut(e,'t')&&hasNativeHost()){e.preventDefault();openNewRepeaterTab();return;}") {
		t.Error("Cmd+T must open a new Repeater tab in the native app")
	}
}

// High contrast: the selected segment of a segmented control needs a fill that
// differs from the black track (the desktop skin's --surface-2 is #0a0a0a).
func TestUIDesktopHighContrastSegmentSelectionIsVisible(t *testing.T) {
	css := readUIAsset(t, "css/desktop/components.css")
	want := `html.desktop-app[data-theme="hc"] :is(.seg button.on,.seg button[aria-pressed="true"],.seg button[aria-selected="true"],.session-role[aria-pressed="true"],.intr-src-seg .btn.on,.android-proxy-seg button.on){color:var(--onAccent);background:var(--accentSolid);box-shadow:none}`
	if !strings.Contains(css, want) {
		t.Fatalf("components.css missing the high-contrast selected segment rule %q", want)
	}
}
