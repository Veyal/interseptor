// discovery.js — Content discovery (forced-browse) tab. Brute-forces paths from
// a wordlist against a base URL via /api/discovery/*, streaming results over the
// SSE 'discovery.update' event. Found endpoints are (optionally) recorded as
// flows server-side, so they also show up in History and the Map.
import { $, esc, escAttr, api, toast, statusColor, fmtSize, pickTextFile, applyTextList, countListLines } from './core.js';
import { sendToRepeater } from './tools.js';
import { flowPopup } from './flowmodal.js';

let wordlistLoaded = false;
const DSC_WORD_OPEN = 'discover.wordOpen';

function setWordlistOpen(open){
  const w=$('#dscWordWrap'), btn=$('#dscWordBtn');
  if(w) w.style.display=open?'block':'none';
  if(btn) btn.setAttribute('aria-expanded', String(open));
  try{ localStorage.setItem(DSC_WORD_OPEN, open?'1':'0'); }catch(e){}
}

export async function loadDiscovery(){
  if(!wordlistLoaded){
    wordlistLoaded = true;
    const ta = $('#dscWords');
    if(ta && !ta.value.trim()){
      try{ ta.value = await (await fetch('/api/discovery/wordlist')).text(); }catch(e){}
    }
    updateWordCount();
    try{
      if(localStorage.getItem(DSC_WORD_OPEN)==='1') setWordlistOpen(true);
    }catch(e){}
  }
  refreshDiscovery();
}

export async function refreshDiscovery(){
  try{ render(await api('/api/discovery/state')); }catch(e){}
}

export function prefillDiscovery(baseUrl){
  const b = $('#dscBase');
  if(b) b.value = baseUrl;
  const tab = document.querySelector('.tab[data-tab="discover"]');
  if(tab) tab.click();
  loadDiscovery();
  if(b){ b.focus(); }
  toast('Discover ready for '+baseUrl+' — press Start');
}

function updateWordCount(){
  const ta = $('#dscWords'), out = $('#dscWordCount');
  if(!ta || !out) return;
  const n = ta.value.split('\n').filter(l=>{const t=l.trim();return t && !t.startsWith('#');}).length;
  out.textContent = n ? n+' words' : '';
}

async function dscOpenResult(url, flowId){
  let id = flowId;
  if(!id){
    try{
      const d = await api('/api/discovery/inspect',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({url})});
      id = d.flowId;
    }catch(e){ toast(e.message); return; }
  }
  flowPopup(id);
}

function hostFromBase(){
  const raw = (($('#dscBase')||{}).value||'').trim();
  if(!raw) return '';
  try{ return new URL(raw).hostname; }catch(e){ return ''; }
}

function appendWords(lines){
  const ta = $('#dscWords');
  if(!ta) return;
  const existing = new Set(ta.value.split('\n').map(l=>l.trim()).filter(l=>l && !l.startsWith('#')));
  const add = lines.filter(l=>l && !existing.has(l));
  if(!add.length){ toast('no new paths to add'); return; }
  ta.value = (ta.value.trim() ? ta.value.trim()+'\n' : '') + add.join('\n');
  updateWordCount();
  toast('added '+add.length+' path'+(add.length===1?'':'s'));
}

async function seedsFromHistory(){
  const host = hostFromBase();
  if(!host){ toast('enter a base URL first'); return; }
  try{
    const d = await api('/api/discovery/seeds?host='+encodeURIComponent(host));
    appendWords(d.paths||[]);
  }catch(e){ toast(e.message); }
}

async function suggestPaths(){
  const base = (($('#dscBase')||{}).value||'').trim();
  const host = hostFromBase();
  if(!host && !base){ toast('enter a base URL first'); return; }
  const q = host ? 'host='+encodeURIComponent(host) : 'baseUrl='+encodeURIComponent(base);
  try{
    const d = await api('/api/discovery/suggest?'+q);
    appendWords(d.paths||[]);
    if(d.aiNote) toast(d.aiNote);
  }catch(e){ toast(e.message); }
}

async function fromScope(){
  try{
    const d = await api('/api/discovery/scope-targets');
    const bases = d.bases||[];
    if(!bases.length){ toast('no include-scope hosts — add scope rules in Settings'); return; }
    const b = $('#dscBase');
    if(b) b.value = bases[0];
    if(bases.length > 1) toast(bases.length+' scope targets — using '+bases[0]);
    else toast('filled from scope');
  }catch(e){ toast(e.message); }
}

function render(st){
  const running = !!(st && st.running);
  const start = $('#dscStart'), stop = $('#dscStop'), count = $('#dscCount');
  if(start) start.disabled = running;
  if(stop) stop.disabled = !running;
  if(count){
    const found = (st && st.found) || 0, tried = (st && st.tried) || 0;
    count.textContent = running ? `scanning… ${found} found / ${tried} tried`
      : tried ? `${found} found / ${tried} tried` : '';
  }
  const box = $('#dscResults');
  if(!box) return;
  const results = (st && st.results) || [];
  if(!results.length){
    if(running){ box.innerHTML = '<div class="hint" style="padding:16px">Calibrating &amp; probing…</div>'; return; }
    if(st && st.tried){ box.innerHTML = '<div class="empty" style="padding:24px">No paths found.<br>Try a bigger wordlist, add extensions, or check the base URL is reachable.</div>'; return; }
    return;
  }
  const rows = results.map(r=>{
    const c = statusColor(r.status);
    const dir = r.dir ? '<span title="directory" style="color:var(--fg3)"> /</span>' : '';
    const redir = r.redirect ? `<span class="hint" style="margin-left:8px">→ ${esc(r.redirect)}</span>` : '';
    const depth = r.depth ? `<span class="hint" style="margin-left:6px">d${r.depth}</span>` : '';
    return `<div class="trow dsc-row" data-url="${escAttr(r.url)}" data-flow="${r.flowId||''}" style="display:flex;align-items:center;gap:10px;padding:5px 12px;border-bottom:1px solid var(--line);cursor:pointer" title="${escAttr(r.url)} — click to inspect">
      <span style="font-weight:700;color:${c};min-width:34px">${r.status||'—'}</span>
      <span class="hint" style="min-width:74px;text-align:right">${fmtSize(r.length||0)}</span>
      <span class="dsc-path" style="flex:1;font-family:var(--mono);font-size:12px;color:var(--fg);overflow:hidden;text-overflow:ellipsis;white-space:nowrap">${esc(r.path)}${dir}${depth}${redir}</span>
      <span class="hint" style="min-width:90px">${esc((r.contentType||'').split(';')[0])}</span>
      <button class="btn dsc-rep" data-url="${escAttr(r.url)}" title="Send to Repeater">→ Rep</button>
    </div>`;
  }).join('');
  const note = st && st.note ? `<div class="hint" style="padding:6px 12px;color:var(--amber)">${esc(st.note)}</div>` : '';
  box.innerHTML = `<div style="position:sticky;top:0;background:var(--bg2);border-bottom:1px solid var(--line2);padding:5px 12px;display:flex;gap:10px;font-size:9px;font-weight:700;letter-spacing:.5px;color:var(--fg3)"><span style="min-width:34px">CODE</span><span style="min-width:74px;text-align:right">SIZE</span><span style="flex:1">PATH</span><span style="min-width:90px">TYPE</span><span style="min-width:52px"></span></div>${rows}${note}`;
  box.querySelectorAll('.dsc-row').forEach(row=>row.onclick=e=>{
    if(e.target.closest('.dsc-rep')) return;
    dscOpenResult(row.dataset.url, parseInt(row.dataset.flow,10)||0);
  });
  box.querySelectorAll('.dsc-rep').forEach(btn=>btn.onclick=async e=>{
    e.stopPropagation();
    const url = btn.dataset.url;
    try{
      const flow = await api('/api/repeater/send',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({method:'GET',url,headers:'',body:''})});
      sendToRepeater(flow);
    }catch(err){ toast(err.message); }
  });
}

async function loadWordlistFile(append){
  try{
    const got=await pickTextFile();
    if(!got) return;
    const ta=$('#dscWords');
    applyTextList(ta, got.text, {append});
    updateWordCount();
    const n=countListLines(got.text, true);
    toast((append?'appended ':'loaded ')+n+' path'+(n===1?'':'s')+' from '+got.name);
  }catch(e){ toast(e.message); }
}

async function start(){
  const base = ($('#dscBase')||{}).value;
  if(!base || !base.trim()){ toast('enter a base URL'); $('#dscBase')&&$('#dscBase').focus(); return; }
  const body = {
    baseUrl: base.trim(),
    wordlist: ($('#dscWords')||{}).value || '',
    extensions: ($('#dscExt')||{}).value || '',
    threads: parseInt(($('#dscThreads')||{}).value,10) || 20,
    delayMs: parseInt(($('#dscDelay')||{}).value,10) || 0,
    recursive: !!($('#dscRec')||{}).checked,
    maxDepth: parseInt(($('#dscDepth')||{}).value,10) || 0,
    filterLen: parseInt(($('#dscFilterLen')||{}).value,10) || 0,
    record: !!($('#dscRecord')||{}).checked,
    autoTagApi: !!($('#dscAutoTagApi')||{}).checked,
  };
  const sb=$('#dscStart'); if(sb) sb.disabled=true; // prevent double-submit before the server reports running
  try{ await api('/api/discovery/start',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify(body)}); refreshDiscovery(); }
  catch(e){ toast(e.message||'could not start'); if(sb) sb.disabled=false; }
}

async function stop(){
  try{ await api('/api/discovery/stop',{method:'POST'}); toast('discovery stopped'); }
  catch(e){ toast(e.message||'could not stop'); }
}

{const b=$('#dscStart'); if(b) b.onclick=start;}
{const b=$('#dscStop'); if(b) b.onclick=stop;}
{const b=$('#dscScope'); if(b) b.onclick=fromScope;}
{const b=$('#dscSeeds'); if(b) b.onclick=seedsFromHistory;}
{const b=$('#dscAi'); if(b) b.onclick=suggestPaths;}
{const b=$('#dscBase'); if(b) b.addEventListener('keydown',e=>{if(e.key==='Enter'){e.preventDefault();start();}});}
{const b=$('#dscWordBtn'); if(b) b.onclick=()=>{const w=$('#dscWordWrap'); if(!w) return; const open=w.style.display==='none'; setWordlistOpen(open);};}
{const b=$('#dscWordLoad'); if(b) b.onclick=()=>loadWordlistFile(false);}
{const b=$('#dscWordAppend'); if(b) b.onclick=()=>loadWordlistFile(true);}
{const t=$('#dscWords'); if(t) t.addEventListener('input',updateWordCount);}
