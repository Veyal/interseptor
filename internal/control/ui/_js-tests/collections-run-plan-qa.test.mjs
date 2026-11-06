import test from 'node:test';
import assert from 'node:assert/strict';
import { buildRunPlan, planSummary, resolvePlanUrls } from '../js/collections-run-plan.js';

// A URL whose host is literal but whose path holds a variable: the host is
// known, so the plan used to treat the request as fully resolved.
const literalHost = [
  { uid: 'p1', kind: 'request', method: 'GET', url: 'https://api.example.com/{{userId}}/roles' },
];

test('a failed resolve behind a literal host is reported as an unresolved variable, not as sendable', async () => {
  // resolvePlanUrls drops a request whose resolve call threw, so `resolved` has
  // no entry for it. The server blocks an unresolved variable before it dials;
  // the review must say so instead of promising the request is sendable.
  const resolved = await resolvePlanUrls(literalHost, {}, async () => { throw new Error('boom'); });
  assert.deepEqual(resolved, {});
  const p = buildRunPlan(literalHost, { resolved });
  assert.equal(p.unresolvedPathRequests, 1);
  assert.deepEqual(p.unresolvedVariables, ['userId']);
  assert.equal(p.unresolvedRequests, 0, 'the host is still known');
  assert.deepEqual(p.hosts, ['api.example.com']);
  assert.match(planSummary(p), /unresolved variable in the path or query \(userId\) and will be blocked/);
});

test('no resolved data at all is the same warning, so a caller that forgot to resolve is not silent', () => {
  const p = buildRunPlan(literalHost, {});
  assert.equal(p.unresolvedPathRequests, 1);
  assert.deepEqual(p.unresolvedVariables, ['userId']);
});

test('a resolved variable URL is unaffected', () => {
  const p = buildRunPlan(literalHost, { resolved: { p1: { value: 'https://api.example.com/7/roles', unresolved: [] } } });
  assert.equal(p.unresolvedPathRequests, 0);
  assert.deepEqual(p.unresolvedVariables, []);
  assert.deepEqual(p.hosts, ['api.example.com']);
  assert.doesNotMatch(planSummary(p), /will be blocked/);
});

test('an unresolved variable URL is not judged by the scope guard on its raw template', () => {
  // blockCheck would see https://api.example.com/{{userId}}/roles and, with a
  // rule that does not match it, report an out-of-scope block that the server
  // never gets to: the unresolved variable stops the request first.
  const blockCheck = () => 'out_of_scope';
  const p = buildRunPlan(literalHost, { resolved: {}, blockCheck });
  assert.equal(p.unresolvedPathRequests, 1);
  assert.equal(p.blocked.total, 0, 'an unresolved request is blocked for that reason, not by scope');
});

test('a fully resolved request is still judged by the scope guard', () => {
  const p = buildRunPlan(literalHost, { resolved: { p1: { value: 'https://api.example.com/7/roles', unresolved: [] } }, blockCheck: () => 'out_of_scope' });
  assert.equal(p.blocked.total, 1);
});
