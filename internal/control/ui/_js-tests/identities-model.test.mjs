import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  identityStatus, statusLabel, formatCountdown, relTime, toMs, parseHeaderLines, headerValidationMessage,
  maskValue, maskedHeaders, decodeJwtClaims, authSummary, authLine, sourceBadge, provenance, duplicateName,
  filterIdentities, identityPatch, isStale, runsForIdentity, runTitle, runKindLabel, matrixView, verdictTone,
  timelineItems, usageSummary, jwtRows, jwtExpSec,
} from '../js/identities-model.js';

const NOW = Date.parse('2026-10-23T12:00:00Z');
const b64u = (o) => Buffer.from(JSON.stringify(o)).toString('base64').replace(/=+$/, '').replace(/\+/g, '-').replace(/\//g, '_');
const jwt = (claims) => b64u({ alg: 'HS256', typ: 'JWT' }) + '.' + b64u(claims) + '.sig';

test('identityStatus: broken wins, then the last check, then a locally expired JWT', () => {
  assert.equal(identityStatus(null), 'unknown');
  assert.equal(identityStatus({ broken: true, lastCheck: { verdict: 'ok' } }), 'broken');
  assert.equal(identityStatus({ lastCheck: { verdict: 'ok' } }, { now: NOW }), 'ok');
  assert.equal(identityStatus({ lastCheck: { verdict: 'denied' } }, { now: NOW }), 'denied');
  assert.equal(identityStatus({ lastCheck: { verdict: 'ok' } }, { jwtExp: NOW / 1000 - 5, now: NOW }), 'expired');
  assert.equal(identityStatus({}, { jwtExp: NOW / 1000 + 600, now: NOW }), 'unknown');
  assert.equal(identityStatus({ lastCheck: { verdict: 'weird' } }, { now: NOW }), 'unknown');
  assert.equal(statusLabel('ok'), 'Healthy');
  assert.equal(statusLabel('nope'), 'Not checked');
});

test('formatCountdown and relTime', () => {
  const s = NOW / 1000;
  assert.equal(formatCountdown(s + 45, NOW), '45s');
  assert.equal(formatCountdown(s + 23 * 60 + 10, NOW), '23m');
  assert.equal(formatCountdown(s + 2 * 3600 + 5 * 60, NOW), '2h 5m');
  assert.equal(formatCountdown(s + 3 * 3600, NOW), '3h');
  assert.equal(formatCountdown(s + 3 * 86400 + 4 * 3600, NOW), '3d 4h');
  assert.equal(formatCountdown(s - 1, NOW), 'expired');
  assert.equal(formatCountdown(0, NOW), '');
  assert.equal(relTime(NOW - 10_000, NOW), 'just now');
  assert.equal(relTime(new Date(NOW - 3 * 3600_000).toISOString(), NOW), '3h ago');
  assert.equal(relTime(NOW / 1000 - 120, NOW), '2m ago');
  assert.equal(relTime(NOW + 4 * 60_000, NOW), 'in 4m');
  assert.equal(relTime('garbage', NOW), '');
  assert.ok(Number.isNaN(toMs('')));
});

test('parseHeaderLines validates Key: Value lines and headerValidationMessage names them', () => {
  const r = parseHeaderLines('Cookie: a=1; b=2\n\n  Authorization: Bearer x  \nnot a header\n: empty name');
  assert.deepEqual(r.headers, [{ name: 'Cookie', value: 'a=1; b=2' }, { name: 'Authorization', value: 'Bearer x' }]);
  assert.deepEqual(r.errors.map((e) => e.line), [4, 5]);
  assert.equal(headerValidationMessage('X-Ok: 1'), '');
  assert.equal(headerValidationMessage('bad'), 'Line 1 must look like "Key: Value".');
  assert.match(headerValidationMessage('a\nb'), /^Lines 1, 2 /);
  assert.equal(headerValidationMessage(''), '');
});

test('masking never returns the value', () => {
  const secret = 'Bearer eyJsupersecret';
  const m = maskedHeaders('Authorization: ' + secret);
  assert.equal(m.length, 1);
  assert.equal(m[0].name, 'Authorization');
  assert.equal(m[0].length, secret.length);
  assert.ok(!m[0].masked.includes('super'));
  assert.match(maskValue('abc'), /\(3 chars\)$/);
  assert.equal(maskValue(''), '(empty)');
});

test('authSummary reads the scheme and a JWT expiry without exposing values', () => {
  const exp = Math.floor(NOW / 1000) + 23 * 60 + 5;
  const bearer = authSummary('Authorization: Bearer ' + jwt({ sub: 'u1', exp }));
  assert.equal(bearer.type, 'bearer');
  assert.equal(bearer.jwtExp, exp);
  assert.equal(authLine(bearer, NOW), 'Bearer · 23m');
  assert.equal(authLine({ type: 'bearer', jwtExp: exp }, (exp + 1) * 1000), 'Bearer · expired');
  assert.equal(authSummary('Cookie: sid=abc').type, 'cookie');
  assert.equal(authSummary('Cookie: sid=' + jwt({ exp })).jwtExp, exp);
  assert.equal(authSummary('Authorization: Basic dTpw').type, 'basic');
  assert.equal(authSummary('X-Api-Key: k').type, 'header');
  assert.equal(authSummary('').type, 'none');
  assert.equal(authLine(authSummary('')), 'Anonymous');
  assert.equal(decodeJwtClaims('not.a'), null);
  assert.equal(decodeJwtClaims('a.' + Buffer.from('nope').toString('base64') + '.c'), null);
  assert.deepEqual(decodeJwtClaims(jwt({ sub: 'x' })), { sub: 'x' });
});

test('sourceBadge and provenance', () => {
  assert.deepEqual(sourceBadge({}), { kind: 'human', label: 'you' });
  assert.deepEqual(sourceBadge({ createdBy: 'ui' }), { kind: 'human', label: 'you' });
  assert.deepEqual(sourceBadge({ createdBy: 'agent-x' }), { kind: 'agent', label: 'agent-x' });
  assert.deepEqual(sourceBadge({ owner: 'claude' }), { kind: 'agent', label: 'claude' });
  assert.deepEqual(sourceBadge({ source: { kind: 'agent' } }), { kind: 'agent', label: 'agent' });
  const p = provenance({ createdBy: 'agent-x', createdAt: new Date(NOW - 3 * 3600_000).toISOString(), source: { kind: 'flow', flowId: 812, method: 'POST', host: 'example.com', path: '/api/login' } }, NOW);
  assert.equal(p.lead, 'Created by agent-x · 3h ago');
  assert.equal(p.flowId, 812);
  assert.equal(p.target, 'POST example.com/api/login');
  assert.equal(provenance({}, NOW).flowId, 0);
});

test('names, filtering, patches and staleness', () => {
  assert.equal(duplicateName('admin', ['admin']), 'admin copy');
  assert.equal(duplicateName('admin', ['admin', 'admin copy', 'admin copy 2']), 'admin copy 3');
  const ids = [{ name: 'admin', role: 'owner' }, { name: 'user', notes: 'tenant B' }];
  assert.deepEqual(filterIdentities(ids, 'TENANT').map((i) => i.name), ['user']);
  assert.equal(filterIdentities(ids, '').length, 2);
  assert.equal(identityPatch({ name: 'a', role: 'r', notes: '', headers: 'X: 1' }, { name: ' a ', role: 'r', notes: '', headers: 'X: 1' }), null);
  assert.deepEqual(identityPatch({ name: 'a', role: '', headers: '' }, { name: 'b', role: 'admin', notes: '', headers: '' }), { name: 'b', role: 'admin' });
  assert.equal(isStale(null, {}), false);
  assert.equal(isStale({ updatedAt: '1' }, { updatedAt: '1' }), false);
  assert.equal(isStale({ updatedAt: '1' }, { updatedAt: '2' }), true);
  assert.equal(isStale({ updatedAt: '1', headers: 'a' }, null), true);
});

test('runs: filtering, titles, kinds', () => {
  const runs = [{ id: 'r1', identities: ['admin', 'user'], kind: 'single', target: { method: 'GET', host: 'example.com', path: '/a' } }, { id: 'r2', identities: ['admin'], kind: 'bulk', target: null, endpoints: 12 }, { id: 'r3' }];
  assert.deepEqual(runsForIdentity(runs, 'user').map((r) => r.id), ['r1']);
  assert.equal(runTitle(runs[0]), 'GET example.com/a');
  assert.equal(runTitle(runs[1]), '12 endpoints');
  assert.equal(runKindLabel('crosshost'), 'Cross-host');
  assert.equal(runKindLabel('x'), 'Run');
});

test('matrixView labels every cell in column order and flags same-as-baseline', () => {
  const v = matrixView({ matrix: { identities: ['admin', 'user', 'anon'], rows: [
    { flowId: 5, method: 'GET', host: 'example.com', path: '/a', baseline: { status: 200, length: 10 }, cells: [
      { identity: 'user', status: 200, length: 10, verdict: 'same', flowId: 9 },
      { identity: 'admin', status: 200, length: 10, verdict: 'allowed', flowId: 8 },
      { identity: 'anon', status: 401, length: 0, verdict: 'mystery' },
    ] },
    { flowId: 6, method: 'POST', host: 'example.com', path: '/b', cells: [] },
  ] } });
  assert.deepEqual(v.columns, ['admin', 'user', 'anon']);
  assert.equal(v.suspicious, 1);
  const [r0, r1] = v.rows;
  assert.deepEqual(r0.cells.map((c) => c.identity), ['admin', 'user', 'anon']);
  assert.equal(r0.cells[1].label, 'Same as baseline');
  assert.equal(r0.cells[1].suspicious, true);
  assert.equal(r0.cells[1].flowId, 9);
  assert.equal(r0.cells[2].verdict, 'error');
  assert.equal(r0.suspicious, true);
  assert.equal(r1.cells[0].missing, true);
  assert.equal(r1.cells[0].label, 'Not run');
  assert.equal(r1.suspicious, false);
  assert.equal(verdictTone('same'), 'danger');
  assert.deepEqual(matrixView(null), { columns: [], rows: [], suspicious: 0 });
});

test('timelineItems puts the source flow first and sorts events oldest first', () => {
  const items = timelineItems({ timeline: {
    source: { flowId: 812, ts: '2026-10-23T09:00:00Z', method: 'POST', host: 'example.com', path: '/api/login', status: 200 },
    events: [
      { ts: '2026-10-23T11:00:00Z', kind: 'checked', actor: 'agent-x', detail: '200 ok' },
      { ts: '2026-10-23T09:00:01Z', kind: 'created', actor: 'agent-x' },
      { kind: 'updated' },
    ],
  } });
  assert.deepEqual(items.map((i) => i.kind === 'source' ? 'source' : i.event), ['source', 'created', 'checked', 'updated']);
  assert.equal(items[1].label, 'Created');
  assert.deepEqual(timelineItems(null), []);
  assert.deepEqual(timelineItems({ timeline: { source: null, events: [] } }), []);
  assert.equal(usageSummary({ usage: { count: 1, recent: [] } }).text, 'Used in 1 request');
  assert.equal(usageSummary(null).count, 0);
});

test('jwtRows shows claims in a fixed order with a countdown', () => {
  const exp = Math.floor(NOW / 1000) + 600;
  const rows = jwtRows({ alg: 'RS256', sub: 'u1', roles: ['admin', 'ops'], issuer: 'https://example.com', audience: ['api'], iat: exp - 3600, exp, expired: false, claims: {} }, NOW);
  assert.deepEqual(rows.map((r) => r.label), ['Algorithm', 'Subject', 'Roles', 'Issuer', 'Audience', 'Issued', 'Expires']);
  assert.equal(rows[2].value, 'admin, ops');
  assert.match(rows[6].value, /\(10m\)$/);
  assert.match(jwtRows({ exp: exp - 1200, expired: true }, NOW)[0].value, /\(expired\)$/);
  assert.deepEqual(jwtRows(null), []);
  assert.equal(jwtExpSec({ exp }), exp);
  assert.equal(jwtExpSec({ exp: '2026-10-23T12:00:00Z' }), NOW / 1000);
  assert.equal(jwtExpSec(null), 0);
});
