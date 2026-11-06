// Node tests for the Intruder's run-snapshot vs display-state split: which view
// may rebase the active run's rows, which rows a finished run contributes to
// History, and which attempt the row actions target.
//
// The three helpers here are the decisions the DOM wiring in tools.js makes on
// every poll / capture / row click; the tests replay those call sites so a
// regression shows up as wrong rows, not as a diff in a string.
import test from 'node:test';
import assert from 'node:assert/strict';
import { mergeLiveHead, pollRebasesRunRows, intrCaptureRows, intrRowSelection } from '../js/intruder.js';

const row = (id, extra = {}) => ({ id, payload: 'p' + id, status: 200, length: 10, timeMs: 5, ...extra });
const ids = (rows) => rows.map((r) => r.id);

// renderIntr's authoritative branch, in the order tools.js runs it: merge the
// state head into the live view, then rebase the run snapshot behind the
// ownership guard.
function poll(state, { runId, results }) {
  const liveBelongsHere = state.runTid == null || state.runTid === state.activeTid;
  if (state.owner === 'live' && liveBelongsHere) {
    const newRun = runId !== state.pageRunKey;
    if (newRun) state.pageRunKey = runId;
    state.displayed = mergeLiveHead(state.displayed, results, newRun);
  }
  if (pollRebasesRunRows(state.owner, liveBelongsHere)) state.runRows = state.displayed.slice();
  return state;
}

// intrPageResults' append, which is what makes the paged tail survive a poll.
function page(state, rows) {
  state.displayed = state.displayed.concat(rows);
  if (pollRebasesRunRows(state.owner, state.runTid == null || state.runTid === state.activeTid)) {
    state.runRows = state.displayed.slice();
  }
  return state;
}

test('the run snapshot is rebased only while the live view shows that run', () => {
  assert.equal(pollRebasesRunRows('live', true), true);
  // History owns the pane: the displayed array is another run's rows.
  assert.equal(pollRebasesRunRows('history', true), false);
  // The run belongs to another attack tab: the pane shows nothing of it.
  assert.equal(pollRebasesRunRows('live', false), false);
  assert.equal(pollRebasesRunRows('history', false), false);
  assert.equal(pollRebasesRunRows(undefined, true), false);
  assert.equal(pollRebasesRunRows('live', undefined), false);
});

test('a poll while History is open leaves the run rows intact', () => {
  const state = { runTid: 1, activeTid: 1, owner: 'live', displayed: [], runRows: [], pageRunKey: '' };
  poll(state, { runId: 'run-1', results: [row(1), row(2)] });
  page(state, [row(3), row(4)]);
  assert.deepEqual(ids(state.runRows), [1, 2, 3, 4], 'the paged tail belongs to the run snapshot');

  // The operator opens an earlier run in History (3 rows of a different run).
  state.owner = 'history';
  state.displayed = [row(91), row(92), row(93)];
  poll(state, { runId: 'run-1', results: [row(1), row(2)] });
  assert.deepEqual(ids(state.runRows), [1, 2, 3, 4], 'the history view must not become the run snapshot');

  // Back to "Live / current run": the pane is restored from the run's own rows,
  // so the paging offset counts this run again instead of the history's 3 rows.
  state.owner = 'live';
  state.displayed = state.runRows.slice();
  assert.deepEqual(ids(state.displayed), [1, 2, 3, 4]);
  poll(state, { runId: 'run-1', results: [row(1, { flagged: true }), row(2)] });
  assert.deepEqual(ids(state.displayed), [1, 2, 3, 4]);
  assert.equal(state.displayed[0].flagged, true, 'the finished head still replaces its prefix');
});

test('a poll for another attack tab does not wipe the run rows', () => {
  const state = { runTid: 1, activeTid: 1, owner: 'live', displayed: [], runRows: [], pageRunKey: '' };
  poll(state, { runId: 'run-1', results: [row(1), row(2)] });
  state.activeTid = 2; // the run belongs to tab 1; tab 2 is now active
  poll(state, { runId: 'run-1', results: [row(1), row(2)] });
  assert.deepEqual(ids(state.runRows), [1, 2], 'the run keeps its rows while another tab is active');
});

// intrPageResults' completion: the page is applied only while the pane still
// shows the run it was fetched for (checked after the await, like the run id).
function pageLands(state, rows) {
  const liveBelongsHere = state.runTid == null || state.runTid === state.activeTid;
  if (!pollRebasesRunRows(state.owner, liveBelongsHere)) return false;
  state.displayed = state.displayed.concat(rows);
  state.runRows = state.displayed.slice();
  return true;
}

test('a page that lands after the operator left the run is dropped', () => {
  const state = { runTid: 1, activeTid: 1, owner: 'live', displayed: [row(1)], runRows: [row(1)] };
  assert.equal(pageLands(state, [row(2)]), true);
  assert.deepEqual(ids(state.displayed), [1, 2]);
  assert.deepEqual(ids(state.runRows), [1, 2]);

  // An earlier run is opened in History while the page is in flight.
  state.owner = 'history';
  state.displayed = [row(81), row(82)];
  assert.equal(pageLands(state, [row(3)]), false);
  assert.deepEqual(ids(state.displayed), [81, 82], 'the history view is not spliced with run rows');
  assert.deepEqual(ids(state.runRows), [1, 2], 'and the run snapshot keeps only its own rows');

  // Same for another attack tab: the run's page must not paint into its pane.
  state.owner = 'live';
  state.activeTid = 2;
  assert.equal(pageLands(state, [row(4)]), false);
  assert.deepEqual(ids(state.displayed), [81, 82]);
});

// intrCaptureHistory's body: the ownership gate, the completion wait, and the
// recorded entry.
function capture(state, done, force = false) {
  const rows = intrCaptureRows(state.owner, state.displayed, state.runRows);
  if (!rows) return null;
  if (!force && rows.length < done) return 'wait';
  return { results: rows.slice(0, 5000), flagged: rows.filter((r) => r.flagged).length };
}

test('History records the run own rows, never the view that happens to be open', () => {
  const state = { owner: 'live', displayed: [row(1), row(2, { flagged: true }), row(3)], runRows: [] };
  state.runRows = state.displayed.slice();
  assert.deepEqual(ids(capture(state, 3).results), [1, 2, 3]);
  assert.equal(capture(state, 3).flagged, 1);

  // History owns the pane and its run is longer than the finished one: the old
  // code captured those three foreign rows (and their flags) under this run.
  state.owner = 'history';
  state.displayed = [row(71, { flagged: true }), row(72, { flagged: true }), row(73), row(74)];
  assert.equal(capture(state, 3), null, 'a History view contributes nothing to the capture');
  state.displayed = [row(71)]; // shorter than the run: must not block it either
  assert.equal(capture(state, 3), null);
});

test('capture waits for the paged tail and copies what it records', () => {
  const state = { owner: 'live', displayed: [row(1)], runRows: [row(1)] };
  assert.equal(capture(state, 3), 'wait', 'a partial set waits for the pager');
  const entry = capture(state, 1);
  assert.deepEqual(ids(entry.results), [1]);
  entry.results.push(row(2));
  assert.deepEqual(ids(state.displayed), [1], 'the entry owns its array, not the view');
  assert.deepEqual(intrCaptureRows('live', null, [row(9)]), [row(9)], 'a missing view falls back to the run rows');
  assert.deepEqual(intrCaptureRows('live', null, null), []);
});

test('result-row actions never keep a previous attempt armed', () => {
  assert.equal(intrRowSelection(12), 12);
  assert.equal(intrRowSelection('12'), 12);
  // A failed attempt has no captured flow: the selection must clear, otherwise
  // Attach / Diff would still act on the row picked before it.
  assert.equal(intrRowSelection(0), 0);
  assert.equal(intrRowSelection(undefined), 0);
  assert.equal(intrRowSelection(''), 0);
  assert.equal(intrRowSelection('abc'), 0);
  assert.equal(intrRowSelection(-3), 0);
  assert.equal(intrRowSelection(12.5), 0);

  // The click → selection path: 12, then a flowless row, then another attempt.
  let active = 0;
  const pick = (el) => {
    const next = intrRowSelection(Number(el.dataset.flow) || 0);
    if (next !== active) active = next;
    return active;
  };
  assert.equal(pick({ dataset: { flow: '12' } }), 12);
  assert.equal(pick({ dataset: { flow: '' } }), 0);
  assert.equal(pick({ dataset: { flow: '31' } }), 31);
});
