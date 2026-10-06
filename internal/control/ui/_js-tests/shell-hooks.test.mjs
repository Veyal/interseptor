import test from 'node:test';
import assert from 'node:assert/strict';
import { registerCommand, listCommands, registerSseHandler, runSseHooks, loadOptionalModules, OPTIONAL_MODULES, setShellApi, getShellApi, emitTabChange, TAB_CHANGE_EVENT } from '../js/shell-hooks.js';

test('registerCommand validates, de-duplicates by title and unregisters', () => {
  assert.throws(() => registerCommand({ t: 'x' }));
  const off = registerCommand({ t: 'Do thing', run: () => 1 });
  registerCommand({ t: 'Do thing', run: () => 2 });
  assert.equal(listCommands().filter((c) => c.t === 'Do thing').length, 1);
  assert.equal(listCommands().find((c) => c.t === 'Do thing').run(), 2);
  off();
  registerCommand({ t: 'Other', run: () => 0 });
  assert.ok(listCommands().some((c) => c.t === 'Other'));
});

test('SSE hooks run per type and wildcard; a throwing hook is isolated', () => {
  const seen = [];
  registerSseHandler('scope.update', () => { throw new Error('bad'); });
  registerSseHandler('scope.update', (m) => seen.push('a:' + m.type));
  const off = registerSseHandler('*', (m) => seen.push('*:' + m.type));
  const errors = [];
  runSseHooks({ type: 'scope.update' }, (e) => errors.push(e.message));
  assert.deepEqual(seen, ['a:scope.update', '*:scope.update']);
  assert.deepEqual(errors, ['bad']);
  off();
  runSseHooks({ type: 'hello' });
  assert.equal(seen.length, 2);
});

test('optional modules load independently; failures are reported, not thrown', async () => {
  const loaded = [];
  const errors = [];
  const results = await loadOptionalModules(async (p) => { if (p === './dock.js') throw new Error('missing'); loaded.push(p); }, OPTIONAL_MODULES, (e, n) => errors.push(n));
  assert.equal(results.length, OPTIONAL_MODULES.length);
  assert.deepEqual(errors, ['dock']);
  assert.equal(results.find((r) => r.name === 'dock').ok, false);
  assert.ok(loaded.includes('./flowdrawer.js'));
});

test('shell API merges and tabchange dispatches a CustomEvent', () => {
  setShellApi({ a: 1 }); setShellApi({ b: 2 });
  assert.deepEqual(getShellApi(), { a: 1, b: 2 });
  const got = [];
  globalThis.CustomEvent = class { constructor(type, init) { this.type = type; this.detail = init.detail; } };
  const target = { dispatchEvent: (e) => got.push(e) };
  assert.equal(emitTabChange('findings', 'proxy', target), true);
  assert.equal(got[0].type, TAB_CHANGE_EVENT);
  assert.deepEqual(got[0].detail, { tab: 'findings', previous: 'proxy' });
});
