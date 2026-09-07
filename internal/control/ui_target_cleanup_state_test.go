package control

import (
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

func TestTargetTemplateApplyDeduplicatesAndRetainsEvidence(t *testing.T) {
	source := regexp.MustCompile(`(?m)^import .*;\n`).ReplaceAllString(readUIAsset(t, "js/finding-target-cleanup.js"), "")
	script := `const esc=x=>x,escAttr=x=>x,toast=()=>{};
 const original=[{url:'https://example.com/records/1',flow_ids:[1]},{url:'https://example.com/records/2',flow_ids:[2]}];
 let calls=0,saved;
 const api=async(path,opts)=>{calls++;if(calls===1)return {targets:structuredClone(original),removed:0,suggestions:original.map((t,index)=>({index,template:'https://example.com/records/{id}'}))};const targets=JSON.parse(opts.body).targets;if(targets.some(t=>t.url!=='https://example.com/records/{id}'))throw Error('template choice lost');return {targets:[{url:targets[0].url,flow_ids:[1,2]}],suggestions:[],removed:1};};
 ` + strings.ReplaceAll(source, "export ", "") + `
 function button(dataset={}){return{dataset,disabled:false,attrs:{'aria-pressed':'false'},setAttribute(k,v){this.attrs[k]=v;},getAttribute(k){return this.attrs[k];}};}
 const start=button(),apply=button(),cancel=button(),toggles=[0,1].map(i=>button({template:String(i),url:'https://example.com/records/{id}'}));
 const panel={isConnected:true,querySelectorAll:s=>toggles,querySelector:s=>s==='[data-apply]'?apply:cancel,remove(){this.isConnected=false;}};
 const root={querySelector:s=>s==='#findTargetCleanup'?start:s==='.find-target-cleanup'?panel:null};
 bindTargetCleanup(root,()=>structuredClone(original),async next=>{saved=next;return true;});await start.onclick();toggles.forEach(b=>b.onclick());await apply.onclick();
 if(calls!==2||saved.length!==1||saved[0].flow_ids.join(',')!=='1,2')throw Error('template Apply left duplicates/lost evidence: '+JSON.stringify(saved));
 `
	if out, err := exec.Command("node", "--input-type=module", "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("target template apply: %v\n%s", err, out)
	}
}
