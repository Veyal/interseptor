import { $, esc, api, state } from './core.js';

/* ---- AI activity feed (glass box: watch what the AI is doing, live) ---- */
export const ACT_MAX=300;
export function actTime(ts){const d=new Date(ts);const p=n=>String(n).padStart(2,'0');return p(d.getHours())+':'+p(d.getMinutes())+':'+p(d.getSeconds());}
export function renderActivity(){
  const box=$('#actFeed');if(!box)return;
  const a=state.activity;
  $('#actCount').textContent=a.length?a.length+(a.length===1?' action':' actions'):'';
  if(!a.length){box.innerHTML='<div class="empty">No AI activity yet.<br>Point your AI assistant at this project over MCP (API → MCP) and its every move shows up here, live.</div>';return;}
  box.innerHTML=a.map(it=>`<div class="act-row">
    <span class="ok" style="background:${it.ok?'var(--accent)':'var(--red)'}" title="${it.ok?'ok':'error'}"></span>
    <span class="act-tool">${esc(it.tool)}</span>
    <span class="act-sum">${esc(it.summary||'')}</span>
    <span class="act-res">${esc(it.result||'')}</span>
    <span class="act-meta">${it.ms}ms · ${actTime(it.ts)}</span>
  </div>`).join('');
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
  if(!it)return;
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
