import { $, esc, escAttr, api, state, toast, wireRowKey, renderLoadError, uiConfirm } from './core.js';
import { selectFlow } from './proxy.js';

/* ---- AI activity feed (glass box: watch what the AI is doing, live) ---- */
export const ACT_MAX=300;
export function actTime(ts){const d=new Date(ts);const p=n=>String(n).padStart(2,'0');return p(d.getHours())+':'+p(d.getMinutes())+':'+p(d.getSeconds());}
function flowIdFromActivity(it){
  const m=(it.result||it.summary||'').match(/flow #(\d+)/i);
  return m?Number(m[1]):null;
}
// sameWorkflow groups an AI's consecutive related tool calls: same stated intent,
// or — when neither states one — calls that fired close together in time (≤20s).
// The feed is newest-first, so `a` is the newer row sitting above the older `b`.
const WORKFLOW_GAP_MS=20000;
function sameWorkflow(a,b){
  if(!a||!b)return false;
  const ia=(a.intent||'').trim().toLowerCase(),ib=(b.intent||'').trim().toLowerCase();
  if(ia||ib)return ia===ib&&ia!=='';
  return Math.abs((a.ts||0)-(b.ts||0))<=WORKFLOW_GAP_MS;
}
// ---- client-side filtering over the loaded feed ----
// A single substring filter over each action's stated intent — enough to find
// "what was the AI trying to do" without a noisy mode toggle.
const actFilter={intent:''};
// Keep a failed feed load visible until a retry succeeds. Tab activation and
// live events both call renderActivity(), so without this guard they would
// replace a useful Retry action with a misleading empty state.
let activityLoadError=false;
let activityLoadGeneration=0;
let activityPendingEvents=[];
function activityEventKey(it){
  if(it&&it.id!=null)return 'id:'+it.id;
  return 'event:'+String(it?.ts||'')+'\u0000'+String(it?.tool||'')+'\u0000'+String(it?.summary||'')+'\u0000'+String(it?.result||'');
}
function mergeActivitySnapshot(snapshot){
  const merged=[],seen=new Set();
  [...activityPendingEvents,...(snapshot||[])].forEach(it=>{
    const key=activityEventKey(it);if(seen.has(key))return;seen.add(key);merged.push(it);
  });
  activityPendingEvents=[];
  return merged.slice(0,ACT_MAX);
}
function passesFilter(it){
  if(actFilter.intent&&!(it.intent||'').toLowerCase().includes(actFilter.intent))return false;
  return true;
}
export function renderActivity(){
  const box=$('#actFeed');if(!box)return;
  if(activityLoadError)return;
  const focusedId=box.querySelector(':focus[data-activity-id]')?.dataset.activityId||'';
  const all=state.activity;
  // Filter first, then group: separators must reflect the visible subset only,
  // so sameWorkflow() compares each row against the previous *visible* row.
  const a=all.filter(passesFilter);
  const total=all.length;
  $('#actCount').textContent=total?(a.length<total?a.length+' / '+total:total+(total===1?' action':' actions')):'';
  if(!total){box.innerHTML='<div class="state-empty"><div class="state-empty-icon"><svg class="icon" aria-hidden="true" focusable="false"><use href="#i-timeline"/></svg></div><div class="state-empty-title">No AI activity yet</div><p class="state-empty-hint">Assistant activity appears here when connected through Settings → API &amp; MCP.</p></div>';return;}
  if(!a.length){box.innerHTML='<div class="state-empty"><div class="state-empty-icon"><svg class="icon" aria-hidden="true" focusable="false"><use href="#i-search"/></svg></div><div class="state-empty-title">No matches</div><p class="state-empty-hint">No activity matches the current filter.</p></div>';return;}
  box.innerHTML=a.map((it,i)=>{
    const fid=flowIdFromActivity(it);
    const grp=i>0&&!sameWorkflow(a[i-1],it)?' act-grp':''; // separator between workflows
    const duration=it.ms == null?'—':it.ms+'ms';
    const status=it.ok?'Success':'Error';
    const activityId=it.id!=null?String(it.id):String(it._uiId||(it._uiId='client-'+Date.now()+'-'+i));
    const label=(it.tool||'activity')+': '+status+'. '+(it.summary||it.result||'');
    return `<div class="act-row${fid?' act-jump':''}${grp}"${fid?' tabindex="0" role="button"':''} data-activity-id="${escAttr(activityId)}" data-flow="${fid||''}" data-i="${i}" aria-label="${escAttr(label)}" title="${fid?'Open flow #'+fid+' in History':''}">
    <span class="ok" aria-hidden="true" style="background:${it.ok?'var(--accent)':'var(--red)'}" title="${status}"></span>
    <span class="act-tool">${esc(it.tool)}</span>
    <span class="act-sum">${esc(it.summary||'')}</span>
    <span class="act-res">${esc(it.result||'')}</span>
    <span class="act-meta">${duration} · ${actTime(it.ts)}</span>
    ${it.intent?`<span class="act-intent" title="the AI's stated reason"><svg class="icon" aria-hidden="true" focusable="false"><use href="#i-thought"/></svg> ${esc(it.intent)}</span>`:''}
  </div>`;
  }).join('');
  box.querySelectorAll('.act-row').forEach(row=>{const open=()=>{
    const id=Number(row.dataset.flow);
    if(!id)return;
    document.querySelector('.tab[data-tab="proxy"]').click();
    selectFlow(id);
  };if(row.classList.contains('act-jump')){row.onclick=open;wireRowKey(row,open);}});
  restoreActivityFocus(box,focusedId);
}
function restoreActivityFocus(box,focusedId){
  if(!focusedId)return;
  const row=box.querySelector(`[data-activity-id="${CSS.escape(focusedId)}"]`);
  if(row)row.focus({preventScroll:true});
}
export function onActivity(it){
  if(!it)return;
  activityPendingEvents.unshift(it);
  if(activityPendingEvents.length>ACT_MAX)activityPendingEvents.length=ACT_MAX;
  state.activity.unshift(it);
  if(state.activity.length>ACT_MAX)state.activity.length=ACT_MAX;
  const onTab=document.querySelector('.tab[data-tab="activity"]').classList.contains('active');
  if(onTab)renderActivity();
  else{state.actUnseen++;const b=$('#actBadge');if(b){b.style.display='inline-block';b.textContent=state.actUnseen;}}
}
export async function loadActivity(){
  const generation=++activityLoadGeneration;
  const box=$('#actFeed');if(box)box.dataset.loading='1';
  try{
    const d=await api('/api/activity');
    if(generation!==activityLoadGeneration)return;
    activityLoadError=false;
    state.activity=mergeActivitySnapshot(d.activity||[]);
    renderActivity();
  }
  catch(e){
    if(generation!==activityLoadGeneration)return;
    activityLoadError=true;
    renderLoadError(box,'Activity',e,loadActivity,state.activity.length>0);
  }
  finally{if(box&&generation===activityLoadGeneration)delete box.dataset.loading;}
}
export function clearActSeen(){state.actUnseen=0;const b=$('#actBadge');if(b)b.style.display='none';}
export function clearActivityLoadError(){
  activityLoadError=false;
  activityLoadGeneration++;
  activityPendingEvents=[];
  const box=$('#actFeed');
  if(box)delete box.dataset.loading;
}
let actClearInFlight=false;
$('#actClear').onclick=async()=>{
  if(actClearInFlight)return;
  if(!await uiConfirm('Clear activity','Remove all AI activity from this project? This cannot be undone.','Clear','btn danger','var(--red)'))return;
  const button=$('#actClear');
  actClearInFlight=true;
  if(button){button.disabled=true;button.setAttribute('aria-busy','true');button.textContent='Clearing…';}
  try{
    activityLoadGeneration++;
    activityPendingEvents=[];
    await api('/api/activity',{method:'DELETE'});
    state.activity=mergeActivitySnapshot([]);clearActivityLoadError();renderActivity();clearActSeen();
  }catch(e){toast('clear failed: '+e.message,'error');}
  finally{
    actClearInFlight=false;
    if(button){button.disabled=false;button.setAttribute('aria-busy','false');button.textContent='Clear';}
  }
};
// Free-text intent filter (substring, case-insensitive).
const actIntentFilter=$('#actIntentFilter');
if(actIntentFilter){
  let actFilterTimer=null;
  actIntentFilter.oninput=()=>{
    clearTimeout(actFilterTimer);
    actFilterTimer=setTimeout(()=>{
      actFilter.intent=actIntentFilter.value.trim().toLowerCase();
      renderActivity();
    },200);
  };
}
