// identities-core.js — shared state and small helpers for the Identities panel
// modules (identities.js entry, identities-detail.js, identities-edit.js).
// Sibling modules never import each other: the entry registers the loaders
// the others call on X, so the static import graph stays acyclic.
import { $, api, state, icon } from './core.js';
import { getShellApi } from './shell-hooks.js';
import { emit as busEmit, EVENTS } from './bus.js';

export const JSON_HDR = { 'content-type': 'application/json' };
export const jsend = (method, path, body) => api(path, { method, headers: JSON_HDR, body: body === undefined ? undefined : JSON.stringify(body) });
export const enc = encodeURIComponent;

// S is the panel's in-memory state. `ids` is GET /api/authz's identities (raw
// headers included: they back the Reveal view and never render unless asked);
// `detail` is GET /api/authz/identities/{name} for the selected one.
export const S = {
  booted: false, loaded: false, ids: [], filter: '', sel: '', tab: 'overview',
  listGen: 0, detailGen: 0, runsGen: 0, matrixGen: 0, timelineGen: 0,
  detail: null, detailFor: '', detailError: null,
  revealed: new Set(), busy: '',
  runs: null, runsError: null, runsAll: false, runSel: '', matrix: null, matrixFor: '', matrixError: null,
  loginTimeline: null, loginTimelineFor: 0, loginTimelineError: null,
  edit: null, // {mode:'create'|'edit', name, baseline, stale, overwrite}
};

// X holds the late-bound cross-module calls (set by identities.js at boot).
export const X = { loadIdentities: null, loadDetail: null, loadRuns: null, renderList: null, renderDetail: null, select: null, openEdit: null };

export const panelActive = () => !!$('#panel-identities')?.classList.contains('active');
export const byName = (name) => S.ids.find((i) => i.name === name) || null;
export const selectedFlowId = () => (Number(state.selId) > 0 ? Number(state.selId) : 0);
export const selectedFlow = () => { const id = selectedFlowId(); return id ? (state.flows || []).find((f) => f.id === id) || null : null; };

export const el = (tag, cls, text) => { const e = document.createElement(tag); if (cls) e.className = cls; if (text != null) e.textContent = text; return e; };
export function btn(label, onClick, cls = 'btn', iconName = '', aria = '') {
  const b = el('button', cls);
  b.type = 'button';
  if (iconName) b.insertAdjacentHTML('beforeend', icon(iconName));
  if (label) b.append(document.createTextNode((iconName ? ' ' : '') + label));
  if (aria) b.setAttribute('aria-label', aria);
  if (onClick) b.addEventListener('click', onClick);
  return b;
}

export function setStatus(text, kind = 'status') {
  const s = $('#identStatus');
  if (!s) return;
  s.textContent = text || '';
  s.dataset.kind = kind;
  s.setAttribute('role', kind === 'error' ? 'alert' : 'status');
}

// openFlowInHistory switches to Proxy and selects a captured flow there, the
// same route authz.js and the auth timeline use.
export function openFlowInHistory(flowId) {
  const id = Number(flowId);
  if (!Number.isSafeInteger(id) || id <= 0) return;
  const shell = getShellApi();
  if (shell.activateTab) shell.activateTab('proxy'); else document.querySelector('.tab[data-tab="proxy"]')?.click();
  busEmit(EVENTS.PROXY_SELECT_FLOW, id);
}

// flowLink is a button that opens a flow in History; the label is text only.
export function flowLink(flowId, label) {
  const b = btn(label || ('#' + flowId), () => openFlowInHistory(flowId), 'btn xs ident-flow-link', 'external');
  b.setAttribute('aria-label', 'Open flow #' + flowId + ' in History');
  b.dataset.flow = String(flowId);
  return b;
}
