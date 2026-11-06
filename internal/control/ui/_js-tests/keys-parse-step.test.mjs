// parseKeys has to read the literal '+' key out of a binding spec. The trailing
// separator was stripped with slice(0, -2), which ate the last letter of the
// modifier too: "Mod++" parsed as the unknown modifier "Mo" and threw, and
// "Ctrl+" reported "unknown modifier Ctr". One '+' is the separator; strip one.
import test from 'node:test';
import assert from 'node:assert/strict';
import { parseKeys, eventStep } from '../js/keys.js';

test('a binding can name the literal + key', () => {
  assert.deepEqual(parseKeys('+'), ['+']);
});

test('Mod++ is the + key with the whole modifier, not a truncated one', () => {
  // The canonical step joins modifier and key with '+', so the + key reads "Mod++".
  assert.deepEqual(parseKeys('Mod++'), ['Mod++']);
  assert.deepEqual(parseKeys('Ctrl++'), ['Mod++']);
  assert.deepEqual(parseKeys('Alt++'), ['Alt++']);
});

test('a trailing separator alone does not truncate the modifier', () => {
  // "Ctrl+" is Mod plus the + key; it must not report the modifier "Ctr".
  assert.deepEqual(parseKeys('Ctrl+'), ['Mod++']);
});

test('the parsed + step matches what a real keypress produces', () => {
  // Shift is carried by the character itself for a bare key, so Shift++ is just +.
  assert.deepEqual(parseKeys('Shift++'), ['+']);
  const ctrlPlus = { key: '+', ctrlKey: true, metaKey: false, altKey: false, shiftKey: false };
  assert.equal(eventStep(ctrlPlus), parseKeys('Mod++')[0]);
  const shiftPlus = { key: '+', ctrlKey: false, metaKey: false, altKey: false, shiftKey: true };
  assert.equal(eventStep(shiftPlus), parseKeys('Shift++')[0]);
});

test('ordinary chords and combinations are unchanged', () => {
  assert.deepEqual(parseKeys('g p'), ['g', 'p']);
  assert.deepEqual(parseKeys('Ctrl+Enter'), ['Mod+Enter']);
  assert.deepEqual(parseKeys('Cmd+K'), ['Mod+k']);
  assert.deepEqual(parseKeys('Shift+Enter'), ['Shift+Enter']);
  assert.deepEqual(parseKeys('N'), ['N']);
  assert.deepEqual(parseKeys('Esc'), ['Escape']);
  assert.throws(() => parseKeys('  '), /empty binding/);
  assert.throws(() => parseKeys('Meta+Nope+'), /unknown modifier/);
});

test('a chord of + key steps stays two steps', () => {
  assert.deepEqual(parseKeys('+ +'), ['+', '+']);
});
