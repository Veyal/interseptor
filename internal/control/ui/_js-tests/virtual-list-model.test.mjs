import test from 'node:test';
import assert from 'node:assert/strict';
import { computeVirtualWindow, sameWindow } from '../js/virtual-list-model.js';

test('returns null below the virtualization threshold', () => {
  assert.equal(computeVirtualWindow({ total: 50, scrollTop: 0, viewportH: 600, rowH: 25, threshold: 200, buffer: 8 }), null);
});

test('returns null for a non-positive row height', () => {
  assert.equal(computeVirtualWindow({ total: 500, scrollTop: 0, viewportH: 600, rowH: 0, threshold: 0 }), null);
  assert.equal(computeVirtualWindow({ total: 500, scrollTop: 0, viewportH: 600, rowH: -5, threshold: 0 }), null);
});

test('computes the window at the top of the list', () => {
  // 600px viewport / 25px rows = 24 visible; +2*buffer(8)=16 overscan.
  const w = computeVirtualWindow({ total: 1000, scrollTop: 0, viewportH: 600, rowH: 25, threshold: 200, buffer: 8 });
  assert.deepEqual(w, { start: 0, end: 40, topPad: 0, bottomPad: (1000 - 40) * 25 });
});

test('windows around a scrolled offset and keeps pads consistent', () => {
  const total = 1000, rowH = 25, buffer = 8;
  const w = computeVirtualWindow({ total, scrollTop: 5000, viewportH: 600, rowH, threshold: 200, buffer });
  // floor(5000/25)=200, minus buffer 8 => start 192.
  assert.equal(w.start, 192);
  assert.equal(w.end, 192 + 24 + 16);
  assert.equal(w.topPad, 192 * rowH);
  assert.equal(w.bottomPad, (total - w.end) * rowH);
  // The three regions always re-sum to the full scroll height.
  assert.equal(w.topPad + (w.end - w.start) * rowH + w.bottomPad, total * rowH);
});

test('clamps the window end to the total and never produces a negative pad', () => {
  const w = computeVirtualWindow({ total: 210, scrollTop: 100000, viewportH: 600, rowH: 25, threshold: 200, buffer: 8 });
  assert.equal(w.end, 210);
  assert.ok(w.bottomPad >= 0);
  assert.ok(w.start <= w.end);
});

test('sameWindow detects equal and differing windows', () => {
  const a = { start: 0, end: 40, topPad: 0, bottomPad: 100 };
  assert.equal(sameWindow(a, a), true);
  assert.equal(sameWindow(a, { ...a }), true);
  assert.equal(sameWindow(a, { ...a, start: 1 }), false);
  assert.equal(sameWindow(a, null), false);
  assert.equal(sameWindow(null, null), true);
});
