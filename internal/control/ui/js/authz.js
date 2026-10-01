// authz.js — authorization (access-control) testing. Replays captured request(s)
// under each saved identity (role) and diffs responses to surface IDOR / broken
// access control. Launched from History right-click or command palette.
import { $, esc, escAttr, state, api, toast, openModal, closeModal, statusColor, fmtSize, wireRowKey, renderLoadError } from './core.js';
import { selectFlow, refreshAuthzIds } from './proxy.js';

let authzFlowId = null;
let authzSelectionAtOpen=null;
let authzSelectionChanged=false;
let authzHintTarget=null;
let authzViewMode = 'list'; // 'list' | 'matrix' — toggled in bulk results
let authzMode = 'flow';     // 'flow' | 'scope' | 'crosshost' — the segmented picker at the top
let authzActionBusy = false;
let authzActionFocus=null;
let authzScopeEpoch=0,authzHintEpoch=0,authzRunEpoch=0,authzIdentityLoadEpoch=0,authzIdentityEditEpoch=0;
let authzIdentityMutationTail=Promise.resolve();

// The results pane speaks the shared state vocabulary from app.css instead of
// hand-rolled `<div class="hint">` placeholders: an empty state is an icon +
// title + one-line hint, a failure is a .state-error with an alert message.
const authzEmptyState=(iconName,title,hint)=>`<div class="state-empty"><div class="state-empty-icon"><svg class="icon" aria-hidden="true" focusable="false"><use href="#i-${iconName}"/></svg></div><div class="state-empty-title">${title}</div><p class="state-empty-hint">${hint}</p></div>`;
const authzErrorState=message=>`<div class="state-error"><div class="state-error-icon"><svg class="icon" aria-hidden="true" focusable="false"><use href="#i-warning"/></svg></div><span class="state-error-msg" role="alert">${esc(message)}</span></div>`;
function setAuthzActionBusy(busy) {
  const modal=$('#authzModal');
  if(busy&&!authzActionBusy){
    const active=document.activeElement;
    authzActionFocus=modal?.contains(active)?active:null;
  }
  const restore=!busy&&authzActionBusy?authzActionFocus:null;
  if(!busy)authzActionFocus=null;
  authzActionBusy = !!busy;
  ['#authzRun', '#authzCheck', '#authzSave', '#authzFromFlow', '#authzAdd', '#authzClose', '#authzScopeEdit'].forEach(sel => {
    const b = $(sel); if (!b) return;
    b.disabled = authzActionBusy;
    b.setAttribute('aria-busy', authzActionBusy ? 'true' : 'false');
  });
  $('#authzIds')?.querySelectorAll('input,textarea,button').forEach(control=>{control.disabled=authzActionBusy;});
  const mode=$('#authzMode');
  if(mode){
    mode.setAttribute('aria-busy',authzActionBusy?'true':'false');
    mode.querySelectorAll('button').forEach(button=>{button.disabled=authzActionBusy;});
  }
  const status=$('#authzStatus');
  if(status)status.setAttribute('aria-busy',authzActionBusy?'true':'false');
  if(authzActionBusy&&authzActionFocus&&status)status.focus({preventScroll:true});
  else if(restore&&authzModalOpen()&&restore.isConnected&&!restore.disabled)restore.focus({preventScroll:true});
}
function setAuthzStatus(message,kind='status') {
  const status = $('#authzStatus');
  if (!status) return;
  status.textContent = message || '';
  status.setAttribute('role',kind==='error'?'alert':'status');
  status.setAttribute('aria-live',kind==='error'?'assertive':'polite');
}
// A context-menu action owns its explicit flow even when another History row was
// selected before the modal opened. A selection made after opening deliberately
// retargets the modal and invalidates any in-flight action through its snapshots.
const authzTarget = () => authzSelectionChanged?(state.selId||authzFlowId):authzFlowId;
function syncAuthzLabel(){
  const f=authzTarget();const el=$('#authzFlow');if(el)el.textContent=f?('#'+f):'(none — select in History)';
  if(authzModalOpen()&&f!==authzHintTarget){
    authzHintTarget=f||null;++authzHintEpoch;
    const box=$('#authzCookieHint');if(box){box.style.display='none';box.textContent='';}
    if(f)loadFlowAuthHint(f);
  }
}
export function onAuthzSelectionChanged(){if(!authzModalOpen())return;authzSelectionChanged=true;syncAuthzLabel();}

function authzModalOpen(){return $('#authzModal')?.style.display==='flex';}
function authzActionCurrent(epoch,mode,target,requiresTarget=true){
  if(epoch!==authzRunEpoch||!authzModalOpen()||authzMode!==mode)return false;
  return !requiresTarget||authzTarget()===target;
}
function closeAuthz(){
  if(authzActionBusy)return;
  ++authzRunEpoch;++authzHintEpoch;++authzScopeEpoch;++authzIdentityLoadEpoch;
  authzHintTarget=null;
  setAuthzActionBusy(false);
  closeModal($('#authzModal'));
}

function openSettingsScope(){
  if(authzActionBusy)return;
  closeAuthz();
  document.querySelector('.tab[data-tab="settings"]')?.click();
  document.querySelector('#setNav button[data-sec="scope"]')?.click();
}
function authzScopeRuleLine(r){
  const tag=r.action==='exclude'?'exclude':'include';
  const color=tag==='exclude'?'var(--red)':'var(--accent)';
  const host=r.host||'(any host)';
  const extra=[r.path?'path:'+r.path:'',r.scheme?r.scheme:''].filter(Boolean).join(' · ');
  return `<div style="font-family:var(--mono);font-size:var(--fs-xs);padding:3px 0"><span style="font-weight:700;color:${color}">${tag}</span> <span style="color:var(--fg)">${esc(host)}</span>${extra?` <span class="hint">${esc(extra)}</span>`:''}</div>`;
}
async function renderAuthzScopePanel(){
  const panel=$('#authzScopePanel');if(!panel)return;
  const epoch=++authzScopeEpoch;
  const scopeMode=authzMode==='scope';
  panel.style.display=scopeMode?'':'none';
  if(!scopeMode)return;
  let rules=[];
  try{const d=await api('/api/scope');if(epoch!==authzScopeEpoch||authzMode!=='scope'||!authzModalOpen())return;rules=d.rules||[];}
  catch(e){if(epoch!==authzScopeEpoch||authzMode!=='scope'||!authzModalOpen())return;renderLoadError(panel,'Authorization scope',e,renderAuthzScopePanel,state.scope.length>0);return;}
  const enabled=rules.filter(r=>r.enabled);
  const includes=enabled.filter(r=>r.action==='include');
  const excludes=enabled.filter(r=>r.action==='exclude');
  let html='';
  if(!rules.length){
    html=`<p class="hint" style="color:var(--amber);margin:0;line-height:1.55"><b>No scope rules.</b> Bulk authz requires include rules in <b>Settings → Target scope</b>.</p>`;
  }else if(!includes.length){
    html=`<p class="hint" style="color:var(--amber);margin:0 0 8px"><b>No include rules.</b> Add at least one before bulk run.</p>`;
    if(excludes.length)html+=excludes.map(authzScopeRuleLine).join('');
  }else{
    html=`<div class="micro-label" style="margin:0 0 6px">IN-SCOPE (from Settings → Target scope)</div>`;
    html+=includes.map(authzScopeRuleLine).join('');
    if(excludes.length)html+=`<div class="micro-label" style="margin:10px 0 4px">EXCLUDE (always wins)</div>`+excludes.map(authzScopeRuleLine).join('');
  }
  html+=`<div class="row" style="gap:8px;margin-top:10px;flex-wrap:wrap;align-items:center"><button class="btn" type="button" id="authzScopeEdit">Settings → Target scope</button><span class="hint" id="authzScopeHosts">checking captured traffic…</span></div>`;
  panel.innerHTML=html;
  setAuthzActionBusy(authzActionBusy);
  $('#authzScopeEdit')?.addEventListener('click',openSettingsScope);
  try{
    const d=await api('/api/flows?limit=500&inScope=1');
    if(epoch!==authzScopeEpoch||authzMode!=='scope'||!authzModalOpen())return;
    const hosts=[...new Set((d.flows||[]).map(f=>f.host).filter(Boolean))].sort();
    const el=$('#authzScopeHosts');
    if(!el)return;
    if(!hosts.length)el.textContent='No in-scope traffic in history yet — browse the target through the proxy first.';
    else el.textContent=`${hosts.length} host${hosts.length===1?'':'s'} in history: ${hosts.slice(0,10).join(', ')}${hosts.length>10?'…':''} (static assets skipped in bulk run)`;
  }catch(e){if(epoch!==authzScopeEpoch||authzMode!=='scope'||!authzModalOpen())return;const el=$('#authzScopeHosts');if(el)renderLoadError(el,'In-scope traffic',e,renderAuthzScopePanel,false);}
}

async function loadFlowAuthHint(flowId){
  authzHintTarget=flowId||null;
  const box=$('#authzCookieHint');if(!box||!flowId){if(box)box.style.display='none';return;}
  const epoch=++authzHintEpoch;
  try{
    const d=await api('/api/authz/flow-auth/'+flowId);
    if(epoch!==authzHintEpoch||authzTarget()!==flowId||!authzModalOpen())return;
    const hints=(d.cookieHints||[]);
    const authLine=[d.cookie?'Cookie: '+d.cookie:'',d.authorization?'Authorization: '+d.authorization:''].filter(Boolean).join('\n');
    if(!hints.length&&!authLine){box.style.display='none';return;}
    let t='';
    if(authLine)t+='Captured request auth: <span style="font-family:var(--mono);color:var(--fg2)">'+esc(authLine.replace(/\n/g,' · '))+'</span>. ';
    if(hints.length)t+='Cookie hints: '+hints.map(h=>esc(h)).join('; ')+'.';
    box.innerHTML=t+' Use <b>⧉ From flow</b> to fill the baseline identity.';
    box.style.display='';
  }catch(e){if(epoch!==authzHintEpoch||authzTarget()!==flowId||!authzModalOpen())return;box.style.display='';renderLoadError(box,'Captured authentication',e,()=>loadFlowAuthHint(flowId),false);}
}

export function openAuthz(flowId){
  ++authzRunEpoch;++authzIdentityLoadEpoch;
  authzSelectionAtOpen=state.selId||null;
  authzSelectionChanged=false;
  authzFlowId=flowId||authzSelectionAtOpen||null;
  authzHintTarget=null;
  setAuthzActionBusy(false);
  openModal($('#authzModal'),{onEscape:closeAuthz,onDismiss:closeAuthz});
  syncAuthzLabel();
  $('#authzResults').innerHTML=authzEmptyState('gate','No replay yet','Define identities, then Run. Use Check sessions first if cookies may be stale.');
  setAuthzStatus('');
  setAuthzMode('flow');
  loadAuthzIdentities();
}
// setAuthzMode drives the 3-way picker (Selected flow | All in-scope | Cross-host
// JWT): each mode shows only the controls it needs and retargets the Run button.
function setAuthzMode(m){
  if(authzActionBusy)return;
  authzMode=m;
  const seg=$('#authzMode');
  if(seg)seg.querySelectorAll('button').forEach(b=>{const on=b.dataset.m===m;b.classList.toggle('on',on);b.setAttribute('aria-pressed',on?'true':'false');});
  const mx=$('#authzMax'); if(mx)mx.style.display=m==='scope'?'':'none';
  const run=$('#authzRun'); if(run)run.textContent=m==='crosshost'?'Run cross-host ▸':'Run ▸';
  renderAuthzScopePanel();
}

async function loadAuthzIdentities(){
  const epoch=++authzIdentityLoadEpoch,editEpoch=authzIdentityEditEpoch;
  try{await authzIdentityMutationTail;if(epoch!==authzIdentityLoadEpoch||editEpoch!==authzIdentityEditEpoch||!authzModalOpen()||authzActionBusy)return;const d=await api('/api/authz');if(epoch!==authzIdentityLoadEpoch||editEpoch!==authzIdentityEditEpoch||!authzModalOpen()||authzActionBusy)return;renderIdentities(d.identities||[]);refreshAuthzIds();}
  catch(e){if(epoch!==authzIdentityLoadEpoch||editEpoch!==authzIdentityEditEpoch||!authzModalOpen()||authzActionBusy)return;renderLoadError($('#authzIds'),'Saved identities',e,loadAuthzIdentities,false);}
}
function renderIdentities(ids){
  if(!ids.length)ids=[{name:'',headers:''}];
  $('#authzIds').innerHTML=ids.map((id,i)=>`<div class="authz-id${id.broken?' authz-id-broken':''}" data-i="${i}">
    <input class="authz-name btn" aria-label="Authorization identity ${i+1} name" style="background:var(--bg3)" placeholder="role e.g. ${i===0?'admin (baseline)':'user'}" value="${escAttr(id.name||'')}">
    <textarea class="authz-hdr rep-edit" aria-label="Authorization identity ${i+1} headers" rows="2" placeholder="Cookie: session=…  (blank = anonymous)">${esc(id.headers||'')}</textarea>
    <div style="display:flex;gap:4px">
      <button class="btn${id.broken?' danger':''} authz-broken" data-i="${i}" aria-label="${id.broken?'Unmark':'Mark'} authorization identity ${i+1} as broken" title="${id.broken?'Account marked broken — click to unmark':'Mark account as broken/locked (skipped in runs)'}">${id.broken?'<svg class="icon" aria-hidden="true" focusable="false"><use href="#i-warning"/></svg> broken':'<svg class="icon" aria-hidden="true" focusable="false"><use href="#i-warning"/></svg>'}</button>
      <button class="btn danger authz-del" data-i="${i}" aria-label="Remove authorization identity ${i+1}" title="Remove identity">✕</button>
    </div>
    ${id.broken&&id.brokenNote?`<div class="hint" style="font-size:var(--fs-xs);margin-top:2px;color:var(--amber)">${esc(id.brokenNote)}</div>`:''}
  </div>`).join('');
  document.querySelectorAll('#authzIds .authz-name,#authzIds .authz-hdr').forEach(input=>input.addEventListener('input',()=>{authzIdentityEditEpoch++;}));
  document.querySelectorAll('#authzIds .authz-del').forEach(b=>b.onclick=()=>{
    authzIdentityEditEpoch++;
    const ids=collectIds();ids.splice(Number(b.dataset.i),1);
    renderIdentities(ids.length?ids:[{name:'',headers:''}]);
  });
  document.querySelectorAll('#authzIds .authz-broken').forEach(b=>b.onclick=()=>{
    authzIdentityEditEpoch++;
    const ids=collectIds();const i=Number(b.dataset.i);
    ids[i].broken=!ids[i].broken;
    renderIdentities(ids);
  });
}
function collectIds(){
  return [...document.querySelectorAll('#authzIds .authz-id')].map(el=>({
    name:el.querySelector('.authz-name').value,
    headers:el.querySelector('.authz-hdr').value,
    broken:el.classList.contains('authz-id-broken'),
  })).filter(x=>x.name||x.headers);
}
function saveIds(identities=collectIds()){
  const operation=authzIdentityMutationTail.then(()=>api('/api/authz',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({identities})}));
  authzIdentityMutationTail=operation.catch(()=>{});
  return operation;
}

async function fillFromFlow(){
  if(authzActionBusy)return;
  const fid=authzTarget(); syncAuthzLabel();
  if(!fid){toast('select a flow first');return;}
  const mode=authzMode,epoch=++authzRunEpoch;
  setAuthzStatus('Loading captured authentication…');
  setAuthzActionBusy(true);
  try{
    const d=await api('/api/authz/flow-auth/'+fid);
    if(!authzActionCurrent(epoch,mode,fid))return;
    const requestAuth=[d.cookie?'Cookie: '+d.cookie:'',d.authorization?'Authorization: '+d.authorization:''].filter(Boolean).join('\n');
    if(!requestAuth){toast('no Cookie/Authorization on that request');setAuthzStatus('No captured authentication found');return;}
    const ids=collectIds();
    let i=ids.findIndex(x=>!x.headers.trim());
    if(i<0){ids.unshift({name:'',headers:''});i=0;}
    ids[i].headers=requestAuth;
    if(!ids[i].name)ids[i].name='from flow';
    authzIdentityEditEpoch++;
    renderIdentities(ids);
    toast('filled identity from flow #'+fid);
    setAuthzStatus('Captured authentication loaded');
  }catch(e){if(authzActionCurrent(epoch,mode,fid)){toast(e.message,'error');setAuthzStatus('Loading captured authentication failed: '+e.message,'error');}}
  finally{if(epoch===authzRunEpoch)setAuthzActionBusy(false);}
}

async function checkSessions(){
  if(authzActionBusy)return;
  const probe=authzTarget(); syncAuthzLabel();
  if(!probe){toast('select a flow to probe sessions (e.g. GET /api/me)');return;}
  const identities=collectIds();
  if(identities.length<1){toast('add at least one identity');return;}
  $('#authzResults').innerHTML='<div class="hint">checking sessions…</div>';
  setAuthzStatus('Checking sessions…');
  const mode=authzMode,epoch=++authzRunEpoch;
  setAuthzActionBusy(true);
  try{
    await saveIds(identities);
    if(!authzActionCurrent(epoch,mode,probe))return;
    const d=await api('/api/authz/check-sessions',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({flowId:probe})});
    if(!authzActionCurrent(epoch,mode,probe))return;
    const checks=d.checks||[];
    $('#authzResults').innerHTML='<div class="authz-row authz-head"><span>identity</span><span>status</span><span>session</span><span></span></div>'
      +checks.map(c=>c.broken?`<div class="authz-row" style="opacity:.55">
        <span>${esc(c.name||'(unnamed)')}</span>
        <span style="color:var(--fg3)">—</span>
        <span><span style="color:var(--amber)"><svg class="icon" aria-hidden="true" focusable="false"><use href="#i-warning"/></svg> broken</span></span>
        <span></span></div>`:`<div class="authz-row${c.sessionInvalid?' flag':''}">
        <span>${esc(c.name||'(unnamed)')}</span>
        <span style="color:${statusColor(c.status)};font-weight:700">${c.error?'ERR':(c.status||'—')}</span>
        <span>${!c.hasAuth?'<span class="hint">anonymous</span>':c.sessionInvalid?'<span style="color:var(--red);font-weight:700">expired?</span>':'<span class="hint">ok</span>'}</span>
        <span></span></div>`).join('');
    setAuthzStatus('Session check complete');
  }catch(e){if(!authzActionCurrent(epoch,mode,probe))return;$('#authzResults').innerHTML=authzErrorState('Check failed: '+e.message);setAuthzStatus('Session check failed: '+e.message,'error');}
  finally{if(epoch===authzRunEpoch)setAuthzActionBusy(false);}
}

function runBody(){
  const bulk=authzMode==='scope';
  const fid=authzTarget(); syncAuthzLabel();
  if(!bulk&&!fid){toast('select a flow or choose all in-scope');return null;}
  const body={maxFlows:parseInt($('#authzMax')?.value,10)||0};
  if(bulk)body.inScope=true; else body.flowId=fid;
  return body;
}

function renderAuthzRow(r,i){
  let verdict='';
  if(r.broken)verdict='<span style="color:var(--amber)"><svg class="icon" aria-hidden="true" focusable="false"><use href="#i-warning"/></svg> broken — skipped</span>';
  else if(i===0)verdict='<span class="hint">baseline</span>';
  else if(r.sessionInvalid)verdict='<span style="color:var(--amber);font-weight:700">session?</span>';
  else if(r.sameAsBaseline)verdict='<span style="color:var(--red);font-weight:700"><svg class="icon" aria-hidden="true" focusable="false"><use href="#i-warning"/></svg> same access</span>';
  else verdict='<span class="hint">differs ✓</span>';
  return `<div class="authz-row${r.sameAsBaseline||r.sessionInvalid?' flag':''}${r.broken?' authz-broken-row':''}"${r.flowId?` data-flow="${r.flowId}"`:''}>
    <span${r.broken?' style="opacity:.6"':''}>${esc(r.name||'(unnamed)')}</span>
    <span style="color:${r.broken?'var(--fg3)':statusColor(r.status)};font-weight:700">${r.broken?'—':(r.error?'ERR':(r.status||'—'))}</span>
    <span>${r.broken?'—':fmtSize(r.length)}</span>
    <span>${verdict}</span></div>`;
}

function wireAuthzFlowRows(box){
  box.querySelectorAll('[data-flow]').forEach(el=>{
    const go=()=>{closeAuthz();selectFlow(Number(el.dataset.flow));};
    el.setAttribute('aria-label','Inspect captured flow #'+el.dataset.flow);
    el.onclick=go;wireRowKey(el,go);
  });
}

function renderAuthzListBulk(runs){
  let html='';
  runs.forEach(run=>{
    const flagged=(run.results||[]).some((r,i)=>i>0&&r.sameAsBaseline);
    html+=`<details style="margin-bottom:8px;border:1px solid var(--line);border-radius:8px;padding:6px 10px"${flagged?' open':''}>
      <summary style="cursor:pointer;font-family:var(--mono);font-size:var(--fs-xs);color:${flagged?'var(--red)':'var(--fg)'}">
        <span style="color:var(--accent);font-weight:700">${esc(run.method)}</span> ${esc(run.host)}${esc(run.path||'/')}
        ${flagged?' · <b style="color:var(--red)"><svg class="icon" aria-hidden="true" focusable="false"><use href="#i-warning"/></svg> access issue</b>':''}
      </summary>
      <div class="authz-row authz-head" style="margin-top:8px"><span>identity</span><span>status</span><span>length</span><span>verdict</span></div>
      ${(run.results||[]).map((r,i)=>renderAuthzRow(r,i)).join('')}
    </details>`;
  });
  return html;
}

function renderAuthzMatrix(runs){
  if(!runs.length)return '<div class="hint">no results</div>';
  const names=(runs[0].results||[]).map(r=>r.name||'(unnamed)');
  let html=`<div style="overflow-x:auto"><table style="width:100%;border-collapse:collapse;font-size:var(--fs-xs)">
    <thead><tr>
      <th style="text-align:left;padding:5px 8px;border-bottom:1px solid var(--line);color:var(--fg3)">endpoint</th>
      ${names.map((n,i)=>`<th style="text-align:center;padding:5px 8px;border-bottom:1px solid var(--line);white-space:nowrap">${i===0?`<span style="color:var(--fg3)">${esc(n)}</span>`:esc(n)}</th>`).join('')}
    </tr></thead><tbody>`;
  for(const run of runs){
    const rowFlagged=(run.results||[]).some((r,i)=>i>0&&r.sameAsBaseline);
    html+=`<tr${rowFlagged?' style="background:color-mix(in srgb,var(--red) 7%,transparent)"':''}>
      <td style="padding:5px 8px;font-family:var(--mono);border-bottom:1px solid var(--line2);white-space:nowrap"><span style="color:var(--accent);font-weight:700">${esc(run.method)}</span> <span style="color:var(--fg2);font-size:var(--fs-xs)">${esc(run.host)}${esc(run.path||'/')}</span></td>
      ${(run.results||[]).map((r,i)=>{
        const warn=i>0&&r.sameAsBaseline;
        const err=!!r.error||r.status===0;
        return `<td style="text-align:center;padding:5px 8px;border-bottom:1px solid var(--line2)"${r.flowId?` data-flow="${r.flowId}"`:''}>${i===0?`<span class="hint" style="font-size:var(--fs-xs)">—</span>`:`<span style="color:${err?'var(--fg3)':statusColor(r.status)};font-weight:700">${err?'ERR':(r.status||'—')}</span><span style="color:var(--fg3);font-size:var(--fs-xs);display:block">${fmtSize(r.length)}</span>${warn?'<span style="color:var(--red);font-size:var(--fs-xs)"><svg class="icon" role="img" aria-label="same access as baseline" focusable="false"><use href="#i-warning"/></svg></span>':''}${r.sessionInvalid?'<span style="color:var(--amber);font-size:var(--fs-xs)">sess?</span>':''}`}</td>`;
      }).join('')}
    </tr>`;
  }
  html+='</tbody></table></div>';
  return html;
}

function renderAuthzResults(d){
  const runs=d.runs||[];
  const box=$('#authzResults');
  if(!runs.length){box.innerHTML=authzEmptyState('clipboard','No results','This run produced no comparable responses.');return;}
  const bulk=runs.length>1||authzMode==='scope';
  if(!bulk){
    const res=runs[0].results||[];
    box.innerHTML='<div class="authz-row authz-head"><span>identity</span><span>status</span><span>length</span><span>verdict</span></div>'
      +res.map((r,i)=>renderAuthzRow(r,i)).join('');
    wireAuthzFlowRows(box);
    return;
  }
  const sum=d.summary||{};
  const toggleHtml=`<div class="row" style="gap:6px;margin-bottom:8px;align-items:center">
    <span class="hint">${sum.endpoints||runs.length} endpoint${(sum.endpoints||runs.length)===1?'':'s'} · ${sum.flagged||0} flagged</span>
    <div class="spacer"></div>
    <button class="btn${authzViewMode==='list'?' on':''}" id="authzViewList" style="padding:2px 7px;font-size:var(--fs-xs)">☰ List</button>
    <button class="btn${authzViewMode==='matrix'?' on':''}" id="authzViewMatrix" style="padding:2px 7px;font-size:var(--fs-xs)">⊞ Matrix</button>
  </div>`;
  box.innerHTML=toggleHtml+(authzViewMode==='matrix'?renderAuthzMatrix(runs):renderAuthzListBulk(runs));
  $('#authzViewList')?.addEventListener('click',()=>{authzViewMode='list';renderAuthzResults(d);});
  $('#authzViewMatrix')?.addEventListener('click',()=>{authzViewMode='matrix';renderAuthzResults(d);});
  wireAuthzFlowRows(box);
}

async function crossHostReplay(){
  if(authzActionBusy)return;
  const fid=authzTarget();syncAuthzLabel();
  if(!fid){toast('select a reference flow first');return;}
  $('#authzResults').innerHTML='<div class="hint">replaying to all in-scope hosts…</div>';
  setAuthzStatus('Running cross-host replay…');
  const mode=authzMode,epoch=++authzRunEpoch;setAuthzActionBusy(true);
  try{
    const d=await api('/api/authz/cross-host-replay',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({flowId:fid})});
    if(!authzActionCurrent(epoch,mode,fid))return;renderCrossHostResults(d);
    setAuthzStatus('Cross-host replay complete');
  }catch(e){if(!authzActionCurrent(epoch,mode,fid))return;$('#authzResults').innerHTML=authzErrorState('Run failed: '+e.message);setAuthzStatus('Cross-host replay failed: '+e.message,'error');}
  finally{if(epoch===authzRunEpoch)setAuthzActionBusy(false);}
}

function renderCrossHostResults(d){
  const box=$('#authzResults');
  const results=d.results||[];
  if(!results.length){box.innerHTML=authzEmptyState('globe','No in-scope hosts','Browse the target through the proxy first.');return;}
  const accepted=results.filter(r=>r.accepted).length;
  let html=`<div class="hint" style="margin-bottom:8px">Cross-host JWT replay · <span style="font-family:var(--mono)">${esc(d.method||'')} ${esc(d.path||'/')}</span> · ${accepted} of ${results.length} host${results.length===1?'':'s'} accepted</div>`;
  html+='<div class="authz-row authz-head"><span>host</span><span>status</span><span>length</span><span>verdict</span></div>';
  results.forEach(r=>{
    const err=!!r.error||r.status===0;
    html+=`<div class="authz-row${r.accepted?' flag':''}"${r.flowId?` data-flow="${r.flowId}"`:''}>
      <span style="font-family:var(--mono);font-size:var(--fs-xs)">${esc(r.host)}</span>
      <span style="color:${err?'var(--fg3)':statusColor(r.status)};font-weight:700">${err?'ERR':(r.status||'—')}</span>
      <span>${fmtSize(r.length)}</span>
      <span>${r.accepted?'<span style="color:var(--red);font-weight:700"><svg class="icon" aria-hidden="true" focusable="false"><use href="#i-warning"/></svg> accepted</span>':'<span class="hint">rejected ✓</span>'}</span>
    </div>`;
  });
  box.innerHTML=html;
  wireAuthzFlowRows(box);
}

$('#authzAdd')&&($('#authzAdd').onclick=()=>{authzIdentityEditEpoch++;renderIdentities([...collectIds(),{name:'',headers:''}]);});
$('#authzFromFlow')&&($('#authzFromFlow').onclick=fillFromFlow);
$('#authzCheck')&&($('#authzCheck').onclick=checkSessions);
$('#authzSave')&&($('#authzSave').onclick=async()=>{
  if(authzActionBusy)return;
  const identities=collectIds(),mode=authzMode,target=authzTarget(),epoch=++authzRunEpoch;
  setAuthzStatus('Saving identities…');
  setAuthzActionBusy(true);
  try{await saveIds(identities);if(!authzActionCurrent(epoch,mode,target,false))return;toast('identities saved');refreshAuthzIds();setAuthzStatus('Identities saved');}
  catch(e){if(!authzActionCurrent(epoch,mode,target,false))return;toast('Save failed: '+e.message,'error');setAuthzStatus('Saving identities failed: '+e.message,'error');}
  finally{if(epoch===authzRunEpoch)setAuthzActionBusy(false);}
});
$('#authzClose')&&($('#authzClose').onclick=closeAuthz);
$('#authzMode')&&($('#authzMode').querySelectorAll('button').forEach(b=>b.onclick=()=>setAuthzMode(b.dataset.m)));
$('#authzRun')&&($('#authzRun').onclick=async()=>{
  if(authzActionBusy)return;
  // Cross-host JWT replay is a distinct action — dispatch it instead of the role-swap run.
  if(authzMode==='crosshost'){crossHostReplay();return;}
  const mode=authzMode,target=authzTarget(),identities=collectIds();
  const body=runBody();if(!body)return;
  if(identities.length<1){toast('add at least one identity');return;}
  $('#authzResults').innerHTML='<div class="hint">replaying…</div>';
  setAuthzStatus('Running authorization replay…');
  const epoch=++authzRunEpoch;setAuthzActionBusy(true);
  try{
    await saveIds(identities);
    if(!authzActionCurrent(epoch,mode,target,mode!=='scope'))return;
    refreshAuthzIds();
    const d=await api('/api/authz/run',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify(body)});
    if(!authzActionCurrent(epoch,mode,target,mode!=='scope'))return;renderAuthzResults(d);
    setAuthzStatus('Authorization replay complete');
  }catch(e){if(!authzActionCurrent(epoch,mode,target,mode!=='scope'))return;$('#authzResults').innerHTML=authzErrorState('Run failed: '+e.message);setAuthzStatus('Authorization replay failed: '+e.message,'error');}
  finally{if(epoch===authzRunEpoch)setAuthzActionBusy(false);}
});

export { renderAuthzScopePanel };
