// proxy-selection.js: pure selection and gesture helpers for the History table.
// No imports and no DOM access so every function runs under `node --test`.
// Selection is a Set of flow ids (not row indexes), so it survives virtual-list
// windowing, reloads and sort changes; ranges are resolved against the current
// filtered id list rather than the rendered window.

export const BULK_CHUNK = 200;
export const LONG_PRESS_MS = 500;
export const LONG_PRESS_SLOP = 8;

// rangeBetween returns the ids from `a` to `b` inclusive in list order. When the
// anchor is no longer in the list it degrades to the target alone; an unknown
// target selects nothing.
export function rangeBetween(ids, anchorId, targetId) {
  const t = ids.indexOf(targetId);
  if (t < 0) return [];
  const a = ids.indexOf(anchorId);
  if (a < 0) return [targetId];
  return ids.slice(Math.min(a, t), Math.max(a, t) + 1);
}

// applyRowClick mutates `sel` for a click or keyboard step and returns the new
// anchor id. Ctrl/Cmd toggles one row (seeding with the inspected row so the
// first Ctrl-click keeps it), Shift selects the range from the anchor, and a
// plain click clears the multi-selection.
export function applyRowClick(sel, ids, { id, anchorId = null, currentId = null, mod = false, shift = false }) {
  if (mod) {
    if (sel.size === 0 && currentId != null && currentId !== id) sel.add(currentId);
    if (sel.has(id)) sel.delete(id); else sel.add(id);
    return { anchorId: id };
  }
  if (shift) {
    const from = anchorId != null ? anchorId : (currentId != null ? currentId : id);
    sel.clear();
    rangeBetween(ids, from, id).forEach((x) => sel.add(x));
    return { anchorId: id };
  }
  sel.clear();
  return { anchorId: id };
}

// toggleAllIds selects every id of the filtered list (replacing any stray
// selection), or clears when all of them are already selected. Returns whether
// the selection is now "all".
export function toggleAllIds(sel, ids) {
  if (!ids.length) { sel.clear(); return false; }
  const all = ids.every((id) => sel.has(id));
  sel.clear();
  if (all) return false;
  ids.forEach((id) => sel.add(id));
  return true;
}

export function chunkIds(ids, size = BULK_CHUNK) {
  const n = size > 0 ? Math.floor(size) : 1;
  const out = [];
  for (let i = 0; i < ids.length; i += n) out.push(ids.slice(i, i + n));
  return out;
}

export const bulkProgressText = (verb, done, total) => `${verb} ${done} of ${total}`;

// createLongPress is a timer state machine: start() arms it, move() beyond the
// slop or end() disarms it, and onLong fires once after LONG_PRESS_MS. Timer
// functions are injectable for tests.
export function createLongPress({ onLong, ms = LONG_PRESS_MS, slop = LONG_PRESS_SLOP, setTimer = setTimeout, clearTimer = clearTimeout }) {
  let timer = null;
  let ox = 0;
  let oy = 0;
  const cancel = () => { if (timer != null) { clearTimer(timer); timer = null; } };
  return {
    start(x, y) {
      cancel();
      ox = x; oy = y;
      timer = setTimer(() => { timer = null; onLong(); }, ms);
    },
    move(x, y) {
      if (timer != null && Math.hypot(x - ox, y - oy) > slop) cancel();
    },
    end: cancel,
  };
}
