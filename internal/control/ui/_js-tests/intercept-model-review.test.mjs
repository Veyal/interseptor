// Review regression for intercept-model.js losslessBeautify: the whitespace
// squasher retried an unterminated string from every later quote, each try
// scanning to the end again, so a held message with a crafted body froze the
// Intercept editor when Pretty was opened (64 KB took over 2 s, quadratic).
import test from 'node:test';
import assert from 'node:assert/strict';
import { losslessBeautify } from '../js/intercept-model.js';

// The previous squasher, kept to prove the comparison is unchanged.
const oldSquash = (s) => s.replace(/"(?:\\.|[^"\\])*"|\s+/g, (m) => (m[0] === '"' ? m : ''));
const oldLossless = (body, beautify) => {
  try { const out = beautify(body); return oldSquash(out) === oldSquash(body.trim()) ? out : body; } catch (e) { return body; }
};

test('losslessBeautify is linear on an unterminated string', () => {
  for (const body of ['{"a":' + '\\"'.repeat(128000), '<a b="' + ' x\\" '.repeat(50000), '"' + 'a \\\n" '.repeat(40000)]) {
    const t = performance.now();
    const out = losslessBeautify(body, (b) => '  ' + b.replace(/ /g, '  '));
    const ms = performance.now() - t;
    assert.ok(ms < 400, `${ms.toFixed(0)} ms for ${body.length} chars`);
    assert.equal(typeof out, 'string');
  }
});

test('losslessBeautify decides exactly as before, malformed text included', () => {
  let seed = 4242;
  const rnd = (n) => { seed = (seed * 1103515245 + 12345) & 0x7fffffff; return seed % n; };
  const alphabet = ['"', '"', '\\', '\\', ' ', ' ', '\n', '\t', 'a', '1', ':', '{', '}', '<', '>'];
  const reformats = [(b) => b.replace(/\s+/g, ' '), (b) => ' ' + b.split('').join(' '), (b) => b.replace(/a/g, 'b'), (b) => b];
  for (let i = 0; i < 6000; i++) {
    let text = '';
    for (let k = rnd(16); k >= 0; k--) text += alphabet[rnd(alphabet.length)];
    const f = reformats[i % reformats.length];
    assert.equal(losslessBeautify(text, f), oldLossless(text, f), JSON.stringify(text));
  }
  const json = '{"a": 1, "b c": [true, "x  y"]}';
  assert.equal(losslessBeautify(json, (b) => JSON.stringify(JSON.parse(b), null, 2)), JSON.stringify(JSON.parse(json), null, 2));
  assert.equal(losslessBeautify('{"a":1.0}', (b) => JSON.stringify(JSON.parse(b), null, 2)), '{"a":1.0}');
});
