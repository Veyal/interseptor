// runner.js — the poll error line.
//
// Reported defect: a failed poll set st.error ("dead state"). Nothing rendered it, so a run
// whose follow-up poll kept failing looked like a run that had simply stopped: the progress
// rows froze and no explanation appeared. The panel needs a non-blocking line — the run is
// still live and the next poll replaces the text — plus a Retry action.
//
// The user-visible text is a pure formatting function; runner.js itself imports core.js,
// which touches the DOM at load, so the helper is extracted from the module source and
// exercised here (the repo's UI tests use the same extraction: see windowed-list.test.mjs).
import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';

const source = fs.readFileSync(new URL('../js/runner.js', import.meta.url), 'utf8');

function extractFunction(src, name) {
  const sig = 'export function ' + name + '(';
  const start = src.indexOf(sig);
  assert.ok(start >= 0, 'runner.js exports ' + name + '()');
  let i = start + sig.length - 1;
  let parens = 0;
  for (; i < src.length; i++) {
    if (src[i] === '(') parens++;
    else if (src[i] === ')' && --parens === 0) { i++; break; }
  }
  const bodyStart = src.indexOf('{', i);
  assert.ok(bodyStart >= i, name + '() has a body');
  let depth = 0;
  for (let j = bodyStart; j < src.length; j++) {
    if (src[j] === '{') depth++;
    else if (src[j] === '}' && --depth === 0) {
      return new Function('return (' + src.slice(start + 'export '.length, j + 1) + ')')();
    }
  }
  throw new Error('unbalanced ' + name + '()');
}

const pollErrorText = extractFunction(source, 'pollErrorText');

test('a failed poll becomes one line naming the failure', () => {
  assert.equal(pollErrorText('fetch failed'), 'Poll failed: fetch failed');
  assert.equal(pollErrorText(new Error('network error').message), 'Poll failed: network error');
  // The fallback the poll loop already uses when the error carries no message.
  assert.equal(pollErrorText('poll failed'), 'Poll failed: poll failed');
});

test('a healthy poll renders nothing, so the line is hidden', () => {
  for (const quiet of ['', '  ', '\n\t ', null, undefined]) {
    assert.equal(pollErrorText(quiet), '', JSON.stringify(quiet));
  }
});

test('the message is trimmed and kept to a single line', () => {
  assert.equal(pollErrorText('  timeout  '), 'Poll failed: timeout');
  // A multi-line message must not grow the panel: the line is one status row, and the run's
  // progress rows below it keep their place.
  assert.equal(pollErrorText('TypeError: fetch failed\n    at poll (runner.js:256)'), 'Poll failed: TypeError: fetch failed at poll (runner.js:256)');
  assert.equal(pollErrorText('a\n\n b\tc'), 'Poll failed: a b c');
});

test('non-string failures still produce a readable line', () => {
  assert.equal(pollErrorText(0), 'Poll failed: 0');
  assert.equal(pollErrorText({ message: 'x' }), 'Poll failed: [object Object]');
  assert.equal(pollErrorText(404), 'Poll failed: 404');
});

test('the run panel keeps a Retry action and the existing strings next to the line', () => {
  // The line is rendered next to the progress rows, not instead of them, and the retry is a
  // button label that already exists in this view's vocabulary.
  assert.match(source, /'Retry'/, 'the poll error line offers a Retry action');
  assert.match(source, /renderPollError\(\)/, 'render() paints the poll error line');
  // Strings a Go UI contract test asserts: they must survive the change.
  assert.match(source, /'Run failed\.'/);
  assert.match(source, /'Starting\.\.\.'/);
  // The line must not replace the progress rows: renderRows() still runs on its own.
  assert.match(source, /function render\(\) \{ renderRows\(\); renderAsk\(\); renderStatus\(\); renderPollError\(\); \}/);
  // A Go UI contract test rejects inline style in this view (Class, not style), and this
  // line must not be the exception.
  assert.ok(!source.includes('.style.'), 'runner.js writes no inline style');
  assert.ok(!source.includes('style="'), 'runner.js writes no style attribute');
});
