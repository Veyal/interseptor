import test from 'node:test';
import assert from 'node:assert/strict';
import { CHECKLIST_STEPS, deriveChecklist, checklistVisible } from '../js/checklist-model.js';

test('five steps in the documented order', () => {
  assert.deepEqual(CHECKLIST_STEPS.map((s) => s.label), [
    'Trust the CA', 'Point a device or browser at the proxy', 'Define scope', 'Capture a first flow', 'Create a first finding',
  ]);
});

test('nothing is done without real state', () => {
  const m = deriveChecklist({});
  assert.equal(m.done, 0);
  assert.equal(m.text, '0 of 5');
  assert.equal(m.complete, false);
  assert.equal(checklistVisible(m, false), true);
});

test('steps derive from readiness, scope, evidence and findings', () => {
  const m = deriveChecklist({
    readiness: { checks: [{ id: 'tls_intercept', ok: true }, { id: 'traffic', ok: true }] },
    scope: { enabled: true, inCount: 2 },
    evidence: { flows: 12 },
    findings: { total: 0 },
  });
  assert.deepEqual(m.steps.map((s) => s.done), [true, true, true, true, false]);
  assert.equal(m.text, '4 of 5');
});

test('flows imply the proxy is pointed at; a failed readiness fetch does not fake the CA', () => {
  const m = deriveChecklist({ readiness: null, evidence: { flows: 3 } });
  assert.deepEqual(m.steps.map((s) => s.done), [false, true, false, true, false]);
});

test('scope needs an enabled include rule, not just the flag', () => {
  assert.equal(deriveChecklist({ scope: { enabled: true, inCount: 0 } }).steps[2].done, false);
});

test('complete and dismissed both hide the card', () => {
  const all = deriveChecklist({
    readiness: { checks: [{ id: 'tls_intercept', ok: true }, { id: 'traffic', ok: true }] },
    scope: { enabled: true, inCount: 1 }, evidence: { flows: 1 }, findings: { total: 1 },
  });
  assert.equal(all.complete, true);
  assert.equal(checklistVisible(all, false), false);
  assert.equal(checklistVisible(deriveChecklist({}), true), false);
});
