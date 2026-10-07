package control

import (
	"os/exec"
	"strings"
	"testing"
)

func TestUIHistoryNoteFailureRetainsDraftAcrossSelection(t *testing.T) {
	src := readUIAsset(t, "js/proxy.js")
	if !strings.Contains(src, "function restoreFlowNoteDraft(") {
		t.Fatal("History note draft restoration missing")
	}
	start := strings.Index(src, "const noteSaveTails=new Map()")
	end := strings.Index(src[start:], "export function scheduleReload")
	saveStart := strings.Index(src, "export function saveNote()")
	saveEnd := strings.Index(src[saveStart:], "$('#noteInput').addEventListener")
	script := `const input={value:'draft'},status={dataset:{},style:{}},retry={hidden:true};
const $=id=>id==='#noteInput'?input:id==='#noteSaved'?status:retry;
const state={selId:1,detail:{note:'old'}},flowStore={byId:new Map()};let fail=true;
let api=async()=>{if(fail)throw new Error('Offline')};const toast=()=>{};const patchFlowRow=()=>{};
` + src[start:start+end] + strings.TrimPrefix(src[saveStart:saveStart+saveEnd], "export ") + `
(async()=>{
await saveNote();if(!noteDrafts.has(1)||retry.hidden||!status.textContent.includes('Offline'))throw new Error('failed note is not recoverable');
state.selId=2;restoreFlowNoteDraft(2,'other');if(input.value!=='other')throw new Error('draft leaks across flows');
state.selId=1;restoreFlowNoteDraft(1,'old');if(input.value!=='draft'||retry.hidden)throw new Error('failed draft lost on selection');
fail=false;await saveNote();if(noteDrafts.has(1)||!retry.hidden||status.textContent!=='Saved')throw new Error('retry not acknowledged');
let acknowledge;api=()=>new Promise(resolve=>{acknowledge=resolve});
input.value='A';const pending=saveNote();await Promise.resolve();await Promise.resolve();
noteEditorGenerations.set(1,noteEditorGeneration(1)+1);input.value='B';noteDrafts.set(1,{value:'B',error:'',saving:false});renderFlowNoteStatus(1);
acknowledge();await pending;
if(input.value!=='B'||noteDrafts.get(1)?.value!=='B'||status.textContent!=='Unsaved')throw new Error('old acknowledgement consumed newer draft');
let sent;api=async(_url,options)=>{sent=JSON.parse(options.body).note};await saveNote();
if(sent!=='B'||noteDrafts.has(1)||status.textContent!=='Saved')throw new Error('newer draft was not saved next');
})().catch(e=>{console.error(e);process.exitCode=1});`
	if out, err := exec.Command("node", "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("History note retry: %v\n%s", err, out)
	}
}

func TestUIRetentionFailureKeepsDataAndOffersRetry(t *testing.T) {
	src := readUIAsset(t, "js/settings.js")
	start := strings.Index(src, "export async function loadRetention(){")
	end := strings.Index(src[start:], "function runRetentionMutation")
	script := `let retentionMutationPromise=null,retentionLoadEpoch=0,retentionRefreshPending=false,retentionSelectionSnapshot,retentionStats={hosts:[{host:'example.com'}]};
const body={innerHTML:'existing rows'},status={style:{}},$=id=>id==='#retentionBody'?body:status;
const snapshotRetentionSelection=()=>new Set(),loadRetentionPolicy=()=>{},restoreRetentionSelection=()=>{};
let fail=true,retry;const api=async()=>{if(fail)throw new Error('Offline');return {hosts:[]}};
const renderRetention=()=>body.innerHTML='new rows';const esc=x=>x;
const renderLoadError=(el,label,error,fn)=>{el.textContent=error.message;retry=fn};
` + strings.TrimPrefix(src[start:start+end], "export ") + `
(async()=>{await loadRetention();if(body.innerHTML!=='existing rows'||!retry||status.textContent!=='Offline')throw new Error('refresh discarded data or lacks retry');
fail=false;await retry();if(body.innerHTML!=='new rows'||status.textContent)throw new Error('retry failed to replace data and clear error');})().catch(e=>{console.error(e);process.exitCode=1});`
	if out, err := exec.Command("node", "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("retention recovery: %v\n%s", err, out)
	}
}

func TestUIMobileNavigationUsesOneVisibleControl(t *testing.T) {
	index := readUIAsset(t, "index.html")
	if strings.Index(index, `id="workspaceHydrationStatus"`) > strings.Index(index, `id="tabs"`) {
		t.Error("mobile startup recovery is inside the hidden navigation rail")
	}
	css := readUIAsset(t, "app.css")
	if !strings.Contains(css, "#tabs{display:none}") {
		t.Error("mobile tool picker duplicates main tab strip")
	}
	if !strings.Contains(css, ".settings-wrap:not(.split) .settings-nav-group{display:contents}") {
		t.Error("compact Settings nav must lay the section buttons out as chips")
	}
	settings := readUIAsset(t, "js/settings.js")
	if !strings.Contains(settings, `<div class="field" style="width:100px;margin-bottom:0">`) {
		t.Error("listener port lacks shared field styling")
	}
}

func TestUIHistoryMobileActionsRemainScrollable(t *testing.T) {
	css := readUIAsset(t, "app.css")
	for _, rule := range []string{"#panel-proxy.active{overflow-y:auto}", "#panel-proxy.active #rows{flex:1 0 100px}", ".note-bar{flex:none;"} {
		if !strings.Contains(css, rule) {
			t.Errorf("History mobile recovery layout missing %q", rule)
		}
	}
}

func TestUIFindingsHeaderDoesNotObscureMobileContent(t *testing.T) {
	css := readUIAsset(t, "app.css")
	if !strings.Contains(css, ".find-header-sticky{position:sticky;top:0;z-index:2;background:var(--bg);") {
		t.Error("sticky desktop header must use an opaque surface")
	}
	if !strings.Contains(css, ".find-header-sticky{position:static}") {
		t.Error("mobile Findings header must scroll with its content")
	}
}

func TestUIConfirmMissingSurfaceCancelsWithoutNativeDialog(t *testing.T) {
	src := readUIAsset(t, "js/core.js")
	start := strings.Index(src, "export function uiConfirm(")
	end := strings.Index(src[start:], "// Minimal, safe markdown")
	fn := strings.TrimPrefix(src[start:start+end], "export ")
	script := `let activeConfirmFinish=null; const $=()=>null; const toast=()=>{};
const window={confirm(){throw new Error('native confirmation opened')}};
` + fn + `
uiConfirm('Delete','Delete data?').then(ok=>{if(ok!==false)throw new Error('missing dialog must cancel')}).catch(e=>{console.error(e);process.exitCode=1});`
	if out, err := exec.Command("node", "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("confirmation fallback: %v\n%s", err, out)
	}
}

func TestUIUpstreamHelpFollowsConnectionChoice(t *testing.T) {
	src := readUIAsset(t, "js/settings.js")
	start := strings.Index(src, "function renderUpstreamProxyFields(")
	end := strings.Index(src[start:], "function decodeURLCredential")
	script := `const fields=new Map(); const $=id=>{if(!fields.has(id))fields.set(id,{value:'',hidden:false,textContent:''});return fields.get(id)};
const upstreamDefaultPorts={http:'8080',https:'443',socks5:'1080',socks5h:'1080'};
` + src[start:start+end] + `
renderUpstreamProxyFields('direct');
if(!$('#upstreamSchemeHelp').hidden)throw new Error('Direct must not show DNS help');
renderUpstreamProxyFields('socks5h');
if($('#upstreamSchemeHelp').hidden||!$('#upstreamSchemeHelp').textContent.includes('proxy'))throw new Error('proxy DNS help missing');
renderUpstreamProxyFields('https');
if(!$('#upstreamSchemeHelp').hidden)throw new Error('HTTPS must not show SOCKS help');`
	if out, err := exec.Command("node", "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("conditional connection help: %v\n%s", err, out)
	}
}

func TestUICustomControlAppearanceIsAvailableBeforeBoot(t *testing.T) {
	css := readUIAsset(t, "app.css")
	for _, rule := range []string{"select{display:none!important}", "input[type=checkbox],input[type=radio]{appearance:none", "input[type=number]::-webkit-inner-spin-button", "summary::-webkit-details-marker"} {
		if !strings.Contains(css, rule) {
			t.Errorf("custom control appearance missing %q", rule)
		}
	}
}

func TestUINotesSaveFailureRemainsActionable(t *testing.T) {
	src := readUIAsset(t, "js/notes.js")
	start := strings.Index(src, "let notesSaveError=")
	end := strings.Index(src[start:], "const notesAutosave=")
	script := `const status={dataset:{},style:{}},retry={hidden:true};
const $=id=>id==='#notesStatus'?status:retry;
` + src[start:start+end] + `
notesSaveError='Offline';setNotesStatus('dirty');
if(status.textContent!=='Save failed: Offline'||retry.hidden)throw new Error('save failure is not actionable');
setNotesStatus('saving');
if(!retry.hidden||status.textContent!=='Saving…')throw new Error('retry state did not clear');
setNotesStatus('saved');
if(status.textContent!=='Saved'||status.dataset.tooltip)throw new Error('saved state retains stale failure');`
	if out, err := exec.Command("node", "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("notes status: %v\n%s", err, out)
	}
}

func TestUITooltipDoesNotCoverExpandedControls(t *testing.T) {
	src := strings.Replace(readUIAsset(t, "js/hints.js"), "export function", "function", 1)
	script := `const events={};let observer;
const attrs={'aria-describedby':'existing-help'};
const owner={dataset:{tooltip:'Report format'},isConnected:true,
 getAttribute:k=>attrs[k]||null,setAttribute:(k,v)=>attrs[k]=v,removeAttribute:k=>delete attrs[k],
 hasAttribute:k=>k in attrs,closest:()=>owner,contains:()=>false,matches:()=>false,
 getBoundingClientRect:()=>({left:10,top:10,bottom:30})};
const tip={hidden:true,style:{},setAttribute(){},addEventListener(){},contains:()=>false,
 getBoundingClientRect:()=>({width:100,height:30})};
global.document={createElement:()=>tip,body:{appendChild(){}},documentElement:{},querySelectorAll:()=>[],addEventListener:(k,v)=>events[k]=v};
global.window={addEventListener(){}};global.innerWidth=1000;global.innerHeight=800;
global.MutationObserver=class{constructor(fn){observer=fn}observe(){}};
` + src + `
initUI=initUiHints;initUI();
events.focusin({target:owner});if(tip.hidden)throw new Error('focus hint missing');
attrs['aria-expanded']='true';observer([{type:'attributes',target:owner,attributeName:'aria-expanded'}]);
if(!tip.hidden)throw new Error('hint covers open dropdown');
events.focusin({target:owner});if(!tip.hidden)throw new Error('hint reopens over dropdown');
if(attrs['aria-describedby']!=='existing-help')throw new Error('existing description lost');`
	if out, err := exec.Command("node", "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("tooltip dropdown interaction: %v\n%s", err, out)
	}
}

func TestUIFindingRefreshPreservesCustomSelection(t *testing.T) {
	src := readUIAsset(t, "js/findings.js")
	start := strings.Index(src, "function findingDetailEditPending()")
	end := strings.Index(src[start:], "function refreshDeferredFindingDetail()")
	script := `let bodyEditing=false,findingDetailPointerActive=false,bodySaveTimers=new Set(),selFinding=1,bodySavesInFlight=0,findingWritesInFlight=0,findEditMode=true,open=false;
const findingDrafts=new Set();
const detail={contains:el=>el?.inside,querySelector:()=>open?{}:null};const $=()=>detail;
const document={activeElement:null};
function focus(kind,inside=true){document.activeElement={inside,matches:selector=>selector.includes(kind)}}
` + src[start:start+end] + `
focus('[role="combobox"]');if(!findingDetailEditPending())throw new Error('focused custom dropdown may be replaced');
open=true;focus('[role="option"]',false);if(!findingDetailEditPending())throw new Error('portaled dropdown may be replaced');
open=false;focus('textarea');if(!findingDetailEditPending())throw new Error('text editor protection regressed');
focus('button');if(findingDetailEditPending())throw new Error('unrelated button blocks refresh');
findingDrafts.add(1);if(!findingDetailEditPending())throw new Error('unsaved draft may be replaced by server refresh');`
	if out, err := exec.Command("node", "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("finding refresh ownership: %v\n%s", err, out)
	}
}

func TestUITooltipPreservesHoverWithoutBlockingControls(t *testing.T) {
	css := readUIAsset(t, "app.css")
	start := strings.Index(css, ".ui-tooltip{")
	rule := css[start : start+strings.Index(css[start:], "}")]
	if !strings.Contains(rule, "pointer-events:none") {
		t.Error("optional tooltip must not intercept clicks on underlying controls")
	}
	src := strings.Replace(readUIAsset(t, "js/hints.js"), "export function", "function", 1)
	script := `const events={};let pending=null,hovered=true;
const attrs={};const owner={dataset:{tooltip:'Map help'},isConnected:true,
 getAttribute:k=>attrs[k]||null,setAttribute:(k,v)=>attrs[k]=v,removeAttribute:k=>delete attrs[k],
 hasAttribute:k=>k in attrs,closest:()=>owner,contains:()=>false,matches:()=>hovered,
 getBoundingClientRect:()=>({left:10,top:10,bottom:30})};
const tip={hidden:true,style:{},setAttribute(){},addEventListener(){},contains:()=>false,matches:()=>false,
 getBoundingClientRect:()=>({left:10,right:110,top:36,bottom:66,width:100,height:30})};
global.document={createElement:()=>tip,body:{appendChild(){}},documentElement:{},querySelectorAll:()=>[],addEventListener:(k,v)=>events[k]=v};
global.window={addEventListener(){}};global.innerWidth=1000;global.innerHeight=800;
global.MutationObserver=class{constructor(){}observe(){}};
global.setTimeout=fn=>{pending=fn;return 1};global.clearTimeout=()=>pending=null;
function flush(){const fn=pending;pending=null;fn?.()}
` + src + `
initUiHints();events.pointerover({pointerType:'mouse',target:owner,clientX:15,clientY:20});
if(tip.hidden)throw new Error('owner hover hint missing');
hovered=false;
if(!events.pointermove)throw new Error('click-through tooltip lacks coordinate hover handling');
events.pointermove({pointerType:'mouse',clientX:50,clientY:45});events.pointerout();flush();
if(tip.hidden)throw new Error('hint disappeared while pointer was over its text');
events.pointermove({pointerType:'mouse',clientX:300,clientY:300});flush();
if(!tip.hidden)throw new Error('hint remained after leaving owner and tooltip');`
	if out, err := exec.Command("node", "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("tooltip hover persistence: %v\n%s", err, out)
	}
}
