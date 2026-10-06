import { $, esc, escAttr, api, state, toast, wireRowKey, renderLoadError, uiConfirm, openFlow, copyText } from './core.js';
import { selectFlow } from './proxy.js';
import { ACTIVITY_CATEGORIES, activityCategory, filterActivity, categoryCounts, groupByDay, dayLabel, shouldDeferRender, pillLabel, activityAppendix, activityTargetOf } from './activity-model.js';

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
const actFilter={intent:'',cats:new Set()};
let actDeferredNew=0;
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
  return filterActivity([it],actFilter).length>0;
}
// Category chips are real toggle buttons with a text count; none pressed = all.
function renderActivityChips(all){
  const host=$('#actChips');if(!host)return;
  const counts=categoryCounts(all);
  host.innerHTML=ACTIVITY_CATEGORIES.map(c=>{
    const on=actFilter.cats.has(c.id);
    return `<button type="button" class="act-chip${on?' on':''}" data-cat="${c.id}" aria-pressed="${on}">${esc(c.label)} <span class="act-chip-n">${counts[c.id]}</span></button>`;
  }).join('');
  host.querySelectorAll('.act-chip').forEach(b=>b.onclick=()=>{
    const id=b.dataset.cat;
    if(actFilter.cats.has(id))actFilter.cats.delete(id);else actFilter.cats.add(id);
    renderActivity();
    $('#actChips')?.querySelector(`[data-cat="${id}"]`)?.focus();
  });
}
function activeFilterCount(){return actFilter.cats.size+(actFilter.intent?1:0);}
function clearActivityFilters(){
  actFilter.cats.clear();actFilter.intent='';
  const input=$('#actIntentFilter');if(input)input.value='';
  renderActivity();
}
function setActivityPill(n){
  actDeferredNew=n;
  const pill=$('#actNewPill');if(!pill)return;
  pill.hidden=n<=0;
  pill.textContent=n>0?pillLabel(n)+' — show':'';
  if(n>0)pill.setAttribute('aria-label',pillLabel(n)+' activity events, show them');
}
// Pending human input is owned by humaninput.js and rendered into #humanInputBar;
// the timeline only mirrors what is there, never a second source of truth.
function renderAttention(){
  const host=$('#actAttention');if(!host)return;
  const prompts=[...document.querySelectorAll('#humanInputBar .hi-prompt[data-id] .hi-msg')].map(n=>n.textContent.trim()).filter(Boolean);
  host.hidden=!prompts.length;
  host.textContent='';
  if(!prompts.length)return;
  const label=document.createElement('strong');
  label.textContent='Needs attention';
  const msg=document.createElement('span');
  msg.textContent=prompts.length===1?prompts[0]:prompts.length+' AI requests are waiting for your answer';
  const go=document.createElement('button');
  go.type='button';go.className='btn xs';go.textContent='Answer';
  go.onclick=()=>{const field=document.querySelector('#humanInputBar .hi-input, #humanInputBar .hi-opt');if(field)field.focus();};
  host.append(label,msg,go);
}
const attentionBar=document.getElementById('humanInputBar');
if(attentionBar&&typeof MutationObserver==='function')new MutationObserver(renderAttention).observe(attentionBar,{childList:true,subtree:true});
export function renderActivity(){
  const box=$('#actFeed');if(!box)return;
  if(activityLoadError)return;
  setActivityPill(0);
  const focusedId=box.querySelector(':focus[data-activity-id]')?.dataset.activityId||'';
  const openIds=new Set([...box.querySelectorAll('.act-expandable[aria-expanded="true"]')].map(row=>row.dataset.activityId));
  const all=state.activity;
  // Filter first, then group: separators must reflect the visible subset only,
  // so sameWorkflow() compares each row against the previous *visible* row.
  const a=all.filter(passesFilter);
  const total=all.length;
  renderActivityChips(all);
  renderAttention();
  $('#actCount').textContent=total?(a.length<total?a.length+' / '+total:total+(total===1?' action':' actions')):'';
  const appendix=$('#actAppendix');if(appendix)appendix.disabled=!a.length;
  if(!total){box.innerHTML='<div class="state-empty"><div class="state-empty-icon"><svg class="icon" aria-hidden="true" focusable="false"><use href="#i-timeline"/></svg></div><div class="state-empty-title">No activity yet</div><p class="state-empty-hint">Assistant activity appears here when connected through Settings → API &amp; MCP.</p></div>';return;}
  if(!a.length){
    const n=activeFilterCount();
    box.innerHTML=`<div class="state-empty"><div class="state-empty-icon"><svg class="icon" aria-hidden="true" focusable="false"><use href="#i-search"/></svg></div><div class="state-empty-title">No matches</div><p class="state-empty-hint">No activity matches ${n} ${n===1?'filter':'filters'}.</p><button type="button" class="btn" id="actClearFilters">Clear filters</button></div>`;
    $('#actClearFilters').onclick=clearActivityFilters;
    return;
  }
  const now=Date.now();
  let i=-1;
  box.innerHTML=groupByDay(a).map(g=>`<section class="act-day" aria-label="${escAttr(dayLabel(g.key,now))}"><h3 class="act-day-h">${esc(dayLabel(g.key,now))} <span class="act-day-n">${g.items.length}</span></h3>${g.items.map(it=>{
    i++;
    const prev=i>0?a[i-1]:null;
    const target=activityTargetOf(it);
    const fid=target?target.id:null;
    const grp=prev&&!sameWorkflow(prev,it)?' act-grp':''; // separator between workflows
    const duration=it.ms == null?'—':it.ms+'ms';
    const status=it.ok?'Success':'Error';
    const activityId=it.id!=null?String(it.id):String(it._uiId||(it._uiId='client-'+Date.now()+'-'+i));
    const label=(it.tool||'activity')+': '+status+'. '+(it.summary||it.result||'');
    const expandable=!fid;
    const expanded=expandable&&openIds.has(activityId);
    const cat=ACTIVITY_CATEGORIES.find(c=>c.id===activityCategory(it.tool));
    const detail=expandable?`<div class="act-detail" id="actDetail-${i}"${expanded?'':' hidden'}><div><b>Summary</b><span>${esc(it.summary||'—')}</span></div><div><b>Result</b><span>${esc(it.result||'—')}</span></div>${it.intent?`<div><b>Intent</b><span>${esc(it.intent)}</span></div>`:''}</div>`:'';
    return `<div class="act-row${fid?' act-jump':''}${expandable?' act-expandable':''}${expanded?' expanded':''}${grp}" tabindex="0" role="button"${expandable?` aria-expanded="${expanded}" aria-controls="actDetail-${i}"`:''} data-activity-id="${escAttr(activityId)}" data-flow="${fid||''}" data-i="${i}" aria-label="${escAttr(label)}"${fid?' title="Open flow #'+fid+'"':''}>
    <span class="ok ${it.ok?'is-ok':'is-fail'}" aria-hidden="true" title="${status}"></span>
    <span class="act-actor" title="Recorded from an AI assistant (MCP)"><svg class="icon" aria-hidden="true" focusable="false"><use href="#i-robot"/></svg>AI</span>
    <span class="act-tool">${esc(it.tool)}</span>
    <span class="act-cat">${esc(cat?cat.label:'Agent/MCP')}</span>
    <span class="act-sum">${esc(it.summary||'')}</span>
    <span class="act-res">${esc(it.result||'')}</span>
    ${fid?`<span class="act-target"><svg class="icon" aria-hidden="true" focusable="false"><use href="#i-link"/></svg>Flow #${fid}</span>`:''}
    <span class="act-outcome${it.ok?' is-ok':' is-fail'}">${status}</span>
    <span class="act-meta">${duration} · ${actTime(it.ts)}</span>
    ${it.intent?`<span class="act-intent" title="the AI's stated reason"><svg class="icon" aria-hidden="true" focusable="false"><use href="#i-thought"/></svg> ${esc(it.intent)}</span>`:''}
    ${detail}
  </div>`;
  }).join('')}</section>`).join('');
  box.querySelectorAll('.act-row').forEach(row=>{const open=()=>{
    if(window.getSelection()?.toString())return;
    const id=Number(row.dataset.flow);
    if(id){if(openFlow(id,{source:'activity'}))return;document.querySelector('.tab[data-tab="proxy"]').click();selectFlow(id);return;}
    if(!row.classList.contains('act-expandable'))return;
    const detail=row.querySelector('.act-detail');if(!detail)return;
    const expanded=row.getAttribute('aria-expanded')==='true';
    row.setAttribute('aria-expanded',expanded?'false':'true');
    detail.hidden=expanded;
    row.classList.toggle('expanded',!expanded);
  };row.onclick=open;wireRowKey(row,open);});
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
  if(onTab&&shouldDeferRender($('#actFeed')?.scrollTop))setActivityPill(actDeferredNew+1);
  else if(onTab)renderActivity();
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
$('#actNewPill')&&($('#actNewPill').onclick=()=>{
  renderActivity();
  const feed=$('#actFeed');if(feed)feed.scrollTop=0;
  feed?.querySelector('.act-row')?.focus({preventScroll:true});
});
// Copy the filtered log as a Markdown appendix for the report; text only, no bodies.
$('#actAppendix')&&($('#actAppendix').onclick=()=>{
  const rows=state.activity.filter(passesFilter);
  if(!rows.length){toast('no activity to copy');return;}
  copyText(activityAppendix(rows),'activity appendix copied');
});
