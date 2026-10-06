import test from 'node:test';
import assert from 'node:assert/strict';
import { trayTiles, moveEvidence, removeEvidence, restoreEvidence, announceMove, announceRemove, keyMoveDir, evidenceTrayHTML, isEvidenceBlock } from '../js/evidence-tray.js';

const blocks = () => [
  { type: 'text', md: 'step 1' },
  { type: 'flow', flowId: 11, method: 'GET', host: 'app.example.com', path: '/api/a', status: 200, role: 'action' },
  { type: 'text', md: 'step 2' },
  { type: 'image', hash: 'abc', mime: 'image/png', caption: 'Login page', role: 'result' },
  { type: 'flow', flowId: 12, missing: true },
];

test('trayTiles lists evidence blocks only, with block index and position', () => {
  const t = trayTiles(blocks());
  assert.deepEqual(t.map((x) => [x.index, x.kind, x.position, x.total]), [[1, 'flow', 1, 3], [3, 'shot', 2, 3], [4, 'flow', 3, 3]]);
  assert.equal(t[0].title, 'GET app.example.com/api/a');
  assert.equal(t[1].thumb, '/api/findings/images/abc');
  assert.equal(t[2].missing, true);
  assert.equal(t[2].thumb, '');
  assert.deepEqual(trayTiles(null), []);
});

test('moveEvidence swaps with the neighbouring evidence block and keeps text in place', () => {
  const r = moveEvidence(blocks(), 1, 1);
  assert.equal(r.index, 3);
  assert.deepEqual(r.blocks.map((b) => b.type), ['text', 'image', 'text', 'flow', 'flow']);
  assert.equal(r.blocks[3].flowId, 11);
});

test('moveEvidence refuses edges, text blocks and bad directions', () => {
  assert.equal(moveEvidence(blocks(), 1, -1), null);
  assert.equal(moveEvidence(blocks(), 4, 1), null);
  assert.equal(moveEvidence(blocks(), 0, 1), null);
  assert.equal(moveEvidence(blocks(), 1, 2), null);
  assert.equal(moveEvidence(null, 0, 1), null);
});

test('moveEvidence does not mutate its input', () => {
  const b = blocks();
  moveEvidence(b, 1, 1);
  assert.equal(b[1].flowId, 11);
});

test('remove then restore round-trips order', () => {
  const b = blocks();
  const r = removeEvidence(b, 3);
  assert.equal(r.blocks.length, 4);
  assert.equal(r.removed.caption, 'Login page');
  assert.deepEqual(restoreEvidence(r.blocks, r.removed, r.index), b);
  assert.equal(removeEvidence(b, 0), null);
});

test('announcements name the kind, title and new position', () => {
  const [flow, shot] = trayTiles(blocks());
  assert.equal(announceMove(flow, 2, 3), 'Flow GET app.example.com/api/a moved to position 2 of 3');
  assert.equal(announceRemove(shot), 'Screenshot Login page removed');
});

test('keyMoveDir needs Alt and no other modifier', () => {
  assert.equal(keyMoveDir({ altKey: true, key: 'ArrowUp' }), -1);
  assert.equal(keyMoveDir({ altKey: true, key: 'ArrowRight' }), 1);
  assert.equal(keyMoveDir({ altKey: false, key: 'ArrowUp' }), 0);
  assert.equal(keyMoveDir({ altKey: true, ctrlKey: true, key: 'ArrowUp' }), 0);
  assert.equal(keyMoveDir({ altKey: true, key: 'x' }), 0);
});

test('html: drop zone, live region, escaped titles, move buttons only when editable', () => {
  const evil = [{ type: 'flow', flowId: 1, method: 'GET', host: '<b>', path: '/' }];
  const view = evidenceTrayHTML(evil, { editable: false, findingId: 5 });
  assert.match(view, /data-evidence-drop/);
  assert.match(view, /data-finding-id="5"/);
  assert.match(view, /role="status" aria-live="polite"/);
  assert.ok(!view.includes('<b>'));
  assert.ok(!view.includes('data-et-move'));
  const edit = evidenceTrayHTML(blocks(), { editable: true, findingId: 5 });
  assert.match(edit, /data-et-move="-1"[^>]*disabled/);
  assert.match(edit, /data-et-remove/);
  assert.match(evidenceTrayHTML([], { editable: true }), /No evidence attached/);
  assert.equal(isEvidenceBlock({ type: 'text' }), false);
});
