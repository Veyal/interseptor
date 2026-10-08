// collections.js — the Collections panel entry: loading, collection picker, wiring,
// command palette entries and keyboard shortcuts. A Postman-style workspace in
// Interseptor's console styling; resolution, scope policy and script trust are
// decided by the server (internal/collexec, internal/control/collections*.go).
// Pure shaping lives in collections-model.js, varscope-model.js and scriptedit.js
// (tested under node). Server strings are only ever inserted with textContent or esc().
import { $, api, toast, toastError, getHook, projectStorageKey, uiPrompt, uiConfirm } from './core.js';
import { registerCommand, TAB_CHANGE_EVENT, getShellApi } from './shell-hooks.js';
import { renderState } from './statepanel.js';
import { createSplitPane } from './split.js';
import * as M from './collections-model.js';
import { S, X, btn, itemByUid, jget, jsend, lsGet, lsSet, panelActive } from './collections-core.js';
import { openItem, renderEditor, renderEmptyEditor, scheduleResolve } from './collections-editor.js';
import { expandTo, refreshScope, renderEnvSelect, renderScriptsChip, selectedFolder, setPhone, updateBadge } from './collections-env.js';
import { sendCurrent, setConsole } from './collections-response.js';
import { openEnvSheet, openImportSheet, openScriptsSheet, runCollection } from './collections-sheets.js';
import { addItem, nudge, renderTree } from './collections-tree.js';

/* ------------------------------------------------------------------ loading */

async function loadAll({ keepSelection = true } = {}) {
  const gen = ++S.loadGen;
  const treeState = $('#collTreeState');
  if (!S.collections.length) renderState(treeState, 'loading', { title: 'Loading collections', rows: 5 });
  try {
    const [cols, envs] = await Promise.all([jget('/api/collections'), jget('/api/environments')]);
    if (gen !== S.loadGen) return;
    S.collections = cols.collections || [];
    S.envs = envs.environments || [];
    renderPicker();
    renderEnvSelect();
    if (!S.collections.length) {
      S.colUid = '';
      S.collection = null;
      S.items = [];
      renderTree();
      renderEmptyEditor();
      return;
    }
    const saved = lsGet('collection');
    const want = (keepSelection && S.colUid) || saved;
    const target = S.collections.find((c) => c.uid === want) || S.collections[0];
    await loadCollection(target.uid, { keepSelection });
  } catch (e) {
    if (gen !== S.loadGen) return;
    renderState(treeState, 'error', { title: 'Could not load collections', message: e.message, status: e.status, onRetry: () => loadAll() });
    $('#collTree').textContent = '';
  }
}

function firstRequestInTreeOrder(nodes) {
  for (const n of nodes) {
    if (n.item.kind === 'request') return n.item;
    const r = firstRequestInTreeOrder(n.children);
    if (r) return r;
  }
  return null;
}

async function loadCollection(uid, { keepSelection = true } = {}) {
  const gen = ++S.loadGen;
  S.colUid = uid;
  lsSet('collection', uid);
  try {
    const [tree, scripts] = await Promise.all([
      jget('/api/collections/' + encodeURIComponent(uid)),
      jget('/api/collections/' + encodeURIComponent(uid) + '/scripts').catch(() => null),
    ]);
    if (gen !== S.loadGen) return;
    S.collection = tree.collection;
    S.items = tree.items || [];
    S.scripts = scripts;
    S.tree = M.buildTree(S.items);
    $('#collPicker').value = uid;
    renderEnvSelect();
    renderTree();
    renderScriptsChip();
    await refreshScope();
    const keep = keepSelection && itemByUid(S.selUid);
    if (keep) { if (!S.ed || S.ed.uid !== S.selUid) await openItem(S.selUid, { focusTree: false }); else renderEditor(); }
    else {
      S.selUid = '';
      const firstReq = firstRequestInTreeOrder(S.tree);
      if (firstReq) { expandTo(firstReq.uid); renderTree(); await openItem(firstReq.uid, { focusTree: false }); } else renderEmptyEditor();
    }
    updateBadge();
  } catch (e) {
    if (gen !== S.loadGen) return;
    renderState($('#collTreeState'), 'error', { title: 'Could not load this collection', message: e.message, status: e.status, onRetry: () => loadCollection(uid) });
  }
}

function renderPicker() {
  const sel = $('#collPicker');
  sel.textContent = '';
  if (!S.collections.length) sel.append(new Option('No collections', ''));
  S.collections.forEach((c) => sel.append(new Option(c.name || 'Untitled', c.uid)));
  sel.value = S.colUid;
  sel.disabled = !S.collections.length;
}

async function newCollection() {
  const name = await uiPrompt({ title: 'New collection', placeholder: 'Name', value: 'New collection' });
  if (!name) return;
  try {
    const c = await jsend('POST', '/api/collections', { name });
    S.colUid = c.uid;
    await loadAll({ keepSelection: false });
    toast('Collection created');
  } catch (e) { toastError('Could not create the collection', e); }
}

/* ------------------------------------------------------------------ phone */

let pane = null;
function wire() {
  $('#collPicker').addEventListener('change', async (e) => { if (S.dirty && !(await uiConfirm('Discard unsaved changes', 'Switching collections discards unsaved changes.', 'Discard', 'btn danger'))) { e.target.value = S.colUid; return; } S.dirty = false; S.selUid = ''; await loadCollection(e.target.value, { keepSelection: false }); });
  $('#collEnv').addEventListener('change', async (e) => { S.envUid = e.target.value; lsSet('env', S.envUid); await refreshScope(); scheduleResolve(); });
  $('#collEnvEdit').addEventListener('click', () => openEnvSheet());
  $('#collScripts').addEventListener('click', () => openScriptsSheet());
  $('#collNew').addEventListener('click', () => newCollection());
  $('#collImport').addEventListener('click', () => openImportSheet());
  $('#collRun').addEventListener('click', () => runCollection(selectedFolder()));
  $('#collConsoleBtn').addEventListener('click', () => setConsole(!S.consoleOpen));
  $('#collAddRequest').addEventListener('click', () => addItem('request', selectedFolder()));
  $('#collAddFolder').addEventListener('click', () => addItem('folder', selectedFolder()));
  $('#collFilter').addEventListener('input', (e) => { S.filter = e.target.value; renderTree(); });
  $('#collFilter').addEventListener('keydown', (e) => { if (e.key === 'Escape') { e.target.value = ''; S.filter = ''; renderTree(); } else if (e.key === 'ArrowDown') { e.preventDefault(); const first = $('#collTree .coll-row'); first?.focus(); } });
  $('#collPhoneSeg').querySelectorAll('button').forEach((b) => b.addEventListener('click', () => setPhone(b.dataset.v)));
  setPhone('req');
  try {
    pane = createSplitPane({ root: $('#collRoot'), key: 'collections.split', orientation: 'right', min: [220, 360], default: 24, stackBelow: 720, scopeKey: projectStorageKey, label: 'Resize collection tree', backLabel: 'Collection' });
    $('#collTree').addEventListener('click', (e) => { const row = e.target.closest?.('.coll-row'); const it = row && itemByUid(row.dataset.uid); if (it && it.kind === 'request' && pane.mode() === 'stack') pane.showDetail(row); });
  } catch (e) { /* the plain layout still works without the split */ }
  document.addEventListener(TAB_CHANGE_EVENT, (e) => { if (e.detail && e.detail.tab === 'collections') onShow(); });
  window.addEventListener('beforeunload', (e) => { if (S.dirty) { e.preventDefault(); e.returnValue = ''; } });
  registerCommands();
  registerKeys();
}

function registerCommands() {
  M.commandEntries({
    import: () => { go(); openImportSheet(); },
    pasteCurl: () => { go(); openImportSheet(' '); setTimeout(() => $('#collImportText')?.focus(), 200); },
    newCollection: () => { go(); newCollection(); },
    switchEnv: () => { go(); setTimeout(() => $('#collEnv')?.parentElement?.querySelector('button,.ui-select')?.focus(), 100); },
    editEnv: () => { go(); setTimeout(() => openEnvSheet(), 50); },
    send: () => { go(); sendCurrent(); },
    runCollection: () => { go(); setTimeout(() => runCollection(''), 50); },
    reviewScripts: () => { go(); setTimeout(() => openScriptsSheet(), 50); },
  }).forEach((c) => registerCommand(c));
}

function go() {
  const api2 = getShellApi();
  if (api2.activateTab) api2.activateTab('collections'); else document.querySelector('.tab[data-tab="collections"]')?.click();
}

function registerKeys() {
  const get = getHook('keyRegistry');
  const reg = get && get();
  if (!reg) return;
  const defs = [
    { id: 'collections.send', keys: 'Mod+Enter', label: 'Send the open request', run: () => sendCurrent() },
    { id: 'collections.filter', keys: '/', label: 'Filter the collection tree', run: () => $('#collFilter')?.focus() },
    { id: 'collections.up', keys: 'Alt+ArrowUp', label: 'Move the selected item up', run: () => nudge(rowUid(), -1) },
    { id: 'collections.down', keys: 'Alt+ArrowDown', label: 'Move the selected item down', run: () => nudge(rowUid(), 1) },
    { id: 'collections.console', keys: 'Mod+`', label: 'Show or hide the console', run: () => setConsole(!S.consoleOpen) },
  ];
  defs.forEach((d) => { try { reg.register({ ...d, scope: 'collections', group: 'Collections' }); } catch (e) { /* already registered */ } });
}
const rowUid = () => document.activeElement?.dataset?.uid || S.selUid;

function onShow() {
  if (!S.booted) { S.booted = true; loadAll(); } else if (!S.dirty) loadAll();
}

function boot() {
  if (!$('#panel-collections')) return;
  X.loadAll = loadAll;
  X.loadCollection = loadCollection;
  wire();
  if (panelActive()) onShow();
}
boot();

export { S as collectionsState, loadAll as loadCollections };

