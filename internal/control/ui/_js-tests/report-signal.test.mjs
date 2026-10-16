import test from 'node:test';
import assert from 'node:assert/strict';
import { shouldShowReadinessSignals } from '../js/project-state.js';
import { isReportOpen, setReportOpen, onReportOpenChange } from '../js/shell-hooks.js';

test('readiness signals stay hidden outside the Report view', () => {
  assert.deepEqual(shouldShowReadinessSignals(false, 5, true), { badge: false, chip: false });
  assert.deepEqual(shouldShowReadinessSignals(undefined, 5, true), { badge: false, chip: false });
});

test('inside Report the chip shows, the badge only when there are blockers and data', () => {
  assert.deepEqual(shouldShowReadinessSignals(true, 3, true), { badge: true, chip: true });
  assert.deepEqual(shouldShowReadinessSignals(true, 0, true), { badge: false, chip: true });
  assert.deepEqual(shouldShowReadinessSignals(true, 3, false), { badge: false, chip: true });
});

test('report-open signal publishes booleans and only notifies on change', () => {
  const seen = [];
  const off = onReportOpenChange((on) => seen.push(on));
  assert.equal(isReportOpen(), false);
  setReportOpen(true); setReportOpen(true); setReportOpen(false);
  assert.deepEqual(seen, [true, false]);
  assert.equal(isReportOpen(), false);
  off(); setReportOpen(true);
  assert.deepEqual(seen, [true, false]);
  setReportOpen(false);
});

test('a throwing listener does not break the others', () => {
  const seen = [];
  const a = onReportOpenChange(() => { throw new Error('boom'); });
  const b = onReportOpenChange((on) => seen.push(on));
  setReportOpen(true);
  assert.deepEqual(seen, [true]);
  a(); b(); setReportOpen(false);
});
