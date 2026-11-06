// repeater.js — Repeater response toolbar, phone Request|Response view, inline
// diff vs the previous send, Copy as and a find bar. Pure helpers are exported
// for node tests; the DOM wiring receives its helpers from tools.js (`deps`) so
// this module never imports core.js and stays loadable without a browser.
//
// The response pane has one action row — Attach as evidence (primary), Diff,
// Copy as and the overflow menu. Save as proof, Copy as PNG, Intruder, + Finding
// and Copy cURL are folded into those two menus: their elements stay in the DOM
// inside #repFolded with their ids and handlers, and the menu rows delegate to
// them, so no click path or contract test loses its control.
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
  if (id) b.id = id;
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
  const ignoreLabel = mk('label', 'hint rep-diff-ignore');
  const ignore = doc.createElement('input');
  ignore.type = 'checkbox';
  ignore.id = prefix + 'Ignore';
  ignoreLabel.append(ignore, doc.createTextNode('Ignore dates, cookies, tokens'));
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
    if (e.ctrlKey || e.metaKey || e.altKey) return;
    if (e.key === 'n') { e.preventDefault(); view.next(); } else if (e.key === 'N') { e.preventDefault(); view.prev(); }
  });
  return { open, close: closePanel, isOpen: () => !panel.hidden };
}

// wireRepeaterExtras(deps) is called once from repInit. deps: {$, api, toast,
// toastError, openCtxMenu, repCur}. Returns {afterSend, sync, setAttached}.
export function wireRepeaterExtras(deps) {
  const doc = document;
  const { $, api, toast, toastError, openCtxMenu, repCur, mountFlowCopyPng, flowPngMenuItems, flowSelectionImageItems, saveFile } = deps;
  const group = $('#repActions');
  const resPane = doc.querySelector('#panel-repeater .rep-res');
  const work = doc.querySelector('#panel-repeater .rep-work');
  const resView = $('#repResView');
  if (!group || !resPane || !work || !resView || $('#repAttach')) return null;
  const attachedFlows = new Set();

  /* toolbar — one visible action row: Attach (primary), Diff, Copy as (menu),
     the overflow, and the Attached chip when it applies. Save as proof,
     Copy as PNG, Intruder, + Finding and Copy cURL leave the row and become
     menu rows; their elements stay in the DOM (#repFolded) with their ids and
     handlers, so every existing click path and contract test still resolves. */
  const attach = button(doc, { id: 'repAttach', icon: 'attach', text: 'Attach as evidence', cls: 'btn xs btn-primary' });
  const proof = button(doc, { id: 'repProof', icon: 'status-done', text: 'Save as proof' });
  const diff = button(doc, { id: 'repDiffBtn', icon: 'diff', text: 'Diff vs previous' });
  const copy = button(doc, { id: 'repCopyAs', icon: 'copy', text: 'Copy as', label: 'Copy the sent request in another format' });
  copy.setAttribute('aria-haspopup', 'menu');
  copy.setAttribute('aria-expanded', 'false');
  const chip = doc.createElement('span');
  chip.id = 'repAttachedChip';
  chip.className = 'rep-attached';
  chip.hidden = true;
  chip.appendChild(iconNode(doc, 'attach'));
  chip.appendChild(doc.createTextNode(' Attached'));
  // The PNG control is painted by core.js (the clipboard write has to start
  // inside the click), so the toolbar only reserves a mount point for it.
  const pngMount = doc.createElement('span');
  pngMount.id = 'repCopyPng';
  pngMount.className = 'rep-copy-png';
  // The desktop skin (desktop-panel-repeater.js) re-parents this group into its
  // status action row and owns an overflow with the same #repMore id, so the
  // fold below is the web layout only: in a desktop host the group keeps the
  // shape that skin was written against.
  const desktopHost = doc.documentElement.classList.contains('desktop-app');
  let more = null;
  if (desktopHost) {
    group.prepend(attach, proof, diff, copy, pngMount, chip);
  } else {
    more = button(doc, { id: 'repMore', icon: 'more', cls: 'btn xs rep-more', label: 'More response actions' });
    more.setAttribute('aria-haspopup', 'menu');
    more.setAttribute('aria-expanded', 'false');
    more.setAttribute('aria-label', 'More response actions');
    const folded = doc.createElement('div');
    folded.id = 'repFolded';
    folded.className = 'rep-folded';
    folded.hidden = true;
    folded.append(proof, pngMount);
    // Intruder / + Finding / Copy cURL are painted by tools.js before this runs.
    // Moving them here keeps their onclick properties (the menu delegates to
    // them) and takes them out of the row without deleting anything.
    for (const id of ['repToIntruder', 'repAddFinding', 'repCopyCurl']) {
      const el = doc.getElementById(id);
      if (el) folded.append(el);
    }
    group.prepend(attach, diff, copy, more, chip, folded);
  }

  // pngFlowId is the sent exchange a picture can be made of: a captured flow,
  // not an in-flight send and not an unsent tab.
  const pngFlowId = (t) => (t && t.resId && !t.sendPending ? t.resId : 0);
  function syncPng(t) {
    const id = pngFlowId(t);
    if (!mountFlowCopyPng) return;
    if (id) {
      mountFlowCopyPng(pngMount, id, 'btn xs');
      return;
    }
    const why = t && t.sendPending ? 'Wait for the send to finish' : 'Send the request first to copy it as a PNG';
    pngMount.replaceChildren();
    const b = button(doc, { icon: 'image', text: 'Copy as PNG', label: why, cls: 'btn xs' });
    b.disabled = true;
    pngMount.appendChild(b);
  }

  function sync() {
    const t = repCur();
    syncPng(t);
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

  // The Copy as menu is the one copy surface: the request formats COPY_AS_KINDS
  // already lists ("curl (bash)" is the cURL copy) plus one PNG group that holds
  // every image variant, so no format appears twice.
  async function openCopyMenu() {
    const t = repCur();
    if (!t || !t.resId || t.sendPending) return;
    const { COPY_AS_KINDS, copyAs } = await import('./copyas.js');
    const id = pngFlowId(t);
    const png = id && flowPngMenuItems ? flowPngMenuItems(id) : [];
    const sections = [{ head: 'Copy request as', items: COPY_AS_KINDS.map((k) => ({ label: k.label, act: () => copyAs(k.kind, [{ id: t.resId }]) })) }];
    if (png.length) sections.push({ head: 'Copy exchange as PNG', items: png });
    const r = copy.getBoundingClientRect();
    openCtxMenu(r.left, r.bottom, sections, copy);
  }
  copy.addEventListener('click', () => { openCopyMenu(); });

  // foldedRow turns a folded control into a menu row that delegates to it, so the
  // element stays the single owner of the behaviour (and its enabled state).
  const foldedRow = (el, label, iconName) => (el ? { label, icon: iconName, disabled: !!el.disabled, act: () => el.click() } : null);
  // findings.css hides .findings-write (the + Finding button) unless editing is
  // on; the menu row follows the same switch instead of offering a dead action.
  const findingsWritable = () => doc.documentElement.dataset.findingsEditing === 'on';
  function openResponseMoreMenu() {
    if (!more) return;
    const items = [
      foldedRow(proof, 'Save as proof', 'status-done'),
      foldedRow(doc.getElementById('repToIntruder'), 'Intruder', 'intruder'),
      findingsWritable() ? foldedRow(doc.getElementById('repAddFinding'), '+ Finding', 'finding') : null,
    ].filter(Boolean);
    const r = more.getBoundingClientRect();
    openCtxMenu(r.left, r.bottom, [{ head: 'RESPONSE', items }], more);
  }
  if (more) more.addEventListener('click', openResponseMoreMenu);

  /* inline diff (shared panel builder, also used by Intruder) */
  const diffPanel = buildDiffPanel(doc, { prefix: 'repDiff', title: 'Diff vs previous response', host: resPane, hide: [resView], api, toast, returnFocus: () => diff });
  async function runDiff() {
    const m = diffModel(repCur() || {});
    if (!m.ok) { toast(m.reason, 'warn'); return; }
    await diffPanel.open(m.previousId, m.currentId);
  }
  diff.addEventListener('click', runDiff);
  const closeDiff = (o) => diffPanel.close(o);

  /* response pane menu — the response view used to be a dead right-click */
  resView.addEventListener('contextmenu', async (e) => {
    const t = repCur();
    const id = pngFlowId(t);
    const sel = String(doc.getSelection && doc.getSelection() || '');
    e.preventDefault();
    const png = id && flowPngMenuItems ? flowPngMenuItems(id) : [];
    const items = [
      sel ? { label: 'Copy selection', icon: 'copy', act: () => navigator.clipboard.writeText(sel).then(() => toast('copied'), () => toast('Clipboard write was blocked', 'error')) } : null,
      { label: 'Copy response body', icon: 'copy', disabled: !id, act: () => copyResponseBody(id) },
      { label: 'Save response to a file', icon: 'download', disabled: !id, act: () => saveResponseBody(id, t) },
      png.length ? { label: 'Copy exchange as PNG', icon: 'image', children: png } : { label: 'Copy exchange as PNG', icon: 'image', disabled: true },
      ...(sel.trim() && id && flowSelectionImageItems ? flowSelectionImageItems(id, 'res', sel) : []),
      { sep: true },
      { label: 'Diff vs previous send', icon: 'diff', disabled: !diffModel(t || {}).ok, act: runDiff },
      { label: 'Attach as evidence', icon: 'attach', disabled: attachModel(t).disabled, act: () => runAttach(false, resView) },
      { label: 'Save as proof', icon: 'status-done', disabled: attachModel(t, { proof: true }).disabled, act: () => runAttach(true, resView) },
    ].filter(Boolean);
    openCtxMenu(e.clientX, e.clientY, [{ head: 'RESPONSE', items }]);
  });
  async function copyResponseBody(id) {
    try {
      const raw = await api('/api/flows/' + id + '/raw?side=res');
      await navigator.clipboard.writeText(splitRawMessage(raw).body);
      toast('response body copied');
    } catch (e) { toastError('Copy response body failed', e); }
  }
  async function saveResponseBody(id, t) {
    try {
      const raw = await api('/api/flows/' + id + '/raw?side=res');
      const name = 'repeater-' + id + '-response.http';
      if (saveFile) await saveFile(new Blob([String(raw == null ? '' : raw)], { type: 'message/http' }), name, 'message/http');
      else { toast('Saving files is not available here', 'warn'); return; }
      toast('exported ' + name);
    } catch (e) { if (e && e.name === 'AbortError') return; toastError('Export response failed', e); }
  }

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
    diffOpen: () => diffPanel.isOpen(),
    closeDiff: () => closeDiff(),
    setPhoneView,
  };
}
