import { $, registerProjectSwitchGuard, projectSwitchBlocker, $$, esc, escAttr, state, toast, toastError, api, fmtBytes, uiConfirm, uiPrompt, openModal, closeModal, copyText, setSeg, syncUiSelectStyles, renderLoadError, closeAllUiSelects, projectStorageKey } from './core.js';
registerProjectSwitchGuard(()=>hasUnsavedSettingsFields()?'Save your Settings changes before switching projects.':'');
import { loadFlows, loadScope } from './proxy.js';
import { loadRules } from './intercept.js';
import { prefersReducedMotion } from './motion.js';
import { createSplitPane } from './split.js';
import { matchSections, searchSummary, highlightRanges } from './settings-model.js';
import { confirmTyped } from './settings-confirm.js';
import { mountChecklist } from './checklist.js';
import './settings-appearance.js';
import './settings-health.js';

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
  const entries = Array.isArray(hostHeaders)
    ? hostHeaders.map(row => [row?.host || '', row?.headers || ''])
    : Object.entries(hostHeaders || {});
  for (const [host, hdrs] of entries) {
    list.appendChild(makeHostHdrRow(host, hdrs));
  }
}

function makeHostHdrRow(host, hdrs) {
  const row = document.createElement('div');
  row.className = 'host-hdr-row';
  row.classList.add('row', 'u-gap-2', 'u-ai-start', 'u-mb-2');
  row.innerHTML = `<input class="btn host-hdr-host" aria-label="Host override hostname" style="background:var(--bg3);font-family:var(--mono);font-size:var(--fs-xs);width:200px;flex-shrink:0" placeholder="hostname.example.com" spellcheck="false" value="${escAttr(host||'')}">` +
    `<textarea class="host-hdr-headers" aria-label="Headers for host override" rows="2" style="flex:1;font-family:var(--mono);font-size:var(--fs-xs);resize:vertical;background:var(--bg3);border:1px solid var(--line);border-radius:4px;padding:4px 6px;min-width:0" placeholder="Authorization: Bearer eyJ…&#10;Cookie: session=…">${esc(hdrs||'')}</textarea>` +
    `<button class="btn host-hdr-del" style="flex-shrink:0;align-self:flex-start;padding:3px 8px;color:var(--red)" title="Remove this host override" aria-label="Remove host header override for ${escAttr(host||'new host')}">×</button>`;
  row.querySelector('.host-hdr-del').onclick = () => {
    row.remove();
    markSettingsDirty($('#hostHdrList'));
  };
  return row;
}

// Live settings.refresh events can arrive while an operator is editing. Keep
// the dirty DOM values keyed by stable control IDs and snapshot dynamic rows
// separately, so a response never silently wins over in-progress work.
function markSettingsDirty(el,edited=true) {
  if (el) {
    el.dataset.settingsDirty = '1';
    if(edited)el.dataset.settingsEditGeneration = String((Number(el.dataset.settingsEditGeneration)||0)+1);
  }
}

function settingsEditGeneration(el) {
  return Number(el?.dataset.settingsEditGeneration)||0;
}

function settingsEditOwned(el,generation,value) {
  if(!el||settingsEditGeneration(el)!==generation)return false;
  return value===undefined||el.value===value;
}

function clearSettingsDirty(ids=[], lists=[]) {
  ids.forEach(id => $('#'+id)?.removeAttribute('data-settings-dirty'));
  lists.forEach(sel => $(sel)?.removeAttribute('data-settings-dirty'));
}

let settingsLoadEpoch=0;
let settingsReconcileTimer=null;
const settingsMutationLanes=new Map();
const settingsAcknowledgedValues=new Map();
let settingsMutationRevision=0;

function scheduleSettingsReconcile() {
  if(settingsReconcileTimer!==null)clearTimeout(settingsReconcileTimer);
  settingsReconcileTimer=setTimeout(()=>{
    settingsReconcileTimer=null;
    void loadSettings();
  },0);
}

function invalidateSettingsLoads({reconcile=true}={}) {
  settingsLoadEpoch++;
  const loadState=$('#settingsLoadState');
  if(loadState?.textContent==='Loading Settings…')loadState.style.display='none';
  if(reconcile)scheduleSettingsReconcile();
}

export async function saveSettingsPatch(patch,{invalidate=true,reconcile=true}={}) {
  const result=await api('/api/settings',{method:'PUT',headers:{'content-type':'application/json'},body:JSON.stringify(patch)});
  if(invalidate)invalidateSettingsLoads({reconcile});
  return result;
}

function settingsMutationValue(key,fallback,loadRevision) {
  const lane=settingsMutationLanes.get(key);
  const mutation=lane?.pending||lane?.active;
  if(mutation)return mutation.value;
  const acknowledged=settingsAcknowledgedValues.get(key);
  return acknowledged&&acknowledged.revision>loadRevision?acknowledged.value:fallback;
}

function setSettingsMutationBusy(lane,mutation) {
  const control=mutation.control;
  if(!control)return;
  if(!lane.controls.has(control))lane.controls.set(control,!!control.disabled);
  lane.lockControl=lane.lockControl||!!mutation.lockControl;
  if(lane.lockControl&&document.activeElement===control)lane.focusReturn=control;
  lane.controls.forEach((_wasDisabled,current)=>{
    current.setAttribute('aria-busy','true');
    if(lane.lockControl)current.disabled=true;
  });
}

function clearSettingsMutationBusy(lane) {
  const focusReturn=lane.focusReturn;
  lane.focusReturn=null;
  lane.controls.forEach((wasDisabled,control)=>{
    control.removeAttribute('aria-busy');
    if(lane.lockControl)control.disabled=wasDisabled;
  });
  if(focusReturn?.isConnected&&!focusReturn.disabled&&
    (!document.activeElement||document.activeElement===document.body))focusReturn.focus({preventScroll:true});
}

function queueSettingsMutation(key,value,options) {
  let lane=settingsMutationLanes.get(key);
  if(!lane){
    lane={generation:0,running:false,pending:null,active:null,controls:new Map(),lockControl:false,focusReturn:null};
    settingsMutationLanes.set(key,lane);
  }
  if(lane.pending)lane.pending.resolve(false);
  const mutation={...options,value,generation:++lane.generation,revision:++settingsMutationRevision,resolve:null};
  const completion=new Promise(resolve=>{mutation.resolve=resolve;});
  lane.pending=mutation;
  setSettingsMutationBusy(lane,mutation);
  if(!lane.running)void drainSettingsMutation(key,lane);
  return completion;
}

async function drainSettingsMutation(key,lane) {
  lane.running=true;
  while(lane.pending){
    const mutation=lane.pending;
    lane.pending=null;
    lane.active=mutation;
    let result,error;
    try{result=await mutation.request(mutation.value);}
    catch(err){error=err;}
    const latest=!lane.pending&&lane.active===mutation&&lane.generation===mutation.generation;
    if(!latest){mutation.resolve(false);continue;}
    lane.active=null;
    lane.running=false;
    settingsMutationLanes.delete(key);
    clearSettingsMutationBusy(lane);
    try{
      if(error)mutation.failure?.(error,mutation.value);
      else{
        settingsAcknowledgedValues.set(key,{revision:++settingsMutationRevision,value:mutation.value});
        mutation.success?.(result,mutation.value);
      }
    }finally{mutation.resolve(!error);}
    return;
  }
  lane.active=null;
  lane.running=false;
  settingsMutationLanes.delete(key);
  clearSettingsMutationBusy(lane);
}

function saveBooleanSetting(key,value,options) {
  return queueSettingsMutation(key,value,{
    control:options.control,
    lockControl:options.lockControl,
    request:current=>saveSettingsPatch({[key]:current},{invalidate:false}),
    success:(_result,current)=>options.success?.(current),
    failure:(error,current)=>options.failure?.(error,current),
  });
}

function collectHostHeaderRows() {
  return [...document.querySelectorAll('.host-hdr-row')].map(row => ({
    host: row.querySelector('.host-hdr-host')?.value || '',
    headers: row.querySelector('.host-hdr-headers')?.value || '',
  }));
}

function snapshotDirtySettings() {
  const snapshot = {fields: {}, proxyListeners: null, hostHeaders: null, deviceProxyMode: null};
  document.querySelectorAll('#panel-settings [data-settings-dirty="1"]').forEach(el => {
    if (el.type === 'file') return;
    if (el.id && el.id !== 'proxyListenersList' && el.id !== 'hostHdrList') {
      snapshot.fields[el.id] = el.type === 'checkbox' ? el.checked : el.value;
    }
  });
  if ($('#proxyListenersList')?.dataset.settingsDirty === '1') snapshot.proxyListeners=collectProxyAddrs();
  if ($('#hostHdrList')?.dataset.settingsDirty === '1') snapshot.hostHeaders=collectHostHeaderRows();
  if ($('#deviceProxyModeSeg')?.dataset.settingsDirty === '1') {
    const selected=$('#deviceProxyModeSeg').querySelector('button.on[data-mode]');
    if(selected)snapshot.deviceProxyMode=selected.dataset.mode;
  }
  return snapshot;
}

function restoreDirtySettings(snapshot) {
  if (!snapshot) return;
  Object.entries(snapshot.fields || {}).forEach(([id, value]) => {
    const el = $('#'+id);
    if (!el) return;
    if (el.type === 'file') return;
    if (el.type === 'checkbox') el.checked = !!value;
    else el.value = value;
    markSettingsDirty(el,false);
  });
  if (snapshot.proxyListeners) {
    renderProxyListeners(snapshot.proxyListeners);
    markSettingsDirty($('#proxyListenersList'),false);
  }
  if (snapshot.hostHeaders) {
    renderHostHdrList(snapshot.hostHeaders);
    markSettingsDirty($('#hostHdrList'),false);
  }
  if(snapshot.deviceProxyMode){
    const seg=$('#deviceProxyModeSeg');
    if(seg){
      seg.querySelectorAll('button[data-mode]').forEach(button=>setSeg(button,button.dataset.mode===snapshot.deviceProxyMode));
      markSettingsDirty(seg,false);
    }
    const manual=$('#deviceProxyManualField');
    if(manual)manual.style.display=snapshot.deviceProxyMode==='manual'?'':'none';
  }
}

function restoreDirtySettingsDerived(snapshot) {
  const fields=snapshot?.fields||{};
  const dirty=id=>Object.prototype.hasOwnProperty.call(fields,id);
  if(dirty('setOobEnabled')){
    state.oobEnabled=!!$('#setOobEnabled')?.checked;
    applyOobDisabledUI();
  }
  if(['setUpstreamScheme','setUpstreamHost','setUpstreamPort','setUpstreamUser','setUpstreamPassword','setUpstreamCA'].some(dirty)){
    renderUpstreamProxyFields($('#setUpstreamScheme').value);
  }
  if(dirty('originTLSVerifyMode'))setOriginTLSVerify($('#originTLSVerifyMode').value==='strict');
  if(dirty('tlsBypassList'))updateBypassCount();
  if(dirty('originTLSVerifyBypassList'))updateOriginTLSVerifyBypassCount();
  if(dirty('proxyAuthEnabled'))setProxyAuth($('#proxyAuthEnabled')?.value==='1');
}

function runSettingsAction(button, action) {
  if (!button || button.dataset.pending === '1') return Promise.resolve(false);
  const wasDisabled = button.disabled;
  button.dataset.pending = '1';
  button.disabled = true;
  button.setAttribute('aria-busy', 'true');
  return Promise.resolve().then(action).finally(() => {
    button.dataset.pending = '0';
    button.disabled = wasDisabled;
    button.removeAttribute('aria-busy');
  });
}

const settingsBody = $('#panel-settings');
if (settingsBody) {
  settingsBody.addEventListener('input', event => {
    const el = event.target.closest('input,select,textarea');
    if (el) markSettingsDirty(el);
    const list = event.target.closest('#proxyListenersList,#hostHdrList');
    if (list) markSettingsDirty(list);
  });
  settingsBody.addEventListener('change', event => {
    const el = event.target.closest('input,select,textarea');
    if (el) markSettingsDirty(el);
    const list = event.target.closest('#proxyListenersList,#hostHdrList');
    if (list) markSettingsDirty(list);
  });
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
  markSettingsDirty($('#hostHdrList'));
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

async function loadNetworkHosts({deferRender=false}={}){
  let result=null;
  try{result=await api('/api/network/hosts');}catch(e){}
  if(deferRender)return result;
  networkHosts=result;
  $('#setControlHost')?.setAttribute('aria-label','Control UI bind host');
  $('#setControlPort')?.setAttribute('aria-label','Control UI bind port');
  // A live settings refresh may finish while the operator is editing the
  // control bind. Leave that select untouched; loadSettings snapshots the
  // dirty value after this request completes.
  if ($('#setControlHost')?.dataset.settingsDirty === '1' || $('#setControlPort')?.dataset.settingsDirty === '1') return;
  renderHostSelect($('#setControlHost'),parseListenAddr(state.controlAddr).host);
}

function makeProxyListenerRow(addr){
  const{host,port}=parseListenAddr(addr);
  const row=document.createElement('div');
  row.className='proxy-listener-row row';
  row.classList.add('u-gap-2', 'u-ai-end', 'u-mb-2', 'u-wrap');
  row.innerHTML=`<div style="flex:1;min-width:180px"><label class="hint">Host</label><select class="btn proxy-host-select" aria-label="Proxy listener host" style="width:100%;text-align:left"></select></div>`+
    `<div class="field" style="width:100px;margin-bottom:0"><label class="hint">Port</label><input class="proxy-port-input" inputmode="numeric" aria-label="Proxy listener port" value="${escAttr(port)}" style="width:100%"></div>`+
    `<button type="button" class="btn proxy-listener-del" title="Remove proxy listener" aria-label="Remove proxy listener" style="color:var(--red);padding:3px 10px">×</button>`;
  renderHostSelect(row.querySelector('.proxy-host-select'),host);
  row.querySelector('.proxy-listener-del').onclick=()=>{
    const list=$('#proxyListenersList');
    if(list&&list.querySelectorAll('.proxy-listener-row').length>1){row.remove();markSettingsDirty(list);}
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
  markSettingsDirty(list);
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
  if(seg)seg.querySelectorAll('button').forEach(b=>setSeg(b,b.dataset.mode===mode));
  const manual=$('#deviceProxyManualField');
  if(manual)manual.style.display=mode==='manual'?'':'none';
  if($('#deviceProxyManualHost')&&ep?.manualHost!=null)$('#deviceProxyManualHost').value=ep.manualHost;
}

function snapshotDeviceProxyDirty(){
  const snapshot=snapshotDirtySettings(),fields={};
  if(Object.prototype.hasOwnProperty.call(snapshot.fields,'deviceProxyManualHost'))fields.deviceProxyManualHost=snapshot.fields.deviceProxyManualHost;
  return {fields,proxyListeners:null,hostHeaders:null,deviceProxyMode:snapshot.deviceProxyMode};
}

let deviceProxyLoadEpoch=0;
let deviceProxyMutationPending=false;
async function loadDeviceProxyEndpoint({deferRender=false}={}){
  const epoch=++deviceProxyLoadEpoch;
  try{
    const ep=await api('/api/proxy/device-endpoint');
    if(epoch!==deviceProxyLoadEpoch)return null;
    if(deviceProxyMutationPending)return null;
    if(!deferRender){
      const dirty=snapshotDeviceProxyDirty();
      renderDeviceProxyUI(ep);
      restoreDirtySettings(dirty);
    }
    return ep;
  }catch(e){/* non-fatal */}
  return null;
}

async function saveDeviceProxyEndpoint(){
  const mode=$('#deviceProxyModeSeg')?.querySelector('.on')?.dataset.mode||'auto';
  const host=($('#deviceProxyManualHost')?.value||'').trim();
  deviceProxyMutationPending=true;
  deviceProxyLoadEpoch++;
  invalidateSettingsLoads({reconcile:false});
  try{
    const ep=await api('/api/proxy/device-endpoint',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({mode,host})});
    deviceProxyLoadEpoch++;
    const dirty=snapshotDeviceProxyDirty();
    const currentMode=$('#deviceProxyModeSeg')?.querySelector('.on')?.dataset.mode||'auto';
    const currentHost=($('#deviceProxyManualHost')?.value||'').trim();
    renderDeviceProxyUI(ep);
    if(currentMode!==mode||currentHost!==host)restoreDirtySettings(dirty);
    else clearSettingsDirty(['deviceProxyManualHost','deviceProxyModeSeg']);
    toast('device proxy → '+ep.endpoint+(ep.mode==='auto'?' (auto)':' (manual)'));
  }catch(e){
    deviceProxyLoadEpoch++;
    toast(e.message);
  }finally{
    deviceProxyMutationPending=false;
    invalidateSettingsLoads({reconcile:true});
  }
}

if($('#deviceProxyModeSeg'))$('#deviceProxyModeSeg').querySelectorAll('button').forEach(b=>{
  b.onclick=()=>{
    $('#deviceProxyModeSeg').querySelectorAll('button').forEach(x=>setSeg(x,x===b));
    markSettingsDirty($('#deviceProxyModeSeg'));
    const manual=$('#deviceProxyManualField');
    if(manual)manual.style.display=b.dataset.mode==='manual'?'':'none';
  };
});
if($('#saveDeviceProxyBtn'))$('#saveDeviceProxyBtn').onclick=()=>runSettingsAction($('#saveDeviceProxyBtn'),saveDeviceProxyEndpoint);
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
  if(row)setTimeout(()=>{row.scrollIntoView({block:'nearest',behavior:prefersReducedMotion()?'auto':'smooth'});row.querySelector('.proxy-host-select')?.focus();},50);
}

function syncSettingsNavA11y(active) {
  $$('#setNav button[data-sec]').forEach(button => {
    const sec = document.querySelector('.set-sec[data-sec="'+button.dataset.sec+'"]');
    if (sec) {
      if (!sec.id) sec.id = 'settings-section-'+button.dataset.sec;
      button.setAttribute('aria-controls', sec.id);
    }
    // Section navigation is a page-navigation list, not a toggle group. Keep
    // stale markup/runtime state from exposing the wrong ARIA model.
    button.removeAttribute('aria-pressed');
    button.setAttribute('aria-current', button === active ? 'page' : 'false');
  });
}

// On phones the section list and the section page are two views of one split;
// choosing a section pushes its page (one guarded history entry, Back returns).
let settingsSplit=null,settingsNavSilent=false;
try{
  const wrap=document.querySelector('.settings-wrap');
  if(wrap)settingsSplit=createSplitPane({root:wrap,list:$('#setNav'),detail:document.querySelector('.settings-body'),key:'settingsSplit',scopeKey:projectStorageKey,min:[200,360],default:22,stackBelow:720,label:'Resize settings navigation',backLabel:'Sections'});
}catch(e){settingsSplit=null;}

$$('#setNav button[data-sec]').forEach(b=>b.onclick=()=>{
  $$('#setNav button[data-sec]').forEach(x=>x.classList.toggle('on',x===b));
  $$('.set-sec').forEach(s=>{s.hidden=s.dataset.sec!==b.dataset.sec;});
  syncSettingsNavA11y(b);
  try{localStorage.setItem('setSec',b.dataset.sec);}catch(e){}
  // lazy-load retention stats the first time the project section is opened
  if(b.dataset.sec==='project'){retentionLoaded=true;loadRetention();}
  if(b.dataset.sec==='tls'){import('./tlsdiag.js').then(m=>m.loadTrafficDiagnosis());}
  if(b.dataset.sec==='devices'){loadAndroid();loadIOS();loadIOSSsh();}
  if(b.dataset.sec==='api'&&!apiLoaded){apiLoaded=true;import('./apipanel.js').then(m=>{m.loadApiKeys();m.loadReference();m.loadMCP();});}
  if(settingsSplit&&!settingsNavSilent&&settingsSplit.mode()==='stack')settingsSplit.showDetail(b);
});
syncSettingsNavA11y(document.querySelector('#setNav button.on[data-sec]')||document.querySelector('#setNav button[data-sec]'));

// Settings search — filter the left nav to sections whose label or body text
// matches the query, so options are discoverable without knowing which group
// they live in. Read-only: it never mutates section markup.
(function wireSettingsSearch(){
  const box=$('#setSearch'); if(!box) return;
  const empty=$('#setNavEmpty');
  // Cache each nav button's label and its section's text; matching and the
  // highlight ranges come from the pure model so they are covered by node tests.
  const entries=$$('#setNav button[data-sec]').map(b=>{
    const sec=document.querySelector('.set-sec[data-sec="'+b.dataset.sec+'"]');
    return {btn:b,label:b.textContent||'',id:b.dataset.sec,text:sec?sec.textContent||'':''};
  });
  const groups=$$('#setNav .settings-nav-group');
  // Highlight matches in a nav label with <mark> nodes (textContent only).
  function paintLabel(btn,label,query){
    const ranges=highlightRanges(label,query);
    btn.textContent='';
    if(!ranges.length){btn.textContent=label;return;}
    let at=0;
    ranges.forEach(([a,z])=>{
      if(a>at)btn.appendChild(document.createTextNode(label.slice(at,a)));
      const m=document.createElement('mark');m.textContent=label.slice(a,z);btn.appendChild(m);at=z;
    });
    if(at<label.length)btn.appendChild(document.createTextNode(label.slice(at)));
  }
  box.oninput=()=>{
    const q=box.value.trim();
    const rows=matchSections(entries.map(e=>({id:e.id,label:e.label,text:e.text})),q);
    let firstVisible=null,anyHidden=false;
    rows.forEach((r,i)=>{
      const e=entries[i];
      e.btn.hidden=!r.hit;
      paintLabel(e.btn,e.label,r.hit?q:'');
      if(r.hit&&!firstVisible)firstVisible=e.btn; else if(!r.hit)anyHidden=true;
    });
    // Hide a group's eyebrow label too when every button in that group is
    // filtered out — otherwise an orphaned "NETWORK"-style label with no
    // buttons under it would linger during a search.
    groups.forEach(g=>{g.hidden=!g.querySelector('button[data-sec]:not([hidden])');});
    const summary=searchSummary(rows);
    if(empty){empty.textContent=summary;empty.hidden=!summary;}
    // If the query hid the active section, jump to the first remaining match
    // (without pushing a phone page while the operator is still typing).
    if(q&&anyHidden&&firstVisible&&!$$('#setNav button.on').some(b=>!b.hidden)){settingsNavSilent=true;try{firstVisible.click();}finally{settingsNavSilent=false;}}
  };
  // Escape clears the filter.
  box.onkeydown=e=>{if(e.key==='Escape'){box.value='';box.oninput();box.blur();}};
})();

/* ---- first-run checklist (Project & data) ---- */
{const checklistHost=$('#settingsChecklistMount');if(checklistHost)mountChecklist(checklistHost);}

/* ---- settings ---- */
let apiLoaded=false;
let projectDataLoaded=false;
let projectModalDataLoaded=false;
let projectLoadEpoch=0;
let projectModalLoadEpoch=0;
let projectPathFlavor='posix';

function setProjectControlsDisabled(disabled,title=''){
  ['projSelect','projSwitchBtn','projNewBtn'].forEach(id=>{
    const el=$('#'+id);if(!el)return;
    el.disabled=!!disabled;
    if(title)el.title=title;else el.removeAttribute('title');
  });
}
function markProjectDataStale(stale){
  const sel=$('#projSelect');
  if(!sel)return;
  if(stale){
    sel.dataset.stale='true';
    sel.setAttribute('aria-label','Saved project (stale — retry to refresh)');
  }else{
    sel.removeAttribute('data-stale');
    sel.setAttribute('aria-label','Saved project');
  }
}
function setProjectModalActionsDisabled(disabled){
  const b=$('#pmNewBtn');if(!b)return;
  b.disabled=!!disabled;
  if(disabled)b.title='Project data is stale — retry before creating or opening a project';
  else b.removeAttribute('title');
}

function settingsLoadState(){
  let el=$('#settingsLoadState');if(el)return el;
  el=document.createElement('div');el.id='settingsLoadState';el.className='tls-diag-banner';
  el.setAttribute('role','status');
  el.setAttribute('aria-live','polite');
  const body=document.querySelector('#panel-settings .settings-body');if(body)body.prepend(el);
  return el;
}
export function setOriginTLSVerify(on){
  const mode=$('#originTLSVerifyMode');
  if(mode)mode.value=on?'strict':'compatible';
  const summary=$('#originTLSModeSummary');
  if(summary)summary.textContent=on
    ?'Strict mode rejects expired, untrusted, and hostname-mismatched origin certificates. Add only known test hosts as exceptions below.'
    :'Compatibility mode accepts self-signed, expired, and hostname-mismatched origin certificates so authorized test environments remain reachable.';
}
$('#originTLSVerifyMode')&&($('#originTLSVerifyMode').onchange=async()=>{
  const mode=$('#originTLSVerifyMode');
  const on=mode.value==='strict';
  return saveBooleanSetting('originTLSVerify',on,{
    control:mode,
    success:current=>{
      clearSettingsDirty(['originTLSVerifyMode']);
      setOriginTLSVerify(current);
      toast(current?'Strict origin certificate verification enabled':'Compatibility mode enabled');
    },
    failure:e=>{
      toast('origin TLS: '+e.message);
      clearSettingsDirty(['originTLSVerifyMode']);
      loadSettings();
    },
  });
});
export async function loadSettings(){const epoch=++settingsLoadEpoch;const settingsRevision=settingsMutationRevision;const loadState=settingsLoadState();if(loadState){loadState.style.display='block';loadState.textContent='Loading Settings…';}
  try{const s=await api('/api/settings');
  if(epoch!==settingsLoadEpoch)return;
  const networkHostsResult=await loadNetworkHosts({deferRender:true});
  if(epoch!==settingsLoadEpoch)return;
  const deviceProxyEndpoint=await loadDeviceProxyEndpoint({deferRender:true});
  if(epoch!==settingsLoadEpoch)return;
  const dirty=snapshotDirtySettings();
  networkHosts=networkHostsResult;
  $('#setControlHost')?.setAttribute('aria-label','Control UI bind host');
  $('#setControlPort')?.setAttribute('aria-label','Control UI bind port');
  setOriginTLSVerify(settingsMutationValue('originTLSVerify',!!s.originTLSVerify,settingsRevision));state.proxyAddr=s.proxyAddr;
  if(!deviceProxyMutationPending){state.deviceProxy=s.deviceProxy||s.proxyAddr;state.deviceProxyMode=s.deviceProxyMode||'auto';}
  state.controlAddr=s.controlAddr||'127.0.0.1:9966';
  renderProxyListeners(s.proxyAddrs||[s.proxyAddr]);
  if($('#setAddr'))$('#setAddr').value=s.proxyAddr;
  $('#proxyAddr').textContent=s.proxyAddr;
  if(deviceProxyEndpoint)renderDeviceProxyUI(deviceProxyEndpoint);
  $('#controlAddr').textContent=state.controlAddr;
  const c=parseListenAddr(state.controlAddr);
  if($('#setControlPort'))$('#setControlPort').value=c.port;
  renderHostSelect($('#setControlHost'),c.host);
  if($('#setControlAddr'))$('#setControlAddr').value=state.controlAddr;
  const tun=$('#oobModalTunnelCmd');if(tun)tun.textContent='cloudflared tunnel --url http://'+state.controlAddr;
   if($('#setUpstreamCA')&&document.activeElement!==$('#setUpstreamCA'))$('#setUpstreamCA').value=s.upstreamProxyCA||'';
   parseUpstreamProxyURL(s.upstreamProxy||'');
  state.oobEnabled=settingsMutationValue('oobEnabled',!!s.oobEnabled,settingsRevision);
  if($('#setOobEnabled'))$('#setOobEnabled').checked=state.oobEnabled;
  if($('#capScopeToggle'))setCapScope(settingsMutationValue('captureScopeOnly',!!s.captureScopeOnly,settingsRevision));
  if($('#suppressTelemetryToggle'))setSuppressTelemetry(settingsMutationValue('suppressBrowserTelemetry',s.suppressBrowserTelemetry!==false,settingsRevision));
  if($('#suppressAndroidTelemetryToggle'))setSuppressAndroidTelemetry(settingsMutationValue('suppressAndroidTelemetry',s.suppressAndroidTelemetry!==false,settingsRevision));
  if($('#invisibleProxyToggle'))setInvisibleProxy(settingsMutationValue('invisibleProxy',!!s.invisibleProxy,settingsRevision));
  state.findingsUIEditing=!!s.findingsUIEditing;
  if($('#findingsUIEditingToggle'))setFindingsUIEditing(settingsMutationValue('findingsUIEditing',!!s.findingsUIEditing,settingsRevision));
  if($('#proxyAuthToggle')?.dataset.settingsDirty!=='1')setProxyAuth(!!s.proxyAuthEnabled);
  if($('#proxyAuthUser')&&document.activeElement!==$('#proxyAuthUser')&&$('#proxyAuthUser').dataset.settingsDirty!=='1')$('#proxyAuthUser').value=s.proxyAuthUser||'';
  if($('#proxyAuthPassword')&&document.activeElement!==$('#proxyAuthPassword')&&$('#proxyAuthPassword').dataset.settingsDirty!=='1')$('#proxyAuthPassword').value=s.proxyAuthPassword||'';
  if($('#autoBypassToggle'))setAutoBypass(settingsMutationValue('autoBypassOnPinFailure',!!s.autoBypassOnPinFailure,settingsRevision));
  // Don't clobber the list while the operator is mid-edit (a live settings.update
  // — e.g. an auto-bypass addition — must not overwrite unsaved typing).
   const bl=$('#tlsBypassList');
   if(bl&&document.activeElement!==bl){bl.value=(s.tlsBypassHosts||[]).join('\n');updateBypassCount();}
   const ol=$('#originTLSVerifyBypassList');
   if(ol&&document.activeElement!==ol){ol.value=(s.originTLSVerifyBypassHosts||[]).join('\n');updateOriginTLSVerifyBypassCount();}

  applyOobDisabledUI();
  state.intercept.enabled=s.interceptEnabled;
  restoreDirtySettings(dirty);
  restoreDirtySettingsDerived(dirty);
  if(loadState)loadState.style.display='none';
  window.dispatchEvent(new CustomEvent('interseptor:findings-ui-editing'));}
  catch(e){if(epoch!==settingsLoadEpoch)return;renderLoadError(loadState,'Settings',e,loadSettings,true);}
  finally{if(epoch===settingsLoadEpoch&&loadState&&loadState.textContent==='Loading Settings…')loadState.style.display='none';}}

export function applyOobDisabledUI(){
  const on=!!state.oobEnabled;
  document.documentElement.classList.toggle('oob-disabled',!on);
  const hint=$('#oobDisabledHint');
  if(hint)hint.style.display=on?'none':'block';
  if(!on&&$('#oobModal')&&$('#oobModal').style.display==='flex')closeModal($('#oobModal'));
}

$('#setOobEnabled')&&($('#setOobEnabled').onchange=async()=>{
  const control=$('#setOobEnabled');
  const enabled=control.checked;
  return saveBooleanSetting('oobEnabled',enabled,{
    control,
    success:current=>{
      state.oobEnabled=current;
      control.checked=current;
      clearSettingsDirty(['setOobEnabled']);
      applyOobDisabledUI();
      toast(current?'OOB catcher enabled':'OOB catcher disabled');
    },
    failure:e=>{toast(e.message);clearSettingsDirty(['setOobEnabled']);loadSettings();},
  });
});

export function setCapScope(on){const b=$('#capScopeToggle');if(!b)return;b.classList.toggle('on',on);b.setAttribute('aria-pressed',on?'true':'false');b.textContent=on?'Saving in-scope only':'Saving all traffic';}
$('#capScopeToggle')&&($('#capScopeToggle').onclick=async()=>{
  const control=$('#capScopeToggle');
  const on=!control.classList.contains('on');
  return saveBooleanSetting('captureScopeOnly',on,{control,lockControl:true,
    success:current=>{setCapScope(current);toast(current?'Now saving only in-scope traffic':'Now saving all traffic');},
    failure:e=>{toast('capture: '+e.message);loadSettings();},
  });
});
export function setSuppressTelemetry(on){const b=$('#suppressTelemetryToggle');if(!b)return;b.classList.toggle('on',on);b.setAttribute('aria-pressed',on?'true':'false');b.textContent=on?'Suppressing browser background traffic':'Capturing browser background traffic';}
$('#suppressTelemetryToggle')&&($('#suppressTelemetryToggle').onclick=async()=>{
  const control=$('#suppressTelemetryToggle');
  const on=!control.classList.contains('on');
  return saveBooleanSetting('suppressBrowserTelemetry',on,{control,lockControl:true,
    success:current=>{setSuppressTelemetry(current);toast(current?'New browser background traffic will be forwarded without capture':'Browser background suppression disabled; other capture policies still apply');},
    failure:e=>{toast('browser background traffic: '+e.message);loadSettings();},
  });
});
export function setSuppressAndroidTelemetry(on){const b=$('#suppressAndroidTelemetryToggle');if(!b)return;b.classList.toggle('on',on);b.setAttribute('aria-pressed',on?'true':'false');b.textContent=on?'Suppressing Android telemetry':'Allowing Android telemetry';}
$('#suppressAndroidTelemetryToggle')&&($('#suppressAndroidTelemetryToggle').onclick=async()=>{
  const control=$('#suppressAndroidTelemetryToggle');
  const on=!control.classList.contains('on');
  return saveBooleanSetting('suppressAndroidTelemetry',on,{control,lockControl:true,
    success:current=>{setSuppressAndroidTelemetry(current);toast(current?'Android telemetry suppressed':'Android telemetry suppression disabled; other capture policies still apply');},
    failure:e=>{toast('android telemetry: '+e.message);loadSettings();},
  });
});
export function setFindingsUIEditing(on){state.findingsUIEditing=on;const b=$('#findingsUIEditingToggle');if(!b)return;b.classList.toggle('on',on);b.setAttribute('aria-pressed',on?'true':'false');b.textContent=on?'Findings editing is on':'Findings editing is off';}
export function setInvisibleProxy(on){const b=$('#invisibleProxyToggle');if(!b)return;b.classList.toggle('on',on);b.setAttribute('aria-pressed',on?'true':'false');b.textContent=on?'Invisible proxy is on':'Invisible proxy is off';}
export function setProxyAuth(on){
  const b=$('#proxyAuthToggle');
  if(b){b.classList.toggle('on',on);b.setAttribute('aria-pressed',on?'true':'false');b.textContent=on?'Proxy authentication is on':'Proxy authentication is off';}
  const hidden=$('#proxyAuthEnabled');
  if(hidden)hidden.value=on?'1':'0';
  const fields=$('#proxyAuthFields');
  if(fields)fields.hidden=!on;
}
$('#proxyAuthToggle')&&($('#proxyAuthToggle').onclick=()=>{
  setProxyAuth($('#proxyAuthToggle').getAttribute('aria-pressed')!=='true');
  markSettingsDirty($('#proxyAuthEnabled'));
});
$('#saveProxyAuthBtn')&&($('#saveProxyAuthBtn').onclick=()=>runSettingsAction($('#saveProxyAuthBtn'),async()=>{
  const enabled=$('#proxyAuthEnabled')?.value==='1';
  const user=($('#proxyAuthUser')?.value||'').trim();
  const password=$('#proxyAuthPassword')?.value||'';
  if(enabled&&(!user||!password)){toast('Proxy authentication needs a username and password');return;}
  const userEl=$('#proxyAuthUser'),passEl=$('#proxyAuthPassword'),enabledEl=$('#proxyAuthEnabled');
  const generation=settingsEditGeneration(enabledEl);
  try{
    await saveSettingsPatch({proxyAuthEnabled:enabled,proxyAuthUser:user,proxyAuthPassword:password});
    if(settingsEditOwned(enabledEl,generation)&&userEl?.value.trim()===user&&passEl?.value===password){
      clearSettingsDirty(['proxyAuthEnabled','proxyAuthUser','proxyAuthPassword']);
    }
    toast(enabled?'Proxy authentication enabled':'Proxy authentication disabled');
  }catch(e){toast('proxy authentication: '+e.message);}
}));
$('#invisibleProxyToggle')&&($('#invisibleProxyToggle').onclick=async()=>{
  const control=$('#invisibleProxyToggle');
  const on=!control.classList.contains('on');
  return saveBooleanSetting('invisibleProxy',on,{control,lockControl:true,
    success:current=>{setInvisibleProxy(current);toast(current?'Invisible proxy enabled':'Invisible proxy disabled');},
    failure:e=>{toast('invisible: '+e.message);loadSettings();},
  });
});

$('#findingsUIEditingToggle')&&($('#findingsUIEditingToggle').onclick=async()=>{
  const control=$('#findingsUIEditingToggle');
  const on=!control.classList.contains('on');
  return saveBooleanSetting('findingsUIEditing',on,{control,lockControl:true,
    success:current=>{setFindingsUIEditing(current);window.dispatchEvent(new CustomEvent('interseptor:findings-ui-editing'));toast(current?'Findings editing enabled':'Findings editing disabled');},
    failure:e=>{toast('findings editing: '+e.message);loadSettings();},
  });
});

// ---- TLS passthrough / SSL-pinning bypass ----
export function setAutoBypass(on){const b=$('#autoBypassToggle');if(!b)return;b.classList.toggle('on',on);b.setAttribute('aria-pressed',on?'true':'false');b.textContent=on?'Auto-bypass on pinning failure is on':'Auto-bypass on pinning failure is off';}
const settingsListWriteTails=new Map();
function queueSettingsListWrite(key,action){
  const previous=settingsListWriteTails.get(key)||Promise.resolve();
  const current=previous.catch(()=>{}).then(action);
  settingsListWriteTails.set(key,current);
  return current.finally(()=>{if(settingsListWriteTails.get(key)===current)settingsListWriteTails.delete(key);});
}
function bypassHostsFromText(){return ($('#tlsBypassList')?.value||'').split(/[\n,]/).map(x=>x.trim().toLowerCase()).filter((v,i,a)=>v&&a.indexOf(v)===i);}
function updateBypassCount(){const el=$('#tlsBypassCount');if(el)el.textContent=(n=>n?n+' domain'+(n>1?'s':'')+' passed through':'No passthrough domains')(bypassHostsFromText().length);}
$('#tlsBypassList')&&($('#tlsBypassList').addEventListener('input',updateBypassCount));
$('#autoBypassToggle')&&($('#autoBypassToggle').onclick=async()=>{
  const control=$('#autoBypassToggle');
  const on=!control.classList.contains('on');
  return saveBooleanSetting('autoBypassOnPinFailure',on,{control,lockControl:true,
    success:current=>{setAutoBypass(current);toast(current?'Auto-bypass on pinning failure enabled':'Auto-bypass disabled');},
    failure:e=>{toast('auto-bypass: '+e.message);loadSettings();},
  });
});
$('#tlsBypassSave')&&($('#tlsBypassSave').onclick=()=>runSettingsAction($('#tlsBypassSave'),async()=>{
  const list=$('#tlsBypassList'),generation=settingsEditGeneration(list),submittedValue=list?.value||'';
  const hosts=bypassHostsFromText();
  try{const savedHosts=await queueSettingsListWrite('tlsBypassHosts',async()=>{
      const next=hosts;
      await saveSettingsPatch({tlsBypassHosts:next});
      return next;
    });
    if(settingsEditOwned(list,generation,submittedValue)){list.value=savedHosts.join('\n');clearSettingsDirty(['tlsBypassList']);}updateBypassCount();
    toast(savedHosts.length?('Passing through '+savedHosts.length+' domain'+(savedHosts.length>1?'s':'')):'Passthrough list cleared');}
  catch(e){toast('passthrough: '+e.message);}
}));
export function addTLSBypassHosts(hosts){
  const additions=[...new Set((hosts||[]).map(h=>String(h).trim().toLowerCase()).filter(Boolean))];
  if(!additions.length)return Promise.resolve([]);
  return queueSettingsListWrite('tlsBypassHosts',async()=>{
    const current=await api('/api/settings');
    const merged=[...new Set([...(current.tlsBypassHosts||[]),...additions])];
    await saveSettingsPatch({tlsBypassHosts:merged});
    return merged;
  });
}
function originTLSVerifyBypassHostsFromText(){return ($('#originTLSVerifyBypassList')?.value||'').split(/[\n,]/).map(x=>x.trim().toLowerCase()).filter((v,i,a)=>v&&a.indexOf(v)===i);}
function updateOriginTLSVerifyBypassCount(){const el=$('#originTLSVerifyBypassCount');if(el)el.textContent=(n=>n?n+' verification exception'+(n>1?'s':''):'No verification exceptions')(originTLSVerifyBypassHostsFromText().length);}
$('#originTLSVerifyBypassList')&&($('#originTLSVerifyBypassList').addEventListener('input',updateOriginTLSVerifyBypassCount));
async function saveOriginTLSVerifyExceptions(hosts,message,{merge=false}={}){
  const list=$('#originTLSVerifyBypassList'),generation=settingsEditGeneration(list),submittedValue=list?.value||'';
  try{const savedHosts=await queueSettingsListWrite('originTLSVerifyBypassHosts',async()=>{
      let next=hosts;
      if(merge){
        const current=await api('/api/settings');
        next=[...new Set([...(current.originTLSVerifyBypassHosts||[]),...hosts])];
      }
      await saveSettingsPatch({originTLSVerifyBypassHosts:next});
      return next;
    });
    if(settingsEditOwned(list,generation,submittedValue)){list.value=savedHosts.join('\n');clearSettingsDirty(['originTLSVerifyBypassList']);}updateOriginTLSVerifyBypassCount();
    toast(message||(savedHosts.length?('Saved '+savedHosts.length+' origin verification exception'+(savedHosts.length>1?'s':'')):'Verification exceptions cleared'));
    return true;
  }catch(e){toast('origin TLS: '+e.message,'error');return false;}
}
function normalizeOriginExceptionInput(raw){
  let value=String(raw||'').trim().toLowerCase();
  if(!value)return '';
  if(value.startsWith('*.'))return /^\*\.[a-z0-9.-]+$/.test(value)?value:'';
  try{
    const parsed=new URL(value.includes('://')?value:'https://'+value);
    value=parsed.hostname.replace(/^\[|\]$/g,'');
  }catch(_){return '';}
  return value;
}
function selectedOriginHost(){
  const flow=state.flows.find(f=>f.id===state.selId)||state.detail;
  return normalizeOriginExceptionInput(flow?.host||'');
}
async function addOriginTLSVerifyException(raw){
  const host=normalizeOriginExceptionInput(raw);
  if(!host){toast('Enter a valid host, IP address, or *.example.com pattern','error');return false;}
  const hosts=originTLSVerifyBypassHostsFromText();
  if(!hosts.includes(host))hosts.push(host);
  const saved=await saveOriginTLSVerifyExceptions(hosts,'Origin TLS exception added for '+host,{merge:true});
  if(saved&&$('#originTLSVerifyBypassHost'))$('#originTLSVerifyBypassHost').value='';
  return saved;
}
$('#originTLSVerifyBypassAdd')&&($('#originTLSVerifyBypassAdd').onclick=()=>addOriginTLSVerifyException($('#originTLSVerifyBypassHost')?.value));
$('#originTLSVerifyBypassHost')&&($('#originTLSVerifyBypassHost').onkeydown=e=>{if(e.key==='Enter'){e.preventDefault();addOriginTLSVerifyException(e.currentTarget.value);}});
$('#originTLSVerifyBypassSelected')&&($('#originTLSVerifyBypassSelected').onclick=()=>{
  const host=selectedOriginHost();
  if(!host){toast('Select a request in History first','error');return;}
  addOriginTLSVerifyException(host);
});
$('#originTLSVerifyBypassSave')&&($('#originTLSVerifyBypassSave').onclick=()=>runSettingsAction($('#originTLSVerifyBypassSave'),()=>saveOriginTLSVerifyExceptions(originTLSVerifyBypassHostsFromText())));

const upstreamDefaultPorts={http:'80',https:'443',socks5:'1080',socks5h:'1080'};
const upstreamProxyFieldIds=['setUpstreamScheme','setUpstreamHost','setUpstreamPort','setUpstreamUser','setUpstreamPassword','setUpstreamCA'];
function upstreamProxyFieldSnapshot(){
  const snapshot={};
  upstreamProxyFieldIds.forEach(id=>{
    const el=$('#'+id);
    if(el)snapshot[id]={generation:settingsEditGeneration(el),value:el.value};
  });
  return snapshot;
}
function renderUpstreamProxyFields(scheme,fillDefaultPort=false){
  scheme=scheme||$('#setUpstreamScheme')?.value||'direct';
  const help=$('#upstreamSchemeHelp');
  if(help){help.hidden=scheme!=='socks5'&&scheme!=='socks5h';help.textContent=scheme==='socks5h'?'The upstream proxy resolves target hostnames.':'Interseptor resolves target hostnames.';}
  const fields=$('#upstreamProxyFields');if(fields)fields.hidden=scheme==='direct';
  const advanced=$('#upstreamProxyAdvanced');if(advanced)advanced.hidden=scheme!=='https';
  const ca=$('#setUpstreamCAWrap');if(ca)ca.hidden=scheme!=='https';
  const port=$('#setUpstreamPort');
  if(port){port.placeholder=upstreamDefaultPorts[scheme]||'';if(fillDefaultPort&&scheme!=='direct'&&!port.value)port.value=upstreamDefaultPorts[scheme]||'';}
  const summary=$('#upstreamProxySummary');
  if(summary){
    if(scheme==='direct')summary.textContent='Direct connection · no upstream proxy';
    else{
      const host=$('#setUpstreamHost')?.value.trim()||'host required';
      const p=port?.value.trim()||upstreamDefaultPorts[scheme]||'port required';
      const dns=scheme==='socks5h'?' · remote DNS':scheme==='socks5'?' · local DNS':'';
      summary.textContent=scheme.toUpperCase()+' · '+host+':'+p+dns;
    }
  }
}
function decodeURLCredential(value){try{return decodeURIComponent(value);}catch(_){return value;}}
function upstreamProxyValues(raw){
  const values={setUpstreamScheme:'direct',setUpstreamHost:'',setUpstreamPort:'',setUpstreamUser:'',setUpstreamPassword:'',invalid:false};
  if(!raw)return values;
  try{
    const parsed=new URL(raw),mode=parsed.protocol.replace(':','').toLowerCase();
    if(!upstreamDefaultPorts[mode])throw new Error('unsupported mode');
    values.setUpstreamScheme=mode;
    values.setUpstreamHost=parsed.hostname.replace(/^\[|\]$/g,'');
    values.setUpstreamPort=parsed.port||upstreamDefaultPorts[mode];
    values.setUpstreamUser=decodeURLCredential(parsed.username);
    values.setUpstreamPassword=decodeURLCredential(parsed.password);
  }catch(_){values.invalid=true;}
  return values;
}
function parseUpstreamProxyURL(raw){
  const values=upstreamProxyValues(raw);
  upstreamProxyFieldIds.slice(0,5).forEach(id=>{const el=$('#'+id);if(el)el.value=values[id]||'';});
  renderUpstreamProxyFields(values.setUpstreamScheme);
  if(values.invalid){const summary=$('#upstreamProxySummary');if(summary)summary.textContent='Saved proxy URL is invalid — choose a connection type and replace it';}
}
function buildUpstreamProxyURL(){
  const scheme=$('#setUpstreamScheme')?.value||'direct';
  if(scheme==='direct')return '';
  let host=$('#setUpstreamHost')?.value.trim()||'';
  const port=$('#setUpstreamPort')?.value.trim()||upstreamDefaultPorts[scheme];
  const user=$('#setUpstreamUser')?.value||'',pass=$('#setUpstreamPassword')?.value||'';
  if(!host)throw new Error('Proxy host is required');
  if(/[\s\/@?#]/.test(host))throw new Error('Proxy host must be a hostname or IP address without a URL path');
  if(!/^\d+$/.test(port)||Number(port)<1||Number(port)>65535)throw new Error('Proxy port must be between 1 and 65535');
  if(host.includes(':')&&!host.startsWith('['))host='['+host+']';
  let parsed;
  try{parsed=new URL(scheme+'://'+host+':'+port);}catch(_){throw new Error('Proxy host is invalid');}
  if(user||pass){parsed.username=user;parsed.password=pass;}
  return parsed.toString();
}
$('#setUpstreamScheme')&&($('#setUpstreamScheme').onchange=e=>renderUpstreamProxyFields(e.currentTarget.value,true));
['setUpstreamHost','setUpstreamPort'].forEach(id=>{$('#'+id)?.addEventListener('input',()=>renderUpstreamProxyFields());});
$('#saveUpstreamBtn')&&($('#saveUpstreamBtn').onclick=()=>runSettingsAction($('#saveUpstreamBtn'),async()=>{
  const submitted=upstreamProxyFieldSnapshot();
  const upstreamProxyCA=$('#setUpstreamCA')?.value.trim()||'';
  try{const upstreamProxy=buildUpstreamProxyURL();const acknowledged=await saveSettingsPatch({upstreamProxy,upstreamProxyCA});
    const saved=upstreamProxyValues(typeof acknowledged?.upstreamProxy==='string'?acknowledged.upstreamProxy:upstreamProxy);
    const savedCA=typeof acknowledged?.upstreamProxyCA==='string'?acknowledged.upstreamProxyCA:upstreamProxyCA;
    upstreamProxyFieldIds.forEach(id=>{
      const el=$('#'+id),snapshot=submitted[id];
      if(!el||!snapshot)return;
      if(settingsEditOwned(el,snapshot.generation,snapshot.value)){
        el.value=id==='setUpstreamCA'?savedCA:(saved[id]||'');
        clearSettingsDirty([id]);
      }
    });
    renderUpstreamProxyFields($('#setUpstreamScheme')?.value||'direct');
    toast(upstreamProxy?'Upstream proxy saved':'Direct connection saved');}catch(e){toast(e.message,'error');}
}));

let sessionLoaded=false;
let sessionLoadEpoch=0;
export async function loadSession(){const epoch=++sessionLoadEpoch;const loadState=$('#sessionLoadState');if(loadState){loadState.style.display='block';loadState.textContent='Loading Session settings…';}
try{const s=await api('/api/session');
  if(epoch!==sessionLoadEpoch)return;
  const dirty=snapshotDirtySettings();
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
  restoreDirtySettings(dirty);
  sessionLoaded=true;if(loadState)loadState.style.display='none';
}catch(e){if(epoch===sessionLoadEpoch)renderLoadError(loadState,'Session settings',e,loadSession,sessionLoaded);}}
function loginMacroBody(){
  return {enabled:$('#loginMacroOn').checked,target:$('#loginMacroTarget').value.trim(),request:$('#loginMacroReq').value,
    refreshSecs:parseInt(($('#loginMacroRefresh')||{}).value,10)||0,reauthOn401:!!($('#loginMacro401')||{}).checked};
}
const SESSION_DIRTY_FIELDS=['setSessionOn','setSessionUnscoped','setSessionHeaders','macroOn','macroReq','macroTarget','macroExtract','macroMode','macroName','loginMacroOn','loginMacroReq','loginMacroTarget','loginMacroRefresh','loginMacro401'];
// Only persisted settings count as drafts. Search, section navigation and the
// project-name form also emit input events, but must not prevent switching.
function hasUnsavedSettingsFields(){
  return [...SESSION_DIRTY_FIELDS,...upstreamProxyFieldIds,'proxyListenersList','deviceProxyModeSeg','deviceProxyManualHost',
    'setControlHost','setControlPort','setControlAddr','tlsBypassList','originTLSVerifyBypassList','originTLSVerifyMode',
    'setOobEnabled','hostHdrList','retMaxAge','retMaxFlows','proxyAuthEnabled','proxyAuthUser','proxyAuthPassword'].some(id=>$('#'+id)?.dataset.settingsDirty==='1');
}
function sessionFormPayload(){
  const macro={enabled:$('#macroOn').checked,target:$('#macroTarget').value.trim(),request:$('#macroReq').value,extract:$('#macroExtract').value.trim(),injectMode:$('#macroMode').value,injectName:$('#macroName').value.trim()};
  return {enabled:$('#setSessionOn').checked,unscoped:!!($('#setSessionUnscoped')&&$('#setSessionUnscoped').checked),headers:$('#setSessionHeaders').value,macro,loginMacro:loginMacroBody(),hostHeaders:collectHostHeaders()};
}
function sessionFieldValues(body){
  return {setSessionOn:body.enabled,setSessionUnscoped:body.unscoped,setSessionHeaders:body.headers,macroOn:body.macro.enabled,macroReq:body.macro.request,macroTarget:body.macro.target,macroExtract:body.macro.extract,macroMode:body.macro.injectMode,macroName:body.macro.injectName,loginMacroOn:body.loginMacro.enabled,loginMacroReq:body.loginMacro.request,loginMacroTarget:body.loginMacro.target,loginMacroRefresh:body.loginMacro.refreshSecs,loginMacro401:body.loginMacro.reauthOn401};
}
function clearAcknowledgedSessionDirty(snapshot){
  const current=sessionFormPayload(),ack=sessionFieldValues(snapshot),now=sessionFieldValues(current);
  SESSION_DIRTY_FIELDS.forEach(id=>{if(Object.is(ack[id],now[id]))$('#'+id)?.removeAttribute('data-settings-dirty');});
  if(JSON.stringify(snapshot.hostHeaders)===JSON.stringify(current.hostHeaders))$('#hostHdrList')?.removeAttribute('data-settings-dirty');
}
let sessionMutationTail=Promise.resolve();
function queueSessionMutation(work){
  const mutation=sessionMutationTail.catch(()=>{}).then(work);
  sessionMutationTail=mutation;
  return mutation;
}
function writeSessionAll(body){
  return api('/api/session',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify(body)});
}
function saveSessionAll(body=sessionFormPayload()){
  return queueSessionMutation(()=>writeSessionAll(body));
}
function runLoginMacroWithSession(body,onSaveAcknowledged){
  return queueSessionMutation(async()=>{
    await writeSessionAll(body);
    if(onSaveAcknowledged)onSaveAcknowledged();
    return api('/api/session/login/run',{method:'POST'});
  });
}
if($('#saveSessionBtn'))$('#saveSessionBtn').onclick=()=>runSettingsAction($('#saveSessionBtn'),async()=>{
  try{
    const submitted=sessionFormPayload();
    await saveSessionAll(submitted);
    sessionLoadEpoch++;
    clearAcknowledgedSessionDirty(submitted);
    // Surface macro completeness (previously on the now-removed per-macro Save buttons).
    const on=submitted.macro.enabled;
    const complete=submitted.macro.target&&submitted.macro.request.trim()&&submitted.macro.extract&&submitted.macro.injectName;
    let msg='session saved';
    if(on) msg=complete?'session saved · token macro on — fires before each send':'session saved — set target, request, extract & inject-name for the token macro to fire';
    if(submitted.loginMacro.enabled) msg+=' · login macro on';
    toast(msg);loadSession();
  }catch(e){toast(e.message);}
});
if($('#loginMacroRun'))$('#loginMacroRun').onclick=()=>runSettingsAction($('#loginMacroRun'),async()=>{
  let saveAcknowledged=false;
  try{
    const submitted=sessionFormPayload();
    const r=await runLoginMacroWithSession(submitted,()=>{saveAcknowledged=true;sessionLoadEpoch++;clearAcknowledgedSessionDirty(submitted);});
    toast('session refreshed ('+r.applied+' header'+(r.applied===1?'':'s')+')');
  }catch(e){toast(e.message);}
  finally{if(saveAcknowledged)await loadSession();}
});
// Test = dry-run: run the login request and show the response + the session it
// would capture, WITHOUT touching the live session (so you can debug it safely).
function hasDirtyLoginMacroDraft(){return ['loginMacroOn','loginMacroReq','loginMacroTarget','loginMacroRefresh','loginMacro401'].some(id=>$('#'+id)?.dataset.settingsDirty==='1');}
if($('#loginMacroTest'))$('#loginMacroTest').onclick=()=>runSettingsAction($('#loginMacroTest'),async()=>{
  const out=$('#loginMacroTestOut');
  try{
    const draftPending=hasDirtyLoginMacroDraft();
    if(out){out.style.display='block';out.innerHTML='<span class="hint">testing saved login macro…</span>';}
    const r=await api('/api/session/login/test',{method:'POST'});
    const sc=r.status||0,scColor=(sc>=200&&sc<400)?'var(--accent)':(sc>=400?'var(--red)':'var(--fg3)');
    const hdrs=r.headers||[];
    let html=`<div style="margin-bottom:6px">Login responded <b style="color:${scColor}">${sc||'no response'}</b> · captured <b>${hdrs.length}</b> session header${hdrs.length===1?'':'s'} <span class="hint">(saved macro dry-run — live session unchanged)</span></div>`;
    if(draftPending)html+='<div class="hint" style="color:var(--amber);margin-bottom:6px">Unsaved login-macro edits were not included. Save Session to test those changes.</div>';
    if(hdrs.length){
      html+=hdrs.map(h=>{const v=String(h.value||'');return `<div style="font-family:var(--mono);font-size:var(--fs-xs);overflow-wrap:anywhere"><span style="color:var(--accent)">${esc(h.key)}</span>: ${esc(v.length>160?v.slice(0,160)+'…':v)}</div>`;}).join('');
    }else{
      html+='<div class="hint" style="color:var(--amber)">No session captured — the login response set no Set-Cookie or Authorization. Check the request, credentials and target.</div>';
    }
    if(out)out.innerHTML=html;
  }catch(e){
    if(out){out.style.display='block';out.innerHTML='<span style="color:var(--red)">Test failed: '+esc(e.message)+'</span>';}
    else toast(e.message);
  }
});

/* ---- data retention panel ---- */
export let retentionStats=null; // cached from last fetch
export let retentionLoaded=false; // set after the Project section has been visited
let retentionLoadEpoch=0;
let retentionPolicyLoadEpoch=0;
let retentionMutationPromise=null;
let retentionSelectionSnapshot=new Set();
let retentionRefreshPending=false;

function setRetentionMutationBusy(busy){
  const section=$('#retentionSection');if(section)section.setAttribute('aria-busy',busy?'true':'false');
  ['retPolicySave','retPolicyRun','retDeleteSelected','retKeepOnly','retPurgePattern','retGc','retMaxAge','retMaxFlows','retPatternInput','retSelectAll']
    .forEach(id=>{const control=$('#'+id);if(control)control.disabled=busy;});
  document.querySelectorAll('#retentionBody button,#retentionBody input').forEach(control=>{control.disabled=busy;});
}

function snapshotRetentionSelection(){
  const boxes=[...document.querySelectorAll('.ret-chk')];
  return boxes.length?new Set(boxes.filter(cb=>cb.checked).map(cb=>cb.dataset.host)):null;
}
function restoreRetentionSelection(selection){
  if(!selection)return;
  document.querySelectorAll('.ret-chk').forEach(cb=>{cb.checked=selection.has(cb.dataset.host);});
  const sa=$('#retSelectAll');if(sa)syncRetSelectAll(sa);
}

export async function loadRetentionPolicy(){
  const epoch=++retentionPolicyLoadEpoch;
  const hint=$('#retentionPolicyHint');
  try{
    const p=await api('/api/flows/retention');
    if(epoch!==retentionPolicyLoadEpoch)return null;
    const age=$('#retMaxAge'), flows=$('#retMaxFlows');
    if(age&&age.dataset.settingsDirty!=='1'&&document.activeElement!==age)age.value=String(p.maxAgeHours||0);
    if(flows&&flows.dataset.settingsDirty!=='1'&&document.activeElement!==flows)flows.value=String(p.maxFlows||0);
    if(hint){
      const parts=[];
      if(p.maxAgeHours>0) parts.push('drop flows older than '+p.maxAgeHours+'h');
      if(p.maxFlows>0) parts.push('keep newest '+p.maxFlows+' flows');
      hint.textContent=parts.length?('Auto: '+parts.join(' · ')+' (every ~30m)'):'Auto policy off — set max age and/or max flows above.';
    }
  }catch(e){if(epoch!==retentionPolicyLoadEpoch)return null;if(hint)hint.textContent=e.message||'';}
}

export async function loadRetention(){
  if(retentionMutationPromise){retentionRefreshPending=true;return null;}
  const epoch=++retentionLoadEpoch;
  const selection=snapshotRetentionSelection();
  retentionSelectionSnapshot=selection||new Set();
  const body=$('#retentionBody');
  const status=$('#retentionLoadState');
  if(status){status.textContent='Refreshing…';status.style.display='block';}
  void loadRetentionPolicy();
  try{
    const d=await api('/api/hosts/stats');
    if(epoch!==retentionLoadEpoch||retentionMutationPromise)return null;
    retentionStats=d;
    renderRetention(d);
    restoreRetentionSelection(retentionSelectionSnapshot);
    if(status){status.textContent='';status.style.display='none';}
  }catch(e){
    if(epoch!==retentionLoadEpoch)return null;
    if(!retentionStats&&body)body.innerHTML='';
    renderLoadError(status,'Storage statistics',e,()=>loadRetention(),!!retentionStats);
  }
}

function runRetentionMutation(action){
  retentionRefreshPending=true;
  retentionLoadEpoch++;retentionPolicyLoadEpoch++;
  setRetentionMutationBusy(true);
  const previous=retentionMutationPromise||Promise.resolve();
  const current=previous.catch(()=>{}).then(action);
  const settled=current.finally(()=>{
    if(retentionMutationPromise!==settled)return;
    retentionMutationPromise=null;setRetentionMutationBusy(false);
    if(retentionRefreshPending){retentionRefreshPending=false;void loadRetention();}
  });
  retentionMutationPromise=settled;
  return settled;
}

export function renderRetention(d){
  const hosts=d.hosts||[];
  const totals=$('#retentionTotals');
  if(totals)totals.innerHTML='<b>'+esc(String(d.totalFlows||0))+' flows</b> · '+fmtBytes(d.totalBytes||0)+' total';
  const body=$('#retentionBody');
  if(!body)return;
  if(!hosts.length){body.innerHTML='<tr><td colspan="5" class="hint" style="padding:10px 8px">No captured flows yet.</td></tr>';return;}
  body.innerHTML=hosts.map(h=>`<tr data-host="${escAttr(h.host)}">
    <td><input type="checkbox" class="ret-chk" data-host="${escAttr(h.host)}" aria-label="Select host ${escAttr(h.host)}"></td>
    <td style="font-family:var(--mono);color:var(--fg)">${esc(h.host)}</td>
    <td style="text-align:right;color:var(--fg2)">${esc(String(h.flows))}</td>
    <td style="text-align:right;color:var(--fg2)">${fmtBytes(h.bytes)}</td>
    <td style="text-align:right"><button class="btn danger ret-del-one" data-host="${escAttr(h.host)}" data-flows="${escAttr(String(h.flows))}" style="color:var(--red);padding:3px 8px" title="Delete all flows from ${escAttr(h.host)}" aria-label="Delete all flows from ${escAttr(h.host)}">Delete</button></td>
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
  const confirmed=await confirmTyped(uiConfirm,'Delete flows from '+esc(host),msg,'Delete','btn danger','var(--red)');
  if(!confirmed)return;
  try{
    const r=await runRetentionMutation(()=>api('/api/flows/purge',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({hosts:[host],mode:'delete'})}));
    toast('deleted '+r.deleted+' flow'+(r.deleted===1?'':'s')+' · reclaiming space…');
    loadFlows();
  }catch(e){toastError('Purge failed', e);}
}

$('#retDeleteSelected').onclick=async()=>{
  const hosts=retChecked();
  if(!hosts.length){toast('select at least one host first');return;}
  // compute total flows for confirmation
  const stats=retentionStats&&retentionStats.hosts||[];
  const totalFlows=hosts.reduce((s,h)=>{const e=stats.find(x=>x.host===h);return s+(e?e.flows:0);},0);
  const msg='Delete all flows from '+hosts.length+' host'+(hosts.length===1?'':'s')+' ('+totalFlows+' flow'+(totalFlows===1?'':'s')+')? This is permanent.';
  const confirmed=await confirmTyped(uiConfirm,'Delete selected hosts',msg,'Delete','btn danger','var(--red)');
  if(!confirmed)return;
  try{
    const r=await runRetentionMutation(()=>api('/api/flows/purge',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({hosts,mode:'delete'})}));
    toast('deleted '+r.deleted+' flow'+(r.deleted===1?'':'s')+' · reclaiming space…');
    loadFlows();
  }catch(e){toastError('Purge failed', e);}
};

$('#retKeepOnly').onclick=async()=>{
  const hosts=retChecked();
  if(!hosts.length){toast('select the hosts to keep — none checked');return;}
  const stats=retentionStats&&retentionStats.hosts||[];
  const keepFlows=hosts.reduce((s,h)=>{const e=stats.find(x=>x.host===h);return s+(e?e.flows:0);},0);
  const total=retentionStats?retentionStats.totalFlows:0;
  const delFlows=total-keepFlows;
  const msg='Keep only '+hosts.length+' host'+(hosts.length===1?'':'s')+' and delete the rest (~'+delFlows+' flow'+(delFlows===1?'':'s')+')? This is permanent.';
  const confirmed=await confirmTyped(uiConfirm,'Keep only selected',msg,'Delete the rest','btn danger','var(--red)');
  if(!confirmed)return;
  try{
    const r=await runRetentionMutation(()=>api('/api/flows/purge',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({hosts,mode:'keepOnly'})}));
    toast('deleted '+r.deleted+' flow'+(r.deleted===1?'':'s')+' · reclaiming space…');
    loadFlows();
  }catch(e){toastError('Purge failed', e);}
};

$('#retPurgePattern').onclick=async()=>{
  const pat=($('#retPatternInput')||{}).value&&$('#retPatternInput').value.trim();
  if(!pat){toast('enter a host pattern first');return;}
  const confirmed=await confirmTyped(uiConfirm,'Purge by pattern',
    'Delete all flows matching <b style="color:var(--accent)">'+esc(pat)+'</b>? This is permanent.','Delete','btn danger','var(--red)');
  if(!confirmed)return;
  try{
    const r=await runRetentionMutation(()=>api('/api/flows/purge',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({hosts:[pat],mode:'delete'})}));
    toast('deleted '+r.deleted+' flow'+(r.deleted===1?'':'s')+' · reclaiming space…');
    if($('#retPatternInput'))$('#retPatternInput').value='';
    loadFlows();
  }catch(e){toastError('Purge failed', e);}
};

$('#retGc').onclick=async()=>{
  const confirmed=await uiConfirm('Reclaim space',
    'Run garbage collection to remove body files no longer referenced by any flow? This is safe but permanent.','Run GC','btn accent','');
  if(!confirmed)return;
  try{
    const r=await runRetentionMutation(()=>api('/api/flows/gc',{method:'POST'}));
    toast('GC done · removed '+r.removedFiles+' file'+(r.removedFiles===1?'':'s')+' · freed '+fmtBytes(r.freedBytes));
  }catch(e){toastError('GC failed', e);}
};

$('#retPolicySave')&&($('#retPolicySave').onclick=async()=>{
  const age=$('#retMaxAge'),flows=$('#retMaxFlows');
  const ageValue=age?.value||'0',flowsValue=flows?.value||'0';
  const ageGeneration=settingsEditGeneration(age),flowsGeneration=settingsEditGeneration(flows);
  const maxAgeHours=Number(ageValue||0);
  const maxFlows=Number(flowsValue||0);
  try{
    retentionPolicyLoadEpoch++;
    await runRetentionMutation(()=>api('/api/flows/retention',{method:'PUT',headers:{'content-type':'application/json'},body:JSON.stringify({maxAgeHours,maxFlows})}));
    if(settingsEditOwned(age,ageGeneration,ageValue))age.removeAttribute('data-settings-dirty');
    if(settingsEditOwned(flows,flowsGeneration,flowsValue))flows.removeAttribute('data-settings-dirty');
    toast('auto retention saved');
  }catch(e){toastError('Retention policy save failed', e);}
});
$('#retPolicyRun')&&($('#retPolicyRun').onclick=async()=>{
  try{
    const r=await runRetentionMutation(()=>api('/api/flows/retention/run',{method:'POST'}));
    toast('retention run · deleted '+(r.deleted||0)+' flow'+(r.deleted===1?'':'s'));
    loadFlows();
  }catch(e){toastError('Retention run failed', e);}
});

// select-all checkbox for retention table
$('#retSelectAll')&&($('#retSelectAll').onclick=function(){
  const boxes=document.querySelectorAll('.ret-chk');
  boxes.forEach(cb=>cb.checked=this.checked);
});

$('#exportProject').onclick=()=>toast('Downloading project export…');
const exportHAR=$('#exportHAR');if(exportHAR)exportHAR.onclick=()=>toast('Downloading HAR export…');
const exportFull=$('#exportFull');if(exportFull)exportFull.onclick=()=>toast('Downloading full project archive…');
const importFullBtn=$('#importFullBtn');if(importFullBtn)importFullBtn.onclick=()=>$('#importFullFile').click();
const importFullFile=$('#importFullFile');
if(importFullFile)importFullFile.onchange=async e=>{
  const f=e.target.files[0];if(!f){return;}
  const def=(f.name||'project').replace(/\.zip$/i,'').replace(/[^A-Za-z0-9._-]/g,'-')||'imported';
  const name=await uiPrompt({title:'Import full project',value:def,placeholder:'New project name'});
  if(name){
    try{
      const r=await api('/api/import/full?name='+encodeURIComponent(name.trim()),{method:'POST',body:f});
      toast('Imported project "'+r.name+'" — open it from the project switcher below');
      loadProject();
    }catch(err){toast('import: '+err.message);}
  }
  e.target.value='';
};
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
const importHARBtn=$('#importHARBtn');if(importHARBtn)importHARBtn.onclick=()=>$('#importHARFile').click();
const importHARFile=$('#importHARFile');
if(importHARFile)importHARFile.onchange=async e=>{
  const f=e.target.files[0];if(!f)return;
  const confirmed=await uiConfirm('Import HAR',
    'Merge entries from <b style="color:var(--accent)">'+esc(f.name||'this HAR')+'</b> into this project\'s History? Existing flows are kept.','Import','btn accent','');
  if(!confirmed){e.target.value='';return;}
  try{
    const text=await f.text();
    const r=await api('/api/import/har',{method:'POST',headers:{'content-type':'application/json'},body:text});
    const n=r.imported!=null?r.imported:(r.importedFlows!=null?r.importedFlows:0);
    toast('HAR import: '+n+' entr'+(n===1?'y':'ies')+' merged');
    loadFlows();
  }catch(err){toast('HAR import: '+err.message);}
  e.target.value='';
};
const importBurpBtn=$('#importBurpBtn');if(importBurpBtn)importBurpBtn.onclick=()=>$('#importBurpFile').click();
const importBurpFile=$('#importBurpFile');
if(importBurpFile)importBurpFile.onchange=async e=>{
  const f=e.target.files[0];if(!f)return;
  const confirmed=await uiConfirm('Import Burp XML',
    'Merge traffic from <b style="color:var(--accent)">'+esc(f.name||'this Burp export')+'</b> into this project\'s History? Existing flows are kept.','Import','btn accent','');
  if(!confirmed){e.target.value='';return;}
  try{
    const r=await api('/api/import/burp',{method:'POST',headers:{'content-type':'application/xml'},body:f});
    const n=r.imported||0,skipped=r.skipped||0;
    toast('Burp import: '+n+' entr'+(n===1?'y':'ies')+' merged'+(skipped?' · '+skipped+' skipped':''));
    loadFlows();
  }catch(err){toast('Burp import: '+err.message);}
  e.target.value='';
};
// ---- project switching (close current, open another / a new path) ----
// Each project entry is {name, path}: path is empty for a named project under
// GlobalDir/projects (switch via {target: name}), or set for an external
// folder the operator chose explicitly (switch via {path}).
export async function loadProject(){
  const epoch=++projectLoadEpoch;
  const hadData=projectDataLoaded;
  if(hadData)markProjectDataStale(true);
  setProjectControlsDisabled(true,hadData?'Project data is stale — retry before switching or creating a project':'Project data is loading — wait before switching or creating a project');
  const loadState=$('#projectLoadState');
  if(loadState&&hadData){loadState.style.display='block';loadState.textContent='Refreshing Projects…';}
  try{const d=await api('/api/project');
    if(epoch!==projectLoadEpoch)return;
    projectPathFlavor=projectPathFlavorFor(d.dir);
    if(loadState)loadState.style.display='none';
    const n=$('#projNameHint');if(n)n.textContent=d.current||'default';
    const dir=$('#projDirHint');if(dir&&d.dir)dir.textContent=d.dir;
    const sel=$('#projSelect');
    if(sel){
      const list=(d.projects&&d.projects.length)?d.projects:[{name:'default',path:''}];
      sel.innerHTML=list.map(p=>{
        const isCur=isCurrentProject(p,d.current,d.dir,list);
        const folder=p.category?`${p.category} / `:'';
        const opened=formatProjectStamp(p.openedAt);
        const label=`${folder}${p.name}${p.path?` — ${p.path}`:''}${opened?` · ${opened}`:''}${isCur?' (current)':''}`;
        return `<option value="${escAttr(p.name)}" data-path="${escAttr(p.path||'')}"${isCur?' disabled':''}>${esc(label)}</option>`;
      }).join('');
    }
    projectDataLoaded=true;markProjectDataStale(false);
    setProjectControlsDisabled(!d.canSwitch,d.canSwitch?'':'project switching is unavailable in this build');
  }catch(e){
    if(epoch!==projectLoadEpoch)return;
    markProjectDataStale(hadData);
    setProjectControlsDisabled(true,'Project data is stale — retry before switching or creating a project');
    renderLoadError($('#projectLoadState'),'Projects',e,loadProject,hadData);
  }
}
let projectSwitchEpoch=0,projectSwitchTimer=null,projectSwitchPending=false;
function projectPathFlavorFor(value){
  const raw=String(value||'').trim();
  return /^[A-Za-z]:[\\/]/.test(raw)||raw.startsWith('\\\\')?'windows':'posix';
}
function projectPathKey(value,flavor=projectPathFlavor){
  const source=String(value||'').trim();
  if(!source)return '';
  const windows=flavor==='windows';
  const raw=windows?source.replace(/\\/g,'/'):source;
  const drive=windows?(raw.match(/^[A-Za-z]:/)||[])[0]||'':'';
  const unc=windows&&!drive&&raw.startsWith('//');
  const rooted=windows?(unc||(drive&&raw.slice(drive.length).startsWith('/'))):raw.startsWith('/');
  if(!rooted)return '';
  const parts=raw.slice(drive.length).split('/'),out=[],rootDepth=unc?2:0;
  for(const part of parts){
    if(!part||part==='.')continue;
    if(part==='..'){if(out.length>rootDepth)out.pop();continue;}
    out.push(part);
  }
  return (unc?'//':drive?drive+'/':'/')+out.join('/');
}
function setProjectSwitchFeedback(text,kind='status'){
  const error=kind==='error';
  ['#projSwitchNote','#pmSwitchNote'].map(selector=>$(selector)).filter(Boolean).forEach(note=>{
    note.style.display='block';note.style.color=error?'var(--red)':'var(--accent)';note.textContent=text;
    note.setAttribute('role',error?'alert':'status');note.setAttribute('aria-live',error?'assertive':'polite');
  });
}
function setProjectPathInvalid(path,invalid){
  ['projNewPath','pmNewPath'].map(id=>$('#'+id)).filter(Boolean).forEach(field=>{
    if(invalid&&field.value.trim()===path)field.setAttribute('aria-invalid','true');
    else if(!invalid)field.removeAttribute('aria-invalid');
  });
}
function clearProjectPathFeedback(){
  if(projectSwitchPending)return;
  setProjectPathInvalid('',false);
  ['#projSwitchNote','#pmSwitchNote'].map(selector=>$(selector)).filter(Boolean).forEach(note=>{
    note.style.display='none';note.textContent='';note.style.color='var(--accent)';
    note.setAttribute('role','status');note.setAttribute('aria-live','polite');
  });
}
const projectSwitchDisabledControls=new Map();
function setProjectSwitchModalBusy(busy){
  const modal=$('#projModal');if(!modal)return;
  if(busy){
    // Invalidate an earlier list load before it can enable replacement rows.
    projectModalLoadEpoch++;
    modal.querySelectorAll('button,input,textarea,select').forEach(control=>{
      if(!projectSwitchDisabledControls.has(control))projectSwitchDisabledControls.set(control,control.disabled);
      control.disabled=true;
    });
    const note=$('#pmSwitchNote');if(note)note.tabIndex=-1;
    openModal(modal,{initialFocus:note,onEscape:()=>{},onDismiss:()=>{}});
  }else{
    projectSwitchDisabledControls.forEach((disabled,control)=>{control.disabled=disabled;});
    projectSwitchDisabledControls.clear();
    if(modal.style.display==='flex')openModal(modal,{initialFocus:$('#pmClose')});
  }
}
export async function doSwitchProject(target,path){
  if(!target&&!path)return;
  if(projectSwitchPending){toast('a project switch is already in progress');return;}
  const expectedPath=path?projectPathKey(path):'';
  if(path&&!expectedPath){
    const message='Custom save location must be an absolute folder path.';
    setProjectPathInvalid(path,true);setProjectSwitchFeedback(message,'error');toast(message,'error');return;
  }
  const blocker=projectSwitchBlocker();
  if(blocker){setProjectSwitchFeedback(blocker,'error');toast(blocker,'error');return;}
  setProjectPathInvalid('',false);
  const switchEpoch=++projectSwitchEpoch;
  projectSwitchPending=true;
  if(projectSwitchTimer){clearTimeout(projectSwitchTimer);projectSwitchTimer=null;}
  // Surface the "restarting…" message wherever it's visible — the Settings panel
  // note and the top-bar Projects modal share this one switch path.
  const setNote=(text,kind='status')=>setProjectSwitchFeedback(text,kind);
  setNote('Switching projects…');
  setProjectSwitchModalBusy(true);
  // The old process keeps serving (same version, same "ok") for a few hundred ms
  // after the switch is requested while the new one is still binding — polling
  // /api/version can't tell them apart and would reload straight back into the
  // OLD project's data. Remember which project we're leaving and poll /api/project
  // instead, waiting for the exact accepted project identity to appear. Never
  // reload merely because the old process still answers: that can put the UI
  // straight back into the project the operator just left.
  setProjectControlsDisabled(true,'project switch is in progress');
  let expected='';
  try{const accepted=await api('/api/project/switch',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify(path?{path}:{target})});
    if(!accepted||!accepted.switching)throw new Error('project switch was not accepted');
    expected=String(accepted.switching);
  }catch(e){projectSwitchPending=false;setProjectSwitchModalBusy(false);setNote('Project switch failed: '+e.message,'error');toast(e.message,'error');loadProject();return;}
  setNote(path?`Switching to "${target||path}" (${path}) — restarting & reconnecting…`:`Switching to "${target}" — restarting & reconnecting…`);
  const deadline=Date.now()+30000;
  const poll=async()=>{
    if(switchEpoch!==projectSwitchEpoch)return;
    try{
      const d=await api('/api/project?_t='+Date.now());
      const reached=path?projectPathKey(d.dir)===expectedPath:String(d.current||'')===expected;
      if(reached){projectSwitchPending=false;projectSwitchTimer=null;location.reload();return;}
    }
    catch(e){}
    if(Date.now()>=deadline){projectSwitchPending=false;projectSwitchTimer=null;setProjectSwitchModalBusy(false);setNote('Project switch was not confirmed within 30 seconds. Retry the switch, or reload manually after the new process is ready.','error');loadProject();return;}
    projectSwitchTimer=setTimeout(poll,500);
  };
  projectSwitchTimer=setTimeout(poll,500);
}
$('#projSwitchBtn').onclick=()=>{
  const sel=$('#projSelect');const opt=sel&&sel.selectedOptions&&sel.selectedOptions[0];
  if(!opt){toast('no other project to open');return;}
  doSwitchProject(opt.value,opt.dataset.path||'');
};
$('#projNewBtn').onclick=()=>createProjectFrom('#projNew','#projNewPath','');

// ---- top-bar Projects picker modal (click the project badge) ----
// Same data + switch endpoint as the Settings panel, surfaced as a prominent,
// first-class action so choosing a project never means opening Settings.
function formatProjectStamp(sec, nowMs){
  const n=Number(sec)||0;
  if(!n)return '';
  const now=nowMs||Date.now();
  const delta=Math.max(0, now-n*1000);
  const min=Math.floor(delta/60000);
  if(min<1)return 'just now';
  if(min<60)return min+'m ago';
  const hr=Math.floor(min/60);
  if(hr<36)return hr+'h ago';
  const day=Math.floor(hr/24);
  if(day<14)return day+'d ago';
  const d=new Date(n*1000);
  const months=['Jan','Feb','Mar','Apr','May','Jun','Jul','Aug','Sep','Oct','Nov','Dec'];
  const year=d.getFullYear()===new Date(now).getFullYear()?'':' '+d.getFullYear();
  return d.getDate()+' '+months[d.getMonth()]+year;
}
function groupProjectTree(projects){
  const root={folders:[], items:[]};
  const find=(list,name)=>{
    let folder=list.find(item=>item.name===name);
    if(!folder){folder={name, folders:[], items:[]}; list.push(folder);}
    return folder;
  };
  for(const project of projects||[]){
    const parts=String(project.category||'').split(/[\\/]/).map(part=>part.trim()).filter(Boolean);
    let node=root;
    for(const part of parts) node=find(node.folders, part);
    node.items.push(project);
  }
  const sortNode=node=>{
    node.folders.sort((a,b)=>a.name.localeCompare(b.name));
    node.items.sort((a,b)=>(Number(b.openedAt)||0)-(Number(a.openedAt)||0)||String(a.name).localeCompare(String(b.name)));
    node.folders.forEach(sortNode);
    node.count=node.items.length+node.folders.reduce((sum,folder)=>sum+(folder.count||0),0);
  };
  sortNode(root);
  return root;
}
function projectMatchesQuery(project, query){
  if(!query)return true;
  return `${project.name||''} ${project.path||''} ${project.category||''}`.toLowerCase().includes(query);
}
function isCurrentProject(project, current, dir, projects){
  const dirKey=projectPathKey(dir);
  const external=(projects||[]).some(item=>item.path&&projectPathKey(item.path)===dirKey);
  if(project.path)return projectPathKey(project.path)===dirKey;
  return !external&&project.name===current;
}
function projectTimeLabel(project, nowMs){
  const opened=formatProjectStamp(project.openedAt, nowMs);
  const created=formatProjectStamp(project.createdAt, nowMs);
  return `${opened?`Opened ${opened}`:'Not opened yet'}${created?` · Created ${created}`:''}`;
}
function projectRowHTML(project, cache){
  const current=isCurrentProject(project, cache.current, cache.dir, cache.projects);
  const path=project.path?`<span class="pm-meta">${esc(project.path)}</span>`:'';
  return `<div class="pm-item"><div class="row u-gap-2"><button type="button" class="btn pm-row" data-proj="${escAttr(project.name)}" data-path="${escAttr(project.path||'')}"${current?' disabled':''}><span class="pm-name">${esc(project.name)}${current?' (current)':''}</span><span class="pm-meta">${esc(projectTimeLabel(project))}</span>${path}</button><button type="button" class="btn pm-folder-btn" data-proj="${escAttr(project.name)}" data-path="${escAttr(project.path||'')}" aria-label="Set folder for ${escAttr(project.name)}">Folder</button></div><div class="pm-folder-edit" hidden><input class="btn btn-field pm-folder-input" value="${escAttr(project.category||'')}" aria-label="Folder for ${escAttr(project.name)}" spellcheck="false" placeholder="Clients/Acme"><button type="button" class="btn pm-folder-save">Save</button></div></div>`;
}
function projectTreeHTML(node, cache, openAll){
  const folders=node.folders.map(folder=>{
    const open=openAll||folderContainsCurrent(folder, cache)?' open':'';
    return `<details class="pm-folder"${open}><summary>${esc(folder.name)}<span class="pm-count">${folder.count}</span></summary><div class="pm-folder-body">${projectTreeHTML(folder, cache, openAll)}</div></details>`;
  }).join('');
  return node.items.map(project=>projectRowHTML(project, cache)).join('')+folders;
}
function folderContainsCurrent(node, cache){
  if(node.items.some(project=>isCurrentProject(project, cache.current, cache.dir, cache.projects)))return true;
  return node.folders.some(folder=>folderContainsCurrent(folder, cache));
}
let projectModalCache=null;
function paintProjectModal(){
  const list=$('#pmList');
  const cache=projectModalCache;
  if(!list||!cache)return;
  if(!cache.canSwitch){list.innerHTML='<div class="hint">Project switching is unavailable in this build.</div>';return;}
  const query=($('#pmFilter')?.value||'').trim().toLowerCase();
  const projects=(cache.projects||[]).filter(project=>projectMatchesQuery(project, query));
  if(!projects.length){
    list.innerHTML=query?'<div class="hint">No projects match that filter.</div>':'<div class="hint">No saved projects yet — create one below.</div>';
    return;
  }
  const openAll=!!query||(cache.projects||[]).length<=8;
  list.innerHTML=projectTreeHTML(groupProjectTree(projects), cache, openAll);
  list.querySelectorAll('.pm-row').forEach(button=>{
    if(button.disabled)return;
    button.onclick=()=>doSwitchProject(button.dataset.proj, button.dataset.path||'');
  });
  list.querySelectorAll('.pm-folder-btn').forEach(button=>button.onclick=()=>{
    const edit=button.closest('.pm-item')?.querySelector('.pm-folder-edit');
    if(edit)edit.hidden=!edit.hidden;
  });
  list.querySelectorAll('.pm-folder-save').forEach(button=>button.onclick=async()=>{
    const item=button.closest('.pm-item');
    const row=item?.querySelector('.pm-row');
    const input=item?.querySelector('.pm-folder-input');
    if(!row||!input)return;
    button.disabled=true;
    try{
      const path=row.dataset.path||'';
      await api('/api/project/folder',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify(path?{path,category:input.value}:{target:row.dataset.proj,category:input.value})});
      await renderProjModal();
    }catch(e){toast(e.message,'error');button.disabled=false;}
  });
}
async function createProjectFrom(nameSel, pathSel, folderSel){
  const v=($(nameSel)?.value||'').trim();
  const path=($(pathSel)?.value||'').trim();
  const folder=folderSel?($(folderSel)?.value||'').trim():'';
  if(!v&&!path){toast('enter a project name, or a custom save folder');return;}
  if(folder){
    try{await api('/api/project/folder',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify(path?{path,category:folder}:{target:v,category:folder})});}
    catch(e){toast(e.message,'error');return;}
  }
  doSwitchProject(v,path);
}
async function renderProjModal(){
  const epoch=++projectModalLoadEpoch;
  const list=$('#pmList');
  if(list){
    list.setAttribute('aria-busy','true');
    list.querySelectorAll('.pm-row').forEach(button=>button.disabled=true);
  }
  setProjectModalActionsDisabled(true);
  try{const d=await api('/api/project');
    if(epoch!==projectModalLoadEpoch)return false;
    projectPathFlavor=projectPathFlavorFor(d.dir);
    const cur=$('#pmCurrent');if(cur)cur.textContent=d.current||'default';
    const dir=$('#pmDir');if(dir)dir.textContent=d.dir||'';
    if(!list)return false;
    projectModalCache={projects:d.projects||[], current:d.current||'default', dir:d.dir||'', canSwitch:!!d.canSwitch};
    paintProjectModal();
    list.removeAttribute('aria-busy');projectModalDataLoaded=true;setProjectModalActionsDisabled(!d.canSwitch);return true;
  }catch(e){
    if(epoch!==projectModalLoadEpoch)return false;
    setProjectModalActionsDisabled(true);
    renderLoadError(list,'Projects',e,renderProjModal,projectModalDataLoaded);
    if(list)list.removeAttribute('aria-busy');
    return false;
  }
}
export async function openProjectModal(){
  if(projectSwitchPending)return;
  const m=$('#projModal');if(!m)return;
  const note=$('#pmNote');if(note){note.style.display='none';note.textContent='';}
  clearProjectPathFeedback();
  const inp=$('#pmNew');if(inp)inp.value='';
  const pinp=$('#pmNewPath');if(pinp)pinp.value='';
  const folder=$('#pmNewFolder');if(folder)folder.value='';
  openModal(m);
  const rendered=await renderProjModal();
  if(rendered&&inp&&m.style.display==='flex'&&!inp.disabled)inp.focus();
}
{const c=$('#pmClose');if(c)c.onclick=()=>{if(!projectSwitchPending)closeModal($('#projModal'));};}
{const nb=$('#pmNewBtn');if(nb)nb.onclick=()=>createProjectFrom('#pmNew','#pmNewPath','#pmNewFolder');}
$('#pmFilter')?.addEventListener('input',paintProjectModal);
{const ni=$('#pmNew');if(ni)ni.addEventListener('keydown',e=>{if(e.key==='Enter'){e.preventDefault();$('#pmNewBtn').click();}});}
{const pi=$('#pmNewPath');if(pi)pi.addEventListener('keydown',e=>{if(e.key==='Enter'){e.preventDefault();$('#pmNewBtn').click();}});}
['projNewPath','pmNewPath'].forEach(id=>$('#'+id)?.addEventListener('input',clearProjectPathFeedback));
$('#saveAddrBtn').onclick=()=>runSettingsAction($('#saveAddrBtn'),async()=>{
  const list=$('#proxyListenersList'),generation=settingsEditGeneration(list);
  const addrs=collectProxyAddrs();
  if(!addrs.length){toast('enter at least one listener');return;}
  try{
    const s=await saveSettingsPatch({proxyAddrs:addrs});
    state.proxyAddr=s.proxyAddr;$('#proxyAddr').textContent=s.proxyAddr;
    if($('#setAddr'))$('#setAddr').value=s.proxyAddr;
    if(settingsEditOwned(list,generation)){renderProxyListeners(s.proxyAddrs||addrs);clearSettingsDirty([],['#proxyListenersList']);}
    await loadDeviceProxyEndpoint();
    toast('proxy now on '+s.proxyAddr);
  }catch(e){toast(e.message);}
});
$('#saveControlAddrBtn').onclick=()=>runSettingsAction($('#saveControlAddrBtn'),async()=>{
  const host=$('#setControlHost'),port=$('#setControlPort');
  const hostGeneration=settingsEditGeneration(host),portGeneration=settingsEditGeneration(port);
  const submittedHost=host?.value,submittedPort=port?.value;
  const controlAddr=syncControlAddrFields();
  if(!controlAddr){toast('enter control host and port');return;}
  try{
    const s=await saveSettingsPatch({controlAddr},{reconcile:false});
    state.controlAddr=s.controlAddr;$('#controlAddr').textContent=s.controlAddr;
    if(settingsEditOwned(host,hostGeneration,submittedHost)&&settingsEditOwned(port,portGeneration,submittedPort)){
      const c=parseListenAddr(s.controlAddr);
      if(port)port.value=c.port;
      renderHostSelect(host,c.host);
      clearSettingsDirty(['setControlHost','setControlPort','setControlAddr']);
    }
    const newUrl='http://'+s.controlAddr;
    if(location.host!==s.controlAddr)toast('Control UI now on '+newUrl+' — open that URL if this page stops updating');
    else{toast('control UI now on '+s.controlAddr);scheduleSettingsReconcile();}
    const tun=$('#oobModalTunnelCmd');if(tun)tun.textContent='cloudflared tunnel --url '+newUrl;
  }catch(e){toast(e.message);}
});
let sysProxyLoadEpoch=0;
let sysProxyMutationPromise=null;
let sysProxyDesired=null;
let sysProxyActiveDesired=null;
let sysProxyState=null;
let sysProxyLoadFailed=false;

function renderSystemProxyState(s){
  if(!s)return;
  sysProxyState=s;
  sysProxyLoadFailed=false;
  const sec=$('#sysProxySection'),button=$('#sysProxyToggle'),hint=$('#sysProxyHint');
  if(!s.supported){
    if(sec)sec.style.display='none';
    return;
  }
  if(sec)sec.style.display='';
  if(button){
    button.disabled=!!sysProxyMutationPromise;
    button.classList.toggle('on',!!s.enabled);
    button.setAttribute('aria-pressed',s.enabled?'true':'false');
    button.textContent=s.enabled?'System proxy is on':'System proxy is off';
  }
  if(hint)hint.textContent=s.enabled?'Traffic routes through '+s.proxy:'';
}

function setSystemProxyBusy(busy){
  const button=$('#sysProxyToggle');
  if(!button)return;
  button.disabled=busy||sysProxyLoadFailed;
  if(busy)button.setAttribute('aria-busy','true');
  else button.removeAttribute('aria-busy');
}

async function readSystemProxyStatus({render=true,throwOnError=false}={}){const epoch=++sysProxyLoadEpoch;
  try{
    const s=await api('/api/sysproxy');
    if(epoch!==sysProxyLoadEpoch)return sysProxyState;
    sysProxyState=s;
    sysProxyLoadFailed=false;
    if(render)renderSystemProxyState(s);
    return s;
  }catch(e){
    if(epoch!==sysProxyLoadEpoch)return sysProxyState;
    sysProxyLoadFailed=true;
    if(render){
      const sec=$('#sysProxySection'),button=$('#sysProxyToggle');
      if(sec)sec.style.display='';
      if(button)button.disabled=true;
      renderLoadError($('#sysProxyHint'),'System proxy',e,loadSysProxy,false);
    }
    if(throwOnError)throw e;
    return null;
  }
}

export function getSystemProxyStatus(options={}){
  if(sysProxyMutationPromise)return sysProxyMutationPromise;
  return readSystemProxyStatus(options);
}

export async function loadSysProxy(){
  try{return await getSystemProxyStatus();}
  catch(_){return sysProxyState;}
}

async function drainSystemProxyMutations(){
  let result=sysProxyState;
  while(sysProxyDesired!==null){
    const desired=sysProxyDesired;
    sysProxyDesired=null;
    sysProxyActiveDesired=desired;
    let current,error;
    try{current=await api('/api/sysproxy',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({enabled:desired})});}
    catch(e){error=e;}
    if(sysProxyDesired!==null)continue;
    if(error)throw error;
    result=current;
    renderSystemProxyState(result);
  }
  return result;
}

async function runSystemProxyMutations(){
  try{return await drainSystemProxyMutations();}
  catch(e){await readSystemProxyStatus();throw e;}
  finally{
    sysProxyActiveDesired=null;
    sysProxyMutationPromise=null;
    sysProxyLoadEpoch++;
    setSystemProxyBusy(false);
  }
}

export function setSystemProxyEnabled(enabled){
  const desired=!!enabled;
  if(sysProxyMutationPromise){
    if(sysProxyDesired===desired||(sysProxyDesired===null&&sysProxyActiveDesired===desired))return sysProxyMutationPromise;
  }
  sysProxyDesired=!!enabled;
  sysProxyLoadEpoch++;
  setSystemProxyBusy(true);
  if(!sysProxyMutationPromise)sysProxyMutationPromise=runSystemProxyMutations();
  return sysProxyMutationPromise;
}

$('#sysProxyToggle').onclick=async()=>{
  const button=$('#sysProxyToggle');
  const on=button.classList.contains('on');
  try{
    const s=await setSystemProxyEnabled(!on);
    toast(s?.enabled?'system proxy enabled':'system proxy disabled');
  }catch(e){toast(e.message);}
};

let androidDeviceSerial='';
let androidLoadEpoch=0;
let androidActionPending=false;
let androidDeviceActionsReady=false;

function androidSerial(){
  return androidDeviceSerial||'';
}

function setAndroidDeviceActionsEnabled(enabled){
  ['androidSetupAllBtn','androidInstallUserBtn','androidInstallSystemBtn','androidProxyBtn','androidUnproxyBtn']
    .forEach(id=>{const button=$('#'+id);if(button){button.disabled=!enabled;button.title=enabled?'':'Connect and authorize an Android device first';}});
  const remove=$('#androidRemoveSystemCa');if(remove)remove.disabled=!enabled;
}
function setAriaBusy(control,busy){
  if(busy)control.setAttribute('aria-busy','true');
  else control.removeAttribute('aria-busy');
}
function setAndroidActionBusy(busy){
  ['androidRefreshBtn','androidSetupAllBtn','androidInstallUserBtn','androidInstallSystemBtn','androidProxyBtn','androidUnproxyBtn']
    .forEach(id=>{const button=$('#'+id);if(button){setAriaBusy(button,busy);if(busy)button.disabled=true;else if(id==='androidRefreshBtn')button.disabled=false;}});
  const trigger=$('#androidDeviceTrigger');if(trigger)trigger.disabled=busy||!androidDeviceActionsReady||!$('#androidDeviceMenu')?.querySelector('[role="option"]:not(:disabled)');
  $('#androidProxyMode')?.querySelectorAll('button').forEach(button=>{button.disabled=busy;});
  if(!busy)setAndroidDeviceActionsEnabled(androidDeviceActionsReady);
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

function closeAndroidDeviceMenu(returnFocus=false){
  const menu=$('#androidDeviceMenu'),trigger=$('#androidDeviceTrigger');
  if(menu)menu.hidden=true;
  if(trigger)trigger.setAttribute('aria-expanded','false');
  if(returnFocus&&trigger)trigger.focus(); // keyboard focus return to its combobox trigger
}

function toggleAndroidDeviceMenu(){
  const menu=$('#androidDeviceMenu'),trigger=$('#androidDeviceTrigger');
  if(!menu||!trigger||trigger.disabled)return;
  if(!menu.hidden){closeAndroidDeviceMenu();return;}
  // One popover at a time: each trigger stops propagation, so the document
  // click that normally dismisses the other menu (and the shared ui-select
  // dropdowns) never fires. Close them explicitly instead.
  closeIOSDeviceMenu();
  closeAllUiSelects();
  menu.hidden=false;
  trigger.setAttribute('aria-expanded','true');
  menu.querySelector('.ui-select-opt.sel')?.scrollIntoView({block:'nearest'});
}

function renderAndroidDevicePicker(devs){
  const menu=$('#androidDeviceMenu'),trigger=$('#androidDeviceTrigger'),valueEl=$('#androidDeviceValue'),meta=$('#androidDeviceMeta');
  if(!menu||!trigger||!valueEl)return;
  trigger.setAttribute('aria-label','Android device');
  trigger.setAttribute('aria-controls','androidDeviceMenu');
  trigger.setAttribute('aria-haspopup','listbox');
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
    return `<button type="button" role="option" id="android-device-option-${escAttr(d.serial)}" class="ui-select-opt${sel?' sel':''}" data-device-option="${escAttr(d.serial)}" data-serial="${escAttr(d.serial)}"${dis?' disabled':''} aria-selected="${sel?'true':'false'}"><span class="ui-select-opt-title">${esc(androidDeviceTitle(d))}${dis?' — '+esc(d.state):''}</span><span class="ui-select-opt-sub">${esc(androidDeviceMeta(d))}</span></button>`;
  }).join('');
  const cur=devs.find(d=>d.serial===androidDeviceSerial);
  valueEl.textContent=cur?androidDeviceTitle(cur):'Select device…';
  if(meta)meta.textContent=cur?androidDeviceMeta(cur):'';
}

function moveDeviceOption(menu, trigger, delta) {
  const opts=[...menu.querySelectorAll('[role="option"]:not(:disabled)')];
  if(!opts.length)return;
  const active=document.activeElement?.closest?.('[role="option"]');
  let i=active?opts.indexOf(active):opts.findIndex(o=>o.getAttribute('aria-selected')==='true');
  if(i<0)i=0;
  i=(i+delta+opts.length)%opts.length;
  opts[i].focus();
  opts.forEach(o=>o.classList.toggle('active',o===opts[i]));
}
function jumpDeviceOption(menu, trigger, end=false) {
  const opts=[...menu.querySelectorAll('[role="option"]:not(:disabled)')];
  if(!opts.length)return;
  const opt=end?opts[opts.length-1]:opts[0];
  opt.focus();
  opts.forEach(o=>o.classList.toggle('active',o===opt));
}
function wireDeviceMenuKeyboard(menu, trigger, openMenu, closeMenu) {
  if(!menu||!trigger)return;
  trigger.addEventListener('keydown',e=>{
    if(!['ArrowDown','ArrowUp','Home','End','Enter',' '].includes(e.key))return;
    e.preventDefault();
    if(menu.hidden){openMenu();}
    if(e.key==='ArrowDown')moveDeviceOption(menu,trigger,1);
    else if(e.key==='ArrowUp')moveDeviceOption(menu,trigger,-1);
    else if(e.key==='Home')jumpDeviceOption(menu,trigger);
    else if(e.key==='End')jumpDeviceOption(menu,trigger,true);
    else moveDeviceOption(menu,trigger,0);
  });
  menu.addEventListener('keydown',e=>{
    if(e.key==='ArrowDown'){e.preventDefault();moveDeviceOption(menu,trigger,1);}
    else if(e.key==='ArrowUp'){e.preventDefault();moveDeviceOption(menu,trigger,-1);}
    else if(e.key==='Home'){e.preventDefault();jumpDeviceOption(menu,trigger);}
    else if(e.key==='End'){e.preventDefault();jumpDeviceOption(menu,trigger,true);}
    else if(e.key==='Escape'){e.preventDefault();closeMenu(true);}
    else if(e.key==='Enter'||e.key===' '){e.preventDefault();document.activeElement?.closest?.('[role="option"]:not(:disabled)')?.click();}
  });
}

async function androidPost(path,body){
  const payload={serial:androidSerial(),proxyMode:androidProxyMode(),...body};
  return api(path,{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify(payload)});
}
const mobileReadinessEpochs=new Map();
async function mobileReadiness(hintSel){
  const epoch=(mobileReadinessEpochs.get(hintSel)||0)+1;
  mobileReadinessEpochs.set(hintSel,epoch);
  const hint=$(hintSel);if(hint)hint.textContent='Checking TLS and captured traffic…';
  try{
    const [tls,readiness]=await Promise.all([api('/api/tls-diagnosis'),api('/api/readiness')]);
    if(mobileReadinessEpochs.get(hintSel)!==epoch)return null;
    const traffic=(readiness.checks||[]).find(c=>c.id==='traffic');
    const tlsCheck=(readiness.checks||[]).find(c=>c.id==='tls_intercept');
    const observed=!traffic?.ok?'no captured traffic yet'
      :!tlsCheck?.ok?'traffic exists, but intercepted HTTPS is not verified'
      :'the project already contains intercepted HTTPS traffic';
    const nextAction=!traffic?.ok?(traffic?.fix||'route traffic through the proxy')
      :!tlsCheck?.ok?(tls.fix||tlsCheck?.fix||'trust the CA and trigger HTTPS')
      :'open Proxy History and identify the new device request';
    if(hint)hint.textContent='Project-wide readiness: '+observed+'; this does not verify the selected device. After setup, send a new HTTPS request from this device, then '+nextAction+'.';
    return {tls,readiness};
  }catch(e){if(mobileReadinessEpochs.get(hintSel)===epoch)renderLoadError(hint,'Mobile readiness',e,()=>mobileReadiness(hintSel),false);return null;}
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

export async function loadAndroid({allowDuringAction=false}={}){
  if(androidActionPending&&!allowDuringAction)return null;
  const epoch=++androidLoadEpoch;
  const sec=$('#androidAdbSection'),hint=$('#androidAdbHint');
  const lanHint=$('#androidLanHint'),caHint=$('#androidCaHint');
  if(!sec){androidDeviceActionsReady=false;return {ok:false,ready:false};}
  try{
    const s=await api('/api/android/status');
    if(epoch!==androidLoadEpoch||(androidActionPending&&!allowDuringAction))return null;
    if(!s.available){
      androidDeviceActionsReady=false;
      setAndroidDeviceActionsEnabled(false);
      sec.style.display='none';
      return {ok:true,ready:false};
    }
    sec.style.display='';
    const devs=s.devices||[];
    renderAndroidDevicePicker(devs);
    androidDeviceActionsReady=devs.some(d=>d.state==='device')&&!!androidSerial();
    setAndroidDeviceActionsEnabled(androidDeviceActionsReady);
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
    return {ok:true,ready:androidDeviceActionsReady};
  }catch(e){
    if(epoch!==androidLoadEpoch||(androidActionPending&&!allowDuringAction))return null;
    androidDeviceActionsReady=false;
    sec.style.display='';
    renderLoadError(hint,'Android device discovery',e,loadAndroid,false);
    setAndroidDeviceActionsEnabled(false);
    return {ok:false,ready:false};
  }
}

async function androidAction(fn){
  if(androidActionPending)return false;
  androidActionPending=true;androidDeviceActionsReady=false;androidLoadEpoch++;setAndroidActionBusy(true);
  let error=null;
  try{
    try{await fn();}catch(e){toast(e.message);error=e;}
    const status=await loadAndroid({allowDuringAction:true});
    setAndroidActionBusy(true);
    if(error||!status?.ok)return false;
    await mobileReadiness('#androidAdbHint');
    return true;
  }finally{
    androidActionPending=false;androidLoadEpoch++;setAndroidActionBusy(false);
  }
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
  closeAndroidDeviceMenu(true);
  loadAndroid();
});}
document.addEventListener('click',()=>closeAndroidDeviceMenu());
{const wrap=$('#androidDeviceSelectWrap');if(wrap)wrap.addEventListener('keydown',e=>{
  if(e.key==='Escape')closeAndroidDeviceMenu(true);
});}
wireDeviceMenuKeyboard($('#androidDeviceMenu'),$('#androidDeviceTrigger'),toggleAndroidDeviceMenu,closeAndroidDeviceMenu);
{const lh=$('#androidLanHint');if(lh)lh.addEventListener('click',e=>{if(e.target.closest('#androidOpenProxyBtn'))openSettingsProxy();});}
$('#androidRefreshBtn')&&($('#androidRefreshBtn').onclick=()=>androidAction(async()=>{}));
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
let iosLoadEpoch=0;
let iosActionPending=false;

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

function closeIOSDeviceMenu(returnFocus=false){
  const menu=$('#iosDeviceMenu'),trigger=$('#iosDeviceTrigger');
  if(menu)menu.hidden=true;
  if(trigger)trigger.setAttribute('aria-expanded','false');
  if(returnFocus&&trigger)trigger.focus(); // keyboard focus return to its combobox trigger
}

function toggleIOSDeviceMenu(){
  const menu=$('#iosDeviceMenu'),trigger=$('#iosDeviceTrigger');
  if(!menu||!trigger||trigger.disabled)return;
  if(!menu.hidden){closeIOSDeviceMenu();return;}
  // See toggleAndroidDeviceMenu(): only one popover may stay open.
  closeAndroidDeviceMenu();
  closeAllUiSelects();
  menu.hidden=false;
  trigger.setAttribute('aria-expanded','true');
}

function renderIOSDevicePicker(devs){
  const menu=$('#iosDeviceMenu'),trigger=$('#iosDeviceTrigger'),valueEl=$('#iosDeviceValue'),meta=$('#iosDeviceMeta');
  if(!menu||!trigger||!valueEl)return;
  trigger.setAttribute('aria-label','iOS device');
  trigger.setAttribute('aria-controls','iosDeviceMenu');
  trigger.setAttribute('aria-haspopup','listbox');
  closeIOSDeviceMenu();
  if(!devs.length){
    iosDeviceUDID='';
    valueEl.textContent='Manual profile (no simulator/device detected)';
    trigger.disabled=true;
    menu.innerHTML='';
    if(meta)meta.textContent='Download profile and open on iPhone Safari, or boot a simulator.';
    return;
  }
  trigger.disabled=iosActionPending;
  if(!devs.some(d=>d.udid===iosDeviceUDID)){
    const booted=devs.find(d=>d.booted)||devs.find(d=>d.kind==='physical')||devs[0];
    iosDeviceUDID=booted?booted.udid:'';
  }
  menu.innerHTML=devs.map(d=>{
    const sel=d.udid===iosDeviceUDID;
    return `<button type="button" role="option" id="ios-device-option-${escAttr(d.udid)}" class="ui-select-opt${sel?' sel':''}" data-device-option="${escAttr(d.udid)}" data-udid="${escAttr(d.udid)}" aria-selected="${sel?'true':'false'}"><span class="ui-select-opt-title">${esc(iosDeviceTitle(d))}</span><span class="ui-select-opt-sub">${esc(iosDeviceMeta(d))}</span></button>`;
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

export async function loadIOS({allowDuringAction=false}={}){
  if(iosActionPending&&!allowDuringAction)return null;
  const epoch=++iosLoadEpoch;
  const sec=$('#iosSection'),hint=$('#iosHint'),lanHint=$('#iosLanHint'),profileLink=$('#iosProfileLink');
  if(!sec)return {ok:false};
  try{
    const s=await api('/api/ios/status');
    if(epoch!==iosLoadEpoch||(iosActionPending&&!allowDuringAction))return null;
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
      if(s.deviceError)html+=(html?'<br>':'')+`<span class="state-error-msg">Device discovery failed: ${esc(s.deviceError)}. Install or select Xcode, or use the manual profile flow.</span>`;
      lanHint.innerHTML=html;
      lanHint.style.display=html?'':'none';
    }
    let msg='';
    if(s.deviceError)msg='Device discovery failed. Install or select Xcode, or download the profile for manual installation.';
    else if(!devs.length)msg='Boot an iOS Simulator or connect an iPhone — or download the profile for manual install.';
    else if(!s.simctlAvailable)msg='Simulator automation needs Xcode on macOS. Physical devices: download profile → open in Safari on the phone.';
    if(hint)hint.textContent=msg;
    return {ok:!s.deviceError};
  }catch(e){
    if(epoch!==iosLoadEpoch||(iosActionPending&&!allowDuringAction))return null;
    sec.style.display='';
    renderLoadError(hint,'iOS device discovery',e,loadIOS,false);
    return {ok:false};
  }
}

async function iosAction(fn){
  if(iosActionPending)return false;
  iosActionPending=true;iosLoadEpoch++;setIOSActionBusy(true);
  let error=null;
  try{
    try{await fn();}catch(e){toast(e.message);error=e;}
    const status=await loadIOS({allowDuringAction:true});
    if(error||!status?.ok)return false;
    await mobileReadiness('#iosHint');
    return true;
  }finally{
    iosActionPending=false;iosLoadEpoch++;setIOSActionBusy(false);
  }
}

function setIOSActionBusy(busy){
  ['iosRefreshBtn','iosSetupAllBtn','iosInstallCaBtn','iosOpenProfileBtn']
    .forEach(id=>{const button=$('#'+id);if(button){setAriaBusy(button,busy);button.disabled=busy;}});
  const trigger=$('#iosDeviceTrigger');if(trigger)trigger.disabled=busy||!$('#iosDeviceMenu')?.querySelector('[role="option"]');
  $('#iosProxyMode')?.querySelectorAll('button').forEach(button=>{button.disabled=busy;});
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
  closeIOSDeviceMenu(true);
  loadIOS();
});}
document.addEventListener('click',()=>closeIOSDeviceMenu());
// Escape must also dismiss while focus is still on the trigger (the menu's own
// handler only fires once focus has moved inside it) — same as Android.
{const wrap=$('#iosDeviceSelectWrap');if(wrap)wrap.addEventListener('keydown',e=>{
  if(e.key==='Escape')closeIOSDeviceMenu(true);
});}
{const lh=$('#iosLanHint');if(lh)lh.addEventListener('click',e=>{if(e.target.closest('#iosOpenProxyBtn'))openSettingsProxy();});}
wireDeviceMenuKeyboard($('#iosDeviceMenu'),$('#iosDeviceTrigger'),toggleIOSDeviceMenu,closeIOSDeviceMenu);
$('#iosRefreshBtn')&&($('#iosRefreshBtn').onclick=()=>iosAction(async()=>{}));
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
let iosSshLoadEpoch=0;
let iosSshActionPending=false;
let iosSshRestored=false;
let iosSshDirty=false;

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
  if(iosSshRestored||iosSshDirty)return;
  iosSshRestored=true;
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

export async function loadIOSSsh({allowDuringAction=false}={}){
  if(iosSshActionPending&&!allowDuringAction)return null;
  const epoch=++iosSshLoadEpoch;
  const sec=$('#iosSshSection'),hint=$('#iosSshHint'),lanHint=$('#iosSshLanHint');
  if(!sec)return {ok:false};
  iosSshRestore();
  try{
    const s=await api('/api/ios/ssh/status');
    if(epoch!==iosSshLoadEpoch||(iosSshActionPending&&!allowDuringAction))return null;
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
    return {ok:true};
  }catch(e){
    if(epoch!==iosSshLoadEpoch||(iosSshActionPending&&!allowDuringAction))return null;
    sec.style.display='';
    renderLoadError(hint,'iOS SSH discovery',e,loadIOSSsh,false);
    return {ok:false};
  }
}

async function iosSshAction(fn){
  if(iosSshActionPending)return false;
  iosSshActionPending=true;iosSshLoadEpoch++;setIOSSshActionBusy(true);
  let error=null;
  try{
    try{await fn();}catch(e){toast(e.message);error=e;}
    const status=await loadIOSSsh({allowDuringAction:true});
    if(error||!status?.ok)return false;
    await mobileReadiness('#iosSshHint');
    return true;
  }finally{
    iosSshActionPending=false;iosSshLoadEpoch++;setIOSSshActionBusy(false);
  }
}

function setIOSSshActionBusy(busy){
  ['iosSshStatusBtn','iosSshSetupBtn','iosSshInstallCaBtn']
    .forEach(id=>{const button=$('#'+id);if(button){setAriaBusy(button,busy);button.disabled=busy;}});
}

{const lh=$('#iosSshLanHint');if(lh)lh.addEventListener('click',e=>{if(e.target.closest('#iosSshOpenProxyBtn'))openSettingsProxy();});}
['iosSshHost','iosSshPort','iosSshUser','iosSshKeyPath'].forEach(id=>{
  const el=$('#'+id);
  if(el){el.addEventListener('input',()=>{iosSshDirty=true;});el.addEventListener('change',iosSshRemember);}
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
