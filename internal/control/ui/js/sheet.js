// sheet.js — BottomSheet: a phone sheet with peek / half / full detents that
// renders as a right-hand drawer at 721px and up. Half and full are modal and use
// core's openModal focus trap (aria-modal, Esc, scrim tap, focus restore); peek
// is non-modal. The handle is a real button: Up/Down (or Enter/Space) cycle the
// detents, and pointer drag on the handle snaps or dismisses. Movement uses
// transform only; reduced motion is handled by the global reduced-motion block.
//
// Sheet ids must be listed in MODAL_IDS (core.js) so shortcut gating sees them.

import { openModal, closeModal } from './core.js';
import { DETENTS, nextDetent, sheetOffsets, resolveSheetDrag, shouldHideDock } from './layout-math.js';

const SVG_NS = 'http://www.w3.org/2000/svg';
const sheets = new Map();
let keyboardWatched = false;

const isDrawer = () => { try { return window.matchMedia('(min-width: 721px)').matches; } catch (e) { return false; } };
const reduced = () => { try { return window.matchMedia('(prefers-reduced-motion: reduce)').matches; } catch (e) { return false; } };

// Mark the document while a soft keyboard is up so the bottom nav can hide.
function watchSoftKeyboard() {
  if (keyboardWatched || !window.visualViewport) return;
  keyboardWatched = true;
  const sync = () => {
    const hidden = shouldHideDock(window.visualViewport.height, window.innerHeight);
    document.documentElement.dataset.softKeyboard = hidden ? 'true' : 'false';
  };
  window.visualViewport.addEventListener('resize', sync);
  sync();
}

function iconNode(name) {
  const svg = document.createElementNS(SVG_NS, 'svg');
  svg.setAttribute('class', 'icon');
  svg.setAttribute('aria-hidden', 'true');
  svg.setAttribute('focusable', 'false');
  const use = document.createElementNS(SVG_NS, 'use');
  use.setAttribute('href', '#i-' + name);
  svg.appendChild(use);
  return svg;
}

function build(id, title) {
  const rootEl = document.createElement('div');
  rootEl.id = id;
  rootEl.className = 'sheet-root';
  rootEl.style.display = 'none';
  const sheet = document.createElement('div');
  sheet.className = 'sheet';
  sheet.setAttribute('role', 'dialog');
  const titleId = id + 'Title';
  sheet.setAttribute('aria-labelledby', titleId);
  const handle = document.createElement('button');
  handle.type = 'button';
  handle.className = 'sheet-handle';
  handle.setAttribute('aria-label', 'Resize panel');
  const bar = document.createElement('span');
  bar.className = 'sheet-grip';
  bar.setAttribute('aria-hidden', 'true');
  handle.appendChild(bar);
  const head = document.createElement('div');
  head.className = 'sheet-head';
  const h = document.createElement('h2');
  h.id = titleId;
  h.className = 'sheet-title';
  h.textContent = title || '';
  const close = document.createElement('button');
  close.type = 'button';
  close.className = 'btn sheet-close';
  close.setAttribute('aria-label', 'Close');
  close.appendChild(iconNode('close'));
  head.append(h, close);
  const body = document.createElement('div');
  body.className = 'sheet-body';
  sheet.append(handle, head, body);
  rootEl.appendChild(sheet);
  document.body.appendChild(rootEl);
  return { rootEl, sheet, handle, close, body, titleEl: h };
}

function fill(body, content) {
  body.textContent = '';
  const v = typeof content === 'function' ? content(body) : content;
  if (v == null || v === body) return;
  if (typeof v === 'string') body.textContent = v;
  else body.appendChild(v);
}

// openSheet({id, title, detents, detent, content, onClose}) -> {el, body, setDetent, close, detent}
// content: a Node, a function(body) that fills or returns a Node, or plain text.
export function openSheet({ id, title = '', detents = DETENTS, detent, content, onClose, opener } = {}) {
  if (!id) throw new Error('openSheet: id required');
  watchSoftKeyboard();
  let entry = sheets.get(id);
  if (entry && entry.open) { entry.titleEl.textContent = title; fill(entry.body, content); entry.onClose = onClose; return entry.api; }
  if (!entry || !entry.rootEl.isConnected) {
    entry = build(id, title);
    sheets.set(id, entry);
  }
  entry.titleEl.textContent = title;
  fill(entry.body, content);
  entry.detents = DETENTS.filter((d) => detents.includes(d));
  entry.onClose = onClose;
  entry.open = true;
  entry.opener = opener || document.activeElement;
  let current = null;
  let dragStart = null;

  const heights = () => sheetOffsets(window.innerHeight);
  const drawer = () => isDrawer();

  function applyDetent(name) {
    const modal = drawer() || name !== 'peek';
    current = drawer() && name === 'peek' ? 'half' : name;
    entry.sheet.dataset.detent = current;
    entry.sheet.setAttribute('aria-modal', modal ? 'true' : 'false');
    entry.rootEl.dataset.modal = modal ? 'true' : 'false';
    entry.rootEl.dataset.presentation = drawer() ? 'drawer' : 'sheet';
    if (modal) {
      openModal(entry.rootEl, { onEscape: close, onDismiss: close, initialFocus: '.sheet-close' });
    } else {
      // Peek is non-modal: release the trap but keep the sheet on screen.
      closeModal(entry.rootEl);
      entry.rootEl.style.display = 'flex';
    }
  }
  function setDetent(name) {
    if (!entry.detents.includes(name)) return;
    applyDetent(name);
  }
  function close() {
    if (!entry.open) return;
    entry.open = false;
    entry.sheet.dataset.detent = 'closed';
    const finish = () => {
      closeModal(entry.rootEl);
      entry.rootEl.style.display = 'none';
      if (entry.opener && entry.opener.isConnected && entry.opener.focus) entry.opener.focus({ preventScroll: true });
      const cb = entry.onClose;
      entry.onClose = null;
      if (cb) cb();
    };
    if (reduced()) finish();
    else {
      let done = false;
      const once = () => { if (done) return; done = true; finish(); };
      entry.sheet.addEventListener('transitionend', once, { once: true });
      window.setTimeout(once, 320);
    }
  }

  // Handle: keyboard alternative to dragging.
  entry.handle.onkeydown = (e) => {
    if (e.key === 'ArrowUp' || e.key === 'ArrowDown') { e.preventDefault(); setDetent(nextDetent(current, e.key === 'ArrowUp' ? 1 : -1, entry.detents)); }
  };
  entry.handle.onclick = () => {
    if (entry.suppressClick) return; // the click that ends a drag is not a tap
    const n = nextDetent(current, 1, entry.detents);
    setDetent(n === current ? entry.detents[0] : n);
  };
  entry.close.onclick = close;
  entry.handle.onpointerdown = (e) => {
    if (drawer()) return;
    try { entry.handle.setPointerCapture(e.pointerId); } catch (err) { /* synthetic pointer */ }
    dragStart = { y: e.clientY, moved: false };
    entry.sheet.classList.add('is-dragging');
  };
  entry.handle.onpointermove = (e) => {
    if (!dragStart) return;
    const dy = e.clientY - dragStart.y;
    if (Math.abs(dy) > 4) dragStart.moved = true;
    entry.sheet.style.setProperty('--sheet-drag', dy + 'px');
  };
  const endDrag = (e) => {
    if (!dragStart) return;
    const dy = e.clientY - dragStart.y;
    const moved = dragStart.moved;
    dragStart = null;
    entry.sheet.classList.remove('is-dragging');
    entry.sheet.style.removeProperty('--sheet-drag');
    if (!moved) return; // a tap is handled by click
    entry.suppressClick = true;
    window.setTimeout(() => { entry.suppressClick = false; }, 0);
    const r = resolveSheetDrag({ detent: current, dy, heights: heights() });
    if (r.action === 'close') close(); else setDetent(r.detent);
  };
  entry.handle.onpointerup = endDrag;
  entry.handle.onpointercancel = endDrag;
  entry.api = {
    el: entry.rootEl, body: entry.body, close, setDetent,
    get detent() { return current; },
    get isOpen() { return entry.open; },
  };
  const initial = detent && entry.detents.includes(detent) ? detent : entry.detents.includes('half') ? 'half' : entry.detents[0];
  // Enter from below: first paint at "closed", then transition to the detent.
  entry.sheet.dataset.detent = 'closed';
  entry.rootEl.style.display = 'flex';
  void entry.sheet.offsetHeight; // commit the closed position so the first detent animates
  applyDetent(initial);
  return entry.api;
}

export function closeSheet(id) {
  const entry = sheets.get(id);
  if (entry && entry.open && entry.api) entry.api.close();
}
