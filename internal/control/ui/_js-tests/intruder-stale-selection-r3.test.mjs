// Attach / Diff vs baseline act on the attempt whose row was last picked. That
// selection has to follow the rows on screen: after a new run, another attack tab or a
// History entry replaced them, the buttons must not stay armed with a flow of the
// previous view.
import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import { intrSelectionInView } from '../js/intruder.js';

test('a selected attempt survives while its row is still among the shown results', () => {
  const rows = [{ id: 1, flowId: 11 }, { id: 2, flowID: 12 }, { id: 3 }];
  assert.equal(intrSelectionInView(11, rows), 11);
  assert.equal(intrSelectionInView(12, rows), 12);
});

test('the selection clears when the shown results no longer hold that flow', () => {
  assert.equal(intrSelectionInView(11, [{ id: 1, flowId: 21 }]), 0);
  assert.equal(intrSelectionInView(11, []), 0);
  assert.equal(intrSelectionInView(11, null), 0);
  assert.equal(intrSelectionInView(0, [{ id: 1, flowId: 11 }]), 0);
});

test('every render re-checks the selection against the displayed rows', () => {
  const src = fs.readFileSync(new URL('../js/intruder.js', import.meta.url), 'utf8');
  const onRender = src.slice(src.indexOf('function onRender(st)'), src.indexOf('/* stepper'));
  assert.match(onRender, /active = intrSelectionInView\(active, st\.results\)/);
});
