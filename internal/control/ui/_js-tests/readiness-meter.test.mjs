import test from 'node:test';
import assert from 'node:assert/strict';
import { stageIndex, readinessSegments, readinessValuetext, readinessMeterAttrs, readinessMeterHTML, humanizeGap, readinessGaps } from '../js/readiness-meter.js';

const label = (g) => ({ title: 'Title', cvss: 'CVSS vector', evidence: 'Proof evidence' })[g] || humanizeGap(g);

test('stageIndex maps the server stage and rejects unknown values', () => {
  assert.equal(stageIndex({ stage: 'draft' }), 0);
  assert.equal(stageIndex({ stage: 'evidence_attached' }), 1);
  assert.equal(stageIndex({ stage: 'reproducible' }), 2);
  assert.equal(stageIndex({ stage: 'report_ready' }), 3);
  assert.equal(stageIndex({ stage: 'invented' }), -1);
  assert.equal(stageIndex(null), -1);
  assert.equal(stageIndex({}), -1);
});

test('valuetext: none reached lists the server gaps', () => {
  const t = readinessValuetext({ stage: 'draft', gaps: ['title', 'cvss'] }, label);
  assert.equal(t, '0 of 3 stages reached, Draft; missing: Title, CVSS vector');
});

test('valuetext: partially reached', () => {
  const t = readinessValuetext({ stage: 'evidence_attached', gaps: ['evidence'] }, label);
  assert.equal(t, '1 of 3 stages reached, Evidence attached; missing: Proof evidence');
});

test('valuetext: all passed carries no missing list even if gaps leak in', () => {
  assert.equal(readinessValuetext({ stage: 'report_ready', gaps: ['title'] }, label), '3 of 3 stages reached, Report ready');
});

test('valuetext: unknown check codes are humanized, never dropped', () => {
  const t = readinessValuetext({ stage: 'draft', gaps: ['capability:browser_execution', 'new_server_code'] });
  assert.match(t, /Claim: browser execution, New server code/);
});

test('valuetext and attrs for missing readiness say unknown, not zero', () => {
  assert.equal(readinessValuetext(undefined), 'Readiness unknown');
  const a = readinessMeterAttrs({ stage: 'nope' });
  assert.equal(a.role, 'meter');
  assert.equal(a['aria-valuenow'], '0');
  assert.equal(a['aria-valuetext'], 'Readiness unknown');
});

test('segments: icon plus state per milestone, reached ones are passes', () => {
  const s = readinessSegments({ stage: 'reproducible' });
  assert.deepEqual(s.map((x) => x.state), ['pass', 'pass', 'todo']);
  assert.deepEqual(s.map((x) => x.icon), ['i-check-circle', 'i-check-circle', 'i-ring']);
  assert.ok(s.every((x) => x.label && x.status));
  assert.deepEqual(readinessSegments(null).map((x) => x.state), ['todo', 'todo', 'todo']);
});

test('readinessGaps ignores non-string entries', () => {
  assert.deepEqual(readinessGaps({ gaps: ['a', 3, '', null, 'b'] }), ['a', 'b']);
  assert.deepEqual(readinessGaps(undefined), []);
});

test('html: meter role, values and escaped labels; segments link only when asked', () => {
  const plain = readinessMeterHTML({ stage: 'draft', gaps: ['<img>'] }, { size: 'compact' });
  assert.match(plain, /role="meter"/);
  assert.match(plain, /aria-valuemax="3"/);
  assert.match(plain, /aria-valuenow="0"/);
  assert.ok(!plain.includes('<img>'));
  assert.ok(!plain.includes('<a '));
  const linked = readinessMeterHTML({ stage: 'draft', gaps: [] }, { hrefFor: (s) => '#finding-7/' + s, id: 'x' });
  assert.match(linked, /href="#finding-7\/evidence"/);
  assert.match(linked, /href="#finding-7\/review"/);
  assert.match(linked, /id="x"/);
  assert.match(plain, /aria-label="Report readiness"/, 'the meter is named');
  assert.ok(!/role="meter"/.test(linked), 'links never nest inside role=meter');
  assert.match(linked, /role="group" aria-label="Report readiness"/);
  const unknown = readinessMeterHTML({ stage: 'zzz' }, { hrefFor: () => '#x' });
  assert.ok(!unknown.includes('<a '), 'unknown stage renders no links');
  assert.match(unknown, /data-unknown="true"/);
});
