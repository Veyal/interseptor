import test from 'node:test';
import assert from 'node:assert/strict';
import {
  buildRunBody, normalizeRows, rowState, summarize, failedItemUids, filterRows, summaryText, clampDelay,
  newLive, applyProgress, applyEvent, runControls, phaseLabel, isFinishedStatus, PERSIST_MODES,
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
  assert.deepEqual(buildRunBody({ collectionUid: 'c1' }), { collectionUid: 'c1', persist: 'ask', bail: 'none', delayMs: 0, noScripts: false });
  const b = buildRunBody({ collectionUid: 'c1', folderUid: 'f', itemUids: ['x', '', 'y'], envUid: 'e', persist: 'keep', bail: 'on-failure', delayMs: '250', noScripts: true });
  assert.deepEqual(b, { collectionUid: 'c1', folderUid: 'f', itemUids: ['x', 'y'], envUid: 'e', persist: 'keep', bail: 'on-failure', delayMs: 250, noScripts: true });
  assert.equal(buildRunBody({ collectionUid: 'c1', persist: 'sometimes' }).persist, 'ask', 'unsupported persist falls back to the interactive default');
  assert.equal(buildRunBody({ collectionUid: 'c1', bail: 'weird' }).bail, 'none');
  assert.throws(() => buildRunBody({}), /collection/);
  assert.equal(clampDelay(-5), 0);
  assert.equal(clampDelay(99999999), 60000);
  assert.equal(clampDelay('abc'), 0);
});

// ---- live runs (async API) ---------------------------------------------------

const item = (seq, name, extra = {}) => ({ seq, itemUid: 'u' + seq, name, outcome: 'sent', status: 200, flowId: 100 + seq, ...extra });

test('item results carry a tests array and scripts instead of count maps', () => {
  const n = normalizeRows([
    item(1, 'ok', { tests: [{ name: 'a', status: 'pass' }, { name: 'b', status: 'pass' }] }),
    item(2, 'bad', { tests: [{ name: 'a', status: 'pass' }, { name: 'b', status: 'fail', message: 'nope' }] }),
    item(3, 'nosupport', { tests: [{ name: 'a', status: 'unsupported' }] }),
    item(4, 'q', { scripts: [{ reason: 'quarantined: not trusted' }, { reason: 'ran' }] }),
  ]);
  assert.deepEqual(n.map((r) => r.state), ['pass', 'fail', 'unsupported', 'pass']);
  assert.equal(n[1].testsText, '1/2');
  assert.equal(n[3].quarantined, 1);
  assert.equal(n[1].detail.tests[1].message, 'nope');
});

test('applyProgress folds a snapshot into the live view and never regresses', () => {
  let live = newLive();
  live = applyProgress(live, { runUid: 'r1', status: 'running', plannedSteps: 3, since: 0, count: 2, items: [item(1, 'a'), item(2, 'b')], finished: false, totals: { requests: 2 } });
  assert.equal(live.runUid, 'r1');
  assert.equal(live.rows.length, 2);
  assert.equal(live.planned, 3);
  assert.equal(live.since, 2);
  // a repeated / overlapping snapshot does not duplicate rows
  live = applyProgress(live, { runUid: 'r1', status: 'running', since: 1, count: 3, items: [item(2, 'b'), item(3, 'c')], finished: false });
  assert.deepEqual(live.rows.map((r) => r.name), ['a', 'b', 'c']);
  assert.equal(live.since, 3);
  live = applyProgress(live, { runUid: 'r1', status: 'done', since: 3, count: 3, items: [], finished: true, report: { status: 'done' } });
  assert.equal(live.finished, true);
  assert.equal(live.status, 'done');
});

test('events add rows, status and the persist prompt', () => {
  let live = applyEvent(newLive(), { type: 'start', runUid: 'r1', plannedSteps: 2, status: 'running' });
  assert.equal(live.runUid, 'r1');
  assert.equal(live.planned, 2);
  live = applyEvent(live, { type: 'item', runUid: 'r1', item: item(1, 'a', { tests: [{ name: 't', status: 'fail', message: 'boom' }] }), totals: { requests: 1 } });
  live = applyEvent(live, { type: 'item', runUid: 'r1', item: item(2, 'b') });
  assert.equal(live.rows[0].detail.tests[0].message, 'boom', 'earlier rows keep their detail when later items arrive');
  assert.equal(live.rows[0].state, 'fail');
  live = { ...live, items: live.items.slice(0, 1), rows: live.rows.slice(0, 1) };
  live = applyEvent(live, { type: 'item', runUid: 'r1', item: item(1, 'a') });
  assert.equal(live.rows.length, 1, 'a replayed item event is ignored');
  live = applyEvent(live, { type: 'paused', runUid: 'r1' });
  assert.equal(live.status, 'paused');
  live = applyEvent(live, { type: 'resumed', runUid: 'r1' });
  assert.equal(live.status, 'running');
  live = applyEvent(live, { type: 'awaiting_persist', runUid: 'r1', pendingWrites: [{ scope: 'environment', key: 'tok', value: '[secret]' }] });
  assert.equal(live.status, 'awaiting_persist');
  assert.equal(live.pending.length, 1);
  live = applyEvent(live, { type: 'done', runUid: 'r1', status: 'done' });
  assert.equal(live.finished, true);
  assert.equal(live.pending.length, 0);
  // events of another run never leak in
  assert.equal(applyEvent(live, { type: 'item', runUid: 'other', item: item(9, 'x') }).rows.length, 1);
});

test('controls follow the run status and always carry text labels', () => {
  const c = (status, finished = false) => runControls({ status, finished });
  assert.deepEqual(c('running'), { pause: true, resume: false, abort: true, decide: false });
  assert.deepEqual(c('paused'), { pause: false, resume: true, abort: true, decide: false });
  assert.deepEqual(c('awaiting_persist'), { pause: false, resume: false, abort: true, decide: true });
  assert.deepEqual(c('done', true), { pause: false, resume: false, abort: false, decide: false });
  assert.deepEqual(runControls(null), { pause: false, resume: false, abort: false, decide: false });
  for (const s of ['running', 'paused', 'awaiting_persist', 'done', 'bailed', 'aborted', 'loop_guard', 'scripts_not_approved', 'weird']) {
    assert.ok(phaseLabel(s).length > 2, s);
  }
  assert.equal(isFinishedStatus('aborted'), true);
  assert.equal(isFinishedStatus('running'), false);
  assert.equal(isFinishedStatus('awaiting_persist'), false);
  assert.ok(PERSIST_MODES.includes('ask'));
});
