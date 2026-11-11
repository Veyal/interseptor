// core.js esc() keeps quotes on purpose (text and the highlighters); a value placed
// inside a quoted attribute needs escAttr(). This scans every UI module that uses the
// core esc for an esc( call sitting inside an attribute quote that is still open.
import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';

const dir = new URL('../js/', import.meta.url);
const files = fs.readdirSync(dir).filter((f) => f.endsWith('.js'));

export function escInOpenAttribute(src) {
  const hits = [];
  const re = /(?<![A-Za-z_$.])esc\(/g;
  let m;
  while ((m = re.exec(src))) {
    const back = src.slice(Math.max(0, m.index - 600), m.index);
    const lt = back.lastIndexOf('<');
    if (lt < 0 || back.lastIndexOf('>') > lt) continue; // not inside a tag
    const tag = back.slice(lt);
    for (const q of ['"', "'"]) {
      const open = Math.max(tag.lastIndexOf('=' + q), tag.lastIndexOf('=\\' + q));
      if (open < 0) continue;
      // Interpolations may hold quotes of their own; what is left must not close the attribute.
      const rest = tag.slice(tag.indexOf(q, open) + 1).replace(/\$\{[^}]*\}/g, '');
      if (q === '"' ? rest.includes('"') : /'\s*(?:[a-z-]+=|>|\/>)/i.test(rest) || !/^[^'"`+]*$/.test(rest)) continue;
      hits.push(src.slice(0, m.index).split('\n').length);
      break;
    }
  }
  return hits;
}

test('the scanner sees esc() in a quoted attribute and ignores text and escAttr', () => {
  assert.deepEqual(escInOpenAttribute('x=`<b title="Change: ${esc(v)}">${esc(v)}</b>`'), [1]);
  assert.deepEqual(escInOpenAttribute("x='<b title=\"'+esc(v)+'\">'"), [1]);
  assert.deepEqual(escInOpenAttribute('x=`<b title="${escAttr(v)}">${esc(v)}</b>`'), []);
  assert.deepEqual(escInOpenAttribute('x=`<b class="a ${k}" id="z">${esc(v)}</b>`'), []);
});

// Lines reviewed and deliberately left alone, as [file, source text]. Matched by the source
// line, not its number, so an unrelated edit above does not break the test. Empty: every hit
// is a defect to fix, not to list here.
const KNOWN_STATIC = [];

test('no module puts core esc() inside a quoted attribute', () => {
  const bad = [];
  for (const f of files) {
    const src = fs.readFileSync(new URL(f, dir), 'utf8');
    // A module with its own five-character escaper named esc is safe in attributes.
    if (/(?:const|let|function)\s+esc\b/.test(src) && f !== 'core.js') continue;
    const lines = src.split('\n');
    for (const line of escInOpenAttribute(src)) {
      if (KNOWN_STATIC.some(([file, text]) => file === f && lines[line - 1].includes(text))) continue;
      bad.push(f + ':' + line);
    }
  }
  assert.deepEqual(bad, []);
});
