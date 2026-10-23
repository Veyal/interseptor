// desktop-model.js — pure rules behind desktop.js: host detection, the native →
// UI command table, the UI → native status snapshot with its dedupe key, colour
// conversion for the theme message, and the browser-chrome key guards. No DOM,
// so it runs under node (_js-tests/desktop-model.test.mjs).

// PANELS are the tool documents `panel:<name>` may open (the rail's data-tab ids).
export const PANELS = Object.freeze(['proxy', 'intercept', 'repeater', 'intruder', 'collections', 'scanner', 'map', 'findings', 'notes', 'activity', 'settings']);

// COMMANDS maps every non-panel command to the action name desktop.js calls.
export const COMMANDS = Object.freeze({
  'intercept:toggle-request': 'toggleRequestIntercept',
  'intercept:toggle-response': 'toggleResponseIntercept',
  'palette:open': 'openPalette',
  'project:switch': 'openProject',
  'find': 'find',
  'theme:toggle': 'toggleTheme',
});

// parseCommand turns a native command string into {action, arg} or null for
// anything outside the contract (unknown name, unknown panel, non-string).
export function parseCommand(command) {
  if (typeof command !== 'string') return null;
  const c = command.trim();
  if (c.startsWith('panel:')) {
    const panel = c.slice(6);
    return PANELS.includes(panel) ? { action: 'panel', arg: panel } : null;
  }
  return Object.prototype.hasOwnProperty.call(COMMANDS, c) ? { action: COMMANDS[c], arg: null } : null;
}

// FIND_TARGETS is the search/filter field `find` focuses for each panel. A panel
// without one (Intercept's filter is a capture rule, not a search; Repeater,
// Intruder and Notes have none) falls back to History search in Proxy.
export const FIND_TARGETS = Object.freeze({
  proxy: '#fSearch',
  scanner: '#scanFilter',
  findings: '#findSearch',
  map: '#mapSearch',
  collections: '#collFilter',
  activity: '#actIntentFilter',
  settings: '#setSearch',
});
export function findTargetFor(panel) {
  return FIND_TARGETS[panel] ? { panel, selector: FIND_TARGETS[panel] } : { panel: 'proxy', selector: FIND_TARGETS.proxy };
}

// detectDesktop names the desktop host, or returns null in a plain browser tab.
// The native macOS shell injects window.interseptorDesktop; a Chrome/Edge
// `--app` window matches (display-mode: standalone), which a normal tab never does.
// A standalone window only counts with a fine (mouse/trackpad) pointer: a phone
// that installed the UI to its home screen is standalone too, and keeps the web
// layout.
export function detectDesktop({ host = null, standalone = false, finePointer = false } = {}) {
  if (host && typeof host === 'object' && typeof host.post === 'function') {
    const platform = /^[a-z0-9-]{1,24}$/.test(String(host.platform || '')) ? String(host.platform) : 'native';
    return { platform, classes: ['desktop-app', 'desktop-' + platform], bridge: true };
  }
  if (standalone && finePointer) return { platform: 'chromeapp', classes: ['desktop-app', 'desktop-chromeapp'], bridge: false };
  return null;
}

// ---- colour ----
const hex2 = (n) => Math.max(0, Math.min(255, Math.round(n))).toString(16).padStart(2, '0');

// parseColor reads the computed colour forms browsers return (`rgb()`,
// `rgba()` with comma or space syntax, `#rgb`/`#rrggbb`, `color(srgb …)`) into
// {r,g,b,a} with 0–255 channels, or null.
export function parseColor(value) {
  const s = String(value || '').trim().toLowerCase();
  let m = /^#([0-9a-f]{3}|[0-9a-f]{6}|[0-9a-f]{8})$/.exec(s);
  if (m) {
    let h = m[1];
    if (h.length === 3) h = h.split('').map((c) => c + c).join('');
    const a = h.length === 8 ? parseInt(h.slice(6, 8), 16) / 255 : 1;
    return { r: parseInt(h.slice(0, 2), 16), g: parseInt(h.slice(2, 4), 16), b: parseInt(h.slice(4, 6), 16), a };
  }
  m = /^rgba?\(\s*([\d.]+)[\s,]+([\d.]+)[\s,]+([\d.]+)(?:\s*[,/]\s*([\d.]+%?))?\s*\)$/.exec(s);
  if (m) {
    const a = m[4] == null ? 1 : m[4].endsWith('%') ? parseFloat(m[4]) / 100 : parseFloat(m[4]);
    return { r: +m[1], g: +m[2], b: +m[3], a };
  }
  m = /^color\(srgb\s+([\d.]+)\s+([\d.]+)\s+([\d.]+)(?:\s*\/\s*([\d.]+%?))?\s*\)$/.exec(s);
  if (m) {
    const a = m[4] == null ? 1 : m[4].endsWith('%') ? parseFloat(m[4]) / 100 : parseFloat(m[4]);
    return { r: +m[1] * 255, g: +m[2] * 255, b: +m[3] * 255, a };
  }
  return null;
}

// toHex returns '#rrggbb' for an opaque colour, or null for a transparent or
// unparseable one (the caller then tries the next surface).
export function toHex(value) {
  const c = parseColor(value);
  if (!c || c.a < 0.999) return null;
  return '#' + hex2(c.r) + hex2(c.g) + hex2(c.b);
}

// firstOpaqueHex picks the first opaque colour from surfaces ordered nearest
// the native title bar first.
export function firstOpaqueHex(values, fallback = '#111318') {
  for (const v of values || []) { const h = toHex(v); if (h) return h; }
  return fallback;
}

// schemeFor classifies a background by relative luminance (WCAG formula):
// light surfaces want dark native controls and vice versa.
export function schemeFor(hex) {
  const c = parseColor(hex);
  if (!c) return 'dark';
  const lin = (v) => { v /= 255; return v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4; };
  const l = 0.2126 * lin(c.r) + 0.7152 * lin(c.g) + 0.0722 * lin(c.b);
  return l > 0.4 ? 'light' : 'dark';
}

export function themeMessage(background) {
  const hex = toHex(background) || '#111318';
  return { type: 'theme', background: hex, scheme: schemeFor(hex) };
}

// ---- status ----
const LIVE = { ok: 'connected', connected: 'connected', reconnecting: 'reconnecting', offline: 'offline' };

function heldSummary(item) {
  if (!item || typeof item !== 'object') return null;
  return { method: String(item.method || ''), host: String(item.host || ''), path: String(item.path || '') };
}

// latestHeld is the newest held message: the tail of each queue (both are kept
// in arrival order), preferring the higher id when both sides hold one.
export function latestHeld(queue, responseQueue) {
  const q = Array.isArray(queue) ? queue : [], r = Array.isArray(responseQueue) ? responseQueue : [];
  const a = q[q.length - 1], b = r[r.length - 1];
  if (a && b) return heldSummary((Number(b.id) || 0) > (Number(a.id) || 0) ? b : a);
  return heldSummary(a || b);
}

// statusMessage builds the contract's status message from the shell's raw
// sources: the project name, the SSE status ('ok' | 'reconnecting' |
// 'offline') and the intercept snapshot (state.intercept).
export function statusMessage({ project = '', live = 'ok', intercept = null } = {}) {
  const ic = intercept && typeof intercept === 'object' ? intercept : {};
  const queue = Array.isArray(ic.queue) ? ic.queue : [], rq = Array.isArray(ic.responseQueue) ? ic.responseQueue : [];
  return {
    type: 'status',
    project: String(project || ''),
    live: LIVE[live] || 'reconnecting',
    intercept: { request: !!ic.enabled, response: !!ic.responseEnabled },
    held: queue.length + rq.length,
    heldLatest: latestHeld(queue, rq),
  };
}

// statusKey is the dedupe identity: two snapshots with the same key are one change.
export function statusKey(msg) {
  const h = msg.heldLatest;
  return [msg.project, msg.live, msg.intercept.request ? 1 : 0, msg.intercept.response ? 1 : 0, msg.held,
    h ? h.method + ' ' + h.host + h.path : ''].join('\u0000');
}

export const STATUS_DEBOUNCE_MS = 150;

// createStatusReporter debounces status recomputation and posts only when the
// snapshot differs from the last one posted.
export function createStatusReporter({ read, post, delay = STATUS_DEBOUNCE_MS, setTimeout: st = globalThis.setTimeout, clearTimeout: ct = globalThis.clearTimeout }) {
  let timer = null, lastKey = null;
  function flush() {
    timer = null;
    let msg;
    try { msg = statusMessage(read()); } catch { return false; }
    const key = statusKey(msg);
    if (key === lastKey) return false;
    lastKey = key;
    post(msg);
    return true;
  }
  return {
    note() { if (timer !== null) ct(timer); timer = st(flush, delay); },
    flush() { if (timer !== null) { ct(timer); timer = null; } return flush(); },
  };
}

// ---- browser-chrome guards ----
// blockedKey reports whether a keydown is a browser reload or history shortcut
// that a desktop window must swallow. `editable` keeps text-editing chords
// (Cmd+Arrow, Option+Arrow on macOS, Backspace) working inside fields.
export function blockedKey(e, { mac = false, editable = false } = {}) {
  const key = String(e.key || '');
  const mod = mac ? !!e.metaKey : !!e.ctrlKey;
  const lower = key.toLowerCase();
  if (key === 'F5' || key === 'BrowserRefresh') return 'reload';
  if (mod && !e.altKey && lower === 'r') return 'reload';
  if (key === 'BrowserBack' || key === 'BrowserForward') return 'history';
  if (mod && !e.altKey && !e.shiftKey && (key === '[' || key === ']')) return 'history';
  if (e.altKey && !e.ctrlKey && !e.metaKey && (key === 'ArrowLeft' || key === 'ArrowRight')) {
    return mac && editable ? null : 'history';
  }
  if (mac && e.metaKey && !e.altKey && !e.ctrlKey && (key === 'ArrowLeft' || key === 'ArrowRight')) {
    return editable ? null : 'history';
  }
  if (key === 'Backspace' && !editable && !e.ctrlKey && !e.metaKey && !e.altKey) return 'history';
  return null;
}

// isNavigationKey reports a key that moves keyboard focus (Tab, arrows,
// Home/End, PageUp/PageDown, F6). Only these turn the desktop focus ring on.
const NAV_KEYS = new Set(['Tab', 'ArrowUp', 'ArrowDown', 'ArrowLeft', 'ArrowRight', 'Home', 'End', 'PageUp', 'PageDown', 'F6']);
export function isNavigationKey(e) {
  return !!e && NAV_KEYS.has(String(e.key || '')) && !e.metaKey && !e.ctrlKey;
}

// ---- status bar (desktop skin) ----
const plural = (n, one, many) => n + ' ' + (n === 1 ? one : many);
const count = (n) => (Number.isFinite(Number(n)) && Number(n) > 0 ? Math.floor(Number(n)) : 0);

// statusBarModel turns the shell's sources into the status bar's items (text,
// tone and accessible label). Input: {proxyAddr, intercept:{enabled,
// responseEnabled, held}, scope:{loaded, inCount, outCount}, flows, live,
// project, version, warnings:[{title, detail}]}. Pure, so it runs under node.
// interceptPillModel: the title-bar pill toggles request interception, so its
// pressed state follows Requests; its text says what is being held, so it never
// reads "off" while responses are held (the status bar says "on" then).
export function interceptPillModel(ic = {}) {
  const req = !!ic.enabled, resp = !!ic.responseEnabled;
  const held = Number(ic.held) > 0 ? Math.floor(Number(ic.held)) : 0;
  const tail = held ? ' \u00b7 ' + held + ' held' : '';
  if (ic.unknown) return { text: 'Intercept unknown', pressed: false, label: 'Request intercept state is unavailable. Toggle request intercept.', title: 'Request intercept state is unavailable. Open Intercept to retry.' };
  const text = (req ? 'Intercept on' : resp ? 'Responses on' : 'Intercept off') + tail;
  const also = req && resp ? ' Responses are also held.' : !req && resp ? ' Responses are held.' : '';
  return {
    text,
    pressed: req,
    label: 'Request intercept ' + (req ? 'on' : 'off') + (held ? ', ' + held + ' held' : '') + '.' + also + ' Toggle request intercept.',
    title: 'Request intercept is ' + (req ? 'on' : 'off') + (!req && resp ? '; responses are held' : '') + '. Click to turn it ' + (req ? 'off' : 'on') + '.',
  };
}

export function statusBarModel(s = {}) {
  const ic = s.intercept || {};
  const held = count(ic.held);
  const on = !!(ic.enabled || ic.responseEnabled);
  const sides = ic.enabled && ic.responseEnabled ? 'requests and responses' : ic.responseEnabled ? 'responses' : 'requests';
  const sc = s.scope || {};
  const inN = count(sc.inCount), outN = count(sc.outCount);
  const live = LIVE[s.live] || 'reconnecting';
  const proxy = String(s.proxyAddr || '').trim();
  const warnings = Array.isArray(s.warnings) ? s.warnings.filter((w) => w && w.title) : [];
  // Auth on with no password (a restored backup never carries it): the listener
  // answers every client with 407, so it is not shown as healthy.
  const authGap = !!s.proxyAuthPasswordMissing;
  if (authGap) warnings.unshift({ title: 'Proxy authentication has no password', detail: 'The password was not included in the backup. Every client gets 407 until you re-enter it in Settings → Proxy authentication.' });
  const flows = count(s.flows);
  return {
    proxy: { text: proxy || 'Proxy', tone: !proxy ? 'muted' : authGap ? 'warn' : 'ok',
      label: !proxy ? 'Proxy address loading. Open proxy settings.' : authGap ? 'Proxy listening on ' + proxy + ' but proxy authentication has no password. Open proxy settings.' : 'Proxy listening on ' + proxy + '. Open proxy settings.' },
    intercept: ic.unknown ? { text: 'Intercept unknown', tone: 'warn', label: 'Intercept state unavailable. Open Intercept to retry.' } : {
      text: on ? 'Intercept on' + (held ? ' · ' + held + ' held' : '') : held ? plural(held, 'held', 'held') : 'Intercept off',
      tone: on || held ? 'warn' : '',
      label: (on ? 'Intercepting ' + sides : 'Intercept off') + (held ? ', ' + plural(held, 'message', 'messages') + ' held' : '') + '. Open Intercept.',
    },
    scope: {
      text: !sc.loaded ? 'Scope …' : inN || outN ? 'Scope ' + inN + ' in · ' + outN + ' out' : 'No scope',
      tone: sc.loaded && !inN ? 'warn' : '',
      label: !sc.loaded ? 'Scope loading. Open scope settings.' : 'Scope: ' + plural(inN, 'include rule', 'include rules') + ', ' + plural(outN, 'exclude rule', 'exclude rules') + '. Open scope settings.',
    },
    flows: { text: plural(flows, 'flow', 'flows'), label: plural(flows, 'flow', 'flows') + ' captured. Open Proxy history.' },
    warnings: { hidden: warnings.length === 0, text: String(warnings.length), label: plural(warnings.length, 'warning', 'warnings') + '. Show details.', items: warnings },
    connection: {
      text: live === 'connected' ? 'Live' : live === 'offline' ? 'Offline' : 'Reconnecting…',
      dot: live === 'connected' ? 'ok' : live === 'offline' ? 'bad' : 'warn',
      label: 'Live updates: ' + live + '. Open connection details.',
    },
    project: { text: String(s.project || 'Project'), label: 'Project ' + String(s.project || '') + '. Switch or create a project.' },
    version: { text: String(s.version || ''), hidden: !s.version, label: 'Interseptor ' + String(s.version || '') + '. Open releases.' },
  };
}

// paletteShortcut is the command palette chord as the title toolbar shows it.
export function paletteShortcut(mac) {
  return mac ? ['⌘', 'K'] : ['Ctrl', 'K'];
}

// ---- unified title bar (native macOS shell) ----
// The native window draws no title bar of its own: the page's title toolbar
// sits under the traffic lights and is the window's drag handle. WKWebView
// ignores -webkit-app-region, so the page reports where a press drags the
// window ({type:'drag-regions'}) and the shell does the drag natively.

// DRAG_EXCLUDE matches the controls inside a drag region that keep their press.
export const DRAG_EXCLUDE = 'button,a[href],input,select,textarea,label,summary,[role="button"],[role="switch"],[role="tab"],[role="combobox"],[role="menuitem"],[contenteditable=""],[contenteditable="true"],[tabindex]:not([tabindex="-1"])';
export const MAX_DRAG_RECTS = 64;

function cleanRects(list) {
  const out = [];
  for (const r of Array.isArray(list) ? list : []) {
    if (!r) continue;
    const x = Number(r.x ?? r.left), y = Number(r.y ?? r.top), w = Number(r.w ?? r.width), h = Number(r.h ?? r.height);
    if (![x, y, w, h].every(Number.isFinite) || w <= 0 || h <= 0) continue;
    // Snap outward to whole pixels so a control's edge never drags.
    const x0 = Math.floor(x), y0 = Math.floor(y);
    out.push({ x: x0, y: y0, w: Math.ceil(x + w) - x0, h: Math.ceil(y + h) - y0 });
    if (out.length === MAX_DRAG_RECTS) break;
  }
  return out;
}

// dragRegionsMessage turns DOMRect-like boxes (drag areas, then the controls
// inside them) into the bridge message, in whole CSS pixels.
export function dragRegionsMessage(drag, exclude) {
  return { type: 'drag-regions', rects: cleanRects(drag), exclude: cleanRects(exclude) };
}

// OVERLAY marks the surfaces that can open over the title toolbar: modals
// (the command palette, dialogs, sheets), menus and popovers. Their opening or
// closing re-reports the drag regions, so a press on an overlay reaches it.
export const OVERLAY = '.modal-overlay,dialog,[role="dialog"],[role="alertdialog"],[role="menu"],[role="listbox"],[aria-modal="true"],[popover],.dsk-pop,.dsk-menu';
export const DRAG_SAMPLE_STEP = 8;

// uncoveredDragRects splits a drag area into the parts no overlay covers.
// clear[i] reports whether the column sampled at area.left + i*step showed the
// area itself (true) or something drawn over it (false). A run of clear
// columns becomes one rect between its outermost samples, extended to the
// area's edge when the run reaches it; a covered sample never drags.
export function uncoveredDragRects(area, clear, step = DRAG_SAMPLE_STEP) {
  const out = [];
  const left = Number(area.left ?? area.x), top = Number(area.top ?? area.y);
  const width = Number(area.width ?? area.w), height = Number(area.height ?? area.h);
  const right = left + width, n = Array.isArray(clear) ? clear.length : 0;
  let i = 0;
  while (i < n) {
    if (!clear[i]) { i++; continue; }
    let j = i;
    while (j + 1 < n && clear[j + 1]) j++;
    const x0 = i === 0 ? left : left + i * step;
    const x1 = j === n - 1 ? right : left + j * step;
    if (x1 > x0) out.push({ x: x0, y: top, w: x1 - x0, h: height });
    i = j + 1;
  }
  return out;
}

// dragRegionsKey is the dedupe identity of a drag-regions message.
export function dragRegionsKey(msg) {
  const k = (rs) => rs.map((r) => r.x + ',' + r.y + ',' + r.w + ',' + r.h).join(';');
  return k(msg.rects) + '|' + k(msg.exclude);
}

// fileAcceptMessage reports a file input's accept list just before its picker
// opens (WKOpenPanelParameters carries none): lower-case tokens, empties
// dropped. An input without accept sends an empty list (any file).
export function fileAcceptMessage(accept) {
  const tokens = String(accept || '').split(',').map((t) => t.trim().toLowerCase()).filter(Boolean).slice(0, 32);
  return { type: 'file-accept', accept: tokens };
}
