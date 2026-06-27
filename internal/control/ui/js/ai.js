// ai.js — the AI assist modal. Ask a free-text question about the selected flow(s)
// and the model's reply streams in token by token, rendered as Markdown. Follow-up
// questions keep the thread (prior Q&A is sent as history). A footer action bar loads
// the analysed flow into Repeater / Intruder in one click.
import { $, api, openModal, closeModal, state, toast, renderMD, esc, copyText } from './core.js';
import { sendToRepeater, sendToIntruder } from './tools.js';

let aiKind = 'ask';         // only mode now: a free-text question
let aiLastText = '';        // last streamed/markdown text (for Copy)
let aiAbort = null;         // AbortController for the in-flight stream
let aiSeq = 0;              // bumped per request; stale runs must not touch the DOM
let aiQuestion = '';        // the free-text question being asked
let aiHistory = [];         // [{role, content}] completed + in-flight user turn
let aiStreaming = '';       // partial assistant reply while streaming

function setStatus(s) { const el = $('#aiStatus'); if (el) el.textContent = s || ''; }

function aiHintHtml() {
  const what = state.aiIds.length > 1 ? state.aiIds.length + ' selected flows' : 'this request / response';
  return '<div class="hint">Ask anything about ' + what + ' — e.g. <i>“is the CSRF token validated?”</i>, <i>“what auth scheme is this?”</i>, <i>“suggest test payloads”</i>. Follow-up questions stay in context. Enable <b>Let AI send requests</b> to let the model probe URLs (Anthropic only).</div>';
}

function renderAiChat() {
  if (!aiHistory.length && !aiStreaming) {
    $('#aiOut').innerHTML = aiHintHtml();
    return;
  }
  let html = '';
  for (const t of aiHistory) {
    if (t.role === 'user') {
      html += '<div class="ai-turn ai-turn-user"><div class="ai-turn-label">You</div><div class="ai-turn-body">' + esc(t.content) + '</div></div>';
    } else if (t.role === 'tool') {
      const ok = t.ok !== false;
      const label = esc(t.tool || 'tool') + (t.summary ? ' · ' + esc(t.summary) : '');
      html += '<div class="ai-turn ai-turn-tool"><div class="ai-turn-label">Tool' + (ok ? '' : ' · failed') + '</div><div class="ai-turn-body"><strong>' + label + '</strong>';
      if (t.result) html += '<pre style="margin:6px 0 0;white-space:pre-wrap">' + esc(t.result) + '</pre>';
      html += '</div></div>';
    } else {
      html += '<div class="ai-turn ai-turn-assistant"><div class="ai-turn-label">AI</div><div class="ai-turn-body md">' + renderMD(t.content) + '</div></div>';
    }
  }
  if (aiStreaming !== '') {
    html += '<div class="ai-turn ai-turn-assistant ai-turn-streaming"><div class="ai-turn-label">AI</div><div class="ai-turn-body md">' + renderMD(aiStreaming) + '</div></div>';
  }
  $('#aiOut').innerHTML = html;
  $('#aiBody').scrollTop = $('#aiBody').scrollHeight;
}

function updateAskPlaceholder() {
  const qi = $('#aiQuestion');
  if (!qi) return;
  qi.placeholder = aiHistory.length
    ? 'Ask a follow-up… (Enter)'
    : 'Ask anything about this request / response… (Enter)';
}

function resetAiChat() {
  aiHistory = [];
  aiStreaming = '';
  aiLastText = '';
  updateAskPlaceholder();
  renderAiChat();
}

// openAi opens the assist panel for the given flow(s) (or the current selection),
// ready for a free-text question — no preset mode is run; the user asks.
export function openAi(ids) {
  if (state.aiDisabled) { toast('AI features are disabled — enable in Settings → AI assist'); return; }
  state.aiIds = (ids && ids.length) ? ids.slice() : (state.selId != null ? [state.selId] : []);
  if (!state.aiIds.length) { toast('select a flow first'); return; }
  abortAi();
  resetAiChat();
  setStatus('');
  updateActionBar();
  openModal($('#aiModal'));
  const qi = $('#aiQuestion'); if (qi) { qi.value = ''; setTimeout(() => qi.focus(), 30); }
}

export async function runAi(kind) {
  const seq = ++aiSeq;
  aiKind = kind;
  abortAi();
  aiLastText = '';
  aiStreaming = '';
  await streamAi(kind, seq);
}

let aiRenderTimer = null;
function scheduleAiRender(seq) {
  clearTimeout(aiRenderTimer);
  aiRenderTimer = setTimeout(() => {
    if (seq !== aiSeq) return;
    renderAiChat();
  }, 90);
}

function assistBody(kind) {
  const ids = state.aiIds;
  const body = ids.length > 1 ? { flowIds: ids, kind } : { flowId: ids[0], kind };
  if (kind === 'ask') {
    body.question = aiQuestion;
    const hist = aiHistory.slice(0, -1).filter(t => t.role === 'user' || t.role === 'assistant');
    if (hist.length) body.history = hist;
    const toggle = $('#aiAgentToggle');
    if (toggle && toggle.checked) body.agent = true;
  }
  return body;
}

// streamAi consumes the SSE stream from /api/ai/assist/stream, re-rendering the
// accumulated Markdown on every delta. Falls back to the non-streaming endpoint if
// the stream can't be opened (older proxy, no Flusher, etc.).
async function streamAi(kind, seq) {
  const ids = state.aiIds;
  const body = assistBody(kind);
  const ctrl = new AbortController(); aiAbort = ctrl;
  $('#aiStop').style.display = '';
  setStatus(ids.length > 1 ? `Analyzing ${ids.length} flows…` : (aiHistory.length > 1 ? 'Thinking…' : 'Thinking…'));
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
      if (seq !== aiSeq) return;
      buf += dec.decode(value, { stream: true });
      let idx;
      while ((idx = buf.indexOf('\n\n')) >= 0) {
        const chunk = buf.slice(0, idx); buf = buf.slice(idx + 2);
        handleSSE(chunk,
          t => {
            if (seq !== aiSeq) return;
            if (!streaming) { streaming = true; setStatus('Streaming…'); }
            acc += t;
            aiStreaming = acc;
            scheduleAiRender(seq);
          },
          msg => { throw new Error(msg); },
          ev => {
            if (seq !== aiSeq) return;
            aiHistory.push({ role: 'tool', tool: ev.tool, summary: ev.summary, ok: ev.ok, result: ev.result || '' });
            renderAiChat();
            setStatus('Tool: ' + (ev.tool || '') + '…');
          });
      }
    }
    if (seq !== aiSeq) return;
    aiStreaming = '';
    aiLastText = acc;
    aiHistory.push({ role: 'assistant', content: acc || '_(empty response)_' });
    renderAiChat();
    setStatus('');
    updateAskPlaceholder();
  } catch (e) {
    if (seq !== aiSeq) return;
    if (ctrl.signal.aborted) {
      if (acc) {
        aiStreaming = '';
        aiHistory.push({ role: 'assistant', content: acc + '\n\n_(stopped)_' });
        renderAiChat();
      } else {
        aiHistory.pop();
        renderAiChat();
      }
      setStatus('stopped');
    } else if (e.message === 'stream-unavailable') { await runAiNonStream(kind, seq); }
    else {
      aiHistory.pop();
      renderAiChat();
      showError(e.message);
    }
  } finally {
    if (seq === aiSeq) { $('#aiStop').style.display = 'none'; aiStreaming = ''; }
    if (aiAbort === ctrl) aiAbort = null;
  }
}

function handleSSE(chunk, onText, onErr, onTool) {
  let ev = 'message', data = '';
  chunk.split('\n').forEach(line => {
    if (line.startsWith('event:')) ev = line.slice(6).trim();
    else if (line.startsWith('data:')) data += line.slice(5).trim();
  });
  if (!data) return;
  if (ev === 'error') { let m = data; try { m = JSON.parse(data); } catch (e) {} onErr(m); return; }
  if (ev === 'done') return;
  if (ev === 'tool' && onTool) {
    try { onTool(JSON.parse(data)); } catch (e) {}
    return;
  }
  try { const t = JSON.parse(data); if (typeof t === 'string') onText(t); } catch (e) {}
}

async function runAiNonStream(kind, seq) {
  const body = assistBody(kind);
  setStatus('Thinking…');
  try {
    const r = await api('/api/ai/assist', { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify(body) });
    if (seq !== aiSeq) return;
    aiLastText = r.text || '';
    aiHistory.push({ role: 'assistant', content: aiLastText || '_(empty response)_' });
    renderAiChat();
    setStatus('');
    updateAskPlaceholder();
  } catch (e) {
    if (seq !== aiSeq) return;
    aiHistory.pop();
    renderAiChat();
    showError(e.message);
  }
}

function updateActionBar() {
  const single = state.aiIds.length === 1;
  $('#aiToRepeater').style.display = single ? '' : 'none';
  $('#aiToIntruder').style.display = single ? '' : 'none';
}

function showError(msg) {
  setStatus('');
  const err = '<div class="hint" style="color:var(--red)">Error: ' + esc(msg) + '</div>'
    + '<div class="hint" style="margin-top:6px">Pick a provider and set its API key in Settings → AI assist (or the ANTHROPIC_API_KEY / OPENROUTER_API_KEY env var).</div>';
  if (aiHistory.length) {
    renderAiChat();
    $('#aiOut').insertAdjacentHTML('beforeend', err);
    $('#aiBody').scrollTop = $('#aiBody').scrollHeight;
  } else {
    $('#aiOut').innerHTML = err;
  }
}

function abortAi() { if (aiAbort) { try { aiAbort.abort(); } catch (e) {} aiAbort = null; } $('#aiStop').style.display = 'none'; }

function copyAiThread() {
  const lines = [];
  for (const t of aiHistory) {
    if (t.role === 'user') lines.push('You: ' + t.content);
    else if (t.role === 'tool') lines.push('Tool ' + (t.tool || '') + (t.summary ? ' (' + t.summary + ')' : '') + ': ' + (t.result || ''));
    else lines.push('AI: ' + t.content);
  }
  copyText(lines.join('\n\n') || aiLastText || '', 'copied');
}

$('#aiExplainBtn') && ($('#aiExplainBtn').onclick = () => openAi());

function runAsk() {
  const q = ($('#aiQuestion').value || '').trim();
  if (!q) { $('#aiQuestion').focus(); return; }
  aiQuestion = q;
  $('#aiQuestion').value = '';
  aiHistory.push({ role: 'user', content: q });
  renderAiChat();
  runAi('ask');
}

$('#aiAskBtn') && ($('#aiAskBtn').onclick = runAsk);
$('#aiQuestion') && $('#aiQuestion').addEventListener('keydown', e => { if (e.key === 'Enter') { e.preventDefault(); runAsk(); } });
$('#aiNewChat') && ($('#aiNewChat').onclick = () => { abortAi(); resetAiChat(); setStatus(''); const qi = $('#aiQuestion'); if (qi) { qi.value = ''; qi.focus(); } });
$('#aiClose').onclick = () => { abortAi(); closeModal($('#aiModal')); };
$('#aiStop').onclick = abortAi;
$('#aiToRepeater').onclick = () => { const id = state.aiIds[0]; if (id) { sendToRepeater({ id }); closeModal($('#aiModal')); } };
$('#aiToIntruder').onclick = () => { const id = state.aiIds[0]; if (id) { sendToIntruder({ id }); closeModal($('#aiModal')); } };
$('#aiCopy').onclick = () => copyAiThread();
