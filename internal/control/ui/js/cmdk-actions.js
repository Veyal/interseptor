// cmdk-actions.js — palette entries added on top of app.js's base command list:
// prefix-mode starters, the reversible actions of the command model (scope,
// readiness, export, blockers, single-key switch), legacy-dialog reachability
// and the `>` selection verbs. Everything here either navigates, opens a dialog
// or performs a toggle that can be reversed from the same place; nothing sends,
// deletes or scans.
import { $, state, toast, openModal, openFlow } from './core.js';
import { getShellApi } from './shell-hooks.js';
import { COPY_AS_KINDS, copyAs } from './copyas.js';
import { LEGACY_MODALS } from './cmdk-logic.js';
import { setSingleKeyShortcuts, singleKeyShortcutsOn } from './keyboard.js';

const NEED_FLOW = 'select a flow in History first';

export function goTab(name) {
  const api = getShellApi();
  if (api && api.activateTab) { api.activateTab(name); return; }
  document.querySelector('.tab[data-tab="' + name + '"]')?.click();
}

export function selectedFlows() {
  const ids = state.selected && state.selected.size ? [...state.selected] : (state.selId ? [state.selId] : []);
  const byId = new Map((state.flows || []).map((f) => [f.id, f]));
  return ids.map((id) => byId.get(id) || { id });
}

// openFlowFor: the Flow Drawer when registered, else the History selection.
export function openFlowFor(flow, { toRepeater = false, fallback } = {}) {
  if (toRepeater) {
    import('./tools.js').then((m) => m.sendToRepeater(flow)).catch(() => toast('Could not open Repeater'));
    return;
  }
  if (openFlow(flow.id, { source: 'palette' })) return;
  if (fallback) fallback(flow);
}

function clickLater(selector, tab) {
  if (tab) goTab(tab);
  const el = document.querySelector(selector);
  if (el && !el.disabled) el.click();
  else toast('Open the relevant view first');
}

function legacyOpen(kind) {
  const flows = selectedFlows();
  if (kind === 'shortcuts') { openModal($('#shortcutsModal')); return; }
  if (!flows.length) { toast(NEED_FLOW); return; }
  if (kind === 'selected-flow') import('./flowmodal.js').then((m) => m.flowPopup(flows[0].id));
  else if (kind === 'session') import('./session-inspection.js').then((m) => m.openSessionInspector(flows.map((f) => f.id)));
  else if (kind === 'auth-timeline') import('./authtimeline.js').then((m) => m.openAuthTimeline(flows[0].id));
}

// Dialogs the base list already offers (`how.cmd`) are not repeated.
export function legacyModalCommands() {
  const out = [];
  for (const m of LEGACY_MODALS) {
    const how = m.how || {};
    if (how.cmd || how.transient) continue;
    const run = how.click ? () => clickLater(how.click, how.tab) : () => legacyOpen(how.open);
    out.push({ t: m.title, kw: m.kw || '', group: 'Dialogs', sub: m.id, run });
  }
  return out;
}

export function builtinActions() {
  return [
    { t: 'Search flows…', kw: 'f: find request history', group: 'Actions', sub: 'f:', prefill: 'f:', run() {} },
    { t: 'Search findings…', kw: '# finding report', group: 'Actions', sub: '#', prefill: '#', run() {} },
    { t: 'Actions on selected flows…', kw: '> selection bulk send copy attach', group: 'Actions', sub: '>', prefill: '>', run() {} },
    { t: 'Switch identity…', kw: '@ send as role user active authz', group: 'Actions', sub: '@', prefill: '@', run() {} },
    { t: 'Show keyboard shortcuts…', kw: '? help cheatsheet keys', group: 'Actions', sub: '?', prefill: '?', run() {} },
    { t: 'Toggle scope on or off', kw: 'engagement scope switch in scope only', group: 'Actions', run: () => clickLater('#ctxScope') },
    { t: 'Run readiness check', kw: 'findings readiness blockers ready report', group: 'Actions', run: () => clickLater('#findReadinessCheck', 'findings') },
    { t: 'Export report', kw: 'export findings report preflight draft', group: 'Actions', run: () => clickLater('#findExportOpen', 'findings') },
    { t: 'Open report blockers', kw: 'blockers readiness gaps strip popover', group: 'Actions', run: () => clickLater('#ctxBlockers') },
    {
      t: singleKeyShortcutsOn() ? 'Turn single-key shortcuts off' : 'Turn single-key shortcuts on',
      kw: 'keyboard hotkeys wcag letters shortcuts switch', group: 'Actions',
      run: () => { const on = setSingleKeyShortcuts(!singleKeyShortcutsOn()); toast('Single-key shortcuts ' + (on ? 'on' : 'off')); },
    },
    ...legacyModalCommands(),
  ];
}

// Verbs for `>`: they act on the History selection.
export function selectionActions() {
  const need = (fn) => () => { const f = selectedFlows(); if (!f.length) { toast(NEED_FLOW); return; } fn(f); };
  const verbs = [
    { t: 'Open in the flow view', kw: 'open drawer inspect flow', run: need((f) => openFlowFor(f[0], { fallback: (x) => { goTab('proxy'); import('./proxy.js').then((m) => m.selectFlow(x.id)); } })) },
    { t: 'Send to Repeater', kw: 'resend craft edit', run: need((f) => import('./tools.js').then((m) => m.sendToRepeater(f[0]))) },
    { t: 'Send to Intruder', kw: 'fuzz payloads', run: need((f) => import('./tools.js').then((m) => m.sendToIntruder(f[0]))) },
    { t: 'Attach to a finding', kw: 'evidence proof add finding', run: need(() => import('./findings.js').then((m) => m.pickFindingForSelection())) },
    { t: 'Compare two flows (diff)', kw: 'diff compare', run: () => { if ((state.selected ? state.selected.size : 0) !== 2) { toast('select exactly two flows to compare'); return; } import('./proxy.js').then((m) => m.openCompare()); } },
    { t: 'Inspect session timeline', kw: 'session roles timeline', run: need(() => legacyOpen('session')) },
    { t: 'Auth timeline from the flow', kw: 'authentication login token', run: need(() => legacyOpen('auth-timeline')) },
  ];
  for (const k of COPY_AS_KINDS) verbs.push({ t: 'Copy as ' + k.label, kw: 'copy clipboard export ' + k.kind, run: need((f) => copyAs(k.kind, f)) });
  return verbs.map((v) => ({ ...v, group: 'Selection' }));
}

// Identity switching drives the strip's own select so persistence and the
// strip stay the single path.
export function setIdentity(name) {
  const sel = $('#ctxIdentity');
  if (!sel) { toast('No identities defined'); return false; }
  sel.value = name;
  sel.dispatchEvent(new Event('change', { bubbles: true }));
  toast('Active identity: ' + name);
  return true;
}

export function openFindingRoute(id) {
  goTab('findings');
  window.location.hash = '#finding-' + id + '/overview';
}
