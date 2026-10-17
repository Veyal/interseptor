import { $, requireFindingsEditing, findingsEditable, esc, escAttr, state, toast, toastError, api, projectStorageKey, openModal, closeModal, copyText, fmtTime, renderMD, pickTextFile, normalizeListText, DEC_OPS, saveFile, uiConfirm, renderLoadError, icon, openCtxMenu, openFlow } from './core.js';
import { openFinding } from './findings.js';
import { animateOnce, MOTION } from './motion.js';
import { createSplitPane } from './split.js';
import { renderState } from './statepanel.js';
import { getShellApi } from './shell-hooks.js';
import { createWindowedList } from './windowed-list.js';
import { stepIndex } from './list-rows-model.js';
import { SEV_ORDER as MODEL_SEV_ORDER, sevRank as modelSevRank, severityMeta, groupIssues, severityCounts, filterGroups, toggleSeverity, summaryText, scopeLine, runGate, promoteBody, verifyTarget } from './scanner-model.js';

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
    if(ownsBase&&document.activeElement!==base)base.value=d.configured===false?'':(d.baseUrl||'');
    renderOobBaseHint(d);
    renderOobList(d.interactions||[],d);
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
// With no saved base URL the payloads fall back to this UI's own address, which
// a remote target usually cannot reach: say so instead of showing that fallback
// as if it were the configured value.
function renderOobBaseHint(d){
  const hint=$('#oobBaseHint');if(!hint)return;
  if(d.configured===false){
    hint.textContent='Not set: payloads use this UI ('+(d.baseUrl||'')+'), reachable only from this machine or network.';
    hint.dataset.state='warn';
  }else{
    hint.textContent='Payload URLs: '+(d.baseUrl||'')+'/<token>';
    delete hint.dataset.state;
  }
}
function oobCountText(n,meta){
  if(!n)return '';
  let t=n+' interaction'+(n===1?'':'s');
  if(meta&&meta.limit&&n>=meta.limit)t+=' (limit)';
  if(meta&&meta.dropped>0)t+=' · '+meta.dropped+' older dropped';
  return t;
}
function oobDetail(label,value,pre=false){
  if(!value)return '';
  return `<div><b>${label}</b>${pre?`<pre>${esc(value)}</pre>`:`<span>${esc(value)}</span>`}</div>`;
}
// Each hit is a native <details> row: the summary keeps the compact grid, the
// body shows everything the catcher recorded (body preview included), so
// exfiltrated data is readable without hovering.
function renderOobList(list,meta={}){
  const c=$('#oobCount');if(c)c.textContent=oobCountText(list.length,meta);
  const box=$('#oobList');if(!box)return;
  if(!list.length){box.innerHTML='<div class="state-empty"><div class="state-empty-icon"><svg class="icon" aria-hidden="true" focusable="false"><use href="#i-oob"/></svg></div><div class="state-empty-title">No interactions yet</div><p class="state-empty-hint">Callbacks to a generated URL appear here live.</p></div>';return;}
  const open=new Set([...box.querySelectorAll('details.oob-hit[open]')].map(d=>d.dataset.id));
  box.innerHTML=list.map(it=>`<details class="oob-hit" data-id="${escAttr(String(it.id))}"${open.has(String(it.id))?' open':''}><summary class="oob-row">
    <span class="oob-m">${esc(it.method)}</span>
    <span class="oob-p">${esc(it.path)}${it.query?'<span class="oob-q">?'+esc(it.query)+'</span>':''}</span>
    <span class="oob-src">${esc(it.remoteAddr||'')}</span>
    <span class="oob-t">${fmtTime(it.ts)}</span></summary>
    <div class="oob-detail">${oobDetail('Time',new Date(it.ts).toLocaleString())}${oobDetail('Token',it.token)}${oobDetail('Host',it.host)}${oobDetail('Path',it.path)}${oobDetail('Query',it.query)}${oobDetail('Source',it.remoteAddr)}${oobDetail('User-Agent',it.userAgent)}${oobDetail('Body',it.bodyPreview,true)}</div></details>`).join('');
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
  finally{if(epoch===oobGenerateEpoch){button.disabled=false;button.setAttribute('aria-busy','false');button.textContent='Generate payload URL';}}
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
let checkMode='code',checkDocsLoaded=false,checkDocsLoadEpoch=0;
let checkSelId=null;
let checkBuiltin=false,checkOverridden=false;
const checkEndpoint='/api/checks';
let checkLoadEpoch=0;
let checkActionEpoch=0;
let checkActionBusy=false;
let checkActionFocus=null;
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
  if(busy&&!checkActionBusy){
    const active=document.activeElement;
    checkActionFocus=$('#checksModal')?.contains(active)?active:null;
  }
  const restore=!busy&&checkActionBusy?checkActionFocus:null;
  if(!busy)checkActionFocus=null;
  checkActionBusy=busy;
  syncCheckEditorControls();
  const out=$('#checkOut');if(out)out.setAttribute('aria-busy',busy?'true':'false');
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
  if(busy&&checkActionFocus&&out)out.focus({preventScroll:true});
  else if(restore&&$('#checksModal')?.style.display==='flex'&&restore.isConnected&&!restore.disabled)restore.focus({preventScroll:true});
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
  // `checkDocsLoaded` only settles after the await, so Retry (or reopening the
  // Checks modal) can start a second read. Latest-request-wins, like the twin
  // loadCodecDocs() in codecs.js.
  const epoch=++checkDocsLoadEpoch;
  try{
    const d=await api('/api/checks/reference');
    if(epoch!==checkDocsLoadEpoch)return;
    box.innerHTML=renderMD(d.markdown||'');
    checkDocsLoaded=true;
  }catch(e){
    if(epoch!==checkDocsLoadEpoch)return;
    box.innerHTML='<div class="state-error"><div class="state-error-icon"><svg class="icon" aria-hidden="true" focusable="false"><use href="#i-alert"/></svg></div><p class="state-error-msg" role="alert">'+esc(e.message)+'</p><button type="button" class="btn" data-check-docs-retry>Retry</button></div>';
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
        <span class="checks-title" style="color:${titleColor}" title="${escAttr(opts.title)}">${esc(opts.title)}${opts.error?' <svg class="icon" aria-hidden="true" focusable="false"><use href="#i-alert"/></svg>':''}</span>
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
    box.innerHTML=`<div class="state-error"><div class="state-error-icon"><svg class="icon" aria-hidden="true" focusable="false"><use href="#i-alert"/></svg></div><p class="state-error-msg" role="alert">Couldn't load checks: ${esc(e.message)}</p><button type="button" class="btn" data-checks-list-retry>Retry</button></div>`;
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
  checkBuiltin=true;checkSelId=id;
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
  checkBuiltin=false;checkOverridden=false;checkSelId=id;
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
  checkBuiltin=false;checkOverridden=false;checkSelId=null;
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
      setCheckOutcome(out,`<div class="check-status check-status-ok"><div class="hint">${esc(note)}</div><div style="color:var(--accent);margin-top:4px"><svg class="icon" aria-hidden="true" focusable="false"><use href="#i-check"/></svg> No finding — check compiles &amp; runs.</div></div>`);
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
    setCheckOutcome(out,'<div class="check-status check-status-ok">Saved <svg class="icon" aria-hidden="true" focusable="false"><use href="#i-check"/></svg> — runs on the next passive scan'+(checkBuiltin?' (replaces built-in)':'')+'.</div>');
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
          <button type="button" class="btn ${on?'':'btn-primary'}" data-pack="${escAttr(p.name)}" ${on?'disabled':''} aria-label="${on?'Pack ':'Install pack '}${escAttr(p.name)}${on?' is installed':''}">${on?'Installed':'Install'}</button></div>`;
      }).join('');
    }
    if(installed.length){
      html+='<div class="hint" style="margin:8px 0 4px;font-weight:700">Installed</div>';
      html+=installed.map(p=>{
        const sig=p.signed==='builtin'?'builtin':(p.signed?('signed '+p.signed):'unsigned');const sigOk=p.signed?' <svg class="icon" aria-hidden="true" focusable="false"><use href="#i-check"/></svg>':'';
        return `<div class="checks-pack-row"><div><b>${esc(p.name)}</b> <span class="hint">v${esc(p.version||'')} · ${esc(sig)}${sigOk}</span></div>
        <button type="button" class="btn" data-remove="${escAttr(p.name)}" title="Uninstall pack" aria-label="Uninstall pack ${escAttr(p.name)}">Remove</button></div>`;
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
  button.disabled=stateName==='pending'||scanRunPending||scanClearPending||scanRunGate().disabled;
  button.textContent=label;
}
function syncScanActionControls(){
  const run=$('#scanRun'),clear=$('#scanClear');
  if(run)run.disabled=scanRunPending||scanClearPending||scanRunGate().disabled;
  if(clear){clear.disabled=scanRunPending||scanClearPending;clear.setAttribute('aria-busy',scanClearPending?'true':'false');}
  syncScanProgress();
}
// The run is one synchronous POST, so progress is indeterminate (no value) and
// there is no Stop: aborting the fetch would not stop the server-side scan.
function syncScanProgress(){
  const row=$('#scanProgress');if(!row)return;
  row.hidden=!scanRunPending;
  const text=$('#scanProgressText');
  if(text)text.textContent=scanRunPending?'Scanning '+((($('#scanTarget')||{}).value)||'all in-scope hosts')+' …':'';
}
function scanRunGate(){return runGate({hostCount:scanUI.hostCount,hostsLoaded:scanUI.hostsLoaded});}
// Scope line + the reason Run is unavailable (shown, and tied to the button via
// aria-describedby). An empty scope is NOT a reason: the server treats it as
// "everything is in scope".
function renderScanContext(){
  const text=$('#scanScopeText');
  if(text)text.textContent=scopeLine({inCount:scanUI.scope?scanUI.scope.inCount:0,hostCount:scanUI.hostCount});
  const reason=$('#scanRunReason'),gate=scanRunGate();
  if(reason){reason.hidden=!gate.disabled;reason.textContent=gate.reason;}
  syncScanActionControls();
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
    scanUI.hostCount=hosts.length;scanUI.hostsLoaded=true;renderScanContext();
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
export const SEV_ORDER=MODEL_SEV_ORDER;
export const sevRank=modelSevRank;
export function scanGroups(){return groupIssues(scanState.issues);}
// Scanner view state that is not part of the issue data: the severity filter,
// the split pane and the scope/target context behind the Run gate.
const scanUI={sevActive:new Set(),pane:null,hostCount:0,hostsLoaded:false,scope:null};
function setScanDetailHTML(html){
  const host=$('#scanDetail');if(!host)return;
  // The SplitPane's Back button lives inside the detail; keep it across renders.
  const back=host.querySelector(':scope > .split-back');
  host.innerHTML=html;
  if(back)host.insertBefore(back,host.firstChild);
}
const SCAN_NO_SELECTION='<div class="state-empty"><div class="state-empty-icon"><svg class="icon" aria-hidden="true" focusable="false"><use href="#i-clipboard"/></svg></div><div class="state-empty-title">No issue selected</div><p class="state-empty-hint">Select an issue from the list to view its details.</p></div>';
function sevChip(sev){const m=severityMeta(sev);return `<span class="sev ${escAttr(sev)}">${icon(m.icon)} ${esc(m.label)}</span>`;}
// Severity count chips double as filters. aria-pressed carries the state; the
// count and the severity name are text, so colour is never the only signal.
function renderScanChips(groups){
  const box=$('#scanSevChips');if(!box)return;
  const counts=severityCounts(groups);
  box.hidden=!counts.length;
  box.innerHTML=counts.map(({severity,count})=>{
    const m=severityMeta(severity),on=scanUI.sevActive.has(severity);
    return `<button type="button" class="scan-sev-chip" data-sev="${escAttr(severity)}" aria-pressed="${on?'true':'false'}">${icon(m.icon)} ${esc(m.label)} <b>${count}</b></button>`;
  }).join('');
}
/* ---- The issue list is windowed (createListView via windowed-list.js): a project with a great many
   issue types keeps only the rows near the viewport in the DOM, and click / right-click / keys are
   three delegated listeners on #scanList instead of three closures per row. Every row has one
   fixed height (panel-scan.css), which is what lets the window be computed. Below SCAN_VIRT_MIN
   rows everything is mounted exactly as before. ---- */
const SCAN_VIRT_MIN=150;
let scanListWin=null;
function scanItemHTML(g,idx){
  const on=idx===scanState.sel;
  return `<div class="scan-item ${on?'sel':''}" id="scan-issue-${idx}" data-i="${idx}" data-key="${escAttr(g.title)}" role="option" tabindex="${on?'0':'-1'}" aria-selected="${on?'true':'false'}" aria-posinset="${idx+1}" aria-setsize="${(scanState.groups||[]).length}">
    ${sevChip(g.severity)}
    <div class="t" title="${escAttr(g.title)}">${esc(g.title)}</div><div class="tg">${g.items.length} target${g.items.length===1?'':'s'}</div></div>`;
}
function ensureScanListWin(list){
  if(scanListWin&&scanListWin.container===list)return scanListWin;
  if(scanListWin)scanListWin.destroy();
  scanListWin=createWindowedList({
    container:list,rowHeight:74,threshold:SCAN_VIRT_MIN,buffer:8,
    rowSelector:'.scan-item',getItems:()=>scanState.groups||[],renderRow:scanItemHTML,
    // A row is identified by its issue title, not its position: a scan that re-sorts the list while a row
    // has focus must hand focus back to that issue, not to whichever issue now sits at its old index.
    keyOf:row=>row.dataset.key,findRow:key=>[...list.querySelectorAll('.scan-item')].find(r=>r.dataset.key===key)||null,
    indexOf:row=>Number(row.dataset.i),
    onContainerKey:e=>moveScanSelection(e,scanState.sel),
    on:{
      click:row=>chooseScanIssue(Number(row.dataset.i)),
      contextmenu:(row,e)=>{
        e.preventDefault();
        const i=Number(row.dataset.i);
        chooseScanIssue(i);
        showScanIssueCtx(e,(scanState.groups||[])[i]);
      },
      keydown:(row,e)=>{
        if(e.key==='Enter'||e.key===' '){e.preventDefault();chooseScanIssue(Number(row.dataset.i));return;}
        moveScanSelection(e,Number(row.dataset.i));
      },
    },
  });
  return scanListWin;
}
function chooseScanIssue(i){
  const list=$('#scanList');
  scanState.sel=i;renderScan();
  const row=list.querySelector('#scan-issue-'+scanState.sel);
  requestAnimationFrame(()=>row?.focus());
  // Phones show one pane at a time: opening an issue pushes its detail.
  if(scanUI.pane&&scanUI.pane.mode()==='stack')scanUI.pane.showDetail(row);
}
// Arrows wrap, Home/End and Page keys clamp. The next row may be outside the mounted window, so the
// list is scrolled to it before it is rendered and focused.
function moveScanSelection(e,from){
  const count=(scanState.groups||[]).length;
  const rowH=scanListWin?scanListWin.rowHeight():74;
  const page=Math.max(1,Math.floor(($('#scanList')?.clientHeight||0)/rowH)-1);
  const next=stepIndex({current:from,count,key:e.key,page,wrap:true});
  if(next<0)return;
  e.preventDefault();
  const nextTitle=(scanState.groups||[])[next]?.title||'';
  scanState.sel=next;
  if(scanListWin)scanListWin.reveal(next);
  // renderScan follows the issue that had focus; a move names the issue that should have it instead, or a
  // long jump (End, Home, a wrap-around) would be carried back to the row it started from.
  renderScan({focusTitle:nextTitle});
  // Focus the new row now as well as next frame: a key repeating faster than a frame would otherwise
  // land on the row the render just rebuilt and step from the wrong place.
  $('#scanList')?.querySelector('#scan-issue-'+next)?.focus({preventScroll:true});
  requestAnimationFrame(()=>$('#scanList')?.querySelector('#scan-issue-'+next)?.focus({preventScroll:true}));
}
export function renderScan(opts){
  const list=$('#scanList');
  const previousSelectedTitle=(scanState.groups||[])[scanState.sel]?.title||'';
  const focusedIssue=!!document.activeElement?.closest?.('#scanList .scan-item');
  const focusedIndex=Number(document.activeElement?.closest?.('#scanList .scan-item')?.dataset.i);
  const focusedTitle=opts&&typeof opts.focusTitle==='string'?opts.focusTitle:Number.isInteger(focusedIndex)?(scanState.groups||[])[focusedIndex]?.title||'':'';
  list.removeAttribute('data-state');
  if(!scanState.issues.length){scanState.groups=[];scanState.sel=null;renderScanChips([]);$('#scanCount').textContent='';if(scanListWin)scanListWin.suspend();list.innerHTML='<div class="state-empty"><div class="state-empty-icon"><svg class="icon" aria-hidden="true" focusable="false"><use href="#i-scanner"/></svg></div><div class="state-empty-title">No issues yet</div><p class="state-empty-hint">Capture some traffic, then Run scan.</p></div>';setScanDetailHTML(SCAN_NO_SELECTION);if(focusedIssue)requestAnimationFrame(()=>$('#scanRun')?.focus({preventScroll:true}));return;}
  const all=groupIssues(scanState.issues);
  const groups=scanState.groups=filterGroups(all,scanUI.sevActive);
  renderScanChips(all);
  $('#scanCount').textContent=summaryText(all,scanState.issues);
  if(!groups.length){
    scanState.sel=null;
    if(scanListWin)scanListWin.suspend();
    renderState(list,'empty-filtered',{filterCount:scanUI.sevActive.size,onClear:()=>{scanUI.sevActive=new Set();renderScan();}});
    setScanDetailHTML(SCAN_NO_SELECTION);
    return;
  }
  const preservedSelection=previousSelectedTitle?groups.findIndex(group=>group.title===previousSelectedTitle):-1;
  if(preservedSelection>=0)scanState.sel=preservedSelection;
  else if(scanState.sel==null||scanState.sel>=groups.length)scanState.sel=0;
  ensureScanListWin(list).render();
  renderScanDetail();
  if(focusedTitle){
    const nextFocus=groups.findIndex(group=>group.title===focusedTitle);
    if(nextFocus>=0){
      // The issue may have moved out of the mounted window (a re-sort): bring it back before focusing it.
      if(!list.querySelector('#scan-issue-'+nextFocus)&&scanListWin&&scanListWin.reveal(nextFocus,{align:'center'}))scanListWin.refresh();
      list.querySelector('#scan-issue-'+nextFocus)?.focus({preventScroll:true});
    }
  }
}
function scanTargetRow(i,n,shared){
  const actions=i.flowId?`<div class="scan-tgt-actions" role="group" aria-label="Actions for ${escAttr(i.target||'this target')}">
      <button type="button" class="btn xs" data-act="open" data-n="${n}">${icon('link')} Open flow #${i.flowId}</button>
      <button type="button" class="btn xs" data-act="verify" data-n="${n}">${icon('repeater')} Verify in Repeater</button>
      <button type="button" class="btn xs" data-act="attach" data-n="${n}">${icon('attach')} Attach as evidence</button></div>`:'';
  return `<div class="scan-tgt"><div class="scan-tgt-url">${esc(i.target||'(no target)')}${i.flowId?` <span class="scan-tgt-flow">flow #${i.flowId}</span>`:''}</div>
    ${(!shared&&i.detail)?`<div class="scan-tgt-detail">${esc(i.detail)}</div>`:''}
    ${i.evidence?`<div class="evidence scan-tgt-evidence">${esc(i.evidence)}</div>`:''}${actions}</div>`;
}
// An issue's affected targets can run to thousands (one "Missing security response headers" target per
// URL). They are cards of varying height, which a fixed-row window cannot mount, so the open issue
// mounts the first SCAN_TARGET_PAGE and a Show more control reveals the rest a page at a time. Copy
// "Affected targets" in the issue menu and Promote to Finding still use every target.
const SCAN_TARGET_PAGE=100;
let scanDetailGroup=null;  // the issue group the detail pane is showing
let scanDetailShared=false;
let scanTargetsShown=0;    // how many of its targets are mounted
let scanTargetsTitle='';   // the issue scanTargetsShown belongs to
function scanTargetsMoreHTML(total,shown){
  if(shown>=total)return '';
  const next=Math.min(SCAN_TARGET_PAGE,total-shown);
  return `<div class="scan-tgt-more"><button type="button" class="btn" data-scan-more>Show ${next} more target${next===1?'':'s'}</button><span class="hint" role="status">Showing ${shown} of ${total} targets</span></div>`;
}
export function renderScanDetail(){
  const g=(scanState.groups||[])[scanState.sel];if(!g)return;
  const first=g.items[0];
  const shared=g.items.every(i=>i.detail===first.detail); // show a common description once
  if(scanTargetsTitle!==g.title){scanTargetsTitle=g.title;scanTargetsShown=SCAN_TARGET_PAGE;}
  scanDetailGroup=g;scanDetailShared=shared;
  const shown=Math.min(g.items.length,scanTargetsShown);
  setScanDetailHTML(`<div class="scan-wrap">
    ${sevChip(g.severity)}
    <div class="scan-head">
      <h1 class="scan-title" tabindex="-1" data-split-focus>${esc(g.title)}</h1>
      <button class="btn accent findings-write" id="scanPromote" title="Create a curated finding from this issue: title, detail, fix, and every PoC flow attached">${icon('plus')} Promote to Finding</button>
    </div>
    ${(shared&&first.detail)?`<p class="scan-detail-text">${esc(first.detail)}</p>`:''}
    <div class="micro-label scan-sec-label">AFFECTED TARGETS (${g.items.length})</div>
    ${g.items.slice(0,shown).map((i,n)=>scanTargetRow(i,n,shared)).join('')}
    ${scanTargetsMoreHTML(g.items.length,shown)}
    ${first.fix?`<div class="micro-label scan-sec-label">REMEDIATION</div><div class="fixbox">${esc(first.fix)}</div>`:''}</div>`);
  const pm=$('#scanPromote'); if(pm){pm.onclick=()=>promoteFinding(g);if(promoteFindingPending)setPromoteFindingState(pm,'pending');}
}
// Show more appends the next page of cards in place, so focus, scroll position and the cards already
// shown are untouched.
function showMoreScanTargets(){
  const g=scanDetailGroup,more=$('#scanDetail')?.querySelector('.scan-tgt-more');
  if(!g||!more)return;
  const total=g.items.length,from=Math.min(total,scanTargetsShown);
  scanTargetsShown=Math.min(total,from+SCAN_TARGET_PAGE);
  const last=more.previousElementSibling;
  more.insertAdjacentHTML('beforebegin',g.items.slice(from,scanTargetsShown).map((i,k)=>scanTargetRow(i,from+k,scanDetailShared)).join(''));
  const status=more.querySelector('.hint'),button=more.querySelector('[data-scan-more]');
  if(scanTargetsShown>=total){
    const firstNew=last?last.nextElementSibling:null;
    more.remove();
    // The control that had focus is gone; land on the first card that was just added (its first action, or
    // the card itself when it has none) so Tab carries on from where the operator was, not from the title.
    if(firstNew&&!firstNew.querySelector('[data-act]'))firstNew.tabIndex=-1;
    (firstNew?.querySelector('[data-act]')||firstNew||$('#scanDetail')?.querySelector('.scan-title'))?.focus({preventScroll:true});
    return;
  }
  const next=Math.min(SCAN_TARGET_PAGE,total-scanTargetsShown);
  if(button)button.textContent='Show '+next+' more target'+(next===1?'':'s');
  if(status)status.textContent='Showing '+scanTargetsShown+' of '+total+' targets';
}
// The per-target actions and Show more are two delegated listeners on the detail pane, so an issue
// with thousands of targets binds nothing per card.
$('#scanDetail')?.addEventListener('click',e=>{
  if(e.target.closest?.('[data-scan-more]')){showMoreScanTargets();return;}
  const b=e.target.closest?.('.scan-tgt-actions [data-act]');
  if(!b||!scanDetailGroup)return;
  scanTargetAction(b.dataset.act,scanDetailGroup.items[Number(b.dataset.n)],b);
});
// showScanIssueCtx — the scanner issue list used to swallow right-click. Every
// action here already exists as a button; the menu just makes them reachable
// from the row. Suppress / false-positive and "edit the check that fired" are
// deliberately absent: a scanner issue carries no check id and the backend has
// no suppression store, so neither could be wired honestly.
function showScanIssueCtx(e,g){
  if(!g)return;
  const first=g.items[0]||{};
  const withFlow=g.items.filter(i=>i.flowId);
  const target=withFlow[0]||null;
  const evidence=g.items.map(i=>i.evidence).filter(Boolean).join('\n');
  const host=(()=>{try{return new URL(first.target||'').hostname;}catch{return '';}})();
  openCtxMenu(e.clientX,e.clientY,[{head:'ISSUE · '+(g.severity||'').toUpperCase(),items:[
    {label:'Verify in Repeater',icon:'repeater',disabled:!target,act:()=>scanTargetAction('verify',target,null)},
    {label:'Open affected flow',icon:'link',val:target?'#'+target.flowId:'',disabled:!target,act:()=>scanTargetAction('open',target,null)},
    {label:'Attach as evidence',icon:'attach',disabled:!target,act:()=>scanTargetAction('attach',target,null)},
    {label:'Promote to Finding',icon:'plus',disabled:!findingsEditable(),act:()=>promoteFinding(g)},
    {label:'Copy',icon:'copy',children:[
      {label:'Issue title',act:()=>copyText(g.title||'','title copied')},
      {label:'Description',disabled:!first.detail,act:()=>copyText(first.detail||'','description copied')},
      {label:'Evidence',disabled:!evidence,act:()=>copyText(evidence,'evidence copied')},
      {label:'Remediation',disabled:!first.fix,act:()=>copyText(first.fix||'','remediation copied')},
      {label:'Affected targets',act:()=>copyText(g.items.map(i=>i.target||'').filter(Boolean).join('\n'),'targets copied')},
    ]},
    {label:'Filter this severity',icon:'filter',disabled:!g.severity,act:()=>{scanUI.sevActive=new Set([String(g.severity).toLowerCase()]);renderScan();}},
    {label:'Scan this host again',icon:'scanner',val:host,disabled:!host,act:()=>prefillScanner(host,'')},
    {label:'Open the check editor',icon:'checks',act:openChecks},
  ]}]);
}

// Per-target actions. Every one starts from the issue's own flow id.
function scanTargetAction(act,issue,button){
  const target=verifyTarget(issue);if(!target)return;
  if(act==='open'){openFlow(target.id,{source:'scanner'});return;}
  if(act==='verify'){
    // Issues carry no payload, so Repeater is preloaded with the affected flow only.
    import('./tools.js').then(m=>m.sendToRepeater(target)).catch(e=>toastError('Repeater',e));
    return;
  }
  if(act==='attach')import('./evidence-attach.js').then(m=>m.attachEvidence({kind:'flow',refs:[target.id]},{anchor:button})).catch(e=>toastError('Attach as evidence',e));
}
// promoteFinding turns a passive-scan issue group into a curated Finding (with all
// its PoC flows attached), then opens it — bridging the two views of "vulns" that
// were previously disconnected silos.
async function promoteFinding(g){
  if(!requireFindingsEditing())return;
  if(promoteFindingPending)return;
  const body=promoteBody(g);
  const flowIds=body.flowIds;
  const button=$('#scanPromote');
  promoteFindingPending=true;
  setPromoteFindingState(button,'pending');
  try{
    const f=await api('/api/findings',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify(body)});
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

/* ---- scanner workbench wiring: severity filters, split, Tools menu, scope ---- */
$('#scanSevChips')?.addEventListener('click',e=>{
  const chip=e.target.closest?.('[data-sev]');if(!chip)return;
  scanUI.sevActive=toggleSeverity(scanUI.sevActive,chip.dataset.sev);
  renderScan();
  // The chips are re-rendered, so hand focus back to the same filter.
  $('#scanSevChips')?.querySelector(`[data-sev="${CSS.escape(chip.dataset.sev)}"]`)?.focus({preventScroll:true});
});
try{
  const root=$('#scanPassiveView');
  if(root)scanUI.pane=createSplitPane({
    root,key:'scanner.split',orientation:'right',min:[240,320],default:34,stackBelow:720,scopeKey:projectStorageKey,
    label:'Resize scanner results and detail',backLabel:'Results',
    // Back from a detail view the list is shown again, scrolled to the top (a hidden element keeps no
    // scroll offset), so bring the open issue back into the window before focusing it.
    onView:view=>{if(view==='list')requestAnimationFrame(()=>{
      if(scanListWin&&scanState.sel!=null&&scanListWin.reveal(scanState.sel,{align:'center'}))scanListWin.refresh();
      root.querySelector('.scan-item.sel')?.focus({preventScroll:true});
    });},
  });
}catch(e){/* the plain flex layout still works without the split */}
// Tools menu: a disclosure holding the modal-backed tools (Checks, Codecs,
// Decoder, OOB). Esc closes it and returns focus to the button.
(function wireScanTools(){
  const btn=$('#scanToolsBtn'),menu=$('#scanToolsMenu');if(!btn||!menu)return;
  const set=open=>{menu.hidden=!open;btn.setAttribute('aria-expanded',open?'true':'false');};
  const close=(restore)=>{if(menu.hidden)return;set(false);if(restore)btn.focus({preventScroll:true});};
  btn.addEventListener('click',()=>{set(menu.hidden);if(!menu.hidden)menu.querySelector('button:not([hidden])')?.focus();});
  // Capture phase: focus returns to the Tools button before the tool's own
  // handler opens its modal, so the modal restores focus to a live element.
  menu.addEventListener('click',()=>close(true),true);
  document.addEventListener('keydown',e=>{if(e.key==='Escape'&&!menu.hidden){e.stopPropagation();close(true);}},true);
  document.addEventListener('pointerdown',e=>{if(!menu.hidden&&!$('#scanTools').contains(e.target))close(false);});
  $('#scanDecoderBtn')?.addEventListener('click',()=>openDecoder());
})();
$('#scanScopeLink')?.addEventListener('click',()=>{const api=getShellApi();if(api&&api.openSettings)api.openSettings('scope');});
// Scope comes from the shared project state; a missing module just leaves the
// "no include rule" wording in place.
import('./project-state.js').then(m=>{
  const apply=s=>{scanUI.scope=s&&s.scope;renderScanContext();};
  m.projectState.subscribe(apply);apply(m.projectState.get());
}).catch(()=>renderScanContext());
