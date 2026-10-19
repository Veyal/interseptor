import { test } from 'node:test';
import assert from 'node:assert/strict';
import { renamedTabLabel, placeAfter } from '../js/repeater-tab-model.js';

// Regression: Esc or Cancel in "Rename tab…" wiped an existing custom name.
test('cancelling a rename keeps the current name; an empty name clears it', () => {
  assert.equal(renamedTabLabel('Login flow', null), 'Login flow');
  assert.equal(renamedTabLabel('Login flow', undefined), 'Login flow');
  assert.equal(renamedTabLabel('Login flow', '  '), '');
  assert.equal(renamedTabLabel('', ' Admin '), 'Admin');
  assert.equal(renamedTabLabel('', 'x'.repeat(80)).length, 60);
});

// Regression: Duplicate Tab appended the copy at the far end of the strip.
test('a duplicate sits right after its original', () => {
  const a = { tid: 1 }, b = { tid: 2 }, c = { tid: 3 }, copy = { tid: 4 };
  const tabs = [a, b, c, copy];
  placeAfter(tabs, copy, a);
  assert.deepEqual(tabs.map((t) => t.tid), [1, 4, 2, 3]);
  placeAfter(tabs, copy, c);
  assert.deepEqual(tabs.map((t) => t.tid), [1, 2, 3, 4]);
});

// Regression: WebKit does not focus a clicked button, so after a mouse-started
// Send focus stayed outside the Repeater panel and Esc (bound to the panel)
// never cancelled; the Diff panel's Esc had the same dependency. Esc is now
// decided at document level while the Repeater is the active panel.
import { repeaterEscapeAction } from '../js/repeater-tab-model.js';
test('Esc in the active Repeater closes the diff, else cancels a send', () => {
  const base = { panelActive: true, modalOpen: false, defaultPrevented: false, diffOpen: false, sendPending: false };
  assert.equal(repeaterEscapeAction({ ...base, sendPending: true }), 'cancel');
  assert.equal(repeaterEscapeAction({ ...base, diffOpen: true, sendPending: true }), 'diff');
  assert.equal(repeaterEscapeAction(base), null);
  assert.equal(repeaterEscapeAction({ ...base, sendPending: true, panelActive: false }), null);
  assert.equal(repeaterEscapeAction({ ...base, sendPending: true, modalOpen: true }), null);
  assert.equal(repeaterEscapeAction({ ...base, sendPending: true, defaultPrevented: true }), null);
});

test('Esc closes the History rail after the diff and an in-flight send', () => {
  const base = { panelActive: true, modalOpen: false, defaultPrevented: false, diffOpen: false, sendPending: false, historyOpen: true };
  assert.equal(repeaterEscapeAction(base), 'history');
  assert.equal(repeaterEscapeAction({ ...base, sendPending: true }), 'cancel');
  assert.equal(repeaterEscapeAction({ ...base, diffOpen: true }), 'diff');
  assert.equal(repeaterEscapeAction({ ...base, modalOpen: true }), null);
  assert.equal(repeaterEscapeAction({ ...base, panelActive: false }), null);
});
