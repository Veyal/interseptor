// decoder.js — the Decoder modal (base64/url/hex/JWT, one layer at a time).
// It is a plain tool: it never touches a captured flow, so it stays independent
// of the panels that open it.
import { $, esc, toast, api, openModal, closeModal, pickTextFile, normalizeListText, DEC_OPS, copyText } from './core.js';

export { DEC_OPS };
export const DEC_GROUPS = [
  { label: 'Decode', ops: [['base64decode', 'Base64 ↓'], ['urldecode', 'URL ↓'], ['hexdecode', 'Hex ↓'], ['htmldecode', 'HTML ↓']] },
  { label: 'Encode', ops: [['base64encode', 'Base64 ↑'], ['urlencode', 'URL ↑'], ['hexencode', 'Hex ↑'], ['htmlencode', 'HTML ↑']] },
  { label: 'Inspect', ops: [['jwtdecode', 'JWT'], ['smart', 'Smart']] },
];

const textEncoder = new TextEncoder();
let decRequestEpoch = 0;
let decModalEpoch = 0;
let decActiveOp = '';
let decChainSteps = [];

function decFormatMeta(text) {
  const s = String(text || '');
  const bytes = textEncoder.encode(s).length;
  return `${s.length} chars · ${bytes} B`;
}

function decOpLabel(op) {
  for (const g of DEC_GROUPS) {
    const match = g.ops.find(([id]) => id === op);
    if (match) return match[1];
  }
  const fallback = DEC_OPS.find(([id]) => id === op);
  return fallback ? fallback[1] : op;
}

function decSetStatus(tone, msg) {
  const err = $('#decErr');
  if (!err) return;
  if (tone) err.dataset.tone = tone;
  else delete err.dataset.tone;
  err.textContent = msg || '';
}

function decRenderChain() {
  const el = $('#decChain');
  if (!el) return;
  const text = decChainSteps.length ? `Chain: ${decChainSteps.join(' -> ')}` : '';
  el.textContent = text;
  el.hidden = !text;
}

export function decSyncMeta() {
  const inMeta = $('#decInMeta');
  const outMeta = $('#decOutMeta');
  if (inMeta) inMeta.textContent = decFormatMeta($('#decIn')?.value);
  if (outMeta) outMeta.textContent = decFormatMeta($('#decOut')?.value);
}

export function decSetActiveOp(op) {
  decActiveOp = op || '';
  const box = $('#decOps');
  if (!box || typeof box.querySelectorAll !== 'function') return;
  box.querySelectorAll('[data-op]').forEach((btn) => {
    const on = btn.dataset.op === decActiveOp;
    btn.classList.toggle('is-active', on);
    btn.setAttribute('aria-pressed', on ? 'true' : 'false');
  });
}

function decSetPending(on, op) {
  const ops = $('#decOps');
  const err = $('#decErr');
  if (ops) ops.setAttribute('aria-busy', on ? 'true' : 'false');
  if (!err) return;
  if (on) {
    decSetStatus('busy', `${decOpLabel(op || 'Operation')}…`);
  } else if (err.dataset.tone === 'busy') {
    decSetStatus('', '');
  }
}

function decInvalidatePending() {
  decRequestEpoch++;
  decSetPending(false);
}

function decCurrent(epoch, modalEpoch, input) {
  const modal = $('#decModal'), field = $('#decIn');
  return epoch === decRequestEpoch && modalEpoch === decModalEpoch && modal?.style.display === 'flex' && field?.value === input;
}

export function decBuildOps() {
  const box = $('#decOps');
  if (!box || box._built) return;
  box._built = 1;
  box.innerHTML = DEC_GROUPS.map((g) => {
    const btns = g.ops.map(([op, label]) => `<button type="button" class="btn dec-op-btn" data-op="${op}" aria-pressed="false">${esc(label)}</button>`).join('');
    return `<div class="dec-op-group"><span class="dec-op-label">${esc(g.label)}</span>${btns}</div>`;
  }).join('');
  box.querySelectorAll('[data-op]').forEach((b) => { b.onclick = () => decApply(b.dataset.op); });
}

export async function decApply(op) {
  const epoch = ++decRequestEpoch;
  const modalEpoch = decModalEpoch;
  const input = $('#decIn').value;
  decSetPending(true, op);
  try {
    const r = await api('/api/decode', {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ op, input }),
    });
    if (!decCurrent(epoch, modalEpoch, input)) return;
    decSetPending(false);
    if (r.error) {
      decSetStatus('error', r.error);
      return;
    }
    $('#decOut').value = r.output;
    decSetActiveOp(op);
    decChainSteps.push(decOpLabel(op));
    decRenderChain();
    decSyncMeta();
    decSetStatus('ok', `${decOpLabel(op)} ready`);
  } catch (e) {
    if (!decCurrent(epoch, modalEpoch, input)) return;
    decSetPending(false);
    decSetStatus('error', e.message);
  }
}

export function decClear() {
  decInvalidatePending();
  if ($('#decIn')) $('#decIn').value = '';
  if ($('#decOut')) $('#decOut').value = '';
  decChainSteps = [];
  decSetActiveOp('');
  decRenderChain();
  decSetStatus('', '');
  decSyncMeta();
}

export function openDecoder(seed) {
  decBuildOps();
  decModalEpoch++;
  decInvalidatePending();
  openModal($('#decModal'));
  if (seed !== undefined) $('#decIn').value = seed;
  if ($('#decOut')) $('#decOut').value = '';
  decChainSteps = [];
  decSetActiveOp('');
  decRenderChain();
  decSetStatus('', '');
  decSyncMeta();
  setTimeout(() => $('#decIn').focus(), 0);
}
async function decLoadFile(){
  decInvalidatePending();
  try{
    const got=await pickTextFile();
    if(!got) return;
    // The native picker yields to the event loop; invalidate again in case a
    // decoder request was started while it was open.
    decInvalidatePending();
    $('#decIn').value=normalizeListText(got.text);
    $('#decOut').value='';
    decChainSteps = [];
    decSetActiveOp('');
    decRenderChain();
    decSetStatus('', '');
    decSyncMeta();
    toast('loaded from '+got.name);
  }catch(e){toast(e.message);}
}
if ($('#decLoad')) $('#decLoad').onclick = decLoadFile;
if ($('#decClose')) $('#decClose').onclick = () => { decModalEpoch++; decInvalidatePending(); closeModal($('#decModal')); };
if ($('#decIn') && typeof $('#decIn').addEventListener === 'function') {
  $('#decIn').addEventListener('input', () => {
    decInvalidatePending();
    decChainSteps = [];
    decSetActiveOp('');
    decRenderChain();
    decSetStatus('', '');
    decSyncMeta();
  });
}
if ($('#decClear')) $('#decClear').onclick = () => { decClear(); $('#decIn')?.focus(); };
if ($('#decUp')) $('#decUp').onclick = () => {
  decInvalidatePending();
  $('#decIn').value = $('#decOut').value;
  $('#decOut').value = '';
  decSetActiveOp('');
  decSetStatus('', '');
  decSyncMeta();
  $('#decIn')?.focus();
};
if ($('#decCopy')) $('#decCopy').onclick = () => copyText($('#decOut').value, 'output copied');
