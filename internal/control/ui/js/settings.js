import { $, $$, esc, escAttr, state, toast, api, fmtBytes, uiConfirm, openModal, closeModal, copyText, setSeg, syncUiSelectStyles } from './core.js';
import { loadFlows, loadScope, syncSourceFilters } from './proxy.js';
import { loadRules } from './intercept.js';

/* ---- JWT expiry countdown ---- */
let sessExpTimer = null;
let sessExpValue = null; // cached Unix exp timestamp from active session JWT

function jwtExpFromHeaders(headersText) {
  for (const line of (headersText || '').split('\n')) {
    const m = line.match(/^Authorization\s*:\s*Bearer\s+([A-Za-z0-9+/=_-]+)\.([A-Za-z0-9+/=_-]+)\./i);
    if (!m) continue;
    try {
      const raw = m[2].replace(/-/g, '+').replace(/_/g, '/');
      const pad = raw.length % 4 ? raw + '='.repeat(4 - raw.length % 4) : raw;
      const payload = JSON.parse(atob(pad));
      if (payload && payload.exp) return Number(payload.exp);
    } catch(e) {}
  }
  return null;
}

function renderSessionExpiry(exp, enabled) {
  const el = $('#sessionExpiry');
  if (!el) return;
  if (!enabled || !exp) { el.style.display = 'none'; return; }
  const secsLeft = exp - Math.floor(Date.now() / 1000);
  let text, color;
  if (secsLeft <= 0) {
    text = 'Token EXPIRED';
    color = 'var(--red)';
  } else if (secsLeft < 300) {
    const m = Math.floor(secsLeft / 60), s = secsLeft % 60;
    text = `Expires in ${m > 0 ? m + 'm ' : ''}${s}s`;
    color = 'var(--red)';
  } else if (secsLeft < 1800) {
    const m = Math.floor(secsLeft / 60), s = secsLeft % 60;
    text = `Expires in ${m}m ${s}s`;
    color = 'var(--amber)';
  } else {
    const totalM = Math.floor(secsLeft / 60), h = Math.floor(totalM / 60);
    text = h > 0 ? `Expires in ${h}h ${totalM % 60}m` : `Expires in ${totalM}m`;
    color = 'var(--fg3)';
  }
  el.textContent = text;
  el.style.color = color;
  el.style.display = '';
}

/* ---- per-host session header rows ---- */
function renderHostHdrList(hostHeaders) {
  const list = $('#hostHdrList');
  if (!list) return;
  list.innerHTML = '';
  if (!hostHeaders || !Object.keys(hostHeaders).length) return;
  for (const [host, hdrs] of Object.entries(hostHeaders)) {
    list.appendChild(makeHostHdrRow(host, hdrs));
  }
}

function makeHostHdrRow(host, hdrs) {
  const row = document.createElement('div');
  row.className = 'host-hdr-row';
  row.style.cssText = 'display:flex;gap:6px;margin-bottom:6px;align-items:flex-start';
  row.innerHTML = `<input class="btn host-hdr-host" style="background:var(--bg3);font-family:var(--mono);font-size:11px;width:200px;flex-shrink:0" placeholder="hostname.example.com" spellcheck="false" value="${escAttr(host||'')}">` +
    `<textarea class="host-hdr-headers" rows="2" style="flex:1;font-family:var(--mono);font-size:11px;resize:vertical;background:var(--bg3);border:1px solid var(--line);border-radius:4px;padding:4px 6px;min-width:0" placeholder="Authorization: Bearer eyJ…&#10;Cookie: session=…">${esc(hdrs||'')}</textarea>` +
    `<button class="btn host-hdr-del" style="flex-shrink:0;align-self:flex-start;padding:3px 8px;color:var(--red)" title="Remove this host override">×</button>`;
  row.querySelector('.host-hdr-del').onclick = () => row.remove();
  return row;
}

function collectHostHeaders() {
  const out = {};
  document.querySelectorAll('.host-hdr-row').forEach(row => {
    const host = (row.querySelector('.host-hdr-host').value || '').trim().toLowerCase();
    const hdrs = (row.querySelector('.host-hdr-headers').value || '').trim();
    if (host) out[host] = hdrs;
  });
  return out;
}

if ($('#addHostHdrBtn')) $('#addHostHdrBtn').onclick = () => {
  const row = makeHostHdrRow('', '');
  $('#hostHdrList').appendChild(row);
  row.querySelector('.host-hdr-host').focus();
};

/* ---- network hosts / proxy listeners ---- */
let networkHosts = null;

function parseListenAddr(addr){
  addr=String(addr||'').trim();
  if(!addr)return{host:'127.0.0.1',port:'8080'};
  if(addr.startsWith('[')){
    const m=addr.match(/^\[([^\]]+)\]:(\d+)$/);
    if(m)return{host:m[1],port:m[2]};
  }
  const i=addr.lastIndexOf(':');
  if(i<0)return{host:addr,port:'8080'};
  return{host:addr.slice(0,i),port:addr.slice(i+1)};
}

function joinListenAddr(host,port){
  host=String(host||'').trim();
  port=String(port||'').trim();
  if(!host||!port)return'';
  if(host.includes(':')&&!host.startsWith('['))return'['+host+']:'+port;
  return host+':'+port;
}

function hostSelectOptions(selectedHost){
  const hosts=networkHosts?.hosts||[
    {address:'127.0.0.1',label:'Loopback (localhost only)'},
    {address:'0.0.0.0',label:'All IPv4 interfaces'},
    {address:'::1',label:'IPv6 loopback'},
  ];
  const sel=String(selectedHost||'');
  const has=hosts.some(h=>h.address===sel);
  let html=hosts.map(h=>`<option value="${escAttr(h.address)}"${h.address===sel?' selected':''}>${esc(h.address)} — ${esc(h.label)}${h.suggested?' ★':''}</option>`).join('');
  if(sel&&!has)html+=`<option value="${escAttr(sel)}" selected>${esc(sel)} (custom)</option>`;
  return html;
}

function renderHostSelect(sel,selectedHost){
  if(!sel)return;
  sel.innerHTML=hostSelectOptions(selectedHost);
  syncUiSelectStyles(sel);
}

async function loadNetworkHosts(){
  try{networkHosts=await api('/api/network/hosts');}catch(e){networkHosts=null;}
  const hint=$('#proxySuggestedHint');if(hint)hint.remove();
  renderHostSelect($('#setControlHost'),parseListenAddr(state.controlAddr).host);
}

function makeProxyListenerRow(addr){
  const{host,port}=parseListenAddr(addr);
  const row=document.createElement('div');
  row.className='proxy-listener-row row';
  row.style.cssText='gap:8px;align-items:flex-end;margin-bottom:8px;flex-wrap:wrap';
  row.innerHTML=`<div style="flex:1;min-width:180px"><label class="hint">Host</label><select class="btn proxy-host-select" style="width:100%;text-align:left"></select></div>`+
    `<div style="width:100px"><label class="hint">Port</label><input class="proxy-port-input" inputmode="numeric" value="${escAttr(port)}" style="width:100%"></div>`+
    `<button type="button" class="btn proxy-listener-del" title="Remove listener" style="color:var(--red);padding:3px 10px">×</button>`;
  renderHostSelect(row.querySelector('.proxy-host-select'),host);
  row.querySelector('.proxy-listener-del').onclick=()=>{
    const list=$('#proxyListenersList');
    if(list&&list.querySelectorAll('.proxy-listener-row').length>1)row.remove();
    else toast('at least one proxy listener required');
  };
  return row;
}

function renderProxyListeners(addrs){
  const list=$('#proxyListenersList');
  if(!list)return;
  list.innerHTML='';
  const items=(addrs&&addrs.length)?addrs:['127.0.0.1:8080'];
  items.forEach(a=>list.appendChild(makeProxyListenerRow(a)));
}

function collectProxyAddrs(){
  return [...document.querySelectorAll('.proxy-listener-row')].map(row=>{
    const host=row.querySelector('.proxy-host-select')?.value;
    const port=row.querySelector('.proxy-port-input')?.value?.trim();
    return joinListenAddr(host,port);
  }).filter(Boolean);
}

function syncControlAddrFields(){
  const host=$('#setControlHost')?.value;
  const port=$('#setControlPort')?.value?.trim();
  const addr=joinListenAddr(host,port);
  if($('#setControlAddr'))$('#setControlAddr').value=addr;
  return addr;
}

if($('#addProxyListenerBtn'))$('#addProxyListenerBtn').onclick=()=>{
  const list=$('#proxyListenersList');
  if(!list)return;
  const suggested=networkHosts?.suggested||'127.0.0.1';
  const port=parseListenAddr(state.proxyAddr).port||'8080';
  list.appendChild(makeProxyListenerRow(joinListenAddr(suggested,port)));
};

function renderDeviceProxyUI(ep){
  const mode=ep?.mode||state.deviceProxyMode||'auto';
  const endpoint=ep?.endpoint||state.deviceProxy||'';
  state.deviceProxy=endpoint;
  state.deviceProxyMode=mode;
  const chip=$('#deviceProxyAddr');
  if(chip){
    const label=mode==='manual'?'manual':'auto';
    chip.textContent=endpoint?endpoint+' ('+label+')':'…';
  }
  // The header device-proxy chip only earns its slot when it advertises a
  // phone-reachable LAN address. On loopback it's just a second copy of the
  // listener addr sitting beside it (127.0.0.1:8080  127.0.0.1:8080) — pure
  // noise — so hide it there. It reappears the moment a real LAN endpoint exists.
  const headerChip=$('#deviceProxyChip');
  if(headerChip){
    const host=(endpoint||'').replace(/:\d+$/,'').replace(/^\[|\]$/g,'');
    const loopback=!endpoint||host==='127.0.0.1'||host==='::1'||host==='localhost'||host.startsWith('127.');
    headerChip.style.display=loopback?'none':'';
  }
  const resolved=$('#deviceProxyResolved');
  if(resolved){
    const src=ep?.source?(' · '+ep.source.replace(/_/g,' ')):'';
    resolved.innerHTML=endpoint?`Resolved: <b style="font-family:var(--mono);color:var(--accent)">${esc(endpoint)}</b>${src}`:'';
  }
  const seg=$('#deviceProxyModeSeg');
  if(seg)seg.querySelectorAll('button').forEach(b=>b.classList.toggle('on',b.dataset.mode===mode));
  const manual=$('#deviceProxyManualField');
  if(manual)manual.style.display=mode==='manual'?'':'none';
  if($('#deviceProxyManualHost')&&ep?.manualHost!=null)$('#deviceProxyManualHost').value=ep.manualHost;
}

async function loadDeviceProxyEndpoint(){
  try{
    const ep=await api('/api/proxy/device-endpoint');
    renderDeviceProxyUI(ep);
  }catch(e){/* non-fatal */}
}

async function saveDeviceProxyEndpoint(){
  const mode=$('#deviceProxyModeSeg')?.querySelector('.on')?.dataset.mode||'auto';
  const host=($('#deviceProxyManualHost')?.value||'').trim();
  try{
    const ep=await api('/api/proxy/device-endpoint',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({mode,host})});
    renderDeviceProxyUI(ep);
    toast('device proxy → '+ep.endpoint+(ep.mode==='auto'?' (auto)':' (manual)'));
  }catch(e){toast(e.message);}
}

if($('#deviceProxyModeSeg'))$('#deviceProxyModeSeg').querySelectorAll('button').forEach(b=>{
  b.onclick=()=>{
    $('#deviceProxyModeSeg').querySelectorAll('button').forEach(x=>x.classList.toggle('on',x===b));
    const manual=$('#deviceProxyManualField');
    if(manual)manual.style.display=b.dataset.mode==='manual'?'':'none';
  };
});
if($('#saveDeviceProxyBtn'))$('#saveDeviceProxyBtn').onclick=saveDeviceProxyEndpoint;
if($('#deviceProxyChip'))$('#deviceProxyChip').onclick=()=>openSettingsProxy();

export { loadDeviceProxyEndpoint };

/* settings sub-nav */
export function openSettingsSection(sec){
  const tab=document.querySelector('.tab[data-tab="settings"]');
  if(tab&&!tab.classList.contains('active'))tab.click();
  document.querySelector('#setNav button[data-sec="'+sec+'"]')?.click();
}

export function openSettingsProxy(){
  openSettingsSection('proxy');
  const row=$('#proxyListenersList .proxy-listener-row');
  if(row)setTimeout(()=>{row.scrollIntoView({block:'nearest',behavior:'smooth'});row.querySelector('.proxy-host-select')?.focus();},50);
}

$$('#setNav button').forEach(b=>b.onclick=()=>{
  $$('#setNav button').forEach(x=>{x.classList.toggle('on',x===b);x.setAttribute('aria-pressed',x===b?'true':'false');});
  $$('.set-sec').forEach(s=>{s.hidden=s.dataset.sec!==b.dataset.sec;});
  try{localStorage.setItem('setSec',b.dataset.sec);}catch(e){}
  // lazy-load retention stats the first time the project section is opened
  if(b.dataset.sec==='project'&&!retentionLoaded){retentionLoaded=true;loadRetention();}
  if(b.dataset.sec==='tls'){import('./tlsdiag.js').then(m=>m.loadTrafficDiagnosis());loadIOS();loadIOSSsh();}
  if(b.dataset.sec==='api'&&!apiLoaded){apiLoaded=true;import('./apipanel.js').then(m=>{m.loadApiKeys();m.loadReference();m.loadMCP();});}
});

/* ---- settings ---- */
let savedAiModel='';
let apiLoaded=false;

export async function loadSettings(){try{const s=await api('/api/settings');state.proxyAddr=s.proxyAddr;state.deviceProxy=s.deviceProxy||s.proxyAddr;state.deviceProxyMode=s.deviceProxyMode||'auto';state.controlAddr=s.controlAddr||'127.0.0.1:9966';
  await loadNetworkHosts();
  renderProxyListeners(s.proxyAddrs||[s.proxyAddr]);
  if($('#setAddr'))$('#setAddr').value=s.proxyAddr;
  $('#proxyAddr').textContent=s.proxyAddr;
  await loadDeviceProxyEndpoint();
  $('#controlAddr').textContent=state.controlAddr;
  const c=parseListenAddr(state.controlAddr);
  if($('#setControlPort'))$('#setControlPort').value=c.port;
  renderHostSelect($('#setControlHost'),c.host);
  if($('#setControlAddr'))$('#setControlAddr').value=state.controlAddr;
  const tun=$('#oobModalTunnelCmd');if(tun)tun.textContent='cloudflared tunnel --url http://'+state.controlAddr;
  if($('#setUpstream'))$('#setUpstream').value=s.upstreamProxy||'';
  state.aiDisabled=!!s.aiDisabled;
  state.oobEnabled=!!s.oobEnabled;
  if($('#setAiDisabled'))$('#setAiDisabled').checked=state.aiDisabled;
  if($('#setOobEnabled'))$('#setOobEnabled').checked=state.oobEnabled;
  if($('#setAiProvider'))$('#setAiProvider').value=s.aiProvider||'anthropic';
  savedAiModel=s.aiModel||'';
  if($('#setAiModel'))$('#setAiModel').value=savedAiModel;
  if($('#aiKeyState'))$('#aiKeyState').textContent=s.aiHasKey?'Key configured ✓':'No key set.';
  if($('#capScopeToggle'))setCapScope(!!s.captureScopeOnly);
  if($('#suppressTelemetryToggle'))setSuppressTelemetry(s.suppressBrowserTelemetry!==false);
  if($('#invisibleProxyToggle'))setInvisibleProxy(!!s.invisibleProxy);
  aiSyncProviderUI();
  applyAiDisabledUI();
  applyOobDisabledUI();
  state.intercept.enabled=s.interceptEnabled;}catch(e){}}

export function applyOobDisabledUI(){
  const on=!!state.oobEnabled;
  document.documentElement.classList.toggle('oob-disabled',!on);
  const hint=$('#oobDisabledHint');
  if(hint)hint.style.display=on?'none':'block';
  if(!on&&$('#oobModal')&&$('#oobModal').style.display==='flex')closeModal($('#oobModal'));
}

$('#setOobEnabled')&&($('#setOobEnabled').onchange=async()=>{
  const enabled=$('#setOobEnabled').checked;
  try{
    await api('/api/settings',{method:'PUT',headers:{'content-type':'application/json'},body:JSON.stringify({oobEnabled:enabled})});
    state.oobEnabled=enabled;
    applyOobDisabledUI();
    toast(enabled?'OOB catcher enabled':'OOB catcher disabled');
  }catch(e){toast(e.message);loadSettings();}
});

export function applyAiDisabledUI(){
  const off=!!state.aiDisabled;
  document.documentElement.classList.toggle('ai-disabled',off);
  const fields=$('#aiSettingsFields'),hint=$('#aiDisabledHint');
  if(fields)fields.style.display=off?'none':'';
  if(hint)hint.style.display=off?'block':'none';
  if(off){
    const act=document.querySelector('.panel[data-panel="activity"]');
    if(act&&act.classList.contains('active'))document.querySelector('.tab[data-tab="proxy"]')?.click();
    const mcpBtn=document.querySelector('#apiSub button[data-s="mcp"]');
    if(mcpBtn&&mcpBtn.classList.contains('on'))document.querySelector('#apiSub button[data-s="keys"]')?.click();
    state.actUnseen=0;
    const b=$('#actBadge');if(b)b.style.display='none';
    if(state.showAI){state.showAI=false;syncSourceFilters();}
  }
}

$('#setAiDisabled')&&($('#setAiDisabled').onchange=async()=>{
  const disabled=$('#setAiDisabled').checked;
  try{
    await api('/api/settings',{method:'PUT',headers:{'content-type':'application/json'},body:JSON.stringify({aiDisabled:disabled})});
    state.aiDisabled=disabled;
    applyAiDisabledUI();
    if(disabled){toast('AI features disabled');loadFlows();}
    else toast('AI features enabled');
  }catch(e){toast(e.message);$('#setAiDisabled').checked=!disabled;}
});

function aiProviderVal(){return ($('#setAiProvider')||{}).value;}
function aiIsOpenRouter(){return aiProviderVal()==='openrouter';}
function aiIsGLM(){return aiProviderVal()==='glm';}
// Providers whose model is chosen from a dropdown (vs. a free-text field).
function aiUsesModelList(){return aiIsOpenRouter()||aiIsGLM();}

// GLM Coding Plan (z.ai) model lineup — curated, newest/flagship first. z.ai's
// Anthropic-compatible endpoint takes these lowercase ids in the `model` field.
// There is no "glm-5-flash"; the GLM-5 speed model is glm-5-turbo (the default),
// and the free flash tier is glm-4.7-flash / glm-4.5-flash.
const GLM_DEFAULT_MODEL='glm-5-turbo';
const GLM_MODELS=[
  {id:'glm-5-turbo',name:'GLM-5-Turbo — GLM-5 speed (default)'},
  {id:'glm-5.2',name:'GLM-5.2 — flagship · 1M context'},
  {id:'glm-5.1',name:'GLM-5.1 — 200K context'},
  {id:'glm-5',name:'GLM-5'},
  {id:'glm-4.7-flash',name:'GLM-4.7-Flash — fast · free'},
  {id:'glm-4.5-flash',name:'GLM-4.5-Flash — free'},
  {id:'glm-4.7',name:'GLM-4.7'},
  {id:'glm-4.6',name:'GLM-4.6 — 200K context'},
  {id:'glm-4.5',name:'GLM-4.5'},
  {id:'glm-4.5-air',name:'GLM-4.5-Air — low cost'},
  {id:'glm-4-air',name:'GLM-4-Air — ultra-low cost'},
];
function populateGlmModels(){
  const sel=$('#setAiModelSelect');if(!sel)return;
  const opts=GLM_MODELS.slice();
  // Keep an unknown-but-saved GLM id selectable so a saved config is never lost.
  if(savedAiModel&&savedAiModel.startsWith('glm-')&&!opts.some(m=>m.id===savedAiModel))
    opts.unshift({id:savedAiModel,name:savedAiModel+' (saved)'});
  sel.innerHTML=opts.map(m=>`<option value="${escAttr(m.id)}">${esc(m.name)}</option>`).join('');
  const want=(savedAiModel&&opts.some(m=>m.id===savedAiModel))?savedAiModel:GLM_DEFAULT_MODEL;
  sel.value=want;
  syncUiSelectStyles(sel);
  const stateEl=$('#aiValidateState');if(stateEl)stateEl.textContent='';
}

export function aiSyncProviderUI(){
  if(!$('#setAiProvider'))return;
  const or=aiIsOpenRouter(),glm=aiIsGLM(),usesList=or||glm;
  const inp=$('#setAiModel'),sel=$('#setAiModelSelect'),loadBtn=$('#loadAiModelsBtn'),hint=$('#setAiModelHint');
  if(inp)inp.style.display=usesList?'none':'';
  if(sel){sel.style.display=usesList?'':'none';syncUiSelectStyles(sel);}
  if(loadBtn)loadBtn.style.display=or?'':'none'; // only OpenRouter fetches its list
  if(hint)hint.textContent=usesList?'(pick from list)':'(optional)';
  // Agent mode (let-AI-send-requests) needs the Anthropic tool-use format, which
  // OpenRouter's chat API doesn't expose — disable it there so it isn't a silent
  // no-op. GLM speaks the Anthropic format, so agent mode stays available.
  const agent=$('#aiAgentToggle');
  if(agent){
    const supported=!or;
    if(!supported&&agent.checked)agent.checked=false;
    agent.disabled=!supported;
  }
  aiPlaceholders();
  if(or)loadOpenRouterModels(false);
  else if(glm)populateGlmModels();
}

export async function loadOpenRouterModels(force){
  if(!aiIsOpenRouter())return;
  const sel=$('#setAiModelSelect'),stateEl=$('#aiValidateState');
  if(!sel)return;
  const key=($('#setAiKey')||{}).value.trim();
  if(!key&&!force&&sel.options.length>1)return;
  sel.disabled=true;
  if(stateEl)stateEl.textContent='Loading models…';
  try{
    const q=key?'?key='+encodeURIComponent(key):'';
    const d=await api('/api/ai/openrouter/models'+q);
    const cur=sel.value||savedAiModel;
    sel.innerHTML='<option value="">— select a model —</option>'+
      (d.models||[]).map(m=>`<option value="${escAttr(m.id)}">${esc(m.name||m.id)}</option>`).join('');
    if(cur&&[...sel.options].some(o=>o.value===cur))sel.value=cur;
    else if(savedAiModel&&[...sel.options].some(o=>o.value===savedAiModel))sel.value=savedAiModel;
    if(stateEl){
      if(d.keyError)stateEl.textContent=d.keyError;
      else if(d.keyValid)stateEl.textContent='Key valid ✓';
      else stateEl.textContent=key?'':'Enter API key, then load models';
    }
  }catch(e){
    if(stateEl)stateEl.textContent='';
    toast('models: '+e.message);
  }finally{sel.disabled=false;}
}

export function aiPlaceholders(){if(!$('#setAiProvider'))return;
  const p=$('#setAiProvider').value;
  if(p==='openrouter'){$('#setAiKey').placeholder='sk-or-…';}
  else if(p==='glm'){$('#setAiKey').placeholder='your GLM Coding Plan key';$('#setAiModel').placeholder='glm-4.6';}
  else{$('#setAiKey').placeholder='sk-ant-…';$('#setAiModel').placeholder='claude-haiku-4-5-20251001';}}
if($('#setAiProvider'))$('#setAiProvider').onchange=aiSyncProviderUI;
if($('#loadAiModelsBtn'))$('#loadAiModelsBtn').onclick=()=>loadOpenRouterModels(true);
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
export function setInvisibleProxy(on){const b=$('#invisibleProxyToggle');if(!b)return;b.classList.toggle('on',on);b.setAttribute('aria-pressed',on?'true':'false');b.textContent=on?'Invisible proxy is on':'Invisible proxy is off';}
$('#invisibleProxyToggle')&&($('#invisibleProxyToggle').onclick=async()=>{
  const on=!$('#invisibleProxyToggle').classList.contains('on');
  try{await api('/api/settings',{method:'PUT',headers:{'content-type':'application/json'},body:JSON.stringify({invisibleProxy:on})});setInvisibleProxy(on);toast(on?'Invisible proxy enabled':'Invisible proxy disabled');}
  catch(e){toast('invisible: '+e.message);}
});
$('#saveUpstreamBtn').onclick=async()=>{
  try{await api('/api/settings',{method:'PUT',headers:{'content-type':'application/json'},body:JSON.stringify({upstreamProxy:$('#setUpstream').value.trim()})});
    toast('upstream proxy saved');}catch(e){toast(e.message);}
};
$('#saveAiBtn').onclick=async()=>{
  const provider=$('#setAiProvider').value;
  const body={aiProvider:provider};
  if($('#setAiKey').value)body.aiApiKey=$('#setAiKey').value;
  if(provider==='openrouter'||provider==='glm'){
    const model=($('#setAiModelSelect')||{}).value;
    if(!model){toast('Select a '+(provider==='glm'?'GLM':'OpenRouter')+' model from the list');return;}
    body.aiModel=model;
  }else{
    const model=$('#setAiModel').value.trim();
    if(model)body.aiModel=model;
  }
  try{
    await api('/api/settings',{method:'PUT',headers:{'content-type':'application/json'},body:JSON.stringify(body)});
    $('#setAiKey').value='';toast('AI settings saved');loadSettings();
  }catch(e){toast(e.message);}
};
export async function loadSession(){try{const s=await api('/api/session');
  if($('#setSessionOn'))$('#setSessionOn').checked=!!s.enabled;
  if($('#setSessionUnscoped'))$('#setSessionUnscoped').checked=!!s.unscoped;
  if($('#setSessionHeaders'))$('#setSessionHeaders').value=s.headers||'';
  const n=(s.headers||'').split('\n').filter(l=>l.trim()&&!l.trim().startsWith('#')).length;
  const hh=s.hostHeaders||{};
  const nhh=Object.keys(hh).length;
  if($('#sessionState')){
    if(!s.enabled) $('#sessionState').textContent='Off.';
    else if(s.unscoped) $('#sessionState').textContent=`Applying ${n} header${n===1?'':'s'} to all hosts (unsafe)${nhh?` · ${nhh} host override${nhh===1?'':'s'}`:''}`;
    else $('#sessionState').textContent=`Applying ${n} header${n===1?'':'s'} to in-scope hosts${nhh?` · ${nhh} host override${nhh===1?'':'s'}`:''}`;
  }
  renderHostHdrList(hh);
  sessExpValue = jwtExpFromHeaders(s.headers);
  renderSessionExpiry(sessExpValue, !!s.enabled);
  if (sessExpTimer) clearInterval(sessExpTimer);
  if (sessExpValue && s.enabled) {
    sessExpTimer = setInterval(() => renderSessionExpiry(sessExpValue, !!($('#setSessionOn')||{}).checked), 30000);
  }
  const m=s.macro||{};
  if($('#macroOn'))$('#macroOn').checked=!!m.enabled;
  if($('#macroReq'))$('#macroReq').value=m.request||'';
  if($('#macroTarget'))$('#macroTarget').value=m.target||'';
  if($('#macroExtract'))$('#macroExtract').value=m.extract||'';
  if($('#macroMode'))$('#macroMode').value=m.injectMode||'header';
  if($('#macroName'))$('#macroName').value=m.injectName||'';
  const lm=s.loginMacro||{};
  if($('#loginMacroOn'))$('#loginMacroOn').checked=!!lm.enabled;
  if($('#loginMacroReq'))$('#loginMacroReq').value=lm.request||'';
  if($('#loginMacroTarget'))$('#loginMacroTarget').value=lm.target||'';
  if($('#loginMacroRefresh'))$('#loginMacroRefresh').value=lm.refreshSecs||0;
  if($('#loginMacro401'))$('#loginMacro401').checked=lm.reauthOn401!==false;
  if($('#loginMacroState'))$('#loginMacroState').textContent=lm.enabled?'Login macro configured':'';
}catch(e){}}
function loginMacroBody(){
  return {enabled:$('#loginMacroOn').checked,target:$('#loginMacroTarget').value.trim(),request:$('#loginMacroReq').value,
    refreshSecs:parseInt(($('#loginMacroRefresh')||{}).value,10)||0,reauthOn401:!!($('#loginMacro401')||{}).checked};
}
// Save session headers + the token macro + per-host overrides together.
function saveSessionAll(){
  const macro={enabled:$('#macroOn').checked,target:$('#macroTarget').value.trim(),request:$('#macroReq').value,extract:$('#macroExtract').value.trim(),injectMode:$('#macroMode').value,injectName:$('#macroName').value.trim()};
  const body={enabled:$('#setSessionOn').checked,unscoped:!!($('#setSessionUnscoped')&&$('#setSessionUnscoped').checked),headers:$('#setSessionHeaders').value,macro,loginMacro:loginMacroBody(),hostHeaders:collectHostHeaders()};
  return api('/api/session',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify(body)});
}
if($('#saveSessionBtn'))$('#saveSessionBtn').onclick=async()=>{
  try{
    await saveSessionAll();
    // Surface macro completeness (previously on the now-removed per-macro Save buttons).
    const on=$('#macroOn').checked;
    const complete=$('#macroTarget').value.trim()&&$('#macroReq').value.trim()&&$('#macroExtract').value.trim()&&$('#macroName').value.trim();
    let msg='session saved';
    if(on) msg=complete?'session saved · token macro on — fires before each send':'session saved — set target, request, extract & inject-name for the token macro to fire';
    if($('#loginMacroOn')&&$('#loginMacroOn').checked) msg+=' · login macro on';
    toast(msg);loadSession();
  }catch(e){toast(e.message);}
};
if($('#loginMacroRun'))$('#loginMacroRun').onclick=async()=>{try{await saveSessionAll();const r=await api('/api/session/login/run',{method:'POST'});toast('session refreshed ('+r.applied+' header'+(r.applied===1?'':'s')+')');loadSession();}catch(e){toast(e.message);}};
// Test = dry-run: run the login request and show the response + the session it
// would capture, WITHOUT touching the live session (so you can debug it safely).
if($('#loginMacroTest'))$('#loginMacroTest').onclick=async()=>{
  const out=$('#loginMacroTestOut');
  try{
    await saveSessionAll(); // test what's in the form
    if(out){out.style.display='block';out.innerHTML='<span class="hint">testing…</span>';}
    const r=await api('/api/session/login/test',{method:'POST'});
    const sc=r.status||0,scColor=(sc>=200&&sc<400)?'var(--accent)':(sc>=400?'var(--red)':'var(--fg3)');
    const hdrs=r.headers||[];
    let html=`<div style="margin-bottom:6px">Login responded <b style="color:${scColor}">${sc||'no response'}</b> · captured <b>${hdrs.length}</b> session header${hdrs.length===1?'':'s'} <span class="hint">(dry-run — live session unchanged)</span></div>`;
    if(hdrs.length){
      html+=hdrs.map(h=>{const v=String(h.value||'');return `<div style="font-family:var(--mono);font-size:11px;overflow-wrap:anywhere"><span style="color:var(--accent)">${esc(h.key)}</span>: ${esc(v.length>160?v.slice(0,160)+'…':v)}</div>`;}).join('');
    }else{
      html+='<div class="hint" style="color:var(--amber)">No session captured — the login response set no Set-Cookie or Authorization. Check the request, credentials and target.</div>';
    }
    if(out)out.innerHTML=html;
  }catch(e){
    if(out){out.style.display='block';out.innerHTML='<span style="color:var(--red)">Test failed: '+esc(e.message)+'</span>';}
    else toast(e.message);
  }
};

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
  // Keep the master checkbox's checked/indeterminate state in sync as individual
  // rows are toggled — without this it stays stuck at the last bulk-set state and
  // misleads the user about how many hosts are selected.
  const sa=$('#retSelectAll');
  if(sa){
    body.querySelectorAll('.ret-chk').forEach(cb=>cb.addEventListener('change',()=>syncRetSelectAll(sa)));
    sa.checked=false;sa.indeterminate=false;
  }
}
function syncRetSelectAll(sa){
  const boxes=document.querySelectorAll('.ret-chk');
  if(!boxes.length){sa.checked=false;sa.indeterminate=false;return;}
  const n=[].slice.call(boxes).filter(b=>b.checked).length;
  sa.checked=n===boxes.length;
  sa.indeterminate=n>0&&n<boxes.length;
}

export function retChecked(){return [].slice.call(document.querySelectorAll('.ret-chk:checked')).map(cb=>cb.dataset.host);}

export async function retDeleteOne(host,flows){
  const msg='Delete all '+flows+' flow'+(flows===1?'':'s')+' from '+esc(host)+'? This is permanent.';
  const confirmed=await uiConfirm('Delete flows from '+esc(host),msg,'Delete','btn danger','var(--red)');
  if(!confirmed)return;
  try{
    const r=await api('/api/flows/purge',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({hosts:[host],mode:'delete'})});
    toast('deleted '+r.deleted+' flow'+(r.deleted===1?'':'s')+' · reclaiming space…');
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
    toast('deleted '+r.deleted+' flow'+(r.deleted===1?'':'s')+' · reclaiming space…');
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
    toast('deleted '+r.deleted+' flow'+(r.deleted===1?'':'s')+' · reclaiming space…');
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
    toast('deleted '+r.deleted+' flow'+(r.deleted===1?'':'s')+' · reclaiming space…');
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

$('#exportProject').onclick=()=>toast('Downloading project export…');
const runSetupBtn=$('#runSetupBtn');
if(runSetupBtn)runSetupBtn.onclick=()=>{import('./setup.js').then(m=>m.openSetup()).catch(e=>toast(e.message));};
const dlCa=$('#dlCaBtn');if(dlCa)dlCa.onclick=()=>toast('Downloading CA certificate — trust it on the client');
$('#importProjectBtn').onclick=()=>$('#importProjectFile').click();
$('#importProjectFile').onchange=async e=>{
  const f=e.target.files[0];if(!f)return;
  try{const text=await f.text();const r=await api('/api/import/project',{method:'POST',headers:{'content-type':'application/json'},body:text});
    toast(`imported ${r.importedFlows} flows · ${r.importedRules} rules · ${r.importedScope} scope`);
    loadFlows();loadRules();loadScope();loadSettings();}catch(err){toast('import: '+err.message);}
  e.target.value='';
};
// ---- project switching (close current, open another / a new path) ----
// Each project entry is {name, path}: path is empty for a named project under
// GlobalDir/projects (switch via {target: name}), or set for an external
// folder the operator chose explicitly (switch via {path}).
export async function loadProject(){
  try{const d=await api('/api/project');
    const n=$('#projNameHint');if(n)n.textContent=d.current||'default';
    const dir=$('#projDirHint');if(dir&&d.dir)dir.textContent=d.dir;
    const sel=$('#projSelect');
    if(sel){
      const list=(d.projects&&d.projects.length)?d.projects:[{name:'default',path:''}];
      sel.innerHTML=list.map(p=>{
        const isCur=p.name===d.current;
        const label=p.path?`${esc(p.name)} — ${esc(p.path)}`:esc(p.name);
        return `<option value="${escAttr(p.name)}" data-path="${escAttr(p.path||'')}"${isCur?' disabled':''}>${label}${isCur?' (current)':''}</option>`;
      }).join('');
    }
    if(!d.canSwitch)['projSwitchBtn','projNewBtn'].forEach(id=>{const b=$('#'+id);if(b){b.disabled=true;b.title='project switching is unavailable in this build';}});
  }catch(e){}
}
export async function doSwitchProject(target,path){
  if(!target&&!path)return;
  // Surface the "restarting…" message wherever it's visible — the Settings panel
  // note and the top-bar Projects modal share this one switch path.
  const notes=['#projSwitchNote','#pmNote'].map(s=>$(s)).filter(Boolean);
  const setNote=t=>notes.forEach(n=>{n.style.display='block';n.textContent=t;});
  setNote(path?`Switching to "${target||path}" (${path}) — restarting & reconnecting…`:`Switching to "${target}" — restarting & reconnecting…`);
  // The old process keeps serving (same version, same "ok") for a few hundred ms
  // after the switch is requested while the new one is still binding — polling
  // /api/version can't tell them apart and would reload straight back into the
  // OLD project's data. Remember which project we're leaving and poll /api/project
  // instead, waiting for `current` to actually flip before reloading. Bounded by a
  // grace period so re-selecting the *same* project (current never changes) doesn't
  // hang forever — after that we accept any live reply, same as before.
  let prevProject=null;
  try{prevProject=(await api('/api/project?_t='+Date.now())).current;}catch(e){}
  try{await api('/api/project/switch',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify(path?{path}:{target})});}catch(e){}
  const graceTries=10; // ~5s worth of polls
  let tries=0;const poll=setInterval(async()=>{tries++;
    try{
      const d=await api('/api/project?_t='+Date.now());
      if(prevProject!=null&&d.current===prevProject&&tries<=graceTries)return; // still the old process — keep waiting
      clearInterval(poll);location.reload();
    }
    catch(e){if(tries>60){clearInterval(poll);setNote('Still restarting… reload the page manually if it doesn\'t return.');}}
  },500);
}
$('#projSwitchBtn').onclick=()=>{
  const sel=$('#projSelect');const opt=sel&&sel.selectedOptions&&sel.selectedOptions[0];
  if(!opt){toast('no other project to open');return;}
  doSwitchProject(opt.value,opt.dataset.path||'');
};
$('#projNewBtn').onclick=()=>{
  const v=(($('#projNew')||{}).value||'').trim();
  const path=(($('#projNewPath')||{}).value||'').trim();
  if(!v&&!path){toast('enter a project name, or a custom save folder');return;}
  doSwitchProject(v,path);
};

// ---- top-bar Projects picker modal (click the project badge) ----
// Same data + switch endpoint as the Settings panel, surfaced as a prominent,
// first-class action so choosing a project never means opening Settings.
async function renderProjModal(){
  try{const d=await api('/api/project');
    const cur=$('#pmCurrent');if(cur)cur.textContent=d.current||'default';
    const dir=$('#pmDir');if(dir)dir.textContent=d.dir||'';
    const list=$('#pmList');if(!list)return;
    if(!d.canSwitch){list.innerHTML='<div class="hint">Project switching is unavailable in this build.</div>';const nb=$('#pmNewBtn');if(nb)nb.disabled=true;return;}
    const others=(d.projects||[]).filter(p=>p.name!==d.current);
    list.innerHTML=others.length
      ?others.map(p=>{
        const sub=p.path?`<div class="hint" style="font-family:var(--mono);font-size:10px;margin-top:1px">${esc(p.path)}</div>`:'';
        return `<button class="btn pm-row" data-proj="${escAttr(p.name)}" data-path="${escAttr(p.path||'')}" style="text-align:left;background:var(--bg3);display:block">◧ ${esc(p.name)}${sub}</button>`;
      }).join('')
      :'<div class="hint">No other saved projects yet — create one below.</div>';
    list.querySelectorAll('.pm-row').forEach(b=>b.onclick=()=>doSwitchProject(b.dataset.proj,b.dataset.path||''));
  }catch(e){}
}
export async function openProjectModal(){
  const m=$('#projModal');if(!m)return;
  const note=$('#pmNote');if(note){note.style.display='none';note.textContent='';}
  const inp=$('#pmNew');if(inp)inp.value='';
  const pinp=$('#pmNewPath');if(pinp)pinp.value='';
  openModal(m);
  await renderProjModal();
  if(inp)inp.focus();
}
{const c=$('#pmClose');if(c)c.onclick=()=>closeModal($('#projModal'));}
{const nb=$('#pmNewBtn');if(nb)nb.onclick=()=>{
  const v=(($('#pmNew')||{}).value||'').trim();
  const path=(($('#pmNewPath')||{}).value||'').trim();
  if(!v&&!path){toast('enter a project name, or a custom save folder');return;}
  doSwitchProject(v,path);
};}
{const ni=$('#pmNew');if(ni)ni.addEventListener('keydown',e=>{if(e.key==='Enter'){e.preventDefault();$('#pmNewBtn').click();}});}
{const pi=$('#pmNewPath');if(pi)pi.addEventListener('keydown',e=>{if(e.key==='Enter'){e.preventDefault();$('#pmNewBtn').click();}});}
$('#saveAddrBtn').onclick=async()=>{
  const addrs=collectProxyAddrs();
  if(!addrs.length){toast('enter at least one listener');return;}
  try{
    const s=await api('/api/settings',{method:'PUT',headers:{'content-type':'application/json'},body:JSON.stringify({proxyAddrs:addrs})});
    state.proxyAddr=s.proxyAddr;$('#proxyAddr').textContent=s.proxyAddr;
    if($('#setAddr'))$('#setAddr').value=s.proxyAddr;
    renderProxyListeners(s.proxyAddrs||addrs);
    await loadDeviceProxyEndpoint();
    toast('proxy now on '+s.proxyAddr);
  }catch(e){toast(e.message);}
};
$('#saveControlAddrBtn').onclick=async()=>{
  const controlAddr=syncControlAddrFields();
  if(!controlAddr){toast('enter control host and port');return;}
  try{
    const s=await api('/api/settings',{method:'PUT',headers:{'content-type':'application/json'},body:JSON.stringify({controlAddr})});
    state.controlAddr=s.controlAddr;$('#controlAddr').textContent=s.controlAddr;
    const c=parseListenAddr(s.controlAddr);
    if($('#setControlPort'))$('#setControlPort').value=c.port;
    renderHostSelect($('#setControlHost'),c.host);
    const newUrl='http://'+s.controlAddr;
    if(location.host!==s.controlAddr)toast('Control UI now on '+newUrl+' — open that URL if this page stops updating');
    else toast('control UI now on '+s.controlAddr);
    const tun=$('#oobModalTunnelCmd');if(tun)tun.textContent='cloudflared tunnel --url '+newUrl;
  }catch(e){toast(e.message);}
};
export async function loadSysProxy(){
  try{const s=await api('/api/sysproxy');const sec=$('#sysProxySection');const b=$('#sysProxyToggle');
    if(!s.supported){
      if(sec)sec.style.display='none';
      return;
    }
    if(sec)sec.style.display='';
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

let androidDeviceSerial='';

function androidSerial(){
  return androidDeviceSerial||'';
}

function androidDeviceTitle(d){
  if(d.model)return d.model;
  if(d.emulator)return 'Android emulator';
  if(d.serial&&d.serial!=='(no serial number)')return d.serial;
  return 'Connected device';
}

function androidDeviceMeta(d){
  const bits=[];
  if(d.emulator)bits.push('emulator');
  if(d.suggestedCAMode==='system')bits.push('system CA suggested');
  else if(d.suggestedCAMode==='user')bits.push('user CA suggested');
  if(d.state&&d.state!=='device')bits.push(d.state);
  if(d.serial==='(no serial number)'&&d.transportId)bits.push('adb transport '+d.transportId);
  else if(d.serial&&d.serial!=='(no serial number)')bits.push(d.serial);
  return bits.join(' · ');
}

function androidProxyMode(){
  const on=$('#androidProxyMode')?.querySelector('button.on');
  return on?.dataset.mode==='wifi'?'wifi':'usb';
}

function closeAndroidDeviceMenu(){
  const menu=$('#androidDeviceMenu'),trigger=$('#androidDeviceTrigger');
  if(menu)menu.hidden=true;
  if(trigger)trigger.setAttribute('aria-expanded','false');
}

function toggleAndroidDeviceMenu(){
  const menu=$('#androidDeviceMenu'),trigger=$('#androidDeviceTrigger');
  if(!menu||!trigger||trigger.disabled)return;
  if(!menu.hidden){closeAndroidDeviceMenu();return;}
  menu.hidden=false;
  trigger.setAttribute('aria-expanded','true');
  menu.querySelector('.ui-select-opt.sel')?.scrollIntoView({block:'nearest'});
}

function renderAndroidDevicePicker(devs){
  const menu=$('#androidDeviceMenu'),trigger=$('#androidDeviceTrigger'),valueEl=$('#androidDeviceValue'),meta=$('#androidDeviceMeta');
  if(!menu||!trigger||!valueEl)return;
  closeAndroidDeviceMenu();
  if(!devs.length){
    androidDeviceSerial='';
    valueEl.textContent='No device connected';
    trigger.disabled=true;
    menu.innerHTML='';
    if(meta)meta.textContent='Connect a device with USB debugging enabled.';
    return;
  }
  trigger.disabled=false;
  if(!devs.some(d=>d.serial===androidDeviceSerial&&d.state==='device')){
    const first=devs.find(d=>d.state==='device');
    androidDeviceSerial=first?first.serial:'';
  }
  menu.innerHTML=devs.map(d=>{
    const sel=d.serial===androidDeviceSerial;
    const dis=d.state!=='device';
    return `<button type="button" role="option" class="ui-select-opt${sel?' sel':''}" data-serial="${escAttr(d.serial)}"${dis?' disabled':''} aria-selected="${sel?'true':'false'}"><span class="ui-select-opt-title">${esc(androidDeviceTitle(d))}${dis?' — '+esc(d.state):''}</span><span class="ui-select-opt-sub">${esc(androidDeviceMeta(d))}</span></button>`;
  }).join('');
  const cur=devs.find(d=>d.serial===androidDeviceSerial);
  valueEl.textContent=cur?androidDeviceTitle(cur):'Select device…';
  if(meta)meta.textContent=cur?androidDeviceMeta(cur):'';
}

async function androidPost(path,body){
  const payload={serial:androidSerial(),proxyMode:androidProxyMode(),...body};
  return api(path,{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify(payload)});
}

function isLoopbackProxyBind(addr){
  if(!addr)return true;
  const host=parseListenAddr(addr).host.toLowerCase();
  return host==='127.0.0.1'||host==='localhost'||host==='::1';
}

function proxyHasExternalBind(s){
  const addrs=(s&&s.proxyAddrs&&s.proxyAddrs.length)?s.proxyAddrs:[s?.proxy||state.proxyAddr];
  return addrs.some(a=>!isLoopbackProxyBind(a));
}

function androidWifiNeedsProxyBind(s){
  return androidProxyMode()==='wifi'&&(!s.externalBindAllowed||!proxyHasExternalBind(s));
}

export async function loadAndroid(){
  const sec=$('#androidAdbSection'),hint=$('#androidAdbHint');
  const lanHint=$('#androidLanHint'),caHint=$('#androidCaHint');
  if(!sec)return;
  try{
    const s=await api('/api/android/status');
    if(!s.available){
      sec.style.display='none';
      return;
    }
    sec.style.display='';
    const devs=s.devices||[];
    renderAndroidDevicePicker(devs);
    if(lanHint){
      let html='';
      if(s.lanHost)html=`<span>LAN host: ${esc(s.lanHost)}</span>`;
      if(androidWifiNeedsProxyBind(s)){
        if(html)html+='<br>';
        html+=`<span>Wi‑Fi mode needs bind <code>0.0.0.0</code> on the proxy listener.</span> <button type="button" class="btn" id="androidOpenProxyBtn">Settings → Proxy</button>`;
      }
      lanHint.innerHTML=html;
      lanHint.style.display=html?'':'none';
    }
    const cur=devs.find(d=>d.serial===androidSerial());
    if(caHint&&cur){
      const sug=cur.suggestedCAMode==='system'?'Suggested: install system CA (emulator)':'Suggested: install user CA (physical device)';
      caHint.textContent=sug;
    }else if(caHint)caHint.textContent='';
    let msg='';
    if(!devs.length)msg='Connect a device with USB debugging enabled.';
    else{
      const selSerial=androidSerial();
      const active=selSerial&&s.proxySerial===selSerial&&s.proxyActive?s.proxyValue:(s.proxyActive?s.proxyValue:'');
      if(active)msg='Device proxy active: '+active;
      else if(devs.some(d=>d.state==='unauthorized'))msg='Accept the USB debugging authorization prompt on the device.';
    }
    if(hint)hint.textContent=msg;
  }catch(e){
    if(hint)hint.textContent='';
    sec.style.display='none';
  }
}

async function androidAction(fn){
  try{await fn();await loadAndroid();}catch(e){toast(e.message);}
}

$('#androidProxyMode')&&$('#androidProxyMode').addEventListener('click',e=>{
  const b=e.target.closest('button[data-mode]');
  if(!b||b.classList.contains('on'))return;
  b.parentElement.querySelectorAll('button[data-mode]').forEach(x=>setSeg(x,x===b));
  loadAndroid();
});
{const t=$('#androidDeviceTrigger');if(t)t.addEventListener('click',e=>{e.stopPropagation();toggleAndroidDeviceMenu();});}
{const m=$('#androidDeviceMenu');if(m)m.addEventListener('click',e=>{
  const opt=e.target.closest('.ui-select-opt');
  if(!opt||opt.disabled)return;
  androidDeviceSerial=opt.dataset.serial||'';
  closeAndroidDeviceMenu();
  loadAndroid();
});}
document.addEventListener('click',()=>closeAndroidDeviceMenu());
{const wrap=$('#androidDeviceSelectWrap');if(wrap)wrap.addEventListener('keydown',e=>{
  if(e.key==='Escape')closeAndroidDeviceMenu();
});}
{const lh=$('#androidLanHint');if(lh)lh.addEventListener('click',e=>{if(e.target.closest('#androidOpenProxyBtn'))openSettingsProxy();});}
$('#androidRefreshBtn')&&($('#androidRefreshBtn').onclick=()=>androidAction(loadAndroid));
$('#androidSetupAllBtn')&&($('#androidSetupAllBtn').onclick=()=>androidAction(async()=>{
  const r=await androidPost('/api/android/setup',{caMode:'auto'});
  toast(r.message||'Android setup complete');
}));
$('#androidInstallUserBtn')&&($('#androidInstallUserBtn').onclick=()=>androidAction(async()=>{
  const r=await androidPost('/api/android/install-ca',{mode:'user'});
  toast(r.message||'CA install prompt opened on device');
}));
$('#androidInstallSystemBtn')&&($('#androidInstallSystemBtn').onclick=()=>androidAction(async()=>{
  const r=await androidPost('/api/android/install-ca',{mode:'system'});
  toast(r.message||'System CA installed');
}));
$('#androidProxyBtn')&&($('#androidProxyBtn').onclick=()=>androidAction(async()=>{
  const r=await androidPost('/api/android/proxy',{});
  toast(r.message||'Device proxied');
}));
$('#androidUnproxyBtn')&&($('#androidUnproxyBtn').onclick=()=>androidAction(async()=>{
  const remove=!!($('#androidRemoveSystemCa')||{}).checked;
  const r=await androidPost('/api/android/unproxy',{removeSystemCA:remove});
  toast(r.warning?(r.message+' — '+r.warning):(r.message||'Device proxy cleared'));
}));

/* ---- iOS (simulator + device profile) ---- */
let iosDeviceUDID='';

function iosUDID(){return iosDeviceUDID||'';}

function iosDeviceTitle(d){
  if(d.name)return d.name+(d.booted?' (booted)':'');
  return d.kind==='simulator'?'iOS Simulator':d.udid||'Device';
}

function iosDeviceMeta(d){
  const bits=[];
  if(d.kind==='simulator')bits.push('simulator');
  else bits.push('physical');
  if(d.runtime)bits.push(d.runtime);
  if(d.state&&d.state!=='Booted'&&d.state!=='connected')bits.push(d.state);
  if(d.udid)bits.push(d.udid.slice(0,8)+'…');
  return bits.join(' · ');
}

function iosProxyMode(){
  const on=$('#iosProxyMode')?.querySelector('button.on');
  return on?.dataset.mode==='wifi'?'wifi':'localhost';
}

function closeIOSDeviceMenu(){
  const menu=$('#iosDeviceMenu'),trigger=$('#iosDeviceTrigger');
  if(menu)menu.hidden=true;
  if(trigger)trigger.setAttribute('aria-expanded','false');
}

function toggleIOSDeviceMenu(){
  const menu=$('#iosDeviceMenu'),trigger=$('#iosDeviceTrigger');
  if(!menu||!trigger||trigger.disabled)return;
  if(!menu.hidden){closeIOSDeviceMenu();return;}
  menu.hidden=false;
  trigger.setAttribute('aria-expanded','true');
}

function renderIOSDevicePicker(devs){
  const menu=$('#iosDeviceMenu'),trigger=$('#iosDeviceTrigger'),valueEl=$('#iosDeviceValue'),meta=$('#iosDeviceMeta');
  if(!menu||!trigger||!valueEl)return;
  closeIOSDeviceMenu();
  trigger.disabled=false;
  if(!devs.length){
    iosDeviceUDID='';
    valueEl.textContent='Manual profile (no simulator/device detected)';
    menu.innerHTML='';
    if(meta)meta.textContent='Download profile and open on iPhone Safari, or boot a simulator.';
    return;
  }
  if(!devs.some(d=>d.udid===iosDeviceUDID)){
    const booted=devs.find(d=>d.booted)||devs.find(d=>d.kind==='physical')||devs[0];
    iosDeviceUDID=booted?booted.udid:'';
  }
  menu.innerHTML=devs.map(d=>{
    const sel=d.udid===iosDeviceUDID;
    return `<button type="button" role="option" class="ui-select-opt${sel?' sel':''}" data-udid="${escAttr(d.udid)}" aria-selected="${sel?'true':'false'}"><span class="ui-select-opt-title">${esc(iosDeviceTitle(d))}</span><span class="ui-select-opt-sub">${esc(iosDeviceMeta(d))}</span></button>`;
  }).join('');
  const cur=devs.find(d=>d.udid===iosDeviceUDID);
  valueEl.textContent=cur?iosDeviceTitle(cur):'Select target…';
  if(meta)meta.textContent=cur?iosDeviceMeta(cur):'';
}

async function iosPost(path,body){
  const payload={udid:iosUDID(),proxyMode:iosProxyMode(),...body};
  return api(path,{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify(payload)});
}

function iosWifiNeedsProxyBind(s){
  return iosProxyMode()==='wifi'&&(!s.externalBindAllowed||!proxyHasExternalBind(s));
}

export async function loadIOS(){
  const sec=$('#iosSection'),hint=$('#iosHint'),lanHint=$('#iosLanHint'),profileLink=$('#iosProfileLink');
  if(!sec)return;
  try{
    const s=await api('/api/ios/status');
    sec.style.display='';
    const devs=s.devices||[];
    renderIOSDevicePicker(devs);
    if(profileLink){
      let href='/api/ios/profile.mobileconfig';
      if(iosProxyMode()==='wifi'&&s.lanHost)href+='?host='+encodeURIComponent(s.lanHost);
      profileLink.href=href;
    }
    if(lanHint){
      let html='';
      if(s.lanHost)html=`<span>LAN host: ${esc(s.lanHost)}</span>`;
      if(iosWifiNeedsProxyBind(s)){
        if(html)html+='<br>';
        html+=`<span>Wi‑Fi mode needs bind <code>0.0.0.0</code> on the proxy listener.</span> <button type="button" class="btn" id="iosOpenProxyBtn">Settings → Proxy</button>`;
      }
      if(!s.simctlAvailable&&devs.every(d=>d.kind!=='simulator'))html+=(html?'<br>':'')+'<span>Install Xcode for simulator automation (<code>xcrun simctl</code>).</span>';
      lanHint.innerHTML=html;
      lanHint.style.display=html?'':'none';
    }
    let msg='';
    if(!devs.length)msg='Boot an iOS Simulator or connect an iPhone — or download the profile for manual install.';
    else if(!s.simctlAvailable)msg='Simulator automation needs Xcode on macOS. Physical devices: download profile → open in Safari on the phone.';
    if(hint)hint.textContent=msg;
  }catch(e){
    if(hint)hint.textContent='';
  }
}

async function iosAction(fn){
  try{await fn();await loadIOS();}catch(e){toast(e.message);}
}

$('#iosProxyMode')&&$('#iosProxyMode').addEventListener('click',e=>{
  const b=e.target.closest('button[data-mode]');
  if(!b||b.classList.contains('on'))return;
  b.parentElement.querySelectorAll('button[data-mode]').forEach(x=>setSeg(x,x===b));
  loadIOS();
});
{const t=$('#iosDeviceTrigger');if(t)t.addEventListener('click',e=>{e.stopPropagation();toggleIOSDeviceMenu();});}
{const m=$('#iosDeviceMenu');if(m)m.addEventListener('click',e=>{
  const opt=e.target.closest('.ui-select-opt');
  if(!opt)return;
  iosDeviceUDID=opt.dataset.udid||'';
  closeIOSDeviceMenu();
  loadIOS();
});}
document.addEventListener('click',()=>closeIOSDeviceMenu());
{const lh=$('#iosLanHint');if(lh)lh.addEventListener('click',e=>{if(e.target.closest('#iosOpenProxyBtn'))openSettingsProxy();});}
$('#iosRefreshBtn')&&($('#iosRefreshBtn').onclick=()=>iosAction(loadIOS));
$('#iosSetupAllBtn')&&($('#iosSetupAllBtn').onclick=()=>iosAction(async()=>{
  const r=await iosPost('/api/ios/setup',{});
  toast(r.message||'iOS setup started');
  if(r.profileUrl&&r.kind!=='simulator')window.open(r.profileUrl,'_blank');
}));
$('#iosInstallCaBtn')&&($('#iosInstallCaBtn').onclick=()=>iosAction(async()=>{
  const r=await iosPost('/api/ios/install-ca',{});
  toast(r.message||'Simulator CA installed');
}));
$('#iosOpenProfileBtn')&&($('#iosOpenProfileBtn').onclick=()=>iosAction(async()=>{
  const r=await iosPost('/api/ios/open-profile',{});
  toast(r.message||'Profile opened in simulator');
}));

/* ---- iOS jailbroken SSH ---- */
const iosSshSessionKey='interceptor.iosSsh';

function iosSshFields(){
  return {
    host:($('#iosSshHost')||{}).value?.trim()||'',
    port:parseInt(($('#iosSshPort')||{}).value,10)||22,
    user:($('#iosSshUser')||{}).value?.trim()||'root',
    password:($('#iosSshPassword')||{}).value||'',
    keyPath:($('#iosSshKeyPath')||{}).value?.trim()||'',
  };
}

function iosSshRemember(){
  const f=iosSshFields();
  try{
    sessionStorage.setItem(iosSshSessionKey,JSON.stringify({
      host:f.host,port:f.port,user:f.user,keyPath:f.keyPath,
    }));
  }catch(e){}
}

function iosSshRestore(){
  try{
    const raw=sessionStorage.getItem(iosSshSessionKey);
    if(!raw)return;
    const s=JSON.parse(raw);
    if(s.host&&$('#iosSshHost'))$('#iosSshHost').value=s.host;
    if(s.port&&$('#iosSshPort'))$('#iosSshPort').value=s.port;
    if(s.user&&$('#iosSshUser'))$('#iosSshUser').value=s.user;
    if(s.keyPath&&$('#iosSshKeyPath'))$('#iosSshKeyPath').value=s.keyPath;
  }catch(e){}
}

async function iosSshPost(path,extra){
  const f=iosSshFields();
  if(!f.host)throw new Error('SSH host is required');
  if(!f.password&&!f.keyPath)throw new Error('SSH password or private key path is required');
  iosSshRemember();
  const body={host:f.host,port:f.port,user:f.user,...extra};
  if(f.password)body.password=f.password;
  if(f.keyPath)body.keyPath=f.keyPath;
  return api(path,{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify(body)});
}

export async function loadIOSSsh(){
  const sec=$('#iosSshSection'),hint=$('#iosSshHint'),lanHint=$('#iosSshLanHint');
  if(!sec)return;
  iosSshRestore();
  try{
    const s=await api('/api/ios/ssh/status');
    sec.style.display='';
    if(lanHint){
      let html='';
      if(s.lanHost)html=`<span>LAN host for profile proxy: ${esc(s.lanHost)}</span>`;
      if(!s.externalBindAllowed||!proxyHasExternalBind(s)){
        if(html)html+='<br>';
        html+=`<span>Device proxy needs bind <code>0.0.0.0</code> on the proxy listener.</span> <button type="button" class="btn" id="iosSshOpenProxyBtn">Settings → Proxy</button>`;
      }
      lanHint.innerHTML=html;
      lanHint.style.display=html?'':'none';
    }
    if(hint&&!iosSshFields().host)hint.textContent='Enter the jailbroken device IP and SSH credentials, then Check SSH status or Setup all.';
  }catch(e){
    if(hint)hint.textContent='';
  }
}

async function iosSshAction(fn){
  try{
    await fn();
    await loadIOSSsh();
  }catch(e){toast(e.message);}
}

{const lh=$('#iosSshLanHint');if(lh)lh.addEventListener('click',e=>{if(e.target.closest('#iosSshOpenProxyBtn'))openSettingsProxy();});}
['iosSshHost','iosSshPort','iosSshUser','iosSshKeyPath'].forEach(id=>{
  const el=$('#'+id);
  if(el)el.addEventListener('change',iosSshRemember);
});
$('#iosSshStatusBtn')&&($('#iosSshStatusBtn').onclick=()=>iosSshAction(async()=>{
  const r=await iosSshPost('/api/ios/ssh/status',{});
  const hint=$('#iosSshHint');
  if(hint)hint.textContent=r.message||'SSH status checked';
  toast(r.message||'SSH status checked');
}));
$('#iosSshSetupBtn')&&($('#iosSshSetupBtn').onclick=()=>iosSshAction(async()=>{
  const r=await iosSshPost('/api/ios/ssh/setup',{});
  const hint=$('#iosSshHint');
  if(hint)hint.textContent=r.warning?(r.message+' — '+r.warning):r.message;
  toast(r.message||'iOS SSH setup started');
}));
$('#iosSshInstallCaBtn')&&($('#iosSshInstallCaBtn').onclick=()=>iosSshAction(async()=>{
  const r=await iosSshPost('/api/ios/ssh/install-ca',{});
  const hint=$('#iosSshHint');
  if(hint)hint.textContent=r.message||'Profile opened on device';
  toast(r.message||'Profile opened on device');
}));
