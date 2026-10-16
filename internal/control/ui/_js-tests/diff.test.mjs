import test from 'node:test';
import assert from 'node:assert/strict';
import { diffLines, diffWords, hunks, changeStarts, normalizeText, normalizeJSON, normalizeBody, DEFAULT_NORMALIZE_RULES } from '../js/diff.js';

const apply = (ops, side) => ops.filter((o) => o.type === 'eq' || o.type === (side === 'a' ? 'del' : 'add')).map((o) => o.text);

test('diffLines identical input is all eq', () => {
  const r = diffLines(['a', 'b'], ['a', 'b']);
  assert.equal(r.tooDifferent, false);
  assert.deepEqual(r.ops.map((o) => o.type), ['eq', 'eq']);
  assert.equal(r.stats.added + r.stats.removed, 0);
});

test('diffLines reconstructs both sides for mixed edits', () => {
  const a = 'one two three four five six'.split(' ');
  const b = 'one 2 three four six seven'.split(' ');
  const r = diffLines(a, b);
  assert.deepEqual(apply(r.ops, 'a'), a);
  assert.deepEqual(apply(r.ops, 'b'), b);
  assert.equal(r.stats.added, 2);
  assert.equal(r.stats.removed, 2);
});

test('diffLines accepts strings and handles empty sides', () => {
  assert.deepEqual(diffLines('', 'x\ny').ops.map((o) => o.type), ['add', 'add']);
  assert.deepEqual(diffLines('x\ny', '').ops.map((o) => o.type), ['del', 'del']);
  assert.equal(diffLines('', '').ops.length, 0);
});

test('diffLines is minimal on a classic Myers case', () => {
  const r = diffLines('ABCABBA'.split(''), 'CBABAC'.split(''));
  assert.equal(r.stats.added + r.stats.removed, 5);
  assert.deepEqual(apply(r.ops, 'a').join(''), 'ABCABBA');
  assert.deepEqual(apply(r.ops, 'b').join(''), 'CBABAC');
});

test('diffLines randomised round trip', () => {
  let seed = 7;
  const rnd = () => (seed = (seed * 1103515245 + 12345) & 0x7fffffff) / 0x7fffffff;
  for (let n = 0; n < 40; n++) {
    const a = Array.from({ length: Math.floor(rnd() * 30) }, () => 'l' + Math.floor(rnd() * 6));
    const b = Array.from({ length: Math.floor(rnd() * 30) }, () => 'l' + Math.floor(rnd() * 6));
    const r = diffLines(a, b);
    assert.deepEqual(apply(r.ops, 'a'), a);
    assert.deepEqual(apply(r.ops, 'b'), b);
  }
});

test('diffLines bails out as too different beyond 20k changed lines', () => {
  const a = Array.from({ length: 20001 }, (_, i) => 'a' + i);
  const b = Array.from({ length: 20001 }, (_, i) => 'b' + i);
  const r = diffLines(a, b);
  assert.equal(r.tooDifferent, true);
  assert.deepEqual(r.ops, []);
});

test('diffLines keeps huge identical inputs cheap (common prefix and suffix are trimmed)', () => {
  const a = Array.from({ length: 30000 }, (_, i) => 'same' + i);
  const b = a.slice();
  b[15000] = 'changed';
  const t = Date.now();
  const r = diffLines(a, b);
  assert.equal(r.tooDifferent, false);
  assert.equal(r.stats.added, 1);
  assert.equal(r.stats.removed, 1);
  assert.ok(Date.now() - t < 2000);
});

test('diffLines bails out of pathological edit distance instead of hanging', () => {
  const a = Array.from({ length: 6000 }, (_, i) => 'a' + i);
  const b = Array.from({ length: 6000 }, (_, i) => 'b' + i);
  const t = Date.now();
  const r = diffLines(a, b);
  assert.equal(r.tooDifferent, true);
  assert.ok(Date.now() - t < 5000);
});

test('diffWords marks changed tokens only', () => {
  const r = diffWords('GET /api/users HTTP/1.1', 'GET /api/orders HTTP/1.1');
  assert.deepEqual(r.a.filter((s) => s.changed).map((s) => s.text), ['users']);
  assert.deepEqual(r.b.filter((s) => s.changed).map((s) => s.text), ['orders']);
  assert.equal(r.a.map((s) => s.text).join(''), 'GET /api/users HTTP/1.1');
  assert.equal(r.b.map((s) => s.text).join(''), 'GET /api/orders HTTP/1.1');
});

test('hunks collapses long unchanged regions and reports the hidden count', () => {
  const a = Array.from({ length: 60 }, (_, i) => 'line' + i);
  const b = a.slice();
  b[30] = 'changed';
  const { ops } = diffLines(a, b);
  const out = hunks(ops, 3);
  const collapsed = out.filter((h) => h.kind === 'collapsed');
  assert.equal(collapsed.length, 2);
  assert.equal(collapsed[0].count, 27);
  assert.equal(collapsed[1].count, 26);
  const shown = out.filter((h) => h.kind === 'ops').flatMap((h) => h.ops);
  assert.equal(shown.filter((o) => o.type !== 'eq').length, 2);
  assert.equal(shown.length + collapsed.reduce((n, c) => n + c.count, 0), ops.length);
});

test('changeStarts lists the first index of every change block for n/N navigation', () => {
  const { ops } = diffLines(['a', 'b', 'c', 'd', 'e'], ['a', 'X', 'c', 'd', 'Y', 'e']);
  assert.deepEqual(changeStarts(ops), [1, 5]);
});

test('normalizeText masks volatile values with the default rules', () => {
  const text = [
    'Date: Tue, 01 Oct 2024 10:00:00 GMT',
    'Cookie: sid=abc123; theme=dark',
    'Set-Cookie: sid=zzz; Path=/',
    'X-Request-Id: 9f2c-1',
    'X-CSRF-Token: tok123',
    '{"csrf_token":"abc","name":"x"}',
  ].join('\n');
  const out = normalizeText(text, DEFAULT_NORMALIZE_RULES);
  assert.ok(!/abc123|zzz|9f2c-1|tok123|10:00:00/.test(out), out);
  assert.ok(out.includes('"name":"x"'));
  assert.ok(out.includes('Cookie: <cookie>'));
});

test('normalizeJSON sorts keys recursively and pretty-prints; non JSON is returned unchanged', () => {
  assert.equal(normalizeJSON('{"b":1,"a":{"d":2,"c":[{"z":1,"y":2}]}}'), '{\n  "a": {\n    "c": [\n      {\n        "y": 2,\n        "z": 1\n      }\n    ],\n    "d": 2\n  },\n  "b": 1\n}');
  assert.equal(normalizeJSON('not json'), 'not json');
});

test('normalizeBody applies json and ignore options independently', () => {
  assert.equal(normalizeBody('{"b":1,"a":2}', { json: true }), '{\n  "a": 2,\n  "b": 1\n}');
  assert.equal(normalizeBody('X-Request-Id: 1', { ignore: true }), 'X-Request-Id: <id>');
  assert.equal(normalizeBody('keep', {}), 'keep');
});
