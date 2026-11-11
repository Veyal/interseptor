// Review regressions for core.js's body highlighters and beautifier. They render
// hostile captured bodies, so each must (1) emit exactly the escaped input plus
// well-formed <span> wrappers and (2) stay linear on adversarial input: the old
// multi-pass regexes backtracked quadratically (or worse) on bodies well under
// PRETTY_MAX and froze the tab. core.js touches the DOM at load, so the test
// slices the highlighter region out of it, as render-md.test.mjs does.
import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';

const src = fs.readFileSync(new URL('../js/core.js', import.meta.url), 'utf8');
const line = (prefix) => src.slice(src.indexOf(prefix), src.indexOf('\n', src.indexOf(prefix)));
const from = src.indexOf('export const PRETTY_MAX=');
const to = src.indexOf('/* ---- copy helpers ---- */');
assert.ok(from > 0 && to > from, 'core.js keeps the highlighter region');
const body = [line('export const esc='), line('export const escAttr='), src.slice(from, to)].join('\n').replace(/export /g, '');
const { esc, beautifyBody, highlightJSON, highlightMarkup, highlightCSS, highlightBodyText, PRETTY_MAX } = new Function(
  'fmtSize', 'withBaseHref',
  body + '\nreturn {esc,beautifyBody,highlightJSON,highlightMarkup,highlightCSS,highlightBodyText,PRETTY_MAX};',
)(String, (x) => x);

// The highlighters only ever add <span class="…"> and </span>. Removing exactly
// those must give back the escaped input, and nothing else may look like a tag.
function assertFaithful(html, input, label) {
  const stripped = html.replace(/<span class="[\w-]+">|<\/span>/g, '');
  assert.equal(stripped, esc(input), label + ': text is the escaped input');
  assert.ok(!/[<>]/.test(stripped), label + ': no stray markup');
  let depth = 0;
  for (const m of html.matchAll(/<span class="[\w-]+">|<\/span>/g)) {
    depth += m[0] === '</span>' ? -1 : 1;
    assert.ok(depth >= 0, label + ': a span closes before it opens');
  }
  assert.equal(depth, 0, label + ': spans are balanced');
}
function timed(fn) { const t = performance.now(); const out = fn(); return { ms: performance.now() - t, out }; }
const BUDGET_MS = 400; // the fixed code needs a few ms; the old code needed seconds to minutes

test('highlightCSS emits well-formed spans for at-rules, comments and selectors', () => {
  const css = '@media screen {\n  a:hover, b > c { color: #fff; margin: 0 10px; width: 50% }\n}\n/* note */ h1 { top: 1.5em }\n@import url("x.css");';
  const html = highlightCSS(css);
  assertFaithful(html, css, 'css');
  assert.ok(!html.includes('</<'), 'no tag is opened inside a closing tag');
  assert.ok(html.includes('<span class="hl-at">@media</span>'));
  assert.ok(html.includes('<span class="hl-cmt">/* note */</span>'));
  assert.ok(/<span class="hl-sel">\s*h1\s*<\/span>/.test(html), 'selector after a comment');
  assert.ok(html.includes('<span class="hl-prop">color</span>: <span class="hl-val">#fff</span>'));
  // As before, only the first value after the colon is colored.
  assert.ok(html.includes('<span class="hl-prop">margin</span>: <span class="hl-val">0</span> 10px;'));
  assert.ok(html.includes('<span class="hl-val">50%</span>') && html.includes('<span class="hl-val">1.5em</span>'));
  assert.ok(html.includes('<span class="hl-sel">a:hover, b &gt; c</span>'), 'the whole selector, escaped combinator included');
});

test('highlightCSS keeps hostile text escaped and intact', () => {
  for (const css of ['a{b:"</span><img src=x onerror=alert(1)>"}', '/* <script>x</script>', 'x { y: 1 } </style><b>', '}{;;@@/*/', '@media{', 'a:b;c{d:e}/**/']) {
    assertFaithful(highlightCSS(css), css, JSON.stringify(css));
  }
});

test('highlightCSS is linear on adversarial input', () => {
  const cases = {
    'space run before ;': 'a' + ' '.repeat(3000) + ';',
    'long word run': 'a'.repeat(32000),
    'unterminated comments': '/* '.repeat(80000),
    'selector chars without a brace': 'a b.c#d:e '.repeat(20000),
  };
  for (const [name, input] of Object.entries(cases)) {
    const { ms, out } = timed(() => highlightCSS(input));
    assert.ok(ms < BUDGET_MS, `${name}: ${ms.toFixed(0)} ms for ${input.length} chars`);
    assertFaithful(out, input, name);
  }
});

test('highlightMarkup colours the whole quoted attribute value', () => {
  const html = highlightMarkup('<a title="great target" href=\'/get;x?a=1&b=2\' data-n=5 hidden>x</a><!-- c -->');
  assert.ok(html.includes('<span class="hl-attr">title</span>=<span class="hl-str">"great target"</span>'), html);
  assert.ok(html.includes('<span class="hl-attr">href</span>=<span class="hl-str">\'/get;x?a=1&amp;b=2\'</span>'), html);
  assert.ok(html.includes('<span class="hl-attr">data-n</span>=<span class="hl-str">5</span>'), html);
  assert.ok(html.includes('&lt;/<span class="hl-tag">a</span>&gt;'));
  assert.ok(html.includes('<span class="hl-cmt">&lt;!-- c --&gt;</span>'));
});

test('highlightMarkup keeps hostile text escaped and intact', () => {
  for (const m of ['<a title="great target" href=\'/get;x\' data-n=5 hidden>x</a><!-- c -->', '<img src=x onerror="alert(1)">', '<a b="</span><script>">', '<!-- <b> --><i x=1>', '<a b=c', '<!--', '<a  b = "c" d=\'e', 'x < y > z']) {
    assertFaithful(highlightMarkup(m), m, JSON.stringify(m));
  }
});

test('highlightMarkup is linear on adversarial input', () => {
  const cases = {
    'whitespace run inside a tag': '<a' + ' '.repeat(64000) + 'x>',
    'long tag name, never closed': '<' + 'a'.repeat(200000),
    'unterminated tags': '<a '.repeat(80000),
    'unterminated comments': '<!-- '.repeat(50000),
    'attribute names without values': '<a' + ' b'.repeat(60000) + '>',
  };
  for (const [name, input] of Object.entries(cases)) {
    const { ms, out } = timed(() => highlightMarkup(input));
    assert.ok(ms < BUDGET_MS, `${name}: ${ms.toFixed(0)} ms for ${input.length} chars`);
    assertFaithful(out, input, name);
  }
});

// The previous implementation, kept to prove well-formed JSON renders unchanged.
const oldHighlightJSON = (s) => esc(s).replace(/("(?:\\.|[^"\\])*")(\s*:)?|\b(true|false|null)\b|(-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?)/g, (m, str, colon, lit, num) => {
  if (str !== undefined) return colon != null ? '<span class="js-key">' + str + '</span>' + colon : '<span class="js-str">' + str + '</span>';
  if (lit !== undefined) return '<span class="js-lit">' + lit + '</span>';
  return '<span class="js-num">' + num + '</span>';
});

test('highlightJSON renders well-formed JSON exactly as before', () => {
  const samples = [
    { a: 1, b: [true, false, null, -1.5e3], 'k"q': 's\\t"x', n: { 'a:b': '<tag> & "q"', u: '\u00e9\n' } },
    [], {}, 'plain', 12, [{ 'x': '' }],
  ];
  for (const v of samples) for (const text of [JSON.stringify(v), JSON.stringify(v, null, 2)]) {
    assert.equal(highlightJSON(text), oldHighlightJSON(text), text);
    assertFaithful(highlightJSON(text), text, text);
  }
});

test('highlightJSON matches the previous output on malformed text too', () => {
  // Deterministic fuzz over the characters that drive the tokenizer.
  let seed = 12345;
  const rnd = (n) => { seed = (seed * 1103515245 + 12345) & 0x7fffffff; return seed % n; };
  const alphabet = ['"', '"', '\\', '\\', ':', ' ', '\n', 'true', 'null', '1', '-2.5e3', 'a', ',', '{', '}', '<', '&'];
  for (let i = 0; i < 4000; i++) {
    let text = '';
    for (let k = rnd(14); k >= 0; k--) text += alphabet[rnd(alphabet.length)];
    assert.equal(highlightJSON(text), oldHighlightJSON(text), JSON.stringify(text));
  }
});

test('highlightJSON is linear on unterminated strings and stays faithful', () => {
  const cases = {
    'escaped quotes, never closed': '{"a":' + '\\"'.repeat(64000),
    'never closed, trailing backslash': '["' + '\\"'.repeat(64000) + '\\',
    'one open quote and a long tail': '{"k": "' + 'x1 true '.repeat(30000),
  };
  for (const [name, input] of Object.entries(cases)) {
    const { ms, out } = timed(() => highlightJSON(input));
    assert.ok(ms < BUDGET_MS, `${name}: ${ms.toFixed(0)} ms for ${input.length} chars`);
    assertFaithful(out, input, name);
  }
});

test('beautifyBody refuses a reformat that would explode in size', () => {
  const cap = 16 * PRETTY_MAX;
  const deep = '['.repeat(6000) + ']'.repeat(6000); // 12 KB -> 72 MB when indented
  const { ms, out } = timed(() => beautifyBody(deep));
  assert.equal(out, deep, 'deep JSON is returned as received');
  assert.ok(ms < BUDGET_MS, `deep JSON took ${ms.toFixed(0)} ms`);
  const tags = '<ab>'.repeat(3000); // 12 KB -> 9 MB when indented
  const m = timed(() => beautifyBody(tags));
  assert.equal(m.out, tags, 'runaway markup indentation is returned as received');
  assert.ok(m.ms < BUDGET_MS, `nested markup took ${m.ms.toFixed(0)} ms`);
  // Ordinary bodies are still reformatted, and stay under the cap.
  const ok = JSON.stringify({ a: [1, 2, { b: 'c' }], d: { e: { f: [] } } });
  assert.equal(beautifyBody(ok), JSON.stringify(JSON.parse(ok), null, 2));
  const wide = JSON.stringify(Array.from({ length: 5000 }, (_, i) => ({ id: i, name: 'item ' + i })));
  const pretty = beautifyBody(wide);
  assert.equal(pretty, JSON.stringify(JSON.parse(wide), null, 2));
  assert.ok(pretty.length < cap);
});

test('beautifyBody indents below single-letter tags', () => {
  assert.equal(beautifyBody('<div><p><b>x</b></p><a><i>y</i></a></div>'), '<div>\n  <p>\n    <b>x</b>\n  </p>\n  <a>\n    <i>y</i>\n  </a>\n</div>');
  assert.equal(beautifyBody('<ul><li>a</li><br/><hr><li>b</li></ul>'), '<ul>\n  <li>a</li>\n  <br/>\n  <hr>\n  <li>b</li>\n</ul>');
});

test('highlightBodyText stays within the budget for a hostile text/css body at the Pretty cap', () => {
  const input = ('a'.repeat(4000) + ' '.repeat(4000)).repeat(31); // 248 KB, under PRETTY_MAX
  assert.ok(input.length < PRETTY_MAX);
  const { ms, out } = timed(() => highlightBodyText(input, 'text/css'));
  assert.ok(ms < BUDGET_MS, `${ms.toFixed(0)} ms`);
  assertFaithful(out, input, 'css at cap');
});
