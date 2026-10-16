import test from 'node:test';
import assert from 'node:assert/strict';
import { buildRows, stepIndex, revealScrollTop, stickyState } from '../js/list-rows-model.js';
import { computeVirtualWindow } from '../js/virtual-list-model.js';

const sev = (s) => ({ severity: s });
const SORTED = [sev('critical'), sev('critical'), sev('high'), sev('low'), sev('low'), sev('low')];

test('buildRows without grouping is one item row per item', () => {
  const m = buildRows([{ id: 1 }, { id: 2 }, { id: 3 }]);
  assert.equal(m.rows.length, 3);
  assert.deepEqual(m.itemRow, [0, 1, 2]);
  assert.deepEqual(m.groupRows, []);
  assert.ok(m.rows.every((r) => r.kind === 'item'));
  assert.deepEqual(buildRows(null), { rows: [], itemRow: [], groupRows: [] });
});

test('buildRows puts one header row before each run and counts the whole group', () => {
  const m = buildRows(SORTED, (f) => f.severity);
  assert.deepEqual(m.rows.map((r) => (r.kind === 'group' ? 'H:' + r.key + ':' + r.count : 'i' + r.index)), [
    'H:critical:2', 'i0', 'i1', 'H:high:1', 'i2', 'H:low:3', 'i3', 'i4', 'i5',
  ]);
  assert.deepEqual(m.groupRows, [0, 3, 5]);
  // itemRow maps an item's position to its row, headers skipped.
  assert.deepEqual(m.itemRow, [1, 2, 4, 6, 7, 8]);
  assert.equal(m.rows[m.itemRow[2]].item, SORTED[2]);
});

test('buildRows keeps a header even when the group key is falsy', () => {
  const m = buildRows([{ k: '' }, { k: '' }], (x) => x.k);
  assert.deepEqual(m.groupRows, [0]);
  assert.equal(m.rows[0].count, 2);
});

test('stepIndex clamps at the ends by default and wraps when asked', () => {
  const at = (key, current, opts = {}) => stepIndex({ current, count: 10, key, page: 4, ...opts });
  assert.equal(at('ArrowDown', 3), 4);
  assert.equal(at('ArrowDown', 9), 9);
  assert.equal(at('ArrowUp', 0), 0);
  assert.equal(at('ArrowUp', 5), 4);
  assert.equal(at('Home', 7), 0);
  assert.equal(at('End', 2), 9);
  assert.equal(at('PageDown', 3), 7);
  assert.equal(at('PageDown', 8), 9);
  assert.equal(at('PageUp', 6), 2);
  assert.equal(at('PageUp', 1), 0);
  assert.equal(at('ArrowDown', 9, { wrap: true }), 0);
  assert.equal(at('ArrowUp', 0, { wrap: true }), 9);
  assert.equal(at('a', 3), -1, 'a key that is not navigation is left alone');
  assert.equal(at('ArrowDown', 3, { count: 0 }), -1, 'an empty list has nowhere to go');
});

test('stepIndex from an unknown position lands on the first row', () => {
  assert.equal(stepIndex({ current: -1, count: 5, key: 'ArrowDown' }), 0);
  assert.equal(stepIndex({ current: -1, count: 5, key: 'ArrowUp' }), 0);
  assert.equal(stepIndex({ current: -1, count: 5, key: 'ArrowDown', wrap: true }), 0);
  assert.equal(stepIndex({ current: NaN, count: 5, key: 'PageDown', page: 3 }), 2);
});

test('revealScrollTop leaves a fully visible row alone', () => {
  const base = { rowH: 50, viewportH: 500, count: 100 };
  assert.equal(revealScrollTop({ ...base, rowIndex: 4, scrollTop: 100 }), 100);
  assert.equal(revealScrollTop({ ...base, rowIndex: 2, scrollTop: 100 }), 100, 'top edge exactly visible');
  assert.equal(revealScrollTop({ ...base, rowIndex: 11, scrollTop: 100 }), 100, 'bottom edge exactly visible');
});

test('revealScrollTop scrolls the minimum distance for the nearest edge', () => {
  const base = { rowH: 50, viewportH: 500, count: 100 };
  assert.equal(revealScrollTop({ ...base, rowIndex: 1, scrollTop: 100 }), 50, 'row above: align its top');
  assert.equal(revealScrollTop({ ...base, rowIndex: 12, scrollTop: 100 }), 150, 'row below: align its bottom');
});

test('revealScrollTop keeps a row clear of a sticky header', () => {
  const base = { rowH: 50, viewportH: 500, count: 100, topInset: 50 };
  // A 50px sticky bar starts the visible band at scrollTop+50, so row 2 (y=100..150) hides beneath it.
  assert.equal(revealScrollTop({ ...base, rowIndex: 2, scrollTop: 100 }), 50);
  assert.equal(revealScrollTop({ ...base, rowIndex: 3, scrollTop: 100 }), 100, 'just below the bar is visible');
});

test('revealScrollTop clamps to the scrollable range', () => {
  const base = { rowH: 50, viewportH: 500, count: 20 };
  assert.equal(revealScrollTop({ ...base, rowIndex: 0, scrollTop: 400 }), 0);
  assert.equal(revealScrollTop({ ...base, rowIndex: 19, scrollTop: 0 }), 500, 'maxTop = 20*50 - 500');
  assert.equal(revealScrollTop({ ...base, rowIndex: 19, scrollTop: 0, align: 'center' }), 500, 'the last row cannot centre past the end');
});

test('revealScrollTop centre mode only moves a row that is not fully visible', () => {
  const base = { rowH: 50, viewportH: 500, count: 1000, align: 'center' };
  assert.equal(revealScrollTop({ ...base, rowIndex: 6, scrollTop: 100 }), 100);
  // Far away: centred in the viewport.
  assert.equal(revealScrollTop({ ...base, rowIndex: 500, scrollTop: 0 }), 500 * 50 - (500 - 50) / 2);
  // A partly visible row takes the short nearest-edge move instead of a jump to the centre.
  assert.equal(revealScrollTop({ ...base, rowIndex: 12, scrollTop: 125 }), 150);
  assert.equal(revealScrollTop({ ...base, rowIndex: 1, scrollTop: 75 }), 50);
});

test('revealScrollTop takes measured geometry for a list whose rows are not all one height', () => {
  // Fully mounted short list: headers are 24px, rows 50px, so a row's top is not index*height.
  const base = { rowH: 50, viewportH: 200, scrollTop: 0, maxTop: 300, topInset: 24 };
  assert.equal(revealScrollTop({ ...base, rowTop: 174, count: 0 }), 24, 'a row below the viewport scrolls its bottom to the edge');
  assert.equal(revealScrollTop({ ...base, rowTop: 60, count: 0 }), 0, 'already visible under the 24px header');
  assert.equal(revealScrollTop({ ...base, rowTop: 10, scrollTop: 100, count: 0 }), 0, 'above: its top clears the header');
  assert.equal(revealScrollTop({ ...base, rowTop: 900, count: 0 }), 300, 'maxTop clamps instead of count*rowH');
});

test('revealScrollTop ignores a hidden or degenerate viewport', () => {
  assert.equal(revealScrollTop({ rowIndex: 40, rowH: 50, viewportH: 0, scrollTop: 30, count: 100 }), 30);
  assert.equal(revealScrollTop({ rowIndex: 40, rowH: 0, viewportH: 500, scrollTop: 30, count: 100 }), 30);
  assert.equal(revealScrollTop({ rowIndex: -1, rowH: 50, viewportH: 500, scrollTop: 30, count: 100 }), 30);
});

// Rows: header 0, items 1..10, header 11, items 12..14 (row height 50).
const GROUPS = [0, 11];

test('stickyState is empty until the first header has scrolled away', () => {
  assert.equal(stickyState({ groupRows: GROUPS, scrollTop: 0, rowH: 50 }), null);
  assert.equal(stickyState({ groupRows: [], scrollTop: 400, rowH: 50 }), null, 'an ungrouped list has no header');
  assert.equal(stickyState({ groupRows: [3], scrollTop: 100, rowH: 50 }), null, 'rows before the first header');
});

test('stickyState pins the current group header while its rows scroll', () => {
  assert.deepEqual(stickyState({ groupRows: GROUPS, scrollTop: 1, rowH: 50 }), { group: 0, groupIndex: 0, offset: 0 });
  assert.deepEqual(stickyState({ groupRows: GROUPS, scrollTop: 300, rowH: 50 }), { group: 0, groupIndex: 0, offset: 0 });
});

test('stickyState lets the next header push the pinned one up', () => {
  // Next header sits at y=550. At scrollTop 520 it is 30px below the top, so the bar loses 20px.
  assert.deepEqual(stickyState({ groupRows: GROUPS, scrollTop: 520, rowH: 50 }), { group: 0, groupIndex: 0, offset: -20 });
  assert.deepEqual(stickyState({ groupRows: GROUPS, scrollTop: 549, rowH: 50 }), { group: 0, groupIndex: 0, offset: -49 });
});

test('stickyState hands over to the next group without a gap or a duplicate', () => {
  // At exactly the next header the inline header is the bar: nothing extra is drawn.
  assert.equal(stickyState({ groupRows: GROUPS, scrollTop: 550, rowH: 50 }), null);
  assert.deepEqual(stickyState({ groupRows: GROUPS, scrollTop: 551, rowH: 50 }), { group: 11, groupIndex: 1, offset: 0 });
  assert.deepEqual(stickyState({ groupRows: GROUPS, scrollTop: 9999, rowH: 50 }), { group: 11, groupIndex: 1, offset: 0 });
});

test('stickyState tolerates a degenerate row height', () => {
  assert.equal(stickyState({ groupRows: GROUPS, scrollTop: 300, rowH: 0 }), null);
});

test('a windowed grouped list mounts a bounded number of rows however many findings exist', () => {
  const findings = [];
  for (let i = 0; i < 20000; i++) findings.push(sev(['critical', 'high', 'medium', 'low', 'info'][Math.floor(i / 4000)]));
  const m = buildRows(findings, (f) => f.severity);
  assert.equal(m.rows.length, 20000 + 5);
  const viewportH = 600, rowH = 50, buffer = 8;
  for (const scrollTop of [0, 12345, 250000, 20005 * 50]) {
    const w = computeVirtualWindow({ total: m.rows.length, scrollTop, viewportH, rowH, threshold: 150, buffer });
    assert.ok(w, 'a long list virtualizes');
    assert.ok(w.end - w.start <= Math.ceil(viewportH / rowH) + 2 * buffer, 'mounted rows stay bounded: ' + (w.end - w.start));
    assert.equal(w.topPad + (w.end - w.start) * rowH + w.bottomPad, m.rows.length * rowH);
  }
});

test('revealing a far finding puts its row inside the mounted window', () => {
  const findings = [];
  for (let i = 0; i < 5000; i++) findings.push(sev(i < 2500 ? 'high' : 'low'));
  const m = buildRows(findings, (f) => f.severity);
  const viewportH = 600, rowH = 50, buffer = 8;
  for (const item of [0, 1, 2499, 2500, 4999]) {
    const row = m.itemRow[item];
    const scrollTop = revealScrollTop({ rowIndex: row, rowH, scrollTop: 0, viewportH, count: m.rows.length, topInset: rowH, align: 'center' });
    const w = computeVirtualWindow({ total: m.rows.length, scrollTop, viewportH, rowH, threshold: 150, buffer });
    assert.ok(row >= w.start && row < w.end, `item ${item} (row ${row}) must be mounted in [${w.start}, ${w.end})`);
    // ...and visible below the sticky bar.
    assert.ok(row * rowH >= scrollTop + rowH || scrollTop === 0, 'not hidden under the sticky bar');
  }
});
