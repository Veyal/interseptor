package control

import (
	"os/exec"
	"testing"
)

// TestUIRepeaterReloadRestoresTheLastResponse round-trips a tab through the
// real serializeRepeaterTab/normalizeRepeaterTab: the last response's flow id,
// status line and view survive a reload instead of resetting to "No response
// yet", while hostile persisted values are rejected.
func TestUIRepeaterReloadRestoresTheLastResponse(t *testing.T) {
	source := readUIAsset(t, "js/tools.js")
	requireUIContains(t, source, "if(e&&e.status===404)$('#repResView').innerHTML=REP_RES_GONE")
	// persistedText through normalizeRepeaterTab are consecutive one-liners, so
	// one extraction (up to the first closing brace line) covers all of them.
	script := repeaterRenderJS(t, source, "function persistedText(value,fallback='')") +
		repeaterRenderJS(t, source, "function serializeRepeaterTab(t)") + `
const REP_HISTORY_RENDER_BATCH=50,newRepHistoryKey=()=>'k'+Math.random();
const normalizeRepHistory=h=>Array.isArray(h)?h:[];
const statusColor=s=>s>=500?'red':s>=200?'green':'grey';
const live={tid:1,method:'GET',url:'http://example.com/a',headers:'',body:'',reqView:'raw',resView:'hex',resId:42,status:'200 OK · 3 ms · 1 KB',color:'green',resCode:200,resMime:'text/html',resLen:1024,historyKey:'k1',history:[]};
const back=normalizeRepeaterTab(JSON.parse(JSON.stringify(serializeRepeaterTab(live))));
if(back.resId!==42||back.status!==live.status||back.color!=='green'||back.resMime!=='text/html'||back.resLen!==1024)throw Error('last response not restored: '+JSON.stringify(back));
if(back.resView!=='hex')throw Error('Hex response view not restored: '+back.resView);
const cancelled=normalizeRepeaterTab({tid:2,resId:5,resStatus:'Send cancelled · showing the previous response',resCode:200});
if(cancelled.color!=='var(--fg3)')throw Error('cancelled status must stay muted');
const fresh=normalizeRepeaterTab(JSON.parse(JSON.stringify(serializeRepeaterTab({...live,tid:3,resId:null,status:''}))));
if(fresh.resId!==null||fresh.status!==''||fresh.color!=='')throw Error('unsent tab must stay empty');
for(const bad of [-1,0,1.5,'7',Number.MAX_SAFE_INTEGER+2]){
  const t=normalizeRepeaterTab({tid:4,resId:bad,resStatus:'<b>x</b>'});
  if(t.resId!==null||t.status!=='')throw Error('unsafe persisted resId accepted: '+bad);
}
`
	if out, err := exec.Command("node", "--input-type=module", "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("Repeater restore behavior: %v\n%s", err, out)
	}
}
