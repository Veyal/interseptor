import { $, $$, esc, escAttr, state, toast, api, methodColor, statusColor, statusText, mimeLabel, fmtSize, fmtBytes, fmtTime, fmtDur, FLAG_WS, FLAG_TLS, FLAG_AI, FLAG_DISCOVERY, RENDER_CAP, highlightHTTP, prettify, copyText, uiPrompt, uiConfirm, closeModals, openModal, closeModal, isBinaryMime, bodyMime, headerBlockText, hideCtxMenu, openCtxMenu, flowBodyDownloadName, flowBodyDownloadHref, selectionWithin, wireSelectionDecode, wireRowKey, createFlowStore, loadFlowStore, upsertFlow as storeUpsertFlow, appendFlows, dropFlowsFrom, createVirtualList } from './core.js';
import { flowFindings, addFlowToFinding, openFinding, updateFindPocBtn } from './findings.js';
import { tagChipStyle, renderTagBar, tagActionTargets, mutateFlowTags, openTagChipMenu } from './tags.js';
import { sendToRepeater, sendToIntruder, repNewTab, renderRepTabs, repLoadEditor, repPersist, repTitle, headersToText } from './tools.js';
import { retentionStats, loadRetention } from './settings.js';
import { openAi } from './ai.js';
import { openAuthz } from './authz.js';
import { openDecoder, prefillScanner } from './scanner.js';
import { prefillDiscovery } from './discovery.js';
import { focusMapSearch } from './map.js';
import { getStartedDiagnosisHint, loadTrafficDiagnosis, onFlowMaybeTLS } from './tlsdiag.js';

// Authz identity cache for the "Send as" context-menu section. Loaded once at
// startup and refreshed whenever identities are saved in the authz modal.
let _authzIdsCache = [];
export function refreshAuthzIds(){
  api('/api/authz').then(d=>{ _authzIdsCache=(d.identities||[]).filter(id=>id.name||id.headers); }).catch(()=>{});
}
refreshAuthzIds();

// Strip Cookie/Authorization from a raw "Key: Value\n…" headers string, then
// append the identity's auth lines — used when loading a flow "as" a role.
const _AUTH_HDR_RE=/^(cookie|authorization|x-auth-token|proxy-authorization):/i;
function applyIdentityToHeaders(hdrsText, identityHdrs){
  const filtered=(hdrsText||'').split('\n').filter(l=>!_AUTH_HDR_RE.test(l.trim())).join('\n').trimEnd();
  if(!identityHdrs||!identityHdrs.trim())return filtered;
  return filtered+'\n'+identityHdrs.trim();
}

async function sendAsIdentity(f, id){
  document.querySelector('.tab[data-tab="repeater"]').click();
  const t=repNewTab();
  try{
    const d=await api('/api/flows/'+f.id);
    const def=(d.scheme==='https'&&d.port===443)||(d.scheme==='http'&&d.port===80);
    t.method=d.method;
    t.url=`${d.scheme}://${d.host}${def?'':':'+d.port}${d.path}`;
    t.headers=applyIdentityToHeaders(headersToText(d.reqHeaders),id.headers||'');
    const raw=await api('/api/flows/'+f.id+'/raw?side=req');
    const i2=raw.indexOf('\r\n\r\n');t.body=i2>=0?raw.slice(i2+4):'';
    t.resId=null;t.status='';t.color='';
    t.title=repTitle(t)+(id.name?' ['+id.name+']':'');
    renderRepTabs();repLoadEditor();repPersist();
    toast('loaded #'+f.id+' as '+( id.name||'identity')+' · Repeater');
  }catch(e){toast(e.message);}
}

const FLOW_PAGE=250;            // primary page size shown in History
const FLOW_BUFFER=50;           // extra rows prefetched ahead of scroll (reduces load-more lag)
const FLOW_FETCH=FLOW_PAGE+FLOW_BUFFER;
const ROW_H=28;                 // virtualized row height (px)
const VIRT_MIN=120;             // virtualize when more rows than this
const VIRT_BUF=40;
const MAX_LIVE_FLOWS=5000;      // cap the in-memory live list so long capture sessions don't grow unbounded (older rows stay on the server, reachable via scroll paging)
let flowHasMore=false;         // the server may have older flows past what's loaded
let loadingMore=false;         // a scroll-triggered page fetch is in flight
const EXCLUDE_NORM=64|128|512; // repeater, intruder, active scan
const FLOW_COLS_KEY='proxy.cols';
const HIDE_TLS_KEY='proxy.hideTlsFailed';
function loadProxyPrefs(){
  try{state.hideTlsFailed=localStorage.getItem(HIDE_TLS_KEY)!=='0';}catch(e){state.hideTlsFailed=true;}
}
loadProxyPrefs();
function syncHideTlsFilter(){
  const btn=$('#hideTlsFilter');
  if(!btn)return;
  btn.classList.toggle('on',state.hideTlsFailed);
  btn.setAttribute('aria-pressed',state.hideTlsFailed?'true':'false');
  btn.title=state.hideTlsFailed?'TLS handshake failures hidden — click to show PIN rows':'Showing TLS handshake failures — click to hide PIN rows';
}
export function setShowTlsFailed(show){
  state.hideTlsFailed=!show;
  try{localStorage.setItem(HIDE_TLS_KEY,state.hideTlsFailed?'1':'0');}catch(e){}
  syncHideTlsFilter();
  renderChips();
}
const FLOW_COLUMNS=[
  {key:'id',label:'#',sort:'id',w:'44px'},
  {key:'method',label:'Method',sort:'method',w:'64px'},
  {key:'host',label:'Host',sort:'host',w:'minmax(110px,1.2fr)'},
  {key:'path',label:'Path',sort:'path',w:'minmax(150px,2.4fr)'},
  {key:'status',label:'St',sort:'status',w:'52px',align:'center'},
  {key:'mime',label:'Type',sort:'mime',w:'70px',defaultVisible:false},
  {key:'size',label:'Size',sort:'size',w:'64px',align:'right'},
  {key:'time',label:'Time',sort:'time',w:'60px',align:'right'},
];
function defaultFlowCols(){return FLOW_COLUMNS.filter(c=>c.defaultVisible!==false).map(c=>c.key);}
function normalizeFlowCols(cols){
  const set=new Set(cols);
  return FLOW_COLUMNS.map(c=>c.key).filter(k=>set.has(k));
}
function flowColGrid(){return state.flowCols.map(k=>FLOW_COLUMNS.find(c=>c.key===k).w).join(' ');}
function loadFlowCols(){
  try{
    const raw=JSON.parse(localStorage.getItem(FLOW_COLS_KEY)||'null');
    if(Array.isArray(raw)&&raw.length){
      const valid=normalizeFlowCols(raw.filter(k=>FLOW_COLUMNS.some(c=>c.key===k)));
      state.flowCols=valid.length?valid:defaultFlowCols();
    }else state.flowCols=defaultFlowCols();
  }catch(e){state.flowCols=defaultFlowCols();}
}
function saveFlowCols(){try{localStorage.setItem(FLOW_COLS_KEY,JSON.stringify(state.flowCols));}catch(e){}}
function wireFlowSort(){
  const toggle=h=>{
    const k=h.dataset.sort;
    if(state.sort.key===k)state.sort.dir*=-1;
    else{state.sort.key=k;state.sort.dir=k==='id'||k==='time'?-1:1;}
    renderFlowHead();
    loadFlows();
  };
  $$('.thead [data-sort]').forEach(h=>{
    // Sortable headers are mouse-only by default — promote to buttons so they're
    // keyboard-operable and announced as such.
    h.setAttribute('role','button');
    h.tabIndex=0;
    h.addEventListener('keydown',e=>{if(e.key==='Enter'||e.key===' '){e.preventDefault();toggle(h);}});
    h.onclick=()=>toggle(h);
  });
}
function sortDirParam(){return state.sort.dir>0?'asc':'desc';}
// flowSortValue matches the server's sort key (for keyset cursors).
function flowSortValue(f){
  const k=state.sort.key;
  if(k==='size')return String(f.resLen||0);
  if(k==='time')return String(f.ts||0);
  if(k==='status')return String(f.status||0);
  if(k==='method')return f.method||'';
  if(k==='host')return (f.host||'').toLowerCase();
  if(k==='path')return (f.path||'').toLowerCase();
  if(k==='mime')return (f.mime||'').toLowerCase();
  return String(f.id);
}
function appendFlowCursor(q,flow){
  q.set('curId',String(flow.id));
  if(state.sort.key!=='id')q.set('curVal',flowSortValue(flow));
}
function sortIsLiveDefault(){return state.sort.key==='id'&&state.sort.dir===-1;}
export function renderFlowHead(){
  const head=$('#flowHead')||$('.thead');
  if(!head)return;
  const grid=flowColGrid();
  head.style.gridTemplateColumns=grid;
  head.innerHTML=state.flowCols.map(k=>{
    const c=FLOW_COLUMNS.find(x=>x.key===k);
    const align=c.align?` style="text-align:${c.align}"`:'';
    const title=k==='id'?' title="Shift+click range · Ctrl+Shift+click toggle · Ctrl+Shift+A select all"':'';
    const sk=state.sort.key,sd=state.sort.dir;
    const sorted=c.sort===sk?` sorted${sd>0?' asc':' desc'}`:'';
    const arrow=c.sort===sk?(sd>0?' ▲':' ▼'):'';
    return `<div class="${sorted.trim()}" data-sort="${c.sort}"${align}${title}>${esc(c.label)}${arrow}</div>`;
  }).join('');
  wireFlowSort();
}
function setFlowCol(key,on){
  if(on){
    if(state.flowCols.includes(key))return;
    state.flowCols=normalizeFlowCols([...state.flowCols,key]);
  }else{
    if(state.flowCols.length<=1){toast('at least one column must stay visible');return;}
    state.flowCols=state.flowCols.filter(k=>k!==key);
  }
  saveFlowCols();
  renderFlowHead();
  renderRows();
  renderColPicker();
}
function renderColPicker(){
  const menu=$('#colPicker');
  if(!menu)return;
  menu.innerHTML=FLOW_COLUMNS.map(c=>`<label><input type="checkbox" data-col="${c.key}"${state.flowCols.includes(c.key)?' checked':''}> ${esc(c.label)}</label>`).join('');
  menu.querySelectorAll('input[data-col]').forEach(inp=>inp.onchange=()=>setFlowCol(inp.dataset.col,inp.checked));
}
function toggleColPicker(){
  const menu=$('#colPicker'),btn=$('#colPickerBtn');
  if(!menu||!btn)return;
  const open=menu.style.display==='none'||!menu.style.display;
  if(open){
    renderColPicker();
    const r=btn.getBoundingClientRect();
    menu.style.display='block';
    const left=Math.min(r.left,window.innerWidth-menu.offsetWidth-8);
    menu.style.left=Math.max(8,left)+'px';
    menu.style.top=(r.bottom+4)+'px';
    btn.setAttribute('aria-expanded','true');
  }else{
    menu.style.display='none';
    btn.setAttribute('aria-expanded','false');
  }
}

function flowExcluded(f){return (f.flags&EXCLUDE_NORM)!==0&&(f.flags&FLAG_AI)===0;}
function canIncremental(){
  if(state.inScopeOnly)return false;
  if(state.filters.search)return false;
  if(state.filters.exclude&&state.filters.exclude.length)return false;
  return true;
}
function flowMatchesFilters(f){
  const fl=state.filters;
  if(flowExcluded(f))return false;
  if(state.hideTlsFailed&&(f.flags&FLAG_TLS)&&state.filters.tag!=='tls-failed')return false;
  if(!state.showManual&&!(f.flags&FLAG_AI))return false;
  if(!state.showAI&&(f.flags&FLAG_AI))return false;
  if(fl.scheme&&f.scheme!==fl.scheme)return false;
  if(fl.method&&f.method!==fl.method)return false;
  if(fl.host&&!f.host.toLowerCase().includes(fl.host.toLowerCase()))return false;
  if(fl.status&&Math.floor((f.status||0)/100)!==Number(fl.status))return false;
  for(const e of fl.exclude||[]){
    const v=String(e.value);
    if(e.field==='method'&&f.method===v)return false;
    if(e.field==='host'&&f.host.toLowerCase().includes(v.toLowerCase()))return false;
    if(e.field==='path'&&f.path.toLowerCase().includes(v.toLowerCase()))return false;
    if(e.field==='status'&&String(f.status)===v)return false;
  }
  return true;
}
function flowRowHTML(f){
  const intercepted=(f.flags&1)!==0;
  const pending=!f.status&&!f.error;
  const hasNote=!!(f.note&&String(f.note).trim());
  const stHTML=f.status?String(f.status):(f.error?(f.flags&FLAG_TLS?'<span title="TLS MITM failed — likely SSL pinning or untrusted CA">PIN</span>':'ERR'):'<span class="blink" style="color:var(--fg3)" title="waiting for response">•••</span>');
  const grid=flowColGrid();
  const rowTitle=(pending?'[pending] ':'')+(hasNote?String(f.note).trim()+' · ':'')+'Click inspect · Shift+click range · Ctrl/Cmd+click toggle';
  const cells={
    id:`<div class="tr-id" data-field="id">${f.id}</div>`,
    method:`<div class="tr-m" data-field="method" style="color:${methodColor(f.method)}">${esc(f.method)}</div>`,
    host:`<div class="tr-host" data-field="host">${esc(f.scheme==='https'?'🔒 ':'')}${esc(f.host)}</div>`,
    path:`<div class="tr-path" data-field="path">${esc(f.path)}${intercepted?' <span style="color:var(--accent)" title="intercepted">●</span>':''}${(f.flags&FLAG_TLS)?'<span class="ai-tag" style="background:var(--redDim);color:var(--red)" title="TLS handshake failed — SSL pinning or untrusted CA">PIN</span>':''}${(f.flags&FLAG_AI)?'<span class="ai-tag" title="sent by the AI assistant">AI</span>':''}${(f.flags&FLAG_DISCOVERY)?'<span class="ai-tag" style="background:var(--violetDim);color:var(--violet)" title="found by content discovery">DSC</span>':''}${(f.tags||[]).map(t=>`<span class="flowtag" data-tagchip="${escAttr(t)}" style="${tagChipStyle(t)}" title="filter by tag ${escAttr(t)}">${esc(t)}</span>`).join('')}</div>`,
    status:`<div class="tr-st" data-field="status" style="color:${statusColor(f.status)}">${stHTML}</div>`,
    mime:`<div class="tr-mime" data-field="mime">${esc(mimeLabel(f.mime))}</div>`,
    size:`<div class="tr-len" data-field="size">${f.status?fmtSize(f.resLen):''}</div>`,
    time:`<div class="tr-t" data-field="time">${fmtTime(f.ts)}</div>`,
  };
  return `<div class="trow ${f.id===state.selId?'sel':''}${state.selected.has(f.id)?' msel':''}${pending?' pending':''}${hasNote?' has-note':''}" data-id="${f.id}" style="grid-template-columns:${grid}" title="${escAttr(rowTitle)}">
      ${state.flowCols.map(k=>cells[k]).join('')}
    </div>`;
}
function wireFlowRow(r){
  const id=Number(r.dataset.id);
  r.onclick=e=>flowRowClick(id,e);
  wireRowKey(r,()=>flowRowClick(id,{})); // Enter/Space inspects the focused row
  r.setAttribute('aria-label','flow '+id);
  r.querySelectorAll('.flowtag').forEach(chip=>{
    const t=chip.dataset.tagchip;
    chip.setAttribute('role','button');
    chip.tabIndex=0;
    chip.setAttribute('aria-label','filter by tag '+t);
    chip.addEventListener('keydown',e=>{
      if(e.key==='Enter'||e.key===' '){e.preventDefault();e.stopPropagation();filterByTag(t);}
    });
    chip.oncontextmenu=e=>{
      e.preventDefault();e.stopPropagation();
      openTagChipMenu(e.clientX,e.clientY,t,id);
    };
  });
  r.oncontextmenu=e=>{
    e.preventDefault();
    const f=flowStore.byId.get(id);
    const cell=e.target.closest('[data-field]');
    showCtx(e.clientX,e.clientY,f,cell?cell.dataset.field:'');
  };
}
function updateTruncBanner(){
  const b=$('#flowCapBanner');
  if(!b)return;
  // No hard cap anymore — older flows stream in as you scroll. Show a subtle
  // affordance only while a page is loading or when more remain below.
  if(loadingMore){b.style.display='block';b.textContent='Loading older flows…';}
  else if(flowHasMore){b.style.display='block';b.textContent='Scroll down to load older flows.';}
  else b.style.display='none';
}
// Infinite scroll: load the next older page as the History list nears its bottom.
{const box=$('#rows');if(box)box.addEventListener('scroll',()=>{
  if(box.scrollTop+box.clientHeight>=box.scrollHeight-400)loadMoreFlows();
});}
export function patchFlowRow(f){
  const row=document.querySelector('#rows .trow[data-id="'+f.id+'"]');
  if(row){
    const tmp=document.createElement('div');
    tmp.innerHTML=flowRowHTML(f);
    const nr=tmp.firstElementChild;
    wireFlowRow(nr);
    row.replaceWith(nr);
    return;
  }
  if(!flowMatchesFilters(f))return;
  const box=$('#rows');
  if(!box||box.querySelector('.state-empty')||box.querySelector('#gsMcp')){renderRows();return;}
  const tmp=document.createElement('div');
  tmp.innerHTML=flowRowHTML(f);
  const nr=tmp.firstElementChild;
  wireFlowRow(nr);
  const sorted=state.flows;
  const idx=sorted.findIndex(x=>x.id===f.id);
  const next=sorted[idx+1];
  if(next){
    const anchor=document.querySelector('#rows .trow[data-id="'+next.id+'"]');
    if(anchor)anchor.before(nr);else box.prepend(nr);
  }else box.prepend(nr);
}
// upsertFlow is Proxy's live-update policy layered on the generic flowStore
// primitive from core.js: it decides *whether* a brand-new flow should be
// inserted at all (only when the live-default sort order applies — otherwise a
// full reload is needed to place it correctly), tracks newly-seen methods for
// the method filter, and enforces the MAX_LIVE_FLOWS memory cap. The actual
// Map/array bookkeeping is delegated to storeUpsertFlow/dropFlowsFrom.
export function upsertFlow(f){
  const ex=flowStore.byId.get(f.id);
  if(ex){
    storeUpsertFlow(flowStore,f); // refresh the object in place — state.flows holds `ex`
  } else if(sortIsLiveDefault()){
    storeUpsertFlow(flowStore,f);
    if(f.method && !seenMethods.has(f.method)){ seenMethods.add(f.method); methodsDirty=true; }
    // Bound memory on long live sessions: drop the oldest rows past the cap.
    // They remain on the server and reload when the user scrolls to the bottom.
    if(state.flows.length>MAX_LIVE_FLOWS){
      dropFlowsFrom(flowStore,MAX_LIVE_FLOWS).forEach(d=>{ if(state.selected)state.selected.delete(d.id); });
      flowHasMore=true;
    }
  } else { scheduleReload(); return; }
  $('#rowCount').textContent=state.flows.length;
}
let liveRenderQueued=false;
function flowRowLiveUpdate(f){
  if(state.flows.length>=VIRT_MIN){
    // Virtualized mode: a per-event full window rebuild janks under heavy traffic
    // (one renderRows per flow). Coalesce — many events per frame collapse to one.
    if(liveRenderQueued)return;
    liveRenderQueued=true;
    requestAnimationFrame(()=>{liveRenderQueued=false;renderRows();});
    return;
  }
  patchFlowRow(f);
}
export function handleFlowNew(f){
  if(!f)return;
  onFlowMaybeTLS(f);
  if(!sortIsLiveDefault()||!canIncremental()||!flowMatchesFilters(f)){scheduleReload();return;}
  upsertFlow(f);
  refreshMethodFilter();
  const proxy=document.querySelector('.panel[data-panel="proxy"]');
  if(!proxy||!proxy.classList.contains('active'))return;
  flowRowLiveUpdate(f);
}
export function handleFlowUpdate(f){
  if(!f)return;
  onFlowMaybeTLS(f);
  if(flowStore.byId.has(f.id)){
    storeUpsertFlow(flowStore,f); // O(1) in-place refresh — no findIndex over the loaded list
    const proxy=document.querySelector('.panel[data-panel="proxy"]');
    if(!proxy||!proxy.classList.contains('active'))return;
    flowRowLiveUpdate(f);
    return;
  }
  if(canIncremental()&&flowMatchesFilters(f)){upsertFlow(f);refreshMethodFilter();flowRowLiveUpdate(f);}
  else scheduleReload();
}

export function getStartedCard(){
  const diag=getStartedDiagnosisHint();
  return `<div style="max-width:640px;margin:26px auto;padding:0 16px">
    <div style="font-size:14px;font-weight:700;color:var(--fg);margin-bottom:4px">No traffic yet — let's capture some</div>
    <div class="hint" style="margin-bottom:14px">Interceptor sits between your client and the internet; point traffic at it and it shows up here live.</div>
    ${diag}
    <ol style="color:var(--fg2);line-height:2;font-size:12.5px;padding-left:20px;margin:0">
      <li>Point your browser/client at the proxy <b style="color:var(--accent);font-family:var(--mono)">${esc(state.proxyAddr)}</b>${navigator.platform&&/win/i.test(navigator.platform)?' — Windows: Settings → Network → Proxy → manual <b>127.0.0.1:8080</b> (or <code>netsh winhttp set proxy 127.0.0.1:8080</code> for system-wide)':''}</li>
      <li><b>Mobile:</b> Settings → TLS → <b>Android (ADB)</b> → Setup all. User CAs are ignored by most Android apps — pinning needs Frida or a patched APK.</li>
      <li>To intercept <b>HTTPS</b>, <a href="/api/ca.crt" download style="color:var(--accent)">download the CA</a> and trust it (details in Settings)</li>
      <li>Browse — flows stream in here. Red <b>PIN</b> rows mean SSL pinning or untrusted CA blocked the handshake.</li>
      <li><b style="color:var(--fg)">Right-click</b> a row to filter, copy as cURL, send to Repeater/Intruder${state.aiDisabled?'':', or ✨ ask AI'}</li>
      ${state.aiDisabled?'':`<li>Using an AI assistant? <button id="gsMcp" class="btn accent" style="padding:2px 9px;vertical-align:middle">Connect it via MCP</button></li>`}
    </ol>
    <div class="hint" style="margin-top:14px">Tip: press <b style="color:var(--fg)">Ctrl/⌘ K</b> for the command palette — jump to any tab, search flows, or run an action.</div></div>`;
}
// The request/response inspector is only useful once a flow is picked. Until then
// it's ~40% of the screen showing two "select a flow" placeholders while the flow
// list — the thing you actually scan — is squeezed. So we hide the inspector (and
// its splitter + note bar) whenever nothing is selected, letting #rows (flex:1)
// take the full height. It reappears the instant a row is clicked — the same
// detail-on-demand pattern as Chrome DevTools' Network panel and Burp's history.
export function syncInspectorVisibility(){
  const has=!!state.selId;
  const insp=$('#inspect'),spl=$('#inspectSplitter'),nb=$('#noteBar');
  if(insp)insp.style.display=has?'flex':'none';
  if(spl)spl.style.display=has?'':'none';
  if(nb&&!has)nb.style.display='none';
}
// flowVirt owns the Proxy history table's windowed-rendering bookkeeping
// (scroll binding + rAF coalescing); computeWindow() below runs the same
// windowing math the hand-rolled version used (start/end/topPad/bottomPad),
// just centralized in core.js so future panels can share it.
const flowVirt=createVirtualList({container:$('#rows'),itemHeight:ROW_H,threshold:VIRT_MIN,buffer:VIRT_BUF,onScroll:renderRows});
export function renderRows(){
  syncInspectorVisibility();
  const box=$('#rows');
  const flows=state.flows;
  $('#rowCount').textContent=state.flows.length;
  if(!flows.length){
    if(anyFilter()||state.inScopeOnly){
      box.innerHTML='<div class="state-empty"><div class="state-empty-icon">🔍</div><div class="state-empty-title">No flows match</div><p class="state-empty-hint">No flows match the current filters.</p><button class="btn" id="emptyClear">Clear filters</button></div>';
      const c=document.getElementById('emptyClear');if(c)c.onclick=()=>{
        if(state.inScopeOnly){state.inScopeOnly=false;const st=$('#scopeToggle');if(st){st.classList.remove('on');st.setAttribute('aria-pressed','false');st.textContent='◎ in scope';}}
        clearAllFilters();
      };
    }else{
      box.innerHTML=getStartedCard();
      const b=document.getElementById('gsMcp');if(b)b.onclick=()=>{document.querySelector('.tab[data-tab="settings"]')?.click();document.querySelector('#setNav button[data-sec="api"]')?.click();document.querySelector('#apiSub button[data-s="mcp"]')?.click();};
    }
    return;}
  const win=flowVirt.computeWindow(flows.length);
  if(win){
    box.innerHTML=`<div style="height:${win.topPad}px" aria-hidden="true"></div>`+flows.slice(win.start,win.end).map(f=>flowRowHTML(f)).join('')+`<div style="height:${win.bottomPad}px" aria-hidden="true"></div>`;
    $$('#rows .trow').forEach(wireFlowRow);
    return;
  }
  box.innerHTML=flows.map(f=>flowRowHTML(f)).join('');
  $$('#rows .trow').forEach(wireFlowRow);
}
export function flowRowClick(id,e){
  // A click on a tag chip filters History by that tag instead of inspecting the row.
  const chip=e&&e.target&&e.target.closest&&e.target.closest('.flowtag');
  if(chip){filterByTag(chip.dataset.tagchip);return;}
  const list=state.flows,idx=list.findIndex(f=>f.id===id);
  if(idx<0)return;
  const mod=e.ctrlKey||e.metaKey;
  if(mod){
    // Ctrl/Cmd-click toggles this single row in/out of the multi-selection
    // (non-contiguous pick), without disturbing the rest. Seed the set with the
    // currently-inspected row first, so Ctrl-clicking a second row keeps the first
    // (plain-clicked) one selected too — not just the Ctrl-clicked one.
    if(state.selected.size===0&&state.selId!=null&&state.selId!==id)state.selected.add(state.selId);
    state.selected.has(id)?state.selected.delete(id):state.selected.add(id);
    state.lastSelIdx=idx;selectFlow(id);updateSelBar();return;
  }
  if(e.shiftKey){
    const anchor=state.lastSelIdx>=0?state.lastSelIdx:idx;
    const a=Math.min(anchor,idx),b=Math.max(anchor,idx);
    state.selected.clear();
    for(let i=a;i<=b;i++)state.selected.add(list[i].id);
    state.lastSelIdx=idx;selectFlow(id);updateSelBar();return;
  }
  state.selected.clear();state.lastSelIdx=idx;selectFlow(id);updateSelBar();
}
export function walkFlowNav(down,e){
  const list=state.flows;
  if(!list.length)return null;
  const i=list.findIndex(f=>f.id===state.selId);
  const ni=i<0?0:(down?Math.min(i+1,list.length-1):Math.max(i-1,0));
  if(ni===i)return null;
  const id=list[ni].id,mod=e.ctrlKey||e.metaKey;
  if(mod){
    state.selected.has(id)?state.selected.delete(id):state.selected.add(id);
    state.lastSelIdx=ni;selectFlow(id);updateSelBar();return id;
  }
  if(e.shiftKey){
    const anchor=state.lastSelIdx>=0?state.lastSelIdx:(i>=0?i:ni);
    const a=Math.min(anchor,ni),b=Math.max(anchor,ni);
    state.selected.clear();
    for(let j=a;j<=b;j++)state.selected.add(list[j].id);
    state.lastSelIdx=ni;selectFlow(id);updateSelBar();return id;
  }
  state.selected.clear();state.lastSelIdx=ni;selectFlow(id);updateSelBar();return id;
}
export function toggleSelectAllShown(){
  const list=state.flows;
  const all=list.length>0&&list.every(f=>state.selected.has(f.id));
  if(all)state.selected.clear();else list.forEach(f=>state.selected.add(f.id));
  updateSelBar();renderRows();
}
// buildFlowParams encodes the active filters into a query (without limit/cursor),
// shared by the initial load and the scroll-triggered page loads.
function buildFlowParams(){
  const q=new URLSearchParams();
  const f=state.filters;
  if(f.scheme)q.set('scheme',f.scheme);
  if(f.search){
    q.set('search',f.search);
    if(f.searchScope==='body')q.set('searchScope','body');
    else if(f.searchScope==='id')q.set('searchScope','id');
  }
  if(state.notesOnly)q.set('hasNote','1');
  if(f.method)q.set('method',f.method);
  if(f.status)q.set('status',f.status);
  if(f.host)q.set('host',f.host);
  if(f.tag)q.set('tag',f.tag);
  (f.exclude||[]).forEach(e=>{const k={method:'notMethod',host:'notHost',path:'notPath',status:'notStatus'}[e.field];if(k)q.append(k,e.value);});
  if(state.inScopeOnly)q.set('inScope','1');
  if(!state.showManual)q.set('manual','0');
  if(!state.showAI)q.set('ai','0');
  if(state.hideTlsFailed&&f.tag!=='tls-failed')q.set('hideTlsFailed','1');
  q.set('sort',state.sort.key);
  q.set('dir',sortDirParam());
  return q;
}
// bodySearchActive: body search resolves a bounded id set server-side, so it isn't
// cursor-paginated — load-more is disabled for it.
function bodySearchActive(){return state.filters.searchScope==='body'&&!!state.filters.search.trim();}

export async function loadFlows(){
  const q=buildFlowParams();
  q.set('limit',String(FLOW_FETCH+1)); // +1 row tells us whether more exist
  try{
    const d=await api('/api/flows?'+q.toString());
    let flows=d.flows||[];
    flowHasMore=flows.length>FLOW_FETCH&&!bodySearchActive();
    if(flows.length>FLOW_FETCH)flows=flows.slice(0,FLOW_FETCH);
    loadFlowStore(flowStore,flows);
    state.flows=flowStore.order;
    seenMethods.clear(); flows.forEach(f=>{ if(f.method) seenMethods.add(f.method); }); methodsDirty=true;
    state.flowSearchNote=d.searchNote||'';
    const box=$('#rows');if(box)box.scrollTop=0;
    renderRows();
    updateTruncBanner();
    refreshMethodFilter();
    loadTrafficDiagnosis();
  }catch(e){toast('flows: '+e.message);}
}

// loadMoreFlows appends the next page (keyset cursor = last visible row) when the
// user scrolls near the bottom. Scroll position is preserved across the re-render.
export async function loadMoreFlows(){
  if(loadingMore||!flowHasMore||!state.flows.length)return;
  loadingMore=true;
  updateTruncBanner();
  try{
    const last=state.flows[state.flows.length-1];
    if(!last){flowHasMore=false;return;}
    const q=buildFlowParams();
    appendFlowCursor(q,last);
    q.set('limit',String(FLOW_FETCH+1));
    const d=await api('/api/flows?'+q.toString());
    let flows=d.flows||[];
    flowHasMore=flows.length>FLOW_FETCH;
    if(flows.length>FLOW_FETCH)flows=flows.slice(0,FLOW_FETCH);
    if(flows.length){
      // appendFlows drops any ids already present (a flow could arrive live between pages).
      const box=$('#rows');const keep=box?box.scrollTop:0;
      const add=appendFlows(flowStore,flows);
      if(add.length){
        renderRows();
        if(box)box.scrollTop=keep;
      }
    }
  }catch(e){/* a failed page-load is non-fatal; the user can scroll again */}
  finally{loadingMore=false;updateTruncBanner();}
}
function refreshMethodFilter(){
  if(state.filters.method)return; // don't shrink the list while filtering by method
  // Only rebuild when a genuinely new method has appeared — scanning all flows and
  // rebuilding the <select> on every flow event janks under heavy traffic.
  if(!methodsDirty)return;
  methodsDirty=false;
  const order=['GET','POST','PUT','PATCH','DELETE','HEAD','OPTIONS','CONNECT','TRACE'];
  const present=[...seenMethods]
    .sort((a,b)=>{const ia=order.indexOf(a),ib=order.indexOf(b);return (ia<0?99:ia)-(ib<0?99:ib)||a.localeCompare(b);});
  const sel=$('#fMethod');if(!sel)return;const cur=sel.value;
  sel.innerHTML='<option value="">method</option>'+present.map(m=>`<option ${m===cur?'selected':''}>${esc(m)}</option>`).join('');
}
const seenMethods=new Set();
let methodsDirty=true; // build the method filter once initially
// flowStore.byId: id -> flow object (the same reference held in state.flows, aka
// flowStore.order). Lets live flow events (new/update, which fire per captured
// request) do O(1) lookup+refresh instead of O(N) findIndex over the whole
// loaded list — essential once you've scrolled deep.
const flowStore=createFlowStore(state.flows);
let reloadTimer=null;
export function scheduleReload(){clearTimeout(reloadTimer);reloadTimer=setTimeout(loadFlows,150);}
export async function selectFlow(id){
  state.selId=id;renderRows();
  try{
    const d=await api('/api/flows/'+id);
    if(state.selId!==id)return; // a newer selection superseded this one mid-fetch — don't overwrite its panes
    state.detail=d;
    $('#noteInput').value=d.note||'';$('#noteBar').style.display='flex';
    await renderSide('req');
    if(d.flags&FLAG_WS){
      $('#resStatus').textContent='WebSocket frames';$('#resStatus').style.color='var(--accent)';
      await renderWSFrames(id);
    }else if(d.flags&FLAG_TLS){
      $('#resView').innerHTML=`<div style="padding:12px;color:var(--fg2);line-height:1.5"><strong style="color:var(--red)">TLS MITM failed</strong> — the app reached the proxy (CONNECT) but rejected the certificate before sending any HTTP request.<br><br>Likely <strong>SSL pinning</strong> or an untrusted CA (Android 7+ ignores user CAs).<br><br><span style="color:var(--fg3)">${esc(d.error||'')}</span><br><br>Try Frida/objection, a patched APK, or <code>android_setup</code> with <code>caMode:system</code> on an emulator.</div>`;
      $('#resStatus').textContent='TLS blocked';$('#resStatus').style.color='var(--red)';
    }else if(!d.status&&!d.error){
      // In-flight request: response not back yet. The flow.update handler
      // re-selects this flow once it lands, filling the pane in automatically.
      $('#resView').innerHTML='<span class="blink" style="color:var(--fg3)">waiting for response…</span>';
      $('#resStatus').textContent='pending';$('#resStatus').style.color='var(--fg3)';
    }else{
      await renderSide('res');
      $('#resStatus').textContent=(d.status?`${d.status} ${statusText(d.status)}`:(d.error||''))+(d.durationMs?` · ${fmtDur(d.durationMs)}`:'');
      $('#resStatus').style.color=statusColor(d.status);
    }
  }catch(e){toast('flow: '+e.message);}
}
function wsOpcode(o){return {0:'cont',1:'text',2:'bin',8:'close',9:'ping',10:'pong'}[o]||('0x'+o.toString(16));}
function wsFrameRow(dir,opcode,length,text){
  const arrow=dir==='send'?'<span style="color:var(--blue)">▲ send</span>':'<span style="color:var(--accent)">▼ recv</span>';
  const replayable=opcode===1; // text frames only — binary has no editable text to load
  return `<div class="ws-frame${replayable?' ws-frame-replay':''}"${replayable?` data-replay="${escAttr(text)}" title="Click to load this frame into the replay box"`:''} style="display:flex;gap:10px;padding:3px 0;border-bottom:1px solid var(--line)">
    <span style="width:60px;flex:none">${arrow}</span>
    <span style="width:46px;flex:none;color:var(--fg3)">${wsOpcode(opcode)}</span>
    <span style="width:58px;flex:none;color:var(--fg2);text-align:right">${length} B</span>
    <span style="color:var(--fg);overflow-wrap:anywhere;flex:1;min-width:0">${esc(text)}</span>${replayable?'<span class="hint" style="flex:none;align-self:center;white-space:nowrap">↩ load</span>':''}</div>`;
}
// wireWsFrames makes text frames click-to-replay: clicking loads that frame's text
// into the #wsMsg box (the most-expected WS-replay affordance that was missing).
function wireWsFrames(root){
  if(!root)return;
  root.querySelectorAll('.ws-frame-replay').forEach(el=>el.onclick=()=>{const m=$('#wsMsg');if(m){m.value=el.dataset.replay||'';m.focus();}});
}
function flowWsURL(d){const s=d.scheme==='https'?'wss':'ws';const def=(d.scheme==='https'&&d.port===443)||(d.scheme==='http'&&d.port===80);return `${s}://${d.host}${def?'':':'+d.port}${d.path||'/'}`;}
export async function renderWSFrames(id){
  try{
    const d=await api('/api/flows/'+id+'/ws');const frames=d.frames||[];
    const url=flowWsURL(state.detail||{});
    const box=`<div style="display:flex;gap:6px;margin-bottom:10px">
        <input id="wsMsg" placeholder="Replay a frame to ${escAttr(url)}" style="flex:1;font-family:var(--mono)">
        <button class="btn accent" id="wsSendBtn">▲ Send</button></div>
      <div id="wsReplayOut" style="margin-bottom:10px"></div>`;
    const list=frames.length?frames.map(f=>wsFrameRow(f.dir,f.opcode,f.length,f.preview)).join('')
      :'<span style="color:var(--fg3)">No frames captured yet — frames stream in live as the socket exchanges messages.</span>';
    $('#resView').innerHTML=box+list;
    wireWsFrames($('#resView'));
    const sb=document.getElementById('wsSendBtn');if(sb)sb.onclick=()=>wsReplay(url);
    const inp=document.getElementById('wsMsg');if(inp)inp.onkeydown=e=>{if(e.key==='Enter')wsReplay(url);};
  }catch(e){$('#resView').textContent='(error: '+e.message+')';}
}
async function wsReplay(url){
  const msg=($('#wsMsg')||{}).value||'';
  const out=$('#wsReplayOut');if(out)out.innerHTML='<span style="color:var(--fg3)">opening socket…</span>';
  try{
    const r=await api('/api/ws/send',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({url,message:msg})});
    const frames=r.frames||[];
    const head=`<div style="font-size:9px;font-weight:700;letter-spacing:.6px;color:var(--fg3);margin:4px 0 4px">${r.status!==101?`Handshake HTTP ${r.status} · `:''}Sent · ${frames.length} frame${frames.length===1?'':'s'} received</div>`;
    if(out){out.innerHTML=head+frames.map(f=>wsFrameRow(f.dir,f.opcode,f.len,f.text)).join('');wireWsFrames(out);}
  }catch(e){if(out)out.innerHTML='<span style="color:var(--red)">'+esc(e.message)+'</span>';}
}
// markFindInHtml wraps occurrences of the find query in <mark>, but only inside
// *text runs* of an already-escaped/highlighted HTML string — never inside a tag
// or attribute. Without this, searching for a common substring like "span" or
// "class" would insert <mark> inside the highlighter's <span class="hl-…"> tags
// and corrupt the markup. The query is escaped the same way the text was, so a
// search for "<html>" matches the visible "&lt;html&gt;".
function markFindInHtml(html,fq){
  const q=esc(fq).replace(/[.*+?^${}()|[\]\\]/g,'\\$&');
  if(!q)return {html,count:0};
  const re=new RegExp(q,'gi');
  let count=0;
  const out=html.replace(/(<[^>]*>)|([^<]+)/g,(m,tag,txt)=>tag!==undefined?m:txt.replace(re,s=>{count++;return '<mark class="find-hit">'+s+'</mark>';}));
  return {html:out,count};
}
export async function renderSide(side){
  const el=side==='req'?$('#reqView'):$('#resView');
  const dec=side==='req'?$('#reqDecode'):$('#resDecode');
  if(dec)dec.hidden=true;
  if(!state.selId){return;}
  const draw=async()=>{
    try{const raw=await api('/api/flows/'+state.selId+'/raw?side='+side);
      el._rawText=raw;
      el._pretty=state.view[side]==='pretty';
      if(side==='res'&&state.view.res==='render'&&mime&&/html/i.test(mime)){
        const i=raw.indexOf('\r\n\r\n');const body=i>=0?raw.slice(i+4):'';
        el.innerHTML=`<iframe sandbox="" title="Rendered HTML" srcdoc="${escAttr(body)}" style="width:100%;min-height:360px;border:1px solid var(--line);border-radius:6px;background:#fff"></iframe>`;
        return;
      }
      let html=highlightHTTP(state.view[side]==='pretty'?prettify(raw):raw,state.view[side]==='pretty',mime);
      const fq=($('#inspectFindIn')||{}).value;
      const stat=$('#inspectFindStat');
      if(side==='res'&&fq&&fq.length>1){
        const r=markFindInHtml(html,fq); html=r.html;
        if(stat)stat.textContent=r.count?r.count+' match'+(r.count===1?'':'es'):'no matches';
      } else if(stat){ stat.textContent=''; }
      el.innerHTML=html;
    }catch(e){el.textContent='(error: '+e.message+')';}
  };
  const len=state.detail?(side==='req'?state.detail.reqLen:state.detail.resLen):0;
  // Binary body (image/font/media/archive/…): show only the headers — the bytes
  // aren't readable as text. Built from the detail DTO, so the body isn't fetched.
  const mime=bodyMime(state.detail,side);
  // "Render" only makes sense for HTML; for JSON/images/etc. it used to silently
  // fall through to an ugly raw view. Hide the button and fall back to Pretty.
  if(side==='res'){
    const isHtml=!!mime&&/html/i.test(mime);
    const renderBtn=document.querySelector('#inspect .seg[data-side="res"] button[data-view="render"]');
    if(renderBtn)renderBtn.style.display=isHtml?'':'none';
    if(!isHtml&&state.view.res==='render'){
      state.view.res='pretty';
      const seg=document.querySelector('#inspect .seg[data-side="res"]');
      if(seg)seg.querySelectorAll('button').forEach(b=>{b.classList.toggle('on',b.dataset.view==='pretty');});
    }
  }
  if(isBinaryMime(mime)){
    const dl=flowBodyDownloadName(state.selId,side,mime), href=flowBodyDownloadHref(state.selId,side);
    el.innerHTML=highlightHTTP(headerBlockText(state.detail,side))+
      `<div class="hint" style="padding:14px 0 0;line-height:1.7">Body is <b>${esc(mime)}</b>${len?' · '+fmtSize(len):''} — binary, not rendered.<br>
        <a class="btn" style="margin-top:8px;display:inline-block" href="${href}" download="${escAttr(dl)}">⤓ Download body</a>
        <button class="btn" data-bin="1" style="margin-top:8px;margin-left:6px">Show raw anyway</button></div>`;
    const b=el.querySelector('[data-bin]');
    if(b)b.onclick=()=>{el.innerHTML='<span class="hint" style="padding:16px">rendering…</span>';setTimeout(draw,10);};
    return;
  }
  if(len>RENDER_CAP){
    const dl=flowBodyDownloadName(state.selId,side,mime), href=flowBodyDownloadHref(state.selId,side);
    el.innerHTML=`<div class="hint" style="padding:18px;line-height:1.8">${side==='req'?'Request':'Response'} body is <b>${fmtSize(len)}</b> — not shown, to keep the browser responsive.<br>
      <a class="btn" style="margin-top:8px;display:inline-block" href="${href}" download="${escAttr(dl)}">⤓ Download body</a>
      <button class="btn" data-bigshow="1" style="margin-top:8px">Show anyway</button></div>`;
    const b=el.querySelector('[data-bigshow]');
    if(b)b.onclick=()=>{el.innerHTML='<span class="hint" style="padding:16px">rendering…</span>';setTimeout(draw,10);};
    return;
  }
  await draw();
}
// Only the inspector's request/response view segs (data-side) — NOT every .seg on the
// page. Other tabs (Intruder, Repeater, AI, Map) own their own seg handlers; a bare
// $$('.seg') here would clobber them since this module loads after them.
$$('.seg[data-side]').forEach(seg=>{const side=seg.dataset.side;seg.querySelectorAll('button').forEach(b=>b.onclick=()=>{
  state.view[side]=b.dataset.view;seg.querySelectorAll('button').forEach(x=>{x.classList.toggle('on',x===b);x.setAttribute('aria-pressed',x===b?'true':'false');});renderSide(side);});});
const inspectFindBar=$('#inspectFind'),inspectFindIn=$('#inspectFindIn');
function toggleInspectFind(show){
  if(!inspectFindBar)return;
  inspectFindBar.style.display=show?'flex':'none';
  if(show&&inspectFindIn){inspectFindIn.focus();inspectFindIn.select();}
  else if(!show&&inspectFindIn){inspectFindIn.value='';renderSide('res');}
}
if(inspectFindIn){
  let inspectFindTimer=null;
  inspectFindIn.oninput=()=>{
    clearTimeout(inspectFindTimer);
    inspectFindTimer=setTimeout(()=>renderSide('res'),150);
  };
}
if($('#inspectFindClose'))$('#inspectFindClose').onclick=()=>toggleInspectFind(false);
document.addEventListener('keydown',e=>{
  if((e.ctrlKey||e.metaKey)&&e.key.toLowerCase()==='f'){
    const p=document.querySelector('.panel[data-panel="proxy"]');
    if(!p||!p.classList.contains('active'))return;
    const t=e.target;if(t&&/^(INPUT|TEXTAREA|SELECT)$/.test(t.tagName))return;
    e.preventDefault();toggleInspectFind(true);
  }
});

loadFlowCols();
renderFlowHead();
{const b=$('#colPickerBtn');if(b)b.onclick=e=>{e.stopPropagation();toggleColPicker();};}
{const m=$('#colPicker');if(m)m.onclick=e=>e.stopPropagation();}
document.addEventListener('click',()=>{const menu=$('#colPicker'),btn=$('#colPickerBtn');if(menu&&menu.style.display==='block'){menu.style.display='none';if(btn)btn.setAttribute('aria-expanded','false');}});

$('#fMethod').onchange=e=>setFilter('method',e.target.value);
$('#fStatus').onchange=e=>setFilter('status',e.target.value);
$('#fSearch').oninput=e=>{state.filters.search=e.target.value;renderChips();scheduleReload();};
function syncSearchPlaceholder(){
  const inp=$('#fSearch'),sc=state.filters.searchScope||'path';
  if(!inp)return;
  inp.placeholder=sc==='id'?'Flow id (e.g. 285 or #285)…':sc==='body'?'Search request/response bodies…':'Search method / host / path / #id…';
}
if($('#fSearchScope'))$('#fSearchScope').onchange=e=>{state.filters.searchScope=e.target.value||'path';syncSearchPlaceholder();if(state.filters.search)loadFlows();};
syncSearchPlaceholder();
if($('#notesFilter'))$('#notesFilter').onclick=()=>{state.notesOnly=!state.notesOnly;const nf=$('#notesFilter');nf.classList.toggle('on',state.notesOnly);nf.setAttribute('aria-pressed',state.notesOnly?'true':'false');loadFlows();};
if($('#hideTlsFilter'))$('#hideTlsFilter').onclick=()=>{
  const next=!state.hideTlsFailed;
  if(next&&state.filters.tag==='tls-failed')state.filters.tag='';
  state.hideTlsFailed=next;
  try{localStorage.setItem(HIDE_TLS_KEY,state.hideTlsFailed?'1':'0');}catch(e){}
  syncHideTlsFilter();renderChips();renderTagBar();loadFlows();
};
syncHideTlsFilter();
// Inspector header actions — operate on the currently-selected flow.
function inspectorFlow(){return state.detail||flowStore.byId.get(state.selId)||null;}
{const b=$('#insRepeater');if(b)b.onclick=()=>{const f=inspectorFlow();if(f)sendToRepeater(f);else toast('select a flow first');};}
{const b=$('#insIntruder');if(b)b.onclick=()=>{const f=inspectorFlow();if(f)sendToIntruder(f);else toast('select a flow first');};}
{const b=$('#insCurl');if(b)b.onclick=()=>{const f=inspectorFlow();if(f)copyCurl(f);else toast('select a flow first');};}
$('#scopeToggle').onclick=()=>{
  state.inScopeOnly=!state.inScopeOnly;
  const st=$('#scopeToggle');
  st.classList.toggle('on',state.inScopeOnly);
  st.textContent=(state.inScopeOnly?'◉':'◎')+' in scope';
  st.setAttribute('aria-pressed',state.inScopeOnly?'true':'false');
  loadFlows();
};
function syncSourceFilters(){
  const mf=$('#manualFilter'); if(!mf)return;
  mf.classList.toggle('on',state.showManual);
  mf.setAttribute('aria-pressed',state.showManual?'true':'false');
}
export { syncSourceFilters };
function toggleSourceFilter(which){
  const nextManual=which==='manual'?!state.showManual:state.showManual;
  if(!nextManual){toast('Manual flows are always shown — filter AI/other sources via the tag bar');return;}
  if(which==='manual')state.showManual=nextManual;
  syncSourceFilters();
  loadFlows();
}
$('#manualFilter')&&($('#manualFilter').onclick=()=>toggleSourceFilter('manual'));
syncSourceFilters();
export async function saveNote(){
  if(!state.selId)return;
  const note=$('#noteInput').value;
  if(state.detail&&note===(state.detail.note||''))return; // unchanged — skip redundant PUT
  try{
    await api('/api/flows/'+state.selId+'/note',{method:'PUT',headers:{'content-type':'application/json'},body:JSON.stringify({note})});
    if(state.detail)state.detail.note=note;
    const fl=flowStore.byId.get(state.selId);
    if(fl){fl.note=note;patchFlowRow(fl);}
    const s=$('#noteSaved');s.style.opacity='1';setTimeout(()=>{s.style.opacity='0';},1200);
  }catch(e){toast('note: '+e.message);}
}
$('#noteInput').addEventListener('keydown',e=>{if(e.key==='Enter'){e.preventDefault();$('#noteInput').blur();}});
$('#noteInput').addEventListener('blur',saveNote);
/* ---- saved views (one dropdown: apply / save / delete) ---- */
export async function loadViews(){try{const d=await api('/api/views');state.views=d.views||[];renderViews();}catch(e){}}
export function renderViews(){
  const btn=$('#viewsBtn'); if(!btn)return;
  const n=state.views.length;
  const txt=n?('Views ▾ · '+n):'Views ▾';
  btn.textContent=txt;
  btn.title=n?(n+' saved view'+(n===1?'':'s')+' — click to apply, save, or delete'):'No saved views yet — click to save the current filters as a view';
}
function applyView(v){
  let f={};try{f=JSON.parse(v.data||'{}');}catch(e){}
  state.filters={scheme:f.scheme||'',method:f.method||'',status:f.status||'',search:f.search||'',host:f.host||'',exclude:Array.isArray(f.exclude)?f.exclude:[]};
  state.inScopeOnly=!!f.inScope;
  syncControls();$('#scopeToggle').classList.toggle('on',state.inScopeOnly);$('#scopeToggle').setAttribute('aria-pressed',state.inScopeOnly?'true':'false');$('#scopeToggle').textContent=(state.inScopeOnly?'◉':'◎')+' in scope';
  renderChips();loadFlows();
  toast('applied view: '+v.name);
}
async function saveCurrentView(){
  const name=await uiPrompt({title:'Save current filters as a view',placeholder:'view name'});if(!name)return;
  const data={...state.filters,inScope:state.inScopeOnly};
  try{await api('/api/views',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({name,data})});toast('view saved');loadViews();}catch(e){toast(e.message);}
}
async function deleteView(id,name){
  if(!await uiConfirm('Delete view','Delete saved view <b>'+esc(name)+'</b>?','Delete','btn danger','var(--red)'))return;
  try{await api('/api/views/'+id,{method:'DELETE'});loadViews();toast('view deleted');}catch(e){toast(e.message);}
}
function openViewsMenu(){
  const btn=$('#viewsBtn'); if(!btn)return;
  const r=btn.getBoundingClientRect();
  const sections=[];
  if(state.views.length){
    sections.push({head:'APPLY VIEW',items:state.views.map(v=>({label:v.name,act:()=>applyView(v)}))});
    sections.push({head:'DELETE VIEW',items:state.views.map(v=>({label:v.name,danger:true,act:()=>deleteView(v.id,v.name)}))});
  }
  sections.push({items:[{label:'＋ Save current filters as a view…',act:saveCurrentView}]});
  openCtxMenu(r.left, r.bottom+2, sections);
}
$('#viewsBtn')&&($('#viewsBtn').onclick=e=>{e.stopPropagation();openViewsMenu();});
/* ---- target scope ---- */
export async function loadScope(){try{const d=await api('/api/scope');state.scope=d.rules||[];renderScope();}catch(e){}}
export async function addHostToScope(host){
  try{await api('/api/scope',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({action:'include',host:host,enabled:true})});
    toast('added '+host+' to scope — toggle ◎ in scope to focus');loadScope();}
  catch(e){toast(e.message);}
}
export function renderScope(){
  const body=$('#scopeBody');if(!body)return;
  const warn=$('#scopeDupWarn');
  const enabled=state.scope.filter(r=>r.enabled);
  const dup=enabled.filter((r,i,a)=>a.findIndex(x=>x.action===r.action&&x.host===r.host&&x.path===r.path&&x.scheme===r.scheme&&x.port===r.port)!==i);
  if(warn){
    if(dup.length){
      warn.style.display='block';
      warn.textContent=`Duplicate scope rule${dup.length===1?'':'s'} detected — only one is needed.`;
    }else warn.style.display='none';
  }
  if(!state.scope.length){body.innerHTML='<tr><td colspan="6" class="hint" style="padding:10px 8px">No scope rules — everything is in scope.</td></tr>';return;}
  body.innerHTML=state.scope.map(r=>`<tr data-id="${r.id}">
    <td><input type="checkbox" ${r.enabled?'checked':''} data-k="enabled"></td>
    <td><select data-k="action"><option value="include" ${r.action==='include'?'selected':''}>include</option><option value="exclude" ${r.action==='exclude'?'selected':''}>exclude</option></select></td>
    <td><input type="text" data-k="host" value="${escAttr(r.host)}" placeholder="*.acme.com"></td>
    <td><input type="text" data-k="path" value="${escAttr(r.path)}" placeholder="/"></td>
    <td><input type="text" data-k="scheme" value="${escAttr(r.scheme)}" placeholder="any"></td>
    <td><button class="btn danger" data-del="${r.id}">Delete</button></td></tr>`).join('');
  body.querySelectorAll('tr').forEach(tr=>{const id=Number(tr.dataset.id);
    tr.querySelectorAll('[data-k]').forEach(inp=>inp.addEventListener('change',()=>updateScope(id,tr)));});
  body.querySelectorAll('[data-del]').forEach(b=>b.onclick=()=>deleteScope(Number(b.dataset.del)));
}
async function updateScope(id,tr){
  const get=k=>tr.querySelector(`[data-k="${k}"]`);
  const upd={id,action:get('action').value,host:get('host').value.trim(),path:get('path').value.trim(),scheme:get('scheme').value.trim(),enabled:get('enabled').checked,port:0};
  try{await api('/api/scope/'+id,{method:'PUT',headers:{'content-type':'application/json'},body:JSON.stringify(upd)});toast('scope saved');}catch(e){toast(e.message);loadScope();}
}
async function deleteScope(id){try{await api('/api/scope/'+id,{method:'DELETE'});loadScope();}catch(e){toast(e.message);}}
$('#addScopeBtn').onclick=async()=>{
  const rule={action:$('#newScopeAction').value,host:$('#newScopeHost').value.trim(),path:$('#newScopePath').value.trim(),scheme:'',enabled:true,port:0};
  if(!rule.host&&!rule.path){toast('host or path required');return;}
  try{await api('/api/scope',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify(rule)});
    $('#newScopeHost').value='';$('#newScopePath').value='';loadScope();toast('scope rule added');}catch(e){toast(e.message);}
};
/* ---- filters: chips + apply/clear, kept in sync with the toolbar controls ---- */
export function syncControls(){
  $('#fMethod').value=state.filters.method;
  $('#fStatus').value=state.filters.status;
  $('#fSearch').value=state.filters.search;
  const ss=$('#fSearchScope');if(ss)ss.value=state.filters.searchScope||'path';
}
export function setFilter(key,val){
  if(key==='tag'&&val==='tls-failed'){
    state.hideTlsFailed=false;
    try{localStorage.setItem(HIDE_TLS_KEY,'0');}catch(e){}
  }
  state.filters[key]=val;syncControls();syncHideTlsFilter();renderChips();renderTagBar();loadFlows();
}
export function clearFilter(key){setFilter(key,'');}
export function clearAllFilters(){
  state.filters={scheme:'',search:'',searchScope:'path',method:'',status:'',host:'',tag:'',exclude:[]};
  state.notesOnly=false;
  {const nf=$('#notesFilter');if(nf){nf.classList.remove('on');nf.setAttribute('aria-pressed','false');}}
  syncControls();renderChips();loadFlows();
}
export function anyFilter(){const f=state.filters;return !!(f.scheme||f.method||f.status||f.host||f.search||f.tag||(f.exclude&&f.exclude.length));}
// filterByTag toggles the History tag filter (click a tag chip to filter; click the
// active one again to clear).
export function filterByTag(t){setFilter('tag',state.filters.tag===t?'':t);}
// parseTags splits a comma/space/semicolon-separated tag string into a list.
function parseTags(s){return String(s||'').split(/[,;\s]+/).map(x=>x.trim()).filter(Boolean);}
// tagFlowPrompt edits one flow's tags — prefilled with its current tags, so removing
// a tag is just deleting it from the field. Replaces the flow's tag set (PUT).
async function tagFlowPrompt(f){
  const cur=(f.tags||[]).join(' ');
  const v=await uiPrompt({title:'Tag flow #'+f.id,value:cur,placeholder:'space- or comma-separated, e.g. auth idor'});
  if(v==null)return;
  try{await api('/api/flows/'+f.id+'/tags',{method:'PUT',headers:{'content-type':'application/json'},body:JSON.stringify({tags:parseTags(v)})});}
  catch(e){toast(e.message);}
}
// tagSelectionPrompt ADDS tags to every selected flow (doesn't clobber existing).
async function tagSelectionPrompt(){
  const ids=[...state.selected];if(!ids.length)return;
  const v=await uiPrompt({title:'Tag '+ids.length+' selected flows',placeholder:'tags to add, e.g. auth candidate'});
  if(v==null)return;
  const add=parseTags(v);if(!add.length)return;
  await mutateFlowTags(ids,{add});
}
// tagSelectionRemovePrompt removes one tag from every selected flow.
async function tagSelectionRemovePrompt(){
  const ids=[...state.selected];if(!ids.length)return;
  const v=await uiPrompt({title:'Remove tag from '+ids.length+' selected flows',placeholder:'tag to remove, e.g. auth'});
  if(v==null)return;
  const remove=parseTags(v);if(!remove.length)return;
  await mutateFlowTags(ids,{remove});
}
// Negative filters: exclude rows matching {field,value}. Toggles off if already present.
export function addExclude(field,value){
  if(value==null||value==='')return;
  const ex=state.filters.exclude||(state.filters.exclude=[]);
  const i=ex.findIndex(e=>e.field===field&&String(e.value)===String(value));
  if(i>=0)ex.splice(i,1); else ex.push({field,value:String(value)});
  renderChips();loadFlows();
}
export function removeExclude(i){state.filters.exclude.splice(i,1);renderChips();loadFlows();}
export function renderChips(){
  const f=state.filters,box=$('#chips'),items=[];
  const add=(k,label,val)=>{if(val)items.push(`<span class="chip"><span>${label} <b>${esc(val)}</b></span><span class="x" data-clear="${k}" title="remove">✕</span></span>`);};
  add('scheme','scheme',f.scheme);
  add('method','method',f.method);
  add('status','status',f.status?f.status+'xx':'');
  add('host','host',f.host);
  add('tag','🏷',f.tag);
  add('search',f.searchScope==='body'?'body':f.searchScope==='id'?'id':'path',f.search);
  if(state.hideTlsFailed)items.push(`<span class="chip"><span>hiding <b>PIN</b> failures</span><span class="x" id="chipHideTlsClear" title="show TLS failures">✕</span></span>`);
  (f.exclude||[]).forEach((e,i)=>{items.push(`<span class="chip not"><span>${esc(e.field)} ≠ <b>${esc(e.value)}</b></span><span class="x" data-ex="${i}" title="remove">✕</span></span>`);});
  const hasFilters=items.length>0;
  if(hasFilters)items.push(`<button class="chip-clear" id="chipsClear" title="Remove all filters">Clear all ✕</button>`);
  box.innerHTML=items.join('');
  box.classList.toggle('has',hasFilters);
  box.querySelectorAll('[data-clear]').forEach(x=>x.onclick=()=>clearFilter(x.dataset.clear));
  box.querySelectorAll('[data-ex]').forEach(x=>x.onclick=()=>removeExclude(Number(x.dataset.ex)));
  const htc=$('#chipHideTlsClear');if(htc)htc.onclick=()=>{state.hideTlsFailed=false;try{localStorage.setItem(HIDE_TLS_KEY,'0');}catch(e){}syncHideTlsFilter();renderChips();loadFlows();};
  const cc=$('#chipsClear');if(cc)cc.onclick=clearAllFilters;
}
/* ---- right-click context menu ---- */
export const ctx=$('#ctxmenu');
const hideCtx=hideCtxMenu;
const openMenu=openCtxMenu;
// isIPHost reports whether h is an IP literal / localhost (so "domain" actions,
// which only make sense for DNS names, are suppressed).
function isIPHost(h){return !h||/^\d{1,3}(\.\d{1,3}){3}$/.test(h)||h.includes(':')||h==='localhost';}
// Second-level public suffixes so "domain" picks app.acme.co.uk → *.acme.co.uk,
// not the useless *.co.uk. Heuristic, not a full PSL — good enough for filtering.
const TWO_LEVEL_TLD=new Set(['co','com','org','net','gov','edu','ac','mil','or','ne','go']);
function registrableDomain(host){
  if(isIPHost(host))return '';
  const p=host.split('.').filter(Boolean);
  if(p.length<=2)return host;
  if(p.length>=3&&TWO_LEVEL_TLD.has(p[p.length-2])&&p[p.length-1].length<=3)return p.slice(-3).join('.');
  return p.slice(-2).join('.');
}
function looksLikeHost(s){return /^[a-z0-9.-]+\.[a-z]{2,}$/i.test(s)&&!s.includes(' ');}
function deleteHost(f){
  return async()=>{
    const hstats=retentionStats&&retentionStats.hosts&&retentionStats.hosts.find(x=>x.host===f.host);
    const flowCount=hstats?hstats.flows:'all';
    const confirmed=await uiConfirm('Delete flows from '+esc(f.host),
      'Permanently delete '+flowCount+' flow'+(flowCount===1?'':'s')+' from <b style="color:var(--accent)">'+esc(f.host)+'</b>?<br>This cannot be undone.',
      'Delete','btn danger','var(--red)');
    if(!confirmed)return;
    try{
      const r=await api('/api/flows/purge',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({hosts:[f.host],mode:'delete'})});
      toast('deleted '+r.deleted+' flow'+(r.deleted===1?'':'s')+' · freed '+fmtBytes(r.freedBytes));
      loadRetention();loadFlows();
    }catch(e){toast('purge: '+e.message);}
  };
}

// flowGlobalSection — the flow-wide actions present in every history/inspector
// menu regardless of which column was clicked (send, copy, AI, authz).
function flowGlobalSection(f,head){
  const items=[
    {label:'Send to Repeater',act:()=>sendToRepeater(f)},
    {label:'Send to Intruder',act:()=>sendToIntruder(f)},
    {label:'Copy URL',act:()=>copyURL(f)},
    {label:'Copy as cURL',act:()=>copyCurl(f)},
  ];
  if(!state.aiDisabled){
    items.push({sep:true},
      {label:'✨ Ask AI',act:()=>openAi({ids: [f.id]})});
  }
  items.push({sep:true},
    {label:'🔍 Scan this host',val:f.host,act:()=>prefillScanner(f.host, (f.path||'').split('?')[0])},
    {label:'🔓 Authz test',val:'roles',act:()=>openAuthz(f.id)},
    {label:'🔑 Use as login macro',act:()=>saveLoginMacroFromFlow(f.id)});
  return {head:head||'REQUEST', items};
}

async function saveLoginMacroFromFlow(id){
  try{
    await api('/api/session/login/from-flow/'+id,{method:'POST'});
    toast('login macro saved — Settings → Session');
    document.querySelector('.tab[data-tab="settings"]').click();
    const b=document.querySelector('#setNav button[data-sec="session"]');if(b)b.click();
  }catch(e){toast(e.message);}
}

// showCtx builds the history-row menu: a contextual top section keyed to the
// clicked column (host / status / method / path) + the always-present global
// flow actions. Right-clicking a host shows host/domain/scope/discover actions;
// right-clicking a status shows status filters — not the other way around.
export function showCtx(x,y,f,field){
  if(!f)return;
  const cls=f.status?Math.floor(f.status/100):0;
  const dom=registrableDomain(f.host);
  const def=(f.scheme==='https'&&f.port===443)||(f.scheme==='http'&&f.port===80);
  const baseURL=`${f.scheme}://${f.host}${def?'':':'+f.port}/`;
  const sections=[];

  if(field==='host'||field==='scheme'||field==='id'){
    const items=[
      {label:'Filter this host',val:f.host,on:field==='host',act:()=>setFilter('host',f.host)},
      {label:'Exclude this host',val:f.host,danger:true,act:()=>addExclude('host',f.host)},
    ];
    if(dom&&dom!==f.host){
      items.push({label:'Filter domain',val:dom+' (+subs)',act:()=>setFilter('host',dom)});
      items.push({label:'Add domain to scope',val:'*.'+dom,act:()=>addHostToScope('*.'+dom)});
    }
    items.push({label:'Add host to scope',val:f.host,act:()=>addHostToScope(f.host)});
    items.push({label:'🔎 Discover content',val:f.host,act:()=>prefillDiscovery(baseURL)});
    items.push({sep:true});
    items.push({label:'🗑 Delete all from host',val:f.host,danger:true,act:deleteHost(f)});
    sections.push({head:'HOST · '+f.host, items});
  }else if(field==='status'){
    const items=[];
    if(cls){
      items.push({label:'Filter status',val:cls+'xx',on:true,act:()=>setFilter('status',String(cls))});
      items.push({label:'Exclude this status',val:String(f.status),danger:true,act:()=>addExclude('status',String(f.status))});
    }else items.push({label:'No response yet',val:'pending'});
    sections.push({head:'STATUS'+(f.status?' · '+f.status:''), items});
  }else if(field==='method'){
    sections.push({head:'METHOD · '+f.method, items:[
      {label:'Filter method',val:f.method,on:true,act:()=>setFilter('method',f.method)},
      {label:'Exclude method',val:f.method,danger:true,act:()=>addExclude('method',f.method)},
    ]});
  }else if(field==='path'){
    sections.push({head:'PATH', items:[
      {label:'Filter path',val:f.path,on:true,act:()=>setFilter('search',f.path)},
      {label:'Exclude path',val:f.path,danger:true,act:()=>addExclude('path',f.path)},
      {label:'Copy path',act:()=>copyText(f.path,'path copied')},
    ]});
  }
  // mime/size/time columns have no column-specific filter — they fall through to
  // the global section below.

  sections.push(flowGlobalSection(f,'REQUEST'));
  // TAGS: filter by / remove an existing tag, or add tags (to this flow, or the whole selection).
  const tagTargets=tagActionTargets(f.id);
  const tagN=tagTargets.length;
  const tagItems=[];
  (f.tags||[]).forEach(t=>{
    tagItems.push({label:'🏷 Filter · '+t,on:state.filters.tag===t,act:()=>filterByTag(t)});
    tagItems.push({label:'✕ Remove · '+t,danger:true,val:tagN>1?tagN+' flows':'',act:()=>mutateFlowTags(tagTargets,{remove:[t]})});
  });
  const selN=(state.selected&&state.selected.size>1&&state.selected.has(f.id))?state.selected.size:0;
  tagItems.push({label:selN?('🏷 Tag '+selN+' selected…'):'🏷 Tag…',act:()=>selN?tagSelectionPrompt():tagFlowPrompt(f)});
  if(selN)tagItems.push({label:'✕ Remove tag from '+selN+' selected…',danger:true,act:()=>tagSelectionRemovePrompt()});
  sections.push({head:(f.tags||[]).length?('TAGS · '+f.tags.join(' ')):'TAGS', items:tagItems});
  const ff=flowFindings(f.id);
  const fitems=ff.map(x=>({label:'📌 '+x.title,val:x.severity,act:()=>openFinding(x.id)}));
  fitems.push({label:'➕ Add to finding',act:()=>addFlowToFinding(f.id)});
  sections.push({head:ff.length?('FINDINGS · in '+ff.length):'FINDINGS',items:fitems});
  if(anyFilter())sections.push({items:[{label:'Clear all filters',act:clearAllFilters}]});
  const sendAsIds=_authzIdsCache.filter(id=>!id.broken&&(id.name||id.headers));
  if(sendAsIds.length)sections.push({head:'SEND AS',items:sendAsIds.map(id=>({label:id.name||'(unnamed)',act:()=>sendAsIdentity(f,id)}))});
  openMenu(x,y,sections);
}

// showInspectorCtx builds the request/response pane menu: a SELECTION section
// (only when text is highlighted) for copy/decode/search/scope, plus the global
// flow actions.
export function showInspectorCtx(x,y,side){
  const f=flowStore.byId.get(state.selId)||state.detail;
  if(!f)return;
  const sel=selectionWithin($(side==='req'?'#reqView':'#resView'));
  const sections=[];
  if(sel){
    const short=sel.length>40?sel.slice(0,40)+'…':sel;
    const items=[
      {label:'Copy',act:()=>copyText(sel,'copied')},
      {label:'Decode / encode',val:short,act:()=>openDecoder(sel)},
      {label:'Search in history',val:short,act:()=>setFilter('search',sel)},
    ];
    if(looksLikeHost(sel))items.push({label:'Add to scope',val:sel,act:()=>addHostToScope(sel)});
    items.push({label:'Search in Map (body)',val:short,act:()=>focusMapSearch(sel,'body')});
    sections.push({head:'SELECTION', items});
  }
  sections.push(flowGlobalSection(f, side==='req'?'REQUEST':'RESPONSE'));
  if(!sel)sections.push({items:[{label:'Open Decoder',act:()=>openDecoder('')}]});
  const sendAsIds2=_authzIdsCache.filter(id=>!id.broken&&(id.name||id.headers));
  if(sendAsIds2.length)sections.push({head:'SEND AS',items:sendAsIds2.map(id=>({label:id.name||'(unnamed)',act:()=>sendAsIdentity(f,id)}))});
  openMenu(x,y,sections);
}
document.addEventListener('click',e=>{if(!ctx.contains(e.target))hideCtx();});
document.addEventListener('keydown',e=>{if(e.key==='Escape'){if(typeof closeModals==='function'&&closeModals())return;hideCtx();}});
// Suppress the browser's native context menu app-wide, but keep it where it's
// genuinely useful: editable fields (paste/cut) and over a live text selection (copy).
document.addEventListener('contextmenu',e=>{
  const t=e.target,tag=(t.tagName||'').toLowerCase();
  if(tag==='input'||tag==='textarea'||t.isContentEditable)return;
  const sel=window.getSelection&&window.getSelection();
  if(sel&&String(sel).length&&!sel.isCollapsed)return;
  e.preventDefault();
});
$('#rows').addEventListener('scroll',hideCtx,{passive:true});
window.addEventListener('blur',hideCtx);
// Request/response inspector panes get their own context menu (selection-aware).
// stopPropagation keeps the app-wide handler from also firing, so the native
// menu never double-shows over a selection.
['reqView','resView'].forEach(id=>{
  const el=$('#'+id);
  if(el)el.addEventListener('contextmenu',e=>{e.preventDefault();e.stopPropagation();showInspectorCtx(e.clientX,e.clientY,id==='reqView'?'req':'resp');});
});
wireSelectionDecode($('#reqView'),$('#reqDecode'),{onDecoder:openDecoder});
wireSelectionDecode($('#resView'),$('#resDecode'),{onDecoder:openDecoder});
export function flowURL(f){const def=(f.scheme==='https'&&f.port===443)||(f.scheme==='http'&&f.port===80);return `${f.scheme}://${f.host}${def?'':':'+f.port}${f.path}`;}
export function copyURL(f){copyText(flowURL(f),'URL copied');}
function shq(s){return "'"+String(s).replace(/'/g,"'\\''")+"'";}
export async function copyCurl(f){
  try{
    const d=await api('/api/flows/'+f.id);
    const parts=[`curl -x http://${state.proxyAddr}`];
    if(f.scheme==='https')parts.push('--cacert interceptor-ca.crt');
    parts.push('-X '+f.method);
    const headers=d.reqHeaders||{};
    Object.keys(headers).sort().forEach(k=>{if(k.toLowerCase()==='host')return;(headers[k]||[]).forEach(v=>parts.push('-H '+shq(k+': '+v)));});
    if(f.reqLen>0){const raw=await api('/api/flows/'+f.id+'/raw?side=req');const i=raw.indexOf('\r\n\r\n');const body=i>=0?raw.slice(i+4):'';if(body)parts.push('--data-raw '+shq(body));}
    parts.push(shq(flowURL(f)));
    copyText(parts.join(' \\\n  '),'cURL copied');
  }catch(e){toast('cURL: '+e.message);}
}
// ---- History multi-select actions ----
export function updateSelBar(){
  const n=state.selected.size;
  $('#selBar').style.display=n?'flex':'none';
  $('#selCount').textContent=n+' selected';
  const cmp=$('#selCompare');if(cmp)cmp.style.display=n===2?'':'none';
  updateFindPocBtn();
}
function compareWordDiff(a,b){
  const tok=s=>String(s||'').split(/(\s+)/);
  const ta=tok(a),tb=tok(b),rows=[];
  let i=0,j=0,n=0;
  while((i<ta.length||j<tb.length)&&n<400){
    if(ta[i]===tb[j]){if(ta[i])rows.push(`<span style="color:var(--fg3)">${esc(ta[i])}</span>`);i++;j++;}
    else{
      const la=ta[i]||'',lb=tb[j]||'';
      rows.push(`<span style="color:var(--red);background:var(--redDim)">${esc(la||'∅')}</span>`);
      rows.push(`<span style="color:var(--accent);background:var(--accentDim)">${esc(lb||'∅')}</span>`);
      i++;j++;
    }
    n++;
  }
  return `<div style="font-family:var(--mono);font-size:11px;line-height:1.55;white-space:pre-wrap;word-break:break-word">${rows.join('')}${(i<ta.length||j<tb.length)?'<span class="hint"> …truncated</span>':''}</div>`;
}
function compareHeaderDiff(ha,hb){
  const keys=new Set([...Object.keys(ha||{}),...Object.keys(hb||{})]);
  const sorted=[...keys].sort((a,b)=>a.localeCompare(b));
  if(!sorted.length)return '<div class="hint">No response headers</div>';
  const rows=sorted.map(k=>{
    const x=(ha&&ha[k]||[]).join(', '),y=(hb&&hb[k]||[]).join(', ');
    if(x===y)return `<div style="font-family:var(--mono);font-size:11px;color:var(--fg3)"><b>${esc(k)}:</b> ${esc(x||'—')}</div>`;
    return `<div style="font-family:var(--mono);font-size:11px;margin:4px 0"><div style="color:var(--red)"><b>${esc(k)}:</b> ${esc(x||'∅')}</div><div style="color:var(--accent)"><b>${esc(k)}:</b> ${esc(y||'∅')}</div></div>`;
  });
  return rows.join('');
}
function compareLineDiff(a,b){
  const la=a.split('\n'),lb=b.split('\n'),n=Math.max(la.length,lb.length),rows=[];
  for(let i=0;i<n&&rows.length<300;i++){
    const x=la[i]??'',y=lb[i]??'';
    if(x===y)rows.push(`<div style="color:var(--fg3);font-family:var(--mono);font-size:11px;white-space:pre-wrap">${esc(x||' ')}</div>`);
    else rows.push(`<div style="font-family:var(--mono);font-size:11px"><span style="color:var(--red);white-space:pre-wrap">${esc(x||'∅')}</span><br><span style="color:var(--accent);white-space:pre-wrap">${esc(y||'∅')}</span></div>`);
  }
  return rows.join('')+(n>300?'<div class="hint">…line diff truncated</div>':'');
}
export async function openCompare(){
  const ids=[...state.selected].sort((a,b)=>a-b);
  if(ids.length!==2){toast('select exactly 2 flows');return;}
  openModal($('#compareModal'));
  const box=$('#compareBody');if(box)box.innerHTML='<div class="hint">loading…</div>';
  try{
    const [fa,fb]=await Promise.all(ids.map(id=>api('/api/flows/'+id)));
    const [ra,rb]=await Promise.all(ids.map(id=>api('/api/flows/'+id+'/raw?side=res')));
    const split=s=>{const i=s.indexOf('\r\n\r\n');return i>=0?s.slice(i+4):s;};
    const limit=512*1024;
    const ba=split(ra).slice(0,limit),bb=split(rb).slice(0,limit);
    const mode=($('#compareMode')&&$('#compareMode').querySelector('.on')?.dataset.m)||'words';
    const bodyHtml=mode==='lines'?compareLineDiff(ba,bb):compareWordDiff(ba,bb);
    $('#compareTitle').textContent='Compare responses · #'+ids[0]+' vs #'+ids[1];
    if(box)box.innerHTML=`<div class="row" style="gap:12px;margin-bottom:8px;font-size:11px;flex-wrap:wrap">
      <span><b style="color:var(--red)">#${ids[0]}</b> ${esc(fa.method)} ${esc(fa.status||'—')} · ${fmtSize(fa.resLen)}</span>
      <span><b style="color:var(--accent)">#${ids[1]}</b> ${esc(fb.method)} ${esc(fb.status||'—')} · ${fmtSize(fb.resLen)}</span>
      <div class="seg" id="compareMode" style="margin-left:auto"><button class="on" data-m="words">Words</button><button data-m="lines">Lines</button></div>
    </div>
    <div style="font-size:9px;font-weight:700;letter-spacing:.6px;color:var(--fg3);margin:8px 0 4px">RESPONSE HEADERS</div>
    ${compareHeaderDiff(fa.resHeaders,fb.resHeaders)}
    <div style="font-size:9px;font-weight:700;letter-spacing:.6px;color:var(--fg3);margin:12px 0 4px">RESPONSE BODY</div>
    ${bodyHtml}`;
    $('#compareMode')?.querySelectorAll('button').forEach(b=>{b.onclick=()=>{ $('#compareMode').querySelectorAll('button').forEach(x=>x.classList.toggle('on',x===b)); openCompare(); };});
  }catch(e){if(box)box.innerHTML='<div class="hint" style="color:var(--red)">'+esc(e.message)+'</div>';}
}
if($('#selCompare'))$('#selCompare').onclick=openCompare;
if($('#compareClose'))$('#compareClose').onclick=()=>closeModal($('#compareModal'));
$('#selClear').onclick=()=>{state.selected.clear();state.lastSelIdx=-1;renderRows();updateSelBar();};
$('#selAsk').onclick=()=>{const ids=[...state.selected];if(ids.length)openAi({ids});};
$('#selScope').onclick=async()=>{
  const hosts=[...new Set([...state.selected].map(id=>{const f=flowStore.byId.get(id);return f&&f.host;}).filter(Boolean))];
  if(!hosts.length)return;
  let added=0;
  for(const host of hosts){try{await api('/api/scope',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({action:'include',host,enabled:true})});added++;}catch(e){}}
  toast('added '+added+' host'+(added===1?'':'s')+' to scope');loadScope();
};
export let _delArm=false,_delTimer;
$('#selDelete').onclick=async()=>{
  const ids=[...state.selected];if(!ids.length)return;
  if(!_delArm){_delArm=true;$('#selDelete').textContent='🗑 Confirm? ('+ids.length+')';clearTimeout(_delTimer);_delTimer=setTimeout(()=>{_delArm=false;$('#selDelete').textContent='🗑 Delete';},2500);return;}
  clearTimeout(_delTimer);_delArm=false;$('#selDelete').textContent='🗑 Delete';
  try{
    const r=await api('/api/flows/delete',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({ids})});
    if(state.selected.has(state.selId))state.selId=null;
    state.selected.clear();state.lastSelIdx=-1;updateSelBar();loadFlows();
    toast('deleted '+(r.deleted!=null?r.deleted:ids.length)+' flow'+((r.deleted!=null?r.deleted:ids.length)===1?'':'s'));
  }catch(e){toast('delete: '+e.message);}
};
/* ---- inspector splitter ---- */
(function(){
  const SPLITTER_KEY='inspect.height';
  const MIN_H=120, MAX_PCT=0.80;
  const splitter=document.getElementById('inspectSplitter');
  const inspect=document.getElementById('inspect');
  if(!splitter||!inspect)return;

  function clamp(h){
    const proxyPanel=inspect.closest('.panel');
    const maxH=proxyPanel?(proxyPanel.clientHeight*MAX_PCT):600;
    return Math.max(MIN_H,Math.min(maxH,h));
  }
  function applyHeight(h){
    h=clamp(h);
    inspect.style.height=h+'px';
    inspect.style.flex='none';
    try{localStorage.setItem(SPLITTER_KEY,String(h));}catch(e){}
  }

  // Restore persisted height on load.
  try{const saved=localStorage.getItem(SPLITTER_KEY);if(saved){const h=parseInt(saved,10);if(h>=MIN_H)applyHeight(h);}}catch(e){}

  // Pointer drag.
  let dragY=null,dragH=null;
  splitter.addEventListener('pointerdown',e=>{
    e.preventDefault();
    dragY=e.clientY;
    dragH=inspect.offsetHeight;
    splitter.setPointerCapture(e.pointerId);
  });
  splitter.addEventListener('pointermove',e=>{
    if(dragY===null)return;
    // Dragging up (negative delta) increases inspector height.
    applyHeight(dragH-(e.clientY-dragY));
  });
  splitter.addEventListener('pointerup',()=>{dragY=null;dragH=null;});
  splitter.addEventListener('pointercancel',()=>{dragY=null;dragH=null;});

  // Keyboard: Up/Down arrows nudge by 20px.
  splitter.addEventListener('keydown',e=>{
    if(e.key!=='ArrowUp'&&e.key!=='ArrowDown')return;
    e.preventDefault();
    const delta=e.key==='ArrowUp'?20:-20;
    applyHeight(inspect.offsetHeight+delta);
  });
})();
