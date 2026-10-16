import test from 'node:test';
import assert from 'node:assert/strict';
import { previousHistoryEntry, diffModel, phoneViewAfterSend, nextPhoneView, attachModel, proofNote, splitRawMessage } from '../js/repeater.js';
import { outlierChips, baselineOf, progressModel, createRateTracker, runSummaryText, needsLargeRunConfirm, LARGE_RUN, stepperNext, resultFlowId } from '../js/intruder.js';

test('previousHistoryEntry returns the next-older send or null', () => {
  const h = [{ id: 9 }, { id: 7 }, { id: 3 }];
  assert.equal(previousHistoryEntry(h, 9).id, 7);
  assert.equal(previousHistoryEntry(h, 7).id, 3);
  assert.equal(previousHistoryEntry(h, 3), null);
  assert.equal(previousHistoryEntry(h, 99), null);
  assert.equal(previousHistoryEntry(null, 1), null);
});

test('diffModel explains why a diff is unavailable and never fakes one', () => {
  assert.equal(diffModel({ resId: 0, history: [] }).ok, false);
  assert.match(diffModel({ resId: 0, history: [] }).reason, /send/i);
  const one = diffModel({ resId: 5, history: [{ id: 5 }] });
  assert.equal(one.ok, false);
  assert.match(one.reason, /earlier|previous/i);
  const two = diffModel({ resId: 5, history: [{ id: 5 }, { id: 4 }] });
  assert.deepEqual([two.ok, two.currentId, two.previousId], [true, 5, 4]);
});

test('phone view switches to the response after a send and cycles', () => {
  assert.equal(phoneViewAfterSend(), 'res');
  assert.equal(nextPhoneView('req'), 'res');
  assert.equal(nextPhoneView('res'), 'req');
  assert.equal(nextPhoneView('bogus'), 'req');
});

test('attachModel disables with a reason until a response flow exists', () => {
  assert.equal(attachModel({ resId: 0, sendPending: false }).disabled, true);
  assert.match(attachModel({ resId: 0 }).title, /send/i);
  assert.equal(attachModel({ resId: 4, sendPending: true }).disabled, true);
  const ok = attachModel({ resId: 4 });
  assert.equal(ok.disabled, false);
  assert.deepEqual(ok.spec, { kind: 'flow', refs: [4] });
  assert.equal(attachModel({ resId: 4 }, { proof: true }).spec.note, proofNote());
});

test('splitRawMessage splits head and body on the first blank line', () => {
  assert.deepEqual(splitRawMessage('HTTP/1.1 200 OK\r\nA: b\r\n\r\nhello\n\nworld'), { head: 'HTTP/1.1 200 OK\nA: b', body: 'hello\n\nworld' });
  assert.deepEqual(splitRawMessage('only head'), { head: 'only head', body: '' });
});

test('baselineOf uses the modal status and median length of non-error rows', () => {
  const rows = [
    { id: 1, status: 200, length: 100 }, { id: 2, status: 200, length: 110 }, { id: 3, status: 403, length: 10 },
    { id: 4, status: 200, length: 120 }, { id: 5, error: 'x', status: 0, length: 0 },
  ];
  assert.deepEqual(baselineOf(rows), { status: 200, length: 110, timeMs: 0 });
  assert.equal(baselineOf([]), null);
  assert.equal(baselineOf([{ error: 'x' }]), null);
});

test('outlierChips always carry text, never colour alone', () => {
  const base = { status: 200, length: 100, timeMs: 50 };
  assert.deepEqual(outlierChips({ status: 200, length: 100, timeMs: 50 }, base), []);
  const chips = outlierChips({ status: 500, length: 400, timeMs: 900, flagged: true, matched: true, anomaly: true }, base);
  const labels = chips.map((c) => c.label);
  assert.ok(labels.includes('Status differs'));
  assert.ok(labels.includes('Longer'));
  assert.ok(labels.includes('Slower'));
  assert.ok(labels.includes('Flagged'));
  assert.ok(labels.includes('Matched'));
  assert.ok(chips.every((c) => c.label && c.kind));
  assert.deepEqual(outlierChips({ error: 'boom' }, base).map((c) => c.label), ['Error']);
  assert.ok(outlierChips({ status: 200, length: 10, timeMs: 50, anomaly: true }, base).some((c) => c.label === 'Shorter'));
  assert.deepEqual(outlierChips({ status: 500, length: 100, timeMs: 50 }, null).map((c) => c.label), []);
});

test('progressModel is determinate and always has text', () => {
  assert.deepEqual(progressModel(0, 0), { value: 0, max: 1, percent: 0, text: 'No attack running' });
  assert.deepEqual(progressModel(25, 100), { value: 25, max: 100, percent: 25, text: '25 of 100 requests (25%)' });
  assert.equal(progressModel(500, 100).value, 100);
  assert.equal(progressModel(-3, 100).value, 0);
});

test('rate tracker reports requests per second and resets between runs', () => {
  let now = 1000;
  const r = createRateTracker(() => now);
  assert.equal(r.update(0, true), 0);
  now = 3000;
  assert.equal(r.update(20, true), 10);
  r.reset();
  assert.equal(r.update(5, true), 0);
});

test('runSummaryText lists requests, rate, errors and anomalies', () => {
  const txt = runSummaryText({ done: 40, total: 100, rate: 12.5, results: [{ error: 'x' }, { anomaly: true }, { anomaly: true }, { status: 200 }] });
  assert.match(txt, /40 of 100 requests/);
  assert.match(txt, /12\.5 req\/s/);
  assert.match(txt, /1 error/);
  assert.match(txt, /2 anomalies/);
  assert.match(runSummaryText({ done: 0, total: 0, rate: 0, results: [] }), /No attack/);
  assert.match(runSummaryText({ done: 3, total: 3, rate: 0, results: [{ error: 'x' }] }), /1 error\b/);
});

test('runs over 1000 requests need an inline confirm', () => {
  assert.equal(LARGE_RUN, 1000);
  assert.equal(needsLargeRunConfirm(1000), false);
  assert.equal(needsLargeRunConfirm(1001), true);
  assert.equal(needsLargeRunConfirm(NaN), false);
});

test('stepperNext clamps within positions, payloads, results', () => {
  assert.equal(stepperNext('positions', 1), 'payloads');
  assert.equal(stepperNext('results', 1), 'results');
  assert.equal(stepperNext('positions', -1), 'positions');
  assert.equal(stepperNext('nope', 1), 'payloads');
});

test('resultFlowId accepts both flowId spellings and rejects missing flows', () => {
  assert.equal(resultFlowId({ flowId: 7 }), 7);
  assert.equal(resultFlowId({ flowID: 8 }), 8);
  assert.equal(resultFlowId({ flowId: 0 }), 0);
  assert.equal(resultFlowId(null), 0);
});
