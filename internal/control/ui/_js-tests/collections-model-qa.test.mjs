import test from 'node:test';
import assert from 'node:assert/strict';
import { itemToEditor, editorToItem, syncParamsFromURL, openGuard, isOpenItem } from '../js/collections-model.js';

// A request imported from Postman: the params carry a description, which the
// editor sheet has no field for and therefore can never re-type.
const IMPORTED = {
  uid: 'r1', rev: 1, kind: 'request', name: 'list', method: 'GET',
  url: 'https://api.example.com/users?page=1&limit=20',
  params: [
    { key: 'page', value: '1', description: '1-based page number' },
    { key: 'limit', value: '20', description: 'page size' },
    { key: 'sort', value: 'asc', disabled: true, description: 'kept disabled' },
  ],
};

test('editing the URL text keeps the descriptions the imported params carried', () => {
  const ed = itemToEditor(IMPORTED);
  assert.equal(ed.params[0].description, '1-based page number');
  // Typing in the URL field re-derives the enabled rows from the URL text.
  ed.url = 'https://api.example.com/users?page=2&limit=20';
  ed.params = syncParamsFromURL(ed.url, ed.params);
  const saved = editorToItem(ed, IMPORTED);
  const byKey = Object.fromEntries(saved.params.map((p) => [p.key, p]));
  assert.equal(byKey.page.value, '2', 'the edit itself still lands');
  assert.equal(byKey.page.description, '1-based page number', 'description survives a URL edit');
  assert.equal(byKey.limit.description, 'page size');
  assert.equal(byKey.sort.description, 'kept disabled', 'a disabled row keeps its own description');
  assert.equal(byKey.sort.disabled, true);
});

test('a param added through the URL text is new and carries nothing stale', () => {
  const ed = itemToEditor(IMPORTED);
  ed.url = 'https://api.example.com/users?page=1&limit=20&extra=1';
  ed.params = syncParamsFromURL(ed.url, ed.params);
  const extra = ed.params.find((p) => p.key === 'extra');
  assert.equal(extra.value, '1');
  assert.equal(extra.description, undefined);
});

test('syncParamsFromURL still puts the disabled rows after the live ones', () => {
  const prev = [{ key: 'a', value: '1', description: 'd' }, { key: 'off', value: 'x', disabled: true, description: 'kept' }];
  const out = syncParamsFromURL('https://e.com/p?q=2', prev);
  assert.deepEqual(out.map((p) => [p.key, !!p.disabled]), [['q', false], ['off', true]]);
  assert.equal(out[0].description, undefined);
});

test('openGuard: re-opening the open request keeps unsaved edits', () => {
  // Clicking the tree row of the request that is already open is not a
  // navigation; reloading it used to discard the edits with no dialog.
  assert.equal(openGuard({ dirty: true, openUid: 'r1', nextUid: 'r1' }), 'keep');
  assert.equal(openGuard({ dirty: true, openUid: 'r1', nextUid: 'r2' }), 'confirm');
  assert.equal(openGuard({ dirty: false, openUid: 'r1', nextUid: 'r2' }), 'load');
  assert.equal(openGuard({ dirty: false, openUid: 'r1', nextUid: 'r1' }), 'load');
  assert.equal(openGuard({ dirty: true, openUid: '', nextUid: 'r1' }), 'load', 'no open editor means nothing to lose');
  assert.equal(openGuard({}), 'load');
});

test('isOpenItem gates an action that follows opening a request', () => {
  // The tree's Send menu opens a request and then sends. When the discard prompt
  // is cancelled the other request stays open, and Send must not fire it.
  assert.equal(isOpenItem({ uid: 'r1' }, 'r1'), true);
  assert.equal(isOpenItem({ uid: 'r1' }, 'r2'), false, 'a cancelled navigation leaves another request open');
  assert.equal(isOpenItem(null, 'r1'), false);
  assert.equal(isOpenItem({ uid: 'r1' }, ''), false);
});
