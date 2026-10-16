import test from 'node:test';
import assert from 'node:assert/strict';
import { clamp, splitBounds, clampSplitSize, splitKeyAction, toPercent, fromPercent, parseStoredPercent, DETENTS, nextDetent, resolveSheetDrag, sheetOffsets, shouldHideDock, resolveSplitMode } from '../js/layout-math.js';
import { findMatches } from '../js/finder.js';

test('clamp and percent helpers', () => {
  assert.equal(clamp(5, 0, 3), 3);
  assert.equal(clamp(-1, 0, 3), 0);
  assert.equal(toPercent(300, 1000), 30);
  assert.equal(fromPercent(30, 1000), 300);
  assert.equal(toPercent(1, 0), 0);
});

test('splitBounds keeps both panes at their minimums', () => {
  assert.deepEqual(splitBounds(1000, [240, 320], 8), { min: 240, max: 672 });
  assert.deepEqual(splitBounds(400, [240, 320], 8), { min: 240, max: 240 });
});

test('clampSplitSize enforces bounds', () => {
  assert.equal(clampSplitSize(10, 1000, [240, 320], 8), 240);
  assert.equal(clampSplitSize(900, 1000, [240, 320], 8), 672);
  assert.equal(clampSplitSize(400, 1000, [240, 320], 8), 400);
});

test('splitKeyAction maps keys per orientation with 8px and 64px steps', () => {
  assert.deepEqual(splitKeyAction('ArrowRight', { orientation: 'right', shift: false }), { delta: 8 });
  assert.deepEqual(splitKeyAction('ArrowLeft', { orientation: 'right', shift: true }), { delta: -64 });
  assert.equal(splitKeyAction('ArrowUp', { orientation: 'right' }), null);
  assert.deepEqual(splitKeyAction('ArrowDown', { orientation: 'bottom', shift: false }), { delta: 8 });
  assert.deepEqual(splitKeyAction('ArrowUp', { orientation: 'bottom', shift: true }), { delta: -64 });
  assert.deepEqual(splitKeyAction('Home', { orientation: 'right' }), { to: 'min' });
  assert.deepEqual(splitKeyAction('End', { orientation: 'right' }), { to: 'max' });
  assert.deepEqual(splitKeyAction('Enter', { orientation: 'right' }), { toggle: true });
  assert.equal(splitKeyAction('x', { orientation: 'right' }), null);
});

test('parseStoredPercent is defensive', () => {
  assert.equal(parseStoredPercent('{"v":1,"pct":42}'), 42);
  assert.equal(parseStoredPercent('{"v":1,"pct":420}'), null);
  assert.equal(parseStoredPercent('garbage'), null);
  assert.equal(parseStoredPercent(null), null);
  assert.equal(parseStoredPercent('{"v":2,"pct":40}'), null);
});

test('resolveSplitMode stacks below the breakpoint and picks orientation otherwise', () => {
  assert.equal(resolveSplitMode(700, { orientation: 'right', stackBelow: 720 }), 'stack');
  assert.equal(resolveSplitMode(1000, { orientation: 'right', stackBelow: 720 }), 'right');
  assert.equal(resolveSplitMode(1000, { orientation: 'bottom', stackBelow: 720 }), 'bottom');
  assert.equal(resolveSplitMode(1000, { orientation: 'auto', stackBelow: 720 }), 'bottom');
  assert.equal(resolveSplitMode(1200, { orientation: 'auto', stackBelow: 720 }), 'right');
});

test('nextDetent cycles with Up/Down and clamps at the ends', () => {
  assert.deepEqual(DETENTS, ['peek', 'half', 'full']);
  assert.equal(nextDetent('peek', 1), 'half');
  assert.equal(nextDetent('half', 1), 'full');
  assert.equal(nextDetent('full', 1), 'full');
  assert.equal(nextDetent('full', -1), 'half');
  assert.equal(nextDetent('peek', -1), 'peek');
  assert.equal(nextDetent('half', 1, ['half', 'full']), 'full');
  assert.equal(nextDetent('peek', 1, ['half', 'full']), 'half');
});

test('sheetOffsets derives visible heights from the viewport', () => {
  assert.deepEqual(sheetOffsets(800), { peek: 72, half: 400, full: 800 });
});

test('resolveSheetDrag snaps, dismisses on downward swipe from half, never skips detents upward', () => {
  const h = sheetOffsets(800);
  assert.deepEqual(resolveSheetDrag({ detent: 'half', dy: 120, heights: h }), { action: 'close' });
  assert.deepEqual(resolveSheetDrag({ detent: 'half', dy: 20, heights: h }), { action: 'detent', detent: 'half' });
  assert.deepEqual(resolveSheetDrag({ detent: 'half', dy: -250, heights: h }), { action: 'detent', detent: 'full' });
  assert.deepEqual(resolveSheetDrag({ detent: 'full', dy: 300, heights: h }), { action: 'detent', detent: 'half' });
  assert.deepEqual(resolveSheetDrag({ detent: 'peek', dy: 60, heights: h }), { action: 'close' });
  assert.deepEqual(resolveSheetDrag({ detent: 'peek', dy: -200, heights: h }), { action: 'detent', detent: 'half' });
});

test('shouldHideDock when a soft keyboard shrinks the visual viewport', () => {
  assert.equal(shouldHideDock(500, 800), true);
  assert.equal(shouldHideDock(700, 800), false);
  assert.equal(shouldHideDock(undefined, 800), false);
});

test('findMatches: literal, case, regex, cap and invalid regex', () => {
  assert.deepEqual(findMatches('Foo foo FOO', 'foo', {}).matches.map((m) => m.start), [0, 4, 8]);
  assert.deepEqual(findMatches('Foo foo FOO', 'foo', { caseSensitive: true }).matches.map((m) => m.start), [4]);
  assert.deepEqual(findMatches('a1 b22 c333', '\\d+', { regex: true }).matches.map((m) => [m.start, m.end]), [[1, 2], [4, 6], [8, 11]]);
  assert.equal(findMatches('abc', '(', { regex: true }).error.length > 0, true);
  const capped = findMatches('a'.repeat(6000), 'a', { cap: 5000 });
  assert.equal(capped.matches.length, 5000);
  assert.equal(capped.capped, true);
  assert.deepEqual(findMatches('abc', '', {}).matches, []);
  assert.equal(findMatches('aaa', 'x*', { regex: true }).matches.length, 0, 'zero-length matches are skipped');
});
