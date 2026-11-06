// diff.js change navigation (n / N over change blocks). The cursor starts at -1
// ("nothing focused yet"); the old `(cursor + delta + count) % count` started a
// first *previous* step at count-2, so pressing N on a fresh diff never reached
// the newest change without wrapping the whole way round.
import test from 'node:test';
import assert from 'node:assert/strict';
import { stepChange, diffLines, changeStarts } from '../js/diff.js';

test('next from nothing focuses the first change, previous the last', () => {
  assert.equal(stepChange(-1, 1, 3), 0);
  assert.equal(stepChange(-1, -1, 3), 2, 'the last change, not the second to last');
  assert.equal(stepChange(-1, -1, 1), 0);
  assert.equal(stepChange(-1, 1, 1), 0);
});

test('stepping walks and wraps in both directions', () => {
  assert.equal(stepChange(0, 1, 3), 1);
  assert.equal(stepChange(2, 1, 3), 0, 'wraps forward');
  assert.equal(stepChange(0, -1, 3), 2, 'wraps backward');
  assert.equal(stepChange(1, -1, 3), 0);
});

test('a stale or empty cursor recovers instead of returning garbage', () => {
  assert.equal(stepChange(9, 1, 3), 0, 'cursor past the end restarts at the first change');
  assert.equal(stepChange(9, -1, 3), 2);
  assert.equal(stepChange(-5, 1, 3), 0);
  assert.equal(stepChange(1, 1, 0), -1, 'no changes: nothing to focus');
  assert.equal(stepChange(1, -1, -3), -1);
  assert.equal(stepChange(NaN, 1, 2), 0);
});

test('a first N on a real diff lands on the last change block', () => {
  const a = ['one', 'two', 'three', 'four', 'five'];
  const b = ['one', 'TWO', 'three', 'four', 'FIVE'];
  const r = diffLines(a, b);
  const blocks = changeStarts(r.ops);
  assert.equal(blocks.length, 2);
  // What createDiffView.go() does with two change blocks:
  const firstPrev = stepChange(-1, -1, blocks.length);
  assert.equal(firstPrev, 1, 'the last block');
  assert.equal(r.ops[blocks[firstPrev]].text, 'five');
  assert.equal(stepChange(firstPrev, 1, blocks.length), 0, 'n from there wraps to the first block');
});
