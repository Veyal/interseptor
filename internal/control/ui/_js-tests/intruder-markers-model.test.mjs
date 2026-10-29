import test from 'node:test';
import assert from 'node:assert/strict';
import { splitMarkers, markersHighlightHTML } from '../js/intruder-markers-model.js';

const kinds = (segs) => segs.map((s) => `${s.kind}:${s.index ?? '-'}:${s.text}`);

test('plain text without markers is a single plain segment', () => {
  assert.deepEqual(splitMarkers('GET / HTTP/1.1'), [{ text: 'GET / HTTP/1.1', kind: 'plain' }]);
  assert.deepEqual(splitMarkers(''), []);
});

test('multiple markers get increasing colour indexes with delimiters', () => {
  const segs = splitMarkers('a=§x§&b=§y§!');
  assert.deepEqual(kinds(segs), [
    'plain:-:a=', 'delim:0:§', 'marker:0:x', 'delim:0:§', 'plain:-:&b=',
    'delim:1:§', 'marker:1:y', 'delim:1:§', 'plain:-:!',
  ]);
});

test('adjacent markers are separate positions', () => {
  const segs = splitMarkers('§a§§b§');
  assert.deepEqual(kinds(segs), [
    'delim:0:§', 'marker:0:a', 'delim:0:§', 'delim:1:§', 'marker:1:b', 'delim:1:§',
  ]);
});

test('empty §§ is invalid and not a position', () => {
  assert.deepEqual(kinds(splitMarkers('a§§b')), ['plain:-:a', 'invalid:-:§', 'invalid:-:§', 'plain:-:b']);
});

test('§§ followed by content mirrors the regex: first § invalid, rest is a marker', () => {
  assert.deepEqual(kinds(splitMarkers('§§abc§')), ['invalid:-:§', 'delim:0:§', 'marker:0:abc', 'delim:0:§']);
});

test('unpaired trailing § is invalid', () => {
  assert.deepEqual(kinds(splitMarkers('§a§ tail §b')), [
    'delim:0:§', 'marker:0:a', 'delim:0:§', 'plain:-: tail ', 'invalid:-:§', 'plain:-:b',
  ]);
});

test('multi-line marker content and CRLF stay inside the marker', () => {
  const segs = splitMarkers('x\r\n§a\r\nb§\r\ny');
  assert.deepEqual(kinds(segs), ['plain:-:x\r\n', 'delim:0:§', 'marker:0:a\r\nb', 'delim:0:§', 'plain:-:\r\ny']);
  assert.equal(segs.map((s) => s.text).join(''), 'x\r\n§a\r\nb§\r\ny');
});

test('segments always concatenate back to the input', () => {
  for (const s of ['§', '§§', '§§§', '§a§§', 'a§b§c§d', '§\n§', '<§<b>§>']) {
    assert.equal(splitMarkers(s).map((x) => x.text).join(''), s);
  }
});

test('marker count equals the intrMarkers regex count', () => {
  for (const s of ['§a§§b§', '§§abc§', 'a§§b', '§a§ §b', '§§§§§', '§x§§§y§']) {
    const want = (s.match(/§[^§]+§/g) || []).length;
    const got = new Set(splitMarkers(s).filter((x) => x.kind === 'marker').map((x) => x.index)).size;
    assert.equal(got, want, s);
  }
});

test('html is escaped and colours cycle through the palette', () => {
  const html = markersHighlightHTML('<img src=x onerror=1>§<b>&§§2§', ['var(--a)', 'var(--b)']);
  assert.ok(!html.includes('<img'));
  assert.ok(!html.includes('<b>'));
  assert.match(html, /&lt;img src=x onerror=1&gt;/);
  assert.match(html, /&lt;b&gt;&amp;/);
  assert.match(html, /class="ih-m" style="--ih:var\(--a\)">&lt;b&gt;&amp;/);
  assert.match(html, /style="--ih:var\(--b\)">2</);
  const three = markersHighlightHTML('§a§§b§§c§', ['var(--a)', 'var(--b)']);
  assert.ok(three.lastIndexOf('--ih:var(--a)') > three.indexOf('--ih:var(--b)'));
});

test('invalid markers get the warning class; disabled drops marker styling', () => {
  assert.match(markersHighlightHTML('a§', ['red']), /<span class="ih-bad">§<\/span>/);
  const off = markersHighlightHTML('§a§ §', ['red'], { enabled: false });
  assert.equal(off, '§a§ §');
});
