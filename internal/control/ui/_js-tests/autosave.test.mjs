// js/core.js createAutosave under node (sliced out: core.js touches the DOM at load).
import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';

const src = fs.readFileSync(new URL('../js/core.js', import.meta.url), 'utf8');
const start = src.indexOf('export function createAutosave(');
let end = -1;
for (let i = src.indexOf('{', src.indexOf(')', start)), depth = 0; i < src.length; i++) {
  if (src[i] === '{') depth++;
  else if (src[i] === '}' && --depth === 0) { end = i + 1; break; }
}
const createAutosave = new Function(src.slice(start, end).replace('export ', '') + '\nreturn createAutosave;')();

test('editing back to the saved text clears "Unsaved changes"', () => {
  const seen = [];
  const a = createAutosave({ delay: 10_000, save: async () => {}, onStatus: (k) => seen.push(k) });
  a.setBaseline('saved text');
  a.schedule('');
  assert.equal(seen.at(-1), 'dirty');
  a.schedule('saved text');
  assert.equal(a.isDirty(), false);
  assert.equal(seen.at(-1), 'saved', 'the status must not stay on "Unsaved changes" when nothing is unsaved');
  a.schedule('saved text');
  assert.equal(seen.filter((k) => k === 'saved').length, 1, 'no repeated status for a no-op');
});

test('an untouched baseline reports no status', () => {
  const seen = [];
  const a = createAutosave({ save: async () => {}, onStatus: (k) => seen.push(k) });
  a.setBaseline('x');
  a.schedule('x');
  assert.deepEqual(seen, []);
});
