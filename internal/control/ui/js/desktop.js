// desktop.js — the desktop-shell layer. In a plain browser tab it only keeps
// <meta name="theme-color"> on the current theme. Inside a desktop host it marks
// <html class="desktop-app desktop-<platform>"> (desktop.css scopes the app-feel
// rules to it) and swallows browser-only gestures: reload, history navigation,
// page zoom and drag ghosts on chrome. The native macOS shell additionally gets
// a two-way bridge on window.interseptorDesktop:
//   UI → native  post({type:'ready'}), {type:'theme',…}, {type:'status',…},
//                {type:'drag-regions',rects,exclude}, {type:'file-accept',accept},
//                {type:'check-updates'} (the native app runs its own updater)
//   native → UI  run(command) → boolean, and the --titlebar-inset-left CSS
//                variable on <html> (the traffic lights' width; 0 in full screen)
// A host with a unified title bar (interseptorDesktop.titlebar === 'unified')
// draws no title bar of its own: the Flight Bar is the window's drag handle
// ([data-window-drag]) and its drag regions are reported on every layout change.
// The shell injects every action through initDesktop, so this module imports
// only its pure model and never the feature modules.
import { detectDesktop, parseCommand, findTargetFor, firstOpaqueHex, themeMessage, createStatusReporter, blockedKey, isNavigationKey, DRAG_EXCLUDE, OVERLAY, DRAG_SAMPLE_STEP, uncoveredDragRects, dragRegionsMessage, dragRegionsKey, fileAcceptMessage } from './desktop-model.js';

const env = { desktop: null, actions: {}, activePanel: () => '', reporter: null, ready: false, themeKey: '', dragKey: '', dragFrame: 0 };

// Chrome surfaces: the rail, the Flight Bar, toolbars, list headers and controls.
const CHROME = '#bar,#tabs,.nav-rail-foot,.toolbar,.mobile-tool-nav,.dock,.thead,button,[role="tab"],[role="menuitem"]';
// Surfaces that own a pinch gesture and must keep it.
const OWN_ZOOM = '#mapGraphSvg,.img-lightbox-stage';

export const isDesktopApp = () => !!env.desktop;

function bridge() { return env.desktop && env.desktop.bridge ? globalThis.window.interseptorDesktop : null; }
function post(message) {
  const host = bridge();
  if (!host) return;
  try { host.post(message); } catch { /* a failing host never breaks the page */ }
}

// pageBackground is the colour nearest the native title bar: the Flight Bar,
// then the body, then the root.
function pageBackground() {
  const read = (el) => (el ? getComputedStyle(el).backgroundColor : '');
  return firstOpaqueHex([read(document.getElementById('bar')), read(document.body), read(document.documentElement)]);
}

function syncTheme() {
  const msg = themeMessage(pageBackground());
  const meta = document.querySelector('meta[name="theme-color"]');
  if (meta && meta.getAttribute('content') !== msg.background) meta.setAttribute('content', msg.background);
  const key = msg.background + msg.scheme;
  if (key === env.themeKey) return;
  env.themeKey = key;
  post(msg);
}

function isEditable(t) {
  const el = t instanceof Element ? t : null;
  return !!(el && (el.closest('input,textarea,select,[contenteditable=""],[contenteditable="true"]') || el.isContentEditable));
}

// Keyboard modality for the desktop focus ring (css/desktop/states.css): a
// navigation key sets html.dsk-kbd; a pointer press or a native menu command
// clears it, so focus moved by code after a command never paints a ring.
function setKeyboardMode(on) {
  // Toggling the class on every click or arrow key would wake the <html> class
  // observer (a full drag-region rescan); only a real change touches it.
  try {
    const root = document.documentElement;
    if (root.classList.contains('dsk-kbd') === on) return;
    root.classList[on ? 'add' : 'remove']('dsk-kbd');
  } catch { /* no classList */ }
}

function installGuards() {
  const mac = /Mac|iPhone|iPad/.test(navigator.platform || '') || /Mac OS X/.test(navigator.userAgent || '');
  window.addEventListener('keydown', (e) => { if (isNavigationKey(e)) setKeyboardMode(true); }, true);
  window.addEventListener('pointerdown', () => setKeyboardMode(false), true);
  // Bubble phase on window: every feature shortcut (Cmd/Ctrl+R sends a flow to
  // Repeater) has already run; only the browser default is cancelled.
  window.addEventListener('keydown', (e) => {
    if (blockedKey(e, { mac, editable: isEditable(e.target) })) e.preventDefault();
  });
  // Mouse back/forward buttons navigate history in Chromium.
  const noHistoryButton = (e) => { if (e.button === 3 || e.button === 4) e.preventDefault(); };
  window.addEventListener('mousedown', noHistoryButton);
  window.addEventListener('mouseup', noHistoryButton);
  // Ctrl+wheel and trackpad pinch (wheel with ctrlKey) zoom the page. Map and
  // the image lightbox handle the same event on their own element first.
  // The native macOS shell disables magnification itself, so the non-passive
  // listener is only needed for the Chrome --app window.
  if (env.desktop && env.desktop.platform === 'chromeapp') window.addEventListener('wheel', (e) => { if (e.ctrlKey) e.preventDefault(); }, { passive: false });
  const noGestureZoom = (e) => { if (!(e.target instanceof Element && e.target.closest(OWN_ZOOM))) e.preventDefault(); };
  window.addEventListener('gesturestart', noGestureZoom, { passive: false });
  window.addEventListener('gesturechange', noGestureZoom, { passive: false });
  // No drag ghosts for chrome icons and links; explicit draggables (evidence
  // ordering, collections) and text selections keep dragging.
  window.addEventListener('dragstart', (e) => {
    const t = e.target instanceof Element ? e.target : null;
    if (!t || t.closest('[draggable="true"]')) return;
    if (t.closest('img,svg,a') && (t.closest(CHROME) || t.closest('svg.icon'))) e.preventDefault();
  });
  // The browser context menu is already replaced app-wide by core.js's
  // contextmenu handler (custom menus for fields, selections and workspace).
}

// ---- unified title bar: drag regions ----
function visibleRect(el) {
  const r = el.getBoundingClientRect();
  return r.width > 0 && r.height > 0 ? r : null;
}
const overlaps = (a, b) => a.left < b.right && b.left < a.right && a.top < b.bottom && b.top < a.bottom;

// clearColumns hit-tests a drag area every DRAG_SAMPLE_STEP px on three rows
// (near the top, the middle and near the bottom): a column is clear only when
// every row hits the area itself, not a modal, palette, menu or popover drawn
// over it.
function clearColumns(el, r) {
  const ys = [r.top + 2, r.top + r.height / 2, r.bottom - 3];
  const out = [];
  for (let x = r.left; x < r.right; x += DRAG_SAMPLE_STEP) {
    const cx = Math.min(x + 0.5, r.right - 1);
    out.push(ys.every((y) => { const hit = document.elementFromPoint(cx, y); return !!hit && el.contains(hit); }));
  }
  return out;
}

function postDragRegions() {
  env.dragFrame = 0;
  const areas = [];
  const visible = [];
  for (const el of document.querySelectorAll('[data-window-drag]')) {
    const r = visibleRect(el);
    if (!r) continue;
    visible.push(el);
    areas.push(...uncoveredDragRects(r, clearColumns(el, r)));
  }
  const controls = [];
  for (const area of visible) {
    for (const c of area.querySelectorAll(DRAG_EXCLUDE)) {
      const r = visibleRect(c);
      if (r && areas.some((a) => overlaps({ left: a.x, top: a.y, right: a.x + a.w, bottom: a.y + a.h }, r))) controls.push(r);
    }
  }
  const msg = dragRegionsMessage(areas, controls);
  const key = dragRegionsKey(msg);
  if (key === env.dragKey) return;
  env.dragKey = key;
  post(msg);
}
function scheduleDragRegions() {
  if (!env.dragFrame) env.dragFrame = requestAnimationFrame(postDragRegions);
}

// installDragRegions marks the Flight Bar as the drag handle and re-reports
// its regions whenever its layout can have moved: a window resize, a size
// change of the bar or anything in it (the intercept pill's text, the project
// name), controls added, hidden or restyled, and the inset or theme changing
// on <html>.
function installDragRegions() {
  const bar = document.getElementById('bar');
  if (!bar || typeof ResizeObserver !== 'function') return;
  bar.setAttribute('data-window-drag', '');
  const sizes = new ResizeObserver(scheduleDragRegions);
  const observeAll = () => { sizes.disconnect(); sizes.observe(bar); for (const c of bar.querySelectorAll('*')) sizes.observe(c); };
  observeAll();
  new MutationObserver(() => { observeAll(); scheduleDragRegions(); })
    .observe(bar, { subtree: true, childList: true, attributes: true, attributeFilter: ['hidden', 'class', 'style'] });
  new MutationObserver(scheduleDragRegions).observe(document.documentElement, { attributes: true, attributeFilter: ['style', 'class', 'data-theme', 'data-density'] });
  // Overlays opening or closing anywhere (openModal flips style.display;
  // popovers flip hidden/open; menus are added and removed). Only mutations of
  // an overlay schedule a re-report, so streaming lists cost one match each.
  const isOverlay = (n) => n instanceof Element && n.matches(OVERLAY);
  new MutationObserver((records) => {
    for (const r of records) {
      if (isOverlay(r.target) || [...r.addedNodes].some(isOverlay) || [...r.removedNodes].some(isOverlay)) { scheduleDragRegions(); return; }
    }
  }).observe(document.body, { subtree: true, childList: true, attributes: true, attributeFilter: ['style', 'hidden', 'open', 'class', 'data-open'] });
  window.addEventListener('resize', scheduleDragRegions);
  scheduleDragRegions();
}

// ---- file pickers: report the input's accept list before the native panel ----
function installFileAccept() {
  const note = (input) => { if (input && input.type === 'file') post(fileAcceptMessage(input.accept)); };
  // A user click on the input or its label dispatches click on the input itself.
  window.addEventListener('click', (e) => { if (e.target instanceof Element && e.target.matches('input[type="file"]')) note(e.target); }, true);
  // Inputs opened by script, including detached ones (pickTextFile).
  const proto = globalThis.HTMLInputElement && HTMLInputElement.prototype;
  if (!proto) return;
  for (const name of ['click', 'showPicker']) {
    const orig = proto[name];
    if (typeof orig !== 'function') continue;
    try { proto[name] = function (...args) { note(this); return orig.apply(this, args); }; } catch { /* frozen prototype */ }
  }
}

// runCommand executes one native → UI command; unknown or unavailable → false.
export function runCommand(command) {
  const parsed = env.desktop ? parseCommand(command) : null;
  if (!parsed) return false;
  setKeyboardMode(false);
  try {
    if (parsed.action === 'find') return focusFind();
    const fn = env.actions[parsed.action];
    return typeof fn === 'function' ? fn(parsed.arg) === true : false;
  } catch { return false; }
}

function focusFind() {
  const current = env.activePanel();
  const target = findTargetFor(current);
  if (target.panel !== current && !(env.actions.panel && env.actions.panel(target.panel) === true)) return false;
  const el = document.querySelector(target.selector);
  if (!el || el.disabled) return false;
  el.focus();
  if (typeof el.select === 'function') el.select();
  return document.activeElement === el;
}

// requestNativeUpdateCheck hands an update to the native macOS app, which
// replaces the whole signed bundle itself. False without a native bridge
// (browser tab, Chrome --app window): the caller opens the web update dialog.
export function requestNativeUpdateCheck() {
  if (!bridge()) return false;
  post({ type: 'check-updates' });
  return true;
}

// noteDesktopStatus tells the bridge a status source may have changed; the
// reporter debounces (150ms) and posts only a real change.
export function noteDesktopStatus() { if (env.reporter) env.reporter.note(); }

// desktopReady posts {type:'ready'} once, after boot has settled, followed by
// the current status.
export function desktopReady() {
  if (env.ready || !env.desktop) return;
  env.ready = true;
  post({ type: 'ready' });
  if (env.reporter) env.reporter.flush();
}

// initDesktop detects the host and wires everything. actions: {panel(name),
// toggleRequestIntercept, toggleResponseIntercept, openPalette, openProject,
// toggleTheme}, each returning true when it acted. readStatus returns
// {project, live, intercept} for the status message; activePanel names the
// focused panel for `find`. Returns the detection result, null in a browser tab.
export function initDesktop({ actions = {}, readStatus = () => ({}), activePanel = () => '' } = {}) {
  const w = globalThis.window;
  if (!w || !w.document) return null;
  const standalone = !!(w.matchMedia && w.matchMedia('(display-mode: standalone)').matches);
  const finePointer = !!(w.matchMedia && w.matchMedia('(pointer: fine)').matches);
  const desktop = detectDesktop({ host: w.interseptorDesktop || null, standalone, finePointer });
  env.desktop = desktop;
  env.actions = actions;
  env.activePanel = activePanel;
  if (desktop) {
    document.documentElement.classList.add(...desktop.classes);
    installGuards();
    // The skin's tokens set 28px rows; History and other virtual lists measured the
    // web --row-h at load, so their row math drifted 2px a row from the real rows.
    if (typeof w.dispatchEvent === 'function' && typeof CustomEvent === 'function') w.dispatchEvent(new CustomEvent('densitychange'));
    if (desktop.bridge) {
      env.reporter = createStatusReporter({ read: readStatus, post });
      try { w.interseptorDesktop.run = runCommand; } catch { /* a frozen host object keeps its own run */ }
      if (w.interseptorDesktop.titlebar === 'unified') installDragRegions();
      installFileAccept();
    }
  }
  syncTheme();
  new MutationObserver(syncTheme).observe(document.documentElement, { attributes: true, attributeFilter: ['data-theme'] });
  return desktop;
}
