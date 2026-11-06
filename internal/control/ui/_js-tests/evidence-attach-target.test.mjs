// Regression cover for two evidence-attach defects:
//  1. a screenshot attached through the picker's "New finding from selection"
//     row wrote to /api/findings/NaN/images (the target carries no id yet);
//  2. readinessPips counted `ok === true`, which the real readiness payload
//     never carries (it lists only open checks — see readiness-meter.js).
import test from 'node:test';
import assert from 'node:assert/strict';
import { attachTargetId, imageAttachRequest, flowAttachRequest, readinessPips } from '../js/evidence-attach.js';
import { findingPickerList } from '../js/evidence-render.js';

const SHOT = { data: 'data:image/png;base64,AA', mime: 'image/png', alt: 'Login page after bypass' };

test('attachTargetId separates "attach to this finding" from "create a new one"', () => {
  assert.equal(attachTargetId({ id: 12 }), 12);
  assert.equal(attachTargetId({ id: '12' }), 12);
  assert.equal(attachTargetId({ create: true }), 0, 'a create target has no id to write to');
  assert.equal(attachTargetId({ id: undefined }), 0);
  assert.equal(attachTargetId({ id: 'abc' }), 0);
  assert.equal(attachTargetId({ id: -3 }), 0);
  assert.equal(attachTargetId({ id: 4.5 }), 0);
  assert.equal(attachTargetId(null), 0);
});

test('a screenshot attach refuses an id-less finding instead of posting to /findings/NaN', () => {
  assert.throws(() => imageAttachRequest(0, SHOT), /choose a finding first/);
  assert.throws(() => imageAttachRequest(undefined, SHOT), /choose a finding first/);
  assert.throws(() => imageAttachRequest(Number.NaN, SHOT), /choose a finding first/);
  // The positive control: a real id still builds the documented request.
  const req = imageAttachRequest(7, SHOT);
  assert.equal(req.path, '/api/findings/7/images');
  assert.equal(req.body.caption, 'Login page after bypass');
  // Alt text is still checked first, so the message names the real blocker.
  assert.throws(() => imageAttachRequest(7, { ...SHOT, alt: ' ' }), /alt text/);
});

test('a flow attach refuses an id-less finding too', () => {
  assert.throws(() => flowAttachRequest(0, 9), /choose a finding first/);
  assert.throws(() => flowAttachRequest(Number.NaN, 9), /choose a finding first/);
  assert.equal(flowAttachRequest(5, 9).path, '/api/findings/5/flows');
});

test('readinessPips counts the open checks the payload carries', () => {
  // Shape sent by GET /api/findings: checks are the gaps, each with code/message.
  const draft = { stage: 'draft', checks: [{ code: 'title', message: 'Add a precise finding title.' }], gaps: ['title'] };
  assert.deepEqual(readinessPips(draft), { passed: 0, total: 1, label: '1 check open' });
  // gaps can outrun checks (a payload trimmed for a list/summary view).
  assert.deepEqual(readinessPips({ stage: 'draft', gaps: ['title', 'cvss', 'fix'] }), { passed: 0, total: 3, label: '3 checks open' });
  // Nothing outstanding is the server's own "ready" stage, not an unknown one.
  assert.equal(readinessPips({ stage: 'report_ready', checks: [], gaps: [] }).label, 'ready');
  assert.equal(readinessPips({ stage: 'evidence_attached', checks: [] }).label, 'ready');
  // No readiness at all stays unknown: nothing is claimed that was not sent.
  assert.equal(readinessPips(undefined).label, 'readiness unknown');
  assert.equal(readinessPips({}).label, 'readiness unknown');
});

test('the render picker bounds its list to the cap and says how many it hid', () => {
  const many = Array.from({ length: 240 }, (_, i) => ({ id: i + 1, title: 't' + (i + 1), severity: 'High' }));
  const capped = findingPickerList(many, 200);
  assert.equal(capped.rows.length, 200);
  assert.equal(capped.hidden, 40);
  assert.match(capped.status, /first 200 of 240 findings/);
  assert.match(capped.status, /older findings are not listed/);
  assert.equal(capped.rows[0].label, '#1 t1 (High)');
  const whole = findingPickerList(many.slice(0, 3), 200);
  assert.equal(whole.rows.length, 3);
  assert.equal(whole.hidden, 0);
  assert.equal(whole.status, '3 findings');
  assert.equal(findingPickerList([], 200).status, 'No findings yet. Create one from Findings first.');
  // Junk rows never become an option with an unusable id.
  assert.deepEqual(findingPickerList([null, { title: 'no id' }, { id: 0 }], 200).rows, []);
});
