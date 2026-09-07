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
