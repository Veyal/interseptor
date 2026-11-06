// inspector-structure.subtreeVisibility drives the JSON tree's fold buttons.
// The tree used to unhide rows purely by depth, so expanding a parent revealed
// everything under a container the user had folded inside it — while that
// container still reported aria-expanded="false". The rows below the subtree have
// to keep their own state, which is why the helper returns flags for the subtree
// only.
import test from 'node:test';
import assert from 'node:assert/strict';
import { subtreeVisibility, flattenJson } from '../js/inspector-structure.js';

// A flat depth-annotated row list, the shape the inspector renders.
const row = (depth, extra = {}) => ({ depth, container: false, expanded: true, ...extra });
const box = (depth, expanded) => row(depth, { container: true, expanded });

test('collapsing a container hides its whole subtree and nothing else', () => {
  const rows = [box(0, true), row(1), box(1, true), row(2), row(1)];
  assert.deepEqual(subtreeVisibility(rows, 0, false), [true, true, true, true]);
  // Rows after the subtree are not in the result at all.
  assert.equal(subtreeVisibility(rows, 0, false).length, 4);
});

test('expanding a container reveals its subtree', () => {
  const rows = [box(0, true), row(1), box(1, true), row(2)];
  assert.deepEqual(subtreeVisibility(rows, 0, true), [false, false, false]);
});

test('a nested fold stays folded when its parent expands', () => {
  // $ (0) > a (1, folded) > x (2) > y (3) ; then a sibling b (1)
  const rows = [box(0, true), box(1, false), row(2), row(3), box(1, true)];
  assert.deepEqual(subtreeVisibility(rows, 0, true), [false, true, true, false]);
});

test('a fold inside a fold unwinds one level at a time', () => {
  // $ (0) > a (1, folded) > b (2, folded) > x (3) > y (2) ; then z (1) and tail (1)
  const rows = [box(0, true), box(1, false), box(2, false), row(3), row(2), row(1), row(1)];
  // Opening $ keeps a folded, so a is visible and nothing under it is.
  assert.deepEqual(subtreeVisibility(rows, 0, true), [false, true, true, true, false, false]);
  // Opening a keeps b folded: a's direct children (b) show, b's rows do not.
  assert.deepEqual(subtreeVisibility(rows, 1, true), [false, true, false]);
  // Opening b reveals only x.
  assert.deepEqual(subtreeVisibility(rows, 2, true), [false]);
});

test('subtreeVisibility stops at a non-descendant and tolerates bad input', () => {
  const rows = [box(0, true), row(1), row(0), row(1)];
  assert.deepEqual(subtreeVisibility(rows, 0, true), [false], 'stops before the depth-0 sibling');
  assert.deepEqual(subtreeVisibility(rows, 2, true), [false]);
  assert.deepEqual(subtreeVisibility(rows, 9, true), []);
  assert.deepEqual(subtreeVisibility(rows, -1, true), []);
  assert.deepEqual(subtreeVisibility(null, 0, true), []);
  assert.deepEqual(subtreeVisibility([row(0)], 0, true), [], 'a leaf has no children');
});

test('the flags line up with flattenJson’s rows for a real body', () => {
  const { rows } = flattenJson({ a: { b: 1, c: 2 }, d: [1] });
  // rows: $ (0) | a (1) | b (2) | c (2) | d (1) | 0 (2)
  assert.deepEqual(rows.map((r) => [r.key, r.depth]), [['$', 0], ['a', 1], ['b', 2], ['c', 2], ['d', 1], ['0', 2]]);
  // Fold `a` (index 1) while expanding `$`: b and c hide, d and its child show.
  const model = rows.map((r) => ({ depth: r.depth, container: r.container, expanded: r.key === 'a' ? false : true }));
  assert.deepEqual(subtreeVisibility(model, 0, true), [false, true, true, false, false]);
});
