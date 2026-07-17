import { $, esc, escAttr, toast, api, methodColor, statusColor, statusText, highlightHTTP, highlightHeaderLines, highlightBodyText, prettify, beautifyBody, fmtDur, fmtSize, openCtxMenu, DEC_OPS, contentTypeFromRaw, pickTextFile, normalizeListText, parseListLines, previewListLines, LIST_PREVIEW_LINES, wireRowKey, uiPrompt, createTabManager, projectStorageKey } from './core.js';

// friendlySendError turns a raw backend/network error (Go's url.Parse wording,
// net.OpError text, etc.) into a short, actionable lead sentence for a user who
// isn't reading Go source — the raw detail stays appended for anyone who is.
function friendlySendError(raw){
  const m=raw||'';
  const lead=
    /invalid request URL/i.test(m) ? "That doesn't look like a valid URL — check the scheme (http/https) and try again." :
    /refusing to (send|attack|forward).*own listener/i.test(m) ? "Can't target Interceptor's own address — that would create a loop." :
    /connection refused/i.test(m) ? 'Connection refused — nothing is listening at that address.' :
    /no such host|lookup .* no such host|dns/i.test(m) ? "Couldn't resolve that host — check the domain name." :
    /(deadline exceeded|timeout|timed out)/i.test(m) ? 'The request timed out.' :
    /x509|certificate/i.test(m) ? "TLS certificate error — the target's certificate isn't trusted." :
    null;
  return lead ? lead+' ('+m+')' : m;
}

// repStatusLine builds a rich response summary: "200 OK · 142 ms · 4.1 KB".
function repStatusLine(f){
  const head=f.status?f.status+' '+statusText(f.status):(f.error||'sent');
  return head+(f.durationMs?' · '+fmtDur(f.durationMs):'')+(f.resLen!=null?' · '+fmtSize(f.resLen):'');
}
// REP_RES_EMPTY — the response pane's placeholder before any send. #repResView
// is a <pre> (it renders raw/highlighted HTTP once a response arrives), so the
// shared .state-empty block is nested inside it rather than replacing the tag.
const REP_RES_EMPTY='<div class="state-empty"><div class="state-empty-icon">▸</div><div class="state-empty-title">No response yet</div><p class="state-empty-hint">Send a request to see the response.</p></div>';

/* ---- repeater (multi-tab; each tab = an endpoint with its own history) ---- */
export function repBlank(seq){return {tid:seq,title:'new tab',method:'GET',url:'',headers:'',body:'',reqView:'pretty',resId:null,resView:'pretty',status:'',color:'',sourceFlowId:null,codecId:'',rawBody:'',applyOnSend:false,decodedPlain:''};}
// repReqContentType reads Content-Type from the editable headers pane so the body
// overlay highlights with the right syntax (JSON/markup/CSS) even before a send.
function repReqContentType(){const h=$('#repHeaders');if(!h)return'';const m=(h.value||'').match(/^content-type:\s*(\S.*?)(?:\s*;|\s*$)/im);return m?m[1].trim():'';}
// repRefreshHL repaints the colored overlays behind the request headers/body
// textareas from their current values. A trailing newline mirrors the textarea's
// reserved last line so the two stay vertically aligned.
export function repRefreshHL(){
  const h=$('#repHeadersHL'),b=$('#repBodyHL');
  if(h)h.innerHTML=highlightHeaderLines(($('#repHeaders').value)||'')+'\n';
  if(b)b.innerHTML=highlightBodyText(($('#repBody').value)||'',repReqContentType())+'\n';
}
export function repTitle(t){if(!t.url)return 'new tab';try{const u=new URL(t.url);return t.method+' '+u.host+u.pathname;}catch(e){return t.method+' '+t.url.slice(0,46);}}
export function repTabEndpoint(t){if(!t||!t.url)return null;try{const u=new URL(t.url);return u.host+u.pathname;}catch(e){return null;}}
export function repFlowEndpoint(f){return f.host+String(f.path||'').split('?')[0];}
export function headersToText(h){if(!h)return'';const out=[];(h.Host||[]).forEach(v=>out.push('Host: '+v));Object.keys(h).sort().forEach(k=>{if(k==='Host')return;(h[k]||[]).forEach(v=>out.push(k+': '+v));});return out.join('\n');}

function compactBody(s){
  const t=(s||'').replace(/^\uFEFF/,'').trim();
  if(t&&(t[0]==='{'||t[0]==='[')){try{return JSON.stringify(JSON.parse(t));}catch(e){}}
  return s||'';
}
function repBodyForDisplay(body,view){
  if(view==='pretty')return beautifyBody(body||'');
  return body||'';
}
function repSyncReqSeg(view){
  const seg=$('#repReqSeg');if(!seg)return;
  seg.querySelectorAll('button').forEach(x=>{const on=x.dataset.view===view;x.classList.toggle('on',on);x.setAttribute('aria-pressed',on?'true':'false');});
}

// repTabs — shared tab-manager instance (docs/UI-REDESIGN-ROADMAP.md §4).
// Storage is project-scoped (`rep.tabs.<project>`) so switching projects does
// not leak another engagement's drafts (#17). Legacy unscoped `rep.tabs` is
// migrated once into the current project via projectStorageKey.
function persistUIState(panel, blob){
  // Fire-and-forget project DB write so drafts survive browser clears / machines.
  api('/api/ui/'+panel,{method:'PUT',headers:{'content-type':'application/json'},body:JSON.stringify(blob)}).catch(()=>{});
}
async function hydrateUIState(panel, storageBase){
  try{
    const d=await api('/api/ui/'+panel);
    if(d&&d.value!=null){
      try{localStorage.setItem(projectStorageKey(storageBase),JSON.stringify(d.value));}catch(e){}
      return true;
    }
  }catch(e){}
  return false;
}
export const repTabs=createTabManager({
  storageKey:()=>projectStorageKey('rep.tabs'),
  blank:repBlank,
  title:repTitle,
  onSave:()=>repSaveEditor(),
  onLoad:()=>repLoadEditor(),
  normalize:t=>({tid:t.tid,method:t.method||'GET',url:t.url||'',headers:t.headers||'',body:t.body||'',reqView:t.reqView||'pretty',resView:t.resView||'pretty',resId:null,status:'',color:'',title:'',sourceFlowId:t.sourceFlowId||null,codecId:t.codecId||'',rawBody:t.rawBody||'',applyOnSend:!!t.applyOnSend,decodedPlain:t.decodedPlain||''}),
  serialize:t=>({tid:t.tid,method:t.method,url:t.url,headers:t.headers,body:t.body,reqView:t.reqView||'pretty',resView:t.resView,sourceFlowId:t.sourceFlowId||null,codecId:t.codecId||'',rawBody:t.rawBody||'',applyOnSend:!!t.applyOnSend,decodedPlain:t.decodedPlain||''}),
  labelStyle:(t,active)=>`color:${active?methodColor(t.method):'inherit'}`,
  onPersist:blob=>persistUIState('repeater',blob),
});
export function repCur(){return repTabs.cur();}
export function renderRepTabs(){repTabs.render('#repTabs');}
export function repSwitch(tid){repTabs.switchTo(tid);}
export function repCloseTab(tid){repTabs.close(tid);}
export function repPersist(){repTabs.persist();}
export function repPersistDebounced(){repTabs.persistDebounced();}
export function repSaveEditor(){
  const t=repCur();if(!t)return;
  t.method=$('#repMethod').value;t.url=$('#repUrl').value;t.headers=$('#repHeaders').value;
  const v=$('#repBody').value;
  if((t.reqView||'raw')==='decoded')t.decodedPlain=v;
  else t.body=v;
  t.title=repTitle(t);
}
function repCodecBadge(t){
  const b=$('#repCodecBadge');if(!b)return;
  if((t.reqView||'')==='decoded'&&t.codecId){
    b.style.display='';
    b.textContent=(t.applyOnSend?'re-encode on send · ':'display · ')+(t.codecId);
  }else{b.style.display='none';b.textContent='';}
}
export function repNewTab(){repSaveEditor();const t=repBlank(repTabs.seq++);repTabs.tabs.push(t);repTabs.active=t.tid;renderRepTabs();return t;}
export function repLoadEditor(){
  const t=repCur();if(!t)return;
  $('#repMethod').value=t.method||'GET';$('#repUrl').value=t.url||'';$('#repHeaders').value=t.headers||'';
  const rv=t.reqView||'raw';
  repSyncReqSeg(rv);
  if(rv==='decoded')$('#repBody').value=t.decodedPlain||'';
  else $('#repBody').value=repBodyForDisplay(t.body,rv);
  repCodecBadge(t);
  repRefreshHL();
  $('#repResSeg').querySelectorAll('button').forEach(x=>{const on=x.dataset.view===(t.resView||'pretty');x.classList.toggle('on',on);x.setAttribute('aria-pressed',on?'true':'false');});
  if(t.resId){$('#repStatus').textContent=t.status||'';$('#repStatus').style.color=t.color||'var(--fg3)';renderRepResponse();}
  else{$('#repStatus').textContent='';$('#repResView').innerHTML=REP_RES_EMPTY;}
  loadRepHistory();
}
async function repEnterDecoded(t){
  const flowId=t.sourceFlowId||t.resId;
  const wire=t.body||'';
  try{
    let d;
    if(flowId){
      d=await api('/api/flows/'+flowId+'/decoded?side=req');
    }else{
      d=await api('/api/codecs/test',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({side:'req',rawBody:wire,host:(()=>{try{return new URL(t.url).host;}catch(e){return'';}})()})});
    }
    if(!d.matched){toast('no message codec matched');t.reqView='pretty';repSyncReqSeg('pretty');repCodecBadge(t);return false;}
    if(d.error){toast(d.error);t.reqView='pretty';repSyncReqSeg('pretty');repCodecBadge(t);return false;}
    t.codecId=d.codecId||'';t.applyOnSend=!!d.applyOnSend;t.rawBody=wire;t.decodedPlain=d.plaintext||'';
    $('#repBody').value=t.decodedPlain;repCodecBadge(t);repRefreshHL();return true;
  }catch(e){toast(e.message);t.reqView='pretty';repSyncReqSeg('pretty');return false;}
}
export async function repSend(){
  repSaveEditor();const t=repCur();if(!t)return;
  if(!(t.url||'').trim()){toast('enter a URL');return;}
  let body=t.body,payload={method:t.method,url:t.url.trim(),headers:t.headers,body};
  if((t.reqView||'raw')==='decoded'){
    if(t.applyOnSend&&t.codecId){
      payload.bodyMode='decoded';payload.codecId=t.codecId;payload.body=t.decodedPlain||'';
      payload.rawBody=t.rawBody||t.body||'';
      if(t.sourceFlowId)payload.flowId=t.sourceFlowId;
    }else{
      toast('decoded view is display-only for this codec — sending raw wire body');
      payload.body=t.rawBody||t.body||'';
    }
  }else{
    t.body=compactBody(t.body);payload.body=t.body;
    if((t.reqView||'raw')==='pretty')$('#repBody').value=repBodyForDisplay(t.body,'pretty');
    else $('#repBody').value=t.body;
  }
  repRefreshHL();
  $('#repSend').textContent='Sending…';$('#repSend').disabled=true;
  $('#repStatus').textContent='sending…';$('#repStatus').style.color='var(--fg3)';
  $('#repResView').innerHTML='<span class="blink" style="color:var(--fg3)">sending…</span>';
  try{
    const flow=await api('/api/repeater/send',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify(payload)});
    t.resId=flow.id;t.status=repStatusLine(flow);t.color=statusColor(flow.status);
    $('#repStatus').textContent=t.status;$('#repStatus').style.color=t.color;
    if(flow.status===401) toast('401 Unauthorized — run login macro in Settings → Session or enable Re-auth on 401');
    await renderRepResponse();loadRepHistory();repPersist();
  }catch(e){const msg=friendlySendError(e.message);$('#repStatus').textContent='';$('#repResView').textContent='(error: '+msg+')';toast(msg);}
  $('#repSend').textContent='Send ▸';$('#repSend').disabled=false;
}
export async function renderRepResponse(){
  const t=repCur();if(!t||!t.resId)return;
  try{const raw=await api('/api/flows/'+t.resId+'/raw?side=res');
    // A tab switch during the fetch would otherwise paint this response into the
    // now-active tab's shared #repResView pane.
    if(repCur()!==t)return;
    $('#repResView').innerHTML=highlightHTTP((t.resView==='pretty')?prettify(raw):raw,t.resView==='pretty',contentTypeFromRaw(raw));}
  catch(e){if(repCur()===t)$('#repResView').textContent='(error: '+e.message+')';}
}
export async function loadRepHistory(){
  const box=$('#repHistory');if(!box)return;const t=repCur();const ep=repTabEndpoint(t);
  const setCount=n=>{const tg=$('#repHistToggle');if(tg)tg.textContent='⟲ History'+(n?' ('+n+')':'');};
  try{
    const d=await api('/api/repeater/history');
    if(repCur()!==t)return; // tab switched mid-fetch — don't paint stale history
    const flows=ep?(d.flows||[]).filter(f=>repFlowEndpoint(f)===ep):[];
    setCount(flows.length);
    if(!flows.length){box.innerHTML='<div class="hint" style="padding:10px">'+(ep?'No sends to this endpoint yet.':'Send a request to start this tab’s history.')+'</div>';return;}
    box.innerHTML=flows.map(f=>`<div class="h ${t&&f.id===t.resId?'sel':''}" data-id="${f.id}">
      <div><span style="color:${methodColor(f.method)};font-weight:700">${esc(f.method)}</span> <span style="color:${statusColor(f.status)};font-weight:700">${f.status||'—'}</span></div>
      <div class="u">${esc(f.host)}${esc(f.path)}</div></div>`).join('');
    box.querySelectorAll('.h').forEach(el=>{el.onclick=()=>repLoadSend(Number(el.dataset.id));wireRowKey(el,()=>repLoadSend(Number(el.dataset.id)));});
  }catch(e){}
}
// Toggle the per-tab history rail (hidden by default to give the editor full width).
$('#repHistToggle')&&($('#repHistToggle').onclick=()=>{const h=$('#repHistory');if(h)h.style.display=(h.style.display==='none'?'':'none');});
export async function repLoadSend(id){
  const t=repCur();if(!t)return;
  try{
    const d=await api('/api/flows/'+id);
    const raw=await api('/api/flows/'+id+'/raw?side=req');
    if(repCur()!==t)return; // user switched tabs while loading — keep their new tab intact
    const def=(d.scheme==='https'&&d.port===443)||(d.scheme==='http'&&d.port===80);
    const i=raw.indexOf('\r\n\r\n');
    t.method=d.method;t.url=`${d.scheme}://${d.host}${def?'':':'+d.port}${d.path}`;t.headers=headersToText(d.reqHeaders);
    t.body=i>=0?raw.slice(i+4):'';
    t.sourceFlowId=id;t.codecId='';t.decodedPlain='';t.rawBody='';t.applyOnSend=false;
    t.resId=id;t.status=repStatusLine(d);t.color=statusColor(d.status);t.title=repTitle(t);
    renderRepTabs();repLoadEditor();repPersist();
  }catch(e){toast(e.message);}
}
export async function sendToRepeater(f){
  document.querySelector('.tab[data-tab="repeater"]').click();
  repSaveEditor();
  const fep=repFlowEndpoint(f);
  let t=repTabs.tabs.find(x=>repTabEndpoint(x)===fep);
  if(!t){t=repBlank(repTabs.seq++);repTabs.tabs.push(t);}
  repTabs.active=t.tid;
  try{
    const d=await api('/api/flows/'+f.id);
    const def=(d.scheme==='https'&&d.port===443)||(d.scheme==='http'&&d.port===80);
    t.method=d.method;t.url=`${d.scheme}://${d.host}${def?'':':'+d.port}${d.path}`;t.headers=headersToText(d.reqHeaders);
    const raw=await api('/api/flows/'+f.id+'/raw?side=req');const i=raw.indexOf('\r\n\r\n');t.body=i>=0?raw.slice(i+4):'';
    t.sourceFlowId=f.id;t.codecId='';t.decodedPlain='';t.rawBody='';t.applyOnSend=false;
    t.resId=null;t.status='';t.color='';t.title=repTitle(t);
    renderRepTabs();repLoadEditor();repPersist();
    toast('loaded #'+f.id+' into Repeater');
  }catch(e){toast(e.message);}
}
export async function repInit(){
  await hydrateUIState('repeater','rep.tabs');
  repTabs.init('#repTabs');
  // First persist migrates localStorage drafts into the project DB.
  if(repTabs.tabs.length) repTabs.persist();
  ['#repMethod','#repUrl'].forEach(s=>{const el=$(s);if(el)el.addEventListener('input',()=>{
    repSaveEditor();
    // Typing in method/url only changes the active tab's label — don't rebuild the
    // whole tab bar (and re-wire every tab) on every keystroke. Update the label.
    const t=repCur(); if(t){
      const lbl=document.querySelector('#repTabs .rep-tab.on .rt-label');
      if(lbl){lbl.textContent=t.title||'new tab'; lbl.style.color=t.tid===repTabs.active?methodColor(t.method):'inherit';}
      const tab=document.querySelector('#repTabs .rep-tab.on'); if(tab)tab.title=t.title||'new tab';
    }
    repPersistDebounced();
  });});
  ['#repHeaders','#repBody'].forEach(s=>{const el=$(s);if(el)el.addEventListener('input',()=>{repSaveEditor();repRefreshHL();repPersistDebounced();});});
  // Keep each colored overlay scrolled in lockstep with its textarea.
  [['#repHeaders','#repHeadersHL'],['#repBody','#repBodyHL']].forEach(([ta,hl])=>{
    const t=$(ta),p=$(hl);if(t&&p)t.addEventListener('scroll',()=>{p.scrollTop=t.scrollTop;p.scrollLeft=t.scrollLeft;});
  });
  repRefreshHL();
  repWireEncodeCtx();
}
async function repEncodeSel(el,op){
  const a=el.selectionStart,b=el.selectionEnd,s=el.value.substring(a,b);
  if(!s){toast('select text first');return;}
  try{
    const r=await api('/api/decode',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({op,input:s})});
    if(r.error){toast(r.error);return;}
    el.value=el.value.slice(0,a)+r.output+el.value.slice(b);
    el.selectionStart=a;el.selectionEnd=a+r.output.length;
    repSaveEditor();repRefreshHL();repPersistDebounced();
  }catch(e){toast(e.message);}
}
function repShowEncodeCtx(e,el){
  const a=el.selectionStart,b=el.selectionEnd,s=el.value.substring(a,b);
  if(!s)return;
  e.preventDefault();
  const short=s.length>28?s.slice(0,28)+'…':s;
  const items=DEC_OPS.filter(([op])=>op!=='jwtdecode'&&op!=='smart')
    .map(([op,label])=>({label,val:label,act:()=>repEncodeSel(el,op)}));
  openCtxMenu(e.clientX,e.clientY,[{head:'ENCODE · '+short,items}]);
}
function repWireEncodeCtx(){
  ['#repUrl','#repHeaders','#repBody'].forEach(sel=>{
    const el=$(sel);if(!el)return;
    el.addEventListener('contextmenu',e=>repShowEncodeCtx(e,el));
  });
}
$('#repSend').onclick=repSend;
$('#repReqSeg')&&$('#repReqSeg').querySelectorAll('button').forEach(b=>b.onclick=async()=>{
  const t=repCur();if(!t)return;
  const next=b.dataset.view;
  if(next===(t.reqView||'raw'))return;
  repSaveEditor();
  if((t.reqView||'raw')==='pretty'&&next==='raw')t.body=compactBody(t.body);
  if((t.reqView||'raw')==='decoded'&&next!=='decoded'){
    // leave decoded — wire body stays in t.body/rawBody
    if(t.rawBody)t.body=t.rawBody;
  }
  t.reqView=next;
  repSyncReqSeg(next);
  if(next==='decoded'){
    const ok=await repEnterDecoded(t);
    if(!ok){$('#repBody').value=repBodyForDisplay(t.body,'pretty');}
  }else{
    $('#repBody').value=repBodyForDisplay(t.body,next);
    repCodecBadge(t);
  }
  repRefreshHL();
  repPersistDebounced();
});
$('#repResSeg').querySelectorAll('button').forEach(b=>b.onclick=()=>{const t=repCur();if(t)t.resView=b.dataset.view;$('#repResSeg').querySelectorAll('button').forEach(x=>{x.classList.toggle('on',x===b);x.setAttribute('aria-pressed',x===b?'true':'false');});renderRepResponse();});

/* ---- intruder ---- */
// Per-position colours tie each §-marker to its payload list (cycle if > 6 markers).
const POS_COLORS=['var(--accent)','var(--blue)','var(--amber)','var(--violet)','var(--cyan)','var(--red)'];
const INTR_TPL='POST /login HTTP/1.1\nHost: example.com\nContent-Type: application/json\n\n{"user":"§admin§","pass":"§password§"}';
const INTR_SNIPER="admin\nadministrator\nroot\n' OR 1=1--\n../../../etc/passwd";
const INTR_POS=["admin\nadministrator\nroot","password\n123456\nchangeme"];
// INTR_RESULTS_EMPTY — the idle state of #intrResults before the first attack.
const INTR_RESULTS_EMPTY='<div class="state-empty"><div class="state-empty-icon">🎯</div><div class="state-empty-title">No results yet</div><p class="state-empty-hint">Set a target, mark <b>§</b> injection points, add payloads, then <b>Start</b>.</p></div>';
// Live editing mirror of the active tab's mode + payload lists (the other fields —
// target/template/threads/delay/repeat — live in the DOM and are snapshotted to tabs).
const INTR_NUM_DEFAULT=()=>({start:1,end:100,step:1,mode:'sequence',count:100,pad:0,unique:false});
export const intrState={type:'sniper',sniper:INTR_SNIPER,pos:INTR_POS.slice(),sniperLines:null,posLines:[],sniperFile:null,posFiles:[],sniperSource:'list',sniperNums:INTR_NUM_DEFAULT(),posSources:[],posNums:[]};
let intrLastFlowId=null; // last flow loaded into Intruder (for ✨ Generate)
export function lines(s){return s.split('\n').map(x=>x.trim()).filter(Boolean);}

// expandNumbers — client-side Numbers payload source (#15). Mirrors
// internal/intruder.ExpandNumbers (sequence + random, optional pad/unique).
export function expandNumbers(cfg, maxN=2000){
  const c=cfg||{};
  const start=Number(c.start), end=Number(c.end);
  let step=Number(c.step);
  const mode=(c.mode||'sequence')==='random'?'random':'sequence';
  const pad=Math.max(0,parseInt(c.pad,10)||0);
  const fmt=n=>{
    let s=String(n);
    if(pad>0){
      const neg=n<0; if(neg) s=String(-n);
      while(s.length<pad) s='0'+s;
      if(neg) s='-'+s;
    }
    return s;
  };
  if(mode==='sequence'){
    if(!Number.isFinite(start)||!Number.isFinite(end)) return [];
    if(!step) step=start>end?-1:1;
    if(step===0) return [];
    if((step>0&&start>end)||(step<0&&start<end)) return [];
    const out=[];
    for(let n=start;;){
      out.push(fmt(n));
      if(out.length>=maxN) break;
      const next=n+step;
      if(step>0?next>end:next<end) break;
      n=next;
    }
    return out;
  }
  let count=Math.max(0,parseInt(c.count,10)||0);
  if(!count||!Number.isFinite(start)||!Number.isFinite(end)) return [];
  if(count>maxN) count=maxN;
  let lo=start,hi=end; if(lo>hi){const t=lo;lo=hi;hi=t;}
  let lattice=null;
  if(step){
    const abs=Math.abs(step); lattice=[];
    for(let n=lo;n<=hi;n+=abs){lattice.push(n);if(lattice.length>=maxN)break;}
  }
  const out=[];
  if(c.unique){
    if(!lattice){
      const span=Math.min(hi-lo+1,maxN);
      lattice=Array.from({length:span},(_,i)=>lo+i);
    }
    const pool=lattice.slice();
    const n=Math.min(count,pool.length);
    for(let i=0;i<n;i++){
      const j=i+Math.floor(Math.random()*(pool.length-i));
      const t=pool[i];pool[i]=pool[j];pool[j]=t;
      out.push(fmt(pool[i]));
    }
    return out;
  }
  for(let i=0;i<count;i++){
    let v;
    if(lattice&&lattice.length) v=lattice[Math.floor(Math.random()*lattice.length)];
    else v=lo+Math.floor(Math.random()*((hi-lo)+1));
    out.push(fmt(v));
  }
  return out;
}
function intrNumsFor(slot){
  if(slot==='s') return intrState.sniperNums||INTR_NUM_DEFAULT();
  const i=Number(slot);
  return (intrState.posNums&&intrState.posNums[i])||INTR_NUM_DEFAULT();
}
function intrSourceFor(slot){
  if(slot==='s') return intrState.sniperSource||'list';
  return (intrState.posSources&&intrState.posSources[Number(slot)])||'list';
}
function intrGetPayloadLines(slot){
  if(intrSourceFor(slot)==='numbers') return expandNumbers(intrNumsFor(slot));
  if(slot==='s') return intrState.sniperLines||lines(intrState.sniper||'');
  const i=Number(slot);
  if(intrState.posLines?.[i]) return intrState.posLines[i];
  return lines(intrState.pos[i]||'');
}
function intrPayloadTruncated(slot){
  if(slot==='s') return !!intrState.sniperLines;
  return !!intrState.posLines?.[Number(slot)];
}
function intrPayloadNote(slot){
  if(!intrPayloadTruncated(slot)) return '';
  const n=intrGetPayloadLines(slot).length;
  const f=slot==='s'?intrState.sniperFile:intrState.posFiles?.[Number(slot)];
  return `Showing first ${LIST_PREVIEW_LINES} of ${n.toLocaleString()} payloads${f?' from '+f:''} — full list is kept for the attack but not rendered here.`;
}
function intrSetPayloadLines(slot, arr, fileName){
  const prev=previewListLines(arr, LIST_PREVIEW_LINES);
  if(slot==='s'){
    if(prev.truncated){intrState.sniperLines=arr;intrState.sniper=prev.text;intrState.sniperFile=fileName||null;}
    else{intrState.sniperLines=null;intrState.sniperFile=null;intrState.sniper=arr.join('\n');}
    return;
  }
  const i=Number(slot);
  while(intrState.pos.length<=i) intrState.pos.push('');
  if(!intrState.posLines) intrState.posLines=[];
  while(intrState.posLines.length<=i) intrState.posLines.push(null);
  if(!intrState.posFiles) intrState.posFiles=[];
  while(intrState.posFiles.length<=i) intrState.posFiles.push(null);
  if(prev.truncated){intrState.posLines[i]=arr;intrState.pos[i]=prev.text;intrState.posFiles[i]=fileName||null;}
  else{intrState.posLines[i]=null;intrState.posFiles[i]=null;intrState.pos[i]=arr.join('\n');}
}
function intrClearPayloadLines(slot){
  if(slot==='s'){intrState.sniperLines=null;intrState.sniperFile=null;return;}
  const i=Number(slot);
  if(intrState.posLines) intrState.posLines[i]=null;
  if(intrState.posFiles) intrState.posFiles[i]=null;
}
function intrMarkers(){return (($('#intrTemplate').value||'').match(/§[^§]*§/g)||[]).map(s=>s.slice(1,-1));}

/* ---- intruder tabs: each is a full saved attack config (mirrors Repeater) ---- */
function intrBlank(seq){return {tid:seq,target:'',template:INTR_TPL,type:'sniper',threads:1,delay:0,repeat:20,sniper:INTR_SNIPER,pos:INTR_POS.slice(),sniperLines:null,posLines:[],sniperFile:null,posFiles:[],sniperSource:'list',sniperNums:INTR_NUM_DEFAULT(),posSources:[],posNums:[],grep:'',extract:'',proc:''};}
function intrTypeLabel(t){return t==='repeat'?'repeat':(t||'sniper');}
function intrTitle(t){if(!t)return 'new attack';let h='';try{h=new URL(t.target).host;}catch(e){h=(t.target||'').replace(/^https?:\/\//,'');}return intrTypeLabel(t.type)+(h?' · '+h:' attack');}
function intrReadEditor(){return {target:$('#intrTarget').value,template:$('#intrTemplate').value,
  threads:parseInt($('#intrThreads').value,10)||1,delay:parseInt($('#intrDelay').value,10)||0,repeat:parseInt($('#intrRepeat').value,10)||20,
  grep:$('#intrGrep').value,extract:$('#intrExtract').value,proc:$('#intrProc').value,
  type:intrState.type,sniper:intrState.sniper,pos:intrState.pos.slice(),
  sniperLines:intrState.sniperLines,posLines:intrState.posLines?.slice()||[],sniperFile:intrState.sniperFile,posFiles:intrState.posFiles?.slice()||[],
  sniperSource:intrState.sniperSource||'list',sniperNums:{...(intrState.sniperNums||INTR_NUM_DEFAULT())},
  posSources:(intrState.posSources||[]).slice(),posNums:(intrState.posNums||[]).map(n=>({...(n||INTR_NUM_DEFAULT())}))};}
function intrSaveCur(){const t=intrTabs.cur();if(t)Object.assign(t,intrReadEditor());}
function intrApply(t){if(!t)return;
  $('#intrTarget').value=t.target||'';$('#intrTemplate').value=t.template||'';
  $('#intrThreads').value=t.threads||1;$('#intrDelay').value=t.delay||0;$('#intrRepeat').value=t.repeat||20;
  $('#intrGrep').value=t.grep||'';$('#intrExtract').value=t.extract||'';$('#intrProc').value=t.proc||'';
  intrState.type=t.type||'sniper';intrState.sniper=t.sniper||'';intrState.pos=Array.isArray(t.pos)?t.pos.slice():[];
  intrState.sniperLines=t.sniperLines||null;intrState.posLines=Array.isArray(t.posLines)?t.posLines.slice():[];
  intrState.sniperFile=t.sniperFile||null;intrState.posFiles=Array.isArray(t.posFiles)?t.posFiles.slice():[];
  intrState.sniperSource=t.sniperSource||'list';intrState.sniperNums={...(t.sniperNums||INTR_NUM_DEFAULT())};
  intrState.posSources=Array.isArray(t.posSources)?t.posSources.slice():[];
  intrState.posNums=Array.isArray(t.posNums)?t.posNums.map(n=>({...(n||INTR_NUM_DEFAULT())})):[];
  updateIntrMode();}
function intrTabForStorage(t){
  const o={...t};
  if(o.sniperLines?.length>500){o.sniperLarge=true;o.sniperCount=o.sniperLines.length;delete o.sniperLines;}
  if(o.posLines?.length){
    o.posCounts=o.posLines.map(a=>a?.length||0);
    o.posLines=o.posLines.map(a=>(a&&a.length<=500)?a:null);
  }
  return o;
}
// intrTabs — project-scoped (`intr.tabs.<project>`) so attack configs do not
// leak across projects (#18). Legacy unscoped key migrates once.
const intrTabs=createTabManager({
  storageKey:()=>projectStorageKey('intr.tabs'),
  blank:intrBlank,
  title:intrTitle,
  onSave:()=>intrSaveCur(),
  onLoad:t=>intrApply(t),
  normalize:t=>({tid:t.tid,target:t.target||'',template:t.template||INTR_TPL,type:t.type||'sniper',threads:t.threads||1,delay:t.delay||0,repeat:t.repeat||20,sniper:t.sniper||'',pos:Array.isArray(t.pos)?t.pos:[],sniperLines:t.sniperLines||null,posLines:Array.isArray(t.posLines)?t.posLines:[],sniperFile:t.sniperFile||null,posFiles:Array.isArray(t.posFiles)?t.posFiles:[],sniperLarge:!!t.sniperLarge,sniperCount:t.sniperCount||0,posCounts:Array.isArray(t.posCounts)?t.posCounts:[],sniperSource:t.sniperSource||'list',sniperNums:{...(t.sniperNums||INTR_NUM_DEFAULT())},posSources:Array.isArray(t.posSources)?t.posSources:[],posNums:Array.isArray(t.posNums)?t.posNums.map(n=>({...(n||INTR_NUM_DEFAULT())})):[],grep:t.grep||'',extract:t.extract||'',proc:t.proc||''}),
  serialize:intrTabForStorage,
  onPersist:blob=>persistUIState('intruder',blob),
});
function intrTouch(){intrSaveCur();renderIntrTabs();intrTabs.persistDebounced();} // save editor → active tab
function renderIntrTabs(){intrTabs.render('#intrTabs');}
export async function intrInit(){
  if(intrInit._done)return; intrInit._done=true;
  await hydrateUIState('intruder','intr.tabs');
  await hydrateIntrPresets();
  intrTabs.init('#intrTabs');
  if(intrTabs.tabs.length) intrTabs.persist();
  renderIntrHistory();loadIntrPresets();
  $('#intrTarget')&&$('#intrTarget').addEventListener('input',intrTouch);
  $('#intrTemplate')&&$('#intrTemplate').addEventListener('input',()=>{intrTemplateChanged();intrTouch();});
  ['#intrThreads','#intrDelay','#intrRepeat'].forEach(s=>{const el=$(s);if(el)el.addEventListener('input',()=>{if(intrState.type==='repeat')renderPayloadInputs();else updateIntrCount();intrTouch();});});
  ['#intrGrep','#intrExtract','#intrProc'].forEach(s=>{const el=$(s);if(el)el.addEventListener('input',intrTouch);});
  const gen=$('#intrAiGen');if(gen)gen.onclick=()=>intrGeneratePayloads();
}

/* ---- intruder run history (this session) ---- */
const intrHistory=[]; let intrCapturePending=false, intrRunCfg=null;
function renderIntrHistory(){
  const box=$('#intrHistory'),tg=$('#intrHistToggle');
  if(tg)tg.textContent='⟲ History'+(intrHistory.length?' ('+intrHistory.length+')':'');
  if(!box)return;
  if(!intrHistory.length){box.innerHTML='<div class="hint" style="padding:10px">No attacks yet this session.</div>';return;}
  box.innerHTML=intrHistory.map((h,i)=>`<div class="h" data-i="${i}" title="re-open this run + its config"><div><span style="font-weight:700;text-transform:capitalize">${esc(intrTypeLabel(h.type))}</span> <span style="color:var(--fg3)">${h.total} req${h.flagged?' · <span style="color:var(--accent)">'+h.flagged+'⚑</span>':''}</span></div><div class="u">${esc(h.target||'')}</div></div>`).join('');
  box.querySelectorAll('.h').forEach(el=>{el.onclick=()=>intrLoadHistory(Number(el.dataset.i));wireRowKey(el,()=>intrLoadHistory(Number(el.dataset.i)));});
}
function intrLoadHistory(i){
  const h=intrHistory[i];if(!h)return;
  if(h.cfg){
    intrState.type=h.cfg.type;intrState.sniper=h.cfg.sniper;intrState.pos=(h.cfg.pos||[]).slice();
    intrState.sniperLines=h.cfg.sniperLines||null;intrState.posLines=(h.cfg.posLines||[]).slice();
    intrState.sniperFile=h.cfg.sniperFile||null;intrState.posFiles=(h.cfg.posFiles||[]).slice();
    $('#intrTarget').value=h.cfg.target||'';$('#intrTemplate').value=h.cfg.template||'';$('#intrThreads').value=h.cfg.threads||1;$('#intrDelay').value=h.cfg.delay||0;$('#intrRepeat').value=h.cfg.repeat||20;
    updateIntrMode();intrTouch();}
  renderIntr({running:false,total:h.total,done:h.total,results:h.results,capped:h.capped});
}
$('#intrHistToggle')&&($('#intrHistToggle').onclick=()=>{const h=$('#intrHistory');if(h)h.style.display=(h.style.display==='none'?'':'none');});

function intrModeText(){
  if(intrState.type==='repeat')
    return 'Null — resend the template verbatim with no payloads or § markers. Use for duplicate submits, idempotency checks, rate limits, or concurrent replays (raise threads, 0 ms delay).';
  if(intrState.type==='battering')
    return 'Battering ram — one payload list applied to every § marker at once (same value in all positions). Good for hitting every field with the same token.';
  if(intrState.type==='cluster')
    return 'Cluster bomb — one payload list per § marker; every combination is tried (cartesian product). Mark N points → fill N lists.';
  if(intrState.type==='pitchfork')
    return 'Pitchfork — one payload list per § marker (colour-matched below). Lists advance together, so mark N injection points → fill N lists; fires min(list lengths) requests. Load each list from a file with 📂 / ＋.';
  return 'Sniper — a single payload list, tried at each § marker one position at a time (the others keep their original value). Load payloads from a file with 📂 / ＋.';
}
const INTR_FILE_BTNS=`<div class="spacer"></div><button type="button" class="btn intr-file-load" data-mode="replace" title="Load payloads from file">📂</button><button type="button" class="btn intr-file-load" data-mode="append" title="Append payloads from file">＋</button>`;
async function intrLoadPayloadFile(ta, append){
  try{
    const got=await pickTextFile();
    if(!got||!ta) return;
    const p=ta.dataset.pos;
    const incoming=parseListLines(got.text);
    const merged=append?[...intrGetPayloadLines(p),...incoming]:incoming;
    intrSetPayloadLines(p, merged, got.name);
    ta.value=p==='s'?intrState.sniper:(intrState.pos[Number(p)]||'');
    ta.readOnly=intrPayloadTruncated(p);
    const note=ta.closest('.intr-pl')?.querySelector('.intr-pl-note');
    if(note) note.textContent=intrPayloadNote(p);
    updateIntrCount();
    intrTouch();
    toast((append?'appended ':'loaded ')+merged.length.toLocaleString()+' payload'+(merged.length===1?'':'s')+' from '+got.name+(merged.length>LIST_PREVIEW_LINES?' (preview only in editor)':''));
  }catch(e){ toast(e.message); }
}
function wireIntrPayloadFileButtons(wrap){
  if(!wrap) return;
  wrap.querySelectorAll('.intr-file-load').forEach(btn=>btn.onclick=e=>{
    e.stopPropagation();
    const ta=btn.closest('.intr-pl')?.querySelector('textarea');
    if(ta) intrLoadPayloadFile(ta, btn.dataset.mode==='append');
  });
}
function intrSourceToggleHTML(slot){
  const src=intrSourceFor(slot);
  return `<span class="intr-src-seg" data-slot="${escAttr(String(slot))}"><button type="button" class="btn${src==='list'?' on':''}" data-src="list" title="Line list / file">List</button><button type="button" class="btn${src==='numbers'?' on':''}" data-src="numbers" title="Numeric range">Numbers</button></span>`;
}
function intrNumbersHTML(slot){
  const n=intrNumsFor(slot);
  const rand=n.mode==='random';
  return `<div class="intr-nums" data-pos="${escAttr(String(slot))}">
    <label>Start <input type="number" data-k="start" value="${escAttr(String(n.start))}"></label>
    <label>End <input type="number" data-k="end" value="${escAttr(String(n.end))}"></label>
    <label>Step <input type="number" data-k="step" value="${escAttr(String(n.step))}" ${rand?'disabled':''}></label>
    <label>Mode <select data-k="mode"><option value="sequence"${!rand?' selected':''}>sequence</option><option value="random"${rand?' selected':''}>random</option></select></label>
    <label class="intr-num-count"${rand?'':' style="display:none"'}>How many <input type="number" data-k="count" min="1" value="${escAttr(String(n.count||100))}"></label>
    <label>Pad <input type="number" data-k="pad" min="0" value="${escAttr(String(n.pad||0))}" title="Zero-pad width (0 = none)"></label>
    <label class="intr-num-unique"${rand?'':' style="display:none"'}><input type="checkbox" data-k="unique"${n.unique?' checked':''}> unique</label>
    <span class="hint intr-num-preview"></span>
  </div>`;
}
function setIntrSource(slot, src){
  if(slot==='s'){intrState.sniperSource=src;return;}
  const i=Number(slot);
  if(!intrState.posSources) intrState.posSources=[];
  while(intrState.posSources.length<=i) intrState.posSources.push('list');
  intrState.posSources[i]=src;
}
function setIntrNums(slot, patch){
  if(slot==='s'){intrState.sniperNums={...(intrState.sniperNums||INTR_NUM_DEFAULT()),...patch};return;}
  const i=Number(slot);
  if(!intrState.posNums) intrState.posNums=[];
  while(intrState.posNums.length<=i) intrState.posNums.push(INTR_NUM_DEFAULT());
  intrState.posNums[i]={...(intrState.posNums[i]||INTR_NUM_DEFAULT()),...patch};
}
function wireIntrNumbers(wrap){
  wrap.querySelectorAll('.intr-nums').forEach(box=>{
    const slot=box.dataset.pos;
    const refresh=()=>{
      const prev=box.querySelector('.intr-num-preview');
      const arr=expandNumbers(intrNumsFor(slot));
      if(prev) prev.textContent=arr.length?`will send ${arr.length.toLocaleString()} payload${arr.length===1?'':'s'}`:'no values';
      const rand=intrNumsFor(slot).mode==='random';
      const countEl=box.querySelector('.intr-num-count');
      const uniqEl=box.querySelector('.intr-num-unique');
      const stepEl=box.querySelector('input[data-k="step"]');
      if(countEl) countEl.style.display=rand?'':'none';
      if(uniqEl) uniqEl.style.display=rand?'':'none';
      if(stepEl) stepEl.disabled=rand&&!intrNumsFor(slot).step;
      updateIntrCount();
    };
    box.querySelectorAll('input,select').forEach(el=>{
      el.addEventListener('input',()=>{
        const k=el.dataset.k; if(!k) return;
        if(k==='unique') setIntrNums(slot,{unique:!!el.checked});
        else if(k==='mode') setIntrNums(slot,{mode:el.value});
        else setIntrNums(slot,{[k]:el.type==='number'?Number(el.value):el.value});
        refresh();intrTouch();
      });
      el.addEventListener('change',()=>{ /* same as input for select */ });
    });
    refresh();
  });
  wrap.querySelectorAll('.intr-src-seg').forEach(seg=>{
    const slot=seg.dataset.slot;
    seg.querySelectorAll('button').forEach(btn=>btn.onclick=()=>{
      setIntrSource(slot, btn.dataset.src);
      renderPayloadInputs();intrTouch();
    });
  });
}
// Build the payload inputs: one shared list for Sniper, one colour-coded list per
// marker for Pitchfork. Values persist in intrState across re-renders.
function renderPayloadInputs(){
  const wrap=$('#intrPayloadsWrap');if(!wrap)return;
  if(intrState.type==='repeat'){
    wrap.innerHTML='<div class="hint">Null mode — no payload lists. The request above is sent verbatim <b>×'+(parseInt($('#intrRepeat').value,10)||0)+'</b> times across <b>'+(parseInt($('#intrThreads').value,10)||1)+'</b> threads.</div>';
    updateIntrCount();return;
  }
  if(intrState.type!=='pitchfork'&&intrState.type!=='cluster'){
    const src=intrSourceFor('s');
    wrap.innerHTML=`<div class="intr-pl"><div class="intr-pl-h"><span class="sw" style="background:var(--accent)"></span>ALL § POSITIONS${intrSourceToggleHTML('s')}${src==='list'?INTR_FILE_BTNS:''}</div><div class="intr-pl-note hint"></div>${src==='numbers'?intrNumbersHTML('s'):'<textarea class="rep-edit" data-pos="s" spellcheck="false" placeholder="one payload per line"></textarea>'}</div>`;
  }else{
    const mk=intrMarkers();
    if(!mk.length){wrap.innerHTML='<div class="hint">Mark injection points with <b>§…§</b> in the template (select text → <b>§ Mark</b>). Each marker gets its own colour-matched payload list here.</div>';updateIntrCount();return;}
    wrap.innerHTML=mk.map((content,i)=>{const c=POS_COLORS[i%POS_COLORS.length];const src=intrSourceFor(i);
      return `<div class="intr-pl">
        <div class="intr-pl-h" title="payloads for the ${ordinal(i+1)} § marker${content?' (currently '+escAttr(content)+')':''}"><span class="sw" style="background:${c}"></span>§${i+1}${content?' · '+esc(content):''}${intrSourceToggleHTML(i)}${src==='list'?INTR_FILE_BTNS:''}</div>
        <div class="intr-pl-note hint"></div>
        ${src==='numbers'?intrNumbersHTML(i):`<textarea class="rep-edit" data-pos="${i}" spellcheck="false" placeholder="payloads for §${i+1}"></textarea>`}</div>`;}).join('');
  }
  wrap.querySelectorAll('textarea').forEach(ta=>{
    const p=ta.dataset.pos;
    ta.value=p==='s'?(intrState.sniper||''):(intrState.pos[Number(p)]||'');
    ta.readOnly=intrPayloadTruncated(p);
    const note=ta.closest('.intr-pl')?.querySelector('.intr-pl-note');
    if(note){
      note.textContent=intrPayloadNote(p);
      const tab=intrTabs.cur();
      if(p==='s'&&tab?.sniperLarge&&!intrState.sniperLines){
        note.textContent=(tab.sniperCount?tab.sniperCount.toLocaleString()+' payloads were':'Large payload list was')+' not restored after reload — load the file again with 📂.';
      }else if(p!=='s'&&tab?.posCounts?.[Number(p)]&&!intrState.posLines?.[Number(p)]){
        note.textContent=`${tab.posCounts[Number(p)].toLocaleString()} payloads were not restored after reload — load the file again with 📂.`;
      }
    }
    ta.addEventListener('input',()=>{
      if(ta.readOnly) return;
      const arr=parseListLines(ta.value);
      if(arr.length>LIST_PREVIEW_LINES){
        intrSetPayloadLines(p, arr, null);
        ta.value=p==='s'?intrState.sniper:(intrState.pos[Number(p)]||'');
        ta.readOnly=true;
        if(note) note.textContent=intrPayloadNote(p);
      }else{
        intrClearPayloadLines(p);
        if(p==='s') intrState.sniper=ta.value; else intrState.pos[Number(p)]=ta.value;
        if(note) note.textContent='';
      }
      updateIntrCount();intrTouch();
    });
  });
  wireIntrPayloadFileButtons(wrap);
  wireIntrNumbers(wrap);
  updateIntrCount();
}
async function intrLoadTemplateFile(){
  try{
    const got=await pickTextFile({accept:'.txt,.http,.req,text/plain'});
    if(!got) return;
    $('#intrTemplate').value=normalizeListText(got.text);
    intrTemplateChanged();
    intrTouch();
    toast('loaded template from '+got.name);
  }catch(e){toast(e.message);}
}
if($('#intrTplLoad'))$('#intrTplLoad').onclick=intrLoadTemplateFile;
function ordinal(n){return n+({1:'st',2:'nd',3:'rd'}[n%10>3||(n%100>=11&&n%100<=13)?0:n%10]||'th');}
// Live payload/request count on the PAYLOADS header + attack bar.
function updateIntrCount(){
  const hint=$('#intrPayHint'),cnt=$('#intrCount'),mk=intrMarkers();
  if(intrState.type==='repeat'){
    const n=parseInt($('#intrRepeat').value,10)||0;
    if(hint)hint.textContent='no payloads — verbatim resend';
    if(cnt)cnt.textContent=`${n} send${n===1?'':'s'}`;
    return;
  }
  if(intrState.type==='pitchfork'){
    const counts=mk.map((_,i)=>intrGetPayloadLines(i).length);
    const reqs=counts.length?Math.min(...counts):0;
    if(hint)hint.textContent=counts.length?counts.map((n,i)=>`§${i+1}:${n.toLocaleString()}`).join(' · '):'no § markers yet';
    if(cnt)cnt.textContent=mk.length?`${reqs.toLocaleString()} request${reqs===1?'':'s'}`:'mark § first';
  }else if(intrState.type==='cluster'){
    const counts=mk.map((_,i)=>intrGetPayloadLines(i).length);
    const reqs=counts.length?counts.reduce((a,b)=>a*b,1):0;
    if(hint)hint.textContent=counts.length?counts.map((n,i)=>`§${i+1}:${n.toLocaleString()}`).join(' · '):'no § markers yet';
    if(cnt)cnt.textContent=mk.length?`${reqs.toLocaleString()} request${reqs===1?'':'s'}`:'mark § first';
  }else{
    const n=intrGetPayloadLines('s').length,P=mk.length,reqs=n*Math.max(P,1);
    if(hint)hint.textContent=`${n.toLocaleString()} payload${n===1?'':'s'}`+(P>1?` × ${P} § positions`:'');
    if(cnt)cnt.textContent=P?`${reqs.toLocaleString()} request${reqs===1?'':'s'}`:`${n.toLocaleString()} payloads · mark § first`;
  }
}
// The 5 attack types are presented as 3 primary modes — Sniper / Lists / Repeat —
// with the list-combination (Battering / Pitchfork / Cluster) chosen by a sub-
// select that appears under "Lists". intrState.type still holds one of the 5.
const LIST_TYPES=['battering','pitchfork','cluster'];
function intrPrimary(){return intrState.type==='sniper'?'sniper':intrState.type==='repeat'?'repeat':'__lists__';}
function updateIntrMode(){
  const repeat=intrState.type==='repeat';
  const primary=intrPrimary();
  $('#intrType').querySelectorAll('button').forEach(x=>{const on=x.dataset.t===primary;x.classList.toggle('on',on);x.setAttribute('aria-pressed',on?'true':'false');});
  const lm=document.getElementById('intrListMode');
  if(lm){
    const isList=primary==='__lists__';
    lm.style.display=isList?'':'none';
    if(isList&&LIST_TYPES.includes(intrState.type))lm.value=intrState.type;
  }
  const h=$('#intrHint');if(h)h.textContent=intrModeText();
  const rw=$('#intrRepeatWrap');if(rw)rw.style.display=repeat?'inline-flex':'none'; // "× N sends" only in Race
  const mk=$('#intrWrap');if(mk)mk.style.opacity=repeat?'.4':''; // § markers irrelevant in Race
  renderPayloadInputs();
}
$('#intrType').querySelectorAll('button').forEach(b=>b.onclick=()=>{
  const t=b.dataset.t;
  if(t==='__lists__'){if(!LIST_TYPES.includes(intrState.type))intrState.type='cluster';}
  else intrState.type=t;
  updateIntrMode();intrTouch();
});
const _intrListMode=document.getElementById('intrListMode');
if(_intrListMode)_intrListMode.onchange=()=>{intrState.type=_intrListMode.value;updateIntrMode();intrTouch();};
$('#intrWrap').onclick=()=>{const ta=$('#intrTemplate');const a=ta.selectionStart,b=ta.selectionEnd,v=ta.value;ta.value=v.slice(0,a)+'§'+v.slice(a,b)+'§'+v.slice(b);ta.focus();ta.selectionStart=a+1;ta.selectionEnd=b+1;intrTemplateChanged();intrTouch();};
// Re-derive the per-marker inputs whenever the template's § markers change. (Input
// listeners for the editor fields are wired in intrInit so they also save to the tab.)
function intrTemplateChanged(){if(intrState.type==='pitchfork'||intrState.type==='cluster')renderPayloadInputs();else updateIntrCount();}
// setSniperPayloads: used by the AI assistant's "load into Intruder" action.
export function setSniperPayloads(text){intrState.type='sniper';intrSetPayloadLines('s', parseListLines(text||''), null);updateIntrMode();intrTouch();}
const INTR_MAX_REQUESTS=2000;
export async function intrStart(){
  const target=$('#intrTarget').value.trim();
  if(!target){toast('enter a target (scheme://host)');$('#intrTarget').focus();return;}
  const threads=Math.max(1,parseInt($('#intrThreads').value,10)||1);
  const delayMs=Math.max(0,parseInt($('#intrDelay').value,10)||0);
  const body={target,template:$('#intrTemplate').value,attackType:intrState.type,threads,delayMs,
    grepMatch:$('#intrGrep').value.trim(),grepExtract:$('#intrExtract').value.trim(),
    processRules:lines($('#intrProc').value.replace(/,/g,'\n')).map(s=>s.trim()).filter(Boolean)};
  if(intrState.type==='repeat'){
    body.repeat=Math.max(1,parseInt($('#intrRepeat').value,10)||1);
  }else{
    const mk=intrMarkers();
    if(!mk.length){toast('mark at least one § injection point — or use Null mode for payload-free resends');$('#intrTemplate').focus();return;}
    if(intrState.type==='pitchfork'||intrState.type==='cluster'){
      body.payloads=mk.map((_,i)=>intrGetPayloadLines(i));
      if(body.payloads.some(l=>!l.length)){toast('add payloads for every § position');return;}
    }else{
      body.payloads=[intrGetPayloadLines('s')];
      if(!body.payloads[0].length){toast('add at least one payload');return;}
    }
    // Refuse oversize Numbers/list combinations before the server caps them.
    let reqs=0;
    if(intrState.type==='pitchfork') reqs=Math.min(...body.payloads.map(l=>l.length));
    else if(intrState.type==='cluster') reqs=body.payloads.reduce((a,l)=>a*l.length,1);
    else reqs=body.payloads[0].length*Math.max(mk.length,1);
    if(reqs>INTR_MAX_REQUESTS){toast(`too many requests (${reqs.toLocaleString()} > ${INTR_MAX_REQUESTS}) — shrink the payload range`,'error');return;}
  }
  intrTouch();                       // persist the launched config to the active tab
  intrRunCfg=intrReadEditor();       // snapshot for the history entry
  intrCapturePending=true;           // capture this run into history on completion
  try{renderIntr(await api('/api/intruder/start',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify(body)}));}
  catch(e){intrCapturePending=false;toast('attack: '+e.message);}
}
$('#intrStart').onclick=intrStart;
function intrPresetsKey(){return projectStorageKey('intruder.presets');}
async function hydrateIntrPresets(){
  try{
    const d=await api('/api/ui/intruder-presets');
    if(d&&Array.isArray(d.value)){
      try{localStorage.setItem(intrPresetsKey(),JSON.stringify(d.value));}catch(e){}
    }
  }catch(e){}
}
function loadIntrPresets(){
  const sel=$('#intrPreset');if(!sel)return;
  let list=[];try{list=JSON.parse(localStorage.getItem(intrPresetsKey())||'[]');}catch(e){}
  sel.innerHTML='<option value="">presets…</option>'+list.map((p,i)=>`<option value="${i}">${esc(p.name||'preset '+i)}</option>`).join('');
  sel.onchange=()=>{
    const i=parseInt(sel.value,10);if(isNaN(i)||!list[i])return;
    const p=list[i];
    intrState.type=p.type||'sniper';intrState.sniper=p.sniper||'';intrState.pos=(p.pos||[]).slice();
    intrState.sniperSource=p.sniperSource||'list';intrState.sniperNums={...(p.sniperNums||INTR_NUM_DEFAULT())};
    intrState.posSources=Array.isArray(p.posSources)?p.posSources.slice():[];
    intrState.posNums=Array.isArray(p.posNums)?p.posNums.map(n=>({...(n||INTR_NUM_DEFAULT())})):[];
    $('#intrTarget').value=p.target||'';$('#intrTemplate').value=p.template||'';
    $('#intrThreads').value=p.threads||1;$('#intrDelay').value=p.delay||0;$('#intrRepeat').value=p.repeat||20;
    $('#intrGrep').value=p.grep||'';$('#intrExtract').value=p.extract||'';$('#intrProc').value=p.proc||'';
    updateIntrMode();intrTouch();toast('loaded preset');
  };
}
if($('#intrPresetSave'))$('#intrPresetSave').onclick=async()=>{
  const name=await uiPrompt({title:'Save attack preset',placeholder:'preset name'});if(!name)return;
  let list=[];try{list=JSON.parse(localStorage.getItem(intrPresetsKey())||'[]');}catch(e){}
  list.unshift({name,target:$('#intrTarget').value,template:$('#intrTemplate').value,type:intrState.type,
    sniper:intrState.sniper,pos:intrState.pos.slice(),sniperSource:intrState.sniperSource,sniperNums:intrState.sniperNums,
    posSources:(intrState.posSources||[]).slice(),posNums:(intrState.posNums||[]).map(n=>({...n})),
    threads:$('#intrThreads').value,delay:$('#intrDelay').value,
    repeat:$('#intrRepeat').value,grep:$('#intrGrep').value,extract:$('#intrExtract').value,proc:$('#intrProc').value});
  if(list.length>20)list.length=20;
  try{localStorage.setItem(intrPresetsKey(),JSON.stringify(list));}catch(e){}
  persistUIState('intruder-presets', list);
  loadIntrPresets();toast('preset saved');
};
export let intrTimer=null;
let intrFilter='all', intrLastResults=[];
function intrIsInteresting(r){return !!(r&& (r.flagged||r.matched||r.anomaly));}
function intrApplyFilter(res){
  if(intrFilter==='interesting') return res.filter(intrIsInteresting);
  if(intrFilter==='error') return res.filter(r=>r.error);
  return res;
}
export function scheduleIntr(){clearTimeout(intrTimer);intrTimer=setTimeout(async()=>{try{renderIntr(await api('/api/intruder/state'));}catch(e){}},120);}
export function renderIntr(st){
  const running=!!st.running,total=st.total||0,done=st.done||0;
  $('#intrProgress').textContent=running?`running ${done}/${total}`:(total?`done ${done}/${total}${st.capped?' (capped)':''}`:'');
  $('#intrStart').disabled=running;$('#intrStart').textContent=running?'Running…':'Start ▸';
  // progress bar
  const bar=$('#intrProgBar'),fill=$('#intrProgFill');
  if(bar&&fill){bar.style.display=(running||total)?'block':'none';fill.style.width=total?Math.round(done/total*100)+'%':'0';}
  // results summary (flagged count)
  const stats=$('#intrStats'),res=st.results||[];
  intrLastResults=res.slice();
  if(stats){
    const fl=res.filter(r=>r.flagged).length, int=res.filter(intrIsInteresting).length;
    const shown=intrApplyFilter(res).length;
    stats.textContent=res.length?`${res.length} sent${fl?' · '+fl+' flagged ⚑':''}${int&&intrFilter!=='interesting'?' · '+int+' interesting':''}${intrFilter!=='all'?' · showing '+shown:''}`:'';
  }
  // capture a completed run into history (once per Start)
  if(!running&&total>0&&intrCapturePending){
    intrCapturePending=false;
    intrHistory.unshift({ts:Date.now(),target:(intrRunCfg&&intrRunCfg.target)||'',type:(intrRunCfg&&intrRunCfg.type)||intrState.type,total,flagged:res.filter(r=>r.flagged).length,results:res.slice(),capped:!!st.capped,cfg:intrRunCfg});
    if(intrHistory.length>30)intrHistory.length=30;
    renderIntrHistory();
  }
  if(running)scheduleIntr(); // self-poll until the attack converges (robust to event/POST races)
  const box=$('#intrResults');
  if(st.error){box.innerHTML='<div class="state-error"><div class="state-error-icon">⚠</div><div class="state-error-msg">'+esc(st.error)+'</div></div>';return;}
  if(!res.length){
    box.innerHTML=running?'<div class="hint" style="padding:12px">sending…</div>':INTR_RESULTS_EMPTY;
    return;
  }
  const view=intrApplyFilter(res);
  if(!view.length){
    box.innerHTML='<div class="hint" style="padding:12px">No results match this filter.</div>';
    return;
  }
  if(view.length>=INTR_VIRT_MIN) renderIntrVirtual(box,view);
  else{box.innerHTML=view.map(intrRowHTML).join('');wireIntrResultRows(box);}
}
{const seg=$('#intrResFilter');
if(seg)seg.querySelectorAll('button').forEach(b=>b.onclick=()=>{
  intrFilter=b.dataset.f||'all';
  seg.querySelectorAll('button').forEach(x=>{const on=x===b;x.classList.toggle('on',on);x.setAttribute('aria-pressed',on?'true':'false');});
  renderIntr({running:false,total:intrLastResults.length,done:intrLastResults.length,results:intrLastResults});
});}
async function intrToFinding(){
  const pool=intrApplyFilter(intrLastResults);
  const withFlow=pool.filter(r=>(r.flowId||r.flowID)>0);
  const interesting=withFlow.filter(intrIsInteresting);
  const pick=interesting.length?interesting:withFlow.slice(0,10);
  if(!pick.length){toast('no attempts with captured flows to attach','warn');return;}
  const title=await uiPrompt({title:'Create finding from Intruder',placeholder:'e.g. IDOR on /api/users?id=',value:($('#intrTarget').value||'Intruder finding').replace(/^https?:\/\//,'')});
  if(!title)return;
  try{
    const body={
      title, severity:'medium', status:'needs_verification', source:'human',
      target:$('#intrTarget').value||'',
      why:'Intruder attack produced interesting responses (flagged / matched / anomalous).',
      impact:'Confirm whether the differing responses indicate unauthorized access or injection.',
      verificationInstructions:'Open each attached PoC flow, compare status/length/body to the baseline, and confirm impact on the target.',
      flowIds:pick.map(r=>Number(r.flowId||r.flowID)).filter(Boolean).slice(0,20),
    };
    const f=await api('/api/findings',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify(body)});
    toast('finding #'+f.id+' created with '+body.flowIds.length+' PoC'+(body.flowIds.length===1?'':'s'));
    document.querySelector('.tab[data-tab="findings"]')?.click();
  }catch(e){toast(e.message||'could not create finding','error');}
}
if($('#intrToFinding'))$('#intrToFinding').onclick=intrToFinding;
// Virtualized Intruder results: rendering thousands of result rows on every poll
// (every 120ms while running) rebuilds the whole DOM and janks the tab. Render only
// the visible window, repaint on scroll — same pattern as the Map table / Proxy rows.
const INTR_ROW_H=25, INTR_VIRT_MIN=200;
function intrRowHTML(r){
  const fid=r.flowId||r.flowID||0;
  const title=r.error?(r.error):(fid?('open attempt #'+r.id+' · flow #'+fid):('attempt #'+r.id+(r.error?' · '+r.error:'')));
  return `<div class="intr-row ${r.flagged?'flag':''}${r.matched?' match':''}" data-flow="${fid||''}" data-err="${escAttr(r.error||'')}" title="${escAttr(title)}" tabindex="0" role="button">
    <div style="color:var(--fg3)">${r.id}</div>
    <div class="pl">${esc(r.payload)}${r.flagged?' ⚑':''}${r.anomaly?' <span class="intr-anomaly" title="length anomaly">∿</span>':''}${r.matched?' <span title="grep matched">✓</span>':''}${r.extracted?' <span class="ext" title="extracted">→ '+esc(r.extracted)+'</span>':''}</div>
    <div style="color:${statusColor(r.status)};font-weight:700;text-align:center">${r.error?'ERR':(r.status||'—')}</div>
    <div style="color:${r.anomaly?'var(--amber)':'var(--fg2)'};text-align:right;font-weight:${r.anomaly?'700':'400'}">${r.length}</div>
    <div style="color:var(--fg3);text-align:right">${r.timeMs}ms</div></div>`;
}
async function openIntrResult(el){
  const fid=Number(el.dataset.flow||0);
  const err=el.dataset.err||'';
  if(fid>0){
    try{
      const {flowPopup}=await import('./flowmodal.js');
      await flowPopup(fid);
    }catch(e){toast(e.message||'evidence no longer in history','warn');}
    return;
  }
  toast(err||'no captured flow for this attempt (send failed or evidence purged)','warn');
}
function wireIntrResultRows(root){
  if(!root)return;
  root.querySelectorAll('.intr-row').forEach(el=>{
    el.onclick=()=>openIntrResult(el);
    wireRowKey(el,()=>openIntrResult(el));
  });
}
function paintIntrWindow(box){
  const res=box._res||[];const st=box.scrollTop,vh=box.clientHeight||360;
  const start=Math.max(0,Math.floor(st/INTR_ROW_H)-8);
  const end=Math.min(res.length,Math.ceil((st+vh)/INTR_ROW_H)+8);
  const body=box.querySelector('.intr-virt-body');if(!body)return;
  body.style.transform=`translateY(${start*INTR_ROW_H}px)`;
  body.innerHTML=res.slice(start,end).map(intrRowHTML).join('');
  wireIntrResultRows(body);
}
function renderIntrVirtual(box,res){
  box._res=res;
  let sp=box.querySelector('.intr-virt-spacer');
  if(!sp){
    box.classList.add('intr-virt');
    box.innerHTML='<div class="intr-virt-spacer"></div><div class="intr-virt-body"></div>';
    if(!box._virtBound){
      box.addEventListener('scroll',()=>{if(box._virtQ)return;box._virtQ=true;requestAnimationFrame(()=>{box._virtQ=false;paintIntrWindow(box);});});
      box._virtBound=true;
    }
    sp=box.querySelector('.intr-virt-spacer');
  }
  sp.style.height=(res.length*INTR_ROW_H)+'px';
  paintIntrWindow(box);
}
// Apply structured AI Intruder suggestion (#16). Does not Start the attack.
export function applyIntruderPayloadSuggestion(data, opts){
  if(!data||!Array.isArray(data.positions)||!data.positions.length){toast('no payload positions in AI reply','warn');return;}
  document.querySelector('.tab[data-tab="intruder"]')?.click();
  const at=(data.attackType||'sniper').toLowerCase();
  if(['sniper','battering','pitchfork','cluster'].includes(at)) intrState.type=at;
  else intrState.type='sniper';
  if(data.template){ $('#intrTemplate').value=String(data.template).replace(/\r\n/g,'\n'); }
  const pos=data.positions;
  if(intrState.type==='pitchfork'||intrState.type==='cluster'){
    intrState.posSources=pos.map(()=>'list');
    pos.forEach((p,i)=>{
      const list=Array.isArray(p.payloads)?p.payloads.map(String):[];
      intrSetPayloadLines(i, list, null);
    });
  }else{
    intrState.sniperSource='list';
    const merged=[];
    pos.forEach(p=>{(p.payloads||[]).forEach(v=>merged.push(String(v)));});
    // Prefer first position's list for sniper (dedupe while preserving order).
    const first=pos[0].payloads||[];
    const seen=new Set(); const list=[];
    (first.length?first:merged).forEach(v=>{const s=String(v);if(!seen.has(s)){seen.add(s);list.push(s);}});
    intrSetPayloadLines('s', list, null);
    if(!data.template&&pos[0].point&&pos[0].marker!=null){
      // Best-effort: wrap the first occurrence of the marker value in §…§.
      const ta=$('#intrTemplate'); const raw=ta.value||'';
      const m=String(pos[0].marker);
      if(m&&raw.includes(m)&&!raw.includes('§')){
        ta.value=raw.replace(m,'§'+m+'§');
      }
    }
  }
  if(Array.isArray(pos[0]?.processRules)&&pos[0].processRules.length){
    $('#intrProc').value=pos[0].processRules.join(', ');
  }
  updateIntrMode();intrTouch();
  const n=pos.reduce((a,p)=>a+(p.payloads||[]).length,0);
  toast((opts&&opts.toast)||(`loaded ${n} AI payload${n===1?'':'s'} into Intruder — review & Start`));
}
export async function intrGeneratePayloads(hint){
  let flowId=intrLastFlowId;
  if(!flowId){
    // Fall back to selected History flow if the operator never used Send→Intruder.
    try{const {state}=await import('./core.js');if(state.selId)flowId=state.selId;}catch(e){}
  }
  if(!flowId){toast('load a request into Intruder (or select a History flow) first','warn');return;}
  const btn=$('#intrAiGen');if(btn){btn.disabled=true;btn.textContent='…';}
  try{
    const body={flowId};
    if(hint) body.hint=hint;
    const data=await api('/api/ai/intruder-payloads',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify(body)});
    applyIntruderPayloadSuggestion(data);
  }catch(e){toast(e.message||'AI generate failed','error');}
  finally{if(btn){btn.disabled=false;btn.textContent='✨ Generate';}}
}
export async function sendToIntruder(f){
  // Switch to the Intruder tab first for responsiveness (matches sendToRepeater),
  // and capture the active attack tab before any await so a sub-tab switch during
  // the fetch can't make intrTouch() save the request into the wrong tab.
  document.querySelector('.tab[data-tab="intruder"]').click();
  const target=intrTabs.cur();
  try{
    const [d,raw]=await Promise.all([api('/api/flows/'+f.id),api('/api/flows/'+f.id+'/raw?side=req')]);
    if(intrTabs.cur()!==target)return;
    intrLastFlowId=f.id;
    const def=(d.scheme==='https'&&d.port===443)||(d.scheme==='http'&&d.port===80);
    $('#intrTarget').value=`${d.scheme}://${d.host}${def?'':':'+d.port}`;
    $('#intrTemplate').value=raw.replace(/\r\n/g,'\n');
    updateIntrMode(); // refresh marker-derived payload inputs for the new template
    intrTouch();      // save the loaded request into the captured attack tab
    toast('loaded #'+f.id+' into Intruder · add § markers');
  }catch(e){toast(e.message);}
}
