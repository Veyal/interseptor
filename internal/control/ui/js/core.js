// core.js — shared foundation imported by every feature module.
// DOM helpers, the global `state` object, the REST helper, formatters, HTTP
// syntax highlighters, copy helpers, the modal system (open/close + focus trap),
// uiPrompt / uiConfirm, and the safe markdown renderer all live here.
//
// Everything is `export`ed; feature modules import what they reference. Mutations
// to the shared `state` object are visible across modules (live object binding).

import { placeFloatingSurface } from './surface-position.js';
import { formatHexDump, hexLineFor } from './hexdump.js';
import { copyImage, copyFailureMessage, copyImageFileName, isCopyableImageURL, flowPreviewPngURL, activeFlowTheme } from './copy-image.js';
import { selectionToFocus } from './flow-focus-model.js';
import { SUBMENU_HOVER_MS, normalizeSections, menuRows, hasChildren, childSections, submenuPlacement, menuKeyAction, nextIndex, textFieldMenuModel, selectionMenuModel, CODEC_OPS, INSERT_SNIPPETS, PAYLOAD_MARKER, shortLabel } from './ctxmenu-model.js';
import { computeVirtualWindow, sameWindow } from './virtual-list-model.js';
import { createInflight, shareableRead, mutationScope } from './api-inflight.js';

export const $=s=>document.querySelector(s);
export const $$=s=>Array.from(document.querySelectorAll(s));
export const esc=s=>String(s).replace(/[&<>]/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;'}[c]));
// escAttr also escapes quotes — use it for a value interpolated INTO an attribute
// ("${escAttr(x)}"), where a raw " or ' in user-authored rules or captured traffic
// (hosts, paths) would otherwise break out of the attribute. Plain esc() (which
// deliberately keeps quotes) stays for text and the JSON/HTTP highlighters.
export const escAttr=s=>esc(s).replace(/"/g,'&quot;').replace(/'/g,'&#39;');

// icon(name) -> markup referencing the inline sprite in index.html. Use this
// anywhere JS builds HTML strings, instead of pasting an emoji: emoji are
// font-dependent, render differently on macOS/Linux/Windows, and cannot take a
// colour token. `name` is the sprite id without the `i-` prefix.
// Decorative by default (aria-hidden) because these sit next to a text label;
// pass a label for icon-only controls so screen readers still announce them.
export const icon=(name,label)=>`<svg class="icon" ${label?`role="img" aria-label="${escAttr(label)}"`:'aria-hidden="true"'} focusable="false"><use href="#i-${name}"/></svg>`;

// A project switch reloads the page. Features own their drafts and register a
// synchronous check here, avoiding imports between their save implementations.
const projectSwitchGuards=[];
export function registerProjectSwitchGuard(check){projectSwitchGuards.push(check);}
export function projectSwitchBlocker(){
  for(const check of projectSwitchGuards){
    try{const message=check();if(message)return message;}
    catch(e){return 'Could not verify saved work. Retry before switching projects.';}
  }
  return '';
}

export const state={flows:[],selId:null,detail:null,intercept:{enabled:false,queue:[]},
  rules:[],scope:[],views:[],inScopeOnly:false,showManual:true,flowTruncated:false,selected:new Set(),lastSelIdx:-1,view:{req:'pretty',res:'pretty'},sort:{key:'id',dir:-1},proxyAddr:'127.0.0.1:8080',deviceProxy:'127.0.0.1:8080',deviceProxyMode:'auto',controlAddr:'127.0.0.1:9966',
  filters:{scheme:'',search:'',searchScope:'anywhere',method:'',status:'',host:'',tag:'',exclude:[]},notesOnly:false,hideTlsFailed:true,activity:[],actUnseen:0,tags:[],tagColors:{},flowCols:['id','method','host','path','status','size','time'],oobEnabled:false,findingsUIEditing:false};

// toast(m) = info; toast(m, 'error'|'warn'|'success') for a longer, colored one.
// Errors and warnings are role=alert (announced assertively). Warnings live longer
// and pause while hovered or focused; errors persist until dismissed. Neither is
// evicted by newer info toasts.
function evictToasts(c) {
  const keep = (sel, max) => {
    const items = c.querySelectorAll(sel);
    for (let i = 0; i < items.length - max; i++) items[i].remove();
  };
  // Older notices are less useful than the latest result, so cap each class.
  // .toast-keep is exempt: it carries an action with a promised lifetime (an
  // Undo), and sweeping it as a stale notice would quietly withdraw the offer
  // before the window the user was told about had passed.
  keep('.toast-item:not(.error):not(.toast-keep)', 3);
  keep('.toast-item.error', 5);
}
function dismissToast(t) {
  t.classList.add('dying');
  t.classList.remove('show');
  setTimeout(() => t.remove(), 220);
}
function armToastTimer(t, ms) {
  let left = ms, started = 0, timer = null;
  const dismiss = () => dismissToast(t);
  const run = () => { started = Date.now(); clearTimeout(timer); timer = setTimeout(dismiss, left); };
  const pause = () => {
    if (timer === null) return;
    clearTimeout(timer); timer = null;
    left = Math.max(2000, left - (Date.now() - started));
  };
  const resume = () => {
    if (t.matches(':hover') || t.contains(document.activeElement)) return;
    run();
  };
  t.addEventListener('mouseenter', pause);
  t.addEventListener('focusin', pause);
  t.addEventListener('mouseleave', resume);
  t.addEventListener('focusout', resume);
  run();
}
export function toast(m, sev) {
  const c = $('#toast');
  if (!c) return null;
  const loud = sev === 'error' || sev === 'warn';
  // An identical notice that is still on screen is not stacked again (repeated clicks).
  const cls = 'toast-item ' + (sev || 'info');
  for (const el of c.children) {
    if (el.className.split(/\s+/).slice(0, 2).join(' ') === cls && el.firstChild && el.firstChild.nodeValue === m && !el.classList.contains('dying')) return el;
  }
  const t = document.createElement('div');
  t.className = 'toast-item ' + (sev || 'info');
  t.textContent = m;
  if (loud) { t.setAttribute('role', 'alert'); t.tabIndex = 0; }
  if (sev === 'error') {
    // Failures stay until dismissed (WCAG 2.2.1): a button, or Escape on the focused toast.
    const x = document.createElement('button');
    x.type = 'button';
    x.className = 'btn xs toast-dismiss';
    x.setAttribute('aria-label', 'Dismiss');
    x.innerHTML = icon('close');
    x.addEventListener('click', () => dismissToast(t));
    t.addEventListener('keydown', e => { if (e.key === 'Escape') dismissToast(t); });
    t.appendChild(x);
  }
  c.appendChild(t);
  evictToasts(c);
  requestAnimationFrame(() => t.classList.add('show'));
  if (sev !== 'error') armToastTimer(t, sev === 'warn' ? 5000 : 2600);
  return t;
}
// Findings are agent-maintained: the web UI writes to them only while
// Settings > findings.uiEditing is on. This is the ONE definition of that gate;
// every UI path that writes a finding (and every control that exists only to
// write one) goes through it. It is a UI guard only: agents keep full write
// access over MCP and REST.
export const FINDINGS_EDITING_OFF = 'Findings editing is off. Enable it in Settings.';
export function findingsEditable(){ return !!state.findingsUIEditing; }
// requireFindingsEditing is the gate for an action that was invoked anyway
// (keyboard shortcut, palette entry, stale handler): it refuses and says why.
export function requireFindingsEditing(){
  if(findingsEditable())return true;
  // One notice at a time: a second blocked action replaces the first (and
  // restarts its timer) instead of stacking an identical toast.
  document.querySelectorAll('#toast [data-toast-key="findings-off"]').forEach(x=>x.remove());
  // Offer the fix in place instead of sending the user to an unrelated page.
  const t=toast(FINDINGS_EDITING_OFF,'warn');
  if(t){
    t.dataset.toastKey='findings-off';
    const b=document.createElement('button');
    b.type='button';b.className='btn xs toast-action';b.textContent='Enable editing';
    b.addEventListener('click',()=>{enableFindingsEditing();dismissToast(t);});
    t.appendChild(b);
  }
  return false;
}
// enableFindingsEditing turns the Settings switch on through its own handler,
// so the save, revision check and confirmation stay in one place.
export function enableFindingsEditing(){
  const b=$('#findingsUIEditingToggle');
  if(!b)return false;
  if(!b.classList.contains('on'))b.click();
  return true;
}
// assertFindingsWritable is the same gate for code that reports failure by
// throwing (queued saves, deletes, evidence writes): re-check it at the write,
// after any await, rather than trusting the state at render time.
export function assertFindingsWritable(){
  if(!findingsEditable())throw new Error(FINDINGS_EDITING_OFF);
}
// toastError(prefix, e) reports a failed action. e may be an Error, a string or
// anything with a message; the prefix names the action ("Save failed"). A single
// non-string argument is treated as the error itself.
export function toastError(prefix, e) {
  if (e === undefined && typeof prefix !== 'string') { e = prefix; prefix = ''; }
  const detail = (e && e.message) || (e ? String(e) : '') || 'request failed';
  toast(prefix ? prefix + ': ' + detail : detail, 'error');
}

// wireRowKey makes a clickable <div> row keyboard-operable: it becomes a focusable
// button to assistive tech and Enter/Space activate it. `onActivate` defaults to a
// synthetic click so existing onclick handlers keep working unchanged. Idempotent —
// re-wiring the same element (rows are recycled on re-render) won't stack listeners.
export function wireRowKey(el, onActivate){
  if(!el||el._rowKeyed) return el;
  el._rowKeyed=true;
  if(!el.hasAttribute('role')) el.setAttribute('role','button');
  if(!el.hasAttribute('tabindex')) el.tabIndex=0;
  el.addEventListener('keydown',e=>{
    if(e.key!=='Enter'&&e.key!==' ') return;
    // Let Enter/Space fall through when focus is on a real control inside the row
    // (button/input/textarea/select/link, or anything we've promoted to role=button
    // such as a tag chip) — those have their own activation.
    const t=e.target;
    if(t!==el&&t.closest&&t.closest('button,input,textarea,select,a,[role="button"]')) return;
    e.preventDefault();
    if(onActivate) onActivate(e); else el.click();
  });
  return el;
}

// setSeg toggles a segmented-control button's pressed state, keeping the visual
// `.on` class and the ARIA `aria-pressed` attribute in sync for screen readers.
export function setSeg(btn,on){
  if(!btn) return;
  btn.classList.toggle('on',!!on);
  btn.setAttribute('aria-pressed',on?'true':'false');
}

// Project-scoped localStorage (#17/#18): feature preferences and drafts must not
// leak across project switches. Repeater/Intruder tabs and presets plus Map view
// preferences use the canonical project directory in each key. On first use of
// a scoped key, an unscoped or uniquely owned name-based legacy value is
// migrated once only when its project ownership is unique; ambiguous or
// conflicting legacy state stays untouched.
// Keep this below the browser's usual per-origin quota so one unexpectedly
// restored blob cannot monopolize the main thread during JSON.parse. The raw
// value remains untouched when the guard trips; the user can still recover it
// from project storage or an older browser profile instead of losing drafts.
export const MAX_PROJECT_UI_STATE_BYTES=4*1024*1024;
export function persistedStateByteLength(value){return new TextEncoder().encode(String(value)).byteLength;}
const MAX_PERSISTED_TAB_COUNT=200;
export function isSafePersistedTabState(value){
  if(!value||typeof value!=='object'||Array.isArray(value)||!Array.isArray(value.tabs)
    ||value.tabs.length>MAX_PERSISTED_TAB_COUNT)return false;
  const seenTabIDs=new Set();
  for(const tab of value.tabs){
    if(!tab||typeof tab!=='object'||Array.isArray(tab)||!Number.isSafeInteger(tab.tid)||tab.tid<=0
      ||tab.tid>=Number.MAX_SAFE_INTEGER||seenTabIDs.has(tab.tid))return false;
    seenTabIDs.add(tab.tid);
  }
  return true;
}
let storageProject='default';
let storageLegacyProject='default';
let storageProjectNames=[];
let storageProjectNamesKnown=false;
const migratedStorageBases=new Set();
const storageMigrationWarnings=new Map();
const legacyProjectStorageID=name=>String(name).replace(/[^A-Za-z0-9._-]+/g,'_')||'default';
export function setStorageProject(name,knownProjects=[],legacyName=name){
  storageProject=(name&&String(name).trim())||'default';
  storageLegacyProject=(legacyName&&String(legacyName).trim())||storageProject;
  const projectEntries=Array.isArray(knownProjects)?knownProjects:[];
  let malformedProjectEntry=!Array.isArray(knownProjects);
  const normalizedProjectNames=projectEntries.map(project=>{
    const isObject=!!project&&typeof project==='object'&&!Array.isArray(project);
    const projectName=typeof project==='string'?project:isObject?project.name:null;
    if(typeof projectName!=='string'||!projectName.trim()
      ||(isObject&&project.path!==undefined&&typeof project.path!=='string')){
      malformedProjectEntry=true;
      return null;
    }
    return projectName;
  }).filter(project=>project!==null);
  storageProjectNames=normalizedProjectNames;
  // A valid-looking name is not enough to prove ownership if another source
  // entry could not be interpreted. Keep legacy state untouched until the
  // complete project list is trustworthy.
  storageProjectNamesKnown=storageProjectNames.length>0&&!malformedProjectEntry;
  if(!storageProjectNames.includes(storageLegacyProject))storageProjectNames.push(storageLegacyProject);
}
export function consumeStorageMigrationWarning(key){
  const warning=storageMigrationWarnings.get(key)||'';
  storageMigrationWarnings.delete(key);
  return warning;
}
export function projectStorageLegacyKeys(base){
  if(!storageProjectNamesKnown)return[];
  const keys=[];
  const exactMatches=storageProjectNames.filter(project=>project===storageLegacyProject);
  if(exactMatches.length===1)keys.push(base+'.v2.'+(encodeURIComponent(storageLegacyProject)||'default'));
  const legacySafe=legacyProjectStorageID(storageLegacyProject);
  const legacyMatches=storageProjectNames.filter(project=>legacyProjectStorageID(project)===legacySafe);
  if(legacyMatches.length===1)keys.push(base+'.'+legacySafe);
  const current=base+'.v2.'+(encodeURIComponent(String(storageProject))||'default');
  return [...new Set(keys.filter(key=>key!==current))];
}
export function projectStorageKey(base){
  // Percent-encode the complete identity instead of replacing characters.
  // Lossy replacement made distinct valid names such as "team alpha" and
  // "team_alpha" share drafts. The versioned namespace lets us distinguish a
  // current encoded key from the older ambiguous sanitized-key format.
  const safe=encodeURIComponent(String(storageProject))||'default';
  const scoped=base+'.v2.'+safe;
  // One-shot migrate: copy legacy state into *this* project only when the
  // project list proves a unique owner, then remove the source key so a later
  // project switch cannot inherit the same state (#17/#18).
  if(!migratedStorageBases.has(base)){
    let migrationComplete=true;
    try{
      const legacySafe=legacyProjectStorageID(storageLegacyProject);
      const legacyCandidates=[base+'.v2.'+(encodeURIComponent(storageLegacyProject)||'default'),base+'.'+legacySafe]
        .filter(key=>key!==scoped);
      const safeLegacyKeys=projectStorageLegacyKeys(base);
      let hasCurrent=localStorage.getItem(scoped)!=null;
      if(!hasCurrent){
        for(const legacyScoped of safeLegacyKeys){
          const legacyValue=localStorage.getItem(legacyScoped);
          if(legacyValue==null)continue;
          if(persistedStateByteLength(legacyValue)>MAX_PROJECT_UI_STATE_BYTES){
            storageMigrationWarnings.set(scoped,'oversized legacy state');
          }else{
            localStorage.setItem(scoped,legacyValue);
            localStorage.removeItem(legacyScoped);
            hasCurrent=true;
          }
          break;
        }
      }
      const old=localStorage.getItem(base);
      if(old!=null){
        const unscopedOwnershipKnown=storageProjectNamesKnown&&storageProjectNames.length===1&&storageProjectNames[0]===storageLegacyProject;
        if(!unscopedOwnershipKnown){
          const unscopedWarning=storageProjectNamesKnown?'ambiguous legacy project state':'legacy project ownership unavailable';
          storageMigrationWarnings.set(scoped,unscopedWarning);
          migrationComplete=false;
        }else if(persistedStateByteLength(old)>MAX_PROJECT_UI_STATE_BYTES){
          // Keep an oversized legacy value at its original key. The scoped key
          // remains safe for the current project and the tab manager surfaces
          // the retained legacy draft instead of overwriting it during boot.
          storageMigrationWarnings.set(scoped,'oversized legacy state');
        }else if(!hasCurrent){
          localStorage.setItem(scoped,old);hasCurrent=true;
          localStorage.removeItem(base);
        }else if(localStorage.getItem(scoped)===old){
          // A byte-identical canonical copy makes removal a cleanup, not a
          // choice between two drafts.
          localStorage.removeItem(base);
        }else{
          storageMigrationWarnings.set(scoped,'conflicting unscoped legacy state');
        }
      }
      const inaccessibleLegacy=legacyCandidates.some(key=>!safeLegacyKeys.includes(key)&&localStorage.getItem(key)!=null);
      if(inaccessibleLegacy&&!storageProjectNamesKnown){
        // /api/version can identify the canonical directory even when the
        // project-list endpoint is unavailable, but it cannot prove ownership
        // of an older name-keyed draft. Keep that draft untouched and retry its
        // migration later. New edits can still use the collision-free key made
        // from the exact canonical project directory.
        storageMigrationWarnings.set(scoped,'legacy project ownership unavailable');
        migrationComplete=false;
      }else if(inaccessibleLegacy){
        storageMigrationWarnings.set(scoped,'ambiguous legacy project state');
      }
    }catch(e){
      migrationComplete=false;
      storageMigrationWarnings.set(scoped,'legacy project state could not be migrated');
    }
    if(migrationComplete)migratedStorageBases.add(base);
  }
  return scoped;
}

export function renderLoadError(el, label, err, retry, stale=false){
  if(!el)return;
  const message=err&&err.message?err.message:String(err||'unknown error');
  el.style.display='block';
  // Keep the alert scoped to the newly rendered error message. The host may be
  // a high-volume list, so making it live would announce every recovery render.
  el.innerHTML=`<span class="state-error-msg" role="alert">${esc(label)} ${stale?'is stale — ':'failed: '}${esc(message)}</span> <button type="button" class="btn xs" data-load-retry>Retry</button>`;
  const btn=el.querySelector('[data-load-retry]');
  if(btn)btn.onclick=retry;
}

// createTabManager — generic "tabs with localStorage persistence" pattern,
// extracted from Repeater's and Intruder's independently-reimplemented copies
// (docs/UI-REDESIGN-ROADMAP.md §4 consolidation targets). Each panel keeps its
// own tab shape (fields differ between Repeater/Intruder) and its own editor
// wiring — the manager only owns the array/active-id/seq bookkeeping, the
// localStorage round-trip, and the `.rep-tab` bar markup, via hooks:
//   storageKey  — localStorage key string, or () => key (project-scoped)
//   blank(seq)  — create a new tab object (receives the next tid)
//   title(tab)  — label text for the tab bar / tooltip
//   onSave()    — snapshot the live editor DOM into the (about to be inactive
//                 or about to be persisted) active tab; called before switching,
//                 closing, adding or persisting
//   onLoad(tab) — paint `tab` into the editor DOM; called after switching,
//                 closing, adding, or on init
//   normalize(raw) — rehydrate one persisted tab object from localStorage into
//                 the shape the panel expects (defaults + type coercion)
//   serialize(tab) — transform a tab before it's written to localStorage
//                 (defaults to identity; Intruder uses this to drop huge
//                 payload arrays down to a truncated/counted form)
//   labelStyle(tab,isActive) — optional inline `style="…"` attribute value
//                 for a tab's `.rt-label` span (Repeater colors it by method;
//                 omit to leave the span unstyled)
// Returns tab/ID allocation, rendering, persistence, switching, closing, and
// initialization operations for the owning panel.
// revealActiveTab scrolls a crowded tab strip so the active tab is fully in view
// (the sticky + keeps its own room through scroll-padding-inline-end).
export function revealActiveTab(bar){
  const tab=bar.querySelector('.rep-tab.on');
  if(!tab||bar.scrollWidth<=bar.clientWidth)return;
  try{tab.scrollIntoView({block:'nearest',inline:'nearest'});}catch(e){/* layout not ready */}
}
export function createTabManager(opts){
  const {storageKey:keyOpt,blank,title,onSave,onLoad,normalize,serialize=(t=>t),labelStyle,onPersist,onClose,tablistLabel='Tabs',tabPanelId='',storageLabel='workspace',onStorageWarning}=opts;
  const storageKey=()=>typeof keyOpt==='function'?keyOpt():keyOpt;
  const mgr={tabs:[],active:null,seq:1,persistT:null,focusEpoch:0,storageWarning:false,storageWarnings:new Set()};
  mgr.warnStorage=message=>{
    mgr.storageWarning=true;
    if(mgr.storageWarnings.has(message))return;
    mgr.storageWarnings.add(message);
    if(typeof onStorageWarning==='function')onStorageWarning(message);
  };
  mgr.takeNextTid=()=>{
    const used=new Set(mgr.tabs.map(tab=>tab.tid));
    let candidate=Number.isSafeInteger(mgr.seq)&&mgr.seq>0&&mgr.seq<Number.MAX_SAFE_INTEGER?mgr.seq:1;
    // Persisted sequence values are advisory. At most 200 IDs can be occupied,
    // so a bounded scan always finds a safe gap without overflowing an integer.
    for(let checked=0;checked<=used.size;checked++){
      if(candidate>=Number.MAX_SAFE_INTEGER)candidate=1;
      if(!used.has(candidate)){
        mgr.seq=candidate+1;
        if(mgr.seq>=Number.MAX_SAFE_INTEGER)mgr.seq=1;
        return candidate;
      }
      candidate++;
    }
    return null;
  };
  mgr.availableSlots=()=>MAX_PERSISTED_TAB_COUNT-mgr.tabs.length;
  mgr.create=function(){
    if(mgr.tabs.length>=MAX_PERSISTED_TAB_COUNT){toast(`${storageLabel} supports up to ${MAX_PERSISTED_TAB_COUNT} tabs. Close one before adding another.`,'warn');return null;}
    const tid=mgr.takeNextTid();
    if(tid===null){toast(`${storageLabel} could not allocate another tab identifier.`,'error');return null;}
    const tab=blank(tid);
    mgr.tabs.push(tab);
    return tab;
  };
  mgr.cur=()=>mgr.tabs.find(t=>t.tid===mgr.active)||null;
  mgr.persist=function(){
    const blob={seq:mgr.seq,active:mgr.active,tabs:mgr.tabs.map(serialize)};
    let storedLocally=true;
    const key=storageKey();
    try{localStorage.setItem(key,JSON.stringify(blob));}
    catch(e){
      storedLocally=false;
      mgr.warnStorage(`${storageLabel} could not be saved in this browser; project storage will still be updated.`);
    }
    // Optional project-DB sync (Repeater/Intruder) — best-effort, never blocks UI.
    if(typeof onPersist==='function'){try{onPersist(blob);}catch(e){}}
    return storedLocally;
  };
  mgr.persistDebounced=function(){mgr.persistPending=true;clearTimeout(mgr.persistT);mgr.persistT=setTimeout(()=>{mgr.persistPending=false;mgr.persist();},400);};
  // Edits typed inside the debounce window must survive a reload or quit.
  mgr.flushPending=function(){if(!mgr.persistPending)return;clearTimeout(mgr.persistT);mgr.persistPending=false;mgr.persist();};
  flushOnClose({isDirty:()=>mgr.persistPending,flush:()=>mgr.flushPending()});
  mgr.render=function(barSel){
    mgr.focusEpoch++;
    const bar=$(barSel);if(!bar)return;
    bar.setAttribute('role','tablist');bar.setAttribute('aria-label',tablistLabel);bar.setAttribute('aria-orientation','horizontal');
    const atTabLimit=mgr.tabs.length>=MAX_PERSISTED_TAB_COUNT;
    const addLabel=atTabLimit?`${storageLabel} supports up to ${MAX_PERSISTED_TAB_COUNT} tabs; close one to add another`:`New ${tablistLabel.replace(/ tabs?$/i,' tab')}`;
    bar.innerHTML=mgr.tabs.map(t=>{
      const active=t.tid===mgr.active;
      const style=labelStyle?labelStyle(t,active):'';
      const label=title(t);
      return `<div class="rep-tab${active?' on':''}" data-tid="${t.tid}" title="${escAttr(label)}">
    <button type="button" class="rt-select" id="${bar.id}Tab${t.tid}" role="tab" aria-selected="${active?'true':'false'}" aria-controls="${escAttr(tabPanelId)}" tabindex="${active?'0':'-1'}" aria-label="${escAttr(label)}">
      <span class="rt-label"${style?` style="${escAttr(style)}"`:''}>${esc(label)}</span>
    </button>
    <button type="button" class="rt-close" data-close="${t.tid}" aria-label="Close ${escAttr(label)}" title="Close ${escAttr(label)}">${icon('close')}</button></div>`;
    }).join('')+`<button type="button" class="rep-tab-add" id="${bar.id}Add" aria-disabled="${atTabLimit?'true':'false'}" aria-label="${escAttr(addLabel)}" title="${escAttr(addLabel)}">${icon('plus')}</button>`;
    revealActiveTab(bar);
    const tabPanel=document.getElementById(tabPanelId);
    const activeTab=bar.querySelector('.rt-select[aria-selected="true"]');
    if(tabPanel&&activeTab)tabPanel.setAttribute('aria-labelledby',activeTab.id);
    const tabButtons=[...bar.querySelectorAll('.rt-select')];
    tabButtons.forEach((el,i)=>{
      const item=el.closest('.rep-tab'),tid=Number(item.dataset.tid);
      el.onclick=()=>mgr.switchTo(tid,true);
      el.onkeydown=e=>{
        let next=-1;
        if(e.key==='ArrowRight')next=(i+1)%tabButtons.length;
        else if(e.key==='ArrowLeft')next=(i-1+tabButtons.length)%tabButtons.length;
        else if(e.key==='Home')next=0;
        else if(e.key==='End')next=tabButtons.length-1;
        else return;
        e.preventDefault();
        const nextTid=Number(tabButtons[next].closest('.rep-tab').dataset.tid);
        mgr.switchTo(nextTid,true);
      };
    });
    bar.querySelectorAll('[data-close]').forEach(x=>x.onclick=e=>{e.stopPropagation();mgr.close(Number(x.dataset.close),true);});
    const addBtn=$('#'+bar.id+'Add');
    if(addBtn)addBtn.onclick=()=>{
      if(mgr.tabs.length>=MAX_PERSISTED_TAB_COUNT){mgr.create();return;}
      onSave();
      const tab=mgr.create();
      if(!tab)return;
      mgr.active=tab.tid;
      mgr.render(barSel);onLoad(mgr.cur());mgr.persist();
      mgr._focusTab?.(mgr.active);
    };
  };
  mgr.switchTo=function(tid,restoreFocus=false){
    if(tid===mgr.active){if(restoreFocus)mgr._focusTab?.(tid);return;}
    onSave();mgr.active=tid;mgr._rerender();onLoad(mgr.cur());mgr.persist();
    if(restoreFocus)mgr._focusTab?.(tid);
  };
  mgr.close=function(tid,restoreFocus=false){
    const i=mgr.tabs.findIndex(t=>t.tid===tid);if(i<0)return;
    const closed=mgr.tabs[i];
    const wasActive=tid===mgr.active;
    closed._closed=true;
    let closeResult;
    if(typeof onClose==='function'){
      try{closeResult=onClose(closed);}catch(e){closed._closed=false;toast(e.message||'Could not close tab');return;}
    }
    mgr.tabs.splice(i,1);
    if(closeResult)Promise.resolve(closeResult).catch(()=>{});
    if(!mgr.tabs.length)mgr.create();
    if(wasActive)mgr.active=mgr.tabs[Math.min(i,mgr.tabs.length-1)].tid;
    mgr._rerender();
    if(wasActive)onLoad(mgr.cur());
    mgr.persist();
    if(restoreFocus)mgr._focusTab?.(mgr.active);
  };
  // init loads persisted tabs (or seeds one blank tab), wires the bar, and
  // paints the editor. barSel is stored so switchTo/close/add can re-render
  // the same bar without every caller having to pass it again.
  mgr.init=function(barSel,fallbackState=null){
    mgr._rerender=()=>mgr.render(barSel);
    mgr._focusTab=tid=>{
      const epoch=++mgr.focusEpoch;
      requestAnimationFrame(()=>{
        if(epoch!==mgr.focusEpoch||mgr.active!==tid)return;
        $(barSel)?.querySelector(`.rep-tab[data-tid="${tid}"] .rt-select`)?.focus();
      });
    };
    let ok=false,localState=null;
    const applyState=d=>{
      if(!isSafePersistedTabState(d)||!d.tabs.length)return false;
      const normalizedTabs=[];
      for(const rawTab of d.tabs)normalizedTabs.push(normalize(rawTab,normalizedTabs));
      mgr.tabs=normalizedTabs;
      mgr.active=(d.active&&mgr.tabs.find(x=>x.tid===d.active))?d.active:mgr.tabs[0].tid;
      const fin=mgr.tabs.map(t=>t.tid).filter(Number.isFinite);
      const persistedSeq=Number.isSafeInteger(d.seq)&&d.seq>0?d.seq:0;
      mgr.seq=Math.max(persistedSeq,(fin.length?Math.max(...fin):0)+1);
      return true;
    };
    try{
      const key=storageKey();
      const migrationWarning=consumeStorageMigrationWarning(key);
      if(migrationWarning==='ambiguous legacy project state')mgr.warnStorage(`${storageLabel} has saved browser state under an older key shared by multiple project names; it was kept unchanged for recovery.`);
      else if(migrationWarning==='legacy project ownership unavailable')mgr.warnStorage(`${storageLabel} legacy migration is deferred until project ownership is known; the older unscoped or name-keyed draft was kept unchanged. New edits still use project-specific browser and project storage.`);
      else if(migrationWarning==='conflicting unscoped legacy state')mgr.warnStorage(`${storageLabel} has an older unscoped browser draft that differs from current project state; both were kept, and the project-specific draft remains active.`);
      else if(migrationWarning==='legacy project state could not be migrated')mgr.warnStorage(`${storageLabel} legacy browser state could not be migrated; the original draft was kept for recovery.`);
      else if(migrationWarning)mgr.warnStorage(`${storageLabel} legacy saved state is too large to load safely; the original draft was kept.`);
      const raw=localStorage.getItem(key);
      if(raw&&persistedStateByteLength(raw)>MAX_PROJECT_UI_STATE_BYTES){
        mgr.warnStorage(`${storageLabel} saved state is too large to load safely; the original draft was kept for recovery. Editing this replacement tab will replace it.`);
      }else{
        const d=JSON.parse(raw||'null');
        if(d&&Array.isArray(d.tabs)&&d.tabs.length>MAX_PERSISTED_TAB_COUNT){
          mgr.warnStorage(`${storageLabel} saved state has too many tabs to load safely; the original draft was kept for recovery. Editing this replacement tab will replace it.`);
        }else if(d!==null&&!isSafePersistedTabState(d)){
          mgr.warnStorage(`${storageLabel} saved state is not recognized; the original draft was kept for recovery. Editing this replacement tab will replace it.`);
        }else if(d&&d.tabs.length)localState=d;
      }
    }catch(e){
      mgr.warnStorage(`${storageLabel} saved state could not be read; the original draft was kept for recovery. Editing this replacement tab will replace it.`);
    }
    if(isSafePersistedTabState(fallbackState))ok=applyState(fallbackState);
    else if(localState)ok=applyState(localState);
    if(!ok){mgr.tabs=[];mgr.create();mgr.active=mgr.tabs[0].tid;}
    mgr._rerender();
    onLoad(mgr.cur());
  };
  return mgr;
}

// ui-select — themed dropdowns that replace the OS-native select menu. The native
// <select> stays in the DOM (hidden) so existing .value / .onchange / innerHTML
// code keeps working; we mirror options into a custom menu and dispatch change.
let uiSelectOpen=null;
let uiSelectSeq=0;

function closeUiSelectMenu(inst){
  if(!inst)return;
  inst.menu.hidden=true;
  inst.menu.removeAttribute('data-motion-side');
  inst.menu.classList.remove('ui-select-menu-fixed');
  inst.menu.style.cssText='';
  if(inst.menuZIndexSaved){
    if(inst.menuPreviousZIndex)inst.menu.style.setProperty('z-index',inst.menuPreviousZIndex,inst.menuPreviousZIndexPriority);
    else inst.menu.style.removeProperty('z-index');
    inst.menuPreviousZIndex='';inst.menuPreviousZIndexPriority='';inst.menuZIndexSaved=false;
  }
  // Return the portaled menu (see openUiSelectMenu) to its wrap.
  if(inst.wrap&&inst.menu.parentNode!==inst.wrap)inst.wrap.appendChild(inst.menu);
  inst.trigger.setAttribute('aria-expanded','false');
  inst.trigger.removeAttribute('aria-activedescendant');
  if(uiSelectOpen&&uiSelectOpen.menu===inst.menu)uiSelectOpen=null;
}

export function closeAllUiSelects(){
  if(uiSelectOpen)closeUiSelectMenu(uiSelectOpen);
}

function scrollUiSelectMenuToSelected(menu){
  const opt=menu.querySelector('.ui-select-opt.active,.ui-select-opt.sel');
  if(!opt)return;
  const top=opt.offsetTop,bottom=top+opt.offsetHeight;
  if(top<menu.scrollTop)menu.scrollTop=top;
  else if(bottom>menu.scrollTop+menu.clientHeight)menu.scrollTop=bottom-menu.clientHeight;
}

function openUiSelectMenu(inst){
  closeAllUiSelects();
  hideCtxMenu(); // dismiss any open ctx/Views menu — same mutually-exclusive group
  const r=inst.trigger.getBoundingClientRect();
  inst.menuPreviousZIndex=inst.menu.style.getPropertyValue('z-index');
  inst.menuPreviousZIndexPriority=inst.menu.style.getPropertyPriority('z-index');
  inst.menuZIndexSaved=true;
  // Portal the menu to <body> before showing it. Its .ui-select wrap lives inside
  // the Proxy .toolbar, whose backdrop-filter creates BOTH a stacking context and a
  // containing block for position:fixed descendants. Left in place, the menu (a)
  // can't raise its z-index above later-painted toolbar siblings (#chips, the flow
  // list) and (b) resolves its fixed coords relative to the toolbar, not the
  // viewport. At <body> level both behave. Menu styling is class-based (not scoped
  // under .ui-select), so it renders identically here; closeUiSelectMenu returns it.
  if(inst.menu.parentNode!==document.body)document.body.appendChild(inst.menu);
  inst.menu.classList.add('ui-select-menu-fixed');
  inst.menu.style.left=r.left+'px';
  inst.menu.style.top=(r.bottom+4)+'px';
  inst.menu.style.width=Math.max(r.width,120)+'px';
  inst.menu.style.setProperty('z-index',String(uiSelectMenuZIndex(inst)));
  inst.menu.hidden=false;
  const vv=window.visualViewport;
  const placement=placeFloatingSurface(r,Math.max(r.width,160),Math.min(320,inst.menu.scrollHeight+2),{width:vv?.width||innerWidth,height:vv?.height||innerHeight,left:vv?.offsetLeft||0,top:vv?.offsetTop||0});
  inst.menu.style.left=placement.left+'px';
  inst.menu.style.top=placement.top+'px';
  inst.menu.style.width=placement.width+'px';
  inst.menu.style.maxHeight=placement.maxHeight+'px';
  inst.trigger.setAttribute('aria-expanded','true');
  uiSelectOpen=inst;
  const selected=[...inst.sel.options].findIndex(o=>o.selected&&!o.disabled);
  inst.setActive(selected>=0?selected:inst.firstEnabled());
  scrollUiSelectMenuToSelected(inst.menu);
  inst.menu.dataset.motionSide=placement.side;
}

export function syncUiSelectStyles(sel){
  const inst=sel&&sel._uiSelect;
  if(!inst)return;
  const w=inst.wrap,s=sel;
  w.hidden=s.hidden;
  w.style.display=s.style.display;
  w.style.maxWidth=s.style.maxWidth;
  w.style.width=s.style.width;
  w.style.minWidth=s.style.minWidth;
  w.style.flex=s.style.flex;
}

export function refreshUiSelect(sel){
  const inst=sel&&sel._uiSelect;
  if(inst)inst.render();
}

function uiSelectAccessibleName(sel){
  const explicit=(sel.getAttribute('aria-label')||'').trim();
  if(explicit)return explicit;
  const label=sel.labels&&sel.labels[0];
  const labelText=(label?.textContent||'').trim();
  if(labelText)return labelText;
  const title=(sel.getAttribute('title')||sel.dataset.tooltip||'').trim();
  if(title)return title;
  const id=(sel.id||'select').replace(/([a-z0-9])([A-Z])/g,'$1 $2').replace(/[-_]+/g,' ').trim();
  return id||'Select option';
}
function uiSelectHandlesKey(e,open){
  return e.key==='Escape'?open
    :['ArrowDown','ArrowUp','Home','End','Enter',' '].includes(e.key)
    ||(!e.ctrlKey&&!e.metaKey&&!e.altKey&&e.key.length===1&&/\S/.test(e.key));
}
function uiSelectMenuZIndex(inst){
  let ownerZ=219;
  for(const entry of modalStack){
    if(entry.dialog.contains(inst.trigger))ownerZ=Math.max(ownerZ,Number.parseInt(getComputedStyle(entry.el).zIndex,10)||0);
  }
  const triggerZ=Number.parseInt(getComputedStyle(inst.trigger).zIndex,10)||0;
  return Math.max(ownerZ,triggerZ)+1;
}
function wireUiSelectLabels(sel,trigger){
  const labels=[...(sel.labels||[])];
  labels.forEach((label,i)=>{
    if(!label.id)label.id=(sel.id||trigger.id||'uiSelect')+'Label'+i;
    label.addEventListener('click',e=>{
      const fromTrigger=e.target===trigger||trigger.contains(e.target);
      if(fromTrigger){e.preventDefault();return;}
      if(e.target.closest?.('a,button,input,textarea,select'))return;
      e.preventDefault();
      trigger.focus();
      trigger.click();
    });
  });
  return labels;
}

export function enhanceSelect(sel){
  if(!sel||sel.tagName!=='SELECT'||sel._uiSelect)return sel;

  const wrap=document.createElement('div');
  wrap.className='ui-select';
  if(sel.classList.contains('btn'))wrap.classList.add('ui-select-btn');
  if(sel.classList.contains('rep-method')||sel.classList.contains('repurl'))wrap.classList.add('ui-select-rep');
  if(sel.closest('.search'))wrap.classList.add('ui-select-search');
  if(sel.closest('.rules-tbl')||sel.closest('td'))wrap.classList.add('ui-select-inline');
  if(sel.closest('.settings-body .field'))wrap.classList.add('ui-select-field');

  const parent=sel.parentNode;
  parent.insertBefore(wrap,sel);
  wrap.appendChild(sel);
  sel.classList.add('ui-select-native');
  sel.tabIndex=-1;
  sel.setAttribute('aria-hidden','true');

  const trigger=document.createElement('button');
  trigger.type='button';
  trigger.className='ui-select-trigger';
  trigger.setAttribute('role','combobox');
  trigger.setAttribute('aria-haspopup','listbox');
  trigger.setAttribute('aria-expanded','false');
  if(sel.getAttribute('aria-describedby'))trigger.setAttribute('aria-describedby',sel.getAttribute('aria-describedby'));
  if(sel.getAttribute('title')||sel.dataset.tooltip)trigger.dataset.tooltip=sel.getAttribute('title')||sel.dataset.tooltip;
  const valueEl=document.createElement('span');
  valueEl.className='ui-select-value';
  const caret=document.createElement('span');
  caret.className='ui-select-caret';
  caret.setAttribute('aria-hidden','true');
  caret.innerHTML=icon('chevron');
  trigger.append(valueEl,caret);

  const menu=document.createElement('div');
  menu.className='ui-select-menu';
  menu.setAttribute('role','listbox');
  menu.id='uiSelectList'+(++uiSelectSeq);
  trigger.setAttribute('aria-controls',menu.id);
  menu.hidden=true;

  wrap.insertBefore(trigger,sel);
  wrap.appendChild(menu);

  trigger.setAttribute('aria-label',uiSelectAccessibleName(sel));
  const sid=sel.id;
  trigger.id=sid?sid+'Ui':menu.id+'Trigger';
  const labels=wireUiSelectLabels(sel,trigger);
  if(labels.length)trigger.setAttribute('aria-labelledby',labels.map(label=>label.id).join(' '));

  const inst={sel,wrap,trigger,menu,valueEl,active:-1,typeahead:'',typeaheadTimer:null,
    menuPreviousZIndex:'',menuPreviousZIndexPriority:'',menuZIndexSaved:false,
    syncStyles(){syncUiSelectStyles(sel);},
    firstEnabled(){return [...sel.options].findIndex(o=>!o.disabled&&!o.hidden);},
    setActive(i){
      const opts=[...sel.options];
      if(i<0||i>=opts.length||opts[i].disabled||opts[i].hidden)return;
      inst.active=i;
      menu.querySelectorAll('.ui-select-opt').forEach((el,n)=>el.classList.toggle('active',n===i));
      const active=menu.querySelector('.ui-select-opt[data-index="'+i+'"]');
      if(active){
        trigger.setAttribute('aria-activedescendant',active.id);
        active.scrollIntoView({block:'nearest'});
      }
    },
    moveActive(delta){
      const enabled=[...sel.options].map((o,i)=>!o.disabled&&!o.hidden?i:-1).filter(i=>i>=0);
      if(!enabled.length)return;
      let pos=enabled.indexOf(inst.active);
      if(pos<0)pos=delta>0?-1:enabled.length;
      inst.setActive(enabled[Math.max(0,Math.min(enabled.length-1,pos+delta))]);
    },
    chooseActive(){
      const opt=sel.options[inst.active];if(!opt||opt.disabled)return;
      if(sel.value!==opt.value){
        sel.value=opt.value;
        sel.dispatchEvent(new Event('change',{bubbles:true}));
      }
      inst.render();inst.close();trigger.focus();
    },
    render(){
      syncUiSelectStyles(sel);
      const cur=sel.value;
      const opts=[...sel.options];
      if(inst.active>=opts.length||opts[inst.active]?.disabled||opts[inst.active]?.hidden)inst.active=-1;
      menu.innerHTML=opts.map((o,i)=>{
        const v=o.value;
        const selOn=v===cur;
        const dis=o.disabled;
        return `<button type="button" role="option" tabindex="-1" id="${menu.id}Opt${i}" class="ui-select-opt${selOn?' sel':''}${i===inst.active?' active':''}" data-index="${i}" data-value="${escAttr(v)}"${dis?' disabled aria-disabled="true"':''}${o.hidden?' hidden':''} aria-selected="${selOn?'true':'false'}"><span class="ui-select-opt-title">${esc(o.textContent)}</span><svg class="icon ui-select-check" aria-hidden="true" focusable="false"><use href="#i-check"/></svg></button>`;
      }).join('');
      const picked=opts.find(o=>o.value===cur)||opts[0];
      valueEl.textContent=picked?picked.textContent:(sel.getAttribute('placeholder')||'…');
      trigger.disabled=!!sel.disabled;
      if(sel.classList.contains('rep-method'))valueEl.style.color=methodColor(sel.value);
      else valueEl.style.color='';
      if(!menu.hidden){
        const selected=opts.findIndex(o=>o.selected&&!o.disabled&&!o.hidden);
        inst.setActive(inst.active>=0?inst.active:(selected>=0?selected:inst.firstEnabled()));
      }
    },
    close(){closeUiSelectMenu(inst);}
  };

  trigger.addEventListener('click',e=>{
    e.stopPropagation();
    if(sel.disabled)return;
    if(!menu.hidden){inst.close();return;}
    openUiSelectMenu(inst);
    // WebKit does not focus a button on mouse click; the arrow keys, Enter and
    // Escape are handled on the trigger, so an open menu must own focus.
    if(document.activeElement!==trigger)trigger.focus({preventScroll:true});
  });
  trigger.addEventListener('keydown',e=>{
    const open=!menu.hidden;
    if(uiSelectHandlesKey(e,open))e.stopPropagation();
    switch(e.key){
    case 'ArrowDown':
      e.preventDefault();if(!open)openUiSelectMenu(inst);else inst.moveActive(1);break;
    case 'ArrowUp':
      e.preventDefault();if(!open)openUiSelectMenu(inst);else inst.moveActive(-1);break;
    case 'Home':
      e.preventDefault();if(!open)openUiSelectMenu(inst);inst.setActive(inst.firstEnabled());break;
    case 'End': {
      e.preventDefault();if(!open)openUiSelectMenu(inst);
      const opts=[...sel.options];for(let i=opts.length-1;i>=0;i--)if(!opts[i].disabled&&!opts[i].hidden){inst.setActive(i);break;}
      break;
    }
    case 'Enter':
    case ' ':
      e.preventDefault();if(open)inst.chooseActive();else openUiSelectMenu(inst);break;
    case 'Escape':
      if(open){e.preventDefault();e.stopPropagation();inst.close();trigger.focus();}break;
    case 'Tab':
      if(open)inst.close();break;
    default:
      if(!e.ctrlKey&&!e.metaKey&&!e.altKey&&e.key.length===1&&/\S/.test(e.key)){
        e.preventDefault();
        clearTimeout(inst.typeaheadTimer);
        inst.typeahead=(inst.typeahead+e.key).toLocaleLowerCase();
        inst.typeaheadTimer=setTimeout(()=>{inst.typeahead='';},600);
        if(!open)openUiSelectMenu(inst);
        const opts=[...sel.options],start=Math.max(0,inst.active+1);
        const query=[...inst.typeahead].every(c=>c===inst.typeahead[0])?inst.typeahead[0]:inst.typeahead;
        for(let n=0;n<opts.length;n++){
          const i=(start+n)%opts.length,o=opts[i];
          if(!o.disabled&&!o.hidden&&o.textContent.trim().toLocaleLowerCase().startsWith(query)){inst.setActive(i);break;}
        }
      }
    }
  });

  menu.addEventListener('click',e=>{
    e.stopPropagation();
    const opt=e.target.closest('.ui-select-opt');
    if(!opt||opt.disabled)return;
    inst.setActive(Number(opt.dataset.index));
    inst.chooseActive();
  });

  sel.addEventListener('change',()=>inst.render());

  const mo=new MutationObserver(()=>inst.render());
  mo.observe(sel,{childList:true,subtree:true,attributes:true,attributeFilter:['disabled','hidden','class','style']});

  // App code frequently does `select.value = x` when populating a form from loaded
  // data (settings, filters, …) — that never fires a native 'change' event, so the
  // custom trigger's label would otherwise go stale and show the wrong option.
  // Intercepting the value setter keeps the visible widget honest without requiring
  // every call site to remember to call refreshUiSelect() afterward.
  const nativeValueDesc=Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype,'value');
  if(nativeValueDesc&&nativeValueDesc.set){
    Object.defineProperty(sel,'value',{
      configurable:true,
      get(){return nativeValueDesc.get.call(this);},
      set(v){nativeValueDesc.set.call(this,v);inst.render();}
    });
  }

  sel._uiSelect=inst;
  inst.render();
  return sel;
}

export function initUiSelects(root=document){
  root.querySelectorAll('select:not(.ui-select-native)').forEach(enhanceSelect);
}

document.addEventListener('click',e=>{
  if(e.target.closest?.('.ui-select'))return;
  closeAllUiSelects();
});
document.addEventListener('keydown',e=>{if(e.key==='Escape')closeAllUiSelects();});
window.addEventListener('scroll',e=>{
  const t=e.target;
  if(t instanceof Element&&t.closest('.ui-select-menu'))return;
  closeAllUiSelects();
},true);
window.addEventListener('resize',()=>closeAllUiSelects());
// Virtualized lists (History, Intruder results, Map) churn rows constantly and
// never contain a <select>; skip their mutations instead of scanning every row.
const UI_SELECT_SKIP='#rows,.intr-virt-body,.map-virt-body';
new MutationObserver(muts=>{
  for(const m of muts){
    if(m.target.closest?.(UI_SELECT_SKIP))continue;
    m.addedNodes.forEach(n=>{
      if(n.nodeType!==1)return;
      if(n.tagName==='SELECT')enhanceSelect(n);
      else if(n.querySelector('select'))initUiSelects(n);
    });
  }
}).observe(document.documentElement,{childList:true,subtree:true});

// apiErrorMessage turns a failed response into one short, human sentence: the
// JSON `error` field, a short plain-text body, the status text, or "HTTP <n>"
// (HTTP/2 has no status text). HTML bodies are never surfaced.
async function apiErrorMessage(r) {
  const fallback = r.statusText || 'HTTP ' + r.status;
  let raw = '';
  try { raw = String(await r.text()).trim(); } catch (e) { return fallback; }
  if (!raw) return fallback;
  try {
    const j = JSON.parse(raw);
    return (j && typeof j.error === 'string' && j.error) || fallback;
  } catch (e) { /* not JSON */ }
  if (raw[0] === '<' || raw.length > 200) return fallback;
  return raw;
}
// apiFetchError wraps a rejected fetch: a caller-driven abort passes through;
// timeouts and unreachable servers get a message the operator can act on.
function apiFetchError(e, opts) {
  if (opts.signal && opts.signal.aborted && !opts.defaultTimeout) return e;
  const timedOut = e && e.name === 'TimeoutError';
  const err = new Error(timedOut
    ? 'The Interseptor control server did not respond in time. Retry in a moment.'
    : 'Cannot reach the Interseptor control server. Check that it is still running, then retry.');
  err.code = timedOut ? 'timeout' : 'network';
  return err;
}
// Concurrent identical GETs share one request (api-inflight.js). Mutations and
// live events clear the table so a read issued after a change never joins an
// older request.
const apiReads=createInflight();
export function clearSharedReads(prefix){apiReads.clear(prefix);}
export async function api(path,opts){
  if(shareableRead(opts)){
    const {ct,text}=await apiReads.share(String(path),()=>apiRequest(path,opts,true));
    return ct.includes('json')?JSON.parse(text):text;
  }
  const method=((opts&&opts.method)||'GET').toUpperCase();
  if(method==='GET'||method==='HEAD')return apiRequest(path,opts,false);
  const scope=mutationScope(path);
  apiReads.clear(scope);
  try{return await apiRequest(path,opts,false);}
  finally{apiReads.clear(scope);}
}
async function apiRequest(path,opts,asText){
  opts=Object.assign({},opts);
  // Remote (cookie-authed) sessions must carry an anti-CSRF header on mutations;
  // it is harmless on the loopback path. Safe methods (GET/HEAD) skip it.
  const method=(opts.method||'GET').toUpperCase();
  if(method!=='GET'&&method!=='HEAD'){
    opts.headers=Object.assign({'X-Interseptor-CSRF':'1'},opts.headers||{});
  }
  // Reads should fail loudly instead of hanging a panel on "loading" forever.
  if(method==='GET'&&!opts.signal&&typeof AbortSignal!=='undefined'&&AbortSignal.timeout){
    opts.signal=AbortSignal.timeout(30000);opts.defaultTimeout=true;
  }
  const {defaultTimeout,...init}=opts;
  let r;
  try{r=await fetch(path,init);}
  catch(e){throw apiFetchError(e,opts);}
  if(r.status===401){ // remote session expired / not signed in → go to login
    if(location.pathname!=='/login'){ location.href='/login'; }
    throw new Error('unauthorized');
  }
  if(!r.ok){const err=new Error(await apiErrorMessage(r));err.status=r.status;throw err;}
  const ct=r.headers.get('content-type')||'';
  if(asText)return {ct,text:await r.text()}; // each sharing caller parses its own copy
  return ct.includes('json')?r.json():r.text();
}

// apiBytes GETs a path as raw bytes (a Uint8Array). Text decoding would turn
// every invalid UTF-8 byte into U+FFFD, so byte views (Hex) must use this.
export async function apiBytes(path){
  const init={};
  if(typeof AbortSignal!=='undefined'&&AbortSignal.timeout)init.signal=AbortSignal.timeout(30000);
  let r;
  try{r=await fetch(path,init);}
  catch(e){throw apiFetchError(e,{...init,defaultTimeout:true});}
  if(r.status===401){if(location.pathname!=='/login')location.href='/login';throw new Error('unauthorized');}
  if(!r.ok){const err=new Error(await apiErrorMessage(r));err.status=r.status;throw err;}
  return new Uint8Array(await r.arrayBuffer());
}

/** apiTry wraps api(); on failure optionally toasts and returns null instead of throwing. */
export async function apiTry(path, opts, {toastOnError=true, label=''}={}){
  try{return await api(path, opts);}
  catch(e){if(toastOnError)toastError(label, e);return null;}
}

export const methodColor=m=>({GET:'var(--blue)',POST:'var(--accent)',PUT:'var(--amber)',PATCH:'var(--violet)',DELETE:'var(--red)'}[m]||'var(--fg2)');
export const statusColor=s=>!s?'var(--fg3)':s<300?'var(--accent)':s<400?'var(--blue)':s<500?'var(--amber)':'var(--red)';
export const statusText=s=>({200:'OK',201:'Created',204:'No Content',301:'Moved',302:'Found',304:'Not Modified',400:'Bad Request',401:'Unauthorized',403:'Forbidden',404:'Not Found',500:'Internal Server Error',502:'Bad Gateway'}[s]||'');
export const mimeLabel=m=>{if(!m)return'—';if(m.includes('json'))return'json';if(m.includes('html'))return'html';if(m.includes('javascript'))return'script';if(m.includes('css'))return'css';if(m.includes('font')||m.includes('woff'))return'font';if(m.includes('image'))return'image';return(m.split('/')[1]||m).split(';')[0];};
// mimeExt maps a Content-Type to a file extension for body downloads.
export function mimeExt(mime){
  if(!mime) return 'bin';
  const m=mime.toLowerCase().split(';')[0].trim();
  const map={
    'application/json':'json','text/html':'html','text/css':'css','text/plain':'txt',
    'application/javascript':'js','text/javascript':'js','application/x-javascript':'js',
    'application/xml':'xml','text/xml':'xml','application/xhtml+xml':'html',
    'application/pdf':'pdf','image/png':'png','image/jpeg':'jpg','image/gif':'gif',
    'image/webp':'webp','image/svg+xml':'svg','image/x-icon':'ico',
    'application/zip':'zip','application/gzip':'gz','application/x-gzip':'gz',
    'application/octet-stream':'bin','application/wasm':'wasm',
    'video/mp4':'mp4','video/webm':'webm','audio/mpeg':'mp3','audio/ogg':'ogg',
    'font/woff':'woff','font/woff2':'woff2',
  };
  if(map[m]) return map[m];
  if(m.startsWith('text/')){const sub=m.slice(5);return sub==='plain'||!sub?'txt':sub.split('+')[0];}
  if(m.startsWith('image/')||m.startsWith('audio/')||m.startsWith('video/')){
    return m.split('/')[1].split('+')[0].replace(/^x-/,'');
  }
  if(m.includes('json')) return 'json';
  if(m.includes('xml')) return 'xml';
  if(m.includes('html')) return 'html';
  return 'bin';
}
export function flowBodyDownloadName(flowId,side,mime){return `flow-${flowId}-${side}.${mimeExt(mime)}`;}
export function flowBodyDownloadHref(flowId,side){return `/api/flows/${flowId}/body?side=${side}`;}
export const fmtSize=n=>!n?'0 B':n<1024?n+' B':n<1048576?(n/1024).toFixed(n<10240?1:0)+' KB':(n/1048576).toFixed(1)+' MB';
// fmtBytes matches the backend/MCP byte-formatting convention exactly:
// < 1 KB → "N B";  < 1 MB → "N.N KB";  < 1 GB → "N.N MB";  else "N.N GB"
export const fmtBytes=n=>{if(!n||n<0)return '0 B';if(n<1024)return n+' B';if(n<1048576)return (n/1024).toFixed(1)+' KB';if(n<1073741824)return (n/1048576).toFixed(1)+' MB';return (n/1073741824).toFixed(1)+' GB';};
export const fmtTime=ms=>{const d=new Date(ms);return d.toLocaleTimeString('en-GB',{hour12:false});};
export const fmtDur=ms=>ms<1000?ms+' ms':(ms/1000).toFixed(ms<10000?2:1)+' s';

/* ---- flowStore: normalized flow list, shared by Proxy history and (future)
   panels that need id -> flow lookup alongside the display-ordered array.
   `order` is a plain array (the same array a caller keeps as e.g. state.flows —
   pass it in once, then always mutate through these helpers so byId stays in
   sync); `byId` is a Map for O(1) lookup during live SSE updates instead of an
   O(N) findIndex over `order`. Callers that only ever read the ordered list
   (map.js, tlsdiag.js, setup.js, app.js) can keep reading state.flows directly —
   this doesn't replace that array, it indexes it. ---- */
export function createFlowStore(order){
  return {order:order||[],byId:new Map()};
}
// loadFlowStore replaces the store's contents wholesale (a fresh /api/flows page
// load or a full reload) — order is reassigned so callers holding the old array
// reference must re-read store.order afterward (proxy.js does: state.flows=store.order).
export function loadFlowStore(store,flows){
  store.order=flows;
  store.byId.clear();
  for(const f of flows)store.byId.set(f.id,f);
}
// upsertFlow inserts a new flow at its id position in `order` (newest first, the
// live-tail sort) or merges onto the existing object in place (so any other
// reference held to it, e.g. state.detail, sees the update without a second
// lookup). Concurrent clients can deliver flows out of id order, so a flow is not
// simply prepended; the scan stops at the first older id, which is index 0 for
// the usual in-order arrival. Returns {flow, isNew} so callers can branch on
// whether this was an insert or a merge.
export function upsertFlow(store,f){
  const ex=store.byId.get(f.id);
  if(ex){Object.assign(ex,f);return {flow:ex,isNew:false};}
  let i=0;
  while(i<store.order.length&&store.order[i].id>f.id)i++;
  store.order.splice(i,0,f);
  store.byId.set(f.id,f);
  return {flow:f,isNew:true};
}
// appendFlows adds flows at the back of `order` (older-page pagination), skipping
// any id already present (a flow can arrive live between two page fetches).
export function appendFlows(store,flows){
  const added=[];
  for(const f of flows){
    if(store.byId.has(f.id))continue;
    store.order.push(f);
    store.byId.set(f.id,f);
    added.push(f);
  }
  return added;
}
// removeFlow deletes by id from both byId and order (O(N) on order — used only
// for the rare single-id case; bulk eviction uses dropFlowsFrom instead).
export function removeFlow(store,id){
  if(!store.byId.delete(id))return null;
  const i=store.order.findIndex(f=>f.id===id);
  const removed=i>=0?store.order.splice(i,1)[0]:null;
  return removed;
}
// dropFlowsFrom truncates `order` at index (splice-to-end) and removes the
// dropped ids from byId — used to cap in-memory live history at MAX_LIVE_FLOWS.
export function dropFlowsFrom(store,index){
  const dropped=store.order.splice(index);
  dropped.forEach(d=>store.byId.delete(d.id));
  return dropped;
}

/* ---- diffVisibleRows: keyed reconciliation plan for a windowed list.
   Pure function, no DOM: given the ids currently mounted (`prevIds`, in DOM
   order) and the ids the next render should mount (`nextIds`, in render order),
   it returns the minimal remove/insert plan that turns one into the other —
   so a caller can patch the two rows that actually changed instead of
   re-serializing and re-wiring the whole visible window.
   `reusable:false` means the two lists can't be reconciled by insert/remove
   alone (the kept ids appear in a different relative order, or an id repeats);
   the caller must fall back to a full re-render. Every `insert` carries the id
   of the surviving row it must be placed before (`before:null` = at the end),
   which is exactly what `anchor.before(el)` wants. ---- */
export function diffVisibleRows(prevIds,nextIds){
  const prev=prevIds||[],next=nextIds||[];
  const prevSet=new Set(prev),nextSet=new Set(next);
  const bail={reusable:false,remove:[],insert:[],keep:[]};
  if(prevSet.size!==prev.length||nextSet.size!==next.length)return bail; // duplicate ids aren't keyed rows
  const keptPrev=prev.filter(id=>nextSet.has(id));
  const keptNext=next.filter(id=>prevSet.has(id));
  if(keptPrev.length!==keptNext.length)return bail;
  for(let i=0;i<keptPrev.length;i++)if(keptPrev[i]!==keptNext[i])return bail; // survivors reordered
  const remove=prev.filter(id=>!nextSet.has(id));
  const insert=[];
  for(let i=0;i<next.length;i++){
    const id=next[i];
    if(prevSet.has(id))continue;
    let before=null;
    for(let j=i+1;j<next.length;j++){if(prevSet.has(next[j])){before=next[j];break;}}
    insert.push({id,before});
  }
  return {reusable:true,remove,insert,keep:keptNext};
}

/* ---- client-side scope evaluation ----
   A faithful JS mirror of internal/scope's Engine.InScope for the subset of
   rules that can be reproduced exactly: exact hosts, `*.base` wildcards, literal
   path prefixes, scheme and port. Regex host/path rules are deliberately NOT
   mirrored — Go's regexp is RE2 and JS's is not, so a regex rule marks the whole
   ruleset `evaluable:false` and the caller must keep asking the server.
   Used by proxy.js so a live flow arriving while "in scope" is on can be placed
   incrementally instead of forcing a full /api/flows reload per event. ---- */
const SCOPE_REGEX_LITERALS=['.*','^','$','(',')','[',']','|','+','?','\\d','\\w','\\s'];
// scopeLooksLikeRegex mirrors internal/scope's looksLikeRegex exactly.
export function scopeLooksLikeRegex(s){
  const v=String(s||'');
  if(v.length>=2&&v[0]==='/'&&v[v.length-1]==='/')return true;
  return SCOPE_REGEX_LITERALS.some(lit=>v.includes(lit));
}
// compileScopeRules mirrors Engine.SetRules: disabled rules are dropped, hosts
// compile to exact/wildcard, paths to a literal prefix. `evaluable` is false as
// soon as one enabled rule needs a regex (host or path) — the caller then treats
// scope as server-only, exactly as it did before this existed.
export function compileScopeRules(rules){
  const out=[];let hasInclude=false,evaluable=true;
  for(const r of rules||[]){
    if(!r||!r.enabled)continue;
    const include=r.action!=='exclude';
    if(include)hasInclude=true;
    const host=String(r.host||'').toLowerCase();
    let hostExact='',hostWild='';
    if(host){
      if(host.startsWith('*.'))hostWild=host.slice(2);
      else if(scopeLooksLikeRegex(host))evaluable=false;
      else hostExact=host;
    }
    const path=String(r.path||'');
    let pathExact='';
    if(path){
      if(scopeLooksLikeRegex(path))evaluable=false;
      else pathExact=path;
    }
    out.push({include,hostExact,hostWild,pathExact,scheme:String(r.scheme||''),port:Number(r.port)||0});
  }
  return {evaluable,hasInclude,rules:out};
}
function scopeHostMatches(r,host){
  if(!r.hostExact&&!r.hostWild)return true;
  const h=String(host||'').toLowerCase();
  if(r.hostWild)return h===r.hostWild||h.endsWith('.'+r.hostWild);
  return h===r.hostExact;
}
function scopeRuleMatches(r,f){
  return scopeHostMatches(r,f.host)&&
    (!r.pathExact||String(f.path||'').startsWith(r.pathExact))&&
    (!r.scheme||r.scheme.toLowerCase()===String(f.scheme||'').toLowerCase())&&
    (!r.port||r.port===Number(f.port||0));
}
// flowInScope mirrors Engine.InScope: an exclude match always wins; with no
// include rules everything is in scope; otherwise at least one include must match.
export function flowInScope(f,compiled){
  if(!compiled||!compiled.evaluable)return false;
  let included=false;
  for(const r of compiled.rules){
    if(!scopeRuleMatches(r,f))continue;
    if(r.include)included=true;
    else return false;
  }
  return compiled.hasInclude?included:true;
}

/* ---- density: --row-h and friends follow :root[data-density] (and the coarse
   pointer tier). Anything that measures rows listens for `densitychange`. ---- */
export const DENSITIES=['compact','default','comfortable'];
export function readRowHeightToken(fallback=30){
  const h=parseFloat(getComputedStyle(document.documentElement).getPropertyValue('--row-h'));
  return h>0?h:fallback;
}
export function getDensity(){
  const d=document.documentElement.getAttribute('data-density');
  return DENSITIES.includes(d)?d:'default';
}
function announceDensity(){window.dispatchEvent(new CustomEvent('densitychange',{detail:{density:getDensity(),rowHeight:readRowHeightToken()}}));}
// setDensity applies and persists the density tier, then announces it.
export function setDensity(value){
  const d=DENSITIES.includes(value)?value:'default';
  document.documentElement.setAttribute('data-density',d);
  try{localStorage.setItem('density',d);}catch(e){}
  announceDensity();
  return d;
}
try{window.matchMedia('(pointer:coarse)').addEventListener('change',announceDensity);}catch(e){}
let rowTokenCache=0;
const densityListeners=new Set();
// rowHeightToken is the cached --row-h value; it is re-read after `densitychange`.
export function rowHeightToken(){return rowTokenCache||(rowTokenCache=readRowHeightToken());}
export function onDensityChange(fn){densityListeners.add(fn);return()=>{densityListeners.delete(fn);};}
window.addEventListener('densitychange',()=>{
  rowTokenCache=0;
  densityListeners.forEach(fn=>{try{fn();}catch(e){}});
});

/* ---- extension registry: later modules register implementations that other
   panels call through core, so panels never import each other. `openFlow` is
   backed by the Flow Drawer; `renderFlowBody` by the shared flow body renderer. ---- */
const hooks=new Map();
const hookWaiters=new Map();
export function registerHook(name,fn){
  if(typeof fn!=='function')throw new Error('registerHook: '+name+' needs a function');
  hooks.set(name,fn);
  const waiting=hookWaiters.get(name);
  if(waiting){hookWaiters.delete(name);waiting.forEach(cb=>{try{cb(fn);}catch(e){}});}
  return ()=>{if(hooks.get(name)===fn)hooks.delete(name);};
}
export function getHook(name){return hooks.get(name)||null;}
// onHook(name, cb) runs cb(fn) as soon as the hook is registered, immediately
// when it already is. Optional modules load after the panels that use them, so
// a panel must not read a hook once at load and give up.
export function onHook(name,cb){
  const fn=hooks.get(name);
  if(fn){cb(fn);return;}
  if(!hookWaiters.has(name))hookWaiters.set(name,[]);
  hookWaiters.get(name).push(cb);
}
// openFlow(id, {source, tab}) opens a flow wherever the unified flow inspector is
// mounted; it returns false when no implementation is registered yet.
export function openFlow(id,opts){const h=hooks.get('openFlow');return h?h(id,opts||{}):false;}
// closeFlow({updateRoute}) closes the flow inspector; the symmetric wrapper for
// openFlow so callers never import the inspector module (and the old flowmodal
// shim is gone). Returns false when nothing was open or no implementation exists.
export function closeFlow(opts){const h=hooks.get('closeFlow');return h?h(opts||{}):false;}
// renderFlowBody(flow, tab) returns the shared body markup, or null before the
// renderer registers.
export function renderFlowBody(flow,tab){const h=hooks.get('renderFlowBody');return h?h(flow,tab):null;}

/* ---- createVirtualList: windowed-rendering helper for long rows-in-a-scroll-
   -div lists (Proxy history today; Map's table and Intruder's results are
   candidates for a later migration, not touched by this pass).
   Owns the scroll-binding + rAF-coalescing bookkeeping a caller would otherwise
   keep as module-level `virtScrollBound`/`scrollTick`/`virtActive` flags, and
   exposes `computeWindow(total)` — pure windowing math the caller uses inside
   its own render function (which still owns building/wiring row HTML; this
   helper only decides which indices are visible). ---- */
export function createVirtualList({container,itemHeight,threshold,buffer,onScroll}){
  let active=false,scrollTick=false,scrollBound=false;
  const rowH=()=>typeof itemHeight==='function'?itemHeight():itemHeight;
  function bindScroll(){
    if(scrollBound||!container)return;
    scrollBound=true;
    // rAF-throttled: below `threshold` the list is fully in the DOM so scrolling
    // needs no re-render at all; once virtualized, recompute the window at most
    // once per frame instead of on every scroll tick.
    container.addEventListener('scroll',()=>{
      if(!active||scrollTick)return;
      scrollTick=true;
      requestAnimationFrame(()=>{scrollTick=false;onScroll();});
    },{passive:true});
  }
  // computeWindow returns the visible slice bounds for `total` items, or null
  // when total is below `threshold` (caller should render the full list and
  // treat the list as non-virtualized — isActive() reflects this after the call).
  function computeWindow(total){
    bindScroll();
    if(total<threshold){active=false;return null;}
    active=true;
    const viewH=container.clientHeight||640,scrollTop=container.scrollTop||0,h=rowH();
    const start=Math.max(0,Math.floor(scrollTop/h)-buffer);
    const end=Math.min(total,start+Math.ceil(viewH/h)+2*buffer);
    return {start,end,topPad:start*h,bottomPad:(total-end)*h};
  }
  return {computeWindow,isActive:()=>active};
}

// createDensityVirtualList is createVirtualList whose rows are measured from the
// --row-h density token instead of a constant. When the density tier (or the
// coarse-pointer tier) changes, core dispatches `densitychange`: the cached token
// is dropped and an active (virtualized) list re-renders its window.
export function createDensityVirtualList(opts){
  const vl=createVirtualList({...opts,itemHeight:rowHeightToken});
  const dispose=onDensityChange(()=>{
    // A list whose container left the DOM unsubscribes itself.
    if(!opts.container||!opts.container.isConnected){dispose();return;}
    if(vl.isActive())opts.onScroll();
  });
  vl.dispose=dispose;
  return vl;
}

/* ---- createListView: the one windowed-list renderer with container-level event
   delegation, built on the pure computeVirtualWindow math above. It replaces the
   per-feature "el.innerHTML = items.map(rowHTML).join(''); then
   el.querySelectorAll(rowSel).forEach(n=>n.onclick=…)" pattern — the serialize-
   all-then-rebind-all shape that leaks listeners, pressures GC and left big lists
   (Intruder results, WebSocket frames, Scanner issues) unvirtualized.

   With a fixed `rowHeight` and more than `threshold` rows it mounts only the
   visible window inside a spacer (same spacer + translateY shape Intruder used,
   so its `.intr-virt-body` DOM and CSS are preserved when it passes those class
   names); below the threshold, or with no usable row height, it renders the whole
   list. Either way ONE delegated listener per wired event sits on the container
   and dispatches to the row via `e.target.closest(rowSelector)`, so rows carry no
   per-element handlers. Handlers receive `(rowElement, event)` and read the row's
   own dataset, exactly as the old per-row closures did.

   Options: {container, rowHeight (number|()=>number; 0/omitted ⇒ never
   virtualize), threshold=80, buffer=8, rowSelector, renderRow(item,index)->html,
   getItems()->array, on:{click,contextmenu,keydown,dblclick,…}, containerClass,
   bodyClass='vlist-body', spacerClass='vlist-spacer'}.
   Returns {render, refresh, isVirtual, destroy, container}. ---- */
export function createListView(opts){
  const {container,rowSelector,renderRow,getItems=()=>[],on={},
    threshold=80,buffer=8,containerClass='',bodyClass='vlist-body',spacerClass='vlist-spacer'}=opts;
  if(!container||typeof renderRow!=='function'||typeof rowSelector!=='string')
    throw new Error('createListView: needs {container, rowSelector, renderRow}');
  const rowH=()=>{const h=typeof opts.rowHeight==='function'?opts.rowHeight():opts.rowHeight;return h>0?h:0;};
  let lastWin=null,virtual=false,scrollBound=false,scrollTick=false,destroyed=false;
  const delegates=[];
  const items=()=>getItems()||[];
  function windowFor(list){
    return computeVirtualWindow({total:list.length,scrollTop:container.scrollTop,viewportH:container.clientHeight,rowH:rowH(),threshold,buffer});
  }
  function mountWindow(list,win){
    let sp=container.querySelector(':scope > .'+spacerClass);
    let body=container.querySelector(':scope > .'+bodyClass);
    if(!sp||!body){
      container.innerHTML='<div class="'+spacerClass+'" aria-hidden="true"></div><div class="'+bodyClass+'"></div>';
      sp=container.querySelector(':scope > .'+spacerClass);
      body=container.querySelector(':scope > .'+bodyClass);
    }
    sp.style.height=(list.length*rowH())+'px';
    body.style.transform='translateY('+win.topPad+'px)';
    body.innerHTML=list.slice(win.start,win.end).map((it,k)=>renderRow(it,win.start+k)).join('');
  }
  function bindScroll(){
    if(scrollBound)return;scrollBound=true;
    container.addEventListener('scroll',()=>{
      if(!virtual||scrollTick)return;
      scrollTick=true;
      requestAnimationFrame(()=>{scrollTick=false;if(!destroyed)refresh();});
    },{passive:true});
  }
  // render() does a full (re)paint from current items; refresh() only re-windows.
  function render(){
    const list=items();
    const win=windowFor(list);
    if(!win){
      virtual=false;
      if(containerClass)container.classList.remove(containerClass);
      container.innerHTML=list.map((it,i)=>renderRow(it,i)).join('');
      lastWin=null;
      return;
    }
    virtual=true;
    if(containerClass)container.classList.add(containerClass);
    bindScroll();
    mountWindow(list,win);
    lastWin=win;
  }
  function refresh(){
    if(!virtual){render();return;}
    const list=items();
    const win=windowFor(list);
    if(!win){render();return;}
    if(sameWindow(win,lastWin))return;
    mountWindow(list,win);
    lastWin=win;
  }
  for(const type of Object.keys(on)){
    const handler=on[type];
    if(typeof handler!=='function')continue;
    const listener=e=>{
      const row=e.target.closest&&e.target.closest(rowSelector);
      if(!row||!container.contains(row))return;
      handler(row,e);
    };
    container.addEventListener(type,listener);
    delegates.push([type,listener]);
  }
  return {
    render,refresh,container,
    isVirtual:()=>virtual,
    // suspend() lets the feature paint its own non-row state (empty, error) into
    // the container: it stops the scroll handler from re-rendering over that
    // markup until the next render().
    suspend(){virtual=false;lastWin=null;},
    destroy(){destroyed=true;for(const [type,listener] of delegates)container.removeEventListener(type,listener);delegates.length=0;},
  };
}

/* ---- createAutosave: shared debounced-save-with-status pattern.
   Modeled on notes.js's project-notebook autosave (the clearest, most
   self-contained of the three variants in this codebase — flow notes save on
   blur only, and findings.js's block-body save needs an extra id-snapshot to
   dodge a stale-finding-id race; both are good follow-up migrations once this
   shape is proven, but aren't touched in this pass).
   `save(value)` performs the actual PUT/PATCH and may throw/reject; `onStatus`
   is called with 'dirty' | 'saving' | 'saved' | '' (idle) exactly where
   notes.js's setNotesStatus() was called, so swapping in this helper doesn't
   change when the status indicator changes. `schedule(value)` compares against
   the last-saved value and no-ops (like notes.js bailing when
   v===notesState.loaded) instead of scheduling a redundant save. ---- */
export function createAutosave({delay=800,save,onStatus}={}){
  let timer=null,saving=null,lastSaved='',current='',shown='';
  const status=k=>{shown=k;if(onStatus)onStatus(k);};
  async function flush(opts){
    clearTimeout(timer);timer=null;
    if(saving)return saving;
    if(current===lastSaved)return;
    const run=(async()=>{
      let submitted=current;
      try{
        while(current!==lastSaved){
          submitted=current;
          status('saving');
          await save(submitted,opts);
          lastSaved=submitted;
          clearTimeout(timer);timer=null;
        }
        status('saved');
      }catch(e){
        status('dirty');
        if(current!==submitted&&!timer)timer=setTimeout(()=>{flush().catch(()=>{});},delay);
        throw e;
      }
    })();
    saving=run;
    try{return await run;}finally{if(saving===run)saving=null;}
  }
  function schedule(value){
    current=value;
    if(value===lastSaved){
      clearTimeout(timer);timer=null;
      // Edited back to the saved text: nothing is unsaved any more.
      if(!saving&&shown==='dirty')status('saved');
      return;
    }
    status('dirty');
    clearTimeout(timer);
    timer=setTimeout(()=>{flush().catch(()=>{});},delay);
  }
  // setBaseline marks `value` as already-saved (e.g. right after the initial
  // load fetch) without triggering a save or a status change.
  function setBaseline(value,{preserveCurrent=false}={}){lastSaved=value;if(!preserveCurrent)current=value;}
  return {schedule,flush,setBaseline,isDirty:()=>current!==lastSaved};
}

/* ---- flushOnClose: save pending edits when the window is closed, reloaded or
   hidden, instead of losing whatever was typed inside the debounce delay.
   `flush(opts)` is called with {keepalive:true}, which a save can pass to
   fetch so the request outlives the page. `isDirty()` gates the call. ---- */
export function flushOnClose({isDirty,flush}){
  const run=()=>{if(isDirty()){try{const p=flush({keepalive:true});if(p&&p.catch)p.catch(()=>{});}catch(e){}}};
  window.addEventListener('pagehide',run);
  window.addEventListener('beforeunload',run);
  document.addEventListener('visibilitychange',()=>{if(document.visibilityState==='hidden')run();});
  return run;
}

export const FLAG_DROPPED=4; // held request/response dropped by the operator or abandoned by its client (store.FlagDropped; the latter carries error "client disconnected while held")
// flowPending: the flow is still in flight (no status, no error, not dropped).
// A dropped flow never gets a status; older builds stored it without an error.
export function flowPending(f){
  return !f.status&&!f.error&&!(f.flags&FLAG_DROPPED);
}
export const FLAG_WS=32;
export const FLAG_TLS=16;
export const FLAG_AI=1024;
export const FLAG_COLLECTION=512; // sent from a collection request (store.FlagCollection)
export const FLAG_DISCOVERY=4096;
export const PRETTY_MAX=256*1024; // only beautify smallish bodies, to stay light
export const RENDER_CAP=2*1024*1024; // bodies larger than this aren't rendered (they lag the browser)

export function beautifyBody(body){
  const t=body.replace(/^﻿/,'').trim();
  if(t&&(t[0]==='{'||t[0]==='[')){try{return JSON.stringify(JSON.parse(t),null,2);}catch(e){}}
  if(t&&t[0]==='<'){try{
    let depth=0,out='';
    t.replace(/>\s*</g,'>\n<').split('\n').forEach(ln=>{
      ln=ln.trim();if(!ln)return;
      if(/^<\//.test(ln))depth=Math.max(0,depth-1);
      out+='  '.repeat(depth)+ln+'\n';
      if(/^<[^!?\/][^>]*[^\/]>$/.test(ln)&&!/^<(area|base|br|col|embed|hr|img|input|link|meta|param|source|track|wbr)\b/i.test(ln))depth++;
    });
    return out.replace(/\s+$/,'');
  }catch(e){}}
  return body;
}
export function prettify(raw){
  const i=raw.indexOf('\r\n\r\n');const head=i>=0?raw.slice(0,i):raw;const body=i>=0?raw.slice(i+4):'';
  if(!body)return raw;
  if(body.length>PRETTY_MAX)return head+'\n\n'+body+'\n\n— body too large to beautify ('+fmtSize(body.length)+'); showing raw —';
  return head+'\n\n'+beautifyBody(body);
}
// bodyHighlightKind picks a syntax highlighter from Content-Type and a quick sniff.
export function bodyHighlightKind(body,mime){
  const m=(mime||'').toLowerCase().split(';')[0].trim();
  const t=String(body).replace(/^\uFEFF/,'').replace(/^\s+/,'');
  if(m.includes('json')||t[0]==='{'||t[0]==='[') return 'json';
  if(m.includes('html')||m.includes('xml')||m.includes('svg')||/^<\?xml\b/i.test(t)||/^<!DOCTYPE/i.test(t)||(/^</.test(t)&&/>/.test(t))) return 'markup';
  if(m==='text/css') return 'css';
  return '';
}
// contentTypeFromRaw reads Content-Type from a raw HTTP message (req or res).
export function contentTypeFromRaw(raw){
  const s=String(raw).replace(/\r\n/g,'\n');
  const sep=s.indexOf('\n\n');
  const head=sep>=0?s.slice(0,sep):s;
  const m=head.match(/^content-type:\s*(\S.*?)(?:\s*;|\s*$)/im);
  return m?m[1].trim():'';
}
// Shared passive HTML preview for captured responses. Headers stay outside the
// document, and the empty sandbox grants no script or same-origin permissions.
export function renderHTMLResponse(raw){
  const separator=/\r?\n\r?\n/.exec(raw);
  const body=separator?raw.slice(separator.index+separator[0].length):'';
  return `<iframe class="http-render-frame" sandbox="" title="Rendered HTML" referrerpolicy="no-referrer" srcdoc="${escAttr(body)}"></iframe>`;
}
function highlightBody(body,pretty,mime){
  if(!pretty||body.length>PRETTY_MAX) return esc(body);
  const kind=bodyHighlightKind(body,mime);
  if(kind==='json') return highlightJSON(body);
  if(kind==='markup') return highlightMarkup(body);
  if(kind==='css') return highlightCSS(body);
  return esc(body);
}
// Colorize a read-only HTTP message: start line, header names/values, status code.
// The text is escaped first (these are arbitrary captured bytes) so the result is
// safe for innerHTML. Only the header block is tokenized; the body is escaped
// verbatim to stay fast on large payloads.
export function highlightHTTP(text,pretty,mime){
  if(!text)return '';
  text=String(text).replace(/\r\n/g,'\n');
  const sep=text.indexOf('\n\n');
  const head=sep>=0?text.slice(0,sep):text;
  const body=sep>=0?text.slice(sep+2):'';
  const lines=head.split('\n');
  let html=hlStartLine(lines[0]||'');
  for(let i=1;i<lines.length;i++){
    const ln=lines[i];const c=ln.indexOf(':');
    html+='\n'+(c>0
      ?'<span class="hl-hname">'+esc(ln.slice(0,c))+'</span>:<span class="hl-hval">'+esc(ln.slice(c+1))+'</span>'
      :esc(ln));
  }
  if(sep>=0) html+='\n\n'+highlightBody(body,pretty,mime||contentTypeFromRaw(text));
  return html;
}
// highlightHeaderLines colorizes bare "Name: value" header lines (no HTTP start
// line) for the editable Repeater request-headers overlay. Reuses the read-only
// header token classes so the editor matches the response pane.
export function highlightHeaderLines(text){
  return String(text).split('\n').map(ln=>{
    const c=ln.indexOf(':');
    if(c<=0)return esc(ln);
    return '<span class="hl-hname">'+esc(ln.slice(0,c))+'</span>:<span class="hl-hval">'+esc(ln.slice(c+1))+'</span>';
  }).join('\n');
}
// highlightBodyText colorizes a body by sniffed kind (JSON / markup / CSS),
// falling back to escaped plain text; a public wrapper over highlightBody with
// the PRETTY_MAX size guard applied (large bodies render escaped, not tokenized).
export function highlightBodyText(body,mime){return highlightBody(String(body),true,mime);}
// Color a (pretty-printed) JSON body: keys, string / number / literal values.
// Escapes first, then tokenizes the escaped text, so the result is safe for
// innerHTML even though the body is arbitrary captured bytes.
export function highlightJSON(s){
  return esc(s).replace(/("(?:\\.|[^"\\])*")(\s*:)?|\b(true|false|null)\b|(-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?)/g,(m,str,colon,lit,num)=>{
    if(str!==undefined)return colon!=null?'<span class="js-key">'+str+'</span>'+colon:'<span class="js-str">'+str+'</span>';
    if(lit!==undefined)return '<span class="js-lit">'+lit+'</span>';
    return '<span class="js-num">'+num+'</span>';
  });
}
// Color HTML / XML markup (tags, attributes, comments).
export function highlightMarkup(s){
  const e=esc(s);
  return e.replace(/&lt;!--[\s\S]*?--&gt;/g,m=>'<span class="hl-cmt">'+m+'</span>')
    .replace(/&lt;(\/?)([\w:.-]+)([\s\S]*?)&gt;/g,(m,slash,tag,rest)=>{
      const attrs=rest.replace(/(\s+)([\w:.-]+)(\s*=\s*)(&quot;[^&]*?&quot;|&#39;[^&]*?&#39;|[^\s&gt;]+)/g,
        '$1<span class="hl-attr">$2</span>$3<span class="hl-str">$4</span>');
      return '&lt;'+slash+'<span class="hl-tag">'+tag+'</span>'+attrs+'&gt;';
    });
}
// Color CSS (comments, at-rules, selectors, properties, values).
export function highlightCSS(s){
  const e=esc(s);
  return e.replace(/\/\*[\s\S]*?\*\//g,m=>'<span class="hl-cmt">'+m+'</span>')
    .replace(/@[\w-]+/g,m=>'<span class="hl-at">'+m+'</span>')
    .replace(/([\w#.: \[\]()>+~*,\\-]+)(\s*\{)/g,'<span class="hl-sel">$1</span>$2')
    .replace(/([\w-]+)(\s*:)/g,'<span class="hl-prop">$1</span>$2')
    .replace(/(:\s*)(&quot;[^&]*?&quot;|#[0-9a-fA-F]{3,8}|\b\d+(?:\.\d+)?(?:px|em|rem|%|s|ms|deg|vh|vw|ch|ex)?\b)/g,'$1<span class="hl-val">$2</span>');
}
export function hlStartLine(ln){
  let m=ln.match(/^(HTTP\/[\d.]+)\s+(\d{3})(.*)$/);            // response status line
  if(m)return '<span class="hl-proto">'+esc(m[1])+'</span> <span class="hl-st'+m[2][0]+'">'+esc(m[2]+m[3])+'</span>';
  m=ln.match(/^([A-Z]+)\s+(\S+)\s+(HTTP\/[\d.]+)$/);           // request line
  if(m)return '<span class="hl-method">'+esc(m[1])+'</span> <span class="hl-url">'+esc(m[2])+'</span> <span class="hl-proto">'+esc(m[3])+'</span>';
  return esc(ln);
}

/* ---- copy helpers ---- */
export function copyText(t,msg){
  if(navigator.clipboard&&navigator.clipboard.writeText){navigator.clipboard.writeText(t).then(()=>toast(msg||'copied')).catch(()=>fallbackCopy(t,msg));}
  else fallbackCopy(t,msg);
}
export function fallbackCopy(t,msg){const ta=document.createElement('textarea');ta.value=t;ta.style.position='fixed';ta.style.opacity='0';document.body.appendChild(ta);ta.select();try{document.execCommand('copy');toast(msg||'copied');}catch(e){toast('copy failed');}document.body.removeChild(ta);}

// Use the browser download flow without opening an app-requested native picker.
export async function saveFile(blob,suggestedName,mimeType){
  const name=suggestedName||'download';
  const type=mimeType||blob.type||'application/octet-stream';
  const url=URL.createObjectURL(blob instanceof Blob?blob:new Blob([blob],{type}));
  const a=document.createElement('a');
  a.href=url;a.download=name;a.click();
  URL.revokeObjectURL(url);
  return name;
}

const MAX_LIST_FILE = 16 * 1024 * 1024; // SecLists-scale wordlists
export const LIST_PREVIEW_LINES = 40; // max payload lines shown in Intruder textareas

// normalizeListText converts CRLF/CR to LF for pasted or loaded list files.
export function normalizeListText(text){
  return String(text||'').replace(/\r\n/g,'\n').replace(/\r/g,'\n');
}

// parseListLines splits a newline list into trimmed non-empty lines.
export function parseListLines(text, {ignoreComments=false}={}){
  return normalizeListText(text).split('\n').map(l=>l.trim()).filter(l=>l&&(!ignoreComments||!l.startsWith('#')));
}

// previewListLines returns a short textarea-safe preview of a payload list.
export function previewListLines(arr, max=LIST_PREVIEW_LINES){
  if(!arr||!arr.length) return {text:'',total:0,truncated:false};
  if(arr.length<=max) return {text:arr.join('\n'),total:arr.length,truncated:false};
  return {text:arr.slice(0,max).join('\n'),total:arr.length,truncated:true};
}

// applyTextList writes text into a textarea/input; append joins with a newline.
export function applyTextList(el, text, {append=false}={}){
  if(!el) return;
  const t=normalizeListText(text);
  if(append&&el.value.trim()) el.value=el.value.trimEnd()+'\n'+t;
  else el.value=t;
}

// pickTextFile opens a native file picker and reads the chosen file as UTF-8 text.
export function pickTextFile({accept='.txt,.lst,.csv,.words,text/plain',maxBytes=MAX_LIST_FILE}={}){
  return new Promise((resolve,reject)=>{
    const input=document.createElement('input');
    input.type='file';
    input.accept=accept;
    input.style.display='none';
    input.onchange=()=>{
      const file=input.files&&input.files[0];
      input.remove();
      if(!file){resolve(null);return;}
      if(file.size>maxBytes){
        reject(new Error('file too large ('+fmtSize(file.size)+' — max '+fmtSize(maxBytes)+')'));
        return;
      }
      const reader=new FileReader();
      reader.onload=()=>resolve({text:String(reader.result||''),name:file.name,size:file.size});
      reader.onerror=()=>reject(new Error('could not read file'));
      reader.readAsText(file);
    };
    document.body.appendChild(input);
    input.click();
  });
}

export function countListLines(text, ignoreComments=false){
  return parseListLines(text, {ignoreComments}).length;
}

/* ---- shared right-click context menu (#ctxmenu) ----
   One engine renders every right-click menu in the UI. A menu is sections of
   items; an item with `children` becomes a cascading flyout, so a surface with
   thirty actions is seven categorised roots instead of a flat list with its own
   scrollbar. The root panel is #ctxmenu; each open flyout is its own panel
   appended to <body> (the root scrolls, so a nested child would be clipped).
   See js/ctxmenu-model.js for the geometry, keyboard and menu-model rules. */
let ctxPanels=[];          // [{el, rows, sel, depth, parentRow}] root first
let ctxHoverTimer=null;
let ctxReturnFocus=null;
let ctxReturnTrigger=null;

// ctxMenuContains reports whether a node sits in the open menu — root panel or
// any flyout. Outside-click dismissal must use this, not #ctxmenu.contains().
export function ctxMenuContains(node){
  if(!node)return false;
  return ctxPanels.some(p=>p.el===node||p.el.contains(node));
}
export function ctxMenuOpen(){return ctxPanels.length>0;}
// ctxMenuTrigger is the element that opened the current menu, but only while
// the click that opened it is still being dispatched: a later click on the same
// element (a row that was right-clicked, a button pressed again) is a new click.
let ctxOpening=false;
export function ctxMenuTrigger(){return ctxOpening&&ctxPanels.length?ctxReturnTrigger:null;}

function ctxClearHoverTimer(){if(ctxHoverTimer){clearTimeout(ctxHoverTimer);ctxHoverTimer=null;}}

// ctxClosePanelsFrom tears down every panel at `depth` and deeper.
function ctxClosePanelsFrom(depth){
  for(let i=ctxPanels.length-1;i>=Math.max(1,depth);i--){
    const p=ctxPanels[i];
    if(p.parentRow){p.parentRow.setAttribute('aria-expanded','false');p.parentRow.classList.remove('ctx-open');}
    p.el.remove();
    ctxPanels.pop();
  }
}

export function hideCtxMenu({restoreFocus=false}={}){
  const ctx=$('#ctxmenu');if(!ctx)return;
  const wasOpen=ctx.classList.contains('show');
  ctxClearHoverTimer();
  ctxClosePanelsFrom(1);
  ctxPanels=[];
  ctx.classList.remove('show');ctx._acts=null;
  ctx.removeAttribute('data-motion-side');
  if(ctxReturnTrigger?.isConnected)ctxReturnTrigger.setAttribute('aria-expanded','false');
  if(restoreFocus&&wasOpen&&ctxReturnFocus?.isConnected&&typeof ctxReturnFocus.focus==='function'){
    ctxReturnFocus?.focus({preventScroll:true});
  }
  ctxReturnFocus=null;ctxReturnTrigger=null;
}

// ctxItemHTML renders one row. Labels are escaped, so markup cannot be smuggled
// through it.label — the icon has to be its own field (it.icon names a sprite
// symbol). A row with children gets the chevron and the menu ARIA that says so.
function ctxItemHTML(it,i){
  const kids=hasChildren(it);
  const dStyle=it.danger?' style="color:var(--red)"':'';
  const val=it.val!=null&&it.val!==''?String(it.val):(it.hint||'');
  const right=val?`<span class="mono ctx-value"${dStyle}>${esc(val)}</span>`:'';
  const glyph=it.icon?icon(it.icon):'';
  const arrow=kids?`<span class="ctx-arrow" aria-hidden="true">${icon('chevron')}</span>`:'';
  const cls=['ctx-item'];
  if(right)cls.push('ctx-has-value');
  if(kids)cls.push('ctx-has-sub');
  // .on is the keyboard/hover highlight (ctxPaintSel); the checked item keeps its own class.
  if(it.on)cls.push('ctx-checked');
  if(it.disabled)cls.push('ctx-disabled');
  const aria=kids?' aria-haspopup="menu" aria-expanded="false"':'';
  const dis=it.disabled?' aria-disabled="true"':'';
  const checked=it.on?' aria-current="true"':'';
  return `<div class="${cls.join(' ')}" role="menuitem" tabindex="-1" data-i="${i}"${aria}${dis}${checked}${it.danger&&!right?dStyle:''}><span class="lbl"${dStyle}>${glyph}<span class="ctx-label-text">${esc(it.label)}</span></span>${right}${arrow}</div>`;
}

// ctxPaintPanel fills a panel element and returns its focusable rows, each
// carrying the item it renders (`row._item`).
function ctxPaintPanel(el,sections){
  let html='';let i=0;
  for(const sec of sections){
    if(sec.head)html+=`<div class="ctx-head">${esc(sec.head)}</div>`;
    for(const it of sec.items){
      if(it.sep){html+='<div class="ctx-sep"></div>';continue;}
      html+=ctxItemHTML(it,i++);
    }
  }
  el.innerHTML=html;
  const items=menuRows(sections);
  const rows=Array.from(el.querySelectorAll('.ctx-item'));
  rows.forEach((row,n)=>{row._item=items[n];});
  return rows;
}

function ctxViewport(){
  const vv=window.visualViewport;
  const v={width:vv?.width||innerWidth,height:vv?.height||innerHeight,left:vv?.offsetLeft||0,top:vv?.offsetTop||0};
  // The desktop app's title bar (search, project, status) stays visible: a tall
  // menu flipped above its anchor must not cover it.
  const de=document.documentElement;
  const bar=de.classList.contains('desktop-app')?parseFloat(getComputedStyle(de).getPropertyValue('--titlebar-h'))||0:0;
  if(bar>0&&v.top<bar){v.height-=bar-v.top;v.top=bar;}
  return v;
}

// ctxPaintSel highlights the current row of one panel. Focus moves only when
// asked, so hovering does not steal it from a keyboard walk.
function ctxPaintSel(depth,moveFocus=false){
  const p=ctxPanels[depth];if(!p)return;
  p.rows.forEach((el,i)=>{const on=i===p.sel;el.classList.toggle('on',on);el.tabIndex=on?0:-1;});
  const cur=p.rows[p.sel];
  if(cur){cur.scrollIntoView({block:'nearest'});if(moveFocus)cur.focus({preventScroll:true});}
}

// ctxOpenSubmenu renders the flyout for row `idx` of panel `depth`, beside its
// parent and flipped to the left when the right edge is too close.
function ctxOpenSubmenu(depth,idx,{focus=false}={}){
  const parent=ctxPanels[depth];if(!parent)return;
  const row=parent.rows[idx];const it=row&&row._item;
  if(!it||it.disabled||!hasChildren(it))return;
  const open=ctxPanels[depth+1];
  if(open&&open.parentRow===row){if(focus)ctxFocusRow(depth+1,0);return;}
  ctxClosePanelsFrom(depth+1);
  const sections=childSections(it);
  if(!sections.length)return;
  const el=document.createElement('div');
  el.className='ctx-submenu';
  el.setAttribute('role','menu');
  el.setAttribute('aria-label',it.label||'Submenu');
  document.body.appendChild(el);
  const rows=ctxPaintPanel(el,sections);
  // Measure at natural width before placing, exactly as the root does.
  el.style.left='0px';el.style.top='0px';el.style.width='max-content';el.style.maxHeight='none';
  el.classList.add('show');
  const box=el.getBoundingClientRect();
  const place=submenuPlacement({parent:parent.el.getBoundingClientRect(),item:row.getBoundingClientRect(),width:box.width,height:box.height,viewport:ctxViewport()});
  el.style.left=place.left+'px';el.style.top=place.top+'px';el.style.width=place.width+'px';el.style.maxHeight=place.maxHeight+'px';
  el.dataset.ctxSide=place.side;
  row.setAttribute('aria-expanded','true');row.classList.add('ctx-open');
  ctxPanels.push({el,rows,sel:0,depth:depth+1,parentRow:row});
  // A flyout is a fresh element every time it opens, so it is wired here; the
  // root is wired once because it is the one panel that outlives a menu.
  ctxWirePanel(depth+1,el);
  ctxPaintSel(depth+1,focus);
}

function ctxFocusRow(depth,idx){
  const p=ctxPanels[depth];if(!p||!p.rows.length)return;
  p.sel=Math.max(0,Math.min(p.rows.length-1,idx));
  ctxPaintSel(depth,true);
}

// ctxRunRow executes a row's action. The menu closes first so the action can
// open its own modal or menu without being torn down by the dismissal.
function ctxRunRow(depth,idx){
  const p=ctxPanels[depth];if(!p)return;
  const row=p.rows[idx];const it=row&&row._item;
  if(!it||it.disabled)return;
  if(hasChildren(it)){ctxOpenSubmenu(depth,idx,{focus:true});return;}
  const fn=it.act;
  hideCtxMenu({restoreFocus:true});
  if(fn)fn();
}

function ctxRowIndex(depth,target){
  const p=ctxPanels[depth];if(!p)return -1;
  const row=target&&target.closest?target.closest('.ctx-item'):null;
  return row?p.rows.indexOf(row):-1;
}

// ctxWirePanel gives a panel its pointer behaviour: click runs or opens, and a
// 120 ms rest opens a flyout (or closes a stale one) so a diagonal sweep towards
// an open flyout never swaps it out from under the pointer.
function ctxWirePanel(depth,el){
  el.addEventListener('click',e=>{
    const i=ctxRowIndex(depth,e.target);
    if(i<0)return;
    e.preventDefault();e.stopPropagation();
    ctxRunRow(depth,i);
  });
  el.addEventListener('pointermove',e=>{
    const i=ctxRowIndex(depth,e.target);
    if(i<0)return;
    const p=ctxPanels[depth];if(!p||p.sel===i&&ctxHoverTimer)return;
    p.sel=i;ctxPaintSel(depth);
    ctxClearHoverTimer();
    const row=p.rows[i];const it=row&&row._item;
    ctxHoverTimer=setTimeout(()=>{
      ctxHoverTimer=null;
      if(!ctxPanels[depth]||ctxPanels[depth].rows[i]!==row)return;
      if(it&&hasChildren(it)&&!it.disabled)ctxOpenSubmenu(depth,i);
      else ctxClosePanelsFrom(depth+1);
    },SUBMENU_HOVER_MS);
  });
}

// ctxKeyHandler is registered once, at module load, so it runs before the
// feature modules' own Escape handlers: ArrowLeft/Escape inside a flyout must
// close that one level, not the whole menu.
function ctxKeyHandler(e){
  if(!ctxPanels.length)return;
  if(e.defaultPrevented)return;
  const depth=ctxPanels.length-1;
  const p=ctxPanels[depth];
  const row=p.rows[p.sel];const it=row&&row._item;
  const action=menuKeyAction(e.key,{children:hasChildren(it)&&!it?.disabled,depth});
  if(!action)return;
  // Tab keeps its meaning: focus leaves the menu, so the menu closes with it.
  if(e.key==='Tab'){hideCtxMenu();return;}
  e.preventDefault();e.stopImmediatePropagation();
  switch(action){
    case 'next':case 'prev':case 'first':case 'last':
      p.sel=nextIndex(p.sel,p.rows.length,action);
      ctxClosePanelsFrom(depth+1);
      ctxPaintSel(depth,true);
      break;
    case 'open':ctxOpenSubmenu(depth,p.sel,{focus:true});break;
    case 'close':
      ctxClosePanelsFrom(depth);
      if(ctxPanels.length)ctxPaintSel(ctxPanels.length-1,true);
      break;
    case 'activate':ctxRunRow(depth,p.sel);break;
    case 'dismiss':hideCtxMenu({restoreFocus:true});break;
  }
}
document.addEventListener('keydown',ctxKeyHandler);

// openCtxMenu renders sectioned items on #ctxmenu and positions at (x,y).
// An item may carry `children` (a flat item list, or [{head,items}] groups) to
// become a cascading flyout instead of another root-level row.
export function openCtxMenu(x,y,sections,trigger=null){
  const ctx=$('#ctxmenu');if(!ctx)return;
  closeAllUiSelects(); // ctx/Views menus and ui-select dropdowns are one mutually-exclusive group
  hideCtxMenu();
  const secs=normalizeSections(sections);
  if(!secs.length)return;
  ctxReturnFocus=trigger||document.activeElement;
  ctxReturnTrigger=trigger||null;
  ctxOpening=true;setTimeout(()=>{ctxOpening=false;},0);
  if(trigger){trigger.setAttribute('aria-haspopup','menu');trigger.setAttribute('aria-expanded','true');}
  ctx.setAttribute('role','menu');
  const rows=ctxPaintPanel(ctx,secs);
  if(!rows.length)return;
  if(!ctx._ctxWired){ctxWirePanel(0,ctx);ctx._ctxWired=true;}
  // Measure this menu afresh: the previous menu's constrained width must not
  // squeeze the next menu, and wrapping must settle before height placement.
  ctx.style.left='0px';ctx.style.top='0px';ctx.style.width='max-content';ctx.style.maxHeight='none';ctx.classList.add('show');
  const anchor=trigger?.getBoundingClientRect?.()||{left:x,top:y,right:x,bottom:y};
  const viewport=ctxViewport();
  ctx.style.width=Math.min(ctx.getBoundingClientRect().width,Math.max(0,viewport.width-16))+'px';
  const size=ctx.getBoundingClientRect();
  const placement=placeFloatingSurface(anchor,size.width,size.height,viewport);
  ctx.style.left=placement.left+'px';ctx.style.top=placement.top+'px';ctx.style.width=placement.width+'px';ctx.style.maxHeight=placement.maxHeight+'px';
  ctxPanels=[{el:ctx,rows,sel:0,depth:0,parentRow:null}];
  ctxPaintSel(0);
  if(rows[0])rows[0].focus();
  ctx.dataset.motionSide=placement.side;
}

/* ---- universal right-click dispatcher ----
   The native menu is suppressed app-wide, which takes Cut/Copy/Paste away from
   every text field and leaves the views without a handler dead. This dispatcher
   closes both holes: a feature handler that already rendered a menu calls
   preventDefault() and wins; everything else lands on a text-field, selection or
   workspace menu. Registered on document in the bubble phase, after load. */
const ctxWorkspaceBuilders=[];
// registerWorkspaceCtxSection lets a feature module contribute roots to the
// fallback workspace menu without core.js importing it (which would cycle).
// build(info) returns a section, an array of sections, or nothing.
export function registerWorkspaceCtxSection(build){if(typeof build==='function')ctxWorkspaceBuilders.push(build);}

const TEXT_INPUT_TYPES=new Set(['','text','search','url','tel','password','email','number']);
// isTextField: an element whose own text the clipboard bridge can act on.
export function isTextField(el){
  if(!el||el.disabled)return false;
  const tag=(el.tagName||'').toLowerCase();
  if(tag==='textarea')return true;
  if(tag==='input')return TEXT_INPUT_TYPES.has((el.getAttribute('type')||'').toLowerCase());
  return !!el.isContentEditable;
}

function fieldValue(el){return el.isContentEditable?(el.innerText||''):String(el.value==null?'':el.value);}
function fieldSelection(el){
  if(el.isContentEditable){const s=window.getSelection?.();return s&&!s.isCollapsed&&el.contains(s.anchorNode)?String(s):'';}
  const a=el.selectionStart,b=el.selectionEnd;
  return a==null||b==null||a===b?'':fieldValue(el).slice(a,b);
}
function fieldEditable(el){return !el.readOnly&&!el.disabled&&(el.isContentEditable||'value'in el);}

// replaceFieldSelection writes `text` over the selection (or at the caret) and
// notifies the owning module through the same `input` event typing would fire.
function replaceFieldSelection(el,text){
  if(el.isContentEditable){
    const s=window.getSelection?.();
    if(s&&s.rangeCount&&el.contains(s.anchorNode)){
      const r=s.getRangeAt(0);r.deleteContents();r.insertNode(document.createTextNode(text));
      s.collapseToEnd();
    }else el.append(document.createTextNode(text));
  }else{
    const a=el.selectionStart??el.value.length,b=el.selectionEnd??el.value.length;
    el.value=el.value.slice(0,a)+text+el.value.slice(b);
    el.selectionStart=a;el.selectionEnd=a+text.length;
  }
  el.dispatchEvent(new Event('input',{bubbles:true}));
}

async function clipboardWrite(text){
  if(!text)return false;
  try{await navigator.clipboard.writeText(text);return true;}
  catch{toast('Clipboard write was blocked by the browser','error');return false;}
}
async function clipboardRead(){
  try{return await navigator.clipboard.readText();}
  catch{toast('Clipboard read was blocked — use the keyboard paste shortcut','error');return null;}
}

// runCodecOp transforms the selection (or the whole field) through /api/decode,
// the same endpoint the Decoder and Repeater encode menus use.
async function runCodecOp(el,op,label){
  const whole=!fieldSelection(el);
  const input=whole?fieldValue(el):fieldSelection(el);
  if(!input){toast('nothing to '+label.toLowerCase());return;}
  const before=fieldValue(el);
  try{
    const r=await api('/api/decode',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({op,input})});
    if(r.error){toast(r.error);return;}
    if(!el.isConnected||fieldValue(el)!==before)return; // the field moved on; discard
    if(whole){
      if(el.isContentEditable)el.textContent=r.output;
      else el.value=r.output;
      el.dispatchEvent(new Event('input',{bubbles:true}));
    }else replaceFieldSelection(el,r.output);
  }catch(e){toastError(label+' failed',e);}
}

function insertSnippet(el,id){
  const spec=INSERT_SNIPPETS.find(s=>s.id===id);
  if(!spec)return;
  let text=spec.value;
  if(id==='marker')text=PAYLOAD_MARKER;
  else if(id==='uuid')text=randomUUID();
  else if(id==='timestamp')text=new Date().toISOString();
  replaceFieldSelection(el,text||'');
}
// randomUUID: crypto.randomUUID needs a secure context, so fall back to the
// same v4 shape built from crypto.getRandomValues (never Math.random).
function randomUUID(){
  if(crypto.randomUUID)return crypto.randomUUID();
  const b=crypto.getRandomValues(new Uint8Array(16));
  b[6]=(b[6]&0x0f)|0x40;b[8]=(b[8]&0x3f)|0x80;
  const h=[...b].map(v=>v.toString(16).padStart(2,'0')).join('');
  return `${h.slice(0,8)}-${h.slice(8,12)}-${h.slice(12,16)}-${h.slice(16,20)}-${h.slice(20)}`;
}

// ctxDecoderOpeners lets scanner.js hand core.js the Decoder drawer without an
// import cycle; the menu item is hidden until something registers one.
let ctxDecoderOpener=null;
export function registerCtxDecoder(open){if(typeof open==='function')ctxDecoderOpener=open;}
let ctxHistorySearch=null;
export function registerCtxHistorySearch(search){if(typeof search==='function')ctxHistorySearch=search;}

// bindTextFieldActions turns the declarative model's action ids into real work.
function bindTextFieldActions(sections,el){
  const run=(action)=>{
    if(action==='cut')return async()=>{const s=fieldSelection(el);if(await clipboardWrite(s))replaceFieldSelection(el,'');};
    if(action==='copy')return()=>{clipboardWrite(fieldSelection(el));};
    if(action==='paste')return async()=>{const t=await clipboardRead();if(t!=null)replaceFieldSelection(el,t);};
    if(action==='select-all')return()=>{el.focus();if(el.isContentEditable)document.getSelection()?.selectAllChildren(el);else el.select?.();};
    if(action==='clear')return()=>{if(el.isContentEditable)el.textContent='';else el.value='';el.dispatchEvent(new Event('input',{bubbles:true}));el.focus();};
    if(action==='decoder')return()=>{const t=fieldSelection(el)||fieldValue(el);if(ctxDecoderOpener)ctxDecoderOpener(t);else copyText(t,'copied — open Decoder from Scanner');};
    if(action==='search')return()=>{const t=fieldSelection(el)||fieldValue(el);if(ctxHistorySearch)ctxHistorySearch(t);};
    if(action&&action.startsWith('codec:')){const op=action.slice(6);const c=CODEC_OPS.find(x=>x.op===op);return()=>runCodecOp(el,op,c?c.label:'Transform');}
    if(action&&action.startsWith('insert:'))return()=>insertSnippet(el,action.slice(7));
    return null;
  };
  const walk=(items)=>items.map(it=>{
    if(it.sep)return it;
    const out={...it};
    if(hasChildren(it))out.children=walk(it.children.filter(Boolean));
    else if(it.action)out.act=run(it.action);
    return out;
  });
  return sections.map(sec=>({head:sec.head,items:walk(sec.items||[])}));
}

// openTextFieldCtxMenu is the clipboard bridge: it gives a text field back the
// Cut/Copy/Paste/Select-all the suppressed native menu took away, plus the
// encoders and inserts a request editor actually needs.
export function openTextFieldCtxMenu(e,el,opts={}){
  const sel=fieldSelection(el);
  const model=textFieldMenuModel({
    selection:sel,
    editable:fieldEditable(el),
    empty:!fieldValue(el),
    payloadMarker:!!opts.payloadMarker,
    clipboardRead:typeof navigator!=='undefined'&&!!navigator.clipboard?.readText,
  });
  const sections=bindTextFieldActions(model,el);
  if(sel)sections[0]={...sections[0],head:'SELECTION · '+shortLabel(sel,28)};
  const extra=opts.sections||[];
  openCtxMenu(e.clientX,e.clientY,[...extra,...sections]);
}

// openSelectionCtxMenu handles a live text selection outside a text field
// (highlighted markdown, a rendered body, a table cell).
function openSelectionCtxMenu(e,text){
  const run=(action)=>{
    if(action==='copy')return()=>copyText(text,'copied');
    if(action==='decoder')return()=>{if(ctxDecoderOpener)ctxDecoderOpener(text);else copyText(text,'copied');};
    if(action==='search')return()=>{if(ctxHistorySearch)ctxHistorySearch(text);};
    if(action&&action.startsWith('codec:'))return()=>copyDecoded(text,action.slice(6));
    return null;
  };
  const walk=(items)=>items.map(it=>{
    const out={...it};
    if(hasChildren(it))out.children=walk(it.children.filter(Boolean));
    else if(it.action)out.act=run(it.action);
    return out;
  });
  const model=selectionMenuModel({selection:text}).map(sec=>({head:sec.head+' · '+shortLabel(text,28),items:walk(sec.items)}));
  openCtxMenu(e.clientX,e.clientY,model);
}
// copyDecoded transforms read-only text through /api/decode and copies the
// result: a rendered pane is not editable, so the transform can only be handed
// back as a value. Exported because the inspector panes need the same behaviour.
export async function copyDecoded(text,op){
  try{
    const r=await api('/api/decode',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({op,input:text})});
    if(r.error){toast(r.error);return;}
    copyText(r.output,'transformed and copied');
  }catch(e){toastError('Transform failed',e);}
}

// openWorkspaceCtxMenu is the last resort: right-click on empty canvas, a feed,
// a list with no menu of its own. It is never empty, so no view is a dead zone.
function openWorkspaceCtxMenu(e){
  const info={target:e.target};
  const sections=[];
  for(const build of ctxWorkspaceBuilders){
    let out=null;
    try{out=build(info);}catch{out=null;}
    if(!out)continue;
    if(Array.isArray(out))sections.push(...out);else sections.push(out);
  }
  if(!sections.length)return;
  openCtxMenu(e.clientX,e.clientY,sections);
}

function documentSelectionText(){
  const s=window.getSelection?.();
  if(!s||s.isCollapsed)return '';
  const t=String(s);
  return t.trim()?t:'';
}

document.addEventListener('contextmenu',e=>{
  // A feature handler that rendered its own menu already called preventDefault.
  if(e.defaultPrevented)return;
  const t=e.target instanceof Element?e.target:null;
  const field=t&&t.closest('input,textarea,[contenteditable=""],[contenteditable="true"]');
  if(field&&isTextField(field)){e.preventDefault();openTextFieldCtxMenu(e,field);return;}
  const sel=documentSelectionText();
  if(sel){e.preventDefault();openSelectionCtxMenu(e,sel);return;}
  e.preventDefault();
  openWorkspaceCtxMenu(e);
});

// formatHexDump (hexdump.js) is a canonical hexdump: offset, hex bytes, ASCII text.
export { formatHexDump };
// mountHexDump fills host with a hexdump that fits its width: 16 bytes per line
// when that leaves the ASCII column visible, 8 when the pane is too narrow (a
// 16-byte line is ~78 characters). It re-fits when the pane is resized.
export function mountHexDump(host,raw){
  if(host._hexRO){host._hexRO.disconnect();host._hexRO=null;}
  host.innerHTML='<pre class="hex-dump"></pre>';
  const pre=host.firstChild;
  let canvas=null,lineW=0;
  const paint=()=>{
    const cs=getComputedStyle(pre);
    canvas=canvas||document.createElement('canvas').getContext('2d');
    canvas.font=cs.fontStyle+' '+cs.fontWeight+' '+cs.fontSize+' '+cs.fontFamily;
    const avail=pre.clientWidth-(parseFloat(cs.paddingLeft)||0)-(parseFloat(cs.paddingRight)||0);
    const w=hexLineFor(avail,canvas.measureText('0').width);
    if(w===lineW)return;
    lineW=w;
    pre.textContent=formatHexDump(raw,32768,w);
  };
  paint();
  if(typeof ResizeObserver==='function'){host._hexRO=new ResizeObserver(()=>{if(pre.isConnected)paint();});host._hexRO.observe(host);}
}

export const DEC_OPS=[['base64decode','Base64 ↓'],['base64encode','Base64 ↑'],['urldecode','URL ↓'],['urlencode','URL ↑'],['hexdecode','Hex ↓'],['hexencode','Hex ↑'],['htmldecode','HTML ↓'],['htmlencode','HTML ↑'],['jwtdecode','JWT'],['smart','Smart']];

// selectionFromRaw maps a DOM selection to the underlying source text when the
// view is pretty-printed/highlighted (span boundaries can split tokens).
export function selectionFromRaw(viewEl, sel){
  if(!sel||!viewEl?._rawText)return sel;
  const raw=viewEl._rawText;
  if(raw.includes(sel))return sel;
  const compact=sel.replace(/\s+/g,'');
  if(compact.length>=4){
    const re=new RegExp(compact.split('').map(c=>c.replace(/[.*+?^${}()|[\]\\]/g,'\\$&')).join('\\s*'),'i');
    const m=raw.match(re);
    if(m)return m[0];
  }
  const off=selectionOffset(viewEl);
  if(off>=0&&off+sel.length<=raw.length){
    const slice=raw.slice(off,off+sel.length);
    if(slice.replace(/\s+/g,'')===compact)return slice;
  }
  return sel;
}

function selectionOffset(root){
  const s=window.getSelection&&window.getSelection();
  if(!s||!s.rangeCount||!root.contains(s.anchorNode))return -1;
  const range=s.getRangeAt(0);
  const pre=document.createRange();
  pre.selectNodeContents(root);
  pre.setEnd(range.startContainer,range.startOffset);
  return pre.toString().length;
}

export function selectionWithin(el){
  const s=window.getSelection&&window.getSelection();
  if(!s||!s.rangeCount)return '';
  if(el&&s.anchorNode&&!el.contains(s.anchorNode))return '';
  let t=String(s);
  if(!t)t=s.getRangeAt(0).toString();
  return t.trim();
}

export function encodeKindLabel(s){
  const t=s.trim();
  if(/^[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]*$/.test(t))return 'JWT';
  if(t.includes('%'))return 'URL';
  if(t.length>=4&&t.length%2===0&&/^[0-9a-fA-F]+$/.test(t))return 'Hex';
  if(t.length>=8&&/^[A-Za-z0-9+/=_-]+$/.test(t))return 'Base64';
  return 'Decoded';
}

// The 4 call sites (Proxy req/res, flow-modal req/res) each own an empty
// `<div class="sel-decode" id="…" hidden></div>` placeholder; this renders its
// shared inner markup once here instead of hand-duplicating it at every call site.
const SEL_DECODE_INNER_HTML=`<span class="sel-decode-kind">Base64</span><span class="sel-decode-arrow">→</span>
  <code class="sel-decode-out"></code>
  <button type="button" class="btn sel-decode-copy" title="Copy decoded" aria-label="Copy decoded value">${icon('copy')}</button>
  <button type="button" class="btn sel-decode-open" title="Open in Decoder">Decoder</button>
  <button type="button" class="btn sel-decode-close" title="Dismiss" aria-label="Dismiss decoded value">${icon('close')}</button>`;
// wireSelectionDecode shows a slim decode strip when highlighted text looks encoded
// (built-in smart) or matches a project message codec (when getContext provides flowId).
export function wireSelectionDecode(viewEl, barEl, {onDecoder, getContext}={}){
  if(!viewEl||!barEl)return;
  barEl.innerHTML=SEL_DECODE_INNER_HTML;
  let timer=null,lastSel='',req=0;
  const hide=()=>{barEl.hidden=true;lastSel='';};
  const run=async()=>{
    const sel=selectionFromRaw(viewEl,selectionWithin(viewEl));
    if(!sel||sel.length<4||sel.length>8192){hide();return;}
    if(sel===lastSel)return;
    const id=++req;
    try{
      const ctx=typeof getContext==='function'?getContext():null;
      const body={input:sel};
      if(ctx&&ctx.flowId){body.flowId=ctx.flowId;if(ctx.side)body.side=ctx.side;}
      const r=await api('/api/selection-decode',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify(body)});
      if(id!==req)return;
      if(!r.matched||!r.output||r.output===sel){hide();return;}
      lastSel=sel;
      barEl.hidden=false;
      barEl.querySelector('.sel-decode-kind').textContent=r.kind||encodeKindLabel(sel);
      const outEl=barEl.querySelector('.sel-decode-out');
      const full=r.output;
      outEl.textContent=full.length>500?full.slice(0,500)+'…':full;
      outEl.title=full.length>500?full:(r.note||'');
      barEl._full=full;barEl._input=sel;
    }catch(e){if(id===req)hide();}
  };
  const schedule=()=>{clearTimeout(timer);timer=setTimeout(run,200);};
  viewEl.addEventListener('mouseup',schedule);
  viewEl.addEventListener('keyup',schedule);
  document.addEventListener('selectionchange',()=>{
    const s=window.getSelection();
    if(!s||!s.anchorNode||!viewEl.contains(s.anchorNode)){if(lastSel)hide();return;}
    schedule();
  });
  barEl.querySelector('.sel-decode-copy')?.addEventListener('click',()=>copyText(barEl._full||'','decoded copied'));
  barEl.querySelector('.sel-decode-open')?.addEventListener('click',()=>{if(onDecoder){const r=onDecoder(barEl._input||'');if(r&&typeof r.then==='function')r.catch(()=>{});}});
  barEl.querySelector('.sel-decode-close')?.addEventListener('click',hide);
  return hide;
}

/* ---- authoritative modal registry + focus stack ---- */
export const FOCUSABLE='a[href],button,input,select,textarea,[contenteditable="true"],[tabindex]:not([tabindex="-1"])';
export const MODAL_IDS=['flowModal','shortcutsModal','checksModal','codecsModal','oobModal','projModal','authzModal','findGuideModal','findCreateModal','findPickModal','findFlowPickModal','findDeletedModal','sessionInspectModal','authTimelineModal','compareModal','decModal','confirmModal','promptModal','setupModal','imgLightbox',
  // Overlay surfaces created by sheet.js and the Flow Drawer. Listed here so shortcut
  // gating (workflowShortcutBlocked) and the focus trap treat them as modals.
  'flowDrawer','flowDiffModal','engagementSheet','filtersSheet','moreSheet','detailSheet','toolsSheet','historySheet','paletteSheet','collEnvSheet','collScriptsSheet','collImportSheet','collExportSheet','collRunSheet',
  'collMatrixSheet','collCoverageSheet','collTimingSheet','collExampleDiffSheet','updateModal'];
const MODAL_Z_BASE=400;
const modalRegistry=new Map();
const modalStack=[];

function visibleFocusable(el){
  if(!el||el.disabled||el.hidden||el.getAttribute('aria-hidden')==='true'||el.getAttribute('aria-disabled')==='true')return false;
  if(el.closest('[hidden],[aria-hidden="true"]'))return false;
  const style=getComputedStyle(el);
  return style.display!=='none'&&style.visibility!=='hidden'&&style.visibility!=='collapse'&&el.getClientRects().length>0;
}
function topModal(){return modalStack[modalStack.length-1]||null;}
export function hasOpenModal(){return !!topModal();}
function modalZIndex(position){return MODAL_Z_BASE+position;}
function syncModalZOrder(){
  modalStack.forEach((entry,position)=>entry.el.style.setProperty('z-index',String(modalZIndex(position))));
}
function restoreModalZIndex(entry){
  if(!entry.zIndexSaved)return;
  if(entry.previousInlineZIndex)entry.el.style.setProperty('z-index',entry.previousInlineZIndex,entry.previousZIndexPriority);
  else entry.el.style.removeProperty('z-index');
  entry.previousInlineZIndex='';entry.previousZIndexPriority='';entry.zIndexSaved=false;
}
function modalFocusables(entry){
  return [...entry.dialog.querySelectorAll(FOCUSABLE)].filter(visibleFocusable);
}
function registerModal(modalEl){
  if(!modalEl)return null;
  let entry=modalRegistry.get(modalEl);
  if(entry)return entry;
  const dialog=modalEl.matches('[role="dialog"]')?modalEl:modalEl.querySelector('[role="dialog"]');
  if(!dialog)return null;
  // Controls can become disabled after opening (for example during a project
  // switch). Every fallback focus path still needs a focusable dialog shell.
  if(!dialog.hasAttribute('tabindex'))dialog.tabIndex=-1;
  entry={el:modalEl,dialog,previousFocus:null,onEscape:null,onDismiss:null,initialFocus:null,
    previousInlineZIndex:'',previousZIndexPriority:'',zIndexSaved:false};
  modalRegistry.set(modalEl,entry);
  modalEl.addEventListener('mousedown',e=>{
    if(e.target!==modalEl||topModal()!==entry)return;
    // The dismissal owner may intentionally refuse while a non-cancelable
    // request is pending. Keep focus inside the dialog either way; a real
    // close restores its invoker through closeModal().
    e.preventDefault();
    if(entry.onDismiss)entry.onDismiss();
    else if(entry.onEscape)entry.onEscape();
    else closeModal(modalEl);
    if(topModal()===entry&&!entry.dialog.contains(document.activeElement)){
      const pending=entry.dialog.querySelector('[aria-busy="true"][tabindex]');
      const target=visibleFocusable(pending)?pending:modalFocusables(entry)[0]||entry.dialog;
      target.focus({preventScroll:true});
    }
  });
  return entry;
}
export function openModal(modalEl,opts={}){
  const entry=registerModal(modalEl);
  if(!entry)return;
  const existing=modalStack.indexOf(entry);
  if(existing>=0)modalStack.splice(existing,1);
  else{
    entry.previousFocus=document.activeElement;
    entry.previousInlineZIndex=modalEl.style.getPropertyValue('z-index');
    entry.previousZIndexPriority=modalEl.style.getPropertyPriority('z-index');
    entry.zIndexSaved=true;
  }
  entry.onEscape=opts.onEscape||null;
  entry.onDismiss=opts.onDismiss||null;
  entry.initialFocus=opts.initialFocus||null;
  modalStack.push(entry);
  syncModalZOrder();
  modalEl.style.display='flex';
  setTimeout(()=>{
    if(topModal()!==entry)return;
    let first=typeof entry.initialFocus==='string'?entry.dialog.querySelector(entry.initialFocus):entry.initialFocus;
    if(!visibleFocusable(first))first=modalFocusables(entry)[0]||entry.dialog;
    if(first===entry.dialog&&!entry.dialog.hasAttribute('tabindex'))entry.dialog.tabIndex=-1;
    first.focus();
  },0);
}
export function closeModal(modalEl){
  const entry=modalRegistry.get(modalEl);
  if(!entry){if(modalEl)modalEl.style.display='none';return;}
  const idx=modalStack.indexOf(entry);
  const wasTop=idx===modalStack.length-1;
  if(idx>=0)modalStack.splice(idx,1);
  modalEl.style.display='none';
  restoreModalZIndex(entry);
  syncModalZOrder();
  entry.onEscape=null;entry.onDismiss=null;entry.initialFocus=null;
  if(!wasTop){
    const above=idx>=0?modalStack[idx]:null;
    if(above&&entry.el.contains(above.previousFocus))above.previousFocus=entry.previousFocus;
    entry.previousFocus=null;
    return;
  }
  const prev=entry.previousFocus;
  entry.previousFocus=null;
  const next=topModal();
  if(next){
    if(prev&&prev.isConnected&&next.dialog.contains(prev)&&visibleFocusable(prev))prev.focus();
    else if(!next.dialog.contains(document.activeElement)){
      const first=modalFocusables(next)[0]||next.dialog;
      first.focus();
    }
    return;
  }
  if(prev&&prev!==document.body&&prev!==document.documentElement&&prev.isConnected&&visibleFocusable(prev)&&typeof prev.focus==='function'){
    prev.focus();
    return;
  }
  // Auto-opened dialogs have no meaningful invoking control. Return keyboard
  // users to the selected workstation tab instead of leaving focus on body.
  const fallback=document.querySelector('.tab[aria-selected="true"]:not([disabled])');
  fallback?.focus({preventScroll:true});
}
function dismissTopModal(){
  const entry=topModal();
  if(!entry)return false;
  if(entry.onEscape)entry.onEscape();else closeModal(entry.el);
  return true;
}
export function closeModals(){return dismissTopModal();}

// Escape and Tab always target the last-opened visible dialog, never DOM order.
window.addEventListener('keydown',e=>{
  const entry=topModal();
  if(!entry)return;
  if(e.key==='Escape'){
    if(e.defaultPrevented)return;
    e.preventDefault();e.stopImmediatePropagation();
    if(entry.onEscape)entry.onEscape();else closeModal(entry.el);
    return;
  }
  if(e.key!=='Tab')return;
  const focusable=modalFocusables(entry);
  if(!focusable.length){e.preventDefault();entry.dialog.focus();return;}
  const first=focusable[0],last=focusable[focusable.length-1];
  if(!entry.dialog.contains(document.activeElement)){
    e.preventDefault();(e.shiftKey?last:first).focus();return;
  }
  if(e.shiftKey&&document.activeElement===first){e.preventDefault();last.focus();}
  else if(!e.shiftKey&&document.activeElement===last){e.preventDefault();first.focus();}
});

MODAL_IDS.forEach(id=>registerModal($('#'+id)));

/* ---- image lightbox (click any .md-img / .find-doc-img to focus + zoom) ---- */
const _imgLb={scale:1,ox:0,oy:0,fitScale:1,dragging:false,moved:false,dx:0,dy:0,px:0,py:0,wired:false};
function imgLbEls(){
  return{m:$('#imgLightbox'),stage:$('#imgLbStage'),img:$('#imgLbImg'),cap:$('#imgLbCaption'),pct:$('#imgLbZoomPct')};
}
function imgLbApply(){
  const{img,pct,stage}=imgLbEls();if(!img)return;
  img.style.transform=`translate(${_imgLb.ox}px,${_imgLb.oy}px) scale(${_imgLb.scale})`;
  if(pct)pct.textContent=Math.round((_imgLb.scale/Math.max(_imgLb.fitScale,1e-9))*100)+'%';
  if(stage)stage.classList.toggle('is-dragging',_imgLb.dragging);
}
function imgLbFit(){
  const{stage,img}=imgLbEls();if(!stage||!img||!img.naturalWidth)return;
  const pad=24,sw=Math.max(1,stage.clientWidth-pad*2),sh=Math.max(1,stage.clientHeight-pad*2);
  const s=Math.min(sw/img.naturalWidth,sh/img.naturalHeight);
  _imgLb.fitScale=s;_imgLb.scale=s;
  _imgLb.ox=(stage.clientWidth-img.naturalWidth*s)/2;
  _imgLb.oy=(stage.clientHeight-img.naturalHeight*s)/2;
  imgLbApply();
}
function imgLbZoomAt(factor,clientX,clientY){
  const{stage}=imgLbEls();if(!stage)return;
  const r=stage.getBoundingClientRect();
  const x=clientX-r.left,y=clientY-r.top;
  const ix=(x-_imgLb.ox)/_imgLb.scale,iy=(y-_imgLb.oy)/_imgLb.scale;
  const min=_imgLb.fitScale*0.5,max=_imgLb.fitScale*12;
  const next=Math.min(max,Math.max(min,_imgLb.scale*factor));
  _imgLb.scale=next;_imgLb.ox=x-ix*next;_imgLb.oy=y-iy*next;
  imgLbApply();
}
export function openImageLightbox(src,caption){
  ensureImageLightbox();
  const{m,stage,img,cap}=imgLbEls();if(!m||!img||!src)return;
  if(cap)cap.textContent=caption||'Screenshot';
  const cp=$('#imgLbCopy');
  if(cp){const ok=isCopyableImageURL(src,location.origin);cp.hidden=!ok;cp.dataset.copySrc=ok?src:'';cp.dataset.copyName=caption||'';cp.setAttribute('aria-label','Copy image'+(caption?': '+caption:''));}
  img.removeAttribute('width');img.removeAttribute('height');
  img.alt=caption||'Screenshot';
  img.onload=()=>{requestAnimationFrame(imgLbFit);};
  // Bust same-src cache so onload always fires when reopening.
  if(img.src===src)img.removeAttribute('src');
  img.src=src;
  _imgLb.dragging=false;_imgLb.moved=false;
  openModal(m);
  const closeBtn=$('#imgLbClose');
  if(closeBtn)setTimeout(()=>closeBtn.focus(),0);
  else if(stage)setTimeout(()=>stage.focus(),0);
  if(img.complete&&img.naturalWidth)requestAnimationFrame(imgLbFit);
}
export function closeImageLightbox(){const{m}=imgLbEls();if(m&&m.style.display==='flex')closeModal(m);}
function ensureImageLightbox(){
  if(_imgLb.wired)return;
  const{m,stage,img}=imgLbEls();
  if(!m||!stage||!img)return;
  _imgLb.wired=true;
  $('#imgLbClose')?.addEventListener('click',e=>{e.stopPropagation();closeImageLightbox();});
  $('#imgLbZoomIn')?.addEventListener('click',e=>{
    e.stopPropagation();
    const r=stage.getBoundingClientRect();
    imgLbZoomAt(1.25,r.left+r.width/2,r.top+r.height/2);
  });
  $('#imgLbZoomOut')?.addEventListener('click',e=>{
    e.stopPropagation();
    const r=stage.getBoundingClientRect();
    imgLbZoomAt(1/1.25,r.left+r.width/2,r.top+r.height/2);
  });
  $('#imgLbZoomReset')?.addEventListener('click',e=>{e.stopPropagation();imgLbFit();});
  stage.addEventListener('wheel',e=>{
    e.preventDefault();
    imgLbZoomAt(e.deltaY<0?1.12:1/1.12,e.clientX,e.clientY);
  },{passive:false});
  stage.addEventListener('dblclick',e=>{
    e.preventDefault();
    if(_imgLb.scale>_imgLb.fitScale*1.05)imgLbFit();
    else imgLbZoomAt((2.5*_imgLb.fitScale)/_imgLb.scale,e.clientX,e.clientY);
  });
  stage.addEventListener('pointerdown',e=>{
    if(e.button!==0)return;
    e.preventDefault();
    _imgLb.dragging=true;_imgLb.moved=false;_imgLb.px=e.clientX;_imgLb.py=e.clientY;
    _imgLb.dx=_imgLb.ox;_imgLb.dy=_imgLb.oy;
    stage.setPointerCapture(e.pointerId);
    imgLbApply();
  });
  stage.addEventListener('pointermove',e=>{
    if(!_imgLb.dragging)return;
    if(Math.abs(e.clientX-_imgLb.px)>3||Math.abs(e.clientY-_imgLb.py)>3)_imgLb.moved=true;
    _imgLb.ox=_imgLb.dx+(e.clientX-_imgLb.px);
    _imgLb.oy=_imgLb.dy+(e.clientY-_imgLb.py);
    imgLbApply();
  });
  const endDrag=e=>{
    if(!_imgLb.dragging)return;
    _imgLb.dragging=false;
    try{stage.releasePointerCapture(e.pointerId);}catch{/* already released */}
    imgLbApply();
  };
  stage.addEventListener('pointerup',endDrag);
  stage.addEventListener('pointercancel',endDrag);
  // Click empty stage (letterbox) closes; click/drag on the image does not.
  stage.addEventListener('click',e=>{
    if(e.target!==stage||_imgLb.moved)return;
    closeImageLightbox();
  });
  m.addEventListener('keydown',e=>{
    if(e.key==='+'||e.key==='='){e.preventDefault();$('#imgLbZoomIn')?.click();}
    else if(e.key==='-'||e.key==='_'){e.preventDefault();$('#imgLbZoomOut')?.click();}
    else if(e.key==='0'){e.preventDefault();imgLbFit();}
  });
  window.addEventListener('resize',()=>{if(m.style.display==='flex')imgLbFit();});
}
function imgLbClickTarget(t){
  if(!(t instanceof Element))return null;
  const img=t.closest('img.md-img, img.find-doc-img');
  if(!img||img.closest('#imgLightbox'))return null;
  return img;
}
function openImageLightboxFromTarget(target){
  const img=imgLbClickTarget(target);if(!img)return false;
  const src=img.currentSrc||img.getAttribute('src')||img.src;if(!src)return false;
  openImageLightbox(src,img.getAttribute('alt')||'');
  return true;
}
// Capture-phase so we open even if a parent stops bubble.
document.addEventListener('click',e=>{
  if(!imgLbClickTarget(e.target))return;
  e.preventDefault();
  e.stopPropagation();
  openImageLightboxFromTarget(e.target);
},true);
document.addEventListener('keydown',e=>{
  if(e.key!=='Enter'&&e.key!==' ')return;
  if(!imgLbClickTarget(e.target))return;
  e.preventDefault();
  e.stopPropagation();
  openImageLightboxFromTarget(e.target);
},true);
// Wire controls once DOM is ready (module scripts are deferred, but be safe).
if(document.readyState==='loading')document.addEventListener('DOMContentLoaded',ensureImageLightbox);
else ensureImageLightbox();

// ---- Copy image (finding images and generated flow previews) ----
// copyImageButton renders the shared compact "Copy image" control. src must be a
// finding image or flow-preview URL (copy-image.js enforces that again on click);
// what names the picture for assistive tech ("proof image", "flow #7 preview").
export function copyImageButton(src, what, name, opts) {
  const o = opts || {};
  const label = o.ariaLabel || 'Copy image' + (what ? ': ' + what : '');
  const extra = o.themeAware ? ' data-copy-theme' : '';
  return `<button type="button" class="${escAttr(o.className || 'btn xs')} js-copy-image" data-copy-src="${escAttr(src)}"${extra} data-copy-name="${escAttr(name || '')}" aria-label="${escAttr(label)}" title="Copy image to the clipboard (paste into Notion or any editor)">${icon('copy')} ${esc(o.text || 'Copy image')}</button>`;
}
// flowCopyPngButton renders the History "Copy as PNG" control for one flow: the
// server-rendered request+response preview, in the UI's active theme (re-read on
// click, so a theme switch after render still applies).
// opts: {side: both|req|res, layout: vertical|horizontal, theme: light|dark,
// text, ariaLabel}. Without an explicit theme the control follows the UI theme
// and re-reads it on click, so a theme switch after render still applies.
export function flowCopyPngButton(id, className, opts) {
  const o = opts || {};
  const side = o.side || 'both';
  const what = side === 'req' ? 'request' : side === 'res' ? 'response' : 'request and response';
  const name = 'flow-' + id + '-preview' + (side === 'both' ? '' : '-' + side) + (o.layout === 'horizontal' ? '-side-by-side' : '');
  return copyImageButton(flowPreviewPngURL(id, { side, layout: o.layout, theme: o.theme || activeFlowTheme(document.documentElement) }), '', name, {
    className: className || 'btn btn-compact',
    text: o.text != null ? o.text : 'Copy as PNG',
    ariaLabel: o.ariaLabel || ('Copy flow #' + id + ' ' + what + ' as PNG'),
    themeAware: !o.theme,
  });
}
// mountFlowCopyPng paints (or clears, when id is null) the Copy as PNG control into a mount element.
export function mountFlowCopyPng(el, id, className, opts) {
  if (el) el.innerHTML = id == null ? '' : flowCopyPngButton(id, className, opts);
}
// copyPreviewPng copies a server-rendered PNG to the clipboard without a button
// behind it, so a context-menu item can do it. It must start the clipboard write
// inside the activating gesture, which is why it calls copyImage() directly.
export function copyPreviewPng(src, name) {
  const run = copyImage(src);
  run.result.then(res => {
    if (res.ok) { toast('Image copied', 'success'); return; }
    const t = toast(copyFailureMessage(res.reason), 'error');
    if (!t) return;
    run.blob.then(blob => {
      const dl = document.createElement('button');
      dl.type = 'button';
      dl.className = 'btn xs';
      dl.setAttribute('aria-label', 'Download image as PNG');
      dl.innerHTML = icon('download') + ' Download';
      dl.addEventListener('click', () => { void saveFile(blob, copyImageFileName(name), 'image/png'); });
      t.insertBefore(dl, t.querySelector('.toast-dismiss'));
    }, () => {});
  });
}
// copyFlowPng copies one flow's rendered preview. opts matches flowCopyPngButton,
// plus the focus options of flowPreviewPngURL (focus, focusSide, regex, lines,
// context, caption) for an image centered on a selection.
export function copyFlowPng(id, opts) {
  const o = opts || {};
  const side = o.side || 'both';
  const focus = { focus: o.focus, focusSide: o.focusSide, regex: o.regex, lines: o.lines, context: o.context, caption: o.caption };
  copyPreviewPng(flowPreviewPngURL(id, { side, layout: o.layout, mask: o.mask, theme: o.theme || activeFlowTheme(document.documentElement), ...focus }),
    'flow-' + id + '-' + side + (o.mask ? '-masked' : '') + (o.focus ? '-focus' : '') + '-preview');
}
// openFlowFocusImage opens the "image around text" preview for a flow: focus
// controls, match result, Download and + Finding. opts: {side, selection,
// focusSide, pick} (pick lands on the finding picker after the first render).
export function openFlowFocusImage(id, opts) {
  const o = opts || {};
  import('./evidence-render.js').then((m) => {
    const { kind, params } = m.flowFocusPreview(id, o);
    return m.open(kind, params, { pick: !!o.pick });
  }).catch((e) => toastError('Flow image failed', e));
}
// flowPngMenuItems are the "Copy as PNG" rows every flow menu shares, so the
// Proxy row menu, the inspector menu and the Repeater menu offer the same three.
export function flowPngMenuItems(id) {
  if (!(Number(id) > 0)) return [];
  return [
    { label: 'Flow as PNG · both stacked', icon: 'image', act: () => copyFlowPng(id) },
    { label: 'Request as PNG', icon: 'image', act: () => copyFlowPng(id, { side: 'req' }) },
    { label: 'Response as PNG', icon: 'image', act: () => copyFlowPng(id, { side: 'res' }) },
    { label: 'Flow as PNG · side by side', icon: 'image', act: () => copyFlowPng(id, { layout: 'horizontal' }) },
    { label: 'Image around text…', icon: 'image', act: () => openFlowFocusImage(id) },
  ];
}
// flowSelectionImageItems are the "around selection" image rows the request and
// response viewers add when text is selected: the image centers on the selection
// (first line, ≤200 chars) searched on the viewer's own side.
export function flowSelectionImageItems(id, side, selection) {
  const focus = selectionToFocus(selection);
  if (!(Number(id) > 0) || !focus) return [];
  const focusSide = side === 'req' ? 'req' : 'res';
  return [
    { label: 'Copy image around selection', icon: 'image', act: () => copyFlowPng(id, { focus, focusSide }) },
    { label: 'Attach image around selection to finding…', icon: 'attach', act: () => openFlowFocusImage(id, { selection: focus, focusSide, pick: true }) },
  ];
}
// The click handler starts the clipboard write synchronously (Safari keeps the
// user gesture only for a ClipboardItem built from a promise inside the handler).
export function copyImageFromButton(btn) {
  let src = btn.dataset.copySrc || '';
  if (btn.hasAttribute('data-copy-theme')) src = src.replace(/([?&]theme=)\w+/, '$1' + activeFlowTheme(document.documentElement));
  if (btn.getAttribute('aria-busy') === 'true') return;
  btn.setAttribute('aria-busy', 'true');
  const run = copyImage(src);
  run.result.then(res => {
    if (res.ok) { toast('Image copied', 'success'); return; }
    const t = toast(copyFailureMessage(res.reason), 'error');
    if (!t) return;
    // Offer Download whenever the picture itself loaded (everything but fetch/blocked).
    run.blob.then(blob => {
      const dl = document.createElement('button');
      dl.type = 'button';
      dl.className = 'btn xs';
      dl.setAttribute('aria-label', 'Download image as PNG');
      dl.innerHTML = icon('download') + ' Download';
      dl.addEventListener('click', () => { void saveFile(blob, copyImageFileName(btn.dataset.copyName), 'image/png'); });
      t.insertBefore(dl, t.querySelector('.toast-dismiss'));
    }, () => {});
  }).finally(() => btn.removeAttribute('aria-busy'));
}
document.addEventListener('click', e => {
  const btn = e.target instanceof Element ? e.target.closest('.js-copy-image') : null;
  if (!btn) return;
  e.preventDefault();
  e.stopPropagation();
  copyImageFromButton(btn);
});

let activePromptFinish=null;
let activeConfirmFinish=null;

// uiPrompt: an in-app replacement for the browser's prompt() — themed, consistent,
// resolves to the entered string or null (Cancel / Escape / backdrop / empty).
export function uiPrompt(opts){
  opts=opts||{};
  if(activePromptFinish)activePromptFinish(null);
  return new Promise(resolve=>{
    const m=$('#promptModal'),inp=$('#promptInput');
    $('#promptTitle').textContent=opts.title||'Enter a value';
    inp.placeholder=opts.placeholder||'';inp.value=opts.value||'';
    let done=false;
    const finish=v=>{if(done)return;done=true;if(activePromptFinish===finish)activePromptFinish=null;closeModal(m);inp.onkeydown=null;resolve(v);};
    activePromptFinish=finish;
    openModal(m,{initialFocus:inp,onEscape:()=>finish(null),onDismiss:()=>finish(null)});
    setTimeout(()=>inp.select(),0);
    // allowEmpty: an empty OK answers '' so a caller can tell it from Cancel (null).
    const answer=()=>inp.value.trim()||(opts.allowEmpty?'':null);
    $('#promptOk').onclick=()=>finish(answer());
    $('#promptCancel').onclick=()=>finish(null);
    inp.onkeydown=e=>{if(e.key==='Enter'){e.preventDefault();finish(answer());}};
  });
}

// uiConfirm: themed confirm dialog reusing the promptModal structure.
// Returns a Promise<boolean>.
export function uiConfirm(title,htmlMsg,okLabel,okClass,okColor){
  if(activeConfirmFinish)activeConfirmFinish(false);
  return new Promise(resolve=>{
    const m=$('#confirmModal');
    if(!m){toast('Confirmation unavailable. Reload and try again.','error');resolve(false);return;}
    $('#confirmTitle').textContent=title;
    $('#confirmMsg').innerHTML=htmlMsg;
    const ok=$('#confirmOk');
    ok.textContent=okLabel||'OK';
    ok.className=(okClass||'btn accent');
    if(okColor)ok.style.color=okColor; else ok.style.color='';
    let done=false;
    const finish=v=>{if(done)return;done=true;if(activeConfirmFinish===finish)activeConfirmFinish=null;closeModal(m);ok.onclick=null;$('#confirmCancel').onclick=null;resolve(v);};
    activeConfirmFinish=finish;
    openModal(m,{initialFocus:$('#confirmCancel'),onEscape:()=>finish(false),onDismiss:()=>finish(false)});
    ok.onclick=()=>finish(true);
    $('#confirmCancel').onclick=()=>finish(false);
  });
}

// Minimal, safe markdown → HTML: escape first, then format a useful subset.
export function renderMD(src){
  if(!src||!src.trim())return '<p class="hint">Empty — switch to Edit and jot down creds, findings, scope…</p>';
  // \x00-\x04 are reserved as placeholder delimiters below; strip them from input
  // so typed control characters can't reference (or forge) a placeholder.
  let s=esc(String(src).replace(/[\x00-\x04]/g,'')),blocks=[],flowRefs=[],media=[];
  // Generated <img>/<a> markup is parked behind opaque placeholders and restored
  // last, so no later pass (notably the link regex) can match across a tag's
  // attributes and leave one open. \x03 = image (may sit inside link text),
  // \x04 = link (never nested inside another link).
  const pushMedia=(mark,html)=>{const i=media.length;media.push(html);return mark+i+mark;};
  const attr=v=>String(v).replace(/"/g,'&quot;').replace(/'/g,'&#39;');
  const pushFlow=(id,label,code)=>{const i=flowRefs.length;flowRefs.push({id:String(id),label,code:!!code});return '\x02'+i+'\x02';};
  s=s.replace(/```(\w*)\r?\n?([\s\S]*?)```/g,(m,lang,code)=>{
    const i=blocks.length;
    blocks.push({code:code.replace(/^\n|\n$/g,''),lang:(lang||'').trim().toLowerCase()});
    return '\x00'+i+'\x00';
  });
  // Protect flow refs early (opaque placeholders) so later HTML passes don't double-link.
  s=s.replace(/`(flow\s*:?\s*#?\s*\d+)`/gi,(m,inner)=>{
    const id=(inner.match(/(\d+)/)||[])[1]; return id?pushFlow(id,inner,true):m;
  });
  s=s.replace(/\b(flow\s*#?\s*)(\d+)\b/gi,(m,label,id)=>pushFlow(id,label+id,false));
  s=s.replace(/^---+\s*$/gm,'<hr class="md-hr">');
  s=s.replace(/^######\s?(.*)$/gm,'<h6>$1</h6>').replace(/^#####\s?(.*)$/gm,'<h5>$1</h5>').replace(/^####\s?(.*)$/gm,'<h4>$1</h4>').replace(/^###\s?(.*)$/gm,'<h3>$1</h3>').replace(/^##\s?(.*)$/gm,'<h2>$1</h2>').replace(/^#\s?(.*)$/gm,'<h1>$1</h1>');
  // esc() ran first, so the tags arrive as &lt;think&gt; — match that escaped form.
  s=s.replace(/&lt;(think|thought|reasoning)&gt;\r?\n?([\s\S]*?)&lt;\/\1&gt;/gi,(m,tag,inner)=>{
    const title = tag.charAt(0).toUpperCase() + tag.slice(1).toLowerCase();
    return '<details class="md-think"><summary>'+title+'</summary><div class="md-think-body">'+inner.trim()+'</div></details>';
  });
  s=s.replace(/(?:^|\n)((?:\|[^\n]+\|\r?\n)+)/g,(m,block)=>{
    const lines=block.trim().split(/\r?\n/).filter(l=>l.trim());
    if(lines.length<2||!/^\|[\s:|-]+\|$/.test(lines[1]))return m;
    const split=row=>row.split('|').slice(1,-1).map(c=>c.trim());
    const hdr=split(lines[0]);
    const flowCols=hdr.map(h=>/\bflow\b/i.test(h));
    const body=lines.slice(2).map(split);
    let html='<div class="md-table-wrap"><table class="md-table"><thead><tr>'+hdr.map(h=>'<th>'+h+'</th>').join('')+'</tr></thead><tbody>';
    body.forEach(r=>{
      html+='<tr>'+r.map((c,ci)=>{
        if(flowCols[ci]&&/^\d+$/.test(c)) return '<td>'+pushFlow(c,c,false)+'</td>';
        return '<td>'+c+'</td>';
      }).join('')+'</tr>';
    });
    return html+'</tbody></table></div>';
  });
  // Quote markers arrive escaped too (esc() turned "> " into "&gt; ").
  s=s.replace(/(?:^|\n)((?:&gt;[ \t]?.*(?:\r?\n|$))+)/g,(m,block)=>{
    const inner=block.trim().split(/\r?\n/).map(l=>l.replace(/^&gt;[ \t]?/,'')).join('\n');
    return '\n\n<blockquote class="md-quote">'+inner.replace(/\n/g,'<br>')+'</blockquote>\n\n';
  });
  s=s.replace(/(?:^|\n)((?:[-*] \[[ xX]\] .*(?:\r?\n|$))+)/g,(m,list)=>{
    const items=list.trim().split(/\r?\n/).map(l=>{
      const t=l.match(/^[-*] \[([ xX])\] (.*)$/);
      const done=t&&t[1]!==' ';
      return '<li class="md-task'+(done?' done':'')+'"><input type="checkbox" disabled'+(done?' checked':'')+'> '+((t&&t[2])||l)+'</li>';
    });
    return '\n<ul class="md-tasks">'+items.join('')+'</ul>';
  });
  s=s.replace(/(?:^|\n)((?:[-*] (?!\[[ xX]\] ).*(?:\r?\n|$))+)/g,(m,list)=>'\n<ul>'+list.trim().split(/\r?\n/).map(l=>'<li>'+l.replace(/^[-*]\s?/,'')+'</li>').join('')+'</ul>');
  s=s.replace(/(?:^|\n)((?:\d+\. .*(?:\r?\n|$))+)/g,(m,list)=>'\n<ol>'+list.trim().split(/\r?\n/).map(l=>'<li>'+l.replace(/^\d+\.\s?/,'')+'</li>').join('')+'</ol>');
  s=s.replace(/!\[([^\]\x00-\x04]*)\]\((https?:\/\/[^\s"<>)\[\]\x00-\x04]+|\/api\/notes\/images\/\d+|data:image\/[a-z0-9.+-]+;base64,[A-Za-z0-9+/=]+)\)/g,(m,alt,src)=>pushMedia('\x03','<img class="md-img" tabindex="0" role="button" aria-label="Open screenshot: '+attr(alt)+'" alt="'+attr(alt)+'" src="'+attr(src)+'">'));
  s=s.replace(/\*\*([^*]+)\*\*/g,'<strong>$1</strong>');
  s=s.replace(/(?<!\*)\*([^*\n]+)\*(?!\*)/g,'<em>$1</em>');
  // Explicit in-app flow links: [label](flow:123) or [label](#flow-123)
  s=s.replace(/\[([^\]\x04]+)\]\((?:flow:|#flow-)(\d+)\)/g,(m,txt,id)=>pushMedia('\x04','<a class="md-flow-link" data-flow="'+id+'" href="#flow-'+id+'">'+txt+'</a>'));
  s=s.replace(/`([^`\n]+)`/g,'<code>$1</code>');
  s=s.replace(/\[([^\]\x04]+)\]\(((?:https?:\/\/|mailto:)[^\s"<>)\x00-\x04]+)\)/g,(m,txt,href)=>pushMedia('\x04','<a href="'+attr(href)+'" target="_blank" rel="noopener">'+txt+'</a>'));
  s='<p>'+s.replace(/\n{2,}/g,'</p><p>').replace(/\n/g,'<br>')+'</p>';
  s=s.replace(/\x00(\d+)\x00/g,(m,i)=>{
    const b=blocks[Number(i)]||{code:'',lang:''};
    const lang=b.lang?(' data-lang="'+escAttr(b.lang)+'"'):'';
    return '<pre class="md-code"'+lang+'>'+b.code+'</pre>';
  });
  s=s.replace(/\x02(\d+)\x02/g,(m,i)=>{
    const r=flowRefs[Number(i)]; if(!r) return m;
    const body=r.code?'<code>flow:'+r.id+'</code>':r.label;
    return '<a class="md-flow-link" data-flow="'+r.id+'" href="#flow-'+r.id+'">'+body+'</a>';
  });
  // Links first (their text may hold image placeholders), then images.
  s=s.replace(/\x04(\d+)\x04/g,(m,i)=>media[Number(i)]||'');
  s=s.replace(/\x03(\d+)\x03/g,(m,i)=>media[Number(i)]||'');
  return s.replace(/<p>\s*<\/p>/g,'').replace(/<p>(<(?:h\d|ul|ol|table|pre|blockquote|hr|div))/g,'$1').replace(/(<\/(?:h\d|ul|ol|table|pre|blockquote|div)>)<\/p>/g,'$1');
}
// ---- binary-body detection (inspector shows headers only for non-text bodies) ----

// bodyMime returns the body's content type for a side: the response MIME for "res",
// or the request's Content-Type header for "req".
export function bodyMime(detail, side){
  if(!detail) return '';
  if(side==='res') return detail.mime||'';
  const h=detail.reqHeaders||{};
  for(const k in h){ if(k.toLowerCase()==='content-type') return (h[k]&&h[k][0])||''; }
  return '';
}
// isBinaryMime is true for content that isn't human-readable as text (images, media,
// fonts, archives, office/binary docs). SVG and the text/* family stay printable.
export function isBinaryMime(m){
  if(!m) return false;
  m=m.toLowerCase();
  if(m.indexOf('svg')>=0) return false;
  if(m.startsWith('image/')||m.startsWith('audio/')||m.startsWith('video/')||m.startsWith('font/')) return true;
  return /(octet-stream|pdf|zip|gzip|wasm|protobuf|msword|ms-excel|ms-powerpoint|officedocument|vnd\.|woff|x-font|x-7z|x-rar|x-tar|x-msdownload|dmg|iso)/.test(m);
}
function headerLines(h){const out=[];if(h)Object.keys(h).forEach(k=>(h[k]||[]).forEach(v=>out.push(k+': '+v)));return out.join('\n');}
// headerBlockText reconstructs just the HTTP header block (start line + headers) from
// a flow-detail DTO — no body, no network fetch — for rendering binary flows.
export function headerBlockText(detail, side){
  if(!detail) return '';
  if(side==='req') return `${detail.method||'GET'} ${detail.path||'/'} ${detail.httpVersion||'HTTP/1.1'}\n`+headerLines(detail.reqHeaders);
  const line=`${detail.httpVersion||'HTTP/1.1'} ${detail.status||''} ${statusText(detail.status)}`.replace(/\s+$/,'');
  return line+'\n'+headerLines(detail.resHeaders);
}

// Wrap each heading + the content beneath it in a collapsible <details>, so long
// notes fold into title/subtitle sections you can open and close.
export function accordionize(box){
  const kids=[...box.childNodes],frag=document.createDocumentFragment();let i=0;
  while(i<kids.length){
    const n=kids[i];
    if(n.nodeType===1&&/^H[1-6]$/.test(n.tagName)){
      const det=document.createElement('details');det.className='acc';det.open=true;
      const sum=document.createElement('summary');sum.className='acc-h';sum.appendChild(n);det.appendChild(sum);
      const body=document.createElement('div');body.className='acc-body';i++;
      while(i<kids.length&&!(kids[i].nodeType===1&&/^H[1-6]$/.test(kids[i].tagName))){body.appendChild(kids[i]);i++;}
      det.appendChild(body);frag.appendChild(det);
    }else{frag.appendChild(n);i++;}
  }
  box.appendChild(frag);
}

initUiSelects();
