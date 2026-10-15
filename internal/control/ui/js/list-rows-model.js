// list-rows-model.js — pure row, keyboard and sticky-header math for the windowed
// Findings and Scanner lists. No DOM and no imports, so it runs under node.
//
// A windowed list (core.js createListView, built on virtual-list-model.js) mounts
// a slice of ONE flat list of fixed-height rows. A grouped list therefore folds its
// group headers into that flat list as rows of the same height, which leaves three
// index spaces to translate between: an item's position, its row, and a pixel
// offset. These helpers do that translation, and keep keyboard movement and the
// pinned group header correct when the row being moved to is not in the DOM.

// buildRows(items, groupOf) → {rows, itemRow, groupRows}.
//   rows      [{kind:'item', item, index} | {kind:'group', key, count}] in display order
//   itemRow   itemRow[k] is the row index of item k (headers skipped)
//   groupRows ascending row indexes of the group headers
// With a groupOf(item) → key function, a header row precedes each run of equal
// keys and carries that key's total count; without one the list is only items.
export function buildRows(items, groupOf) {
  const list = Array.isArray(items) ? items : [];
  const rows = [];
  const itemRow = [];
  const groupRows = [];
  let counts = null;
  if (typeof groupOf === 'function') {
    counts = new Map();
    for (const item of list) {
      const key = groupOf(item);
      counts.set(key, (counts.get(key) || 0) + 1);
    }
  }
  let current;
  let started = false;
  list.forEach((item, index) => {
    if (counts) {
      const key = groupOf(item);
      if (!started || key !== current) {
        groupRows.push(rows.length);
        rows.push({ kind: 'group', key, count: counts.get(key) });
        current = key;
        started = true;
      }
    }
    itemRow.push(rows.length);
    rows.push({ kind: 'item', item, index });
  });
  return { rows, itemRow, groupRows };
}

// stepIndex({current, count, key, page, wrap}) is the item a navigation key moves
// to, or -1 when `key` is not one (so the caller leaves the event alone). Arrow keys
// clamp at the ends unless `wrap`; Page keys always clamp and move `page` items.
// An unknown `current` (-1) behaves as "before the first item".
export function stepIndex({ current, count, key, page = 1, wrap = false }) {
  const n = Math.floor(count) || 0;
  if (n <= 0) return -1;
  const cur = Number.isFinite(current) ? Math.floor(current) : -1;
  const pg = Math.max(1, Math.floor(page) || 1);
  switch (key) {
    case 'ArrowDown':
      return wrap ? (cur + 1) % n : Math.min(n - 1, cur + 1);
    case 'ArrowUp':
      if (cur < 0) return 0;
      return wrap ? (cur - 1 + n) % n : Math.max(0, cur - 1);
    case 'Home':
      return 0;
    case 'End':
      return n - 1;
    case 'PageDown':
      return Math.min(n - 1, cur + pg);
    case 'PageUp':
      return Math.max(0, cur - pg);
    default:
      return -1;
  }
}

// revealScrollTop({rowIndex | rowTop, rowH, scrollTop, viewportH, count | maxTop, topInset,
// align}) is the scrollTop that brings a row fully into view, or the current one when it
// already is (or the viewport is hidden). A windowed list's rows all share one height, so
// `rowIndex` + `count` are enough; a list whose rows are all mounted may differ in height
// (short headers), so it passes the measured `rowTop` and `maxTop` instead. `topInset` is
// the height of a pinned header that covers the top of the viewport. `align`:
//   'nearest' (default) the shortest scroll that exposes the row;
//   'center'  as 'nearest' for a row that is already partly visible, otherwise the
//             row is centred in the visible band, so a far jump lands mid-screen.
// The result is clamped to the scrollable range.
export function revealScrollTop({ rowIndex, rowTop, rowH, scrollTop, viewportH, count, maxTop, topInset = 0, align = 'nearest' }) {
  const h = Number(rowH);
  const view = Number(viewportH);
  const cur = Math.max(0, Number(scrollTop) || 0);
  const top = Number.isFinite(rowTop) ? rowTop : rowIndex * h;
  if (!(h > 0) || !(view > 0) || !Number.isFinite(top) || top < 0) return cur;
  const limit = Number.isFinite(maxTop) ? Math.max(0, maxTop) : Math.max(0, Math.max(0, Math.floor(count) || 0) * h - view);
  const inset = Math.min(Math.max(0, Number(topInset) || 0), Math.max(0, view - h));
  const bottom = top + h;
  const bandTop = cur + inset;
  const bandBottom = cur + view;
  if (top >= bandTop && bottom <= bandBottom) return cur;
  const overlaps = bottom > bandTop && top < bandBottom;
  let next;
  if (align === 'center' && !overlaps) next = top - inset - (view - inset - h) / 2;
  else if (top < bandTop) next = top - inset;
  else next = bottom - view;
  return Math.min(limit, Math.max(0, next));
}

// stickyState({groupRows, scrollTop, rowH, stickyH}) describes the group header a
// windowed list must draw pinned to its top: {group, groupIndex, offset}, or null
// when nothing extra is needed. `group` is the header's row index, `offset` (≤ 0)
// is how far the next header has pushed it up. Mounting only a window of rows means
// the current group's own header is usually not in the DOM, so a native
// position:sticky cannot do this; the caller draws one element from this state.
// While a header is exactly at the top its inline row is the bar, so null.
export function stickyState({ groupRows, scrollTop, rowH, stickyH = rowH }) {
  const h = Number(rowH);
  if (!(h > 0) || !Array.isArray(groupRows) || !groupRows.length) return null;
  const top = Math.max(0, Number(scrollTop) || 0);
  const topRow = Math.floor(top / h);
  let lo = 0;
  let hi = groupRows.length - 1;
  let at = -1;
  while (lo <= hi) {
    const mid = (lo + hi) >> 1;
    if (groupRows[mid] <= topRow) { at = mid; lo = mid + 1; } else hi = mid - 1;
  }
  if (at < 0) return null;
  const group = groupRows[at];
  if (top <= group * h) return null;
  let offset = 0;
  if (at + 1 < groupRows.length) {
    const gap = groupRows[at + 1] * h - top;
    if (gap < stickyH) offset = gap - stickyH;
  }
  return { group, groupIndex: at, offset };
}
