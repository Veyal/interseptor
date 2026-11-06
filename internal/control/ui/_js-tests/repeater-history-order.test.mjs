// Node tests for the Repeater tab strip helpers: the send-history merge order
// (the rail and "Diff vs previous" read the list positionally) and moving a tab
// next to another one.
import test from 'node:test';
import assert from 'node:assert/strict';
import { mergeHistoryNewestFirst, placeAfter } from '../js/repeater-tab-model.js';
import { previousHistoryEntry } from '../js/repeater.js';

const entry = (id) => ({ id, method: 'GET', status: 200, host: 'example.com', path: '/a' });
const ids = (list) => list.map((e) => e.id);

test('a migrated legacy history merge stays newest first', () => {
  // This tab's own sends, then the legacy endpoint snapshot. The legacy query
  // answers for the whole endpoint, so it can hold a flow captured after this
  // tab's last send (id 12 > 10).
  const session = [entry(10), entry(9)];
  const legacy = [entry(12), entry(3)];
  assert.deepEqual(ids(mergeHistoryNewestFirst(session, legacy)), [12, 10, 9, 3]);
  // Why it matters: "previous" is resolved positionally, so the raw
  // concatenation made flow 12 the "previous send" of the older flow 9.
  assert.equal(previousHistoryEntry([...session, ...legacy], 9).id, 12);
  assert.equal(previousHistoryEntry(mergeHistoryNewestFirst(session, legacy), 9).id, 3);
});

test('merging de-duplicates by id and keeps the first row seen', () => {
  const first = entry(5);
  const merged = mergeHistoryNewestFirst([first], [{ id: 5, host: 'stale' }, { id: 4 }]);
  assert.deepEqual(ids(merged), [5, 4]);
  assert.equal(merged[0], first, 'the in-memory row wins over a stored copy');
});

test('merging drops rows without a usable flow id and tolerates empty input', () => {
  const merged = mergeHistoryNewestFirst(
    [entry(2), null, { id: 0 }, { id: -1 }, { id: 'x' }, { id: 3.5 }],
    null,
    [entry(7)],
    undefined,
  );
  assert.deepEqual(ids(merged), [7, 2]);
  assert.deepEqual(mergeHistoryNewestFirst(), []);
  assert.deepEqual(mergeHistoryNewestFirst([], []), []);
});

test('merging never mutates its inputs and re-sorts an already-unsorted list', () => {
  const session = [entry(9), entry(10)]; // a stored list that drifted out of order
  const legacy = [entry(11)];
  assert.deepEqual(ids(mergeHistoryNewestFirst(session, legacy)), [11, 10, 9]);
  assert.deepEqual(ids(session), [9, 10]);
  assert.deepEqual(ids(legacy), [11]);
});

test('placing a tab after itself leaves the strip untouched', () => {
  const tabs = [{ tid: 1 }, { tid: 2 }, { tid: 3 }];
  const same = placeAfter(tabs, tabs[1], tabs[1]);
  assert.equal(same, tabs);
  assert.deepEqual(same.map((t) => t.tid), [1, 2, 3]);
});

test('placing a tab after another anchor moves it there', () => {
  const tabs = [{ tid: 1 }, { tid: 2 }, { tid: 3 }];
  assert.deepEqual(placeAfter(tabs, tabs[2], tabs[0]).map((t) => t.tid), [1, 3, 2]);
  assert.deepEqual(placeAfter(tabs, tabs[0], tabs[2]).map((t) => t.tid), [3, 2, 1]);
  const unknown = { tid: 9 };
  assert.equal(placeAfter(tabs, unknown, tabs[0]), tabs);
  assert.equal(placeAfter(tabs, tabs[0], unknown), tabs);
});
