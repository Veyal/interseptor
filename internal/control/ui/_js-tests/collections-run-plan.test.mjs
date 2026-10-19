import test from 'node:test';
import assert from 'node:assert/strict';
import {
  CONFIRM_PHRASE, STATE_CHANGING, buildRunPlan, planSummary, planConfirmed, methodBreakdown, blockedText,
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

// A rerun of just the failed requests is neither the whole collection nor one
// folder, and describing it as either overstates what is about to be sent.
test('an explicit scope label names a subset run', () => {
  const subset = [
    { uid: 'r2', kind: 'request', method: 'DELETE', url: 'https://api.example.com/users/1', parentUid: 'f1' },
  ];
  const p = buildRunPlan(subset, { collectionName: 'Demo API', scopeLabel: '1 failed request' });
  assert.equal(p.scopeLabel, '1 failed request');
  assert.doesNotMatch(planSummary(p), /Demo API/, 'the collection name must not reappear');
  assert.equal(p.liveRequests, 1, 'the count follows the subset, not the collection');
  assert.equal(p.needsPhrase, true, 'a subset that still deletes still needs the phrase');
});

test('a scope label carrying markup is stripped like every other plan string', () => {
  const p = buildRunPlan([{ uid: 'a', kind: 'request', method: 'GET', url: 'https://h.example.com/', parentUid: '' }],
    { scopeLabel: '<img src=x onerror=alert(1)>' });
  assert.doesNotMatch(p.scopeLabel, /[<>]/);
});

import { planResolveTargets, resolvePlanUrls } from '../js/collections-run-plan.js';

const varItems = [
  { uid: 'v1', kind: 'request', method: 'GET', url: '{{baseUrl}}/users', parentUid: '' },
  { uid: 'v2', kind: 'request', method: 'POST', url: { raw: '{{baseUrl}}/users' }, parentUid: '' },
  { uid: 'v3', kind: 'request', method: 'GET', url: 'https://{{region}}.example.com/x', parentUid: '' },
  { uid: 'v4', kind: 'request', method: 'GET', url: 'https://plain.example.com/y', parentUid: '' },
];

test('a variable-only collection resolves its hosts and never shows zero', async () => {
  const fake = async (it) => (it.uid === 'v3' ? { value: 'https://{{region}}.example.com/x', unresolved: ['region'] } : { value: 'https://api.example.com/users', unresolved: [] });
  const resolved = await resolvePlanUrls(varItems, {}, fake);
  assert.deepEqual(planResolveTargets(varItems).map((i) => i.uid), ['v1', 'v2', 'v3']);
  const p = buildRunPlan(varItems, { resolved });
  assert.deepEqual(p.hosts, ['api.example.com', 'plain.example.com']);
  assert.equal(p.unresolvedRequests, 1);
  assert.deepEqual(p.unresolvedHosts, ['{{region}}.example.com']);
  assert.match(planSummary(p), /could not be resolved \(\{\{region\}\}\.example\.com\)/);
});

test('without any resolved data a variable URL is listed as unresolved, not omitted', () => {
  const p = buildRunPlan(varItems.slice(0, 2), {});
  assert.deepEqual(p.hosts, []);
  assert.equal(p.unresolvedRequests, 2);
  assert.deepEqual(p.unresolvedHosts, ['{{baseUrl}}']);
});

test('a failed resolve call counts as unresolved', async () => {
  const resolved = await resolvePlanUrls(varItems, {}, async () => { throw new Error('boom'); });
  assert.equal(buildRunPlan(varItems, { resolved }).unresolvedRequests, 3);
});

// Round 3 (a5): a literal host is a known host even when only the path holds a variable.
test('a literal host stays resolved when only the path has an undefined variable', () => {
  const its = [{ uid: 'p1', kind: 'request', method: 'GET', url: 'http://127.0.0.1:18695/{{p}}/x' }];
  const p = buildRunPlan(its, { resolved: { p1: { value: 'http://127.0.0.1:18695/{{p}}/x', unresolved: ['p'] } } });
  assert.deepEqual(p.hosts, ['127.0.0.1:18695']);
  assert.equal(p.unresolvedRequests, 0);
  assert.deepEqual(p.unresolvedHosts, []);
  assert.equal(p.unresolvedPathRequests, 1);
  assert.deepEqual(p.unresolvedVariables, ['p']);
  const s = planSummary(p);
  assert.doesNotMatch(s, /host that could not be resolved/);
  assert.match(s, /unresolved variable in the path or query \(p\)/);
});

test('an undefined variable in the host part is still an unresolved host', () => {
  const its = [{ uid: 'h1', kind: 'request', method: 'GET', url: '{{baseUrl}}/x' }];
  const p = buildRunPlan(its, { resolved: { h1: { value: '{{baseUrl}}/x', unresolved: ['baseUrl'] } } });
  assert.equal(p.unresolvedRequests, 1);
  assert.equal(p.unresolvedPathRequests, 0);
  assert.deepEqual(p.hosts, []);
});

// Round 3 (a5): the review warns before RUN when scope or the env pin will block requests.
test('the plan counts requests that scope or the environment pin will block, and says why', () => {
  const blockCheck = (url) => (url.includes('blocked.example.com') ? 'out_of_scope' : url.includes('pinned.example.com') ? 'base_target_pin' : '');
  const its = [
    { uid: 'a', kind: 'request', method: 'GET', url: 'https://blocked.example.com/a' },
    { uid: 'b', kind: 'request', method: 'GET', url: 'https://pinned.example.com/b' },
    { uid: 'c', kind: 'request', method: 'GET', url: 'https://ok.example.com/c' },
  ];
  const p = buildRunPlan(its, { blockCheck });
  assert.equal(p.blocked.total, 2);
  assert.equal(p.blocked.scope, 1);
  assert.equal(p.blocked.pin, 1);
  assert.equal(p.blocked.all, false);
  assert.match(blockedText(p), /2 of 3 requests will be blocked \(1 out of scope, 1 not matching the environment target pin\)/);
  assert.match(planSummary(p), /will be blocked/);
  const all = buildRunPlan(its.slice(0, 1), { blockCheck });
  assert.equal(all.blocked.all, true);
  assert.match(blockedText(all), /^Every request will be blocked/);
});

test('no block check or no blocked request adds no warning; identities multiply the count', () => {
  assert.equal(blockedText(buildRunPlan(items, {})), '');
  const only = [{ uid: 'a', kind: 'request', method: 'GET', url: 'https://x.example.com/' }];
  const p = buildRunPlan(only, { blockCheck: () => 'out_of_scope', identities: ['alice', 'bob'] });
  assert.equal(p.blocked.total, 2);
  assert.equal(p.blocked.requests, 1);
});

// Round 4 (a5) B1: an empty scope still blocks loopback/private targets (IP guard).
test('the plan counts requests the IP guard will block and names them in the warning', () => {
  const blockCheck = (url) => (url.includes('127.0.0.1') ? 'ip_guard' : '');
  const its = [
    { uid: 'a', kind: 'request', method: 'GET', url: 'http://127.0.0.1:9/a' },
    { uid: 'b', kind: 'request', method: 'GET', url: 'https://ok.example.com/b' },
  ];
  const p = buildRunPlan(its, { blockCheck });
  assert.equal(p.blocked.total, 1);
  assert.equal(p.blocked.guard, 1);
  assert.equal(p.blocked.scope, 0);
  assert.match(blockedText(p), /1 of 2 requests will be blocked \(1 to a private or loopback address\)/);
  assert.match(blockedText(p), /Add the host to scope/);
  const all = buildRunPlan(its.slice(0, 1), { blockCheck });
  assert.match(blockedText(all), /^Every request will be blocked \(1 to a private or loopback address\)/);
});
