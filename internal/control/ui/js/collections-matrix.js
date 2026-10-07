// collections-matrix.js — Collections identity matrix, OpenAPI coverage,
// run timing, saved-example diff and Intruder handoff (internal/collmatrix).
// Self-contained: injects its own stylesheet, imports only shared modules
// (never collections.js, so no load-order dependency on the Collections
// panel), and reaches the rest of the UI through command-palette entries
// registered on import — the merge step wires this file's <script> tag and
// the stylesheet's <link> into index.html (see .claude/skills/collmatrix).
import { $, esc, api, toast, toastError, icon, uiPrompt, getHook } from './core.js';
import { registerCommand } from './shell-hooks.js';
import { openSheet, closeSheet } from './sheet.js';
import { renderState } from './statepanel.js';
import { createDiffView, diffLines, normalizeBody } from './diff.js';
import * as MM from './collections-matrix-model.js';

function injectStylesheet() {
  if (document.querySelector('link[href="/css/collections-matrix.css"]')) return;
  const l = document.createElement('link');
  l.rel = 'stylesheet';
  l.href = '/css/collections-matrix.css';
  document.head.appendChild(l);
}

const el = (tag, cls, text) => { const e = document.createElement(tag); if (cls) e.className = cls; if (text != null) e.textContent = text; return e; };
const btn = (label, onClick, cls = 'btn', iconName) => {
  const b = el('button', cls);
  b.type = 'button';
  if (iconName) b.insertAdjacentHTML('beforeend', icon(iconName));
  b.append(document.createTextNode(label));
  if (onClick) b.addEventListener('click', onClick);
  return b;
};

// activeCollection reads the Collections panel's own picker rather than
// importing collections-core.js (which would couple load order); it is a
// plain <select>, read by id like every other cross-module read in this UI.
function activeCollection() {
  const sel = $('#collPicker');
  return sel && sel.value ? sel.value : '';
}
function activeEnv() {
  const sel = $('#collEnv');
  return sel ? sel.value || '' : '';
}

async function pickCollection() {
  let uid = activeCollection();
  if (!uid) {
    const typed = await uiPrompt({ title: 'Collection — Collection UID (open it in the Collections panel first to pick it automatically):', placeholder: 'collection uid' });
    uid = (typed || '').trim();
  }
  return uid;
}

/* ------------------------------------------------------------------ identity matrix */

async function fetchIdentities() {
  try {
    const r = await api('/api/authz');
    return (r.identities || []).map((i) => i.name).filter(Boolean);
  } catch (e) {
    return [];
  }
}

function matrixTable(m) {
  const t = el('table', 'cxm-table');
  t.setAttribute('aria-label', 'Identity matrix: ' + MM.matrixCaption(m));
  const thead = el('thead');
  const hr = el('tr');
  hr.append(el('th', '', 'Request'));
  (m.identities || []).forEach((name) => {
    const th = el('th', '', name);
    if (name === m.baseline) th.classList.add('cxm-baseline');
    if (m.expect && m.expect[name]) th.title = 'expected: ' + m.expect[name];
    hr.append(th);
  });
  thead.append(hr);
  t.append(thead);
  const tbody = el('tbody');
  (m.rows || []).forEach((row) => {
    const tr = el('tr');
    const label = el('td', 'cxm-row-name');
    label.append(el('span', 'cxm-method', row.method || ''), document.createTextNode(' ' + (row.name || row.itemUid)));
    tr.append(label);
    (row.cells || []).forEach((cell, i) => {
      const v = MM.cellVerdict(cell);
      const td = el('td', 'cxm-cell');
      td.dataset.flag = cell.flag || '';
      td.dataset.class = cell.class || '';
      td.title = `${m.identities[i]}: ${MM.classLabel(cell.class)}${cell.status ? ' (HTTP ' + cell.status + ')' : ''} — ${v.word}`;
      const g = el('span', 'cxm-glyph', v.glyph);
      td.append(g);
      if (cell.status) td.append(el('span', 'cxm-status', String(cell.status)));
      if (cell.flowId) {
        td.classList.add('cxm-has-flow');
        td.tabIndex = 0;
        td.addEventListener('click', () => openFlowPreview(cell.flowId));
        td.addEventListener('keydown', (e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); openFlowPreview(cell.flowId); } });
      }
      tr.append(td);
    });
    tbody.append(tr);
  });
  t.append(tbody);
  return t;
}

function openFlowPreview(flowId) {
  getHook('openFlow')?.(flowId) || window.open('/api/flows/' + flowId + '/raw?side=req', '_blank', 'noopener');
}

function matrixLegend() {
  const l = el('div', 'cxm-legend');
  [['=', 'same as baseline'], ['~', 'differs'], ['V', 'unexpected access (hypothesis)'], ['?', 'unexpected denial'], ['!', 'blocked / error'], ['·', 'not run']]
    .forEach(([g, w]) => { const s = el('span', 'cxm-legend-item'); s.append(el('span', 'cxm-glyph', g), document.createTextNode(' ' + w)); l.append(s); });
  return l;
}

async function attachMatrixFlow(m, findingId, onlyFlagged) {
  const r = await api('/api/collmatrix/' + m.id + '/attach', { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ findingId, onlyFlagged }) });
  toast('attached ' + r.attached + ' flow' + (r.attached === 1 ? '' : 's') + ' to finding #' + findingId);
}

function renderMatrix(body, m, timingDiffs) {
  body.textContent = '';
  const wrap = el('div', 'cxm-wrap');
  wrap.append(el('p', 'cxm-summary', MM.matrixCaption(m)));
  if (m.hypotheses?.length) {
    const h = el('div', 'cxm-hypotheses');
    m.hypotheses.forEach((s) => h.append(el('p', '', s)));
    wrap.append(h);
  }
  if (m.skipped?.length) wrap.append(el('p', 'cxm-note', 'Skipped (broken or no credentials): ' + m.skipped.join(', ')));
  if (m.warnings?.length) wrap.append(el('p', 'cxm-note', m.warnings.join(' · ')));
  const scroll = el('div', 'cxm-scroll');
  scroll.append(matrixTable(m));
  wrap.append(scroll, matrixLegend());
  if (timingDiffs?.length) {
    const tsec = el('div', 'cxm-timing-diff');
    tsec.append(el('h4', '', 'Response-time differential'), el('p', 'cxm-note', MM.flaggedRowCount(m) ? '' : ''));
    const ul = el('ul');
    timingDiffs.forEach((d) => ul.append(el('li', '', `${d.row}: ${d.identity} took ${d.ms} ms (row median ${d.medianMs} ms)`)));
    tsec.append(ul);
    wrap.append(tsec);
  }
  const actions = el('div', 'cxm-actions');
  actions.append(btn('Render image', async () => {
    try { window.open('/api/collmatrix/' + m.id + '/render.png', '_blank', 'noopener'); } catch (e) { toastError('Render failed', e); }
  }, 'btn', 'image'));
  actions.append(btn('Attach flagged to finding', async () => {
    const id = Number((await uiPrompt({ title: 'Attach evidence — Finding ID', placeholder: '123' })) || '');
    if (!id) return;
    try { await attachMatrixFlow(m, id, true); } catch (e) { toastError('Attach failed', e); }
  }, 'btn', 'attach'));
  actions.append(btn('Attach every cell', async () => {
    const id = Number((await uiPrompt({ title: 'Attach evidence — Finding ID', placeholder: '123' })) || '');
    if (!id) return;
    try { await attachMatrixFlow(m, id, false); } catch (e) { toastError('Attach failed', e); }
  }, 'btn'));
  wrap.append(actions);
  body.append(wrap);
}

async function openMatrixSheet() {
  injectStylesheet();
  const collectionUid = await pickCollection();
  if (!collectionUid) return;
  openSheet({
    id: 'collMatrixSheet', title: 'Identity matrix', detents: ['half', 'full'], detent: 'full',
    content: (body) => { renderState(body, 'loading', { title: 'Loading identities' }); runMatrix(collectionUid, body); return body; },
  });
}

async function runMatrix(collectionUid, body) {
  const known = await fetchIdentities();
  if (!known.length) {
    renderState(body, 'empty-first', { title: 'No saved authz identities', hint: 'Add identities in the Authz panel first, or run with anonymous only.' });
  }
  try {
    const r = await api('/api/collmatrix/run', { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ collectionUid, envUid: activeEnv() }) });
    renderMatrix(body, r.matrix, r.timing);
  } catch (e) {
    renderState(body, 'error', { title: 'Could not run the identity matrix', message: e.message, status: e.status, onRetry: () => runMatrix(collectionUid, body) });
  }
}

/* ------------------------------------------------------------------ coverage */

function coverageRow(op) {
  const tr = el('tr');
  tr.dataset.state = op.state;
  const name = el('td', 'cxm-row-name');
  name.append(el('span', 'cxm-method', op.method), document.createTextNode(' ' + op.path));
  if (op.deprecated) name.append(el('span', 'cxm-chip', 'deprecated'));
  tr.append(name);
  tr.append(el('td', '', op.itemName || ''));
  const st = el('td', 'cxm-state-' + op.state, MM.coverageLabel(op.state));
  tr.append(st);
  tr.append(el('td', '', op.hits ? String(op.hits) : ''));
  tr.append(el('td', '', (op.statuses || []).join(', ')));
  return tr;
}

function renderCoverage(body, rep) {
  body.textContent = '';
  const wrap = el('div', 'cxm-wrap');
  wrap.append(el('p', 'cxm-summary', MM.coverageSummaryText(rep)));
  if (rep.groups?.length) {
    const bars = el('div', 'cxm-groups');
    rep.groups.forEach((g) => {
      const row = el('div', 'cxm-group-row');
      row.append(el('span', 'cxm-group-name', g.name));
      const track = el('span', 'cxm-group-track');
      const fill = el('span', 'cxm-group-fill');
      fill.style.width = Math.round(MM.groupFraction(g) * 100) + '%';
      track.append(fill);
      row.append(track, el('span', 'cxm-group-n', g.exercised + '/' + g.total));
      bars.append(row);
    });
    wrap.append(bars);
  }
  const filter = el('div', 'cxm-filter-row');
  const search = Object.assign(document.createElement('input'), { type: 'search', placeholder: 'Filter operations', 'aria-label': 'Filter operations' });
  const select = document.createElement('select');
  select.setAttribute('aria-label', 'State');
  ['', 'untested', 'blocked', 'failing', 'passing'].forEach((s) => { const o = document.createElement('option'); o.value = s; o.textContent = s ? MM.coverageLabel(s) : 'All states'; select.append(o); });
  filter.append(search, select);
  wrap.append(filter);
  const scroll = el('div', 'cxm-scroll');
  const table = el('table', 'cxm-table');
  table.innerHTML = '<thead><tr><th>Operation</th><th>Item</th><th>State</th><th>Hits</th><th>Statuses seen</th></tr></thead>';
  const tbody = el('tbody');
  table.append(tbody);
  scroll.append(table);
  wrap.append(scroll);
  body.append(wrap);
  const paint = () => {
    tbody.textContent = '';
    MM.filterOperations(rep.operations, select.value, search.value).forEach((op) => tbody.append(coverageRow(op)));
    if (!tbody.children.length) tbody.append((() => { const tr = el('tr'); const td = el('td', 'cxm-note', 'No matching operations.'); td.colSpan = 5; tr.append(td); return tr; })());
  };
  search.addEventListener('input', paint);
  select.addEventListener('change', paint);
  paint();
}

async function openCoverageSheet() {
  injectStylesheet();
  const collectionUid = await pickCollection();
  if (!collectionUid) return;
  openSheet({
    id: 'collCoverageSheet', title: 'OpenAPI coverage', detents: ['half', 'full'], detent: 'full',
    content: (body) => { renderState(body, 'loading', { title: 'Loading coverage' }); loadCoverage(collectionUid, body); return body; },
  });
}

async function loadCoverage(collectionUid, body) {
  try {
    const rep = await api('/api/collmatrix/coverage?collection=' + encodeURIComponent(collectionUid));
    renderCoverage(body, rep);
  } catch (e) {
    renderState(body, 'error', { title: 'Could not load coverage', message: e.message, status: e.status, onRetry: () => loadCoverage(collectionUid, body) });
  }
}

/* ------------------------------------------------------------------ timing */

function renderTiming(body, tr) {
  body.textContent = '';
  const wrap = el('div', 'cxm-wrap');
  wrap.append(el('p', 'cxm-summary', MM.timingSummaryText(tr)), el('p', 'cxm-note', tr.note));
  const bars = el('div', 'cxm-phasebar');
  MM.phaseBars(tr.phases).forEach((p) => {
    const seg = el('span', 'cxm-phase-' + p.name);
    seg.style.width = Math.max(0, p.pct) + '%';
    seg.title = p.name + ': ' + MM.fmtMs(p.ms);
    bars.append(seg);
  });
  wrap.append(bars);
  const scroll = el('div', 'cxm-scroll');
  const table = el('table', 'cxm-table');
  table.innerHTML = '<thead><tr><th>Request</th><th>Count</th><th>Min</th><th>P50</th><th>P95</th><th>Max</th><th>Bytes</th></tr></thead>';
  const tbody = el('tbody');
  (tr.items || []).forEach((it) => {
    const row = el('tr');
    const name = el('td', 'cxm-row-name');
    name.append(el('span', 'cxm-method', it.method || ''), document.createTextNode(' ' + it.name));
    row.append(name, el('td', '', String(it.count)), el('td', '', MM.fmtMs(it.minMs)), el('td', '', MM.fmtMs(it.p50Ms)), el('td', '', MM.fmtMs(it.p95Ms)), el('td', '', MM.fmtMs(it.maxMs)), el('td', '', String(it.bytes || 0)));
    tbody.append(row);
  });
  table.append(tbody);
  scroll.append(table);
  wrap.append(scroll);
  const outliers = MM.sortedOutliers(tr.outliers);
  if (outliers.length) {
    const sec = el('div', 'cxm-outliers');
    sec.append(el('h4', '', 'Slow outliers'));
    const ul = el('ul');
    outliers.forEach((o) => {
      const li = el('li', '', `${o.name} iteration ${o.iteration + 1}: ${MM.fmtMs(o.durationMs)} vs a ${MM.fmtMs(o.medianMs)} median`);
      if (o.flowId) li.append(' ', btn('view', () => openFlowPreview(o.flowId), 'btn xs'));
      ul.append(li);
    });
    sec.append(ul);
    wrap.append(sec);
  }
  body.append(wrap);
}

async function openTimingSheet() {
  injectStylesheet();
  const collectionUid = await pickCollection();
  if (!collectionUid) return;
  const runUid = (await uiPrompt({ title: 'Run timing — Run UID (see the Runner view or History)', placeholder: 'run uid' }) || '').trim();
  if (!runUid) return;
  openSheet({
    id: 'collTimingSheet', title: 'Run timing', detents: ['half', 'full'], detent: 'full',
    content: (body) => { renderState(body, 'loading', { title: 'Loading timing' }); loadTiming(collectionUid, runUid, body); return body; },
  });
}

async function loadTiming(collectionUid, runUid, body) {
  try {
    const tr = await api('/api/collmatrix/timing?collection=' + encodeURIComponent(collectionUid) + '&run=' + encodeURIComponent(runUid));
    renderTiming(body, tr);
  } catch (e) {
    renderState(body, 'error', { title: 'Could not load timing', message: e.message, status: e.status, onRetry: () => loadTiming(collectionUid, runUid, body) });
  }
}

/* ------------------------------------------------------------------ saved-example diff */

function renderExampleDiff(body, d) {
  body.textContent = '';
  const wrap = el('div', 'cxm-wrap');
  wrap.append(el('p', 'cxm-summary', MM.diffSummaryText(d)));
  if (d.headers?.length) {
    const ul = el('ul', 'cxm-headerdiff');
    d.headers.forEach((h) => ul.append(el('li', '', `${h.kind} ${h.name}${h.expected ? ' expected=' + h.expected : ''}${h.actual ? ' actual=' + h.actual : ''}`)));
    wrap.append(ul);
  }
  if (d.json?.length) {
    const ul = el('ul', 'cxm-jsondiff');
    d.json.forEach((c) => ul.append(el('li', '', `${c.kind} ${c.path}${c.expected !== undefined && c.expected !== '' ? ' ' + c.expected + ' →' : ''}${c.actual !== undefined && c.actual !== '' ? ' ' + c.actual : ''}`)));
    wrap.append(ul);
  }
  if (d.bodyFormat && (d.expectedBody || d.actualBody)) {
    const host = el('div', 'cxm-diffview');
    wrap.append(host);
    const view = createDiffView(host, { mode: 'unified' });
    view.setResult(diffLines(normalizeBody(d.expectedBody, { json: d.bodyFormat === 'json' }), normalizeBody(d.actualBody, { json: d.bodyFormat === 'json' })));
  }
  body.append(wrap);
}

async function openDiffSheet() {
  injectStylesheet();
  const collectionUid = await pickCollection();
  if (!collectionUid) return;
  const itemUid = (await uiPrompt({ title: 'Saved-example diff — Request item UID (open it in Collections to copy its UID)', placeholder: 'item uid' }) || '').trim();
  if (!itemUid) return;
  const example = (await uiPrompt({ title: 'Saved-example diff — Example name (or its 1-based position)', placeholder: 'e.g. ok' }) || '').trim();
  const flowId = Number((await uiPrompt({ title: 'Saved-example diff — Flow ID of the actual response to compare', placeholder: '123' })) || '');
  if (!flowId) return;
  openSheet({
    id: 'collExampleDiffSheet', title: 'Saved-example diff', detents: ['half', 'full'], detent: 'full',
    content: (body) => { renderState(body, 'loading', { title: 'Diffing' }); loadDiff(collectionUid, itemUid, example, flowId, body); return body; },
  });
}

async function loadDiff(collectionUid, itemUid, example, flowId, body) {
  try {
    const d = await api('/api/collmatrix/diff-example', { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ collectionUid, itemUid, example, flowId }) });
    renderExampleDiff(body, d);
  } catch (e) {
    renderState(body, 'error', { title: 'Could not diff the example', message: e.message, status: e.status, onRetry: () => loadDiff(collectionUid, itemUid, example, flowId, body) });
  }
}

/* ------------------------------------------------------------------ Intruder handoff */

// loadIntoIntruder fills the Intruder editor and dispatches the same `input`
// event tools.js already listens on, so Intruder's own wiring (payload
// inputs, touch/save) runs without this module reaching into tools.js.
function loadIntoIntruder(target, template) {
  const t = $('#intrTarget'), tpl = $('#intrTemplate');
  if (!t || !tpl) { toast('Intruder is not available'); return false; }
  document.querySelector('.tab[data-tab="intruder"]')?.click();
  t.value = target;
  tpl.value = template;
  tpl.dispatchEvent(new Event('input', { bubbles: true }));
  return true;
}

async function openHandoffSheet() {
  injectStylesheet();
  const collectionUid = await pickCollection();
  if (!collectionUid) return;
  const itemUid = (await uiPrompt({ title: 'Send to Intruder — Request item UID (open it in Collections to copy its UID)', placeholder: 'item uid' }) || '').trim();
  if (!itemUid) return;
  const positions = (await uiPrompt({ title: 'Send to Intruder — Variable name(s) to fuzz, comma-separated (leave empty to auto-detect)', placeholder: 'e.g. orderId, userId' }) || '').trim();
  try {
    const h = await api('/api/collmatrix/handoff', {
      method: 'POST', headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ collectionUid, itemUid, envUid: activeEnv(), positions: positions ? positions.split(',').map((s) => s.trim()).filter(Boolean) : [] }),
    });
    if (h.notes?.length) toast(h.notes[0]);
    if (loadIntoIntruder(h.target, h.template)) toast('Loaded into Intruder — ' + MM.handoffSummaryText(h));
  } catch (e) { toastError('Send to Intruder failed', e); }
}

/* ------------------------------------------------------------------ commands */

registerCommand({ t: 'Collections: Identity matrix', kw: 'authz matrix identity bola bfla access control', group: 'Actions', run: openMatrixSheet });
registerCommand({ t: 'Collections: OpenAPI coverage', kw: 'openapi coverage spec operations exercised', group: 'Actions', run: openCoverageSheet });
registerCommand({ t: 'Collections: Run timing breakdown', kw: 'timing performance latency run', group: 'Actions', run: openTimingSheet });
registerCommand({ t: 'Collections: Diff saved example', kw: 'example diff response compare', group: 'Actions', run: openDiffSheet });
registerCommand({ t: 'Collections: Send item to Intruder (auto positions)', kw: 'intruder fuzz handoff send', group: 'Actions', run: openHandoffSheet });

export const _internal = { activeCollection, activeEnv, loadIntoIntruder };
