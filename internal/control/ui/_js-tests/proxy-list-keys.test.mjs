import test from 'node:test';
import assert from 'node:assert/strict';
import { listKeyTarget } from '../js/proxy-selection.js';

test('listKeyTarget moves one row, stops at the ends and starts at the top', () => {
  assert.equal(listKeyTarget('ArrowDown', 3, 10), 4);
  assert.equal(listKeyTarget('ArrowUp', 3, 10), 2);
  assert.equal(listKeyTarget('ArrowDown', 9, 10), 9, 'no wrap past the last row');
  assert.equal(listKeyTarget('ArrowUp', 0, 10), 0, 'no wrap past the first row');
  assert.equal(listKeyTarget('ArrowDown', -1, 10), 0, 'nothing selected: the first row');
  assert.equal(listKeyTarget('ArrowUp', -1, 10), 0);
});

test('listKeyTarget handles Home, End and paging', () => {
  assert.equal(listKeyTarget('Home', 7, 10), 0);
  assert.equal(listKeyTarget('End', 2, 10), 9);
  assert.equal(listKeyTarget('PageDown', 2, 100, 20), 22);
  assert.equal(listKeyTarget('PageDown', 95, 100, 20), 99);
  assert.equal(listKeyTarget('PageUp', 30, 100, 20), 10);
  assert.equal(listKeyTarget('PageUp', 5, 100, 20), 0);
  assert.equal(listKeyTarget('PageDown', 0, 100, 0), 1, 'a zero page still moves one row');
});

test('listKeyTarget ignores other keys and empty lists', () => {
  assert.equal(listKeyTarget('Enter', 2, 10), null);
  assert.equal(listKeyTarget('j', 2, 10), null, 'j/k stay on the global walk');
  assert.equal(listKeyTarget('ArrowDown', 0, 0), null);
});
