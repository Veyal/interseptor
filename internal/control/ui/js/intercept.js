import { $, $$, esc, escAttr, state, toast, api, methodColor, wireRowKey, prettify } from './core.js';
import { animateOnce, MOTION } from './motion.js';

/* ---- intercept ---- */
// One unified hold queue (requests + responses) feeding one editor. state.heldSel
// is {id, side:'req'|'resp'} for the selected item, or null.
const heldRawCache=new Map();
const heldOriginalCache=new Map();
let heldKeysReady=false;
let knownHeldKeys=new Set();
let heldSignalWindowAt=0,heldSignalCount=0;
let heldActionInFlight=null;
let heldActionEpoch=0;
let heldLoadingKey=null;
function allowHeldSignal(){
  const now=performance.now();
  if(now-heldSignalWindowAt>800){heldSignalWindowAt=now;heldSignalCount=0;}
  heldSignalCount++;
  return !document.hidden&&heldSignalCount<=4;
}
function heldKey(side,id){return side+':'+id;}
function heldOriginal(h){return h.original||h.raw||'';}
function setHeldModified(raw, original){
  const badge=$('#heldModified');
  const modified=raw!==original;
  if(badge){badge.hidden=!modified;badge.textContent=modified?'MODIFIED':'';}
}

function setHeldControlsDisabled(disabled){
  ['#heldRaw','#forwardBtn','#dropBtn','#heldBeautifyBtn','#heldResetBtn','#heldDecodeBtn']
    .map(s=>$(s)).forEach(el=>{if(el)el.disabled=!!disabled||!!heldActionInFlight;});
}

function clearHeldLoadMessage(){
  const el=$('#heldLoadState');
  if(el)el.remove();
}

function showHeldLoadState(h,text,retry){
  clearHeldLoadMessage();
  const main=document.querySelector('.icpt-main');
  if(!main)return;
  const el=document.createElement('div');
  el.id='heldLoadState';el.className='hint';el.setAttribute('role',retry?'alert':'status');
  el.style.cssText='padding:10px 14px;border-bottom:1px solid var(--line);color:'+(retry?'var(--red)':'var(--fg2)');
  el.textContent=text;
  if(retry){
    const b=document.createElement('button');b.type='button';b.className='btn';b.style.marginLeft='10px';b.textContent='Retry loading held message';
    b.onclick=()=>selectHeld(h.id,h.side);el.appendChild(b);
  }
  main.insertBefore(el,main.firstChild);
}

function showHeldLoading(h){
  heldLoadingKey=heldKey(h.side,h.id);
  clearHeldLoadMessage();
  const head=$('#heldEditor'),empty=$('#heldEmpty'),ta=$('#heldRaw'),dec=$('#heldDecoded');
  if(head)head.style.display='flex';
  if(empty)empty.style.display='none';
  if(dec){dec.style.display='none';dec.hidden=true;dec.textContent='';}
  if(ta){ta.style.display='block';ta.value='';ta.placeholder='Loading held message…';ta.disabled=true;}
  setHeldControlsDisabled(true);
  showHeldLoadState(h,'Loading held message…',false);
}

function showHeldLoadError(h,error){
  heldLoadingKey=null;
  const ta=$('#heldRaw');
  if(ta){ta.value='';ta.placeholder='Held message could not be loaded';ta.disabled=true;}
  setHeldControlsDisabled(true);
  showHeldLoadState(h,'Unable to load held message: '+(error?.message||'request failed'),true);
}

export function renderIntercept(){
  const ic=state.intercept||{};
  const rq=ic.queue||[], rrq=ic.responseQueue||[];
  setSwitch('#interceptToggle','#icptReqState',ic.enabled);
  setSwitch('#respInterceptToggle','#icptResState',ic.responseEnabled);
  // conditional-intercept filter (don't clobber fields the user is editing)
  const fo=$('#interceptFilterOn');if(fo&&document.activeElement!==fo)fo.checked=!!ic.filterEnabled;
  const ft=$('#interceptFilterTarget');if(ft&&document.activeElement!==ft)ft.value=ic.filterTarget||'any';
  const fp=$('#interceptFilterPattern');if(fp&&document.activeElement!==fp)fp.value=ic.filterPattern||'';
  // unified queue: requests then responses, each tagged with its side
  const items=[...rq.map(h=>({...h,side:'req'})),...rrq.map(h=>({...h,side:'resp'}))];
  if(heldActionInFlight&&!items.some(h=>heldKey(h.side,h.id)===heldActionInFlight.key)){
    // The SSE update can arrive before fetch() resolves. Keep the acknowledged
    // item's DOM row in place until the action handler starts its exit motion;
    // state still comes from the server and the deferred render is short.
    heldActionInFlight.deferred=true;
    return;
  }
  const nextHeldKeys=new Set(items.map(h=>heldKey(h.side,h.id)));
  const arrivals=heldKeysReady?items.filter(h=>!knownHeldKeys.has(heldKey(h.side,h.id))&&allowHeldSignal()):[];
  knownHeldKeys=nextHeldKeys;
  heldKeysReady=true;
  const total=items.length;
  const danger=$('#interceptWarning');
  const interceptDanger=!!(ic.enabled||ic.responseEnabled||total);
  document.documentElement.classList.toggle('intercept-danger',interceptDanger);
  if(danger){
    danger.style.display=interceptDanger?'block':'none';
    danger.textContent=total
      ?`INTERCEPT ACTIVE — ${total} request/response item${total===1?' is':'s are'} held until you Forward or Drop. Off after restart (session-only).`
      :'INTERCEPT ACTIVE — matching traffic will be held until you Forward or Drop. Off after restart (session-only).';
  }
  const badge=$('#heldBadge');if(badge){badge.style.display=total?'inline-block':'none';badge.textContent=total;}
  const ht=$('#heldTotal');if(ht){ht.style.display=total?'inline-block':'none';ht.textContent=total;}
  const list=$('#heldList');
  if(!total){list.innerHTML='';state.heldSel=null;showEditor(null);return;}
  list.innerHTML=items.map(h=>`<div class="icpt-item${(state.heldSel&&state.heldSel.id===h.id&&state.heldSel.side===h.side)?' sel':''}" data-id="${h.id}" data-side="${h.side}">
    <span class="icpt-tag ${h.side}">${h.side==='req'?'REQ':'RESP'}</span>
    ${h.side==='req'?`<span class="m" style="color:${methodColor(h.method)}">${esc(h.method)}</span>`:''}
    <span class="u">${esc(h.host)}${esc(h.path)}</span></div>`).join('');
  $$('#heldList .icpt-item').forEach(el=>{el.onclick=()=>selectHeld(Number(el.dataset.id),el.dataset.side);wireRowKey(el,()=>selectHeld(Number(el.dataset.id),el.dataset.side));});
  arrivals.forEach(h=>{
    const el=list.querySelector(`.icpt-item[data-id="${h.id}"][data-side="${h.side}"]`);
    animateOnce(el,[
      {opacity:.45,transform:'translateX(-4px)',backgroundColor:'var(--accentDim)'},
      {opacity:1,transform:'translateX(0)',backgroundColor:'transparent'},
    ],{duration:MOTION.base,easing:MOTION.enter});
  });
  const cur=state.heldSel&&items.find(h=>h.id===state.heldSel.id&&h.side===state.heldSel.side);
  if(cur)selectHeld(cur.id,cur.side,{keepEditor:true});
  else selectHeld(items[0].id,items[0].side);
}
function setSwitch(btnSel,stateSel,on){
  const b=$(btnSel);if(b){b.classList.toggle('on',!!on);b.setAttribute('aria-pressed',on?'true':'false');}
  const s=$(stateSel);if(s)s.textContent=on?'On':'Off';
}
function heldItem(id,side){const q=side==='resp'?(state.intercept.responseQueue||[]):(state.intercept.queue||[]);return q.find(x=>x.id===id);}
function showEditor(h){
  const head=$('#heldEditor'),ta=$('#heldRaw'),empty=$('#heldEmpty'),title=$('#heldTitle');
  clearHeldLoadMessage();
  if(!h){
    heldLoadingKey=null;
    if(head)head.style.display='none';
    if(ta){ta.style.display='none';ta.disabled=false;ta.removeAttribute('placeholder');}
    if(empty)empty.style.display='flex';
    const dec=$('#heldDecoded');if(dec){dec.style.display='none';dec.hidden=true;dec.textContent='';}
    setHeldControlsDisabled(false);
    return;
  }
  if(head)head.style.display='flex';
  if(empty)empty.style.display='none';
  const dec=$('#heldDecoded');if(dec){dec.style.display='none';dec.hidden=true;dec.textContent='';}
  const original=heldOriginal(h);
  if(ta){ta.style.display='block';ta.disabled=false;ta.removeAttribute('placeholder');ta.value=h.raw||'';setHeldModified(ta.value,original);}
  setHeldControlsDisabled(false);
  if(title)title.innerHTML=h.side==='resp'
    ?`<span class="icpt-tag resp" style="margin-right:8px">RESP</span><span class="u">${esc(h.host)}${esc(h.path)}</span>`
    :`<span style="color:${methodColor(h.method)};font-weight:700">${esc(h.method)}</span> ${esc(h.host)}${esc(h.path)}`;
}
export async function selectHeld(id,side,opts={}){
  state.heldSel={id,side};
  $$('#heldList .icpt-item').forEach(el=>el.classList.toggle('sel',Number(el.dataset.id)===id&&el.dataset.side===side));
   const h=heldItem(id,side);if(!h)return;
   const cacheKey=side+':'+id;
   let raw=h.raw;
   if(!heldOriginalCache.has(cacheKey))heldOriginalCache.set(cacheKey,heldOriginal(h));

  if(heldLoadingKey===cacheKey&&opts.keepEditor)return;

  if(opts.keepEditor){
    const ta=$('#heldRaw');
    if(ta&&document.activeElement===ta)return;
    if(ta&&ta.value)raw=ta.value;
  }
  if(!raw&&h.len!=null){
    showHeldLoading({...h,side});
    if(heldRawCache.has(cacheKey))raw=heldRawCache.get(cacheKey);
    else{
      try{
        const d=await api('/api/intercept/held/'+id+'/raw?side='+side);
        // A fast second click on another item can change state.heldSel while this
        // fetch was in flight. Bail (and restore that item's editor) so the editor
        // — and thus #forwardBtn, which reads state.heldSel — never shows item A's
        // body while the selection is item B.
        if(!state.heldSel||state.heldSel.id!==id||state.heldSel.side!==side)return;
        raw=d.raw||'';
        heldRawCache.set(cacheKey,raw);
        h.raw=raw;
      }catch(e){
        if(state.heldSel&&state.heldSel.id===id&&state.heldSel.side===side)showHeldLoadError({...h,side},e);
        return;
      }
    }
  }
  heldLoadingKey=null;
  showEditor({...h,side,raw});
}
$('#respInterceptToggle').onclick=async()=>{
  try{const s=await api('/api/intercept/response/toggle',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({enabled:!state.intercept.responseEnabled})});state.intercept=s;renderIntercept();}catch(e){toast(e.message);}
};
export async function toggleIntercept(){
  try{const s=await api('/api/intercept/toggle',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({enabled:!state.intercept.enabled})});
    state.intercept=s;renderIntercept();}catch(e){toast(e.message);}
}
$('#interceptToggle').onclick=toggleIntercept;
// Forward / Drop act on the selected item, routing to the request or response API.
function setHeldActionState(button,stateName,label){
  const buttons=[$('#forwardBtn'),$('#dropBtn')];
  buttons.forEach(b=>{if(b)b.disabled=stateName==='pending';});
  button.classList.remove('is-pending','is-success','is-error');
  if(stateName!=='idle')button.classList.add('is-'+stateName);
  button.dataset.state=stateName;
  button.setAttribute('aria-busy',stateName==='pending'?'true':'false');
  button.textContent=label;
}
function resetHeldAction(button,label,delay,epoch){
  setTimeout(()=>{if(epoch===heldActionEpoch)setHeldActionState(button,'idle',label);},delay);
}
function finishHeldExit(row){
  const deferred=!!heldActionInFlight?.deferred;
  heldActionInFlight=null;
  if(deferred)renderIntercept();
  else if(row?.isConnected)row.remove();
}
function releaseHeldAction(){
  const deferred=!!heldActionInFlight?.deferred;
  heldActionInFlight=null;
  if(deferred)renderIntercept();
}
$('#forwardBtn').onclick=async()=>{const sel=state.heldSel;if(!sel)return;
  const base=sel.side==='resp'?'/api/intercept/response/':'/api/intercept/';
  const button=$('#forwardBtn');
  const row=document.querySelector(`#heldList .icpt-item[data-id="${sel.id}"][data-side="${sel.side}"]`);
  const epoch=++heldActionEpoch;
  heldActionInFlight={key:heldKey(sel.side,sel.id),deferred:false};
  setHeldActionState(button,'pending','Forwarding…');
  try{await api(base+sel.id+'/forward',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({raw:$('#heldRaw').value})});
    await animateOnce(row,[{opacity:1,transform:'translateX(0)'},{opacity:0,transform:'translateX(6px)'}],{duration:MOTION.base,easing:MOTION.exit});
    heldRawCache.delete(sel.side+':'+sel.id);
    heldOriginalCache.delete(heldKey(sel.side,sel.id));
    finishHeldExit(row);
    setHeldActionState(button,'success','Forwarded');
    resetHeldAction(button,'Forward',600,epoch);
    toast(sel.side==='resp'?'response forwarded':'forwarded');
  }catch(e){releaseHeldAction();setHeldActionState(button,'error','Forward failed');resetHeldAction(button,'Forward',900,epoch);toast(e.message);}};
$('#heldDecodeBtn')&&($('#heldDecodeBtn').onclick=async()=>{
  const sel=state.heldSel;if(!sel)return;
  const rawEl=$('#heldRaw'),decEl=$('#heldDecoded');
  if(!rawEl||!decEl)return;
  if(decEl.style.display&&decEl.style.display!=='none'){
    decEl.style.display='none';decEl.hidden=true;rawEl.style.display='block';return;
  }
  const raw=rawEl.value||'';
  const i=raw.indexOf('\r\n\r\n');const body=i>=0?raw.slice(i+4):raw;
  const hostLine=(raw.match(/^Host:\s*(.+)$/im)||[])[1]||'';
  const side=sel.side==='resp'?'res':'req';
  try{
    const d=await api('/api/codecs/test',{method:'POST',headers:{'content-type':'application/json'},
      body:JSON.stringify({side,rawBody:body,host:hostLine.trim()})});
    if(!d.matched){toast('no message codec matched');return;}
    if(d.error){toast(d.error);return;}
    decEl.textContent=(d.title||d.codecId||'decoded')+'\n\n'+(d.plaintext||'');
    decEl.hidden=false;decEl.style.display='block';rawEl.style.display='none';
  }catch(e){toast(e.message);}
});
$('#heldRaw').addEventListener('input',()=>{
  const sel=state.heldSel,h=sel&&heldItem(sel.id,sel.side);
  if(h)setHeldModified($('#heldRaw').value,heldOriginalCache.get(heldKey(sel.side,sel.id))||heldOriginal(h));
});
$('#heldBeautifyBtn')&&($('#heldBeautifyBtn').onclick=()=>{
  const ta=$('#heldRaw');if(!ta)return;
  ta.value=prettify(ta.value);ta.dispatchEvent(new Event('input'));ta.focus();
});
$('#heldResetBtn')&&($('#heldResetBtn').onclick=()=>{
  const sel=state.heldSel,ta=$('#heldRaw');if(!sel||!ta)return;
  const original=heldOriginalCache.get(heldKey(sel.side,sel.id));if(original==null)return;
  ta.value=original;ta.dispatchEvent(new Event('input'));ta.focus();
});

$('#dropBtn').onclick=async()=>{const sel=state.heldSel;if(!sel)return;
  const base=sel.side==='resp'?'/api/intercept/response/':'/api/intercept/';
  const button=$('#dropBtn');
  const row=document.querySelector(`#heldList .icpt-item[data-id="${sel.id}"][data-side="${sel.side}"]`);
  const epoch=++heldActionEpoch;
  heldActionInFlight={key:heldKey(sel.side,sel.id),deferred:false};
  setHeldActionState(button,'pending','Dropping…');
  try{await api(base+sel.id+'/drop',{method:'POST'});
    await animateOnce(row,[{opacity:1,transform:'translateY(0)'},{opacity:0,transform:'translateY(-3px)'}],{duration:MOTION.fast,easing:MOTION.exit});
    heldRawCache.delete(sel.side+':'+sel.id);
    heldOriginalCache.delete(heldKey(sel.side,sel.id));
    finishHeldExit(row);
    setHeldActionState(button,'success','Dropped');
    resetHeldAction(button,'Drop',600,epoch);
    toast(sel.side==='resp'?'response dropped':'dropped');
  }catch(e){releaseHeldAction();setHeldActionState(button,'error','Drop failed');resetHeldAction(button,'Drop',900,epoch);toast(e.message);}};
export async function applyInterceptFilter(){
  const enabled=$('#interceptFilterOn').checked,target=$('#interceptFilterTarget').value,pattern=$('#interceptFilterPattern').value;
  try{const s=await api('/api/intercept/filter',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({enabled,target,pattern})});
    state.intercept=s;renderIntercept();toast(enabled&&pattern?'filter applied':'filter off');}catch(e){toast(e.message);}
}
// The conditional filter auto-applies on a debounce (and on Enter), so there is
// no Apply button — keeping one would be a redundant third commit path.
$('#interceptFilterPattern').addEventListener('keydown',e=>{if(e.key==='Enter'){e.preventDefault();applyInterceptFilter();}});
let icptFilterTimer=null;
function scheduleInterceptFilter(){
  clearTimeout(icptFilterTimer);
  icptFilterTimer=setTimeout(applyInterceptFilter,650);
}
['interceptFilterOn','interceptFilterTarget','interceptFilterPattern'].forEach(id=>{
  const el=$('#'+id);if(!el)return;
  el.addEventListener('change',scheduleInterceptFilter);
  if(el.tagName==='INPUT')el.addEventListener('input',scheduleInterceptFilter);
});

/* ---- rules ---- */
export function renderRules(){
  const body=$('#rulesBody');
  const det=document.querySelector('details.icpt-mr');
  if(det&&state.rules.length)det.open=true;
  if(!state.rules.length){body.innerHTML='<tr><td colspan="5" class="hint" style="padding:10px 8px">No rules. Add one below.</td></tr>';return;}
  body.innerHTML=state.rules.map(r=>`<tr data-id="${r.id}">
    <td><input type="checkbox" ${r.enabled?'checked':''} data-k="enabled"></td>
    <td><select data-k="type">${['req-header','req-body','res-header','res-body'].map(tp=>`<option value="${tp}" ${r.type===tp?'selected':''}>${tp}</option>`).join('')}</select></td>
    <td><input type="text" data-k="match" value="${escAttr(r.match)}"></td>
    <td><input type="text" data-k="replace" value="${escAttr(r.replace)}"></td>
    <td><button class="btn danger" data-del="${r.id}">Delete</button></td></tr>`).join('');
  body.querySelectorAll('tr').forEach(tr=>{
    const id=Number(tr.dataset.id);
    tr.querySelectorAll('[data-k]').forEach(inp=>{
      inp.addEventListener('change',()=>updateRule(id,tr));
    });
  });
  body.querySelectorAll('[data-del]').forEach(b=>b.onclick=()=>deleteRule(Number(b.dataset.del)));
}
export async function loadRules(){try{const d=await api('/api/rules');state.rules=d.rules||[];renderRules();}catch(e){toast(e.message);}}
export async function updateRule(id,tr){
  const r=state.rules.find(x=>x.id===id);if(!r)return;
  const get=k=>tr.querySelector(`[data-k="${k}"]`);
  const upd={id,ord:r.ord,enabled:get('enabled').checked,type:get('type').value,match:get('match').value,replace:get('replace').value};
  try{await api('/api/rules/'+id,{method:'PUT',headers:{'content-type':'application/json'},body:JSON.stringify(upd)});toast('rule saved');}catch(e){toast(e.message);loadRules();}
}
export async function deleteRule(id){try{await api('/api/rules/'+id,{method:'DELETE'});loadRules();}catch(e){toast(e.message);}}
$('#addRuleBtn').onclick=async()=>{
  const rule={type:$('#newRuleType').value,match:$('#newRuleMatch').value,replace:$('#newRuleReplace').value,enabled:true};
  if(!rule.match){toast('match regex required');return;}
  try{await api('/api/rules',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify(rule)});
    $('#newRuleMatch').value='';$('#newRuleReplace').value='';loadRules();toast('rule added');}catch(e){toast(e.message);}
};
