import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  cellVerdict, cellTitle, classLabel, matrixCaption, flaggedRowCount, identityChoices,
  coverageLabel, coverageSummaryText, groupFraction, filterOperations,
  fmtMs, phaseBars, timingSummaryText, sortedOutliers,
  diffHasChanges, diffSummaryText, handoffSummaryText, matrixRunIdentities, MATRIX_MAX_IDENTITIES, latestRunUid,
} from '../js/collections-matrix-model.js';

test('cellVerdict priority: blocked/error over flag over sameness', () => {
  assert.equal(cellVerdict({ class: 'blocked' }).word, 'blocked');
  assert.equal(cellVerdict({ class: 'error' }).word, 'error');
  assert.equal(cellVerdict({ class: 'success', flag: 'violation', sameAsBaseline: true }).glyph, 'V');
  assert.equal(cellVerdict({ class: 'success', flag: 'violation', sameAsBaseline: true }).word, 'unexpected access');
  assert.equal(cellVerdict({ class: 'success', flag: 'unexpected_denial' }).word, 'unexpected denial');
  assert.equal(cellVerdict({ class: 'success', sameAsBaseline: true }).word, 'same as baseline');
  assert.equal(cellVerdict({ class: 'other' }).word, 'differs');
  assert.equal(cellVerdict({ class: 'not_run' }).word, 'not run');
  assert.equal(cellVerdict(null).word, 'not run');
});

test('classLabel falls back to the raw class', () => {
  assert.equal(classLabel('authz_failure'), 'authz failure');
  assert.equal(classLabel('weird'), 'weird');
  assert.equal(classLabel(''), '');
});

test('matrixCaption lists counts only when non-zero', () => {
  const cap = matrixCaption({ summary: { rows: 3, identities: 2, violations: 1 } });
  assert.equal(cap, '3 requests · 2 identities · 1 unexpected access');
  assert.equal(matrixCaption(null), '');
  assert.equal(matrixCaption({ summary: { rows: 1, identities: 1 } }), '1 request · 1 identity');
});

test('flaggedRowCount counts rows with any flagged cell', () => {
  const m = { rows: [{ cells: [{ flag: 'violation' }, {}] }, { cells: [{}, {}] }] };
  assert.equal(flaggedRowCount(m), 1);
  assert.equal(flaggedRowCount({}), 0);
});

test('identityChoices excludes skipped names', () => {
  assert.deepEqual(identityChoices(['admin', 'user', 'locked'], ['locked']), ['admin', 'user']);
  assert.deepEqual(identityChoices(null, null), []);
});

test('coverageLabel and summary text', () => {
  assert.equal(coverageLabel('untested'), 'Untested');
  assert.equal(coverageLabel('weird'), 'weird');
  assert.equal(coverageSummaryText({ exercised: 2, total: 4, percent: 50, nonSpecItems: 1, runsFolded: 3 }),
    '2 of 4 operations exercised (50%) · 1 request not from the spec · across 3 runs');
  assert.equal(coverageSummaryText(null), '');
});

test('groupFraction clamps and handles zero total', () => {
  assert.equal(groupFraction({ exercised: 2, total: 4 }), 0.5);
  assert.equal(groupFraction({ exercised: 0, total: 0 }), 0);
  assert.equal(groupFraction(null), 0);
});

test('filterOperations matches state and free text across fields', () => {
  const ops = [
    { state: 'untested', path: '/users', operationId: 'listUsers', itemName: 'List users', key: 'GET /users' },
    { state: 'passing', path: '/orders/{id}', operationId: 'getOrder', itemName: 'Get order', key: 'GET /orders/{id}' },
  ];
  assert.equal(filterOperations(ops, 'untested', '').length, 1);
  assert.equal(filterOperations(ops, '', 'order').length, 1);
  assert.equal(filterOperations(ops, '', 'LISTUSERS').length, 1);
  assert.equal(filterOperations(ops, '', 'zzz').length, 0);
  assert.equal(filterOperations(ops, '', '').length, 2);
});

test('fmtMs switches units', () => {
  assert.equal(fmtMs(5), '5 ms');
  assert.equal(fmtMs(999), '999 ms');
  assert.equal(fmtMs(1500), '1.50 s');
  assert.equal(fmtMs(25000), '25.0 s');
  assert.equal(fmtMs(null), '');
});

test('phaseBars sums to 100 by nudging the biggest phase', () => {
  const rows = phaseBars([{ name: 'requests', ms: 10, percent: 33.3 }, { name: 'tests', ms: 1, percent: 33.3 }, { name: 'other', ms: 1, percent: 33.3 }]);
  const sum = rows.reduce((a, r) => a + r.pct, 0);
  assert.ok(Math.abs(sum - 100) < 1e-6, String(sum));
  assert.equal(phaseBars([]).length, 0);
});

test('timingSummaryText renders every bucket', () => {
  const t = timingSummaryText({ requests: 4, wallMs: 1000, requestMs: 400, testMs: 10, otherMs: 590 });
  assert.equal(t, '4 requests · wall 1.00 s · requests 400 ms · tests 10 ms · other 590 ms');
});

test('sortedOutliers is slowest first and does not mutate input', () => {
  const list = [{ durationMs: 10 }, { durationMs: 900 }, { durationMs: 50 }];
  const sorted = sortedOutliers(list);
  assert.deepEqual(sorted.map((o) => o.durationMs), [900, 50, 10]);
  assert.equal(list[0].durationMs, 10);
});

test('diffHasChanges and diffSummaryText', () => {
  assert.equal(diffHasChanges({ equal: true }), false);
  assert.equal(diffHasChanges({ equal: false }), true);
  assert.equal(diffSummaryText({ equal: true }), 'Matches the saved example.');
  assert.equal(
    diffSummaryText({ equal: false, status: { changed: true, expected: 200, actual: 500 }, headers: [{}], json: [{}, {}], changesCapped: true }),
    'status 200 → 500 · 1 header change · 2 field changes · more changes not shown',
  );
  assert.equal(diffSummaryText({ equal: false, bodyChanged: true }), 'body changed');
  assert.equal(diffSummaryText({ equal: false }), 'Response differs.');
});

test('handoffSummaryText lists the attack type and positions', () => {
  assert.equal(handoffSummaryText({ attackType: 'pitchfork', positions: ['id', 'uid'] }), 'pitchfork · 2 positions (id, uid)');
  assert.equal(handoffSummaryText(null), '');
});

test('matrixRunIdentities mirrors the server: skips broken and header-less, appends anonymous', () => {
  const got = matrixRunIdentities([
    { name: 'admin', headers: 'Cookie: a=1' },
    { name: 'locked', headers: 'Cookie: b=1', broken: true },
    { name: 'empty', headers: '  ' },
    { name: 'user', headers: 'Authorization: x' },
    { name: 'Admin', headers: 'Cookie: dup' },
    { name: '', headers: 'Cookie: noname' },
  ]);
  assert.deepEqual(got, ['admin', 'user', 'anonymous']);
});

test('matrixRunIdentities with nothing saved still runs anonymous, and caps at the server limit', () => {
  assert.deepEqual(matrixRunIdentities(null), ['anonymous']);
  const many = Array.from({ length: 20 }, (_, i) => ({ name: 'u' + i, headers: 'X: ' + i }));
  assert.equal(matrixRunIdentities(many).length, MATRIX_MAX_IDENTITIES);
  assert.equal(MATRIX_MAX_IDENTITIES, 12);
});

test('latestRunUid takes the newest run so timing needs no pasted UID', () => {
  assert.equal(latestRunUid([{ uid: 'new' }, { uid: 'old' }]), 'new');
  assert.equal(latestRunUid([{}, { uid: 'x' }]), 'x');
  assert.equal(latestRunUid([]), '');
  assert.equal(latestRunUid(null), '');
});

test('timingEmpty is true for a run that sent nothing', async () => {
  const { timingEmpty } = await import('../js/collections-matrix-model.js');
  assert.equal(timingEmpty(null), true);
  assert.equal(timingEmpty({ requests: 0, items: [] }), true);
  assert.equal(timingEmpty({ requests: 2, items: [] }), true);
  assert.equal(timingEmpty({ requests: 2, items: [{ name: 'a' }] }), false);
});

// Round 4 (a5) B3: a matrix cell's tooltip carries the cause, not "blocked — blocked".
test('cellTitle names why a cell was blocked or errored', () => {
  assert.equal(cellTitle('admin', { class: 'blocked', reason: 'out_of_scope' }), 'admin: blocked — The target host is out of scope');
  assert.equal(cellTitle('admin', { class: 'blocked', reason: 'ip_guard', error: 'destination blocked by IP guard: 127.0.0.1 resolves to non-public address' }),
    'admin: blocked — destination blocked by IP guard: 127.0.0.1 resolves to non-public address');
  assert.equal(cellTitle('user', { class: 'error', error: 'Script prerequest: error: boom' }), 'user: error — Script prerequest: error: boom');
  assert.equal(cellTitle('user', { class: 'error' }), 'user: error — the request failed before a response');
  assert.equal(cellTitle('user', { class: 'success', status: 200, flag: 'violation', reason: 'candidate cross-object access' }),
    'user: success (HTTP 200) — unexpected access — candidate cross-object access');
  assert.equal(cellTitle('user', { class: 'success', status: 200, sameAsBaseline: true }), 'user: success (HTTP 200) — same as baseline');
});

test('untrustedScriptCount counts quarantined scripts only', async () => {
  const { untrustedScriptCount } = await import('../js/collections-matrix-model.js');
  assert.equal(untrustedScriptCount(null), 0);
  assert.equal(untrustedScriptCount({ scripts: [{ trusted: true }, { trusted: false }, {}] }), 2);
});
