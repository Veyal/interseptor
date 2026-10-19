// desktop-panel-tools-model.js: the pure rules behind the desktop skin of the
// test panels (sentence-case labels, the Send chord, the merged Intruder run
// line, the Collections resting line and the overflow menu keys and placement).
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { sentenceCase, sendHint, stripGlyph, runLine, isCleanDirtyText, overflowStep, placeMenu } from '../js/desktop-panel-tools-model.js';

test('sentenceCase rewrites ALL-CAPS labels and leaves sentences alone', () => {
  assert.equal(sentenceCase('REQUEST'), 'Request');
  assert.equal(sentenceCase('REQUEST TEMPLATE'), 'Request template');
  assert.equal(sentenceCase('ALL § POSITIONS'), 'All § positions');
  assert.equal(sentenceCase('Headers'), 'Headers');
  assert.equal(sentenceCase('use §…§ for injection points'), 'use §…§ for injection points');
  assert.equal(sentenceCase('§'), '§', 'no letters, nothing to change');
  assert.equal(sentenceCase('  BODY '), 'Body');
  assert.equal(sentenceCase(null), '');
  assert.equal(sentenceCase(undefined), '');
});

test('sendHint writes the platform chord', () => {
  assert.equal(sendHint(true), '⌘↩');
  assert.equal(sendHint(false), 'Ctrl+↩');
});

test('stripGlyph drops the decorative trailing arrow only', () => {
  assert.equal(stripGlyph('Send ▸'), 'Send');
  assert.equal(stripGlyph('Start ▸'), 'Start');
  assert.equal(stripGlyph('Running…'), 'Running…');
  assert.equal(stripGlyph('▸ Menu'), '▸ Menu');
  assert.equal(stripGlyph(null), '');
});

test('runLine merges the summary and the flagged count into one sentence', () => {
  assert.equal(runLine('15 of 15 requests · 0 errors · 1 anomaly', '15 sent · 6 flagged · 6 interesting'), '15 of 15 requests · 6 flagged · 0 errors · 1 anomaly');
  assert.equal(runLine('3 of 20 requests · 4.5 req/s · 0 errors · 0 anomalies', '3 sent · 1 flagged'), '3 of 20 requests · 4.5 req/s · 1 flagged · 0 errors · 0 anomalies');
  assert.equal(runLine('15 of 15 requests · 0 errors · 0 anomalies', '15 sent'), '15 of 15 requests · 0 errors · 0 anomalies', 'nothing flagged: the summary as is');
  assert.equal(runLine('', '1 flagged'), '', 'no attack, no line');
  assert.equal(runLine('No attack running', ''), 'No attack running');
  assert.equal(runLine('15 of 15 requests', '1,200 sent · 1,200 flagged'), '15 of 15 requests · 1,200 flagged');
});

test('isCleanDirtyText recognises only the resting Collections line', () => {
  assert.equal(isCleanDirtyText('Matches the stored request'), true);
  assert.equal(isCleanDirtyText('  matches the stored request '), true);
  assert.equal(isCleanDirtyText('Unsaved changes'), false);
  assert.equal(isCleanDirtyText(''), false);
});

test('overflowStep wraps arrows and jumps with Home and End', () => {
  assert.equal(overflowStep('ArrowDown', 0, 3), 1);
  assert.equal(overflowStep('ArrowDown', 2, 3), 0);
  assert.equal(overflowStep('ArrowUp', 0, 3), 2);
  assert.equal(overflowStep('ArrowUp', 2, 3), 1);
  assert.equal(overflowStep('Home', 2, 3), 0);
  assert.equal(overflowStep('End', 0, 3), 2);
  assert.equal(overflowStep('a', 0, 3), -1);
  assert.equal(overflowStep('ArrowDown', 0, 0), -1);
});

test('placeMenu right-aligns under the trigger and flips or clamps to the viewport', () => {
  const vp = { width: 1000, height: 700 };
  // Under the trigger, right edges aligned.
  assert.deepEqual(placeMenu({ right: 900, bottom: 100, top: 72 }, { width: 200, height: 150 }, vp), { left: 700, top: 104 });
  // Near the bottom: opens above the trigger.
  assert.deepEqual(placeMenu({ right: 900, bottom: 660, top: 632 }, { width: 200, height: 150 }, vp), { left: 700, top: 478 });
  // A trigger near the left edge clamps to the margin.
  assert.deepEqual(placeMenu({ right: 60, bottom: 100, top: 72 }, { width: 200, height: 150 }, vp), { left: 8, top: 104 });
  // A trigger past the right edge clamps inside.
  assert.deepEqual(placeMenu({ right: 1200, bottom: 100, top: 72 }, { width: 200, height: 150 }, vp), { left: 792, top: 104 });
});
