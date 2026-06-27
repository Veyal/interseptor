// ai.js — the AI assist modal. Ask a free-text question about the selected flow(s)
// and the model's reply streams in token by token, rendered as Markdown. A footer
// action bar loads the analysed flow into Repeater / Intruder in one click.
import { $, api, openModal, closeModal, state, toast, renderMD, esc, copyText } from './core.js';
import { sendToRepeater, sendToIntruder } from './tools.js';

let aiKind = 'ask';         // only mode now: a free-text question
let aiLastText = '';        // last streamed/markdown text (for Copy)
let aiAbort = null;         // AbortController for the in-flight stream
let aiSeq = 0;              // bumped per request; stale runs must not touch the DOM
let aiQuestion = '';        // the free-text question being asked

// setStatus writes the small status line in the AI modal footer ("Thinking…",
// "Streaming…", ""). It is called throughout the run; a missing definition threw
// a ReferenceError before the request even fired, breaking the whole panel.
function setStatus(s) { const el = $('#aiStatus'); if (el) el.textContent = s || ''; }

// openAi opens the assist panel for the given flow(s) (or the current selection),
// ready for a free-text question — no preset mode is run; the user asks.
export function openAi(ids) {
  if (state.aiDisabled) { toast('AI features are disabled — enable in Settings → AI assist'); return; }
  state.aiIds = (ids && ids.length) ? ids.slice() : (state.selId != null ? [state.selId] : []);
  if (!state.aiIds.length) { toast('select a flow first'); return; }
  abortAi();
  aiLastText = '';
  const what = state.aiIds.length > 1 ? state.aiIds.length + ' selected flows' : 'this request / response';
  $('#aiOut').innerHTML = '<div class="hint">Ask anything about ' + what + ' — e.g. <i>“is the CSRF token validated?”</i>, <i>“what auth scheme is this?”</i>, <i>“suggest test payloads”</i>.</div>';
  setStatus('');
  updateActionBar();
  openModal($('#aiModal'));
  const qi = $('#aiQuestion'); if (qi) { qi.value = ''; setTimeout(() => qi.focus(), 30); }
}

export async function runAi(kind) {
  const seq = ++aiSeq; // invalidates any in-flight request
  aiKind = kind;
  abortAi();
  aiLastText = '';
  $('#aiOut').innerHTML = '';
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

function updateActionBar() {
  const single = state.aiIds.length === 1;
  $('#aiToRepeater').style.display = single ? '' : 'none';
  $('#aiToIntruder').style.display = single ? '' : 'none';
}

function showError(msg) {
  setStatus('');
  $('#aiOut').innerHTML = '<div class="hint" style="color:var(--red)">Error: ' + esc(msg) + '</div>'
    + '<div class="hint" style="margin-top:6px">Pick a provider and set its API key in Settings → AI assist (or the ANTHROPIC_API_KEY / OPENROUTER_API_KEY env var).</div>';
}

function abortAi() { if (aiAbort) { try { aiAbort.abort(); } catch (e) {} aiAbort = null; } $('#aiStop').style.display = 'none'; }

$('#aiExplainBtn') && ($('#aiExplainBtn').onclick = () => openAi());
// Ask the typed question about the selected flow(s).
function runAsk() {
  const q = ($('#aiQuestion').value || '').trim();
  if (!q) { $('#aiQuestion').focus(); return; }
  aiQuestion = q;
  runAi('ask');
}
$('#aiAskBtn') && ($('#aiAskBtn').onclick = runAsk);
$('#aiQuestion') && $('#aiQuestion').addEventListener('keydown', e => { if (e.key === 'Enter') { e.preventDefault(); runAsk(); } });
$('#aiClose').onclick = () => { abortAi(); closeModal($('#aiModal')); };
$('#aiStop').onclick = abortAi;
$('#aiToRepeater').onclick = () => { const id = state.aiIds[0]; if (id) { sendToRepeater({ id }); closeModal($('#aiModal')); } };
$('#aiToIntruder').onclick = () => { const id = state.aiIds[0]; if (id) { sendToIntruder({ id }); closeModal($('#aiModal')); } };
$('#aiCopy').onclick = () => copyText(aiLastText || '', 'copied');
