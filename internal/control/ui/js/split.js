// split.js — SplitPane: a resizable list | detail layout shared by every panel
// that has one. Sash is a keyboard-operable separator; below the stack
// breakpoint the detail becomes a full-panel view with a Back button and a
// single guarded history entry. The module has no static imports (geometry lives
// in layout-math.js); callers pass `scopeKey` (core's projectStorageKey) so the
// persisted size is per panel and per project.
//
// Markup: <div class="split"><div class="split-list">…</div><div class="split-detail">…</div></div>
// (a .split-sash is created when absent). Old class names such as icpt-queue,
// rep-hist, find-browser, settings-nav and inspect stay on the same elements as
// aliases for tests and existing styles.

import { clampSplitSize, splitBounds, splitKeyAction, toPercent, fromPercent, parseStoredPercent, resolveSplitMode } from './layout-math.js';

const SVG_NS = 'http://www.w3.org/2000/svg';
let stackSeq = 0;

function prefersReducedMotion() {
  try { return window.matchMedia('(prefers-reduced-motion: reduce)').matches; } catch (e) { return false; }
}

export function createSplitPane(options) {
  const { root, key, orientation = 'right', collapsible = false, stackBelow = 720, scopeKey, storage } = options;
  const mins = options.min || [240, 320];
  const defaultPct = options.default || 40;
  const doc = root.ownerDocument;
  const win = doc.defaultView || window;
  const list = options.list || root.querySelector(':scope > .split-list');
  const detail = options.detail || root.querySelector(':scope > .split-detail');
  if (!list || !detail) throw new Error('createSplitPane: .split-list and .split-detail are required');
  root.classList.add('split');
  list.classList.add('split-list');
  detail.classList.add('split-detail');

  let sash = root.querySelector(':scope > .split-sash');
  if (!sash) {
    sash = doc.createElement('div');
    sash.className = 'split-sash';
    root.insertBefore(sash, detail);
  }
  sash.setAttribute('role', 'separator');
  sash.tabIndex = 0;
  if (!sash.getAttribute('aria-label')) sash.setAttribute('aria-label', options.label || 'Resize panes');

  const st = { mode: 'right', pct: defaultPct, collapsed: false, view: 'list', origin: null, pushed: false, ignorePop: false, destroyed: false };
  const entryId = 'split-' + (++stackSeq);

  // ---- persistence (guarded: storage can throw or be blocked) ----
  const storageKey = () => (scopeKey ? scopeKey(key) : key);
  const store = () => { try { return storage || win.localStorage; } catch (e) { return null; } };
  function load() {
    if (!key) return;
    try { const v = parseStoredPercent(store().getItem(storageKey())); if (v != null) st.pct = v; } catch (e) { /* default size */ }
  }
  function save() {
    if (!key) return;
    try { store().setItem(storageKey(), JSON.stringify({ v: 1, pct: Math.round(st.pct * 10) / 10 })); } catch (e) { /* not persisted */ }
  }

  // ---- geometry ----
  const total = () => (st.mode === 'bottom' ? root.clientHeight : root.clientWidth) || 0;
  const gutter = () => 8;
  function currentSize() { return clampSplitSize(fromPercent(st.pct, total()), total(), mins, gutter()); }
  function paint() {
    if (st.mode === 'stack') { root.style.removeProperty('--split-size'); return; }
    const t = total();
    const size = currentSize();
    root.style.setProperty('--split-size', size + 'px');
    const b = splitBounds(t, mins, gutter());
    sash.setAttribute('aria-valuenow', String(Math.round(toPercent(size, t))));
    sash.setAttribute('aria-valuemin', String(Math.round(toPercent(b.min, t))));
    sash.setAttribute('aria-valuemax', String(Math.round(toPercent(b.max, t))));
  }
  function setSize(px, { persist = true } = {}) {
    const t = total();
    if (!t) return;
    st.pct = toPercent(clampSplitSize(px, t, mins, gutter()), t);
    paint();
    if (persist) save();
  }

  // ---- mode ----
  function applyMode() {
    const next = resolveSplitMode(root.clientWidth || win.innerWidth, { orientation, stackBelow });
    if (next === st.mode && root.dataset.mode) { paint(); return; }
    st.mode = next;
    root.dataset.mode = next;
    sash.setAttribute('aria-orientation', next === 'bottom' ? 'horizontal' : 'vertical');
    sash.hidden = next === 'stack';
    if (next === 'stack') { setView(st.view === 'detail' ? 'detail' : 'list', { silent: true }); }
    else { delete root.dataset.view; detail.removeAttribute('inert'); list.removeAttribute('inert'); if (st.pushed) dropHistory(); }
    paint();
  }

  // ---- collapse (transform/opacity transition, then hidden) ----
  function collapse() {
    if (!collapsible || st.collapsed || st.mode === 'stack') return;
    st.collapsed = true;
    root.dataset.collapsed = 'true';
    sash.setAttribute('aria-expanded', 'false');
    if (prefersReducedMotion()) { detail.hidden = true; return; }
    detail.classList.add('is-collapsing');
    let done = false;
    const finish = () => { if (done) return; done = true; if (st.collapsed) detail.hidden = true; };
    detail.addEventListener('transitionend', finish, { once: true });
    win.setTimeout(finish, 320);
  }
  function expand() {
    if (!st.collapsed) return;
    st.collapsed = false;
    delete root.dataset.collapsed;
    sash.setAttribute('aria-expanded', 'true');
    detail.hidden = false;
    detail.classList.remove('is-collapsing');
  }
  const toggle = () => (st.collapsed ? expand() : collapse());

  // ---- stack view with one guarded history entry ----
  function pushHistory() {
    if (st.pushed) return;
    try { win.history.pushState({ splitStack: entryId }, ''); st.pushed = true; } catch (e) { /* history unavailable */ }
  }
  function dropHistory() {
    if (!st.pushed) return;
    st.pushed = false;
    st.ignorePop = true;
    try { win.history.back(); } catch (e) { st.ignorePop = false; }
  }
  function setView(view, { silent = false } = {}) {
    st.view = view;
    root.dataset.view = view;
    // The hidden pane leaves the accessibility tree and tab order.
    if (view === 'detail') { list.setAttribute('inert', ''); detail.removeAttribute('inert'); }
    else { detail.setAttribute('inert', ''); list.removeAttribute('inert'); }
    if (!silent && options.onView) options.onView(view);
  }
  function showDetail(trigger) {
    if (st.mode !== 'stack') return;
    st.origin = trigger || doc.activeElement;
    setView('detail');
    pushHistory();
    const target = detail.querySelector('[data-split-focus],h1,h2,h3') || backBtn;
    if (target) { if (!target.hasAttribute('tabindex') && !/^(BUTTON|A|INPUT)$/.test(target.tagName)) target.tabIndex = -1; target.focus({ preventScroll: true }); }
  }
  function showList({ fromPop = false } = {}) {
    if (st.mode !== 'stack' || st.view === 'list') return;
    setView('list');
    if (!fromPop) dropHistory(); else st.pushed = false;
    const o = st.origin;
    st.origin = null;
    if (o && o.isConnected && o.focus) o.focus({ preventScroll: true });
  }
  const onPop = () => {
    if (st.ignorePop) { st.ignorePop = false; return; }
    if (st.mode === 'stack' && st.view === 'detail' && st.pushed) showList({ fromPop: true });
  };
  win.addEventListener('popstate', onPop);

  // Back button (visible in stack mode only, by CSS).
  let backBtn = detail.querySelector(':scope > .split-back');
  if (!backBtn) {
    backBtn = doc.createElement('button');
    backBtn.type = 'button';
    backBtn.className = 'btn split-back';
    const svg = doc.createElementNS(SVG_NS, 'svg');
    svg.setAttribute('class', 'icon');
    svg.setAttribute('aria-hidden', 'true');
    svg.setAttribute('focusable', 'false');
    const use = doc.createElementNS(SVG_NS, 'use');
    use.setAttribute('href', '#i-chevron');
    svg.appendChild(use);
    backBtn.append(svg, doc.createTextNode(options.backLabel || 'Back'));
    detail.insertBefore(backBtn, detail.firstChild);
  }
  backBtn.addEventListener('click', () => showList());

  // ---- sash interaction ----
  let dragFrame = 0, dragStart = null;
  sash.addEventListener('pointerdown', (e) => {
    if (st.mode === 'stack' || e.button > 0) return;
    e.preventDefault();
    try { sash.setPointerCapture(e.pointerId); } catch (err) { /* synthetic pointer */ }
    dragStart = { pos: st.mode === 'bottom' ? e.clientY : e.clientX, size: currentSize() };
    root.classList.add('is-resizing');
  });
  sash.addEventListener('pointermove', (e) => {
    if (!dragStart) return;
    const pos = st.mode === 'bottom' ? e.clientY : e.clientX;
    cancelAnimationFrame(dragFrame);
    dragFrame = requestAnimationFrame(() => setSize(dragStart ? dragStart.size + (pos - dragStart.pos) : 0, { persist: false }));
  });
  const endDrag = (e) => {
    if (!dragStart) return;
    dragStart = null;
    cancelAnimationFrame(dragFrame);
    try { sash.releasePointerCapture(e.pointerId); } catch (err) { /* already released */ }
    root.classList.remove('is-resizing');
    save();
  };
  sash.addEventListener('pointerup', endDrag);
  sash.addEventListener('pointercancel', endDrag);
  sash.addEventListener('dblclick', () => { st.pct = defaultPct; if (st.collapsed) expand(); paint(); save(); });
  sash.addEventListener('keydown', (e) => {
    const act = splitKeyAction(e.key, { orientation: st.mode, shift: e.shiftKey });
    if (!act) return;
    e.preventDefault();
    if (act.toggle) { if (collapsible) toggle(); return; }
    if (st.collapsed) expand();
    const t = total();
    const b = splitBounds(t, mins, gutter());
    if (act.to) setSize(act.to === 'min' ? b.min : b.max);
    else setSize(currentSize() + act.delta);
  });

  // ---- lifecycle ----
  let ro = null;
  load();
  applyMode();
  if (typeof win.ResizeObserver === 'function') {
    ro = new win.ResizeObserver(() => { if (!st.destroyed) applyMode(); });
    ro.observe(root);
  } else win.addEventListener('resize', applyMode);

  return {
    sash, list, detail,
    mode: () => st.mode,
    view: () => st.view,
    getPercent: () => st.pct,
    setSize, collapse, expand, toggle, showDetail, showList,
    isCollapsed: () => st.collapsed,
    refresh: applyMode,
    destroy() {
      st.destroyed = true;
      if (ro) ro.disconnect(); else win.removeEventListener('resize', applyMode);
      win.removeEventListener('popstate', onPop);
      cancelAnimationFrame(dragFrame);
    },
  };
}
