// collections-core.js — shared state and tiny helpers for the Collections panel
// modules (tree, editor, response, sheets, env and the collections.js entry).
// Modules import each other's functions directly; the two loaders the entry
// module owns are reached through X so no module imports the entry (no cycle).
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

// X holds the loaders owned by collections.js (set at boot). repaintTree lets a
// module repaint the tree without importing it, which would close a cycle
// (collections-tree.js already imports from collections-env.js).
export const X = { loadAll: null, loadCollection: null, repaintTree: null };
