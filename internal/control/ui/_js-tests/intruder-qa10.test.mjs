import test from 'node:test';
import assert from 'node:assert/strict';
import { intrRequestCount, intrValidateRegex, intrValidateTarget, intrSortRows, INTR_MAX_DELAY_MS } from '../js/intruder.js';

test('battering ram sends one request per payload, sniper one per payload per marker', () => {
  assert.equal(intrRequestCount('battering', [2], 2), 2);
  assert.equal(intrRequestCount('sniper', [2], 2), 4);
  assert.equal(intrRequestCount('sniper', [3], 0), 3);
  assert.equal(intrRequestCount('pitchfork', [3, 5], 2), 3);
  assert.equal(intrRequestCount('cluster', [3, 5], 2), 15);
  assert.equal(intrRequestCount('cluster', [], 0), 0);
});

test('extract regex validation rejects uncompilable patterns only', () => {
  assert.equal(intrValidateRegex('Extract', ''), '');
  assert.equal(intrValidateRegex('Extract', 'id=(\\d+)'), '');
  assert.match(intrValidateRegex('Extract', '(unclosed'), /Extract is not a valid regular expression/);
  assert.match(intrValidateRegex('Extract', '(['), /valid regular expression/);
});

test('target must be an http(s) URL with a host', () => {
  assert.equal(intrValidateTarget('https://example.com'), '');
  assert.equal(intrValidateTarget('http://example.com:8080'), '');
  assert.match(intrValidateTarget(''), /enter a target/);
  assert.match(intrValidateTarget('foo'), /https:\/\/host/);
  assert.match(intrValidateTarget('ftp://example.com'), /http/);
});

test('delay cap is finite and sane', () => {
  assert.ok(INTR_MAX_DELAY_MS >= 1000 && INTR_MAX_DELAY_MS <= 600000);
});

test('default # ascending sort orders by attempt id, not completion order', () => {
  const rows = [{ id: 1 }, { id: 4 }, { id: 3 }, { id: 5 }, { id: 2 }];
  assert.deepEqual(intrSortRows(rows, { col: 'id', dir: 'asc' }).map((r) => r.id), [1, 2, 3, 4, 5]);
  assert.deepEqual(intrSortRows(rows, { col: 'id', dir: 'desc' }).map((r) => r.id), [5, 4, 3, 2, 1]);
  assert.deepEqual(rows.map((r) => r.id), [1, 4, 3, 5, 2], 'input is not mutated');
  assert.deepEqual(intrSortRows([{ payload: 'b' }, { payload: 'a' }], { col: 'payload', dir: 'asc' }).map((r) => r.payload), ['a', 'b']);
});

test('result row tooltip carries the full payload', async () => {
  const { intrRowTitle } = await import('../js/intruder.js');
  assert.equal(intrRowTitle({ id: 3, payload: 'long-payload-value', flowId: 9 }), '#3 · long-payload-value · open flow #9');
  assert.equal(intrRowTitle({ id: 4, payload: 'x', error: 'send failed' }), '#4 · x · send failed');
  assert.equal(intrRowTitle({ id: 5, payload: '' }), '#5');
});

test('repeat count validates instead of printing a nonsense count', async () => {
  const { intrRepeatError, intrCountLabel } = await import('../js/intruder.js');
  for (const v of ['20', '1', '2000', 20]) assert.equal(intrRepeatError(v), '', String(v));
  for (const v of ['-3', '0', '2001', '1.5', '', 'abc']) assert.match(intrRepeatError(v), /between 1 and 2000/, String(v));
  assert.equal(intrCountLabel('repeat', { repeat: '20' }), '20 sends');
  assert.equal(intrCountLabel('repeat', { repeat: '1' }), '1 send');
  for (const v of ['-3', '0', '2001', '1.5']) assert.doesNotMatch(intrCountLabel('repeat', { repeat: v }), /\d+ sends?$/, v);
  assert.match(intrCountLabel('repeat', { repeat: '-3' }), /invalid/i);
});

test('numbers range reports a validation message for unusable settings', async () => {
  const { intrNumbersError } = await import('../js/intruder.js');
  assert.equal(intrNumbersError({ start: 1, end: 10, step: 1, mode: 'sequence' }), '');
  assert.equal(intrNumbersError({ start: 10, end: 1, step: -2, mode: 'sequence' }), '');
  assert.match(intrNumbersError({ start: 1, end: 10, step: -1, mode: 'sequence' }), /step/i);
  assert.match(intrNumbersError({ start: NaN, end: 10, step: 1, mode: 'sequence' }), /start and end/i);
  assert.match(intrNumbersError({ start: 1, end: 10, step: 1.5, mode: 'sequence' }), /whole/i);
  assert.match(intrNumbersError({ start: 1, end: 10, mode: 'random', count: 0 }), /how many/i);
});

test('a stopped run reads Stopped at N/M, not done', async () => {
  const { intrProgressLabel, runSummaryText } = await import('../js/intruder.js');
  assert.equal(intrProgressLabel({ running: true, done: 3, total: 10 }), 'running 3/10');
  assert.equal(intrProgressLabel({ running: false, done: 10, total: 10 }), 'done 10/10');
  assert.equal(intrProgressLabel({ running: false, done: 10, total: 10, capped: true }), 'done 10/10 (capped)');
  assert.equal(intrProgressLabel({ running: false, done: 16, total: 500 }), 'Stopped at 16/500');
  assert.equal(intrProgressLabel({ running: false, done: 0, total: 0 }), '');
  assert.match(runSummaryText({ done: 16, total: 500, running: false }), /^Stopped at 16 of 500 requests/);
  assert.match(runSummaryText({ done: 500, total: 500, running: false }), /^500 of 500 requests/);
  assert.match(runSummaryText({ done: 16, total: 500, running: true }), /^16 of 500 requests/);
});

test('result chips are icon-only in rows and carry the full label as a tooltip', async () => {
  const { chipsHTML } = await import('../js/intruder.js');
  const html = chipsHTML({ status: 500, flagged: true }, { status: 200, length: 0, timeMs: 0 }, (s) => s);
  assert.match(html, /title="Status differs"/);
  assert.match(html, /aria-label="Flagged"/);
  assert.match(html, /<span class="intr-chip-text">Status differs<\/span>/);
});

test('sorting by St/Len/Time breaks ties by attempt id', () => {
  const rows = [
    { id: 5, status: 200 }, { id: 1, status: 401 }, { id: 3, status: 200 },
    { id: 2, status: 200 }, { id: 6, status: 401 },
  ];
  assert.deepEqual(intrSortRows(rows, { col: 'status', dir: 'asc' }).map((r) => r.id), [2, 3, 5, 1, 6]);
  assert.deepEqual(intrSortRows(rows, { col: 'status', dir: 'desc' }).map((r) => r.id), [1, 6, 2, 3, 5]);
  assert.deepEqual(intrSortRows([{ id: 2, payload: 'a' }, { id: 1, payload: 'a' }], { col: 'payload', dir: 'asc' }).map((r) => r.id), [1, 2]);
});
