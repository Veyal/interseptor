package control

import (
	"os/exec"
	"strings"
	"testing"
)

func TestFindingDeleteSharedWriteBoundary(t *testing.T) {
	source := readUIAsset(t, "js/findings.js")
	section := func(start, end string) string {
		t.Helper()
		a := strings.Index(source, start)
		if a < 0 {
			t.Fatalf("missing %s", start)
		}
		b := strings.Index(source[a:], end)
		if b < 0 {
			t.Fatalf("missing %s", end)
		}
		return source[a : a+b]
	}
	workspace := readUIAsset(t, "js/finding-workspace.js")
	workspace = strings.ReplaceAll(workspace[strings.Index(workspace, "export function createFindingDraftStore"):], "export ", "")
	script := workspace + `
 let findings=[{id:1,title:'One'},{id:2,title:'Two'}],bodyBlocks=[],bodyFindingId=1,selFinding=1,findEditMode=true;
 let bodySavesInFlight=0,findingWritesInFlight=0,findingsLoadEpoch=0,guard;
 const findingWriteQueues=new Map(),bodySaveTimers=new Map(),bodySaveSnapshots=new Map(),cvssApplyDraftTokens=new Map(),findingEvidenceWrites=new Map();
 const findingDeletesPending=new Set(),findingAttachPending=new Set();
 const findingDrafts=createFindingDraftStore(),cvssPreviewDrafts=createFindingDraftStore();
 const findingsEditable=()=>true,assertFindingsWritable=()=>{},FINDINGS_EDITING_OFF='off';
 const controls={findDetail:{inert:false},findSaveState:{},findSaveRecovery:{},findSaveRetry:{focus(){}}};
 const $=s=>controls[s.slice(1)],toast=()=>{},refreshDeferredFindingDetail=()=>{},loadFindings=async()=>{};
 const captureActiveFindingTextEditor=()=>{},registerProjectSwitchGuard=fn=>guard=fn;
 const requests=[],api=(path,options)=>new Promise((resolve,reject)=>requests.push({path,...options,resolve,reject}));
 const tick=()=>new Promise(r=>setTimeout(r,30));
 const outcome=p=>p.then(()=>null,e=>e);
 ` + section("function scheduleSave(", "// Paste/drop evidence") + section("function findingWriteQueue(", "function renderFindingDetail(") + section("async function settleFindingsBeforeExport(", "export function flowFindings(") + section("registerProjectSwitchGuard(", "\n") + `
 for(const fields of [{title:'Failed metadata'},{blocks:[{type:'text',md:'Failed body'}]}]) {
  const save=outcome(patchFinding(1,fields));requests.at(-1).reject(Error('save failed'));await save;
  const count=requests.length,err=await outcome(deleteFinding(1));
  if(!err||!err.message.includes('Retry')||requests.length!==count||!findingDrafts.failed(1)||!findings.some(f=>f.id===1))throw Error('delete stranded failed drafts');
  const retry=retryFindingSaves(1);await tick();requests.at(-1).resolve({});await retry;
 }
 stageCvssPreview(1,'Unapplied preview');
 const previewCount=requests.length;
 if(!await outcome(deleteFinding(1))||requests.length!==previewCount||!cvssPreviewDrafts.has(1))throw Error('delete discarded unapplied preview');
 await discardCvssPreview(1);
 const first=patchFinding(1,{title:'Pending'});
 bodyBlocks=[{type:'text',md:'Debounced body'}];scheduleSave(1);
 const evidence=withFindingEvidenceWrite(1,()=>api('/api/findings/1/images',{method:'POST'}));
 const evidenceRequest=requests.at(-1),patchRequest=requests.at(-2);
 findingAttachPending.add(1);
 const deleting=outcome(deleteFinding(1));await tick();
 if(!guard()||!controls.findDetail.inert||!findingDeletesPending.has(1)||requests.some(r=>r.method==='DELETE'))throw Error('delete did not lock and wait');
 if(!await outcome(patchFinding(1,{title:'Too late'})))throw Error('new PATCH accepted');
 if(!await outcome(withFindingEvidenceWrite(1,async()=>{})))throw Error('new evidence accepted');
 const draftTokens=findingDrafts.tokens(1);stageCvssPreview(1,'Too late');scheduleSave(1);
 if(cvssPreviewDrafts.has(1)||Object.keys(findingDrafts.tokens(1)).length!==Object.keys(draftTokens).length)throw Error('new drafts staged during delete');
 let exported=false;const exporting=settleFindingsBeforeExport().then(()=>exported=true);
 patchRequest.resolve({});await first;await tick();
 if(requests.at(-1).method!=='PATCH'||!JSON.parse(requests.at(-1).body).blocks)throw Error('debounced body not flushed');
 requests.at(-1).resolve({});await tick();
 if(requests.at(-1).method==='DELETE')throw Error('delete overtook evidence');
 evidenceRequest.resolve({});await evidence;await tick();
 if(requests.at(-1).method==='DELETE')throw Error('delete overtook pending flow attachment');
 findingAttachPending.delete(1);await tick();
 if(requests.at(-1).method!=='DELETE'||exported)throw Error('delete missing or export overtook delete');
 requests.at(-1).reject(Error('delete failed'));const err=await deleting;await exporting;
 if(!err||!findings.some(f=>f.id===1)||findingDeletesPending.size||controls.findDetail.inert)throw Error('failed DELETE lost recovery');
 const retry=patchFinding(1,{title:'Recovered'});requests.at(-1).resolve({});await retry;
 const other=findingDrafts.stage(2,{summary:'Keep other edits'});cvssPreviewDrafts.stage(2,{vector:'Other preview'});
 cvssApplyDraftTokens.set(1,{old:Symbol()});
 const success=deleteFinding(1);await tick();
 if(requests.at(-1).method!=='DELETE')throw Error('other finding draft blocked deletion');
 requests.at(-1).resolve({});await success;
 if(findings.some(f=>f.id===1)||findingDrafts.has(1)||cvssApplyDraftTokens.has(1)||bodySaveTimers.has(1)||findingDeletesPending.size||findingEvidenceWrites.size)throw Error('successful deletion retained owned state');
 if(findingDrafts.values(2).summary!=='Keep other edits'||cvssPreviewDrafts.values(2).vector!=='Other preview'||!guard())throw Error('other drafts lost');
 if(!await outcome(patchFinding(1,{title:'Late dialog'})))throw Error('late callback resurrected deleted draft');
 findingDrafts.acknowledge(2,other);cvssPreviewDrafts.discard(2,'vector');await settleFindingsBeforeExport();
 if(guard())throw Error('successful delete still blocks project switching');
 const restoreOptions={
 ` + source[strings.LastIndex(source, " canRestore:"):strings.LastIndex(source, " restored:")] + `
 };
 if(!restoreOptions.canRestore())throw Error('successful deletion blocks restore');
 findingDeletesPending.add(2);if(restoreOptions.canRestore())throw Error('restore bypassed deletion');findingDeletesPending.delete(2);
 findingEvidenceWrites.set(2,1);if(restoreOptions.canRestore())throw Error('restore bypassed evidence');findingEvidenceWrites.delete(2);
 `
	if out, err := exec.Command("node", "--input-type=module", "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("deletion boundary: %v\n%s", err, out)
	}
}
