import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createInflight, shareableRead } from '../js/api-inflight.js';

const deferred = () => { let resolve, reject; const promise = new Promise((a, b) => { resolve = a; reject = b; }); return { promise, resolve, reject }; };

test('concurrent identical reads share one request', async () => {
  const inflight = createInflight();
  let calls = 0;
  const d = deferred();
  const start = () => { calls++; return d.promise; };
  const a = inflight.share('/api/version', start);
  const b = inflight.share('/api/version', start);
  const c = inflight.share('/api/project', () => { calls++; return Promise.resolve('p'); });
  assert.equal(a, b);
  d.resolve('v');
  assert.deepEqual(await Promise.all([a, b, c]), ['v', 'v', 'p']);
  assert.equal(calls, 2);
});

test('an entry is dropped once it settles, so later reads refetch', async () => {
  const inflight = createInflight();
  let calls = 0;
  await inflight.share('/k', () => { calls++; return 1; });
  assert.equal(inflight.size(), 0);
  await inflight.share('/k', () => { calls++; return 2; });
  assert.equal(calls, 2);
});

test('a rejection reaches every waiter and clears the entry', async () => {
  const inflight = createInflight();
  const err = new Error('boom');
  const a = inflight.share('/k', () => Promise.reject(err));
  const b = inflight.share('/k', () => assert.fail('must join'));
  await assert.rejects(a, err);
  await assert.rejects(b, err);
  assert.equal(inflight.size(), 0);
  assert.equal(await inflight.share('/k', () => 'ok'), 'ok');
});

test('a synchronous throw in start rejects instead of leaking an entry', async () => {
  const inflight = createInflight();
  await assert.rejects(inflight.share('/k', () => { throw new Error('sync'); }), /sync/);
  assert.equal(inflight.size(), 0);
});

test('clear() makes the next caller start fresh while earlier waiters keep theirs', async () => {
  const inflight = createInflight();
  const d = deferred();
  const old = inflight.share('/k', () => d.promise);
  inflight.clear();
  const fresh = inflight.share('/k', () => 'new');
  assert.notEqual(old, fresh);
  d.resolve('old');
  assert.equal(await old, 'old');
  assert.equal(await fresh, 'new');
  assert.equal(inflight.size(), 0);
});

test('shareableRead: only option-less GETs may share', () => {
  assert.equal(shareableRead(undefined), true);
  assert.equal(shareableRead({}), true);
  assert.equal(shareableRead({ method: 'get' }), true);
  assert.equal(shareableRead({ signal: undefined }), true);
  assert.equal(shareableRead({ method: 'POST' }), false);
  assert.equal(shareableRead({ method: 'HEAD' }), false);
  assert.equal(shareableRead({ signal: {} }), false);
  assert.equal(shareableRead({ cache: 'no-store' }), false);
  assert.equal(shareableRead({ headers: { Accept: 'text/plain' } }), false);
});
