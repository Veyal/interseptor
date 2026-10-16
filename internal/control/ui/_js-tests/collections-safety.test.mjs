import test from 'node:test';
import assert from 'node:assert/strict';
import {
  UNDO_WINDOW_MS, MAX_UNDO_REQUESTS, orderForRestore, restoreBody, needsScriptStrip, undoable,
  rememberSentOver, sentOverFor, forgetSentOver, undoMessage,
} from '../js/collections-safety.js';

const f = (uid, parentUid = '', rank = 'a') => ({ uid, parentUid, kind: 'folder', rank, name: uid });
const r = (uid, parentUid = '', rank = 'a', extra = {}) => ({ uid, parentUid, kind: 'request', rank, name: uid, rev: 3, ts: 9, collectionUid: 'c1', ...extra });

test('orderForRestore puts parents before children, root first', () => {
  const items = [r('r2', 'f2'), f('f2', 'f1'), r('r1', 'f1'), f('f1')];
  const out = orderForRestore(items, 'f1').map((i) => i.uid);
  assert.equal(out[0], 'f1');
  assert.ok(out.indexOf('f2') < out.indexOf('r2'));
  assert.ok(out.indexOf('f1') < out.indexOf('r1'));
  assert.equal(out.length, 4);
});

test('orderForRestore of a single request is that request', () => {
  assert.deepEqual(orderForRestore([r('a')], 'a').map((i) => i.uid), ['a']);
});

test('restoreBody drops identity fields, keeps rank and maps the parent', () => {
  const b = restoreBody(r('x', 'old', 'm', { url: 'https://example.com' }), 'new', false);
  assert.equal(b.parentUid, 'new');
  assert.equal(b.rank, 'm');
  assert.equal(b.url, 'https://example.com');
  for (const k of ['uid', 'rev', 'ts', 'collectionUid']) assert.ok(!(k in b), k);
});

test('restoreBody with strip removes events but nothing else', () => {
  const b = restoreBody(r('x', '', 'a', { events: [{ listen: 'test' }], headers: [1] }), '', true);
  assert.ok(!('events' in b));
  assert.deepEqual(b.headers, [1]);
});

test('needsScriptStrip: no events never strips; unknown trust strips; untrusted owner strips', () => {
  assert.equal(needsScriptStrip(r('a'), null), false);
  const withEv = r('a', '', 'a', { events: [{}] });
  assert.equal(needsScriptStrip(withEv, null), true);
  assert.equal(needsScriptStrip(withEv, { scripts: [{ owners: ['a'], trusted: true }] }), false);
  assert.equal(needsScriptStrip(withEv, { scripts: [{ owners: ['a'], trusted: false }] }), true);
  assert.equal(needsScriptStrip(withEv, { scripts: [{ owners: ['zzz'], trusted: false }] }), false);
});

test('undoable is bounded by request count', () => {
  assert.equal(undoable([r('a')]), true);
  assert.equal(undoable(Array.from({ length: MAX_UNDO_REQUESTS + 1 }, (_, i) => r('r' + i))), false);
  assert.equal(undoable(Array.from({ length: 500 }, (_, i) => f('f' + i))), true);
});

test('sent-over memory keeps the oldest stored version per uid and is capped', () => {
  const m = new Map();
  rememberSentOver(m, 'a', { rev: 1, name: 'first' });
  rememberSentOver(m, 'a', { rev: 2, name: 'second' });
  assert.equal(sentOverFor(m, 'a').name, 'first');
  for (let i = 0; i < 30; i++) rememberSentOver(m, 'u' + i, { rev: i });
  assert.ok(m.size <= 20);
  forgetSentOver(m, 'u29');
  assert.equal(sentOverFor(m, 'u29'), null);
});

test('undoMessage names the item and counts descendants', () => {
  assert.equal(UNDO_WINDOW_MS, 15000);
  assert.match(undoMessage('login', 0), /Deleted "login"/);
  assert.match(undoMessage('Auth', 3), /and 3 items/);
});
