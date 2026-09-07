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
	start := strings.Index(findingsSource, "function stageCvssPreview(")
	end := strings.Index(findingsSource, "function enqueueFindingPatch(")
	if start < 0 || end <= start {
		t.Fatal("missing finding-owned preview recovery")
	}
	if !strings.Contains(findingsSource, "vector => stageCvssPreview(f.id, vector), () => discardCvssPreview(f.id)") {
		t.Fatal("CVSS recovery callbacks not wired")
	}
	script := `
 const esc=x=>x,escAttr=x=>x;
 let previews=0,patches=0;
 const api=async(path,opts)=>{previews++;const vector=JSON.parse(opts.body).vector;if(!vector||vector==='invalid')throw Error('Invalid vector');return {canonicalVector:vector,score:8.7,rating:'HIGH',nomenclature:'CVSS-B'};};
 ` + strings.ReplaceAll(workspace, "export ", "") + strings.ReplaceAll(source, "export ", "") + `
 const findingWriteQueues=new Map(),cvssPreviewDrafts=createFindingDraftStore(),findings=[{id:1,cvss:''},{id:2,cvss:'B'}];
 ` + findingsSource[start:end] + `
 const control=value=>({value,disabled:false,textContent:'',handlers:{},classList:{toggle(){}},addEventListener(k,fn){this.handlers[k]=fn;}});
 const input=control(''),status=control(''),preview=control(''),apply=control(''),discard=control('');
 const metric={value:'H',dataset:{cvssMetric:'VC'},options:[{value:''},{value:'H'}],addEventListener(){}};
 const root={isConnected:true,querySelector:s=>({'#findCvss':input,'[data-cvss-status]':status,'[data-cvss-preview]':preview,'[data-cvss-apply]':apply,'[data-cvss-discard]':discard}[s]),querySelectorAll:()=>[metric]};
 let complete,reject;
 bindCvssEditor(root,async({vector})=>{patches++;const tokens=cvssPreviewDrafts.tokens(1);findingWriteQueues.set(1,{latestValues:{cvss:vector}});try{await new Promise((resolve,fail)=>{complete=resolve;reject=fail;});findings[0].cvss=vector;cvssPreviewDrafts.acknowledge(1,tokens);}finally{findingWriteQueues.delete(1);}},vector=>stageCvssPreview(1,vector),()=>discardCvssPreview(1));
 const type=value=>{input.value=value;input.handlers.input();};
 const evaluate=async()=>{preview.handlers.click();await new Promise(resolve=>setTimeout(resolve,0));};
 stageCvssPreview(2,'Other draft');
 type('invalid');await evaluate();if(!apply.disabled||!cvssPreviewDrafts.has(1))throw Error('invalid fixture not retained');
 await discard.handlers.click();if(input.value!==''||cvssPreviewDrafts.has(1)||metric.value!==''||patches)throw Error('empty persisted vector not restored without PATCH');
 if(!cvssPreviewDrafts.has(2))throw Error('discard cleared another finding');
 type('invalid');type('');if(cvssPreviewDrafts.has(1))throw Error('exact empty revert remains blocked');
 findings[0].cvss='A';type('');await discard.handlers.click();if(input.value!=='A'||cvssPreviewDrafts.has(1))throw Error('empty preview did not restore saved vector');
 type('invalid');type('A');if(cvssPreviewDrafts.has(1))throw Error('exact saved revert remains blocked');
 type('B');await evaluate();const saving=apply.handlers.click();type('A');if(!cvssPreviewDrafts.has(1))throw Error('revert to old value lost while Apply pending');
 const discarding=discard.handlers.click();await new Promise(resolve=>setTimeout(resolve,5));if(!discard.disabled||input.value!=='A')throw Error('discard did not wait for Apply');
 complete();await saving;await discarding;
 if(input.value!=='B'||cvssPreviewDrafts.has(1)||patches!==1)throw Error('discard did not use settled persisted value');
 type('C');await evaluate();const savingAgain=apply.handlers.click();type('invalid');const oldDiscard=discard.handlers.click();type('newer typing');complete();await savingAgain;await oldDiscard;
 if(input.value!=='newer typing'||cvssPreviewDrafts.values(1).vector!=='newer typing')throw Error('old completion discarded newer typing');
 await discard.handlers.click();if(input.value!=='C'||cvssPreviewDrafts.has(1))throw Error('newer draft not recoverable');
 type('D');await evaluate();const failing=apply.handlers.click();type('invalid');const discardAfterFailure=discard.handlers.click();reject(Error('save failed'));await failing;await discardAfterFailure;
 if(input.value!=='C'||cvssPreviewDrafts.has(1)||patches!==3)throw Error('failed Apply discard used unsaved value');
 cvssPreviewDrafts.discard(2,'vector');if(cvssPreviewDrafts.hasAny())throw Error('discard leaves draft guards blocked');
 root.isConnected=false;await new Promise(resolve=>setTimeout(resolve,220));
 `
	if out, err := exec.Command("node", "--input-type=module", "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("CVSS preview recovery: %v\n%s", err, out)
	}
}
