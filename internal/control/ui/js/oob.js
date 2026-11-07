// oob.js — the out-of-band interaction catcher: the modal, its payload URL, and
// the list of callbacks. Kept separate from the panels that use it because an
// SSRf or blind-injection test opens it from History, Findings and Settings.
import { $, esc, escAttr, state, toast, api, openModal, closeModal, copyText, fmtTime, uiConfirm } from './core.js';

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
  if(!state.oobEnabled){toast('OOB is disabled — enable in Settings → Out-of-band');return;}
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

// Settings → Out-of-band re-reads the list when the section opens.
export function initOob(){ void loadOob(); }
