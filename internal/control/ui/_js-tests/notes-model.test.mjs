import test from 'node:test';
import assert from 'node:assert/strict';
import { parseNoteRefs, collectRefIds, resolveRefs, promoteRequest, chipLabel, MAX_REFS } from '../js/notes-model.js';

test('finding and flow references become tokens', () => {
  const t = parseNoteRefs('See #F-02 and flow:123, also #F-7.');
  assert.deepEqual(t.filter((x) => x.type !== 'text').map((x) => [x.type, x.id]), [['finding', 2], ['flow', 123], ['finding', 7]]);
  assert.equal(t.map((x) => x.text).join(''), 'See #F-02 and flow:123, also #F-7.');
});

test('lookalikes stay plain text', () => {
  const t = parseNoteRefs('abc#F-2 xflow:5 #F- #Fx-2 flow: 9 http://example.com/#F-3');
  assert.equal(t.every((x) => x.type === 'text'), true);
});

test('collectRefIds dedupes and caps', () => {
  const text = Array.from({ length: 80 }, (_, i) => 'flow:' + (i + 1)).join(' ') + ' #F-1 #F-1';
  const ids = collectRefIds(text);
  assert.equal(ids.flows.size, MAX_REFS);
  assert.deepEqual([...ids.findings], [1]);
});

test('unknown ids render as plain text, never as chips', () => {
  const tokens = parseNoteRefs('#F-1 #F-2 flow:9 flow:10');
  const out = resolveRefs(tokens, { findingIds: new Set([1]), flowIds: new Set([10]) });
  assert.deepEqual(out.filter((x) => x.type !== 'text').map((x) => x.text), ['#F-1', 'flow:10']);
  assert.equal(out.map((x) => x.text).join(''), '#F-1 #F-2 flow:9 flow:10');
  const none = resolveRefs(tokens, {});
  assert.equal(none.every((x) => x.type === 'text'), true);
});

test('chipLabel is readable and names the kind', () => {
  assert.equal(chipLabel({ type: 'finding', id: 2 }), 'Finding F-2');
  assert.equal(chipLabel({ type: 'flow', id: 123 }), 'Flow #123');
});

test('promoteRequest derives a bounded draft from a selection', () => {
  const r = promoteRequest('## Login bypass\nThe reset token is predictable.');
  assert.equal(r.path, '/api/findings');
  assert.equal(r.method, 'POST');
  assert.equal(r.body.title, 'Login bypass');
  assert.equal(r.body.status, 'needs_verification');
  assert.equal(r.body.source, 'human');
  assert.match(r.body.detail, /predictable/);
  assert.equal(promoteRequest('   \n '), null);
  assert.equal(promoteRequest('x'.repeat(500)).body.title.length <= 120, true);
});
