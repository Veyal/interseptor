package control

import (
	"os/exec"
	"strings"
	"testing"
)

// TestUIRepeaterResponseStates covers the response pane edge states with the
// real repStatusLine and renderRepResponse: a transport failure is named
// instead of painting a synthetic "502 Bad Gateway", a multi-megabyte body in
// Raw/Pretty waits for an opt-in, and a 0 ms send still shows its duration.
func TestUIRepeaterResponseStates(t *testing.T) {
	core := readUIAsset(t, "js/core.js")
	source := readUIAsset(t, "js/tools.js")
	send := source[strings.Index(source, "export async function repSend()"):strings.Index(source, "export async function renderRepResponse()")]
	release := strings.Index(send, "setRepSendState('success','Sent');resetRepSend(600,t,sendEpoch);")
	paint := strings.Index(send, "await renderRepResponse();")
	if release < 0 || paint < 0 || release > paint {
		t.Fatal("Send must be released (and Cancel hidden) before a large response body is fetched and painted")
	}
	script := repeaterRenderJS(t, core, "export function formatHexDump(strOrBytes, maxBytes=32768)") +
		repeaterRenderJS(t, core, "export function renderHTMLResponse(raw)") +
		repeaterRenderJS(t, source, "function friendlySendError(raw)") +
		repeaterRenderJS(t, source, "function repStatusLine(f)") +
		repeaterRenderJS(t, source, "function repSyncResView(t)") +
		repeaterRenderJS(t, source, "export async function renderRepResponse()") + `
const esc=s=>String(s).replaceAll('&','&amp;').replaceAll('<','&lt;'),escAttr=esc;
const highlightHTTP=s=>s,prettify=s=>s,contentTypeFromRaw=()=>'',highlightBodyText=String;
const RENDER_CAP=2*1024*1024,fmtSize=n=>n+' B',fmtDur=ms=>ms+' ms',statusText=s=>({200:'OK',502:'Bad Gateway'}[s]||'');
const flowBodyDownloadHref=id=>'/body/'+id,flowBodyDownloadName=()=>'body.txt';
const REP_RES_GONE='GONE';
const buttons=['raw','pretty','decoded','hex','render'].map(view=>({dataset:{view},hidden:false,attrs:{},classList:{toggle(){}},setAttribute(k,v){this.attrs[k]=v;}}));
let onclick=null;
const pane={innerHTML:'',textContent:'',querySelector(){return {set onclick(f){onclick=f;}};}};
const $=s=>s==='#repResView'?pane:{querySelectorAll:()=>buttons};
let repResponseEpoch=0,active,calls=0;
const repCur=()=>active;
const api=async()=>{calls++;return 'HTTP/1.1 200 OK\r\n\r\nbody';};

const refused={status:502,error:'dial tcp 127.0.0.1:9: connect: connection refused',durationMs:0,resLen:0};
const line=repStatusLine(refused);
if(!line.startsWith('No response from the target · 0 ms')||line.includes('Bad Gateway'))throw Error('transport failure status line: '+line);
if(repStatusLine({status:200,durationMs:0,resLen:4})!=='200 OK · 0 ms · 4 B')throw Error('0 ms send lost its duration');

active={resId:9,resView:'pretty',resLen:0,resError:refused.error,sendPending:false,sendError:''};
await renderRepResponse();
if(calls!==0||!pane.innerHTML.includes('Connection refused')||pane.innerHTML.includes('502'))throw Error('transport failure pane: '+pane.innerHTML);

for(const view of ['raw','pretty']){
  calls=0;active={resId:10,resView:view,resLen:RENDER_CAP+1,resMime:'text/plain',sendPending:false,sendError:''};
  await renderRepResponse();
  if(calls!==0||!pane.innerHTML.includes('Show anyway'))throw Error(view+': large body fetched without opt-in');
  onclick();await new Promise(r=>setTimeout(r,0));
  if(calls!==1||!pane.innerHTML.includes('body'))throw Error(view+': Show anyway did not render the body');
}
calls=0;active={resId:11,resView:'pretty',resLen:1024,sendPending:false,sendError:''};
await renderRepResponse();
if(calls!==1)throw Error('small body must render directly');
`
	if out, err := exec.Command("node", "--input-type=module", "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("Repeater response states: %v\n%s", err, out)
	}
}
