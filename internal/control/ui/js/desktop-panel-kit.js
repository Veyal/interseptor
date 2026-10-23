// desktop-panel-kit.js — the one helper module of the desktop skin
// (docs/desktop-design.md). Every desktop module (the shell chrome and the panel
// layouts) takes these from here instead of keeping its own copy:
//   - host and platform checks (isDesktop, isMac),
//   - tiny DOM builders (el, iconNode),
//   - "wait until the panel built its DOM" watchers (whenReady, whenDesktop, observe),
//   - sentence-case labels (caseLabels),
//   - the overflow menu (createOverflow): one trigger look (an ellipsis button named "More actions"),
//     one menu, one keyboard model,
//   - the one outside-click / Escape / resize handling for every desktop popup
//     (registerPopup), so menus and popovers close the same way everywhere,
//   - restore: when html.desktop-app goes away, every control the skin moved goes
//     back where the web markup had it.
// Leaf module: it imports only its pure model, and nothing runs at import time.
import { sentenceCase, overflowStep, placeMenu } from './desktop-panel-tools-model.js';

const SVG_NS = 'http://www.w3.org/2000/svg';
const DESKTOP_CLASS = 'desktop-app';

export const isDesktop = () => !!document.documentElement.classList.contains('desktop-app');
export const isMac = () => /Mac|iPhone|iPad/.test((globalThis.navigator && navigator.platform) || '') || /Mac OS X/.test((globalThis.navigator && navigator.userAgent) || '');
// modShortcut writes a Mod+key chord the way this platform does: ⌘K on macOS,
// Ctrl+K elsewhere.
export function modShortcut(key) {
  return isMac() ? '⌘' + key : 'Ctrl+' + key;
}
// hasNativeHost: running inside the native app's web view, where a Mod+key the
// browser would reserve (Ctrl/⌘+T) reaches the page.
export const hasNativeHost = () => !!(globalThis.window && globalThis.window.interseptorDesktop);

// A give-up timer never keeps a node test process alive.
const unref = (t) => { if (t && typeof t.unref === 'function') t.unref(); return t; };

export function el(tag, cls, text) {
  const e = document.createElement(tag);
  if (cls) e.className = cls;
  if (text != null) e.textContent = text;
  return e;
}

export function iconNode(name) {
  const svg = document.createElementNS(SVG_NS, 'svg');
  svg.setAttribute('class', 'icon');
  svg.setAttribute('aria-hidden', 'true');
  svg.setAttribute('focusable', 'false');
  const use = document.createElementNS(SVG_NS, 'use');
  use.setAttribute('href', '#i-' + name);
  svg.appendChild(use);
  return svg;
}

// whenReady runs `run` once `test()` is truthy. The panels build parts of their
// DOM after boot (tabs, action rows, extras), so this watches the document
// until the parts exist, then stops. It gives up after `timeout` ms.
export function whenReady(test, run, timeout = 30000) {
  let done = false;
  let obs = null;
  const attempt = () => {
    if (done) return true;
    let ok = false;
    try { ok = !!test(); } catch { ok = false; }
    if (!ok) return false;
    done = true;
    if (obs) obs.disconnect();
    try { run(); } catch (e) { console.error('desktop panel setup failed:', e); }
    return true;
  };
  if (attempt()) return;
  obs = new MutationObserver(attempt);
  obs.observe(document.body || document.documentElement, { childList: true, subtree: true });
  unref(setTimeout(() => { if (obs) obs.disconnect(); }, timeout));
}

// whenDesktop runs setup() once the page is a desktop host AND ready() says the
// panel's controls exist. A browser tab never gets html.desktop-app, so setup
// never runs there; the class watcher gives up after `timeout` ms.
export function whenDesktop(ready, setup, timeout = 15000) {
  const go = () => whenReady(ready, setup, timeout);
  if (isDesktop()) { go(); return; }
  const obs = new MutationObserver(() => { if (isDesktop()) { obs.disconnect(); go(); } });
  obs.observe(document.documentElement, { attributes: true, attributeFilter: ['class'] });
  unref(setTimeout(() => obs.disconnect(), timeout));
}

// observe keeps a callback in sync with an element: it runs now and after every
// DOM change under `el`. `fn` must be idempotent, because its own edits trigger
// the observer once more.
export function observe(node, fn, options = { childList: true, subtree: true, characterData: true }) {
  if (!node) return null;
  fn();
  const obs = new MutationObserver(() => fn());
  obs.observe(node, options);
  return obs;
}

// caseLabels rewrites the leaf text of `selector` (under `root`) to sentence case.
export function caseLabels(root, selector) {
  root.querySelectorAll(selector).forEach((node) => {
    const nodes = node.children.length ? [...node.children].filter((c) => !c.children.length) : [node];
    nodes.forEach((n) => { const next = sentenceCase(n.textContent); if (next !== n.textContent.trim() && next) n.textContent = next; });
    // A label written as a bare text node next to an element (".rep-sub").
    [...node.childNodes].forEach((n) => {
      if (n.nodeType !== 3) return;
      const next = sentenceCase(n.data);
      if (next !== n.data.trim() && next) n.data = next;
    });
  });
}

// ---- restore: moved controls go home when the desktop class goes away ----
const moves = [];
const built = [];
let restoreWatch = null;
function watchRestore() {
  if (restoreWatch) return;
  restoreWatch = new MutationObserver(() => { if (!isDesktop()) restoreAll(); });
  restoreWatch.observe(document.documentElement, { attributes: true, attributeFilter: ['class'] });
}
// move re-parents `node` into `parent` (before `before`, or last) and records
// where it came from, so restoreAll can put it back.
export function move(node, parent, before = null) {
  if (!node || !parent) return node;
  if (!moves.some((m) => m.node === node)) {
    moves.push({ node, parent: node.parentNode, next: node.nextSibling, tabindex: node.getAttribute('tabindex'), hidden: node.getAttribute('aria-hidden') });
  }
  parent.insertBefore(node, before);
  watchRestore();
  return node;
}
// built marks a node the skin created; restoreAll hides it (it is never removed:
// the desktop modules do not delete DOM).
export function track(node) { if (node) { built.push(node); watchRestore(); } return node; }
export function restoreAll() {
  closeAllPopups();
  for (let i = moves.length - 1; i >= 0; i--) {
    const m = moves[i];
    const next = m.next && m.next.parentNode === m.parent ? m.next : null;
    if (m.parent) m.parent.insertBefore(m.node, next);
    if (m.tabindex === null) m.node.removeAttribute('tabindex'); else m.node.setAttribute('tabindex', m.tabindex);
    if (m.hidden === null) m.node.removeAttribute('aria-hidden'); else m.node.setAttribute('aria-hidden', m.hidden);
  }
  moves.length = 0;
  built.forEach((n) => { n.hidden = true; });
}

// ---- popups: one outside-click / Escape / resize handling ----
// A popup is {isOpen(), close({focus}), contains(node), ignore?(node)}. Opening
// one closes the others; a press outside, a resize or the window losing focus
// closes it; Escape closes the open one and returns focus to its trigger,
// unless a select menu inside it is open (that Escape belongs to the select).
const popups = new Set();
let dismissInstalled = false;
function closeAllPopups(except = null) { popups.forEach((p) => { if (p !== except && p.isOpen()) p.close({ focus: false }); }); }
function installDismiss() {
  if (dismissInstalled) return;
  dismissInstalled = true;
  document.addEventListener('pointerdown', (e) => {
    const t = e.target;
    popups.forEach((p) => {
      if (!p.isOpen() || p.contains(t)) return;
      if (p.ignore && p.ignore(t)) return;
      p.close({ focus: false });
    });
  }, true);
  document.addEventListener('keydown', (e) => {
    if (e.key !== 'Escape' || e.defaultPrevented) return;
    const open = [...popups].filter((p) => p.isOpen());
    if (!open.length) return;
    if (document.querySelector('.ui-select-trigger[aria-expanded="true"]')) return;
    // A modal opened over a popup owns Escape: close the popups quietly and let it through.
    if (!open.some((p) => p.contains(e.target))) { open.forEach((p) => p.close({ focus: false })); return; }
    e.preventDefault();
    e.stopPropagation();
    open.forEach((p) => p.close({ focus: true }));
  }, true);
  window.addEventListener('resize', () => closeAllPopups());
  window.addEventListener('blur', () => closeAllPopups());
}
export function registerPopup(p) {
  installDismiss();
  popups.add(p);
  return {
    opened: () => closeAllPopups(p),
    unregister: () => popups.delete(p),
  };
}

// sep builds a hairline between groups of menu items.
export function menuSeparator() {
  const s = document.createElement('div');
  s.className = 'dsk-menu-sep';
  s.setAttribute('role', 'separator');
  return s;
}

// moreButton is the one overflow trigger: an icon-only ellipsis button whose
// accessible name is "More actions" unless the menu is a real noun ("Views").
export function moreButton({ id, label = 'More actions', tooltip, className = '' } = {}) {
  const button = document.createElement('button');
  button.type = 'button';
  button.className = 'btn dsk-more' + (className ? ' ' + className : '');
  if (id) button.id = id;
  button.setAttribute('aria-haspopup', 'menu');
  button.setAttribute('aria-expanded', 'false');
  button.setAttribute('aria-label', label);
  button.setAttribute('data-tooltip', tooltip || label);
  button.appendChild(iconNode('more'));
  return track(button);
}

// menuItem renders a descriptor {label, icon, hint, run} as a menu button. Its
// state functions (disabled, hidden, checked, label) are re-read on every open.
function menuItem(d) {
  const b = document.createElement('button');
  b.type = 'button';
  b.className = 'btn dsk-menu-item';
  if (d.key) b.dataset.key = d.key;
  if (d.icon) b.appendChild(iconNode(d.icon));
  const text = el('span', 'dsk-menu-label');
  b.appendChild(text);
  if (d.hint) { const k = el('kbd', 'dsk-menu-hint', d.hint); k.setAttribute('aria-hidden', 'true'); b.appendChild(k); }
  b._dsk = d;
  b.addEventListener('click', () => { if (!b.disabled && typeof d.run === 'function') setTimeout(() => d.run(), 0); });
  return b;
}
function syncItem(b) {
  const d = b._dsk;
  if (!d) return;
  const val = (f, dflt) => (typeof f === 'function' ? f() : (f === undefined ? dflt : f));
  b.querySelector('.dsk-menu-label').textContent = val(d.label, '');
  b.disabled = !!val(d.disabled, false);
  b.hidden = !!val(d.hidden, false);
  const checked = val(d.checked, null);
  if (checked === null) b.removeAttribute('aria-checked');
  else { b.setAttribute('role', 'menuitemcheckbox'); b.setAttribute('aria-checked', checked ? 'true' : 'false'); }
}

let menuSeq = 0;
const isVisibleItem = (n) => n.tagName === 'BUTTON' && !n.hidden && !(n.closest && n.closest('.evr-menu')) && (!n.getClientRects || n.getClientRects().length > 0);
// lockTabs takes a moved control and everything inside it out of the Tab order:
// a split button wrapped in a div keeps its inner buttons tabbable otherwise, and
// the closed menu would show up as an invisible tab stop.
const TABBABLE = /^(BUTTON|INPUT|SELECT|TEXTAREA|A)$/;
export function lockTabs(node) {
  const all = [node, ...walk(node, [])];
  all.forEach((n) => { if (TABBABLE.test(n.tagName)) n.tabIndex = -1; });
}
function walk(node, out) { for (const c of node.children || []) { out.push(c); walk(c, out); } return out; }

// createOverflow builds the overflow trigger and its menu. `items` are existing
// elements (re-parented into the menu, keeping ids, listeners, tooltips and
// disabled state), separators, or descriptors {label, icon, hint, run, disabled,
// hidden, checked} for actions that live elsewhere. A closed menu is transparent
// and inert to the pointer, never display:none or visibility:hidden, so:
//   - a control that anchors a popover to itself (Copy as, Attach as evidence)
//     still has a real rectangle after the menu closed, and
//   - focus the app returns to a moved control (a sheet closing gives focus back
//     to Import) is accepted and handed on to the menu's trigger.
// Items are out of the Tab order (tabindex -1); arrows move between them.
export function createOverflow({ id, label = 'More actions', tooltip, items, className = '', button: given = null, onOpen = null }) {
  const n = ++menuSeq;
  const button = given || moreButton({ id, label, tooltip, className });
  const menu = track(document.createElement('div'));
  menu.className = 'dsk-menu';
  menu.id = (id || 'dskMore' + n) + 'Menu';
  menu.setAttribute('role', 'group');
  menu.setAttribute('aria-label', label);
  menu.setAttribute('aria-hidden', 'true');
  menu.dataset.open = 'false';
  button.setAttribute('aria-controls', menu.id);
  document.body.appendChild(menu);
  const add = (it) => {
    if (!it) return;
    const node = it.nodeType ? it : menuItem(it);
    if (it.nodeType && node.className !== 'dsk-menu-sep') move(node, menu); else menu.appendChild(node);
    lockTabs(node);
  };
  (items || []).forEach(add);

  const focusables = () => walk(menu, []).filter((b) => isVisibleItem(b) && !b.disabled);
  // Separators show only between two visible groups.
  function tidy() {
    walk(menu, []).forEach((c) => { if (c._dsk) syncItem(c); });
    lockTabs(menu);
    let seenItem = false;
    let pending = null;
    for (const c of [...menu.children]) {
      if (c.className === 'dsk-menu-sep') { c.hidden = true; if (seenItem) pending = c; continue; }
      if (c.hidden) continue;
      if (pending) { pending.hidden = false; pending = null; }
      seenItem = true;
    }
  }
  function place() {
    const t = button.getBoundingClientRect();
    const pos = placeMenu({ right: t.right, bottom: t.bottom, top: t.top }, { width: menu.offsetWidth, height: menu.offsetHeight }, { width: window.innerWidth, height: window.innerHeight });
    menu.style.left = pos.left + 'px';
    menu.style.top = pos.top + 'px';
  }
  const isOpen = () => menu.dataset.open === 'true';
  function open() {
    if (isOpen()) return;
    reg.opened();
    if (typeof onOpen === 'function') onOpen(api);
    tidy();
    menu.dataset.open = 'true';
    menu.removeAttribute('aria-hidden');
    button.setAttribute('aria-expanded', 'true');
    place();
    const first = focusables()[0];
    if (first) first.focus({ preventScroll: true });
  }
  function close({ focus = false } = {}) {
    if (!isOpen()) return;
    menu.dataset.open = 'false';
    menu.setAttribute('aria-hidden', 'true');
    button.setAttribute('aria-expanded', 'false');
    if (focus) button.focus({ preventScroll: true });
  }
  const api = { button, menu, open, close, isOpen, add, refresh: tidy };
  const reg = registerPopup({ isOpen, close, contains: (t) => menu.contains(t) || button.contains(t), ignore: (t) => !!(t && t.closest && t.closest('.evr-menu')) });
  button.addEventListener('click', () => { if (isOpen()) close({ focus: true }); else open(); });
  menu.addEventListener('click', (e) => {
    const b = e.target.closest && e.target.closest('button');
    if (!b || b.disabled) return;
    // A split control's caret opens its own dropdown inside the menu: stay open.
    if (b.matches && b.matches('.evr-caret')) return;
    // Keep focus on the trigger when it was inside the menu; a click that moved
    // focus elsewhere (a sheet, a popover) keeps it.
    close({ focus: menu.contains(document.activeElement) });
  });
  // Focus that lands in a closed menu (a sheet or popover returning it to the
  // control that opened it) goes to the trigger instead of an invisible item.
  menu.addEventListener('focusin', () => { if (!isOpen()) button.focus({ preventScroll: true }); });
  menu.addEventListener('keydown', (e) => {
    if (e.key === 'Tab') { e.preventDefault(); if (isOpen()) close({ focus: true }); else button.focus({ preventScroll: true }); return; }
    const list = focusables();
    const at = list.indexOf(document.activeElement);
    const next = overflowStep(e.key, at < 0 ? 0 : at, list.length);
    if (next >= 0 && list[next]) { e.preventDefault(); list[next].focus({ preventScroll: true }); }
  });
  button.addEventListener('keydown', (e) => {
    if ((e.key === 'ArrowDown' || e.key === 'ArrowUp') && !isOpen()) { e.preventDefault(); open(); }
  });
  return api;
}

// revealBeforeFocus opens a closed <details> before any control inside it takes
// focus, so app code that focuses a field to show a validation error (Intruder's
// threads/delay/repeat) never focuses something the user cannot see. Fields
// added later are covered too.
export function revealBeforeFocus(details) {
  if (!details) return;
  const wrap = (node) => {
    if (!node || node._dskReveal || !/^(INPUT|SELECT|TEXTAREA|BUTTON)$/.test(node.tagName)) return;
    node._dskReveal = true;
    const own = node.focus;
    node.focus = function focus(...args) { if (!details.open) details.open = true; return own.apply(this, args); };
  };
  const scan = () => walk(details, []).forEach(wrap);
  scan();
  new MutationObserver(scan).observe(details, { childList: true, subtree: true });
  // A native constraint check (reportValidity) also needs the section open.
  details.addEventListener('invalid', () => { details.open = true; }, true);
}
