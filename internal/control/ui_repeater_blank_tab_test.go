package control

import (
	"os/exec"
	"strings"
	"testing"
)

// Sending a flow to a fresh Repeater used to leave the untouched starter tab
// open next to the new one. The send-to path must fill an untouched blank tab,
// and only a genuinely blank one.
func TestUIRepeaterSendToReusesAnUntouchedBlankTab(t *testing.T) {
	source := readUIAsset(t, "js/tools.js")
	start := strings.Index(source, "export async function sendToRepeater(f)")
	end := strings.Index(source, "export function repTabIsBlank(t)")
	if start < 0 || end <= start {
		t.Fatal("sendToRepeater or repTabIsBlank not found")
	}
	requireUIContains(t, source[start:end], "&&repTabIsBlank(snapshot.tab));", "let t=reusable?.tab||blank?.tab||null;")
	script := repeaterRenderJS(t, source, "export function repTabIsBlank(t)") + `
const blank={method:'GET',url:'',headers:'',body:'',label:'',resId:null,sourceFlowId:null,history:[],sendPending:false};
if(!repTabIsBlank(blank))throw Error('a fresh tab must count as blank');
for(const [k,v] of [['url','http://example.com/'],['headers','X: 1'],['body','a=1'],['label','login'],['resId',4],['sourceFlowId',3],['history',[{id:1}]],['sendPending',true],['historyNeedsMigration',true]]){
  if(repTabIsBlank({...blank,[k]:v}))throw Error('tab with '+k+' must not be reused as blank');
}
if(repTabIsBlank({...blank,url:'   '})===false)throw Error('whitespace-only URL is still blank');
`
	if out, err := exec.Command("node", "--input-type=module", "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("Repeater blank tab behavior: %v\n%s", err, out)
	}
}
