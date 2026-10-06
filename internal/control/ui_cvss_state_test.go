package control

import (
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

func TestCVSSApplyDoesNotOverwriteNewerPreviewState(t *testing.T) {
	source := regexp.MustCompile(`(?m)^import .*;\n`).ReplaceAllString(readUIAsset(t, "js/cvss.js"), "")
	script := `const esc=x=>x,escAttr=x=>x;let evaluations=0;
 const api=async()=>{evaluations++;return {canonicalVector:'CVSS:4.0/example',score:8.7,rating:'HIGH',nomenclature:'CVSS-B'};};
 ` + strings.ReplaceAll(source, "export ", "") + `
 const control=value=>({value,disabled:false,textContent:'',handlers:{},classList:{toggle(){}},addEventListener(k,fn){this.handlers[k]=fn;}});
 const input=control('CVSS:4.0/example'),status=control(''),preview=control(''),apply=control('');
 const root={isConnected:true,querySelector:s=>({'#findCvss':input,'[data-cvss-status]':status,'[data-cvss-preview]':preview,'[data-cvss-apply]':apply}[s]),querySelectorAll:()=>[]};
 let complete;const saved=[];bindCvssEditor(root, fields=>{saved.push(fields);return new Promise(resolve=>complete=resolve);});
 preview.handlers.click();await new Promise(r=>setTimeout(r,0));
 if(apply.disabled)throw Error('valid preview did not enable Apply');
 const pending=apply.handlers.click();input.value='new incomplete draft';input.handlers.input();complete();await pending;
 if(status.textContent.includes('Applied')||status.textContent.includes('Could not apply')||!apply.disabled)throw Error('stale Apply owned newer input: '+status.textContent);
 if(saved.length!==1||saved[0].severity!=='High')throw Error('Apply did not preserve selected evaluation');
 root.isConnected=false;await new Promise(r=>setTimeout(r,220));
 if(!apply.disabled)throw Error('detached preview re-enabled Apply');
 `
	if out, err := exec.Command("node", "--input-type=module", "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("CVSS async state: %v\n%s", err, out)
	}
}

func TestCVSSPreviewDraftSurvivesRemountAndRetry(t *testing.T) {
	source := regexp.MustCompile(`(?m)^import .*;\n`).ReplaceAllString(readUIAsset(t, "js/cvss.js"), "")
	workspace := readUIAsset(t, "js/finding-workspace.js")
	workspace = workspace[strings.Index(workspace, "export function createFindingDraftStore"):]
	script := `const esc=x=>x,escAttr=x=>x;const api=async()=>({canonicalVector:'A',score:8.7,rating:'HIGH',nomenclature:'CVSS-B'});` + strings.ReplaceAll(workspace, "export ", "") + strings.ReplaceAll(source, "export ", "") + `
 const drafts=createFindingDraftStore();
 const control=value=>({value,disabled:false,textContent:'',handlers:{},classList:{toggle(){}},addEventListener(k,fn){this.handlers[k]=fn;}});
 const mount=id=>{const input=control(drafts.values(id).vector||'saved'),status=control(''),preview=control(''),apply=control('');const root={isConnected:true,querySelector:s=>({'#findCvss':input,'[data-cvss-status]':status,'[data-cvss-preview]':preview,'[data-cvss-apply]':apply}[s]),querySelectorAll:()=>[]};return {input,status,preview,apply,root};};
 let finish,reject;
 const bind=(ui,id)=>bindCvssEditor(ui.root,async()=>{const tokens=drafts.tokens(id);await new Promise((a,b)=>{finish=a;reject=b});drafts.acknowledge(id,tokens);},value=>drafts.stage(id,{vector:value}));
 let ui=mount(1);bind(ui,1);ui.input.value='A';ui.input.handlers.input();ui.preview.handlers.click();await new Promise(r=>setTimeout(r,0));const pending=ui.apply.handlers.click();ui.input.value='B';ui.input.handlers.input();finish();await pending;ui.root.isConnected=false;
 ui=mount(1);bind(ui,1);if(ui.input.value!=='B'||mount(2).input.value!=='saved')throw Error('preview lost ownership across remount/finding switch');
 ui.preview.handlers.click();await new Promise(r=>setTimeout(r,0));const failed=ui.apply.handlers.click();reject(Error('save failed'));await failed;
 if(!drafts.has(1)||ui.apply.disabled)throw Error('failure discarded draft or disabled retry');
 const retry=ui.apply.handlers.click();finish();await retry;if(drafts.has(1))throw Error('successful retry retained applied draft');
 ui.root.isConnected=false;await new Promise(r=>setTimeout(r,220));
 `
	if out, err := exec.Command("node", "--input-type=module", "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("CVSS remount: %v\n%s", err, out)
	}
}

func TestCVSSDiscardAndExactRevertRecoverFindingDrafts(t *testing.T) {
	source := regexp.MustCompile(`(?m)^import .*;\n`).ReplaceAllString(readUIAsset(t, "js/cvss.js"), "")
	workspace := readUIAsset(t, "js/finding-workspace.js")
	workspace = workspace[strings.Index(workspace, "export function createFindingDraftStore"):]
	findingsSource := readUIAsset(t, "js/findings.js")
	section := func(start, end string) string {
		t.Helper()
		a := strings.Index(findingsSource, start)
		if a < 0 {
			t.Fatalf("missing %s", start)
		}
		b := strings.Index(findingsSource[a:], end)
		if b < 0 {
			t.Fatalf("missing %s", end)
		}
		return findingsSource[a : a+b]
	}
	if !strings.Contains(findingsSource, "fields => applyCvssFinding(f.id, fields), vector => stageCvssPreview(f.id, vector), () => discardCvssPreview(f.id)") {
		t.Fatal("CVSS recovery not wired to shared writes")
	}
	script := `
 const esc=x=>x,escAttr=x=>x;
 const requests=[];
 const api=async(path,opts)=>{
  const fields=JSON.parse(opts.body);
  if(path==='/api/finding-cvss'){if(!fields.vector||fields.vector==='invalid')throw Error('Invalid vector');return {canonicalVector:fields.vector,score:8.7,rating:'HIGH',nomenclature:'CVSS-B'};}
  if(opts.method!=='PATCH')throw Error('unexpected mutation');
  return new Promise((resolve,reject)=>requests.push({fields,resolve,reject}));
 };
 ` + strings.ReplaceAll(workspace, "export ", "") + strings.ReplaceAll(source, "export ", "") + `
 const findingWriteQueues=new Map(),cvssApplyDraftTokens=new Map(),cvssPreviewDrafts=createFindingDraftStore(),findingDrafts=createFindingDraftStore();
 const findings=[{id:1,cvss:'',severity:'Info'},{id:2,cvss:'B',severity:'Low'}];
 let bodyFindingId=1,selFinding=1,findingWritesInFlight=0,bodySavesInFlight=0,findEditMode=true,renders=0,guard;
 const bodySaveTimers=new Map(),bodySaveSnapshots=new Map(),findingAttachPending=new Set(),findingDeletesPending=new Set(),findingEvidenceWrites=new Map();
 const control=value=>({value,disabled:false,textContent:'',hidden:false,isConnected:true,handlers:{},classList:{toggle(){}},addEventListener(k,fn){this.handlers[k]=fn;}});
 const controls={findSaveState:control(''),findSaveRecovery:control(''),findSaveRetry:control(''),findToggleEdit:control('')};
 const $=s=>controls[s.slice(1)];
 const loadFindings=async()=>{},refreshDeferredFindingDetail=()=>{},renderFindingDetail=()=>renders++,toast=()=>{},captureActiveFindingTextEditor=()=>{},flushPendingBodySave=async()=>{},registerProjectSwitchGuard=fn=>guard=fn;
 ` + section("function findingWriteQueue(", "function renderFindingDetail(") + section("async function settleFindingsBeforeExport(", "export function flowFindings(") + section("registerProjectSwitchGuard(", "\n") + `
 const bindDone=()=>{const f=findings[0],edit=true;
 ` + section("  const te = $('#findToggleEdit');", "  const blurPatch") + `
 };
 const input=control(''),status=control(''),preview=control(''),apply=control(''),discard=control('');
 const metric={value:'H',dataset:{cvssMetric:'VC'},options:[{value:''},{value:'H'}],addEventListener(){}};
 const root={isConnected:true,querySelector:s=>({'#findCvss':input,'[data-cvss-status]':status,'[data-cvss-preview]':preview,'[data-cvss-apply]':apply,'[data-cvss-discard]':discard}[s]),querySelectorAll:()=>[metric]};
 bindCvssEditor(root,fields=>applyCvssFinding(1,fields),vector=>stageCvssPreview(1,vector),()=>discardCvssPreview(1));
 const type=value=>{input.value=value;input.handlers.input();};
 const tick=()=>new Promise(resolve=>setTimeout(resolve,0));
 const beginApply=async value=>{type(value);preview.handlers.click();await tick();const pending=apply.handlers.click();return {pending,request:requests.at(-1)};};
 const rejectApply=async value=>{const work=await beginApply(value);work.request.reject(Error('save failed'));await work.pending;};
 const unblocked=async()=>{
  if(findingDrafts.hasAny()||cvssPreviewDrafts.hasAny()||guard())throw Error('drafts still block project guard');
  await settleFindingsBeforeExport();
  findEditMode=true;bindDone();await controls.findToggleEdit.onclick();if(findEditMode)throw Error('Done still blocked');findEditMode=true;
  if(controls.findSaveState.textContent!=='Saved'||!controls.findSaveRecovery.hidden)throw Error('save/retry feedback stale');
 };
 type('invalid');preview.handlers.click();await tick();await discard.handlers.click();
 if(input.value!==''||metric.value!==''||requests.length)throw Error('invalid preview discard mutated persisted values');
 await rejectApply('A');
 if(!findingDrafts.failed(1)||findingDrafts.values(1).severity!=='High'||!guard())throw Error('real rejected Apply not retained');
 const count=requests.length;await discard.handlers.click();
 if(input.value!==''||requests.length!==count)throw Error('discard patched unwanted values');
 await unblocked();await retryFindingSaves(1);if(requests.length!==count)throw Error('Retry resubmitted abandoned Apply');
 await rejectApply('A');type('');await unblocked();await retryFindingSaves(1);if(requests.length!==count+1)throw Error('exact revert left retryable values');
 await rejectApply('A');
 const severity=findingDrafts.stage(1,{severity:'Low'}),summary=findingDrafts.stage(1,{summary:'Independent summary'}),other=findingDrafts.stage(2,{title:'Other finding'});
 stageCvssPreview(2,'Other preview');await discard.handlers.click();
 const retained=findingDrafts.values(1);
 if(retained.cvss!==undefined||retained.severity!=='Low'||retained.summary!=='Independent summary'||!findingDrafts.has(2)||cvssPreviewDrafts.values(2).vector!=='Other preview')throw Error('discard cleared independent edits');
 if(controls.findSaveState.textContent!=='Unsaved changes'||!controls.findSaveRecovery.hidden)throw Error('independent draft feedback incorrect');
 findingDrafts.acknowledge(1,severity);findingDrafts.acknowledge(1,summary);findingDrafts.acknowledge(2,other);cvssPreviewDrafts.discard(2,'vector');
 await rejectApply('A');const newerCVSS=findingDrafts.stage(1,{cvss:'Independent CVSS'});type('');
 if(findingDrafts.values(1).cvss!=='Independent CVSS'||findingDrafts.values(1).severity!==undefined)throw Error('exact revert cleared newer CVSS draft');findingDrafts.acknowledge(1,newerCVSS);
 await rejectApply('A');const retry=retryFindingSaves(1);await tick();requests.at(-1).reject(Error('retry failed'));await retry;
 await discard.handlers.click();await unblocked();
 const revertedPending=await beginApply('A');type('');revertedPending.request.reject(Error('save failed'));await revertedPending.pending;await unblocked();
 for(const scenario of ['cwe-success','cwe-failure','different-preview','independent-severity']) {
  const savedVector=findings[0].cvss;
  const applying=await beginApply('Queued Apply');
  const cweWrite=patchFinding(1,{cwe:'CWE-20'}).then(()=>null,error=>error);
  type(scenario==='different-preview'?'New preview B':savedVector);
  const independent=scenario==='independent-severity'?findingDrafts.stage(1,{severity:'Low'}):null;
  applying.request.reject(Error('Apply failed'));await applying.pending;
  const cweRequest=requests.at(-1);
  if(cweRequest===applying.request||cweRequest.fields.cwe!=='CWE-20'||!findingWriteQueues.has(1))throw Error('CWE did not remain pending after Apply failed');
  if(scenario==='cwe-failure')cweRequest.reject(Error('CWE failed'));else cweRequest.resolve({});
  await cweWrite;
  const remaining=findingDrafts.values(1);
  if(scenario==='different-preview') {
   if(cvssPreviewDrafts.values(1).vector!=='New preview B'||remaining.cvss!=='Queued Apply'||remaining.severity!=='High')throw Error('queue idle discarded differing preview or its drafts');
   await discard.handlers.click();await unblocked();
  } else {
   if(cvssPreviewDrafts.has(1)||remaining.cvss!==undefined||(!independent&&remaining.severity!==undefined))throw Error('queue idle left abandoned Apply values');
   if(independent){if(remaining.severity!=='Low')throw Error('queue idle discarded independent severity');findingDrafts.acknowledge(1,independent);updateFindingSaveFeedback(1);}
   if(scenario==='cwe-failure') {
    if(remaining.cwe!=='CWE-20'||!findingDrafts.failed(1)||controls.findSaveRecovery.hidden||!guard())throw Error('CWE failure not retained for recovery');
    const retryCwe=retryFindingSaves(1);await tick();const retryRequest=requests.at(-1);
    if(JSON.stringify(retryRequest.fields)!==JSON.stringify({cwe:'CWE-20'}))throw Error('Retry included abandoned Apply fields');
    retryRequest.resolve({});await retryCwe;
   }
   await unblocked();const beforeRetry=requests.length;await retryFindingSaves(1);if(requests.length!==beforeRetry)throw Error('Retry resurrected discarded Apply');
  }
 }
 const pending=await beginApply('B');type('');const discarding=discard.handlers.click();await tick();if(!discard.disabled)throw Error('discard failed to wait for Apply');pending.request.resolve({});await pending.pending;await discarding;
 if(input.value!=='B'||findings[0].cvss!=='B')throw Error('discard ignored settled persisted vector');await unblocked();
 const delayed=await beginApply('C');type('invalid');const oldDiscard=discard.handlers.click();type('newer typing');
 const newSeverity=findingDrafts.stage(1,{severity:'Medium'});delayed.request.reject(Error('save failed'));await delayed.pending;await oldDiscard;
 if(input.value!=='newer typing'||cvssPreviewDrafts.values(1).vector!=='newer typing'||findingDrafts.values(1).severity!=='Medium'||findingDrafts.values(1).cvss!==undefined)throw Error('old completion stole newer input or severity');
 findingDrafts.acknowledge(1,newSeverity);await discard.handlers.click();await unblocked();
 root.isConnected=false;await new Promise(resolve=>setTimeout(resolve,220));
 `
	if out, err := exec.Command("node", "--input-type=module", "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("CVSS shared draft recovery: %v\n%s", err, out)
	}
}
