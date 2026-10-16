import test from 'node:test';
import assert from 'node:assert/strict';
import { linkedIndex, endpointLinked, authState, coveragePips, coverageSummary, withoutEvidence } from '../js/map-coverage.js';

const eps = [
  { host: 'example.com', method: 'GET', path: '/a', statuses: [200], lastFlowId: 1 },
  { host: 'example.com', method: 'GET', path: '/admin', statuses: [403], lastFlowId: 2 },
  { host: 'example.com', method: 'POST', path: '/b', statuses: [200, 401], lastFlowId: 3 },
];
const findings = [{ id: 1, flows: [{ flowId: 9, method: 'GET', host: 'example.com', path: '/a?x=1' }, { flowId: 3 }] }];

test('no findings data means no linkage is claimed', () => {
  assert.equal(linkedIndex(null), null);
  assert.equal(endpointLinked(eps[0], null), false);
  assert.equal(coverageSummary(eps, null), null);
  assert.deepEqual(coveragePips(eps[0], null).map((p) => p.id), ['captured']);
  assert.equal(withoutEvidence(eps, null).length, 3);
});

test('linkage matches by flow id or by method host and path ignoring the query', () => {
  const idx = linkedIndex(findings);
  assert.equal(endpointLinked(eps[0], idx), true);
  assert.equal(endpointLinked(eps[1], idx), false);
  assert.equal(endpointLinked(eps[2], idx), true);
});

test('summary and filter are consistent', () => {
  const idx = linkedIndex(findings);
  const s = coverageSummary(eps, idx);
  assert.deepEqual([s.linked, s.total], [2, 3]);
  assert.equal(s.text, 'Linked to a finding 2/3');
  assert.deepEqual(withoutEvidence(eps, idx).map((e) => e.path), ['/admin']);
});

test('pips carry text labels and only describe real data', () => {
  const idx = linkedIndex(findings);
  const pips = coveragePips(eps[0], idx);
  assert.deepEqual(pips.map((p) => [p.id, p.on]), [['captured', true], ['linked', true]]);
  assert.equal(pips.every((p) => p.label.length > 3), true);
  assert.equal(coveragePips(eps[1], idx)[1].on, false);
});

test('auth state derives from observed statuses only', () => {
  assert.equal(authState(eps[1]).kind, 'auth');
  assert.equal(authState(eps[2]).kind, 'auth');
  assert.equal(authState(eps[0]).kind, 'open');
  assert.equal(authState({ statuses: [] }).kind, 'unknown');
  assert.equal(authState({ statuses: [404] }).kind, 'unknown');
  assert.equal(authState(eps[0]).label, 'Open');
});
