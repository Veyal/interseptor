import test from 'node:test';
import assert from 'node:assert/strict';
import {
  buildRunBody, normalizeRows, rowState, summarize, failedItemUids, filterRows, summaryText, clampDelay,
} from '../js/runner-model.js';

const rows = [
  { itemUid: 'a', name: 'login', outcome: 'sent', status: 200, flowId: 11, tests: { pass: 2 } },
  { itemUid: 'b', name: 'get order', outcome: 'sent', status: 403, flowId: 12, tests: { pass: 1, fail: 2 } },
  { itemUid: 'c', name: 'export', outcome: 'blocked', blockReason: 'scope' },
  { itemUid: 'd', name: 'boom', outcome: 'error', error: 'dial tcp refused' },
  { itemUid: 'e', name: 'skipped', outcome: 'skipped' },
  { itemUid: 'b', name: 'get order', outcome: 'sent', status: 500, flowId: 13, tests: { unsupported: 1 } },
];

test('row state is derived from outcome and tests, with text for every state', () => {
  const n = normalizeRows(rows);
  assert.deepEqual(n.map((r) => r.state), ['pass', 'fail', 'blocked', 'error', 'skipped', 'unsupported']);
  for (const r of n) assert.ok(r.label && r.label.length > 0, 'state label present');
  assert.equal(n[0].n, 1);
  assert.equal(n[1].testsText, '1/3');
  assert.equal(n[2].testsText, '-');
  assert.equal(rowState({ outcome: 'sent', tests: {} }), 'pass');
  assert.equal(rowState(null), 'error');
});

test('unsupported is never reported as a pass', () => {
  assert.notEqual(rowState({ outcome: 'sent', tests: { unsupported: 2, pass: 5 } }), 'pass');
});

test('summary counts rows and tests', () => {
  const s = summarize(normalizeRows(rows));
  assert.equal(s.total, 6);
  assert.equal(s.pass, 1);
  assert.equal(s.fail, 1);
  assert.equal(s.blocked, 1);
  assert.equal(s.error, 1);
  assert.equal(s.skipped, 1);
  assert.equal(s.unsupported, 1);
  assert.equal(s.testsPass, 3);
  assert.equal(s.testsFail, 2);
  assert.match(summaryText(s), /6 requests/);
  assert.match(summaryText(s), /2 failed/);
  assert.equal(summaryText(summarize([])), 'No results yet');
});

test('rerun failed picks unique items that did not pass, in run order', () => {
  assert.deepEqual(failedItemUids(normalizeRows(rows)), ['b', 'c', 'd']);
  assert.deepEqual(failedItemUids(normalizeRows([rows[0]])), []);
  assert.deepEqual(failedItemUids(null), []);
});

test('filters', () => {
  const n = normalizeRows(rows);
  assert.equal(filterRows(n, 'all').length, 6);
  assert.equal(filterRows(n, 'problems').length, 4);
  assert.deepEqual(filterRows(n, 'failed').map((r) => r.itemUid), ['b', 'c', 'd', 'b']);
  assert.equal(filterRows(n, 'bogus').length, 6);
});

test('run body is validated and minimal', () => {
  assert.deepEqual(buildRunBody({ collectionUid: 'c1' }), { collectionUid: 'c1', persist: 'discard', bail: 'none', delayMs: 0, noScripts: false });
  const b = buildRunBody({ collectionUid: 'c1', folderUid: 'f', itemUids: ['x', '', 'y'], envUid: 'e', persist: 'keep', bail: 'on-failure', delayMs: '250', noScripts: true });
  assert.deepEqual(b, { collectionUid: 'c1', folderUid: 'f', itemUids: ['x', 'y'], envUid: 'e', persist: 'keep', bail: 'on-failure', delayMs: 250, noScripts: true });
  assert.equal(buildRunBody({ collectionUid: 'c1', persist: 'ask' }).persist, 'discard', 'unsupported persist falls back to the safe mode');
  assert.equal(buildRunBody({ collectionUid: 'c1', bail: 'weird' }).bail, 'none');
  assert.throws(() => buildRunBody({}), /collection/);
  assert.equal(clampDelay(-5), 0);
  assert.equal(clampDelay(99999999), 60000);
  assert.equal(clampDelay('abc'), 0);
});
