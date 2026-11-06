// finder.js — the match list after the query is cleared.
//
// Reported defect: run() starts by clearing the marks (clearMarks() re-wraps the text and
// empties state.marks), but it then returned early when the query was empty WITHOUT
// dropping state.matchMarks. next()/prev() step through state.matchMarks, so after an
// empty query they re-activated a <mark> that clearMarks() had already detached: the bar
// wrote "1 / N" for a match the pane no longer showed, and scrollIntoView() ran on a node
// outside the document.
//
// The two pure halves of that fix are tested here — the list advance, and the state reset
// clearMarks() performs — so no browser harness is needed or faked.
import test from 'node:test';
import assert from 'node:assert/strict';
import { nextMarkIndex, clearMarkState, findMatches, MAX_HIGHLIGHTS } from '../js/finder.js';

test('the match list advances with wrap-around and reports "nothing to show" for an empty list', () => {
  // Forward and backward both wrap, in both directions, over N marks.
  assert.equal(nextMarkIndex(0, 3, 1), 1);
  assert.equal(nextMarkIndex(1, 3, 1), 2);
  assert.equal(nextMarkIndex(2, 3, 1), 0, 'next() after the last match wraps to the first');
  assert.equal(nextMarkIndex(0, 3, -1), 2, 'prev() before the first match wraps to the last');
  assert.equal(nextMarkIndex(-1, 3, 1), 0, 'no active mark yet: the first step picks the first match');
  assert.equal(nextMarkIndex(1, 1, 1), 0, 'a single match stays on itself');
  assert.equal(nextMarkIndex(4, 4, 0), 0, 'index 4 of 4 is 0');

  // The defect's exit: an empty list must answer -1 so the caller does nothing at all
  // (no index, no "n / N" text, no scrollIntoView on a detached node).
  for (const current of [-1, 0, 1, 2]) {
    for (const dir of [1, -1]) {
      assert.equal(nextMarkIndex(current, 0, dir), -1, `nextMarkIndex(${current}, 0, ${dir})`);
    }
  }
  // A list that was never populated and an unset active index are the same "nothing".
  assert.equal(nextMarkIndex(undefined, 0, 1), -1);
  assert.equal(nextMarkIndex(undefined, 2, 1), 0, 'an unset index still steps into a real list');
});

test('clearing the marks drops the whole list, so next()/prev() cannot reach a detached mark', () => {
  // The state a cleared finder left behind: the <mark> nodes are detached from the DOM,
  // state.marks is empty, but the per-match groups survived and active still pointed at one.
  const groups = [{ detached: 1 }, { detached: 2 }];
  const state = { marks: [...groups], matchMarks: groups, active: 1, query: '' };

  clearMarkState(state);

  assert.deepEqual(state.matchMarks, [], 'matchMarks is the list next()/prev() walk');
  assert.deepEqual(state.marks, [], 'marks is the flat list clearMarks() unwrapped');
  assert.equal(state.active, -1, 'no active match after a reset');

  // With the stale list this was 0: next() re-activated a mark no longer in the document.
  assert.equal(nextMarkIndex(state.active, state.matchMarks.length, 1), -1);
});

test('a reset state keeps matching correctly when the query comes back', () => {
  // reset -> search again is the normal "clear then retype" path; the reset must not leave
  // a stale offset behind that would then be used as a starting point.
  const state = { marks: [], matchMarks: [{ a: 1 }, { b: 2 }], active: 1, query: '' };
  clearMarkState(state);
  const found = findMatches('one two one', 'one');
  assert.equal(found.matches.length, 2);
  assert.equal(state.matchMarks.length, 0);
  assert.equal(nextMarkIndex(state.active, found.matches.length, 1), 0);
  assert.ok(MAX_HIGHLIGHTS >= found.matches.length);
});

test('the reset is idempotent', () => {
  const state = { marks: [], matchMarks: [], active: -1 };
  clearMarkState(state);
  clearMarkState(state);
  assert.deepEqual(state, { marks: [], matchMarks: [], active: -1 });
});
