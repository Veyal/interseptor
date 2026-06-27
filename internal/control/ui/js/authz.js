// authz.js — authorization (access-control) testing. Replays captured request(s)
// under each saved identity (role) and diffs responses to surface IDOR / broken
// access control. Launched from History right-click or command palette.
import { $, esc, escAttr, state, api, toast, openModal, closeModal, statusColor, fmtSize, wireRowKey } from './core.js';
import { selectFlow, refreshAuthzIds } from './proxy.js';

let authzFlowId = null;
let authzViewMode = 'list'; // 'list' | 'matrix' — toggled in bulk results
// authzTarget resolves the flow to act on AT CALL TIME — the live History selection
// wins (so changing selection while the modal is open isn't ignored, which would
// silently test the wrong endpoint for IDOR), falling back to the flow the modal
// was opened for.
const authzTarget = () => state.selId || authzFlowId;
function syncAuthzLabel(){ const f=authzTarget(); const el=$('#authzFlow'); if(el)el.textContent=f?('#'+f):'(none — select in History)'; }

function openSettingsScope(){
  closeModal($('#authzModal'));
  document.querySelector('.tab[data-tab="settings"]')?.click();
  document.querySelector('#setNav button[data-sec="scope"]')?.click();
}
function authzScopeRuleLine(r){
  const tag=r.action==='exclude'?'exclude':'include';
  const color=tag==='exclude'?'var(--red)':'var(--accent)';
  const host=r.host||'(any host)';
  const extra=[r.path?'path:'+r.path:'',r.scheme?r.scheme:''].filter(Boolean).join(' · ');
  return `<div style="font-family:var(--mono);font-size:11.5px;padding:3px 0"><span style="font-weight:700;color:${color}">${tag}</span> <span style="color:var(--fg)">${esc(host)}</span>${extra?` <span class="hint">${esc(extra)}</span>`:''}</div>`;
}
async function renderAuthzScopePanel(){
  const panel=$('#authzScopePanel');if(!panel)return;
  const scopeMode=$('#authzTargetScope')?.checked;
  panel.style.display=scopeMode?'':'none';
  if(!scopeMode)return;
  try{const d=await api('/api/scope');state.scope=d.rules||[];}catch(e){}
  const enabled=(state.scope||[]).filter(r=>r.enabled);
  const includes=enabled.filter(r=>r.action==='include');
  const excludes=enabled.filter(r=>r.action==='exclude');
  let html='';
  if(!state.scope.length){
    html=`<p class="hint" style="color:var(--amber);margin:0;line-height:1.55"><b>No scope rules.</b> Bulk authz requires include rules in <b>Settings → Target scope</b>.</p>`;
  }else if(!includes.length){
    html=`<p class="hint" style="color:var(--amber);margin:0 0 8px"><b>No include rules.</b> Add at least one before bulk run.</p>`;
    if(excludes.length)html+=excludes.map(authzScopeRuleLine).join('');
  }else{
    html=`<div style="font-size:9px;font-weight:700;letter-spacing:.6px;color:var(--fg3);margin:0 0 6px">IN-SCOPE (from Settings → Target scope)</div>`;
    html+=includes.map(authzScopeRuleLine).join('');
    if(excludes.length)html+=`<div style="font-size:9px;font-weight:700;letter-spacing:.6px;color:var(--fg3);margin:10px 0 4px">EXCLUDE (always wins)</div>`+excludes.map(authzScopeRuleLine).join('');
  }
  html+=`<div class="row" style="gap:8px;margin-top:10px;flex-wrap:wrap;align-items:center"><button class="btn" type="button" id="authzScopeEdit">Settings → Target scope</button><span class="hint" id="authzScopeHosts">checking captured traffic…</span></div>`;
  panel.innerHTML=html;
  $('#authzScopeEdit')?.addEventListener('click',openSettingsScope);
  try{
    const d=await api('/api/flows?limit=500&inScope=1');
    const hosts=[...new Set((d.flows||[]).map(f=>f.host).filter(Boolean))].sort();
    const el=$('#authzScopeHosts');
    if(!el)return;
    if(!hosts.length)el.textContent='No in-scope traffic in history yet — browse the target through the proxy first.';
    else el.textContent=`${hosts.length} host${hosts.length===1?'':'s'} in history: ${hosts.slice(0,10).join(', ')}${hosts.length>10?'…':''} (static assets skipped in bulk run)`;
  }catch(e){const el=$('#authzScopeHosts');if(el)el.textContent='';}
}

async function loadFlowAuthHint(flowId){
  const box=$('#authzCookieHint');if(!box||!flowId){if(box)box.style.display='none';return;}
  try{
    const d=await api('/api/authz/flow-auth/'+flowId);
    const hints=(d.cookieHints||[]);
    if(!hints.length&&!d.requestAuth){box.style.display='none';return;}
    let t='';
    if(d.requestAuth)t+='Captured request auth: <span style="font-family:var(--mono);color:var(--fg2)">'+esc(d.requestAuth.replace(/\n/g,' · '))+'</span>. ';
    if(hints.length)t+='Cookie hints: '+hints.map(h=>esc(h)).join('; ')+'.';
    box.innerHTML=t+' Use <b>⧉ From flow</b> to fill the baseline identity.';
    box.style.display='';
  }catch(e){box.style.display='none';}
}

export function openAuthz(flowId){
  authzFlowId=flowId||state.selId||null;
  openModal($('#authzModal'));
  $('#authzFlow').textContent=authzFlowId?('#'+authzFlowId):'(none — select in History)';
  $('#authzResults').innerHTML='<div class="hint">Define identities, then <b>Run</b>. Use <b>Check sessions</b> first if cookies may be stale.</div>';
  if($('#authzTargetFlow'))$('#authzTargetFlow').checked=true;
  loadAuthzIdentities();
  renderAuthzScopePanel();
  loadFlowAuthHint(authzFlowId);
}

async function loadAuthzIdentities(){
  try{const d=await api('/api/authz');renderIdentities(d.identities||[]);refreshAuthzIds();}catch(e){renderIdentities([]);}
}
function renderIdentities(ids){
  if(!ids.length)ids=[{name:'',headers:''}];
  $('#authzIds').innerHTML=ids.map((id,i)=>`<div class="authz-id${id.broken?' authz-id-broken':''}" data-i="${i}">
    <input class="authz-name btn" style="background:var(--bg3)" placeholder="role e.g. ${i===0?'admin (baseline)':'user'}" value="${escAttr(id.name||'')}">
    <textarea class="authz-hdr rep-edit" rows="2" placeholder="Cookie: session=…  (blank = anonymous)">${esc(id.headers||'')}</textarea>
    <div style="display:flex;gap:4px">
      <button class="btn${id.broken?' danger':''} authz-broken" data-i="${i}" title="${id.broken?'Account marked broken — click to unmark':'Mark account as broken/locked (skipped in runs)'}">${id.broken?'⚠ broken':'⚠'}</button>
      <button class="btn danger authz-del" data-i="${i}" title="remove">✕</button>
    </div>
    ${id.broken&&id.brokenNote?`<div class="hint" style="font-size:11px;margin-top:2px;color:var(--amber)">${esc(id.brokenNote)}</div>`:''}
  </div>`).join('');
  document.querySelectorAll('#authzIds .authz-del').forEach(b=>b.onclick=()=>{
    const ids=collectIds();ids.splice(Number(b.dataset.i),1);
    renderIdentities(ids.length?ids:[{name:'',headers:''}]);
  });
  document.querySelectorAll('#authzIds .authz-broken').forEach(b=>b.onclick=()=>{
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
async function saveIds(){await api('/api/authz',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({identities:collectIds()})});}

async function fillFromFlow(){
  const fid=authzTarget(); syncAuthzLabel();
  if(!fid){toast('select a flow first');return;}
  try{
    const d=await api('/api/authz/flow-auth/'+fid);
    if(!d.requestAuth){toast('no Cookie/Authorization on that request');return;}
    const ids=collectIds();
    let i=ids.findIndex(x=>!x.headers.trim());
    if(i<0){ids.unshift({name:'',headers:''});i=0;}
    ids[i].headers=d.requestAuth;
    if(!ids[i].name)ids[i].name='from flow';
    renderIdentities(ids);
    toast('filled identity from flow #'+fid);
  }catch(e){toast(e.message);}
}

async function checkSessions(){
  const probe=authzTarget(); syncAuthzLabel();
  if(!probe){toast('select a flow to probe sessions (e.g. GET /api/me)');return;}
  if(collectIds().length<1){toast('add at least one identity');return;}
  $('#authzResults').innerHTML='<div class="hint">checking sessions…</div>';
  try{
    await saveIds();
    const d=await api('/api/authz/check-sessions',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({flowId:probe})});
    const checks=d.checks||[];
    $('#authzResults').innerHTML='<div class="authz-row authz-head"><span>identity</span><span>status</span><span>session</span><span></span></div>'
      +checks.map(c=>c.broken?`<div class="authz-row" style="opacity:.55">
        <span>${esc(c.name||'(unnamed)')}</span>
        <span style="color:var(--fg3)">—</span>
        <span><span style="color:var(--amber)">⚠ broken</span></span>
        <span></span></div>`:`<div class="authz-row${c.sessionInvalid?' flag':''}">
        <span>${esc(c.name||'(unnamed)')}</span>
        <span style="color:${statusColor(c.status)};font-weight:700">${c.error?'ERR':(c.status||'—')}</span>
        <span>${!c.hasAuth?'<span class="hint">anonymous</span>':c.sessionInvalid?'<span style="color:var(--red);font-weight:700">expired?</span>':'<span class="hint">ok</span>'}</span>
        <span></span></div>`).join('');
  }catch(e){$('#authzResults').innerHTML='<div class="hint" style="color:var(--red)">'+esc(e.message)+'</div>';}
}

function runBody(){
  const bulk=$('#authzTargetScope')?.checked;
  const fid=authzTarget(); syncAuthzLabel();
  if(!bulk&&!fid){toast('select a flow or choose all in-scope');return null;}
  const body={maxFlows:parseInt($('#authzMax')?.value,10)||0};
  if(bulk)body.inScope=true; else body.flowId=fid;
  return body;
}

function renderAuthzRow(r,i){
  let verdict='';
  if(r.broken)verdict='<span style="color:var(--amber)">⚠ broken — skipped</span>';
  else if(i===0)verdict='<span class="hint">baseline</span>';
  else if(r.sessionInvalid)verdict='<span style="color:var(--amber);font-weight:700">session?</span>';
  else if(r.sameAsBaseline)verdict='<span style="color:var(--red);font-weight:700">⚠ same access</span>';
  else verdict='<span class="hint">differs ✓</span>';
  return `<div class="authz-row${r.sameAsBaseline||r.sessionInvalid?' flag':''}${r.broken?' authz-broken-row':''}"${r.flowId?` data-flow="${r.flowId}"`:''}>
    <span${r.broken?' style="opacity:.6"':''}>${esc(r.name||'(unnamed)')}</span>
    <span style="color:${r.broken?'var(--fg3)':statusColor(r.status)};font-weight:700">${r.broken?'—':(r.error?'ERR':(r.status||'—'))}</span>
    <span>${r.broken?'—':fmtSize(r.length)}</span>
    <span>${verdict}</span></div>`;
}

function renderAuthzListBulk(runs){
  let html='';
  runs.forEach(run=>{
    const flagged=(run.results||[]).some((r,i)=>i>0&&r.sameAsBaseline);
    html+=`<details style="margin-bottom:8px;border:1px solid var(--line);border-radius:8px;padding:6px 10px"${flagged?' open':''}>
      <summary style="cursor:pointer;font-family:var(--mono);font-size:11.5px;color:${flagged?'var(--red)':'var(--fg)'}">
        <span style="color:var(--accent);font-weight:700">${esc(run.method)}</span> ${esc(run.host)}${esc(run.path||'/')}
        ${flagged?' · <b style="color:var(--red)">⚠ access issue</b>':''}
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
  let html=`<div style="overflow-x:auto"><table style="width:100%;border-collapse:collapse;font-size:11.5px">
    <thead><tr>
      <th style="text-align:left;padding:5px 8px;border-bottom:1px solid var(--line);color:var(--fg3)">endpoint</th>
      ${names.map((n,i)=>`<th style="text-align:center;padding:5px 8px;border-bottom:1px solid var(--line);white-space:nowrap">${i===0?`<span style="color:var(--fg3)">${esc(n)}</span>`:esc(n)}</th>`).join('')}
    </tr></thead><tbody>`;
  for(const run of runs){
    const rowFlagged=(run.results||[]).some((r,i)=>i>0&&r.sameAsBaseline);
    html+=`<tr${rowFlagged?' style="background:color-mix(in srgb,var(--red) 7%,transparent)"':''}>
      <td style="padding:5px 8px;font-family:var(--mono);border-bottom:1px solid var(--line2);white-space:nowrap"><span style="color:var(--accent);font-weight:700">${esc(run.method)}</span> <span style="color:var(--fg2);font-size:11px">${esc(run.host)}${esc(run.path||'/')}</span></td>
      ${(run.results||[]).map((r,i)=>{
        const warn=i>0&&r.sameAsBaseline;
        const err=!!r.error||r.status===0;
        return `<td style="text-align:center;padding:5px 8px;border-bottom:1px solid var(--line2)"${r.flowId?` data-flow="${r.flowId}"`:''}>${i===0?`<span class="hint" style="font-size:10px">—</span>`:`<span style="color:${err?'var(--fg3)':statusColor(r.status)};font-weight:700">${err?'ERR':(r.status||'—')}</span><span style="color:var(--fg3);font-size:10px;display:block">${fmtSize(r.length)}</span>${warn?'<span style="color:var(--red);font-size:10px">⚠</span>':''}${r.sessionInvalid?'<span style="color:var(--amber);font-size:10px">sess?</span>':''}`}</td>`;
      }).join('')}
    </tr>`;
  }
  html+='</tbody></table></div>';
  return html;
}

function renderAuthzResults(d){
  const runs=d.runs||[];
  const box=$('#authzResults');
  if(!runs.length){box.innerHTML='<div class="hint">no results</div>';return;}
  const bulk=runs.length>1||$('#authzTargetScope')?.checked;
  if(!bulk){
    const res=runs[0].results||[];
    box.innerHTML='<div class="authz-row authz-head"><span>identity</span><span>status</span><span>length</span><span>verdict</span></div>'
      +res.map((r,i)=>renderAuthzRow(r,i)).join('');
    box.querySelectorAll('[data-flow]').forEach(el=>{const go=()=>{closeModal($('#authzModal'));selectFlow(Number(el.dataset.flow));};el.onclick=go;wireRowKey(el,go);});
    return;
  }
  const sum=d.summary||{};
  const toggleHtml=`<div class="row" style="gap:6px;margin-bottom:8px;align-items:center">
    <span class="hint">${sum.endpoints||runs.length} endpoint${(sum.endpoints||runs.length)===1?'':'s'} · ${sum.flagged||0} flagged</span>
    <div class="spacer"></div>
    <button class="btn${authzViewMode==='list'?' on':''}" id="authzViewList" style="padding:2px 7px;font-size:11px">☰ List</button>
    <button class="btn${authzViewMode==='matrix'?' on':''}" id="authzViewMatrix" style="padding:2px 7px;font-size:11px">⊞ Matrix</button>
  </div>`;
  box.innerHTML=toggleHtml+(authzViewMode==='matrix'?renderAuthzMatrix(runs):renderAuthzListBulk(runs));
  $('#authzViewList')?.addEventListener('click',()=>{authzViewMode='list';renderAuthzResults(d);});
  $('#authzViewMatrix')?.addEventListener('click',()=>{authzViewMode='matrix';renderAuthzResults(d);});
  box.querySelectorAll('[data-flow]').forEach(el=>el.onclick=()=>{closeModal($('#authzModal'));selectFlow(Number(el.dataset.flow));});
}

async function crossHostReplay(){
  const fid=authzTarget();syncAuthzLabel();
  if(!fid){toast('select a reference flow first');return;}
  $('#authzResults').innerHTML='<div class="hint">replaying to all in-scope hosts…</div>';
  try{
    const d=await api('/api/authz/cross-host-replay',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({flowId:fid})});
    renderCrossHostResults(d);
  }catch(e){$('#authzResults').innerHTML='<div class="hint" style="color:var(--red)">'+esc(e.message)+'</div>';}
}

function renderCrossHostResults(d){
  const box=$('#authzResults');
  const results=d.results||[];
  if(!results.length){box.innerHTML='<div class="hint">no in-scope hosts found — browse the target through the proxy first</div>';return;}
  const accepted=results.filter(r=>r.accepted).length;
  let html=`<div class="hint" style="margin-bottom:8px">Cross-host JWT replay · <span style="font-family:var(--mono)">${esc(d.method||'')} ${esc(d.path||'/')}</span> · ${accepted} of ${results.length} host${results.length===1?'':'s'} accepted</div>`;
  html+='<div class="authz-row authz-head"><span>host</span><span>status</span><span>length</span><span>verdict</span></div>';
  results.forEach(r=>{
    const err=!!r.error||r.status===0;
    html+=`<div class="authz-row${r.accepted?' flag':''}"${r.flowId?` data-flow="${r.flowId}"`:''}>
      <span style="font-family:var(--mono);font-size:11.5px">${esc(r.host)}</span>
      <span style="color:${err?'var(--fg3)':statusColor(r.status)};font-weight:700">${err?'ERR':(r.status||'—')}</span>
      <span>${fmtSize(r.length)}</span>
      <span>${r.accepted?'<span style="color:var(--red);font-weight:700">⚠ accepted</span>':'<span class="hint">rejected ✓</span>'}</span>
    </div>`;
  });
  box.innerHTML=html;
  box.querySelectorAll('[data-flow]').forEach(el=>el.onclick=()=>{closeModal($('#authzModal'));selectFlow(Number(el.dataset.flow));});
}

$('#authzAdd')&&($('#authzAdd').onclick=()=>renderIdentities([...collectIds(),{name:'',headers:''}]));
$('#authzCrossHost')&&($('#authzCrossHost').onclick=crossHostReplay);
$('#authzFromFlow')&&($('#authzFromFlow').onclick=fillFromFlow);
$('#authzCheck')&&($('#authzCheck').onclick=checkSessions);
$('#authzSave')&&($('#authzSave').onclick=async()=>{try{await saveIds();toast('identities saved');refreshAuthzIds();}catch(e){toast(e.message);}});
$('#authzClose')&&($('#authzClose').onclick=()=>closeModal($('#authzModal')));
$('#authzTargetFlow')&&($('#authzTargetFlow').onchange=renderAuthzScopePanel);
$('#authzTargetScope')&&($('#authzTargetScope').onchange=renderAuthzScopePanel);
$('#authzRun')&&($('#authzRun').onclick=async()=>{
  const body=runBody();if(!body)return;
  if(collectIds().length<1){toast('add at least one identity');return;}
  $('#authzResults').innerHTML='<div class="hint">replaying…</div>';
  try{
    await saveIds();refreshAuthzIds();
    const d=await api('/api/authz/run',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify(body)});
    renderAuthzResults(d);
  }catch(e){$('#authzResults').innerHTML='<div class="hint" style="color:var(--red)">'+esc(e.message)+'</div>';}
});

export { renderAuthzScopePanel };
