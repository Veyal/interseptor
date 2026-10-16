package control

import (
	"os/exec"
	"strings"
	"testing"
)

func repeaterRenderJS(t *testing.T, source, marker string) string {
	t.Helper()
	start := strings.Index(source, marker)
	if start < 0 {
		t.Fatalf("missing JavaScript function %s", marker)
	}
	end := strings.Index(source[start:], "\n}")
	if end < 0 {
		t.Fatalf("unterminated JavaScript function %s", marker)
	}
	return strings.TrimPrefix(source[start:start+end+2], "export ") + "\n"
}

func TestUIRepeaterRenderOwnsResponseAndUsesSandbox(t *testing.T) {
	ui := readUIAsset(t, "index.html")
	start := strings.Index(ui, `id="repResSeg"`)
	if start < 0 || !strings.Contains(ui[start:strings.Index(ui[start:], "</div>")+start], `data-view="render"`) {
		t.Fatal("Repeater response needs a Render view control")
	}
	core := readUIAsset(t, "js/core.js")
	source := readUIAsset(t, "js/tools.js")
	script := repeaterRenderJS(t, core, "export function formatHexDump(strOrBytes, maxBytes=32768)") +
		repeaterRenderJS(t, core, "export function renderHTMLResponse(raw)") +
		repeaterRenderJS(t, source, "function repSyncResView(t)") +
		repeaterRenderJS(t, source, "export async function renderRepResponse()") + `
const esc=String,escAttr=s=>String(s).replaceAll('&','&amp;').replaceAll('"','&quot;').replaceAll('<','&lt;');
const highlightHTTP=s=>s,prettify=s=>'pretty:'+s,contentTypeFromRaw=()=>'',highlightBodyText=String;
const RENDER_CAP=2*1024*1024,fmtSize=String,flowBodyDownloadHref=id=>'/body/'+id,flowBodyDownloadName=()=> 'body.html';
const buttons=['raw','pretty','decoded','hex','render'].map(view=>({dataset:{view},hidden:false,attrs:{},classList:{toggle(){}},setAttribute(k,v){this.attrs[k]=v;}}));
const pane={innerHTML:'',textContent:'',querySelector(){return null;}};
const $=s=>s==='#repResView'?pane:{querySelectorAll:()=>buttons};
let repResponseEpoch=0,active,api;
const repCur=()=>active;
const response=body=>'HTTP/1.1 200 OK\r\nContent-Type: text/html\r\n\r\n'+body;
function tab(id=1,mime='text/html',view='render'){return {resId:id,resMime:mime,resView:view,resLen:40,sendPending:false,sendError:''};}
active=tab();api=async()=>response('<h1 title="Greeting">Fixture</h1>');
await renderRepResponse();
if(!pane.innerHTML.includes('sandbox=""')||!pane.innerHTML.includes('Fixture')||pane.innerHTML.includes('HTTP/1.1')||pane.innerHTML.includes('allow-scripts')||pane.innerHTML.includes('allow-same-origin'))throw Error('HTML preview is not isolated body-only markup');
if(!renderHTMLResponse('HTTP/1.1 200 OK\n\n<p>LF body</p>').includes('LF body'))throw Error('LF header separator lost body');
active=tab(2,'application/json');api=async()=>'{"ok":true}';await renderRepResponse();
if(active.resView!=='pretty'||!buttons.find(b=>b.dataset.view==='render').hidden||buttons[1].attrs['aria-pressed']!=='true'||pane.innerHTML.includes('<iframe'))throw Error('non-HTML did not fall back to Pretty accessibly');
active=tab(4,'application/json','hex');api=async()=>'foo';await renderRepResponse();
if(!pane.innerHTML.includes('hex-dump')||!pane.innerHTML.includes('00000000'))throw Error('hex view did not render canonical dump');
active=tab();const pending=[];api=()=>new Promise(resolve=>pending.push(resolve));
const old=renderRepResponse(),latest=renderRepResponse();
pending[1](response('<p>Newest</p>'));await latest;pending[0](response('<p>Old</p>'));await old;
if(!pane.innerHTML.includes('Newest'))throw Error('older render replaced latest same-response preview');
const owner=active;const switching=renderRepResponse();active=tab(3);pane.innerHTML='other tab';pending[2](response('<p>Wrong tab</p>'));await switching;
if(pane.innerHTML!=='other tab')throw Error('preview painted into another tab');
active=owner;const sending=renderRepResponse();active.sendPending=true;pane.innerHTML='sending';pending[3](response('<p>Previous send</p>'));await sending;
if(pane.innerHTML!=='sending')throw Error('previous response replaced pending state');
active=tab();active.resLen=RENDER_CAP+1;api=()=>{throw Error('large preview fetched without opt-in');};await renderRepResponse();
if(pane.innerHTML.includes('<iframe')||!pane.innerHTML.includes('Show anyway'))throw Error('large HTML response needs explicit render opt-in');
`
	if out, err := exec.Command("node", "--input-type=module", "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("Repeater Render behavior: %v\n%s", err, out)
	}
}
