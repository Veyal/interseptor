// Node tests for the pure run-state helpers behind the Intruder results pane:
// folding the bounded state head into the shown rows, and saving the whole
// payload list in a preset.
import test from 'node:test';
import assert from 'node:assert/strict';
import { mergeLiveHead, presetFromEditor } from '../js/intruder.js';

const row = (id, extra = {}) => ({ id, payload: 'p' + id, status: 200, length: 10, timeMs: 5, ...extra });

test('mergeLiveHead replaces the head in place so finished-run flags are shown', () => {
  // The live head arrives without derived flags; the finished state carries the
  // same rows with flagged/anomaly applied. A same-length head must still win.
  const live = [row(1), row(2)];
  const finished = [row(1, { flagged: true }), row(2, { matched: true })];
  const merged = mergeLiveHead(live, finished);
  assert.deepEqual(merged.map((r) => [r.id, !!r.flagged, !!r.matched]), [[1, true, false], [2, false, true]]);
});

test('mergeLiveHead keeps the paged tail past the head', () => {
  const shown = [row(1), row(2), row(3), row(4)];
  const head = [row(1, { flagged: true }), row(2)];
  assert.deepEqual(mergeLiveHead(shown, head).map((r) => r.id), [1, 2, 3, 4]);
  assert.equal(mergeLiveHead(shown, head)[0].flagged, true);
});

test('mergeLiveHead replaces everything when the run changed', () => {
  const shown = [row(1), row(2), row(3)];
  assert.deepEqual(mergeLiveHead(shown, [row(9)], true).map((r) => r.id), [9]);
  assert.deepEqual(mergeLiveHead(shown, [], true), []);
});

test('mergeLiveHead tolerates empty heads and never aliases its arrays', () => {
  const shown = [row(1)];
  const head = [row(2)];
  const merged = mergeLiveHead(shown, head);
  assert.notEqual(merged, head);
  merged.push(row(3));
  assert.equal(head.length, 1);
  assert.deepEqual(mergeLiveHead(null, [row(3)]).map((r) => r.id), [3]);
  assert.deepEqual(mergeLiveHead([row(4)], null).map((r) => r.id), [4]);
});

test('presetFromEditor keeps payload lists larger than the tab preview', () => {
  const big = Array.from({ length: 1000 }, (_, i) => 'payload-' + i);
  const snapshot = {
    target: 'https://example.com', template: 'x=§a§', type: 'sniper', sniper: big.slice(0, 40).join('\n'), pos: ['§a§'],
    sniperLines: big, posLines: [big], sniperFile: 'list.txt', posFiles: ['pos.txt'],
    sniperSource: 'list', sniperNums: { start: 1 }, posSources: ['list'], posNums: [{ start: 2 }],
    threads: 2, delay: 5, repeat: 20, grep: '', extract: '', proc: '',
  };
  const p = presetFromEditor(snapshot, 'Attack');
  assert.equal(p.name, 'Attack');
  assert.equal(p.sniperLines.length, 1000, 'the full single list is saved, not its 40-line preview');
  assert.equal(p.posLines[0].length, 1000, 'the full per-position list is saved');
  assert.equal(p.sniperFile, 'list.txt');
  // Arrays are copied, so later editor edits cannot mutate a saved preset.
  snapshot.sniperLines.push('later');
  assert.equal(p.sniperLines.length, 1000);
  assert.deepEqual(presetFromEditor({ sniperLines: [] }, 'x').sniperLines, []);
});
