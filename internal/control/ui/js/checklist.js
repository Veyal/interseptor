// checklist.js — the dismissible first-run card (Trust the CA, point a client at
// the proxy, define scope, capture a flow, create a finding). Every step comes
// from real state (the readiness report and the project store), never a manual
// tick. It shows "N of 5" as text plus a <progress>, hides itself at 5 of 5, and
// remembers a dismissal per project. Panels mount it through the
// `mountChecklist` hook (Proxy empty state, the More sheet).
import { api, projectStorageKey, registerHook, getHook } from './core.js';
import { projectState } from './project-state.js';
import { getShellApi } from './shell-hooks.js';
import { deriveChecklist, checklistVisible } from './checklist-model.js';

const DISMISS_KEY = 'checklistDismissed';
const MIN_GAP_MS = 10000;
const NS = 'http://www.w3.org/2000/svg';
let readiness = null;
let lastFetch = 0;
let inflight = false;
const mounts = new Set();

function readDismissed() { try { return localStorage.getItem(projectStorageKey(DISMISS_KEY)) === '1'; } catch (e) { return false; } }
function writeDismissed() { try { localStorage.setItem(projectStorageKey(DISMISS_KEY), '1'); } catch (e) { /* not persisted */ } }

const el = (tag, cls, text) => {
  const n = document.createElement(tag);
  if (cls) n.className = cls;
  if (text != null) n.textContent = text;
  return n;
};
function iconNode(name) {
  const svg = document.createElementNS(NS, 'svg');
  svg.setAttribute('class', 'icon');
  svg.setAttribute('aria-hidden', 'true');
  svg.setAttribute('focusable', 'false');
  const use = document.createElementNS(NS, 'use');
  use.setAttribute('href', '#i-' + name);
  svg.appendChild(use);
  return svg;
}

export function runChecklistAction(action) {
  const shell = getShellApi();
  if (action === 'setup') { import('./setup.js').then(m => m.openSetup()); return; }
  if (action === 'scope') {
    const open = getHook('openEngagementSheet');
    if (open) open(); else if (shell.openSettings) shell.openSettings('scope');
    return;
  }
  if (action === 'devices') { if (shell.openSettings) shell.openSettings('devices'); return; }
  if (shell.activateTab) shell.activateTab(action);
}

function factsNow() {
  const s = projectState.get();
  return { readiness, scope: s.scope, evidence: s.evidence, findings: s.findings };
}

function renderCard(host, model, onDismiss) {
  host.textContent = '';
  const card = el('section', 'checklist');
  card.setAttribute('aria-labelledby', 'checklistTitle');
  const head = el('div', 'checklist-head');
  const title = el('h3', 'checklist-title', 'Get started');
  title.id = 'checklistTitle';
  const count = el('span', 'checklist-count', model.text);
  const dismiss = el('button', 'btn checklist-dismiss', 'Dismiss');
  dismiss.type = 'button';
  dismiss.setAttribute('aria-label', 'Dismiss the getting started checklist');
  dismiss.addEventListener('click', onDismiss);
  head.append(title, count, dismiss);
  const bar = el('progress', 'checklist-progress');
  bar.max = model.total;
  bar.value = model.done;
  bar.setAttribute('aria-label', 'Setup progress: ' + model.text);
  const list = el('ol', 'checklist-steps');
  model.steps.forEach((s) => {
    const li = el('li', 'checklist-step');
    li.dataset.done = s.done ? 'true' : 'false';
    li.appendChild(iconNode(s.done ? 'status-done' : 'status-todo'));
    li.appendChild(el('span', 'checklist-label', s.label));
    li.appendChild(el('span', 'checklist-state', s.done ? 'Done' : 'To do'));
    if (!s.done) {
      const go = el('button', 'btn checklist-go', 'Do it');
      go.type = 'button';
      go.setAttribute('aria-label', 'Do it: ' + s.label);
      go.addEventListener('click', () => runChecklistAction(s.action));
      li.appendChild(go);
    }
    list.appendChild(li);
  });
  const live = el('p', 'u-sr', model.text + ' setup steps complete');
  live.setAttribute('role', 'status');
  live.setAttribute('aria-live', 'polite');
  card.append(head, bar, list, live);
  host.appendChild(card);
}

function paint(m) {
  if (!projectState.get().loaded) { m.host.hidden = true; return; }
  const model = deriveChecklist(factsNow());
  const show = checklistVisible(model, m.dismissed);
  m.host.hidden = !show;
  if (!show) { m.host.textContent = ''; return; }
  renderCard(m.host, model, () => { m.dismissed = true; writeDismissed(); paint(m); });
}

async function refreshReadiness() {
  if (inflight || !projectState.get().loaded) return;
  const now = Date.now();
  if (now - lastFetch < MIN_GAP_MS) return;
  lastFetch = now;
  inflight = true;
  try { readiness = await api('/api/readiness'); } catch (e) { readiness = null; } finally { inflight = false; }
  mounts.forEach(paint);
}

// mountChecklist(host) renders the card into host and keeps it current.
export function mountChecklist(host) {
  const m = { host, dismissed: readDismissed(), off: null };
  host.classList.add('checklist-host');
  mounts.add(m);
  m.off = projectState.subscribe(() => { if (m.host.isConnected) { paint(m); refreshReadiness(); } });
  paint(m);
  refreshReadiness();
  return { refresh: () => paint(m), destroy() { if (m.off) m.off(); mounts.delete(m); m.host.textContent = ''; } };
}

registerHook('mountChecklist', mountChecklist);
