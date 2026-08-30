import { $, esc, escAttr, toast, api, methodColor, statusColor, statusText, highlightHTTP, highlightHeaderLines, highlightBodyText, prettify, beautifyBody, fmtDur, fmtSize, openCtxMenu, DEC_OPS, contentTypeFromRaw, pickTextFile, normalizeListText, parseListLines, previewListLines, LIST_PREVIEW_LINES, wireRowKey, uiPrompt, createTabManager, projectStorageKey, syncUiSelectStyles, icon } from './core.js';
import { animateOnce, MOTION } from './motion.js';

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

function setRepSendState(stateName,label){
  const button=$('#repSend');if(!button)return;
  button.classList.remove('is-pending','is-success','is-error');
  if(stateName!=='idle')button.classList.add('is-'+stateName);
  button.dataset.state=stateName;
  button.setAttribute('aria-busy',stateName==='pending'?'true':'false');
  button.disabled=stateName==='pending';
  const labelEl=$('#repSendLabel');
  if(labelEl)labelEl.textContent=label;
  else button.textContent=label;
}
function resetRepSend(delay,t){setTimeout(()=>{if(repCur()===t&&!t.sendPending&&!t.sendError)setRepSendState('idle','Send ▸');},delay);}

/* ---- repeater (multi-tab; each tab owns its request workspace + history) ---- */
// A Repeater tab owns every send made during its lifetime. Keep the complete
// list in tab state, but only paint a bounded window so long sessions do not
// turn opening History into a large synchronous DOM update.
const REP_HISTORY_RENDER_BATCH=100;
const REP_HISTORY_DB_NAME='interseptor-repeater-history';
const REP_HISTORY_DB_VERSION=1;
const REP_HISTORY_STORE='entries';
let repHistoryDBPromise=null,repHistoryKeySeq=0;
function newRepHistoryKey(){
  if(globalThis.crypto?.randomUUID)return globalThis.crypto.randomUUID();
  repHistoryKeySeq++;
  return Date.now().toString(36)+'-'+repHistoryKeySeq.toString(36)+'-'+Math.random().toString(36).slice(2);
}
function repHistoryEntry(flow){
  return {id:Number(flow&&flow.id),method:String(flow&&flow.method||''),status:Number(flow&&flow.status)||0,host:String(flow&&flow.host||''),path:String(flow&&flow.path||'')};
}
function normalizeRepHistory(history){
  const out=[],seen=new Set();
  for(const item of Array.isArray(history)?history:[]){
    const entry=repHistoryEntry(item),id=entry.id;
    if(!Number.isSafeInteger(id)||id<=0||seen.has(id))continue;
    seen.add(id);
    entry.method=entry.method.slice(0,32);
    entry.host=entry.host.slice(0,512);
    entry.path=entry.path.slice(0,2048);
    out.push(entry);
  }
  return out;
}
function repHistoryDB(){
  if(repHistoryDBPromise)return repHistoryDBPromise;
  repHistoryDBPromise=new Promise((resolve,reject)=>{
    if(!globalThis.indexedDB){reject(new Error('IndexedDB is unavailable'));return;}
    const request=indexedDB.open(REP_HISTORY_DB_NAME,REP_HISTORY_DB_VERSION);
    request.onupgradeneeded=()=>{
      const db=request.result;
      const store=db.objectStoreNames.contains(REP_HISTORY_STORE)?request.transaction.objectStore(REP_HISTORY_STORE):db.createObjectStore(REP_HISTORY_STORE,{keyPath:'key'});
      if(!store.indexNames.contains('tabKey'))store.createIndex('tabKey','tabKey',{unique:false});
    };
    request.onsuccess=()=>resolve(request.result);
    request.onerror=()=>reject(request.error||new Error('Could not open Repeater history storage'));
    request.onblocked=()=>reject(new Error('Repeater history storage is blocked by another window'));
  }).catch(error=>{repHistoryDBPromise=null;throw error;});
  return repHistoryDBPromise;
}
function repHistoryTabKey(t){return projectStorageKey('rep.history')+'|'+t.historyKey;}
function repHistoryCleanupStorageKey(){return projectStorageKey('rep.history.cleanup');}
function repHistoryCleanupKeys(){
  const raw=localStorage.getItem(repHistoryCleanupStorageKey());
  if(!raw)return[];
  try{
    const prefix=projectStorageKey('rep.history')+'|';
    return [...new Set(JSON.parse(raw).filter(key=>typeof key==='string'&&key.startsWith(prefix)&&key.length<=512))];
  }catch(e){return[];}
}
function repWriteHistoryCleanupKeys(keys){
  if(keys.length)localStorage.setItem(repHistoryCleanupStorageKey(),JSON.stringify(keys));
  else localStorage.removeItem(repHistoryCleanupStorageKey());
}
function repMarkHistoryCleanup(t){
  const tabKey=repHistoryTabKey(t),keys=repHistoryCleanupKeys();
  if(!keys.includes(tabKey))keys.push(tabKey);
  repWriteHistoryCleanupKeys(keys);
  return tabKey;
}
function repClearHistoryCleanup(tabKey){repWriteHistoryCleanupKeys(repHistoryCleanupKeys().filter(key=>key!==tabKey));}
function repHistoryTxnDone(tx){
  return new Promise((resolve,reject)=>{
    tx.oncomplete=()=>resolve();
    tx.onerror=()=>reject(tx.error||new Error('Repeater history transaction failed'));
    tx.onabort=()=>reject(tx.error||new Error('Repeater history transaction was aborted'));
  });
}
// IndexedDB transactions are ordered per connection, but a send, hydration,
// and tab-close cleanup can all reach the database from different async
// continuations. Serialize every tab-owned history operation so cleanup is
// queued behind an in-flight writer and late writers become no-ops after the
// manager marks the tab closed.
function repHistoryOperation(t,work,allowClosed=false){
  if(!t||(!allowClosed&&t._closed))return Promise.resolve(false);
  const previous=t.historyOperation||Promise.resolve();
  const next=previous.catch(()=>{}).then(async()=>{
    if(!allowClosed&&t._closed)return false;
    await work();
    return true;
  });
  t.historyOperation=next.catch(()=>{});
  return next;
}
async function repStoreHistoryEntries(t,entries){
  const normalized=normalizeRepHistory(entries);if(!normalized.length)return;
  return repHistoryOperation(t,async()=>{
    const db=await repHistoryDB();
    if(t._closed)return false;
    const tabKey=repHistoryTabKey(t);
    const tx=db.transaction(REP_HISTORY_STORE,'readwrite'),store=tx.objectStore(REP_HISTORY_STORE);
    normalized.forEach(entry=>store.put({key:tabKey+'|'+entry.id,tabKey,entry}));
    await repHistoryTxnDone(tx);
  });
}
async function repReadHistory(t){
  const db=await repHistoryDB(),tx=db.transaction(REP_HISTORY_STORE,'readonly');
  const index=tx.objectStore(REP_HISTORY_STORE).index('tabKey');
  const request=index.getAll(repHistoryTabKey(t));
  const rows=await new Promise((resolve,reject)=>{request.onsuccess=()=>resolve(request.result||[]);request.onerror=()=>reject(request.error||new Error('Could not read Repeater history'));});
  await repHistoryTxnDone(tx);
  return normalizeRepHistory(rows.map(row=>row.entry)).sort((a,b)=>b.id-a.id);
}
async function repDeleteHistoryKey(tabKey){
  const db=await repHistoryDB(),tx=db.transaction(REP_HISTORY_STORE,'readwrite');
  const index=tx.objectStore(REP_HISTORY_STORE).index('tabKey');
  const request=index.openCursor(tabKey);
  request.onsuccess=()=>{const cursor=request.result;if(cursor){cursor.delete();cursor.continue();}};
  await repHistoryTxnDone(tx);
}
async function repDeleteHistory(t){
  const tabKey=repMarkHistoryCleanup(t);
  return repHistoryOperation(t,async()=>{
    if(!t._closed)return;
    await repDeleteHistoryKey(tabKey);
    repClearHistoryCleanup(tabKey);
  },true);
}
async function repRetryHistoryCleanup(openTabs){
  const openKeys=new Set(openTabs.map(repHistoryTabKey));
  let cleanupKeys;
  try{cleanupKeys=repHistoryCleanupKeys();}catch(e){return;}
  for(const tabKey of cleanupKeys){
    if(openKeys.has(tabKey)){try{repClearHistoryCleanup(tabKey);}catch(e){}continue;}
    try{await repDeleteHistoryKey(tabKey);}catch(e){continue;}
    try{repClearHistoryCleanup(tabKey);}catch(e){}
  }
}
async function repHydrateTabHistory(t){
  if(t.historyHydrationPromise)return t.historyHydrationPromise;
  t.historyHydrationPromise=(async()=>{
    const embedded=normalizeRepHistory(t.history);
    try{
      const stored=await repReadHistory(t);
      const merged=normalizeRepHistory([...embedded,...stored]).sort((a,b)=>b.id-a.id);
      if(t.historyStoreNeedsMigration&&merged.length)await repStoreHistoryEntries(t,merged);
      t.history=merged;t.historyStoreNeedsMigration=false;t.historyStorageError='';
    }catch(e){
      t.history=embedded;t.historyStorageError=e.message||'unknown error';
    }
  })();
  await t.historyHydrationPromise;
}
async function repRecordHistory(t,flow){
  if(!t||!flow)return;
  const entry=repHistoryEntry(flow);
  t.history=normalizeRepHistory([entry,...(t.history||[])]);
  t.historyLoadError='';
  try{await repStoreHistoryEntries(t,t.historyStoreNeedsMigration?t.history:[entry]);t.historyStoreNeedsMigration=false;t.historyStorageError='';}
  catch(e){const first=!t.historyStorageError;t.historyStoreNeedsMigration=true;t.historyStorageError=e.message||'unknown error';if(first)toast('Repeater history could not be saved for reload: '+t.historyStorageError,'error');}
}
export function repBlank(seq){return {tid:seq,title:'new tab',label:'',method:'GET',url:'',headers:'',body:'',reqView:'pretty',resId:null,resView:'pretty',status:'',color:'',sendError:'',sourceFlowId:null,codecId:'',rawBody:'',applyOnSend:false,decodedPlain:'',reqEditEpoch:0,warnings:[],history:[],historyKey:newRepHistoryKey(),historyVisibleCount:REP_HISTORY_RENDER_BATCH,historyNeedsMigration:false,historyLegacyURL:'',historyStoreNeedsMigration:false};}
function normalizeRepeaterTab(t){
  const url=t.url||'';
  const hasHistory=Object.prototype.hasOwnProperty.call(t,'history')&&Array.isArray(t.history);
  const historyKey=typeof t.historyKey==='string'&&t.historyKey?t.historyKey.slice(0,160):newRepHistoryKey();
  const historyNeedsMigration=t.historyNeedsMigration===true||(!t.historyKey&&!hasHistory);
  const history=normalizeRepHistory(t.history);
  return {tid:t.tid,method:t.method||'GET',url,headers:t.headers||'',body:t.body||'',reqView:t.reqView||'pretty',resView:t.resView||'pretty',resId:null,status:'',color:'',sendError:'',title:'',label:t.label||'',sourceFlowId:t.sourceFlowId||null,codecId:t.codecId||'',rawBody:t.rawBody||'',applyOnSend:!!t.applyOnSend,decodedPlain:t.decodedPlain||'',reqEditEpoch:0,warnings:Array.isArray(t.warnings)?t.warnings.filter(w=>typeof w==='string'&&w):[],history,historyKey,historyVisibleCount:REP_HISTORY_RENDER_BATCH,historyNeedsMigration,historyLegacyURL:historyNeedsMigration?String(t.historyLegacyURL||url):'',historyStoreNeedsMigration:hasHistory&&history.length>0};
}
function serializeRepeaterTab(t){
  const out={tid:t.tid,method:t.method,url:t.url,headers:t.headers,body:t.body,reqView:t.reqView||'pretty',resView:t.resView,sourceFlowId:t.sourceFlowId||null,codecId:t.codecId||'',rawBody:t.rawBody||'',applyOnSend:!!t.applyOnSend,decodedPlain:t.decodedPlain||'',label:t.label||'',warnings:t.warnings||[],historyKey:t.historyKey,historyNeedsMigration:!!t.historyNeedsMigration,historyLegacyURL:t.historyNeedsMigration?String(t.historyLegacyURL||t.url||''):''};
  if(t.historyStoreNeedsMigration)out.history=normalizeRepHistory(t.history);
  return out;
}
function repWarningSuffix(t){const warnings=Array.isArray(t&&t.warnings)?t.warnings.filter(w=>typeof w==='string'&&w):[];return warnings.length?' [warning] '+warnings.join(' · '):'';}
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
export function repTitle(t){const warning=repWarningSuffix(t);if(t.label)return t.label+warning;if(!t.url)return 'new tab'+warning;try{const u=new URL(t.url);return t.method+' '+u.host+u.pathname+warning;}catch(e){return t.method+' '+t.url.slice(0,46)+warning;}}
// Keep tab/flow identity in lockstep with the history API contract. URL.host
// drops an explicit default port, while flow metadata always carries one, so
// normalize both sides to scheme + hostname + port + queryless path first.
function repEndpointParts(scheme,host,port,path){
  const s=String(scheme||'').trim().toLowerCase().replace(/:$/,'');
  const h=String(host||'').trim().toLowerCase().replace(/^\[|\]$/g,'');
  if(!s||!h)return null;
  const n=Number(port);
  const p=n>0?n:(s==='https'?443:80);
  let pathname=String(path||'').split(/[?#]/)[0];
  if(!pathname)pathname='/';
  if(pathname[0]!=='/')pathname='/'+pathname;
  return {scheme:s,host:h,port:p,path:pathname};
}
function repEndpointKey(ep){return ep?ep.scheme+'|'+ep.host+'|'+ep.port+'|'+ep.path:null;}
function repEndpointAuthority(scheme,host,port){
  const ep=repEndpointParts(scheme,host,port,'/');
  if(!ep)return '';
  let authority=ep.host;
  if(authority.includes(':'))authority='['+authority.replace(/%/g,'%25')+']';
  const def=(ep.scheme==='https'&&ep.port===443)||(ep.scheme==='http'&&ep.port===80);
  return authority+(def?'':':'+ep.port);
}
function repTabEndpointParts(t){
  if(!t||!t.url)return null;
  try{const u=new URL(t.url);return repEndpointParts(u.protocol,u.hostname,u.port,u.pathname);}catch(e){return null;}
}
export function repTabEndpoint(t){return repEndpointKey(repTabEndpointParts(t));}
export function repFlowEndpoint(f){return repEndpointKey(repEndpointParts(f&&f.scheme,f&&f.host,f&&f.port,f&&f.path));}
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
const uiPersistenceReady=new Map();
const uiPersistenceQueues=new Map();
let resolveRepeaterReady,resolveIntruderReady,resolveWorkstationReady;
const repeaterReady=new Promise(resolve=>{resolveRepeaterReady=resolve;});
const intruderReady=new Promise(resolve=>{resolveIntruderReady=resolve;});
export const workstationReady=new Promise(resolve=>{resolveWorkstationReady=resolve;});
export function releaseWorkstationReady(result={ok:true}){resolveWorkstationReady(result);}
export async function waitForWorkstationReady(){
  const result=await workstationReady;
  if(result?.ok)return true;
  toast(result?.message||'Active project unavailable · project-scoped tools are locked','error');
  return false;
}
function uiPendingStateKey(panel){return projectStorageKey('ui.pending.'+panel);}
export function uiStateSyncPending(){
  try{return ['repeater','intruder','intruder-presets'].some(panel=>localStorage.getItem(uiPendingStateKey(panel))!==null);}
  catch(e){return true;}
}
function readPendingUIState(panel){
  try{
    const raw=localStorage.getItem(uiPendingStateKey(panel));
    if(raw===null)return null;
    return JSON.parse(raw);
  }catch(e){try{localStorage.removeItem(uiPendingStateKey(panel));}catch(ignore){}return null;}
}
function uiPersistenceQueue(panel){
  let queue=uiPersistenceQueues.get(panel);
  if(!queue){queue={pending:null,saving:false};uiPersistenceQueues.set(panel,queue);}
  return queue;
}
async function drainUIState(panel){
  const queue=uiPersistenceQueue(panel);
  if(queue.saving||uiPersistenceReady.get(panel)!==true)return;
  queue.saving=true;
  try{
    while(queue.pending!==null){
      const body=queue.pending;queue.pending=null;
      try{
        await api('/api/ui/'+panel,{method:'PUT',headers:{'content-type':'application/json'},body});
        if(queue.pending===null){
          try{
            if(localStorage.getItem(uiPendingStateKey(panel))===body){
              localStorage.removeItem(uiPendingStateKey(panel));
              document.dispatchEvent(new CustomEvent('interseptor:ui-state-sync',{detail:{pending:uiStateSyncPending()}}));
            }
          }catch(e){}
        }
      }catch(e){
        if(queue.pending===null)queue.pending=body;
        document.dispatchEvent(new CustomEvent('interseptor:ui-state-sync',{detail:{pending:true,error:true,panel}}));
        break;
      }
    }
  }finally{queue.saving=false;}
}
export async function retryUIStateSync(){
  const panels=['repeater','intruder','intruder-presets'].filter(panel=>uiPersistenceQueue(panel).pending!==null);
  await Promise.all(panels.map(panel=>drainUIState(panel)));
  const pending=uiStateSyncPending();
  document.dispatchEvent(new CustomEvent('interseptor:ui-state-sync',{detail:{pending}}));
  return !pending;
}
function persistUIState(panel, blob){
  let body;
  try{body=JSON.stringify(blob);}catch(e){return false;}
  try{localStorage.setItem(uiPendingStateKey(panel),body);}catch(e){}
  uiPersistenceQueue(panel).pending=body;
  if(uiPersistenceReady.get(panel)!==true)return false;
  drainUIState(panel);
  return true;
}
const UI_HYDRATE_TIMEOUT_MS=2500;
async function readBoundedUIState(panel){
  const controller=new AbortController();
  const timer=setTimeout(()=>controller.abort(),UI_HYDRATE_TIMEOUT_MS);
  try{
    const d=await api('/api/ui/'+panel,{signal:controller.signal});
    if(d&&d.value!=null)return {status:'success',value:d.value};
    return {status:'empty'};
  }catch(e){return {status:'error',error:e};}
  finally{clearTimeout(timer);}
}
async function hydrateUIState(panel,storageBase,valid=()=>true){
  let pending=readPendingUIState(panel);
  if(pending!==null&&!valid(pending)){
    try{localStorage.removeItem(uiPendingStateKey(panel));}catch(e){}
    pending=null;
  }
  const result=await readBoundedUIState(panel);
  const validServer=result.status!=='success'||valid(result.value);
  uiPersistenceReady.set(panel,result.status!=='error'&&validServer);
  if(!validServer)return 'error';
  if(pending!==null){
    try{localStorage.setItem(projectStorageKey(storageBase),JSON.stringify(pending));}catch(e){}
    if(result.status!=='error')persistUIState(panel,pending);
    return result.status==='error'?'error':'pending';
  }
  if(result.status==='success'){
    try{localStorage.setItem(projectStorageKey(storageBase),JSON.stringify(result.value));}catch(e){}
  }
  return result.status;
}
export const repTabs=createTabManager({
  storageKey:()=>projectStorageKey('rep.tabs'),
  blank:repBlank,
  title:repTitle,
  onSave:()=>repSaveEditor(),
  onLoad:()=>repLoadEditor(),
  normalize:normalizeRepeaterTab,
  serialize:serializeRepeaterTab,
  labelStyle:(t,active)=>`color:${active?methodColor(t.method):'inherit'}`,
  tablistLabel:'Repeater tabs',
  tabPanelId:'repTabPanel',
  onPersist:blob=>persistUIState('repeater',blob),
  onClose:t=>repDeleteHistory(t),
});
export function repCur(){return repTabs.cur();}
export function renderRepTabs(){repTabs.render('#repTabs');}
export function repSwitch(tid){repTabs.switchTo(tid);}
export function repCloseTab(tid){repTabs.close(tid);}
export function repPersist(){repTabs.persist();}
export function repPersistDebounced(){repTabs.persistDebounced();}
export function repSaveEditor(){
  const t=repCur();if(!t)return;
  const method=$('#repMethod').value,url=$('#repUrl').value,headers=$('#repHeaders').value;
  const v=$('#repBody').value;
  const previous=((t.reqView||'raw')==='decoded'?t.decodedPlain:t.body)||'';
  if(method!==t.method||url!==t.url||headers!==t.headers||v!==previous)t.reqEditEpoch=(t.reqEditEpoch||0)+1;
  t.method=method;t.url=url;t.headers=headers;
  if((t.reqView||'raw')==='decoded')t.decodedPlain=v;
  else t.body=v;
  t.title=repTitle(t);
}
function repSetMethod(method){
  const select=$('#repMethod');if(!select)return;
  const value=String(method||'GET');
  if(![...select.options].some(option=>option.value===value)){
    const option=document.createElement('option');option.value=value;option.textContent=value;select.append(option);
  }
  select.value=value;
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
  if(t.sendPending)setRepSendState('pending','Sending…');
  else if(t.sendError)setRepSendState('error','Send failed');
  else setRepSendState('idle','Send ▸');
  repSetMethod(t.method);$('#repUrl').value=t.url||'';$('#repHeaders').value=t.headers||'';
  const rv=t.reqView||'raw';
  repSyncReqSeg(rv);
  if(rv==='decoded')$('#repBody').value=t.decodedPlain||'';
  else $('#repBody').value=repBodyForDisplay(t.body,rv);
  repCodecBadge(t);
  repRefreshHL();
  $('#repResSeg').querySelectorAll('button').forEach(x=>{const on=x.dataset.view===(t.resView||'pretty');x.classList.toggle('on',on);x.setAttribute('aria-pressed',on?'true':'false');});
  if(t.sendError){$('#repStatus').textContent='send failed';$('#repStatus').style.color='var(--red)';$('#repResView').textContent='(error: '+t.sendError+')';}
  else if(t.resId){$('#repStatus').textContent=t.status||'';$('#repStatus').style.color=t.color||'var(--fg3)';renderRepResponse();}
  else{$('#repStatus').textContent='';$('#repResView').innerHTML=REP_RES_EMPTY;}
  loadRepHistory();
}
async function repEnterDecoded(t){
  const flowId=t.sourceFlowId||t.resId;
  const wire=t.body||'';
  const startView=t.reqView||'pretty';
  t.reqDecodeEpoch=(t.reqDecodeEpoch||0)+1;
  const decodeEpoch=t.reqDecodeEpoch;
  const editorEpoch=t.reqEditEpoch||0;
  t.reqDecodePending=true;
  const current=()=>t.reqDecodeEpoch===decodeEpoch&&t.reqEditEpoch===editorEpoch&&t.reqView===startView&&(t.sourceFlowId||t.resId)===flowId&&(t.body||'')===wire;
  try{
    let d;
    if(flowId){
      d=await api('/api/flows/'+flowId+'/decoded?side=req');
    }else{
      d=await api('/api/codecs/test',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({side:'req',rawBody:wire,host:(()=>{try{return new URL(t.url).host;}catch(e){return'';}})()})});
    }
    if(!current())return null;
    if(!d.matched||d.error){
      if(repCur()===t)toast(d.error||'no message codec matched');
      return false;
    }
    t.reqView='decoded';t.codecId=d.codecId||'';t.applyOnSend=!!d.applyOnSend;t.rawBody=wire;t.decodedPlain=d.plaintext||'';
    if(repCur()!==t)return true;
    $('#repBody').value=t.decodedPlain;repCodecBadge(t);repRefreshHL();return true;
  }catch(e){
    if(!current())return null;
    if(repCur()===t)toast(e.message);
    return false;
  }finally{t.reqDecodePending=false;}
}
export async function repSend(){
  repSaveEditor();const t=repCur();if(!t)return;
  if(t.sendPending)return;
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
  t.sendError='';
  t.sendPending=true;
  setRepSendState('pending','Sending…');
  $('#repStatus').textContent='sending…';$('#repStatus').style.color='var(--fg3)';
  $('#repResView').innerHTML='<span style="color:var(--fg3)">sending…</span>';
  try{
    const flow=await api('/api/repeater/send',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify(payload)});
    t.sendPending=false;
    t.sendError='';t.resId=flow.id;t.status=repStatusLine(flow);t.color=statusColor(flow.status);await repRecordHistory(t,flow);repPersist();
    // The editor panes are shared between tabs. A slow send can finish after
    // the operator has switched to another tab; keep the result on its source
    // tab, but never paint that result into the currently visible tab.
    if(repCur()!==t)return;
    $('#repStatus').textContent=t.status;$('#repStatus').style.color=t.color;
    if(flow.status===401) toast('401 Unauthorized — run login macro in Settings → Session or enable Re-auth on 401');
    await renderRepResponse();
    if(repCur()!==t)return;
    await animateOnce($('#repResView'),[{opacity:.55},{opacity:1}],{duration:MOTION.base,easing:MOTION.enter});
    if(repCur()!==t)return;
    setRepSendState('success','Sent');resetRepSend(600,t);
    loadRepHistory();
  }catch(e){
    t.sendPending=false;
    const msg=friendlySendError(e.message);
    t.sendError=msg;t.status='send failed';t.color='var(--red)';
    if(repCur()!==t){repPersist();return;}
    $('#repStatus').textContent='send failed';$('#repStatus').style.color='var(--red)';
    $('#repResView').textContent='(error: '+msg+')';
    await animateOnce($('#repResView'),[{opacity:.55},{opacity:1}],{duration:MOTION.fast,easing:MOTION.enter});
    if(repCur()!==t){repPersist();return;}
    setRepSendState('error','Send failed');resetRepSend(900,t);toast(msg);
  }
}
export async function renderRepResponse(){
  const t=repCur();if(!t||!t.resId)return;
  const resId=t.resId,resView=t.resView||'pretty';
  try{
    // Message-codec Decoded view (same engine as History inspect).
    if(resView==='decoded'){
      const d=await api('/api/flows/'+resId+'/decoded?side=res');
      if(repCur()!==t||t.resId!==resId||t.resView!==resView||t.sendPending||t.sendError)return;
      if(!d.matched){
        $('#repResView').innerHTML=`<div class="hint" style="padding:14px;line-height:1.7">No project message codec matched this response.<br>
          Add one under <b>Scanner → Codecs</b> (or <code>project/codecs/*.star</code>).</div>`;
        return;
      }
      if(d.error){
        $('#repResView').innerHTML=`<div class="hint" style="padding:14px;color:var(--red)">Codec <b>${esc(d.codecId||'')}</b> error: ${esc(d.error)}</div>`;
        return;
      }
      const fields=d.fields&&Object.keys(d.fields).length
        ? `<div class="hint" style="padding:8px 0 10px">Decoded fields: ${Object.keys(d.fields).map(k=>`<code>${esc(k)}</code>`).join(', ')}</div>` : '';
      const badge=`<div class="hint" style="padding:0 0 8px">Decoded for display · <b>${esc(d.title||d.codecId||'')}</b>${d.note?' · '+esc(d.note):''}</div>`;
      const body=typeof d.plaintext==='string'?d.plaintext:'';
      $('#repResView').innerHTML=badge+fields+'<pre style="margin:0;white-space:pre-wrap">'+highlightBodyText(body,'application/json')+'</pre>';
      return;
    }
    const raw=await api('/api/flows/'+resId+'/raw?side=res');
    // A tab switch during the fetch would otherwise paint this response into the
    // now-active tab's shared #repResView pane.
    if(repCur()!==t||t.resId!==resId||t.resView!==resView||t.sendPending||t.sendError)return;
    $('#repResView').innerHTML=highlightHTTP(resView==='pretty'?prettify(raw):raw,resView==='pretty',contentTypeFromRaw(raw));
  }catch(e){if(repCur()===t&&t.resId===resId&&t.resView===resView&&!t.sendPending&&!t.sendError)$('#repResView').textContent='(error: '+e.message+')';}
}
async function migrateLegacyRepHistory(t){
  if(!t.historyNeedsMigration)return true;
  if(t.historyMigrationPromise)return t.historyMigrationPromise;
  const legacyURL=t.historyLegacyURL||t.url;
  const ep=repTabEndpointParts({url:legacyURL});
  if(!ep){t.historyNeedsMigration=false;t.historyLegacyURL='';repPersist();return true;}
  const params=new URLSearchParams({scheme:ep.scheme,host:ep.host,port:String(ep.port),path:ep.path});
  t.historyMigrationPromise=(async()=>{
    try{
      const d=await api('/api/repeater/history?'+params.toString());
      // A send can complete while this one-time snapshot is in flight. Keep
      // those tab-owned rows first, merge the legacy endpoint rows behind them,
      // and let normalization de-duplicate the result.
      const legacy=normalizeRepHistory(d.flows||[]);
      t.history=normalizeRepHistory([...(t.history||[]),...legacy]);
      await repStoreHistoryEntries(t,legacy);
      t.historyNeedsMigration=false;t.historyLegacyURL='';t.historyLoadError='';repPersist();return true;
    }catch(e){t.historyLoadError=e.message||'unknown error';return false;}
    finally{t.historyMigrationPromise=null;}
  })();
  return t.historyMigrationPromise;
}
export async function loadRepHistory(){
  const box=$('#repHistory');if(!box)return;const t=repCur();if(!t)return;
  const setCount=n=>{const tg=$('#repHistToggle');if(tg)tg.textContent='⟲ History'+(n?' ('+n+')':'');};
  await repHydrateTabHistory(t);
  if(repCur()!==t)return;
  let migrationError=t.historyStorageError?`<div class="state-error" style="margin:8px"><span>History reload storage unavailable: ${esc(t.historyStorageError)}</span> <button type="button" class="btn xs" data-rep-history-storage-retry>Retry</button></div>`:'';
  if(t.historyNeedsMigration){
    setCount(0);box.innerHTML='<div class="hint" style="padding:10px">Loading this tab’s saved history…</div>';
    const migrated=await migrateLegacyRepHistory(t);
    if(repCur()!==t)return;
    if(!migrated)migrationError=`<div class="state-error" style="margin:8px"><span>Could not restore older sends: ${esc(t.historyLoadError||'unknown error')}</span> <button type="button" class="btn xs" data-rep-history-retry>Retry</button></div>`;
  }
  const flows=normalizeRepHistory(t.history);
  t.history=flows;setCount(flows.length);
  if(!flows.length){box.innerHTML=migrationError||'<div class="hint" style="padding:10px">Send a request to start this tab’s history.</div>';}
  else{
    const visibleCount=Math.min(flows.length,Math.max(REP_HISTORY_RENDER_BATCH,Number(t.historyVisibleCount)||0));
    const visible=flows.slice(0,visibleCount);
    const remaining=flows.length-visibleCount;
    box.innerHTML=migrationError+visible.map(f=>`<div class="h ${f.id===t.resId?'sel':''}" data-id="${f.id}">
    <div><span style="color:${methodColor(f.method)};font-weight:700">${esc(f.method||'—')}</span> <span style="color:${statusColor(f.status)};font-weight:700">${f.status||'—'}</span></div>
    <div class="u">${esc((f.host||'')+(f.path||''))}</div></div>`).join('')+(remaining?`<button type="button" class="rep-hist-more" data-rep-history-more>Show ${Math.min(REP_HISTORY_RENDER_BATCH,remaining)} older <span aria-hidden="true">·</span> ${remaining} remaining</button>`:'');
    box.querySelector('[data-rep-history-more]')?.addEventListener('click',()=>{
      t.historyVisibleCount=Math.min(flows.length,visibleCount+REP_HISTORY_RENDER_BATCH);
      loadRepHistory();
    });
  }
  box.querySelector('[data-rep-history-storage-retry]')?.addEventListener('click',async()=>{t.historyHydrationPromise=null;await repHydrateTabHistory(t);if(repCur()===t){repPersist();loadRepHistory();}});
  box.querySelector('[data-rep-history-retry]')?.addEventListener('click',loadRepHistory);
  box.querySelectorAll('.h').forEach(el=>{el.onclick=()=>repLoadSend(Number(el.dataset.id));wireRowKey(el,()=>repLoadSend(Number(el.dataset.id)));});
}
// Toggle the per-tab history rail (hidden by default to give the editor full width).
$('#repHistToggle')&&($('#repHistToggle').onclick=()=>{
  const h=$('#repHistory');if(!h)return;
  const show=h.style.display==='none';h.style.display=show?'':'none';
  $('#repHistToggle').setAttribute('aria-expanded',show?'true':'false');
});
export async function repLoadSend(id){
  const t=repCur();if(!t)return;
  t.historyLoadEpoch=(t.historyLoadEpoch||0)+1;
  const loadEpoch=t.historyLoadEpoch;
  const editorEpoch=t.reqEditEpoch||0;
  const current=()=>repCur()===t&&t.historyLoadEpoch===loadEpoch&&(t.reqEditEpoch||0)===editorEpoch;
  try{
    const d=await api('/api/flows/'+id);
    if(!current())return;
    const raw=await api('/api/flows/'+id+'/raw?side=req');
    if(!current())return;
    const i=raw.indexOf('\r\n\r\n');
    t.method=d.method;t.url=`${d.scheme}://${repEndpointAuthority(d.scheme,d.host,d.port)}${d.path}`;t.headers=headersToText(d.reqHeaders);
    t.body=i>=0?raw.slice(i+4):'';
    t.reqView='pretty';t.resView='pretty';t.sourceFlowId=id;t.codecId='';t.decodedPlain='';t.rawBody='';t.applyOnSend=false;t.label='';
    t.reqEditEpoch=(t.reqEditEpoch||0)+1;
    t.sendError='';t.resId=id;t.status=repStatusLine(d);t.color=statusColor(d.status);t.title=repTitle(t);
    renderRepTabs();repLoadEditor();repPersist();
  }catch(e){if(current())toast('History item #'+id+' is no longer available: '+e.message,'warn');}
}
export async function sendToRepeater(f){
  if(!await waitForWorkstationReady())return false;
  repSaveEditor();
  const tabEditEpochs=new Map(repTabs.tabs.map(t=>[t.tid,t.reqEditEpoch||0]));
  try{
    const d=await api('/api/flows/'+f.id);
    const raw=await api('/api/flows/'+f.id+'/raw?side=req');
    // Findings and other callers may pass only {id}. Resolve metadata before
    // choosing a tab so requests do not collapse into an "undefined" endpoint.
    const fep=repFlowEndpoint(d);
    let t=repTabs.tabs.find(x=>repTabEndpoint(x)===fep);
    if(t&&tabEditEpochs.get(t.tid)!==(t.reqEditEpoch||0))t=null;
    if(!t){t=repBlank(repTabs.seq++);repTabs.tabs.push(t);}
    repTabs.active=t.tid;
    t.method=d.method;t.url=`${d.scheme}://${repEndpointAuthority(d.scheme,d.host,d.port)}${d.path}`;t.headers=headersToText(d.reqHeaders);
    const i=raw.indexOf('\r\n\r\n');t.body=i>=0?raw.slice(i+4):'';
    t.reqView='pretty';t.sourceFlowId=f.id;t.codecId='';t.decodedPlain='';t.rawBody='';t.applyOnSend=false;t.label='';
    t.resId=null;t.status='';t.color='';t.sendError='';t.title=repTitle(t);
    renderRepTabs();repPersist();
    // Stay on the source panel when either read fails. Navigating now confirms
    // that a complete editable request is ready.
    document.querySelector('.tab[data-tab="repeater"]').click();
    repLoadEditor();
    toast('loaded #'+f.id+' into Repeater');
    return true;
  }catch(e){toast(e.message);return false;}
}
export async function repInit(){
  if(repInit._done)return repeaterReady;repInit._done=true;
  const hydration=await hydrateUIState('repeater','rep.tabs');
  repTabs.init('#repTabs');
  await repRetryHistoryCleanup(repTabs.tabs);
  await Promise.all(repTabs.tabs.map(repHydrateTabHistory));
  if(repCur())loadRepHistory();
  // First persist migrates localStorage drafts into the project DB.
  if(repTabs.tabs.length&&hydration!=='error')repTabs.persist();
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
  wirePostmanImport();
  resolveRepeaterReady(hydration);
  return hydration;
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
  if(next==='decoded'){
    if(t.reqDecodePending)return;
    b.disabled=true;b.setAttribute('aria-busy','true');
    const ok=await repEnterDecoded(t);
    b.disabled=false;b.setAttribute('aria-busy','false');
    if(ok===null)return;
    if(repCur()!==t)return;
    if(ok){repSyncReqSeg('decoded');}
    else{$('#repBody').value=repBodyForDisplay(t.body,t.reqView||'pretty');repSyncReqSeg(t.reqView||'pretty');repCodecBadge(t);}
    repRefreshHL();repPersistDebounced();
    return;
  }
  if((t.reqView||'raw')==='pretty'&&next==='raw')t.body=compactBody(t.body);
  if((t.reqView||'raw')==='decoded'&&next!=='decoded'){
    // leave decoded — wire body stays in t.body/rawBody
    if(t.rawBody)t.body=t.rawBody;
  }
  t.reqView=next;
  repSyncReqSeg(next);
  $('#repBody').value=repBodyForDisplay(t.body,next);
  repCodecBadge(t);
  repRefreshHL();
  repPersistDebounced();
});
$('#repResSeg').querySelectorAll('button').forEach(b=>b.onclick=()=>{
  const t=repCur();if(!t)return;
  t.resView=b.dataset.view;
  $('#repResSeg').querySelectorAll('button').forEach(x=>{x.classList.toggle('on',x===b);x.setAttribute('aria-pressed',x===b?'true':'false');});
  renderRepResponse();
});

/* ---- intruder ---- */
function postmanDocumentKind(doc){
  if(doc&&doc.collection&&doc.collection.info&&Array.isArray(doc.collection.item))return 'wrapper';
  if(doc&&doc.info&&Array.isArray(doc.item))return 'collection';
  if(doc&&Array.isArray(doc.values))return 'environment';
  return '';
}
async function importPostmanFiles(files){
  const docs=[];
  for(const file of files){
    let doc;
    try{doc=JSON.parse(await file.text());}
    catch(e){throw new Error((file.name||'file')+' is not valid JSON');}
    docs.push({file,doc,kind:postmanDocumentKind(doc)});
  }
  let collection=null,environment=null;
  for(const entry of docs){
    if(entry.kind==='wrapper'){
      if(collection)throw new Error('select one Postman collection');
      collection=entry.doc.collection;environment=entry.doc.environment||environment;
    }else if(entry.kind==='collection'){
      if(collection)throw new Error('select one Postman collection');
      collection=entry.doc;
    }else if(entry.kind==='environment'){
      if(environment)throw new Error('select one Postman environment');
      environment=entry.doc;
    }else throw new Error((entry.file.name||'file')+' is not a Postman collection or environment');
  }
  if(!collection)throw new Error('select a Postman collection JSON');
  const payload=environment?{collection,environment}:collection;
  const result=await api('/api/import/postman',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify(payload)});
  const requests=Array.isArray(result.requests)?result.requests:[];
  if(!requests.length)throw new Error('the collection has no importable HTTP requests');
  repSaveEditor();
  const created=[];
  requests.forEach((request,index)=>{
    const tab=repBlank(repTabs.seq++);
    tab.label=((request.folder?request.folder+' / ':'')+(request.name||request.method+' '+request.url||('request '+(index+1))));
    tab.method=request.method||'GET';tab.url=request.url||'';tab.headers=request.headers||'';tab.body=request.body||'';
    tab.warnings=Array.isArray(request.warnings)?request.warnings.slice():[];
    created.push(tab);repTabs.tabs.push(tab);
  });
  repTabs.active=created[0].tid;renderRepTabs();repLoadEditor();repPersist();
  const unresolved=Array.isArray(result.unresolved)?result.unresolved:[];
  const skipped=result.skipped?` · ${result.skipped} skipped`:'';
  const warnings=Array.isArray(result.warnings)?result.warnings:[];
  const caution=warnings.length?` · ${warnings.length} warning${warnings.length===1?'':'s'} — review imported tabs`:'';
  const missing=unresolved.length?` · unresolved: ${unresolved.join(', ')} — review before Send`:'';
  toast(`Postman: loaded ${requests.length} request${requests.length===1?'':'s'} from ${result.name||'collection'} into Repeater${skipped}${caution}${missing}`);
}
function wirePostmanImport(){
  const button=$('#repPostmanImport'),input=$('#repPostmanFile');
  if(!button||!input)return;
  button.onclick=()=>input.click();
  input.onchange=async e=>{
    const files=[...e.target.files||[]];
    if(!files.length)return;
    try{await importPostmanFiles(files);}catch(err){toast('Postman import: '+err.message);}
    e.target.value='';
  };
}
// Per-position colours tie each §-marker to its payload list (cycle if > 6 markers).
const POS_COLORS=['var(--accent)','var(--blue)','var(--amber)','var(--violet)','var(--cyan)','var(--red)'];
const INTR_TPL='POST /login HTTP/1.1\nHost: example.com\nContent-Type: application/json\n\n{"user":"§admin§","pass":"§password§"}';
const INTR_SNIPER="admin\nadministrator\nroot\n' OR 1=1--\n../../../etc/passwd";
const INTR_POS=["admin\nadministrator\nroot","password\n123456\nchangeme"];
// INTR_RESULTS_EMPTY — the idle state of #intrResults before the first attack.
const INTR_RESULTS_EMPTY='<div class="state-empty"><div class="state-empty-icon"><svg class="icon" aria-hidden="true" focusable="false"><use href="#i-target"/></svg></div><div class="state-empty-title">No results yet</div><p class="state-empty-hint">Set a target, mark <b>§</b> injection points, add payloads, then <b>Start</b>.</p></div>';
// Live editing mirror of the active tab's mode + payload lists (the other fields —
// target/template/threads/delay/repeat — live in the DOM and are snapshotted to tabs).
const INTR_NUM_DEFAULT=()=>({start:1,end:100,step:1,mode:'sequence',count:100,pad:0,unique:false});
export const intrState={type:'sniper',sniper:INTR_SNIPER,pos:INTR_POS.slice(),sniperLines:null,posLines:[],sniperFile:null,posFiles:[],sniperSource:'list',sniperNums:INTR_NUM_DEFAULT(),posSources:[],posNums:[]};
let intrLastFlowId=null; // last flow loaded into Intruder (for the Generate action)
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
  tablistLabel:'Intruder tabs',
  tabPanelId:'intrTabPanel',
  onPersist:blob=>persistUIState('intruder',blob),
});
function intrTouch(){intrSaveCur();renderIntrTabs();intrTabs.persistDebounced();} // save editor → active tab
function renderIntrTabs(){intrTabs.render('#intrTabs');syncIntrTabLock(intrStartPending||intrLastRunning);}
export async function intrInit(){
  if(intrInit._done)return intruderReady; intrInit._done=true;
  const [tabHydration,presetHydration]=await Promise.all([hydrateUIState('intruder','intr.tabs'),hydrateIntrPresets()]);
  const hydration=[tabHydration,presetHydration].includes('error')?'error':[tabHydration,presetHydration].includes('pending')?'pending':tabHydration;
  intrTabs.init('#intrTabs');
  if(intrTabs.tabs.length&&hydration!=='error')intrTabs.persist();
  renderIntrHistory();loadIntrPresets();
  const tabBar=$('#intrTabs');
  if(tabBar)tabBar.addEventListener('keydown',e=>{
    if((intrStartPending||intrLastRunning)&&['ArrowLeft','ArrowRight','Home','End'].includes(e.key)){
      e.preventDefault();e.stopImmediatePropagation();
    }
  },true);
  $('#intrTarget')&&$('#intrTarget').addEventListener('input',intrTouch);
  $('#intrTemplate')&&$('#intrTemplate').addEventListener('input',()=>{intrTemplateChanged();intrTouch();});
  ['#intrThreads','#intrDelay','#intrRepeat'].forEach(s=>{const el=$(s);if(el)el.addEventListener('input',()=>{if(intrState.type==='repeat')renderPayloadInputs();else updateIntrCount();intrTouch();});});
  ['#intrGrep','#intrExtract','#intrProc'].forEach(s=>{const el=$(s);if(el)el.addEventListener('input',intrTouch);});
  const gen=$('#intrAiGen');if(gen)gen.onclick=()=>intrGeneratePayloads();
  resolveIntruderReady(hydration);
  return hydration;
}

/* ---- intruder run history (this session) ---- */
const intrHistory=[]; let intrCapturePending=false, intrRunCfg=null;
let intrStartPending=false;
let intrRunTabId=null;
let intrPollError='';
let intrLastRunning=false,intrLastTotal=0,intrLastDone=0;
function syncIntrTabLock(locked){
  const bar=$('#intrTabs');if(!bar)return;
  bar.setAttribute('aria-busy',locked?'true':'false');
  bar.querySelectorAll('.rt-select').forEach(button=>{
    const tid=Number(button.closest('.rep-tab')?.dataset.tid);
    button.disabled=!!locked&&tid!==intrRunTabId;
  });
  bar.querySelectorAll('.rt-close,.rep-tab-add').forEach(button=>{button.disabled=!!locked;});
  bar.title=locked?'Attack tabs are locked until the active run finishes':'';
}
function renderIntrHistory(){
  const box=$('#intrHistory'),tg=$('#intrHistToggle');
  if(tg)tg.textContent='⟲ History'+(intrHistory.length?' ('+intrHistory.length+')':'');
  if(!box)return;
  if(!intrHistory.length){box.innerHTML='<div class="hint" style="padding:10px">No attacks yet this session.</div>';return;}
  const liveRow=intrDisplayOwner==='history'?`<div class="h intr-live" data-intr-live title="Return to the current run"><div><span style="font-weight:700;color:var(--accent)">Live / current run</span></div><div class="u">${esc((intrRunCfg&&intrRunCfg.target)||intrDisplayedTarget||'')}</div></div>`:'';
  box.innerHTML=liveRow+intrHistory.map((h,i)=>`<div class="h${h===intrDisplayedHistory?' sel':''}" data-i="${i}" aria-current="${h===intrDisplayedHistory?'true':'false'}" title="re-open this run + its config"><div><span style="font-weight:700;text-transform:capitalize">${esc(intrTypeLabel(h.type))}</span> <span style="color:var(--fg3)">${h.total} req${h.flagged?' · <span style="color:var(--accent)">'+h.flagged+'<svg class="icon" aria-hidden="true" focusable="false"><use href="#i-flag"/></svg></span>':''}</span></div><div class="u">${esc(h.target||'')}</div></div>`).join('');
  const live=box.querySelector('[data-intr-live]');if(live){live.onclick=showIntrLiveResults;wireRowKey(live,showIntrLiveResults);}
  box.querySelectorAll('.h[data-i]').forEach(el=>{el.onclick=()=>intrLoadHistory(Number(el.dataset.i));wireRowKey(el,()=>intrLoadHistory(Number(el.dataset.i)));});
}
function intrLoadHistory(i){
  const h=intrHistory[i];if(!h)return;
  // History is a display choice, not a replacement for the authoritative
  // server snapshot. Keep polling, locks, and recovery tied to the active run
  // while filters and finding creation operate on what the operator chose.
  intrDisplayOwner='history';
  intrDisplayedHistory=h;
  intrDisplayedTarget=h.target||'';
  intrDisplayedResults=h.results.slice();
  if(h.cfg&&!intrLastRunning&&!intrStartPending){
    // Reuse the same complete config applicator as tab navigation. History
    // includes processing, extraction, numeric generators, and per-position
    // payload sources—not just the visible target/template fields.
    intrApply(h.cfg);
    intrTouch();}
  renderIntrHistory();
  renderIntr({running:false,total:h.total,done:h.total,results:intrDisplayedResults,capped:h.capped},{authoritative:false});
}
$('#intrHistToggle')&&($('#intrHistToggle').onclick=()=>{
  const h=$('#intrHistory');if(!h)return;
  const show=h.style.display==='none';h.style.display=show?'':'none';
  $('#intrHistToggle').setAttribute('aria-expanded',show?'true':'false');
});

function intrModeText(){
  if(intrState.type==='repeat')
    return 'Race / repeat — resend the template verbatim with no payloads or § markers. Use for duplicate submits, idempotency checks, rate limits, or concurrent replays (raise threads, 0 ms delay).';
  if(intrState.type==='battering')
    return 'Battering ram — one payload list applied to every § marker at once (same value in all positions). Good for hitting every field with the same token.';
  if(intrState.type==='cluster')
    return 'Cluster bomb — one payload list per § marker; every combination is tried (cartesian product). Mark N points → fill N lists.';
  if(intrState.type==='pitchfork')
    return 'Pitchfork — one payload list per § marker (colour-matched below). Lists advance together, so mark N injection points → fill N lists; fires min(list lengths) requests. Load each list from a file with the Load / Append buttons.';
  return 'Sniper — a single payload list, tried at each § marker one position at a time (the others keep their original value). Load payloads from a file with the Load / Append buttons.';
}
const INTR_FILE_BTNS=`<div class="spacer"></div><button type="button" class="btn intr-file-load" data-mode="replace" title="Load payloads from file"><svg class="icon" aria-hidden="true" focusable="false"><use href="#i-folder"/></svg></button><button type="button" class="btn intr-file-load" data-mode="append" title="Append payloads from file">＋</button>`;
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
    wrap.innerHTML='<div class="hint">Race / repeat — no payload lists. The request above is sent verbatim <b>×'+(parseInt($('#intrRepeat').value,10)||0)+'</b> times across <b>'+(parseInt($('#intrThreads').value,10)||1)+'</b> threads.</div>';
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
    ta.setAttribute('aria-label',p==='s'?'Payload list for all injection positions':`Payload list for injection position ${Number(p)+1}`);
    ta.value=p==='s'?(intrState.sniper||''):(intrState.pos[Number(p)]||'');
    ta.readOnly=intrPayloadTruncated(p);
    const note=ta.closest('.intr-pl')?.querySelector('.intr-pl-note');
    if(note){
      note.textContent=intrPayloadNote(p);
      const tab=intrTabs.cur();
      if(p==='s'&&tab?.sniperLarge&&!intrState.sniperLines){
        note.textContent=(tab.sniperCount?tab.sniperCount.toLocaleString()+' payloads were':'Large payload list was')+' not restored after reload — load the file again with the Load button.';
      }else if(p!=='s'&&tab?.posCounts?.[Number(p)]&&!intrState.posLines?.[Number(p)]){
        note.textContent=`${tab.posCounts[Number(p)].toLocaleString()} payloads were not restored after reload — load the file again with the Load button.`;
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
  syncButtonGroupTabStops($('#intrType'));
  const lm=document.getElementById('intrListMode');
  if(lm){
    const isList=primary==='__lists__';
    lm.style.display=isList?'':'none';
    syncUiSelectStyles(lm);
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
function syncButtonGroupTabStops(group){
  if(!group)return;
  const buttons=[...group.querySelectorAll('button')];
  const active=buttons.find(b=>b.getAttribute('aria-pressed')==='true')||buttons[0];
  buttons.forEach(b=>b.tabIndex=b===active?0:-1);
}
function wireButtonGroupKeys(group){
  if(!group)return;
  const buttons=[...group.querySelectorAll('button')];
  syncButtonGroupTabStops(group);
  buttons.forEach((button,i)=>button.addEventListener('keydown',e=>{
    let next=-1;
    if(e.key==='ArrowRight'||e.key==='ArrowDown')next=(i+1)%buttons.length;
    else if(e.key==='ArrowLeft'||e.key==='ArrowUp')next=(i-1+buttons.length)%buttons.length;
    else if(e.key==='Home')next=0;
    else if(e.key==='End')next=buttons.length-1;
    else return;
    e.preventDefault();buttons[next].focus();buttons[next].click();
  }));
}
wireButtonGroupKeys($('#intrType'));
const _intrListMode=document.getElementById('intrListMode');
if(_intrListMode)_intrListMode.onchange=()=>{intrState.type=_intrListMode.value;updateIntrMode();intrTouch();};
$('#intrWrap').onclick=()=>{const ta=$('#intrTemplate');const a=ta.selectionStart,b=ta.selectionEnd,v=ta.value;ta.value=v.slice(0,a)+'§'+v.slice(a,b)+'§'+v.slice(b);ta.focus();ta.selectionStart=a+1;ta.selectionEnd=b+1;intrTemplateChanged();intrTouch();};
// Re-derive the per-marker inputs whenever the template's § markers change. (Input
// listeners for the editor fields are wired in intrInit so they also save to the tab.)
function intrTemplateChanged(){if(intrState.type==='pitchfork'||intrState.type==='cluster')renderPayloadInputs();else updateIntrCount();}
// setSniperPayloads: used by the AI assistant's "load into Intruder" action.
export function setSniperPayloads(text){intrState.type='sniper';intrSetPayloadLines('s', parseListLines(text||''), null);updateIntrMode();intrTouch();}
const INTR_MAX_REQUESTS=2000;
function setIntrStartState(stateName,label){
  const button=$('#intrStart');if(!button)return;
  button.classList.remove('is-pending','is-success','is-error');
  if(stateName!=='idle')button.classList.add('is-'+stateName);
  button.dataset.state=stateName;
  button.setAttribute('aria-busy',stateName==='pending'?'true':'false');
  button.disabled=stateName==='pending';
  button.textContent=label;
}
function resetIntrStart(delay,epoch){setTimeout(()=>{if(epoch===intrStartEpoch&&!intrLastRunning)setIntrStartState('idle','Start ▸');},delay);}
let intrStartEpoch=0;
export async function intrStart(){
  if(intrStartPending)return;
  const target=$('#intrTarget').value.trim();
  if(!target){toast('enter a target (scheme://host)');$('#intrTarget').focus();return;}
  const threadsValue=Number($('#intrThreads').value),delayValue=Number($('#intrDelay').value);
  if(!Number.isInteger(threadsValue)||threadsValue<1||threadsValue>64){toast('threads must be between 1 and 64','error');$('#intrThreads').focus();return;}
  if(!Number.isInteger(delayValue)||delayValue<0){toast('delay must be zero or greater','error');$('#intrDelay').focus();return;}
  const threads=threadsValue,delayMs=delayValue;
  const body={target,template:$('#intrTemplate').value,attackType:intrState.type,threads,delayMs,
    grepMatch:$('#intrGrep').value.trim(),grepExtract:$('#intrExtract').value.trim(),
    processRules:lines($('#intrProc').value.replace(/,/g,'\n')).map(s=>s.trim()).filter(Boolean)};
  if(intrState.type==='repeat'){
    const repeatValue=Number($('#intrRepeat').value);
    if(!Number.isInteger(repeatValue)||repeatValue<1||repeatValue>2000){toast('repeat must be between 1 and 2000','error');$('#intrRepeat').focus();return;}
    body.repeat=repeatValue;
  }else{
    const mk=intrMarkers();
    if(!mk.length){toast('mark at least one § injection point — or use Race / repeat for payload-free resends');$('#intrTemplate').focus();return;}
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
  intrStartPending=true;
  intrRunTabId=intrTabs.cur()?.tid??null;
  syncIntrTabLock(true);
  const epoch=++intrStartEpoch;
  const pollEpoch=invalidateIntrPoll();
  intrPollError='';
  showIntrLiveResults();
  setIntrStartState('pending','Starting…');
  try{
    const started=await api('/api/intruder/start',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify(body)});
    intrStartPending=false;
    intrDisplayOwner='live';
    intrDisplayedHistory=null;
    renderIntrHistory();
    if(pollEpoch===intrPollEpoch)renderIntr(started);
    else scheduleIntr();
  }catch(e){
    intrStartPending=false;intrCapturePending=false;
    intrRunTabId=null;syncIntrTabLock(false);
    setIntrStartState('error','Start failed');
    resetIntrStart(1000,epoch);
    toast('attack: '+e.message,'error');
  }
}
$('#intrStart').onclick=intrStart;
function intrPresetsKey(){return projectStorageKey('intruder.presets');}
async function hydrateIntrPresets(){
  return hydrateUIState('intruder-presets','intruder.presets',Array.isArray);
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
  const serverSyncQueued=persistUIState('intruder-presets',list);
  loadIntrPresets();toast(serverSyncQueued?'preset saved locally · server sync queued':'preset saved locally · server sync unavailable',serverSyncQueued?'success':'warn');
};
export let intrTimer=null;
let intrFilter='all', intrLastResults=[], intrDisplayedResults=[], intrDisplayOwner='live', intrDisplayedTarget='', intrDisplayedHistory=null;
let intrPollEpoch=0;
let intrPollInFlight=false,intrPollQueued=false;
function showIntrLiveResults(){
  intrDisplayOwner='live';
  intrDisplayedHistory=null;
  intrDisplayedResults=intrLastResults.slice();
  intrDisplayedTarget=(intrRunCfg&&intrRunCfg.target)||$('#intrTarget').value||'';
  renderIntrHistory();
  renderIntr({running:intrLastRunning,total:intrLastTotal,done:intrLastDone,results:intrDisplayedResults},{authoritative:false});
}
function invalidateIntrPoll(){
  clearTimeout(intrTimer);
  intrPollQueued=false;
  return ++intrPollEpoch;
}
function intrIsInteresting(r){return !!(r&& (r.flagged||r.matched||r.anomaly));}
function intrApplyFilter(res){
  if(intrFilter==='interesting') return res.filter(intrIsInteresting);
  if(intrFilter==='error') return res.filter(r=>r.error);
  return res;
}
export function scheduleIntr(){
  clearTimeout(intrTimer);
  if(intrPollInFlight){intrPollQueued=true;return;}
  const epoch=++intrPollEpoch;
  intrTimer=setTimeout(async()=>{
    intrPollInFlight=true;
    try{
      const st=await api('/api/intruder/state');
      if(epoch!==intrPollEpoch)return;
      renderIntr(st);
    }catch(e){
      if(epoch!==intrPollEpoch)return;
      // Keep the last result set visible while the state endpoint is unavailable.
      // A retry affordance makes a long-running attack recoverable instead of
      // silently freezing at its last progress value.
      intrPollError=e&&e.message?e.message:'connection unavailable';
      renderIntr({running:intrLastRunning,total:intrLastTotal,done:intrLastDone,results:intrDisplayedResults,pollFailed:true},{authoritative:false});
    }finally{
      intrPollInFlight=false;
      if(intrPollQueued){intrPollQueued=false;scheduleIntr();}
    }
  },120);
}
export function renderIntr(st,{authoritative=true}={}){
  const running=!!st.running,total=st.total||0,done=st.done||0,res=Array.isArray(st.results)?st.results:[];
  if(authoritative){
    if(running&&intrRunTabId==null)intrRunTabId=intrTabs.cur()?.tid??null;
    if(!st.pollFailed){
      intrPollError='';intrLastRunning=running;intrLastTotal=total;intrLastDone=done;
      intrLastResults=res.slice();
      if(intrDisplayOwner==='live'){
        intrDisplayedResults=res.slice();
        intrDisplayedTarget=(intrRunCfg&&intrRunCfg.target)||$('#intrTarget').value||'';
      }
    }
    if(!running&&!intrStartPending)intrRunTabId=null;
    if(!intrStartPending)setIntrStartState(running?'pending':'idle',running?'Running…':'Start ▸');
    if(!running&&total>0&&intrCapturePending){
      intrCapturePending=false;
      intrHistory.unshift({ts:Date.now(),target:(intrRunCfg&&intrRunCfg.target)||'',type:(intrRunCfg&&intrRunCfg.type)||intrState.type,total,flagged:res.filter(r=>r.flagged).length,results:res.slice(),capped:!!st.capped,cfg:intrRunCfg});
      if(intrHistory.length>30)intrHistory.length=30;
      renderIntrHistory();
    }
    if(running&&!st.pollFailed)scheduleIntr();
  }
  const displayRes=authoritative
    ?(intrDisplayOwner==='live'?res:intrDisplayedResults)
    :(Array.isArray(st.results)?st.results:intrDisplayedResults);
  syncIntrTabLock(intrLastRunning||intrStartPending);
  $('#intrProgress').textContent=running?`running ${done}/${total}`:(total?`done ${done}/${total}${st.capped?' (capped)':''}`:'');
  const pollStatus=$('#intrPollStatus');
  if(pollStatus){
    if(intrPollError){
      pollStatus.innerHTML='<span class="state-error-msg">Attack status unavailable: '+esc(intrPollError)+'</span> <button type="button" class="btn xs" data-intr-poll-retry>Retry</button>';
      const retry=pollStatus.querySelector('[data-intr-poll-retry]');
      if(retry)retry.onclick=()=>{intrPollError='';pollStatus.textContent='Retrying…';scheduleIntr();};
    }else pollStatus.textContent='';
  }
  // progress bar
  const bar=$('#intrProgBar'),fill=$('#intrProgFill');
  if(bar&&fill){bar.style.display=(running||total)?'block':'none';fill.style.width=total?Math.round(done/total*100)+'%':'0';}
  // results summary (flagged count)
  const stats=$('#intrStats');
  if(stats){
    const fl=displayRes.filter(r=>r.flagged).length, int=displayRes.filter(intrIsInteresting).length;
    const shown=intrApplyFilter(displayRes).length;
    stats.textContent=displayRes.length?`${displayRes.length} sent${fl?' · '+fl+' flagged':''}${int&&intrFilter!=='interesting'?' · '+int+' interesting':''}${intrFilter!=='all'?' · showing '+shown:''}`:'';
  }
  const box=$('#intrResults');
  if(st.error){box.innerHTML='<div class="state-error"><div class="state-error-icon"><svg class="icon" aria-hidden="true" focusable="false"><use href="#i-warning"/></svg></div><div class="state-error-msg">'+esc(st.error)+'</div></div>';return;}
  if(!displayRes.length){
    box.innerHTML=running?'<div class="hint" style="padding:12px">sending…</div>':INTR_RESULTS_EMPTY;
    return;
  }
  const view=intrApplyFilter(displayRes);
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
  syncButtonGroupTabStops(seg);
  renderIntr({running:intrLastRunning,total:intrLastTotal,done:intrLastDone,results:intrDisplayedResults},{authoritative:false});
});}
wireButtonGroupKeys($('#intrResFilter'));
async function intrToFinding(){
  const pool=intrApplyFilter(intrDisplayedResults);
  const withFlow=pool.filter(r=>(r.flowId||r.flowID)>0);
  const interesting=withFlow.filter(intrIsInteresting);
  const pick=interesting.length?interesting:withFlow.slice(0,10);
  if(!pick.length){toast('no attempts with captured flows to attach','warn');return;}
  const displayTarget=intrDisplayedTarget||$('#intrTarget').value||'';
  const title=await uiPrompt({title:'Create finding from Intruder',placeholder:'e.g. IDOR on /api/users?id=',value:(displayTarget||'Intruder finding').replace(/^https?:\/\//,'')});
  if(!title)return;
  try{
    const body={
      title, severity:'medium', status:'needs_verification', source:'human',
      target:displayTarget,
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
    <div class="pl">${esc(r.payload)}${r.flagged?' <svg class="icon" aria-hidden="true" focusable="false"><use href="#i-flag"/></svg>':''}${r.anomaly?' <span class="intr-anomaly" title="length anomaly">∿</span>':''}${r.matched?' <span title="grep matched">✓</span>':''}${r.extracted?' <span class="ext" title="extracted">→ '+esc(r.extracted)+'</span>':''}</div>
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
export async function sendToIntruder(f){
  if(!await waitForWorkstationReady())return false;
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
