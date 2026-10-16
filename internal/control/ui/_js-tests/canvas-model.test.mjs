// Pure tab/split state-machine tests for the multi-document workbench canvas.
// No DOM: canvas-model.js is dependency-free so it runs under `node --test`.
import test from 'node:test';
import assert from 'node:assert/strict';
import {
  createCanvasState, openDoc, focusDoc, closeDoc, splitCanvas, unsplit,
  focusPane, reorderDoc, focusedDocId, focusedPanel, visiblePanels, paneDocIds,
  isSplit, docOrderPanels, serializeCanvas, deserializeCanvas,
} from '../js/canvas-model.js';

const panels = (s) => docOrderPanels(s);

test('createCanvasState opens one document in a single pane', () => {
  const s = createCanvasState('proxy');
  assert.equal(isSplit(s), false);
  assert.deepEqual(panels(s), ['proxy']);
  assert.equal(focusedPanel(s), 'proxy');
  assert.deepEqual(visiblePanels(s), ['proxy']);
  assert.equal(s.panes.length, 1);
});

test('openDoc adds a new document and focuses it in the focused pane', () => {
  let s = createCanvasState('proxy');
  s = openDoc(s, 'repeater');
  assert.deepEqual(panels(s), ['proxy', 'repeater']);
  assert.equal(focusedPanel(s), 'repeater');
  // single pane: only the focused doc is visible
  assert.deepEqual(visiblePanels(s), ['repeater']);
});

test('openDoc on an already-open doc focuses it without duplicating', () => {
  let s = createCanvasState('proxy');
  s = openDoc(s, 'repeater');
  s = openDoc(s, 'proxy');
  assert.deepEqual(panels(s), ['proxy', 'repeater']);
  assert.equal(focusedPanel(s), 'proxy');
});

test('openDoc focuses the OTHER pane when the doc is already shown there (split)', () => {
  let s = createCanvasState('proxy');
  s = openDoc(s, 'repeater');          // focused repeater, proxy also open
  s = splitCanvas(s, 'split-lr');      // pane0=repeater(focused), pane1=proxy
  assert.equal(isSplit(s), true);
  assert.deepEqual(visiblePanels(s).sort(), ['proxy', 'repeater']);
  assert.equal(focusedPanel(s), 'repeater');
  s = openDoc(s, 'proxy');             // proxy already in pane1 -> focus pane1, don't replace pane0
  assert.equal(focusedPanel(s), 'proxy');
  assert.deepEqual(visiblePanels(s).sort(), ['proxy', 'repeater']);
});

test('splitCanvas pairs the focused doc with the next open doc', () => {
  let s = createCanvasState('proxy');
  s = openDoc(s, 'repeater');          // order [proxy, repeater], focused repeater
  s = splitCanvas(s, 'split-lr');
  assert.equal(isSplit(s), true);
  assert.deepEqual(paneDocIds(s), ['repeater', 'proxy']);
  assert.equal(focusedPanel(s), 'repeater');   // focus stays on the originally-focused doc
});

test('splitCanvas opens a fallback doc when only one is open', () => {
  let s = createCanvasState('repeater');
  s = splitCanvas(s, 'split-tb', 'proxy');
  assert.equal(isSplit(s), true);
  assert.deepEqual(visiblePanels(s).sort(), ['proxy', 'repeater']);
  assert.equal(focusedPanel(s), 'repeater');
  assert.equal(s.layout, 'split-tb');
});

test('splitCanvas while already split only changes orientation', () => {
  let s = createCanvasState('proxy');
  s = openDoc(s, 'repeater');
  s = splitCanvas(s, 'split-lr');
  const before = paneDocIds(s);
  s = splitCanvas(s, 'split-tb');
  assert.equal(s.layout, 'split-tb');
  assert.deepEqual(paneDocIds(s), before);
});

test('unsplit keeps the focused doc', () => {
  let s = createCanvasState('proxy');
  s = openDoc(s, 'repeater');
  s = splitCanvas(s, 'split-lr');      // focused repeater
  s = unsplit(s);
  assert.equal(isSplit(s), false);
  assert.equal(focusedPanel(s), 'repeater');
  assert.deepEqual(visiblePanels(s), ['repeater']);
});

test('focusPane moves focus between panes in a split', () => {
  let s = createCanvasState('proxy');
  s = openDoc(s, 'repeater');
  s = splitCanvas(s, 'split-lr');      // pane0 repeater focused, pane1 proxy
  s = focusPane(s, 1);
  assert.equal(focusedPanel(s), 'proxy');
  s = focusPane(s, 5);                 // out of range is ignored
  assert.equal(focusedPanel(s), 'proxy');
});

test('focusDoc focuses the pane showing the doc, else swaps it into the focused pane', () => {
  let s = createCanvasState('proxy');
  s = openDoc(s, 'repeater');
  s = openDoc(s, 'findings');          // order [proxy,repeater,findings], focused findings
  s = splitCanvas(s, 'split-lr');      // pane0 findings, pane1 proxy
  s = focusDoc(s, 'proxy');            // proxy shown in pane1 -> focus it
  assert.equal(focusedPanel(s), 'proxy');
  s = focusDoc(s, 'repeater');         // repeater not shown -> into focused pane (pane1)
  assert.equal(focusedPanel(s), 'repeater');
  assert.deepEqual(paneDocIds(s), ['findings', 'repeater']);
});

test('closeDoc removes the doc and reveals the next in a single pane', () => {
  let s = createCanvasState('proxy');
  s = openDoc(s, 'repeater');          // focused repeater
  s = closeDoc(s, 'repeater');
  assert.deepEqual(panels(s), ['proxy']);
  assert.equal(focusedPanel(s), 'proxy');
});

test('closeDoc re-opens the default when the last doc closes', () => {
  let s = createCanvasState('proxy');
  s = closeDoc(s, 'proxy', 'proxy');
  assert.deepEqual(panels(s), ['proxy']);
  assert.equal(focusedPanel(s), 'proxy');
});

test('closeDoc on a split pane shows another open doc, or unsplits if none left', () => {
  let s = createCanvasState('proxy');
  s = openDoc(s, 'repeater');
  s = openDoc(s, 'findings');          // [proxy,repeater,findings]
  s = splitCanvas(s, 'split-lr');      // pane0 findings(focused), pane1 proxy; repeater hidden
  s = closeDoc(s, 'findings');         // pane0 loses its doc -> shows repeater (the hidden open doc)
  assert.equal(isSplit(s), true);
  assert.deepEqual(paneDocIds(s).sort(), ['proxy', 'repeater']);
  // now close one more: only proxy+repeater remain, both shown -> closing one unsplits
  s = closeDoc(s, 'proxy');
  assert.equal(isSplit(s), false);
  assert.deepEqual(panels(s), ['repeater']);
  assert.equal(focusedPanel(s), 'repeater');
});

test('reorderDoc moves a tab within the strip order', () => {
  let s = createCanvasState('proxy');
  s = openDoc(s, 'repeater');
  s = openDoc(s, 'findings');
  s = reorderDoc(s, 'findings', 0);
  assert.deepEqual(panels(s), ['findings', 'proxy', 'repeater']);
});

test('serialize/deserialize round-trips and survives garbage', () => {
  let s = createCanvasState('proxy');
  s = openDoc(s, 'repeater');
  s = splitCanvas(s, 'split-tb');
  const raw = serializeCanvas(s);
  const back = deserializeCanvas(JSON.parse(JSON.stringify(raw)), { validPanels: ['proxy', 'repeater', 'findings'] });
  assert.equal(back.layout, 'split-tb');
  assert.deepEqual(paneDocIds(back).sort(), paneDocIds(s).sort());
  assert.equal(focusedPanel(back), focusedPanel(s));
  // garbage -> null (caller falls back to a default)
  assert.equal(deserializeCanvas(null, { validPanels: ['proxy'] }), null);
  assert.equal(deserializeCanvas({ v: 99 }, { validPanels: ['proxy'] }), null);
  assert.equal(deserializeCanvas('nope', { validPanels: ['proxy'] }), null);
});

test('deserialize drops unknown panels and clamps a broken focus', () => {
  const raw = { v: 1, order: ['proxy', 'ghost', 'repeater', 'proxy'], layout: 'split-lr', paneDocs: ['ghost', 'repeater'], focused: 9 };
  const s = deserializeCanvas(raw, { validPanels: ['proxy', 'repeater'] });
  assert.ok(s);
  assert.deepEqual(panels(s), ['proxy', 'repeater']);     // ghost dropped, dupes removed
  // paneDocs referenced ghost (dropped) -> that pane falls back to an open doc; focus clamped in range
  assert.ok(s.focused >= 0 && s.focused < s.panes.length);
  for (const id of paneDocIds(s)) assert.ok(panels(s).includes(id));
});

test('deserialize bounds the number of open docs', () => {
  const many = Array.from({ length: 50 }, (_, i) => 'p' + i);
  const valid = many.slice();
  const raw = { v: 1, order: many, layout: 'single', paneDocs: [many[0]], focused: 0 };
  const s = deserializeCanvas(raw, { validPanels: valid, maxDocs: 12 });
  assert.ok(s);
  assert.ok(panels(s).length <= 12);
});
