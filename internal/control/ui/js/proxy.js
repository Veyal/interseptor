import { $, registerProjectSwitchGuard, $$, esc, escAttr, state, toast, toastError, api, saveFile, methodColor, statusColor, statusText, mimeLabel, fmtSize, fmtBytes, fmtTime, fmtDur, FLAG_WS, FLAG_TLS, FLAG_AI, FLAG_DISCOVERY, RENDER_CAP, highlightHTTP, highlightBodyText, prettify, formatHexDump, copyText, uiPrompt, uiConfirm, hasOpenModal, openModal, closeModal, isBinaryMime, bodyMime, headerBlockText, hideCtxMenu, openCtxMenu, closeAllUiSelects, flowBodyDownloadName, flowBodyDownloadHref, selectionWithin, wireSelectionDecode, wireRowKey, createFlowStore, loadFlowStore, upsertFlow as storeUpsertFlow, appendFlows, dropFlowsFrom, removeFlow, createVirtualList, diffVisibleRows, compileScopeRules, flowInScope, icon, renderLoadError } from './core.js';
registerProjectSwitchGuard(()=>noteDrafts.size||noteSaveTails.size?'Save or retry History notes before switching projects.':'');
import { flowFindings, addFlowToFinding, openFinding, updateFindPocBtn, loadFindings, pickFindingForSelection } from './findings.js';
import { tagChipStyle, renderTagBar, tagActionTargets, mutateFlowTags, openTagChipMenu } from './tags.js';
import { sendToRepeater, sendToIntruder, repNewTab, renderRepTabs, repLoadEditor, repPersist, repTitle, headersToText, waitForWorkstationReady } from './tools.js';
import { retentionStats, loadRetention } from './settings.js';
import { openAuthz, onAuthzSelectionChanged } from './authz.js';
import { openDecoder, prefillScanner } from './scanner.js';
import { openSessionInspector } from './session-inspection.js';
import { openAuthTimeline } from './authtimeline.js';
import { loadTrafficDiagnosis, onFlowMaybeTLS } from './tlsdiag.js';
import { animateOnce, MOTION } from './motion.js';
import { loadMapModule } from './project.js';
import { placeFloatingSurface } from './surface-position.js';
import { renderHTMLResponse, getHook, openFlow as openFlowDrawer } from './core.js';
import { applyRowClick, toggleAllIds, chunkIds, bulkProgressText, createLongPress, BULK_CHUNK } from './proxy-selection.js';
import { activeFilterCount, popoverFilterCount, emptyStateModel, attachedLabel, bulkVerbs, parseDockPref, resolveDock, middleEllipsis } from './proxy-filters.js';
import { wsOpcodeName, flowUrl } from './flowbody.js';
import { renderState } from './statepanel.js';
import { copyAs, COPY_AS_KINDS } from './copyas.js';
import { createKeyRegistry } from './keys.js';
const flowSearchContract="'/api/flow-searches' flowSearchScriptEditor flowSearchScriptSave flowSearchScriptError";

// map.js is dynamically imported (not statically, like the modules above) because
// it is a panel lazy-loaded on first visit (Phase 4a, UI-REDESIGN-ROADMAP.md §4) —
// a static import here would defeat that by pulling it in at boot via proxy.js's
// own always-loaded chain.
const focusMapSearch=(...args)=>loadMapModule().then(m=>m.focusMapSearch(...args));

// Authz identity cache for the "Send as" context-menu section. Loaded once at
// startup and refreshed whenever identities are saved in the authz modal.
let _authzIdsCache = [];
let _authzIdsEpoch=0;
export function refreshAuthzIds(){
  const epoch=++_authzIdsEpoch;
  api('/api/authz').then(d=>{ if(epoch===_authzIdsEpoch)_authzIdsCache=(d.identities||[]).filter(id=>id.name||id.headers); }).catch(()=>{});
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
  if(!await waitForWorkstationReady())return false;
  const t=repNewTab();
  if(!t)return false;
  document.querySelector('.tab[data-tab="repeater"]').click();
  const editEpoch=t.reqEditEpoch||0;
  const current=()=>!t._closed&&(t.reqEditEpoch||0)===editEpoch;
  try{
    const [d,raw]=await Promise.all([api('/api/flows/'+f.id),api('/api/flows/'+f.id+'/raw?side=req')]);
    if(!current()){if(!t._closed)toast('identity load skipped because the Repeater tab changed','warn');return false;}
    const def=(d.scheme==='https'&&d.port===443)||(d.scheme==='http'&&d.port===80);
    t.method=d.method;
    t.url=`${d.scheme}://${d.host}${def?'':':'+d.port}${d.path}`;
    t.headers=applyIdentityToHeaders(headersToText(d.reqHeaders),id.headers||'');
    const i2=raw.indexOf('\r\n\r\n');t.body=i2>=0?raw.slice(i2+4):'';
    t.resId=null;t.status='';t.color='';
    t.title=repTitle(t)+(id.name?' ['+id.name+']':'');
    renderRepTabs();repLoadEditor();repPersist();
    toast('loaded #'+f.id+' as '+( id.name||'identity')+' · Repeater');
  }catch(e){toastError('Send as identity failed',e);}
}

const FLOW_PAGE=250;            // primary page size shown in History
const FLOW_BUFFER=50;           // extra rows prefetched ahead of scroll (reduces load-more lag)
const FLOW_FETCH=FLOW_PAGE+FLOW_BUFFER;
const ROW_H_FALLBACK=30;
const PHONE_ROW_H=56;           // phone rows are two-line cards (panel-proxy.css)
function readRowHeight(){
  if(typeof matchMedia==='function'&&matchMedia('(max-width:720px)').matches)return PHONE_ROW_H;
  try{const v=parseFloat(getComputedStyle(document.documentElement).getPropertyValue('--row-h'));if(v>0)return v;}catch(e){}
  return ROW_H_FALLBACK;
}
let ROW_H=readRowHeight();     // virtualized row height (px), read from the --row-h CSS token
const VIRT_MIN=120;             // virtualize when more rows than this
const VIRT_BUF=40;
const MAX_LIVE_FLOWS=5000;      // cap the in-memory live list so long capture sessions don't grow unbounded (older rows stay on the server, reachable via scroll paging)
const FLOW_SIGNAL_LIMIT=6;       // one-shot arrival cues allowed per burst window
const FLOW_SIGNAL_WINDOW=800;
let flowHasMore=false;         // the server may have older flows past what's loaded
let loadingMore=false;         // a scroll-triggered page fetch is in flight
let flowRefreshing=false;
let flowPageError=null;
let flowLoadError=null;
let flowLoadEpoch=0,flowPageEpoch=0;
let flowLoadEvents=new Map();
let flowLoadOverflow=false;
const EXCLUDE_NORM=64|128; // repeater, intruder
const FLOW_COLS_KEY='proxy.cols';
const FLOW_COLS_ATTACHED_KEY='proxy.cols.attachedAdded'; // one-time: show the evidence column for saved column sets
const FLOW_COLW_KEY='proxy.colW';   // per-column pixel-width overrides (drag-to-resize)
const FLOW_COL_MIN=40;              // floor width for a resized column
const HIDE_TLS_KEY='proxy.hideTlsFailed';
// History source filters are intentionally client state: the backend already
// exposes the matching `manual` and `ai` query parameters, and live SSE rows
// need the same predicates before they are admitted to the in-memory window.
if(typeof state.showAI!=='boolean')state.showAI=true;
function loadProxyPrefs(){
  try{state.hideTlsFailed=localStorage.getItem(HIDE_TLS_KEY)!=='0';}catch(e){state.hideTlsFailed=true;}
}
loadProxyPrefs();
function syncHideTlsFilter(){
  const btn=$('#hideTlsFilter');
  if(!btn)return;
  btn.classList.toggle('on',state.hideTlsFailed);
  btn.setAttribute('aria-pressed',state.hideTlsFailed?'true':'false');
  btn.title=state.hideTlsFailed?'TLS handshake failures hidden — click to show them':'Showing TLS handshake failures — click to hide them';
}
export function setShowTlsFailed(show){
  state.hideTlsFailed=!show;
  try{localStorage.setItem(HIDE_TLS_KEY,state.hideTlsFailed?'1':'0');}catch(e){}
  syncHideTlsFilter();
  renderChips();
}
const FLOW_COLUMNS=[
  {key:'id',label:'#',sort:'id',w:'44px'},
  {key:'attached',label:'Ev',sort:'',w:'32px',align:'center'},
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
// On phones the History table keeps only the columns needed to triage a flow and
// uses compact tracks, so it fits a 375px pane instead of scrolling sideways.
// The phone layout renders each row as a two-line card (panel-proxy.css), so the
// tracks below only decide which cells exist; the card grid ignores their widths.
const PHONE_FLOW_COLS={id:'40px',attached:'28px',method:'56px',host:'minmax(64px,1fr)',path:'minmax(64px,1.6fr)',status:'44px',size:'52px',time:'52px'};
const flowPhoneQuery=typeof matchMedia==='function'?matchMedia('(max-width:720px)'):null;
function visibleFlowCols(){
  if(!flowPhoneQuery||!flowPhoneQuery.matches)return state.flowCols;
  const cols=state.flowCols.filter(k=>PHONE_FLOW_COLS[k]);
  return cols.length?cols:Object.keys(PHONE_FLOW_COLS);
}
function flowColGrid(){
  const phone=flowPhoneQuery&&flowPhoneQuery.matches;
  return visibleFlowCols().map(k=>{
    if(phone)return PHONE_FLOW_COLS[k];
    const w=state.flowColW&&state.flowColW[k];
    return (typeof w==='number')?w+'px':FLOW_COLUMNS.find(c=>c.key===k).w;
  }).join(' ');
}
// Push the current column template into a CSS var the header and every row read
// (.thead / .trow both use `grid-template-columns:var(--flow-cols)`), so a live
// drag repaints all tracks with one write instead of restyling every row.
function applyFlowGrid(){
  const grid=flowColGrid();
  const h=$('#flowHead');if(h)h.style.setProperty('--flow-cols',grid);
  const box=$('#rows');if(box)box.style.setProperty('--flow-cols',grid);
}
function loadFlowCols(){
  try{
    const raw=JSON.parse(localStorage.getItem(FLOW_COLS_KEY)||'null');
    if(Array.isArray(raw)&&raw.length){
      const withNew=raw.slice();
      try{if(!localStorage.getItem(FLOW_COLS_ATTACHED_KEY)){localStorage.setItem(FLOW_COLS_ATTACHED_KEY,'1');if(!withNew.includes('attached'))withNew.push('attached');}}catch(e){}
      const valid=normalizeFlowCols(withNew.filter(k=>FLOW_COLUMNS.some(c=>c.key===k)));
      state.flowCols=valid.length?valid:defaultFlowCols();
    }else state.flowCols=defaultFlowCols();
  }catch(e){state.flowCols=defaultFlowCols();}
}
function saveFlowCols(){try{localStorage.setItem(FLOW_COLS_KEY,JSON.stringify(state.flowCols));}catch(e){}}
function loadFlowColW(){
  const out={};
  try{
    const raw=JSON.parse(localStorage.getItem(FLOW_COLW_KEY)||'null');
    if(raw&&typeof raw==='object')for(const c of FLOW_COLUMNS){
      const v=raw[c.key];
      if(typeof v==='number'&&isFinite(v))out[c.key]=Math.max(FLOW_COL_MIN,Math.min(1200,Math.round(v)));
    }
  }catch(e){}
  state.flowColW=out;
}
function saveFlowColW(){try{localStorage.setItem(FLOW_COLW_KEY,JSON.stringify(state.flowColW));}catch(e){}}
// --- drag-to-resize a history column -------------------------------------
// The dragged column is pinned to an explicit pixel width (read from its live
// rendered width + drag delta); untouched flexible columns (Host/Path) absorb
// the slack, so the table keeps filling the pane. Double-click a grip to reset.
let colResizeState=null;
let colResizeSuppressClick=false; // swallow the click-to-sort that trails a drag
function startColResize(e){
  e.preventDefault();e.stopPropagation();
  const handle=e.currentTarget,cell=handle.parentElement;
  colResizeState={key:handle.dataset.col,startX:e.clientX,startW:cell.getBoundingClientRect().width};
  document.body.classList.add('col-resizing');
  document.addEventListener('mousemove',onColResizeMove);
  document.addEventListener('mouseup',endColResize);
}
function onColResizeMove(e){
  if(!colResizeState)return;
  const w=Math.max(FLOW_COL_MIN,Math.round(colResizeState.startW+(e.clientX-colResizeState.startX)));
  state.flowColW[colResizeState.key]=w;
  applyFlowGrid();
}
function endColResize(){
  if(!colResizeState)return;
  colResizeState=null;
  document.removeEventListener('mousemove',onColResizeMove);
  document.removeEventListener('mouseup',endColResize);
  document.body.classList.remove('col-resizing');
  saveFlowColW();
  colResizeSuppressClick=true;setTimeout(()=>{colResizeSuppressClick=false;},0);
}
function resetColWidth(e){
  e.preventDefault();e.stopPropagation();
  const key=e.currentTarget.dataset.col;
  if(state.flowColW[key]!=null){delete state.flowColW[key];applyFlowGrid();saveFlowColW();}
}
function wireFlowSort(){
  const toggle=h=>{
    const k=h.dataset.sort;
    if(state.sort.key===k)state.sort.dir*=-1;
    else{state.sort.key=k;state.sort.dir=k==='id'||k==='time'?-1:1;}
    renderFlowHead();
    loadFlows();
  };
  // Delegate the click across the whole header bar: each column cell is only
  // text-height (~13px) centered in the 28px #flowHead, so per-cell onclick left
  // dead strips above/below the label where clicks silently did nothing. The
  // click can land on the bar between cells (e.target = the container), so we
  // resolve the column by x-coordinate — making the full bar height sortable.
  // Cells keep role/tabIndex/keydown for keyboard users (tab to a column, Enter/Space).
  const head=$('#flowHead')||$('.thead');
  if(head)head.onclick=e=>{
    if(colResizeSuppressClick||e.target.closest('.col-resize'))return;
    const cell=[...head.children].find(c=>{const r=c.getBoundingClientRect();return e.clientX>=r.x&&e.clientX<r.x+r.width;});
    if(cell&&cell.dataset.sort)toggle(cell);
  };
  $$('.thead [data-sort]').forEach(h=>{
    h.setAttribute('role','button');
    h.tabIndex=0;
    h.addEventListener('keydown',e=>{if(e.key==='Enter'||e.key===' '){e.preventDefault();toggle(h);}});
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
  head.innerHTML=visibleFlowCols().map(k=>{
    const c=FLOW_COLUMNS.find(x=>x.key===k);
    const alignCls=c.align?' u-ta-'+c.align:'';
    const accessible=c.label==='St'?'Status':c.label==='Ev'?'Evidence':c.label;
    const title=k==='id'?' title="Shift+click range · Ctrl+Shift+click toggle · Ctrl+Shift+A select all"':'';
    const sk=state.sort.key,sd=state.sort.dir;
    const sorted=c.sort===sk?` sorted${sd>0?' asc':' desc'}`:'';
    const arrow=c.sort===sk?(sd>0?' ▲':' ▼'):'';
    return `<div class="${(sorted.trim()+alignCls).trim()}"${c.sort?` data-sort="${c.sort}"`:''} aria-label="${escAttr(accessible)}"${align}${title}>${esc(c.label)}${arrow}<span class="col-resize" data-col="${c.key}" title="Drag to resize · double-click to reset"></span></div>`;
  }).join('');
  head.querySelectorAll('.col-resize').forEach(h=>{
    h.addEventListener('mousedown',startColResize);
    h.addEventListener('dblclick',resetColWidth);
  });
  applyFlowGrid();
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
  const focused=menu.querySelector(':focus')?.dataset.col;
  menu.innerHTML=FLOW_COLUMNS.map(c=>`<label><input type="checkbox" data-col="${c.key}"${state.flowCols.includes(c.key)?' checked':''}> ${esc(c.label)}</label>`).join('');
  menu.querySelectorAll('input[data-col]').forEach(inp=>inp.onchange=()=>setFlowCol(inp.dataset.col,inp.checked));
  if(focused)menu.querySelector(`[data-col="${CSS.escape(focused)}"]`)?.focus({preventScroll:true});
}
function closeColPicker(restoreFocus=false){
  const menu=$('#colPicker'),btn=$('#colPickerBtn');
  if(!menu||menu.style.display!=='block')return;
  menu.style.display='none';btn?.setAttribute('aria-expanded','false');
  if(restoreFocus)btn?.focus({preventScroll:true});
}
function toggleColPicker(){
  const menu=$('#colPicker'),btn=$('#colPickerBtn');
  if(!menu||!btn)return;
  const open=menu.style.display==='none'||!menu.style.display;
  if(open){
    closeAllUiSelects();hideCtxMenu();
    renderColPicker();
    const r=btn.getBoundingClientRect();
    menu.style.display='block';
    const vv=window.visualViewport;
    const pos=placeFloatingSurface(r,168,menu.scrollHeight+2,{left:vv?.offsetLeft||0,top:vv?.offsetTop||0,width:vv?.width||innerWidth,height:vv?.height||innerHeight});
    Object.assign(menu.style,{left:pos.left+'px',top:pos.top+'px',width:pos.width+'px',maxHeight:pos.maxHeight+'px'});
    btn.setAttribute('aria-expanded','true');
    menu.querySelector('input')?.focus({preventScroll:true});
  }else closeColPicker(true);
}

function flowExcluded(f){return (f.flags&EXCLUDE_NORM)!==0&&(f.flags&FLAG_AI)===0;}
// activeScopeMatcher compiles state.scope once per rule-set identity (loadScope
// reassigns state.scope on every /api/scope read, so a fresh array is the signal
// that the rules changed) — compiling per live flow event would defeat the point.
// scopeLoaded distinguishes "no rules" from "rules unknown": until /api/scope has
// answered once, an empty state.scope must not be read as everything-in-scope.
let scopeMatcherSource=null,scopeMatcherCache=null,scopeLoaded=false;
function activeScopeMatcher(){
  const rules=state.scope||[];
  if(scopeMatcherSource!==rules){scopeMatcherSource=rules;scopeMatcherCache=compileScopeRules(rules);}
  return scopeMatcherCache;
}
// scopeDecidableHere reports whether "in scope" mode can be evaluated against the
// flow objects this client already holds, i.e. whether the rule set avoids regex
// host/path patterns (Go's regexp is RE2, JS's isn't — see compileScopeRules).
function scopeDecidableHere(){return !state.inScopeOnly||(scopeLoaded&&activeScopeMatcher().evaluable);}
// canIncremental used to unconditionally bail to a full /api/flows reload
// whenever scope-mode/search/exclude-filters were active — degrading every new
// flow event to a full reload for the common pentester workflow (scope mode on).
// It's now a narrow "is every active filter one flowMatchesFilters() can decide
// purely from the flow object the client already has" gate. Two things are NOT
// safely decidable client-side, so they still force a full reload:
//   - inScopeOnly *with a regex host/path rule*: those compile to Go RE2 on the
//     server (internal/scope/scope.go), which JS's RegExp is not — so a regex
//     rule anywhere in the set puts scope mode back on the server-only path.
//     Everything else scope supports (exact host, *.wildcard host, literal path
//     prefix, scheme, port, include/exclude precedence) is a plain field
//     comparison this client reproduces exactly — see core.js's flowInScope,
//     which is cross-checked against scope.Engine.InScope in Go tests.
//   - an active text search: the default (path) scope is SQLite FTS5
//     (unicode61 tokenizer, per-token prefix match, OR-joined —
//     internal/store/flows_fts.go) and the body scope requires reading a
//     response body the client doesn't have; neither is a simple field compare
//     a client-side check can faithfully reproduce. (id-scope search *is*
//     trivially exact — `f.id === N` — but is rare enough not to special-case.)
// Everything else the toolbar can filter by (method/host/scheme/status/tag/
// hasNote/exclude-rules) is a simple, already-duplicated field comparison — see
// flowMatchesFilters() below — so those stay incremental.
function canIncremental(){
  return scopeDecidableHere()&&!(state.filters.search&&state.filters.search.trim());
}
function flowMatchesFilters(f){
  const fl=state.filters;
  const isAI=(f.flags&FLAG_AI)!==0;
  if(!state.showManual&&!isAI)return false;
  if(!state.showAI&&isAI)return false;
  if(flowExcluded(f))return false;
  if(state.hideTlsFailed&&(f.flags&FLAG_TLS)&&state.filters.tag!=='tls-failed')return false;
  // Scope mode is part of the server-side query (buildFlowParams sets inScope=1),
  // so the client-side predicate has to apply it too or an incrementally inserted
  // row could show traffic the same filter set would have excluded on a reload.
  // With a regex rule in play flowInScope() reports false, which keeps the row
  // out until the full reload canIncremental() is already forcing places it.
  if(state.inScopeOnly&&(!scopeLoaded||!flowInScope(f,activeScopeMatcher())))return false;

  if(state.notesOnly&&!(f.note&&String(f.note).trim()))return false;
  if(fl.scheme&&f.scheme!==fl.scheme)return false;
  if(fl.method&&f.method!==fl.method)return false;
  if(fl.host&&!f.host.toLowerCase().includes(fl.host.toLowerCase()))return false;
  if(fl.status&&Math.floor((f.status||0)/100)!==Number(fl.status))return false;
  if(fl.tag&&!(f.tags||[]).includes(fl.tag))return false;
  for(const e of fl.exclude||[]){
    const v=String(e.value);
    if(e.field==='method'&&f.method===v)return false;
    if(e.field==='host'&&f.host.toLowerCase().includes(v.toLowerCase()))return false;
    if(e.field==='path'&&f.path.toLowerCase().includes(v.toLowerCase()))return false;
    if(e.field==='status'&&String(f.status)===v)return false;
  }
  return true;
}
function http2Chip(f){
  const v=String(f.httpVersion||'');
  if(!/^HTTP\/2/i.test(v)&&v!=='h2')return '';
  return '<span class="ai-tag" style="background:var(--accentDim);color:var(--accent)" title="Upstream spoke '+escAttr(v)+' (MITM client leg is HTTP/1.1)">h2</span>';
}
function flowRowHTML(f){
  const intercepted=(f.flags&1)!==0;
  const pending=!f.status&&!f.error;
  const hasNote=!!(f.note&&String(f.note).trim());
  const stHTML=f.status?String(f.status):(f.error?(f.flags&FLAG_TLS?'<span title="TLS MITM failed — likely SSL pinning or untrusted CA">PIN</span>':'ERR'):'<span class="blink tr-wait" title="waiting for response" aria-label="waiting for response">…</span>');
  const linked=attachedLabel(flowFindings(f.id));
  const phoneCard=!!(flowPhoneQuery&&flowPhoneQuery.matches);
  const rowTitle=[pending?'[pending]':'',hasNote?String(f.note).trim():''].filter(Boolean).join(' · ');
  const title=rowTitle?` title="${escAttr(rowTitle)}"`:'';
  const cells={
    id:`<div class="tr-id" data-field="id">${f.id}</div>`,
    attached:`<div class="tr-att" data-field="attached">${linked?`<span title="${escAttr(linked)}">${icon('paperclip')}<span class="u-sr">${esc(linked)}</span></span>`:''}</div>`,
    method:`<div class="tr-m" data-field="method" style="color:${methodColor(f.method)}">${esc(f.method)}</div>`,
    host:`<div class="tr-host" data-field="host">${f.scheme==='https'?icon('lock','HTTPS')+' ':''}${esc(f.host)}</div>`,
    path:`<div class="tr-path" data-field="path"${phoneCard?` title="${escAttr(f.path)}"`:''}>${esc(phoneCard?middleEllipsis(f.path,44):f.path)}${intercepted?` <span class="tr-held" title="intercepted" role="img" aria-label="intercepted">${icon('flag')}</span>`:''}${http2Chip(f)}${(f.flags&FLAG_TLS)?'<span class="ai-tag" style="background:var(--redDim);color:var(--red)" title="TLS handshake failed — SSL pinning or untrusted CA">PIN</span>':''}${(f.flags&FLAG_AI)?'<span class="ai-tag" title="sent by the AI assistant">AI</span>':''}${(f.flags&FLAG_DISCOVERY)?'<span class="ai-tag" style="background:var(--violetDim);color:var(--violet)" title="legacy content-discovery engine (removed) — old project data">DSC</span>':''}${(f.tags||[]).map(t=>`<span class="flowtag" data-tagchip="${escAttr(t)}" style="${tagChipStyle(t)}" title="filter by tag ${escAttr(t)}">${esc(t)}</span>`).join('')}</div>`,
    status:`<div class="tr-st" data-field="status" style="color:${statusColor(f.status)}">${stHTML}</div>`,
    mime:`<div class="tr-mime" data-field="mime">${esc(mimeLabel(f.mime))}</div>`,
    size:`<div class="tr-len" data-field="size">${f.status?fmtSize(f.resLen):''}</div>`,
    time:`<div class="tr-t" data-field="time">${fmtTime(f.ts)}</div>`,
  };
  return `<div class="trow ${f.id===state.selId?'sel':''}${state.selected.has(f.id)?' msel':''}${pending?' pending':''}${hasNote?' has-note':''}" data-id="${f.id}" aria-current="${f.id===state.selId?'true':'false'}" aria-pressed="${state.selected.has(f.id)?'true':'false'}"${title}>
      ${visibleFlowCols().map(k=>cells[k]).join('')}
      <button type="button" class="tr-more rowOverflow" data-rowmenu="${f.id}" aria-haspopup="menu" aria-label="Actions for flow #${f.id}" tabindex="-1">${icon('chevron')}</button>
    </div>`;
}
function wireFlowRow(r){
  const id=Number(r.dataset.id);
  r.onclick=e=>flowRowClick(id,e);
  wireRowKey(r,()=>flowRowClick(id,{})); // Enter/Space inspects the focused row
  const flow=flowStore.byId.get(id);
  r.setAttribute('aria-label',flow
    ? `Flow #${id}: ${flow.method||'request'} ${flow.host||''}${flow.path||''}${flow.status?`, status ${flow.status} ${statusText(flow.status)}`:''}`
    : 'Flow #'+id);
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
  r.addEventListener('keydown',e=>{
    if(e.key==='ContextMenu'||(e.shiftKey&&e.key==='F10')){
      e.preventDefault();
      const f=flowStore.byId.get(id),cell=e.target.closest('[data-field]');
      const box=r.getBoundingClientRect();
      showCtx(box.left+24,box.top+Math.min(24,box.height),f,cell?cell.dataset.field:'');
    }
  });
}
let flowSignalWindowAt=0,flowSignalCount=0;
const pendingFlowSignals=new Set();
function queueFlowSignal(id){
  if(document.hidden)return;
  const now=performance.now();
  if(now-flowSignalWindowAt>FLOW_SIGNAL_WINDOW){flowSignalWindowAt=now;flowSignalCount=0;}
  flowSignalCount++;
  if(flowSignalCount<=FLOW_SIGNAL_LIMIT)pendingFlowSignals.add(id);
}
function consumeFlowSignals(){
  for(const id of pendingFlowSignals){
    pendingFlowSignals.delete(id);
    const row=document.querySelector('#rows .trow[data-id="'+id+'"]');
    if(!row)continue;
    row.classList.add('flow-new');
    animateOnce(row,[
      {backgroundColor:'var(--accentDim)',borderLeftColor:'var(--accent)'},
      {backgroundColor:'transparent',borderLeftColor:'transparent'},
    ],{duration:MOTION.slow,easing:MOTION.enter}).finally(()=>row.classList.remove('flow-new'));
  }
}
function updateTruncBanner(){
  const b=$('#flowCapBanner');
  if(!b)return;
  // No hard cap anymore — older flows stream in as you scroll. Show a compact
  // status and a retry affordance when a page request fails; the failure must
  // remain visible until the operator retries or another page succeeds.
  // `message` is the dedicated text node when the banner has one, and the banner
  // itself as a fallback — writing through it keeps a sibling retry button alive
  // instead of being wiped by a textContent assignment on the whole banner.
  const message=$('#flowCapMessage')||b.querySelector('[data-flow-cap-message]')||b.querySelector('span')||b;
  const retry=$('#flowCapRetry');
  const show=text=>{message.textContent=text;b.style.display='flex';};
  if(flowLoadError){
    const stale=state.flows.length>0;
    show((stale?'History is stale — ':'Could not load History: ')+(flowLoadError.message||flowLoadError));
    if(retry){retry.hidden=false;retry.disabled=flowRefreshing;retry.onclick=()=>loadFlows();}
  }else if(flowPageError){
    show('Could not load older flows: '+(flowPageError.message||flowPageError));
    if(retry){retry.hidden=false;retry.disabled=loadingMore;retry.onclick=()=>loadMoreFlows();}
  }else if(flowRefreshing){
    show('Refreshing flows…');if(retry)retry.hidden=true;
  }else if(loadingMore){
    show('Loading older flows…');if(retry)retry.hidden=true;
  }else if(flowHasMore){
    show('Scroll down to load older flows.');if(retry)retry.hidden=true;
  }else{b.style.display='none';if(retry)retry.hidden=true;}
}
// Infinite scroll: load the next older page as the History list nears its bottom.
let flowScrollSource=null,flowScrollSyncing=false;
function syncFlowHorizontalScroll(){
  const head=$('#flowHead'),box=$('#rows');
  if(!head||!box||flowScrollSyncing)return;
  flowScrollSyncing=true;
  if(flowScrollSource===head)box.scrollLeft=head.scrollLeft;
  else head.scrollLeft=box.scrollLeft;
  flowScrollSyncing=false;
}
{const head=$('#flowHead'),box=$('#rows');
  if(head)head.addEventListener('scroll',()=>{flowScrollSource=head;syncFlowHorizontalScroll();},{passive:true});
  if(box)box.addEventListener('scroll',()=>{
  flowScrollSource=box;syncFlowHorizontalScroll();
  if(box.scrollTop+box.clientHeight>=box.scrollHeight-400)loadMoreFlows();
  },{passive:true});
}
// buildFlowRowEl materializes one wired row element and stamps the exact markup
// it was built from onto the node. The stamp is what lets reconcileVirtualRows
// tell "this mounted row already renders the current state" from "this row is
// stale" without diffing attributes by hand — and therefore what makes keyed
// reconciliation produce byte-identical DOM to a full re-render.
function buildFlowRowEl(f){
  const html=flowRowHTML(f);
  const tmp=document.createElement('div');
  tmp.innerHTML=html;
  const el=tmp.firstElementChild;
  el._flowHTML=html;
  wireFlowRow(el);
  return el;
}
export function patchFlowRow(f){
  const row=document.querySelector('#rows .trow[data-id="'+f.id+'"]');
  if(row){
    const hadFocus=row===document.activeElement||row.contains(document.activeElement);
    const nr=buildFlowRowEl(f);
    row.replaceWith(nr);
    // SSE response updates replace the row's DOM node. Keep keyboard users on
    // the same flow instead of dropping focus to the document body.
    if(hadFocus)nr.focus({preventScroll:true});
    return;
  }
  if(!flowMatchesFilters(f))return;
  const box=$('#rows');
  if(!box||box.querySelector('.state-empty')||box.querySelector('#gsMcp')){renderRows();return;}
  const nr=buildFlowRowEl(f);
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
}
let liveRenderQueued=false;
function queueFullWindowRebuild(){
  // Virtualized mode, insert/removal path: a new row at the front shifts every
  // other row's window index, so the window itself has to be recomputed. That
  // recomputation is now a keyed reconcile (reconcileVirtualRows) that patches
  // only the rows the new window actually adds or drops, with renderRows() kept
  // as the fallback for windows it can't patch. Coalesce via rAF so a burst of
  // inserts in the same frame collapses to one pass (same as before).
  if(liveRenderQueued)return;
  liveRenderQueued=true;
  const fire=()=>{liveRenderQueued=false;if(!reconcileVirtualRows())renderRows();};
  // rAF is suspended outright (not just throttled) for a backgrounded/hidden
  // tab in every major browser — Interceptor's control UI is routinely left
  // in a background tab while the operator works in the app under test, so
  // waiting on rAF here would silently freeze the visible row set (and the
  // top-of-history rows specifically) while flowStore/state.flows keeps
  // growing underneath — the flow count updates but new rows never appear
  // until something else happens to force a render. Fall back to a plain
  // timer while hidden; the visibilitychange listener below also forces one
  // definitive catch-up render the moment the tab regains visibility, as a
  // backstop against any edge case in this queuing.
  if(document.hidden)setTimeout(fire,0);else requestAnimationFrame(fire);
}
// flowRowLiveUpdate is the keyed-reconciliation entry point for a live SSE flow
// event (new or updated) once it's already been accepted into flowStore/state.flows.
// `isNew` distinguishes an insert (which can move every row's window index) from
// an in-place update (which never changes order/count — id/sort key are
// immutable, so an update can only ever change what a row *displays*, never
// where it sits). That distinction is what lets updates patch a single DOM node
// even while virtualized, instead of falling back to a full window rebuild.
function flowRowLiveUpdate(f,isNew){
  if(!flowVirt.isActive()){
    // A list that started below the threshold is still on the incremental DOM
    // patch path. The insert that crosses the threshold must rebuild once so
    // computeWindow() can activate virtualization; otherwise every later live
    // row remains mounted for the rest of the capture session.
    if(isNew&&state.flows.length>=VIRT_MIN){renderRows();return;}
    patchFlowRow(f);consumeFlowSignals();return;
  }
  if(isNew){queueFullWindowRebuild();return;}
  // Virtualized + update: the row is either currently rendered (patch it directly,
  // same surgical replace patchFlowRow already does for the non-virtualized case)
  // or it's scrolled out of the rendered window (nothing on screen needs to
  // change at all — nothing to patch, and no rebuild needed either).
  const row=document.querySelector('#rows .trow[data-id="'+f.id+'"]');
  if(row)patchFlowRow(f);
}
function rememberFlowLoadEvent(kind,flow){
  if(!flowRefreshing)return;
  const previous=flowLoadEvents.get(flow.id);
  const replayKind=kind==='new'||(previous&&previous.kind==='new')?'new':'update';
  // Keep one latest snapshot per flow. Delete first so Map iteration remains in
  // live-arrival order while a new+update pair consumes only one bounded slot.
  flowLoadEvents.delete(flow.id);
  flowLoadEvents.set(flow.id,{kind:replayKind,flow});
  if(flowLoadEvents.size>MAX_LIVE_FLOWS){
    flowLoadOverflow=true;
    flowLoadEvents.delete(flowLoadEvents.keys().next().value);
  }
}
function reconcileFlowLoadEvent(event){
  const f=event.flow;
  if(event.kind==='new'){
    if(!sortIsLiveDefault()||!canIncremental())return false;
    if(flowMatchesFilters(f))upsertFlow(f);
    return true;
  }
  if(!canIncremental())return false;
  const existing=flowStore.byId.get(f.id);
  if(existing){
    const sortValueBefore=flowSortValue(existing),sortValueAfter=flowSortValue(f);
    if(!flowMatchesFilters(f)){
      removeFlow(flowStore,f.id);
      if(state.selected)state.selected.delete(f.id);
      if(state.selId===f.id){state.selId=null;state.detail=null;onAuthzSelectionChanged();}
      return true;
    }
    storeUpsertFlow(flowStore,f);
    return sortValueBefore===sortValueAfter;
  }
  if(!flowMatchesFilters(f))return true;
  if(!sortIsLiveDefault())return false;
  upsertFlow(f);
  return true;
}
export function handleFlowNew(f){
  if(!f)return;
  rememberFlowLoadEvent('new',f);
  onFlowMaybeTLS(f);
  if(!sortIsLiveDefault()||!canIncremental()){scheduleReload();return;}
  if(!flowMatchesFilters(f))return; // doesn't match the active filters — nothing to show, no reload needed
  upsertFlow(f);
  refreshMethodFilter();
  const proxy=document.querySelector('.panel[data-panel="proxy"]');
  if(!proxy||!proxy.classList.contains('active'))return;
  queueFlowSignal(f.id);
  flowRowLiveUpdate(f,true);
}
export function handleFlowUpdate(f){
  if(!f)return;
  rememberFlowLoadEvent('update',f);
  onFlowMaybeTLS(f);
  const proxy=document.querySelector('.panel[data-panel="proxy"]');
  const active=proxy&&proxy.classList.contains('active');
  if(flowStore.byId.has(f.id)){
    const existing=flowStore.byId.get(f.id);
    const sortValueBefore=flowSortValue(existing);
    const sortValueAfter=flowSortValue(f);
    // Already visible in the loaded list. A previously-matching flow can stop
    // matching on update (e.g. its status now falls outside an active status
    // filter) — remove it from the list in that case rather than leaving a
    // stale row. Same ambiguous-case handling applies to a flow that newly
    // starts matching (see the else-branch below): insert/remove a single row
    // instead of a full reload either way.
    if(!canIncremental()){storeUpsertFlow(flowStore,f);if(active)flowRowLiveUpdate(f,false);return;}
    if(!flowMatchesFilters(f)){
      const removedSelected=state.selId===f.id;
      removeFlow(flowStore,f.id);
      if(state.selected)state.selected.delete(f.id);
      if(removedSelected){closeInspector();return;}
      if(active){const row=document.querySelector('#rows .trow[data-id="'+f.id+'"]');if(row)row.remove();else if(flowVirt.isActive())queueFullWindowRebuild();}
      return;
    }
    storeUpsertFlow(flowStore,f); // O(1) in-place refresh — no findIndex over the loaded list
    // Status, response length, and MIME are mutable after capture. If one of
    // them is the active sort key, patching in place would leave History in the
    // wrong order. Coalesced reloads keep this correct without doing a full
    // fetch for every burst event.
    if(sortValueBefore!==sortValueAfter){scheduleReload();return;}
    if(!active)return;
    flowRowLiveUpdate(f,false);
    return;
  }
  if(canIncremental()&&flowMatchesFilters(f)){upsertFlow(f);refreshMethodFilter();if(active)flowRowLiveUpdate(f,true);}
  else if(!canIncremental())scheduleReload();
  // else: doesn't match filters and was never in the list — nothing to do.
}

export function getStartedCard(){
  return `<div class="state-empty history-welcome">
    <div class="state-empty-icon"><svg class="icon" aria-hidden="true" focusable="false"><use href="#i-traffic"/></svg></div>
    <div class="state-empty-title">Your traffic starts here</div>
    <p class="state-empty-hint">Connect a client to <code>${esc(state.proxyAddr)}</code>. Requests appear here as they arrive.</p>
    <div class="history-welcome-actions"><button type="button" class="btn btn-primary" id="gsSettings">Connection settings</button><button type="button" class="btn" id="gsTLS">HTTPS setup</button></div>
  </div>`;
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
// Drop the single-flow selection and collapse the request/response inspector.
// Returns whether it had anything open, so the Escape handler can chain cleanly.
export function closeInspector(){
  if(state.selId==null)return false;
  state.selId=null;state.detail=null;
  onAuthzSelectionChanged();
  if($('#panel-proxy')?.classList.contains('dock-drawer'))getHook('closeFlow')?.();
  renderRows();
  return true;
}
// flowVirt owns the Proxy history table's windowed-rendering bookkeeping
// (scroll binding + rAF coalescing); computeWindow() below runs the same
// windowing math the hand-rolled version used (start/end/topPad/bottomPad),
// just centralized in core.js so future panels can share it.
const flowVirt=createVirtualList({container:$('#rows'),itemHeight:()=>ROW_H,threshold:VIRT_MIN,buffer:VIRT_BUF,onScroll:()=>{if(!reconcileVirtualRows())renderRows();}});
// Backstop for queueFullWindowRebuild's hidden-tab fallback above: force one
// definitive re-render the moment the tab regains visibility, regardless of
// whether a rebuild was already queued — cheap (renderRows() just rebuilds
// from the current in-memory state.flows) and guarantees the visible window
// can never stay stale after a background period.
document.addEventListener('visibilitychange',()=>{if(!document.hidden&&flowVirt.isActive())renderRows();});
function refreshRowHeight(){
  const h=readRowHeight();
  if(h===ROW_H)return;
  ROW_H=h;
  if(flowVirt.isActive())renderRows();
}
window.addEventListener('resize',refreshRowHeight);
try{window.matchMedia('(pointer:coarse)').addEventListener('change',refreshRowHeight);}catch(e){}
function captureFlowListFocus(box){
  const active=document.activeElement,row=active?.closest?.('#rows .trow[data-id]');
  if(!row||!box.contains(row))return null;
  const tag=active.closest?.('.flowtag');
  return {id:row.dataset.id,tag:tag?.dataset.tagchip||''};
}
function restoreFlowListFocus(box,focus){
  if(!focus)return;
  const row=box.querySelector(`.trow[data-id="${CSS.escape(focus.id)}"]`);if(!row)return;
  const target=focus.tag?[...row.querySelectorAll('.flowtag')].find(chip=>chip.dataset.tagchip===focus.tag):row;
  target?.focus({preventScroll:true});
}
// refreshVisibleRows re-syncs the mounted History rows with current state (for
// example after a tag color change): keyed patch when the window allows it, a
// full rebuild otherwise.
export function refreshVisibleRows(){if(!reconcileVirtualRows())renderRows();}
// reconcileVirtualRows is the keyed-reconciliation counterpart to renderRows for
// the virtualized window. Instead of re-serializing and re-wiring the entire
// visible slice on every live insert/removal, it diffs the mounted row ids
// against the ids the next window should hold and touches only the difference:
// surviving rows keep their identity, so their listeners, any focus inside them,
// and the container's scroll position all survive untouched. A row whose
// rendering drifted is replaced individually (the stamp from buildFlowRowEl
// makes that comparison exact), so the resulting DOM is identical to what
// renderRows() would have produced for the same state.
// Returns false when the current DOM is not a window it may patch (empty state,
// non-virtualized list, reordered survivors) so the caller can fall back.
function reconcileVirtualRows(){
  const box=$('#rows');
  const flows=state.flows;
  if(!box||!flows.length)return false;
  const top=box.firstElementChild,bottom=box.lastElementChild;
  if(!top||!bottom||top===bottom)return false;
  if(top.dataset.vpad!=='top'||bottom.dataset.vpad!=='bottom')return false;
  // Inspector visibility changes #rows' height, so settle it before sizing the window.
  syncInspectorVisibility();
  applyFlowGrid();
  const win=flowVirt.computeWindow(flows.length);
  if(!win)return false;
  const slice=flows.slice(win.start,win.end);
  const mounted=Array.from(box.querySelectorAll('.trow'));
  const plan=diffVisibleRows(mounted.map(r=>Number(r.dataset.id)),slice.map(f=>f.id));
  if(!plan.reusable)return false;
  const byId=new Map(mounted.map(r=>[Number(r.dataset.id),r]));
  for(const id of plan.remove){
    const row=byId.get(id);
    if(row)row.remove();
    byId.delete(id);
  }
  const nextById=new Map(slice.map(f=>[f.id,f]));
  for(const ins of plan.insert){
    const el=buildFlowRowEl(nextById.get(ins.id));
    const anchor=ins.before==null?bottom:byId.get(ins.before);
    (anchor||bottom).before(el);
    byId.set(ins.id,el);
  }
  for(const f of slice){
    const row=byId.get(f.id);
    if(!row||row._flowHTML===flowRowHTML(f))continue;
    const hadFocus=row===document.activeElement||row.contains(document.activeElement);
    const focus=hadFocus?captureFlowListFocus(box):null;
    const nr=buildFlowRowEl(f);
    row.replaceWith(nr);
    byId.set(f.id,nr);
    if(focus)restoreFlowListFocus(box,focus);
    else if(hadFocus)nr.focus({preventScroll:true});
  }
  top.style.height=win.topPad+'px';
  bottom.style.height=win.bottomPad+'px';
  consumeFlowSignals();
  return true;
}
let checklistHandle=null;
// renderEmptyHistory paints the guided empty state: a first-run card (proxy
// address, device setup, FirstRunChecklist) or a filtered-empty card that names
// how many filters hide everything, with one Clear filters action.
function renderEmptyHistory(box){
  if(checklistHandle){checklistHandle.destroy();checklistHandle=null;}
  const model=emptyStateModel(state,{proxyAddr:state.proxyAddr});
  if(model.kind==='empty-filtered'){
    renderState(box,'empty-filtered',{title:model.title,hint:model.hint,filterCount:model.count,onClear:()=>{syncScopeToggle(false);clearAllFilters();}});
    return;
  }
  renderState(box,'empty-first',{title:model.title,hint:model.hint,icon:'traffic'});
  const wrap=box.querySelector('.state-panel');
  const actions=document.createElement('div');
  actions.className='history-welcome-actions';
  const settings=document.createElement('button');
  settings.type='button';settings.className='btn btn-primary';settings.id='gsSettings';settings.textContent='Connection settings';
  settings.onclick=()=>{document.querySelector('.tab[data-tab="settings"]')?.click();document.querySelector('#setNav button[data-sec="proxy"]')?.click();};
  const tls=document.createElement('button');
  tls.type='button';tls.className='btn';tls.id='gsTLS';tls.textContent='HTTPS setup';
  tls.onclick=()=>{document.querySelector('.tab[data-tab="settings"]')?.click();document.querySelector('#setNav button[data-sec="tls"]')?.click();};
  actions.append(settings,tls);
  const mount=document.createElement('div');
  mount.id='proxyChecklistMount';
  (wrap||box).append(actions,mount);
  // The checklist module is optional: a missing or failed module must not blank History.
  const mountChecklist=getHook('mountChecklist');
  if(mountChecklist){try{checklistHandle=mountChecklist(mount);}catch(e){checklistHandle=null;}}
}
export function renderRows(){
  syncInspectorVisibility();
  const box=$('#rows');
  const focus=captureFlowListFocus(box);
  applyFlowGrid();
  const flows=state.flows;
  if(!flows.length){
    renderEmptyHistory(box);
    return;}
  if(checklistHandle){checklistHandle.destroy();checklistHandle=null;}
  const win=flowVirt.computeWindow(flows.length);
  if(win){
    // The pads carry data-vpad so reconcileVirtualRows can recognize a window it
    // is allowed to patch (and resize) instead of re-serializing it.
    const html=flows.slice(win.start,win.end).map(f=>flowRowHTML(f));
    box.innerHTML=`<div data-vpad="top" style="height:${win.topPad}px" aria-hidden="true"></div>`+html.join('')+`<div data-vpad="bottom" style="height:${win.bottomPad}px" aria-hidden="true"></div>`;
    stampFlowRows(box,html);
    consumeFlowSignals();
    restoreFlowListFocus(box,focus);
    return;
  }
  const html=flows.map(f=>flowRowHTML(f));
  box.innerHTML=html.join('');
  stampFlowRows(box,html);
  consumeFlowSignals();
  restoreFlowListFocus(box,focus);
}
// stampFlowRows wires every freshly-serialized row and records the markup it came
// from, so the next keyed reconcile can skip rows whose rendering hasn't changed.
function stampFlowRows(box,html){
  const rows=box.querySelectorAll('.trow');
  rows.forEach((r,i)=>{r._flowHTML=html[i];wireFlowRow(r);});
}
export function flowRowClick(id,e){
  // A click on a tag chip filters History by that tag instead of inspecting the row.
  const chip=e&&e.target&&e.target.closest&&e.target.closest('.flowtag');
  if(chip){filterByTag(chip.dataset.tagchip);return;}
  const ids=state.flows.map(f=>f.id);
  if(!ids.includes(id))return;
  stepSelection(ids,id,{mod:e.ctrlKey||e.metaKey,shift:e.shiftKey});
  selectFlow(id);updateSelBar();
}
// stepSelection applies one click or keyboard step through the id-keyed model:
// ranges resolve against the current (filtered) id list, never the rendered
// window, and the anchor is an id so it survives reloads and virtualization.
function stepSelection(ids,id,mods){
  const r=applyRowClick(state.selected,ids,{id,anchorId:state.selAnchorId,currentId:state.selId,...mods});
  state.selAnchorId=r.anchorId;
  state.lastSelIdx=ids.indexOf(id);
}
export function walkFlowNav(down,e){
  const list=state.flows;
  if(!list.length)return null;
  const i=list.findIndex(f=>f.id===state.selId);
  const ni=i<0?0:(down?Math.min(i+1,list.length-1):Math.max(i-1,0));
  if(ni===i)return null;
  const id=list[ni].id;
  if(state.selAnchorId==null&&i>=0)state.selAnchorId=list[i].id;
  stepSelection(list.map(f=>f.id),id,{mod:e.ctrlKey||e.metaKey,shift:e.shiftKey});
  selectFlow(id);updateSelBar();return id;
}
// Ctrl+Shift+A: select every flow matching the current filters (the loaded,
// filtered id list), not just the rendered window; again to clear.
export function toggleSelectAllShown(){
  toggleAllIds(state.selected,state.flows.map(f=>f.id));
  updateSelBar();renderRows();
}
export function toggleSelectCurrentFlow(){
  if(state.selId==null)return;
  const id=state.selId;
  if(state.selected.has(id)){
    state.selected.delete(id);
  }else{
    state.selected.add(id);
  }
  const row=document.querySelector(`.trow[data-id="${id}"]`);
  if(row){
    const has=state.selected.has(id);
    row.classList.toggle('msel',has);
    row.setAttribute('aria-pressed',has?'true':'false');
  }
  updateSelBar();
}
// buildFlowParams encodes the active filters into a query (without limit/cursor),
// shared by the initial load and the scroll-triggered page loads.
function buildFlowParams(){
  const q=new URLSearchParams();
  const f=state.filters;
  if(f.scheme)q.set('scheme',f.scheme);
  if(f.search){
    q.set('search',f.search);
    if(f.searchScope==='script')q.set('savedSearch',f.search);
    else if(f.searchScope&&f.searchScope!=='anywhere')q.set('searchScope',f.searchScope);
  }
  if(state.notesOnly)q.set('hasNote','1');
  if(f.method)q.set('method',f.method);
  if(f.status)q.set('status',f.status);
  if(f.host)q.set('host',f.host);
  if(f.tag)q.set('tag',f.tag);
  (f.exclude||[]).forEach(e=>{const k={method:'notMethod',host:'notHost',path:'notPath',status:'notStatus'}[e.field];if(k)q.append(k,e.value);});
  if(state.inScopeOnly)q.set('inScope','1');

  if(state.hideTlsFailed&&f.tag!=='tls-failed')q.set('hideTlsFailed','1');
  q.set('manual',state.showManual?'1':'0');
  q.set('ai',state.showAI?'1':'0');
  q.set('sort',state.sort.key);
  q.set('dir',sortDirParam());
  return q;
}
function bodySearchActive(){return false;}

function inspectorFilterSignature(){
  const f=state.filters;
  return JSON.stringify({
    scheme:f.scheme||'',search:f.search||'',searchScope:f.searchScope||'anywhere',
    method:f.method||'',status:f.status||'',host:f.host||'',tag:f.tag||'',
    exclude:f.exclude||[],notesOnly:!!state.notesOnly,inScopeOnly:!!state.inScopeOnly,
    hideTlsFailed:!!state.hideTlsFailed&&f.tag!=='tls-failed',
    showManual:!!state.showManual,showAI:!!state.showAI,
    sort:(state.sort&&state.sort.key)||'',dir:sortDirParam(),
  });
}

export async function loadFlows(){
  const filterSignature=inspectorFilterSignature();
  const signatureChanged=filterSignature!==flowFilterSignature;
  if(signatureChanged){flowFilterSignature=filterSignature;flowFilterEpoch++;}
  const filterEpoch=flowFilterEpoch;
  const previousSelected=state.selId;
  const previousFlow=previousSelected==null?null:(flowStore.byId.get(previousSelected)||state.detail);
  const epoch=++flowLoadEpoch;
  flowPageEpoch++;
  loadingMore=false;
  flowLoadEvents=new Map();
  flowLoadOverflow=false;
  flowRefreshing=true;
  flowPageError=null;
  flowLoadError=null;
  flowHasMore=false;
  updateTruncBanner();
  const q=buildFlowParams();
  q.set('limit',String(FLOW_FETCH+1)); // +1 row tells us whether more exist
  try{
    const d=await api('/api/flows?'+q.toString());
    if(epoch!==flowLoadEpoch)return;
    const replay=Array.from(flowLoadEvents.values());
    const replayOverflow=flowLoadOverflow;
    flowLoadEvents=new Map();
    flowLoadOverflow=false;
    // Rows remain clickable while a server-side filter request is pending.
    // Snapshot the selection at commit time, not only at request start, so a
    // user who selected a different old row during the request is reconciled
    // against the winning result too.
    const committedSelected=state.selId;
    const committedFlow=committedSelected==null?null:(flowStore.byId.get(committedSelected)||state.detail);
    let flows=d.flows||[];
    flowHasMore=flows.length>FLOW_FETCH&&!bodySearchActive();
    if(flows.length>FLOW_FETCH)flows=flows.slice(0,FLOW_FETCH);
    loadFlowStore(flowStore,flows);
    state.flows=flowStore.order;
    seenMethods.clear(); flows.forEach(f=>{ if(f.method) seenMethods.add(f.method); }); methodsDirty=true;
    let replayExact=!replayOverflow;
    for(const event of replay)if(!reconcileFlowLoadEvent(event))replayExact=false;
    // Reconcile against the replacement cache only after the latest response
    // wins. This preserves an older paged selection when its known flow still
    // matches, while restarting an invalidated pending detail request for a
    // flow that remains visible in the new cache.
    if(filterEpoch===flowFilterEpoch){
      const filterChanged=filterEpoch!==flowFilterReconciledEpoch;
      flowFilterReconciledEpoch=filterEpoch;
      if(state.selId===previousSelected)reconcileInspectorSelectionAfterReload(previousFlow,filterChanged);
      else if(state.selId===committedSelected)reconcileInspectorSelectionAfterReload(committedFlow,filterChanged);
    }
    state.flowSearchNote=d.searchNote||'';
    const box=$('#rows');if(signatureChanged&&box)box.scrollTop=0;
    renderRows();
    updateTruncBanner();
    refreshMethodFilter();
    loadTrafficDiagnosis();
    if(!replayExact)scheduleReload();
  }catch(e){
    if(epoch===flowLoadEpoch){flowHasMore=false;flowLoadError=e;}
    if(epoch===flowLoadEpoch&&filterEpoch!==flowFilterReconciledEpoch&&state.selId!=null&&!state.detail){
      selectFlowEpoch++;
      showInspectorFilterLoadError(state.selId,e);
    }
  }
  finally{if(epoch===flowLoadEpoch){flowRefreshing=false;updateTruncBanner();}}
}

// loadMoreFlows appends the next page (keyset cursor = last visible row) when the
// user scrolls near the bottom. Scroll position is preserved across the re-render.
export async function loadMoreFlows(){
  if(flowRefreshing||loadingMore||!flowHasMore||!state.flows.length)return;
  const loadEpoch=flowLoadEpoch,pageEpoch=++flowPageEpoch;
  loadingMore=true;
  flowPageError=null;
  updateTruncBanner();
  try{
    const last=state.flows[state.flows.length-1];
    if(!last){flowHasMore=false;return;}
    const q=buildFlowParams();
    appendFlowCursor(q,last);
    q.set('limit',String(FLOW_FETCH+1));
    const d=await api('/api/flows?'+q.toString());
    if(loadEpoch!==flowLoadEpoch||pageEpoch!==flowPageEpoch)return;
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
  }catch(e){
    if(loadEpoch===flowLoadEpoch&&pageEpoch===flowPageEpoch)flowPageError=e;
  }
  finally{if(pageEpoch===flowPageEpoch){loadingMore=false;updateTruncBanner();}}
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
  sel.innerHTML='<option value="">All methods</option>'+present.map(m=>`<option ${m===cur?'selected':''}>${esc(m)}</option>`).join('');
}
const seenMethods=new Set();
let methodsDirty=true; // build the method filter once initially
// flowStore.byId: id -> flow object (the same reference held in state.flows, aka
// flowStore.order). Lets live flow events (new/update, which fire per captured
// request) do O(1) lookup+refresh instead of O(N) findIndex over the whole
// loaded list — essential once you've scrolled deep.
const flowStore=createFlowStore(state.flows);
let reloadTimer=null,reloadFirstAt=0;
const RELOAD_DEBOUNCE_MS=150,RELOAD_MAX_WAIT_MS=1000;
const renderSideEpoch={req:0,res:0};
let wsRenderEpoch=0,wsReplayEpoch=0;
let selectFlowEpoch=0;
// A full History reload can replace the page cache while an Inspector detail
// request is in flight. Keep that request owned by the filter generation that
// started it, so a response for a flow excluded by the newer filter cannot
// repaint the Inspector after the selection has gone stale.
let flowFilterEpoch=0;
let flowFilterSignature='';
let flowFilterReconciledEpoch=0;
const noteSaveTails=new Map();
const noteEditorGenerations=new Map();
function noteEditorGeneration(flowId){return noteEditorGenerations.get(flowId)||0;}
const noteDrafts=new Map();
function renderFlowNoteStatus(flowId,saved=false){
  if(state.selId!==flowId)return;
  const draft=noteDrafts.get(flowId),status=$('#noteSaved'),retry=$('#noteRetry');
  if(status){
    status.textContent=draft?.error?'Save failed: '+draft.error:draft?.saving?'Saving…':draft?'Unsaved':saved?'Saved':'';
    status.dataset.state=draft?.error?'error':draft?'pending':'saved';
  }
  if(retry)retry.hidden=!draft?.error;
}
function restoreFlowNoteDraft(flowId,fallback){
  $('#noteInput').value=noteDrafts.get(flowId)?.value??fallback;
  renderFlowNoteStatus(flowId);
}
// scheduleReload debounces bursts of reload triggers but never starves: once the
// first pending trigger is RELOAD_MAX_WAIT_MS old the reload fires regardless.
export function scheduleReload(){
  const now=Date.now();
  if(!reloadFirstAt)reloadFirstAt=now;
  clearTimeout(reloadTimer);
  const wait=Math.max(0,Math.min(RELOAD_DEBOUNCE_MS,RELOAD_MAX_WAIT_MS-(now-reloadFirstAt)));
  reloadTimer=setTimeout(()=>{reloadFirstAt=0;loadFlows();},wait);
}
function reconcileInspectorSelectionAfterReload(previousFlow,filterChanged){
  if(state.selId==null)return;
  if(!state.detail&&flowStore.byId.has(state.selId)){selectFlow(state.selId);return;}
  if(!state.detail&&!flowStore.byId.has(state.selId)&&canIncremental()&&!previousFlow){selectFlow(state.selId);return;}
  if(!state.detail&&previousFlow&&canIncremental()&&flowMatchesFilters(previousFlow)){
    // If a filter refresh invalidated the first detail request, resume it
    // under the new generation even when the selected older page was evicted
    // from the replacement's first page. The retained snapshot is enough for
    // these client-decidable filters.
    selectFlow(state.selId);
    return;
  }
  // Server-only filters (text search and in-scope matching) cannot be
  // reconstructed from a retained client snapshot. If the replacement page
  // does not contain the selected flow, do not repaint retained detail as if
  // it matched; provide an explicit state instead.
  if(!canIncremental()&&!flowStore.byId.has(state.selId)){
    // Invalidate an in-flight detail response for an old row that the
    // server-only result excluded. The unavailable state must remain the
    // authoritative Inspector until the operator changes or clears filters.
    selectFlowEpoch++;
    state.detail=null;
    showInspectorSelectionUnavailable(state.selId);
    return;
  }
  if(flowStore.byId.has(state.selId)){
    if(filterChanged&&state.detail)selectFlow(state.selId);
    return;
  }
  // A page-cache replacement can legitimately evict an older selected flow;
  // preserve that selection while its known snapshot still matches filters.
  // For client-decidable filters, an explicit mismatch is authoritative and
  // should close the Inspector instead of leaving a stale loading pane.
  if(previousFlow&&canIncremental()&&!flowMatchesFilters(previousFlow))closeInspector();
  else if(previousFlow&&canIncremental()&&filterChanged&&state.detail)selectFlow(state.selId);
  else if(!state.detail)showInspectorSelectionUnavailable(state.selId);
}
function setInspectorActionState(disabled){
  ['#inspectSendRepeater','#inspectSendIntruder','#inspectMoreActions'].forEach(sel=>{
    const button=$(sel);if(!button)return;
    button.disabled=disabled;
    button.setAttribute('aria-disabled',disabled?'true':'false');
  });
}
function showInspectorLoading(id){
  setInspectorActionState(true);
  const req=$('#reqView'),res=$('#resView'),status=$('#resStatus'),noteBar=$('#noteBar'),reqDecode=$('#reqDecode'),resDecode=$('#resDecode');
  if(reqDecode)reqDecode.hidden=true;
  if(resDecode)resDecode.hidden=true;
  if(noteBar)noteBar.style.display='none';
  if(req)req.innerHTML='<span class="inspector-state" role="status" aria-live="polite">Loading flow #'+esc(id)+'…</span>';
  if(res)res.innerHTML='<span class="inspector-state" role="status" aria-live="polite">Loading request and response…</span>';
  if(status){status.textContent='loading…';status.style.color='var(--fg3)';}
}
function showInspectorLoadError(id,error){
  setInspectorActionState(true);
  const message=error&&error.message?error.message:String(error||'unknown error');
  const retry='<button type="button" class="btn xs" data-inspector-retry>Retry</button>';
  const content='<span class="state-error-msg">Flow #'+esc(id)+' could not be loaded: '+esc(message)+'</span> '+retry;
  const req=$('#reqView'),res=$('#resView'),status=$('#resStatus'),noteBar=$('#noteBar');
  if(noteBar)noteBar.style.display='none';
  if(req)req.innerHTML='<div class="state-error" role="alert">'+content+'</div>';
  if(res)res.innerHTML='<div class="state-error" role="alert">Select Retry to request this flow again.</div>';
  if(status){status.textContent='load failed';status.style.color='var(--red)';}
  $$('#reqView [data-inspector-retry]').forEach(button=>button.onclick=()=>selectFlow(id));
}
function showInspectorFilterLoadError(id,error){
  setInspectorActionState(true);
  const message=error&&error.message?error.message:String(error||'unknown error');
  const retry='<button type="button" class="btn xs" data-inspector-filter-retry>Retry History refresh</button>';
  const req=$('#reqView'),res=$('#resView'),status=$('#resStatus'),noteBar=$('#noteBar');
  if(noteBar)noteBar.style.display='none';
  const content='History filters could not be refreshed: '+esc(message)+' — Inspector paused for flow #'+esc(id)+'. '+retry;
  if(req)req.innerHTML='<div class="state-error" role="alert">'+content+'</div>';
  if(res)res.innerHTML='<div class="state-error" role="alert">Retry the History refresh to confirm whether this flow matches the current filters.</div>';
  if(status){status.textContent='History refresh failed';status.style.color='var(--red)';}
  $$('#reqView [data-inspector-filter-retry]').forEach(button=>button.onclick=()=>loadFlows());
}
function showInspectorSelectionUnavailable(id){
  setInspectorActionState(true);
  const req=$('#reqView'),res=$('#resView'),status=$('#resStatus'),noteBar=$('#noteBar');
  if(noteBar)noteBar.style.display='none';
  if(req)req.innerHTML='<div class="state-empty" role="status"><div class="state-empty-title">Flow #'+esc(id)+' is not available under the current search or scope filters.</div><p class="state-empty-hint">Clear or adjust the History filters to inspect this flow.</p></div>';
  if(res)res.innerHTML='<div class="state-empty" role="status"><p class="state-empty-hint">No response can be shown until the selected flow matches the current filters.</p></div>';
  if(status){status.textContent='not in current filters';status.style.color='var(--fg3)';}
}
// syncSelectedRow moves the inspected-row highlight by touching only the old and
// new rows. It returns false when a multi-select is (or was) drawn, because the
// msel/aria-pressed state of arbitrary rows may then be stale and needs renderRows.
function syncSelectedRow(prevId){
  const box=$('#rows');
  if(!box||state.selected.size||box.querySelector('.trow.msel'))return false;
  const rows=new Map();
  for(const id of new Set([prevId,state.selId])){
    if(id==null)continue;
    const row=box.querySelector('.trow[data-id="'+id+'"]');
    if(!row)continue;
    if(!flowStore.byId.get(id))return false;
    rows.set(id,row);
  }
  rows.forEach((row,id)=>{
    const sel=id===state.selId;
    row.classList.toggle('sel',sel);
    row.setAttribute('aria-current',sel?'true':'false');
    row._flowHTML=flowRowHTML(flowStore.byId.get(id));
  });
  return true;
}
export async function selectFlow(id){
  const selectEpoch=++selectFlowEpoch;
  const filterEpoch=flowFilterEpoch;
  const current=()=>selectFlowEpoch===selectEpoch&&state.selId===id&&flowFilterEpoch===filterEpoch;
  const switching=state.selId!==id;
  const needsLoadingState=switching||!state.detail;
  if(switching){
    state.detail=null;
    const note=$('#noteInput');if(note)note.value='';
  }
  const prevSelId=state.selId;
  state.selId=id;
  if(switching)onAuthzSelectionChanged();
  if(!syncSelectedRow(prevSelId))renderRows();
  openDockedDrawer(id);
  if(needsLoadingState)showInspectorLoading(id);
  try{
    const pendingNoteSave=noteSaveTails.get(id);
    if(pendingNoteSave){await pendingNoteSave;if(!current())return;}
    const noteGeneration=noteEditorGeneration(id);
    const preserveNoteDraft=state.selId===id&&state.detail&&$('#noteInput').value!==(state.detail.note||'');
    const d=await api('/api/flows/'+id);
    if(!current())return;
    if(canIncremental()&&!flowMatchesFilters(d)){closeInspector();return;}
    state.detail=d;
    if(!preserveNoteDraft&&noteEditorGeneration(id)===noteGeneration)restoreFlowNoteDraft(id,d.note||'');
    setInspectorActionState(false);
    $('#noteBar').style.display='flex';
    await renderSide('req');
    if(!current())return;
    if(d.flags&FLAG_WS){
      $('#resStatus').textContent='WebSocket frames';$('#resStatus').style.color='var(--accent)';
      await renderWSFrames(id);
      if(!current())return;
    }else if(d.flags&FLAG_TLS){
      $('#resView').innerHTML=`<div class="tls-blocked"><strong class="u-danger">TLS MITM failed</strong> — the app reached the proxy (CONNECT) but rejected the certificate before sending any HTTP request.<br><br>Likely <strong>SSL pinning</strong> or an untrusted CA (Android 7+ ignores user CAs).<br><br><span class="u-fg3">${esc(d.error||'')}</span><br><br>Try Frida/objection, a patched APK, or <code>android_setup</code> with <code>caMode:system</code> on an emulator.</div>`;
      $('#resStatus').textContent='TLS blocked';$('#resStatus').style.color='var(--red)';
    }else if(!d.status&&!d.error){
      // In-flight request: response not back yet. The flow.update handler
      // re-selects this flow once it lands, filling the pane in automatically.
      $('#resView').innerHTML='<span class="blink u-fg3">waiting for response…</span>';
      $('#resStatus').textContent='pending';$('#resStatus').style.color='var(--fg3)';
    }else{
      await renderSide('res');
      if(!current())return;
      $('#resStatus').textContent=(d.status?`${d.status} ${statusText(d.status)}`:(d.error||''))+(d.durationMs?` · ${fmtDur(d.durationMs)}`:'');
      $('#resStatus').style.color=statusColor(d.status);
    }
  }catch(e){if(current())showInspectorLoadError(id,e);}
}
// fetchRawMessage caches the raw message per flowId:side for the lifetime of one
// state.detail, so typing in Find-in-response re-highlights without a refetch.
let rawCache={detail:null,map:new Map()};
function fetchRawMessage(flowId,side,detail){
  if(rawCache.detail!==detail)rawCache={detail,map:new Map()};
  const key=flowId+':'+side;
  if(rawCache.map.has(key))return Promise.resolve(rawCache.map.get(key));
  const cache=rawCache;
  return api('/api/flows/'+flowId+'/raw?side='+side).then(raw=>{cache.map.set(key,raw);return raw;});
}
function wsFrameRow(dir,opcode,length,text){
  const arrow=dir==='send'?'<span class="ws-dir-send">▲ send</span>':'<span class="u-accent">▼ recv</span>';
  const replayable=opcode===1; // text frames only — binary has no editable text to load
  return `<div class="ws-frame${replayable?' ws-frame-replay':''}"${replayable?` data-replay="${escAttr(text)}" title="Click to load this frame into the replay box"`:''}>
    <span class="ws-col ws-col-dir">${arrow}</span>
    <span class="ws-col ws-col-op">${wsOpcodeName(opcode)}</span>
    <span class="ws-col ws-col-len">${length} B</span>
    <span class="ws-col-text">${esc(text)}</span>${replayable?'<span class="hint ws-load-hint">↩ load</span>':''}</div>`;
}
// wireWsFrames makes text frames click-to-replay: clicking loads that frame's text
// into the #wsMsg box (the most-expected WS-replay affordance that was missing).
function wireWsFrames(root){
  if(!root)return;
  root.querySelectorAll('.ws-frame-replay').forEach(el=>{
    const activate=()=>{const m=$('#wsMsg');if(m){m.value=el.dataset.replay||'';m.focus();}};
    el.setAttribute('aria-label','Load this text frame into the WebSocket replay editor');
    wireRowKey(el,activate);
  });
}
function flowWsURL(d){const s=d.scheme==='https'?'wss':'ws';const def=(d.scheme==='https'&&d.port===443)||(d.scheme==='http'&&d.port===80);return `${s}://${d.host}${def?'':':'+d.port}${d.path||'/'}`;}
export async function renderWSFrames(id){
  const epoch=++wsRenderEpoch;
  const detail=state.detail;
  const selectEpoch=selectFlowEpoch;
  const filterEpoch=flowFilterEpoch;
  const current=()=>selectFlowEpoch===selectEpoch&&state.selId===id&&state.detail===detail&&flowFilterEpoch===filterEpoch&&wsRenderEpoch===epoch;
  try{
    const d=await api('/api/flows/'+id+'/ws');const frames=d.frames||[];
    if(!current())return;
    const url=flowWsURL(detail||{});
    const list=frames.length?frames.map(f=>wsFrameRow(f.dir,f.opcode,f.length,f.preview)).join('')
      :'<span class="u-fg3">No frames captured yet — frames stream in live as the socket exchanges messages.</span>';
    // A live frame only refreshes the frame list: rebuilding the whole pane would
    // destroy the replay input's value and focus mid-typing.
    const existing=$('#wsFrameList');
    if(existing&&existing.dataset.flowId===String(id)&&$('#wsMsg')&&$('#resView').contains(existing)){
      existing.innerHTML=list;
      wireWsFrames(existing);
      return;
    }
    const box=`<div class="ws-replay-row">
        <input id="wsMsg" aria-label="WebSocket replay message for ${escAttr(url)}" placeholder="Replay a frame to ${escAttr(url)}" class="ws-replay-input">
        <button class="btn accent" id="wsSendBtn">▲ Send</button></div>
      <div id="wsReplayOut" class="ws-replay-out"></div>`;
    $('#resView').innerHTML=box+`<div id="wsFrameList" data-flow-id="${id}">${list}</div>`;
    wireWsFrames($('#resView'));
    const sb=document.getElementById('wsSendBtn');if(sb)sb.onclick=()=>wsReplay(url);
    const inp=document.getElementById('wsMsg');if(inp)inp.onkeydown=e=>{if(e.key==='Enter')wsReplay(url);};
  }catch(e){if(current())$('#resView').textContent='(error: '+e.message+')';}
}
// scheduleWSFrames coalesces a burst of ws.frame events into one trailing refresh.
const WS_FRAME_COALESCE_MS=250;
let wsFrameTimer=null,wsFrameId=null;
export function scheduleWSFrames(id){
  wsFrameId=id;
  if(wsFrameTimer)return;
  wsFrameTimer=setTimeout(()=>{
    wsFrameTimer=null;
    if(wsFrameId!=null&&state.selId===wsFrameId)renderWSFrames(wsFrameId);
  },WS_FRAME_COALESCE_MS);
}
async function wsReplay(url){
  const out=$('#wsReplayOut'),button=$('#wsSendBtn');
  if(!out||!button||button.disabled)return;
  const epoch=++wsReplayEpoch,flowId=state.selId,detail=state.detail,selectionEpoch=selectFlowEpoch;
  const current=()=>epoch===wsReplayEpoch&&selectFlowEpoch===selectionEpoch&&state.selId===flowId&&state.detail===detail&&out.isConnected&&button.isConnected&&$('#wsReplayOut')===out&&$('#wsSendBtn')===button;
  const msg=($('#wsMsg')||{}).value||'';
  if(button){button.disabled=true;button.setAttribute('aria-busy','true');button.textContent='Sending…';}
  if(out)out.innerHTML='<span class="u-fg3">opening socket…</span>';
  try{
    const r=await api('/api/ws/send',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({url,message:msg})});
    const frames=r.frames||[];
    const head=`<div class="micro-label ws-send-head">${r.status!==101?`Handshake HTTP ${r.status} · `:''}Sent · ${frames.length} frame${frames.length===1?'':'s'} received</div>`;
    if(!current())return;
    if(out){out.innerHTML=head+frames.map(f=>wsFrameRow(f.dir,f.opcode,f.len,f.text)).join('');wireWsFrames(out);}
  }catch(e){if(current())out.innerHTML='<span class="u-danger">'+esc(e.message)+'</span>';
  }finally{if(current()){button.disabled=false;button.setAttribute('aria-busy','false');button.textContent='▲ Send';}}
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
  const flowId=state.selId;
  const detail=state.detail;
  const selectEpoch=selectFlowEpoch;
  const filterEpoch=flowFilterEpoch;
  const epoch=++renderSideEpoch[side];
  if(!flowId||!detail){return;}
  const len=side==='req'?detail.reqLen:detail.resLen;
  // Binary body (image/font/media/archive/…): show only the headers — the bytes
  // aren't readable as text. Built from the detail DTO, so the body isn't fetched.
  const mime=bodyMime(detail,side);
  // "Render" only makes sense for HTML; for JSON/images/etc. it used to silently
  // fall through to an ugly raw view. Hide the button and fall back to Pretty.
  if(side==='res'){
    const isHtml=!!mime&&/html/i.test(mime);
    const renderBtn=document.querySelector('#inspect .seg[data-side="res"] button[data-view="render"]');
    if(renderBtn)renderBtn.style.display=isHtml?'':'none';
    if(!isHtml&&state.view.res==='render'){
      state.view.res='pretty';
      const seg=document.querySelector('#inspect .seg[data-side="res"]');
      if(seg)seg.querySelectorAll('button').forEach(b=>{const on=b.dataset.view==='pretty';b.classList.toggle('on',on);b.setAttribute('aria-pressed',on?'true':'false');});
    }
  }
  const view=state.view[side];
  const current=()=>selectFlowEpoch===selectEpoch&&renderSideEpoch[side]===epoch&&state.selId===flowId&&state.detail===detail&&flowFilterEpoch===filterEpoch&&state.view[side]===view;
  const draw=async()=>{
    try{
      if(view==='decoded'){
        const d=await api('/api/flows/'+flowId+'/decoded?side='+side);
        if(!current())return;
        if(!d.matched){
          el.innerHTML=`<div class="hint body-note">No project message codec matched this ${side==='req'?'request':'response'}.<br>
            Add one under <b>Scanner → Codecs</b> (or <code>project/codecs/*.star</code>).</div>`;
          return;
        }
        if(d.error){
          el.innerHTML=`<div class="hint body-note u-danger">Codec <b>${esc(d.codecId||'')}</b> error: ${esc(d.error)}</div>`;
          return;
        }
        const fields=d.fields&&Object.keys(d.fields).length
          ? `<div class="hint codec-fields">Decoded fields: ${Object.keys(d.fields).map(k=>`<code>${esc(k)}</code>`).join(', ')}</div>` : '';
        const badge=`<div class="hint codec-badge">Decoded for display · <b>${esc(d.title||d.codecId||'')}</b>${d.applyOnSend?' · apply_on_send':''}${d.note?' · '+esc(d.note):''}</div>`;
        const body=typeof d.plaintext==='string'?d.plaintext:'';
        el._rawText=body;
        el._pretty=true;
        el.innerHTML=badge+fields+'<pre class="codec-pre">'+highlightBodyText(body,mime||'application/json')+'</pre>';
        return;
      }
      if(view==='hex'){
        const raw=await fetchRawMessage(flowId,side,detail);
        if(!current())return;
        el._rawText=raw;
        el._pretty=false;
        el.innerHTML='<pre class="hex-dump">'+esc(formatHexDump(raw))+'</pre>';
        return;
      }
      const raw=await fetchRawMessage(flowId,side,detail);
      if(!current())return;
      el._rawText=raw;
      el._pretty=view==='pretty';
      if(side==='res'&&view==='render'&&mime&&/html/i.test(mime)){
        el.innerHTML=renderHTMLResponse(raw);
        return;
      }
      let html=highlightHTTP(view==='pretty'?prettify(raw):raw,view==='pretty',mime);
      const fq=($('#inspectFindIn')||{}).value;
      const stat=$('#inspectFindStat');
      if(side==='res'&&fq&&fq.length>1){
        const r=markFindInHtml(html,fq);html=r.html;
        if(stat)stat.textContent=r.count?r.count+' match'+(r.count===1?'':'es'):'no matches';
      }else if(stat){stat.textContent='';}
      el.innerHTML=html;
    }catch(e){if(current())el.textContent='(error: '+e.message+')';}
  };
  if(isBinaryMime(mime)){
    if(view==='hex'){
      await draw();
      return;
    }
    const dl=flowBodyDownloadName(flowId,side,mime),href=flowBodyDownloadHref(flowId,side);
    el.innerHTML=highlightHTTP(headerBlockText(detail,side))+
      `<div class="hint body-note body-note-flush">Body is <b>${esc(mime)}</b>${len?' · '+fmtSize(len):''} — binary, not rendered.<br>
        <a class="btn body-action u-inline-block" href="${href}" download="${escAttr(dl)}">⤓ Download body</a>
        <button class="btn body-action body-action-next" data-bin="1">Show raw anyway</button>
        <button class="btn body-action body-action-next" data-bin-hex="1">Hex dump</button></div>`;
    const b=el.querySelector('[data-bin]');
    if(b)b.onclick=()=>{el.innerHTML='<span class="hint body-pad">rendering…</span>';setTimeout(draw,10);};
    const bHex=el.querySelector('[data-bin-hex]');
    if(bHex)bHex.onclick=()=>{
      state.view[side]='hex';
      const seg=document.querySelector('#inspect .seg[data-side="'+side+'"]');
      if(seg)seg.querySelectorAll('button').forEach(x=>{const on=x.dataset.view==='hex';x.classList.toggle('on',on);x.setAttribute('aria-pressed',on?'true':'false');});
      el.innerHTML='<span class="hint body-pad">rendering…</span>';
      setTimeout(draw,10);
    };
    return;
  }
  if(len>RENDER_CAP){
    const dl=flowBodyDownloadName(flowId,side,mime),href=flowBodyDownloadHref(flowId,side);
    el.innerHTML=`<div class="hint body-note body-note-lg">${side==='req'?'Request':'Response'} body is <b>${fmtSize(len)}</b> — not shown, to keep the browser responsive.<br>
      <a class="btn body-action u-inline-block" href="${href}" download="${escAttr(dl)}">⤓ Download body</a>
      <button class="btn body-action" data-bigshow="1">Show anyway</button></div>`;
    const b=el.querySelector('[data-bigshow]');
    if(b)b.onclick=()=>{el.innerHTML='<span class="hint body-pad">rendering…</span>';setTimeout(draw,10);};
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
export function openInspectFind(){toggleInspectFind(true);}
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
  if(e.key==='Escape'&&inspectFindBar.style.display==='flex'){
    e.preventDefault();e.stopImmediatePropagation();toggleInspectFind(false);return;
  }
  if((e.ctrlKey||e.metaKey)&&e.key.toLowerCase()==='f'){
    const p=document.querySelector('.panel[data-panel="proxy"]');
    if(!p||!p.classList.contains('active'))return;
    const t=e.target;if(t&&/^(INPUT|TEXTAREA|SELECT)$/.test(t.tagName))return;
    e.preventDefault();toggleInspectFind(true);
  }
});

loadFlowCols();
loadFlowColW();
renderFlowHead();
if(flowPhoneQuery)flowPhoneQuery.addEventListener('change',()=>{refreshRowHeight();renderFlowHead();renderRows();syncDock();});
{const b=$('#colPickerBtn');if(b)b.onclick=e=>{e.stopPropagation();toggleColPicker();};}
{const m=$('#colPicker');if(m)m.onclick=e=>e.stopPropagation();}
document.addEventListener('click',()=>closeColPicker());
document.addEventListener('keydown',e=>{
  if(e.key==='Escape'&&$('#colPicker')?.style.display==='block'){
    e.preventDefault();e.stopImmediatePropagation();closeColPicker(true);
  }
},true);
document.addEventListener('focusin',e=>{if(!e.target.closest('#colPicker,#colPickerBtn'))closeColPicker();});
window.addEventListener('resize',()=>closeColPicker());
window.visualViewport?.addEventListener('resize',()=>closeColPicker());

$('#fMethod').onchange=e=>setFilter('method',e.target.value);
$('#fStatus').onchange=e=>setFilter('status',e.target.value);
$('#fSearch').oninput=e=>{state.filters.search=e.target.value;renderChips();scheduleReload();};
function syncSearchPlaceholder(){
  const inp=$('#fSearch'),sc=state.filters.searchScope||'anywhere';
  if(!inp)return;
  inp.placeholder=sc==='id'?'Flow id (e.g. 285 or #285)…':sc==='body'?'Search request/response bodies…':sc==='headers'?'Search request/response headers…':sc==='metadata'?'Search flow metadata…':sc==='script'?'Search with saved script…':'Search method / host / path / #id…';
}
if($('#fSearchScope'))$('#fSearchScope').onchange=e=>{state.filters.searchScope=e.target.value||'anywhere';syncSearchPlaceholder();if(state.filters.search)loadFlows();};
syncSearchPlaceholder();
const defaultFlowSearch={searchScope:'anywhere'};
const flowSearchUI={items:[],name:''};
let flowSearchSourceEpoch=0,flowSearchLoadEpoch=0,flowSearchEditEpoch=0,flowSearchTestEpoch=0,flowSearchTestPending=false,flowSearchMutationPending=false;
function flowSearchStatus(text,error=false){const el=$('#flowSearchScriptError'),status=$('#flowSearchScriptStatus');if(el)el.textContent=error?String(text||''):'';if(status)status.textContent=error?'':String(text||'');}
function flowSearchPayload(){return {name:($('#flowSearchScriptName')||{}).value.trim(),scope:'anywhere',script:($('#flowSearchScriptEditor')||{}).value||'',flowId:state.selId||0};}
function renderFlowSearches(){const list=$('#flowSearchScriptList');if(!list)return;list.innerHTML='<option value="">new search…</option>'+flowSearchUI.items.map(x=>`<option value="${escAttr(x.name)}">${esc(x.name)} · ${esc(x.scope||'anywhere')}</option>`).join('');list.value=flowSearchUI.name;}
async function loadFlowSearches(){const epoch=++flowSearchLoadEpoch;try{const d=await api('/api/flow-searches');if(epoch!==flowSearchLoadEpoch||flowSearchMutationPending)return;flowSearchUI.items=d.searches||[];renderFlowSearches();}catch(e){if(epoch===flowSearchLoadEpoch&&!flowSearchMutationPending)flowSearchStatus(e.message,true);}}
async function loadFlowSearchSource(name){
  const epoch=++flowSearchSourceEpoch;
  const current=()=>epoch===flowSearchSourceEpoch&&$('#flowSearchScriptList')?.value===name;
  try{
    const d=await api('/api/flow-searches/'+encodeURIComponent(name)+'/source');
    if(!current())return;
    flowSearchUI.name=name;$('#flowSearchScriptName').value=d.name||name;$('#flowSearchScriptEditor').value=d.script||'';
    state.filters.search=name;state.filters.searchScope='script';syncControls();renderChips();renderFlowSearches();flowSearchStatus('loaded');loadFlows();
  }catch(e){if(current())flowSearchStatus(e.message,true);}
}
async function testFlowSearch(){
  const p=flowSearchPayload();if(!p.name){flowSearchStatus('name required',true);return;}if(!p.script.trim()){flowSearchStatus('script required',true);return;}if(flowSearchTestPending)return;
  const epoch=++flowSearchTestEpoch,editEpoch=flowSearchEditEpoch,button=$('#flowSearchScriptTest');
  const current=()=>epoch===flowSearchTestEpoch&&editEpoch===flowSearchEditEpoch;
  flowSearchTestPending=true;if(button){button.disabled=true;button.setAttribute('aria-busy','true');}flowSearchStatus('testing…');
  try{const d=await api('/api/flow-searches/test',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify(p)});if(current())flowSearchStatus(d.valid?'valid':'invalid',!d.valid);}
  catch(e){if(current())flowSearchStatus(e.message,true);}
  finally{if(epoch===flowSearchTestEpoch){flowSearchTestPending=false;if(button){button.disabled=flowSearchMutationPending;button.setAttribute('aria-busy',flowSearchMutationPending?'true':'false');}}}
}
function setFlowSearchMutationPending(pending){flowSearchMutationPending=pending;['#flowSearchScriptTest','#flowSearchScriptSave','#flowSearchScriptDelete'].forEach(sel=>{const button=$(sel),busy=pending||(sel==='#flowSearchScriptTest'&&flowSearchTestPending);if(button){button.disabled=busy;button.setAttribute('aria-busy',busy?'true':'false');}});}
async function saveFlowSearch(){
  const p=flowSearchPayload();if(!p.name){flowSearchStatus('name required',true);return;}if(!p.script.trim()){flowSearchStatus('script required',true);return;}if(flowSearchMutationPending)return;
  const sourceEpoch=flowSearchSourceEpoch;setFlowSearchMutationPending(true);flowSearchLoadEpoch++;flowSearchStatus('saving…');
  let saved=false;
  try{const exists=flowSearchUI.items.some(x=>x.name===p.name);await api(exists?'/api/flow-searches/'+encodeURIComponent(p.name):'/api/flow-searches',{method:exists?'PUT':'POST',headers:{'content-type':'application/json'},body:JSON.stringify(p)});saved=true;if(sourceEpoch===flowSearchSourceEpoch&&$('#flowSearchScriptName')?.value.trim()===p.name){flowSearchUI.name=p.name;flowSearchStatus('saved');}}
  catch(e){if(sourceEpoch===flowSearchSourceEpoch)flowSearchStatus(e.message,true);}
  finally{setFlowSearchMutationPending(false);}
  if(saved)await loadFlowSearches();
}
async function deleteFlowSearch(){
  const name=flowSearchUI.name||($('#flowSearchScriptName')||{}).value.trim();if(!name||flowSearchMutationPending)return;
  const sourceEpoch=flowSearchSourceEpoch;setFlowSearchMutationPending(true);flowSearchLoadEpoch++;
  let deleted=false;
  try{await api('/api/flow-searches/'+encodeURIComponent(name),{method:'DELETE'});deleted=true;flowSearchUI.items=flowSearchUI.items.filter(x=>x.name!==name);if(sourceEpoch===flowSearchSourceEpoch){flowSearchUI.name='';$('#flowSearchScriptName').value='';$('#flowSearchScriptEditor').value='';flowSearchStatus('deleted');}}
  catch(e){if(sourceEpoch===flowSearchSourceEpoch)flowSearchStatus(e.message,true);}
  finally{setFlowSearchMutationPending(false);}
  if(deleted)await loadFlowSearches();else renderFlowSearches();
}
$('#flowSearchScriptList')&&($('#flowSearchScriptList').onchange=e=>{flowSearchEditEpoch++;if(e.target.value)loadFlowSearchSource(e.target.value);else{++flowSearchSourceEpoch;flowSearchUI.name='';$('#flowSearchScriptName').value='';$('#flowSearchScriptEditor').value='def match(flow):\\n  return False';state.filters.search='';state.filters.searchScope='anywhere';syncControls();renderChips();flowSearchStatus('');renderFlowSearches();loadFlows();}});
['#flowSearchScriptName','#flowSearchScriptEditor'].forEach(sel=>{$(sel)?.addEventListener('input',()=>{flowSearchSourceEpoch++;flowSearchEditEpoch++;flowSearchStatus(flowSearchMutationPending?'edited while save pending — save again':'unsaved');});});
$('#flowSearchScriptTest')&&($('#flowSearchScriptTest').onclick=testFlowSearch);
$('#flowSearchScriptSave')&&($('#flowSearchScriptSave').onclick=saveFlowSearch);
$('#flowSearchScriptDelete')&&($('#flowSearchScriptDelete').onclick=deleteFlowSearch);
loadFlowSearches();
if($('#notesFilter'))$('#notesFilter').onclick=()=>{state.notesOnly=!state.notesOnly;const nf=$('#notesFilter');nf.classList.toggle('on',state.notesOnly);nf.setAttribute('aria-pressed',state.notesOnly?'true':'false');renderChips();loadFlows();};
if($('#hideTlsFilter'))$('#hideTlsFilter').onclick=()=>{
  const next=!state.hideTlsFailed;
  if(next&&state.filters.tag==='tls-failed')state.filters.tag='';
  state.hideTlsFailed=next;
  try{localStorage.setItem(HIDE_TLS_KEY,state.hideTlsFailed?'1':'0');}catch(e){}
  syncHideTlsFilter();renderChips();renderTagBar();loadFlows();
};
syncHideTlsFilter();
// syncScopeToggle keeps the In scope only chip's state, icon and label together
// (the old code overwrote the whole button text with a Unicode glyph).
function syncScopeToggle(on){
  state.inScopeOnly=!!on;
  const st=$('#scopeToggle');if(!st)return;
  st.classList.toggle('on',state.inScopeOnly);
  st.setAttribute('aria-pressed',state.inScopeOnly?'true':'false');
  st.innerHTML=icon('scope')+' In scope only';
}
$('#scopeToggle').onclick=()=>{
  syncScopeToggle(!state.inScopeOnly);
  renderChips();
  loadFlows();
};
function syncSourceFilters(){
  const mf=$('#manualFilter');
  if(mf){mf.classList.toggle('on',state.showManual);mf.setAttribute('aria-pressed',state.showManual?'true':'false');}
  const af=$('#aiFilter');
  if(af){af.classList.toggle('on',state.showAI);af.setAttribute('aria-pressed',state.showAI?'true':'false');}
}
export { syncSourceFilters };
$('#manualFilter')&&($('#manualFilter').onclick=()=>{state.showManual=!state.showManual;syncSourceFilters();renderChips();loadFlows();});
$('#aiFilter')&&($('#aiFilter').onclick=()=>{state.showAI=!state.showAI;syncSourceFilters();renderChips();loadFlows();});
 syncSourceFilters();
export function saveNote(){
  const flowId=state.selId,detail=state.detail;
  if(!flowId)return Promise.resolve();
  const note=$('#noteInput').value;
  const editorGeneration=noteEditorGeneration(flowId);
  if(detail&&note===(detail.note||'')&&!noteSaveTails.has(flowId)){
    noteDrafts.delete(flowId);renderFlowNoteStatus(flowId);return Promise.resolve();
  }
  const draft={value:note,saving:true,error:''};
  noteDrafts.set(flowId,draft);renderFlowNoteStatus(flowId);
  const previous=noteSaveTails.get(flowId)||Promise.resolve();
  const save=previous.then(async()=>{
    try{
      await api('/api/flows/'+flowId+'/note',{method:'PUT',headers:{'content-type':'application/json'},body:JSON.stringify({note})});
      if(detail)detail.note=note;
      const fl=flowStore.byId.get(flowId);
      if(fl){fl.note=note;patchFlowRow(fl);}
      if(noteDrafts.get(flowId)===draft)noteDrafts.delete(flowId);
      if(state.selId===flowId&&noteEditorGeneration(flowId)===editorGeneration&&$('#noteInput').value===note){
        if(state.detail)state.detail.note=note;
        renderFlowNoteStatus(flowId,true);
      }
    }catch(e){
      draft.saving=false;draft.error=e.message||'Could not save';
      renderFlowNoteStatus(flowId);
    }
  });
  const tail=save.catch(()=>{});
  noteSaveTails.set(flowId,tail);
  tail.finally(()=>{if(noteSaveTails.get(flowId)===tail)noteSaveTails.delete(flowId);});
  return save;
}
$('#noteInput').addEventListener('keydown',e=>{if(e.key==='Enter'){e.preventDefault();$('#noteInput').blur();}});
$('#noteInput').addEventListener('input',()=>{
  const flowId=state.selId;if(!flowId)return;
  noteEditorGenerations.set(flowId,noteEditorGeneration(flowId)+1);
  noteDrafts.set(flowId,{value:$('#noteInput').value,error:'',saving:false});
  renderFlowNoteStatus(flowId);
});
$('#noteInput').addEventListener('blur',saveNote);
$('#noteRetry').onclick=saveNote;
/* ---- saved views (one dropdown: apply / save / delete) ---- */
let viewsLoadError=null,viewsLoadEpoch=0,viewsMutationPending=false;
export async function loadViews(){const epoch=++viewsLoadEpoch;try{const d=await api('/api/views');if(epoch!==viewsLoadEpoch||viewsMutationPending)return;viewsLoadError=null;state.views=d.views||[];renderViews();}catch(e){if(epoch!==viewsLoadEpoch||viewsMutationPending)return;viewsLoadError=e;renderViews();}}
export function renderViews(){
  const btn=$('#viewsBtn'); if(!btn)return;
  if(viewsLoadError){btn.textContent='Views !';btn.title='Saved views unavailable: '+(viewsLoadError.message||viewsLoadError)+' — click to retry';return;}
  const n=state.views.length;
  const txt=n?('Views ▾ · '+n):'Views ▾';
  btn.textContent=txt;
  btn.title=n?(n+' saved view'+(n===1?'':'s')+' — click to apply, save, or delete'):'No saved views yet — click to save the current filters as a view';
}
function applyView(v){
  let f={};try{f=JSON.parse(v.data||'{}');}catch(e){}
  state.filters={scheme:f.scheme||'',method:f.method||'',status:f.status||'',search:f.search||'',searchScope:f.searchScope||'anywhere',host:f.host||'',tag:f.tag||'',exclude:Array.isArray(f.exclude)?f.exclude:[]};
  state.inScopeOnly=!!f.inScope;
  state.notesOnly=!!f.notesOnly;
  state.showManual=f.showManual!==false;state.showAI=f.showAI!==false;
  state.hideTlsFailed=f.hideTlsFailed!==false;
  const notes=$('#notesFilter');if(notes){notes.classList.toggle('on',state.notesOnly);notes.setAttribute('aria-pressed',state.notesOnly?'true':'false');}
  syncSourceFilters();syncHideTlsFilter();
  syncControls();syncScopeToggle(state.inScopeOnly);
  renderChips();renderTagBar();loadFlows();
  toast('applied view: '+v.name);
}
async function saveCurrentView(){
  const name=await uiPrompt({title:'Save current filters as a view',placeholder:'view name'});if(!name)return;
  if(viewsMutationPending)return;
  const data={...state.filters,inScope:state.inScopeOnly,notesOnly:state.notesOnly,showManual:state.showManual,showAI:state.showAI,hideTlsFailed:state.hideTlsFailed};
  viewsMutationPending=true;viewsLoadEpoch++;if($('#viewsBtn'))$('#viewsBtn').disabled=true;
  try{await api('/api/views',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({name,data})});toast('view saved');}
  catch(e){toastError('Save view failed',e);}
  finally{viewsMutationPending=false;if($('#viewsBtn'))$('#viewsBtn').disabled=false;loadViews();}
}
async function deleteView(id,name){
  if(!await uiConfirm('Delete view','Delete saved view <b>'+esc(name)+'</b>?','Delete','btn danger','var(--red)'))return;
  if(viewsMutationPending)return;
  viewsMutationPending=true;viewsLoadEpoch++;if($('#viewsBtn'))$('#viewsBtn').disabled=true;
  try{await api('/api/views/'+id,{method:'DELETE'});toast('view deleted');}
  catch(e){toastError('Delete view failed',e);}
  finally{viewsMutationPending=false;if($('#viewsBtn'))$('#viewsBtn').disabled=false;loadViews();}
}
function openViewsMenu(){
  const btn=$('#viewsBtn'); if(!btn)return;
  btn.setAttribute('aria-haspopup','menu');
  {const cp=$('#colPicker'),cpb=$('#colPickerBtn');if(cp)cp.style.display='none';if(cpb)cpb.setAttribute('aria-expanded','false');} // Views joins the toolbar menu group
  const r=btn.getBoundingClientRect();
  const sections=[];
  if(viewsLoadError){
    sections.push({head:'SAVED VIEWS UNAVAILABLE',items:[{label:'Retry loading saved views',act:loadViews}]});
  }else if(state.views.length){
    sections.push({head:'APPLY VIEW',items:state.views.map(v=>({label:v.name,act:()=>applyView(v)}))});
    sections.push({head:'DELETE VIEW',items:state.views.map(v=>({label:v.name,danger:true,act:()=>deleteView(v.id,v.name)}))});
  }
  if(!viewsLoadError)sections.push({items:[{label:'＋ Save current filters as a view…',act:saveCurrentView}]});
  openCtxMenu(r.left, r.bottom+2, sections, btn);
}
$('#viewsBtn')&&($('#viewsBtn').onclick=e=>{e.stopPropagation();openViewsMenu();});
/* ---- target scope ---- */
let scopeLoadEpoch=0,scopeMutationEpoch=0,scopeMutationLanes=new Map(),scopeMutationRevision=new Map(),scopeDrafts=new Map();
export async function loadScope(){
  const epoch=++scopeLoadEpoch,mutationSnapshot=scopeMutationEpoch;
  const loadState=$('#scopeLoadState');
  try{
    const d=await api('/api/scope');
    if(epoch!==scopeLoadEpoch||mutationSnapshot!==scopeMutationEpoch||scopeMutationLanes.size)return;
    if(loadState)loadState.style.display='none';
    const changed=JSON.stringify(state.scope)!==JSON.stringify(d.rules||[]);
    state.scope=d.rules||[];
    scopeLoaded=true;
    // Events replayed after an in-flight loadFlows() are judged against the rules
    // held at that moment; a changed rule set needs one more window fetch.
    if(changed&&state.inScopeOnly)scheduleReload();
    renderScope();
  }catch(e){
    if(epoch===scopeLoadEpoch&&!scopeMutationLanes.size)renderLoadError(loadState,'Target scope',e,loadScope,state.scope.length>0);
  }
}
export async function addHostToScope(host){
  try{await scopeMutation(0,()=>api('/api/scope',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({action:'include',host:host,enabled:true})}));
    toast('added '+host+' to scope — turn on In scope only to focus');}
  catch(e){toastError('Add to scope failed',e);}
}
export function renderScope(){
  const body=$('#scopeBody');if(!body)return;
  const active=document.activeElement;
  const activeRow=active?.closest?.('#scopeBody tr[data-id]');
  const focus={id:activeRow?.dataset.id||'',key:active?.dataset?.k||'',start:active?.selectionStart,end:active?.selectionEnd};
  const warn=$('#scopeDupWarn');
  const rows=state.scope.map(r=>({...r,...(scopeDrafts.get(r.id)||{})}));
  const enabled=rows.filter(r=>r.enabled);
  const dup=enabled.filter((r,i,a)=>a.findIndex(x=>x.action===r.action&&x.host===r.host&&x.path===r.path&&x.scheme===r.scheme&&x.port===r.port)!==i);
  if(warn){
    if(dup.length){
      warn.style.display='block';
      warn.textContent=`Duplicate scope rule${dup.length===1?'':'s'} detected — only one is needed.`;
    }else warn.style.display='none';
  }
  if(!state.scope.length){body.innerHTML='<tr><td colspan="6" class="hint scope-empty">No scope rules — everything is in scope.</td></tr>';return;}
  body.innerHTML=rows.map(r=>`<tr data-id="${r.id}">
    <td><input type="checkbox" aria-label="Enable scope rule ${r.id}" ${r.enabled?'checked':''} data-k="enabled"></td>
    <td><select data-k="action" aria-label="Scope rule ${r.id} action"><option value="include" ${r.action==='include'?'selected':''}>include</option><option value="exclude" ${r.action==='exclude'?'selected':''}>exclude</option></select></td>
    <td><input type="text" data-k="host" aria-label="Scope rule ${r.id} host" value="${escAttr(r.host)}" placeholder="*.example.com"></td>
    <td><input type="text" data-k="path" aria-label="Scope rule ${r.id} path" value="${escAttr(r.path)}" placeholder="/"></td>
    <td><input type="text" data-k="scheme" aria-label="Scope rule ${r.id} scheme" value="${escAttr(r.scheme)}" placeholder="any"></td>
    <td><button class="btn danger" data-del="${r.id}" data-k="delete" aria-label="Delete scope rule ${r.id}">Delete</button></td></tr>`).join('');
  body.querySelectorAll('tr').forEach(tr=>{const id=Number(tr.dataset.id);
    tr.querySelectorAll('input[data-k],select[data-k]').forEach(inp=>{inp.addEventListener('input',()=>rememberScopeDraft(id,tr));inp.addEventListener('change',()=>updateScope(id,tr));});});
  body.querySelectorAll('[data-del]').forEach(b=>b.onclick=()=>deleteScope(Number(b.dataset.del)));
  if(focus.id&&focus.key)requestAnimationFrame(()=>{const el=body.querySelector(`tr[data-id="${focus.id}"] [data-k="${focus.key}"]`);if(!el)return;el.focus({preventScroll:true});if(typeof focus.start==='number'&&el.setSelectionRange)el.setSelectionRange(focus.start,focus.end);});
}
function scopeMutation(id,work){
  scopeMutationEpoch++;
  const rev=(scopeMutationRevision.get(id)||0)+1;scopeMutationRevision.set(id,rev);
  const prior=scopeMutationLanes.get(id)||Promise.resolve();
  const next=prior.catch(()=>{}).then(work);
  scopeMutationLanes.set(id,next);
  return next.finally(()=>{if(scopeMutationLanes.get(id)!==next)return;scopeMutationLanes.delete(id);if(!scopeMutationLanes.size)loadScope();});
}
function rememberScopeDraft(id,tr){
  const get=k=>tr.querySelector(`[data-k="${k}"]`);
  const draft={id,action:get('action').value,host:get('host').value.trim(),path:get('path').value.trim(),scheme:get('scheme').value.trim(),enabled:get('enabled').checked,port:0};
  scopeDrafts.set(id,draft);return draft;
}
async function updateScope(id,tr){
  const upd=rememberScopeDraft(id,tr),pending=scopeMutation(id,()=>api('/api/scope/'+id,{method:'PUT',headers:{'content-type':'application/json'},body:JSON.stringify(upd)})),revision=scopeMutationRevision.get(id);
  try{await pending;if(revision===scopeMutationRevision.get(id)&&scopeDrafts.get(id)===upd){scopeDrafts.delete(id);toast('scope saved');}}
  catch(e){if(revision===scopeMutationRevision.get(id)){if(scopeDrafts.get(id)===upd){scopeDrafts.delete(id);renderScope();}toastError('Scope rule not saved',e);}}
}
async function deleteScope(id){
  const hadDraft=scopeDrafts.has(id),draftAtDelete=scopeDrafts.get(id);
  const pending=scopeMutation(id,()=>api('/api/scope/'+id,{method:'DELETE'})),revision=scopeMutationRevision.get(id);
  try{await pending;if(revision===scopeMutationRevision.get(id))scopeDrafts.delete(id);}
  catch(e){
    if(revision===scopeMutationRevision.get(id)&&hadDraft&&scopeDrafts.get(id)===draftAtDelete){scopeDrafts.delete(id);renderScope();}
    if(revision===scopeMutationRevision.get(id))toastError('Scope rule not deleted',e);
  }
}
let scopeAddInFlight=false,scopeAddEpoch=0;
function setScopeAddState(stateName){const b=$('#addScopeBtn');if(!b)return;b.disabled=stateName==='pending';b.setAttribute('aria-busy',stateName==='pending'?'true':'false');b.textContent=stateName==='pending'?'Adding…':stateName==='success'?'Added':'+ Add';}
$('#addScopeBtn').onclick=async()=>{
  if(scopeAddInFlight)return;
  const rule={action:$('#newScopeAction').value,host:$('#newScopeHost').value.trim(),path:$('#newScopePath').value.trim(),scheme:'',enabled:true,port:0};
  if(!rule.host&&!rule.path){toast('host or path required');return;}
  scopeAddInFlight=true;const addEpoch=++scopeAddEpoch;setScopeAddState('pending');
  try{await scopeMutation(0,()=>api('/api/scope',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify(rule)}));
    $('#newScopeHost').value='';$('#newScopePath').value='';toast('scope rule added');setScopeAddState('success');}catch(e){toastError('Add scope rule failed',e);setScopeAddState('idle');}
  finally{scopeAddInFlight=false;if($('#addScopeBtn')?.textContent==='Added')setTimeout(()=>{if(addEpoch===scopeAddEpoch)setScopeAddState('idle');},600);}
};
/* ---- filters: chips + apply/clear, kept in sync with the toolbar controls ---- */
export function syncControls(){
  $('#fMethod').value=state.filters.method;
  $('#fStatus').value=state.filters.status;
  $('#fSearch').value=state.filters.search;
  const ss=$('#fSearchScope');if(ss)ss.value=state.filters.searchScope||'anywhere';
  syncSearchPlaceholder();
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
  state.filters={scheme:'',search:'',searchScope:'anywhere',method:'',status:'',host:'',tag:'',exclude:[]};
  state.notesOnly=false;state.showManual=true;state.showAI=true;state.inScopeOnly=false;state.hideTlsFailed=false;
  try{localStorage.setItem(HIDE_TLS_KEY,'0');}catch(e){}
  syncSourceFilters();syncHideTlsFilter();
  {const nf=$('#notesFilter');if(nf){nf.classList.remove('on');nf.setAttribute('aria-pressed','false');}}
  syncScopeToggle(false);
  syncControls();renderChips();renderTagBar();loadFlows();
}
export function anyFilter(){const f=state.filters;return !!(f.scheme||f.method||f.status||f.host||f.search||f.tag||(f.exclude&&f.exclude.length)||state.notesOnly||state.inScopeOnly||!state.showManual||!state.showAI);}
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
  catch(e){toastError('Tag failed',e);}
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
  const add=(k,label,val)=>{if(val)items.push(`<span class="chip"><span>${label} <b>${esc(val)}</b></span><button type="button" class="x" data-clear="${k}" title="Remove filter" aria-label="Remove ${escAttr(k)} filter">${icon('close')}</button></span>`);};
  add('scheme','scheme',f.scheme);
  add('method','method',f.method);
  add('status','status',f.status?f.status+'xx':'');
  add('host','host',f.host);
  add('tag','<svg class="icon" aria-hidden="true" focusable="false"><use href="#i-tag"/></svg>',f.tag);
  add('search',f.searchScope==='body'?'body':f.searchScope==='id'?'id':'path',f.search);
  if(state.hideTlsFailed)items.push(`<span class="chip"><span>hiding <b>TLS</b> failures</span><button type="button" class="x" id="chipHideTlsClear" title="Show TLS failures" aria-label="Show TLS failures">${icon('close')}</button></span>`);
  (f.exclude||[]).forEach((e,i)=>{items.push(`<span class="chip not"><span>${esc(e.field)} ≠ <b>${esc(e.value)}</b></span><button type="button" class="x" data-ex="${i}" title="Remove exclusion" aria-label="Remove ${escAttr(e.field)} exclusion">${icon('close')}</button></span>`);});
  // Source, notes and scope toggles live elsewhere (toolbar chip, Filters popover),
  // so an active one also shows here as a removable chip: removal clicks the toggle.
  [[state.inScopeOnly,'#scopeToggle','in scope only'],[state.notesOnly,'#notesFilter','with notes'],[!state.showManual,'#manualFilter','hiding manual'],[!state.showAI,'#aiFilter','hiding external agent']].forEach(([on,sel,label])=>{
    if(on)items.push(`<span class="chip"><span><b>${esc(label)}</b></span><button type="button" class="x" data-toggle="${sel}" title="Remove filter" aria-label="Remove ${escAttr(label)} filter">${icon('close')}</button></span>`);
  });
  const hasFilters=items.length>0;
  if(hasFilters)items.push(`<button class="chip-clear" id="chipsClear" title="Remove all filters">Clear all ${icon('close')}</button>`);
  box.innerHTML=items.join('');
  box.classList.toggle('has',hasFilters);
  const bar=$('#activeFilterBar');if(bar)bar.hidden=!hasFilters;
  syncFiltersButton();
  box.querySelectorAll('[data-toggle]').forEach(x=>x.onclick=()=>{$(x.dataset.toggle)?.click();});
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
      toast('deleted '+r.deleted+' flow'+(r.deleted===1?'':'s'));
      if(state.detail?.host===f.host)closeInspector();
      loadRetention();loadFlows();
    }catch(e){toastError('Purge failed',e);}
  };
}

// A replay link opens Interseptor's confirm page (GET /replay/{id}); clicking
// "Replay now" there re-sends this exact request — the easy way to re-fire a POST
// you can't reproduce from a browser URL bar. session=current runs it under the
// active session/auth; session=flow replays it exactly as captured.
function replayLink(f,session){return location.origin+'/replay/'+f.id+'?session='+session;}
function copyReplayLink(f,session){copyText(replayLink(f,session),'replay link copied');}
function sessionInspectionSelection(id){
  return state.selected?.size>1&&state.selected.has(id)?[...state.selected]:[id];
}
async function exportRawFlow(f,side,variant=''){
  try{
    const query=new URLSearchParams({side});
    if(variant)query.set('variant',variant);
    const raw=await api('/api/flows/'+f.id+'/raw?'+query.toString());
    const host=(f.host||'flow').replace(/[^a-z0-9.-]+/gi,'_');
    const method=(f.method||'HTTP').replace(/[^a-z0-9]+/gi,'_');
    const suffix=variant==='original'?'_original':'';
    const name=`flow-${f.id}_${host}_${method}_${side}${suffix}.http`;
    await saveFile(new Blob([raw],{type:'message/http'}),name,'message/http');
    toast('exported '+name);
  }catch(e){if(e&&e.name==='AbortError')return;toastError('Export '+side+(variant?' original':'')+' failed',e);}
}
function rawExportItems(f,side){
  const label=side==='req'?'request':'response';
  const items=[{label:'Export raw '+label,act:()=>exportRawFlow(f,side)}];
  const hasOriginal=side==='req'?!!(f.originalReqBodyHash||f.originalReqHeaders):!!(f.originalResBodyHash||f.originalResHeaders);
  if(hasOriginal)items.push({label:'Export original raw '+label,act:()=>exportRawFlow(f,side,'original')});
  return items;
}
// flowGlobalSection — the flow-wide actions present in every history/inspector
// menu regardless of which column was clicked (send, copy, AI, authz).
function flowGlobalSection(f,head,side='both'){
  const exportItems=side==='req'?rawExportItems(f,'req'):side==='res'?rawExportItems(f,'res'):[
    ...rawExportItems(f,'req'),
    {sep:true},
    ...rawExportItems(f,'res'),
  ];
  const items=[
    ...exportItems,
    {sep:true},
    {label:'Inspect session timeline',icon:'timeline',val:state.selected?.size>1&&state.selected.has(f.id)?`${state.selected.size} selected captures`:'selected capture',act:()=>openSessionInspector(sessionInspectionSelection(f.id))},
    {label:'Auth timeline from this flow',icon:'timeline',val:'read-only',act:()=>openAuthTimeline(f.id)},
    {label:'Send to Repeater',act:()=>sendToRepeater(f)},
    {label:'Send to Intruder',act:()=>sendToIntruder(f)},
    {label:'Copy URL',act:()=>copyURL(f)},
    {label:'Copy as cURL',act:()=>copyCurl(f)},
    {label:'Copy replay link · current session',act:()=>copyReplayLink(f,'current')},
    {label:"Copy replay link · flow's session",act:()=>copyReplayLink(f,'flow')},
  ];

  items.push({sep:true},
    ...(side==='req'?[]:side==='res'?[]:rawExportItems(f,'res')),
    {label:'Scan this host',icon:'search',val:f.host,act:()=>prefillScanner(f.host, (f.path||'').split('?')[0])},
    {label:'Authz test',icon:'lock-open',val:'roles',act:()=>openAuthz(f.id)},
    {label:'Use as login macro',icon:'key',act:()=>saveLoginMacroFromFlow(f.id)});
  return {head:head||'REQUEST', items};
}

async function saveLoginMacroFromFlow(id){
  try{
    await api('/api/session/login/from-flow/'+id,{method:'POST'});
    toast('login macro saved — Settings → Session');
    document.querySelector('.tab[data-tab="settings"]').click();
    const b=document.querySelector('#setNav button[data-sec="session"]');if(b)b.click();
  }catch(e){toastError('Save login macro failed',e);}
}

// showCtx builds the history-row menu: a contextual top section keyed to the
// clicked column (host / status / method / path) + the always-present global
// flow actions. Right-clicking a host shows host/domain/scope/discover actions;
// right-clicking a status shows status filters — not the other way around.
export function showCtx(x,y,f,field){
  if(!f)return;
  const cls=f.status?Math.floor(f.status/100):0;
  const dom=registrableDomain(f.host);
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
    items.push({sep:true});
    items.push({label:'Delete all from host',icon:'trash',val:f.host,danger:true,act:deleteHost(f)});
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

  sections.push(flowGlobalSection(f,'REQUEST','both'));
  // TAGS: filter by / remove an existing tag, or add tags (to this flow, or the whole selection).
  const tagTargets=tagActionTargets(f.id);
  const tagN=tagTargets.length;
  const tagItems=[];
  (f.tags||[]).forEach(t=>{
    tagItems.push({label:'Filter · '+t,icon:'tag',on:state.filters.tag===t,act:()=>filterByTag(t)});
    tagItems.push({label:'✕ Remove · '+t,danger:true,val:tagN>1?tagN+' flows':'',act:()=>mutateFlowTags(tagTargets,{remove:[t]})});
  });
  const selN=(state.selected&&state.selected.size>1&&state.selected.has(f.id))?state.selected.size:0;
  tagItems.push({label:selN?('Tag '+selN+' selected…'):'Tag…',icon:'tag',act:()=>selN?tagSelectionPrompt():tagFlowPrompt(f)});
  if(selN)tagItems.push({label:'✕ Remove tag from '+selN+' selected…',danger:true,act:()=>tagSelectionRemovePrompt()});
  sections.push({head:(f.tags||[]).length?('TAGS · '+f.tags.join(' ')):'TAGS', items:tagItems});
  const ff=flowFindings(f.id);
  const fitems=ff.map(x=>({label:x.title,icon:'pin',val:x.severity,act:()=>openFinding(x.id)}));
  fitems.push({label:'Add to finding',icon:'plus',act:()=>addFlowToFinding(f.id)});
  sections.push({head:ff.length?('FINDINGS · in '+ff.length):'FINDINGS',items:fitems});
  if(anyFilter())sections.push({items:[{label:'Clear all filters',act:clearAllFilters}]});
  const sendAsIds=_authzIdsCache.filter(id=>!id.broken&&(id.name||id.headers));
  if(sendAsIds.length)sections.push({head:'SEND AS',items:sendAsIds.map(id=>({label:id.name||'(unnamed)',act:()=>sendAsIdentity(f,id)}))});
  openMenu(x,y,sections);
}

export function showInspectorCtx(x,y,side){
  const f=flowStore.byId.get(state.selId)||state.detail;
  if(!f)return;
  const curSide=side==='resp'?'res':side;
  const sel=selectionWithin($(curSide==='req'?'#reqView':'#resView'));
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
  const copyItems=[
    {label:curSide==='req'?'Copy entire request':'Copy entire response',act:()=>copyFlowRaw(f,curSide)},
    {label:curSide==='req'?'Copy request body':'Copy response body',act:()=>copyFlowBody(f,curSide)},
  ];
  sections.push({head:'COPY', items:copyItems});
  sections.push(flowGlobalSection(f, curSide==='req'?'REQUEST':'RESPONSE', curSide));
  if(!sel)sections.push({items:[{label:'Open Decoder',act:()=>openDecoder('')}]});
  const sendAsIds2=_authzIdsCache.filter(id=>!id.broken&&(id.name||id.headers));
  if(sendAsIds2.length)sections.push({head:'SEND AS',items:sendAsIds2.map(id=>({label:id.name||'(unnamed)',act:()=>sendAsIdentity(f,id)}))});
  openMenu(x,y,sections);
}
function selectedInspectorFlow(){return flowStore.byId.get(state.selId)||state.detail;}
const inspectSendRepeater=$('#inspectSendRepeater');
if(inspectSendRepeater)inspectSendRepeater.onclick=()=>{const f=selectedInspectorFlow();if(f)sendToRepeater(f);};
const inspectAddFinding=$('#inspectAddFinding');
if(inspectAddFinding)inspectAddFinding.onclick=()=>{const f=selectedInspectorFlow();if(f)addFlowToFinding(f.id);};
const inspectSendIntruder=$('#inspectSendIntruder');
if(inspectSendIntruder)inspectSendIntruder.onclick=()=>{const f=selectedInspectorFlow();if(f)sendToIntruder(f);};
const inspectMoreActions=$('#inspectMoreActions');
if(inspectMoreActions)inspectMoreActions.onclick=()=>{
  const f=selectedInspectorFlow();if(!f)return;
  const r=inspectMoreActions.getBoundingClientRect();
  showCtx(r.left,r.bottom+2,f,'');
};
document.addEventListener('click',e=>{if(!ctx.contains(e.target))hideCtx({restoreFocus:false});});
document.addEventListener('interceptor:session-open-flow',e=>{
  const id=Number(e.detail);
  if(Number.isSafeInteger(id)&&id>0)selectFlow(id);
});
document.addEventListener('keydown',e=>{if(e.key==='Escape'){if(hasOpenModal())return;if(ctx.classList.contains('show')){hideCtx({restoreFocus:true});return;}closeInspector();}});
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
  if(el)el.addEventListener('contextmenu',e=>{e.preventDefault();e.stopPropagation();showInspectorCtx(e.clientX,e.clientY,id==='reqView'?'req':'res');});
});
wireSelectionDecode($('#reqView'),$('#reqDecode'),{onDecoder:openDecoder,getContext:()=>state.selId?{flowId:state.selId,side:'req'}:null});
wireSelectionDecode($('#resView'),$('#resDecode'),{onDecoder:openDecoder,getContext:()=>state.selId?{flowId:state.selId,side:'res'}:null});
export function flowURL(f){return flowUrl(f);}
export function copyURL(f){copyText(flowURL(f),'URL copied');}
function shq(s){return "'"+String(s).replace(/'/g,"'\\''")+"'";}
export async function copyFlowRaw(f,side='req'){
  try{
    const raw=await api('/api/flows/'+f.id+'/raw?side='+side);
    copyText(raw,(side==='req'?'Request':'Response')+' copied');
  }catch(e){toastError('Copy failed',e);}
}
export async function copyFlowBody(f,side='req'){
  try{
    const raw=await api('/api/flows/'+f.id+'/raw?side='+side);
    let i=raw.indexOf('\r\n\r\n');
    let body='';
    if(i>=0){
      body=raw.slice(i+4);
    }else{
      i=raw.indexOf('\n\n');
      body=i>=0?raw.slice(i+2):'';
    }
    copyText(body,(side==='req'?'Request':'Response')+' body copied');
  }catch(e){toastError('Copy body failed',e);}
}
export async function copyCurl(f){
  try{
    const d=await api('/api/flows/'+f.id);
    const parts=[`curl -x http://${state.proxyAddr}`];
    if(f.scheme==='https')parts.push('--cacert interseptor-ca.crt');
    parts.push('-X '+f.method);
    const headers=d.reqHeaders||{};
    Object.keys(headers).sort().forEach(k=>{if(k.toLowerCase()==='host')return;(headers[k]||[]).forEach(v=>parts.push('-H '+shq(k+': '+v)));});
    if(f.reqLen>0){const raw=await api('/api/flows/'+f.id+'/raw?side=req');const i=raw.indexOf('\r\n\r\n');const body=i>=0?raw.slice(i+4):'';if(body)parts.push('--data-raw '+shq(body));}
    parts.push(shq(flowURL(f)));
    copyText(parts.join(' \\\n  '),'cURL copied');
  }catch(e){toastError('cURL copy failed',e);}
}
// ---- History multi-select actions ----
export function updateSelBar(){
  const n=state.selected.size;
  $('#selBar').style.display=n?'flex':'none';
  $('#selCount').textContent=n+' selected';
  // Verbs stay visible and are disabled with a reason, so "Diff" is discoverable
  // before the second flow is selected.
  const buttonFor={repeater:'selSendTo',intruder:'selSendTo',scanner:'selSendTo',tag:'selTag',finding:'selAddFinding',copyas:'selCopyAs',diff:'selCompare',delete:'selDelete'};
  const enabled={};
  bulkVerbs(n).forEach(v=>{
    const id=buttonFor[v.id];
    enabled[id]=enabled[id]||v.enabled;
    const btn=$('#'+id);
    if(btn&&id!=='selSendTo'){
      if(btn.dataset.title===undefined)btn.dataset.title=btn.dataset.tooltip||btn.getAttribute('title')||'';
      btn.disabled=!v.enabled||btn.dataset.busy==='1';
      btn.title=v.enabled?btn.dataset.title:v.reason;
    }
  });
  const send=$('#selSendTo');if(send)send.disabled=!enabled.selSendTo;
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
  return `<div style="font-family:var(--mono);font-size:var(--fs-xs);line-height:1.55;white-space:pre-wrap;word-break:break-word">${rows.join('')}${(i<ta.length||j<tb.length)?'<span class="hint"> …truncated</span>':''}</div>`;
}
function compareHeaderDiff(ha,hb){
  const keys=new Set([...Object.keys(ha||{}),...Object.keys(hb||{})]);
  const sorted=[...keys].sort((a,b)=>a.localeCompare(b));
  if(!sorted.length)return '<div class="hint">No response headers</div>';
  const rows=sorted.map(k=>{
    const x=(ha&&ha[k]||[]).join(', '),y=(hb&&hb[k]||[]).join(', ');
    if(x===y)return `<div style="font-family:var(--mono);font-size:var(--fs-xs);color:var(--fg3)"><b>${esc(k)}:</b> ${esc(x||'—')}</div>`;
    return `<div style="font-family:var(--mono);font-size:var(--fs-xs);margin:4px 0"><div style="color:var(--red)"><b>${esc(k)}:</b> ${esc(x||'∅')}</div><div style="color:var(--accent)"><b>${esc(k)}:</b> ${esc(y||'∅')}</div></div>`;
  });
  return rows.join('');
}
function compareLineDiff(a,b){
  const la=a.split('\n'),lb=b.split('\n'),n=Math.max(la.length,lb.length),rows=[];
  for(let i=0;i<n&&rows.length<300;i++){
    const x=la[i]??'',y=lb[i]??'';
    if(x===y)rows.push(`<div style="color:var(--fg3);font-family:var(--mono);font-size:var(--fs-xs);white-space:pre-wrap">${esc(x||' ')}</div>`);
    else rows.push(`<div style="font-family:var(--mono);font-size:var(--fs-xs)"><span style="color:var(--red);white-space:pre-wrap">${esc(x||'∅')}</span><br><span style="color:var(--accent);white-space:pre-wrap">${esc(y||'∅')}</span></div>`);
  }
  return rows.join('')+(n>300?'<div class="hint">…line diff truncated</div>':'');
}
let compareEpoch=0,compareMode='words';
function compareModalOpen(modal,box){return modal?.style.display==='flex'&&box?.isConnected&&$('#compareBody')===box;}
function closeCompare(){++compareEpoch;closeModal($('#compareModal'));}
export async function openCompare(restoreModeFocus=''){
  const epoch=++compareEpoch;
  const ids=[...state.selected].sort((a,b)=>a-b);
  if(ids.length!==2){toast('select exactly 2 flows');return;}
  const modal=$('#compareModal'),box=$('#compareBody');
  if(!compareModalOpen(modal,box))openModal(modal,{onEscape:closeCompare,onDismiss:closeCompare});
  const current=()=>epoch===compareEpoch&&compareModalOpen(modal,box)&&state.selected.size===2&&ids.every(id=>state.selected.has(id));
  $('#compareTitle').textContent='Compare responses · #'+ids[0]+' vs #'+ids[1];
  if(box)box.innerHTML='<div class="hint">loading…</div>';
  try{
    const [fa,fb]=await Promise.all(ids.map(id=>api('/api/flows/'+id)));
    if(!current())return;
    const [ra,rb]=await Promise.all(ids.map(id=>api('/api/flows/'+id+'/raw?side=res')));
    const split=s=>{const i=s.indexOf('\r\n\r\n');return i>=0?s.slice(i+4):s;};
    const limit=512*1024;
    const ba=split(ra).slice(0,limit),bb=split(rb).slice(0,limit);
    if(!current())return;
    const mode=compareMode;
    const bodyHtml=mode==='lines'?compareLineDiff(ba,bb):compareWordDiff(ba,bb);
    $('#compareTitle').textContent='Compare responses · #'+ids[0]+' vs #'+ids[1];
    if(box)box.innerHTML=`<div class="row" style="gap:12px;margin-bottom:8px;font-size:var(--fs-xs);flex-wrap:wrap">
      <span><b style="color:var(--red)">#${ids[0]}</b> ${esc(fa.method)} ${esc(fa.status||'—')} · ${fmtSize(fa.resLen)}</span>
      <span><b style="color:var(--accent)">#${ids[1]}</b> ${esc(fb.method)} ${esc(fb.status||'—')} · ${fmtSize(fb.resLen)}</span>
      <div class="seg" id="compareMode" role="group" aria-label="Response comparison mode" style="margin-left:auto"><button type="button" class="${mode==='words'?'on':''}" data-m="words" aria-pressed="${mode==='words'?'true':'false'}">Words</button><button type="button" class="${mode==='lines'?'on':''}" data-m="lines" aria-pressed="${mode==='lines'?'true':'false'}">Lines</button></div>
    </div>
    <div class="micro-label" style="margin:8px 0 4px">RESPONSE HEADERS</div>
    ${compareHeaderDiff(fa.resHeaders,fb.resHeaders)}
    <div class="micro-label" style="margin:12px 0 4px">RESPONSE BODY</div>
    ${bodyHtml}`;
    $('#compareMode')?.querySelectorAll('button').forEach(b=>{b.onclick=()=>{compareMode=b.dataset.m;openCompare(compareMode);};});
    if(restoreModeFocus)requestAnimationFrame(()=>{if(current())$('#compareMode')?.querySelector(`[data-m="${restoreModeFocus}"]`)?.focus({preventScroll:true});});
  }catch(e){if(current())box.innerHTML='<div class="hint" style="color:var(--red)">'+esc(e.message)+'</div>';}
}
if($('#selCompare'))$('#selCompare').onclick=()=>openCompare();
if($('#compareClose'))$('#compareClose').onclick=closeCompare;
$('#selClear').onclick=()=>{state.selected.clear();state.selAnchorId=null;state.lastSelIdx=-1;renderRows();updateSelBar();};

$('#selScope').onclick=async()=>{
  const hosts=[...new Set([...state.selected].map(id=>{const f=flowStore.byId.get(id);return f&&f.host;}).filter(Boolean))];
  if(!hosts.length)return;
  const button=$('#selScope');if(!button||button.disabled)return;
  button.disabled=true;button.setAttribute('aria-busy','true');
  let added=0;const failed=[];
  try{
    await scopeMutation(0,async()=>{for(const host of hosts){try{await api('/api/scope',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({action:'include',host,enabled:true})});added++;}catch(e){failed.push(host);}}});
    if(failed.length){
      const names=failed.slice(0,3).join(', ')+(failed.length>3?' +'+(failed.length-3)+' more':'');
      toast((added?'added '+added+' of '+hosts.length+' hosts':'no hosts added')+' · failed: '+names,'warn');
    }else toast('added '+added+' host'+(added===1?'':'s')+' to scope','success');
  }finally{
    button.disabled=false;button.removeAttribute('aria-busy');
  }
};
function restoreSelDelete(btn){delete btn.dataset.busy;btn.disabled=false;btn.removeAttribute('aria-busy');btn.innerHTML=icon('trash')+' Delete';}
// bulkRun runs `work(chunk)` over BULK_CHUNK-sized id batches and announces
// "Verb N of M" through the polite #selProgress region between batches.
async function bulkRun(verb,ids,work){
  const out=$('#selProgress');
  let done=0;
  for(const chunk of chunkIds(ids,BULK_CHUNK)){
    const r=await work(chunk);
    done+=chunk.length;
    if(out)out.textContent=bulkProgressText(verb,done,ids.length);
    if(r===false)break;
  }
  return done;
}
$('#selDelete').onclick=async()=>{
  const btn=$('#selDelete');
  if(btn.disabled)return;
  const ids=[...state.selected];if(!ids.length)return;
  const ok=await uiConfirm('Delete '+ids.length+' flow'+(ids.length===1?'':'s')+'?','This permanently removes the selected flows and their stored bodies. It cannot be undone.','Delete','btn btn-danger');
  if(!ok)return;
  btn.disabled=true;btn.dataset.busy='1';btn.setAttribute('aria-busy','true');btn.textContent='Deleting…';
  let deleted=0;
  try{
    await bulkRun('Deleting',ids,async chunk=>{
      const r=await api('/api/flows/delete',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({ids:chunk})});
      deleted+=r.deleted!=null?r.deleted:chunk.length;
    });
  }catch(e){restoreSelDelete(btn);toastError('Delete failed',e);if(deleted)loadFlows();return;}
  // Restore the label first so updateSelBar and later selection changes own the final state.
  restoreSelDelete(btn);
  if(state.selected.has(state.selId)){state.selId=null;onAuthzSelectionChanged();}
  state.selected.clear();state.selAnchorId=null;state.lastSelIdx=-1;updateSelBar();loadFlows();
  const out=$('#selProgress');if(out)out.textContent='';
  toast('deleted '+deleted+' flow'+(deleted===1?'':'s'));
};
/* ---- inspector splitter ---- */
(function(){
  const SPLITTER_KEY='inspect.height';
  const MIN_H=120, MAX_PCT=0.80;
  const splitter=document.getElementById('inspectSplitter');
  const inspect=document.getElementById('inspect');
  if(!splitter||!inspect)return;
  const proxyPanel=inspect.closest('.panel');
  function maxHeight(){return Math.max(MIN_H,(proxyPanel?.clientHeight||750)*MAX_PCT);}
  function clamp(h){
    return Math.max(MIN_H,Math.min(maxHeight(),h));
  }
  function syncRange(){
    const height=clamp(inspect.offsetHeight||Number.parseFloat(inspect.style.height)||MIN_H);
    splitter.setAttribute('aria-valuemin',String(MIN_H));
    splitter.setAttribute('aria-valuemax',String(Math.round(maxHeight())));
    splitter.setAttribute('aria-valuenow',String(Math.round(height)));
  }
  function applyHeight(h){
    h=clamp(h);
    inspect.style.height=h+'px';
    inspect.style.flex='none';
    syncRange();
    try{localStorage.setItem(SPLITTER_KEY,String(h));}catch(e){}
  }

  // Restore persisted height on load.
  try{const saved=localStorage.getItem(SPLITTER_KEY);if(saved){const h=parseInt(saved,10);if(h>=MIN_H)applyHeight(h);}}catch(e){}
  syncRange();
  if(typeof ResizeObserver!=='undefined'){
    const observer=new ResizeObserver(()=>{
      if(proxyPanel?.clientHeight&&inspect.style.height)inspect.style.height=clamp(Number.parseFloat(inspect.style.height))+'px';
      syncRange();
    });
    observer.observe(inspect);
    if(proxyPanel)observer.observe(proxyPanel);
  }

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

/* ---- Proxy panel workbench: Filters popover, inspector dock, bulk verbs, Copy as, gestures ---- */
function syncFiltersButton(){
  const btn=$('#filtersBtn');if(!btn)return;
  const n=popoverFilterCount(state);
  const badge=$('#filtersCount');
  if(badge){badge.hidden=n===0;badge.textContent=String(n);}
  btn.setAttribute('aria-label',n?`Filters, ${n} active`:'Filters');
}
function setFiltersOpen(open,{restoreFocus=false}={}){
  const pop=$('#proxyFilters'),btn=$('#filtersBtn');
  if(!pop||!btn)return;
  pop.hidden=!open;
  btn.setAttribute('aria-expanded',open?'true':'false');
  if(open)pop.querySelector('button,input,select,summary')?.focus();
  else if(restoreFocus)btn.focus();
}
{
  const btn=$('#filtersBtn');
  if(btn)btn.onclick=e=>{e.stopPropagation();const pop=$('#proxyFilters');setFiltersOpen(!!pop&&pop.hidden);};
  // Esc closes the popover first (capture), and focus returns to its button.
  document.addEventListener('keydown',e=>{
    const pop=$('#proxyFilters');
    if(e.key!=='Escape'||!pop||pop.hidden)return;
    e.preventDefault();e.stopImmediatePropagation();setFiltersOpen(false,{restoreFocus:true});
  },true);
  document.addEventListener('pointerdown',e=>{
    const pop=$('#proxyFilters');
    if(pop&&!pop.hidden&&!e.target.closest('#proxyFilters,#filtersBtn'))setFiltersOpen(false);
  });
  syncFiltersButton();
}

// Inspector dock: at >=1100px selecting a flow opens the Flow Drawer and the
// bottom inspector hides; "dock bottom" (or a narrower viewport) keeps the
// classic bottom inspector. The preference persists in localStorage.
const DOCK_KEY='proxy.dock';
let dockPref=(()=>{try{return parseDockPref(localStorage.getItem(DOCK_KEY));}catch(e){return 'drawer';}})();
function syncDock(){
  const panel=$('#panel-proxy'),btn=$('#inspectDock');
  const wide=resolveDock(dockPref,window.innerWidth)==='drawer';
  const drawer=wide&&!!getHook('openFlow');
  if(panel)panel.classList.toggle('dock-drawer',drawer);
  if(btn){
    btn.hidden=resolveDock('drawer',window.innerWidth)!=='drawer'||!getHook('openFlow');
    btn.setAttribute('aria-pressed',drawer?'true':'false');
    btn.innerHTML=icon('panel')+(drawer?' Side drawer':' Bottom inspector');
  }
  return drawer;
}
function openDockedDrawer(id){
  if(!syncDock())return;
  const current=getHook('flowDrawerCurrent');
  if(current&&current()===id)return;
  const opened=openFlowDrawer(id,{source:'proxy',focusTitle:false,siblings:state.flows.map(f=>f.id)});
  if(!opened)$('#panel-proxy')?.classList.remove('dock-drawer');
}
{
  const btn=$('#inspectDock');
  if(btn)btn.onclick=()=>{
    dockPref=dockPref==='drawer'?'bottom':'drawer';
    try{localStorage.setItem(DOCK_KEY,dockPref);}catch(e){}
    const drawer=syncDock();
    if(drawer&&state.selId!=null)openDockedDrawer(state.selId);
    else if(!drawer)getHook('closeFlow')?.();
  };
  window.addEventListener('resize',()=>{const was=$('#panel-proxy')?.classList.contains('dock-drawer');if(!syncDock()&&was)getHook('closeFlow')?.();});
  syncDock();
}
// The shared row-height token changes with density; re-measure the virtual list.
window.addEventListener('densitychange',refreshRowHeight);
// Evidence markers come from the loaded findings, so repaint rows when the panel
// becomes visible again instead of keeping stale paperclips.
// The Flow Drawer module loads after this one, so the dock state is re-synced here too.
document.addEventListener('interseptor:tabchange',e=>{if(e&&e.detail&&e.detail.tab==='proxy'){syncDock();refreshVisibleRows();}});

// attachFlows routes "attach as evidence" through the shared attachEvidence
// popover (one entry point for button, key, long press), falling back to the
// legacy picker when the module is unavailable or the batch is large.
const ATTACH_MAX=32;
async function attachFlows(ids,anchor){
  if(!ids.length)return;
  const attach=getHook('attachEvidence');
  if(!attach||ids.length>ATTACH_MAX){if(ids.length===1)addFlowToFinding(ids[0]);else pickFindingForSelection();return;}
  const result=await attach({kind:'flow',refs:ids},{anchor});
  if(result&&result.attached){await loadFindings();refreshVisibleRows();}
}
{
  const attach=$('#inspectAddFinding');
  if(attach)attach.onclick=()=>{const f=selectedInspectorFlow();if(f)attachFlows([f.id],attach).catch(e=>toastError('Attach failed',e));};
  const bulkAttach=$('#selAddFinding');
  if(bulkAttach)bulkAttach.onclick=()=>attachFlows([...state.selected],bulkAttach).catch(e=>toastError('Attach failed',e));
}

// Bulk verbs.
function selectedFlowRecords(){return [...state.selected].map(id=>flowStore.byId.get(id)||{id});}
{
  const send=$('#selSendTo');
  if(send)send.onclick=()=>{
    const ids=[...state.selected];if(!ids.length)return;
    const items=[{label:ids.length>1?'Repeater ('+ids.length+' tabs)':'Repeater',icon:'plus',act:async()=>{
      if(ids.length>20){toast('sending the first 20 flows to Repeater','warn');}
      for(const f of selectedFlowRecords().slice(0,20))await sendToRepeater(f);
    }}];
    if(ids.length===1){
      items.push({label:'Intruder',act:()=>{const f=selectedFlowRecords()[0];if(f)sendToIntruder(f);}});
      items.push({label:'Scanner',icon:'search',act:()=>{const f=selectedFlowRecords()[0];if(f)prefillScanner(f.host,(f.path||'').split('?')[0]);}});
    }
    openMenu(0,0,[{head:'SEND '+ids.length+' TO',items}],send);
  };
  const tag=$('#selTag');
  if(tag)tag.onclick=()=>{tagSelectionPrompt();};
}

// Copy as: one menu for the inspector flow, the bulk selection and the `y` chord.
function copyAsSections(getFlows){
  return [{head:'COPY AS',items:COPY_AS_KINDS.map(k=>({label:k.label,val:k.key,act:()=>{copyAs(k.kind,getFlows(),{redact:k.redact}).catch(e=>toastError('Copy failed',e));}}))}];
}
{
  const one=$('#inspectCopyAs');
  if(one)one.onclick=()=>{const f=selectedInspectorFlow();if(!f)return;openMenu(0,0,copyAsSections(()=>[f]),one);};
  const many=$('#selCopyAs');
  if(many)many.onclick=()=>{if(!state.selected.size)return;openMenu(0,0,copyAsSections(selectedFlowRecords),many);};
}

// Single-key shortcuts for the focused History row (gated by keys.js: not in
// inputs, not while a modal is open, and switchable in Settings). `d` diffs two
// selected flows, `y` then a letter copies as, `/` jumps to the filter box. The
// evidence key `e` is registered by evidence-attach.js.
{
  const registry=createKeyRegistry({isModalOpen:()=>hasOpenModal()});
  registry.register({id:'proxy.diff',keys:'d',scope:'proxy-list',label:'Diff two selected flows',group:'History',run:()=>{
    if(state.selected.size!==2){toast('select exactly two flows to diff');return;}
    openCompare();
  }});
  registry.register({id:'proxy.filter',keys:'/',scope:'proxy-list',label:'Focus the history search',group:'History',run:()=>{$('#fSearch')?.focus();}});
  COPY_AS_KINDS.forEach(k=>registry.register({id:'proxy.copyas.'+k.kind,keys:'y '+k.key,scope:'proxy-list',label:'Copy as '+k.label,group:'History',run:()=>{
    const f=selectedInspectorFlow();if(f)copyAs(k.kind,state.selected.size>1?selectedFlowRecords():[f],{redact:k.redact}).catch(e=>toastError('Copy failed',e));
  }}));
  document.addEventListener('keydown',e=>{
    if(e.defaultPrevented||!e.target||!e.target.closest||!e.target.closest('#rows .trow[data-id]'))return;
    registry.handle(e,{scopes:['proxy-list']});
  });
}

// Phone rows: a 56px two-line card has no room for inline buttons, so the row
// overflow button (always available) and a 500ms long press (8px slop) both open
// the evidence popover. Everything stays reachable without the gesture.
{
  const box=$('#rows');
  let pressId=0,suppressClick=false;
  const press=createLongPress({onLong:()=>{
    suppressClick=true;
    const row=box.querySelector(`.trow[data-id="${pressId}"]`);
    attachFlows([pressId],row||box).catch(e=>toastError('Attach failed',e));
  }});
  box.addEventListener('pointerdown',e=>{
    if(e.pointerType!=='touch')return;
    const row=e.target.closest('.trow[data-id]');if(!row)return;
    pressId=Number(row.dataset.id);suppressClick=false;press.start(e.clientX,e.clientY);
  });
  box.addEventListener('pointermove',e=>{if(e.pointerType==='touch')press.move(e.clientX,e.clientY);});
  ['pointerup','pointercancel','pointerleave'].forEach(t=>box.addEventListener(t,()=>press.end()));
  box.addEventListener('click',e=>{
    if(suppressClick){suppressClick=false;e.stopImmediatePropagation();e.preventDefault();return;}
    const more=e.target.closest('.rowOverflow[data-rowmenu]');
    if(!more)return;
    e.stopImmediatePropagation();
    const f=flowStore.byId.get(Number(more.dataset.rowmenu));
    if(f){const r=more.getBoundingClientRect();showCtx(r.left,r.bottom+2,f,'');}
  },true);
}
