// evidence-attach.js — one entry point for "attach this as evidence".
//
//   attachEvidence({kind:'flow'|'shot'|'ws'|'note', refs, note?}, {findingId?, anchor?})
//
// Without a findingId it opens a compact searchable popover (combobox +
// listbox) of findings with readiness summaries and a first row "New finding
// from selection". It calls only the existing finding endpoints
// (POST /api/findings, POST /api/findings/{id}/flows|images, and the matching
// DELETEs for undo), then asks projectState to refresh. Readiness is never
// computed here: the summary is read from the server's `readiness.checks`.
//
// The pure helpers at the top have no imports and run under `node --test`;
// everything that touches the DOM loads core.js lazily, so importing this file
// from node (or before core.js) has no side effects.

export const EVIDENCE_KINDS = ['flow', 'shot', 'ws', 'note'];
export const MAX_REFS = 32;
export const UNDO_MS = 5000;

export function normalizeRefs(kind, refs) {
  if (!Array.isArray(refs)) return [];
  if (kind === 'shot') return refs.filter((r) => r && typeof r.data === 'string' && r.data).slice(0, MAX_REFS);
  const seen = new Set();
  const out = [];
  for (const r of refs) {
    const n = Number(r);
    if (!Number.isSafeInteger(n) || n <= 0 || seen.has(n)) continue;
    seen.add(n);
    out.push(n);
    if (out.length >= MAX_REFS) break;
  }
  return out;
}

// ws and note evidence are attached through the owning flow with a describing
// note, because the API has no separate frame or note evidence block.
export function flowAttachRequest(findingId, flowId, { note, proof } = {}) {
  const body = { flowId: Number(flowId), role: 'result' };
  if (note) body.note = String(note);
  if (proof) body.proof = String(proof);
  return { path: '/api/findings/' + Number(findingId) + '/flows', method: 'POST', body };
}

export function validateAltText(alt) {
  const t = String(alt == null ? '' : alt).trim();
  return t ? { ok: true, text: t } : { ok: false, text: '' };
}

export function imageAttachRequest(findingId, { data, mime, alt }) {
  const v = validateAltText(alt);
  if (!v.ok) throw new Error('alt text is required for a screenshot');
  return { path: '/api/findings/' + Number(findingId) + '/images', method: 'POST', body: { data, mime, caption: v.text, role: 'result', source: 'operator_upload' } };
}

export function newFindingRequest(flowIds, { title, target } = {}) {
  const t = String(title == null ? '' : title).trim();
  if (!t) return null;
  const body = { severity: 'Medium', status: 'needs_verification', source: 'human', title: t, flowIds: flowIds.slice() };
  if (target) body.target = target;
  return { path: '/api/findings', method: 'POST', body };
}

export function filterFindings(items, query) {
  const q = String(query || '').trim().toLowerCase();
  const list = Array.isArray(items) ? items : [];
  if (!q) return list.slice();
  if (q[0] === '#') {
    const digits = q.slice(1);
    return list.filter((f) => String(f.id).startsWith(digits));
  }
  return list.filter((f) => String(f.title || '').toLowerCase().includes(q) || String(f.severity || '').toLowerCase() === q || String(f.id) === q);
}

export function readinessPips(readiness) {
  const checks = readiness && Array.isArray(readiness.checks) ? readiness.checks : [];
  if (!checks.length) return { passed: 0, total: 0, label: 'readiness unknown' };
  const passed = checks.filter((c) => c && c.ok === true).length;
  return { passed, total: checks.length, label: `${passed} of ${checks.length} checks passed` };
}

export function findingOptionLabel(f) {
  return `#${f.id} ${f.title}, ${f.severity}, ${readinessPips(f.readiness).label}`;
}

// undoPlan lists the DELETE requests that reverse an attach. Attaching to an
// existing finding detaches only the flows this action added; a finding this
// action created is deleted (deleted findings stay recoverable server-side).
export function undoPlan({ created, findingId, flowIds }) {
  if (created) return [{ method: 'DELETE', path: '/api/findings/' + findingId }];
  return (flowIds || []).map((id) => ({ method: 'DELETE', path: '/api/findings/' + findingId + '/flows/' + id }));
}

export function attachKeyTarget(target, drawerFlowId) {
  const row = target && typeof target.closest === 'function' ? target.closest('.trow[data-id]') : null;
  const fromRow = row ? Number(row.dataset.id) : 0;
  if (Number.isSafeInteger(fromRow) && fromRow > 0) return fromRow;
  const d = Number(drawerFlowId);
  return Number.isSafeInteger(d) && d > 0 ? d : null;
}

/* ---------------- DOM layer (lazy core) ---------------- */

let corePromise = null;
const loadCore = () => corePromise || (corePromise = import('./core.js'));
const loadProjectState = () => import('./project-state.js').then((m) => m.projectState);

function el(tag, cls, text) {
  const e = document.createElement(tag);
  if (cls) e.className = cls;
  if (text != null) e.textContent = text;
  return e;
}

async function send(core, req) {
  const init = { method: req.method };
  if (req.body !== undefined) { init.headers = { 'content-type': 'application/json' }; init.body = JSON.stringify(req.body); }
  return core.api(req.path, init);
}

// undoToast lives five seconds, pauses while hovered or focused, and keeps the
// Undo button keyboard reachable.
function undoToast(message, onUndo) {
  const host = document.getElementById('toast');
  if (!host) return null;
  const t = el('div', 'toast-item info show', message);
  t.setAttribute('role', 'status');
  const btn = el('button', 'btn xs toast-action', 'Undo');
  btn.type = 'button';
  t.append(' ', btn);
  host.appendChild(t);
  let timer = null;
  const dismiss = () => { clearTimeout(timer); t.remove(); };
  const arm = () => { clearTimeout(timer); timer = setTimeout(dismiss, UNDO_MS); };
  t.addEventListener('mouseenter', () => clearTimeout(timer));
  t.addEventListener('mouseleave', arm);
  t.addEventListener('focusin', () => clearTimeout(timer));
  t.addEventListener('focusout', arm);
  btn.addEventListener('click', () => { dismiss(); onUndo(); });
  arm();
  return t;
}

async function refreshState(reason) {
  try { await (await loadProjectState()).refresh({ reason }); } catch (e) { /* counters only */ }
}

async function runUndo(core, plan, label) {
  const failed = [];
  for (const step of plan) {
    try { await send(core, step); } catch (e) { failed.push(e); }
  }
  if (failed.length) core.toastError('Undo failed', failed[0]);
  else core.toast(label + ' undone', 'success');
  await refreshState('attach-undo');
}

async function attachFlows(core, findingId, ids, opts) {
  const attached = [];
  const failed = [];
  for (const id of ids) {
    try { await send(core, flowAttachRequest(findingId, id, opts)); attached.push(id); } catch (e) { failed.push({ id, error: e }); }
  }
  return { attached, failed };
}

async function askAlt(core, ref) {
  if (validateAltText(ref.alt).ok) return ref.alt;
  for (;;) {
    const v = await core.uiPrompt({ title: 'Describe this screenshot', placeholder: 'What does it show? (required, used as alt text)' });
    if (v == null) return null;
    if (validateAltText(v).ok) return v;
  }
}

async function createFinding(core, ids, spec) {
  const title = await core.uiPrompt({ title: 'Name the new finding', placeholder: 'e.g. IDOR on /api/user/{id}', value: spec.titleHint || '' });
  const req = title == null ? null : newFindingRequest(ids, { title, target: spec.target });
  if (!req) return null;
  const f = await send(core, req);
  return f && f.id ? f : null;
}

async function performAttach(core, spec, target) {
  const kind = spec.kind;
  if (kind === 'shot') {
    const shots = normalizeRefs('shot', spec.refs);
    let done = 0;
    for (const shot of shots) {
      const alt = await askAlt(core, shot);
      if (alt == null) break;
      try { await send(core, imageAttachRequest(target.id, { ...shot, alt })); done++; } catch (e) { core.toastError('Could not attach screenshot', e); }
    }
    if (done) { core.toast(`attached ${done} screenshot${done === 1 ? '' : 's'} to #${target.id}`, 'success'); await refreshState('attach'); }
    return { attached: done, findingId: target.id };
  }
  const ids = normalizeRefs(kind, spec.refs);
  if (!ids.length) { core.toast('nothing to attach'); return { attached: 0 }; }
  const note = spec.note || (kind === 'ws' ? 'WebSocket traffic' : '');
  let findingId = target.id;
  let created = false;
  if (target.create) {
    const f = await createFinding(core, ids, spec).catch((e) => { core.toastError('Could not create finding', e); return null; });
    if (!f) return { attached: 0 };
    findingId = f.id; created = true;
    await refreshState('attach');
    undoToast(`finding #${findingId} created with ${ids.length} item${ids.length === 1 ? '' : 's'}`, () => runUndo(core, undoPlan({ created, findingId, flowIds: ids }), 'create'));
    return { attached: ids.length, findingId, created };
  }
  const { attached, failed } = await attachFlows(core, findingId, ids, { note });
  await refreshState('attach');
  if (failed.length && !attached.length) { core.toastError('Could not attach evidence', failed[0].error); return { attached: 0, failed, findingId }; }
  const msg = `attached ${attached.length} item${attached.length === 1 ? '' : 's'} to #${findingId}` + (failed.length ? ` (${failed.length} failed)` : '');
  undoToast(msg, () => runUndo(core, undoPlan({ created: false, findingId, flowIds: attached }), 'attach'));
  return { attached: attached.length, failed, findingId };
}

/* ---- picker popover: combobox + listbox ---- */

let openPicker = null;

function closePicker(restore = true) {
  if (!openPicker) return;
  const { root, opener, onDocDown } = openPicker;
  document.removeEventListener('pointerdown', onDocDown, true);
  root.remove();
  openPicker = null;
  if (restore && opener && opener.isConnected && opener.focus) opener.focus({ preventScroll: true });
}

function placePicker(root, anchor) {
  if (!anchor || !anchor.getBoundingClientRect) return;
  const r = anchor.getBoundingClientRect();
  const w = Math.min(360, window.innerWidth - 16);
  root.style.setProperty('--attach-left', Math.max(8, Math.min(r.left, window.innerWidth - w - 8)) + 'px');
  root.style.setProperty('--attach-top', Math.min(r.bottom + 4, window.innerHeight - 280) + 'px');
}

function optionNode(id, label, sub, pips) {
  const li = el('li', 'attach-opt');
  li.id = id;
  li.setAttribute('role', 'option');
  li.setAttribute('aria-selected', 'false');
  li.append(el('span', 'attach-opt-title', label));
  if (sub) li.append(el('span', 'attach-opt-sub', sub));
  if (pips) {
    const p = el('span', 'attach-pips');
    p.setAttribute('aria-hidden', 'true');
    for (let i = 0; i < pips.total; i++) p.append(el('span', 'attach-pip' + (i < pips.passed ? ' is-on' : '')));
    li.append(p, el('span', 'u-sr', pips.label));
  }
  return li;
}

function pickFinding(core, anchor) {
  return new Promise((resolve) => {
    closePicker(false);
    const root = el('div', 'attach-pop');
    root.id = 'attachPicker';
    const title = el('h2', 'attach-title', 'Attach as evidence');
    title.id = 'attachPickerTitle';
    const input = el('input', 'attach-search');
    input.type = 'text';
    input.setAttribute('role', 'combobox');
    input.setAttribute('aria-expanded', 'true');
    input.setAttribute('aria-controls', 'attachList');
    input.setAttribute('aria-autocomplete', 'list');
    input.setAttribute('aria-label', 'Search findings');
    input.placeholder = 'Search findings or #id';
    const list = el('ul', 'attach-list');
    list.id = 'attachList';
    list.setAttribute('role', 'listbox');
    list.setAttribute('aria-label', 'Findings');
    const status = el('p', 'attach-status u-sr');
    status.setAttribute('role', 'status');
    root.setAttribute('role', 'dialog');
    root.setAttribute('aria-modal', 'false');
    root.setAttribute('aria-labelledby', 'attachPickerTitle');
    root.append(title, input, list, status);
    document.body.appendChild(root);
    placePicker(root, anchor);
    const opener = document.activeElement;
    let items = [];
    let rows = [];
    let active = 0;
    const finish = (value) => { closePicker(!value); resolve(value); };
    const setActive = (i) => {
      active = Math.max(0, Math.min(rows.length - 1, i));
      rows.forEach((r, n) => r.node.setAttribute('aria-selected', n === active ? 'true' : 'false'));
      const cur = rows[active];
      if (cur) { input.setAttribute('aria-activedescendant', cur.node.id); cur.node.scrollIntoView({ block: 'nearest' }); } else input.removeAttribute('aria-activedescendant');
    };
    const render = () => {
      const found = filterFindings(items, input.value);
      list.textContent = '';
      rows = [{ node: optionNode('attachOptNew', 'New finding from selection', '', null), value: { create: true } }];
      found.forEach((f) => rows.push({ node: optionNode('attachOpt' + f.id, `#${f.id} ${f.title}`, f.severity, readinessPips(f.readiness)), value: { id: f.id } }));
      rows.forEach((r) => { r.node.addEventListener('click', () => finish(r.value)); list.append(r.node); });
      status.textContent = found.length + (found.length === 1 ? ' finding' : ' findings');
      setActive(input.value.trim() && found.length ? 1 : 0);
    };
    input.addEventListener('input', render);
    root.addEventListener('keydown', (e) => {
      if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); finish(null); }
      else if (e.key === 'ArrowDown') { e.preventDefault(); setActive(active + 1); }
      else if (e.key === 'ArrowUp') { e.preventDefault(); setActive(active - 1); }
      else if (e.key === 'Home') { e.preventDefault(); setActive(0); }
      else if (e.key === 'End') { e.preventDefault(); setActive(rows.length - 1); }
      else if (e.key === 'Enter' && rows[active]) { e.preventDefault(); finish(rows[active].value); }
    });
    const onDocDown = (e) => { if (!root.contains(e.target)) finish(null); };
    document.addEventListener('pointerdown', onDocDown, true);
    openPicker = { root, opener, onDocDown };
    render();
    core.api('/api/findings').then((d) => { if (openPicker && openPicker.root === root) { items = d.findings || []; render(); } }).catch((e) => { status.textContent = 'Could not load findings: ' + (e && e.message); });
    input.focus();
  });
}

export async function attachEvidence(spec, { findingId, anchor } = {}) {
  if (!spec || !EVIDENCE_KINDS.includes(spec.kind)) return { attached: 0 };
  const core = await loadCore();
  if (findingId) return performAttach(core, spec, { id: Number(findingId) });
  const choice = await pickFinding(core, anchor || document.activeElement);
  if (!choice) return { attached: 0, cancelled: true };
  return performAttach(core, spec, choice);
}

/* ---- keyboard: `e` attaches the focused row or the open drawer flow ---- */

async function initKeys(core) {
  const { createKeyRegistry } = await import('./keys.js');
  const registry = createKeyRegistry({ isModalOpen: () => core.hasOpenModal() });
  const attach = (e) => {
    const get = core.getHook('flowDrawerCurrent');
    const id = attachKeyTarget(e.target, get ? get() : 0);
    if (id) attachEvidence({ kind: 'flow', refs: [id] }, { anchor: e.target && e.target.closest ? e.target.closest('.trow, #flowDrawer') : null });
  };
  registry.register({ id: 'evidence.attach.list', keys: 'e', scope: 'proxy-list', label: 'Attach as evidence', group: 'Evidence', run: attach });
  registry.register({ id: 'evidence.attach.drawer', keys: 'e', scope: 'flow-drawer', label: 'Attach as evidence', group: 'Evidence', run: attach });
  document.addEventListener('keydown', (e) => {
    if (e.defaultPrevented || !e.target || !e.target.closest) return;
    if (e.target.closest('#attachPicker')) return;
    const scopes = e.target.closest('#flowDrawer') ? ['flow-drawer'] : e.target.closest('.trow[data-id]') ? ['proxy-list'] : null;
    if (scopes) registry.handle(e, { scopes });
  });
  core.registerHook('attachEvidence', attachEvidence);
  core.registerHook('evidenceKeys', () => registry);
}

/* ---- drag: one delegated dragstart; drop zones are additive only ---- */

export const FLOW_DRAG_TYPE = 'application/x-interseptor-flow';

function initDrag(core) {
  document.addEventListener('pointerdown', (e) => {
    const row = e.target.closest && e.target.closest('.trow[data-id], [data-evidence-flow]');
    if (row && !row.draggable) row.draggable = true;
  });
  document.addEventListener('dragstart', (e) => {
    const row = e.target.closest && e.target.closest('.trow[data-id], [data-evidence-flow]');
    if (!row || !e.dataTransfer) return;
    const id = Number(row.dataset.evidenceFlow || row.dataset.id);
    if (!Number.isSafeInteger(id) || id <= 0) return;
    e.dataTransfer.setData(FLOW_DRAG_TYPE, String(id));
    e.dataTransfer.effectAllowed = 'copy';
  });
  const zone = (e) => (e.target.closest ? e.target.closest('[data-evidence-drop]') : null);
  const isFlowDrag = (e) => !!e.dataTransfer && [...(e.dataTransfer.types || [])].includes(FLOW_DRAG_TYPE);
  document.addEventListener('dragover', (e) => { const z = zone(e); if (z && isFlowDrag(e)) { e.preventDefault(); z.classList.add('is-drag-over'); } });
  document.addEventListener('dragleave', (e) => { const z = zone(e); if (z) z.classList.remove('is-drag-over'); });
  document.addEventListener('drop', (e) => {
    const z = zone(e);
    if (!z || !isFlowDrag(e)) return;
    e.preventDefault();
    z.classList.remove('is-drag-over');
    const id = Number(e.dataTransfer.getData(FLOW_DRAG_TYPE));
    const findingId = Number(z.dataset.findingId);
    if (id) attachEvidence({ kind: 'flow', refs: [id] }, { findingId: findingId || undefined, anchor: z });
  });
}

if (typeof document !== 'undefined') {
  loadCore().then((core) => { initKeys(core); initDrag(core); }).catch(() => { /* optional module: a failure must not block boot */ });
}

// Human label for a finding image/evidence block source; generated sources say so.
export const evidenceSourceLabel = source => ({ device_screenshot: 'Device capture · reviewer declared', browser_screenshot: 'Browser capture · reviewer declared', operator_upload: 'Uploaded image · origin unconfirmed', flow_preview: 'Generated HTTP preview', evidence_render: 'Generated evidence render · not browser proof', generated_image: 'Generated image · not browser proof', tool_output: 'Tool output', captured_flow: 'Captured traffic' })[source] || source || 'Origin unconfirmed';
