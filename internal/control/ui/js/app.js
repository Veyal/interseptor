// app.js — entry module and glue. Imports every feature module (which wires its
// own DOM handlers on load), then owns the cross-cutting pieces: tab switching,
// the command palette, global keyboard shortcuts, the live SSE event stream,
// theme, the version badge, and the boot sequence that kicks everything off.
import { $, $$, esc, state, api, toast, MODAL_IDS, openModal, closeModal } from './core.js';
import { selectFlow, renderChips, loadFlows, loadScope, loadViews, scheduleReload, renderWSFrames, clearAllFilters, walkFlowNav, toggleSelectAllShown, handleFlowNew, handleFlowUpdate } from './proxy.js';
import { renderIntercept, toggleIntercept, loadRules } from './intercept.js';
import { repInit, repSend, sendToRepeater, sendToIntruder, scheduleIntr } from './tools.js';
import { loadIssues, runScan, loadScanTargets, openActive, openDecoder, openChecks, loadActive, loadChecksList, loadOob, renderAsScopePanel } from './scanner.js';
import { loadEndpoints } from './map.js';
import { loadDiscovery, refreshDiscovery } from './discovery.js';
import { loadSettings, loadSysProxy, loadAndroid, loadSession, loadProject, openProjectModal, applyAiDisabledUI, applyOobDisabledUI } from './settings.js';
import { loadNotes, flushNotesSave, focusNotes, organizeNotes } from './notes.js';
import { renderActivity, onActivity, loadActivity, clearActSeen } from './activity.js';
import { loadFindings } from './findings.js';
import { loadTags } from './tags.js';
import { loadHumanInput } from './humaninput.js';
import './flowmodal.js'; // side-effect: flow inspect popup + modal handlers
import './ai.js'; // side-effect: wires the AI assist modal (its openAi is also imported by proxy.js)
import './authz.js'; // side-effect: wires authz modal buttons
import { openAuthz, renderAuthzScopePanel } from './authz.js';
import { maybeShowSetup } from './setup.js';

/* ---- tabs ---- */
function activateTab(t){
  const prev=$('.panel.active');
  if(prev&&prev.dataset.panel==='notes')flushNotesSave();
  const tabs=$$('.tab');
  tabs.forEach(x=>{x.classList.remove('active');x.setAttribute('aria-selected','false');x.tabIndex=-1;});
  t.classList.add('active');t.setAttribute('aria-selected','true');t.tabIndex=0;
  $$('.panel').forEach(p=>p.classList.toggle('active',p.dataset.panel===t.dataset.tab));
  try{localStorage.setItem('tab',t.dataset.tab);}catch(e){} // remember the open tab across refresh
  if(t.dataset.tab==='activity'){renderActivity();clearActSeen();}
  if(t.dataset.tab==='scanner')loadScanTargets();
  if(t.dataset.tab==='findings')loadFindings();
  if(t.dataset.tab==='discover')loadDiscovery();
  if(t.dataset.tab==='map')loadEndpoints();
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
// Roving arrow-key navigation within the tablist (ARIA tablist pattern).
$('#tabs').addEventListener('keydown',e=>{
  if(e.key!=='ArrowLeft'&&e.key!=='ArrowRight')return;
  const tabs=$$('.tab');
  const idx=tabs.indexOf(document.activeElement);
  if(idx<0)return;
  e.preventDefault();
  const next=e.key==='ArrowRight'?tabs[(idx+1)%tabs.length]:tabs[(idx-1+tabs.length)%tabs.length];
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
    if(t&&/^(INPUT|TEXTAREA|SELECT)$/.test(t.tagName))return;
    e.preventDefault();toggleSelectAllShown();return;
  }
  if(e.key!=='ArrowDown'&&e.key!=='ArrowUp'&&e.key!=='j'&&e.key!=='k')return;
  if(!p||!p.classList.contains('active'))return;
  const t=e.target;
  if(t&&/^(INPUT|TEXTAREA|SELECT)$/.test(t.tagName))return;
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
  if(!capLast){ s.textContent='· waiting for traffic'; return; }
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
    loadEndpoints();
  },2000);
}

/* ---- live events ---- */
function connectEvents(){
  const es=new EventSource('/api/events');
  es.onmessage=e=>{let m;try{m=JSON.parse(e.data);}catch(err){return;}
    if(m.type==='flow.new'){if(m.flow)handleFlowNew(m.flow);else scheduleReload();onCapture();scheduleMapRefresh();}
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
    else if(m.type==='checks.update'){if($('#checksModal')&&$('#checksModal').style.display==='flex')loadChecksList();}
    else if(m.type==='activescan.update'){if($('#activeModal')&&$('#activeModal').style.display==='flex')loadActive();}
    else if(m.type==='oob.update'){if($('#oobModal')&&$('#oobModal').style.display==='flex')loadOob();}
    else if(m.type==='discovery.update'){if(document.querySelector('.tab[data-tab="discover"]').classList.contains('active'))refreshDiscovery();}
    else if(m.type==='settings.update'){loadSettings();applyAiDisabledUI();applyOobDisabledUI();}
    else if(m.type==='notes.update')loadNotes();
    else if(m.type==='findings.update')loadFindings();
    else if(m.type==='tags.update')loadTags();
    else if(m.type==='human.input')loadHumanInput();
  };
  es.onerror=()=>{/* browser auto-reconnects */};
}

/* ---- command palette (Ctrl/Cmd+K) ---- */
const cmdk={el:null,input:null,list:null,items:[],sel:0,open:false};
function cmdkBuild(){
  const o=document.createElement('div');o.id='cmdk';
  o.style.cssText='position:fixed;inset:0;z-index:300;display:none;align-items:flex-start;justify-content:center;background:var(--overlay)';
  o.innerHTML='<div style="margin-top:11vh;width:min(680px,92vw);background:var(--bg2);border:1px solid var(--line);border-radius:12px;box-shadow:0 24px 70px var(--shadow);overflow:hidden">'
    +'<input id="cmdkInput" placeholder="Search flows · jump to a tab · run a command…" autocomplete="off" spellcheck="false" style="width:100%;box-sizing:border-box;padding:14px 16px;border:0;border-bottom:1px solid var(--line);background:transparent;color:var(--fg);font-size:15px;outline:none">'
    +'<div id="cmdkList" style="max-height:52vh;overflow:auto;padding:6px"></div>'
    +'<div style="padding:7px 14px;border-top:1px solid var(--line);color:var(--fg3);font-size:10px;display:flex;gap:16px"><span>↑ ↓ navigate</span><span>⏎ run</span><span>esc close</span></div></div>';
  document.body.appendChild(o);
  cmdk.el=o;cmdk.input=o.querySelector('#cmdkInput');cmdk.list=o.querySelector('#cmdkList');
  o.onclick=e=>{if(e.target===o)cmdkClose();};
  cmdk.input.oninput=cmdkRender;
  cmdk.input.onkeydown=e=>{
    if(e.key==='ArrowDown'){e.preventDefault();cmdk.sel=Math.min(cmdk.items.length-1,cmdk.sel+1);cmdkPaint();}
    else if(e.key==='ArrowUp'){e.preventDefault();cmdk.sel=Math.max(0,cmdk.sel-1);cmdkPaint();}
    else if(e.key==='Enter'){e.preventDefault();cmdkRun(cmdk.sel);}
    else if(e.key==='Escape'){e.preventDefault();cmdkClose();}
  };
}
// The palette only NAVIGATES — it jumps to a tab, a Settings subsection, or a tool
// screen. It deliberately never performs a mutating action (run a scan, toggle
// intercept, export, send a request) so a mis-typed Enter can't do anything
// destructive; you act from the screen it takes you to. `kw` adds search aliases.
function cmdkCommands(){
  const go=name=>()=>document.querySelector('.tab[data-tab="'+name+'"]').click();
  const goSet=sec=>()=>{document.querySelector('.tab[data-tab="settings"]').click();const b=document.querySelector('#setNav button[data-sec="'+sec+'"]');if(b)b.click();};
  return [
    {t:'Go to Proxy History',kw:'proxy history flows requests traffic inspect captured',run:go('proxy')},
    {t:'Go to Intercept',kw:'hold forward drop match replace rules',run:go('intercept')},
    {t:'Go to Repeater',kw:'resend craft edit request',run:go('repeater')},
    {t:'Go to Intruder',kw:'fuzz brute force payloads enumerate',run:go('intruder')},
    {t:'Go to Scanner',kw:'passive active scan checks issues vulnerabilities report',run:go('scanner')},
    {t:'Go to Findings',kw:'findings poc vulnerability record curated impact',run:go('findings')},
    {t:'Go to Discover',kw:'content discovery forced browse brute force dirbuster gobuster ffuf wordlist directories endpoints fuzz paths',run:go('discover')},
    {t:'Go to Map',kw:'endpoints attack surface graph tree headers body search',run:go('map')},
    {t:'Go to Notes',kw:'scratchpad markdown findings notebook',run:goToNotes},
    {t:'Edit custom scanner checks',kw:'starlark checks passive custom rules',run:openChecks},
    {t:'Open active scan',kw:'active attack payloads consent arm fuzz',run:openActive},
    ...(state.oobEnabled?[{t:'Open OOB catcher',kw:'out of band blind ssrf callback collab',run:()=>{openModal($('#oobModal'));loadOob();}}]:[]),
    ...(state.aiDisabled?[]:[{t:'Organize project notes with AI',kw:'notes structure sort clean headings findings todo',run:()=>{goToNotes();organizeNotes();}}]),
    {t:'Open Authz test',kw:'authorization access control roles identity',run:()=>{const f=selectedFlow();if(f)openAuthz(f.id);else toast('select a flow in History first');}},
    ...(state.aiDisabled?[]:[{t:'Go to Activity',kw:'ai mcp glass box agent log',run:go('activity')}]),
    {t:'Settings: API & MCP',kw:'keys tokens rest mcp reference',run:goSet('api')},
    {t:'Settings: Proxy & network',kw:'listener bind port upstream system proxy capture browser telemetry',run:goSet('proxy')},
    {t:'Settings: TLS / CA — download CA certificate',kw:'https certificate cert trust install ca download mitm',run:goSet('tls')},
    {t:'Settings: Target scope',kw:'include exclude host path in scope',run:goSet('scope')},
    {t:'Settings: AI assist — provider & API key',kw:'anthropic openrouter model api key llm',run:goSet('ai')},
    {t:'Settings: Session / auth headers',kw:'cookie token authorization bearer login',run:goSet('session')},
    {t:'Settings: Scanner & OOB',kw:'scanner oob passive active checks enable',run:goSet('scanner')},
    {t:'Settings: Project & data — export, import, retention',kw:'export import har json switch project data retention delete purge gc reclaim space',run:goSet('project')},
    {t:'Open Decoder (base64 / url / jwt / hex…)',kw:'encode decode smart',run:()=>openDecoder()},
    {t:'Keyboard shortcuts',kw:'help cheatsheet keys hotkeys',run:()=>openModal($('#shortcutsModal'))},
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
    '<div class="cmdk-row" data-i="'+i+'" style="display:flex;justify-content:space-between;gap:12px;padding:9px 12px;border-radius:8px;cursor:pointer;'+(i===cmdk.sel?'background:var(--accent);color:var(--onAccent)':'')+'">'
    +'<span style="overflow:hidden;text-overflow:ellipsis;white-space:nowrap">'+esc(it.label)+'</span>'
    +'<span style="opacity:.55;font-size:11px;flex:none">'+esc(it.sub||it.kind)+'</span></div>'
  ).join('')||'<div style="padding:14px;color:var(--fg3)">No matches</div>';
  cmdk.list.querySelectorAll('.cmdk-row').forEach(r=>{
    r.onclick=()=>cmdkRun(Number(r.dataset.i));
    r.onmousemove=()=>{const n=Number(r.dataset.i);if(n!==cmdk.sel){cmdk.sel=n;cmdkPaint();}};
  });
  const cur=cmdk.list.querySelector('.cmdk-row[data-i="'+cmdk.sel+'"]');if(cur)cur.scrollIntoView({block:'nearest'});
}
function cmdkRun(i){const it=cmdk.items[i];if(!it)return;cmdkClose();try{it.run();}catch(e){toast(e.message);}}
function cmdkOpen(){if(!cmdk.el)cmdkBuild();cmdk.open=true;cmdk.el.style.display='flex';cmdk.input.value='';cmdkRender();cmdk.input.focus();}
function cmdkClose(){cmdk.open=false;if(cmdk.el)cmdk.el.style.display='none';}

/* ---- global keyboard shortcuts ---- */
function selectedFlow(){return state.selId?state.flows.find(x=>x.id===state.selId):null;}
function activePanel(){const p=document.querySelector('.panel.active');return p?p.dataset.panel:'';}
// Flow send shortcuts apply only where History selection is the focus — not Settings, Repeater, etc.
function flowSendShortcutAllowed(){return activePanel()==='proxy';}
document.addEventListener('keydown',e=>{
  const mod=e.ctrlKey||e.metaKey;
  const tag=(e.target.tagName||'').toLowerCase();
  const typing=tag==='input'||tag==='textarea'||tag==='select'||e.target.isContentEditable;
  if(mod&&e.key.toLowerCase()==='b'){
    e.preventDefault();goToNotes();return;
  }
  if(mod&&e.key.toLowerCase()==='k'){e.preventDefault();cmdk.open?cmdkClose():cmdkOpen();return;}
  if(cmdk.open)return; // the palette handles its own keys
  if(mod&&(e.key===' '||e.code==='Space')){
    const rep=document.querySelector('.panel[data-panel="repeater"]');
    if(!rep||!rep.classList.contains('active'))return;
    e.preventDefault();repSend();return;
  }
  if(mod&&e.key.toLowerCase()==='r'){
    if(typing||!flowSendShortcutAllowed())return;
    const f=selectedFlow();if(f){e.preventDefault();sendToRepeater(f);}return;
  }
  if(!mod&&!typing&&e.key==='r'){
    if(!flowSendShortcutAllowed())return;
    const f=selectedFlow();if(f){e.preventDefault();sendToRepeater(f);}return;
  }
  if(mod&&e.key.toLowerCase()==='i'){
    if(typing||!flowSendShortcutAllowed())return;
    const f=selectedFlow();if(f){e.preventDefault();sendToIntruder(f);}return;
  }
  // Ctrl+Shift+F forward / Ctrl+D drop the selected held item — Intercept tab only
  if(document.querySelector('.tab[data-tab="intercept"]').classList.contains('active')){
    const drop=mod&&!typing&&e.key.toLowerCase()==='d';
    const fwd=mod&&e.shiftKey&&e.key.toLowerCase()==='f';
    if((drop||fwd)&&state.heldSel){e.preventDefault();$(drop?'#dropBtn':'#forwardBtn').click();return;}
  }
  if(e.key==='/'&&!typing&&!mod){const s=$('#fSearch');if(s){e.preventDefault();document.querySelector('.tab[data-tab="proxy"]').click();s.focus();}return;} // /: focus search
  if(e.key==='?'&&!typing&&!mod){e.preventDefault();openModal($('#shortcutsModal'));return;} // ?: keyboard cheatsheet
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
      el.title='Interceptor v'+(d.version||'');
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

/* ---- boot ---- */
async function refreshIntercept(){try{state.intercept=await api('/api/intercept');renderIntercept();}catch(e){}}
renderChips();loadSettings();loadSysProxy();loadAndroid();loadSession();loadFlows();loadRules();loadScope();loadViews();refreshIntercept().then(()=>renderIcptStat());repInit();loadIssues();loadActivity();loadProject();loadVersion(true);loadHumanInput();loadFindings();loadTags();connectEvents();restoreTab();
// First-run setup wizard: shown once after the initial flow load, unless the
// user already completed/skipped it or already has captured traffic.
setTimeout(()=>{ if(state.flows && !state.flows.length) maybeShowSetup(); }, 600);
{const cb=$('#cmdkBtn');if(cb)cb.onclick=()=>cmdkOpen();}
{const hb=$('#helpBtn');if(hb)hb.onclick=()=>openModal($('#shortcutsModal'));}
