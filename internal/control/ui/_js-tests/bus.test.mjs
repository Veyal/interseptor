import test from 'node:test';
import assert from 'node:assert/strict';
import { on, once, off, emit, onBusError, listenerCount, resetBus, EVENTS } from '../js/bus.js';

test('on/emit delivers payloads and counts listeners', () => {
  resetBus();
  const seen = [];
  on('x', (p) => seen.push(p));
  on('x', (p) => seen.push(p * 10));
  assert.equal(emit('x', 3), 2);
  assert.deepEqual(seen, [3, 30]);
  assert.equal(listenerCount('x'), 2);
  assert.equal(emit('unheard', 1), 0);
});

test('on returns an unsubscribe and off removes a listener', () => {
  resetBus();
  let n = 0;
  const offa = on('e', () => { n++; });
  emit('e');
  offa();
  emit('e');
  assert.equal(n, 1);
  assert.equal(listenerCount('e'), 0);
  // off() for an unknown listener is harmless.
  off('e', () => {});
});

test('once fires exactly once', () => {
  resetBus();
  let n = 0;
  once('o', () => { n++; });
  emit('o'); emit('o'); emit('o');
  assert.equal(n, 1);
  assert.equal(listenerCount('o'), 0);
});

test('a throwing listener never blocks the others and reaches the error sink', () => {
  resetBus();
  const errors = [];
  const stop = onBusError((err, event) => errors.push([event, err.message]));
  const order = [];
  on('t', () => { order.push('a'); });
  on('t', () => { throw new Error('boom'); });
  on('t', () => { order.push('c'); });
  assert.equal(emit('t', null), 3);
  assert.deepEqual(order, ['a', 'c']);
  assert.deepEqual(errors, [['t', 'boom']]);
  stop();
});

test('wildcard listeners receive every event with its name', () => {
  resetBus();
  const seen = [];
  on('*', (payload, event) => seen.push([event, payload]));
  emit('one', 1);
  emit('two', 2);
  assert.deepEqual(seen, [['one', 1], ['two', 2]]);
  // Emitting '*' directly does not double-deliver.
  seen.length = 0;
  emit('*', 'direct');
  assert.deepEqual(seen, [['*', 'direct']]);
});

test('a handler may subscribe or unsubscribe during delivery without corrupting the pass', () => {
  resetBus();
  let late = 0;
  on('m', () => { on('m', () => { late++; }); });
  // The listener added mid-emit must not run during this same emit (snapshot).
  emit('m');
  assert.equal(late, 0);
  emit('m');
  assert.equal(late, 1);
});

test('bad arguments throw', () => {
  resetBus();
  assert.throws(() => on('e', null));
  assert.throws(() => on(123, () => {}));
  assert.throws(() => once('e', 'nope'));
  assert.throws(() => onBusError('nope'));
});

test('EVENTS names are unique, frozen string constants', () => {
  const vals = Object.values(EVENTS);
  assert.equal(new Set(vals).size, vals.length, 'no duplicate topic strings');
  assert.ok(vals.every((v) => typeof v === 'string' && v.length), 'all topics are non-empty strings');
  assert.ok(Object.isFrozen(EVENTS));
});
