import test from 'node:test';
import assert from 'node:assert/strict';
import { connectionModel, createOfflineWatcher, OFFLINE_BANNER_MS } from '../js/connection.js';

test('connectionModel words, state and dot class per status', () => {
  const ok = connectionModel('ok');
  assert.equal(ok.word, 'live'); assert.equal(ok.chipText, 'Live'); assert.equal(ok.state, 'connected'); assert.equal(ok.dotClass, 'sse-dot ok');
  const re = connectionModel('reconnecting');
  assert.equal(re.chipText, 'Reconnecting'); assert.equal(re.reconnecting, true); assert.equal(re.offline, false);
  const off = connectionModel('offline');
  assert.equal(off.chipText, 'Offline'); assert.equal(off.offline, true); assert.match(off.title, /Reconnect/);
});

test('offline banner appears only after 5s down and clears on recovery', () => {
  const timers = []; const events = [];
  const w = createOfflineWatcher({ setTimeout: (fn, ms) => { timers.push({ fn, ms }); return timers.length; }, clearTimeout: (id) => { timers[id - 1].dead = true; }, onChange: (v) => events.push(v) });
  w.update('reconnecting');
  w.update('offline');
  assert.equal(timers.length, 1, 'one timer for the whole outage');
  assert.equal(timers[0].ms, OFFLINE_BANNER_MS);
  w.update('ok');
  assert.equal(timers[0].dead, true);
  assert.deepEqual(events, [], 'a short blip never shows the banner');
  w.update('offline');
  timers[1].fn();
  assert.deepEqual(events, [true]);
  assert.equal(w.shown, true);
  w.update('ok');
  assert.deepEqual(events, [true, false]);
});
