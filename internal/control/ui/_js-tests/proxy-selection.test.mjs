import test from 'node:test';
import assert from 'node:assert/strict';
import {
  rangeBetween, applyRowClick, toggleAllIds, chunkIds, bulkProgressText, createLongPress, LONG_PRESS_MS, LONG_PRESS_SLOP,
} from '../js/proxy-selection.js';

const ids = [10, 9, 8, 7, 6, 5];

test('rangeBetween is inclusive, order-independent and falls back to the target', () => {
  assert.deepEqual(rangeBetween(ids, 9, 6), [9, 8, 7, 6]);
  assert.deepEqual(rangeBetween(ids, 6, 9), [9, 8, 7, 6]);
  assert.deepEqual(rangeBetween(ids, 999, 7), [7], 'anchor scrolled out or deleted: only the target');
  assert.deepEqual(rangeBetween(ids, 9, 999), []);
});

test('plain click replaces the selection and moves the anchor', () => {
  const sel = new Set([1, 2]);
  const r = applyRowClick(sel, ids, { id: 8, anchorId: 10, currentId: 10 });
  assert.deepEqual([...sel], []);
  assert.equal(r.anchorId, 8);
});

test('ctrl click seeds with the inspected row then toggles', () => {
  const sel = new Set();
  applyRowClick(sel, ids, { id: 8, anchorId: 10, currentId: 10, mod: true });
  assert.deepEqual([...sel].sort(), [10, 8]);
  applyRowClick(sel, ids, { id: 8, anchorId: 8, currentId: 8, mod: true });
  assert.deepEqual([...sel], [10]);
});

test('shift click selects the range from the anchor id, not a stale index', () => {
  const sel = new Set();
  const r = applyRowClick(sel, ids, { id: 6, anchorId: 9, currentId: 9, shift: true });
  assert.deepEqual([...sel].sort((a, b) => a - b), [6, 7, 8, 9]);
  assert.equal(r.anchorId, 6);
});

test('selection is keyed by id, so rows outside the rendered window stay selected', () => {
  const sel = new Set([5, 6, 7]);
  const window = [10, 9, 8];
  applyRowClick(sel, [...window, 7, 6, 5], { id: 9, anchorId: 9, currentId: 9, mod: true });
  assert.ok(sel.has(5) && sel.has(6) && sel.has(7) && sel.has(9));
});

test('toggleAllIds selects every filtered id, not just the rendered window, then clears', () => {
  const sel = new Set([1]);
  const filtered = Array.from({ length: 1000 }, (_, i) => i + 100);
  assert.equal(toggleAllIds(sel, filtered), true);
  assert.equal(sel.size, 1000, 'previous stray selection is replaced by the filtered set');
  assert.equal(toggleAllIds(sel, filtered), false);
  assert.equal(sel.size, 0);
  assert.equal(toggleAllIds(sel, []), false);
});

test('chunkIds splits into bounded batches', () => {
  assert.deepEqual(chunkIds([1, 2, 3, 4, 5], 2), [[1, 2], [3, 4], [5]]);
  assert.equal(chunkIds(Array.from({ length: 450 }, (_, i) => i)).length, 3, 'default chunk is 200');
  assert.deepEqual(chunkIds([], 200), []);
  assert.deepEqual(chunkIds([1], 0), [[1]], 'a non-positive size never loops forever');
});

test('bulkProgressText is plain announcement text', () => {
  assert.equal(bulkProgressText('Deleting', 200, 450), 'Deleting 200 of 450');
  assert.equal(bulkProgressText('Deleting', 450, 450), 'Deleting 450 of 450');
});

test('long press fires after 500ms, cancels on movement beyond 8px or release', () => {
  assert.equal(LONG_PRESS_MS, 500);
  assert.equal(LONG_PRESS_SLOP, 8);
  let timers = [], fired = 0, nextId = 1;
  const mk = () => createLongPress({
    onLong: () => { fired++; },
    setTimer: (fn, ms) => { const t = { id: nextId++, fn, ms }; timers.push(t); return t.id; },
    clearTimer: (id) => { timers = timers.filter((t) => t.id !== id); },
  });
  let lp = mk();
  lp.start(10, 10);
  assert.equal(timers[0].ms, 500);
  lp.move(14, 12);
  assert.equal(timers.length, 1, 'small jitter keeps the press alive');
  timers[0].fn();
  assert.equal(fired, 1);
  lp = mk(); timers = [];
  lp.start(0, 0); lp.move(20, 0);
  assert.equal(timers.length, 0, 'moving past the slop cancels');
  lp.start(0, 0); lp.end();
  assert.equal(timers.length, 0, 'release cancels');
  assert.equal(fired, 1);
});
