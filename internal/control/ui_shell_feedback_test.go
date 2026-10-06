package control

import (
	"os/exec"
	"strings"
	"testing"
)

// shellFeedbackHarness evaluates the real core.js toast/api helpers against a
// tiny fake DOM so the behavior (not only the source text) is pinned.
func shellFeedbackHarness(t *testing.T, body string) {
	t.Helper()
	core := readUIAsset(t, "js/core.js")
	script := "const icon=(n)=>'<svg>'+n+'</svg>';\n" +
		repeaterRenderJS(t, core, "function evictToasts(c)") +
		repeaterRenderJS(t, core, "function dismissToast(t)") +
		repeaterRenderJS(t, core, "function armToastTimer(t, ms)") +
		repeaterRenderJS(t, core, "export function toast(m, sev)") +
		repeaterRenderJS(t, core, "export function toastError(prefix, e)") +
		repeaterRenderJS(t, core, "async function apiErrorMessage(r)") +
		repeaterRenderJS(t, core, "function apiFetchError(e, opts)") +
		repeaterRenderJS(t, core, "export async function api(path,opts)") +
		repeaterRenderJS(t, core, "export async function apiTry(path, opts, {toastOnError=true, label=''}={})") +
		`
const eq=(got,want,msg)=>{if(got!==want)throw Error(msg+': got '+JSON.stringify(got)+' want '+JSON.stringify(want));};
class FakeEl{
  constructor(){this.children=[];this.attrs={};this.listeners={};this.classes=new Set();this.parent=null;this.textContent='';this.tabIndex=-1;}
  set className(v){this.classes=new Set(String(v).split(/\s+/).filter(Boolean));}
  get className(){return [...this.classes].join(' ');}
  get classList(){const s=this.classes;return {add:c=>s.add(c),remove:c=>s.delete(c),contains:c=>s.has(c)};}
  setAttribute(k,v){this.attrs[k]=String(v);}
  getAttribute(k){return this.attrs[k]??null;}
  appendChild(c){c.parent=this;this.children.push(c);return c;}
  remove(){if(this.parent)this.parent.children=this.parent.children.filter(x=>x!==this);}
  addEventListener(n,f){(this.listeners[n]??=[]).push(f);}
  fire(n){(this.listeners[n]||[]).forEach(f=>f({}));}
  matches(sel){return sel===':hover'?!!this.hover:false;}
  contains(x){return x===this;}
  querySelectorAll(sel){
    const not=sel.match(/^\.toast-item:not\(\.(\w+)\)$/);
    if(not)return this.children.filter(c=>!c.classes.has(not[1]));
    if(sel==='.toast-item')return this.children.slice();
    if(sel==='.toast-item.error')return this.children.filter(c=>c.classes.has('error'));
    throw Error('unsupported selector '+sel);
  }
}
const container=new FakeEl();
globalThis.document={createElement:()=>new FakeEl(),activeElement:null};
globalThis.requestAnimationFrame=f=>f();
const $=()=>container;
const timers=[];let now=0;
globalThis.setTimeout=(f,ms)=>{const h={f,at:now+ms,live:true};timers.push(h);return h;};
globalThis.clearTimeout=h=>{if(h)h.live=false;};
globalThis.Date={now:()=>now};
const advance=ms=>{now+=ms;for(const h of timers.slice()){if(h.live&&h.at<=now){h.live=false;h.f();}}};
` + body
	if out, err := exec.Command("node", "--input-type=module", "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("shell feedback harness: %v\n%s", err, out)
	}
}

func TestUIToastErrorsAreAssertivePersistentAndPausable(t *testing.T) {
	shellFeedbackHarness(t, `
toast('saved');
eq(container.children[0].getAttribute('role'),null,'info toasts stay in the polite live region');
toastError('Save failed',new Error('disk full'));
const err=container.children[1];
eq(err.textContent,'Save failed: disk full','toastError prefixes the message');
eq(err.getAttribute('role'),'alert','errors are announced assertively');
eq(err.classes.has('error'),true,'errors carry the error class');
eq(err.tabIndex,0,'errors are focusable so keyboard users can pause them');
toastError(new Error('bare'));
eq(container.children[2].textContent,'bare','toastError(e) works without a prefix');
eq(err.children.length,1,'errors carry a dismiss button');
eq(err.children[0].getAttribute('aria-label'),'Dismiss','the dismiss button is named');
// errors outlive the info lifetime and are never evicted by newer info toasts
for(let i=0;i<6;i++)toast('info '+i);
eq(container.children.includes(err),true,'info toasts must not evict an error');
advance(2700);
eq(container.children.includes(err),true,'an error outlives the 2.6s info lifetime');
// errors never auto-dismiss (WCAG 2.2.1)
advance(600000);
eq(container.children.includes(err),true,'an error persists until dismissed');
// the dismiss button removes it
err.children[0].fire('click');advance(300);
eq(container.children.includes(err),false,'the Dismiss button removes the error');
// Escape on a focused error dismisses it
const err2=container.children.find(c=>c.classes.has('error'));
(err2.listeners.keydown||[]).forEach(f=>f({key:'Escape'}));advance(300);
eq(container.children.includes(err2),false,'Escape dismisses a focused error');
// warnings still time out and pause on hover
toast('careful','warn');
const warn=container.children[container.children.length-1];
warn.hover=true;warn.fire('mouseenter');
advance(60000);
eq(container.children.includes(warn),true,'a hovered warning stays put');
warn.hover=false;warn.fire('mouseleave');
advance(20000);advance(300);
eq(container.children.includes(warn),false,'a warning is removed after the pointer leaves');
`)
}

func TestUIApiErrorsAreActionable(t *testing.T) {
	shellFeedbackHarness(t, `
const loc={pathname:'/',href:''};globalThis.location=loc;
const reject=async(f,msg)=>{try{await f();}catch(e){return e;}throw Error('expected rejection: '+msg);};
const res=(status,body,{statusText='',ct='text/plain'}={})=>({ok:status<400,status,statusText,headers:{get:()=>ct},
  text:async()=>body,json:async()=>JSON.parse(body)});
globalThis.fetch=async()=>{throw new TypeError('Failed to fetch');};
let e=await reject(()=>api('/api/x'),'network');
eq(/Cannot reach the Interseptor control server/.test(e.message),true,'network failure message: '+e.message);
eq(e.code,'network','network failures are tagged');
globalThis.fetch=async()=>res(502,'',{statusText:''});
e=await reject(()=>api('/api/x'),'empty statusText');
eq(e.message,'HTTP 502','empty HTTP/2 statusText falls back to the status');
globalThis.fetch=async()=>res(500,'<html>boom</html>',{statusText:'Internal Server Error'});
e=await reject(()=>api('/api/x'),'html body');
eq(e.message,'Internal Server Error','HTML bodies are never surfaced');
globalThis.fetch=async()=>res(400,'bad cidr 10.0.0.0/99\n',{statusText:'Bad Request'});
e=await reject(()=>api('/api/x'),'text body');
eq(e.message,'bad cidr 10.0.0.0/99','short plain-text bodies are surfaced');
globalThis.fetch=async()=>res(400,JSON.stringify({error:'name required'}),{ct:'application/json'});
e=await reject(()=>api('/api/x'),'json body');
eq(e.message,'name required','JSON errors are surfaced');
eq(e.status,400,'status is attached');
// GETs get a default timeout signal, mutations do not, caller signals win
AbortSignal.timeout??=()=>({});
let seen;globalThis.fetch=async(p,o)=>{seen=o;return res(200,'{}',{ct:'application/json'});};
await api('/api/x');eq(!!seen.signal,true,'GET requests carry a default timeout signal');
await api('/api/x',{method:'POST'});eq(!!seen.signal,false,'mutations carry no default timeout');
const own=new AbortController().signal;await api('/api/x',{signal:own});eq(seen.signal,own,'a caller signal is preserved');
globalThis.fetch=async()=>res(401,'',{});
await reject(()=>api('/api/x'),'401');eq(loc.href,'/login','401 still redirects to login');
// apiTry reports failures through toastError
globalThis.fetch=async()=>res(500,JSON.stringify({error:'nope'}),{ct:'application/json'});
const out=await apiTry('/api/x',null,{label:'Load'});
eq(out,null,'apiTry returns null on failure');
const last=container.children[container.children.length-1];
eq(last.textContent,'Load: nope','apiTry toasts the labelled error');
eq(last.classes.has('error'),true,'apiTry failures are error toasts');
`)
}

func TestUIToastStylesAllowHoverPause(t *testing.T) {
	css := readUIAsset(t, "surfaces.css")
	if !strings.Contains(css, "#toast .toast-item{pointer-events:auto}") {
		t.Error("toast items must re-enable pointer events so hover can pause them")
	}
}
