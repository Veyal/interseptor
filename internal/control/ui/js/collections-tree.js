// collections-tree.js — the collection tree: rows, drag reorder, keyboard, context menu and item mutations.
import { $, esc, api, toast, toastError, icon, openCtxMenu, uiPrompt, uiConfirm } from './core.js';
import { renderState } from './statepanel.js';
import * as M from './collections-model.js';
import * as V from './varscope-model.js';
import { S, X, announce, btn, el, itemByUid, jsend, setStatus } from './collections-core.js';
import { deleteItemUndoable, focusEditorUrl, openItem } from './collections-editor.js';
import { expandTo } from './collections-env.js';
import { sendCurrent } from './collections-response.js';
import { openImportSheet, runCollection } from './collections-sheets.js';

// The host shared by most requests is not repeated on every row. Computed once
// per render, memoised on the items array so a re-render without edits is free.
let domHost = '';
let domFor = null;
let domVal = '';
function dominantHostOf(items) {
  if (domFor !== items) { domFor = items; domVal = M.dominantHost(items); }
  return domVal;
}

// refreshScope() in collections-env.js replaces S.scope after the tree first
// painted (environment switch, collection load). Rows that flag unresolved
// variables must follow, so refreshScope calls this once it has the new scope.
export function repaintForScope() {
  if (S.collection) repaintKeepingFocus();
}
function repaintKeepingFocus() {
  const host = $('#collTree');
  if (!host) return;
  const focus = host.contains(document.activeElement) ? document.activeElement.dataset.uid : '';
  const top = host.scrollTop;
  renderTree();
  host.scrollTop = top;
  if (focus) focusRow(focus);
}

export function renderTree() {
  const host = $('#collTree');
  const state = $('#collTreeState');
  host.textContent = '';
  if (!S.collection) {
    renderState(state, 'empty-first', {
      title: 'No collections yet', hint: 'Import a Postman collection, an OpenAPI file or a curl command, or start an empty collection.',
      actionLabel: 'Import a file', onAction: () => openImportSheet(),
    });
    return;
  }
  const filtered = M.filterTree(S.tree, S.filter);
  const rows = M.flattenVisible(filtered, S.expanded, { forceOpen: !!S.filter.trim() });
  if (!rows.length) {
    if (S.filter.trim()) renderState(state, 'empty-filtered', { filterCount: 1, title: 'No requests match "' + S.filter.trim() + '"', onClear: () => { $('#collFilter').value = ''; S.filter = ''; renderTree(); } });
    else renderState(state, 'empty-first', { title: 'This collection is empty', hint: 'Add a request or a folder, or import a file.', actionLabel: 'New request', onAction: () => addItem('request', '') });
    return;
  }
  state.textContent = '';
  state.removeAttribute('data-state');
  S.treeRows = rows;
  domHost = dominantHostOf(S.items);
  const frag = document.createDocumentFragment();
  rows.forEach((r, i) => frag.append(treeRow(r, i === 0)));
  host.append(frag);
  const sel = host.querySelector('[aria-selected="true"]');
  if (sel) { host.querySelectorAll('.coll-row').forEach((x) => { x.tabIndex = -1; }); sel.tabIndex = 0; }
}

export function highlightMatch(span, text) {
  const q = S.filter.trim();
  const at = q ? text.toLowerCase().indexOf(q.toLowerCase()) : -1;
  if (at < 0) { span.textContent = text; return; }
  span.append(document.createTextNode(text.slice(0, at)));
  const m = el('mark', 'coll-match', text.slice(at, at + q.length));
  span.append(m, document.createTextNode(text.slice(at + q.length)));
}

const NONE = Object.freeze([]);

// fillPath writes path text into a span: plain text with the search highlight,
// or, when it holds {{variables}}, tokens styled like the URL editor (.coll-vt).
function fillPath(span, text) {
  if (!text.includes('{{')) { highlightMatch(span, text); return; }
  for (const t of V.annotate(text, S.scope)) {
    if (t.kind === 'var') {
      const v = el('span', 'coll-vt', t.text);
      v.dataset.s = t.status;
      span.append(v);
    } else if (S.filter.trim()) {
      const p = el('span');
      highlightMatch(p, t.text);
      span.append(p);
    } else span.append(document.createTextNode(t.text));
  }
}

// pathLine is the muted second line: optional host, then the path. The head of
// the path ellipsizes, the tail never does, so /users/1 and /users/1/roles stay
// distinguishable.
function pathLine(line) {
  const p = el('span', 'coll-path');
  if (line.showHost) {
    const h = el('span', 'coll-host');
    fillPath(h, line.host);
    p.append(h);
  }
  if (line.head) { const h = el('span', 'coll-head'); fillPath(h, line.head); p.append(h); }
  const t = el('span', 'coll-tail');
  fillPath(t, line.tail);
  p.append(t);
  return p;
}

export function treeRow(r, first) {
  const it = r.item;
  const row = el('div', 'coll-row');
  row.dataset.uid = it.uid;
  row.setAttribute('role', 'treeitem');
  row.setAttribute('aria-level', String(r.depth + 1));
  row.setAttribute('aria-posinset', String(r.posinset));
  row.setAttribute('aria-setsize', String(r.setsize));
  row.setAttribute('aria-selected', it.uid === S.selUid ? 'true' : 'false');
  if (r.folder) row.setAttribute('aria-expanded', r.open ? 'true' : 'false');
  row.dataset.depth = String(Math.min(r.depth, 8));
  row.tabIndex = first ? 0 : -1;
  row.draggable = !S.filter.trim();
  if (r.folder) {
    const caret = el('span', 'coll-caret');
    caret.innerHTML = icon('chevron');
    row.append(caret);
    const f = el('span', 'coll-fold');
    f.innerHTML = icon('folder');
    row.append(f);
  } else {
    row.classList.add('coll-req');
    const m = el('span', 'coll-method', (it.method || 'GET').slice(0, 7));
    m.dataset.m = (it.method || 'GET').toUpperCase();
    row.append(m);
  }
  const name = el('span', 'coll-name');
  highlightMatch(name, it.name || (r.folder ? 'Untitled folder' : 'Untitled request'));
  const quarantined = !r.folder && itemHasUntrustedScript(it);
  if (r.folder) {
    row.append(name, el('span', 'coll-count', String(r.count)));
  } else {
    const raw = M.itemUrlRaw(it);
    const names = raw.includes('{{') ? V.unresolvedIn(raw, S.scope) : NONE;
    const line = M.rowLine(it, domHost);
    // An unresolved base such as {{baseUrl}} is shown even when it is the shared host.
    if (names.length && line.host.includes('{{') && V.unresolvedIn(line.host, S.scope).length) line.showHost = true;
    const body = el('span', 'coll-body');
    const top = el('span', 'coll-top');
    top.append(name);
    if (names.length) {
      const u = el('span', 'coll-unset', 'unset');
      u.title = 'Unresolved: ' + names.map((n) => '{{' + n + '}}').join(', ');
      top.append(u);
      row.dataset.unresolved = '1';
    }
    body.append(top, pathLine(line));
    row.append(body);
    row.setAttribute('aria-label', M.rowLabel(it, line, names, quarantined));
    if (raw) row.title = raw;
  }
  if (quarantined) {
    const fl = el('span', 'coll-flag');
    fl.innerHTML = icon('lock', 'Scripts are quarantined');
    row.append(fl);
  }
  row.addEventListener('click', () => { if (r.folder) toggleFolder(it.uid); selectRow(it.uid, !r.folder); });
  row.addEventListener('keydown', (e) => treeKeydown(e, it.uid));
  row.addEventListener('contextmenu', (e) => { e.preventDefault(); selectRow(it.uid, false); openItemMenu(it, e.clientX, e.clientY, row); });
  row.addEventListener('dragstart', (e) => { S.drag = it.uid; row.classList.add('is-dragging'); e.dataTransfer.effectAllowed = 'move'; try { e.dataTransfer.setData('text/plain', it.uid); } catch (err) { /* ignore */ } });
  row.addEventListener('dragend', () => { S.drag = ''; clearDropMarks(); row.classList.remove('is-dragging'); });
  row.addEventListener('dragover', (e) => { if (!S.drag) return; const where = dropWhere(e, row, r.folder); if (!M.planMove(S.items, S.drag, it.uid, where)) return; e.preventDefault(); clearDropMarks(); row.classList.add('is-drop-' + where); });
  row.addEventListener('dragleave', () => row.classList.remove('is-drop-before', 'is-drop-after', 'is-drop-into'));
  row.addEventListener('drop', (e) => { e.preventDefault(); const uid = S.drag; S.drag = ''; const where = dropWhere(e, row, r.folder); clearDropMarks(); if (uid) moveItem(uid, it.uid, where); });
  return row;
}

export function itemHasUntrustedScript(it) {
  if (!S.scripts || !S.scripts.scripts) return false;
  return S.scripts.scripts.some((s) => !s.trusted && (s.owners || []).includes(it.uid));
}

export function dropWhere(e, row, folder) {
  const b = row.getBoundingClientRect();
  const y = (e.clientY - b.top) / Math.max(1, b.height);
  if (folder && y > 0.25 && y < 0.75) return 'into';
  return y < 0.5 ? 'before' : 'after';
}
export function clearDropMarks() { document.querySelectorAll('.coll-row.is-drop-before,.coll-row.is-drop-after,.coll-row.is-drop-into').forEach((r) => r.classList.remove('is-drop-before', 'is-drop-after', 'is-drop-into')); }

export function toggleFolder(uid) {
  if (S.expanded.has(uid)) S.expanded.delete(uid); else S.expanded.add(uid);
  const focus = document.activeElement?.dataset?.uid;
  renderTree();
  if (focus) focusRow(focus);
}
export function focusRow(uid) {
  const row = $('#collTree').querySelector('[data-uid="' + CSS.escape(uid) + '"]');
  if (!row) return;
  $('#collTree').querySelectorAll('.coll-row').forEach((x) => { x.tabIndex = -1; });
  row.tabIndex = 0;
  row.focus({ preventScroll: false });
}
export function selectRow(uid, open) {
  S.selUid = uid;
  $('#collTree').querySelectorAll('.coll-row').forEach((r) => r.setAttribute('aria-selected', r.dataset.uid === uid ? 'true' : 'false'));
  if (open) openItem(uid, { focusTree: false });
}

export function treeKeydown(e, uid) {
  if (e.altKey && (e.key === 'ArrowUp' || e.key === 'ArrowDown')) return; // handled by the key registry
  if (e.key === 'Enter' || e.key === ' ') {
    e.preventDefault();
    const it = itemByUid(uid);
    if (it && it.kind === 'folder') toggleFolder(uid);
    selectRow(uid, !!it && it.kind === 'request');
    if (it && it.kind === 'request') focusEditorUrl();
    return;
  }
  if (e.key === 'F10' && e.shiftKey || e.key === 'ContextMenu') {
    e.preventDefault();
    const row = e.currentTarget, b = row.getBoundingClientRect(), it = itemByUid(uid);
    if (it) openItemMenu(it, b.left + 24, b.bottom, row);
    return;
  }
  if (e.key === 'Delete') { e.preventDefault(); const it = itemByUid(uid); if (it) deleteItem(it); return; }
  if (e.key === 'F2') { e.preventDefault(); const it = itemByUid(uid); if (it) renameItem(it); return; }
  const act = M.treeKey(S.treeRows || [], uid, e.key);
  if (!act) return;
  e.preventDefault();
  if (act.focus) focusRow(act.focus);
  else if (act.expand || act.collapse) { const u = act.expand || act.collapse; if (act.expand) S.expanded.add(u); else S.expanded.delete(u); renderTree(); focusRow(uid); }
}

export function openItemMenu(it, x, y, trigger) {
  const folder = it.kind === 'folder';
  const items = [];
  if (!folder) {
    items.push({ label: 'Open', icon: 'search', act: () => selectRow(it.uid, true) });
    items.push({ label: 'Send', icon: 'rocket', act: async () => { await openItem(it.uid, { focusTree: false }); sendCurrent(); } });
  } else {
    items.push({ label: 'New request here', icon: 'plus', act: () => addItem('request', it.uid) });
    items.push({ label: 'New folder here', icon: 'folder', act: () => addItem('folder', it.uid) });
    items.push({ label: 'Run folder', icon: 'rocket', act: () => runCollection(it.uid) });
  }
  const sections = [{ head: folder ? 'FOLDER' : 'REQUEST', items }];
  const more = [{ label: 'Rename', icon: 'edit', act: () => renameItem(it) }];
  if (!folder) more.push({ label: 'Duplicate', icon: 'copy', act: () => duplicateItem(it) });
  more.push({ label: 'Move up', icon: 'arrow-up', act: () => nudge(it.uid, -1) }, { label: 'Move down', icon: 'arrow-down', act: () => nudge(it.uid, 1) });
  more.push({ label: 'Delete', icon: 'trash', danger: true, act: () => deleteItem(it) });
  sections.push({ items: more });
  openCtxMenu(x, y, sections, trigger);
}

/* ------------------------------------------------------------------ tree mutations */

export async function mutate(label, fn) {
  try { await fn(); } catch (e) {
    if (e.status === 409) setStatus('warn', 'Someone changed this item. Reloaded the latest version.', []);
    toastError(label, e);
    await X.loadCollection(S.colUid);
  }
}

export async function addItem(kind, parentUid) {
  if (!S.colUid) { toast('Create or import a collection first'); return; }
  const name = await uiPrompt({ title: kind === 'folder' ? 'New folder' : 'New request', placeholder: 'Name', value: kind === 'folder' ? 'New folder' : 'New request' });
  if (!name) return;
  await mutate('Could not create the ' + kind, async () => {
    const sibs = S.items.filter((i) => (i.parentUid || '') === (parentUid || ''));
    const last = sibs.reduce((m, i) => (i.rank > m ? i.rank : m), '');
    const body = kind === 'folder' ? { kind: 'folder', name, parentUid: parentUid || '' } : { ...M.newRequestItem(S.colUid, parentUid, name) };
    body.rank = M.rankBetween(last, '');
    const created = await jsend('POST', '/api/collections/' + encodeURIComponent(S.colUid) + '/items', body);
    if (parentUid) S.expanded.add(parentUid);
    await X.loadCollection(S.colUid);
    if (created && created.uid && kind === 'request') { expandTo(created.uid); renderTree(); await openItem(created.uid, { focusTree: false }); focusEditorUrl(); }
  });
}

export async function renameItem(it) {
  const name = await uiPrompt({ title: 'Rename', value: it.name, placeholder: 'Name' });
  if (!name || name === it.name) return;
  await mutate('Could not rename', async () => {
    await jsend('PUT', '/api/items/' + encodeURIComponent(it.uid), { ...it, name });
    await X.loadCollection(S.colUid);
  });
}

export async function duplicateItem(it) {
  await mutate('Could not duplicate', async () => {
    const copy = await jsend('POST', '/api/items/' + encodeURIComponent(it.uid) + '/duplicate');
    await X.loadCollection(S.colUid);
    if (copy && copy.uid) { expandTo(copy.uid); renderTree(); await openItem(copy.uid, { focusTree: false }); }
  });
}

export async function deleteItem(it) {
  const kids = it.kind === 'folder' ? M.descendantUids(S.items, it.uid).size : 0;
  const ok = await uiConfirm('Delete ' + (it.kind === 'folder' ? 'folder' : 'request'),
    '<b>' + esc(it.name || 'Untitled') + '</b>' + (kids ? ' and its ' + kids + ' item' + (kids === 1 ? '' : 's') : '')
      + ' will be removed. Captured flows stay in History. You can Undo for 15 seconds; after that it cannot be recovered.', 'Delete', 'btn danger');
  if (!ok) return;
  await mutate('Could not delete', async () => {
    await deleteItemUndoable(it);
    if (S.selUid === it.uid || (S.ed && kids && M.descendantUids(S.items, it.uid).has(S.ed.uid))) { S.selUid = ''; S.ed = null; }
    await X.loadCollection(S.colUid, { keepSelection: false });
  });
}

export async function moveItem(uid, targetUid, where) {
  const plan = M.planMove(S.items, uid, targetUid, where);
  const it = itemByUid(uid);
  if (!plan || !it) return;
  await mutate('Could not move', async () => {
    await jsend('PUT', '/api/items/' + encodeURIComponent(uid), { ...it, parentUid: plan.parentUid, rank: plan.rank });
    if (plan.parentUid) S.expanded.add(plan.parentUid);
    await X.loadCollection(S.colUid);
    focusRow(uid);
  });
}

export async function nudge(uid, dir) {
  const plan = M.planNudge(S.items, uid, dir);
  const it = itemByUid(uid);
  if (!plan || !it) return;
  await mutate('Could not move', async () => {
    await jsend('PUT', '/api/items/' + encodeURIComponent(uid), { ...it, parentUid: plan.parentUid, rank: plan.rank });
    await X.loadCollection(S.colUid);
    focusRow(uid);
    announce('Moved ' + (dir < 0 ? 'up' : 'down'));
  });
}

