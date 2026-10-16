// settings-health.js — a health chip in every Settings section header (proxy,
// TLS, scope, auth, scanner). Always icon plus text, never colour alone. It reads
// the server's readiness report and the project state; it never fetches before the
// project store has loaded (so nothing is requested while the UI is locked), and a
// failed read keeps the last good chip with a dashed "stale" marker.
import { api } from './core.js';
import { projectState } from './project-state.js';
import { sectionHealth } from './settings-model.js';
import { TAB_CHANGE_EVENT } from './shell-hooks.js';

const SECTIONS = ['proxy', 'tls', 'scope', 'scanner', 'session'];
const ICONS = { ok: 'status-done', warn: 'alert', unknown: 'status-todo' };
const MIN_GAP_MS = 5000;
let readiness = null;
let stale = false;
let lastFetch = 0;
let inflight = false;
const NS = 'http://www.w3.org/2000/svg';

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

function chipFor(sec) {
  const host = document.querySelector('.set-sec[data-sec="' + sec + '"] .settings-section-heading');
  if (!host) return null;
  let chip = host.querySelector('.settings-health');
  if (!chip) {
    chip = document.createElement('span');
    chip.className = 'settings-health';
    chip.dataset.healthFor = sec;
    chip.hidden = true;
    host.appendChild(chip);
  }
  return chip;
}

function paint(chip, health) {
  chip.textContent = '';
  if (!health) { chip.hidden = true; return; }
  chip.hidden = false;
  chip.dataset.state = health.state;
  chip.dataset.stale = stale && health.state !== 'unknown' ? 'true' : 'false';
  chip.title = (health.detail || '') + (chip.dataset.stale === 'true' ? ' (could not refresh)' : '');
  const label = document.createElement('span');
  label.textContent = health.text + (chip.dataset.stale === 'true' ? ' (stale)' : '');
  chip.append(iconNode(ICONS[health.state] || 'status-todo'), label);
}

export function renderHealth() {
  const s = projectState.get();
  const ctx = { readiness, stale, scope: s.scope, brief: s.brief, identities: s.identities.length };
  SECTIONS.forEach((sec) => { const chip = chipFor(sec); if (chip) paint(chip, sectionHealth(sec, ctx)); });
}

async function refreshReadiness() {
  if (inflight || !projectState.get().loaded) return;
  const now = Date.now();
  if (now - lastFetch < MIN_GAP_MS) return;
  lastFetch = now;
  inflight = true;
  try { readiness = await api('/api/readiness'); stale = false; } catch (e) { stale = true; } finally { inflight = false; }
  renderHealth();
}

projectState.subscribe(() => { renderHealth(); refreshReadiness(); });
document.addEventListener(TAB_CHANGE_EVENT, (e) => { if (e.detail && e.detail.tab === 'settings') refreshReadiness(); });
renderHealth();
