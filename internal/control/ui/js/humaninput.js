import { $, esc, escAttr, api, toast, toastError } from './core.js';

// AI→human handoff: when the AI calls request_human_input, a prompt shows in the
// top banner so the operator can answer/approve. Loaded on boot and refreshed on
// the SSE "human.input" event (so it survives reconnects).
let humanInputLoadEpoch=0;
const humanInputPending=new Set();
let humanInputShown=0; // prompts currently rendered (an error row is not a prompt)

export async function loadHumanInput() {
  const epoch=++humanInputLoadEpoch;
  try {
    const d = await api('/api/human-input');
    if(epoch!==humanInputLoadEpoch)return;
    renderHumanInput(d.prompts || []);
  }
  catch (e) {
    if(epoch!==humanInputLoadEpoch)return;
    // Existing prompts stay usable; with nothing shown, a silent failure would
    // hide a pending AI request, so say so and offer Retry.
    if(!humanInputShown)renderHumanInputError(e);
  }
}

function renderHumanInputError(e) {
  const bar = $('#humanInputBar'); if (!bar) return;
  bar.hidden = false;
  bar.innerHTML = `<div class="hi-prompt hi-error" role="alert">
      <span class="hi-msg">Could not check for AI input requests: ${esc((e && e.message) || 'request failed')}</span>
      <span class="hi-actions"><button type="button" class="btn xs hi-retry">Retry</button></span>
    </div>`;
  bar.querySelector('.hi-retry').onclick = () => loadHumanInput();
}

function renderHumanInput(prompts) {
  const bar = $('#humanInputBar'); if (!bar) return;
  humanInputShown = prompts.length;
  if (!prompts.length) { bar.hidden = true; bar.innerHTML = ''; return; }
  bar.hidden = false;
  bar.innerHTML = prompts.map(p => {
    const opts = (p.options || []).map(o =>
      `<button class="btn xs hi-opt" data-id="${escAttr(p.id)}" data-ans="${escAttr(o)}">${esc(o)}</button>`).join('');
    return `<div class="hi-prompt" data-id="${escAttr(p.id)}">
      <span class="hi-icon" title="The AI is waiting for your input"><svg class="icon" aria-hidden="true" focusable="false"><use href="#i-robot"/></svg></span>
      <span class="hi-msg">${esc(p.message)}</span>
      <span class="hi-actions">${opts}
        <input class="hi-input" data-id="${escAttr(p.id)}" placeholder="type an answer…" aria-label="Answer AI prompt: ${escAttr(p.message)}">
        <button class="btn xs accent hi-send" data-id="${escAttr(p.id)}">Send ▸</button>
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
  humanInputPending.forEach(id=>setPromptPending(id,true));
}

function setPromptPending(id,pending){
  const row=$('#humanInputBar')?.querySelector('.hi-prompt[data-id="'+CSS.escape(String(id))+'"]');
  if(!row)return;
  if(pending)row.setAttribute('aria-busy','true');
  else row.setAttribute('aria-busy','false');
  row.querySelectorAll('button,input').forEach(el=>{el.disabled=pending;});
}

async function respond(id, answer) {
  id=String(id);
  if (!answer || !answer.trim()) { toast('type an answer (or pick an option)'); return; }
  if(humanInputPending.has(id))return;
  humanInputPending.add(id);
  humanInputLoadEpoch++;
  setPromptPending(id,true);
  try {
    await api('/api/human-input/' + id + '/respond', { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ answer }) });
    await loadHumanInput();
  }
  catch (e) {
    toastError('Could not send answer', e);
    // The prompt may have expired or been answered elsewhere; refresh the list.
    loadHumanInput();
  }
  finally{humanInputPending.delete(id);setPromptPending(id,false);}
}
