// evidence-render.js — shared "evidence render" preview. Interseptor draws
// deterministic, light-canvas PNGs from recorded data only (Intruder timeline,
// distribution, race window and payload strip, plus the authz matrix, flow
// diff, flow waterfall and finding chain). This module is the one UI for all of
// them:
//
//   EvidenceRender.open(kind, params, {opener?})
//
// opens a modal that fetches the PNG, shows it with the server-generated alt
// text as the img alt and a visible caption, and offers kind tabs, "Download
// PNG" and "+ Finding" (pick a finding, then POST /api/findings/{id}/evidence-render).
//
// Server contract used here:
//   GET  /api/intruder/attacks/{runId}/render.png?kind=timeline|distribution|race|strip
//        -> image/png; X-Render-Alt and X-Render-Summary carry the alt text and
//           a one-line summary (percent-encoded UTF-8).
//   POST /api/findings/{id}/evidence-render  {kind, runId, caption, role}
// Other families pass `params.url` (the PNG GET) and optional `params.attach`
// (extra POST fields) from their own entry points.
//
// Nothing is drawn client-side and nothing untrusted reaches innerHTML: every
// string is set through textContent / attributes. The pure helpers at the top
// import nothing and run under `node --test`; DOM code loads core.js lazily.

export const KINDS = {
  'intruder-timeline': { family: 'intruder', short: 'timeline', label: 'Timeline', hint: 'Waterfall lanes: did the target throttle or lock out, and when?' },
  'intruder-distribution': { family: 'intruder', short: 'distribution', label: 'Distribution', hint: 'Status, length and latency clusters with outliers.' },
  'intruder-race': { family: 'intruder', short: 'race', label: 'Race', hint: 'Launch window and duplicated responses for a race / repeat run.' },
  'intruder-strip': { family: 'intruder', short: 'strip', label: 'Strip', hint: 'One cell per payload in dispatch order; outliers labelled.' },
  'authz-matrix': { family: 'authz-matrix', short: 'matrix', label: 'Authz matrix', hint: 'Identities by endpoints with differences from the baseline.' },
  'flow-diff': { family: 'flow-diff', short: 'diff', label: 'Flow diff', hint: 'What changed between two flows.' },
  'flow-waterfall': { family: 'flow-waterfall', short: 'waterfall', label: 'Flow waterfall', hint: 'Order and total duration of a flow sequence.' },
  'finding-chain': { family: 'finding-chain', short: 'chain', label: 'Finding chain', hint: 'How findings connect into an attack path.' },
};

const kindOrder = Object.keys(KINDS);
const meta = (kind) => {
  const m = KINDS[kind];
  if (!m) throw new Error('unknown render kind: ' + kind);
  return m;
};

// familyKinds returns the sibling kinds shown as tabs, in a stable order.
export function familyKinds(kind) {
  const fam = meta(kind).family;
  return kindOrder.filter((k) => KINDS[k].family === fam);
}

const safeId = (v) => String(v == null ? '' : v).replace(/[^A-Za-z0-9_-]+/g, '-').replace(/^-+|-+$/g, '');

// renderRequest builds the GET for a kind. Intruder kinds need params.runId;
// every other family brings its own params.url.
export function renderRequest(kind, params = {}) {
  const m = meta(kind);
  if (m.family === 'intruder') {
    const runId = String(params.runId || '');
    if (!runId) throw new Error('an Intruder render needs a run id');
    return { url: '/api/intruder/attacks/' + encodeURIComponent(runId) + '/render.png?kind=' + m.short };
  }
  if (!params.url) throw new Error('this render needs a url');
  return { url: String(params.url) };
}

export function downloadName(kind, params = {}) {
  const m = meta(kind);
  if (m.family === 'intruder') return 'interseptor-intruder-' + (safeId(params.runId) || 'run') + '-' + m.short + '.png';
  const id = safeId(params.id != null ? params.id : params.runId);
  return 'interseptor-' + kind + (id ? '-' + id : '') + '.png';
}

export function attachRequest(findingId, kind, params = {}, caption = '') {
  const id = Number(findingId);
  if (!Number.isSafeInteger(id) || id <= 0) throw new Error('choose a finding first');
  meta(kind);
  const body = Object.assign({}, params.attach || {}, { kind });
  if (params.runId) body.runId = String(params.runId);
  body.caption = String(caption || '');
  body.role = 'result';
  return { path: '/api/findings/' + id + '/evidence-render', method: 'POST', body };
}

export const hasTiming = (rows) => Array.isArray(rows) && rows.some((r) => r && (Number(r.startUs) > 0 || Number(r.endUs) > 0));

// toolbarState decides whether the Intruder "Preview image" control is usable.
export function toolbarState({ runId, running, total, results } = {}) {
  const rows = Array.isArray(results) ? results : [];
  const base = { enabled: false, reason: '', note: '', timingRecorded: hasTiming(rows) };
  if (!runId) return { ...base, reason: 'Preview needs a run ID. Start an attack; runs recorded before this feature have none.' };
  if (running) return { ...base, reason: 'Wait for the attack to finish before rendering a preview.' };
  if (!total || !rows.length) return { ...base, reason: 'No results to render yet.' };
  return { ...base, enabled: true, note: base.timingRecorded ? '' : 'timing not recorded for this run: Timeline and Race fall back to a completion-ordered strip' };
}

export function decodeHeader(v) {
  if (v == null) return '';
  const s = String(v);
  try { return decodeURIComponent(s); } catch (e) { return s; }
}

/* ---- DOM ---- */

let corePromise = null;
const loadCore = () => corePromise || (corePromise = import('./core.js'));
const $id = (id) => document.getElementById(id);

const FINDING_CAP = 200;
let session = null; // the open preview: {kind, params, epoch, ctrl, blobUrl, blob, alt, opener}
let epoch = 0;

function setStatus(text) {
  const s = $id('evRenderStatus');
  if (s) s.textContent = text;
}

function revoke() {
  if (session && session.blobUrl) { try { URL.revokeObjectURL(session.blobUrl); } catch (e) { /* ignore */ } }
  if (session) { session.blobUrl = ''; session.blob = null; }
}

function setBusy(on) {
  const stage = $id('evRenderStage');
  if (stage) stage.setAttribute('aria-busy', on ? 'true' : 'false');
  for (const id of ['evRenderAttach', 'evRenderDownload']) {
    const b = $id(id);
    if (b) b.disabled = on || !session || !session.blob;
  }
  const load = $id('evRenderLoading');
  if (load) load.hidden = !on;
}

function showError(msg) {
  const img = $id('evRenderImg');
  const err = $id('evRenderError');
  if (img) { img.hidden = true; img.removeAttribute('src'); img.alt = ''; }
  if (err) { err.hidden = false; err.textContent = msg; }
  $id('evRenderCaption').textContent = '';
  $id('evRenderSummary').textContent = '';
  setStatus('Preview failed: ' + msg);
}

function renderTabs(kind) {
  const tabs = $id('evRenderTabs');
  const kinds = familyKinds(kind);
  const same = tabs.children.length === kinds.length && kinds.every((k, i) => tabs.children[i].dataset.kind === k);
  if (!same) {
    tabs.textContent = '';
    kinds.forEach((k) => {
      const b = document.createElement('button');
      b.type = 'button';
      b.className = 'evr-tab';
      b.id = 'evRenderTab-' + k;
      b.setAttribute('role', 'tab');
      b.setAttribute('aria-controls', 'evRenderStage');
      b.dataset.kind = k;
      b.textContent = KINDS[k].label;
      tabs.appendChild(b);
    });
  }
  tabs.hidden = kinds.length < 2;
  for (const b of tabs.children) {
    const on = b.dataset.kind === kind;
    b.setAttribute('aria-selected', on ? 'true' : 'false');
    b.tabIndex = on ? 0 : -1;
  }
  $id('evRenderStage').setAttribute('aria-labelledby', 'evRenderTab-' + kind);
}

function renderNote(kind, params) {
  const notes = [];
  if (kind === 'intruder-race') notes.push('Separate connections launched together, not single-packet synchronisation.');
  if (params && params.timingRecorded === false && (kind === 'intruder-timeline' || kind === 'intruder-race')) notes.push('Timing not recorded for this run; the render falls back to a completion-ordered strip.');
  $id('evRenderNote').textContent = notes.join(' ');
}

async function load(kind) {
  const my = ++epoch;
  if (session && session.ctrl) session.ctrl.abort();
  revoke();
  session.kind = kind;
  session.alt = '';
  const m = KINDS[kind];
  $id('evRenderTitle').textContent = 'Evidence render: ' + m.label;
  $id('evRenderHint').textContent = m.hint;
  renderTabs(kind);
  renderNote(kind, session.params);
  hidePicker(false);
  const err = $id('evRenderError');
  err.hidden = true;
  err.textContent = '';
  const img = $id('evRenderImg');
  img.hidden = true;
  img.removeAttribute('src');
  $id('evRenderCaption').textContent = '';
  $id('evRenderSummary').textContent = '';
  setBusy(true);
  setStatus('Rendering ' + m.label + ' preview');
  let req;
  try { req = renderRequest(kind, session.params); } catch (e) { setBusy(false); showError(e.message); return; }
  const ctrl = typeof AbortController !== 'undefined' ? new AbortController() : null;
  session.ctrl = ctrl;
  try {
    const res = await fetch(req.url, ctrl ? { signal: ctrl.signal } : undefined);
    if (my !== epoch) return;
    if (res.status === 401 && location.pathname !== '/login') { location.href = '/login'; return; }
    if (!res.ok) {
      let msg = 'HTTP ' + res.status;
      try { const t = (await res.text()).trim(); if (t) msg = t.slice(0, 200); } catch (e) { /* keep status */ }
      if (my === epoch) { setBusy(false); showError(msg); }
      return;
    }
    const blob = await res.blob();
    if (my !== epoch) return;
    const alt = decodeHeader(res.headers.get('X-Render-Alt')) || m.label + ' evidence render generated from recorded data';
    const summary = decodeHeader(res.headers.get('X-Render-Summary'));
    session.blob = blob;
    session.blobUrl = URL.createObjectURL(blob);
    session.alt = alt;
    img.alt = alt;
    img.src = session.blobUrl;
    img.hidden = false;
    $id('evRenderCaption').textContent = alt;
    $id('evRenderSummary').textContent = summary;
    setBusy(false);
    setStatus(m.label + ' preview ready. ' + (summary || alt));
  } catch (e) {
    if (my !== epoch || (e && e.name === 'AbortError')) return;
    setBusy(false);
    showError((e && e.message) || 'request failed');
  }
}

/* ---- finding picker (+ Finding) ---- */

let findingsLoaded = false;

function hidePicker(refocus) {
  const pick = $id('evRenderPick');
  if (!pick || pick.hidden) return;
  pick.hidden = true;
  const b = $id('evRenderAttach');
  if (b) b.setAttribute('aria-expanded', 'false');
  if (refocus && b) b.focus();
}

async function fillFindings(core) {
  const sel = $id('evRenderFinding');
  const st = $id('evRenderPickStatus');
  if (findingsLoaded) return;
  st.textContent = 'Loading findings…';
  try {
    const d = await core.api('/api/findings');
    const list = (d && d.findings || []).slice(0, FINDING_CAP);
    sel.textContent = '';
    list.forEach((f) => {
      const o = document.createElement('option');
      o.value = String(f.id);
      o.textContent = '#' + f.id + ' ' + (f.title || 'Untitled') + (f.severity ? ' (' + f.severity + ')' : '');
      sel.appendChild(o);
    });
    findingsLoaded = true;
    st.textContent = list.length ? list.length + (list.length === 1 ? ' finding' : ' findings') : 'No findings yet. Create one from Findings first.';
    $id('evRenderPickGo').disabled = !list.length;
  } catch (e) {
    st.textContent = 'Could not load findings: ' + ((e && e.message) || 'request failed');
    $id('evRenderPickGo').disabled = true;
  }
}

async function togglePicker() {
  const pick = $id('evRenderPick');
  if (!session || !session.blob) return;
  if (!pick.hidden) { hidePicker(true); return; }
  const core = await loadCore();
  pick.hidden = false;
  $id('evRenderAttach').setAttribute('aria-expanded', 'true');
  await fillFindings(core);
  (findingsLoaded && $id('evRenderFinding').options.length ? $id('evRenderFinding') : $id('evRenderPickCancel')).focus();
}

async function attach() {
  if (!session || !session.blob) return;
  const core = await loadCore();
  const go = $id('evRenderPickGo');
  const sel = $id('evRenderFinding');
  let req;
  try { req = attachRequest(sel.value, session.kind, session.params, session.alt); } catch (e) { setStatus(e.message); core.toast(e.message, 'warn'); return; }
  go.disabled = true;
  go.setAttribute('aria-busy', 'true');
  try {
    await core.api(req.path, { method: req.method, headers: { 'content-type': 'application/json' }, body: JSON.stringify(req.body) });
    const msg = 'Attached ' + KINDS[session.kind].label + ' render to finding #' + sel.value + ' as a generated evidence render.';
    setStatus(msg);
    core.toast(msg, 'success');
    findingsLoaded = false;
    hidePicker(true);
    try { const ps = (await import('./project-state.js')).projectState; await ps.refresh({ reason: 'attach' }); } catch (e) { /* counters only */ }
  } catch (e) {
    setStatus('Attach failed: ' + ((e && e.message) || 'request failed'));
    core.toastError('Attach failed', e);
  } finally {
    go.disabled = false;
    go.removeAttribute('aria-busy');
  }
}

async function download() {
  if (!session || !session.blob) return;
  const core = await loadCore();
  const name = downloadName(session.kind, session.params);
  await core.saveFile(session.blob, name, 'image/png');
  setStatus('Downloaded ' + name);
}

/* ---- modal wiring ---- */

let wired = false;
function wire(core) {
  if (wired) return;
  wired = true;
  const modal = $id('evRenderDialog');
  const tabs = $id('evRenderTabs');
  tabs.addEventListener('click', (e) => {
    const b = e.target.closest && e.target.closest('button[data-kind]');
    if (b && session && b.dataset.kind !== session.kind) { load(b.dataset.kind); }
  });
  tabs.addEventListener('keydown', (e) => {
    const list = [...tabs.querySelectorAll('button[data-kind]')];
    const i = list.indexOf(document.activeElement);
    const d = e.key === 'ArrowRight' ? 1 : e.key === 'ArrowLeft' ? -1 : 0;
    if (i < 0 || !(d || e.key === 'Home' || e.key === 'End')) return;
    e.preventDefault();
    const n = e.key === 'Home' ? 0 : e.key === 'End' ? list.length - 1 : (i + d + list.length) % list.length;
    list[n].focus();
    load(list[n].dataset.kind);
  });
  $id('evRenderClose').addEventListener('click', close);
  $id('evRenderAttach').addEventListener('click', togglePicker);
  $id('evRenderPickGo').addEventListener('click', attach);
  $id('evRenderPickCancel').addEventListener('click', () => hidePicker(true));
  $id('evRenderDownload').addEventListener('click', download);
  // Esc inside the picker only closes the picker; core's handler closes the modal otherwise.
  modal.addEventListener('keydown', (e) => {
    if (e.key === 'Escape' && !$id('evRenderPick').hidden) { e.preventDefault(); e.stopImmediatePropagation(); hidePicker(true); }
  }, true);
}

export async function open(kind, params = {}, opts = {}) {
  meta(kind);
  const core = await loadCore();
  const modal = $id('evRenderDialog');
  if (!modal) { core.toast('Evidence render preview is unavailable in this build', 'error'); return; }
  wire(core);
  if (session) { if (session.ctrl) session.ctrl.abort(); revoke(); }
  session = { kind, params: params || {}, ctrl: null, blobUrl: '', blob: null, alt: '', opener: opts.opener || document.activeElement };
  findingsLoaded = false;
  core.openModal(modal, { onEscape: close, onDismiss: close, initialFocus: '#evRenderClose' });
  await load(kind);
}

export function close() {
  const modal = $id('evRenderDialog');
  epoch++;
  if (session && session.ctrl) session.ctrl.abort();
  revoke();
  const opener = session && session.opener;
  session = null;
  const img = $id('evRenderImg');
  if (img) { img.hidden = true; img.removeAttribute('src'); }
  hidePicker(false);
  if (modal) loadCore().then((core) => { core.closeModal(modal); if (opener && opener.isConnected && typeof opener.focus === 'function') opener.focus(); });
}

/* ---- Intruder toolbar: "Preview image" split button ---- */

const INTRUDER_KINDS = ['intruder-timeline', 'intruder-distribution', 'intruder-race', 'intruder-strip'];

// wireIntruderPreview binds the toolbar markup in index.html. Returns
// {update({runId, running, total, results})}, called by tools.js on each render.
export function wireIntruderPreview() {
  const main = $id('intrRenderBtn');
  const caret = $id('intrRenderMenuBtn');
  const menu = $id('intrRenderMenu');
  const status = $id('intrRenderStatus');
  if (!main || !caret || !menu || main.dataset.wired) return null;
  main.dataset.wired = '1';
  let state = toolbarState({});
  let last = 'intruder-timeline';
  let runId = '';
  let lastSpoken = '';

  INTRUDER_KINDS.forEach((k) => {
    const li = document.createElement('button');
    li.type = 'button';
    li.setAttribute('role', 'menuitem');
    li.className = 'evr-menuitem';
    li.dataset.kind = k;
    const t = document.createElement('span');
    t.className = 'evr-menuitem-title';
    t.textContent = KINDS[k].label;
    const h = document.createElement('span');
    h.className = 'evr-menuitem-hint';
    h.textContent = KINDS[k].hint;
    h.dataset.hint = '1';
    li.append(t, h);
    menu.appendChild(li);
  });
  const items = () => [...menu.querySelectorAll('[role="menuitem"]')];

  function say(text) {
    if (!status || text === lastSpoken) return;
    lastSpoken = text;
    status.textContent = text;
  }
  function closeMenu(refocus) {
    if (menu.hidden) return;
    menu.hidden = true;
    caret.setAttribute('aria-expanded', 'false');
    if (refocus) caret.focus();
  }
  function openMenu() {
    if (!state.enabled) { say(state.reason); return; }
    menu.hidden = false;
    caret.setAttribute('aria-expanded', 'true');
    const first = items().find((i) => i.dataset.kind === last) || items()[0];
    if (first) first.focus();
  }
  function launch(kind, opener) {
    if (!state.enabled) { say(state.reason); return; }
    last = kind;
    closeMenu(false);
    open(kind, { runId, timingRecorded: state.timingRecorded }, { opener });
  }
  function apply() {
    const dis = !state.enabled;
    for (const b of [main, caret]) {
      b.setAttribute('aria-disabled', dis ? 'true' : 'false');
      b.title = dis ? state.reason : (state.note || b.dataset.title || '');
    }
    main.title = dis ? state.reason : (state.note ? 'Preview image: ' + state.note : 'Preview image of this run, drawn from recorded data');
    for (const it of items()) {
      const note = !state.timingRecorded && (it.dataset.kind === 'intruder-timeline' || it.dataset.kind === 'intruder-race');
      it.querySelector('[data-hint]').textContent = note ? 'Timing not recorded for this run' : KINDS[it.dataset.kind].hint;
    }
    if (dis) closeMenu(false);
    say(dis ? '' : 'Preview image available' + (state.note ? ': ' + state.note : ''));
  }

  main.addEventListener('click', () => launch(last, main));
  caret.addEventListener('click', () => (menu.hidden ? openMenu() : closeMenu(true)));
  caret.addEventListener('keydown', (e) => {
    if (e.key === 'ArrowDown') { e.preventDefault(); openMenu(); }
  });
  menu.addEventListener('click', (e) => { const b = e.target.closest && e.target.closest('[data-kind]'); if (b) launch(b.dataset.kind, caret); });
  menu.addEventListener('keydown', (e) => {
    const list = items();
    const i = list.indexOf(document.activeElement);
    if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); closeMenu(true); }
    else if (e.key === 'ArrowDown') { e.preventDefault(); list[(i + 1) % list.length].focus(); }
    else if (e.key === 'ArrowUp') { e.preventDefault(); list[(i - 1 + list.length) % list.length].focus(); }
    else if (e.key === 'Home') { e.preventDefault(); list[0].focus(); }
    else if (e.key === 'End') { e.preventDefault(); list[list.length - 1].focus(); }
    else if (e.key === 'Tab') closeMenu(false);
  });
  document.addEventListener('pointerdown', (e) => { if (!menu.hidden && !menu.contains(e.target) && !caret.contains(e.target)) closeMenu(false); }, true);
  // A disabled-looking control stays focusable (aria-disabled) so its reason is reachable.
  for (const b of [main, caret]) b.addEventListener('click', (e) => { if (!state.enabled) { e.stopImmediatePropagation(); say(state.reason); } }, true);

  apply();
  return {
    update(info) {
      runId = info && info.runId ? String(info.runId) : '';
      state = toolbarState({ ...info, runId });
      apply();
    },
  };
}

if (typeof window !== 'undefined') window.EvidenceRender = { open, close, KINDS, wireIntruderPreview };
