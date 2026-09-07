package control

import (
	"os/exec"
	"strings"
	"testing"
)

func TestUIProjectSwitchRejectsUnsavedDraftsBeforePosting(t *testing.T) {
	src := readUIAsset(t, "js/settings.js")
	start := strings.Index(src, "export async function doSwitchProject(")
	if start < 0 {
		t.Fatal("project switch function missing")
	}
	end := strings.Index(src[start:], "$('#projSwitchBtn').onclick")
	script := `let blocker='Save or retry Notes before switching projects.',posted=0,feedback='';
let projectSwitchPending=false,projectSwitchEpoch=0,projectSwitchTimer=null;
const projectSwitchBlocker=()=>blocker,toast=()=>{},projectPathKey=x=>x;
const setProjectPathInvalid=()=>{},setProjectSwitchFeedback=x=>feedback=x;
const setProjectControlsDisabled=()=>{},setProjectSwitchModalBusy=()=>{},loadProject=()=>{};
const api=async()=>{posted++;return {switching:'next'}};const setTimeout=()=>1;
` + strings.TrimPrefix(src[start:start+end], "export ") + `
(async()=>{
await doSwitchProject('next','');
if(posted||projectSwitchPending||!feedback.includes('Notes'))throw new Error('project switch accepted an unsaved Notes draft');
blocker='';await doSwitchProject('next','');
if(posted!==1||!projectSwitchPending)throw new Error('clean project switch did not proceed');
})().catch(e=>{console.error(e);process.exitCode=1});`
	if out, err := exec.Command("node", "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("project draft guard: %v\n%s", err, out)
	}
}

func TestUIProjectSwitchIgnoresNavigationButGuardsSettingsDrafts(t *testing.T) {
	src := readUIAsset(t, "js/settings.js")
	start := strings.Index(src, "const SESSION_DIRTY_FIELDS=")
	end := strings.Index(src[start:], "function sessionFormPayload(")
	script := `const upstreamProxyFieldIds=['setUpstreamScheme','setUpstreamHost','setUpstreamPort','setUpstreamUser','setUpstreamPassword','setUpstreamCA'];
const controls=new Map();const $=id=>controls.get(id);
const dirty=id=>controls.set('#'+id,{dataset:{settingsDirty:'1'}});
` + src[start:start+end] + `
['setSearch','settingsSectionSelect','projSelect','projNew','projNewPath'].forEach(dirty);
if(hasUnsavedSettingsFields())throw Error('navigation or project form blocked a clean switch');
for(const id of ['setSessionHeaders','hostHdrList','setUpstreamHost','proxyListenersList','retMaxFlows']){
 dirty(id);if(!hasUnsavedSettingsFields())throw Error(id+' draft would be discarded');controls.delete('#'+id);
}
if(hasUnsavedSettingsFields())throw Error('saved settings still block switching');`
	if out, err := exec.Command("node", "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("settings draft guard: %v\n%s", err, out)
	}
}

func TestUIProjectSwitchKeepsModalLockedUntilFailureRecovery(t *testing.T) {
	src := readUIAsset(t, "js/settings.js")
	start := strings.Index(src, "const projectSwitchDisabledControls=")
	end := strings.Index(src[start:], "export async function doSwitchProject(")
	script := `let projectModalLoadEpoch=3,options;
const fields=[{disabled:false},{disabled:true}];
const note={},modal={style:{display:'flex'},querySelectorAll:()=>fields};
const $=id=>id==='#projModal'?modal:note;
const openModal=(el,opts)=>{options=opts};
` + src[start:start+end] + `
setProjectSwitchModalBusy(true);
if(fields.some(f=>!f.disabled)||projectModalLoadEpoch!==4)throw Error('pending switch still permits editing or a stale list load');
if(!options.onEscape||!options.onDismiss)throw Error('pending switch can be dismissed');
options.onEscape();options.onDismiss();
setProjectSwitchModalBusy(false);
if(fields[0].disabled||!fields[1].disabled)throw Error('failure did not restore original control states');
if(options.onEscape||options.onDismiss)throw Error('recovered project dialog cannot be closed');`
	if out, err := exec.Command("node", "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("project switch modal recovery: %v\n%s", err, out)
	}
}

func TestUIProjectSwitchGuardRegistryFailsClosed(t *testing.T) {
	src := readUIAsset(t, "js/core.js")
	start := strings.Index(src, "const projectSwitchGuards=")
	if start < 0 {
		t.Fatal("project-switch guard registry missing")
	}
	end := strings.Index(src[start:], "export const state=")
	script := strings.ReplaceAll(src[start:start+end], "export function", "function") + `
let dirty=true;registerProjectSwitchGuard(()=>dirty?'Notes':'');
if(projectSwitchBlocker()!=='Notes')throw new Error('dirty module was ignored');
dirty=false;if(projectSwitchBlocker())throw new Error('clean modules blocked navigation');
registerProjectSwitchGuard(()=>{throw new Error('unavailable')});
if(!projectSwitchBlocker())throw new Error('failed guard allowed destructive reload');`
	if out, err := exec.Command("node", "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("project guard registry: %v\n%s", err, out)
	}
}

func TestUICommandPaletteCannotBypassActiveModal(t *testing.T) {
	src := readUIAsset(t, "js/app.js")
	start := strings.Index(src, "function cmdkOpen()")
	end := strings.Index(src[start:], "function cmdkClose()")
	script := `let blocked=true,opened=0;const projectScopedUIReady=true;
const workflowShortcutBlocked=()=>blocked,toast=()=>{},cmdkRender=()=>{},cmdkClose=()=>{};
const cmdk={el:{},input:{value:'preserve'},open:false};const openModal=()=>opened++;
` + src[start:start+end] + `
cmdkOpen();
if(opened||cmdk.open||cmdk.input.value!=='preserve')throw Error('command palette bypassed active project dialog');
blocked=false;cmdkOpen();
if(opened!==1||!cmdk.open)throw Error('normal command palette entry is broken');`
	if out, err := exec.Command("node", "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("command palette modal guard: %v\n%s", err, out)
	}
}

func TestUIModalShellRemainsFocusableWhenControlsBecomeDisabled(t *testing.T) {
	src := readUIAsset(t, "js/core.js")
	start := strings.Index(src, "function registerModal(")
	end := strings.Index(src[start:], "export function openModal(")
	script := `const modalRegistry=new Map();
const makeDialog=initial=>({tabIndex:initial,hasAttribute:()=>initial!==undefined,focus(){if(this.tabIndex===undefined)throw Error('disabled dialog has no focus destination')}});
const wrap=dialog=>({matches:()=>false,querySelector:()=>dialog,addEventListener:()=>{}});
` + src[start:start+end] + `
const dialog=makeDialog(undefined);const entry=registerModal(wrap(dialog));entry.dialog.focus();
if(dialog.tabIndex!==-1)throw Error('modal shell was added to normal Tab order');
const authored=makeDialog(0);registerModal(wrap(authored));
if(authored.tabIndex!==0)throw Error('authored focus behavior was overwritten');`
	if out, err := exec.Command("node", "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("disabled dialog focus fallback: %v\n%s", err, out)
	}
}
