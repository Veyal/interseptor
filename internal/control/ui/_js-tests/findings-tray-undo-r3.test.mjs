// findings.js evidence tray: the Undo toast of a removed tile outlives the editor
// it came from (five seconds, longer while hovered). After the operator opens
// another finding, Undo must not write that other finding's blocks onto the
// finding the tile was removed from. The REAL renderEvidenceTray is cut from
// findings.js; its collaborators are stubs.
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

const src = readFileSync(new URL('../js/findings.js', import.meta.url), 'utf8');
const a = src.indexOf('function renderEvidenceTray(fid, editable) {');
const b = src.indexOf('function scheduleSave(fid) {', a);
assert.ok(a >= 0 && b > a, 'renderEvidenceTray not found');

function harness() {
  const log = { saves: [], renders: [], toasts: [], wired: null };
  const host = { innerHTML: '', querySelector: () => ({}) };
  const make = new Function('$', 'evidenceTrayHTML', 'wireEvidenceTray', 'renderFindBody', 'scheduleSave', 'toast', `
    let bodyBlocks = [], bodyFindingId = null;
    ${src.slice(a, b)}
    return { renderEvidenceTray, open(id, blocks) { bodyFindingId = id; bodyBlocks = blocks; }, blocks: () => bodyBlocks };`);
  const api = make(
    () => host, () => '<div class="et"></div>', (root, cb) => { log.wired = cb; },
    (fid) => log.renders.push(fid), (fid) => log.saves.push(fid), (message, kind) => log.toasts.push({ message, kind }));
  return { log, ...api };
}
const flow = (id) => ({ type: 'flow', flowId: id });

test('tray edits of the open finding still save to it', () => {
  const h = harness();
  h.open(1, [flow(10), flow(11)]);
  h.renderEvidenceTray(1, true);
  h.log.wired.setBlocks([flow(11)], 'remove');
  assert.deepEqual(h.blocks(), [flow(11)]);
  assert.deepEqual(h.log.saves, [1]);
});

test('Undo after another finding was opened writes nothing and says why', () => {
  const h = harness();
  h.open(1, [flow(10), flow(11)]);
  h.renderEvidenceTray(1, true);
  const tray = h.log.wired;
  tray.setBlocks([flow(11)], 'remove');           // tile removed on finding 1: Undo toast is up
  h.log.saves.length = 0; h.log.renders.length = 0;
  const other = [{ type: 'text', md: 'steps of finding 2' }, flow(99)];
  h.open(2, other);                               // operator opens finding 2
  // What the Undo button runs: restoreEvidence(getBlocks(), removed, index) -> setBlocks.
  tray.setBlocks([flow(10), ...tray.getBlocks()], 'undo');
  assert.deepEqual(h.log.saves, [], 'a save of finding 1 was scheduled from finding 2\'s blocks');
  assert.deepEqual(h.log.renders, [], 'finding 1\'s editor was rendered into finding 2');
  assert.equal(h.blocks(), other, 'finding 2\'s blocks were replaced');
  assert.equal(h.log.toasts.length, 1);
  assert.match(h.log.toasts[0].message, /#1/);
});
