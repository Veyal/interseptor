// virtual-list-model.js — pure windowing math for list virtualization, shared by
// core.js's createVirtualList (Proxy history) and createListView (Intruder
// results, WebSocket frames, Scanner issues). No DOM, no imports, so it runs
// under node and the windowing arithmetic is unit-tested directly instead of
// through a browser.
//
// A windowed list renders only the rows near the viewport; `topPad`/`bottomPad`
// are the pixel heights of the unrendered rows above and below, so the scrollbar
// and scroll position stay honest. Below `threshold` rows the list is small
// enough to render whole (returns null — the caller renders everything).

// computeVirtualWindow({total, scrollTop, viewportH, rowH, threshold, buffer})
// returns {start, end, topPad, bottomPad} for a fixed-row-height list, or null
// when the list should not virtualize (too few rows, or a non-positive row
// height). `buffer` is the overscan count added above and below the viewport so
// a fast scroll does not flash blank rows.
export function computeVirtualWindow({ total, scrollTop, viewportH, rowH, threshold = 0, buffer = 0 }) {
  total = Math.max(0, Math.floor(total) || 0);
  rowH = Number(rowH);
  if (!(rowH > 0)) return null;
  if (total < threshold) return null;
  viewportH = Math.max(0, Number(viewportH) || 0);
  scrollTop = Math.max(0, Number(scrollTop) || 0);
  buffer = Math.max(0, Math.floor(buffer) || 0);
  const visible = Math.ceil(viewportH / rowH);
  // Clamp start to maxStart so scrolling to (or past) the end still fills a whole
  // viewport instead of leaving start beyond end with an empty, over-tall window.
  const maxStart = Math.max(0, total - visible);
  const start = Math.max(0, Math.min(Math.floor(scrollTop / rowH) - buffer, maxStart));
  const end = Math.min(total, start + visible + 2 * buffer);
  return { start, end, topPad: start * rowH, bottomPad: Math.max(0, (total - end) * rowH) };
}

// sameWindow(a, b) is true when two windows would mount the identical row slice
// and pads — lets a scroll handler skip a re-render when nothing moved.
export function sameWindow(a, b) {
  if (a === b) return true;
  if (!a || !b) return false;
  return a.start === b.start && a.end === b.end && a.topPad === b.topPad && a.bottomPad === b.bottomPad;
}
