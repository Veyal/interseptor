import test from 'node:test';
import assert from 'node:assert/strict';
import { applyDelta, snapshotSeq, queueKeyForSide, deltaType, SEQ_UNKNOWN } from '../js/intercept-delta.js';

const held = (seq, item, side = 'req') => ({ type: 'intercept.held', seq, side, item });
const released = (seq, id, side = 'req') => ({ type: 'intercept.released', seq, side, id });

test('snapshotSeq reads a numeric seq and falls back to unknown', () => {
  assert.equal(snapshotSeq({ seq: 0 }), 0);
  assert.equal(snapshotSeq({ seq: 7 }), 7);
  assert.equal(snapshotSeq({}), SEQ_UNKNOWN);
  assert.equal(snapshotSeq(null), SEQ_UNKNOWN);
  assert.equal(snapshotSeq({ seq: -2 }), SEQ_UNKNOWN);
  assert.equal(snapshotSeq({ seq: 'x' }), SEQ_UNKNOWN);
});

test('queueKeyForSide / deltaType', () => {
  assert.equal(queueKeyForSide('resp'), 'responseQueue');
  assert.equal(queueKeyForSide('req'), 'queue');
  assert.equal(deltaType({ type: 'intercept.held' }), 'held');
  assert.equal(deltaType({ type: 'intercept.released' }), 'released');
  assert.equal(deltaType({ type: 'held' }), 'held');
  assert.equal(deltaType({ type: 'other' }), '');
});

test('a delta with no baseline (unknown seq) is a gap, not applied', () => {
  const r = applyDelta({ queue: [] }, SEQ_UNKNOWN, held(1, { id: 1 }));
  assert.equal(r.status, 'gap');
  assert.equal(r.seq, SEQ_UNKNOWN);
  assert.deepEqual(r.state.queue, []);
});

test('held then released applied incrementally matches a full rebuild', () => {
  // Start from a snapshot at seq 10 with one item already held.
  let seq = snapshotSeq({ seq: 10 });
  let state = { enabled: true, queue: [{ id: 5, host: 'example.com', path: '/a', method: 'GET' }], responseQueue: [] };

  let r = applyDelta(state, seq, held(11, { id: 6, host: 'example.com', path: '/b', method: 'POST' }));
  assert.equal(r.status, 'applied');
  state = r.state; seq = r.seq;
  assert.equal(seq, 11);

  r = applyDelta(state, seq, released(12, 5));
  assert.equal(r.status, 'applied');
  state = r.state; seq = r.seq;
  assert.equal(seq, 12);

  // A full rebuild: the authoritative snapshot the server would send at seq 12.
  const rebuilt = { queue: [{ id: 6, host: 'example.com', path: '/b', method: 'POST' }] };
  assert.deepEqual(state.queue, rebuilt.queue);
  assert.equal(state.enabled, true); // untouched fields survive the shallow clone
});

test('response-side deltas touch only responseQueue', () => {
  let state = { queue: [{ id: 1 }], responseQueue: [] };
  let r = applyDelta(state, 3, held(4, { id: 9 }, 'resp'));
  assert.equal(r.status, 'applied');
  assert.deepEqual(r.state.responseQueue, [{ id: 9 }]);
  assert.deepEqual(r.state.queue, [{ id: 1 }], 'request queue untouched');
});

test('a duplicate (already-seen seq) is ignored, leaving state unchanged', () => {
  const state = { queue: [{ id: 5 }] };
  const dup = applyDelta(state, 11, held(11, { id: 5 }));
  assert.equal(dup.status, 'duplicate');
  assert.equal(dup.seq, 11);
  assert.equal(dup.state, state, 'same reference: no rebuild on a duplicate');
  // A re-delivered older release must not pop a since-re-added item.
  const old = applyDelta({ queue: [{ id: 5 }] }, 11, released(9, 5));
  assert.equal(old.status, 'duplicate');
  assert.deepEqual(old.state.queue, [{ id: 5 }]);
});

test('a future seq is a gap and leaves state untouched for the caller to resnapshot', () => {
  const state = { queue: [{ id: 1 }] };
  const r = applyDelta(state, 10, held(12, { id: 2 })); // seq 11 was dropped
  assert.equal(r.status, 'gap');
  assert.equal(r.seq, 10, 'seq is NOT advanced on a gap');
  assert.deepEqual(r.state.queue, [{ id: 1 }], 'queue is NOT mutated on a gap');
});

test('gap recovery: a dropped delta is healed by rebasing on a snapshot', () => {
  // Client at seq 10 with item 1 held.
  let seq = 10;
  let state = { queue: [{ id: 1 }], responseQueue: [] };

  // Server emits held#11 (dropped by SSE) and released#12 (arrives first).
  let r = applyDelta(state, seq, released(12, 1));
  assert.equal(r.status, 'gap'); // detected the hole — does not forward item 1 wrongly
  // Caller resets to unknown and fetches a snapshot. Any delta meanwhile is ignored.
  seq = SEQ_UNKNOWN;
  const ignored = applyDelta(state, seq, held(13, { id: 2 }));
  assert.equal(ignored.status, 'gap');

  // The authoritative snapshot lands at seq 13: item 1 gone, item 2 present.
  const snapshot = { seq: 13, queue: [{ id: 2 }], responseQueue: [] };
  state = { ...snapshot };
  seq = snapshotSeq(snapshot);
  assert.equal(seq, 13);

  // Deltas now flow contiguously again from the rebased seq.
  r = applyDelta(state, seq, held(14, { id: 3 }));
  assert.equal(r.status, 'applied');
  assert.deepEqual(r.state.queue, [{ id: 2 }, { id: 3 }]);
});
