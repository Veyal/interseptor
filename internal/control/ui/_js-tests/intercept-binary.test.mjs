import { test } from 'node:test';
import assert from 'node:assert/strict';
import { normalizeEOL, heldIsModified, heldForwardBody, forwardAllPlan, heldEditorBaseline, binaryNoteText, previewableImage, heldReleasedText } from '../js/intercept-model.js';

// Regression: a <textarea> folds a lone CR into LF as well as CRLF, so an
// untouched PNG (whose signature holds "\r\n\x1a\n" and stray CRs) read as
// modified and Forward replaced it with the lossy editor text.
test('a lone CR is a line ending too: an untouched message is not modified', () => {
  assert.equal(normalizeEOL('a\rb\r\nc'), 'a\nb\nc');
  const original = 'HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\n\r\nline1\rline2';
  const textareaValue = original.replace(/\r\n?/g, '\n');
  assert.equal(heldIsModified(textareaValue, original), false);
  assert.equal(heldForwardBody(textareaValue, original), null);
});

test('an edit to a binary message forwards the head and keeps the held body', () => {
  const original = 'HTTP/1.1 200 OK\r\nContent-Type: image/png\r\n\r\n';
  assert.equal(heldForwardBody(original.replace(/\r\n/g, '\n'), original, { binary: true }), null);
  assert.deepEqual(heldForwardBody('HTTP/1.1 200 OK\nX-A: 1\n\n', original, { binary: true }), { raw: 'HTTP/1.1 200 OK\nX-A: 1\n\n', keepBody: true });
  assert.deepEqual(heldForwardBody('GET /b HTTP/1.1\n\n', 'GET /a HTTP/1.1\r\n\r\n'), { raw: 'GET /b HTTP/1.1\n\n' });
});

test('Forward all keeps binary bodies too', () => {
  const items = [{ side: 'resp', id: 1 }, { side: 'req', id: 2 }];
  const rawCache = new Map([['resp:1', 'HTTP/1.1 200 OK\nX: 1\n\n'], ['req:2', 'GET / HTTP/1.1\r\n\r\n']]);
  const originalCache = new Map([['resp:1', 'HTTP/1.1 200 OK\r\n\r\n'], ['req:2', 'GET / HTTP/1.1\r\n\r\n']]);
  const plan = forwardAllPlan(items, { rawCache, originalCache, binary: new Set(['resp:1']) });
  assert.deepEqual(plan, [{ side: 'resp', id: 1, raw: 'HTTP/1.1 200 OK\nX: 1\n\n', keepBody: true }, { side: 'req', id: 2 }]);
});

// Regression: returning to an edited item compared the edit against itself, so
// MODIFIED and the Content-Length note disappeared although the edit was kept.
test('the editor baseline is the cached original, not the cached edit', () => {
  const cache = new Map([['req:1', 'GET /a HTTP/1.1\r\n\r\n']]);
  assert.equal(heldEditorBaseline(cache, 'req:1', { raw: 'GET /edited HTTP/1.1\n\n' }), 'GET /a HTTP/1.1\r\n\r\n');
  assert.equal(heldEditorBaseline(new Map(), 'req:2', { original: 'O', raw: 'R' }), 'O');
  assert.equal(heldEditorBaseline(new Map(), 'req:2', { raw: 'R' }), 'R');
});

test('binary note and image preview', () => {
  assert.equal(binaryNoteText({ bodyLen: 7858 }), 'Binary body · 7,858 bytes · forwarded unchanged; only the head is editable.');
  assert.equal(previewableImage('image/png; charset=binary'), true);
  assert.equal(previewableImage('image/svg+xml'), false);
  assert.equal(previewableImage('text/html'), false);
  assert.equal(previewableImage(''), false);
});

test('a held item released by its client says so', () => {
  assert.equal(heldReleasedText({ side: 'req', method: 'POST', host: 'example.com', path: '/login' }, true), 'POST example.com/login was released: the client closed the connection. Your edit is kept — open it in Repeater.');
  assert.equal(heldReleasedText({ side: 'resp', host: 'example.com', path: '/a' }, false), 'Response example.com/a was released: the client closed the connection.');
});

import { binaryHandoff, repeaterLoadedText, binaryTailWarning } from '../js/intercept-model.js';

// Regression: a binary held request reached Repeater with an empty body and its
// stale Content-Length, with no hint that the bytes were left behind.
test('binary hand-off to Repeater zeroes Content-Length and says what was left behind', () => {
  const h = binaryHandoff('POST /up HTTP/1.1\nHost: example.com\nContent-Length: 98\n\n', 98);
  assert.match(h.raw, /Content-Length: 0\n/);
  assert.doesNotMatch(h.raw, /Content-Length: 98/);
  assert.match(h.notice, /98 bytes/);
  assert.match(h.notice, /without a body/);
});

test('Repeater toast says what became of the held item', () => {
  assert.match(repeaterLoadedText('held'), /still held/);
  assert.doesNotMatch(repeaterLoadedText('forwarded'), /still held/);
  assert.match(repeaterLoadedText('forwarded'), /forwarded/);
});

test('text after the head of a binary message is flagged', () => {
  assert.equal(binaryTailWarning('POST / HTTP/1.1\nA: b\n\n'), '');
  assert.equal(binaryTailWarning('POST / HTTP/1.1\nA: b\n\n  \n'), '');
  assert.match(binaryTailWarning('POST / HTTP/1.1\nA: b\n\nnew body'), /ignored/);
  assert.equal(binaryTailWarning('POST / HTTP/1.1\nA: b'), '');
});
