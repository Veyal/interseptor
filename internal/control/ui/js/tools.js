import { $, esc, escAttr, toast, api, methodColor, statusColor, statusText, highlightHTTP, prettify, beautifyBody, fmtDur, fmtSize, openCtxMenu, DEC_OPS, contentTypeFromRaw, pickTextFile, applyTextList, countListLines, normalizeListText, wireRowKey } from './core.js';

// repStatusLine builds a rich response summary: "200 OK · 142 ms · 4.1 KB".
function repStatusLine(f){
  const head=f.status?f.status+' '+statusText(f.status):(f.error||'sent');
  return head+(f.durationMs?' · '+fmtDur(f.durationMs):'')+(f.resLen!=null?' · '+fmtSize(f.resLen):'');
}

/* ---- repeater (multi-tab; each tab = an endpoint with its own history) ---- */
export let repSeq=1, repTabs=[], repActive=null;
export function repBlank(){return {tid:repSeq++,title:'new tab',method:'GET',url:'',headers:'',body:'',reqView:'raw',resId:null,resView:'pretty',status:'',color:''};}
export function repCur(){return repTabs.find(t=>t.tid===repActive)||null;}
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

export function renderRepTabs(){
  const bar=$('#repTabs');if(!bar)return;
  bar.innerHTML=repTabs.map(t=>`<div class="rep-tab${t.tid===repActive?' on':''}" data-tid="${t.tid}" title="${escAttr(t.title||'new tab')}">
    <span class="rt-label" style="color:${t.tid===repActive?methodColor(t.method):'inherit'}">${esc(t.title||'new tab')}</span>
    <span class="rt-close" data-close="${t.tid}" title="close tab">✕</span></div>`).join('')
    +`<button class="rep-tab-add" id="repTabAdd" title="New tab">＋</button>`;
  bar.querySelectorAll('.rep-tab').forEach(el=>{el.onclick=e=>{if(e.target.dataset.close!=null)return;repSwitch(Number(el.dataset.tid));};wireRowKey(el,()=>repSwitch(Number(el.dataset.tid)));});
  bar.querySelectorAll('[data-close]').forEach(x=>x.onclick=e=>{e.stopPropagation();repCloseTab(Number(x.dataset.close));});
  $('#repTabAdd').onclick=()=>{repSaveEditor();repTabs.push(repBlank());repActive=repTabs[repTabs.length-1].tid;renderRepTabs();repLoadEditor();repPersist();};
}
export function repSwitch(tid){if(tid===repActive)return;repSaveEditor();repActive=tid;renderRepTabs();repLoadEditor();repPersist();}
export function repCloseTab(tid){
  const i=repTabs.findIndex(t=>t.tid===tid);if(i<0)return;
  const wasActive=tid===repActive;
  repTabs.splice(i,1);
  if(!repTabs.length)repTabs.push(repBlank());
  if(wasActive)repActive=repTabs[Math.min(i,repTabs.length-1)].tid;
  renderRepTabs();repLoadEditor();repPersist();
}
export function repSaveEditor(){const t=repCur();if(!t)return;t.method=$('#repMethod').value;t.url=$('#repUrl').value;t.headers=$('#repHeaders').value;t.body=$('#repBody').value;t.title=repTitle(t);}
export function repLoadEditor(){
  const t=repCur();if(!t)return;
  $('#repMethod').value=t.method||'GET';$('#repUrl').value=t.url||'';$('#repHeaders').value=t.headers||'';
  const rv=t.reqView||'raw';
  repSyncReqSeg(rv);
  $('#repBody').value=repBodyForDisplay(t.body,rv);
  $('#repResSeg').querySelectorAll('button').forEach(x=>{const on=x.dataset.view===(t.resView||'pretty');x.classList.toggle('on',on);x.setAttribute('aria-pressed',on?'true':'false');});
  if(t.resId){$('#repStatus').textContent=t.status||'';$('#repStatus').style.color=t.color||'var(--fg3)';renderRepResponse();}
  else{$('#repStatus').textContent='';$('#repResView').innerHTML='<span style="color:var(--fg3)">Send a request to see the response.</span>';}
  loadRepHistory();
}
export async function repSend(){
  repSaveEditor();const t=repCur();if(!t)return;
  if(!(t.url||'').trim()){toast('enter a URL');return;}
  t.body=compactBody(t.body);
  if((t.reqView||'raw')==='pretty')$('#repBody').value=repBodyForDisplay(t.body,'pretty');
  else $('#repBody').value=t.body;
  $('#repSend').textContent='Sending…';$('#repSend').disabled=true;
  $('#repStatus').textContent='sending…';$('#repStatus').style.color='var(--fg3)';
  $('#repResView').innerHTML='<span class="blink" style="color:var(--fg3)">sending…</span>';
  try{
    const flow=await api('/api/repeater/send',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({method:t.method,url:t.url.trim(),headers:t.headers,body:t.body})});
    t.resId=flow.id;t.status=repStatusLine(flow);t.color=statusColor(flow.status);
    $('#repStatus').textContent=t.status;$('#repStatus').style.color=t.color;
    if(flow.status===401) toast('401 Unauthorized — run login macro in Settings → Session or enable Re-auth on 401');
    await renderRepResponse();loadRepHistory();repPersist();
  }catch(e){$('#repStatus').textContent='';$('#repResView').textContent='(error: '+e.message+')';toast('send: '+e.message);}
  $('#repSend').textContent='Send ▸';$('#repSend').disabled=false;
}
export async function renderRepResponse(){
  const t=repCur();if(!t||!t.resId)return;
  try{const raw=await api('/api/flows/'+t.resId+'/raw?side=res');$('#repResView').innerHTML=highlightHTTP((t.resView==='pretty')?prettify(raw):raw,t.resView==='pretty',contentTypeFromRaw(raw));}
  catch(e){$('#repResView').textContent='(error: '+e.message+')';}
}
export async function loadRepHistory(){
  const box=$('#repHistory');if(!box)return;const t=repCur();const ep=repTabEndpoint(t);
  const setCount=n=>{const tg=$('#repHistToggle');if(tg)tg.textContent='⟲ History'+(n?' ('+n+')':'');};
  try{
    const d=await api('/api/repeater/history');const flows=ep?(d.flows||[]).filter(f=>repFlowEndpoint(f)===ep):[];
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
    const def=(d.scheme==='https'&&d.port===443)||(d.scheme==='http'&&d.port===80);
    t.method=d.method;t.url=`${d.scheme}://${d.host}${def?'':':'+d.port}${d.path}`;t.headers=headersToText(d.reqHeaders);
    const raw=await api('/api/flows/'+id+'/raw?side=req');const i=raw.indexOf('\r\n\r\n');t.body=i>=0?raw.slice(i+4):'';
    t.resId=id;t.status=repStatusLine(d);t.color=statusColor(d.status);t.title=repTitle(t);
    renderRepTabs();repLoadEditor();repPersist();
  }catch(e){toast(e.message);}
}
export async function sendToRepeater(f){
  document.querySelector('.tab[data-tab="repeater"]').click();
  repSaveEditor();
  const fep=repFlowEndpoint(f);
  let t=repTabs.find(x=>repTabEndpoint(x)===fep);
  if(!t){t=repBlank();repTabs.push(t);}
  repActive=t.tid;
  try{
    const d=await api('/api/flows/'+f.id);
    const def=(d.scheme==='https'&&d.port===443)||(d.scheme==='http'&&d.port===80);
    t.method=d.method;t.url=`${d.scheme}://${d.host}${def?'':':'+d.port}${d.path}`;t.headers=headersToText(d.reqHeaders);
    const raw=await api('/api/flows/'+f.id+'/raw?side=req');const i=raw.indexOf('\r\n\r\n');t.body=i>=0?raw.slice(i+4):'';
    t.resId=null;t.status='';t.color='';t.title=repTitle(t);
    renderRepTabs();repLoadEditor();repPersist();
    toast('loaded #'+f.id+' into Repeater');
  }catch(e){toast(e.message);}
}
export function repPersist(){try{localStorage.setItem('rep.tabs',JSON.stringify({seq:repSeq,active:repActive,tabs:repTabs.map(t=>({tid:t.tid,method:t.method,url:t.url,headers:t.headers,body:t.body,reqView:t.reqView||'raw',resView:t.resView}))}));}catch(e){}}
export let repPersistT=null;export function repPersistDebounced(){clearTimeout(repPersistT);repPersistT=setTimeout(repPersist,400);}
export function repInit(){
  let ok=false;
  try{const d=JSON.parse(localStorage.getItem('rep.tabs')||'null');
    if(d&&d.tabs&&d.tabs.length){
      repTabs=d.tabs.map(t=>({tid:t.tid,method:t.method||'GET',url:t.url||'',headers:t.headers||'',body:t.body||'',reqView:t.reqView||'raw',resView:t.resView||'pretty',resId:null,status:'',color:'',title:''}));
      repTabs.forEach(t=>t.title=repTitle(t));
      repActive=(d.active&&repTabs.find(x=>x.tid===d.active))?d.active:repTabs[0].tid;
      {const fin=repTabs.map(t=>t.tid).filter(Number.isFinite);repSeq=Math.max(d.seq||0,(fin.length?Math.max(...fin):0)+1);}ok=true;
    }
  }catch(e){}
  if(!ok){repTabs=[repBlank()];repActive=repTabs[0].tid;}
  renderRepTabs();repLoadEditor();
  ['#repMethod','#repUrl'].forEach(s=>{const el=$(s);if(el)el.addEventListener('input',()=>{repSaveEditor();renderRepTabs();repPersistDebounced();});});
  ['#repHeaders','#repBody'].forEach(s=>{const el=$(s);if(el)el.addEventListener('input',()=>{repSaveEditor();repPersistDebounced();});});
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
    repSaveEditor();repPersistDebounced();
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
$('#repReqSeg')&&$('#repReqSeg').querySelectorAll('button').forEach(b=>b.onclick=()=>{
  const t=repCur();if(!t)return;
  const next=b.dataset.view;
  if(next===(t.reqView||'raw'))return;
  repSaveEditor();
  if((t.reqView||'raw')==='pretty'&&next==='raw')t.body=compactBody(t.body);
  t.reqView=next;
  repSyncReqSeg(next);
  $('#repBody').value=repBodyForDisplay(t.body,next);
  repPersistDebounced();
});
$('#repResSeg').querySelectorAll('button').forEach(b=>b.onclick=()=>{const t=repCur();if(t)t.resView=b.dataset.view;$('#repResSeg').querySelectorAll('button').forEach(x=>{x.classList.toggle('on',x===b);x.setAttribute('aria-pressed',x===b?'true':'false');});renderRepResponse();});

/* ---- intruder ---- */
// Per-position colours tie each §-marker to its payload list (cycle if > 6 markers).
const POS_COLORS=['var(--accent)','var(--blue)','var(--amber)','var(--violet)','var(--cyan)','var(--red)'];
const INTR_TPL='POST /login HTTP/1.1\nHost: example.com\nContent-Type: application/json\n\n{"user":"§admin§","pass":"§password§"}';
const INTR_SNIPER="admin\nadministrator\nroot\n' OR 1=1--\n../../../etc/passwd";
const INTR_POS=["admin\nadministrator\nroot","password\n123456\nchangeme"];
// Live editing mirror of the active tab's mode + payload lists (the other fields —
// target/template/threads/delay/repeat — live in the DOM and are snapshotted to tabs).
export const intrState={type:'sniper',sniper:INTR_SNIPER,pos:INTR_POS.slice()};
export function lines(s){return s.split('\n').map(x=>x.trim()).filter(Boolean);}
function intrMarkers(){return (($('#intrTemplate').value||'').match(/§[^§]*§/g)||[]).map(s=>s.slice(1,-1));}

/* ---- intruder tabs: each is a full saved attack config (mirrors Repeater) ---- */
let intrSeq=1, intrTabs=[], intrActive=null, intrPersistT=null;
function intrBlank(){return {tid:intrSeq++,target:'',template:INTR_TPL,type:'sniper',threads:1,delay:0,repeat:20,sniper:INTR_SNIPER,pos:INTR_POS.slice(),grep:'',extract:'',proc:''};}
function intrCur(){return intrTabs.find(t=>t.tid===intrActive)||null;}
function intrTypeLabel(t){return t==='repeat'?'null':(t||'sniper');}
function intrTitle(t){if(!t)return 'new attack';let h='';try{h=new URL(t.target).host;}catch(e){h=(t.target||'').replace(/^https?:\/\//,'');}return intrTypeLabel(t.type)+(h?' · '+h:' attack');}
function intrReadEditor(){return {target:$('#intrTarget').value,template:$('#intrTemplate').value,
  threads:parseInt($('#intrThreads').value,10)||1,delay:parseInt($('#intrDelay').value,10)||0,repeat:parseInt($('#intrRepeat').value,10)||20,
  grep:$('#intrGrep').value,extract:$('#intrExtract').value,proc:$('#intrProc').value,
  type:intrState.type,sniper:intrState.sniper,pos:intrState.pos.slice()};}
function intrSaveCur(){const t=intrCur();if(t)Object.assign(t,intrReadEditor());}
function intrApply(t){if(!t)return;
  $('#intrTarget').value=t.target||'';$('#intrTemplate').value=t.template||'';
  $('#intrThreads').value=t.threads||1;$('#intrDelay').value=t.delay||0;$('#intrRepeat').value=t.repeat||20;
  $('#intrGrep').value=t.grep||'';$('#intrExtract').value=t.extract||'';$('#intrProc').value=t.proc||'';
  intrState.type=t.type||'sniper';intrState.sniper=t.sniper||'';intrState.pos=Array.isArray(t.pos)?t.pos.slice():[];
  updateIntrMode();}
function intrPersist(){try{localStorage.setItem('intr.tabs',JSON.stringify({seq:intrSeq,active:intrActive,tabs:intrTabs}));}catch(e){}}
function intrPersistDebounced(){clearTimeout(intrPersistT);intrPersistT=setTimeout(intrPersist,400);}
function intrTouch(){intrSaveCur();renderIntrTabs();intrPersistDebounced();} // save editor → active tab
function renderIntrTabs(){
  const bar=$('#intrTabs');if(!bar)return;
  bar.innerHTML=intrTabs.map(t=>`<div class="rep-tab${t.tid===intrActive?' on':''}" data-tid="${t.tid}" title="${escAttr(intrTitle(t))}"><span class="rt-label">${esc(intrTitle(t))}</span><span class="rt-close" data-close="${t.tid}" title="close tab">✕</span></div>`).join('')+`<button class="rep-tab-add" id="intrTabAdd" title="New attack">＋</button>`;
  bar.querySelectorAll('.rep-tab').forEach(el=>{el.onclick=e=>{if(e.target.dataset.close!=null)return;intrSwitch(Number(el.dataset.tid));};wireRowKey(el,()=>intrSwitch(Number(el.dataset.tid)));});
  bar.querySelectorAll('[data-close]').forEach(x=>x.onclick=e=>{e.stopPropagation();intrCloseTab(Number(x.dataset.close));});
  $('#intrTabAdd').onclick=()=>{intrSaveCur();intrTabs.push(intrBlank());intrActive=intrTabs[intrTabs.length-1].tid;renderIntrTabs();intrApply(intrCur());intrPersist();};
}
function intrSwitch(tid){if(tid===intrActive)return;intrSaveCur();intrActive=tid;renderIntrTabs();intrApply(intrCur());intrPersist();}
function intrCloseTab(tid){const i=intrTabs.findIndex(t=>t.tid===tid);if(i<0)return;const wasActive=tid===intrActive;intrTabs.splice(i,1);if(!intrTabs.length)intrTabs.push(intrBlank());if(wasActive)intrActive=intrTabs[Math.min(i,intrTabs.length-1)].tid;renderIntrTabs();if(wasActive)intrApply(intrCur());intrPersist();}
export function intrInit(){
  let ok=false;
  try{const d=JSON.parse(localStorage.getItem('intr.tabs')||'null');
    if(d&&d.tabs&&d.tabs.length){
      intrTabs=d.tabs.map(t=>({tid:t.tid,target:t.target||'',template:t.template||INTR_TPL,type:t.type||'sniper',threads:t.threads||1,delay:t.delay||0,repeat:t.repeat||20,sniper:t.sniper||'',pos:Array.isArray(t.pos)?t.pos:[],grep:t.grep||'',extract:t.extract||'',proc:t.proc||''}));
      intrActive=(d.active&&intrTabs.find(x=>x.tid===d.active))?d.active:intrTabs[0].tid;
      {const fin=intrTabs.map(t=>t.tid).filter(Number.isFinite);intrSeq=Math.max(d.seq||0,(fin.length?Math.max(...fin):0)+1);}ok=true;}
  }catch(e){}
  if(!ok){intrTabs=[intrBlank()];intrActive=intrTabs[0].tid;}
  renderIntrTabs();intrApply(intrCur());renderIntrHistory();
  $('#intrTarget')&&$('#intrTarget').addEventListener('input',intrTouch);
  $('#intrTemplate')&&$('#intrTemplate').addEventListener('input',()=>{intrTemplateChanged();intrTouch();});
  ['#intrThreads','#intrDelay','#intrRepeat'].forEach(s=>{const el=$(s);if(el)el.addEventListener('input',()=>{if(intrState.type==='repeat')renderPayloadInputs();else updateIntrCount();intrTouch();});});
  ['#intrGrep','#intrExtract','#intrProc'].forEach(s=>{const el=$(s);if(el)el.addEventListener('input',intrTouch);});
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
  if(h.cfg){intrState.type=h.cfg.type;intrState.sniper=h.cfg.sniper;intrState.pos=(h.cfg.pos||[]).slice();
    $('#intrTarget').value=h.cfg.target||'';$('#intrTemplate').value=h.cfg.template||'';$('#intrThreads').value=h.cfg.threads||1;$('#intrDelay').value=h.cfg.delay||0;$('#intrRepeat').value=h.cfg.repeat||20;
    updateIntrMode();intrTouch();}
  renderIntr({running:false,total:h.total,done:h.total,results:h.results,capped:h.capped});
}
$('#intrHistToggle')&&($('#intrHistToggle').onclick=()=>{const h=$('#intrHistory');if(h)h.style.display=(h.style.display==='none'?'':'none');});

function intrModeText(){
  if(intrState.type==='repeat')
    return 'Null — resend the template verbatim with no payloads or § markers. Use for duplicate submits, idempotency checks, rate limits, or concurrent replays (raise threads, 0 ms delay).';
  return intrState.type==='pitchfork'
    ? 'Pitchfork — one payload list per § marker (colour-matched below). Lists advance together, so mark N injection points → fill N lists; fires min(list lengths) requests. Load each list from a file with 📂 / ＋.'
    : 'Sniper — a single payload list, tried at each § marker one position at a time (the others keep their original value). Load payloads from a file with 📂 / ＋.';
}
const INTR_FILE_BTNS=`<div class="spacer"></div><button type="button" class="btn intr-file-load" data-mode="replace" title="Load payloads from file">📂</button><button type="button" class="btn intr-file-load" data-mode="append" title="Append payloads from file">＋</button>`;
async function intrLoadPayloadFile(ta, append){
  try{
    const got=await pickTextFile();
    if(!got||!ta) return;
    applyTextList(ta, got.text, {append});
    const p=ta.dataset.pos;
    if(p==='s') intrState.sniper=ta.value;
    else intrState.pos[Number(p)]=ta.value;
    updateIntrCount();
    intrTouch();
    const n=countListLines(got.text);
    toast((append?'appended ':'loaded ')+n+' payload'+(n===1?'':'s')+' from '+got.name);
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
// Build the payload inputs: one shared list for Sniper, one colour-coded list per
// marker for Pitchfork. Values persist in intrState across re-renders.
function renderPayloadInputs(){
  const wrap=$('#intrPayloadsWrap');if(!wrap)return;
  if(intrState.type==='repeat'){
    wrap.innerHTML='<div class="hint">Null mode — no payload lists. The request above is sent verbatim <b>×'+(parseInt($('#intrRepeat').value,10)||0)+'</b> times across <b>'+(parseInt($('#intrThreads').value,10)||1)+'</b> threads.</div>';
    updateIntrCount();return;
  }
  if(intrState.type!=='pitchfork'){
    wrap.innerHTML=`<div class="intr-pl"><div class="intr-pl-h"><span class="sw" style="background:var(--accent)"></span>ALL § POSITIONS${INTR_FILE_BTNS}</div><textarea class="rep-edit" data-pos="s" spellcheck="false" placeholder="one payload per line"></textarea></div>`;
  }else{
    const mk=intrMarkers();
    if(!mk.length){wrap.innerHTML='<div class="hint">Mark injection points with <b>§…§</b> in the template (select text → <b>§ Mark</b>). Each marker gets its own colour-matched payload list here.</div>';updateIntrCount();return;}
    wrap.innerHTML=mk.map((content,i)=>{const c=POS_COLORS[i%POS_COLORS.length];
      return `<div class="intr-pl" style="border-top:2px solid ${c}">
        <div class="intr-pl-h" title="payloads for the ${ordinal(i+1)} § marker${content?' (currently '+escAttr(content)+')':''}"><span class="sw" style="background:${c}"></span>§${i+1}${content?' · '+esc(content):''}${INTR_FILE_BTNS}</div>
        <textarea class="rep-edit" data-pos="${i}" spellcheck="false" placeholder="payloads for §${i+1}"></textarea></div>`;}).join('');
  }
  wrap.querySelectorAll('textarea').forEach(ta=>{
    const p=ta.dataset.pos;
    ta.value=p==='s'?(intrState.sniper||''):(intrState.pos[Number(p)]||'');
    ta.addEventListener('input',()=>{if(p==='s')intrState.sniper=ta.value;else intrState.pos[Number(p)]=ta.value;updateIntrCount();intrTouch();});
  });
  wireIntrPayloadFileButtons(wrap);
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
    const counts=mk.map((_,i)=>lines(intrState.pos[i]||'').length);
    const reqs=counts.length?Math.min(...counts):0;
    if(hint)hint.textContent=counts.length?counts.map((n,i)=>`§${i+1}:${n}`).join(' · '):'no § markers yet';
    if(cnt)cnt.textContent=mk.length?`${reqs} request${reqs===1?'':'s'}`:'mark § first';
  }else{
    const n=lines(intrState.sniper||'').length,P=mk.length,reqs=n*Math.max(P,1);
    if(hint)hint.textContent=`${n} payload${n===1?'':'s'}`+(P>1?` × ${P} § positions`:'');
    if(cnt)cnt.textContent=P?`${reqs} request${reqs===1?'':'s'}`:`${n} payloads · mark § first`;
  }
}
function updateIntrMode(){
  const repeat=intrState.type==='repeat';
  $('#intrType').querySelectorAll('button').forEach(x=>{const on=x.dataset.t===intrState.type;x.classList.toggle('on',on);x.setAttribute('aria-pressed',on?'true':'false');});
  const h=$('#intrHint');if(h)h.textContent=intrModeText();
  const rw=$('#intrRepeatWrap');if(rw)rw.style.display=repeat?'inline-flex':'none'; // "× N sends" only in Race
  const mk=$('#intrWrap');if(mk)mk.style.opacity=repeat?'.4':''; // § markers irrelevant in Race
  renderPayloadInputs();
}
$('#intrType').querySelectorAll('button').forEach(b=>b.onclick=()=>{intrState.type=b.dataset.t;updateIntrMode();intrTouch();});
$('#intrWrap').onclick=()=>{const ta=$('#intrTemplate');const a=ta.selectionStart,b=ta.selectionEnd,v=ta.value;ta.value=v.slice(0,a)+'§'+v.slice(a,b)+'§'+v.slice(b);ta.focus();ta.selectionStart=a+1;ta.selectionEnd=b+1;intrTemplateChanged();intrTouch();};
// Re-derive the per-marker inputs whenever the template's § markers change. (Input
// listeners for the editor fields are wired in intrInit so they also save to the tab.)
function intrTemplateChanged(){if(intrState.type==='pitchfork')renderPayloadInputs();else updateIntrCount();}
// setSniperPayloads: used by the AI assistant's "load into Intruder" action.
export function setSniperPayloads(text){intrState.type='sniper';intrState.sniper=text||'';updateIntrMode();intrTouch();}
export async function intrStart(){
  const target=$('#intrTarget').value.trim();
  if(!target){toast('enter a target (scheme://host)');return;}
  const threads=Math.max(1,parseInt($('#intrThreads').value,10)||1);
  const delayMs=Math.max(0,parseInt($('#intrDelay').value,10)||0);
  const body={target,template:$('#intrTemplate').value,attackType:intrState.type,threads,delayMs,
    grepMatch:$('#intrGrep').value.trim(),grepExtract:$('#intrExtract').value.trim(),
    processRules:lines($('#intrProc').value.replace(/,/g,'\n')).map(s=>s.trim()).filter(Boolean)};
  if(intrState.type==='repeat'){
    body.repeat=Math.max(1,parseInt($('#intrRepeat').value,10)||1);
  }else{
    const mk=intrMarkers();
    if(!mk.length){toast('mark at least one § injection point — or use Null mode for payload-free resends');return;}
    if(intrState.type==='pitchfork'){
      body.payloads=mk.map((_,i)=>lines(intrState.pos[i]||''));
      if(body.payloads.some(l=>!l.length)){toast('add payloads for every § position');return;}
    }else{
      body.payloads=[lines(intrState.sniper||'')];
      if(!body.payloads[0].length){toast('add at least one payload');return;}
    }
  }
  intrTouch();                       // persist the launched config to the active tab
  intrRunCfg=intrReadEditor();       // snapshot for the history entry
  intrCapturePending=true;           // capture this run into history on completion
  try{renderIntr(await api('/api/intruder/start',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify(body)}));}
  catch(e){intrCapturePending=false;toast('attack: '+e.message);}
}
$('#intrStart').onclick=intrStart;
export let intrTimer=null;
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
  if(stats){const fl=res.filter(r=>r.flagged).length;stats.textContent=res.length?`${res.length} sent${fl?' · '+fl+' flagged ⚑':''}`:'';}
  // capture a completed run into history (once per Start)
  if(!running&&total>0&&intrCapturePending){
    intrCapturePending=false;
    intrHistory.unshift({ts:Date.now(),target:(intrRunCfg&&intrRunCfg.target)||'',type:(intrRunCfg&&intrRunCfg.type)||intrState.type,total,flagged:res.filter(r=>r.flagged).length,results:res.slice(),capped:!!st.capped,cfg:intrRunCfg});
    if(intrHistory.length>30)intrHistory.length=30;
    renderIntrHistory();
  }
  if(running)scheduleIntr(); // self-poll until the attack converges (robust to event/POST races)
  const box=$('#intrResults');
  if(st.error){box.innerHTML='<div class="hint" style="padding:12px;color:var(--red)">'+esc(st.error)+'</div>';return;}
  if(!res.length){box.innerHTML='<div class="hint" style="padding:12px">'+(running?'sending…':'Set a target, mark § injection points in the template, add payloads, then Start.')+'</div>';return;}
  box.innerHTML=res.map(r=>`<div class="intr-row ${r.flagged?'flag':''}${r.matched?' match':''}">
    <div style="color:var(--fg3)">${r.id}</div>
    <div class="pl">${esc(r.payload)}${r.flagged?' ⚑':''}${r.matched?' <span title="grep matched">✓</span>':''}${r.extracted?' <span class="ext" title="extracted">→ '+esc(r.extracted)+'</span>':''}</div>
    <div style="color:${statusColor(r.status)};font-weight:700;text-align:center">${r.error?'ERR':(r.status||'—')}</div>
    <div style="color:var(--fg2);text-align:right">${r.length}</div>
    <div style="color:var(--fg3);text-align:right">${r.timeMs}ms</div></div>`).join('');
}
intrInit(); // load saved tabs + history, wire editor inputs (seeds the editor too)
export function sendToIntruder(f){
  Promise.all([api('/api/flows/'+f.id),api('/api/flows/'+f.id+'/raw?side=req')]).then(([d,raw])=>{
    const def=(d.scheme==='https'&&d.port===443)||(d.scheme==='http'&&d.port===80);
    $('#intrTarget').value=`${d.scheme}://${d.host}${def?'':':'+d.port}`;
    $('#intrTemplate').value=raw.replace(/\r\n/g,'\n');
    updateIntrMode(); // refresh marker-derived payload inputs for the new template
    intrTouch();      // save the loaded request into the active attack tab
    document.querySelector('.tab[data-tab="intruder"]').click();
    toast('loaded #'+f.id+' into Intruder · add § markers');
  }).catch(e=>toast(e.message));
}
