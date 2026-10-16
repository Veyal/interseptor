// windowed-list.js — the DOM glue the long selectable lists (Findings, Scanner
// issues) put around core.js's createListView.
//
// createListView mounts only the rows near the viewport and delegates events, but a
// list you can select from and drive with the keyboard needs more once most of its
// rows are not in the DOM:
//   - the window must follow the container's size, not only its scroll position
//     (a tab shown after load, a pane resized, a phone rotated);
//   - exactly one mounted row must always be a tab stop, or Tab skips the list when
//     the selected row has scrolled out of the window;
//   - a focused row that scrolls out of the window (or is rebuilt by a refresh)
//     must not drop focus to <body>, which would strand a keyboard user;
//   - a row that is not mounted must be scrollable into view before it can be
//     focused.
// Row height comes from the stylesheet: while the list is virtualized (.vlist) it
// sets one explicit height on every row (group headers included), and the first
// mounted row is measured once so the window math cannot disagree with the layout.
// Nothing here reads layout on the scroll path after the first paint.

import { createListView } from './core.js';
import { revealScrollTop } from './list-rows-model.js';

// createWindowedList(options) wraps createListView(options) and returns
// {view, container, render, refresh, suspend, isVirtual, rowHeight, reveal, destroy}.
//
// Everything createListView takes is forwarded (container, rowSelector, renderRow,
// getItems, on, threshold, buffer) except `rowHeight`, which here is the *initial
// guess* in px, and `containerClass`, which defaults to 'vlist'. Extra options:
//   keyOf(rowEl)            stable identity of a row element, to restore its focus
//                           (a key that survives a re-sort: not a position)
//   findRow(key)            the mounted row element for a key, or null
//   indexOf(rowEl)          the row's position in getItems(), so a list whose open
//                           row is not mounted puts its one tab stop on the first row
//                           in the viewport instead of an overscan row above it
//   topInset()              px at the top of the viewport covered by a pinned header
//   onWindow(win, scrollTop) runs after every mount of a window (scroll, resize,
//                           render); scrollTop was read before the DOM changed
//   onContainerKey(e)       keydown whose target is the container itself — where
//                           focus is parked when its row can no longer be mounted
export function createWindowedList(options) {
  const {
    container, rowSelector, keyOf = (row) => row.dataset.id || '', findRow = null,
    indexOf = null, topInset = null, onWindow = null, onContainerKey = null,
  } = options;
  const guess = Number(options.rowHeight) > 0 ? Number(options.rowHeight) : 40;
  let measured = 0;
  let calibrated = false;
  let destroyed = false;
  let suspended = false; // the owner has painted its own markup (empty, error) into the container
  let frame = 0;
  let resizeFrame = 0;
  let pendingKey; // focused row key captured when a scroll began; undefined = none captured
  const rowHeight = () => measured || guess;

  const view = createListView({
    ...options,
    containerClass: options.containerClass === undefined ? 'vlist' : options.containerClass,
    rowHeight,
  });

  // The container is the focus fallback, never a tab stop of its own.
  if (!container.hasAttribute('tabindex')) container.tabIndex = -1;

  // The key of the row that has focus; '' when focus is on some other control inside the list (an
  // empty state's "Clear filters"); null when it is elsewhere or on the container itself.
  function focusedKey() {
    const active = document.activeElement;
    if (!active || active === container || !container.contains(active)) return null;
    const row = active.closest(rowSelector);
    return row ? keyOf(row) : '';
  }

  // A tab stop must survive the window moving off the open row. The open row carries it in the
  // owner's markup while it is mounted. When it is not, a row that has focus keeps it (a rebuilt
  // row comes back at tabindex -1, and Tab would otherwise walk on to a second stop a few rows
  // down instead of leaving the list); failing that it lands on the first row actually in the
  // viewport, clear of a pinned header: the first mounted row can be one of up to `buffer`
  // overscan rows above it, and Tab would scroll the list back to reach it and leave the keyboard
  // on a row nobody chose.
  function ensureTabStop(scrollTop) {
    const rows = container.querySelectorAll(rowSelector);
    if (!rows.length) return;
    for (const row of rows) if (row.getAttribute('tabindex') === '0') return;
    const active = document.activeElement;
    const focused = active && container.contains(active) ? active.closest(rowSelector) : null;
    let stop = focused || rows[0];
    if (!focused && indexOf && view.isVirtual() && rowHeight() > 0) {
      const first = Math.ceil(((Number(scrollTop) || 0) + (topInset ? topInset() : 0)) / rowHeight());
      stop = Array.prototype.find.call(rows, (row) => indexOf(row) >= first) || stop;
    }
    stop.setAttribute('tabindex', '0');
  }

  // Adopt the laid-out row height once (and again after a resize): a measurement
  // forces layout, so the scroll path never does it. Only a virtualized list is
  // measured -- the stylesheet fixes its rows (.vlist), while a short list keeps
  // natural rows that may differ in height. A hidden list measures 0 and is retried
  // when it is next shown. Returns true when the window must repaint.
  function calibrate() {
    if (calibrated || !view.isVirtual()) return false;
    const row = container.querySelector(rowSelector);
    if (!row) return false;
    const h = row.getBoundingClientRect().height;
    if (!(h > 0)) return false;
    calibrated = true;
    if (Math.abs(h - rowHeight()) <= 0.5) return false;
    measured = h;
    return true;
  }

  // Put focus back after the DOM it was in was rebuilt: on the same row if it is still mounted,
  // else (a row that scrolled out) on the container so the keys keep working, else (a control that
  // is not a row, replaced by rows) on the open row.
  function restoreFocus(key) {
    if (key == null) return;
    const active = document.activeElement;
    if (active && active !== document.body) return; // focus is somewhere live already
    const row = key && findRow ? findRow(key) : null;
    const fallback = key ? container : (container.querySelector(rowSelector + '[tabindex="0"]') || container);
    (row || fallback).focus({ preventScroll: true });
  }

  // settle runs after any window change: adopt the real row height, put focus back, keep exactly
  // one tab stop (around wherever focus is), and tell the owner.
  function settle(key, scrollTop) {
    if (calibrate()) view.render();
    restoreFocus(key);
    ensureTabStop(scrollTop);
    if (onWindow) onWindow(api, scrollTop);
  }

  function run(change) {
    if (destroyed) return;
    const key = focusedKey();
    const top = container.scrollTop;
    change();
    settle(key, top);
  }

  // One passive scroll listener. createListView re-windows on a frame callback of
  // its own; ours repeats that (a no-op when the window already moved) and then
  // settles, so the post-window work runs whichever callback fires first. The
  // focused row and the offset are read here, before any DOM write can drop or
  // move them.
  function onScroll() {
    if (destroyed || suspended || !view.isVirtual()) return;
    if (pendingKey === undefined) pendingKey = focusedKey();
    if (frame) return;
    frame = requestAnimationFrame(() => {
      frame = 0;
      const key = pendingKey;
      pendingKey = undefined;
      if (destroyed || suspended) return;
      const top = container.scrollTop;
      view.refresh();
      settle(key, top);
    });
  }
  container.addEventListener('scroll', onScroll, { passive: true });

  // A pane that is shown, resized or re-laid-out changes how many rows fit. Deferred
  // to a frame so re-windowing never resizes anything inside the observer callback.
  let observer = null;
  if (typeof ResizeObserver === 'function') {
    observer = new ResizeObserver(() => {
      if (resizeFrame) return;
      resizeFrame = requestAnimationFrame(() => {
        resizeFrame = 0;
        if (destroyed || suspended || !container.isConnected) return;
        // A hidden list (another tab, the phone's detail view) measures 0px: keep the window it had,
        // so its rows are still mounted when it is shown again and this fires once more.
        if (!container.clientHeight) return;
        // A short list (not virtualized) renders every row, so a resize changes nothing, and
        // createListView.refresh() would answer by repainting all of them.
        if (!view.isVirtual()) return;
        calibrated = false;
        run(() => view.refresh());
      });
    });
    observer.observe(container);
  }

  const onKey = (event) => {
    if (event.target === container && onContainerKey) onContainerKey(event);
  };
  container.addEventListener('keydown', onKey);

  const api = {
    view,
    container,
    rowHeight,
    isVirtual: () => view.isVirtual(),
    // render() repaints from the current items; refresh() only re-windows (and does
    // nothing for a short list, which has no window).
    render: () => { suspended = false; run(() => view.render()); },
    refresh: () => { if (!suspended && view.isVirtual()) run(() => view.refresh()); },
    // suspend() lets the caller paint its own empty or error markup into the
    // container. createListView alone cannot tell that from "a short list, render
    // everything", so a scroll or resize afterwards would repaint stale rows over it;
    // nothing here repaints until the next render().
    suspend: () => { suspended = true; view.suspend(); },
    // reveal(rowIndex, {align, inset}) scrolls the container so the row is visible
    // (see revealScrollTop) and reports whether it moved. The caller re-windows with
    // render(), which reads the new scrollTop at once. A virtualized list knows every
    // row's position (index × row height); a list with every row mounted may have rows
    // of different heights (short sticky headers), so its row is measured instead.
    reveal(rowIndex, { align = 'nearest', inset = 0 } = {}) {
      const before = container.scrollTop;
      const viewportH = container.clientHeight;
      let where;
      if (view.isVirtual()) {
        const items = options.getItems ? options.getItems() : null;
        where = { rowIndex, rowH: rowHeight(), count: items ? items.length : 0 };
      } else {
        const row = container.children[rowIndex];
        if (!row) return false;
        const box = row.getBoundingClientRect();
        where = { rowTop: box.top - container.getBoundingClientRect().top + before, rowH: box.height, maxTop: container.scrollHeight - viewportH };
      }
      const next = revealScrollTop({ ...where, scrollTop: before, viewportH, topInset: inset, align });
      if (Math.abs(next - before) < 0.5) return false;
      container.scrollTop = next;
      return true;
    },
    destroy() {
      destroyed = true;
      container.removeEventListener('scroll', onScroll);
      container.removeEventListener('keydown', onKey);
      if (observer) observer.disconnect();
      if (frame) cancelAnimationFrame(frame);
      if (resizeFrame) cancelAnimationFrame(resizeFrame);
      view.destroy();
    },
  };
  return api;
}
