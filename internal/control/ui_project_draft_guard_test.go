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
	end := strings.Index(src[start:], "// ---- end project switching ----")
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
['setSearch','pmFilter','pmNew','pmNewFolder','pmNewPath','pmMoveFolder'].forEach(dirty);
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
	src := readUIAsset(t, "js/project-picker.js")
	start := strings.Index(src, "const projectSwitchDisabledControls =")
	if start < 0 {
		t.Fatal("project switch lock missing from project-picker.js")
	}
	end := strings.Index(src[start:], "/* ---- wiring ---- */")
	script := `let projectModalLoadEpoch=3,options;
const fields=[{disabled:false},{disabled:true}];
const attrs=new Set();
const note={dataset:{}},modal={style:{display:'flex'},querySelectorAll:sel=>sel.includes('aria-busy')?[]:fields,
  setAttribute:k=>attrs.add(k),removeAttribute:k=>attrs.delete(k)};
const $=id=>id==='#projModal'?modal:note;
const openModal=(el,opts)=>{options=opts};
const restoreCreateButton=()=>{};
` + strings.ReplaceAll(src[start:start+end], "export function", "function") + `
setProjectSwitchModalBusy(true);
if(fields.some(f=>!f.disabled)||projectModalLoadEpoch!==4)throw Error('pending switch still permits editing or a stale list load');
if(!attrs.has('aria-busy'))throw Error('pending switch is not announced as busy');
if(!options.onEscape||!options.onDismiss)throw Error('pending switch can be dismissed');
options.onEscape();options.onDismiss();
setProjectSwitchModalBusy(false);
if(fields[0].disabled||!fields[1].disabled)throw Error('failure did not restore original control states');
if(options.onEscape||options.onDismiss)throw Error('recovered project dialog cannot be closed');
if(attrs.has('aria-busy'))throw Error('recovered project dialog still reports busy');`
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
	src := readUIAsset(t, "js/cmdk.js")
	start := strings.Index(src, "export function cmdkOpen()")
	end := strings.Index(src[start:], "export function cmdkClose()")
	script := `let blocked=true,opened=0;
const cmdkEnv={blocked:()=>blocked,ready:()=>true},toast=()=>{},cmdkRender=()=>{},cmdkClose=()=>{};
const cmdk={el:{},input:{value:'preserve'},open:false};const openModal=()=>opened++;
` + strings.TrimPrefix(src[start:start+end], "export ") + `
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

// TestUIProjectPickerMarkupContract pins the picker's structure: the filter is
// the first control, other projects form one tree (one Tab stop), the current
// project is pinned once, creation and folder moves are separate labelled
// steps, and Settings and phones reach the same dialog instead of a select.
func TestUIProjectPickerMarkupContract(t *testing.T) {
	index := requireUIContracts(t, "index.html",
		`<link rel="stylesheet" href="/css/projects.css">`,
		`id="pmFilter"`, `aria-controls="pmList"`,
		`id="pmList" class="pm-list" role="tree"`,
		`id="pmCurrent" aria-current="true"`,
		`id="pmCopyDir" aria-label="Copy current project folder path"`,
		`id="pmSummary" role="status" aria-live="polite"`,
		`id="pmNewToggle" aria-expanded="false" aria-controls="pmCreate"`,
		`<form id="pmCreate" class="pm-create" novalidate hidden`,
		`<label for="pmNew">Project name</label>`,
		`<summary>Folder / Location</summary>`,
		`<label for="pmNewFolder">Folder`, `<label for="pmNewPath">Save location`,
		`<form id="pmMove" class="pm-move" novalidate hidden`,
		`id="pmMoveFolder" class="btn btn-field" list="pmFolderList"`,
		`<datalist id="pmFolderList">`,
		`id="projPickerBtn" aria-haspopup="dialog"`,
		`<button type="button" class="nav-foot-proj u-hidden" id="projBadge"`,
		`id="mobileProjectChip" class="pm-mobile-chip" aria-haspopup="dialog"`,
	)
	dialog := index[strings.Index(index, `<div id="projModal"`):strings.Index(index, `<div id="sessionInspectModal"`)]
	if strings.Index(dialog, `id="pmFilter"`) > strings.Index(dialog, `id="pmNew"`) {
		t.Error("the filter must precede the create form in the projects dialog")
	}
	for _, gone := range []string{`id="projSelect"`, `id="projSwitchBtn"`, `id="projNewBtn"`, `id="projNewPath"`, `id="pmNote"`, `role="button" tabindex="0" title="Active project`} {
		if strings.Contains(index, gone) {
			t.Errorf("index.html still carries the replaced project control %s", gone)
		}
	}
	bar := index[strings.Index(index, `<header id="bar"`):strings.Index(index, `</header>`)]
	if !strings.Contains(bar, `id="mobileProjectChip"`) {
		t.Error("the phone project chip must live in the top bar, not the hidden tool nav")
	}
	css := requireUIContracts(t, "css/projects.css", ".pm-mobile-chip{display:none", "@media (max-width:720px)", ".pm-mobile-chip{display:inline-flex}")
	if strings.Contains(css, "gradient") {
		t.Error("projects.css must use flat tokens, not gradients")
	}
	requireUIContracts(t, "js/project-picker.js",
		"import {", "from './project-picker-model.js'",
		"buildPickerModel(cache.projects",
		"opener.focus({ preventScroll: true })",
		"e.key === 'F10' && e.shiftKey",
		"writeOpenState(node.dataset.section, next)",
		"validateNewProject(createValues()",
	)
	requireUIContracts(t, "js/settings.js",
		"configureProjectPicker({",
		"switchProject:doSwitchProject",
		"export function openProjectModal(from){return openProjectPicker(from);}",
	)
}
