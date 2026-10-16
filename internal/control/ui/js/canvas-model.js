// canvas-model.js — the pure tab/split state machine behind the multi-document
// workbench canvas (#103). No DOM and no imports, so every transition is unit
// tested under `node --test` (internal/control/ui/_js-tests/canvas-model.test.mjs)
// and it stays in the pure tier (TestUIPureModulesStayDOMFree).
//
// A "document" is one open tool surface. In this pass a document maps 1:1 to a
// singleton panel, so its id IS the panel name (`proxy`, `repeater`, …); the
// per-instance case (two Repeater documents) is deliberately deferred — see
// docs/architecture.md. The canvas holds an ordered strip of open documents and
// a layout of one or two visible panes:
//
//   state = {
//     docs:   { [panel]: { id, panel, title } },   // open documents
//     order:  [panel, …],                          // left→right tab order
//     layout: 'single' | 'split-lr' | 'split-tb',
//     panes:  [ { docId }, { docId }? ],            // 1 (single) or 2 (split)
//     focused: 0 | 1,                               // index into panes
//   }
//
// Every exported transition returns a NEW state; inputs are never mutated.

const DEFAULT_MAX_DOCS = 16;

function mkDoc(panel, title) {
  return { id: panel, panel, title: title || panel };
}

function clone(s) {
  return { docs: { ...s.docs }, order: [...s.order], layout: s.layout, panes: s.panes.map((p) => ({ ...p })), focused: s.focused };
}

// normalize repairs any invariant drift: panes must reference open docs, a
// single layout has exactly one pane, and focus stays in range.
function normalize(s) {
  const firstOpen = s.order[0];
  s.panes = s.panes.map((p) => (s.docs[p.docId] ? p : { docId: firstOpen }));
  if (s.layout === 'single' && s.panes.length > 1) s.panes = [s.panes[s.focused] || s.panes[0]];
  if (s.panes.length === 0) s.panes = [{ docId: firstOpen }];
  if (s.focused >= s.panes.length) s.focused = s.panes.length - 1;
  if (s.focused < 0) s.focused = 0;
  return s;
}

// createCanvasState starts with a single pane showing one document.
export function createCanvasState(panel = 'proxy', title) {
  const p = panel || 'proxy';
  return { docs: { [p]: mkDoc(p, title) }, order: [p], layout: 'single', panes: [{ docId: p }], focused: 0 };
}

// openDoc ensures a document exists for `panel` and focuses it: if it is already
// shown in a pane, that pane becomes focused; otherwise it is swapped into the
// focused pane. New documents are appended to the strip.
export function openDoc(state, panel, title) {
  if (!panel) return state;
  const s = clone(state);
  if (!s.docs[panel]) { s.docs[panel] = mkDoc(panel, title); s.order = [...s.order, panel]; }
  else if (title && s.docs[panel].title !== title) s.docs[panel] = { ...s.docs[panel], title };
  const at = s.panes.findIndex((p) => p.docId === panel);
  if (at >= 0) s.focused = at;
  else s.panes = s.panes.map((p, i) => (i === s.focused ? { docId: panel } : p));
  return normalize(s);
}

// focusDoc focuses an already-open document (no-op if it is not open): the pane
// showing it if any, else swapping it into the focused pane.
export function focusDoc(state, docId) {
  if (!state.docs[docId]) return state;
  const s = clone(state);
  const at = s.panes.findIndex((p) => p.docId === docId);
  if (at >= 0) s.focused = at;
  else s.panes = s.panes.map((p, i) => (i === s.focused ? { docId } : p));
  return normalize(s);
}

// focusPane moves focus to a pane by index (ignored when out of range).
export function focusPane(state, idx) {
  if (!Number.isInteger(idx) || idx < 0 || idx >= state.panes.length || idx === state.focused) return state;
  const s = clone(state);
  s.focused = idx;
  return s;
}

// splitCanvas enters a two-pane layout (or just re-orients an existing split).
// The focused document stays in pane 0; pane 1 takes the next open document, or
// `fallbackPanel` (opening it) when only one document is open.
export function splitCanvas(state, orientation = 'split-lr', fallbackPanel) {
  const ori = orientation === 'split-tb' ? 'split-tb' : 'split-lr';
  if (isSplit(state)) { const s = clone(state); s.layout = ori; return s; }
  const s = clone(state);
  const focusedDoc = s.panes[0].docId;
  let other = s.order.find((p) => p !== focusedDoc);
  if (!other) {
    const fb = fallbackPanel && fallbackPanel !== focusedDoc ? fallbackPanel : (focusedDoc === 'proxy' ? 'repeater' : 'proxy');
    if (!s.docs[fb]) { s.docs[fb] = mkDoc(fb); s.order = [...s.order, fb]; }
    other = fb;
  }
  s.layout = ori;
  s.panes = [{ docId: focusedDoc }, { docId: other }];
  s.focused = 0;
  return normalize(s);
}

// unsplit collapses back to a single pane keeping the focused document.
export function unsplit(state) {
  if (!isSplit(state)) return state;
  const s = clone(state);
  const focusedDoc = s.panes[s.focused].docId;
  s.layout = 'single';
  s.panes = [{ docId: focusedDoc }];
  s.focused = 0;
  return s;
}

// closeDoc removes a document. In a single pane the next open document is
// revealed; in a split the emptied pane shows another open document, or the
// canvas unsplits when none is left. Closing the last document re-opens
// `defaultPanel` so the canvas is never empty.
export function closeDoc(state, docId, defaultPanel = 'proxy') {
  if (!state.docs[docId]) return state;
  const s = clone(state);
  delete s.docs[docId];
  s.order = s.order.filter((p) => p !== docId);
  if (s.order.length === 0) return createCanvasState(defaultPanel || 'proxy');
  if (s.layout === 'single') {
    if (s.panes[0].docId === docId) s.panes = [{ docId: s.order[0] }];
    s.focused = 0;
    return normalize(s);
  }
  const other = (i) => (i === 0 ? 1 : 0);
  for (let i = 0; i < s.panes.length; i++) {
    if (s.panes[i].docId !== docId) continue;
    const otherDoc = s.panes[other(i)].docId;
    const cand = s.order.find((p) => p !== otherDoc);
    if (cand) { s.panes = s.panes.map((p, j) => (j === i ? { docId: cand } : p)); }
    else {
      const survivor = otherDoc && s.docs[otherDoc] ? otherDoc : s.order[0];
      return { ...s, layout: 'single', panes: [{ docId: survivor }], focused: 0 };
    }
  }
  if (s.focused >= s.panes.length) s.focused = s.panes.length - 1;
  return normalize(s);
}

// reorderDoc moves a document to a new index in the strip order (for drag or
// keyboard reordering).
export function reorderDoc(state, docId, toIndex) {
  if (!state.docs[docId]) return state;
  const s = clone(state);
  const arr = s.order.filter((p) => p !== docId);
  const idx = Math.max(0, Math.min(Number(toIndex) || 0, arr.length));
  arr.splice(idx, 0, docId);
  s.order = arr;
  return s;
}

// ---- selectors ----
export const isSplit = (s) => s.layout !== 'single' && s.panes.length > 1;
export const focusedDocId = (s) => s.panes[s.focused].docId;
export const focusedPanel = (s) => (s.docs[focusedDocId(s)] || {}).panel || focusedDocId(s);
export const paneDocIds = (s) => s.panes.map((p) => p.docId);
export function visiblePanels(s) {
  const seen = [];
  for (const p of s.panes) { const panel = (s.docs[p.docId] || {}).panel || p.docId; if (!seen.includes(panel)) seen.push(panel); }
  return seen;
}
export const docOrderPanels = (s) => [...s.order];
export const paneOrientationAxis = (s) => (s.layout === 'split-tb' ? 'vertical' : 'horizontal');

// ---- persistence ----
export function serializeCanvas(s) {
  return { v: 1, order: [...s.order], layout: s.layout, paneDocs: s.panes.map((p) => p.docId), focused: s.focused };
}

// deserializeCanvas rebuilds state from a stored envelope, dropping unknown or
// duplicate panels, bounding the open-document count, and repairing a broken
// pane or focus reference. Returns null for anything it cannot trust, so the
// caller falls back to a fresh default.
export function deserializeCanvas(raw, { validPanels = [], maxDocs = DEFAULT_MAX_DOCS, defaultPanel = 'proxy' } = {}) {
  if (!raw || typeof raw !== 'object' || raw.v !== 1 || !Array.isArray(raw.order)) return null;
  const valid = new Set(validPanels);
  const order = [];
  for (const p of raw.order) {
    if (typeof p === 'string' && valid.has(p) && !order.includes(p)) order.push(p);
    if (order.length >= maxDocs) break;
  }
  if (order.length === 0) return null;
  const docs = {};
  for (const p of order) docs[p] = mkDoc(p);
  const layout = raw.layout === 'split-lr' || raw.layout === 'split-tb' ? raw.layout : 'single';
  const paneDocs = Array.isArray(raw.paneDocs) ? raw.paneDocs.filter((p) => typeof p === 'string' && order.includes(p)) : [];
  if (layout === 'single') {
    const d = paneDocs[0] && order.includes(paneDocs[0]) ? paneDocs[0] : order[0];
    return normalize({ docs, order, layout: 'single', panes: [{ docId: d }], focused: 0 });
  }
  let a = order.includes(paneDocs[0]) ? paneDocs[0] : order[0];
  let b = order.includes(paneDocs[1]) && paneDocs[1] !== a ? paneDocs[1] : order.find((p) => p !== a);
  if (!b) return normalize({ docs, order, layout: 'single', panes: [{ docId: a }], focused: 0 });
  return normalize({ docs, order, layout, panes: [{ docId: a }, { docId: b }], focused: raw.focused === 1 ? 1 : 0 });
}
