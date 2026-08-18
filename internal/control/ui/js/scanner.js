import { $, esc, escAttr, state, toast, api, apiTry, openModal, closeModal, copyText, fmtTime, renderMD, pickTextFile, normalizeListText, DEC_OPS, wireRowKey, saveFile, uiConfirm, renderLoadError, icon } from './core.js';
import { flowPopup } from './flowmodal.js';
import { openFinding } from './findings.js';

/* ---- out-of-band (OOB) interaction catcher ---- */
export async function loadOob(){
  const d=await apiTry('/api/oob/state',{},{toastOnError:false});
  if(!d)return;
  if(document.activeElement!==$('#oobBase'))$('#oobBase').value=d.baseUrl||'';
  renderOobList(d.interactions||[]);
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
$('#oobGen')&&($('#oobGen').onclick=async()=>{try{const r=await api('/api/oob/new',{method:'POST'});$('#oobUrl').value=r.url||'';copyText(r.url||'','OOB URL generated & copied');}catch(e){toast(e.message);}});
$('#oobCopy')&&($('#oobCopy').onclick=()=>{const u=$('#oobUrl').value;if(u)copyText(u,'OOB URL copied');else toast('generate a URL first');});
$('#oobSaveBase')&&($('#oobSaveBase').onclick=async()=>{try{await api('/api/oob/base',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({baseUrl:$('#oobBase').value.trim()})});toast('OOB base saved');loadOob();}catch(e){toast(e.message);}});
$('#oobClear')&&($('#oobClear').onclick=async()=>{try{await api('/api/oob/interactions',{method:'DELETE'});loadOob();toast('OOB interactions cleared');}catch(e){toast(e.message);}});

function oobTunnelCmd(){return 'cloudflared tunnel --url http://'+(state.controlAddr||'127.0.0.1:9966');}
$('#oobModalTunnelCopy')&&($('#oobModalTunnelCopy').onclick=()=>copyText(oobTunnelCmd(),'Tunnel command copied'));

/* ---- custom checks editor ---- */
let checkMode='code',checkDocsLoaded=false;
let checkSelId=null;
let checkBuiltin=false,checkOverridden=false;
const checkEndpoint='/api/checks';
function checkSetEditorReadonly(on){const el=$('#checkId');if(el)el.readOnly=!!on;}
function checkSetMode(mode){
  checkMode=mode;
  const seg=$('#checkModeSeg');
  if(seg)seg.querySelectorAll('[data-mode]').forEach(b=>{
    const on=b.dataset.mode===mode;
    b.classList.toggle('on',on);
    b.setAttribute('aria-selected',on?'true':'false');
  });
  const panes={code:'#checkPaneCode',docs:'#checkPaneDocs'};
  Object.entries(panes).forEach(([m,sel])=>{const el=$(sel);if(el)el.style.display=m===mode?'':'none';});
  if(mode==='docs')loadCheckDocs();
}
async function loadCheckDocs(){
  if(checkDocsLoaded)return;
  const box=$('#checkDocs');if(!box)return;
  try{
    const d=await api('/api/checks/reference');
    box.innerHTML=renderMD(d.markdown||'');
    checkDocsLoaded=true;
  }catch(e){box.innerHTML='<div class="state-error"><div class="state-error-icon"><svg class="icon" aria-hidden="true" focusable="false"><use href="#i-warning"/></svg></div><p class="state-error-msg">'+esc(e.message)+'</p></div>';}
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
export async function loadChecksList(){
  try{
    const d=await api('/api/checks');const box=$('#checksList');if(!box)return;
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
        ${cb}<div class="checks-body">
        <span class="checks-title" style="color:${titleColor}" title="${escAttr(opts.title)}">${esc(opts.title)}${opts.error?' <svg class="icon" aria-hidden="true" focusable="false"><use href="#i-warning"/></svg>':''}</span>
        <div class="checks-meta">${opts.severity?sevBadge(opts.severity):''}${opts.category?catBadge(opts.category):''}${ov}</div>
        </div></div>`;
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
    markChecksSelected(box);
    box.querySelectorAll('.checks-pick[data-id]').forEach(el=>{
      const id=el.dataset.id;
      const builtin=el.classList.contains('checks-builtin');
      const open=()=>builtin?loadBuiltinCheck(id):loadCheck(id);
      el.onclick=e=>{if(e.target.classList.contains('check-en'))return;open();};
      wireRowKey(el,open);
    });
    // Any checkbox change (built-in or custom) recomputes the disabled set.
    box.querySelectorAll('.check-en').forEach(cb=>cb.onchange=async()=>{
      const disabled=[...box.querySelectorAll('.check-en')].filter(x=>!x.checked).map(x=>x.dataset.id);
      try{await api('/api/checks/disabled',{method:'PUT',headers:{'content-type':'application/json'},body:JSON.stringify({disabled})});
        toast('check '+(cb.checked?'enabled':'disabled'));}catch(e){toast(e.message);}
    });
    checksApplyFilter(); // re-apply an active filter across the freshly rendered rows
  }catch(e){const box=$('#checksList');if(box)box.innerHTML=`<div class="state-error"><div class="state-error-icon"><svg class="icon" aria-hidden="true" focusable="false"><use href="#i-warning"/></svg></div><p class="state-error-msg">Couldn't load checks: ${esc(e.message)}</p></div>`;}
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
    btn.disabled=false;
  }
}
export async function loadBuiltinCheck(id){
  checkBuiltin=true;checkSelId=id;refreshCheckEditorMode();
  try{
    const d=await api(checkEndpoint+'/'+encodeURIComponent(id));
    checkOverridden=!!d.overridden;
    $('#checkId').value=id;checkSetEditorReadonly(true);
    $('#checkSrc').value=d.source||'';
    const note=checkOverridden?'your Starlark override is active':'edit & Save to write ~/.interseptor/checks/'+id+'.star';
    $('#checkOut').innerHTML='<div class="check-status check-status-pending">Built-in <b>'+esc(id)+'</b> — '+note+'</div>';
    updateCheckDeleteLabel();
    markChecksSelected($('#checksList'));
  }catch(e){toast(e.message);}
}
export async function loadCheck(id){
  checkBuiltin=false;checkOverridden=false;checkSelId=id;refreshCheckEditorMode();
  try{const d=await api(checkEndpoint+'/'+encodeURIComponent(id));$('#checkId').value=id;checkSetEditorReadonly(false);
    $('#checkSrc').value=d.source||'';
    $('#checkOut').innerHTML='<div class="check-status check-status-pending">Loaded <b>'+esc(id)+'</b> (passive). Edit on <b>Code</b>, then Save.</div>';
    updateCheckDeleteLabel();
    markChecksSelected($('#checksList'));}catch(e){toast(e.message);}
}
export function checkNew(){
  checkBuiltin=false;checkOverridden=false;checkSelId=null;refreshCheckEditorMode();
  checkSetEditorReadonly(false);
  $('#checkId').value='';
  $('#checkSrc').value = "def check(flow):\n    # inspect flow, return a list of finding(...)\n    return []\n";
  $('#checkOut').innerHTML='<div class="check-status check-status-pending">New passive check — set an id, write Starlark on <b>Code</b>, Test, then Save.</div>';$('#checkId').focus();
  updateCheckDeleteLabel();
  markChecksSelected($('#checksList'));
}
export async function checkTest(){
  const out=$('#checkOut');out.innerHTML='<div class="check-status check-status-pending">running…</div>';
  try{const r=await api(checkEndpoint()+'/test',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({source:$('#checkSrc').value,flowId:state.selId||0})});
    if(r.error){out.innerHTML='<div class="check-status check-status-error"><b>Compile/runtime error</b><pre>'+esc(r.error)+'</pre></div>';return;}
    // Passive checks return {findings:[...]} (testCheck in internal/control/checks.go)
    // — zero, one, or many findings on the tested flow.
    const findings=r.findings||[];
    if(!findings.length){
      const note=r.note||'no finding';
      out.innerHTML=`<div class="check-status check-status-ok"><div class="hint">${esc(note)}</div><div style="color:var(--accent);margin-top:4px">✓ No finding — check compiles &amp; runs.</div></div>`;
      return;
    }
    const note='finding'+(findings.length===1?'':'s')+' on flow #'+(r.flowId||'?');
    out.innerHTML=`<div class="check-status check-status-finding"><div class="hint" style="margin-bottom:6px">${esc(note)}</div>`
      +findings.map(f=>`<div><span class="sev ${escAttr(f.severity)}">${esc(f.severity)}</span> ${esc(f.title)}${f.evidence?' <span class="hint">— '+esc(f.evidence)+'</span>':''}</div>`).join('')
      +`</div>`;
  }catch(e){out.innerHTML='<div class="check-status check-status-error"><b>Request failed</b><pre>'+esc(e.message)+'</pre></div>';}
}
export async function checkSave(){
  const id=$('#checkId').value.trim();if(!id){toast('set a check id first');return;}
  const out=$('#checkOut');
  out.innerHTML='<div class="check-status check-status-pending">saving…</div>';
  try{await api(checkEndpoint()+'/'+encodeURIComponent(id),{method:'PUT',headers:{'content-type':'application/json'},body:JSON.stringify({source:$('#checkSrc').value})});
    checkOverridden=checkBuiltin||checkOverridden;
    out.innerHTML='<div class="check-status check-status-ok">Saved ✓ — runs on the next passive scan'+(checkBuiltin?' (replaces built-in)':'')+'.</div>';
    updateCheckDeleteLabel();
    loadChecksList();}
  catch(e){out.innerHTML='<div class="check-status check-status-error"><b>Save failed</b><pre>'+esc(e.message)+'</pre></div>';}
}
export async function checkDelete(){
  const id=$('#checkId').value.trim();if(!id)return;
  if(checkBuiltin&&!checkOverridden){toast('no override saved — nothing to revert');return;}
  const label=checkBuiltin?'Revert override for built-in check':'Delete check';
  const body=checkBuiltin?`Delete your Starlark override for <b>${esc(id)}</b>? The compiled built-in will run again.`:`Delete passive check <b>${esc(id)}</b>? Its Starlark source will be removed.`;
  if(!await uiConfirm(label,body,checkBuiltin?'Revert':'Delete','btn danger','var(--red)'))return;
  try{
    await api(checkEndpoint+'/'+encodeURIComponent(id),{method:'DELETE'});
    if(checkBuiltin){await loadBuiltinCheck(id);}else{checkNew();}
    loadChecksList();
    toast(checkBuiltin?'reverted to built-in':'deleted '+id);
  }catch(e){toast(e.message);}
}
async function loadPacksPanel(){
  const box=$('#checksPackList'); if(!box) return;
  try{
    const [cat, inst] = await Promise.all([
      api('/api/packs/catalog').catch(()=>({packs:[]})),
      api('/api/packs').catch(()=>({packs:[]})),
    ]);
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
    box.innerHTML=html;
    box.querySelectorAll('[data-pack]').forEach(b=>b.onclick=async()=>{
      b.disabled=true; b.textContent='…';
      try{
        await api('/api/packs/catalog/'+encodeURIComponent(b.dataset.pack)+'/install',{method:'POST'});
        toast('pack installed'); loadChecksList(); loadPacksPanel();
      }catch(e){toast(e.message,'error'); b.disabled=false; b.textContent='Install';}
    });
    box.querySelectorAll('[data-remove]').forEach(b=>b.onclick=async()=>{
      if(!await uiConfirm('Remove pack?','Uninstall <b>'+esc(b.dataset.remove)+'</b> and delete its checks from disk?','Remove','btn danger')) return;
      try{
        await api('/api/packs/'+encodeURIComponent(b.dataset.remove),{method:'DELETE'});
        toast('pack removed'); loadChecksList(); loadPacksPanel();
      }catch(e){toast(e.message,'error');}
    });
  }catch(e){box.textContent=e.message||'could not load packs';}
}
async function installPackFile(file){
  if(!file) return;
  try{
    const buf=await file.arrayBuffer();
    const allow=$('#checksPackAllowUnsigned')&&$('#checksPackAllowUnsigned').checked;
    const q=allow?'?allowUnsigned=1':'';
    await api('/api/packs/install'+q,{method:'POST',headers:{'content-type':'application/gzip'},body:buf});
    toast('pack installed from '+file.name); loadChecksList(); loadPacksPanel();
  }catch(e){toast(e.message||'install failed','error');}
}
export function openChecks(){openModal($('#checksModal'));const s=$('#checksSearch');if(s)s.value='';loadChecksList();loadPacksPanel();updateCheckFlowHint();if(!$('#checkSrc').value)checkNew();checkSetMode('code');}
if($('#checksBtn'))$('#checksBtn').onclick=openChecks;
if($('#checksPackFile'))$('#checksPackFile').onchange=e=>{const f=e.target.files&&e.target.files[0]; if(f) installPackFile(f); e.target.value='';};
if($('#checksClose'))$('#checksClose').onclick=()=>closeModal($('#checksModal'));
if($('#checkNew'))$('#checkNew').onclick=checkNew;
if($('#checkTest'))$('#checkTest').onclick=checkTest;
if($('#checkSave'))$('#checkSave').onclick=checkSave;
if($('#checkDelete'))$('#checkDelete').onclick=checkDelete;
if($('#checkModeSeg'))$('#checkModeSeg').querySelectorAll('[data-mode]').forEach(b=>b.onclick=()=>checkSetMode(b.dataset.mode));
if($('#checksSearch'))$('#checksSearch').oninput=checksApplyFilter;

/* ---- decoder ---- */
export { DEC_OPS };
export function decBuildOps(){const box=$('#decOps');if(!box||box._built)return;box._built=1;
  box.innerHTML=DEC_OPS.map(([op,label])=>`<button class="btn" data-op="${op}">${esc(label)}</button>`).join('');
  box.querySelectorAll('[data-op]').forEach(b=>b.onclick=()=>decApply(b.dataset.op));}
export async function decApply(op){
  const err=$('#decErr');err.textContent='';
  try{const r=await api('/api/decode',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({op,input:$('#decIn').value})});
    if(r.error){err.style.color='var(--red)';err.textContent=r.error;return;}
    $('#decOut').value=r.output;}
  catch(e){err.style.color='var(--red)';err.textContent=e.message;}
}
export function openDecoder(seed){decBuildOps();openModal($('#decModal'));if(seed)$('#decIn').value=seed;$('#decOut').value='';$('#decErr').textContent='';setTimeout(()=>$('#decIn').focus(),0);}
async function decLoadFile(){
  try{
    const got=await pickTextFile();
    if(!got) return;
    $('#decIn').value=normalizeListText(got.text);
    $('#decOut').value='';$('#decErr').textContent='';
    toast('loaded from '+got.name);
  }catch(e){toast(e.message);}
}
if($('#decLoad'))$('#decLoad').onclick=decLoadFile;
if($('#decClose'))$('#decClose').onclick=()=>closeModal($('#decModal'));
if($('#decUp'))$('#decUp').onclick=()=>{$('#decIn').value=$('#decOut').value;$('#decOut').value='';$('#decIn').focus();};
if($('#decCopy'))$('#decCopy').onclick=()=>copyText($('#decOut').value,'output copied');

/* ---- scanner ---- */
export const scanState={sel:null,issues:[]};
export async function loadIssues(){
  const stateEl=$('#scanRescanState');if(stateEl)stateEl.textContent='Loading scanner results…';
  try{const d=await api('/api/scanner/issues');scanState.issues=d.issues||[];renderScan();if(stateEl)stateEl.textContent='';}
  catch(e){renderLoadError(stateEl,'Scanner results',e,loadIssues,scanState.issues.length>0);}
  finally{if(stateEl&&stateEl.textContent==='Loading scanner results…')stateEl.textContent='';}
}
export async function runScan(){
  $('#scanRun').textContent='Scanning…';$('#scanRun').disabled=true;
  const host=($('#scanTarget')||{}).value||'',search=(($('#scanFilter')||{}).value||'').trim();
  const q=new URLSearchParams();if(host)q.set('host',host);if(search)q.set('search',search);
  const stateEl=$('#scanRescanState');if(stateEl)stateEl.textContent='Rescanning selected in-scope traffic…';
  try{const d=await api('/api/scanner/run'+(q.toString()?'?'+q:''),{method:'POST'});scanState.issues=d.issues||[];renderScan();
    if(stateEl)stateEl.textContent='Rescan complete · stale issues reconciled for this scan';
    toast(scanState.issues.length+' issue'+(scanState.issues.length===1?'':'s')+(host?' · '+host:'')+(search?' · "'+search+'"':''));}
  catch(e){renderLoadError(stateEl,'Scanner',e,runScan,scanState.issues.length>0);}
  finally{$('#scanRun').textContent='Run scan ▸';$('#scanRun').disabled=false;}
}
// Populate the scanner's target dropdown from in-scope history only.
export async function loadScanTargets(){
  const sel=$('#scanTarget');if(!sel)return;
  try{const d=await api('/api/scanner/targets');
    if(d.truncated)throw new Error('server returned a truncated host list — retry before choosing a target');
    const hosts=(d.hosts||[]).filter(h=>h&&h.host);
    const cur=sel.value;
    sel.innerHTML='<option value="">All in-scope hosts</option>'+hosts.map(h=>`<option value="${escAttr(h.host)}">${esc(h.host)} (${Number(h.count)||0})</option>`).join('');
    if(hosts.some(h=>h.host===cur))sel.value=cur;
  }catch(e){renderLoadError($('#scanRescanState'),'Scanner targets',e,loadScanTargets,false);}
}
export function prefillScanner(host, pathSearch){
  document.querySelector('.tab[data-tab="scanner"]')?.click();
  loadScanTargets().then(()=>{
    const sel=$('#scanTarget');
    if(sel&&host) sel.value=host;
    const f=$('#scanFilter');
    if(f&&pathSearch) f.value=pathSearch;
  });
  toast('Scanner ready'+(host?' · '+host:''));
}
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
  if(!scanState.issues.length){$('#scanCount').textContent='';list.innerHTML='<div class="state-empty"><div class="state-empty-icon"><svg class="icon" aria-hidden="true" focusable="false"><use href="#i-shield"/></svg></div><div class="state-empty-title">No issues yet</div><p class="state-empty-hint">Capture some traffic, then Run scan.</p></div>';$('#scanDetail').innerHTML='<div class="state-empty"><div class="state-empty-icon"><svg class="icon" aria-hidden="true" focusable="false"><use href="#i-clipboard"/></svg></div><div class="state-empty-title">No issue selected</div><p class="state-empty-hint">Select an issue from the list to view its details.</p></div>';return;}
  const groups=scanState.groups=scanGroups();
  const c={};scanState.issues.forEach(i=>c[i.severity]=(c[i.severity]||0)+1);
  $('#scanCount').textContent=`${groups.length} finding${groups.length===1?'':'s'} · ${scanState.issues.length} target${scanState.issues.length===1?'':'s'} · ${c.High||0}H ${c.Medium||0}M ${c.Low||0}L`;
  if(scanState.sel==null||scanState.sel>=groups.length)scanState.sel=0;
  list.innerHTML=groups.map((g,idx)=>`<div class="scan-item ${idx===scanState.sel?'sel':''}" data-i="${idx}">
    <span class="sev ${escAttr(g.severity)}">${esc(g.severity)}</span>
    <div class="t">${esc(g.title)}</div><div class="tg">${g.items.length} target${g.items.length===1?'':'s'}</div></div>`).join('');
  list.querySelectorAll('.scan-item').forEach(el=>{el.onclick=()=>{scanState.sel=Number(el.dataset.i);renderScan();};wireRowKey(el);});
  renderScanDetail();
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
  const pm=$('#scanPromote'); if(pm) pm.onclick=()=>promoteFinding(g);
}
// promoteFinding turns a passive-scan issue group into a curated Finding (with all
// its PoC flows attached), then opens it — bridging the two views of "vulns" that
// were previously disconnected silos.
async function promoteFinding(g){
  const first=g.items[0]||{};
  const flowIds=g.items.map(i=>i.flowId).filter(Boolean);
  try{
    const f=await api('/api/findings',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({
      title:g.title,severity:g.severity,source:'scanner',
      detail:first.detail||'',evidence:first.evidence||'',fix:first.fix||'',
      flowIds,
    })});
    toast('Promoted to Finding #'+f.id+(flowIds.length?' · '+flowIds.length+' PoC flow'+(flowIds.length===1?'':'s'):''));
    openFinding(f.id);
  }catch(e){toast(e.message);}
}
$('#scanRun').onclick=runScan;
$('#scanClear')&&($('#scanClear').onclick=async()=>{
  const confirmed=await uiConfirm(
    'Clear passive scanner results?',
    'Remove all passive scanner issues? Curated <b>Findings</b> are kept.',
    'Clear results','btn btn-danger'
  );
  if(!confirmed)return;
  try{
    await api('/api/scanner/issues',{method:'DELETE'});
    scanState.issues=[];scanState.sel=null;renderScan();
    $('#scanRescanState').textContent='Scanner results cleared · curated Findings were not changed';
  }catch(e){renderLoadError($('#scanRescanState'),'Clear scanner results',e,()=>$('#scanClear').click(),scanState.issues.length>0);}
});
