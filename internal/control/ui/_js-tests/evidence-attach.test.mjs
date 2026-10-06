import test from 'node:test';
import assert from 'node:assert/strict';
import { normalizeRefs, flowAttachRequest, imageAttachRequest, newFindingRequest, filterFindings, readinessPips, findingOptionLabel, undoPlan, EVIDENCE_KINDS, validateAltText, attachKeyTarget } from '../js/evidence-attach.js';

test('normalizeRefs dedupes, drops invalid ids and caps the list', () => {
  assert.deepEqual(normalizeRefs('flow', [3, '3', 4, 0, -1, 'x', 5.5]), [3, 4]);
  assert.equal(normalizeRefs('flow', Array.from({ length: 80 }, (_, i) => i + 1)).length, 32);
  assert.deepEqual(normalizeRefs('flow', null), []);
});

test('flowAttachRequest targets the existing flow evidence endpoint only', () => {
  const r = flowAttachRequest(12, 7, { note: 'n', proof: 'p' });
  assert.equal(r.path, '/api/findings/12/flows');
  assert.equal(r.method, 'POST');
  assert.deepEqual(r.body, { flowId: 7, role: 'result', note: 'n', proof: 'p' });
  assert.deepEqual(flowAttachRequest(12, 7).body, { flowId: 7, role: 'result' });
});

test('ws and note kinds attach the owning flow with a describing note', () => {
  assert.equal(EVIDENCE_KINDS.join(), 'flow,shot,ws,note');
  const r = flowAttachRequest(1, 9, { note: 'WebSocket frames 3-9' });
  assert.equal(r.body.note, 'WebSocket frames 3-9');
});

test('imageAttachRequest requires alt text and uses the images endpoint', () => {
  assert.equal(validateAltText('  ').ok, false);
  assert.equal(validateAltText('Login page after bypass').ok, true);
  const r = imageAttachRequest(4, { data: 'data:image/png;base64,AA', mime: 'image/png', alt: 'Login page' });
  assert.equal(r.path, '/api/findings/4/images');
  assert.equal(r.body.caption, 'Login page');
  assert.equal(r.body.source, 'operator_upload');
  assert.equal(r.body.role, 'result');
  assert.throws(() => imageAttachRequest(4, { data: 'x', mime: 'image/png', alt: '' }), /alt text/);
});

test('newFindingRequest creates a finding from flow ids with the existing defaults', () => {
  const r = newFindingRequest([7, 8], { title: 'IDOR on /api/user', target: 'https://example.com' });
  assert.equal(r.path, '/api/findings');
  assert.deepEqual(r.body, { severity: 'Medium', status: 'needs_verification', source: 'human', title: 'IDOR on /api/user', flowIds: [7, 8], target: 'https://example.com' });
  assert.equal(newFindingRequest([7], { title: ' ' }), null);
});

test('filterFindings matches title, id and severity, case-insensitively', () => {
  const items = [{ id: 1, title: 'SQL injection', severity: 'High' }, { id: 2, title: 'Open redirect', severity: 'Low' }, { id: 31, title: 'Other', severity: 'Info' }];
  assert.deepEqual(filterFindings(items, 'sql').map((f) => f.id), [1]);
  assert.deepEqual(filterFindings(items, '#31').map((f) => f.id), [31]);
  assert.deepEqual(filterFindings(items, 'low').map((f) => f.id), [2]);
  assert.equal(filterFindings(items, '').length, 3);
  assert.equal(filterFindings(items, 'zzz').length, 0);
});

test('readinessPips derives from server checks and never invents pass/fail', () => {
  const p = readinessPips({ checks: [{ id: 'proof', ok: true }, { id: 'cvss', ok: false }] });
  assert.deepEqual(p, { passed: 2 - 1, total: 2, label: '1 of 2 checks passed' });
  assert.deepEqual(readinessPips(null), { passed: 0, total: 0, label: 'readiness unknown' });
  assert.deepEqual(readinessPips({}), { passed: 0, total: 0, label: 'readiness unknown' });
});

test('findingOptionLabel is a plain-text accessible name', () => {
  assert.equal(findingOptionLabel({ id: 3, title: 'XSS', severity: 'High', readiness: { checks: [{ ok: true }, { ok: false }] } }), '#3 XSS, High, 1 of 2 checks passed');
});

test('undoPlan: attach to an existing finding detaches only that flow; new finding deletes it', () => {
  assert.deepEqual(undoPlan({ created: false, findingId: 5, flowIds: [7, 8] }), [{ method: 'DELETE', path: '/api/findings/5/flows/7' }, { method: 'DELETE', path: '/api/findings/5/flows/8' }]);
  assert.deepEqual(undoPlan({ created: true, findingId: 9, flowIds: [7] }), [{ method: 'DELETE', path: '/api/findings/9' }]);
  assert.deepEqual(undoPlan({ created: false, findingId: 5, flowIds: [] }), []);
});

test('attachKeyTarget resolves the flow id from a row or the drawer, else null', () => {
  const row = { closest: (s) => (s === '.trow[data-id]' ? { dataset: { id: '42' } } : null) };
  assert.equal(attachKeyTarget(row, 0), 42);
  const nothing = { closest: () => null };
  assert.equal(attachKeyTarget(nothing, 0), null);
  assert.equal(attachKeyTarget(nothing, 11), 11);
  assert.equal(attachKeyTarget(null, 0), null);
});
