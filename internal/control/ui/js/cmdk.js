// cmdk.js — the command palette v2 (Ctrl/Cmd+K). Fuzzy-ranked groups (Go to,
// Actions, Flows, Findings, Settings, Dialogs, Shortcuts), recents and prefix
// modes: `>` actions on the selection, `f:` flows, `#` findings, `@` identity,
// `?` shortcuts. The palette NAVIGATES, opens dialogs and performs a few
// reversible toggles (scope, single-key shortcuts); it never sends, deletes or
// scans, so a mis-typed Enter cannot do anything destructive.
//
// Pure ranking and assembly live in cmdk-logic.js (node-tested); the extra
// entries in cmdk-actions.js; the shared key registry and the generated
// shortcut sheet in keyboard.js. app.js owns the base command list and the
// readiness/modal guards and hands them over through configureCommandPalette();
// other modules add entries with registerCommand() from shell-hooks.js.
// Styling is class-based (surfaces.css); phones get a full-height sheet shape
// from the same markup.
import { esc, state, api, $, openModal, closeModal, toast, toastError } from './core.js';
import { listCommands } from './shell-hooks.js';
import { projectState } from './project-state.js';
import { buildResults, dedupeCommands, flattenResults, flattenCheatsheet, resultCountText, modeHint, parseQuery, loadRecents, saveRecents, pushRecent } from './cmdk-logic.js';
import { builtinActions, selectionActions, openFlowFor, openFindingRoute, setIdentity } from './cmdk-actions.js';
import { shortcutSheet, mountShortcutSheet } from './keyboard.js';

const cmdk = { el: null, input: null, list: null, hint: null, live: null, items: [], res: null, sel: 0, open: false, prefill: '', findings: null, findingsBusy: false, remote: [], remoteQ: '', keepRemote: false, flowTimer: 0, flowEpoch: 0, liveTimer: 0, recents: null };
const cmdkEnv = { commands: () => [], ready: () => true, blocked: () => false, openFlow: () => {} };
const FLOW_SCAN_CAP = 3000;

export function configureCommandPalette(env) { Object.assign(cmdkEnv, env); }
export function isCommandPaletteOpen() { return cmdk.open; }
export function toggleCommandPalette() { if (cmdk.open) cmdkClose(); else cmdkOpen(); }
// Opens with text already in the field, e.g. cmdkOpenWith('f:') for flows.
export function cmdkOpenWith(text) { cmdk.prefill = String(text || ''); cmdkOpen(); }

function cmdkBuild(){
  const o=document.createElement('div');o.id='cmdk';o.className='modal-overlay cmdk-overlay';
  o.innerHTML='<div role="dialog" aria-modal="true" aria-labelledby="cmdkTitle" class="modal-shell cmdk-shell">'
    +'<div class="modal-shell-head"><span id="cmdkTitle" class="modal-shell-title">Command palette</span></div>'
    +'<input id="cmdkInput" class="cmdk-input" role="combobox" aria-label="Search commands, flows and findings" aria-controls="cmdkList" aria-expanded="false" aria-autocomplete="list" aria-describedby="cmdkHint" placeholder="Search flows, findings, actions…" autocomplete="off" autocapitalize="off" spellcheck="false" enterkeyhint="go">'
    +'<div id="cmdkHint" class="cmdk-hint"></div>'
    +'<div id="cmdkList" class="cmdk-list" role="listbox" aria-label="Command results"></div>'
    +'<div id="cmdkLive" class="u-sr" role="status" aria-live="polite" aria-atomic="true"></div>'
    +'<div class="cmdk-foot"><span>↑ ↓ navigate</span><span>⏎ run</span><span>esc close</span><span class="cmdk-modes"><kbd>&gt;</kbd> selection <kbd>f:</kbd> flows <kbd>#</kbd> findings <kbd>@</kbd> identity <kbd>?</kbd> keys</span></div></div>';
  document.body.appendChild(o);
  cmdk.el=o;cmdk.input=o.querySelector('#cmdkInput');cmdk.list=o.querySelector('#cmdkList');cmdk.hint=o.querySelector('#cmdkHint');cmdk.live=o.querySelector('#cmdkLive');
  cmdk.input.oninput=cmdkRender;
  cmdk.input.onkeydown=e=>{
    if(e.key==='ArrowDown'){e.preventDefault();cmdk.sel=Math.min(cmdk.items.length-1,cmdk.sel+1);cmdkPaint();}
    else if(e.key==='ArrowUp'){e.preventDefault();cmdk.sel=Math.max(0,cmdk.sel-1);cmdkPaint();}
    else if(e.key==='Home'&&!cmdk.input.value){e.preventDefault();cmdk.sel=0;cmdkPaint();}
    else if(e.key==='End'&&!cmdk.input.value){e.preventDefault();cmdk.sel=Math.max(0,cmdk.items.length-1);cmdkPaint();}
    else if(e.key==='Enter'){e.preventDefault();cmdkRun(cmdk.sel,{shift:e.shiftKey});}
    else if(e.key==='Escape'){e.preventDefault();e.stopPropagation();cmdkClose();}
  };
}
function cmdkSources(mode,query){
  const base=dedupeCommands(cmdkEnv.commands(),listCommands(),builtinActions());
  const local=(state.flows||[]).slice(0,FLOW_SCAN_CAP);
  const have=new Set(local.map(f=>f.id));
  const flows=cmdk.remoteQ===query?local.concat(cmdk.remote.filter(f=>f&&!have.has(f.id))):local;
  const ps=projectState.get();
  return {commands:base,flows,findings:cmdk.findings||[],identities:ps.identities||[],activeIdentity:ps.activeIdentity,
    selectionActions:mode==='actions'?selectionActions():[],shortcuts:mode==='shortcuts'?flattenCheatsheet(shortcutSheet()):[],
    recents:cmdk.recents||(cmdk.recents=loadRecents())};
}
function cmdkNeedsFindings(mode,query){return mode==='findings'||(mode==='all'&&query.length>=1);}
function cmdkLoadFindings(){
  if(cmdk.findings||cmdk.findingsBusy)return;
  cmdk.findingsBusy=true;
  api('/api/findings').then(d=>{cmdk.findings=Array.isArray(d&&d.findings)?d.findings:[];}).catch(()=>{cmdk.findings=[];}).finally(()=>{cmdk.findingsBusy=false;if(cmdk.open)cmdkRerender();});
}
function cmdkLoadRemoteFlows(mode,query){
  clearTimeout(cmdk.flowTimer);
  if(mode!=='flows'||query.length<2||/^(?:id:|#)?\d+$/i.test(query)){cmdk.remote=[];cmdk.remoteQ='';return;}
  const mine=++cmdk.flowEpoch;
  cmdk.flowTimer=setTimeout(()=>{
    api('/api/flows?limit=20&search='+encodeURIComponent(query)).then(d=>{
      if(mine!==cmdk.flowEpoch||!cmdk.open)return;
      cmdk.remote=Array.isArray(d&&d.flows)?d.flows:[];cmdk.remoteQ=query;cmdkRerender();
    }).catch(()=>{});
  },250);
}
function cmdkRerender(){cmdk.keepRemote=true;cmdkRender();}
function cmdkRender(){
  const raw=cmdk.input.value;
  const {mode,query}=parseQuery(raw);
  if(!cmdk.keepRemote)cmdkLoadRemoteFlows(mode,query);
  cmdk.keepRemote=false;
  if(cmdkNeedsFindings(mode,query))cmdkLoadFindings();
  const res=buildResults({raw,...cmdkSources(mode,query)});
  cmdk.res=res;cmdk.items=flattenResults(res);cmdk.sel=0;
  cmdk.hint.textContent=modeHint(mode,{hasSelection:!!(state.selected&&state.selected.size)||!!state.selId});
  cmdkPaint();cmdkAnnounce(res);
}
function cmdkAnnounce(res){
  clearTimeout(cmdk.liveTimer);
  cmdk.liveTimer=setTimeout(()=>{if(cmdk.live)cmdk.live.textContent=resultCountText(res.total,res.mode);},350);
}
function cmdkPaint(){
  let n=0,html='';
  (cmdk.res?cmdk.res.groups:[]).forEach((g,gi)=>{
    html+='<div role="group" aria-labelledby="cmdkGrp'+gi+'"><div id="cmdkGrp'+gi+'" class="cmdk-group">'+esc(g.group)+'</div>';
    g.items.forEach(it=>{
      html+='<div class="cmdk-row" id="cmdkOpt'+n+'" role="option" aria-selected="'+(n===cmdk.sel?'true':'false')+'" data-i="'+n+'">'
        +'<span class="cmdk-text">'+esc(it.label)+'</span>'
        +'<span class="cmdk-sub">'+esc(it.sub||it.kind)+'</span></div>';
      n++;
    });
    html+='</div>';
  });
  cmdk.list.innerHTML=html||'<div class="cmdk-empty">No matches</div>';
  cmdk.input.setAttribute('aria-expanded',cmdk.items.length?'true':'false');
  if(cmdk.items.length)cmdk.input.setAttribute('aria-activedescendant','cmdkOpt'+cmdk.sel);
  else cmdk.input.removeAttribute('aria-activedescendant');
  cmdk.list.querySelectorAll('.cmdk-row').forEach(r=>{
    r.onclick=e=>cmdkRun(Number(r.dataset.i),{shift:e.shiftKey});
    r.onmousemove=()=>{const k=Number(r.dataset.i);if(k!==cmdk.sel){cmdk.sel=k;cmdkSelectOnly();}};
  });
  const cur=cmdk.list.querySelector('.cmdk-row[data-i="'+cmdk.sel+'"]');if(cur)cur.scrollIntoView({block:'nearest'});
}
// Hover only moves the selection; repainting the list would swallow the click.
function cmdkSelectOnly(){
  cmdk.list.querySelectorAll('.cmdk-row').forEach(r=>r.setAttribute('aria-selected',Number(r.dataset.i)===cmdk.sel?'true':'false'));
  cmdk.input.setAttribute('aria-activedescendant','cmdkOpt'+cmdk.sel);
}
function cmdkRun(i,opts){const it=cmdk.items[i];if(!it)return;
  if(it.kind==='command'&&typeof it.ref.prefill==='string'){cmdk.input.value=it.ref.prefill;cmdk.input.focus();cmdkRender();return;}
  cmdkClose();try{cmdkExec(it,opts||{});}catch(e){toastError('Command failed',e);}}
function cmdkExec(it,opts){
  if(it.kind==='command'){cmdk.recents=pushRecent(cmdk.recents||loadRecents(),it.ref.t);saveRecents(cmdk.recents);it.ref.run();}
  else if(it.kind==='flow')openFlowFor(it.ref,{toRepeater:!!opts.shift,fallback:cmdkEnv.openFlow});
  else if(it.kind==='finding')openFindingRoute(it.ref.id);
  else if(it.kind==='identity')setIdentity(it.ref.name);
  else if(it.kind==='shortcut')openModal($('#shortcutsModal'));
}
export function cmdkOpen(){if(cmdkEnv.blocked())return;if(!cmdkEnv.ready()){toast('Loading saved workspace…');return;}if(!cmdk.el)cmdkBuild();cmdk.open=true;cmdk.input.value=cmdk.prefill||'';cmdk.prefill='';cmdkRender();openModal(cmdk.el,{initialFocus:cmdk.input,onEscape:cmdkClose,onDismiss:cmdkClose});}
export function cmdkClose(){if(!cmdk.open)return;cmdk.open=false;clearTimeout(cmdk.flowTimer);clearTimeout(cmdk.liveTimer);cmdk.findings=null;cmdk.remote=[];cmdk.remoteQ='';closeModal(cmdk.el);}

mountShortcutSheet();
