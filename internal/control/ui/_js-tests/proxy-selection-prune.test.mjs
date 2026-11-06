// History filter change must prune the selection it hides.
//
// state.selected is a Set of flow ids, and nothing used to reconcile it with the
// filters: after narrowing History, ids of flows that were no longer visible
// stayed selected, so the bulk bar offered Delete / Tag / Repeater for rows the
// operator could not see. proxy.js now prunes through pruneSelection (the same
// predicate the rows use, flowMatchesFilters) — once as the reload starts and once
// when the replacement page commits. pruneFlowSelection is sliced out of proxy.js
// and driven against stubs, the way windowed-list.test.mjs slices core.js.
import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import { pruneSelection } from '../js/proxy-selection.js';

const proxySrc = fs.readFileSync(new URL('../js/proxy.js', import.meta.url), 'utf8');

function sliceFunction(src, signature) {
  const start = src.indexOf(signature);
  assert.ok(start >= 0, 'proxy.js has ' + signature);
  let depth = 0;
  for (let i = src.indexOf('{', start); i < src.length; i++) {
    if (src[i] === '{') depth++;
    else if (src[i] === '}' && --depth === 0) return src.slice(start, i + 1);
  }
  throw new Error('unbalanced ' + signature);
}

// A slice of History's filter state, enough for the real flowMatchesFilters
// semantics that matter here: a host filter and a notes-only toggle.
function makePruner({ selected = [], flows = [], selId = null, anchorId = null } = {}) {
  return new Function('pruneSelection', `
const state={selected:new Set(${JSON.stringify(selected)}),flows:[],selId:${selId === null ? 'null' : selId},selAnchorId:${anchorId === null ? 'null' : anchorId},lastSelIdx:3};
const scopeLoaded=true;
const hostFilter='a.example.com';
const notesOnly=false;
const flowMatchesFilters=(f)=>(!hostFilter||f.host===hostFilter)&&(!notesOnly||!!f.note);
let barUpdates=0;
const updateSelBar=()=>{barUpdates++;};
state.flows=${JSON.stringify(flows)};
${sliceFunction(proxySrc, 'function pruneFlowSelection(')}
return {
  state,
  prune:(outgoing)=>pruneFlowSelection(outgoing),
  barUpdates:()=>barUpdates,
  flows(v){state.flows=v;},
  reset(){},
};
`)(pruneSelection);
}

test('a filter change drops the selected ids it hides, keeping visible ones', () => {
  const p = makePruner({
    selected: [1, 2, 3],
    flows: [
      { id: 1, host: 'a.example.com' },
      { id: 2, host: 'b.example.com' },
      { id: 3, host: 'a.example.com' },
    ],
  });
  const dropped = p.prune();
  assert.equal(dropped, 1);
  assert.deepEqual([...p.state.selected], [1, 3], 'the hidden flow loses its bulk actions');
  assert.equal(p.barUpdates(), 1, 'the bulk bar and its count are repainted');
});

test('nothing changes when every selected flow still matches', () => {
  const p = makePruner({
    selected: [1],
    anchorId: 1,
    flows: [{ id: 1, host: 'a.example.com' }],
  });
  assert.equal(p.prune(), 0);
  assert.deepEqual([...p.state.selected], [1]);
  assert.equal(p.state.selAnchorId, 1, 'a surviving anchor keeps range selection intact');
  assert.equal(p.barUpdates(), 0, 'no needless repaint');
});

test('the commit pass also judges the page cache the reload replaced', () => {
  // #9 was paged in and is now filtered out; it is gone from the new page, so only
  // the outgoing cache can refute it.
  const p = makePruner({
    selected: [9],
    flows: [{ id: 5, host: 'a.example.com' }],
  });
  assert.equal(p.prune(), 0, 'no object to judge: a paged-out id keeps its place');
  const dropped = p.prune([{ id: 9, host: 'b.example.com' }]);
  assert.equal(dropped, 1);
  assert.equal(p.state.selected.size, 0);
});

test('pruning everything clears the anchor and the keyboard cursor', () => {
  const p = makePruner({
    selected: [2],
    anchorId: 2,
    flows: [{ id: 2, host: 'b.example.com' }],
  });
  assert.equal(p.prune(), 1);
  assert.equal(p.state.selected.size, 0);
  assert.equal(p.state.selAnchorId, null, 'a pruned anchor would make the next Shift-click select a ghost range');
  assert.equal(p.state.lastSelIdx, -1);
  assert.equal(p.barUpdates(), 1, 'the bulk bar hides itself');
});

test('an anchor that was never part of the selection keeps working', () => {
  // A plain click anchors a row without selecting it; pruning a *different* id must
  // not drop that anchor, or the next Shift-click would start from the wrong row.
  const p = makePruner({
    selected: [2, 3],
    anchorId: 1,
    flows: [
      { id: 1, host: 'a.example.com' },
      { id: 2, host: 'b.example.com' },
      { id: 3, host: 'a.example.com' },
    ],
  });
  assert.equal(p.prune(), 1);
  assert.deepEqual([...p.state.selected], [3]);
  assert.equal(p.state.selAnchorId, 1);
});

test('an empty selection never touches the bar', () => {
  const p = makePruner({ selected: [], flows: [{ id: 1, host: 'b.example.com' }] });
  assert.equal(p.prune(), 0);
  assert.equal(p.barUpdates(), 0);
});

test('loadFlows prunes on the filter change and again on the commit', () => {
  const start = proxySrc.indexOf('export async function loadFlows(){');
  assert.ok(start >= 0);
  const end = proxySrc.indexOf('export async function loadMoreFlows(', start);
  const body = proxySrc.slice(start, end);
  assert.ok(body.includes('pruneFlowSelection();'), 'pruned as the reload starts');
  assert.ok(body.includes('pruneFlowSelection(outgoingFlows);'), 'pruned again when the page commits');
  const beforeFetch = body.indexOf('pruneFlowSelection();');
  const fetch = body.indexOf("await api('/api/flows?'");
  assert.ok(beforeFetch >= 0 && beforeFetch < fetch,
    'the bar is right before the response lands, not only after it');
  const commit = body.indexOf('pruneFlowSelection(outgoingFlows);');
  const storeSwap = body.indexOf('loadFlowStore(flowStore,flows);');
  assert.ok(storeSwap >= 0 && storeSwap < commit, 'the outgoing cache is snapshotted before it is replaced');
  assert.ok(commit < body.indexOf('renderRows();'), 'the pruned selection is what gets rendered');
  assert.ok(body.includes('new Map(flowStore.byId)'), 'the cache is snapshotted, not read after the swap');
});

test('the prune uses the rows’ own predicate, and "select all shown" is untouched', () => {
  const prune = sliceFunction(proxySrc, 'function pruneFlowSelection(');
  assert.ok(prune.includes('pruneSelection(previous,flows,flowMatchesFilters)'),
    'rows and the bulk bar must agree on one predicate');
  assert.ok(!prune.includes('toggleAllIds('), 'prune must not redefine select-all');
  const toggle = sliceFunction(proxySrc, 'export function toggleSelectAllShown(){');
  assert.ok(toggle.includes('toggleAllIds(state.selected,state.flows.map(f=>f.id))'),
    'Select all shown keeps selecting the loaded, filtered id list');
});
