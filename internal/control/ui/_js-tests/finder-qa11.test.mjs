// finder.js findMatches: offsets are UTF-16 indices into the searched string, and
// the bar wraps each range in a <mark> over the rendered text nodes. A pattern
// without the `u` flag matches code units, so "." over an emoji used to report
// two one-unit matches — each mark then held half a surrogate pair (two U+FFFD in
// the pane) and the bar counted one character as two matches.
import test from 'node:test';
import assert from 'node:assert/strict';
import { findMatches, MAX_HIGHLIGHTS } from '../js/finder.js';

const slices = (text, out) => out.matches.map((m) => text.slice(m.start, m.end));
const onCodePointBoundaries = (text, out) => out.matches.every((m) => {
  const startOk = m.start === 0 || !(text.charCodeAt(m.start) >= 0xdc00 && text.charCodeAt(m.start) <= 0xdfff);
  const endOk = m.end === text.length || !(text.charCodeAt(m.end - 1) >= 0xd800 && text.charCodeAt(m.end - 1) <= 0xdbff);
  return startOk && endOk;
});

test('a literal search reports exact offsets, including multibyte text', () => {
  const text = 'héllo wörld 😀 wörld';
  const out = findMatches(text, 'wörld');
  assert.equal(out.matches.length, 2);
  assert.deepEqual(slices(text, out), ['wörld', 'wörld']);
  assert.equal(text.slice(out.matches[0].start, out.matches[0].end), 'wörld');
  // An astral literal matches as one whole pair.
  const emoji = findMatches(text, '😀');
  assert.deepEqual(slices(text, emoji), ['😀']);
  assert.deepEqual(emoji.matches[0], { start: text.indexOf('😀'), end: text.indexOf('😀') + 2 });
});

test('regex matches never split a surrogate pair or overlap each other', () => {
  for (const text of ['a😀b', '😀😀', 'x😀😀y', '😀']) {
    const out = findMatches(text, '.', { regex: true });
    assert.ok(onCodePointBoundaries(text, out), text + ' -> ' + JSON.stringify(out.matches));
    let last = -1;
    for (const m of out.matches) {
      assert.ok(m.start >= last, 'no overlap: ' + text);
      last = m.end;
    }
    // Every code point is covered exactly once for a single-unit pattern.
    assert.equal(out.matches.reduce((n, m) => n + (m.end - m.start), 0), text.length, text);
  }
  const out = findMatches('a😀b', '.', { regex: true });
  assert.deepEqual(slices('a😀b', out), ['a', '😀', 'b'], 'one match per character, not per code unit');
  // A lone surrogate in the input has no pair to respect; it must not crash or overlap.
  const lone = findMatches('a\ud83db', '.', { regex: true });
  assert.deepEqual(lone.matches, [{ start: 0, end: 1 }, { start: 1, end: 2 }, { start: 2, end: 3 }]);
});

test('literal and case-insensitive modes still behave', () => {
  const text = 'Foo foo FOO';
  assert.equal(findMatches(text, 'foo').matches.length, 3);
  assert.equal(findMatches(text, 'foo', { caseSensitive: true }).matches.length, 1);
  // Regex metacharacters are escaped in literal mode.
  assert.deepEqual(slices('a.b*c', findMatches('a.b*c', '.')), ['.']);
  assert.equal(findMatches('abc', '[').error, '', 'a literal [ is not a pattern error');
  assert.ok(findMatches('abc', '(', { regex: true }).error);
  assert.deepEqual(findMatches('abc', ''), { matches: [], capped: false, error: '' });
  assert.deepEqual(findMatches(null, 'x').matches, []);
});

test('the cap stops the scan and says so', () => {
  const text = 'aaaaaa';
  const out = findMatches(text, 'a', { cap: 3 });
  assert.equal(out.matches.length, 3);
  assert.equal(out.capped, true);
  assert.equal(findMatches(text, 'a', { cap: MAX_HIGHLIGHTS }).capped, false);
  // Exactly-cap matches are not "capped": there is nothing more to hide.
  assert.equal(findMatches('aaa', 'a', { cap: 3 }).capped, false);
});

test('zero-width patterns advance instead of spinning', () => {
  const out = findMatches('abc', 'x*', { regex: true });
  assert.equal(out.error, '');
  assert.ok(out.matches.every((m) => m.end > m.start));
});
