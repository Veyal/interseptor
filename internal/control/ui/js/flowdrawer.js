// flowdrawer.js — the Flow Drawer: one place to read a captured flow from any
// panel. Registered as core's `openFlow` hook, so panels call openFlow(id,
// {source, tab, siblings}) without importing each other.
//
//   docked (>= 1101px)  role=complementary beside the workspace, no focus trap
//   overlay (<= 1100px) role=dialog aria-modal through core's openModal trap
//
// Nothing is fetched until a flow is opened. Bodies come from the pure
// renderFlowBody in flowbody.js (shared with the Proxy inspector); raw text is
// never requested for binary bodies or bodies above 1 MB.

import { $, api, state, toastError, openModal, closeModal, registerHook, projectStorageKey, highlightHTTP, prettify, isBinaryMime, bodyMime, headerBlockText, flowBodyDownloadHref, copyText, mountFlowCopyPng } from './core.js';
import { renderState } from './statepanel.js';
import { readSingleKeyPref, isTypingTarget } from './keys.js';
import { createFinder } from './finder.js';
import { tabsForFlow, nextTab, renderFlowBody, flowUrl, clampDrawerWidth, clampDrawerHeight, normalizeFlowDrawerLayout, DEFAULT_FLOW_DRAWER_LAYOUT, stepSibling, linkedFindings, rawState, DRAWER_WIDTH, DRAWER_HEIGHT } from './flowbody.js';

const root = document.getElementById('flowDrawer');
const OVERLAY_QUERY = '(max-width: 1100px)';
const WIDTH_KEY = 'interseptor.flowDrawerW';
const HEIGHT_KEY = 'interseptor.flowDrawerH';
const LAYOUT_KEY = 'interseptor.flowDrawerLayout';

const cur = { id: 0, detail: null, raw: {}, rawOrig: {}, variant: 'current', ws: null, timeline: null, auth: null, tab: 'request', opts: {}, epoch: 0, tabEpoch: 0, opener: null, overlay: false, open: false, layout: 'bottom' };

// A flow modified in Intercept carries its pre-interception bytes; the Compare
// control (folded in from the deleted flowmodal.js) switches the Request/Response
// body between the forwarded "current" bytes and the captured "original".
function hasOriginal(d) { return !!(d && (d.originalReqBodyHash || d.originalResBodyHash || d.originalReqHeaders || d.originalResHeaders)); }

function storedWidth() {
  try { return clampDrawerWidth(localStorage.getItem(projectStorageKey(WIDTH_KEY))); } catch (e) { return DRAWER_WIDTH.def; }
}
function saveWidth(w) { try { localStorage.setItem(projectStorageKey(WIDTH_KEY), String(w)); } catch (e) { /* private mode */ } }
let width = DRAWER_WIDTH.def;

function applyWidth(w, persist) {
  width = clampDrawerWidth(w);
  document.documentElement.style.setProperty('--flow-drawer-w', width + 'px');
  const sash = $('#fdSash');
  if (sash && cur.layout !== 'bottom') sash.setAttribute('aria-valuenow', String(width));
  if (persist) saveWidth(width);
}

function storedHeight() {
  try { return clampDrawerHeight(localStorage.getItem(projectStorageKey(HEIGHT_KEY))); } catch (e) { return DRAWER_HEIGHT.def; }
}
function saveHeight(h) { try { localStorage.setItem(projectStorageKey(HEIGHT_KEY), String(h)); } catch (e) { /* private mode */ } }
let height = DRAWER_HEIGHT.def;

function applyHeight(h, persist) {
  height = clampDrawerHeight(h);
  document.documentElement.style.setProperty('--flow-drawer-h', height + 'px');
  const sash = $('#fdSash');
  if (sash && cur.layout === 'bottom') sash.setAttribute('aria-valuenow', String(height));
  if (persist) saveHeight(height);
}

function storedLayout() {
  try { return normalizeFlowDrawerLayout(localStorage.getItem(projectStorageKey(LAYOUT_KEY))); } catch (e) { return DEFAULT_FLOW_DRAWER_LAYOUT; }
}
function saveLayout(l) { try { localStorage.setItem(projectStorageKey(LAYOUT_KEY), normalizeFlowDrawerLayout(l)); } catch (e) { /* private mode */ } }

const isOverlay = () => window.matchMedia(OVERLAY_QUERY).matches;

// The shared Finder sits between the tabs and the body and searches the body's
// rendered text; Ctrl+F or `/` inside the drawer opens it.
const finder = createFinder($('#fdPanel'), { root: $('#fdBody'), insertBefore: () => $('#fdBody'), label: 'Find in flow' });

/* ---- body rendering ---- */
const bodyDeps = {
  highlight: (raw, side) => highlightHTTP(prettify(raw), true, bodyMime(cur.detail, side)),
  downloadHref: (id, side) => flowBodyDownloadHref(id, side),
  mimeOf: (detail, side) => bodyMime(detail, side),
  isBinaryMime,
  headerText: (detail, side) => headerBlockText(detail, side),
};
const rawStore = () => (cur.variant === 'original' ? cur.rawOrig : cur.raw);
const flowView = () => ({ id: cur.id, detail: cur.detail, raw: rawStore(), ws: cur.ws, timeline: cur.timeline, auth: cur.auth });

function paintBody() {
  const body = $('#fdBody');
  if (!body || !cur.detail) return;
  body.removeAttribute('aria-busy');
  body.dataset.state = 'ready';
  body.innerHTML = renderFlowBody(flowView(), cur.tab, bodyDeps);
  body.scrollTop = 0;
  if (finder.isOpen()) finder.refresh(); // marks died with the old markup
}

function showError(err, retry, title) {
  const body = $('#fdBody');
  renderState(body, 'error', { title, status: err && err.status, message: err && err.message, onRetry: retry });
}

async function loadTab(tab) {
  const epoch = cur.epoch;
  const tabEpoch = ++cur.tabEpoch;
  const id = cur.id;
  const live = () => cur.epoch === epoch && cur.tabEpoch === tabEpoch && cur.tab === tab;
  const side = tab === 'request' ? 'req' : tab === 'response' ? 'res' : '';
  try {
    if (side) {
      const store = rawStore();
      if (rawState(cur.detail, side, bodyDeps) !== 'text' || typeof store[side] === 'string') return paintBody();
      renderState($('#fdBody'), 'loading', { rows: 4, title: 'Loading ' + (side === 'req' ? 'request' : 'response') });
      const raw = await api('/api/flows/' + id + '/raw?side=' + side + (cur.variant === 'original' ? '&variant=original' : ''));
      if (!live()) return;
      store[side] = raw;
    } else if (tab === 'timeline' && !cur.timeline) {
      renderState($('#fdBody'), 'loading', { rows: 3, title: 'Loading timeline' });
      const t = await api('/api/flows/session-inspect?ids=' + id);
      if (!live()) return;
      cur.timeline = t;
    } else if (tab === 'auth' && !cur.auth) {
      renderState($('#fdBody'), 'loading', { rows: 3, title: 'Loading auth timeline' });
      const a = await api('/api/flows/' + id + '/auth-timeline');
      if (!live()) return;
      cur.auth = a;
    } else if (tab === 'ws') {
      renderState($('#fdBody'), 'loading', { rows: 3, title: 'Loading frames' });
      const w = await api('/api/flows/' + id + '/ws');
      if (!live()) return;
      cur.ws = w;
    }
    paintBody();
  } catch (e) {
    if (live()) showError(e, () => loadTab(tab), 'Could not load this section');
  }
}

/* ---- tabs (roving tabindex) ---- */
function renderTabs() {
  const host = $('#fdTabs');
  host.textContent = '';
  const tabs = tabsForFlow(cur.detail);
  if (!tabs.some((t) => t.id === cur.tab)) cur.tab = tabs[0].id;
  for (const t of tabs) {
    const b = document.createElement('button');
    b.type = 'button';
    b.className = 'flow-drawer-tab';
    b.id = 'fdTab-' + t.id;
    b.dataset.tab = t.id;
    b.setAttribute('role', 'tab');
    b.setAttribute('aria-selected', t.id === cur.tab ? 'true' : 'false');
    b.setAttribute('aria-controls', 'fdBody');
    b.tabIndex = t.id === cur.tab ? 0 : -1;
    b.textContent = t.label;
    host.appendChild(b);
  }
  $('#fdBody').setAttribute('aria-labelledby', 'fdTab-' + cur.tab);
}
function selectTab(id, focus) {
  if (!cur.detail) return;
  cur.tab = id;
  $('#fdTabs').querySelectorAll('[role=tab]').forEach((b) => {
    const on = b.dataset.tab === id;
    b.setAttribute('aria-selected', on ? 'true' : 'false');
    b.tabIndex = on ? 0 : -1;
    if (on && focus) b.focus();
  });
  $('#fdBody').setAttribute('aria-labelledby', 'fdTab-' + id);
  loadTab(id);
}
$('#fdTabs').addEventListener('click', (e) => { const b = e.target.closest('[role=tab]'); if (b) selectTab(b.dataset.tab, false); });
$('#fdTabs').addEventListener('keydown', (e) => {
  const next = nextTab(tabsForFlow(cur.detail || {}), cur.tab, e.key);
  if (next !== cur.tab) { e.preventDefault(); selectTab(next, true); }
});

/* ---- header, linked findings, step buttons ---- */
function paintHeader() {
  const d = cur.detail;
  $('#fdTitle').textContent = `${d.method} ${flowUrl(d)}`;
  $('#fdStatus').textContent = d.status ? `${d.status}${d.durationMs ? ' in ' + d.durationMs + ' ms' : ''}` : (d.error || 'No response');
  mountFlowCopyPng($('#fdCopyPng'), cur.id, 'btn');
  syncSteps();
  syncVariantToggle();
}
// syncVariantToggle injects an Original/Current segmented control into the drawer
// head when (and only when) the flow was modified in Intercept, preserving the
// deleted modal's Compare. It is built dynamically so index.html carries no dead
// control for the common unmodified flow.
function syncVariantToggle() {
  const head = $('#fdHead');
  if (!head) return;
  let wrap = $('#fdVariant');
  if (!hasOriginal(cur.detail)) { if (wrap) wrap.remove(); return; }
  if (!wrap) {
    wrap = document.createElement('div');
    wrap.id = 'fdVariant';
    wrap.className = 'seg fd-variant';
    wrap.setAttribute('role', 'group');
    wrap.setAttribute('aria-label', 'Compare request/response with the pre-interception original');
    for (const [value, label] of [['current', 'Current'], ['original', 'Original']]) {
      const b = document.createElement('button');
      b.type = 'button';
      b.dataset.variant = value;
      b.textContent = label;
      wrap.appendChild(b);
    }
    wrap.addEventListener('click', (e) => { const b = e.target.closest('[data-variant]'); if (b) setVariant(b.dataset.variant); });
    head.appendChild(wrap);
  }
  wrap.querySelectorAll('[data-variant]').forEach((b) => {
    const on = b.dataset.variant === cur.variant;
    b.classList.toggle('on', on);
    b.setAttribute('aria-pressed', String(on));
  });
}
function setVariant(v) {
  const next = v === 'original' ? 'original' : 'current';
  if (next === cur.variant) return;
  cur.variant = next;
  syncVariantToggle();
  loadTab(cur.tab);
}
function syncSteps() {
  const sib = cur.opts.siblings;
  $('#fdPrev').disabled = stepSibling(sib, cur.id, -1) == null;
  $('#fdNext').disabled = stepSibling(sib, cur.id, 1) == null;
  $('#fdHead').classList.toggle('has-steps', Array.isArray(sib) && sib.length > 1);
}
function step(delta) {
  const next = stepSibling(cur.opts.siblings, cur.id, delta);
  if (next != null) openFlow(next, { ...cur.opts, tab: cur.tab, keepOpener: true });
}
$('#fdNarrow').addEventListener('click', () => {
  if (cur.layout === 'bottom') applyHeight(height - 48, true);
  else applyWidth(width - 64, true);
});
$('#fdWide').addEventListener('click', () => {
  if (cur.layout === 'bottom') applyHeight(height + 48, true);
  else applyWidth(width + 64, true);
});
const popupBtn = $('#fdPopupBtn');
if (popupBtn) popupBtn.addEventListener('click', () => togglePopup());
$('#fdHead').addEventListener('dblclick', (e) => {
  if (e.target.closest('button, input, select, a, [role="tab"]')) return;
  togglePopup();
});
$('#fdPrev').addEventListener('click', () => step(-1));
$('#fdNext').addEventListener('click', () => step(1));
$('#fdHead').addEventListener('keydown', (e) => {
  if (e.ctrlKey || e.metaKey || e.altKey || !readSingleKeyPref()) return;
  if (e.key === 'j') { e.preventDefault(); step(1); } else if (e.key === 'k') { e.preventDefault(); step(-1); }
});

async function paintLinked(epoch) {
  const host = $('#fdLinked');
  host.textContent = '';
  try {
    const d = await api('/api/findings');
    if (cur.epoch !== epoch) return;
    const linked = linkedFindings(d.findings || [], cur.id);
    if (!linked.length) return;
    const label = document.createElement('span');
    label.className = 'flow-drawer-linked-label';
    label.textContent = 'Linked:';
    host.appendChild(label);
    for (const f of linked.slice(0, 6)) {
      const a = document.createElement('a');
      a.className = 'flow-chip';
      a.href = '#finding-' + f.id + '/evidence';
      a.textContent = '#' + f.id + ' ' + f.title;
      host.appendChild(a);
    }
  } catch (e) { /* linked chips are a convenience */ }
}

/* ---- small menu popover ---- */
let menu = null;
function closeMenu(restore = true) {
  if (!menu) return;
  const { el, trigger, onDown } = menu;
  document.removeEventListener('pointerdown', onDown, true);
  el.remove();
  trigger.setAttribute('aria-expanded', 'false');
  menu = null;
  if (restore) trigger.focus({ preventScroll: true });
}
function openMenu(trigger, items) {
  if (menu && menu.trigger === trigger) return closeMenu();
  closeMenu(false);
  const el = document.createElement('ul');
  el.className = 'flow-menu';
  el.setAttribute('role', 'menu');
  el.setAttribute('aria-label', trigger.textContent.trim() || trigger.getAttribute('aria-label'));
  const buttons = items.map((it) => {
    const li = document.createElement('li');
    li.setAttribute('role', 'none');
    const b = document.createElement('button');
    b.type = 'button';
    b.className = 'flow-menu-item';
    b.setAttribute('role', 'menuitem');
    b.tabIndex = -1;
    b.textContent = it.label;
    b.addEventListener('click', () => { closeMenu(false); it.run(); });
    li.appendChild(b);
    el.appendChild(li);
    return b;
  });
  $('#fdFoot').appendChild(el);
  trigger.setAttribute('aria-expanded', 'true');
  const onDown = (e) => { if (!el.contains(e.target) && e.target !== trigger) closeMenu(false); };
  document.addEventListener('pointerdown', onDown, true);
  menu = { el, trigger, onDown };
  let at = 0;
  const focusAt = (i) => { at = (i + buttons.length) % buttons.length; buttons[at].focus(); };
  el.addEventListener('keydown', (e) => {
    if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); closeMenu(); }
    else if (e.key === 'ArrowDown') { e.preventDefault(); focusAt(at + 1); }
    else if (e.key === 'ArrowUp') { e.preventDefault(); focusAt(at - 1); }
    else if (e.key === 'Home') { e.preventDefault(); focusAt(0); }
    else if (e.key === 'End') { e.preventDefault(); focusAt(buttons.length - 1); }
    else if (e.key === 'Tab') closeMenu(false);
  });
  focusAt(0);
}

/* ---- footer actions ---- */
const attach = (extra = {}) => import('./evidence-attach.js').then((m) => m.attachEvidence({ kind: 'flow', refs: [cur.id], ...extra }, { anchor: $('#fdAttach') }));

$('#fdAttach').addEventListener('click', () => attach());
$('#fdAttachMore').addEventListener('click', (e) => {
  const items = [{ label: 'Attach with a proof statement...', run: async () => {
    const { uiPrompt } = await import('./core.js');
    const proof = await uiPrompt({ title: 'What does this flow prove?', placeholder: 'State the exact claim this evidence supports' });
    if (proof) attach({ proof });
  } }];
  if (cur.detail && tabsForFlow(cur.detail).some((t) => t.id === 'ws')) items.push({ label: 'Attach as WebSocket evidence', run: () => import('./evidence-attach.js').then((m) => m.attachEvidence({ kind: 'ws', refs: [cur.id], note: 'WebSocket traffic' }, { anchor: $('#fdAttach') })) });
  openMenu(e.currentTarget, items);
});
$('#fdRepeater').addEventListener('click', () => import('./tools.js').then((m) => m.sendToRepeater({ id: cur.id })).catch((e) => toastError('Repeater', e)));
$('#fdIntruder').addEventListener('click', () => import('./tools.js').then((m) => m.sendToIntruder({ id: cur.id })).catch((e) => toastError('Intruder', e)));
$('#fdScanner').addEventListener('click', async () => {
  try {
    const proxy = await import('./proxy.js');
    await proxy.selectFlow(cur.id);
    (await import('./scanner.js')).openChecks();
  } catch (e) { toastError('Scanner check', e); }
});
$('#fdMore').addEventListener('click', async (e) => {
  const trigger = e.currentTarget;
  const { COPY_AS_KINDS, copyAs } = await import('./copyas.js');
  const id = cur.id;
  const items = [
    { label: 'Copy URL', run: () => copyText(flowUrl(cur.detail), 'URL copied') },
    { label: 'Copy link to this flow', run: () => copyText(location.origin + '/#flow-' + id, 'Flow link copied') },
    ...COPY_AS_KINDS.map((k) => ({ label: 'Copy as ' + k.label, run: () => copyAs(k.kind, [{ id }]) })),
    { label: 'Show in Proxy history', run: () => showInProxy() },
  ];
  openMenu(trigger, items);
});
async function showInProxy() {
  const d = cur.detail;
  if (!d) return;
  const id = cur.id;
  document.querySelector('.tab[data-tab="proxy"]')?.click();
  state.filters = { scheme: '', method: d.method || '', status: '', host: d.host || '', search: (d.path || '').split('?')[0], exclude: [] };
  const proxy = await import('./proxy.js');
  proxy.syncControls(); proxy.renderChips(); proxy.loadFlows();
  proxy.selectFlow(id);
}

/* ---- open / close ---- */
function updatePopupBtn() {
  const btn = $('#fdPopupBtn');
  if (!btn) return;
  const isPopup = cur.layout === 'popup';
  btn.setAttribute('title', isPopup ? 'Dock to bottom (or double-click header)' : 'Open as popup dialog (or double-click header)');
  btn.setAttribute('aria-label', isPopup ? 'Dock to bottom' : 'Open as popup dialog');
  const iconName = isPopup ? 'panel-bottom' : 'external';
  const use = btn.querySelector('use');
  if (use) use.setAttribute('href', '#i-' + iconName);
  const narrow = $('#fdNarrow');
  const wide = $('#fdWide');
  if (narrow && wide) {
    if (cur.layout === 'bottom') {
      narrow.title = 'Shorter';
      narrow.setAttribute('aria-label', 'Make the flow viewer shorter');
      wide.title = 'Taller';
      wide.setAttribute('aria-label', 'Make the flow viewer taller');
    } else {
      narrow.title = 'Narrower';
      narrow.setAttribute('aria-label', 'Make the drawer narrower');
      wide.title = 'Wider';
      wide.setAttribute('aria-label', 'Make the drawer wider');
    }
  }
}

function syncMode() {
  cur.overlay = cur.layout === 'popup' || (cur.layout === 'side' && isOverlay());
  const panel = $('#fdPanel');
  root.dataset.layout = cur.layout;
  root.dataset.mode = cur.overlay ? (cur.layout === 'popup' ? 'popup' : 'overlay') : 'docked';
  if (cur.overlay) { panel.setAttribute('role', 'dialog'); panel.setAttribute('aria-modal', 'true'); } else { panel.setAttribute('role', 'complementary'); panel.removeAttribute('aria-modal'); }
  const sash = $('#fdSash');
  if (sash) {
    if (cur.layout === 'bottom') {
      sash.setAttribute('aria-orientation', 'horizontal');
      sash.setAttribute('aria-valuemin', String(DRAWER_HEIGHT.min));
      sash.setAttribute('aria-valuemax', String(DRAWER_HEIGHT.max));
      sash.setAttribute('aria-valuenow', String(height));
    } else {
      sash.setAttribute('aria-orientation', 'vertical');
      sash.setAttribute('aria-valuemin', String(DRAWER_WIDTH.min));
      sash.setAttribute('aria-valuemax', String(DRAWER_WIDTH.max));
      sash.setAttribute('aria-valuenow', String(width));
    }
  }
}

export function togglePopup() {
  if (!cur.open) return;
  const next = cur.layout === 'popup' ? 'bottom' : 'popup';
  saveLayout(next);
  openFlow(cur.id, { ...cur.opts, tab: cur.tab, layout: next, keepOpener: true });
}

function revertEvidenceRoute() {
  const match = location.hash.match(/^#finding-(\d+)\/flow-\d+$/i);
  if (match) history.replaceState(null, '', `#finding-${match[1]}/evidence`);
}

export function closeFlowDrawer({ updateRoute = true } = {}) {
  if (!cur.open) return false;
  closeMenu(false);
  cur.epoch++;
  cur.tabEpoch++;
  cur.open = false;
  finder.close();
  const wasOverlay = cur.overlay;
  if (wasOverlay) closeModal(root);
  root.style.removeProperty('display');
  root.classList.remove('is-open');
  document.body.classList.remove('flow-drawer-docked');
  document.body.classList.remove('flow-drawer-docked-bottom');
  if (!wasOverlay && cur.opener && cur.opener.isConnected && cur.opener.focus) cur.opener.focus({ preventScroll: true });
  cur.opener = null;
  if (updateRoute) revertEvidenceRoute();
  return true;
}

export function openFlow(id, opts = {}) {
  const flowId = Number(id);
  if (!Number.isSafeInteger(flowId) || flowId <= 0) return false;
  const reopen = cur.open;
  if (reopen && cur.overlay && (opts.layout || cur.layout) !== 'popup') closeModal(root);
  document.body.classList.remove('flow-drawer-docked');
  document.body.classList.remove('flow-drawer-docked-bottom');
  cur.epoch++;
  cur.tabEpoch++;
  const epoch = cur.epoch;
  if (!reopen) {
    cur.opener = opts.opener || document.activeElement;
    width = storedWidth();
    height = storedHeight();
    cur.layout = opts.layout || storedLayout();
  } else if (opts.layout) {
    cur.layout = opts.layout;
  }
  Object.assign(cur, { id: flowId, detail: null, raw: {}, rawOrig: {}, variant: 'current', ws: null, timeline: null, auth: null, opts, tab: opts.tab || 'response' });
  syncMode();
  updatePopupBtn();
  cur.open = true;
  $('#fdTitle').textContent = 'Flow #' + flowId;
  $('#fdStatus').textContent = '';
  mountFlowCopyPng($('#fdCopyPng'), null);
  $('#fdLinked').textContent = '';
  $('#fdTabs').textContent = '';
  syncSteps();
  renderState($('#fdBody'), 'loading', { rows: 5, title: 'Loading flow' });
  root.classList.add('is-open');
  if (cur.overlay) {
    openModal(root, { onEscape: () => closeFlowDrawer(), onDismiss: () => closeFlowDrawer(), initialFocus: '#fdTitle' });
  } else {
    if (cur.layout === 'bottom') {
      document.body.classList.add('flow-drawer-docked-bottom');
      applyHeight(height, false);
    } else {
      document.body.classList.add('flow-drawer-docked');
      applyWidth(width, false);
    }
    if (!reopen || opts.focusTitle !== false) $('#fdTitle').focus({ preventScroll: true });
  }
  api('/api/flows/' + flowId).then((detail) => {
    if (cur.epoch !== epoch) return;
    cur.detail = detail;
    if (!opts.tab) cur.tab = detail.status || detail.resLen ? 'response' : 'request';
    paintHeader();
    renderTabs();
    loadTab(cur.tab);
    paintLinked(epoch);
  }).catch((e) => {
    if (cur.epoch !== epoch) return;
    const gone = e && (e.status === 404 || /not found/i.test(e.message || ''));
    showError(e, () => openFlow(flowId, { ...opts, focusTitle: false }), gone ? 'This flow was deleted' : 'Could not load this flow');
  });
  return true;
}

$('#fdClose').addEventListener('click', () => closeFlowDrawer());
$('#fdPanel').addEventListener('keydown', (e) => {
  const inFinder = e.target && e.target.closest && e.target.closest('.finder');
  if (!inFinder && !e.defaultPrevented && (((e.ctrlKey || e.metaKey) && !e.altKey && (e.key === 'f' || e.key === 'F'))
    || (e.key === '/' && !e.ctrlKey && !e.metaKey && !e.altKey && readSingleKeyPref() && !isTypingTarget(e.target)))) {
    e.preventDefault();
    finder.open();
    return;
  }
  if (e.key === 'Escape' && !cur.overlay && !e.defaultPrevented) { e.preventDefault(); closeFlowDrawer(); }
});

window.addEventListener('flowdrawerlayoutchange', (e) => {
  const next = normalizeFlowDrawerLayout(e.detail);
  saveLayout(next);
  if (cur.open) {
    openFlow(cur.id, { ...cur.opts, tab: cur.tab, layout: next, keepOpener: true });
  } else {
    cur.layout = next;
    syncMode();
    updatePopupBtn();
  }
});

/* ---- resize sash (docked only) ---- */
(function wireSash() {
  const sash = $('#fdSash');
  let drag = null;
  sash.addEventListener('pointerdown', (e) => {
    if (cur.overlay) return;
    try { sash.setPointerCapture(e.pointerId); } catch (err) { /* synthetic pointer */ }
    drag = cur.layout === 'bottom' ? { y: e.clientY, h: height } : { x: e.clientX, w: width };
    root.classList.add('is-resizing');
  });
  sash.addEventListener('pointermove', (e) => {
    if (!drag) return;
    if (cur.layout === 'bottom') applyHeight(drag.h + (drag.y - e.clientY), false);
    else applyWidth(drag.w + (drag.x - e.clientX), false);
  });
  const end = () => {
    if (!drag) return;
    drag = null;
    root.classList.remove('is-resizing');
    if (cur.layout === 'bottom') saveHeight(height);
    else saveWidth(width);
  };
  sash.addEventListener('pointerup', end);
  sash.addEventListener('pointercancel', end);
  sash.addEventListener('keydown', (e) => {
    const big = e.shiftKey ? 64 : 16;
    if (cur.layout === 'bottom') {
      let h = null;
      if (e.key === 'ArrowUp') h = height + big;
      else if (e.key === 'ArrowDown') h = height - big;
      else if (e.key === 'Home') h = DRAWER_HEIGHT.min;
      else if (e.key === 'End') h = DRAWER_HEIGHT.max;
      if (h == null) return;
      e.preventDefault();
      applyHeight(h, true);
    } else {
      let w = null;
      if (e.key === 'ArrowLeft') w = width + big;
      else if (e.key === 'ArrowRight') w = width - big;
      else if (e.key === 'Home') w = DRAWER_WIDTH.min;
      else if (e.key === 'End') w = DRAWER_WIDTH.max;
      if (w == null) return;
      e.preventDefault();
      applyWidth(w, true);
    }
  });
  sash.addEventListener('dblclick', () => {
    if (cur.layout === 'bottom') applyHeight(DRAWER_HEIGHT.def, true);
    else applyWidth(DRAWER_WIDTH.def, true);
  });
})();

// Markdown flow links (a.md-flow-link, injected by the safe markdown renderer in
// notes/findings) open in the unified inspector. Moved here from the deleted
// flowmodal.js so the one inspector owns every "open this flow" entry point.
document.addEventListener('click', (e) => {
  const a = e.target.closest && e.target.closest('a.md-flow-link');
  if (!a) return;
  e.preventDefault();
  const id = Number(a.dataset.flow);
  if (id) openFlow(id, { source: 'md-link' });
});

registerHook('openFlow', openFlow);
registerHook('closeFlow', closeFlowDrawer);
registerHook('flowDrawerCurrent', () => (cur.open ? cur.id : 0));
registerHook('renderFlowBody', (flow, tab) => renderFlowBody(flow, tab, bodyDeps));
