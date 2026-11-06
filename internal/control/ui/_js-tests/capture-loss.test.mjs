import test from 'node:test';
import assert from 'node:assert/strict';
import { foldAggregate, createProjectState } from '../js/project-state.js';
import { captureLossModel } from '../js/capture-loss-model.js';

test('foldAggregate carries the capture drop counters', () => {
  assert.deepEqual(foldAggregate({ capture: { flowWritesDropped: 12, wsFramesDropped: 3 } }).capture, { flowWritesDropped: 12, wsFramesDropped: 3 });
  assert.deepEqual(foldAggregate({}).capture, { flowWritesDropped: 0, wsFramesDropped: 0 });
  assert.deepEqual(foldAggregate({ capture: { flowWritesDropped: -4, wsFramesDropped: 'x' } }).capture, { flowWritesDropped: 0, wsFramesDropped: 0 });
});

test('project state exposes capture counters after a refresh', async () => {
  let t = 0;
  const timers = [];
  const ps = createProjectState({
    fetchJson: async (path) => (path === '/api/project/readiness' ? { capture: { flowWritesDropped: 5, wsFramesDropped: 0 } } : { identities: [] }),
    now: () => t,
    setTimeout: (fn, ms) => { timers.push({ fn, at: t + ms }); return timers.length; },
    clearTimeout: () => {},
  });
  assert.deepEqual(ps.get().capture, { flowWritesDropped: 0, wsFramesDropped: 0 });
  const done = ps.refresh();
  t += 1000;
  timers.splice(0).forEach((x) => x.fn());
  await done;
  assert.deepEqual(ps.get().capture, { flowWritesDropped: 5, wsFramesDropped: 0 });
});

test('captureLossModel hides when nothing was dropped', () => {
  assert.equal(captureLossModel(null).show, false);
  assert.equal(captureLossModel({ flowWritesDropped: 0, wsFramesDropped: 0 }).show, false);
});

test('captureLossModel names what was lost in plain text', () => {
  const m = captureLossModel({ flowWritesDropped: 1, wsFramesDropped: 0 });
  assert.equal(m.show, true);
  assert.equal(m.text, 'History is incomplete: 1 captured flow write was dropped because capture fell behind.');
  const both = captureLossModel({ flowWritesDropped: 2048, wsFramesDropped: 3 });
  assert.equal(both.text, 'History is incomplete: 2,048 captured flow writes and 3 WebSocket frames were dropped because capture fell behind.');
  assert.match(both.hint, /re-capture/i);
  assert.equal(both.key, '2048/3');
  const ws = captureLossModel({ flowWritesDropped: 0, wsFramesDropped: 1 });
  assert.equal(ws.text, 'History is incomplete: 1 WebSocket frame was dropped because capture fell behind.');
});
