// copyas.js: the 1 MB body cap is a *byte* cap, and the URL builder has to keep
// working for a WebSocket (ws/wss) flow and for an IPv6 literal.
//
// The cap counted UTF-16 code units, so a multibyte body was copied at up to
// three times MAX_BODY_BYTES while the notice still claimed "1 MB", and a cut in
// the middle of an astral character left a lone surrogate (U+FFFD once pasted).
import test from 'node:test';
import assert from 'node:assert/strict';
import { flowURL, truncateBody, buildCopyAs, MAX_BODY_BYTES, urlHost } from '../js/copyas.js';

const bytes = (s) => Buffer.byteLength(s, 'utf8');
const utf16 = (s) => s.length;

test('truncateBody caps by UTF-8 bytes, not code units', () => {
  const ascii = truncateBody('a'.repeat(MAX_BODY_BYTES + 10));
  assert.equal(ascii.truncated, true);
  assert.equal(ascii.text.length, MAX_BODY_BYTES);

  // 'é' is two bytes: half as many characters fit in the same byte budget.
  const accent = truncateBody('é'.repeat(MAX_BODY_BYTES));
  assert.equal(accent.truncated, true);
  assert.equal(bytes(accent.text), MAX_BODY_BYTES);
  assert.equal(utf16(accent.text), MAX_BODY_BYTES / 2);

  // A 3-byte character (CJK) is the case the old code let through 3x too large.
  const cjk = 'ヨ'.repeat(MAX_BODY_BYTES);
  const cut = truncateBody(cjk);
  assert.equal(cut.truncated, true, 'a 3 MB body is truncated');
  assert.ok(bytes(cut.text) <= MAX_BODY_BYTES, `${bytes(cut.text)} bytes`);
  assert.equal(bytes(cut.text), MAX_BODY_BYTES - (MAX_BODY_BYTES % 3));
});

test('truncateBody never splits a surrogate pair', () => {
  for (const limit of [1, 2, 3, 4, 5, 6, 7]) {
    const out = truncateBody('a😀b', limit);
    assert.ok(bytes(out.text) <= limit, `limit ${limit} kept ${bytes(out.text)} bytes`);
    assert.ok(!/[\uD800-\uDBFF]$/.test(out.text), `limit ${limit} left a lone high surrogate`);
    assert.ok(!/^[\uDC00-\uDFFF]/.test(out.text.slice(0, 1)), `limit ${limit} started with a lone low surrogate`);
    // Whatever survives is a prefix of the original on code-point boundaries.
    assert.ok('a😀b'.startsWith(out.text), JSON.stringify(out.text));
  }
  assert.deepEqual(truncateBody('a😀b', 5), { text: 'a😀', truncated: true });
  assert.deepEqual(truncateBody('a😀b', 6), { text: 'a😀b', truncated: false });
});

test('truncateBody keeps a body that already fits and honours odd limits', () => {
  assert.deepEqual(truncateBody('small'), { text: 'small', truncated: false });
  assert.deepEqual(truncateBody(''), { text: '', truncated: false });
  assert.deepEqual(truncateBody(null), { text: '', truncated: false });
  const whole = 'x'.repeat(MAX_BODY_BYTES);
  assert.equal(truncateBody(whole).truncated, false, 'exactly the cap is not truncated');
  assert.deepEqual(truncateBody('abcdef', 0), { text: '', truncated: true });
});

test('flowURL builds a ws/wss replay URL, keeping a bracketed IPv6 host', () => {
  const v6 = { scheme: 'http', host: '::1', port: 8080, path: '/chat?x=1' };
  assert.equal(flowURL(v6, { scheme: 'ws' }), 'ws://[::1]:8080/chat?x=1');
  assert.equal(flowURL({ scheme: 'https', host: '::1', port: 443, path: '/chat' }, { scheme: 'wss' }), 'wss://[::1]/chat');
  assert.equal(flowURL({ scheme: 'https', host: 'app.example.com', port: 8443, path: '/ws' }, { scheme: 'wss' }), 'wss://app.example.com:8443/ws');
  assert.equal(flowURL({ scheme: 'https', host: 'app.example.com', port: 443, path: '/' }, { scheme: 'wss' }), 'wss://app.example.com/');
  // The transport scheme decides the default port, and no override means http(s).
  assert.equal(flowURL({ scheme: 'http', host: 'h', port: 80, path: '/' }), 'http://h/');
  assert.equal(flowURL({ scheme: 'http', host: 'h', port: '8080', path: '/' }), 'http://h:8080/');
});

test('urlHost brackets only a literal with more than one colon', () => {
  assert.equal(urlHost('::1'), '[::1]');
  assert.equal(urlHost('2001:db8::1'), '[2001:db8::1]');
  assert.equal(urlHost('[::1]'), '[::1]');
  assert.equal(urlHost('example.com:8080'), 'example.com:8080');
  assert.equal(urlHost(''), '');
});

test('copy-as URL output brackets an IPv6 host and never echoes an index', () => {
  const flows = [
    { scheme: 'http', host: '::1', port: 8080, path: '/a' },
    { scheme: 'http', host: '::2', port: 8080, path: '/b' },
  ];
  assert.equal(buildCopyAs('url', flows).text, 'http://[::1]:8080/a\nhttp://[::2]:8080/b');
});
