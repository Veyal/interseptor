// autopwn.js — Autopilot: the autonomous AI pentester panel. Lazy-loaded on first
// visit (like discovery.js/map.js). Fetches run state + past runs, renders the run
// controls, budget meters, and a live plan→candidate→verification→finding pipeline
// driven by the SSE `autopwn.update` event. Start is gated behind an explicit
// confirm because it launches fully-autonomous active scanning against in-scope
// targets; Stop is the kill switch. Verified findings deep-link to the Findings tab.
import { $, esc, escAttr, api, toast, uiConfirm } from './core.js';
import { openFinding } from './findings.js';

// ---- live run model (rebuilt from state, incrementally updated by SSE) ----
// candById lets a later verify/skip event update the exact candidate card in place;
// candOrder preserves first-seen order for a stable render.
let cur = null;              // last RunState
let candById = new Map();    // key -> candidate {vulnClass,severity,target,outcome,...}
let candOrder = [];          // keys in first-seen order
let planSteps = [];          // plan step summaries (if the frame ships full objects)
let planStepCount = 0;       // plan step count (the executing-phase frame ships an int)

const SEV_VAR = { critical:'var(--sev-critical)', high:'var(--sev-high)', medium:'var(--sev-medium)', low:'var(--sev-low)', info:'var(--sev-info)' };
function sevVar(s){ return SEV_VAR[String(s||'').toLowerCase()] || 'var(--sev-info)'; }

const PHASE_COLOR = { planning:'var(--blue)', executing:'var(--amber)', verifying:'var(--violet)', done:'var(--accent)', stopped:'var(--fg3)', error:'var(--red)' };

// candKey identifies a candidate across its verdict/skip updates. The engine does
// not emit a stable id per candidate, so we key on (vulnClass,target) which is the
// unit the plan/verifier works in.
function candKey(c){ return (c.vulnClass||'?') + '|' + (c.target||''); }

export async function loadAutopwn(){
  try{
    const [st, runs] = await Promise.all([
      api('/api/autopwn/state').catch(()=>null),
      api('/api/autopwn/runs').catch(()=>({runs:[]})),
    ]);
    seedFromState(st);
    render();
    renderHistory((runs&&runs.runs)||[]);
  }catch(e){ toast(e.message||'could not load Autopilot'); }
}

export async function refreshAutopwn(){ return loadAutopwn(); }

// seedFromState resets the live model from a full RunState snapshot (on load /
// resync). SSE deltas mutate the same structures thereafter.
function seedFromState(st){
  cur = st && st.runId ? st : null;
  candById = new Map();
  candOrder = [];
  planSteps = [];
  planStepCount = 0;
}

function fmtInt(n){ return (n||0).toLocaleString(); }
function pct(used,max){ if(!max) return 0; return Math.min(100, Math.round((used/max)*100)); }

function meter(label, used, max, valFmt){
  const p = pct(used,max);
  const cls = p>=100 ? 'over' : p>=80 ? 'warn' : '';
  const val = valFmt ? valFmt(used,max) : (fmtInt(used)+' / '+fmtInt(max));
  return `<div class="ap-meter">
    <div class="ap-meter-top"><span>${esc(label)}</span><span class="ap-meter-val">${esc(val)}</span></div>
    <div class="ap-meter-bar"><div class="ap-meter-fill ${cls}" style="width:${p}%"></div></div>
  </div>`;
}

function render(){
  const empty=$('#apEmpty'), run=$('#apRun'), start=$('#apStart'), stop=$('#apStop'), status=$('#apStatus');
  const active = !!(cur && cur.active);
  if(start) start.disabled = active;
  if(stop) stop.disabled = !active;
  // status/phase badge
  if(status){
    if(cur && cur.status){
      const ph = cur.phase && cur.phase!==cur.status ? ' · '+cur.phase : '';
      status.style.display='inline';
      status.textContent = cur.status + ph;
      status.style.background = PHASE_COLOR[cur.status] || PHASE_COLOR[cur.phase] || 'var(--fg3)';
    } else { status.style.display='none'; }
  }
  if(!cur){
    if(empty) empty.style.display='';
    if(run) run.style.display='none';
    return;
  }
  if(empty) empty.style.display='none';
  if(run) run.style.display='flex';
  renderMeters();
  renderPlan();
  renderCandidates();
  renderCounts();
}

function renderMeters(){
  const box=$('#apMeters'); if(!box||!cur) return;
  const b = cur.budget||{}, c = cur.consumed||{};
  box.innerHTML = [
    meter('Requests', c.requests, b.maxRequests),
    meter('Tokens', c.tokens, b.maxTokens),
    meter('Wall clock', c.wallMs, b.maxWallMs, (u,m)=>fmtDur(u)+' / '+fmtDur(m)),
  ].join('');
}

function fmtDur(ms){
  const s=Math.round((ms||0)/1000);
  if(s<60) return s+'s';
  const m=Math.floor(s/60), r=s%60;
  return m+'m'+(r?(' '+r+'s'):'');
}

function renderPlan(){
  const box=$('#apPlan'); if(!box) return;
  const note=$('#apPhaseNote');
  if(note && cur){
    const phase = cur.phase || '';
    note.textContent = planStepCount ? phase+' · '+planStepCount+' step'+(planStepCount===1?'':'s') : phase;
  }
  // The executing-phase SSE frame ships only a step COUNT (not the full plan), so
  // in the common live case we show a count summary; the persisted plan detail
  // isn't streamed. When a frame does carry full step objects, render them.
  if(planSteps.length){
    box.innerHTML = planSteps.map(s=>{
      const sev = s.severity ? `<span class="ap-sev" style="color:${sevVar(s.severity)}">${esc(s.severity)}</span>` : '';
      return `<div class="ap-plan-step">${sev}${esc(s.text)}</div>`;
    }).join('');
    return;
  }
  if(planStepCount){
    box.innerHTML = `<div class="ap-plan-step">Attack plan built — <b>${planStepCount}</b> step${planStepCount===1?'':'s'} queued. Progress streams into the Activity feed and candidates appear as they're verified.</div>`;
    return;
  }
  box.innerHTML = `<div class="ap-plan-empty">${cur&&cur.phase==='planning'?'Reading history &amp; building an attack plan…':'No plan yet.'}</div>`;
}

function outcomeHtml(c){
  if(c.outcome==='filed'){
    const conf = c.confidence!=null ? ' (conf '+c.confidence+')' : '';
    const link = c.findingId ? ` <a class="ap-cand-link" data-finding="${c.findingId}">view finding →</a>` : '';
    return `<span class="ap-out-filed">✓ filed${esc(conf)}</span>${link}`;
  }
  if(c.outcome==='rejected'){
    return `<span class="ap-out-rejected">✗ rejected${c.rejectedAt?' @ '+esc(c.rejectedAt):''}</span>`;
  }
  if(c.outcome==='skipped'){
    return `<span class="ap-out-skipped">skipped${c.reason?': '+esc(c.reason):''}</span>`;
  }
  return `<span class="ap-out-verifying">verifying…</span>`;
}

function renderCandidates(){
  const box=$('#apCandidates'); if(!box) return;
  if(!candOrder.length){
    box.innerHTML = `<div class="ap-plan-empty">No candidates yet — they appear as the run finds and verifies them.</div>`;
    return;
  }
  box.innerHTML = candOrder.map(k=>{
    const c = candById.get(k); if(!c) return '';
    return `<div class="ap-cand">
      <div class="ap-cand-top">
        <span class="ap-cand-class">${esc(c.vulnClass||'?')}</span>
        ${c.severity?`<span class="ap-cand-sev" style="color:${sevVar(c.severity)}">${esc(c.severity)}</span>`:''}
        <span class="ap-cand-target" title="${escAttr(c.target||'')}">${esc(c.target||'')}</span>
      </div>
      <div class="ap-cand-outcome">${outcomeHtml(c)}</div>
    </div>`;
  }).join('');
  box.querySelectorAll('.ap-cand-link[data-finding]').forEach(a=>{
    a.onclick=()=>{ const id=parseInt(a.dataset.finding,10); if(id) openFinding(id); };
  });
}

function renderCounts(){
  const el=$('#apCandCounts'); if(!el||!cur) return;
  const parts=[];
  if(cur.candidates!=null) parts.push(cur.candidates+' seen');
  if(cur.verified!=null) parts.push(cur.verified+' verified');
  if(cur.filed!=null) parts.push(cur.filed+' filed');
  if(cur.rejected!=null) parts.push(cur.rejected+' rejected');
  el.textContent = parts.join(' · ');
}

// ---- run history ----
function renderHistory(runs){
  const wrap=$('#apHistoryWrap'), box=$('#apHistory');
  if(!wrap||!box) return;
  if(!runs.length){ wrap.style.display='none'; return; }
  wrap.style.display='block';
  // most recent first
  const sorted = runs.slice().sort((a,b)=>(b.ts||0)-(a.ts||0));
  box.innerHTML = sorted.map(r=>{
    let sum={};
    try{ sum = r.summary ? JSON.parse(r.summary) : {}; }catch(e){}
    const counts = [];
    if(sum.candidates!=null) counts.push(sum.candidates+' cand');
    if(sum.filed!=null) counts.push(sum.filed+' filed');
    if(sum.rejected!=null) counts.push(sum.rejected+' rejected');
    const col = PHASE_COLOR[r.status] || 'var(--fg3)';
    return `<div class="ap-history-row">
      <span class="ap-hist-status" style="color:${col}">${esc(r.status||'—')}</span>
      <span class="ap-hist-time">${esc(fmtWhen(r.ts))}</span>
      <span class="ap-hist-sum">${esc(counts.join(' · ')||(r.error?'error':''))}</span>
    </div>`;
  }).join('');
}

function fmtWhen(ts){
  if(!ts) return '';
  const d=new Date(ts);
  const p=n=>String(n).padStart(2,'0');
  return d.getFullYear()+'-'+p(d.getMonth()+1)+'-'+p(d.getDate())+' '+p(d.getHours())+':'+p(d.getMinutes());
}

// ---- start / stop ----
async function start(){
  const maxRequests = parseInt(($('#apMaxReq')||{}).value,10) || 2000;
  const maxTokens = parseInt(($('#apMaxTok')||{}).value,10) || 500000;
  const maxMin = parseInt(($('#apMaxMin')||{}).value,10) || 30;
  const maxWallMs = maxMin * 60 * 1000;
  const targetHint = (($('#apTargetHint')||{}).value||'').trim();

  const ok = await uiConfirm(
    '⚠ Launch autonomous pentest?',
    `Autopilot runs a <b>fully-autonomous</b> active pentest. It will:<br><br>` +
    `• test <b>only targets that match your scope rules</b> (own listeners always excluded)<br>` +
    `• send <b>real attack traffic</b> to those targets<br>` +
    `• file <b>only machine-verified</b> findings (unproven candidates are never reported)<br><br>` +
    `Every request is visible in Proxy History and every step in the Activity feed. ` +
    `Confirm you are <b>authorized</b> to actively test the in-scope targets.`,
    'Start autonomous run', 'btn btn-danger'
  );
  if(!ok) return;

  const body = { budget:{ maxRequests, maxTokens, maxWallMs }, targetHint };
  const sb=$('#apStart'); if(sb) sb.disabled=true;
  try{
    const r = await api('/api/autopwn/start',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify(body)});
    seedFromState(r&&r.state);
    render();
    toast('Autopilot started');
  }catch(e){
    if(sb) sb.disabled=false;
    const msg=(e.message||'').toLowerCase();
    if(msg.includes('scope')) toast('Add target scope rules first — Autopilot refuses to run without a defined boundary');
    else if(msg.includes('active')||msg.includes('already')) toast('A run is already active — stop it first');
    else toast(e.message||'could not start');
  }
}

async function stop(){
  try{
    const st = await api('/api/autopwn/stop',{method:'POST'});
    if(st){ cur = st.runId ? st : cur; if(cur){ cur.active=false; } }
    render();
    toast('Autopilot stopping…');
  }catch(e){ toast(e.message||'could not stop'); }
}

// ---- SSE: incremental live update ----
// Each autopwn.update payload carries type:"autopwn.update". Variants:
//   phase:      {runId,phase,status} — plan variant adds steps[]
//   execute:    {phase:"executing",tool,target,ok}
//   candidate verdict: {phase:"verifying",candidate:{vulnClass,severity,target,proven,rejectedAt,findingId?,confidence?}}
//   candidate skip:    {phase:"verifying",candidate:{vulnClass,target,skipped:true,reason}}
//   finish:     {runId,status,phase,summary:{...},state:<RunState>}
export function onAutopwnUpdate(m){
  if(!m) return;
  // A finish frame ships a full RunState — adopt it wholesale (keeps meters/counts exact).
  if(m.state){
    cur = m.state.runId ? m.state : cur;
    if(cur){ cur.status = m.status||cur.status; cur.phase = m.phase||cur.phase; }
  } else {
    // ensure a live model exists even if the tab was opened after the run began
    if(!cur) cur = { runId:m.runId||0, active:true, status:m.status||'', phase:m.phase||'', budget:{}, consumed:{} };
    if(m.status) cur.status = m.status;
    if(m.phase) cur.phase = m.phase;
    if(m.runId) cur.runId = m.runId;
    // terminal phases end the run
    if(['done','stopped','error'].includes(m.status||m.phase)) cur.active=false;
  }

  // Plan info arrives on the planning-phase frame. The committed backend ships a
  // step COUNT (integer); tolerate a future array-of-steps form too.
  if(Array.isArray(m.steps)){
    planSteps = m.steps.map(s=>{
      if(typeof s==='string') return { text:s };
      const cls = Array.isArray(s.vulnClasses) ? s.vulnClasses.join(', ') : s.vulnClass;
      return { text:s.text||s.summary||[cls,s.target].filter(Boolean).join(' → ')||JSON.stringify(s), severity:s.severity };
    });
    planStepCount = planSteps.length;
  } else if(typeof m.steps==='number'){
    planStepCount = m.steps;
  }

  // candidate verdict / skip
  if(m.candidate){
    const c=m.candidate;
    const k=candKey(c);
    let rec=candById.get(k);
    if(!rec){ rec={ vulnClass:c.vulnClass, severity:c.severity, target:c.target, outcome:'verifying' }; candById.set(k,rec); candOrder.push(k); }
    if(c.severity) rec.severity=c.severity;
    if(c.skipped){ rec.outcome='skipped'; rec.reason=c.reason; }
    else if(c.findingId || c.proven===true){ rec.outcome='filed'; rec.findingId=c.findingId; if(c.confidence!=null) rec.confidence=c.confidence; }
    else if(c.rejectedAt || c.proven===false){ rec.outcome='rejected'; rec.rejectedAt=c.rejectedAt; }
    else rec.outcome='verifying';
  }

  // Only re-render if the panel is materialized (it always is once index.html loads,
  // but guard against a stray early event before the panel exists).
  if($('#apRun')) render();
  // refresh history on a finish frame so the just-completed run lands in the list
  if(m.state && ['done','stopped','error'].includes(m.status)){
    api('/api/autopwn/runs').then(r=>renderHistory((r&&r.runs)||[])).catch(()=>{});
  }
}

// ---- wire controls (top-level, like discovery.js) ----
{const b=$('#apStart'); if(b) b.onclick=start;}
{const b=$('#apStop'); if(b) b.onclick=stop;}
