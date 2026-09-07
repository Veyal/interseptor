package control

import (
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

func TestFindingTargetMutationsKeepRenderedIndicesAfterFailure(t *testing.T) {
	source := readUIAsset(t, "js/finding-assessment.js")
	source = regexp.MustCompile(`(?m)^import .*;\n`).ReplaceAllString(source, "")
	script := `const initUiSelects=()=>{},toast=()=>{},bindTargetCleanup=()=>{},bindCapabilityClaims=()=>{};` + strings.ReplaceAll(source, "export ", "") + `
function element(dataset={},value='') {
  return {dataset,value,disabled:false,handlers:{},addEventListener(k,fn){this.handlers[k]=fn;},setAttribute(){}};
}
function fixture() {
  const cards=[0,1].map(i=>{
    const url=element({targetField:'url'},'https://example.com/'+i);
    const note=element({targetField:'note'},'');
    const move=element({targetMove:i?' -1':'1'}), remove=element();
    return {...element({targetIndex:String(i)}),url,note,move,remove,
      querySelectorAll(s){return s==='[data-target-field]'?[url,note]:s==='[data-target-move]'?[move]:[];},
      querySelector(s){return s==='[data-target-remove]'?remove:null;}};
  });
  const root={querySelector(){return null;},querySelectorAll(s){
    return s==='[data-target-index]'?cards:s==='[data-target-field]'?cards.flatMap(c=>[c.url,c.note]):s==='#find-sec-target input, #find-sec-target button'?cards.flatMap(c=>[c.url,c.note,c.move,c.remove]):[];
  }};
  let rejectFirst;const saved=[];
  bindFindingAssessment(root,{targets:cards.map((c,i)=>({url:c.url.value,flow_ids:[i+1]}))},{stage(){},refresh:async()=>{},openFlow(){},save(fields){
    saved.push(structuredClone(fields));
    return saved.length===1?new Promise((resolve,reject)=>{rejectFirst=reject;}):Promise.resolve({latest:true});
  }});
  return {cards,saved,reject:()=>rejectFirst(Error('injected failure'))};
}
for (const action of ['move','remove']) {
  const f=fixture();
  const pending=f.cards[0][action].onclick();
  f.reject();await pending;
  f.cards[1].note.value='Still belongs to target 2';
  f.cards[1].note.handlers.input();f.cards[1].note.handlers.blur();
  await new Promise(resolve=>setTimeout(resolve,0));
  const last=f.saved.at(-1).targets;
  if(last.length!==2||last[0].url!=='https://example.com/0'||last[1].url!=='https://example.com/1'||last[1].note!=='Still belongs to target 2'||last[0].flow_ids[0]!==1||last[1].flow_ids[0]!==2)throw Error(action+' failure corrupted rendered target ownership: '+JSON.stringify(last));
}
const f=fixture();const pending=f.cards[0].move.onclick();
await f.cards[1].remove.onclick();
if(f.saved.length!==1)throw Error('overlapping target mutation was accepted');
f.reject();await pending;
const unchanged=fixture();await unchanged.cards[0].url.handlers.blur();
if(unchanged.saved.length)throw Error('unchanged target blur triggered a save and refresh');
`
	if out, err := exec.Command("node", "--input-type=module", "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("finding target mutation recovery: %v\n%s", err, out)
	}
}
