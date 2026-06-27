import { $, esc, api, state, toast } from './core.js';
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
export function renderActivity(){
  const box=$('#actFeed');if(!box)return;
  const a=state.activity;
  $('#actCount').textContent=a.length?a.length+(a.length===1?' action':' actions'):'';
  if(!a.length){box.innerHTML='<div class="empty">No AI activity yet.<br>Point your AI assistant at this project over MCP (API → MCP) and its every move shows up here, live.</div>';return;}
  box.innerHTML=a.map((it,i)=>{
    const fid=flowIdFromActivity(it);
    const grp=i>0&&!sameWorkflow(a[i-1],it)?' act-grp':''; // separator between workflows
    return `<div class="act-row${fid?' act-jump':''}${grp}" data-flow="${fid||''}" data-i="${i}" title="${fid?'Open flow #'+fid+' in History':''}">
    <span class="ok" style="background:${it.ok?'var(--accent)':'var(--red)'}" title="${it.ok?'ok':'error'}"></span>
    <span class="act-tool">${esc(it.tool)}</span>
    <span class="act-sum">${esc(it.summary||'')}</span>
    <span class="act-res">${esc(it.result||'')}</span>
    <span class="act-meta">${it.ms}ms · ${actTime(it.ts)}</span>
    ${it.intent?`<span class="act-intent" title="the AI's stated reason">💭 ${esc(it.intent)}</span>`:''}
  </div>`;
  }).join('');
  box.querySelectorAll('.act-row.act-jump').forEach(row=>row.onclick=()=>{
    const id=Number(row.dataset.flow);
    if(!id)return;
    document.querySelector('.tab[data-tab="proxy"]').click();
    selectFlow(id);
  });
}
export let aiPulseTimer=null;
export function flashAiPulse(tool){
  const p=$('#aiPulse');if(!p)return;
  p.style.display='inline-flex';p.classList.add('live');
  const lbl=$('#aiPulseLbl');if(lbl)lbl.textContent=tool?('AI · '+tool):'AI active';
  clearTimeout(aiPulseTimer);
  aiPulseTimer=setTimeout(()=>{p.classList.remove('live');if(lbl)lbl.textContent='AI active';},2500);
}
export function onActivity(it){
  if(!it||state.aiDisabled)return;
  state.activity.unshift(it);
  if(state.activity.length>ACT_MAX)state.activity.length=ACT_MAX;
  const onTab=document.querySelector('.tab[data-tab="activity"]').classList.contains('active');
  if(onTab)renderActivity();
  else{state.actUnseen++;const b=$('#actBadge');if(b){b.style.display='inline-block';b.textContent=state.actUnseen;}}
  flashAiPulse(it.tool);
}
export async function loadActivity(){try{const d=await api('/api/activity');state.activity=d.activity||[];renderActivity();}catch(e){}}
export function clearActSeen(){state.actUnseen=0;const b=$('#actBadge');if(b)b.style.display='none';}
$('#actClear').onclick=async()=>{try{await api('/api/activity',{method:'DELETE'});}catch(e){}state.activity=[];renderActivity();clearActSeen();};
$('#aiPulse').onclick=()=>document.querySelector('.tab[data-tab="activity"]').click();
$('#aiPulse').addEventListener('keydown',e=>{if(e.key==='Enter'||e.key===' '){e.preventDefault();$('#aiPulse').click();}});
