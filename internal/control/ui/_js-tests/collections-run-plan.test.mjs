import test from 'node:test';
import assert from 'node:assert/strict';
import {
  CONFIRM_PHRASE, STATE_CHANGING, buildRunPlan, planSummary, planConfirmed, methodBreakdown,
} from '../js/collections-run-plan.js';

// A small collection with a destructive folder, shaped like store items.
const items = [
  { uid: 'f1', kind: 'folder', name: 'Users', parentUid: '' },
  { uid: 'r1', kind: 'request', name: 'list users', method: 'GET', url: 'https://api.example.com/users', parentUid: 'f1' },
  { uid: 'r2', kind: 'request', name: 'delete user', method: 'DELETE', url: 'https://api.example.com/users/1', parentUid: 'f1' },
  { uid: 'f2', kind: 'folder', name: 'Health', parentUid: '' },
  { uid: 'r3', kind: 'request', name: 'ping', method: 'GET', url: 'https://health.example.com/ping', parentUid: 'f2' },
];

test('a whole-collection plan counts every request and names the scope', () => {
  const p = buildRunPlan(items, { collectionName: 'Demo API' });
  assert.equal(p.scope, 'collection');
  assert.equal(p.requests, 3);
  assert.equal(p.identities, 1);
  assert.equal(p.liveRequests, 3);
  assert.match(p.scopeLabel, /Demo API/);
  assert.deepEqual(p.hosts, ['api.example.com', 'health.example.com']);
});

test('a folder plan counts only that folder, and says so', () => {
  const p = buildRunPlan(items, { scope: 'folder', folderUid: 'f2', collectionName: 'Demo API' });
  assert.equal(p.scope, 'folder');
  assert.equal(p.requests, 1);
  assert.match(p.scopeLabel, /Health/);
  // The mislabelled scope was the bug: a folder run must never claim the collection.
  assert.doesNotMatch(p.scopeLabel, /Demo API/);
  assert.deepEqual(p.hosts, ['health.example.com']);
});

test('a folder plan includes nested requests', () => {
  const nested = items.concat([
    { uid: 'f3', kind: 'folder', name: 'Admin', parentUid: 'f1' },
    { uid: 'r4', kind: 'request', name: 'purge', method: 'POST', url: 'https://api.example.com/purge', parentUid: 'f3' },
  ]);
  const p = buildRunPlan(nested, { scope: 'folder', folderUid: 'f1' });
  assert.equal(p.requests, 3);
});

test('identities multiply the live request count', () => {
  const p = buildRunPlan(items, { identities: ['anonymous', 'user', 'admin'] });
  assert.equal(p.identities, 3);
  assert.equal(p.liveRequests, 9);
  assert.equal(p.identityNames.length, 3);
});

test('state-changing requests are counted across identities', () => {
  const p = buildRunPlan(items, { identities: ['user', 'admin'] });
  // one DELETE, fired once per identity
  assert.equal(p.destructive, 2);
  assert.ok(p.destructiveMethods.includes('DELETE'));
  assert.ok(STATE_CHANGING.has('DELETE') && STATE_CHANGING.has('POST') && STATE_CHANGING.has('PATCH') && STATE_CHANGING.has('PUT'));
  assert.ok(!STATE_CHANGING.has('GET'), 'GET is not state-changing');
});

test('a read-only plan still needs confirmation, but not a typed phrase', () => {
  const p = buildRunPlan([items[0], items[1]], { scope: 'folder', folderUid: 'f1' });
  assert.equal(p.destructive, 0);
  assert.equal(p.needsConfirm, true, 'any bulk send is confirmed');
  assert.equal(p.needsPhrase, false);
  assert.equal(planConfirmed(p, ''), true, 'no phrase required');
});

test('a plan that changes state requires the typed phrase', () => {
  const p = buildRunPlan(items, {});
  assert.equal(p.needsPhrase, true);
  assert.equal(planConfirmed(p, ''), false);
  assert.equal(planConfirmed(p, 'run'), false, 'the phrase is case-sensitive');
  assert.equal(planConfirmed(p, CONFIRM_PHRASE), true);
  assert.equal(planConfirmed(p, ' ' + CONFIRM_PHRASE + ' '), true, 'surrounding space is forgiven');
});

test('an empty plan sends nothing and is not confirmable', () => {
  const p = buildRunPlan([], {});
  assert.equal(p.requests, 0);
  assert.equal(p.liveRequests, 0);
  assert.equal(p.needsConfirm, false);
  assert.equal(p.empty, true);
});

test('the method breakdown is ordered by live request count', () => {
  const many = [
    { uid: 'a', kind: 'request', method: 'GET', url: 'https://x.example.com/1', parentUid: '' },
    { uid: 'b', kind: 'request', method: 'GET', url: 'https://x.example.com/2', parentUid: '' },
    { uid: 'c', kind: 'request', method: 'DELETE', url: 'https://x.example.com/3', parentUid: '' },
  ];
  const p = buildRunPlan(many, { identities: ['u1', 'u2'] });
  assert.deepEqual(methodBreakdown(p), [{ method: 'GET', count: 4, stateChanging: false }, { method: 'DELETE', count: 2, stateChanging: true }]);
});

test('a missing method counts as GET and a blank URL has no host', () => {
  const p = buildRunPlan([{ uid: 'a', kind: 'request', parentUid: '' }], {});
  assert.deepEqual(methodBreakdown(p), [{ method: 'GET', count: 1, stateChanging: false }]);
  assert.deepEqual(p.hosts, []);
});

test('the summary states the live request count, the hosts and the destructive count', () => {
  const s = planSummary(buildRunPlan(items, { identities: ['user', 'admin'], collectionName: 'Demo API' }));
  assert.match(s, /6 live requests/);
  assert.match(s, /2 identities/);
  assert.match(s, /2 of them change state/);
  assert.match(s, /api\.example\.com/);
});

test('the summary of a single-identity read-only run says nothing about state changes', () => {
  const s = planSummary(buildRunPlan([items[0], items[1]], { scope: 'folder', folderUid: 'f1' }));
  assert.match(s, /1 live request\b/);
  assert.doesNotMatch(s, /change state/);
});

test('plan text carries no HTML, so it is safe to escape and show', () => {
  const hostile = [{ uid: 'a', kind: 'request', name: '<img onerror=alert(1)>', method: 'GET', url: 'https://x.example.com/', parentUid: '' }];
  const p = buildRunPlan(hostile, { collectionName: '<script>bad()</script>' });
  assert.doesNotMatch(planSummary(p), /[<>]/, 'the summary never carries raw markup');
  assert.doesNotMatch(p.scopeLabel, /[<>]/);
});

// The run sheet's title is already the scope label, so the sentence under it
// must not repeat it.
test('the summary can omit the scope when it is already on screen', () => {
  const p = buildRunPlan([{ uid: 'a', kind: 'request', method: 'DELETE', url: 'https://x.example.com/1', parentUid: '' }], { collectionName: 'Demo' });
  const withScope = planSummary(p);
  const without = planSummary(p, { withScope: false });
  assert.match(withScope, /^Whole collection "Demo": /);
  assert.doesNotMatch(without, /Demo/);
  assert.match(without, /^1 live request\./);
  // Everything after the scope survives either way.
  assert.match(without, /change state/);
  assert.match(without, /x\.example\.com/);
});
