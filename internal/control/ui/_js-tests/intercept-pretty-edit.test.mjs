import test from 'node:test';
import assert from 'node:assert/strict';
import { normalizeEOL, heldIsModified, heldForwardRaw, createPrettyBridge } from '../js/intercept-model.js';

// Same shape as intercept.js heldBeautified for a JSON body: indent the body.
function beautify(raw) {
  const m = /\r?\n\r?\n/.exec(raw);
  if (!m) return raw;
  const head = raw.slice(0, m.index).replace(/\r\n/g, '\n');
  const body = raw.slice(m.index + m[0].length);
  try { return head + '\n\n' + JSON.stringify(JSON.parse(body), null, 2); } catch { return raw; }
}

// A fake editor session: `ta` mirrors #heldRaw (LF-normalized like a textarea).
function session(original) {
  const bridge = createPrettyBridge({ beautify });
  const s = {
    ta: normalizeEOL(original),
    viewPretty() { return bridge.show(s.ta); },
    typePretty(text) { s.ta = bridge.edit(text); },
    typeRaw(text) { s.ta = text; },
    reset() { s.ta = normalizeEOL(original); bridge.reset(); },
    get modified() { return heldIsModified(s.ta, original); },
    get forward() { return heldForwardRaw(s.ta, original); },
  };
  return s;
}

const REQ = 'POST /api HTTP/1.1\r\nHost: example.com\r\nContent-Type: application/json\r\nContent-Length: 17\r\n\r\n{"a":1,"b":"two"}';

test('view-only Pretty does not modify the message; forward omits raw (original bytes)', () => {
  const s = session(REQ);
  const pretty = s.viewPretty();
  assert.notEqual(pretty, s.ta, 'pretty is an indented presentation');
  assert.match(pretty, /\{\n {2}"a": 1,/);
  assert.equal(s.modified, false);
  assert.equal(s.forward, null, 'untouched message must forward the server-held original');
  assert.equal(s.ta, normalizeEOL(REQ), 'viewing never writes back to the raw editor');
});

test('edit in Pretty becomes the message to forward and marks it modified', () => {
  const s = session(REQ);
  const p = s.viewPretty().replace('"two"', '"three"');
  s.typePretty(p);
  assert.equal(s.modified, true);
  assert.equal(s.forward, p);
  assert.ok(s.forward.includes('"three"'));
});

test('raw reflects Pretty edits and Pretty keeps them after a Raw round trip', () => {
  const s = session(REQ);
  const p = s.viewPretty().replace('1', '42');
  s.typePretty(p);
  assert.equal(s.ta, p, 'Raw view shows the edited pretty text');
  assert.equal(s.viewPretty(), p, 'Raw -> Pretty does not lose or reformat the edit');
  s.typeRaw(s.ta + ' ');
  assert.equal(s.modified, true);
  assert.equal(s.viewPretty(), beautify(s.ta), 'a raw edit after a pretty edit re-derives pretty');
});

test('reset restores the original and pretty is re-derived from it', () => {
  const s = session(REQ);
  s.typePretty(s.viewPretty().replace('two', 'x'));
  s.reset();
  assert.equal(s.modified, false);
  assert.equal(s.forward, null);
  assert.equal(s.viewPretty(), beautify(normalizeEOL(REQ)));
});

test('reverting an edit by hand back to the original text clears MODIFIED', () => {
  const s = session(REQ);
  s.typeRaw(s.ta + 'x');
  assert.equal(s.modified, true);
  s.typeRaw(normalizeEOL(REQ));
  assert.equal(s.modified, false);
  assert.equal(s.forward, null);
});

test('CRLF originals are not considered modified by textarea LF normalization', () => {
  assert.equal(heldIsModified('A\nB', 'A\r\nB'), false);
  assert.equal(heldForwardRaw('A\nB', 'A\r\nB'), null);
  assert.equal(heldForwardRaw('A\nC', 'A\r\nB'), 'A\nC');
});

test('responses behave the same way', () => {
  const RESP = 'HTTP/1.1 200 OK\r\nContent-Type: application/json\r\n\r\n{"ok":true}';
  const s = session(RESP);
  assert.equal(s.forward, null);
  s.viewPretty();
  assert.equal(s.forward, null);
  s.typePretty(s.viewPretty().replace('true', 'false'));
  assert.equal(s.modified, true);
  assert.match(s.forward, /false/);
});
