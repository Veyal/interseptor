// identities.js — the Identities panel entry: what agents (and you) saved for
// authorization testing. The list shows each identity's health, auth type with
// a JWT expiry countdown and who created it; the detail (identities-detail.js)
// shows provenance, the masked token, the auth flow and access-test results;
// identities-edit.js is the New/Edit sheet. Live updates arrive as
// `authz.update` SSE events and are debounced; an open edit sheet is never
// replaced, it is marked stale instead. Pure rules: identities-model.js.
import { $, api, toast, toastError, uiPrompt, uiConfirm, esc, projectStorageKey, getHook } from './core.js';
import { registerCommand, registerSseHandler, TAB_CHANGE_EVENT, getShellApi } from './shell-hooks.js';
import { on as busOn, EVENTS } from './bus.js';
import { renderState } from './statepanel.js';
import { wireListbox, setListboxSelection } from './listbox.js';
import { createSplitPane } from './split.js';
import { openAuthz } from './authz.js';
import * as M from './identities-model.js';
import { S, X, jsend, enc, el, byName, panelActive, selectedFlowId, setStatus } from './identities-core.js';
import { renderDetail, setTab, selectRun } from './identities-detail.js';
import { openEdit, checkEditStale } from './identities-edit.js';

let pane = null;

/* ------------------------------------------------------------------ loading */

async function loadIdentities() {
  const gen = ++S.listGen;
  if (!S.loaded) renderState($('#identListState'), 'loading', { title: 'Loading identities', rows: 4 });
  try {
    const d = await api('/api/authz');
    if (gen !== S.listGen) return;
    S.ids = Array.isArray(d.identities) ? d.identities : [];
    S.loaded = true;
    if (S.sel && !byName(S.sel)) S.sel = '';
    if (!S.sel && S.ids.length) S.sel = S.ids[0].name;
    renderList();
    checkEditStale();
    renderDetail();
    if (S.sel) loadDetail(S.sel);
  } catch (e) {
    if (gen !== S.listGen) return;
    $('#identList').textContent = '';
    renderState($('#identListState'), 'error', { title: 'Could not load identities', message: e.message, status: e.status, onRetry: () => loadIdentities() });
  }
}

async function loadDetail(name) {
  if (!name) return;
  const gen = ++S.detailGen;
  if (S.detailFor !== name) { S.detail = null; S.detailError = null; }
  try {
    const d = await api('/api/authz/identities/' + enc(name));
    if (gen !== S.detailGen || S.sel !== name) return;
    S.detail = d; S.detailFor = name; S.detailError = null;
  } catch (e) {
    if (gen !== S.detailGen || S.sel !== name) return;
    S.detailFor = name; S.detailError = e;
  }
  renderDetail();
}

async function loadRuns() {
  const gen = ++S.runsGen;
  S.runsError = null;
  try {
    const d = await api(M.runsURL());
    if (gen !== S.runsGen) return;
    S.runs = Array.isArray(d.runs) ? d.runs : [];
    if (S.runSel && !S.runs.some((r) => String(r.id) === S.runSel)) { S.runSel = ''; S.matrix = null; S.matrixFor = ''; }
  } catch (e) {
    if (gen !== S.runsGen) return;
    S.runsError = e;
  }
  if (S.tab === 'access') renderDetail();
}

/* ------------------------------------------------------------------ list */

function renderList() {
  const list = $('#identList');
  const st = $('#identListState');
  if (!list || !st) return;
  list.textContent = '';
  st.textContent = '';
  if (!S.ids.length) {
    renderState(st, 'empty-first', {
      icon: 'identity', title: 'No identities yet',
      hint: 'An identity is a saved set of auth headers (a session cookie, a bearer token) that access tests replay to act as another user. Agents create them over MCP or REST when they log in or promote a captured login; you can add one with New or From selected flow.',
      actionLabel: 'New identity', onAction: () => openEdit({ mode: 'create', opener: $('#identNew') }),
    });
    return;
  }
  const shown = M.filterIdentities(S.ids, S.filter);
  if (!shown.length) {
    renderState(st, 'empty-filtered', { filterCount: 1, onClear: () => { $('#identFilter').value = ''; S.filter = ''; renderList(); $('#identFilter').focus(); } });
    return;
  }
  const now = Date.now();
  shown.forEach((i) => {
    const sum = M.authSummary(i.headers);
    const status = M.identityStatus(i, { jwtExp: sum.jwtExp, now });
    const row = el('div', 'ident-row');
    row.dataset.name = i.name;
    row.id = 'identRow-' + S.ids.indexOf(i);
    const dot = el('span', 'ident-dot');
    dot.dataset.status = status;
    dot.setAttribute('aria-hidden', 'true');
    const main = el('div', 'ident-row-main');
    const top = el('div', 'ident-row-top');
    top.append(el('span', 'ident-row-name', i.name || '(unnamed)'));
    if (i.role) top.append(el('span', 'ident-row-role', i.role));
    const sub = el('div', 'ident-row-sub');
    const auth = el('span', 'ident-row-auth', M.authLine(sum, now));
    if (sum.jwtExp) { auth.dataset.exp = String(sum.jwtExp); auth.dataset.type = sum.type; }
    const b = M.sourceBadge(i);
    const src = el('span', 'ident-chip ident-source', b.label);
    src.dataset.kind = b.kind;
    sub.append(el('span', 'ident-row-status', M.statusLabel(status)), auth, src);
    main.append(top, sub);
    row.append(dot, main);
    row.setAttribute('aria-label', [i.name, i.role, M.statusLabel(status), M.authLine(sum, now), 'created by ' + b.label].filter(Boolean).join(', '));
    list.append(row);
  });
  wireListbox(list, list.querySelectorAll('.ident-row'), {
    label: 'Identities',
    selected: (r) => r.dataset.name === S.sel,
    activate: (r) => { select(r.dataset.name); if (pane && pane.mode() === 'stack') pane.showDetail(r); },
  });
}

function select(name, { tab = '' } = {}) {
  if (!name || !byName(name)) return;
  const changed = S.sel !== name;
  S.sel = name;
  if (tab) S.tab = tab;
  if (changed) { S.detail = null; S.detailError = null; S.detailFor = ''; S.loginTimeline = null; S.loginTimelineFor = 0; }
  setListboxSelection(document.querySelectorAll('#identList .ident-row'), (r) => r.dataset.name === name);
  renderDetail();
  if (changed || !S.detail) loadDetail(name);
  if (S.tab === 'access' && !S.runs) loadRuns();
}

/* ------------------------------------------------------------------ countdowns */

// One quiet ticker while the panel is visible: countdown texts only, no re-render.
let ticker = 0;
function tick() {
  // Stop while the panel is hidden; rendering the panel starts it again.
  if (!panelActive()) { clearInterval(ticker); ticker = 0; return; }
  const now = Date.now();
  document.querySelectorAll('#panel-identities [data-exp]').forEach((n) => {
    const exp = Number(n.dataset.exp);
    if (n.classList.contains('ident-row-auth')) n.textContent = M.authLine({ type: n.dataset.type, jwtExp: exp }, now);
    else if (n.classList.contains('ident-countdown')) { const left = M.formatCountdown(exp, now); n.textContent = ' · ' + (left === 'expired' ? 'expired' : 'expires in ' + left); }
  });
}
function startTicker() { if (!ticker) ticker = setInterval(tick, 15000); }

/* ------------------------------------------------------------------ toolbar actions */

async function fromSelectedFlow() {
  const fid = selectedFlowId();
  if (!fid) { toast('Select a flow with Cookie or Authorization headers in History first'); return; }
  const name = ((await uiPrompt({ title: 'Identity from flow #' + fid, placeholder: 'Name, e.g. admin or user-b', value: 'from flow #' + fid })) || '').trim();
  if (!name) return;
  if (byName(name)) {
    const ok = await uiConfirm('Replace identity', 'An identity named <b>' + esc(name) + '</b> exists. Replace its headers with the ones captured in flow #' + esc(String(fid)) + '?', 'Replace', 'btn danger');
    if (!ok) return;
  }
  setStatus('Promoting flow #' + fid + '…');
  try {
    await jsend('POST', '/api/authz/from-flow/' + enc(String(fid)), { name, merge: true });
    S.sel = name;
    setStatus('Created ' + name + ' from flow #' + fid);
    await loadIdentities();
  } catch (e) { setStatus('Could not promote the flow: ' + e.message, 'error'); toastError('Could not promote the flow', e); }
}

function runAccessTest() {
  const fid = selectedFlowId();
  openAuthz(fid || null);
}

function go() {
  const shell = getShellApi();
  if (shell.activateTab) shell.activateTab('identities'); else document.querySelector('.tab[data-tab="identities"]')?.click();
}

// openIdentities is the bus entry (authz modal, palette): show the panel, then
// the named identity and/or the saved run.
async function openIdentities(p = {}) {
  go();
  if (!S.loaded) await loadIdentities();
  if (p.name && byName(p.name)) select(p.name);
  if (p.runId) {
    S.tab = 'access';
    await loadRuns();
    const run = (S.runs || []).find((r) => String(r.id) === String(p.runId));
    // Show the run under an identity it includes (the matrix needs a detail
    // pane); fall back to the All runs view.
    const member = run && (run.identities || []).find((n) => byName(n));
    if (!p.name && member && !(run.identities || []).includes(S.sel)) select(member, { tab: 'access' });
    if (run && !(run.identities || []).includes(S.sel)) S.runsAll = true;
    renderDetail();
    selectRun(String(p.runId));
  }
}

/* ------------------------------------------------------------------ live updates */

let sseTimer = 0;
let pending = { ids: false, runs: false };
function onAuthzUpdate(m) {
  if (m && m.what === 'runs') pending.runs = true; else pending.ids = true;
  clearTimeout(sseTimer);
  sseTimer = setTimeout(() => {
    const p = pending;
    pending = { ids: false, runs: false };
    if (p.ids) { if (panelActive() || S.edit) loadIdentities(); else S.loaded = false; }
    if (p.runs) { if (panelActive() && S.tab === 'access') loadRuns(); else S.runs = null; }
  }, 250);
}

/* ------------------------------------------------------------------ wiring */

function onShow() {
  startTicker();
  loadIdentities();
  if (S.tab === 'access') loadRuns();
}

function registerCommands() {
  registerCommand({ t: 'Open Identities', kw: 'identities authz roles sessions tokens jwt access control agents', run: go });
  registerCommand({ t: 'New identity', kw: 'identity create role cookie bearer authz', run: () => { go(); setTimeout(() => openEdit({ mode: 'create', opener: $('#identNew') }), 50); } });
  registerCommand({ t: 'Identity from selected flow', kw: 'identity promote flow cookie authorization login authz', run: () => { go(); fromSelectedFlow(); } });
}

function registerKeys() {
  const get = getHook('keyRegistry');
  const reg = get && get();
  if (!reg) return;
  try { reg.register({ id: 'identities.filter', keys: '/', label: 'Filter identities', scope: 'identities', group: 'Identities', run: () => $('#identFilter')?.focus() }); } catch (e) { /* already registered */ }
}

function wire() {
  $('#identNew').addEventListener('click', (e) => openEdit({ mode: 'create', opener: e.currentTarget }));
  $('#identFromFlow').addEventListener('click', fromSelectedFlow);
  $('#identRunTest').addEventListener('click', runAccessTest);
  $('#identFilter').addEventListener('input', (e) => { S.filter = e.target.value; renderList(); });
  $('#identFilter').addEventListener('keydown', (e) => {
    if (e.key === 'Escape' && e.target.value) { e.target.value = ''; S.filter = ''; renderList(); }
    else if (e.key === 'ArrowDown') { e.preventDefault(); $('#identList .ident-row')?.focus(); }
  });
  try {
    pane = createSplitPane({ root: $('#identRoot'), key: 'identities.split', orientation: 'right', min: [220, 360], default: 30, stackBelow: 720, scopeKey: projectStorageKey, label: 'Resize identity list', backLabel: 'Identities' });
  } catch (e) { /* the plain layout still works without the split */ }
  document.addEventListener(TAB_CHANGE_EVENT, (e) => { if (e.detail && e.detail.tab === 'identities') onShow(); });
  registerSseHandler('authz.update', onAuthzUpdate);
  busOn(EVENTS.IDENTITIES_OPEN, (p) => { openIdentities(p || {}).catch((e) => toastError('Could not open Identities', e)); });
  registerCommands();
  registerKeys();
}

function boot() {
  if (!$('#panel-identities')) return;
  X.loadIdentities = loadIdentities;
  X.loadDetail = loadDetail;
  X.loadRuns = loadRuns;
  X.renderList = renderList;
  X.renderDetail = renderDetail;
  X.select = select;
  X.openEdit = openEdit;
  X.setTab = setTab;
  wire();
  if (panelActive()) onShow();
}
boot();

export { S as identitiesState, loadIdentities };
