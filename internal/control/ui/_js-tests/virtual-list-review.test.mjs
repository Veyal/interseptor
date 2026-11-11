// Review regression for core.js createVirtualList (the History list's window).
// When the list shrinks while it is scrolled deep (a bulk delete or a reload
// that returns fewer rows), scrollTop still points past the new end. The window
// then started beyond the last row: no rows were mounted under a top pad taller
// than the whole list. Each frame the browser clamped scrollTop to that pad and
// the window moved up by one viewport plus the overscan (60 rows at the History
// settings), so 5000 -> 300 rows left the list blank for about 77 frames.
import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';

const src = fs.readFileSync(new URL('../js/core.js', import.meta.url), 'utf8');
const start = src.indexOf('export function createVirtualList(');
const end = src.indexOf('// createDensityVirtualList', start);
assert.ok(start > 0 && end > start, 'core.js keeps createVirtualList');
const createVirtualList = new Function(src.slice(start, end).replace('export ', '') + '\nreturn createVirtualList;')();
globalThis.requestAnimationFrame = (fn) => fn();

function list({ scrollTop, clientHeight = 600 }) {
  const container = { scrollTop, clientHeight, addEventListener() {} };
  return createVirtualList({ container, itemHeight: 30, threshold: 120, buffer: 40, onScroll() {} });
}

test('a list that shrank under a deep scroll position still mounts its last rows', () => {
  const total = 300; // was 5000 rows, scrolled to the bottom
  const win = list({ scrollTop: 4980 * 30 }).computeWindow(total);
  assert.ok(win.end > win.start, 'rows are mounted: ' + JSON.stringify(win));
  assert.equal(win.end, total);
  assert.equal(win.start, total - Math.ceil(600 / 30), 'the window ends at the last row');
  assert.ok(win.topPad + (win.end - win.start) * 30 + win.bottomPad === total * 30, 'pads and rows add up to the list height');
});

test('an ordinary window is unchanged', () => {
  assert.deepEqual(list({ scrollTop: 0 }).computeWindow(1000), { start: 0, end: 100, topPad: 0, bottomPad: 900 * 30 });
  assert.deepEqual(list({ scrollTop: 3000 }).computeWindow(1000), { start: 60, end: 160, topPad: 1800, bottomPad: 840 * 30 });
  // Scrolled exactly to the bottom: the overscan above stays, nothing below.
  const bottom = list({ scrollTop: 1000 * 30 - 600 }).computeWindow(1000);
  assert.deepEqual(bottom, { start: 940, end: 1000, topPad: 940 * 30, bottomPad: 0 });
  assert.equal(list({ scrollTop: 0 }).computeWindow(50), null, 'below the threshold the list is not windowed');
});
