import { $, $$, esc, escAttr, state, toast, api, methodColor } from './core.js';

/* ---- intercept ---- */
// One unified hold queue (requests + responses) feeding one editor. state.heldSel
// is {id, side:'req'|'resp'} for the selected item, or null.
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
  const total=items.length;
  const badge=$('#heldBadge');if(badge){badge.style.display=total?'inline-block':'none';badge.textContent=total;}
  const ht=$('#heldTotal');if(ht){ht.style.display=total?'inline-block':'none';ht.textContent=total;}
  const list=$('#heldList');
  if(!total){list.innerHTML='';state.heldSel=null;showEditor(null);return;}
  list.innerHTML=items.map(h=>`<div class="icpt-item${(state.heldSel&&state.heldSel.id===h.id&&state.heldSel.side===h.side)?' sel':''}" data-id="${h.id}" data-side="${h.side}">
    <span class="icpt-tag ${h.side}">${h.side==='req'?'REQ':'RESP'}</span>
    ${h.side==='req'?`<span class="m" style="color:${methodColor(h.method)}">${esc(h.method)}</span>`:''}
    <span class="u">${esc(h.host)}${esc(h.path)}</span></div>`).join('');
  $$('#heldList .icpt-item').forEach(el=>el.onclick=()=>selectHeld(Number(el.dataset.id),el.dataset.side));
  const cur=state.heldSel&&items.find(h=>h.id===state.heldSel.id&&h.side===state.heldSel.side);
  if(cur)selectHeld(cur.id,cur.side); else selectHeld(items[0].id,items[0].side);
}
function setSwitch(btnSel,stateSel,on){
  const b=$(btnSel);if(b){b.classList.toggle('on',!!on);b.setAttribute('aria-pressed',on?'true':'false');}
  const s=$(stateSel);if(s)s.textContent=on?'On':'Off';
}
function heldItem(id,side){const q=side==='resp'?(state.intercept.responseQueue||[]):(state.intercept.queue||[]);return q.find(x=>x.id===id);}
function showEditor(h){
  const head=$('#heldEditor'),ta=$('#heldRaw'),empty=$('#heldEmpty'),title=$('#heldTitle');
  if(!h){if(head)head.style.display='none';if(ta)ta.style.display='none';if(empty)empty.style.display='flex';return;}
  if(head)head.style.display='flex';
  if(empty)empty.style.display='none';
  if(ta){ta.style.display='block';ta.value=h.raw||'';}
  if(title)title.innerHTML=h.side==='resp'
    ?`<span class="icpt-tag resp" style="margin-right:8px">RESP</span><span class="u">${esc(h.host)}${esc(h.path)}</span>`
    :`<span style="color:${methodColor(h.method)};font-weight:700">${esc(h.method)}</span> ${esc(h.host)}${esc(h.path)}`;
}
export function selectHeld(id,side){
  state.heldSel={id,side};
  $$('#heldList .icpt-item').forEach(el=>el.classList.toggle('sel',Number(el.dataset.id)===id&&el.dataset.side===side));
  const h=heldItem(id,side);if(h)showEditor({...h,side});
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
$('#forwardBtn').onclick=async()=>{const sel=state.heldSel;if(!sel)return;
  const base=sel.side==='resp'?'/api/intercept/response/':'/api/intercept/';
  try{await api(base+sel.id+'/forward',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({raw:$('#heldRaw').value})});
    toast(sel.side==='resp'?'response forwarded':'forwarded');}catch(e){toast(e.message);}};
$('#dropBtn').onclick=async()=>{const sel=state.heldSel;if(!sel)return;
  const base=sel.side==='resp'?'/api/intercept/response/':'/api/intercept/';
  try{await api(base+sel.id+'/drop',{method:'POST'});toast(sel.side==='resp'?'response dropped':'dropped');}catch(e){toast(e.message);}};
export async function applyInterceptFilter(){
  const enabled=$('#interceptFilterOn').checked,target=$('#interceptFilterTarget').value,pattern=$('#interceptFilterPattern').value;
  try{const s=await api('/api/intercept/filter',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({enabled,target,pattern})});
    state.intercept=s;renderIntercept();toast(enabled&&pattern?'filter applied':'filter off');}catch(e){toast(e.message);}
}
$('#interceptFilterApply').onclick=applyInterceptFilter;
$('#interceptFilterPattern').addEventListener('keydown',e=>{if(e.key==='Enter'){e.preventDefault();applyInterceptFilter();}});

/* ---- rules ---- */
export function renderRules(){
  const body=$('#rulesBody');
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
      const ev=inp.tagName==='SELECT'||inp.type==='checkbox'?'change':'change';
      inp.addEventListener(ev,()=>updateRule(id,tr));
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
