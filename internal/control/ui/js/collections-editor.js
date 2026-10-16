// collections-editor.js — the request editor: URL bar, Params/Headers/Body/Auth/Pre-request/Tests/Settings panes, resolve preview and save.
import { $, api, toast, toastError, icon, uiConfirm } from './core.js';
import { renderState } from './statepanel.js';
import { createScriptEditor, SNIPPETS } from './scriptedit.js';
import * as M from './collections-model.js';
import * as V from './varscope-model.js';
import { S, X, btn, el, itemByUid, jget, jsend, setStatus } from './collections-core.js';
import * as SF from './collections-safety.js';
import { paintEnvDot, renderScriptsChip, updateBadge } from './collections-env.js';
import { copyCurl, openInRepeater, renderResponse, sendCurrent } from './collections-response.js';
import { openImportSheet, openScriptsSheet, runCollection } from './collections-sheets.js';
import { addItem, focusRow, renameItem, renderTree, selectRow } from './collections-tree.js';

export function renderEmptyEditor() {
  S.ed = null;
  S.edBase = null;
  const host = $('#collEditor');
  renderState(host, S.collection ? 'empty-first' : 'empty-first', S.collection
    ? { title: 'Select a request', hint: 'Choose a request in the tree, or add one.', actionLabel: 'New request', onAction: () => addItem('request', '') }
    : { title: 'No collection selected', hint: 'Import a file or create a collection to start.', actionLabel: 'Import a file', onAction: () => openImportSheet() });
  renderResponse();
}

export async function openItem(uid, { focusTree = true } = {}) {
  if (S.dirty && S.ed && S.ed.uid !== uid) {
    const ok = await uiConfirm('Discard unsaved changes', 'The open request has unsaved changes. Discard them and open another request?', 'Discard', 'btn danger');
    if (!ok) { selectRow(S.ed.uid, false); return; }
  }
  const gen = ++S.itemGen;
  S.selUid = uid;
  const it = itemByUid(uid);
  if (!it) return;
  let full = it;
  if (it.kind === 'request') {
    try { full = await jget('/api/items/' + encodeURIComponent(uid)); } catch (e) { toastError('Could not open the request', e); return; }
  }
  if (gen !== S.itemGen) return;
  S.edBase = full;
  if (full.kind === 'folder') { renderFolderEditor(full); return; }
  S.ed = M.itemToEditor(full);
  S.edSig = M.editorSignature(S.ed);
  S.dirty = false;
  S.step = null;
  S.flow = null;
  S.scriptEd = {};
  renderEditor();
  renderResponse();
  $('#collTree').querySelectorAll('.coll-row').forEach((r) => r.setAttribute('aria-selected', r.dataset.uid === uid ? 'true' : 'false'));
  scheduleResolve();
  if (focusTree) focusRow(uid);
}

export function renderFolderEditor(folder) {
  S.ed = null;
  const host = $('#collEditor');
  host.textContent = '';
  const pane = el('div', 'coll-pane');
  pane.append(el('h3', '', folder.name || 'Untitled folder'));
  pane.append(el('p', 'coll-note', 'A folder groups requests. Scripts, auth and variables set on a folder apply to every request below it.'));
  const row = el('div', 'coll-actions');
  row.append(btn('Run folder', () => runCollection(folder.uid), 'btn accent', 'rocket'), btn('New request', () => addItem('request', folder.uid), 'btn', 'plus'), btn('Rename', () => renameItem(folder), 'btn', 'edit'));
  pane.append(row);
  host.append(pane);
  renderResponse();
}

function folderPath(uid) {
  const parts = [];
  let it = itemByUid(uid);
  while (it && it.parentUid) { it = itemByUid(it.parentUid); if (it) parts.unshift(it.name || 'Untitled folder'); }
  return parts.join(' / ');
}

export function markDirty() {
  if (!S.ed) return;
  S.dirty = M.editorSignature(S.ed) !== S.edSig;
  const d = $('#collDirty');
  if (d) d.textContent = S.dirty ? 'Unsaved changes: differs from the stored request' : 'Matches the stored request';
  const save = $('#collSave');
  if (save) save.disabled = !S.dirty || S.saving;
  const rev = $('#collRevert');
  if (rev) rev.hidden = !S.dirty;
}

export function focusEditorUrl() { requestAnimationFrame(() => $('#collUrl')?.focus()); }

export function renderEditor() {
  const host = $('#collEditor');
  if (!S.ed) { renderEmptyEditor(); return; }
  const ed = S.ed;
  host.textContent = '';

  const title = el('div', 'coll-title');
  const crumb = folderPath(ed.uid);
  if (crumb) title.append(el('span', 'coll-crumb', crumb + ' /'));
  title.append(el('strong', '', ed.name || 'Untitled request'));
  host.append(title);

  const bar = el('div', 'coll-urlbar');
  const method = document.createElement('select');
  method.id = 'collMethod';
  method.className = 'btn btn-field';
  method.setAttribute('aria-label', 'HTTP method');
  M.METHODS.concat(M.METHODS.includes(ed.method) ? [] : [ed.method]).forEach((m) => method.append(new Option(m, m)));
  method.value = ed.method;
  method.addEventListener('change', () => { ed.method = method.value; markDirty(); });
  const wrap = el('div', 'coll-urlwrap');
  const hl = el('div', 'coll-hl');
  hl.setAttribute('aria-hidden', 'true');
  const url = document.createElement('input');
  url.type = 'text';
  url.id = 'collUrl';
  url.className = 'btn btn-field coll-url';
  url.setAttribute('aria-label', 'Request URL');
  url.placeholder = '{{baseUrl}}/path';
  url.spellcheck = false;
  url.autocomplete = 'off';
  url.value = ed.url;
  url.addEventListener('input', () => {
    ed.url = url.value;
    ed.params = M.syncParamsFromURL(ed.url, ed.params);
    refreshParamsPane();
    markDirty();
    scheduleResolve();
    paintEnvDot();
  });
  url.addEventListener('keydown', (e) => { if (e.key === 'Enter' && !e.ctrlKey && !e.metaKey) { e.preventDefault(); sendCurrent(); } });
  wrap.append(url);
  const send = btn('Send', () => sendCurrent(), 'btn accent', 'rocket');
  send.id = 'collSend';
  send.title = 'Send (Ctrl+Enter)';
  bar.append(method, wrap, send);
  host.append(bar);

  const resolved = el('div', 'coll-resolved');
  resolved.id = 'collResolved';
  resolved.setAttribute('aria-label', 'Resolved URL');
  host.append(resolved);

  const tabs = el('div', 'coll-tabs');
  tabs.setAttribute('role', 'tablist');
  tabs.setAttribute('aria-label', 'Request sections');
  M.EDITOR_TABS.forEach((t) => {
    const b = el('button', 'coll-tab');
    b.type = 'button';
    b.id = 'collTab-' + t;
    b.setAttribute('role', 'tab');
    b.setAttribute('aria-selected', S.tab === t ? 'true' : 'false');
    b.setAttribute('aria-controls', 'collPane-' + t);
    b.tabIndex = S.tab === t ? 0 : -1;
    b.append(document.createTextNode(M.TAB_LABEL[t]));
    const n = tabCount(t);
    if (n) b.append(el('span', 'coll-n', String(n)));
    b.addEventListener('click', () => switchTab(t));
    b.addEventListener('keydown', (e) => {
      const i = M.EDITOR_TABS.indexOf(t);
      let to = -1;
      if (e.key === 'ArrowRight') to = (i + 1) % M.EDITOR_TABS.length; else if (e.key === 'ArrowLeft') to = (i + M.EDITOR_TABS.length - 1) % M.EDITOR_TABS.length;
      else if (e.key === 'Home') to = 0; else if (e.key === 'End') to = M.EDITOR_TABS.length - 1;
      if (to >= 0) { e.preventDefault(); switchTab(M.EDITOR_TABS[to], true); }
    });
    tabs.append(b);
  });
  host.append(tabs);

  M.EDITOR_TABS.forEach((t) => {
    const pane = el('div', 'coll-pane');
    pane.id = 'collPane-' + t;
    pane.setAttribute('role', 'tabpanel');
    pane.setAttribute('aria-labelledby', 'collTab-' + t);
    pane.hidden = S.tab !== t;
    host.append(pane);
  });
  paintPane(S.tab);

  const actions = el('div', 'coll-actions');
  const save = btn('Save', () => saveCurrent(), 'btn', 'save');
  save.id = 'collSave';
  save.disabled = true;
  const revert = btn('Revert', () => revertEdits(), 'btn', 'refresh');
  revert.id = 'collRevert';
  revert.hidden = true;
  revert.title = 'Discard unsaved edits and go back to the stored request';
  const dirty = el('span', 'coll-dirty');
  dirty.id = 'collDirty';
  dirty.setAttribute('role', 'status');
  const sentOver = el('div', 'coll-sentover');
  sentOver.id = 'collSentOver';
  sentOver.setAttribute('role', 'status');
  sentOver.hidden = true;
  host.append(sentOver);
  actions.append(save, revert, dirty,
    btn('Open in Repeater', () => openInRepeater(), 'btn', 'repeater'),
    btn('Copy as cURL', () => copyCurl(), 'btn', 'copy'));
  host.append(actions);
  markDirty();
  paintSentOver();
  paintEnvDot();
}

export function tabCount(t) {
  const ed = S.ed;
  if (t === 'params') return ed.params.filter((p) => !p.disabled).length;
  if (t === 'headers') return ed.headers.filter((p) => !p.disabled).length;
  if (t === 'body') return ed.body.mode === 'none' ? 0 : 1;
  if (t === 'prerequest') return ed.scripts.prerequest.trim() ? 1 : 0;
  if (t === 'tests') return ed.scripts.tests.trim() ? 1 : 0;
  return 0;
}

export function switchTab(t, focus = false) {
  S.tab = t;
  M.EDITOR_TABS.forEach((x) => {
    const b = $('#collTab-' + x), p = $('#collPane-' + x);
    if (b) { b.setAttribute('aria-selected', x === t ? 'true' : 'false'); b.tabIndex = x === t ? 0 : -1; }
    if (p) p.hidden = x !== t;
  });
  paintPane(t);
  if (focus) $('#collTab-' + t)?.focus();
}

export function refreshTabCounts() {
  M.EDITOR_TABS.forEach((t) => {
    const b = $('#collTab-' + t);
    if (!b) return;
    b.querySelector('.coll-n')?.remove();
    const n = tabCount(t);
    if (n) b.append(el('span', 'coll-n', String(n)));
  });
}

export function refreshParamsPane() { if (S.tab === 'params') paintPane('params'); refreshTabCounts(); }

export function paintPane(t) {
  const pane = $('#collPane-' + t);
  if (!pane || !S.ed) return;
  pane.textContent = '';
  if (t === 'params') paintKV(pane, S.ed.params, 'Query parameters', { onChange: () => { S.ed.url = M.buildURL(M.splitURL(S.ed.url).base, S.ed.params, M.splitURL(S.ed.url).hash); const u = $('#collUrl'); if (u) u.value = S.ed.url; scheduleResolve(); } });
  else if (t === 'headers') paintHeaders(pane);
  else if (t === 'body') paintBody(pane);
  else if (t === 'auth') paintAuth(pane);
  else if (t === 'prerequest' || t === 'tests') paintScript(pane, t);
  else if (t === 'settings') paintSettings(pane);
}

/* key/value grid shared by params, headers, urlencoded and form-data */
export function paintKV(pane, rows, label, { onChange, keyPlaceholder = 'Key', valuePlaceholder = 'Value' } = {}) {
  const note = el('p', 'coll-note', label + '. Variables use {{name}}.');
  const wrap = el('div', 'coll-scroll-x');
  const table = el('table', 'coll-kv');
  table.setAttribute('aria-label', label);
  const head = el('thead');
  head.innerHTML = '<tr><th scope="col"><span class="u-sr">Enabled</span></th><th scope="col">Key</th><th scope="col">Value</th><th scope="col"><span class="u-sr">Remove</span></th></tr>';
  table.append(head);
  const body = el('tbody');
  const changed = () => { markDirty(); refreshTabCounts(); if (onChange) onChange(); };
  const addRow = (r, i) => {
    const tr = el('tr');
    const on = el('td', 'coll-on');
    const cb = document.createElement('input');
    cb.type = 'checkbox';
    cb.checked = !r.disabled;
    cb.setAttribute('aria-label', (label.replace(/s$/, '')) + ' ' + (i + 1) + ' enabled');
    cb.addEventListener('change', () => { r.disabled = !cb.checked; changed(); });
    on.append(cb);
    const mk = (prop, ph, aria) => {
      const td = el('td');
      const inp = document.createElement('input');
      inp.type = 'text';
      inp.className = 'btn btn-field';
      inp.value = r[prop];
      inp.placeholder = ph;
      inp.spellcheck = false;
      inp.autocomplete = 'off';
      inp.setAttribute('aria-label', aria);
      inp.addEventListener('input', () => { r[prop] = inp.value; changed(); });
      td.append(inp);
      return td;
    };
    const del = el('td', 'coll-del');
    const x = btn('', () => { rows.splice(rows.indexOf(r), 1); changed(); paintPane(S.tab); }, 'btn xs');
    x.innerHTML = icon('trash');
    x.setAttribute('aria-label', 'Remove ' + (r.key || 'row ' + (i + 1)));
    del.append(x);
    tr.append(on, mk('key', keyPlaceholder, label + ' key ' + (i + 1)), mk('value', valuePlaceholder, label + ' value ' + (i + 1)), del);
    body.append(tr);
  };
  rows.forEach(addRow);
  table.append(body);
  wrap.append(table);
  const add = btn('Add row', () => { rows.push({ key: '', value: '', description: '', disabled: false }); changed(); paintPane(S.tab); const last = pane.querySelectorAll('tbody tr:last-child input[type="text"]')[0]; last?.focus(); }, 'btn', 'plus');
  pane.append(note, wrap, add);
}

export function paintHeaders(pane) {
  pane.append(el('p', 'coll-note', 'Headers are sent in this order, duplicates allowed, names exactly as typed. Host, Content-Length and a missing User-Agent are added on the wire.'));
  paintKVInto(pane, S.ed.headers, 'Headers');
}
export function paintKVInto(pane, rows, label) { const holder = el('div'); pane.append(holder); paintKV(holder, rows, label); }

export function paintBody(pane) {
  const b = S.ed.body;
  const row = el('div', 'coll-snips');
  const mode = document.createElement('select');
  mode.className = 'btn btn-field';
  mode.setAttribute('aria-label', 'Body type');
  M.BODY_MODES.forEach((m) => mode.append(new Option({ none: 'none', raw: 'raw', urlencoded: 'x-www-form-urlencoded', formdata: 'form-data', graphql: 'GraphQL' }[m], m)));
  mode.value = b.mode;
  mode.addEventListener('change', () => { b.mode = mode.value; markDirty(); refreshTabCounts(); paintPane('body'); });
  row.append(mode);
  if (b.mode === 'raw') {
    const lang = document.createElement('select');
    lang.className = 'btn btn-field';
    lang.setAttribute('aria-label', 'Raw body language');
    M.RAW_LANGS.forEach((l) => lang.append(new Option(l, l)));
    lang.value = M.RAW_LANGS.includes(b.language) ? b.language : 'text';
    lang.addEventListener('change', () => { b.language = lang.value; markDirty(); });
    row.append(lang);
  }
  pane.append(row);
  if (b.mode === 'none') pane.append(el('p', 'coll-note', 'This request has no body.'));
  else if (b.mode === 'raw') {
    const ta = document.createElement('textarea');
    ta.className = 'coll-body-raw';
    ta.setAttribute('aria-label', 'Raw request body');
    ta.spellcheck = false;
    ta.value = b.raw;
    ta.addEventListener('input', () => { b.raw = ta.value; markDirty(); });
    pane.append(ta);
  } else if (b.mode === 'urlencoded') paintKVInto(pane, b.urlencoded, 'Form fields');
  else if (b.mode === 'formdata') {
    pane.append(el('p', 'coll-note', 'File parts are not sent: collections never read local paths. Use text parts.'));
    paintKVInto(pane, b.formdata, 'Form parts');
  } else if (b.mode === 'graphql') {
    const q = document.createElement('textarea');
    q.className = 'coll-body-raw';
    q.setAttribute('aria-label', 'GraphQL query');
    q.value = b.gqlQuery;
    q.addEventListener('input', () => { b.gqlQuery = q.value; markDirty(); });
    const v = document.createElement('textarea');
    v.className = 'coll-body-raw';
    v.setAttribute('aria-label', 'GraphQL variables (JSON)');
    v.value = b.gqlVars;
    v.addEventListener('input', () => { b.gqlVars = v.value; markDirty(); });
    pane.append(el('p', 'coll-note', 'Query'), q, el('p', 'coll-note', 'Variables (JSON)'), v);
  }
}

export function paintAuth(pane) {
  const a = S.ed.auth;
  const row = el('div', 'coll-field-row');
  const lab = el('label', '', 'Auth type');
  lab.htmlFor = 'collAuthType';
  const sel = document.createElement('select');
  sel.id = 'collAuthType';
  sel.className = 'btn btn-field';
  M.AUTH_TYPES.forEach((t) => sel.append(new Option({ inherit: 'Inherit from parent', none: 'No auth', bearer: 'Bearer token', basic: 'Basic auth', apikey: 'API key' }[t], t)));
  if (!M.AUTH_TYPES.includes(a.type)) sel.append(new Option(a.type + ' (imported, read-only)', a.type));
  sel.value = a.type;
  sel.addEventListener('change', () => { a.type = sel.value; markDirty(); paintPane('auth'); });
  row.append(lab, sel);
  pane.append(row);
  if (a.type === 'inherit') pane.append(el('p', 'coll-note', 'The request uses the auth of its folder or collection. Auth is applied after pre-request scripts and variable resolution.'));
  const names = M.AUTH_FIELDS[a.type] || [];
  names.forEach((k) => {
    const r = el('div', 'coll-field-row');
    const l = el('label', '', k === 'in' ? 'Add to (header or query)' : k[0].toUpperCase() + k.slice(1));
    const id = 'collAuth-' + k;
    l.htmlFor = id;
    const inp = document.createElement('input');
    inp.id = id;
    inp.type = k === 'password' || k === 'token' ? 'password' : 'text';
    inp.className = 'btn btn-field';
    inp.autocomplete = 'off';
    inp.spellcheck = false;
    inp.value = a.fields[k] || '';
    inp.addEventListener('input', () => { a.fields[k] = inp.value; markDirty(); });
    r.append(l, inp);
    if (inp.type === 'password') {
      const show = btn('Show', () => { const on = inp.type === 'password'; inp.type = on ? 'text' : 'password'; show.textContent = on ? 'Hide' : 'Show'; }, 'btn xs');
      r.append(show);
    }
    pane.append(r);
  });
  if (names.length) pane.append(el('p', 'coll-note', 'Tip: store secrets in a secret variable and reference it as {{token}}. Values typed here are saved in the project.'));
}

export function paintScript(pane, t) {
  const text = S.ed.scripts[t];
  const trust = trustLineFor(S.ed.uid, t);
  if (trust) pane.append(trust);
  const snips = el('div', 'coll-snips');
  (SNIPPETS[t] || []).forEach((s) => snips.append(btn(s.label, () => S.scriptEd[t] && S.scriptEd[t].insert(s.code), 'btn xs')));
  pane.append(snips);
  const host = el('div');
  pane.append(host);
  const editor = createScriptEditor(host, {
    label: (t === 'tests' ? 'Tests' : 'Pre-request') + ' script', value: text,
    onChange: (v) => { S.ed.scripts[t] = v; markDirty(); refreshTabCounts(); },
  });
  S.scriptEd[t] = editor;
  const notes = serverNotesFor(S.ed.uid, t);
  if (notes.length) editor.setNotes(notes);
  pane.append(el('p', 'coll-note', 'Scripts you write here run in the sandbox with the capabilities you granted. Imported scripts stay quarantined until you approve them in Scripts.'));
}

export function scriptsFor(uid, t) {
  const listen = t === 'tests' ? 'test' : 'prerequest';
  return ((S.scripts && S.scripts.scripts) || []).filter((s) => s.listen === listen && (s.owners || []).includes(uid));
}
export function serverNotesFor(uid, t) {
  const out = [];
  scriptsFor(uid, t).forEach((s) => {
    if (s.status === 'unsupported') out.push({ note: 'The analyser found APIs the sandbox does not ship; affected tests report "unsupported", never a pass.' });
    else if (s.status === 'partial') out.push({ note: 'The analyser found partially supported APIs.' });
    (s.flags || []).forEach((f) => out.push({ note: 'Review flag: ' + f }));
  });
  return out;
}
export function trustLineFor(uid, t) {
  const list = scriptsFor(uid, t);
  if (!list.length) return null;
  const untrusted = list.some((s) => !s.trusted);
  const line = el('div', 'coll-trustline');
  line.dataset.s = untrusted ? 'untrusted' : 'trusted';
  line.innerHTML = icon(untrusted ? 'lock' : 'check');
  line.append(el('span', '', untrusted ? 'Quarantined: this script will not run until you approve it.' : 'Approved: this script runs when the request is sent.'));
  if (untrusted) line.append(btn('Review scripts', () => openScriptsSheet(), 'btn xs'));
  return line;
}

export function paintSettings(pane) {
  const s = S.ed.settings;
  const chain = el('div', 'coll-chain');
  chain.append(el('strong', '', 'Execution chain: '), document.createTextNode('collection pre-request > folder pre-request > request pre-request > send > collection tests > folder tests > request tests'));
  pane.append(chain);
  const tri = (key, label, opts) => {
    const r = el('div', 'coll-field-row');
    const l = el('label', '', label);
    l.htmlFor = 'collSet-' + key;
    const sel = document.createElement('select');
    sel.id = 'collSet-' + key;
    sel.className = 'btn btn-field';
    opts.forEach(([v, t]) => sel.append(new Option(t, v)));
    sel.value = s[key];
    sel.addEventListener('change', () => { s[key] = sel.value; markDirty(); });
    r.append(l, sel);
    pane.append(r);
  };
  const inherit = [['', 'Inherit'], ['true', 'On'], ['false', 'Off']];
  tri('followRedirects', 'Follow redirects', inherit);
  tri('verifyTls', 'Verify TLS certificates', inherit);
  tri('useSession', 'Use global session headers', [['', 'Off (reproducible, default)'], ['true', 'On'], ['false', 'Off']]);
  tri('unresolved', 'Unresolved variables', [['', 'Block the send (default)'], ['literal', 'Send {{name}} literally'], ['empty', 'Replace with empty']]);
  const num = (key, label, ph) => {
    const r = el('div', 'coll-field-row');
    const l = el('label', '', label);
    l.htmlFor = 'collSet-' + key;
    const inp = document.createElement('input');
    inp.id = 'collSet-' + key;
    inp.type = 'text';
    inp.inputMode = 'numeric';
    inp.className = 'btn btn-field';
    inp.placeholder = ph;
    inp.value = s[key];
    inp.addEventListener('input', () => { s[key] = inp.value.replace(/[^0-9]/g, ''); markDirty(); });
    r.append(l, inp);
    pane.append(r);
  };
  num('maxRedirects', 'Maximum redirects', 'default');
  num('timeoutMs', 'Timeout (milliseconds)', 'default');
  const d = el('div', 'coll-field-row');
  const dl = el('label', '', 'Description');
  dl.htmlFor = 'collDesc';
  const ta = document.createElement('textarea');
  ta.id = 'collDesc';
  ta.className = 'coll-body-raw';
  ta.value = S.ed.description;
  ta.addEventListener('input', () => { S.ed.description = ta.value; markDirty(); });
  d.append(dl, ta);
  pane.append(d);
  const nm = el('div', 'coll-field-row');
  const nl = el('label', '', 'Request name');
  nl.htmlFor = 'collName';
  const ni = document.createElement('input');
  ni.id = 'collName';
  ni.type = 'text';
  ni.className = 'btn btn-field';
  ni.value = S.ed.name;
  ni.addEventListener('input', () => { S.ed.name = ni.value; markDirty(); });
  nm.append(nl, ni);
  pane.prepend(nm);
}

/* ------------------------------------------------------------------ resolve preview */

export function scheduleResolve() {
  clearTimeout(S.resolveTimer);
  S.resolveTimer = setTimeout(runResolve, 250);
}

export async function runResolve() {
  const box = $('#collResolved');
  if (!box || !S.ed) return;
  const gen = ++S.resolveGen;
  const tokens = V.annotate(S.ed.url, S.scope);
  box.textContent = '';
  if (!S.ed.url.trim()) return;
  // Local colouring right away; the server's resolved value (secrets masked) follows.
  const hasVars = tokens.some((t) => t.kind === 'var');
  if (!hasVars) return;
  tokens.forEach((t) => {
    if (t.kind === 'text') box.append(document.createTextNode(t.text));
    else {
      const s = el('span', 'coll-vt', t.text);
      s.dataset.s = t.status;
      s.title = t.status === 'unresolved' ? 'Not defined in the active environment, globals or collection' : t.status === 'empty' ? 'Defined but has no value' : t.status === 'secret' ? 'Secret value (masked)' : t.status === 'dynamic' ? 'Dynamic value generated at send time' : 'Resolved';
      box.append(s);
    }
  });
  try {
    const r = await jsend('POST', '/api/variables/resolve', { template: S.ed.url, itemUid: S.ed.uid, envUid: S.envUid || undefined });
    if (gen !== S.resolveGen || !$('#collResolved')) return;
    box.textContent = '';
    box.append(el('span', '', r.value || ''));
    if ((r.unresolved || []).length) {
      box.append(document.createTextNode('  '));
      const u = el('span', 'coll-unres', 'Unresolved: ' + r.unresolved.join(', '));
      box.append(u);
    }
    paintEnvDot((r.unresolved || []).length);
  } catch (e) { /* keep the local colouring */ }
}

/* ------------------------------------------------------------------ save */

/* ------------------------------------------------------- unsaved / sent-over safety */

// The send pipeline runs the STORED item, so a Send of an edited request saves the edit first. The stored
// version it replaces is kept here (in memory, per request) so the user can always get back to it.
const SENT_OVER = new Map();

export async function saveForSend() {
  if (!S.ed) return false;
  if (!S.dirty) return true;
  const uid = S.ed.uid;
  const stored = S.edBase;
  if (!(await saveCurrent())) return false;
  if (stored && stored.uid === uid) SF.rememberSentOver(SENT_OVER, uid, stored);
  paintSentOver();
  return true;
}

export function paintSentOver() {
  const host = $('#collSentOver');
  if (!host) return;
  host.textContent = '';
  const prev = S.ed ? SF.sentOverFor(SENT_OVER, S.ed.uid) : null;
  host.hidden = !prev;
  if (!prev) return;
  host.append(el('span', '', 'Sent with edits that replaced the stored request. The previous version is kept until you reload.'),
    btn('Restore previous version', () => restoreSentOver(), 'btn xs', 'refresh'),
    btn('Keep these edits', () => { SF.forgetSentOver(SENT_OVER, S.ed.uid); paintSentOver(); }, 'btn xs'));
}

export async function restoreSentOver() {
  if (!S.ed) return;
  const uid = S.ed.uid;
  const prev = SF.sentOverFor(SENT_OVER, uid);
  if (!prev) return;
  if (S.dirty) {
    const ok = await uiConfirm('Restore previous version', 'This replaces the stored request and discards your unsaved edits.', 'Restore', 'btn danger');
    if (!ok) return;
  }
  try {
    const saved = await jsend('PUT', '/api/items/' + encodeURIComponent(uid), { ...prev, rev: S.edBase ? S.edBase.rev : prev.rev });
    const idx = S.items.findIndex((i) => i.uid === uid);
    if (idx >= 0) S.items[idx] = saved;
    SF.forgetSentOver(SENT_OVER, uid);
    S.dirty = false;
    S.tree = M.buildTree(S.items);
    renderTree();
    await openItem(uid, { focusTree: false });
    toast('Previous version restored');
  } catch (e) { toastError('Could not restore the previous version', e); }
}

export async function revertEdits() {
  if (!S.ed || !S.dirty || !S.edBase) return;
  const ok = await uiConfirm('Revert changes', 'Discard your unsaved edits and go back to the stored request?', 'Revert', 'btn danger');
  if (!ok) return;
  S.dirty = false;
  await openItem(S.ed.uid, { focusTree: false });
  toast('Reverted to the stored request');
}

/* ---------------------------------------------------------------- delete with Undo */

let undoBar = null;
let undoTimer = 0;
function clearUndoBar() { clearTimeout(undoTimer); if (undoBar) undoBar.remove(); undoBar = null; }

// itemsUnder is the delete's blast radius: the item plus everything beneath it.
function itemsUnder(it) {
  const ids = new Set([it.uid, ...M.descendantUids(S.items, it.uid)]);
  return S.items.filter((i) => ids.has(i.uid));
}

// deleteIsUndoable lets the confirmation dialog tell the truth before it asks.
// Undo is capped by request count, and that cap used to be evaluated after the
// user had already consented to a dialog promising a 15-second Undo, so
// deleting a folder of 201 requests took consent on a promise it then broke.
export function deleteIsUndoable(it) {
  return SF.undoable(itemsUnder(it));
}

// deleteItemUndoable replaces the tree's bare DELETE call. It captures the full payload first, deletes, then
// offers Undo for UNDO_WINDOW_MS. The buffer lives in memory only: a reload or the window closing ends it.
export async function deleteItemUndoable(it) {
  const colUid = S.colUid;
  const listed = itemsUnder(it);
  const canUndo = SF.undoable(listed);
  let full = null;
  if (canUndo) {
    try { full = await Promise.all(listed.map((i) => (i.kind === 'request' ? jget('/api/items/' + encodeURIComponent(i.uid)) : Promise.resolve(i)))); }
    catch (e) { toastError('Could not delete (the request could not be read for Undo)', e); return; }
  }
  const scripts = S.scripts;
  await jsend('DELETE', '/api/items/' + encodeURIComponent(it.uid));
  clearUndoBar();
  const note = SF.undoMessage(it.name, listed.length - 1);
  if (!full) { toast(note + ' Too many items to offer Undo.', 'warn'); return; }
  const stripped = full.filter((i) => SF.needsScriptStrip(i, scripts)).length;
  const root = full.find((i) => i.uid === it.uid);
  // toast-keep: evictToasts caps ordinary notices at 3 and drops the oldest, so
  // without it three toasts inside the window silently removed the Undo button
  // well before the 15 seconds this bar promises.
  const bar = el('div', 'toast-item info show coll-undo toast-keep');
  bar.setAttribute('role', 'status');
  bar.append(el('span', '', note + ' Undo works for ' + Math.round(SF.UNDO_WINDOW_MS / 1000) + ' seconds and is lost if you reload.'));
  const b = btn('Undo', async () => {
    clearUndoBar();
    try { await restoreDeleted(colUid, full, root, scripts, stripped); } catch (e) { toastError('Could not restore', e); await X.loadCollection(S.colUid); }
  }, 'btn xs');
  bar.append(b);
  const host = $('#toast');
  if (host) { host.append(bar); undoBar = bar; undoTimer = setTimeout(clearUndoBar, SF.UNDO_WINDOW_MS); }
}

async function restoreDeleted(colUid, full, root, scripts, stripped) {
  const map = new Map();
  const parentStillThere = root.parentUid && S.items.some((i) => i.uid === root.parentUid);
  let created = null;
  for (const item of SF.orderForRestore(full, root.uid)) {
    const parent = item.uid === root.uid ? (parentStillThere ? root.parentUid : '') : map.get(item.parentUid);
    const out = await jsend('POST', '/api/collections/' + encodeURIComponent(colUid) + '/items', SF.restoreBody(item, parent, SF.needsScriptStrip(item, scripts)));
    map.set(item.uid, out.uid);
    if (item.uid === root.uid) created = out;
  }
  if (S.colUid === colUid) {
    await X.loadCollection(colUid);
    if (created && created.kind === 'request') await openItem(created.uid, { focusTree: false });
  }
  toast(stripped ? 'Restored. Scripts that were not trusted were left out.' : 'Restored "' + (root.name || 'Untitled') + '"', stripped ? 'warn' : undefined);
}

export async function saveCurrent() {
  if (!S.ed || S.saving || !S.dirty) return true;
  S.saving = true;
  markDirty();
  try {
    const body = M.editorToItem(S.ed, S.edBase);
    const saved = await jsend('PUT', '/api/items/' + encodeURIComponent(S.ed.uid), body);
    const idx = S.items.findIndex((i) => i.uid === saved.uid);
    if (idx >= 0) S.items[idx] = saved;
    S.edBase = saved;
    S.edSig = M.editorSignature(S.ed);
    S.dirty = false;
    S.tree = M.buildTree(S.items);
    renderTree();
    refreshScriptsQuiet();
    toast('Saved');
    return true;
  } catch (e) {
    if (e.status === 409) setStatus('warn', 'This request changed elsewhere. Reload it to continue editing.', [{ label: 'Reload request', run: () => { S.dirty = false; openItem(S.ed.uid, { focusTree: false }); setStatus('', ''); } }]);
    toastError('Could not save', e);
    return false;
  } finally { S.saving = false; markDirty(); }
}

export async function refreshScriptsQuiet() {
  try { S.scripts = await jget('/api/collections/' + encodeURIComponent(S.colUid) + '/scripts'); renderScriptsChip(); updateBadge(); renderTree(); } catch (e) { /* chip keeps its last state */ }
}

