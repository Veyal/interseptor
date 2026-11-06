import test from 'node:test';
import assert from 'node:assert/strict';
import { decodedShape } from '../js/codec-view-model.js';

const beautify = (s) => {
  const t = s.trim();
  if (t && (t[0] === '{' || t[0] === '[')) {
    try { return JSON.stringify(JSON.parse(t), null, 2); } catch (e) { /* not JSON */ }
  }
  return s;
};

test('compact JSON becomes indented and keeps the raw text', () => {
  const raw = '{"rc":"0","data":[1,2]}';
  const r = decodedShape(raw, 256 * 1024, beautify);
  assert.equal(r.beautified, true);
  assert.equal(r.raw, raw);
  assert.match(r.shown, /\n\s+"rc": "0"/);
});

test('plain text is left alone, with no toggle', () => {
  const r = decodedShape('SSH-2.0-OpenSSH_9.6', 256 * 1024, beautify);
  assert.equal(r.beautified, false);
  assert.equal(r.shown, 'SSH-2.0-OpenSSH_9.6');
});

test('a body over the ceiling is shown as-is', () => {
  const raw = '{"a":' + '1'.repeat(40) + '}';
  const r = decodedShape(raw, 10, beautify);
  assert.equal(r.beautified, false);
  assert.equal(r.shown, raw);
});

test('a throwing beautifier degrades to the raw text', () => {
  const r = decodedShape('{"a":1}', 1 << 20, () => { throw new Error('boom'); });
  assert.equal(r.beautified, false);
  assert.equal(r.shown, '{"a":1}');
});

test('empty or non-string input is safe', () => {
  assert.deepEqual(decodedShape('', 100, beautify), { shown: '', raw: '', beautified: false });
  assert.equal(decodedShape(null, 100, beautify).shown, '');
});
