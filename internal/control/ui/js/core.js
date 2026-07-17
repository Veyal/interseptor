// core.js — shared foundation imported by every feature module.
// DOM helpers, the global `state` object, the REST helper, formatters, HTTP
// syntax highlighters, copy helpers, the modal system (open/close + focus trap),
// uiPrompt / uiConfirm, and the safe markdown renderer all live here.
//
// Everything is `export`ed; feature modules import what they reference. Mutations
// to the shared `state` object are visible across modules (live object binding).

export const $=s=>document.querySelector(s);
export const $$=s=>Array.from(document.querySelectorAll(s));
export const esc=s=>String(s).replace(/[&<>]/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;'}[c]));
// escAttr also escapes quotes — use it for a value interpolated INTO an attribute
// ("${escAttr(x)}"), where a raw " or ' in user-authored rules or captured traffic
// (hosts, paths) would otherwise break out of the attribute. Plain esc() (which
// deliberately keeps quotes) stays for text and the JSON/HTTP highlighters.
export const escAttr=s=>esc(s).replace(/"/g,'&quot;').replace(/'/g,'&#39;');

export const state={flows:[],selId:null,detail:null,intercept:{enabled:false,queue:[]},
  rules:[],scope:[],views:[],inScopeOnly:false,showManual:true,showAI:true,aiDisabled:false,flowTruncated:false,selected:new Set(),lastSelIdx:-1,aiIds:[],view:{req:'pretty',res:'pretty'},sort:{key:'id',dir:-1},proxyAddr:'127.0.0.1:8080',deviceProxy:'127.0.0.1:8080',deviceProxyMode:'auto',controlAddr:'127.0.0.1:9966',
  filters:{scheme:'',search:'',searchScope:'path',method:'',status:'',host:'',tag:'',exclude:[]},notesOnly:false,hideTlsFailed:true,activity:[],actUnseen:0,tags:[],tagColors:{},flowCols:['id','method','host','path','status','size','time'],oobEnabled:false};

// toast(m) = info; toast(m, 'error'|'warn'|'success') for a longer, colored one.
export function toast(m, sev){
  const c = $('#toast');
  if (!c) return;
  const t = document.createElement('div');
  t.className = 'toast-item ' + (sev || 'info');
  t.textContent = m;
  c.appendChild(t);
  requestAnimationFrame(() => t.classList.add('show'));
  const ms = sev === 'error' ? 4500 : 2600;
  setTimeout(() => { t.classList.remove('show'); setTimeout(() => t.remove(), 220); }, ms);
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

// Project-scoped localStorage (#17/#18): Repeater/Intruder tabs (and Intruder
// presets) must not leak across project switches. Keys look like `rep.tabs.default`.
// On first use of a scoped key, an unscoped legacy value is migrated once.
let storageProject='default';
const migratedStorageBases=new Set();
export function setStorageProject(name){
  storageProject=(name&&String(name).trim())||'default';
}
export function projectStorageKey(base){
  const safe=String(storageProject).replace(/[^A-Za-z0-9._-]+/g,'_')||'default';
  const scoped=base+'.'+safe;
  // One-shot migrate: copy legacy unscoped key into *this* project, then remove
  // it so a later project switch cannot inherit the same drafts (#17/#18).
  if(!migratedStorageBases.has(base)){
    migratedStorageBases.add(base);
    try{
      const old=localStorage.getItem(base);
      if(old!=null){
        if(localStorage.getItem(scoped)==null) localStorage.setItem(scoped,old);
        localStorage.removeItem(base);
      }
    }catch(e){}
  }
  return scoped;
}

export function renderLoadError(el, label, err, retry, stale=false){
  if(!el)return;
  const message=err&&err.message?err.message:String(err||'unknown error');
  el.style.display='block';
  el.innerHTML=`<span class="state-error-msg">${esc(label)} ${stale?'is stale — ':'failed: '}${esc(message)}</span> <button type="button" class="btn xs" data-load-retry>Retry</button>`;
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
// Returns {tabs,cur,add,switchTo,close,persist,persistDebounced,render,init}.
export function createTabManager(opts){
  const {storageKey:keyOpt,blank,title,onSave,onLoad,normalize,serialize=(t=>t),labelStyle,onPersist}=opts;
  const storageKey=()=>typeof keyOpt==='function'?keyOpt():keyOpt;
  const mgr={tabs:[],active:null,seq:1,persistT:null};
  mgr.cur=()=>mgr.tabs.find(t=>t.tid===mgr.active)||null;
  mgr.persist=function(){
    const blob={seq:mgr.seq,active:mgr.active,tabs:mgr.tabs.map(serialize)};
    try{localStorage.setItem(storageKey(),JSON.stringify(blob));}catch(e){}
    // Optional project-DB sync (Repeater/Intruder) — best-effort, never blocks UI.
    if(typeof onPersist==='function'){try{onPersist(blob);}catch(e){}}
  };
  mgr.persistDebounced=function(){clearTimeout(mgr.persistT);mgr.persistT=setTimeout(mgr.persist,400);};
  mgr.render=function(barSel){
    const bar=$(barSel);if(!bar)return;
    bar.innerHTML=mgr.tabs.map(t=>{
      const active=t.tid===mgr.active;
      const style=labelStyle?labelStyle(t,active):'';
      return `<div class="rep-tab${active?' on':''}" data-tid="${t.tid}" title="${escAttr(title(t))}">
    <span class="rt-label"${style?` style="${escAttr(style)}"`:''}>${esc(title(t))}</span>
    <button type="button" class="rt-close" data-close="${t.tid}" aria-label="close tab" title="close tab">✕</button></div>`;
    }).join('')+`<button class="rep-tab-add" id="${bar.id}Add" title="New tab">＋</button>`;
    bar.querySelectorAll('.rep-tab').forEach(el=>{el.onclick=e=>{if(e.target.dataset.close!=null)return;mgr.switchTo(Number(el.dataset.tid));};wireRowKey(el,()=>mgr.switchTo(Number(el.dataset.tid)));});
    bar.querySelectorAll('[data-close]').forEach(x=>x.onclick=e=>{e.stopPropagation();mgr.close(Number(x.dataset.close));});
    const addBtn=$('#'+bar.id+'Add');
    if(addBtn)addBtn.onclick=()=>{
      onSave();mgr.tabs.push(blank(mgr.seq++));mgr.active=mgr.tabs[mgr.tabs.length-1].tid;
      mgr.render(barSel);onLoad(mgr.cur());mgr.persist();
    };
  };
  mgr.switchTo=function(tid){if(tid===mgr.active)return;onSave();mgr.active=tid;mgr._rerender();onLoad(mgr.cur());mgr.persist();};
  mgr.close=function(tid){
    const i=mgr.tabs.findIndex(t=>t.tid===tid);if(i<0)return;
    const wasActive=tid===mgr.active;
    mgr.tabs.splice(i,1);
    if(!mgr.tabs.length)mgr.tabs.push(blank(mgr.seq++));
    if(wasActive)mgr.active=mgr.tabs[Math.min(i,mgr.tabs.length-1)].tid;
    mgr._rerender();
    if(wasActive)onLoad(mgr.cur());
    mgr.persist();
  };
  // init loads persisted tabs (or seeds one blank tab), wires the bar, and
  // paints the editor. barSel is stored so switchTo/close/add can re-render
  // the same bar without every caller having to pass it again.
  mgr.init=function(barSel){
    mgr._rerender=()=>mgr.render(barSel);
    let ok=false;
    try{
      const d=JSON.parse(localStorage.getItem(storageKey())||'null');
      if(d&&d.tabs&&d.tabs.length){
        mgr.tabs=d.tabs.map(normalize);
        mgr.active=(d.active&&mgr.tabs.find(x=>x.tid===d.active))?d.active:mgr.tabs[0].tid;
        const fin=mgr.tabs.map(t=>t.tid).filter(Number.isFinite);
        mgr.seq=Math.max(d.seq||0,(fin.length?Math.max(...fin):0)+1);
        ok=true;
      }
    }catch(e){}
    if(!ok){mgr.tabs=[blank(mgr.seq++)];mgr.active=mgr.tabs[0].tid;}
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
  // Defensive: should <body> itself ever sit under a transformed/filtered ancestor
  // (which would re-establish a fixed containing block), nudge the menu back onto
  // the trigger by the delta between intended and rendered position. No-op normally.
  const got=inst.menu.getBoundingClientRect();
  const dx=r.left-got.left,dy=(r.bottom+4)-got.top;
  if(dx||dy){
    inst.menu.style.left=(r.left+dx)+'px';
    inst.menu.style.top=(r.bottom+4+dy)+'px';
  }
  inst.trigger.setAttribute('aria-expanded','true');
  uiSelectOpen=inst;
  const selected=[...inst.sel.options].findIndex(o=>o.selected&&!o.disabled);
  inst.setActive(selected>=0?selected:inst.firstEnabled());
  scrollUiSelectMenuToSelected(inst.menu);
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
  const title=(sel.getAttribute('title')||'').trim();
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
  if(!sel||sel.tagName!=='SELECT'||sel._uiSelect||sel.dataset.uiSelect==='off')return sel;

  const wrap=document.createElement('div');
  wrap.className='ui-select';
  if(sel.classList.contains('btn'))wrap.classList.add('ui-select-btn');
  if(sel.classList.contains('rep-method'))wrap.classList.add('ui-select-rep');
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
  const valueEl=document.createElement('span');
  valueEl.className='ui-select-value';
  const caret=document.createElement('span');
  caret.className='ui-select-caret';
  caret.setAttribute('aria-hidden','true');
  caret.textContent='▾';
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
        return `<button type="button" role="option" tabindex="-1" id="${menu.id}Opt${i}" class="ui-select-opt${selOn?' sel':''}${i===inst.active?' active':''}" data-index="${i}" data-value="${escAttr(v)}"${dis?' disabled aria-disabled="true"':''}${o.hidden?' hidden':''} aria-selected="${selOn?'true':'false'}"><span class="ui-select-opt-title">${esc(o.textContent)}</span></button>`;
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
new MutationObserver(muts=>{
  for(const m of muts){
    m.addedNodes.forEach(n=>{
      if(n.nodeType!==1)return;
      if(n.tagName==='SELECT')enhanceSelect(n);
      else initUiSelects(n);
    });
  }
}).observe(document.documentElement,{childList:true,subtree:true});

export async function api(path,opts){
  opts=opts||{};
  // Remote (cookie-authed) sessions must carry an anti-CSRF header on mutations;
  // it is harmless on the loopback path. Safe methods (GET/HEAD) skip it.
  const method=(opts.method||'GET').toUpperCase();
  if(method!=='GET'&&method!=='HEAD'){
    opts.headers=Object.assign({'X-Interseptor-CSRF':'1'},opts.headers||{});
  }
  const r=await fetch(path,opts);
  if(r.status===401){ // remote session expired / not signed in → go to login
    if(location.pathname!=='/login'){ location.href='/login'; }
    throw new Error('unauthorized');
  }
  if(!r.ok){let m=r.statusText;try{m=(await r.json()).error||m}catch(e){}throw new Error(m);}
  const ct=r.headers.get('content-type')||'';return ct.includes('json')?r.json():r.text();
}

/** apiTry wraps api(); on failure optionally toasts and returns null instead of throwing. */
export async function apiTry(path, opts, {toastOnError=true, label=''}={}){
  try{return await api(path, opts);}
  catch(e){if(toastOnError)toast((label?label+': ':'')+e.message);return null;}
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
// upsertFlow inserts a new flow at the front of `order` (the live-tail position)
// or merges onto the existing object in place (so any other reference held to it,
// e.g. state.detail, sees the update without a second lookup). Returns
// {flow, isNew} so callers can branch on whether this was an insert or a merge.
export function upsertFlow(store,f){
  const ex=store.byId.get(f.id);
  if(ex){Object.assign(ex,f);return {flow:ex,isNew:false};}
  store.order.unshift(f);
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
    const viewH=container.clientHeight||640,scrollTop=container.scrollTop||0;
    const start=Math.max(0,Math.floor(scrollTop/itemHeight)-buffer);
    const end=Math.min(total,start+Math.ceil(viewH/itemHeight)+2*buffer);
    return {start,end,topPad:start*itemHeight,bottomPad:(total-end)*itemHeight};
  }
  return {computeWindow,isActive:()=>active};
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
  let timer=null,saving=false,lastSaved='',current='';
  const status=k=>{if(onStatus)onStatus(k);};
  async function flush(){
    clearTimeout(timer);timer=null;
    if(current===lastSaved||saving)return;
    saving=true;status('saving');
    try{
      await save(current);
      lastSaved=current;
      status('saved');
    }catch(e){
      status('dirty');
      throw e;
    }finally{saving=false;}
  }
  function schedule(value){
    current=value;
    if(value===lastSaved){clearTimeout(timer);timer=null;return;}
    status('dirty');
    clearTimeout(timer);
    timer=setTimeout(()=>{flush().catch(()=>{});},delay);
  }
  // setBaseline marks `value` as already-saved (e.g. right after the initial
  // load fetch) without triggering a save or a status change.
  function setBaseline(value){lastSaved=value;current=value;}
  return {schedule,flush,setBaseline,isDirty:()=>current!==lastSaved};
}

export const FLAG_WS=32;
export const FLAG_TLS=16;
export const FLAG_AI=1024;
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

// saveFile opens the native Save dialog when available (File System Access API),
// otherwise triggers a download with the suggested filename.
export async function saveFile(blob,suggestedName,mimeType){
  const name=suggestedName||'download';
  const type=mimeType||blob.type||'application/octet-stream';
  if(window.showSaveFilePicker){
    try{
      const ext=name.includes('.')?name.slice(name.lastIndexOf('.')).toLowerCase():'';
      const types=[];
      if(ext==='.har')types.push({description:'HTTP Archive',accept:{'application/json':['.har']}});
      const handle=await window.showSaveFilePicker({suggestedName:name,types:types.length?types:undefined});
      const writable=await handle.createWritable();
      await writable.write(blob instanceof Blob?blob:new Blob([blob],{type}));
      await writable.close();
      return handle.name;
    }catch(e){
      if(e&&e.name==='AbortError')throw e;
    }
  }
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

/* ---- shared right-click context menu (#ctxmenu) ---- */
export function hideCtxMenu(){
  const ctx=$('#ctxmenu');if(!ctx)return;
  if(ctx._keyHandler){document.removeEventListener('keydown',ctx._keyHandler);ctx._keyHandler=null;}
  ctx.classList.remove('show');ctx._acts=null;
}

// openCtxMenu renders sectioned items on #ctxmenu and positions at (x,y).
export function openCtxMenu(x,y,sections){
  const ctx=$('#ctxmenu');if(!ctx)return;
  closeAllUiSelects(); // ctx/Views menus and ui-select dropdowns are one mutually-exclusive group
  ctx.setAttribute('role','menu');
  const acts=[];let html='';
  sections.forEach(sec=>{
    const items=(sec.items||[]).filter(Boolean);
    if(!items.length)return;
    if(sec.head)html+=`<div class="ctx-head">${esc(sec.head)}</div>`;
    items.forEach(it=>{
      if(it.sep){html+='<div class="ctx-sep"></div>';return;}
      const dStyle=it.danger?' style="color:var(--red)"':'';
      const right=it.val!=null?`<span class="mono"${dStyle}>${esc(it.val)}</span>`:'';
      html+=`<div class="ctx-item${it.on?' on':''}" role="menuitem" data-i="${acts.length}"${it.danger&&it.val==null?dStyle:''}><span class="lbl"${dStyle}>${esc(it.label)}</span>${right}</div>`;
      acts.push(it.act);
    });
  });
  if(!acts.length)return;
  ctx.innerHTML=html;ctx._acts=acts;ctx._sel=0;
  const items=ctx.querySelectorAll('.ctx-item');
  items.forEach((el,i)=>el.classList.toggle('on',i===0));
  ctx.querySelectorAll('[data-i]').forEach(el=>el.onclick=()=>{const fn=ctx._acts[Number(el.dataset.i)];hideCtxMenu();if(fn)fn();});
  ctx.style.left=x+'px';ctx.style.top=y+'px';ctx.classList.add('show');
  const r=ctx.getBoundingClientRect();
  if(r.right>innerWidth)ctx.style.left=Math.max(4,x-r.width)+'px';
  if(r.bottom>innerHeight)ctx.style.top=Math.max(4,y-r.height)+'px';
  const paintSel=()=>{items.forEach((el,i)=>el.classList.toggle('on',i===ctx._sel));const cur=items[ctx._sel];if(cur)cur.scrollIntoView({block:'nearest'});};
  ctx._keyHandler=e=>{
    if(!ctx.classList.contains('show'))return;
    if(e.key==='ArrowDown'){e.preventDefault();ctx._sel=Math.min(items.length-1,ctx._sel+1);paintSel();}
    else if(e.key==='ArrowUp'){e.preventDefault();ctx._sel=Math.max(0,ctx._sel-1);paintSel();}
    else if(e.key==='Enter'){e.preventDefault();const fn=ctx._acts[ctx._sel];hideCtxMenu();if(fn)fn();}
    else if(e.key==='Escape'){e.preventDefault();hideCtxMenu();}
  };
  document.addEventListener('keydown',ctx._keyHandler);
}

export const DEC_OPS=[['base64decode','Base64 ↓'],['base64encode','Base64 ↑'],['urldecode','URL ↓'],['urlencode','URL ↑'],['hexdecode','Hex ↓'],['hexencode','Hex ↑'],['htmldecode','HTML ↓'],['htmlencode','HTML ↑'],['jwtdecode','JWT'],['smart','✨ Smart']];

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

// wireSelectionDecode shows a slim decode strip when highlighted text looks encoded.
export function wireSelectionDecode(viewEl, barEl, {onDecoder}={}){
  if(!viewEl||!barEl)return;
  let timer=null,lastSel='',req=0;
  const hide=()=>{barEl.hidden=true;lastSel='';};
  const run=async()=>{
    const sel=selectionFromRaw(viewEl,selectionWithin(viewEl));
    if(!sel||sel.length<4||sel.length>8192){hide();return;}
    if(sel===lastSel)return;
    const id=++req;
    try{
      const r=await api('/api/decode',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({op:'smart',input:sel})});
      if(id!==req)return;
      if(r.error||!r.output||r.output===sel){hide();return;}
      lastSel=sel;
      barEl.hidden=false;
      barEl.querySelector('.sel-decode-kind').textContent=encodeKindLabel(sel);
      const outEl=barEl.querySelector('.sel-decode-out');
      const full=r.output;
      outEl.textContent=full.length>500?full.slice(0,500)+'…':full;
      outEl.title=full.length>500?full:'';
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
export const MODAL_IDS=['notesAiModal','flowModal','aiModal','shortcutsModal','checksModal','codecsModal','activeModal','oobModal','projModal','authzModal','findCreateModal','findTriageModal','findPickModal','findFlowPickModal','compareModal','decModal','confirmModal','promptModal','setupModal','imgLightbox'];
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
  entry={el:modalEl,dialog,previousFocus:null,onEscape:null,onDismiss:null,initialFocus:null,
    previousInlineZIndex:'',previousZIndexPriority:'',zIndexSaved:false};
  modalRegistry.set(modalEl,entry);
  modalEl.addEventListener('mousedown',e=>{
    if(e.target!==modalEl||topModal()!==entry)return;
    if(entry.onDismiss)entry.onDismiss();
    else if(entry.onEscape)entry.onEscape();
    else closeModal(modalEl);
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
  if(prev&&prev.isConnected&&visibleFocusable(prev)&&typeof prev.focus==='function')prev.focus();
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
// Capture-phase so we open even if a parent stops bubble.
document.addEventListener('click',e=>{
  const img=imgLbClickTarget(e.target);if(!img)return;
  const src=img.currentSrc||img.getAttribute('src')||img.src;if(!src)return;
  e.preventDefault();
  e.stopPropagation();
  openImageLightbox(src,img.getAttribute('alt')||'');
},true);
// Wire controls once DOM is ready (module scripts are deferred, but be safe).
if(document.readyState==='loading')document.addEventListener('DOMContentLoaded',ensureImageLightbox);
else ensureImageLightbox();

// uiPrompt: an in-app replacement for the browser's prompt() — themed, consistent,
// resolves to the entered string or null (Cancel / Escape / backdrop / empty).
export function uiPrompt(opts){
  opts=opts||{};
  return new Promise(resolve=>{
    const m=$('#promptModal'),inp=$('#promptInput');
    $('#promptTitle').textContent=opts.title||'Enter a value';
    inp.placeholder=opts.placeholder||'';inp.value=opts.value||'';
    let done=false;
    const finish=v=>{if(done)return;done=true;closeModal(m);inp.onkeydown=null;resolve(v);};
    openModal(m,{initialFocus:inp,onEscape:()=>finish(null),onDismiss:()=>finish(null)});
    setTimeout(()=>inp.select(),0);
    $('#promptOk').onclick=()=>finish(inp.value.trim()||null);
    $('#promptCancel').onclick=()=>finish(null);
    inp.onkeydown=e=>{if(e.key==='Enter'){e.preventDefault();finish(inp.value.trim()||null);}};
  });
}

// uiConfirm: themed confirm dialog reusing the promptModal structure.
// Returns a Promise<boolean>.
export function uiConfirm(title,htmlMsg,okLabel,okClass,okColor){
  return new Promise(resolve=>{
    const m=$('#confirmModal');
    if(!m){resolve(window.confirm(title+'\n\n'+htmlMsg.replace(/<[^>]+>/g,'')));return;}
    $('#confirmTitle').textContent=title;
    $('#confirmMsg').innerHTML=htmlMsg;
    const ok=$('#confirmOk');
    ok.textContent=okLabel||'OK';
    ok.className=(okClass||'btn accent');
    if(okColor)ok.style.color=okColor; else ok.style.color='';
    let done=false;
    const finish=v=>{if(done)return;done=true;closeModal(m);ok.onclick=null;$('#confirmCancel').onclick=null;resolve(v);};
    openModal(m,{initialFocus:$('#confirmCancel'),onEscape:()=>finish(false),onDismiss:()=>finish(false)});
    ok.onclick=()=>finish(true);
    $('#confirmCancel').onclick=()=>finish(false);
  });
}

// Minimal, safe markdown → HTML: escape first, then format a useful subset.
export function renderMD(src){
  if(!src||!src.trim())return '<p class="hint">Empty — switch to Edit and jot down creds, findings, scope…</p>';
  let s=esc(src),blocks=[],flowRefs=[];
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
  s=s.replace(/<(think|thought|reasoning)>\r?\n?([\s\S]*?)<\/\1>/gi,(m,tag,inner)=>{
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
  s=s.replace(/(?:^|\n)((?:>\s?.*(?:\r?\n|$))+)/g,(m,block)=>{
    const inner=block.trim().split(/\r?\n/).map(l=>l.replace(/^>\s?/,'')).join('\n');
    return '<blockquote class="md-quote">'+inner.replace(/\n/g,'<br>')+'</blockquote>';
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
  s=s.replace(/!\[([^\]]*)\]\((https?:\/\/[^\s")]+|\/api\/notes\/images\/\d+|data:image\/[a-z0-9.+-]+;base64,[A-Za-z0-9+/=]+)\)/g,(m,alt,src)=>'<img class="md-img" alt="'+alt.replace(/"/g,'&quot;')+'" src="'+src.replace(/"/g,'&quot;')+'">');
  s=s.replace(/\*\*([^*]+)\*\*/g,'<strong>$1</strong>');
  s=s.replace(/(?<!\*)\*([^*\n]+)\*(?!\*)/g,'<em>$1</em>');
  // Explicit in-app flow links: [label](flow:123) or [label](#flow-123)
  s=s.replace(/\[([^\]]+)\]\((?:flow:|#flow-)(\d+)\)/g,(m,txt,id)=>'<a class="md-flow-link" data-flow="'+id+'" href="#flow-'+id+'">'+txt+'</a>');
  s=s.replace(/`([^`\n]+)`/g,'<code>$1</code>');
  s=s.replace(/\[([^\]]+)\]\((https?:\/\/[^\s)]+|mailto:[^\s)]+)\)/g,(m,txt,href)=>'<a href="'+href.replace(/"/g,'&quot;')+'" target="_blank" rel="noopener">'+txt+'</a>');
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
