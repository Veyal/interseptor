// app.js — entry module and glue. Imports every feature module (which wires its
// own DOM handlers on load), then owns the cross-cutting pieces: tab switching,
// the command palette, global keyboard shortcuts, the live SSE event stream,
// theme, the version badge, and the boot sequence that kicks everything off.
import { $, $$, esc, state, api, toast, MODAL_IDS, openModal, closeModal, setStorageProject } from './core.js';
import { selectFlow, renderChips, loadFlows, loadScope, loadViews, scheduleReload, renderWSFrames, clearAllFilters, walkFlowNav, toggleSelectAllShown, handleFlowNew, handleFlowUpdate, openCompare, copyCurl } from './proxy.js';
import { renderIntercept, toggleIntercept, loadRules } from './intercept.js';
import { repInit, intrInit, repSend, sendToRepeater, sendToIntruder, scheduleIntr } from './tools.js';
import { loadIssues, runScan, loadScanTargets, openActive, openDecoder, openChecks, loadActive, loadChecksList, loadOob, renderAsScopePanel } from './scanner.js';
import { openCodecs, loadCodecsList } from './codecs.js';
import { loadSettings, loadSysProxy, loadAndroid, loadIOS, loadIOSSsh, loadSession, loadProject, openProjectModal, applyAiDisabledUI, applyOobDisabledUI, loadDeviceProxyEndpoint } from './settings.js';
import { loadNotes, flushNotesSave, focusNotes, organizeNotes } from './notes.js';
import { renderActivity, onActivity, loadActivity, clearActSeen } from './activity.js';
import { loadFindings } from './findings.js';
import { loadTags } from './tags.js';
import { loadHumanInput } from './humaninput.js';
import './flowmodal.js'; // side-effect: flow inspect popup + modal handlers
import { openAi } from './ai.js';
import './authz.js'; // side-effect: wires authz modal buttons
import { openAuthz, renderAuthzScopePanel } from './authz.js';
import { maybeShowSetup, openSetup } from './setup.js';
import { loadTrafficDiagnosis, syncTlsBannerSetting, setTlsBannerHidden } from './tlsdiag.js';
// map.js is NOT imported here: every other feature module is already reachable
// from the boot sequence below (loadIssues/loadFindings/loadSettings/etc. all run
// unconditionally on load, and proxy.js's own import chain pulls in
// tags/ai/authz/tlsdiag/flowmodal regardless of active tab), so static-importing
// it buys nothing. Map's code never runs unless the user visits it — see
// loadMapModule() below for the dynamic import() (Phase 4a).
let mapMod=null, autopwnMod=null;
function loadMapModule(){ return mapMod || (mapMod=import('./map.js')); }
function loadAutopwnModule(){ return autopwnMod || (autopwnMod=import('./autopwn.js')); }

/* ---- nav-rail badges (Discover/Map off-screen-update dots) ---- */
// Mirrors the existing heldBadge/actBadge pattern (set on event, clear on tab
// visit) but as a simple on/off dot per the roadmap's minimal-first-pass ask —
// full SSE-contract unification is a later, separate step.
function setNavDot(id,on){ const el=$('#'+id); if(el)el.classList.toggle('on',!!on); }
function clearNavDot(id){ setNavDot(id,false); }

/* ---- breadcrumb (top bar "Group / Panel" context) ---- */
function updateCrumb(t){
  const crumb=$('#crumb'); if(!crumb)return;
  const g=crumb.querySelector('.crumb-group'), p=crumb.querySelector('.crumb-panel');
  if(g)g.textContent=t.dataset.group||'';
  if(p)p.textContent=(t.textContent||'').trim().replace(/\d+$/,'').trim();
}

/* ---- tabs ---- */
function activateTab(t){
  const prev=$('.panel.active');
  if(prev&&prev.dataset.panel==='notes')flushNotesSave();
  const tabs=$$('.tab');
  tabs.forEach(x=>{x.classList.remove('active');x.setAttribute('aria-selected','false');x.tabIndex=-1;});
  t.classList.add('active');t.setAttribute('aria-selected','true');t.tabIndex=0;
  $$('.panel').forEach(p=>p.classList.toggle('active',p.dataset.panel===t.dataset.tab));
  try{localStorage.setItem('tab',t.dataset.tab);}catch(e){} // remember the open tab across refresh
  updateCrumb(t);
  if(t.dataset.tab==='activity'){renderActivity();clearActSeen();}
  if(t.dataset.tab==='scanner')loadScanTargets();
  if(t.dataset.tab==='findings')loadFindings();
  if(t.dataset.tab==='map'){clearNavDot('mapBadge');loadMapModule().then(m=>m.loadEndpoints());}
  if(t.dataset.tab==='autopwn'){clearNavDot('autopwnBadge');loadAutopwnModule().then(m=>m.loadAutopwn());}
  if(t.dataset.tab==='notes')loadNotes();
}
function goToNotes(){
  const tab=document.querySelector('.tab[data-tab="notes"]');
  if(tab)activateTab(tab);
  focusNotes();
}
$$('.tab').forEach(t=>{
  t.onclick=()=>activateTab(t);
  // Roving tabindex: only the active tab is in the tab sequence initially
  t.tabIndex=t.classList.contains('active')?0:-1;
});
// Roving arrow-key navigation within the tablist (ARIA tablist pattern). The
// rail is vertical, so Up/Down walks it; Left/Right are also accepted so
// muscle memory from the old horizontal strip still works.
$('#tabs').addEventListener('keydown',e=>{
  if(e.key!=='ArrowLeft'&&e.key!=='ArrowRight'&&e.key!=='ArrowUp'&&e.key!=='ArrowDown')return;
  const tabs=$$('.tab');
  const idx=tabs.indexOf(document.activeElement);
  if(idx<0)return;
  e.preventDefault();
  const fwd=e.key==='ArrowRight'||e.key==='ArrowDown';
  const next=fwd?tabs[(idx+1)%tabs.length]:tabs[(idx-1+tabs.length)%tabs.length];
  next.focus();
  activateTab(next);
});
// Re-open whichever tab was active before a refresh (default stays Proxy).
function restoreTab(){
  try{let id=localStorage.getItem('tab');
    // Legacy: Findings used to be a sub-view of the Scanner tab (scanSub==='findings').
    // It is now its own top-level tab — redirect old saved state to it.
    if(id==='scanner'&&localStorage.getItem('scanSub')==='findings'){id='findings';localStorage.setItem('tab','findings');localStorage.removeItem('scanSub');}
    if(id==='api'){id='settings';localStorage.setItem('tab','settings');}
    if(!id||id==='proxy')return;
    const b=document.querySelector('.tab[data-tab="'+id+'"]');if(b)b.click();
    if(id==='settings'&&localStorage.getItem('setSec')==='api'){document.querySelector('#setNav button[data-sec="api"]')?.click();}
  }catch(e){}
}

// ↑/↓/j/k walk History; Shift extends range, Ctrl+Shift toggles — only on Proxy tab.
document.addEventListener('keydown',e=>{
  const mod=e.ctrlKey||e.metaKey;
  const p=document.querySelector('.panel[data-panel="proxy"]');
  if(p&&p.classList.contains('active')&&mod&&e.shiftKey&&e.key.toLowerCase()==='a'){
    const t=e.target;
    if(isTypingTarget(t))return;
    e.preventDefault();toggleSelectAllShown();return;
  }
  if(e.key!=='ArrowDown'&&e.key!=='ArrowUp'&&e.key!=='j'&&e.key!=='k')return;
  if(!p||!p.classList.contains('active'))return;
  const t=e.target;
  if(isTypingTarget(t))return;
  if(MODAL_IDS.some(id=>{const m=$('#'+id);return m&&m.style.display==='flex';}))return;
  if(typeof cmdk!=='undefined'&&cmdk.open)return;
  const down=e.key==='ArrowDown'||e.key==='j';
  e.preventDefault();
  const id=walkFlowNav(down,e);
  if(id==null)return;
  const row=document.querySelector('#rows .trow[data-id="'+id+'"]');
  if(row)row.scrollIntoView({block:'nearest'});
});

/* ---- capture liveness (top bar) ---- */
let capLast=0, capCount=0;
function onCapture(){ capLast=Date.now(); capCount++; const d=$('#capDot'); if(d)d.classList.add('live'); renderCapStat(); }
function renderCapStat(){
  const s=$('#capStat'); if(!s)return;
  if(!capLast){ s.textContent=''; return; }
  const ago=Math.round((Date.now()-capLast)/1000);
  const d=$('#capDot');
  if(ago<3){ s.textContent='· capturing live'; }
  else { if(d)d.classList.remove('live'); s.textContent='· idle · '+capCount+' captured this session'; }
  renderIcptStat();
}
function renderIcptStat(){
  const el=$('#icptStat'); if(!el)return;
  const ic=state.intercept||{};
  const held=(ic.queue||[]).length+(ic.responseQueue||[]).length;
  const parts=[];
  if(ic.enabled) parts.push('REQ intercept ON');
  if(ic.responseEnabled) parts.push('RESP intercept ON');
  if(held) parts.push(held+' held');
  if(parts.length){ el.style.display='inline'; el.textContent='· '+parts.join(' · '); }
  else el.style.display='none';
}
setInterval(renderCapStat,1000);

let mapRefreshT=null, mapLoadedSig='';
function scheduleMapRefresh(){
  clearTimeout(mapRefreshT);
  mapRefreshT=setTimeout(()=>{
    if(!document.querySelector('.tab[data-tab="map"]')?.classList.contains('active')) return;
    // On a busy proxy the SSE stream fires constantly; don't re-fetch + re-render
    // the (potentially huge) map unless new flows actually arrived since the last
    // load. Newest id + count is a cheap "did anything change" signal.
    const sig=state.flows.length+':'+((state.flows[0]&&state.flows[0].id)||0);
    if(sig===mapLoadedSig) return;
    mapLoadedSig=sig;
    loadMapModule().then(m=>m.loadEndpoints());
  },2000);
}

/* ---- live events ---- */
// STALE_GAP_MS: native EventSource auto-reconnects on drop but replays nothing —
// any broadcasts the server fanned out while this tab had no open connection
// (backgrounded tab throttled/suspended, laptop sleep, brief network blip) are
// simply gone (confirmed server-side: internal/control/events.go drops a
// disconnected client's channel outright, no replay buffer). A live per-event
// handler can't distinguish "nothing happened" from "something happened but we
// missed the broadcast," so past a threshold gap we stop trusting incremental
// per-event catch-up and do one full resync instead. The server emits a named
// `hello` SSE event (event: hello) on every connection, including reconnects —
// used here purely as a reconnect signal, not parsed as a payload.
const STALE_GAP_MS=8000;
let lastSSEMsgAt=0;   // wall-clock time of the last message/connection event seen
let sseConnectedOnce=false; // false until the very first `hello` (initial connect)
function resyncAfterStaleReconnect(){
  // Full-refresh path per panel/global state a long gap could have gone stale
  // for. Mirrors scheduleReload()'s "just refetch everything" philosophy but
  // applied beyond just the flow list, since ANY event type could have been
  // dropped, not only flow.new/flow.update.
  scheduleReload();
  loadScope();
  loadRules();
  loadTags();
  if(document.querySelector('.tab[data-tab="intruder"]')?.classList.contains('active'))scheduleIntr();
  if(document.querySelector('.tab[data-tab="scanner"]')?.classList.contains('active'))loadIssues();
  if(document.querySelector('.tab[data-tab="findings"]')?.classList.contains('active'))loadFindings();
  if(document.querySelector('.tab[data-tab="notes"]')?.classList.contains('active'))loadNotes();
  if(document.querySelector('.tab[data-tab="activity"]')?.classList.contains('active'))renderActivity();
  if(document.querySelector('.tab[data-tab="map"]')?.classList.contains('active'))loadMapModule().then(m=>m.loadEndpoints());
  if(document.querySelector('.tab[data-tab="autopwn"]')?.classList.contains('active'))loadAutopwnModule().then(m=>m.refreshAutopwn());
  refreshIntercept().then(()=>renderIcptStat());
  loadHumanInput();
}
/* ---- SSE event contract convention ----
   The event stream (`/api/events`) currently mixes several different contracts
   per the UI-REDESIGN-ROADMAP.md §4 audit:
     - payload-inline:        the message carries the full changed object, no
                               refetch needed at all (flow.new/flow.update when
                               `m.flow` is present, intercept.update, activity).
     - always-reload:         the message is just a nudge ("something changed");
                               the handler unconditionally refetches, regardless
                               of which tab/panel is open (rules.update,
                               notes.update, findings.update, tags.update,
                               views.update, session.update, settings.update).
     - panel-gated nudge:     refetch only if the relevant panel is the active
                               tab, to avoid needless work for a panel the user
                               isn't looking at (scanner.update, discovery.update
                               — the latter also sets a nav-rail badge when the
                               panel is inactive instead of silently no-op'ing).
     - modal-gated nudge:     same idea as panel-gated, but gated on a modal's
                               open state instead of a tab (checks.update,
                               activescan.update, oob.update — all three follow
                               the exact same shape: "reload this modal's list
                               if it's currently open, else do nothing since
                               there's no badge/affordance for these modals").
     - conditional/derived:   the handler's action depends on more than just
                               "which panel is open" (flow.new/flow.update
                               without a payload, ws.frame keyed to the selected
                               flow id, scope.update fanning out to up to 3
                               different panels' reload calls).
   This registry doesn't migrate every event (that's explicitly out of scope for
   this pass — see UI-REDESIGN-ROADMAP.md §4's "partial, correct, and
   well-documented is better than complete-but-risky") but gives the modal-gated
   and always-reload shapes ONE shared helper each, so the next contributor
   adding a modal-nudge event has a documented pattern to extend instead of
   copy-pasting a fourth `if($('#xModal').style.display==='flex')...` clause.
   `onModalUpdate` generalizes the same "refresh if open, else no-op" contract
   the Phase 3 Discover/Map badges already established for tabs (see
   setNavDot/clearNavDot above) — for events with no badge affordance, "else"
   is simply a no-op instead of setting a dot. */
// onModalUpdate: reload a modal's contents only while it's open — the shared
// shape behind checks.update/activescan.update/oob.update below.
function onModalUpdate(modalId,reloadFn){
  const m=$('#'+modalId);
  if(m&&m.style.display==='flex')reloadFn();
}
// SSE_HANDLERS documents (and, for the modal-gated group, implements) each
// event's contract in one place. Events not listed here are still handled
// directly in the es.onmessage dispatcher below — this is a partial migration,
// not a full replacement of the if/else chain (see the comment above).
const SSE_HANDLERS={
  'checks.update':{contract:'modal-gated nudge',run:()=>onModalUpdate('checksModal',loadChecksList)},
  'codecs.update':{contract:'modal-gated nudge',run:()=>onModalUpdate('codecsModal',loadCodecsList)},
  'activescan.update':{contract:'modal-gated nudge',run:()=>onModalUpdate('activeModal',loadActive)},
  'oob.update':{contract:'modal-gated nudge',run:()=>onModalUpdate('oobModal',loadOob)},
  'notes.update':{contract:'always-reload',run:loadNotes},
  'findings.update':{contract:'always-reload',run:loadFindings},
  'tags.update':{contract:'always-reload',run:loadTags},
};
function connectEvents(){
  const es=new EventSource('/api/events');
  // Fires on the initial connect AND every browser auto-reconnect (the server
  // sends it fresh on each new stream, see handleEvents in events.go). The very
  // first `hello` is just the normal boot connection — the boot sequence already
  // loaded everything, so it never triggers a resync. Every `hello` after that IS
  // a reconnect by definition (this handler only runs once per open connection);
  // treat any reconnect following a gap longer than STALE_GAP_MS since the last
  // thing we saw (a message, or the previous connection) as "may have missed
  // broadcasts" and resync.
  es.addEventListener('hello',()=>{
    setSseStatus('ok');
    const now=Date.now();
    const gap=lastSSEMsgAt?now-lastSSEMsgAt:Infinity;
    if(sseConnectedOnce&&gap>STALE_GAP_MS)resyncAfterStaleReconnect();
    sseConnectedOnce=true;
    lastSSEMsgAt=now;
  });
  es.onmessage=e=>{lastSSEMsgAt=Date.now();let m;try{m=JSON.parse(e.data);}catch(err){return;}
    const handler=SSE_HANDLERS[m.type];
    if(handler){handler.run(m);return;}
    if(m.type==='flow.new'){if(m.flow)handleFlowNew(m.flow);else scheduleReload();onCapture();scheduleMapRefresh();if(!document.querySelector('.tab[data-tab="map"]').classList.contains('active'))setNavDot('mapBadge',true);}
    else if(m.type==='flow.update'){if(m.flow)handleFlowUpdate(m.flow);else scheduleReload();if(m.flow&&m.flow.id===state.selId)selectFlow(state.selId);}
    else if(m.type==='activity')onActivity(m.item);
    else if(m.type==='activity.clear'){state.activity=[];if(document.querySelector('.tab[data-tab="activity"]').classList.contains('active'))renderActivity();clearActSeen();}
    else if(m.type==='intercept.update'){state.intercept=m.intercept;renderIntercept();renderIcptStat();}
    else if(m.type==='rules.update')loadRules();
    else if(m.type==='intruder.update')scheduleIntr();
    else if(m.type==='scanner.update')loadIssues();
    else if(m.type==='ws.frame'){if(m.flowId===state.selId)renderWSFrames(state.selId);}
    else if(m.type==='scope.update'){loadScope();if(state.inScopeOnly)loadFlows();if($('#activeModal')&&$('#activeModal').style.display==='flex')renderAsScopePanel();if($('#authzModal')&&$('#authzModal').style.display==='flex')renderAuthzScopePanel();}
    else if(m.type==='views.update')loadViews();
    else if(m.type==='session.update')loadSession();
    else if(m.type==='autopwn.update'){const onTab=document.querySelector('.tab[data-tab="autopwn"]').classList.contains('active');if(!onTab)setNavDot('autopwnBadge',true);loadAutopwnModule().then(mod=>mod.onAutopwnUpdate(m));}
    else if(m.type==='settings.update'){loadSettings();loadVersion(false);loadSysProxy();loadDeviceProxyEndpoint();loadAndroid();loadIOS();loadIOSSsh();applyAiDisabledUI();applyOobDisabledUI();}
    else if(m.type==='human.input')loadHumanInput();
    else if(m.type==='tunnel.update')window.dispatchEvent(new CustomEvent('interceptor:tunnel'));
  };
  es.onerror=()=>{ setSseStatus('reconnecting'); /* browser auto-reconnects */ };
}

function setSseStatus(s){
  const dot=$('#sseDot'), label=$('#sseLabel'), wrap=$('#sseStatus');
  if(!dot) return;
  dot.className='sse-dot '+s;
  if(s==='ok'){ label.textContent='live'; if(wrap) wrap.title='Live updates: connected'; }
  else { label.textContent='reconnecting'; if(wrap) wrap.title='Live updates: reconnecting…'; }
}

/* ---- command palette (Ctrl/Cmd+K) ---- */
const cmdk={el:null,input:null,list:null,items:[],sel:0,open:false};
function cmdkBuild(){
  const o=document.createElement('div');o.id='cmdk';
  o.style.cssText='position:fixed;inset:0;z-index:300;display:none;align-items:flex-start;justify-content:center;background:var(--overlay)';
  o.innerHTML='<div role="dialog" aria-modal="true" aria-labelledby="cmdkTitle" class="modal-shell" style="margin-top:11vh;width:min(680px,92vw)">'
    +'<div class="modal-shell-head"><span id="cmdkTitle" class="modal-shell-title">Command palette</span></div>'
    +'<input id="cmdkInput" role="combobox" aria-label="Search commands and flows" aria-controls="cmdkList" aria-expanded="true" aria-autocomplete="list" placeholder="Search flows · jump to a tab · run a command…" autocomplete="off" spellcheck="false" style="width:100%;box-sizing:border-box;padding:14px 16px;border:0;border-bottom:1px solid var(--line);background:transparent;color:var(--fg);font-size:15px">'
    +'<div id="cmdkList" role="listbox" aria-label="Command results" style="max-height:52vh;overflow:auto;padding:6px"></div>'
    +'<div style="padding:7px 14px;border-top:1px solid var(--line);color:var(--fg3);font-size:10px;display:flex;gap:16px"><span>↑ ↓ navigate</span><span>⏎ run</span><span>esc close</span></div></div>';
  document.body.appendChild(o);
  cmdk.el=o;cmdk.input=o.querySelector('#cmdkInput');cmdk.list=o.querySelector('#cmdkList');
  cmdk.input.oninput=cmdkRender;
  cmdk.input.onkeydown=e=>{
    if(e.key==='ArrowDown'){e.preventDefault();cmdk.sel=Math.min(cmdk.items.length-1,cmdk.sel+1);cmdkPaint();}
    else if(e.key==='ArrowUp'){e.preventDefault();cmdk.sel=Math.max(0,cmdk.sel-1);cmdkPaint();}
    else if(e.key==='Home'){e.preventDefault();cmdk.sel=0;cmdkPaint();}
    else if(e.key==='End'){e.preventDefault();cmdk.sel=Math.max(0,cmdk.items.length-1);cmdkPaint();}
    else if(e.key==='Enter'){e.preventDefault();cmdkRun(cmdk.sel);}
    else if(e.key==='Escape'){e.preventDefault();e.stopPropagation();cmdkClose();}
  };
}
// The palette NAVIGATES — it jumps to a tab, a Settings subsection, or a tool
// screen — plus a few non-destructive conveniences (toggle theme, copy the
// selected flow as cURL). It deliberately never performs a mutating/irreversible
// action (run a scan, toggle intercept, export, send/delete a request) so a
// mis-typed Enter can't do anything destructive; you act from the screen it takes
// you to. `kw` adds search aliases.
function cmdkCommands(){
  const go=name=>()=>document.querySelector('.tab[data-tab="'+name+'"]').click();
  const goSet=sec=>()=>{document.querySelector('.tab[data-tab="settings"]').click();const b=document.querySelector('#setNav button[data-sec="'+sec+'"]');if(b)b.click();};
  return [
    ...(state.aiDisabled?[]:[{t:'Go to Autopilot',kw:'autopilot autopwn autonomous ai pentest agent run scan verified findings',run:go('autopwn')}]),
    {t:'Go to Proxy',kw:'proxy history flows requests traffic inspect captured',run:go('proxy')},
    {t:'Go to Intercept',kw:'hold forward drop match replace rules',run:go('intercept')},
    {t:'Go to Repeater',kw:'resend craft edit request',run:go('repeater')},
    {t:'Go to Intruder',kw:'fuzz brute force payloads enumerate',run:go('intruder')},
    {t:'Go to Scanner',kw:'passive active scan checks issues vulnerabilities report',run:go('scanner')},
    {t:'Go to Findings',kw:'findings poc vulnerability record curated impact',run:go('findings')},
    {t:'Go to Map',kw:'endpoints attack surface graph tree headers body search',run:go('map')},
    {t:'Go to Notes',kw:'scratchpad markdown findings notebook',run:goToNotes},
    {t:'Edit custom scanner checks',kw:'starlark checks passive custom rules',run:openChecks},
    {t:'Edit message codecs',kw:'encrypt decrypt aes codec plaintext decoded',run:openCodecs},
    {t:'Open active scan',kw:'active attack payloads consent arm fuzz',run:openActive},
    ...(state.oobEnabled?[{t:'Open OOB catcher',kw:'out of band blind ssrf callback collab',run:()=>{openModal($('#oobModal'));loadOob();}}]:[]),
    ...(state.aiDisabled?[]:[{t:'Organize project notes with AI',kw:'notes structure sort clean headings findings todo',run:()=>{goToNotes();organizeNotes();}}]),
    {t:'Open Authz test',kw:'authorization access control roles identity',run:()=>{const f=selectedFlow();if(f)openAuthz(f.id);else toast('select a flow in History first');}},
    {t:'Send selected flow to Repeater',kw:'resend craft edit request history',run:()=>{const f=selectedFlow();if(f)sendToRepeater(f);else toast('select a flow in History first');}},
    {t:'Send selected flow to Intruder',kw:'fuzz brute force payloads enumerate',run:()=>{const f=selectedFlow();if(f)sendToIntruder(f);else toast('select a flow in History first');}},
    {t:'Open Decoder (base64 / url / jwt / hex…)',kw:'encode decode smart',run:()=>openDecoder()},
    {t:'Compare selected flows (diff)',kw:'compare diff two flows responses side by side',run:()=>openCompare()},
    {t:'Copy selected flow as cURL',kw:'curl copy clipboard request reproduce',run:()=>{const f=selectedFlow();if(f)copyCurl(f);else toast('select a flow in History first');}},
    ...(state.aiDisabled?[]:[{t:'Go to Activity',kw:'ai mcp glass box agent log',run:go('activity')}]),
    {t:'Switch or create project',kw:'projects workspace engagement open new default',run:openProjectModal},
    {t:'Run setup wizard',kw:'setup wizard onboarding first run guide proxy ca scope',run:openSetup},
    {t:'Toggle theme (dark / light)',kw:'dark mode light appearance ui color scheme',run:toggleTheme},
    {t:'Settings: Proxy & network',kw:'listener bind port upstream system proxy capture browser telemetry android gms crashlytics invisible',run:goSet('proxy')},
    {t:'Settings: TLS / CA — download CA certificate',kw:'https certificate cert trust install ca download mitm ssl pinning diagnosis passthrough bypass',run:goSet('tls')},
    {t:'Settings: Mobile devices — Android / iOS',kw:'android ios adb simulator device jailbreak ssh proxy install ca mobile phone',run:goSet('devices')},
    {t:'Settings: Target scope',kw:'include exclude host path in scope',run:goSet('scope')},
    {t:'Settings: AI assist — provider & API key',kw:'anthropic openrouter model api key llm',run:goSet('ai')},
    {t:'Settings: Scanner & OOB',kw:'scanner oob passive active checks enable',run:goSet('scanner')},
    {t:'Settings: Session / auth headers',kw:'cookie token authorization bearer login macro',run:goSet('session')},
    {t:'Settings: Project & data — export, import, retention',kw:'export import har json switch project data retention delete purge gc reclaim space',run:goSet('project')},
    {t:'Settings: API & MCP',kw:'keys tokens rest mcp reference',run:goSet('api')},
    {t:'Shortcuts',kw:'help cheatsheet keys hotkeys',run:()=>openModal($('#shortcutsModal'))},
  ];
}
function cmdkRender(){
  const q=cmdk.input.value.trim().toLowerCase();
  const items=[];
  cmdkCommands().forEach(c=>{if(!q||(c.t+' '+(c.kw||'')).toLowerCase().includes(q))items.push({label:c.t,kind:'command',run:c.run});});
  if(q){
    const idQ=q.replace(/^#/,'').replace(/^id:/,'');
    const idWant=/^\d+$/.test(idQ)?Number(idQ):0;
    state.flows.filter(f=>{
      if(idWant&&f.id===idWant)return true;
      return (f.method+' '+f.host+f.path+' #'+f.id).toLowerCase().includes(q);
    }).slice(0,8).forEach(f=>{
      items.push({label:f.method+'  '+f.host+f.path,kind:'flow',sub:String(f.status||'—'),
        run:()=>{document.querySelector('.tab[data-tab="proxy"]').click();selectFlow(f.id);}});
    });
  }
  cmdk.items=items;cmdk.sel=0;cmdkPaint();
}
function cmdkPaint(){
  cmdk.list.innerHTML=cmdk.items.map((it,i)=>
    '<div class="cmdk-row" id="cmdkOpt'+i+'" role="option" aria-selected="'+(i===cmdk.sel?'true':'false')+'" data-i="'+i+'" style="display:flex;justify-content:space-between;gap:12px;padding:9px 12px;border-radius:8px;cursor:pointer;'+(i===cmdk.sel?'background:var(--accent);color:var(--onAccent)':'')+'">'
    +'<span style="overflow:hidden;text-overflow:ellipsis;white-space:nowrap">'+esc(it.label)+'</span>'
    +'<span style="opacity:.55;font-size:11px;flex:none">'+esc(it.sub||it.kind)+'</span></div>'
  ).join('')||'<div style="padding:14px;color:var(--fg3)">No matches</div>';
  if(cmdk.items.length)cmdk.input.setAttribute('aria-activedescendant','cmdkOpt'+cmdk.sel);
  else cmdk.input.removeAttribute('aria-activedescendant');
  cmdk.list.querySelectorAll('.cmdk-row').forEach(r=>{
    r.onclick=()=>cmdkRun(Number(r.dataset.i));
    r.onmousemove=()=>{const n=Number(r.dataset.i);if(n!==cmdk.sel){cmdk.sel=n;cmdkPaint();}};
  });
  const cur=cmdk.list.querySelector('.cmdk-row[data-i="'+cmdk.sel+'"]');if(cur)cur.scrollIntoView({block:'nearest'});
}
function cmdkRun(i){const it=cmdk.items[i];if(!it)return;cmdkClose();try{it.run();}catch(e){toast(e.message);}}
function cmdkOpen(){if(!cmdk.el)cmdkBuild();cmdk.open=true;cmdk.input.value='';cmdkRender();openModal(cmdk.el,{initialFocus:cmdk.input,onEscape:cmdkClose,onDismiss:cmdkClose});}
function cmdkClose(){if(!cmdk.open)return;cmdk.open=false;closeModal(cmdk.el);}

/* ---- global keyboard shortcuts ---- */
function selectedFlow(){return state.selId?state.flows.find(x=>x.id===state.selId):null;}
function activePanel(){const p=document.querySelector('.panel.active');return p?p.dataset.panel:'';}
function hasAnyModifier(e){return !!(e.ctrlKey||e.metaKey||e.altKey||e.shiftKey);}
function exactModifiers(e,{mod=false,shift=false,alt=false}={}){
  return !!(e.ctrlKey||e.metaKey)===mod&&!!e.shiftKey===shift&&!!e.altKey===alt;
}
function isModShortcut(e,key){return e.key.toLowerCase()===key.toLowerCase()&&exactModifiers(e,{mod:true});}
function isPlainShortcut(e,key,{shift=false}={}){return e.key.toLowerCase()===key.toLowerCase()&&exactModifiers(e,{shift});}
function isHelpShortcut(e){return e.key==='?'&&!e.ctrlKey&&!e.metaKey&&!e.altKey;}
function isTypingTarget(t){
  const tag=(t?.tagName||'').toLowerCase();
  return tag==='input'||tag==='textarea'||tag==='select'||!!t?.isContentEditable
    ||!!t?.closest?.('[role="combobox"],[role="listbox"]');
}
function workflowShortcutBlocked(){return MODAL_IDS.some(id=>{const m=$('#'+id);return m&&m.style.display==='flex';});}
// Flow send shortcuts apply only where History selection is the focus — not Settings, Repeater, etc.
function flowSendShortcutAllowed(){return activePanel()==='proxy';}
const GO_MNEMONICS={o:'autopwn',p:'proxy',i:'intercept',r:'repeater',u:'intruder',s:'scanner',m:'map',f:'findings',n:'notes',a:'activity',t:'settings'};
let gotoPending=false,gotoTimer=null;
function resetGoto(){gotoPending=false;clearTimeout(gotoTimer);}
document.addEventListener('keydown',e=>{
  const typing=isTypingTarget(e.target);
  if(gotoPending&&(typing||hasAnyModifier(e)))resetGoto();
  if(isModShortcut(e,'k')){e.preventDefault();cmdk.open?cmdkClose():cmdkOpen();return;}
  if(cmdk.open)return; // the palette handles its own keys
  if(e.key==='Escape'){resetGoto();return;}
  if(typing)return;
  if(isHelpShortcut(e)){e.preventDefault();openModal($('#shortcutsModal'));return;} // ?: keyboard cheatsheet
  if(workflowShortcutBlocked())return;
  if(gotoPending){
    const panel=isPlainShortcut(e,e.key)?GO_MNEMONICS[e.key.toLowerCase()]:null;
    resetGoto();
    if(panel){e.preventDefault();document.querySelector('.tab[data-tab="'+panel+'"]')?.click();}
    return;
  }
  if(isPlainShortcut(e,'g')){
    e.preventDefault();gotoPending=true;gotoTimer=setTimeout(resetGoto,1200);return;
  }
  if(activePanel()==='repeater'&&isModShortcut(e,'Enter')){e.preventDefault();repSend();return;}
  if(flowSendShortcutAllowed()&&(isPlainShortcut(e,'r')||isPlainShortcut(e,'i'))){
    const f=selectedFlow();
    if(f){e.preventDefault();(e.key==='r'?sendToRepeater:sendToIntruder)(f);}
    return;
  }
  if(activePanel()==='intercept'&&(isPlainShortcut(e,'f')||isPlainShortcut(e,'d'))){
    if(state.heldSel){e.preventDefault();$(e.key==='d'?'#dropBtn':'#forwardBtn').click();}
    return;
  }
  if(activePanel()==='proxy'&&isPlainShortcut(e,'/')){const s=$('#fSearch');if(s){e.preventDefault();s.focus();}return;} // /: focus search
});
$('#scClose').onclick=()=>closeModal($('#shortcutsModal'));

/* ---- project badge → Projects picker (switch / create) ---- */
{const pb=$('#projBadge');if(pb){
  pb.addEventListener('click',openProjectModal);
  pb.addEventListener('keydown',e=>{if(e.key==='Enter'||e.key===' '){e.preventDefault();openProjectModal();}});
}}

/* ---- version / update check ---- */
async function loadVersion(retry){
  try{
    const d=await api('/api/version');const el=$('#verBadge');if(!el)return;
    const pb=$('#projBadge');if(pb&&d.project){pb.style.display='inline-block';pb.textContent='◧ '+d.project;pb.title='Active project: '+d.project+(d.projectDir?'\n'+d.projectDir:'');}
    const pdh=$('#projDirHint');if(pdh&&d.projectDir)pdh.textContent=d.projectDir;
    if(d.updateAvailable&&d.latest){
      el.textContent='↑ v'+d.latest+' available';el.style.color='var(--accent)';el.style.fontWeight='700';
      el.title='You have v'+(d.version||'?')+' — a newer release is available. Click for releases.';
    }else{
      el.textContent='v'+(d.version||'');el.style.color='var(--fg3)';el.style.fontWeight='';
      el.title='Interseptor v'+(d.version||'');
    }
    if(!d.latest&&retry)setTimeout(()=>loadVersion(false),3500); // the server's update check may still be in flight
  }catch(e){}
}

/* ---- theme ---- */
function currentTheme(){return document.documentElement.getAttribute('data-theme')==='light'?'light':'dark';}
function applyTheme(t){
  if(t==='light')document.documentElement.setAttribute('data-theme','light');
  else document.documentElement.removeAttribute('data-theme');
  const b=$('#themeToggle');if(b)b.textContent=t==='light'?'☀':'☾';
}
function toggleTheme(){const t=currentTheme()==='light'?'dark':'light';try{localStorage.setItem('theme',t);}catch(e){}applyTheme(t);}
$('#themeToggle').onclick=toggleTheme;
applyTheme(currentTheme()); // sync the button icon with the theme applied pre-paint

{const tlsBanner=$('#tlsShowBanner');
if(tlsBanner){
  syncTlsBannerSetting();
  tlsBanner.onchange=()=>{setTlsBannerHidden(!tlsBanner.checked);loadTrafficDiagnosis();};
}}

/* ---- boot ---- */
async function refreshIntercept(){try{state.intercept=await api('/api/intercept');renderIntercept();}catch(e){}}
// Resolve the active project before Repeater/Intruder tab init so localStorage
// keys are project-scoped (#17/#18). Other boot work can proceed in parallel.
async function bootProjectScopedUI(){
  try{
    const d=await api('/api/version');
    setStorageProject(d.project||'default');
  }catch(e){ setStorageProject('default'); }
  repInit();
  intrInit();
}
renderChips();loadSettings();loadSysProxy();loadAndroid();loadIOS();loadIOSSsh();loadSession();loadFlows();loadTrafficDiagnosis();loadRules();loadScope();loadViews();refreshIntercept().then(()=>renderIcptStat());bootProjectScopedUI();loadIssues();loadActivity();loadProject();loadVersion(true);loadHumanInput();loadFindings();loadTags();connectEvents();restoreTab();
// First-run setup wizard: shown once after the initial flow load, unless the
// user already completed/skipped it or already has captured traffic.
setTimeout(()=>{ if(state.flows && !state.flows.length) maybeShowSetup(); }, 600);
{const cb=$('#cmdkBtn');if(cb)cb.onclick=()=>cmdkOpen();}
{const ab=$('#askAiBtn');if(ab)ab.onclick=()=>openAi({project:true});}
