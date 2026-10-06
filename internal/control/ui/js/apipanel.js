import { $, esc, escAttr, api, toast, toastError, methodColor, copyText, uiConfirm, uiPrompt, fmtBytes, renderLoadError } from './core.js';

// Keep previously fetched read-only reference data useful during a transient
// refresh failure, but never let it look like a current server contract.
let restReferenceLoaded=false;
let restReferenceLoadEpoch=0,mcpLoadEpoch=0,sessionKeyRevealEpoch=0;
let allowlistLoadEpoch=0;
let allowlistLoaded=false;
let apiKeysLoadEpoch=0;
let shareLoadEpoch=0;
let mergeStatusLoadEpoch=0;
let vaultPanelLoadEpoch=0;
let vaultListLoadEpoch=0;
let vaultConfigured=false;
let allowlistMutationPending=false;
let peerMergePending=false;
let vaultActionPending=false;
function markRESTReferenceStale(stale){
  const list=$('#restList');
  if(list){
    if(stale){
      list.dataset.stale='true';
      list.setAttribute('aria-label','REST reference (stale — retry to refresh)');
    }else{
      list.removeAttribute('data-stale');
      list.removeAttribute('aria-label');
    }
  }
  const base=$('#apiBase');
  if(base){
    if(stale)base.dataset.stale='true';
    else base.removeAttribute('data-stale');
  }
}

function allowlistLoadState(){
  const list=$('#allowList');if(!list)return null;
  let loadState=$('#allowListLoadState');if(loadState)return loadState;
  loadState=document.createElement('div');
  loadState.id='allowListLoadState';
  loadState.className='tls-diag-banner';
  loadState.setAttribute('role','status');
  loadState.setAttribute('aria-live','polite');
  const table=list.closest('table'),parent=table?.parentNode;
  if(parent)parent.insertBefore(loadState,table);
  return loadState;
}

function showAllowlistMutationError(error){
  const loadState=allowlistLoadState();
  if(loadState){
    loadState.removeAttribute('data-allowlist-stale');
    loadState.style.display='block';
    loadState.innerHTML='<span class="state-error-msg" role="alert">Allowlist update failed: '+esc(error?.message||'request failed')+' — Review the values and try the action again.</span> <button type="button" class="btn xs" data-load-retry>Refresh list</button>';
    // Refresh only reconciles the list. The operator must press Add/Remove
    // again (and reconfirm removal), so recovery cannot duplicate a mutation.
    const retry=loadState.querySelector('[data-load-retry]');if(retry)retry.onclick=loadAllowlist;
  }else toastError('Allowlist update failed', error);
}

/* ---- api module ---- */
$('#apiSub').querySelectorAll('button').forEach(b=>b.onclick=()=>{
  $('#apiSub').querySelectorAll('button').forEach(x=>{x.classList.toggle('on',x===b);x.setAttribute('aria-pressed',x===b?'true':'false');});
  ['Keys','Allowlist','Share','Rest','Mcp'].forEach(s=>{const el=$('#api'+s);if(el)el.style.display=(s.toLowerCase()===b.dataset.s)?'block':'none';});
  if(b.dataset.s==='share'){loadShare();loadMergeStatus();loadVaultPanel();}
  if(b.dataset.s==='allowlist')loadAllowlist();
});
export async function loadAllowlist(){
  const epoch=++allowlistLoadEpoch;
  const loadState=allowlistLoadState();
  const hadData=allowlistLoaded;
  if(loadState){loadState.style.display='block';loadState.textContent='Loading allowlist…';}
  try{
    const d=await api('/api/allowlist');
    if(epoch!==allowlistLoadEpoch)return;
    const entries=d.entries||[];
    const cip=d.clientIP||'';
    const hint=$('#allowClientIP');
    if(hint)hint.innerHTML=cip?('This request’s client IP: <b class="u-mono">'+esc(cip)+'</b>'):'';
    const btn=$('#allowThisIP');
    if(btn){btn.disabled=!cip||allowlistMutationPending;btn.onclick=()=>{if(!cip)return;$('#allowCIDR').value=cip;createAllowEntry();};}
    $('#allowList').innerHTML=entries.length?entries.map(e=>`<tr>
      <td class="u-mono">${esc(e.cidr)}</td>
      <td>${esc(e.label||'')}</td>
      <td class="u-fg3">${e.created?esc(new Date(e.created).toLocaleString()):'—'}</td>
      <td><button class="btn danger" data-allow-del="${e.id}" aria-label="Remove allowlist entry ${escAttr(e.cidr)}">Remove</button></td></tr>`).join('')
      :'<tr><td colspan="4" class="hint cell-note">No allowlisted IPs — remote access still needs an API key.</td></tr>';
    $('#allowList').querySelectorAll('[data-allow-del]').forEach(b=>b.onclick=()=>deleteAllowEntry(Number(b.dataset.allowDel)));
    if(allowlistMutationPending)setAllowlistMutationPending(true);
    allowlistLoaded=true;
    if(loadState){loadState.style.display='none';loadState.textContent='';loadState.removeAttribute('data-allowlist-stale');}
  }catch(e){
    if(epoch!==allowlistLoadEpoch)return;
    if(loadState){
      if(hadData)loadState.setAttribute('data-allowlist-stale','true');
      else loadState.removeAttribute('data-allowlist-stale');
      renderLoadError(loadState,'Allowlist',e,loadAllowlist,hadData);
    }else toastError('Allowlist failed', e);
  }
}
function setAllowlistMutationPending(pending){
  allowlistMutationPending=pending;
  ['allowAdd','allowThisIP','allowCIDR','allowLabel'].forEach(id=>{const control=$('#'+id);if(control){control.disabled=pending||(id==='allowThisIP'&&!($('#allowClientIP')?.textContent||'').trim());control.setAttribute('aria-busy',pending&&id==='allowAdd'?'true':'false');}});
  document.querySelectorAll('#allowList [data-allow-del]').forEach(button=>{button.disabled=pending;});
}
async function createAllowEntry(){
  if(allowlistMutationPending)return;
  const cidr=$('#allowCIDR').value.trim();
  const label=$('#allowLabel').value.trim();
  if(!cidr){toast('IP or CIDR required');return;}
  setAllowlistMutationPending(true);
  try{
    allowlistLoadEpoch++;
    await api('/api/allowlist',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({cidr,label})});
    $('#allowCIDR').value='';$('#allowLabel').value='';
    toast('allowlist updated');loadAllowlist();
  }catch(e){
    await loadAllowlist();
    showAllowlistMutationError(e);
    toastError('Allowlist request failed', e);
  }
  finally{setAllowlistMutationPending(false);}
}
async function deleteAllowEntry(id){
  if(!await uiConfirm('Remove allowlist entry','Clients from this IP will need an API key again.','Remove','btn danger','var(--red)'))return;
  if(allowlistMutationPending)return;
  setAllowlistMutationPending(true);
  try{allowlistLoadEpoch++;await api('/api/allowlist/'+id,{method:'DELETE'});toast('removed');loadAllowlist();}
  catch(e){
    await loadAllowlist();
    showAllowlistMutationError(e);
    toastError('Allowlist request failed', e);
  }
  finally{setAllowlistMutationPending(false);}
}
{const ab=$('#allowAdd');if(ab)ab.onclick=createAllowEntry;}
export async function loadApiKeys(){
  const epoch=++apiKeysLoadEpoch;
  try{const d=await api('/api/keys');if(epoch!==apiKeysLoadEpoch)return;const keys=d.keys||[];
    $('#keyList').innerHTML=keys.length?keys.map(k=>`<tr>
      <td class="mono-accent">${esc(k.prefix)}…</td>
      <td>${esc(k.label)}</td>
      <td><span class="sev ${k.scope==='read'?'Info':'Low'}">${esc(k.scope||'full')}</span></td>
      <td class="u-fg3">${k.created?esc(new Date(k.created).toLocaleString()):'—'}${k.expires?'<br><span class="u-warn">exp '+esc(new Date(k.expires).toLocaleDateString())+'</span>':''}</td>
      <td><button class="btn danger" data-revoke="${k.id}" data-kp="${escAttr(k.prefix||'')}" data-kl="${escAttr(k.label||'')}" aria-label="Revoke API key ${escAttr(k.label||k.prefix||('#'+k.id))}">Revoke</button></td></tr>`).join('')
      :'<tr><td colspan="5" class="hint cell-note">No keys yet.</td></tr>';
    $('#keyList').querySelectorAll('[data-revoke]').forEach(b=>b.onclick=()=>revokeKey(Number(b.dataset.revoke),b.dataset.kp,b.dataset.kl));
  }catch(e){
    if(epoch!==apiKeysLoadEpoch)return;
    const list=$('#keyList');if(!list)return;
    list.innerHTML='<tr><td colspan="5" class="state-error-msg cell-note"><span role="alert">Keys unavailable: '+esc(e.message||'request failed')+'</span> <button type="button" class="btn xs" data-key-list-retry>Retry</button></td></tr>';
    const retry=list.querySelector('[data-key-list-retry]');if(retry)retry.onclick=loadApiKeys;
  }
}
let apiKeyCreatePending=false;
export async function createApiKey(){
  if(apiKeyCreatePending)return;
  const button=$('#keyCreate');
  const label=$('#keyLabel').value.trim()||'key';
  const scope=($('#keyScope')||{}).value||'full';
  const expiresIn=Number(($('#keyExpiry')||{}).value||0);
  apiKeyCreatePending=true;
  if(button){button.disabled=true;button.setAttribute('aria-busy','true');button.textContent='Creating…';}
  try{apiKeysLoadEpoch++;const d=await api('/api/keys',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({label,scope,expiresIn})});
    $('#keyNew').style.display='block';
    $('#keyNew').innerHTML='New '+esc(scope)+' token — copy now, it is shown only once:<br><b class="u-accent u-select-all">'+esc(d.token)+'</b>';
    $('#keyLabel').value='';loadApiKeys();
  }catch(e){toastError('Create key failed', e);}
  finally{
    apiKeyCreatePending=false;
    if(button){button.disabled=false;button.removeAttribute('aria-busy');button.textContent='Create key';}
  }
}

/** Reveal the API token for the current cookie session (remote Tailscale login). */
export async function revealSessionKey(){
  const box=$('#keySession'); if(!box)return;
  // Two clicks used to leave two reads in flight, both writing the box and
  // rebinding Copy to their own token — a late failure could even erase a
  // revealed key. Latest request wins, and the trigger stays busy meanwhile.
  const epoch=++sessionKeyRevealEpoch;
  const button=$('#keyRevealSession');
  if(button){button.disabled=true;button.setAttribute('aria-busy','true');}
  try{
    const d=await api('/api/session/access-key');
    if(epoch!==sessionKeyRevealEpoch)return;
    box.style.display='block';
    box.innerHTML='Session access key ('+esc(d.scope||'full')+(d.prefix?' · '+esc(d.prefix)+'…':'')+') — <button class="btn btn-compact" id="keySessionCopy">Copy</button><br><b class="u-accent u-select-all u-break">'+esc(d.token)+'</b>';
    const cp=$('#keySessionCopy'); if(cp)cp.onclick=()=>copyText(d.token,'Access key copied');
  }catch(e){
    if(epoch!==sessionKeyRevealEpoch)return;
    box.style.display='block';
    renderLoadError(box,'Session access key',e,revealSessionKey,false);
    box.insertAdjacentHTML('beforeend','<div class="hint u-mt-2">This only works when signed in via /login (cookie session). Loopback use has no session key.</div>');
  }
  finally{if(epoch===sessionKeyRevealEpoch&&button){button.disabled=false;button.setAttribute('aria-busy','false');}}
}
{const rb=$('#keyRevealSession'); if(rb)rb.onclick=revealSessionKey;}

/* ---- Share (Cloudflare tunnel) + peer sync ---- */
export async function loadShare(){
  const epoch=++shareLoadEpoch;
  try{const s=await api('/api/share/status');
    if(epoch!==shareLoadEpoch)return;
    lastShareStatus=s;
    let html;
    if(s.running&&s.url){
      html='<div class="row u-gap-2"><span class="sev Low">live</span> <b class="u-accent u-select-all">'+esc(s.url)+'</b> <button class="btn btn-compact" id="shareCopy">Copy</button></div>'+
           '<div class="hint u-mt-2">Send this URL + an access key to a teammate, or point a VPS AI agent at <code>'+esc(s.url)+'/mcp</code> with a full-access key.</div>';
    }else if(s.running){
      html='<span class="sev Info">starting…</span> waiting for the public URL — refresh in a moment.';
    }else if(!s.installed){
      html='<span class="sev Medium">cloudflared not installed</span><div class="hint u-mt-2">Install <code>cloudflared</code> (<a href="https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/downloads/" target="_blank" rel="noopener">download</a>) and retry — Interseptor runs a free quick tunnel, no account needed.</div>';
    }else if(!s.hasKeys){
      html='<span class="sev Medium">no access key</span><div class="hint u-mt-2">Create an access key first (Keys tab) — sharing is refused with no key so the surface is never exposed unauthenticated.</div>';
    }else{
      html='Not sharing. Click <b>Start sharing</b> to open a public tunnel.';
    }
    if(s.err)html+='<div class="hint u-danger u-mt-2">'+esc(s.err)+'</div>';
    $('#shareStatus').innerHTML=html;
    const start=$('#shareStart');
    start.style.display=s.running?'none':'inline-flex';
    start.disabled=shareActionPending||(!s.running&&(!s.installed||!s.hasKeys));
    const prereq=$('#sharePrereq');
    if(prereq){
      if(!s.hasKeys)prereq.innerHTML='Create an access key before sharing. <button class="btn xs" id="shareGoKeys">Go to Keys</button>';
      else if(!s.installed)prereq.innerHTML='Install cloudflared first. <a class="btn xs" href="https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/downloads/" target="_blank" rel="noopener">Install guidance</a>';
      else prereq.textContent='';
      const go=$('#shareGoKeys');if(go)go.onclick=()=>$('#apiSub button[data-s="keys"]')?.click();
    }
    $('#shareStop').style.display=s.running?'inline-flex':'none';
    $('#shareStop').disabled=shareActionPending;
    const cp=$('#shareCopy');if(cp)cp.onclick=()=>copyText(s.url,'Tunnel URL copied');
  }catch(e){if(epoch!==shareLoadEpoch)return;renderLoadError($('#shareStatus'),'Share status',e,loadShare,false);}
}
let shareActionPending=false;
let shareActionEpoch=0,shareRefreshTimer=null;
let lastShareStatus=null;
function setShareActionPending(pending){
  shareActionPending=pending;
  const start=$('#shareStart'),stop=$('#shareStop');
  if(start){start.disabled=pending||!!(lastShareStatus&&!lastShareStatus.running&&(!lastShareStatus.installed||!lastShareStatus.hasKeys));start.setAttribute('aria-busy',pending?'true':'false');}
  if(stop){stop.disabled=pending;stop.setAttribute('aria-busy',pending?'true':'false');}
}
async function startShare(){
  if(shareActionPending)return;
  clearTimeout(shareRefreshTimer);shareRefreshTimer=null;
  const epoch=++shareActionEpoch;
  setShareActionPending(true);shareLoadEpoch++;
  try{
    await api('/api/share/start',{method:'POST'});
    if(epoch!==shareActionEpoch)return;
    toast('tunnel starting…');
    shareRefreshTimer=setTimeout(()=>{if(epoch!==shareActionEpoch)return;shareRefreshTimer=null;loadShare();},1500);
  }catch(e){if(epoch===shareActionEpoch)toastError('Share failed', e);}
  finally{if(epoch===shareActionEpoch){await loadShare();if(epoch===shareActionEpoch)setShareActionPending(false);}}
}
async function stopShare(){
  if(shareActionPending)return;
  clearTimeout(shareRefreshTimer);shareRefreshTimer=null;
  const epoch=++shareActionEpoch;
  setShareActionPending(true);shareLoadEpoch++;
  try{await api('/api/share/stop',{method:'POST'});if(epoch===shareActionEpoch)toast('tunnel stopped');}
  catch(e){if(epoch===shareActionEpoch)toastError('Share failed', e);}
  finally{if(epoch===shareActionEpoch){await loadShare();if(epoch===shareActionEpoch)setShareActionPending(false);}}
}
async function loadMergeStatus(){
  const el=$('#mergePresence'); if(!el) return;
  const epoch=++mergeStatusLoadEpoch;
  try{
    const s=await api('/api/merge/status');
    if(epoch!==mergeStatusLoadEpoch)return;
    if(!s.lastAt){el.textContent='No peer sync yet.';return;}
    const when=new Date(Number(s.lastAt)).toLocaleString();
    el.innerHTML=`Last <b>${esc(s.lastDir||'sync')}</b> ${s.lastLabel?('· '+esc(s.lastLabel)+' '):''}· ${esc(when)}${s.lastPeer?`<div class="hint font-mono">${esc(s.lastPeer)}</div>`:''}`;
  }catch(e){if(epoch!==mergeStatusLoadEpoch)return;renderLoadError(el,'Peer sync status',e,loadMergeStatus,false);}
}
function setPeerMergePending(pending){
  peerMergePending=pending;
  ['peerPull','peerPush','peerUrl','peerKey','peerLabel'].forEach(id=>{const control=$('#'+id);if(control){control.disabled=pending;control.setAttribute('aria-busy',pending&&id.startsWith('peer')&&['peerPull','peerPush'].includes(id)?'true':'false');}});
}
async function peerMerge(dir){
  if(peerMergePending)return;
  const peerUrl=$('#peerUrl').value.trim(),key=$('#peerKey').value.trim(),label=$('#peerLabel').value.trim();
  if(!peerUrl||!key){toast('peer URL and key are required');return;}
  const verb=dir==='pull'?'Pull from':'Push to';
  setPeerMergePending(true);
  $('#mergeResult').textContent='Previewing…';
  let previewMsg='';
  try{
    const prev=await api('/api/merge/'+dir,{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({peerUrl,key,label,dryRun:true})});
    if(dir==='pull'){
      previewMsg=`Would add <b>${prev.flowsAdded||0}</b> flows + <b>${prev.findingsAdded||0}</b> findings`
        +` (skip ${prev.flowsSkipped||0}/${prev.findingsSkipped||0} already present`
        +(prev.bodiesAdded?`; ${prev.bodiesAdded} new bodies`:'')+`).`;
    }else{
      previewMsg=`Would send <b>${prev.localFlows||0}</b> flows + <b>${prev.localFindings||0}</b> findings to peer.`
        +(prev.note?` <span class="hint">${esc(prev.note)}</span>`:'');
    }
    $('#mergeResult').innerHTML=previewMsg;
    if(!await uiConfirm(verb+' peer',previewMsg+'<br><br>'+verb+' <b>'+esc(peerUrl)+'</b>?',verb.split(' ')[0],'btn accent','var(--accent)')){
      $('#mergeResult').textContent='Cancelled.';
      return;
    }
    $('#mergeResult').textContent=verb.toLowerCase()+'ing…';
    const r=await api('/api/merge/'+dir,{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({peerUrl,key,label})});
    $('#mergeResult').innerHTML='<span class="u-accent">Done.</span> '+r.flowsAdded+' flows + '+r.findingsAdded+' findings added ('+r.flowsSkipped+'/'+r.findingsSkipped+' already present).';
    toast('sync complete');
    loadMergeStatus();
  }catch(e){$('#mergeResult').innerHTML='<span class="u-danger">'+esc(e.message)+'</span>';}
  finally{setPeerMergePending(false);}
}
// Live tunnel URL arrival (SSE) refreshes the panel if it's open.
window.addEventListener('interceptor:tunnel',()=>{const p=$('#apiShare');if(p&&p.style.display==='block')loadShare();});
const ss=$('#shareStart');if(ss)ss.onclick=startShare;
const sp=$('#shareStop');if(sp)sp.onclick=stopShare;
const pp=$('#peerPull');if(pp)pp.onclick=()=>peerMerge('pull');
const pu=$('#peerPush');if(pu)pu.onclick=()=>peerMerge('push');

/* ---- Project vault (always-on remote archive store) ---- */
async function loadVaultPanel(){
  const epoch=++vaultPanelLoadEpoch;
  const hint=$('#vaultCfgHint');
  try{
    const c=await api('/api/vault/config');
    if(epoch!==vaultPanelLoadEpoch)return;
    vaultConfigured=!!(c.url&&c.hasKey);
    const urlEl=$('#vaultUrl'); if(urlEl&&!urlEl.value) urlEl.value=c.url||'';
    if(hint) hint.textContent=c.hasKey?(c.url?'Configured → '+c.url:'Key saved — set URL'):'Save vault URL + token (iv_…) first.';
    const idEl=$('#vaultBackupId');
    if(idEl&&!idEl.value){
      try{const p=await api('/api/project');if(epoch!==vaultPanelLoadEpoch)return;idEl.placeholder=p.current||'project id';}catch{}
    }
    if(epoch!==vaultPanelLoadEpoch)return;
    setVaultActionPending(vaultActionPending);
    if(!vaultConfigured){
      vaultListLoadEpoch++;
      const tb=$('#vaultList');
      if(tb)tb.innerHTML='<tr><td colspan="4" class="hint cell-note">Configure vault URL and token to list backups.</td></tr>';
      return;
    }
    await refreshVaultList();
  }catch(e){if(epoch!==vaultPanelLoadEpoch)return;if(hint)hint.textContent=e.message||'';}
}
async function saveVaultCfg(){
  if(vaultActionPending)return;
  const url=$('#vaultUrl').value.trim();
  const key=$('#vaultKey').value.trim();
  const body={}; if(url) body.url=url; if(key) body.key=key;
  if(!url&&!key){toast('enter URL and/or key');return;}
  setVaultActionPending(true);
  try{
    vaultPanelLoadEpoch++;vaultListLoadEpoch++;
    await api('/api/vault/config',{method:'PUT',headers:{'content-type':'application/json'},body:JSON.stringify(body)});
    if(key&&$('#vaultKey').value.trim()===key)$('#vaultKey').value='';
    toast('vault config saved');
    loadVaultPanel();
  }catch(e){toastError('Vault config failed', e);}
  finally{setVaultActionPending(false);}
}
async function refreshVaultList(){
  const tb=$('#vaultList'); if(!tb) return;
  if(!vaultConfigured){
    tb.innerHTML='<tr><td colspan="4" class="hint cell-note">Configure vault URL and token to list backups.</td></tr>';
    return;
  }
  const epoch=++vaultListLoadEpoch;
  tb.innerHTML='<tr><td colspan="4" class="hint cell-note">Loading…</td></tr>';
  try{
    const d=await api('/api/vault/remote');
    if(epoch!==vaultListLoadEpoch)return;
    const projects=d.projects||[];
    tb.innerHTML=projects.length?projects.map(p=>{
      const id=esc(p.id||'');
      const rev=p.latestRev!=null?('#'+p.latestRev):'—';
      const size=p.latestSize!=null?fmtBytes(p.latestSize):'—';
      const label=p.latestLabel?esc(p.latestLabel):'';
      const when=p.latestAt?esc(new Date(p.latestAt).toLocaleString()):'';
      return `<tr>
        <td class="u-mono">${id}${label?' <span class="hint">'+label+'</span>':''}</td>
        <td>${esc(String(rev))}${when?('<div class="hint">'+when+'</div>'):''}</td>
        <td class="hint">${esc(String(size))}</td>
        <td><button class="btn btn-compact" data-vimp="${escAttr(p.id||'')}" aria-label="Import vault project ${escAttr(p.id||'')}">Import</button>
            <button class="btn btn-compact" data-vmerge="${escAttr(p.id||'')}" aria-label="Merge vault project ${escAttr(p.id||'')}">Merge</button></td></tr>`;
    }).join(''):'<tr><td colspan="4" class="hint cell-note">No projects in vault yet.</td></tr>';
    tb.querySelectorAll('[data-vimp]').forEach(b=>b.onclick=()=>vaultImport(b.dataset.vimp));
    tb.querySelectorAll('[data-vmerge]').forEach(b=>b.onclick=()=>vaultMerge(b.dataset.vmerge));
    if(vaultActionPending)setVaultActionPending(true);
  }catch(e){
    if(epoch!==vaultListLoadEpoch)return;
    tb.innerHTML='<tr><td colspan="4" class="hint cell-note u-warn">'+esc(e.message||'failed')+'</td></tr>';
  }
}
function setVaultActionPending(pending){
  vaultActionPending=pending;
  ['vaultBackup','vaultSaveCfg','vaultRefresh'].forEach(id=>{const button=$('#'+id);if(button){button.disabled=pending||(id!=='vaultSaveCfg'&&!vaultConfigured);button.setAttribute('aria-busy',pending&&id==='vaultBackup'?'true':'false');}});
  document.querySelectorAll('#vaultList [data-vimp],#vaultList [data-vmerge]').forEach(button=>{button.disabled=pending;});
}
async function vaultBackup(){
  if(vaultActionPending)return;
  const id=$('#vaultBackupId').value.trim();
  const label=$('#vaultBackupLabel').value.trim();
  const res=$('#vaultResult');
  setVaultActionPending(true);
  if(res) res.textContent='Backing up…';
  try{
    vaultListLoadEpoch++;
    const r=await api('/api/vault/backup',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({id:id||undefined,label:label||undefined})});
    if(res) res.innerHTML='<span class="u-accent">Backed up</span> '+(id||'')+(r.rev!=null?' rev #'+r.rev:'')+(r.size!=null?' · '+fmtBytes(r.size):'');
    toast('vault backup complete');
    refreshVaultList();
  }catch(e){if(res)res.innerHTML='<span class="u-danger">'+esc(e.message)+'</span>';toastError('Vault request failed', e);}
  finally{setVaultActionPending(false);}
}
async function vaultImport(id){
  if(vaultActionPending)return;
  setVaultActionPending(true);
  const res=$('#vaultResult');
  try{
    const name=await uiPrompt({title:'Import vault project',value:id,placeholder:'New project name'});
    if(!name)return;
    if(res) res.textContent='Importing…';
    const r=await api('/api/vault/import',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({id,name})});
    if(res) res.innerHTML='<span class="u-accent">Imported</span> as <b>'+esc(r.name||name)+'</b> — switch to it from Projects.';
    toast('imported '+ (r.name||name));
  }catch(e){if(res)res.innerHTML='<span class="u-danger">'+esc(e.message)+'</span>';toastError('Vault request failed', e);}
  finally{setVaultActionPending(false);}
}
async function vaultMerge(id){
  if(vaultActionPending)return;
  setVaultActionPending(true);
  const res=$('#vaultResult');
  if(res) res.textContent='Previewing…';
  let previewMsg='';
  try{
    const prev=await api('/api/vault/merge',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({id,dryRun:true})});
    previewMsg=`Would add <b>${prev.flowsAdded||0}</b> flows + <b>${prev.findingsAdded||0}</b> findings`
      +` (skip ${prev.flowsSkipped||0}/${prev.findingsSkipped||0} already present`
      +(prev.bodiesAdded?`; ${prev.bodiesAdded} new bodies`:'')+`).`;
    if(res) res.innerHTML=previewMsg;
    if(!await uiConfirm('Merge from vault',previewMsg+'<br><br>Merge <b>'+esc(id)+'</b> into the active project?','Merge','btn accent','var(--accent)')){
      if(res) res.textContent='Cancelled.';
      return;
    }
    if(res) res.textContent='Merging…';
    const r=await api('/api/vault/merge',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({id})});
    if(res) res.innerHTML='<span class="u-accent">Done.</span> '+r.flowsAdded+' flows + '+r.findingsAdded+' findings added ('+r.flowsSkipped+'/'+r.findingsSkipped+' already present).';
    toast('vault merge complete');
    loadMergeStatus();
  }catch(e){if(res)res.innerHTML='<span class="u-danger">'+esc(e.message)+'</span>';}
  finally{setVaultActionPending(false);}
}
{const vs=$('#vaultSaveCfg'); if(vs)vs.onclick=saveVaultCfg;}
{const vb=$('#vaultBackup'); if(vb)vb.onclick=vaultBackup;}
{const vr=$('#vaultRefresh'); if(vr)vr.onclick=refreshVaultList;}

export async function revokeKey(id,prefix,label){
  const who=(prefix?esc(prefix)+'…':'')+(label?' <b>'+esc(label)+'</b>':'');
  if(!await uiConfirm('Revoke API key',`Revoke key ${who||'#'+id}? Any client using it stops working immediately, and this can't be undone.`,'Revoke','btn danger','var(--red)'))return;
  try{apiKeysLoadEpoch++;await api('/api/keys/'+id,{method:'DELETE'});loadApiKeys();toast('key revoked');}catch(e){toastError('Revoke failed', e);}
}
$('#keyCreate').onclick=createApiKey;
export async function loadReference(){
  const epoch=++restReferenceLoadEpoch;
  const loadState=$('#restLoadState');
  const hadData=restReferenceLoaded;
  if(loadState){
    if(hadData){loadState.style.display='block';loadState.textContent='Refreshing REST reference…';}
    else loadState.style.display='none';
  }
  if(hadData)markRESTReferenceStale(true);
  try{const d=await api('/api/reference');
    if(epoch!==restReferenceLoadEpoch)return;
    $('#apiBase').textContent='Base URL: '+d.baseUrl;
    $('#restList').innerHTML=(d.routes||[]).map(r=>`<tr>
      <td style="color:${methodColor(r.method)};font-weight:700;font-family:var(--mono)">${esc(r.method)}</td>
      <td class="u-mono u-fg">${esc(r.path)}</td>
      <td class="u-fg2">${esc(r.desc)}</td></tr>`).join('');
    restReferenceLoaded=true;markRESTReferenceStale(false);
    if(loadState)loadState.style.display='none';
  }catch(e){
    if(epoch!==restReferenceLoadEpoch)return;
    markRESTReferenceStale(hadData);
    renderLoadError($('#restLoadState'),'REST reference',e,loadReference,hadData);
  }
}
export async function loadMCP(){
  // Re-entering the API & MCP section, the Retry action and a project switch can
  // all start this read; only the newest one may paint and rebind the Copy
  // buttons (matching loadReference() above).
  const epoch=++mcpLoadEpoch;
  try{const m=await api('/api/mcp');
    if(epoch!==mcpLoadEpoch)return;
    const httpCfg=JSON.stringify(m.clientConfig||{},null,2);
    const stdioCfg=JSON.stringify(m.stdioClientConfig||{},null,2);
    const cmd=`${(m.transport&&m.transport.command)||'interseptor'} ${((m.transport&&m.transport.args)||[]).join(' ')}`.trim();
    const tools=(m.tools||[]).map(t=>`<tr>
      <td class="mono-accent">${esc(t.name)}</td>
      <td class="u-fg2">${esc(t.desc)}</td></tr>`).join('');
    $('#mcpBody').innerHTML=`
      <div class="row u-gap-2"><span class="sev ${m.status==='ready'?'Low':'Info'}">${esc(m.status)}</span>
        <span class="hint">Assistant connection</span></div>
      <details class="settings-help"${m.status!=='ready'?' open':''}><summary>Connection help</summary><p class="hint u-lh u-my-3">${esc(m.note||'')}</p></details>
      <div class="micro-label micro-label-accent u-m-0 u-mt-4 u-mb-2">Cursor · HTTP</div>
      <div class="row u-gap-3 u-mb-2"><span class="hint"><code>.cursor/mcp.json</code></span><button class="btn accent btn-compact" id="mcpCopyHttp">Copy</button></div>
      <pre class="evidence pre-scroll">${esc(httpCfg)}</pre>
      ${m.httpTransport?`<p class="hint u-m-0 u-mt-2 u-lh">Endpoint: <code>${esc(m.httpTransport.url||'')}</code> · ${esc(m.httpTransport.note||'')}</p>`:''}
      <details class="settings-help"><summary>Claude Desktop / stdio</summary>
      <div class="evidence u-mono u-mb-2">${esc(cmd)}</div>
      <p class="hint u-m-0 u-mb-2">Windows: <code>scripts/interceptor-mcp.cmd</code> resolves the latest <code>interseptor</code> on PATH after <code>go install</code> / <code>interseptor update</code>.</p>
      <div class="row u-gap-3 u-mb-2"><span class="micro-label">STDIO CLIENT CONFIG</span><button class="btn btn-compact" id="mcpCopyStdio">Copy</button></div>
      <pre class="evidence pre-scroll">${esc(stdioCfg)}</pre>
      </details><details class="settings-help"><summary>Available tools (${(m.tools||[]).length})</summary>
      <table class="rules-tbl"><thead><tr><th class="col-4">Tool</th><th>Description</th></tr></thead><tbody>${tools}</tbody></table></details>`;
    const cpH=document.getElementById('mcpCopyHttp'); if(cpH) cpH.onclick=()=>copyText(httpCfg,'Cursor MCP config copied');
    const cpS=document.getElementById('mcpCopyStdio'); if(cpS) cpS.onclick=()=>copyText(stdioCfg,'stdio MCP config copied');
  }catch(e){if(epoch!==mcpLoadEpoch)return;renderLoadError($('#mcpBody'),'MCP reference',e,loadMCP,false);}
}
