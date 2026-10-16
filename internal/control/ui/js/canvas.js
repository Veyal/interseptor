// canvas.js — DOM controller for the multi-document workbench canvas (#103).
// The pure tab/split transitions live in canvas-model.js; this module reflects
// a model state onto the DOM: it lays out one or two panes (#canvas), MOVES the
// singleton tool panels into the visible panes (parking the rest in a hidden pool) and mirrors focus onto the
// rail (the rail is the only navigation surface; there is no document tab strip).
// The split/close controls (#paneActions) are moved into the focused pane.
// Keeping `.panel.active` == "mounted in a visible pane" means the existing
// panel-gated SSE paths (proxy.handleFlowNew, onPanelUpdate, …) keep working with
// two panels visible: a visible panel still renders live events, a parked one
// still pauses. The focused panel additionally carries `.is-focused`, which
// activePanel()/keyboard shortcuts read.
import { $, $$, projectStorageKey } from './core.js';
import * as M from './canvas-model.js';
import { clampSplitSize, splitBounds, toPercent, fromPercent, splitKeyAction } from './layout-math.js';
import { transitionView, prefersReducedMotion } from './motion.js';

const SPLIT_MIN = [240, 240];
const SASH = 6;
const MAX_DOCS = 16;

let state = null;
let splitPct = 50;
let lastFocused = null;
let opts = {};
let els = {};
let narrowMq = null;
let drag = null;
let built = false;

const allPanels = () => $$('.panel[data-panel]').map((p) => p.dataset.panel);
const panelEl = (panel) => document.querySelector('.panel[data-panel="' + panel + '"]');
const effLayout = () => (narrowMq && narrowMq.matches ? 'single' : state.layout);
const effSplit = () => effLayout() !== 'single';
const visibleEff = () => (effSplit() ? M.visiblePanels(state) : [M.focusedPanel(state)]);

// ---- persistence (guarded) ----
function persist() {
  try {
    localStorage.setItem(projectStorageKey('canvasLayout'), JSON.stringify({ ...M.serializeCanvas(state), splitPct: Math.round(splitPct * 10) / 10 }));
  } catch (e) { /* not persisted */ }
}
function restore() {
  try {
    const raw = JSON.parse(localStorage.getItem(projectStorageKey('canvasLayout')));
    const st = M.deserializeCanvas(raw, { validPanels: allPanels(), maxDocs: MAX_DOCS });
    if (st) {
      state = st;
      if (raw && typeof raw.splitPct === 'number' && raw.splitPct >= 5 && raw.splitPct <= 95) splitPct = raw.splitPct;
      return true;
    }
  } catch (e) { /* fall back to default */ }
  return false;
}

// ---- DOM layout ----
function placeInPane(pane, panel) {
  const el = panelEl(panel);
  if (!el) return;
  if (el.parentElement !== pane) pane.appendChild(el);
  el.classList.add('active');
  el.removeAttribute('inert');
}
function mountPanels() {
  const vis = visibleEff();
  els.canvas.dataset.layout = effLayout();
  placeInPane(els.pane0, vis[0]);
  if (effSplit() && vis[1]) placeInPane(els.pane1, vis[1]);
  for (const panel of allPanels()) {
    if (vis.includes(panel)) continue;
    const el = panelEl(panel);
    if (el) { el.classList.remove('active', 'is-focused'); if (el.parentElement !== els.pool) els.pool.appendChild(el); }
  }
  els.sash.hidden = !effSplit();
  els.sash.setAttribute('aria-orientation', effLayout() === 'split-tb' ? 'horizontal' : 'vertical');
  applySplitSize();
}
function reflect() {
  const focused = M.focusedPanel(state);
  for (const p of $$('.panel[data-panel]')) p.classList.toggle('is-focused', p.dataset.panel === focused && p.classList.contains('active'));
  for (const t of $$('#tabs .tab')) {
    const on = t.dataset.tab === focused;
    t.classList.toggle('active', on);
    t.setAttribute('aria-selected', on ? 'true' : 'false');
    t.tabIndex = on ? 0 : -1;
  }
  if (els.pane0) els.pane0.dataset.focused = (!effSplit() || state.focused === 0) ? 'true' : 'false';
  if (els.pane1) els.pane1.dataset.focused = (effSplit() && state.focused === 1) ? 'true' : 'false';
  placeActions();
  syncSplitButtons();
}
// The action cluster floats over the focused pane (pane 0 when not split).
function placeActions() {
  if (!els.actions) return;
  const pane = effSplit() && state.focused === 1 ? els.pane1 : els.pane0;
  if (pane && pane.lastElementChild !== els.actions) {
    // Keep the cluster last in the pane (after the panel in tab order) without
    // dropping focus if one of its buttons currently has it.
    const held = els.actions.contains(document.activeElement) ? document.activeElement : null;
    pane.appendChild(els.actions);
    if (held && document.activeElement !== held) held.focus();
  }
  // A document can be closed while more than one is open (the rail reopens any tool).
  if (els.close) els.close.hidden = M.docOrderPanels(state).length < 2;
}
function syncSplitButtons() {
  if (els.splitLr) els.splitLr.setAttribute('aria-pressed', effLayout() === 'split-lr' ? 'true' : 'false');
  if (els.splitTb) els.splitTb.setAttribute('aria-pressed', effLayout() === 'split-tb' ? 'true' : 'false');
}

// ---- split sizing ----
function splitTotal() {
  return effLayout() === 'split-tb' ? els.canvas.clientHeight : els.canvas.clientWidth;
}
function applySplitSize() {
  if (!effSplit()) { els.canvas.style.removeProperty('--canvas-split'); return; }
  const total = splitTotal();
  if (!total) { els.canvas.style.setProperty('--canvas-split', splitPct + '%'); return; }
  const px = clampSplitSize(fromPercent(splitPct, total), total, SPLIT_MIN, SASH);
  els.canvas.style.setProperty('--canvas-split', px + 'px');
  const b = splitBounds(total, SPLIT_MIN, SASH);
  els.sash.setAttribute('aria-valuenow', String(Math.round(toPercent(px, total))));
  els.sash.setAttribute('aria-valuemin', String(Math.round(toPercent(b.min, total))));
  els.sash.setAttribute('aria-valuemax', String(Math.round(toPercent(b.max, total))));
}
function setSplitPx(px) {
  const total = splitTotal();
  if (!total) return;
  splitPct = toPercent(clampSplitSize(px, total, SPLIT_MIN, SASH), total);
  applySplitSize();
}

// ---- commit: apply a new model state to the DOM + fire callbacks ----
function commit(next, { structural = false, explicit = false } = {}) {
  const prevFocused = lastFocused;
  const prevVisible = state ? visibleEff() : [];
  state = next;
  const newFocused = M.focusedPanel(state);
  const run = () => { if (structural) mountPanels(); reflect(); };
  if (!prefersReducedMotion() && prevFocused && prevFocused !== newFocused) {
    setMotionDir(prevFocused, newFocused);
    const tr = transitionView(run);
    if (tr) tr.finished.catch(() => {}).finally(clearMotionDir); else clearMotionDir();
  } else run();
  persist();
  const newVisible = visibleEff();
  for (const p of newVisible) if (p !== newFocused && !prevVisible.includes(p)) opts.onDocShown && opts.onDocShown(p);
  if (prevFocused !== null && (explicit || prevFocused !== newFocused)) opts.onFocusedChange && opts.onFocusedChange(newFocused, prevFocused);
  lastFocused = newFocused;
}
function setMotionDir(prev, next) {
  const order = allPanels();
  const dir = order.indexOf(next) < order.indexOf(prev) ? 'back' : 'forward';
  document.documentElement.dataset.motionDirection = dir;
}
function clearMotionDir() { delete document.documentElement.dataset.motionDirection; }

// ---- public operations ----
export function canvasOpenDoc(panel, { title } = {}) {
  if (!state || !panel) return;
  commit(M.openDoc(state, panel, title), { structural: true, explicit: true });
}
export function canvasFocusDoc(panel) {
  if (!state) return;
  commit(M.focusDoc(state, panel), { structural: true, explicit: true });
}
export function canvasCloseDoc(panel) {
  if (!state) return;
  commit(M.closeDoc(state, panel, opts.defaultPanel || 'proxy'), { structural: true, explicit: false });
}
function fallbackPane() {
  const focused = M.focusedPanel(state);
  return focused === 'proxy' ? 'repeater' : 'proxy';
}
export function canvasSplit(orientation) {
  if (!state) return;
  if (narrowMq && narrowMq.matches) return; // no split on phones
  commit(M.splitCanvas(state, orientation, fallbackPane()), { structural: true });
}
export function canvasUnsplit() {
  if (!state) return;
  commit(M.unsplit(state), { structural: true });
}
function focusPaneIdx(idx) {
  if (!state) return;
  commit(M.focusPane(state, idx), { structural: false, explicit: false });
}

// ---- selectors used by the shell ----
export const canvasFocusedPanel = () => (state ? M.focusedPanel(state) : '');
export const canvasVisiblePanels = () => (state ? visibleEff() : []);
export const canvasIsVisible = (name) => canvasVisiblePanels().includes(name);
export const canvasIsSplit = () => (state ? effSplit() : false);

// ---- event wiring ----
function wireSplitButtons() {
  if (els.splitLr) els.splitLr.onclick = () => (effLayout() === 'split-lr' ? canvasUnsplit() : canvasSplit('split-lr'));
  if (els.splitTb) els.splitTb.onclick = () => (effLayout() === 'split-tb' ? canvasUnsplit() : canvasSplit('split-tb'));
  // Closes the document in the focused pane; the next open document is revealed.
  if (els.close) els.close.onclick = () => canvasCloseDoc(M.focusedDocId(state));
}
function wirePaneFocus() {
  const focusFromEvent = (e) => {
    const pane = e.target.closest && e.target.closest('.canvas-pane');
    if (!pane || !els.canvas.contains(pane)) return;
    const idx = Number(pane.dataset.pane);
    if (effSplit() && idx !== state.focused) focusPaneIdx(idx);
  };
  els.canvas.addEventListener('focusin', focusFromEvent);
  els.canvas.addEventListener('pointerdown', focusFromEvent, true);
}
function wireSash() {
  els.sash.addEventListener('pointerdown', (e) => {
    if (!effSplit() || e.button > 0) return;
    e.preventDefault();
    try { els.sash.setPointerCapture(e.pointerId); } catch (err) { /* synthetic */ }
    const horizontal = effLayout() === 'split-lr';
    const total = splitTotal();
    drag = { pos: horizontal ? e.clientX : e.clientY, startPx: fromPercent(splitPct, total), horizontal };
    els.canvas.classList.add('is-resizing');
  });
  els.sash.addEventListener('pointermove', (e) => {
    if (!drag) return;
    const pos = drag.horizontal ? e.clientX : e.clientY;
    setSplitPx(drag.startPx + (pos - drag.pos));
  });
  const end = (e) => {
    if (!drag) return;
    drag = null;
    try { els.sash.releasePointerCapture(e.pointerId); } catch (err) { /* already released */ }
    els.canvas.classList.remove('is-resizing');
    persist();
  };
  els.sash.addEventListener('pointerup', end);
  els.sash.addEventListener('pointercancel', end);
  els.sash.addEventListener('dblclick', () => { splitPct = 50; applySplitSize(); persist(); });
  els.sash.addEventListener('keydown', (e) => {
    if (!effSplit()) return;
    const act = splitKeyAction(e.key, { orientation: effLayout() === 'split-tb' ? 'bottom' : 'right', shift: e.shiftKey });
    if (!act) return;
    e.preventDefault();
    const total = splitTotal();
    const b = splitBounds(total, SPLIT_MIN, SASH);
    if (act.to) setSplitPx(act.to === 'min' ? b.min : b.max);
    else setSplitPx(fromPercent(splitPct, total) + act.delta);
    persist();
  });
}

// ---- init ----
export function initCanvas(options = {}) {
  opts = options || {};
  els = {
    main: $('#main'), canvas: $('#canvas'),
    pane0: document.querySelector('.canvas-pane[data-pane="0"]'),
    pane1: document.querySelector('.canvas-pane[data-pane="1"]'),
    sash: $('#canvasSash'), pool: $('#canvasPool'),
    splitLr: $('#splitLrBtn'), splitTb: $('#splitTbBtn'),
    actions: $('#paneActions'), close: $('#paneCloseBtn'),
  };
  if (!els.canvas || !els.pane0) return false;
  if (!restore()) state = M.createCanvasState(opts.initialPanel || 'proxy');
  if (!built) {
    wireSplitButtons();
    wirePaneFocus();
    wireSash();
    narrowMq = window.matchMedia('(max-width:720px)');
    const onNarrow = () => commit(state, { structural: true });
    if (narrowMq.addEventListener) narrowMq.addEventListener('change', onNarrow); else narrowMq.addListener(onNarrow);
    if (typeof window.ResizeObserver === 'function') new window.ResizeObserver(() => { if (effSplit()) applySplitSize(); }).observe(els.canvas);
    built = true;
  }
  // Initial commit is silent: lastFocused is null, so no focus callback fires
  // (matches the pre-canvas "no tabchange emitted for the default tab at boot").
  commit(state, { structural: true });
  return true;
}
