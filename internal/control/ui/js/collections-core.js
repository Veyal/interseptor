// collections-core.js — shared state and tiny helpers for the Collections panel
// modules (tree, editor, response, sheets, env and the collections.js entry).
// Sibling modules never import each other: each registers the functions the
// others call on X (below) and calls them as X.name(), so the static import
// graph stays acyclic (ui_collections_cycle_test.go pins this).
import { api, icon, projectStorageKey, $ } from './core.js';

export const JSON_HDR = { 'content-type': 'application/json' };
export const jget = (path) => api(path);
export const jsend = (method, path, body) => api(path, { method, headers: JSON_HDR, body: body === undefined ? undefined : JSON.stringify(body) });

export const S = {
  booted: false, loadGen: 0, itemGen: 0, sendGen: 0,
  collections: [], envs: [], colUid: '', collection: null, items: [], tree: [],
  selUid: '', expanded: new Set(), filter: '',
  envUid: '', scope: new Map(), scripts: null,
  ed: null, edBase: null, edSig: '', tab: 'params', dirty: false, saving: false,
  step: null, flow: null, resTab: 'body', busy: false, phone: 'req',
  consoleOpen: false, consoleLines: [], run: null, scriptEd: {}, resolveTimer: 0, resolveGen: 0, drag: '',
};

export const el = (tag, cls, text) => { const e = document.createElement(tag); if (cls) e.className = cls; if (text != null) e.textContent = text; return e; };
export function btn(label, onClick, cls = 'btn', iconName) {
  const b = el('button', cls);
  b.type = 'button';
  if (iconName) b.insertAdjacentHTML('beforeend', icon(iconName));
  b.append(document.createTextNode(label));
  if (onClick) b.addEventListener('click', onClick);
  return b;
}
export const lsKey = (k) => projectStorageKey('collections.' + k);
export const lsGet = (k) => { try { return localStorage.getItem(lsKey(k)) || ''; } catch (e) { return ''; } };
export const lsSet = (k, v) => { try { localStorage.setItem(lsKey(k), v); } catch (e) { /* not persisted */ } };
export function fmtSize(n) { return n < 1024 ? n + ' B' : n < 1048576 ? (n / 1024).toFixed(1) + ' KB' : (n / 1048576).toFixed(1) + ' MB'; }
export const itemByUid = (uid) => S.items.find((i) => i.uid === uid) || null;
export const panelActive = () => !!$('#panel-collections')?.classList.contains('active');

export function setStatus(kind, text, actions = []) {
  const bar = $('#collStatus');
  if (!bar) return;
  bar.textContent = '';
  if (!text) { bar.hidden = true; return; }
  bar.dataset.kind = kind || 'info';
  bar.hidden = false;
  bar.append(el('span', '', text));
  actions.forEach((a) => bar.append(btn(a.label, a.run, 'btn xs')));
}

export function announce(text) {
  const live = $('#collStatus');
  if (!live) return;
  if (live.hidden) { live.textContent = text; live.hidden = false; setTimeout(() => { if (live.textContent === text) { live.textContent = ''; live.hidden = true; } }, 1500); }
}

/* ------------------------------------------------------------------ editor */

// X is the late-bound registry for cross-module calls. collections.js sets the
// loaders at boot; tree, editor, response and sheets register their shared
// functions when they load (collections.js imports all of them before any
// user action can fire), so X.renderTree() and friends need no sibling import.
export const X = { loadAll: null, loadCollection: null, repaintTree: null };
