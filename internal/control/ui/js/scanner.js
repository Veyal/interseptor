import { $, esc, escAttr, state, toast, api, openModal, closeModal, copyText, fmtTime, renderMD, pickTextFile, normalizeListText, DEC_OPS, wireRowKey, saveFile, uiConfirm, renderLoadError, icon } from './core.js';
import { flowPopup } from './flowmodal.js';
import { openFinding } from './findings.js';
import { animateOnce, MOTION } from './motion.js';

/* ---- out-of-band (OOB) interaction catcher ---- */
let oobLoadEpoch=0;
let oobClearEpoch=0;
let oobGenerateEpoch=0;
let oobBaseEditEpoch=0;
let oobBaseAcknowledgedEditEpoch=0;
let oobBaseSaveEpoch=0;
let oobBaseSaveQueue=Promise.resolve();
export async function loadOob(baseOwner=null){
  const epoch=++oobLoadEpoch;
  const baseLoadEditEpoch=oobBaseEditEpoch;
  const status=$('#oobLoadState');
  if(status)status.textContent='Loading interactions…';
  try{
    const d=await api('/api/oob/state');
    if(epoch!==oobLoadEpoch)return;
    const base=$('#oobBase');
    const ownsBase=baseOwner
      ?baseOwner.editEpoch===oobBaseEditEpoch&&base.value.trim()===baseOwner.value
      :baseLoadEditEpoch===oobBaseEditEpoch&&oobBaseEditEpoch===oobBaseAcknowledgedEditEpoch;
    if(ownsBase&&document.activeElement!==base)base.value=d.baseUrl||'';
    renderOobList(d.interactions||[]);
    if(status)status.textContent='';
  }catch(e){
    if(epoch!==oobLoadEpoch)return;
    if(status){
      status.innerHTML='<span class="state-error-msg" role="alert">Couldn\'t load interactions: '+esc(e.message||'request failed')+'</span> <button type="button" class="btn xs" data-oob-retry>Retry</button>';
      const retry=status.querySelector('[data-oob-retry]');
      if(retry)retry.onclick=()=>loadOob();
    }
  }
}
function renderOobList(list){
  const c=$('#oobCount');if(c)c.textContent=list.length?list.length+' interaction'+(list.length===1?'':'s'):'';
  const box=$('#oobList');if(!box)return;
  if(!list.length){box.innerHTML='<div class="hint">No interactions yet — callbacks to a generated URL appear here live.</div>';return;}
  box.innerHTML=list.map(it=>`<div class="oob-row">
    <span class="oob-m">${esc(it.method)}</span>
    <span class="oob-p" title="${escAttr(it.path+(it.query?'?'+it.query:''))}">${esc(it.path)}${it.query?'<span style="color:var(--fg3)">?'+esc(it.query)+'</span>':''}</span>
    <span class="oob-src" title="source · ${escAttr(it.userAgent||'')}">${esc(it.remoteAddr||'')}</span>
    <span class="oob-t">${fmtTime(it.ts)}</span></div>`).join('');
}
$('#oobBtn')&&($('#oobBtn').onclick=()=>{
  if(!state.oobEnabled){toast('OOB is disabled — enable in Settings → Scanner');return;}
  openModal($('#oobModal'));loadOob();
});
$('#oobClose')&&($('#oobClose').onclick=()=>closeModal($('#oobModal')));
$('#oobGen')&&($('#oobGen').onclick=async()=>{
  const button=$('#oobGen');
  if(button.disabled)return;
  const epoch=++oobGenerateEpoch;
  button.disabled=true;button.setAttribute('aria-busy','true');button.textContent='Generating…';
  try{const r=await api('/api/oob/new',{method:'POST'});$('#oobUrl').value=r.url||'';copyText(r.url||'','OOB URL generated & copied');}
  catch(e){toast(e.message,'error');}
  finally{if(epoch===oobGenerateEpoch){button.disabled=false;button.setAttribute('aria-busy','false');button.textContent='＋ Generate payload URL';}}
});
$('#oobCopy')&&($('#oobCopy').onclick=()=>{const u=$('#oobUrl').value;if(u)copyText(u,'OOB URL copied');else toast('generate a URL first');});
$('#oobSaveBase')&&($('#oobSaveBase').onclick=async()=>{
  const input=$('#oobBase'),button=$('#oobSaveBase');
  const submitted={editEpoch:oobBaseEditEpoch,value:input.value.trim()};
  const saveEpoch=++oobBaseSaveEpoch;
  button.setAttribute('aria-busy','true');button.textContent='Saving…';
  const task=oobBaseSaveQueue.catch(()=>{}).then(()=>api('/api/oob/base',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({baseUrl:submitted.value})}));
  oobBaseSaveQueue=task.catch(()=>{});
  try{
    await task;
    oobBaseAcknowledgedEditEpoch=submitted.editEpoch;
    if(saveEpoch!==oobBaseSaveEpoch)return;
    toast('OOB base saved');
    await loadOob(submitted);
  }catch(e){if(saveEpoch===oobBaseSaveEpoch)toast(e.message,'error');}
  finally{if(saveEpoch===oobBaseSaveEpoch){button.setAttribute('aria-busy','false');button.textContent='Save';}}
});
$('#oobBase')?.addEventListener('input',()=>{oobBaseEditEpoch++;});
$('#oobClear')&&($('#oobClear').onclick=async()=>{
  const button=$('#oobClear');
  if(button.disabled)return;
  if(!await uiConfirm('Clear OOB interactions?','Remove all captured callback interactions? This evidence cannot be recovered.','Clear interactions','btn danger','var(--red)'))return;
  const epoch=++oobClearEpoch;
  button.disabled=true;button.setAttribute('aria-busy','true');button.textContent='Clearing…';
  try{await api('/api/oob/interactions',{method:'DELETE'});await loadOob();toast('OOB interactions cleared');}
  catch(e){toast(e.message,'error');}
  finally{if(epoch===oobClearEpoch){button.disabled=false;button.setAttribute('aria-busy','false');button.textContent='Clear';}}
});

function oobTunnelCmd(){return 'cloudflared tunnel --url http://'+(state.controlAddr||'127.0.0.1:9966');}
$('#oobModalTunnelCopy')&&($('#oobModalTunnelCopy').onclick=()=>copyText(oobTunnelCmd(),'Tunnel command copied'));

/* ---- custom checks editor ---- */
let checkMode='code',checkDocsLoaded=false;
let checkSelId=null;
let checkBuiltin=false,checkOverridden=false;
const checkEndpoint='/api/checks';
let checkLoadEpoch=0;
let checkActionEpoch=0;
let checkActionBusy=false;
let checkEditorReady=false;
let checkEditorLoading=false;
let checkRestoreFocus=false;
let checkToggleEpoch=0;
let checkListEpoch=0;
let checkToggleBusy=false;
let checkPacksLoadEpoch=0;
let checkPackMutationEpoch=0;
let checkPackMutationBusy=false;
let checkDraftEpoch=0;
function syncCheckEditorControls(){
  const test=$('#checkTest'),save=$('#checkSave'),del=$('#checkDelete');
  const blocked=checkActionBusy||checkEditorLoading||!checkEditorReady;
  [test,save].forEach(button=>{if(button){button.disabled=blocked;button.setAttribute('aria-busy',checkActionBusy?'true':'false');}});
  ['checkId','checkSrc'].forEach(id=>{const control=$('#'+id);if(control)control.disabled=blocked;});
  const fresh=$('#checkNew');if(fresh)fresh.disabled=checkActionBusy;
  const close=$('#checksClose');if(close)close.disabled=checkActionBusy;
  $('#checksList')?.querySelectorAll('button,input').forEach(control=>{control.disabled=checkActionBusy;});
  if(del){del.disabled=blocked||(checkBuiltin&&!checkOverridden);del.setAttribute('aria-busy',checkActionBusy?'true':'false');}
}
function setCheckActionState(kind,stateName){
  const test=$('#checkTest'),save=$('#checkSave'),del=$('#checkDelete');
  if(!test||!save)return;
  const busy=stateName==='pending';
  checkActionBusy=busy;
  syncCheckEditorControls();
  test.classList.remove('is-pending','is-success','is-error');
  save.classList.remove('is-pending','is-success','is-error');
  if(stateName!=='idle'){
    if(kind==='delete'&&del){
      del.textContent=stateName==='pending'?'Deleting…':(stateName==='error'?'Delete failed':'Deleted');
    }else{
      const button=kind==='test'?test:save;
      button.classList.add('is-'+stateName);
      button.textContent=stateName==='pending'?(kind==='test'?'Testing…':'Saving…'):(stateName==='success'?(kind==='test'?'Tested':'Saved'):(kind==='test'?'Test failed':'Save failed'));
    }
  }else{
    test.textContent='Test ▸';save.textContent='Save';
    updateCheckDeleteLabel();
  }
}
function resetCheckAction(kind,delay,epoch){
  setTimeout(()=>{if(epoch===checkActionEpoch)setCheckActionState(kind,'idle');},delay);
}
function cancelCheckAction(){
  checkActionEpoch++;
  if(checkActionBusy)setCheckActionState('other','idle');
}
function checkSetEditorReadonly(on){const el=$('#checkId');if(el)el.readOnly=!!on;}
function setCheckOutcome(out,html,kind='status'){
  if(!out)return;
  const role=kind==='error'?'alert':'status';
  const live=kind==='error'?'assertive':'polite';
  out.setAttribute('role',role);
  out.setAttribute('aria-live',live);
  out.innerHTML=html;
}
function restoreCheckEditorFocus(target){
  if(!checkRestoreFocus||!target)return;
  checkRestoreFocus=false;
  target.focus({preventScroll:true});
}
function setCheckEditorLoadState(stateName,id,error,retry){
  checkEditorLoading=stateName==='loading';
  checkEditorReady=stateName==='ready';
  const out=$('#checkOut');
  if(out){
    out.setAttribute('aria-busy',checkEditorLoading?'true':'false');
    if(stateName==='loading'){
      setCheckOutcome(out,'<div class="check-status check-status-pending">Loading check <b>'+esc(id)+'</b>…</div>');
    }else if(stateName==='error'){
      setCheckOutcome(out,'<div class="check-status check-status-error">Couldn\'t load <b>'+esc(id)+'</b>: '+esc(error||'request failed')+' <button type="button" class="btn xs" data-check-retry>Retry</button></div>','error');
      const button=out.querySelector('[data-check-retry]');
      if(button&&retry)button.onclick=event=>retry({restoreFocus:event.detail===0});
      if(checkRestoreFocus&&button){checkRestoreFocus=false;button.focus({preventScroll:true});}
    }
  }
  syncCheckEditorControls();
  if(stateName==='ready')restoreCheckEditorFocus($('#checkId')||out);
}
function checkSetMode(mode){
  checkMode=mode;
  const seg=$('#checkModeSeg');
  if(seg)seg.querySelectorAll('[data-mode]').forEach(b=>{
    const on=b.dataset.mode===mode;
    b.classList.toggle('on',on);
    b.setAttribute('aria-selected',on?'true':'false');
    b.tabIndex=on?0:-1;
  });
  const panes={code:'#checkPaneCode',docs:'#checkPaneDocs'};
  Object.entries(panes).forEach(([m,sel])=>{const el=$(sel);if(el){const on=m===mode;el.style.display=on?'':'none';el.hidden=!on;}});
  if(mode==='docs')loadCheckDocs();
}
function wireCheckModeKeys(seg){
  if(!seg)return;
  const tabs=[...seg.querySelectorAll('[role="tab"]')];
  tabs.forEach((tab,i)=>tab.addEventListener('keydown',e=>{
    let next=-1;
    if(e.key==='ArrowRight'||e.key==='ArrowDown')next=(i+1)%tabs.length;
    else if(e.key==='ArrowLeft'||e.key==='ArrowUp')next=(i-1+tabs.length)%tabs.length;
    else if(e.key==='Home')next=0;
    else if(e.key==='End')next=tabs.length-1;
    else return;
    e.preventDefault();tabs[next].focus();tabs[next].click();
  }));
}
async function loadCheckDocs(){
  if(checkDocsLoaded)return;
  const box=$('#checkDocs');if(!box)return;
  try{
    const d=await api('/api/checks/reference');
    box.innerHTML=renderMD(d.markdown||'');
    checkDocsLoaded=true;
  }catch(e){
    box.innerHTML='<div class="state-error"><div class="state-error-icon"><svg class="icon" aria-hidden="true" focusable="false"><use href="#i-warning"/></svg></div><p class="state-error-msg" role="alert">'+esc(e.message)+'</p><button type="button" class="btn" data-check-docs-retry>Retry</button></div>';
    const retry=box.querySelector('[data-check-docs-retry]');if(retry)retry.onclick=loadCheckDocs;
  }
}
function updateCheckFlowHint(){
  const el=$('#checkFlowHint');if(!el)return;
  el.textContent=state.selId!=null?('Test flow: #'+state.selId+' (selected)'):'Test uses latest captured flow';
}
function markChecksSelected(box){
  if(!box)return;
  box.querySelectorAll('.checks-pick[data-id]').forEach(el=>{
    el.classList.toggle('sel',!!checkSelId&&el.dataset.id===checkSelId);
  });
}
async function saveCheckToggle(cb,box){
  const previous=!cb.checked;
  const epoch=++checkToggleEpoch;
  checkToggleBusy=true;
  checkListEpoch++;
  const toggles=[...box.querySelectorAll('.check-en')];
  const disabled=toggles.filter(x=>!x.checked).map(x=>x.dataset.id);
  toggles.forEach(toggle=>{toggle.disabled=true;});
  box.setAttribute('aria-busy','true');
  try{
    await api('/api/checks/disabled',{method:'PUT',headers:{'content-type':'application/json'},body:JSON.stringify({disabled})});
    if(epoch!==checkToggleEpoch)return;
    toast('check '+(cb.checked?'enabled':'disabled'));
  }catch(e){
    if(epoch!==checkToggleEpoch)return;
    cb.checked=previous;
    toast(e.message,'error');
  }finally{
    if(epoch===checkToggleEpoch){
      checkToggleBusy=false;
      checkListEpoch++;
      box.removeAttribute('aria-busy');
      await loadChecksList();
    }
  }
}
export async function loadChecksList(){
  const epoch=++checkListEpoch;
  try{
    const d=await api('/api/checks');const box=$('#checksList');if(!box)return;
    if(epoch!==checkListEpoch||checkToggleBusy)return;
    const cs=d.checks||[];const dis=new Set(d.disabled||[]);
    const builtin=d.builtin||[];
    const sevBadge=s=>`<span class="sev ${escAttr(s)}" style="font-size:var(--fs-xs)">${esc(s)}</span>`;
    const catBadge=c=>c?`<span class="checks-cat">${esc(c)}</span>`:'';
    const builtinIds=new Set(builtin.map(b=>b.id));
    const customPassive=cs.filter(c=>!builtinIds.has(c.id));
    const row=(opts)=>{
      const cb=opts.toggleable!==false?`<input type="checkbox" class="check-en" data-id="${escAttr(opts.id)}" ${dis.has(opts.id)?'':'checked'} aria-label="enable ${escAttr(opts.title)}">`:'';
      const pick=opts.pickable?' checks-pick':'';
      const cls=['checks-row',opts.rowClass||'',pick].filter(Boolean).join(' ');
      const data=opts.id?` data-id="${escAttr(opts.id)}"`:'';
      const titleColor=opts.error?'var(--red)':'var(--fg)';
      const ov=opts.overridden?'<span class="checks-cat" style="color:var(--accent)">customized</span>':'';
      return `<div class="${cls}"${data} title="${escAttr(opts.hint||'')}" aria-label="${escAttr(opts.aria||opts.title)}">
        ${cb}<button type="button" class="checks-body checks-edit-target" style="padding:0;border:0;background:transparent;color:inherit;text-align:left;font:inherit;cursor:pointer" aria-label="Edit ${escAttr(opts.title)}">
        <span class="checks-title" style="color:${titleColor}" title="${escAttr(opts.title)}">${esc(opts.title)}${opts.error?' <svg class="icon" aria-hidden="true" focusable="false"><use href="#i-warning"/></svg>':''}</span>
        <div class="checks-meta">${opts.severity?sevBadge(opts.severity):''}${opts.category?catBadge(opts.category):''}${ov}</div>
        </button></div>`;
    };
    const group=(title,open,body)=>`<details class="checks-group${open?' checks-group-custom':''}"${open?' open':''} data-default-open="${open?'1':'0'}"><summary>${title}</summary><div class="checks-group-body">${body}</div></details>`;
    let html='';
    let customBody='';
    if(!customPassive.length){
      customBody='<div class="hint" style="padding:8px 12px;line-height:1.5">No extra passive checks — customize a <b>built-in</b> (click it) or <b>+ New passive</b>.</div>';
    }else{
      customBody=customPassive.map(c=>row({id:c.id,title:c.id,pickable:true,rowClass:'checks-custom',category:'custom',error:c.error,hint:'click to edit',aria:'custom check '+c.id})).join('');
    }
    html+=group(`CUSTOM · PASSIVE (${customPassive.length})`,true,customBody);
    if(builtin.length){
      const builtinBody=builtin.map(b=>row({id:b.id,title:b.title,pickable:true,rowClass:'checks-builtin',severity:b.severity,category:b.category,overridden:!!b.overridden,hint:(b.description||'')+' — click to edit Starlark override',aria:'built-in check '+b.title,toggleable:true})).join('');
      html+=group(`BUILT-IN · PASSIVE (${builtin.length}) — click to edit`,false,builtinBody);
    }
    box.innerHTML=html;
    box.removeAttribute('aria-busy');
    markChecksSelected(box);
    box.querySelectorAll('.checks-pick[data-id]').forEach(el=>{
      const id=el.dataset.id;
      const builtin=el.classList.contains('checks-builtin');
      const open=()=>builtin?loadBuiltinCheck(id):loadCheck(id);
      el.querySelector('.checks-edit-target')?.addEventListener('click',open);
    });
    // Persist the whole disabled set as one acknowledged transaction. Disable
    // sibling toggles until it resolves so rapid clicks cannot reorder writes.
    box.querySelectorAll('.check-en').forEach(cb=>cb.onchange=()=>saveCheckToggle(cb,box));
    syncCheckEditorControls();
    checksApplyFilter(); // re-apply an active filter across the freshly rendered rows
  }catch(e){
    if(epoch!==checkListEpoch||checkToggleBusy)return;
    const box=$('#checksList');if(!box)return;
    box.removeAttribute('aria-busy');
    box.innerHTML=`<div class="state-error"><div class="state-error-icon"><svg class="icon" aria-hidden="true" focusable="false"><use href="#i-warning"/></svg></div><p class="state-error-msg" role="alert">Couldn't load checks: ${esc(e.message)}</p><button type="button" class="btn" data-checks-list-retry>Retry</button></div>`;
    box.querySelector('[data-checks-list-retry]')?.addEventListener('click',loadChecksList);
  }
}
// Filters the sidebar by title/id substring match. Groups auto-expand while a
// filter is active (so a match in a collapsed built-in group is still found)
// and collapse back to their default open/closed state once cleared.
function checksApplyFilter(){
  const q=(($('#checksSearch')||{}).value||'').trim().toLowerCase();
  const box=$('#checksList');if(!box)return;
  box.querySelectorAll('.checks-group').forEach(group=>{
    let anyVisible=false;
    group.querySelectorAll('.checks-row').forEach(row=>{
      const hay=(row.querySelector('.checks-title')?.textContent||'')+' '+(row.dataset.id||'');
      const match=!q||hay.toLowerCase().includes(q);
      row.style.display=match?'':'none';
      if(match)anyVisible=true;
    });
    if(q){group.classList.toggle('checks-group-empty',!anyVisible);group.open=anyVisible;}
    else{group.classList.remove('checks-group-empty');group.open=group.dataset.defaultOpen==='1';}
  });
}
function refreshCheckEditorMode(){
  const kh=$('#checkKindHint');
  if(kh){kh.style.display='none';kh.textContent='';}
}
// The single Delete/Revert button is dual-purpose: for a built-in check it
// reverts a saved Starlark override (disabled when there's no override to
// revert); for anything else it deletes the custom check outright. The label
// switches so the two are never confused.
function updateCheckDeleteLabel(){
  const btn=$('#checkDelete');if(!btn)return;
  if(checkBuiltin){
    btn.textContent='↺ Revert';
    btn.title=checkOverridden?'Delete your Starlark override — the built-in check runs again':'No override saved yet — nothing to revert';
    btn.disabled=!checkOverridden;
  }else{
    btn.innerHTML=icon('trash')+' Delete';
    btn.title='Delete this custom check';
  }
  syncCheckEditorControls();
}
export async function loadBuiltinCheck(id,{preserveAction=false,restoreFocus=false}={}){
  const epoch=++checkLoadEpoch;
  checkDraftEpoch++;
  checkEditorReady=false;checkEditorLoading=true;
  checkRestoreFocus=!!restoreFocus;
  if(!preserveAction)cancelCheckAction();
  checkBuiltin=true;checkSelId=id;refreshCheckEditorMode();
  $('#checkId').value=id;checkSetEditorReadonly(true);
  $('#checkSrc').value='';
  setCheckEditorLoadState('loading',id);
  try{
    const d=await api(checkEndpoint+'/'+encodeURIComponent(id));
    if(epoch!==checkLoadEpoch||checkSelId!==id||!checkBuiltin)return;
    checkOverridden=!!d.overridden;
    $('#checkId').value=id;checkSetEditorReadonly(true);
    $('#checkSrc').value=d.source||'';
    setCheckEditorLoadState('ready',id);
    const note=checkOverridden?'your Starlark override is active':'edit & Save to write ~/.interseptor/checks/'+id+'.star';
    setCheckOutcome($('#checkOut'),'<div class="check-status check-status-pending">Built-in <b>'+esc(id)+'</b> — '+note+'</div>');
    updateCheckDeleteLabel();
    markChecksSelected($('#checksList'));
  }catch(e){if(epoch===checkLoadEpoch&&checkSelId===id&&checkBuiltin){setCheckEditorLoadState('error',id,e.message,({restoreFocus=false}={})=>loadBuiltinCheck(id,{restoreFocus}));}}
}
export async function loadCheck(id,{restoreFocus=false}={}){
  const epoch=++checkLoadEpoch;
  checkDraftEpoch++;
  checkEditorReady=false;checkEditorLoading=true;
  checkRestoreFocus=!!restoreFocus;
  cancelCheckAction();
  checkBuiltin=false;checkOverridden=false;checkSelId=id;refreshCheckEditorMode();
  $('#checkId').value=id;checkSetEditorReadonly(false);
  $('#checkSrc').value='';
  setCheckEditorLoadState('loading',id);
  try{const d=await api(checkEndpoint+'/'+encodeURIComponent(id));
    if(epoch!==checkLoadEpoch||checkSelId!==id||checkBuiltin)return;
    $('#checkId').value=id;checkSetEditorReadonly(false);
    $('#checkSrc').value=d.source||'';
    setCheckEditorLoadState('ready',id);
    setCheckOutcome($('#checkOut'),'<div class="check-status check-status-pending">Loaded <b>'+esc(id)+'</b> (passive). Edit on <b>Code</b>, then Save.</div>');
    updateCheckDeleteLabel();
    markChecksSelected($('#checksList'));}catch(e){if(epoch===checkLoadEpoch&&checkSelId===id&&!checkBuiltin){setCheckEditorLoadState('error',id,e.message,({restoreFocus=false}={})=>loadCheck(id,{restoreFocus}));}}
}
export function checkNew(){
  checkLoadEpoch++;
  checkDraftEpoch++;
  cancelCheckAction();
  checkEditorLoading=false;checkEditorReady=true;
  checkRestoreFocus=false;
  checkBuiltin=false;checkOverridden=false;checkSelId=null;refreshCheckEditorMode();
  checkSetEditorReadonly(false);
  $('#checkId').value='';
  $('#checkSrc').value = "def check(flow):\n    # inspect flow, return a list of finding(...)\n    return []\n";
  setCheckOutcome($('#checkOut'),'<div class="check-status check-status-pending">New passive check — set an id, write Starlark on <b>Code</b>, Test, then Save.</div>');$('#checkId').focus();
  updateCheckDeleteLabel();
  markChecksSelected($('#checksList'));
}
export async function checkTest(){
  if(checkActionBusy)return;
  if(checkEditorLoading||!checkEditorReady)return;
  const epoch=++checkActionEpoch,selection=checkSelId;
  const draftEpoch=checkDraftEpoch;
  const source=$('#checkSrc').value,flowId=state.selId||0;
  setCheckActionState('test','pending');
  const out=$('#checkOut');setCheckOutcome(out,'<div class="check-status check-status-pending">running…</div>');
  try{const r=await api(checkEndpoint+'/test',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({source,flowId})});
    if(epoch!==checkActionEpoch||selection!==checkSelId)return;
    if(draftEpoch!==checkDraftEpoch)return;
    if((state.selId||0)!==flowId){setCheckOutcome(out,'<div class="check-status check-status-pending">Test result ignored — selected flow changed. Test again.</div>');setCheckActionState('test','idle');return;}
    if(r.error){setCheckOutcome(out,'<div class="check-status check-status-error"><b>Compile/runtime error</b><pre>'+esc(r.error)+'</pre></div>','error');setCheckActionState('test','error');resetCheckAction('test',1000,epoch);return;}
    // Passive checks return {findings:[...]} (testCheck in internal/control/checks.go)
    // — zero, one, or many findings on the tested flow.
    const findings=r.findings||[];
    if(!findings.length){
      const note=r.note||'no finding';
      setCheckOutcome(out,`<div class="check-status check-status-ok"><div class="hint">${esc(note)}</div><div style="color:var(--accent);margin-top:4px">✓ No finding — check compiles &amp; runs.</div></div>`);
      setCheckActionState('test','success');resetCheckAction('test',800,epoch);
      return;
    }
    const note='finding'+(findings.length===1?'':'s')+' on flow #'+(r.flowId||'?');
    setCheckOutcome(out,`<div class="check-status check-status-finding"><div class="hint" style="margin-bottom:6px">${esc(note)}</div>`
      +findings.map(f=>`<div><span class="sev ${escAttr(f.severity)}">${esc(f.severity)}</span> ${esc(f.title)}${f.evidence?' <span class="hint">— '+esc(f.evidence)+'</span>':''}</div>`).join('')
      +`</div>`);
    setCheckActionState('test','success');resetCheckAction('test',800,epoch);
  }catch(e){
    if(epoch!==checkActionEpoch||selection!==checkSelId||draftEpoch!==checkDraftEpoch)return;
    if((state.selId||0)!==flowId){setCheckOutcome(out,'<div class="check-status check-status-pending">Test result ignored — selected flow changed. Test again.</div>');setCheckActionState('test','idle');return;}
    setCheckOutcome(out,'<div class="check-status check-status-error"><b>Request failed</b><pre>'+esc(e.message)+'</pre></div>','error');
    setCheckActionState('test','error');resetCheckAction('test',1000,epoch);
  }
}
export async function checkSave(){
  if(checkActionBusy)return;
  if(checkEditorLoading||!checkEditorReady)return;
  const id=$('#checkId').value.trim();if(!id){toast('set a check id first');return;}
  const source=$('#checkSrc').value;
  const epoch=++checkActionEpoch,selection=checkSelId;
  const draftEpoch=checkDraftEpoch;
  setCheckActionState('save','pending');
  const out=$('#checkOut');
  setCheckOutcome(out,'<div class="check-status check-status-pending">saving…</div>');
  try{await api(checkEndpoint+'/'+encodeURIComponent(id),{method:'PUT',headers:{'content-type':'application/json'},body:JSON.stringify({source})});
    loadChecksList();
    if(epoch!==checkActionEpoch||selection!==checkSelId)return;
    if(draftEpoch!==checkDraftEpoch)return;
    checkOverridden=checkBuiltin||checkOverridden;
    setCheckOutcome(out,'<div class="check-status check-status-ok">Saved ✓ — runs on the next passive scan'+(checkBuiltin?' (replaces built-in)':'')+'.</div>');
    updateCheckDeleteLabel();
    setCheckActionState('save','success');resetCheckAction('save',800,epoch);}
  catch(e){
    if(epoch!==checkActionEpoch)return;
    setCheckOutcome(out,'<div class="check-status check-status-error"><b>Save failed</b><pre>'+esc(e.message)+'</pre></div>','error');
    setCheckActionState('save','error');resetCheckAction('save',1000,epoch);
  }
}
export async function checkDelete(){
  if(checkActionBusy)return;
  if(checkEditorLoading||!checkEditorReady)return;
  const id=$('#checkId').value.trim();if(!id)return;
  if(checkBuiltin&&!checkOverridden){toast('no override saved — nothing to revert');return;}
  const label=checkBuiltin?'Revert override for built-in check':'Delete check';
  const body=checkBuiltin?`Delete your Starlark override for <b>${esc(id)}</b>? The compiled built-in will run again.`:`Delete passive check <b>${esc(id)}</b>? Its Starlark source will be removed.`;
  const selection=checkSelId,draftEpoch=checkDraftEpoch,builtin=checkBuiltin;
  if(!await uiConfirm(label,body,builtin?'Revert':'Delete','btn danger','var(--red)'))return;
  if(checkActionBusy||selection!==checkSelId||draftEpoch!==checkDraftEpoch)return;
  const epoch=++checkActionEpoch;
  setCheckActionState('delete','pending');
  try{
    await api(checkEndpoint+'/'+encodeURIComponent(id),{method:'DELETE'});
    if(epoch!==checkActionEpoch||selection!==checkSelId||draftEpoch!==checkDraftEpoch)return;
    if(builtin){checkOverridden=false;updateCheckDeleteLabel();await loadBuiltinCheck(id,{preserveAction:true});if(epoch!==checkActionEpoch)return;setCheckActionState('delete','idle');}
    else{setCheckActionState('delete','idle');checkNew();}
    loadChecksList();
    toast(builtin?'reverted to built-in':'deleted '+id);
  }catch(e){
    if(epoch!==checkActionEpoch)return;
    toast(e.message,'error');setCheckActionState('delete','error');resetCheckAction('delete',1000,epoch);
  }
}
async function loadPacksPanel(){
  const box=$('#checksPackList'); if(!box) return;
  const epoch=++checkPacksLoadEpoch;
  try{
    const [cat, inst] = await Promise.all([
      api('/api/packs/catalog'),
      api('/api/packs'),
    ]);
    if(epoch!==checkPacksLoadEpoch||checkPackMutationBusy)return;
    const catalog=cat.packs||[];
    const installed=inst.packs||[];
    let html='';
    if(catalog.length){
      html+='<div class="hint" style="margin-bottom:4px;font-weight:700">Official packs</div>';
      html+=catalog.map(p=>{
        const on=!!p.installed;
        return `<div class="checks-pack-row"><div><b>${esc(p.name)}</b> <span class="hint">v${esc(p.version)} · ${p.checks} checks</span><div class="hint">${esc(p.description||'')}</div></div>
          <button type="button" class="btn ${on?'':'btn-primary'}" data-pack="${escAttr(p.name)}" ${on?'disabled':''}>${on?'Installed':'Install'}</button></div>`;
      }).join('');
    }
    if(installed.length){
      html+='<div class="hint" style="margin:8px 0 4px;font-weight:700">Installed</div>';
      html+=installed.map(p=>{
        const sig=p.signed==='builtin'?'builtin ✓':(p.signed?('signed ✓ '+p.signed):'unsigned');
        return `<div class="checks-pack-row"><div><b>${esc(p.name)}</b> <span class="hint">v${esc(p.version||'')} · ${esc(sig)}</span></div>
        <button type="button" class="btn" data-remove="${escAttr(p.name)}" title="Uninstall pack">Remove</button></div>`;
      }).join('');
    }
    if(!html) html='<span class="hint">No packs yet — install an official pack or upload a signed .tar.gz.</span>';
    if(epoch!==checkPacksLoadEpoch||checkPackMutationBusy)return;
    box.innerHTML=html;
    box.querySelectorAll('[data-pack]').forEach(b=>b.onclick=async()=>{
      if(checkPackMutationBusy)return;
      const mutationEpoch=++checkPackMutationEpoch;
      checkPackMutationBusy=true;
      box.setAttribute('aria-busy','true');
      box.querySelectorAll('button').forEach(button=>button.disabled=true);
      b.textContent='Installing…';
      try{
        await api('/api/packs/catalog/'+encodeURIComponent(b.dataset.pack)+'/install',{method:'POST'});
        if(mutationEpoch!==checkPackMutationEpoch)return;
        toast('pack installed'); loadChecksList();
      }catch(e){if(mutationEpoch===checkPackMutationEpoch)toast(e.message,'error');}
      finally{if(mutationEpoch===checkPackMutationEpoch){checkPackMutationBusy=false;box.removeAttribute('aria-busy');loadPacksPanel();}}
    });
    box.querySelectorAll('[data-remove]').forEach(b=>b.onclick=async()=>{
      if(checkPackMutationBusy)return;
      if(!await uiConfirm('Remove pack?','Uninstall <b>'+esc(b.dataset.remove)+'</b> and delete its checks from disk?','Remove','btn danger')) return;
      const mutationEpoch=++checkPackMutationEpoch;
      checkPackMutationBusy=true;
      box.setAttribute('aria-busy','true');
      box.querySelectorAll('button').forEach(button=>button.disabled=true);
      try{
        await api('/api/packs/'+encodeURIComponent(b.dataset.remove),{method:'DELETE'});
        if(mutationEpoch!==checkPackMutationEpoch)return;
        toast('pack removed'); loadChecksList();
      }catch(e){if(mutationEpoch===checkPackMutationEpoch)toast(e.message,'error');}
      finally{if(mutationEpoch===checkPackMutationEpoch){checkPackMutationBusy=false;box.removeAttribute('aria-busy');loadPacksPanel();}}
    });
  }catch(e){if(epoch===checkPacksLoadEpoch&&!checkPackMutationBusy)renderLoadError(box,'Check packs',e,loadPacksPanel,false);}
}
async function installPackFile(file){
  if(!file) return;
  if(checkPackMutationBusy)return;
  const mutationEpoch=++checkPackMutationEpoch;
  checkPackMutationBusy=true;
  const box=$('#checksPackList');
  if(box){box.setAttribute('aria-busy','true');box.querySelectorAll('button').forEach(button=>button.disabled=true);}
  try{
    const buf=await file.arrayBuffer();
    const allow=$('#checksPackAllowUnsigned')&&$('#checksPackAllowUnsigned').checked;
    const q=allow?'?allowUnsigned=1':'';
    await api('/api/packs/install'+q,{method:'POST',headers:{'content-type':'application/gzip'},body:buf});
    if(mutationEpoch!==checkPackMutationEpoch)return;
    toast('pack installed from '+file.name); loadChecksList();
  }catch(e){if(mutationEpoch===checkPackMutationEpoch)toast(e.message||'install failed','error');}
  finally{if(mutationEpoch===checkPackMutationEpoch){checkPackMutationBusy=false;if(box)box.removeAttribute('aria-busy');loadPacksPanel();}}
}
function closeChecks(){if(checkActionBusy)return;checkLoadEpoch++;checkDraftEpoch++;cancelCheckAction();closeModal($('#checksModal'));}
export function openChecks(){openModal($('#checksModal'),{onEscape:closeChecks,onDismiss:closeChecks});const s=$('#checksSearch');if(s)s.value='';loadChecksList();loadPacksPanel();updateCheckFlowHint();if(!$('#checkSrc').value)checkNew();checkSetMode('code');}
if($('#checksBtn'))$('#checksBtn').onclick=openChecks;
if($('#checksPackFile'))$('#checksPackFile').onchange=e=>{const f=e.target.files&&e.target.files[0]; if(f) installPackFile(f); e.target.value='';};
if($('#checksClose'))$('#checksClose').onclick=closeChecks;
if($('#checkNew'))$('#checkNew').onclick=checkNew;
if($('#checkTest'))$('#checkTest').onclick=checkTest;
if($('#checkSave'))$('#checkSave').onclick=checkSave;
if($('#checkDelete'))$('#checkDelete').onclick=checkDelete;
if($('#checkModeSeg'))$('#checkModeSeg').querySelectorAll('[data-mode]').forEach(b=>b.onclick=()=>checkSetMode(b.dataset.mode));
wireCheckModeKeys($('#checkModeSeg'));
if($('#checksSearch'))$('#checksSearch').oninput=checksApplyFilter;
['#checkId','#checkSrc'].forEach(sel=>$(sel)?.addEventListener('input',()=>{checkLoadEpoch++;checkDraftEpoch++;cancelCheckAction();}));

/* ---- decoder ---- */
export { DEC_OPS };
let decRequestEpoch=0;
let decModalEpoch=0;
function decSetPending(on,op){
  const ops=$('#decOps'),err=$('#decErr');
  if(ops)ops.setAttribute('aria-busy',on?'true':'false');
  if(!err)return;
  if(on){err.style.color='var(--fg3)';err.textContent=(op||'Operation')+'…';}
  else if(err.textContent.endsWith('…')){err.textContent='';}
}
function decInvalidatePending(){decRequestEpoch++;decSetPending(false);}
function decCurrent(epoch,modalEpoch,input){
  const modal=$('#decModal'),field=$('#decIn');
  return epoch===decRequestEpoch&&modalEpoch===decModalEpoch&&modal?.style.display==='flex'&&field?.value===input;
}
export function decBuildOps(){const box=$('#decOps');if(!box||box._built)return;box._built=1;
  box.innerHTML=DEC_OPS.map(([op,label])=>`<button class="btn" data-op="${op}">${esc(label)}</button>`).join('');
  box.querySelectorAll('[data-op]').forEach(b=>b.onclick=()=>decApply(b.dataset.op));}
export async function decApply(op){
  const epoch=++decRequestEpoch;
  const modalEpoch=decModalEpoch;
  const input=$('#decIn').value;
  const err=$('#decErr');
  decSetPending(true,op);
  try{const r=await api('/api/decode',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({op,input})});
    if(!decCurrent(epoch,modalEpoch,input))return;
    if(r.error){err.style.color='var(--red)';err.textContent=r.error;decSetPending(false);return;}
    $('#decOut').value=r.output;decSetPending(false);}
  catch(e){if(!decCurrent(epoch,modalEpoch,input))return;err.style.color='var(--red)';err.textContent=e.message;decSetPending(false);}
}
export function openDecoder(seed){decBuildOps();decModalEpoch++;decInvalidatePending();openModal($('#decModal'));if(seed)$('#decIn').value=seed;$('#decOut').value='';$('#decErr').textContent='';setTimeout(()=>$('#decIn').focus(),0);}
async function decLoadFile(){
  decInvalidatePending();
  try{
    const got=await pickTextFile();
    if(!got) return;
    // The native picker yields to the event loop; invalidate again in case a
    // decoder request was started while it was open.
    decInvalidatePending();
    $('#decIn').value=normalizeListText(got.text);
    $('#decOut').value='';$('#decErr').textContent='';
    toast('loaded from '+got.name);
  }catch(e){toast(e.message);}
}
if($('#decLoad'))$('#decLoad').onclick=decLoadFile;
if($('#decClose'))$('#decClose').onclick=()=>{decModalEpoch++;decInvalidatePending();closeModal($('#decModal'));};
if($('#decIn'))$('#decIn').addEventListener('input',()=>decInvalidatePending());
if($('#decUp'))$('#decUp').onclick=()=>{decInvalidatePending();$('#decIn').value=$('#decOut').value;$('#decOut').value='';$('#decIn').focus();};
if($('#decCopy'))$('#decCopy').onclick=()=>copyText($('#decOut').value,'output copied');

/* ---- scanner ---- */
export const scanState={sel:null,issues:[]};
let scanRunEpoch=0,scanRunPending=false,scanClearEpoch=0,scanClearPending=false,scanResultsRefreshPending=false;
let scanTargetLoadEpoch=0;
let scannerPrefillEpoch=0;
// Results are shared by the initial load and an explicit rescan. A later
// request owns the result surface; older responses must not roll it back.
let scanResultsEpoch=0;
let promoteFindingPending=false;
function setScanRunState(stateName,label){
  const button=$('#scanRun');if(!button)return;
  button.classList.remove('is-pending','is-success','is-error');
  if(stateName!=='idle')button.classList.add('is-'+stateName);
  button.dataset.state=stateName;
  button.setAttribute('aria-busy',stateName==='pending'?'true':'false');
  button.disabled=stateName==='pending'||scanRunPending||scanClearPending;
  button.textContent=label;
}
function syncScanActionControls(){
  const run=$('#scanRun'),clear=$('#scanClear');
  if(run)run.disabled=scanRunPending||scanClearPending;
  if(clear){clear.disabled=scanRunPending||scanClearPending;clear.setAttribute('aria-busy',scanClearPending?'true':'false');}
}
function resetScanRun(delay,epoch){setTimeout(()=>{if(epoch===scanRunEpoch)setScanRunState('idle','Run scan ▸');},delay);}
export async function loadIssues(){
  if(scanRunPending||scanClearPending){scanResultsRefreshPending=true;return;}
  const resultsEpoch=++scanResultsEpoch;
  const stateEl=$('#scanRescanState');if(stateEl)stateEl.textContent='Loading scanner results…';
  try{const d=await api('/api/scanner/issues');
    if(resultsEpoch!==scanResultsEpoch)return;
    scanState.issues=d.issues||[];renderScan();if(stateEl)stateEl.textContent='';}
  catch(e){if(resultsEpoch===scanResultsEpoch)renderLoadError(stateEl,'Scanner results',e,loadIssues,scanState.issues.length>0);}
  finally{if(resultsEpoch===scanResultsEpoch&&stateEl&&stateEl.textContent==='Loading scanner results…')stateEl.textContent='';}
}
export async function runScan(){
  if(scanRunPending||scanClearPending){toast(scanClearPending?'wait for scanner results to finish clearing':'scan already running');return;}
  const epoch=++scanRunEpoch;
  const resultsEpoch=++scanResultsEpoch;
  scanRunPending=true;
  setScanRunState('pending','Scanning…');
  syncScanActionControls();
  const host=($('#scanTarget')||{}).value||'',search=(($('#scanFilter')||{}).value||'').trim();
  const q=new URLSearchParams();if(host)q.set('host',host);if(search)q.set('search',search);
  const stateEl=$('#scanRescanState');if(stateEl)stateEl.textContent='Rescanning selected in-scope traffic…';
  try{const d=await api('/api/scanner/run'+(q.toString()?'?'+q:''),{method:'POST'});
    if(resultsEpoch!==scanResultsEpoch){
      // A newer results load owns the issue list, but this run still owns its
      // button lifecycle unless another run started after it.
      if(epoch===scanRunEpoch){setScanRunState('success','Scan complete');resetScanRun(700,epoch);}
      return;
    }
    scanState.issues=d.issues||[];renderScan();
    await animateOnce($('#scanPassiveView'),[{opacity:.6},{opacity:1}],{duration:MOTION.base,easing:MOTION.enter});
    if(stateEl)stateEl.textContent='Rescan complete · stale issues reconciled for this scan';
    setScanRunState('success','Scan complete');resetScanRun(700,epoch);
    toast(scanState.issues.length+' issue'+(scanState.issues.length===1?'':'s')+(host?' · '+host:'')+(search?' · "'+search+'"':''));}
  catch(e){
    if(resultsEpoch!==scanResultsEpoch){
      if(epoch===scanRunEpoch){setScanRunState('error','Scan failed');resetScanRun(1000,epoch);}
      return;
    }
    setScanRunState('error','Scan failed');resetScanRun(1000,epoch);renderLoadError(stateEl,'Scanner',e,runScan,scanState.issues.length>0);
  }
  finally{
    scanRunPending=false;syncScanActionControls();
    if(scanResultsRefreshPending){scanResultsRefreshPending=false;loadIssues();}
  }
}
// Populate the scanner's target dropdown from in-scope history only.
export async function loadScanTargets(){
  const sel=$('#scanTarget');if(!sel)return;
  const epoch=++scanTargetLoadEpoch;
  try{const d=await api('/api/scanner/targets');
    if(epoch!==scanTargetLoadEpoch)return;
    if(d.truncated)throw new Error('server returned a truncated host list — retry before choosing a target');
    const hosts=(d.hosts||[]).filter(h=>h&&h.host);
    const cur=sel.value;
    sel.innerHTML='<option value="">All in-scope hosts</option>'+hosts.map(h=>`<option value="${escAttr(h.host)}">${esc(h.host)} (${Number(h.count)||0})</option>`).join('');
    if(hosts.some(h=>h.host===cur))sel.value=cur;
  }catch(e){if(epoch===scanTargetLoadEpoch)renderLoadError($('#scanRescanState'),'Scanner targets',e,loadScanTargets,false);}
}
export function prefillScanner(host, pathSearch){
  const prefillEpoch=++scannerPrefillEpoch;
  document.querySelector('.tab[data-tab="scanner"]')?.click();
  loadScanTargets().then(()=>{
    if(prefillEpoch!==scannerPrefillEpoch)return;
    const sel=$('#scanTarget');
    if(sel&&host) sel.value=host;
    const f=$('#scanFilter');
    if(f) f.value=pathSearch||'';
  });
  toast('Scanner ready'+(host?' · '+host:''));
}
$('#scanTarget')?.addEventListener('change',()=>{scannerPrefillEpoch++;scanTargetLoadEpoch++;});
$('#scanFilter')?.addEventListener('input',()=>{scannerPrefillEpoch++;});
// Group findings by title: one list row per finding type, the affected targets
// nested in its detail — instead of a separate row per (finding × target).
export const SEV_ORDER=['High','Medium','Low','Info'];
export const sevRank=s=>{const i=SEV_ORDER.indexOf(s);return i<0?SEV_ORDER.length:i;};
export function scanGroups(){
  const map=new Map();
  scanState.issues.forEach(i=>{
    let g=map.get(i.title);
    if(!g){g={title:i.title,severity:i.severity,items:[]};map.set(i.title,g);}
    g.items.push(i);
    if(sevRank(i.severity)<sevRank(g.severity))g.severity=i.severity; // keep the most severe
  });
  return [...map.values()].sort((a,b)=>sevRank(a.severity)-sevRank(b.severity)||a.title.localeCompare(b.title));
}
export function renderScan(){
  const list=$('#scanList');
  const previousSelectedTitle=(scanState.groups||[])[scanState.sel]?.title||'';
  const focusedIssue=!!document.activeElement?.closest?.('#scanList .scan-item');
  const focusedIndex=Number(document.activeElement?.closest?.('#scanList .scan-item')?.dataset.i);
  const focusedTitle=Number.isInteger(focusedIndex)?(scanState.groups||[])[focusedIndex]?.title||'':'';
  if(!scanState.issues.length){scanState.groups=[];scanState.sel=null;$('#scanCount').textContent='';list.innerHTML='<div class="state-empty"><div class="state-empty-icon"><svg class="icon" aria-hidden="true" focusable="false"><use href="#i-shield"/></svg></div><div class="state-empty-title">No issues yet</div><p class="state-empty-hint">Capture some traffic, then Run scan.</p></div>';$('#scanDetail').innerHTML='<div class="state-empty"><div class="state-empty-icon"><svg class="icon" aria-hidden="true" focusable="false"><use href="#i-clipboard"/></svg></div><div class="state-empty-title">No issue selected</div><p class="state-empty-hint">Select an issue from the list to view its details.</p></div>';if(focusedIssue)requestAnimationFrame(()=>$('#scanRun')?.focus({preventScroll:true}));return;}
  const groups=scanState.groups=scanGroups();
  const c={};scanState.issues.forEach(i=>c[i.severity]=(c[i.severity]||0)+1);
  $('#scanCount').textContent=`${groups.length} finding${groups.length===1?'':'s'} · ${scanState.issues.length} target${scanState.issues.length===1?'':'s'} · ${c.High||0}H ${c.Medium||0}M ${c.Low||0}L`;
  const preservedSelection=previousSelectedTitle?groups.findIndex(group=>group.title===previousSelectedTitle):-1;
  if(preservedSelection>=0)scanState.sel=preservedSelection;
  else if(scanState.sel==null||scanState.sel>=groups.length)scanState.sel=0;
  list.innerHTML=groups.map((g,idx)=>`<div class="scan-item ${idx===scanState.sel?'sel':''}" id="scan-issue-${idx}" data-i="${idx}" role="option" tabindex="${idx===scanState.sel?'0':'-1'}" aria-selected="${idx===scanState.sel?'true':'false'}">
    <span class="sev ${escAttr(g.severity)}">${esc(g.severity)}</span>
    <div class="t">${esc(g.title)}</div><div class="tg">${g.items.length} target${g.items.length===1?'':'s'}</div></div>`).join('');
  list.querySelectorAll('.scan-item').forEach(el=>{
    const choose=()=>{
      scanState.sel=Number(el.dataset.i);renderScan();
      requestAnimationFrame(()=>list.querySelector('#scan-issue-'+scanState.sel)?.focus());
    };
    el.onclick=choose;wireRowKey(el);
    el.addEventListener('keydown',e=>{
      if(!['ArrowDown','ArrowUp','Home','End'].includes(e.key))return;
      e.preventDefault();
      const items=[...list.querySelectorAll('.scan-item')];
      let next=Number(el.dataset.i);
      if(e.key==='ArrowDown')next=(next+1)%items.length;
      else if(e.key==='ArrowUp')next=(next-1+items.length)%items.length;
      else if(e.key==='Home')next=0;
      else next=items.length-1;
      scanState.sel=next;
      renderScan();
      requestAnimationFrame(()=>list.querySelector('#scan-issue-'+next)?.focus());
    });
  });
  renderScanDetail();
  if(focusedTitle){
    const nextFocus=groups.findIndex(group=>group.title===focusedTitle);
    if(nextFocus>=0)list.querySelector('#scan-issue-'+nextFocus)?.focus({preventScroll:true});
  }
}
export function renderScanDetail(){
  const g=(scanState.groups||[])[scanState.sel];if(!g)return;
  const first=g.items[0];
  const shared=g.items.every(i=>i.detail===first.detail); // show a common description once
  const tgts=g.items.map(i=>`<div class="scan-tgt"${i.flowId?` data-flow="${i.flowId}"`:''} style="${i.flowId?'cursor:pointer;':''}padding:7px 9px;border:1px solid var(--line);border-radius:6px;margin-bottom:6px">
    <div style="font-family:var(--mono);font-size:var(--fs-sm);color:var(--accent);word-break:break-all">${esc(i.target||'(no target)')}${i.flowId?` <span style="color:var(--fg3)">· flow #${i.flowId}</span>`:''}</div>
    ${(!shared&&i.detail)?`<div style="font-size:var(--fs-sm);color:var(--fg2);margin-top:5px;line-height:1.5">${esc(i.detail)}</div>`:''}
    ${i.evidence?`<div class="evidence" style="margin-top:6px">${esc(i.evidence)}</div>`:''}</div>`).join('');
  $('#scanDetail').innerHTML=`<div class="scan-wrap">
    <span class="sev ${escAttr(g.severity)}">${esc(g.severity)}</span>
    <div class="row" style="align-items:center;gap:10px;margin:12px 0 6px;flex-wrap:wrap">
      <h1 style="font-size:var(--fs-2xl);font-weight:700;line-height:1.3;flex:1;margin:0;min-width:0">${esc(g.title)}</h1>
      <button class="btn accent" id="scanPromote" title="Create a curated finding from this issue — title, detail, fix, and every PoC flow attached"><svg class="icon" aria-hidden="true" focusable="false"><use href="#i-plus"/></svg> Promote to Finding</button>
    </div>
    ${(shared&&first.detail)?`<p style="font-size:var(--fs-md);color:var(--fg2);line-height:1.6">${esc(first.detail)}</p>`:''}
    <div class="micro-label" style="margin:14px 0 6px">AFFECTED TARGETS (${g.items.length})</div>
    ${tgts}
    ${first.fix?`<div class="micro-label" style="margin:14px 0 6px">REMEDIATION</div><div class="fixbox">${esc(first.fix)}</div>`:''}</div>`;
  $('#scanDetail').querySelectorAll('.scan-tgt[data-flow]').forEach(el=>{el.onclick=()=>flowPopup(Number(el.dataset.flow));wireRowKey(el,()=>flowPopup(Number(el.dataset.flow)));});
  const pm=$('#scanPromote'); if(pm){pm.onclick=()=>promoteFinding(g);if(promoteFindingPending)setPromoteFindingState(pm,'pending');}
}
// promoteFinding turns a passive-scan issue group into a curated Finding (with all
// its PoC flows attached), then opens it — bridging the two views of "vulns" that
// were previously disconnected silos.
async function promoteFinding(g){
  if(promoteFindingPending)return;
  const first=g.items[0]||{};
  const flowIds=g.items.map(i=>i.flowId).filter(Boolean);
  const button=$('#scanPromote');
  promoteFindingPending=true;
  setPromoteFindingState(button,'pending');
  try{
    const f=await api('/api/findings',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({
      title:g.title,severity:g.severity,source:'scanner',
      detail:first.detail||'',evidence:first.evidence||'',fix:first.fix||'',
      flowIds,
    })});
    const warnings=Array.isArray(f.warnings)?f.warnings:[];
    const success='Promoted to Finding #'+f.id+(flowIds.length?' · '+flowIds.length+' PoC flow'+(flowIds.length===1?'':'s'):'');
    toast(warnings.length?success+' · '+warnings.length+' PoC attachment warning'+(warnings.length===1?'':'s')+': '+warnings.join(' · '):success,warnings.length?'warn':'success');
    openFinding(f.id);
  }catch(e){toast(e.message,'error');}
  finally{
    promoteFindingPending=false;
    if(button&&document.body.contains(button))setPromoteFindingState(button,'idle');
  }
}
function setPromoteFindingState(button,stateName){
  if(!button)return;
  button.dataset.state=stateName;
  button.setAttribute('aria-busy',stateName==='pending'?'true':'false');
  button.disabled=stateName==='pending';
  if(stateName==='pending')button.textContent='Promoting…';
  else button.innerHTML=icon('plus')+' Promote to Finding';
}
$('#scanRun').onclick=runScan;
$('#scanClear')&&($('#scanClear').onclick=async()=>{
  if(scanRunPending||scanClearPending){toast(scanRunPending?'wait for the active scan to finish':'scanner results are already clearing');return;}
  const confirmed=await uiConfirm(
    'Clear passive scanner results?',
    'Remove all passive scanner issues? Curated <b>Findings</b> are kept.',
    'Clear results','btn btn-danger'
  );
  if(!confirmed)return;
  if(scanRunPending||scanClearPending){toast(scanRunPending?'wait for the active scan to finish':'scanner results are already clearing');return;}
  const clearEpoch=++scanClearEpoch;
  const clearRunEpoch=++scanRunEpoch;
  const clearResultsEpoch=++scanResultsEpoch;
  scanClearPending=true;
  const button=$('#scanClear');
  if(button){button.disabled=true;button.setAttribute('aria-busy','true');button.textContent='Clearing…';}
  setScanRunState('idle','Run scan ▸');
  syncScanActionControls();
  try{
    await api('/api/scanner/issues',{method:'DELETE'});
    if(clearEpoch!==scanClearEpoch||clearRunEpoch!==scanRunEpoch||clearResultsEpoch!==scanResultsEpoch)return;
    scanState.issues=[];scanState.sel=null;renderScan();
    $('#scanRescanState').textContent='Scanner results cleared · curated Findings were not changed';
  }catch(e){
    if(clearEpoch===scanClearEpoch&&clearResultsEpoch===scanResultsEpoch)renderLoadError($('#scanRescanState'),'Clear scanner results',e,()=>$('#scanClear').click(),scanState.issues.length>0);
  }finally{
    if(clearEpoch===scanClearEpoch){scanClearPending=false;if(button)button.textContent='Clear';syncScanActionControls();if(scanResultsRefreshPending){scanResultsRefreshPending=false;loadIssues();}}
  }
});
