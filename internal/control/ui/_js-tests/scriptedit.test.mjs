import test from 'node:test';
import assert from 'node:assert/strict';
import { tokenize, highlightHTML, lineCount, scanUnsupported, esc } from '../js/scriptedit.js';

test('tokenize reproduces the source exactly', () => {
  const src = "// c\nconst a = pm.variables.get('x'); /* b */ if (a === 1.5e3) { console.log(`t ${a}`); }\n";
  assert.equal(tokenize(src).map((t) => t.s).join(''), src);
});

test('token kinds', () => {
  const kinds = (s) => tokenize(s).filter((t) => t.t !== 'ws').map((t) => [t.t, t.s]);
  assert.deepEqual(kinds("const x = 'a' // c"), [['kw', 'const'], ['id', 'x'], ['pun', '='], ['str', "'a'"], ['com', '// c']]);
  assert.deepEqual(kinds('pm.test(1)')[0], ['api', 'pm']);
  assert.deepEqual(kinds('true')[0], ['lit', 'true']);
});

test('unterminated string and comment do not hang', () => {
  assert.equal(tokenize("'abc").length, 1);
  assert.equal(tokenize('/* open').length, 1);
  assert.equal(tokenize('`multi\nline').length, 1);
});

test('highlight escapes markup', () => {
  const h = highlightHTML('<img onerror=1> "x" &');
  assert.ok(!h.includes('<img'));
  assert.ok(h.includes('&lt;img'));
  assert.equal(esc('<&>'), '&lt;&amp;&gt;');
  assert.equal(highlightHTML('a\n').endsWith(' '), true);
  assert.equal(lineCount('a\nb\n'), 3);
});

test('unsupported hints ignore comments and strings, report lines', () => {
  const src = "// pm.vault.get('x')\nconst s = 'pm.vault';\npm.vault.get('k');\nconst d = _.map(a, f);\nfetch('http://example.com');\npm.test('ok', () => {});";
  const hits = scanUnsupported(src);
  assert.deepEqual(hits.map((h) => [h.line, h.name]), [[3, 'pm.vault'], [4, '_ (lodash)'], [5, 'fetch']]);
  assert.deepEqual(scanUnsupported("pm.environment.set('a','b')"), []);
});
