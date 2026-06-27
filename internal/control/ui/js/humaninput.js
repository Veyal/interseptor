import { $, esc, escAttr, api, toast } from './core.js';

// AI→human handoff: when the AI calls request_human_input, a prompt shows in the
// top banner so the operator can answer/approve. Loaded on boot and refreshed on
// the SSE "human.input" event (so it survives reconnects).

export async function loadHumanInput() {
  try { const d = await api('/api/human-input'); renderHumanInput(d.prompts || []); }
  catch (e) { /* best-effort */ }
}

function renderHumanInput(prompts) {
  const bar = $('#humanInputBar'); if (!bar) return;
  if (!prompts.length) { bar.style.display = 'none'; bar.innerHTML = ''; return; }
  bar.style.display = 'block';
  bar.innerHTML = prompts.map(p => {
    const opts = (p.options || []).map(o =>
      `<button class="btn xs hi-opt" data-id="${p.id}" data-ans="${escAttr(o)}">${esc(o)}</button>`).join('');
    return `<div class="hi-prompt" data-id="${p.id}">
      <span class="hi-icon" title="The AI is waiting for your input">🤖</span>
      <span class="hi-msg">${esc(p.message)}</span>
      <span class="hi-actions">${opts}
        <input class="hi-input" data-id="${p.id}" placeholder="type an answer…" aria-label="Answer the AI">
        <button class="btn xs accent hi-send" data-id="${p.id}">Send ▸</button>
      </span>
    </div>`;
  }).join('');
  bar.querySelectorAll('.hi-opt').forEach(b => b.onclick = () => respond(b.dataset.id, b.dataset.ans));
  bar.querySelectorAll('.hi-send').forEach(b => b.onclick = () => {
    const inp = bar.querySelector('.hi-input[data-id="' + b.dataset.id + '"]');
    respond(b.dataset.id, inp ? inp.value : '');
  });
  bar.querySelectorAll('.hi-input').forEach(inp => inp.onkeydown = e => {
    if (e.key === 'Enter') { e.preventDefault(); respond(inp.dataset.id, inp.value); }
  });
}

async function respond(id, answer) {
  if (!answer || !answer.trim()) { toast('type an answer (or pick an option)'); return; }
  try { await api('/api/human-input/' + id + '/respond', { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ answer }) }); }
  catch (e) { toast(e.message); }
}
