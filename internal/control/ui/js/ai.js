// ai.js — the AI assist modal. Explain / Summary stream the model's reply token by
// token and render it as Markdown; Payloads asks for structured test suggestions
// and renders them as cards you can copy or load straight into Intruder. A footer
// action bar turns the analysed flow into one-click Repeater / Intruder loads.
import { $, api, openModal, closeModal, state, toast, renderMD, esc, copyText } from './core.js';
import { sendToRepeater, sendToIntruder, setSniperPayloads } from './tools.js';

let aiKind = 'explain';     // current mode
let aiPayloads = [];        // structured suggestions (Payloads mode)
let aiLastText = '';        // last streamed/markdown text (for Copy)
let aiAbort = null;         // AbortController for the in-flight stream
let aiSeq = 0;              // bumped per request; stale runs must not touch the DOM
let aiQuestion = '';        // free-text question for the "ask" mode

// setStatus writes the small status line in the AI modal footer ("Thinking…",
// "Streaming…", ""). It is called throughout the run; a missing definition threw
// a ReferenceError before the request even fired, breaking the whole panel.
function setStatus(s) { const el = $('#aiStatus'); if (el) el.textContent = s || ''; }

export function openAi(kind, ids) {
  if (state.aiDisabled) { toast('AI features are disabled — enable in Settings → AI assist'); return; }
  state.aiIds = (ids && ids.length) ? ids.slice() : (state.selId != null ? [state.selId] : []);
  if (!state.aiIds.length) { toast('select a flow first'); return; }
  openModal($('#aiModal'));
  $('#aiKindSeg').querySelectorAll('button').forEach(b => { const on = b.dataset.k === kind; b.classList.toggle('on', on); b.setAttribute('aria-pressed', on ? 'true' : 'false'); });
  runAi(kind);
}

export async function runAi(kind) {
  const seq = ++aiSeq; // invalidates any in-flight request from a previous mode
  aiKind = kind;
  abortAi();
  aiPayloads = []; aiLastText = '';
  $('#aiPayloads').innerHTML = ''; $('#aiOut').innerHTML = '';
  updateActionBar();
  if (kind === 'suggest') { await loadActions(seq); return; }
  await streamAi(kind, seq);
}

let aiRenderTimer=null, aiPending='';
function scheduleAiRender(seq, text){
  aiPending=text;
  clearTimeout(aiRenderTimer);
  aiRenderTimer=setTimeout(()=>{
    if(seq!==aiSeq)return;
    $('#aiOut').innerHTML=renderMD(aiPending);
    $('#aiBody').scrollTop=$('#aiBody').scrollHeight;
  },90);
}

// streamAi consumes the SSE stream from /api/ai/assist/stream, re-rendering the
// accumulated Markdown on every delta. Falls back to the non-streaming endpoint if
// the stream can't be opened (older proxy, no Flusher, etc.).
async function streamAi(kind, seq) {
  const ids = state.aiIds;
  const body = ids.length > 1 ? { flowIds: ids, kind } : { flowId: ids[0], kind };
  if (kind === 'ask') body.question = aiQuestion;
  const ctrl = new AbortController(); aiAbort = ctrl;
  $('#aiStop').style.display = '';
  setStatus(ids.length > 1 ? `Analyzing ${ids.length} flows…` : 'Thinking…');
  let acc = '';
  try {
    const r = await fetch('/api/ai/assist/stream', {
      method: 'POST', headers: { 'content-type': 'application/json' },
      body: JSON.stringify(body), signal: ctrl.signal,
    });
    if (!r.ok || !r.body) throw new Error('stream-unavailable');
    const reader = r.body.getReader(), dec = new TextDecoder();
    let buf = '', streaming = false;
    for (;;) {
      const { value, done } = await reader.read();
      if (done) break;
      if (seq !== aiSeq) return; // a newer mode took over — stop touching the DOM
      buf += dec.decode(value, { stream: true });
      let idx;
      while ((idx = buf.indexOf('\n\n')) >= 0) {
        const chunk = buf.slice(0, idx); buf = buf.slice(idx + 2);
        handleSSE(chunk,
          t => { if (seq !== aiSeq) return; if (!streaming) { streaming = true; setStatus('Streaming…'); } acc += t; scheduleAiRender(seq, acc); },
          msg => { throw new Error(msg); });
      }
    }
    if (seq !== aiSeq) return;
    aiLastText = acc;
    $('#aiOut').innerHTML = renderMD(acc || '_(empty response)_');
    setStatus('');
  } catch (e) {
    if (seq !== aiSeq) return; // superseded; the newer run owns the UI
    if (ctrl.signal.aborted) { setStatus('stopped'); }
    else if (e.message === 'stream-unavailable') { await runAiNonStream(kind, seq); }
    else { showError(e.message); }
  } finally {
    if (seq === aiSeq) { $('#aiStop').style.display = 'none'; }
    if (aiAbort === ctrl) aiAbort = null;
  }
}

// handleSSE parses one "\n\n"-delimited SSE event. Text deltas arrive as a
// JSON-encoded string on a default-event data line; errors as event:error.
function handleSSE(chunk, onText, onErr) {
  let ev = 'message', data = '';
  chunk.split('\n').forEach(line => {
    if (line.startsWith('event:')) ev = line.slice(6).trim();
    else if (line.startsWith('data:')) data += line.slice(5).trim();
  });
  if (!data) return;
  if (ev === 'error') { let m = data; try { m = JSON.parse(data); } catch (e) {} onErr(m); return; }
  if (ev === 'done') return;
  try { const t = JSON.parse(data); if (typeof t === 'string') onText(t); } catch (e) {}
}

// runAiNonStream is the fallback: a single completion rendered as Markdown.
async function runAiNonStream(kind, seq) {
  const ids = state.aiIds;
  const body = ids.length > 1 ? { flowIds: ids, kind } : { flowId: ids[0], kind };
  if (kind === 'ask') body.question = aiQuestion;
  setStatus('Thinking…');
  try {
    const r = await api('/api/ai/assist', { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify(body) });
    if (seq !== aiSeq) return;
    aiLastText = r.text || '';
    $('#aiOut').innerHTML = renderMD(aiLastText || '_(empty response)_');
    setStatus('');
  } catch (e) { if (seq === aiSeq) showError(e.message); }
}

// loadActions fetches structured payload suggestions for the (single) flow and
// renders them as actionable cards.
async function loadActions(seq) {
  const id = state.aiIds[0];
  setStatus('Finding payloads…');
  $('#aiOut').innerHTML = '<div class="hint">Finding test payloads…</div>';
  try {
    const r = await api('/api/ai/actions', { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ flowId: id }) });
    if (seq !== aiSeq) return; // superseded by a newer mode switch
    aiPayloads = r.payloads || [];
    $('#aiOut').innerHTML = aiPayloads.length
      ? '<div class="hint" style="margin-bottom:8px">' + aiPayloads.length + ' suggested payloads. Each shows the recommended tool — <b>→ Repeater</b> for a one-shot manual probe (sends one request, you read the response), <b>→ Intruder</b> for fuzzing/enumeration over many values (mark <code>§</code> and Start).</div>'
      : '<div class="hint">No payload suggestions for this request.</div>';
    renderPayloads(aiPayloads);
    setStatus('');
    updateActionBar();
  } catch (e) { if (seq !== aiSeq) return; aiPayloads = []; showError(e.message); updateActionBar(); }
}

function renderPayloads(payloads) {
  const box = $('#aiPayloads');
  if (!payloads || !payloads.length) { box.innerHTML = ''; return; }
  box.innerHTML = payloads.map((p, i) => {
    const rep = (p.tool || '').toLowerCase() === 'repeater'; // AI's recommended tool
    const repBtn = `<button class="btn${rep ? ' accent' : ''}" data-act="rep" data-i="${i}" title="Load the request into Repeater (payload copied to clipboard)">→ Repeater</button>`;
    const intrBtn = `<button class="btn${rep ? '' : ' accent'}" data-act="intr" data-i="${i}" title="Stage this point for fuzzing in Intruder">→ Intruder</button>`;
    return `<div style="border:1px solid var(--line);border-radius:8px;padding:9px 11px;margin-bottom:8px">
      <div class="row" style="gap:8px;margin-bottom:5px">
        <span class="sev Info">${esc(p.point || 'param')}</span>
        <span class="hint" style="font-size:10px">${rep ? 'one-shot' : 'fuzz'}</span>
        <div class="spacer"></div>
        ${rep ? repBtn + intrBtn : intrBtn + repBtn}
        <button class="btn" data-act="copy" data-i="${i}" title="Copy payload">⧉</button>
      </div>
      <code style="display:block;background:var(--bg3);border-radius:4px;padding:6px 8px;overflow-wrap:anywhere;font-size:12px">${esc(p.payload || '')}</code>
      ${p.why ? `<div class="hint" style="margin-top:5px">${esc(p.why)}</div>` : ''}
    </div>`;
  }).join('');
  box.querySelectorAll('[data-act]').forEach(b => b.onclick = () => {
    const p = payloads[Number(b.dataset.i)];
    if (b.dataset.act === 'copy') { copyText(p.payload || '', 'payload copied'); return; }
    if (b.dataset.act === 'rep') { loadRepeater(p.payload); return; }
    loadIntruder([p.payload]);
  });
}

// loadIntruder stages the analysed request in Intruder with the given payload list
// pre-filled — the user places the § marker(s) and hits Start (load & stage; we
// never auto-fire attack payloads).
function loadIntruder(list) {
  const id = state.aiIds[0]; if (!id) return;
  const picked = (list || []).filter(Boolean);
  sendToIntruder({ id });
  setSniperPayloads(picked.join('\n')); // AI payloads go into the single Sniper list
  closeModal($('#aiModal'));
  toast('loaded request + ' + picked.length + ' payload(s) into Intruder · wrap the injection point in § and Start');
}

// loadRepeater stages the request in Repeater for a one-shot manual probe and copies
// the payload to the clipboard (Repeater has no payload slot — you paste it at the
// injection point, then Send).
function loadRepeater(payload) {
  const id = state.aiIds[0]; if (!id) return;
  sendToRepeater({ id });
  closeModal($('#aiModal'));
  if (payload) copyText(payload, 'request loaded in Repeater · payload copied — paste it at the injection point');
}

function updateActionBar() {
  const single = state.aiIds.length === 1;
  $('#aiToRepeater').style.display = single ? '' : 'none';
  $('#aiToIntruder').style.display = single ? '' : 'none';
  $('#aiAllIntruder').style.display = (single && aiKind === 'suggest' && aiPayloads.length) ? '' : 'none';
}

function showError(msg) {
  setStatus('');
  $('#aiOut').innerHTML = '<div class="hint" style="color:var(--red)">Error: ' + esc(msg) + '</div>'
    + '<div class="hint" style="margin-top:6px">Pick a provider and set its API key in Settings → AI assist (or the ANTHROPIC_API_KEY / OPENROUTER_API_KEY env var).</div>';
}

function abortAi() { if (aiAbort) { try { aiAbort.abort(); } catch (e) {} aiAbort = null; } $('#aiStop').style.display = 'none'; }

$('#aiExplainBtn').onclick = () => openAi('explain');
$('#aiKindSeg').querySelectorAll('button').forEach(b => b.onclick = () => { $('#aiKindSeg').querySelectorAll('button').forEach(x => { x.classList.toggle('on', x === b); x.setAttribute('aria-pressed', x === b ? 'true' : 'false'); }); runAi(b.dataset.k); });
// Free-text question: run the "ask" mode and clear the preset seg's active state.
function runAsk() {
  const q = ($('#aiQuestion').value || '').trim();
  if (!q) { $('#aiQuestion').focus(); return; }
  aiQuestion = q;
  $('#aiKindSeg').querySelectorAll('button').forEach(x => { x.classList.remove('on'); x.setAttribute('aria-pressed', 'false'); });
  runAi('ask');
}
$('#aiAskBtn') && ($('#aiAskBtn').onclick = runAsk);
$('#aiQuestion') && $('#aiQuestion').addEventListener('keydown', e => { if (e.key === 'Enter') { e.preventDefault(); runAsk(); } });
$('#aiClose').onclick = () => { abortAi(); closeModal($('#aiModal')); };
$('#aiStop').onclick = abortAi;
$('#aiToRepeater').onclick = () => { const id = state.aiIds[0]; if (id) { sendToRepeater({ id }); closeModal($('#aiModal')); } };
$('#aiToIntruder').onclick = () => { const id = state.aiIds[0]; if (id) { sendToIntruder({ id }); closeModal($('#aiModal')); } };
$('#aiAllIntruder').onclick = () => loadIntruder(aiPayloads.map(p => p.payload));
$('#aiCopy').onclick = () => {
  if (aiKind === 'suggest') copyText(aiPayloads.map(p => p.payload).filter(Boolean).join('\n'), 'payloads copied');
  else copyText(aiLastText || '', 'copied');
};
