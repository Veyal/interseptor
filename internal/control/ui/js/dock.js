// dock.js — the phone bottom navigation (<=720px). Five destinations map onto
// the rail groups; each opens its last-used panel and shows a segmented control
// for its sibling panels. Panel switching goes through the shell's activateTab
// (one source of truth), and the dock only mirrors the state that comes back via
// the tabchange event. The legacy #mobileToolSelect stays in the DOM and stays
// synced by app.js; the rail (#tabs) is display:none at this width, so assistive
// tech never sees two navs.

import { getShellApi, TAB_CHANGE_EVENT } from './shell-hooks.js';
import { openSheet, closeSheet } from './sheet.js';
import { openModal, projectStorageKey, setDensity, getDensity } from './core.js';
import { watchSoftKeyboard } from './soft-keyboard.js';
import {
  DOCK_DESTINATIONS, PANEL_LABELS, destinationForPanel, resolvePanel, rememberPanel, parseStoredLast,
  badgeCount, badgeText, badgeAnnouncement, dockKeyTarget,
} from './dock-model.js';

const $ = (id) => document.getElementById(id);
const mk = (tag, cls, text) => {
  const n = document.createElement(tag);
  if (cls) n.className = cls;
  if (text != null) n.textContent = text;
  return n;
};

let last = {};
let dock = null;
let seg = null;

function loadLast() {
  try { last = parseStoredLast(localStorage.getItem(projectStorageKey('dockLast'))); } catch (e) { last = {}; }
}
function saveLast() {
  try { localStorage.setItem(projectStorageKey('dockLast'), JSON.stringify(last)); } catch (e) { /* storage blocked: memory only */ }
}

const activePanel = () => document.querySelector('.tab.active')?.dataset.tab || 'proxy';
const buttons = () => [...dock.querySelectorAll('.dock-btn')];
const goTo = (panel) => getShellApi().activateTab(panel);

/* ---- segmented sub-control for destinations with 2+ panels ---- */
function renderSeg(panel) {
  const dest = DOCK_DESTINATIONS.find((d) => d.id === destinationForPanel(panel));
  const multi = dest && dest.panels.length > 1;
  seg.hidden = !multi;
  if (!multi) { seg.textContent = ''; seg.dataset.dest = ''; return; }
  if (seg.dataset.dest === dest.id) {
    seg.querySelectorAll('button').forEach((b) => b.setAttribute('aria-pressed', String(b.dataset.panel === panel)));
    return;
  }
  seg.dataset.dest = dest.id;
  seg.setAttribute('aria-label', dest.label + ' panels');
  seg.textContent = '';
  for (const p of dest.panels) {
    const b = mk('button', '', PANEL_LABELS[p]);
    b.type = 'button';
    b.dataset.panel = p;
    b.setAttribute('aria-pressed', String(p === panel));
    b.addEventListener('click', () => goTo(p));
    seg.appendChild(b);
  }
}

/* ---- state mirrored from the shell ---- */
function syncActive(panel = activePanel()) {
  const dest = destinationForPanel(panel);
  const next = rememberPanel(last, panel);
  if (dest && next[dest] !== last[dest]) { last = next; saveLast(); }
  for (const b of buttons()) {
    if (b.dataset.dock === dest) b.setAttribute('aria-current', 'page'); else b.removeAttribute('aria-current');
  }
  renderSeg(panel);
}

function readBadge(id) {
  const el = $(id);
  if (!el) return 0;
  const hidden = el.hidden || getComputedStyle(el).display === 'none';
  return badgeCount((el.firstElementChild || el).textContent, hidden);
}

function syncBadges() {
  const counts = { capture: readBadge('heldBadge'), report: readBadge('findBadge') };
  for (const b of buttons()) {
    const n = counts[b.dataset.dock] || 0;
    const visible = b.querySelector('.dock-badge');
    const spoken = b.querySelector('.dock-badge-note');
    if (!visible || !spoken) continue;
    visible.textContent = badgeText(n);
    visible.hidden = n === 0;
    spoken.textContent = n ? ', ' + badgeAnnouncement(b.dataset.dock, n) : '';
  }
}

function syncEnabled() {
  const tabs = [...document.querySelectorAll('.tab')];
  const off = tabs.length === 0 || tabs.every((t) => t.disabled);
  for (const b of buttons()) b.disabled = off;
  dock.setAttribute('aria-busy', String(off));
  if (seg) seg.querySelectorAll('button').forEach((b) => { b.disabled = off; });
}

function syncAll() { syncEnabled(); syncActive(); syncBadges(); }

/* ---- More sheet: Settings, project, appearance, shortcuts, version ---- */
function row(label, control) {
  const r = mk('div', 'dock-more-row');
  r.append(mk('span', 'dock-more-label', label), control);
  return r;
}
function actionButton(text, run) {
  const b = mk('button', 'btn dock-more-btn', text);
  b.type = 'button';
  b.addEventListener('click', run);
  return b;
}
function segControl(label, options, current, onPick) {
  const s = mk('div', 'seg');
  s.setAttribute('role', 'group');
  s.setAttribute('aria-label', label);
  for (const [value, text] of options) {
    const b = mk('button', '', text);
    b.type = 'button';
    b.setAttribute('aria-pressed', String(value === current));
    b.addEventListener('click', () => {
      onPick(value);
      s.querySelectorAll('button').forEach((x) => x.setAttribute('aria-pressed', String(x === b)));
    });
    s.appendChild(b);
  }
  return s;
}

function themeChoice() {
  let saved = null;
  try { saved = localStorage.getItem('theme'); } catch (e) { /* default to system */ }
  return saved === 'dark' || saved === 'light' || saved === 'hc' ? saved : 'system';
}
function applyThemeChoice(choice) {
  let effective = choice;
  try {
    if (choice === 'system') {
      localStorage.removeItem('theme');
      effective = window.matchMedia('(prefers-color-scheme: light)').matches ? 'light' : 'dark';
    } else localStorage.setItem('theme', choice);
  } catch (e) { if (choice === 'system') effective = 'dark'; }
  const root = document.documentElement;
  if (effective === 'light' || effective === 'hc') root.setAttribute('data-theme', effective); else root.removeAttribute('data-theme');
  $('themeToggle')?.querySelector('use')?.setAttribute('href', effective === 'light' ? '#i-sun' : '#i-moon');
}

function buildMore() {
  const wrap = mk('div', 'dock-more');
  const leaveThen = (fn) => () => { closeSheet('moreSheet'); setTimeout(fn, 0); };
  wrap.append(
    row('Settings', actionButton('Open Settings', leaveThen(() => goTo('settings')))),
    row('Project', actionButton('Switch project', leaveThen(() => $('mobileProjectBtn')?.click()))),
    row('Theme', segControl('Theme', [['system', 'System'], ['dark', 'Dark'], ['light', 'Light'], ['hc', 'High contrast']], themeChoice(), applyThemeChoice)),
    row('Density', segControl('Density', [['compact', 'Compact'], ['default', 'Default'], ['comfortable', 'Comfortable']], getDensity(), setDensity)),
    row('Shortcuts', actionButton('Keyboard shortcuts', leaveThen(() => openModal($('shortcutsModal'))))),
  );
  const version = $('verBadge')?.textContent.trim();
  if (version) wrap.appendChild(mk('p', 'dock-more-version', 'Interseptor ' + version));
  return wrap;
}

function openMore(opener) {
  openSheet({ id: 'moreSheet', title: 'More', detents: ['half', 'full'], detent: 'half', content: buildMore, opener });
}

/* ---- events ---- */
function onDockClick(e) {
  const b = e.target.closest('.dock-btn');
  if (!b || b.disabled) return;
  if (b.dataset.dock === 'more') { openMore(b); return; }
  const panel = resolvePanel(b.dataset.dock, last);
  if (panel && panel !== activePanel()) goTo(panel);
}

function onDockKey(e) {
  const list = buttons();
  const at = list.indexOf(document.activeElement);
  if (at < 0) return;
  const to = dockKeyTarget(e.key, at, list.length);
  if (to < 0) return;
  e.preventDefault();
  list[to].focus();
}

function init() {
  dock = $('dock');
  if (!dock || dock.dataset.ready === 'true') return;
  dock.dataset.ready = 'true';
  loadLast();
  seg = mk('div', 'seg dock-seg');
  seg.id = 'dockSeg';
  seg.setAttribute('role', 'group');
  seg.hidden = true;
  $('appRow')?.before(seg);
  dock.addEventListener('click', onDockClick);
  dock.addEventListener('keydown', onDockKey);
  document.addEventListener(TAB_CHANGE_EVENT, (e) => { syncActive(e.detail && e.detail.tab); syncEnabled(); });
  const tabs = $('tabs');
  if (tabs) new MutationObserver(syncAll).observe(tabs, { subtree: true, attributes: true, childList: true, characterData: true, attributeFilter: ['disabled', 'class', 'style'] });
  watchSoftKeyboard();
  syncAll();
}

init();
