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
