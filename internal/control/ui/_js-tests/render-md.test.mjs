// js/core.js renderMD under node. core.js touches the DOM at load, so the test
// slices renderMD and the escape helpers out of it, as windowed-list.test does.
import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';

const src = fs.readFileSync(new URL('../js/core.js', import.meta.url), 'utf8');
function sliceFunction(signature) {
  const start = src.indexOf(signature);
  assert.ok(start >= 0, 'core.js has ' + signature);
  let depth = 0;
  for (let i = src.indexOf('{', start); i < src.length; i++) {
    if (src[i] === '{') depth++;
    else if (src[i] === '}' && --depth === 0) return src.slice(start, i + 1);
  }
  throw new Error('unbalanced ' + signature);
}
const line = (prefix) => src.slice(src.indexOf(prefix), src.indexOf('\n', src.indexOf(prefix)));
const body = [line('export const esc='), line('export const escAttr='), sliceFunction('export function renderMD(src)')]
  .join('\n').replace(/export /g, '');
const renderMD = new Function(body + '\nreturn renderMD;')();

test('blockquotes render after escaping (esc turns > into &gt;)', () => {
  const html = renderMD('intro\n\n> first line\n> second **bold**\n\nafter');
  assert.match(html, /<blockquote class="md-quote">first line<br>second <strong>bold<\/strong><\/blockquote>/);
  assert.doesNotMatch(html, /&gt; first/);
  assert.match(html, /<p>after<\/p>/);
});

test('a quoted tag stays escaped inside the blockquote', () => {
  const html = renderMD('> <img src=x onerror=alert(1)>');
  assert.match(html, /<blockquote class="md-quote">&lt;img src=x onerror=alert\(1\)&gt;<\/blockquote>/);
});

test('a > inside a line is not a quote', () => {
  const html = renderMD('a > b');
  assert.doesNotMatch(html, /blockquote/);
  assert.match(html, /a &gt; b/);
});
