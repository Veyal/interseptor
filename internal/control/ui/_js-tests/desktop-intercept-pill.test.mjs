import { test } from 'node:test';
import assert from 'node:assert/strict';
import { interceptPillModel, statusBarModel, OVERLAY } from '../js/desktop-model.js';

// Regression: with only Responses on, the title-bar pill read "Intercept off · 1
// held" while the status bar read "Intercept on". The pill still toggles
// requests (its pressed state), but its text names what is being held.
test('the pill and the status bar agree when only responses are intercepted', () => {
  const ic = { enabled: false, responseEnabled: true, held: 1 };
  const pill = interceptPillModel(ic);
  assert.equal(pill.text, 'Responses on · 1 held');
  assert.equal(pill.pressed, false);
  assert.match(pill.title, /Request intercept is off; responses are held/);
  assert.match(statusBarModel({ intercept: ic, scope: {} }).intercept.text, /^Intercept on/);
});

test('pill states', () => {
  assert.deepEqual(interceptPillModel({}).text, 'Intercept off');
  assert.equal(interceptPillModel({ enabled: true, held: 2 }).text, 'Intercept on · 2 held');
  assert.equal(interceptPillModel({ enabled: true }).pressed, true);
  assert.equal(interceptPillModel({ enabled: false, held: 3 }).text, 'Intercept off · 3 held');
  assert.equal(interceptPillModel({ enabled: true, responseEnabled: true }).label, 'Request intercept on. Responses are also held. Toggle request intercept.');
});

test('an unknown intercept state is never reported as off', () => {
  const pill = interceptPillModel({ unknown: true });
  assert.equal(pill.text, 'Intercept unknown');
  assert.equal(pill.pressed, false);
  const bar = statusBarModel({ intercept: { unknown: true }, scope: {} }).intercept;
  assert.equal(bar.text, 'Intercept unknown');
  assert.doesNotMatch(bar.label, /Intercept off/);
});

test('open desktop menus count as overlays for the drag-region rescan', () => {
  assert.ok(OVERLAY.split(',').includes('.dsk-menu'));
});
