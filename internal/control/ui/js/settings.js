import { $, $$, esc, escAttr, state, toast, api, fmtBytes, uiConfirm } from './core.js';
import { loadFlows, loadScope } from './proxy.js';
import { loadRules } from './intercept.js';

/* settings sub-nav */
$$('#setNav button').forEach(b=>b.onclick=()=>{
  $$('#setNav button').forEach(x=>x.classList.toggle('on',x===b));
  $$('.set-sec').forEach(s=>{s.hidden=s.dataset.sec!==b.dataset.sec;});
  // lazy-load retention stats the first time the project section is opened
  if(b.dataset.sec==='project'&&!retentionLoaded){retentionLoaded=true;loadRetention();}
});

/* ---- settings ---- */
export async function loadSettings(){try{const s=await api('/api/settings');state.proxyAddr=s.proxyAddr;$('#proxyAddr').textContent=s.proxyAddr;$('#setAddr').value=s.proxyAddr;
  if($('#setUpstream'))$('#setUpstream').value=s.upstreamProxy||'';
  if($('#setAiProvider'))$('#setAiProvider').value=s.aiProvider||'anthropic';
  if($('#setAiModel'))$('#setAiModel').value=s.aiModel||'';
  if($('#aiKeyState'))$('#aiKeyState').textContent=s.aiHasKey?'Key configured ✓':'No key set.';
  if($('#capScopeToggle'))setCapScope(!!s.captureScopeOnly);
  if($('#suppressTelemetryToggle'))setSuppressTelemetry(s.suppressBrowserTelemetry!==false);
  aiPlaceholders();
  state.intercept.enabled=s.interceptEnabled;}catch(e){}}
export function setCapScope(on){const b=$('#capScopeToggle');if(!b)return;b.classList.toggle('on',on);b.setAttribute('aria-pressed',on?'true':'false');b.textContent=on?'Saving in-scope only':'Saving all traffic';}
$('#capScopeToggle')&&($('#capScopeToggle').onclick=async()=>{
  const on=!$('#capScopeToggle').classList.contains('on');
  try{await api('/api/settings',{method:'PUT',headers:{'content-type':'application/json'},body:JSON.stringify({captureScopeOnly:on})});setCapScope(on);toast(on?'Now saving only in-scope traffic':'Now saving all traffic');}
  catch(e){toast('capture: '+e.message);}
});
export function setSuppressTelemetry(on){const b=$('#suppressTelemetryToggle');if(!b)return;b.classList.toggle('on',on);b.setAttribute('aria-pressed',on?'true':'false');b.textContent=on?'Suppressing browser telemetry':'Allowing browser telemetry';}
$('#suppressTelemetryToggle')&&($('#suppressTelemetryToggle').onclick=async()=>{
  const on=!$('#suppressTelemetryToggle').classList.contains('on');
  try{await api('/api/settings',{method:'PUT',headers:{'content-type':'application/json'},body:JSON.stringify({suppressBrowserTelemetry:on})});setSuppressTelemetry(on);toast(on?'Browser telemetry suppressed':'Browser telemetry now visible in history');}
  catch(e){toast('telemetry: '+e.message);}
});
export function aiPlaceholders(){if(!$('#setAiProvider'))return;
  const p=$('#setAiProvider').value;
  if(p==='openrouter'){$('#setAiKey').placeholder='sk-or-…';$('#setAiModel').placeholder='anthropic/claude-3.5-haiku';}
  else{$('#setAiKey').placeholder='sk-ant-…';$('#setAiModel').placeholder='claude-haiku-4-5-20251001';}}
if($('#setAiProvider'))$('#setAiProvider').onchange=aiPlaceholders;
$('#saveUpstreamBtn').onclick=async()=>{
  try{await api('/api/settings',{method:'PUT',headers:{'content-type':'application/json'},body:JSON.stringify({upstreamProxy:$('#setUpstream').value.trim()})});
    toast('upstream proxy saved');}catch(e){toast(e.message);}
};
$('#saveAiBtn').onclick=async()=>{
  const body={aiProvider:$('#setAiProvider').value,aiModel:$('#setAiModel').value.trim()};
  if($('#setAiKey').value)body.aiApiKey=$('#setAiKey').value;
  try{await api('/api/settings',{method:'PUT',headers:{'content-type':'application/json'},body:JSON.stringify(body)});
    $('#setAiKey').value='';toast('AI settings saved');loadSettings();}catch(e){toast(e.message);}
};
export async function loadSession(){try{const s=await api('/api/session');
  if($('#setSessionOn'))$('#setSessionOn').checked=!!s.enabled;
  if($('#setSessionHeaders'))$('#setSessionHeaders').value=s.headers||'';
  const n=(s.headers||'').split('\n').filter(l=>l.trim()&&!l.trim().startsWith('#')).length;
  if($('#sessionState'))$('#sessionState').textContent=s.enabled?`Applying ${n} header${n===1?'':'s'} to sends`:'Off.';
  const m=s.macro||{};
  if($('#macroOn'))$('#macroOn').checked=!!m.enabled;
  if($('#macroReq'))$('#macroReq').value=m.request||'';
  if($('#macroTarget'))$('#macroTarget').value=m.target||'';
  if($('#macroExtract'))$('#macroExtract').value=m.extract||'';
  if($('#macroMode'))$('#macroMode').value=m.injectMode||'header';
  if($('#macroName'))$('#macroName').value=m.injectName||'';
}catch(e){}}
// Save session headers + the token macro together (the endpoint takes both).
function saveSessionAll(){
  const macro={enabled:$('#macroOn').checked,target:$('#macroTarget').value.trim(),request:$('#macroReq').value,extract:$('#macroExtract').value.trim(),injectMode:$('#macroMode').value,injectName:$('#macroName').value.trim()};
  const body={enabled:$('#setSessionOn').checked,headers:$('#setSessionHeaders').value,macro};
  return api('/api/session',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify(body)});
}
if($('#saveSessionBtn'))$('#saveSessionBtn').onclick=async()=>{try{await saveSessionAll();toast('session saved');loadSession();}catch(e){toast(e.message);}};
if($('#macroSave'))$('#macroSave').onclick=async()=>{try{await saveSessionAll();toast($('#macroOn').checked?'token macro on — fires before each send':'macro saved');loadSession();}catch(e){toast(e.message);}};

/* ---- data retention panel ---- */
export let retentionStats=null; // cached from last fetch
export let retentionLoaded=false; // lazy: load on first show

export async function loadRetention(){
  const body=$('#retentionBody');
  if(body)body.innerHTML='<tr><td colspan="5" class="hint" style="padding:10px 8px">Loading…</td></tr>';
  try{
    const d=await api('/api/hosts/stats');
    retentionStats=d;
    renderRetention(d);
  }catch(e){
    if(body)body.innerHTML='<tr><td colspan="5" class="hint" style="padding:10px 8px;color:var(--red)">'+esc(e.message)+'</td></tr>';
  }
}

export function renderRetention(d){
  const hosts=d.hosts||[];
  const totals=$('#retentionTotals');
  if(totals)totals.innerHTML='<b>'+esc(String(d.totalFlows||0))+' flows</b> · '+fmtBytes(d.totalBytes||0)+' total';
  const body=$('#retentionBody');
  if(!body)return;
  if(!hosts.length){body.innerHTML='<tr><td colspan="5" class="hint" style="padding:10px 8px">No captured flows yet.</td></tr>';return;}
  body.innerHTML=hosts.map(h=>`<tr data-host="${escAttr(h.host)}">
    <td><input type="checkbox" class="ret-chk" data-host="${escAttr(h.host)}" aria-label="Select ${escAttr(h.host)}"></td>
    <td style="font-family:var(--mono);color:var(--fg)">${esc(h.host)}</td>
    <td style="text-align:right;color:var(--fg2)">${esc(String(h.flows))}</td>
    <td style="text-align:right;color:var(--fg2)">${fmtBytes(h.bytes)}</td>
    <td style="text-align:right"><button class="btn danger ret-del-one" data-host="${escAttr(h.host)}" data-flows="${escAttr(String(h.flows))}" style="color:var(--red);padding:3px 8px" title="Delete all flows from ${escAttr(h.host)}">Delete</button></td>
  </tr>`).join('');
  // per-row delete buttons
  body.querySelectorAll('.ret-del-one').forEach(b=>b.onclick=()=>retDeleteOne(b.dataset.host,Number(b.dataset.flows)));
  // select-all checkbox sync
  const sa=$('#retSelectAll');if(sa){sa.checked=false;sa.indeterminate=false;}
}

export function retChecked(){return [].slice.call(document.querySelectorAll('.ret-chk:checked')).map(cb=>cb.dataset.host);}

export async function retDeleteOne(host,flows){
  const msg='Delete all '+flows+' flow'+(flows===1?'':'s')+' from '+host+'? This is permanent.';
  const confirmed=await uiConfirm('Delete flows from '+esc(host),msg,'Delete','btn danger','var(--red)');
  if(!confirmed)return;
  try{
    const r=await api('/api/flows/purge',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({hosts:[host],mode:'delete'})});
    toast('deleted '+r.deleted+' flow'+(r.deleted===1?'':'s')+' · freed '+fmtBytes(r.freedBytes));
    loadRetention();loadFlows();
  }catch(e){toast('purge: '+e.message);}
}

$('#retDeleteSelected').onclick=async()=>{
  const hosts=retChecked();
  if(!hosts.length){toast('select at least one host first');return;}
  // compute total flows for confirmation
  const stats=retentionStats&&retentionStats.hosts||[];
  const totalFlows=hosts.reduce((s,h)=>{const e=stats.find(x=>x.host===h);return s+(e?e.flows:0);},0);
  const msg='Delete all flows from '+hosts.length+' host'+(hosts.length===1?'':'s')+' ('+totalFlows+' flow'+(totalFlows===1?'':'s')+')? This is permanent.';
  const confirmed=await uiConfirm('Delete selected hosts',msg,'Delete','btn danger','var(--red)');
  if(!confirmed)return;
  try{
    const r=await api('/api/flows/purge',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({hosts,mode:'delete'})});
    toast('deleted '+r.deleted+' flow'+(r.deleted===1?'':'s')+' · freed '+fmtBytes(r.freedBytes));
    loadRetention();loadFlows();
  }catch(e){toast('purge: '+e.message);}
};

$('#retKeepOnly').onclick=async()=>{
  const hosts=retChecked();
  if(!hosts.length){toast('select the hosts to keep — none checked');return;}
  const stats=retentionStats&&retentionStats.hosts||[];
  const keepFlows=hosts.reduce((s,h)=>{const e=stats.find(x=>x.host===h);return s+(e?e.flows:0);},0);
  const total=retentionStats?retentionStats.totalFlows:0;
  const delFlows=total-keepFlows;
  const msg='Keep only '+hosts.length+' host'+(hosts.length===1?'':'s')+' and delete the rest (~'+delFlows+' flow'+(delFlows===1?'':'s')+')? This is permanent.';
  const confirmed=await uiConfirm('Keep only selected',msg,'Delete the rest','btn danger','var(--red)');
  if(!confirmed)return;
  try{
    const r=await api('/api/flows/purge',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({hosts,mode:'keepOnly'})});
    toast('deleted '+r.deleted+' flow'+(r.deleted===1?'':'s')+' · freed '+fmtBytes(r.freedBytes));
    loadRetention();loadFlows();
  }catch(e){toast('purge: '+e.message);}
};

$('#retPurgePattern').onclick=async()=>{
  const pat=($('#retPatternInput')||{}).value&&$('#retPatternInput').value.trim();
  if(!pat){toast('enter a host pattern first');return;}
  const confirmed=await uiConfirm('Purge by pattern',
    'Delete all flows matching <b style="color:var(--accent)">'+esc(pat)+'</b>? This is permanent.','Delete','btn danger','var(--red)');
  if(!confirmed)return;
  try{
    const r=await api('/api/flows/purge',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({hosts:[pat],mode:'delete'})});
    toast('deleted '+r.deleted+' flow'+(r.deleted===1?'':'s')+' · freed '+fmtBytes(r.freedBytes));
    if($('#retPatternInput'))$('#retPatternInput').value='';
    loadRetention();loadFlows();
  }catch(e){toast('purge: '+e.message);}
};

$('#retGc').onclick=async()=>{
  const confirmed=await uiConfirm('Reclaim space',
    'Run garbage collection to remove body files no longer referenced by any flow? This is safe but permanent.','Run GC','btn accent','');
  if(!confirmed)return;
  try{
    const r=await api('/api/flows/gc',{method:'POST'});
    toast('GC done · removed '+r.removedFiles+' file'+(r.removedFiles===1?'':'s')+' · freed '+fmtBytes(r.freedBytes));
    loadRetention();
  }catch(e){toast('gc: '+e.message);}
};

// select-all checkbox for retention table
$('#retSelectAll')&&($('#retSelectAll').onclick=function(){
  const boxes=document.querySelectorAll('.ret-chk');
  boxes.forEach(cb=>cb.checked=this.checked);
});

$('#exportProject').onclick=()=>toast('Project exported — .json downloaded');
const dlCa=$('#dlCaBtn');if(dlCa)dlCa.onclick=()=>toast('CA certificate downloaded — trust it on the client');
$('#importProjectBtn').onclick=()=>$('#importProjectFile').click();
$('#importProjectFile').onchange=async e=>{
  const f=e.target.files[0];if(!f)return;
  try{const text=await f.text();const r=await api('/api/import/project',{method:'POST',headers:{'content-type':'application/json'},body:text});
    toast(`imported ${r.importedFlows} flows · ${r.importedRules} rules · ${r.importedScope} scope`);
    loadFlows();loadRules();loadScope();loadSettings();}catch(err){toast('import: '+err.message);}
  e.target.value='';
};
// ---- project switching (close current, open another / a new path) ----
export async function loadProject(){
  try{const d=await api('/api/project');
    const n=$('#projNameHint');if(n)n.textContent=d.current||'default';
    const dir=$('#projDirHint');if(dir&&d.dir)dir.textContent=d.dir;
    const sel=$('#projSelect');if(sel)sel.innerHTML=(d.projects||['default']).map(p=>`<option value="${escAttr(p)}"${p===d.current?' disabled':''}>${esc(p)}${p===d.current?' (current)':''}</option>`).join('');
    if(!d.canSwitch)['projSwitchBtn','projNewBtn'].forEach(id=>{const b=$('#'+id);if(b){b.disabled=true;b.title='project switching is unavailable in this build';}});
  }catch(e){}
}
export async function doSwitchProject(target){
  if(!target)return;
  const note=$('#projSwitchNote');if(note){note.style.display='block';note.textContent='Switching to "'+target+'" — restarting & reconnecting…';}
  try{await api('/api/project/switch',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({target})});}catch(e){}
  let tries=0;const poll=setInterval(async()=>{tries++;
    try{await api('/api/version');clearInterval(poll);location.reload();}
    catch(e){if(tries>60){clearInterval(poll);if(note)note.textContent='Still restarting… reload the page manually if it doesn’t return.';}}
  },500);
}
$('#projSwitchBtn').onclick=()=>{const v=($('#projSelect')||{}).value;if(v)doSwitchProject(v);else toast('no other project to open');};
$('#projNewBtn').onclick=()=>{const v=(($('#projNew')||{}).value||'').trim();if(v)doSwitchProject(v);else toast('enter a project name or path');};
$('#saveAddrBtn').onclick=async()=>{
  try{const s=await api('/api/settings',{method:'PUT',headers:{'content-type':'application/json'},body:JSON.stringify({proxyAddr:$('#setAddr').value})});
    $('#proxyAddr').textContent=s.proxyAddr;toast('proxy now on '+s.proxyAddr);}catch(e){toast(e.message);}
};
export async function loadSysProxy(){
  try{const s=await api('/api/sysproxy');const b=$('#sysProxyToggle');
    if(!s.supported){b.disabled=true;b.textContent='Auto-config: macOS only';$('#sysProxyHint').textContent='Set your OS/browser proxy to '+s.proxy+' manually.';return;}
    b.classList.toggle('on',s.enabled);b.setAttribute('aria-pressed',s.enabled?'true':'false');b.textContent=s.enabled?'System proxy is on':'System proxy is off';
    $('#sysProxyHint').textContent=s.enabled?'Traffic routes through '+s.proxy:'';
  }catch(e){}
}
$('#sysProxyToggle').onclick=async()=>{
  const on=$('#sysProxyToggle').classList.contains('on');$('#sysProxyToggle').disabled=true;
  try{const s=await api('/api/sysproxy',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({enabled:!on})});
    $('#sysProxyToggle').classList.toggle('on',s.enabled);$('#sysProxyToggle').setAttribute('aria-pressed',s.enabled?'true':'false');$('#sysProxyToggle').textContent=s.enabled?'System proxy is on':'System proxy is off';
    $('#sysProxyHint').textContent=s.enabled?'Traffic routes through '+s.proxy:'';toast(s.enabled?'system proxy enabled':'system proxy disabled');}
  catch(e){toast(e.message);}
  $('#sysProxyToggle').disabled=false;
};
