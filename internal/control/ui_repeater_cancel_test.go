package control

import (
	"os/exec"
	"testing"
)

// TestUIRepeaterCancelAbortsTheInFlightSend runs the real repSend,
// setRepSendState and repCancelSend under node with the DOM stubbed: a pending
// send shows Cancel, Cancel aborts the fetch signal, and the tab settles in a
// non-error "cancelled" state that keeps its previous response.
func TestUIRepeaterCancelAbortsTheInFlightSend(t *testing.T) {
	ui := readUIAsset(t, "index.html")
	requireUIContains(t, ui, `id="repCancel"`, `title="Stop the request in flight (Esc)" hidden>Cancel</button>`)
	source := readUIAsset(t, "js/tools.js")
	requireUIContains(t, source, "onClose:t=>{t.sendAbort?.abort();return repDeleteHistory(t);}", "else if(action==='cancel'&&repCancelSend())e.preventDefault();")
	script := repeaterRenderJS(t, source, "function friendlySendError(raw)") +
		repeaterRenderJS(t, source, "function setRepSendState(stateName,label)") +
		repeaterRenderJS(t, source, "export function repCancelSend()") +
		repeaterRenderJS(t, source, "export async function repSend()") + `
const REP_RES_CANCELLED='CANCELLED';
const el=()=>({textContent:'',innerHTML:'',value:'',hidden:false,disabled:false,dataset:{},style:{},attrs:{},
  classList:{s:new Set(),add(c){this.s.add(c)},remove(...c){c.forEach(x=>this.s.delete(x))},toggle(){}},
  setAttribute(k,v){this.attrs[k]=v},focus(){globalThis.document.activeElement=this}});
const nodes={'#repSend':el(),'#repSendLabel':el(),'#repCancel':el(),'#repStatus':el(),'#repResView':el(),'#repBody':el()};
nodes['#repCancel'].hidden=true;
globalThis.document={activeElement:null};
const $=s=>nodes[s];
let active,rendered=0,persisted=0,toasts=[];
const repCur=()=>active,repSaveEditor=()=>{},repRefreshHL=()=>{},syncRepActions=()=>{},repSyncResView=()=>{};
const compactBody=s=>s,repBodyForDisplay=s=>s,statusColor=()=>'green',repStatusLine=f=>String(f.status);
const repPersist=()=>{persisted++},renderRepResponse=async()=>{rendered++},toast=m=>toasts.push(m);
const repRecordHistory=async()=>{},animateOnce=async()=>{},MOTION={},resetRepSend=()=>{},refreshRepHistory=()=>{};
let repResponseEpoch=0,repExtras=null,seenSignal=null;
const api=(path,opts)=>new Promise((resolve,reject)=>{seenSignal=opts.signal;
  opts.signal.addEventListener('abort',()=>reject(Object.assign(new Error('The operation was aborted.'),{name:'AbortError'})));});
function tab(resId){return {method:'GET',url:'http://example.com/slow',headers:'',body:'',reqView:'raw',resId,status:'200',sendEpoch:0};}

// 1. A pending send offers Cancel and moves focus there from Send.
active=tab(7);nodes['#repSend'].focus();
const sending=repSend();
await Promise.resolve();
if(!active.sendPending||nodes['#repCancel'].hidden)throw Error('pending send must show Cancel');
if(globalThis.document.activeElement!==nodes['#repCancel'])throw Error('focus must follow from the disabled Send to Cancel');
if(!seenSignal)throw Error('send must pass an AbortSignal to the control API');
// 2. Cancel aborts the request signal and settles without an error.
if(!repCancelSend())throw Error('repCancelSend must abort a pending send');
await sending;
if(!seenSignal.aborted)throw Error('Cancel did not abort the fetch');
if(active.sendPending||active.sendError||active.sendAbort)throw Error('cancelled tab still pending or errored: '+JSON.stringify(active));
if(!nodes['#repCancel'].hidden||nodes['#repSend'].disabled)throw Error('Cancel must hide and Send re-enable after cancelling');
if(active.resId!==7||rendered!==1||!/cancelled/i.test(nodes['#repStatus'].textContent))throw Error('cancel must keep and repaint the previous response: '+nodes['#repStatus'].textContent);
if(toasts.length)throw Error('a cancel is not an error toast: '+toasts);
// 3. With no earlier response the pane says the send was cancelled.
active=tab(null);
const second=repSend();await Promise.resolve();repCancelSend();await second;
if(nodes['#repResView'].innerHTML!=='CANCELLED')throw Error('no-response cancel must show the cancelled state');
// 4. Nothing to cancel when idle.
if(repCancelSend())throw Error('idle tab has nothing to cancel');
`
	if out, err := exec.Command("node", "--input-type=module", "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("Repeater cancel behavior: %v\n%s", err, out)
	}
}
