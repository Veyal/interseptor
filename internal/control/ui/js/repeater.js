// repeater.js — Repeater response toolbar, phone Request|Response view, inline
// diff vs the previous send, Copy as and a find bar. Pure helpers are exported
// for node tests; the DOM wiring receives its helpers from tools.js (`deps`) so
// this module never imports core.js and stays loadable without a browser.
//
// Nothing here changes how a request is built or sent: every action reads the
// flow the existing send already captured (`t.resId`) and reuses the existing
// evidence, copy and raw-flow endpoints.

import { diffLines, createDiffView, normalizeBody, normalizeText } from './diff.js';
import { createFinder } from './finder.js';

export const PHONE_VIEWS = ['req', 'res'];
export const MAX_DIFF_BYTES = 1024 * 1024;

/* ---- pure helpers ---- */

// History is newest first, so the "previous" response is the next entry.
export function previousHistoryEntry(history, resId) {
  if (!Array.isArray(history)) return null;
  const i = history.findIndex((h) => h && h.id === resId);
  return i >= 0 && history[i + 1] ? history[i + 1] : null;
}

export function diffModel(tab) {
  const resId = Number(tab && tab.resId) || 0;
  if (!resId) return { ok: false, reason: 'Send the request first to compare responses' };
  const prev = previousHistoryEntry(tab.history, resId);
  if (!prev) return { ok: false, reason: 'No earlier send in this tab to compare with' };
  return { ok: true, currentId: resId, previousId: prev.id };
}

export const phoneViewAfterSend = () => 'res';
export const nextPhoneView = (v) => (v === 'req' ? 'res' : 'req');

export const proofNote = () => 'Proof: exact request and response captured in Repeater';

// attachModel decides the state of "Attach as evidence" / "Save as proof".
export function attachModel(tab, { proof = false } = {}) {
  const resId = Number(tab && tab.resId) || 0;
  if (!resId) return { disabled: true, title: 'Send the request first to attach its flow as evidence' };
  if (tab.sendPending) return { disabled: true, title: 'Wait for the send to finish' };
  const spec = { kind: 'flow', refs: [resId] };
  if (proof) spec.note = proofNote();
  return { disabled: false, title: proof ? 'Attach this request and response as the proof flow' : 'Attach this response flow to a finding', spec };
}

export function splitRawMessage(raw) {
  const text = String(raw == null ? '' : raw).replace(/\r\n/g, '\n');
  const i = text.indexOf('\n\n');
  return i < 0 ? { head: text, body: '' } : { head: text.slice(0, i), body: text.slice(i + 2) };
}

// diffText turns one raw response into the line text the diff compares.
export function diffText(raw, { ignore = false } = {}) {
  const { head, body } = splitRawMessage(raw);
  const h = ignore ? normalizeText(head) : head;
  return h + '\n\n' + normalizeBody(body, { json: true, ignore });
}

/* ---- DOM wiring ---- */

const SVG_NS = 'http://www.w3.org/2000/svg';
function iconNode(doc, name) {
  const svg = doc.createElementNS(SVG_NS, 'svg');
  svg.setAttribute('class', 'icon');
  svg.setAttribute('aria-hidden', 'true');
  svg.setAttribute('focusable', 'false');
  const use = doc.createElementNS(SVG_NS, 'use');
  use.setAttribute('href', '#i-' + name);
  svg.appendChild(use);
  return svg;
}
function button(doc, { id, label, text, icon, cls = 'btn xs' }) {
  const b = doc.createElement('button');
  b.type = 'button';
  b.id = id;
  b.className = cls;
  if (label) b.title = label;
  if (icon) b.appendChild(iconNode(doc, icon));
  if (text) b.appendChild(doc.createTextNode((icon ? ' ' : '') + text));
  return b;
}

// buildDiffPanel mounts a response diff panel into `host`. open(aId, bId) loads
// both flows' responses lazily (bounded to 1 MB each), shows removed (-) lines
// from a and added (+) lines from b, and hides the `hide` elements meanwhile.
export function buildDiffPanel(doc, { prefix, title, host, hide = [], api, toast, returnFocus, onClose }) {
  const mk = (tag, cls, text) => { const e = doc.createElement(tag); if (cls) e.className = cls; if (text != null) e.textContent = text; return e; };
  const btn = (suffix, text) => button(doc, { id: prefix + suffix, text });
  const panel = mk('section', 'rep-diff');
  panel.id = prefix;
  panel.hidden = true;
  panel.setAttribute('aria-label', title);
  const head = mk('div', 'rep-diff-head');
  const ignoreLabel = mk('label', 'hint');
  const ignore = doc.createElement('input');
  ignore.type = 'checkbox';
  ignore.id = prefix + 'Ignore';
  ignoreLabel.append(ignore, doc.createTextNode(' Ignore dates, cookies, tokens'));
  const mode = btn('Mode', 'Side by side');
  mode.setAttribute('aria-pressed', 'false');
  const prev = btn('Prev', 'Prev change');
  const next = btn('Next', 'Next change');
  const close = btn('Close', 'Close diff');
  head.append(mk('strong', null, title), ignoreLabel, mode, prev, next, close);
  const note = mk('p', 'hint rep-diff-note');
  note.setAttribute('role', 'status');
  const body = mk('div', 'rep-diff-body');
  panel.append(head, note, body);
  host.appendChild(panel);
  const view = createDiffView(body, { mode: 'unified' });
  let token = 0;
  let ids = null;

  async function fetchRaw(id) {
    const raw = await api('/api/flows/' + id + '/raw?side=res');
    const text = typeof raw === 'string' ? raw : String(raw == null ? '' : raw);
    return text.length > MAX_DIFF_BYTES ? { text: text.slice(0, MAX_DIFF_BYTES), truncated: true } : { text, truncated: false };
  }
  async function open(aId, bId) {
    ids = [aId, bId];
    const mine = ++token;
    hide.forEach((el) => { el.hidden = true; });
    panel.hidden = false;
    note.textContent = 'Loading both responses...';
    try {
      const [a, b] = await Promise.all([fetchRaw(aId), fetchRaw(bId)]);
      if (mine !== token) return;
      const opts = { ignore: ignore.checked };
      view.setResult(diffLines(diffText(a.text, opts).split('\n'), diffText(b.text, opts).split('\n')));
      note.textContent = `Flow #${aId} (removed, -) compared with flow #${bId} (added, +)` + (a.truncated || b.truncated ? '. Truncated at 1 MB.' : '');
    } catch (e) {
      if (mine !== token) return;
      note.textContent = 'Could not load the responses: ' + ((e && e.message) || e);
    }
  }
  function closePanel({ focus = true } = {}) {
    token++;
    panel.hidden = true;
    hide.forEach((el) => { el.hidden = false; });
    if (focus && returnFocus) { const f = returnFocus(); if (f && f.focus) f.focus(); }
    if (onClose) onClose();
  }
  close.addEventListener('click', () => closePanel());
  ignore.addEventListener('change', () => { if (ids) open(ids[0], ids[1]); });
  next.addEventListener('click', () => view.next());
  prev.addEventListener('click', () => view.prev());
  mode.addEventListener('click', () => {
    const split = mode.getAttribute('aria-pressed') !== 'true';
    mode.setAttribute('aria-pressed', String(split));
    mode.textContent = split ? 'Unified' : 'Side by side';
    view.setMode(split ? 'split' : 'unified');
  });
  panel.addEventListener('keydown', (e) => {
    if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); closePanel(); return; }
    if (e.target && e.target.closest && e.target.closest('input,textarea,select')) return;
    if (e.key === 'n') { e.preventDefault(); view.next(); } else if (e.key === 'N') { e.preventDefault(); view.prev(); }
  });
  return { open, close: closePanel, isOpen: () => !panel.hidden };
}

// wireRepeaterExtras(deps) is called once from repInit. deps: {$, api, toast,
// toastError, openCtxMenu, repCur}. Returns {afterSend, sync, setAttached}.
export function wireRepeaterExtras(deps) {
  const doc = document;
  const { $, api, toast, toastError, openCtxMenu, repCur } = deps;
  const group = $('#repActions');
  const resPane = doc.querySelector('#panel-repeater .rep-res');
  const work = doc.querySelector('#panel-repeater .rep-work');
  const resView = $('#repResView');
  if (!group || !resPane || !work || !resView || $('#repAttach')) return null;
  const attachedFlows = new Set();

  /* toolbar */
  const attach = button(doc, { id: 'repAttach', icon: 'attach', text: 'Attach as evidence', cls: 'btn xs btn-primary' });
  const proof = button(doc, { id: 'repProof', icon: 'status-done', text: 'Save as proof' });
  const diff = button(doc, { id: 'repDiffBtn', icon: 'diff', text: 'Diff vs previous' });
  const copy = button(doc, { id: 'repCopyAs', icon: 'copy', text: 'Copy as', label: 'Copy the sent request in another format' });
  copy.setAttribute('aria-haspopup', 'menu');
  const chip = doc.createElement('span');
  chip.id = 'repAttachedChip';
  chip.className = 'rep-attached';
  chip.hidden = true;
  chip.appendChild(iconNode(doc, 'attach'));
  chip.appendChild(doc.createTextNode(' Attached'));
  group.prepend(attach, proof, diff, copy, chip);

  function sync() {
    const t = repCur();
    const a = attachModel(t);
    attach.disabled = proof.disabled = a.disabled;
    attach.title = a.title;
    proof.title = attachModel(t, { proof: true }).title;
    const d = diffModel(t || {});
    diff.disabled = !d.ok;
    diff.title = d.ok ? 'Compare this response with the previous send in this tab' : d.reason;
    copy.disabled = !t || !t.resId || !!t.sendPending;
    copy.title = copy.disabled ? 'Send the request first to copy it' : 'Copy the sent request in another format';
    chip.hidden = !(t && attachedFlows.has(t.resId));
  }

  async function runAttach(withProof, anchor) {
    const t = repCur();
    const m = attachModel(t, { proof: withProof });
    if (m.disabled) { toast(m.title, 'warn'); return; }
    try {
      const { attachEvidence } = await import('./evidence-attach.js');
      const out = await attachEvidence(m.spec, { anchor });
      if (out && out.attached) { attachedFlows.add(m.spec.refs[0]); sync(); }
    } catch (e) { toastError('Could not attach evidence', e); }
  }
  attach.addEventListener('click', () => runAttach(false, attach));
  proof.addEventListener('click', () => runAttach(true, proof));

  copy.addEventListener('click', async () => {
    const t = repCur();
    if (!t || !t.resId) return;
    const { COPY_AS_KINDS, copyAs } = await import('./copyas.js');
    const r = copy.getBoundingClientRect();
    openCtxMenu(r.left, r.bottom, [{ head: 'Copy request as', items: COPY_AS_KINDS.map((k) => ({ label: k.label, act: () => copyAs(k.kind, [{ id: t.resId }]) })) }], copy);
  });

  /* inline diff (shared panel builder, also used by Intruder) */
  const diffPanel = buildDiffPanel(doc, { prefix: 'repDiff', title: 'Diff vs previous response', host: resPane, hide: [resView], api, toast, returnFocus: () => diff });
  async function runDiff() {
    const m = diffModel(repCur() || {});
    if (!m.ok) { toast(m.reason, 'warn'); return; }
    await diffPanel.open(m.previousId, m.currentId);
  }
  diff.addEventListener('click', runDiff);
  const closeDiff = (o) => diffPanel.close(o);

  /* find in the response pane: Ctrl/Cmd+F while focus is inside it */
  const finder = createFinder(resPane, { root: resView, label: 'Find in response' });
  resPane.addEventListener('keydown', (e) => {
    if ((e.ctrlKey || e.metaKey) && !e.altKey && (e.key === 'f' || e.key === 'F')) { e.preventDefault(); finder.open(); }
  });

  /* phone: Request | Response segmented control */
  const seg = doc.createElement('div');
  seg.className = 'seg rep-phone-seg';
  seg.id = 'repPhoneSeg';
  seg.setAttribute('role', 'group');
  seg.setAttribute('aria-label', 'Show request or response');
  const labels = { req: 'Request', res: 'Response' };
  PHONE_VIEWS.forEach((v) => {
    const b = doc.createElement('button');
    b.type = 'button';
    b.dataset.view = v;
    b.textContent = labels[v];
    seg.appendChild(b);
  });
  work.parentNode.insertBefore(seg, work);
  function setPhoneView(v) {
    work.dataset.phoneView = v;
    seg.querySelectorAll('button').forEach((b) => {
      const on = b.dataset.view === v;
      b.classList.toggle('on', on);
      b.setAttribute('aria-pressed', String(on));
    });
  }
  seg.addEventListener('click', (e) => { const b = e.target.closest && e.target.closest('button[data-view]'); if (b) setPhoneView(b.dataset.view); });
  setPhoneView('req');

  sync();
  return {
    sync,
    afterSend() { setPhoneView(phoneViewAfterSend()); closeDiff({ focus: false }); sync(); },
    setPhoneView,
  };
}
