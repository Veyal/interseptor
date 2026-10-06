// cmdk.js — the command palette (Ctrl/Cmd+K), extracted from app.js with its
// behaviour unchanged. The palette NAVIGATES: it jumps to a tab, a Settings
// subsection or a tool screen, plus non-destructive conveniences and workflow
// entries that only OPEN a dialog. It never performs a mutating action (run a
// scan, toggle intercept, send or delete a request), so a mis-typed Enter
// cannot do anything destructive.
//
// app.js owns the base command list and the readiness/modal guards and hands
// them over through configureCommandPalette(); other modules add entries with
// registerCommand() from shell-hooks.js. Styling is class-based (surfaces.css).
import { esc, state, openModal, closeModal, toast, toastError } from './core.js';
import { listCommands } from './shell-hooks.js';

const cmdk = { el: null, input: null, list: null, items: [], sel: 0, open: false };
const cmdkEnv = { commands: () => [], ready: () => true, blocked: () => false, openFlow: () => {} };

export function configureCommandPalette(env) { Object.assign(cmdkEnv, env); }
export function isCommandPaletteOpen() { return cmdk.open; }
export function toggleCommandPalette() { if (cmdk.open) cmdkClose(); else cmdkOpen(); }

function cmdkBuild(){
  const o=document.createElement('div');o.id='cmdk';o.className='modal-overlay cmdk-overlay';
  o.innerHTML='<div role="dialog" aria-modal="true" aria-labelledby="cmdkTitle" class="modal-shell cmdk-shell">'
    +'<div class="modal-shell-head"><span id="cmdkTitle" class="modal-shell-title">Command palette</span></div>'
    +'<input id="cmdkInput" class="cmdk-input" role="combobox" aria-label="Search commands and flows" aria-controls="cmdkList" aria-expanded="true" aria-autocomplete="list" placeholder="Search flows · jump to a tab · run a command…" autocomplete="off" spellcheck="false">'
    +'<div id="cmdkList" class="cmdk-list" role="listbox" aria-label="Command results"></div>'
    +'<div class="cmdk-foot"><span>↑ ↓ navigate</span><span>⏎ run</span><span>esc close</span></div></div>';
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
function cmdkRender(){
  const q=cmdk.input.value.trim().toLowerCase();
  const items=[];
  [...cmdkEnv.commands(),...listCommands()].forEach(c=>{if(!q||(c.t+' '+(c.kw||'')).toLowerCase().includes(q))items.push({label:c.t,kind:'command',run:c.run});});
  if(q){
    const idQ=q.replace(/^#/,'').replace(/^id:/,'');
    const idWant=/^\d+$/.test(idQ)?Number(idQ):0;
    state.flows.filter(f=>{
      if(idWant&&f.id===idWant)return true;
      return (f.method+' '+f.host+f.path+' #'+f.id).toLowerCase().includes(q);
    }).slice(0,8).forEach(f=>{
      items.push({label:f.method+'  '+f.host+f.path,kind:'flow',sub:String(f.status||'—'),
        run:()=>cmdkEnv.openFlow(f)});
    });
  }
  cmdk.items=items;cmdk.sel=0;cmdkPaint();
}
function cmdkPaint(){
  cmdk.list.innerHTML=cmdk.items.map((it,i)=>
    '<div class="cmdk-row" id="cmdkOpt'+i+'" role="option" aria-selected="'+(i===cmdk.sel?'true':'false')+'" data-i="'+i+'">'
    +'<span class="cmdk-text">'+esc(it.label)+'</span>'
    +'<span class="cmdk-sub">'+esc(it.sub||it.kind)+'</span></div>'
  ).join('')||'<div class="cmdk-empty">No matches</div>';
  if(cmdk.items.length)cmdk.input.setAttribute('aria-activedescendant','cmdkOpt'+cmdk.sel);
  else cmdk.input.removeAttribute('aria-activedescendant');
  cmdk.list.querySelectorAll('.cmdk-row').forEach(r=>{
    r.onclick=()=>cmdkRun(Number(r.dataset.i));
    r.onmousemove=()=>{const n=Number(r.dataset.i);if(n!==cmdk.sel){cmdk.sel=n;cmdkPaint();}};
  });
  const cur=cmdk.list.querySelector('.cmdk-row[data-i="'+cmdk.sel+'"]');if(cur)cur.scrollIntoView({block:'nearest'});
}
function cmdkRun(i){const it=cmdk.items[i];if(!it)return;cmdkClose();try{it.run();}catch(e){toastError('Command failed',e);}}
export function cmdkOpen(){if(cmdkEnv.blocked())return;if(!cmdkEnv.ready()){toast('Loading saved workspace…');return;}if(!cmdk.el)cmdkBuild();cmdk.open=true;cmdk.input.value='';cmdkRender();openModal(cmdk.el,{initialFocus:cmdk.input,onEscape:cmdkClose,onDismiss:cmdkClose});}
export function cmdkClose(){if(!cmdk.open)return;cmdk.open=false;closeModal(cmdk.el);}
