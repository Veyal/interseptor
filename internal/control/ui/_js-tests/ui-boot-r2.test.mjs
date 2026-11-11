import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createBootReads } from '../js/api-inflight.js';
import { streamHelloProjectCheck } from '../js/shell-hooks.js';

test('a boot answer is handed out once, then callers ask the server', async () => {
  const boot = createBootReads();
  boot.keep('/api/project', Promise.resolve({ current: 'demo' }));
  assert.deepEqual(await boot.take('/api/project'), { current: 'demo' });
  assert.equal(boot.take('/api/project'), null);
  assert.equal(boot.size(), 0);
});

test('take() returns null for a path nobody read at boot', () => {
  const boot = createBootReads();
  boot.keep('/api/project', Promise.resolve(1));
  assert.equal(boot.take('/api/version'), null);
  assert.equal(boot.take('/api/project?_t=1'), null);
});

test('a boot answer still pending is awaited by the taker', async () => {
  const boot = createBootReads();
  let resolve; boot.keep('/api/version', new Promise((r) => { resolve = r; }));
  const taken = boot.take('/api/version');
  resolve({ version: '1.2.3' });
  assert.deepEqual(await taken, { version: '1.2.3' });
});

test('a failed boot read hands over nothing, so the taker falls back to its own request', async () => {
  const boot = createBootReads();
  boot.keep('/api/project', Promise.reject(new Error('down')));
  let asked = 0;
  const d = (await boot.take('/api/project')) || (asked++, { current: 'fresh' });
  assert.deepEqual(d, { current: 'fresh' });
  assert.equal(asked, 1);
});

test('clear() drops every boot answer: a read issued after a change never gets the older one', () => {
  const boot = createBootReads();
  boot.keep('/api/project', Promise.resolve({ current: 'old' }));
  boot.keep('/api/version', Promise.resolve({ project: 'old' }));
  boot.clear();
  assert.equal(boot.take('/api/project'), null);
  assert.equal(boot.take('/api/version'), null);
});

test('first hello of a clean boot trusts the project the page was booted for', () => {
  assert.deepEqual(streamHelloProjectCheck({ knownKey: null, bootKey: 'dir\u0000demo', hadError: false }), { baseline: 'dir\u0000demo', ask: false });
});

test('first hello after a failed connection asks, and compares with the boot project', () => {
  assert.deepEqual(streamHelloProjectCheck({ knownKey: null, bootKey: 'dir\u0000demo', hadError: true }), { baseline: 'dir\u0000demo', ask: true });
});

test('without a boot identity the first hello asks and adopts the answer', () => {
  assert.deepEqual(streamHelloProjectCheck({ knownKey: null, bootKey: null, hadError: false }), { baseline: null, ask: true });
  assert.deepEqual(streamHelloProjectCheck(), { baseline: null, ask: true });
});

test('every later hello asks and compares with what the stream first saw', () => {
  for (const hadError of [false, true]) {
    assert.deepEqual(streamHelloProjectCheck({ knownKey: 'dir\u0000demo', bootKey: 'other\u0000x', hadError }), { baseline: 'dir\u0000demo', ask: true });
  }
});
