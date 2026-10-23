// desktop.js against a minimal fake DOM: a plain browser tab gets nothing but
// the theme-color sync, and a native host gets the bridge contract.
import { test } from 'node:test';
import assert from 'node:assert/strict';

class FakeElement {
  constructor(tag, attrs = {}) { this.tag = tag; this.attrs = { ...attrs }; this.bg = ''; this.disabled = false; }
  getAttribute(n) { return n in this.attrs ? this.attrs[n] : null; }
  setAttribute(n, v) { this.attrs[n] = String(v); }
  closest() { return null; }
  focus() { globalThis.document.activeElement = this; }
  select() {}
}

function installDom({ standalone = false, host = undefined, finePointer = true } = {}) {
  const classes = new Set();
  const listeners = [];
  const handlers = {};
  const observers = [];
  const dispatched = [];
  const meta = new FakeElement('meta', { name: 'theme-color', content: '#171a20' });
  const bar = new FakeElement('header'); bar.bg = 'rgb(23, 26, 32)';
  const search = new FakeElement('input');
  const root = new FakeElement('html');
  root.classList = { add: (...c) => c.forEach((x) => classes.add(x)), remove: (...c) => c.forEach((x) => classes.delete(x)), contains: (c) => classes.has(c) };
  const document = {
    documentElement: root, body: new FakeElement('body'), activeElement: null,
    getElementById: (id) => (id === 'bar' ? bar : null),
    querySelector: (sel) => (sel === 'meta[name="theme-color"]' ? meta : sel === '#findSearch' || sel === '#fSearch' ? search : null),
  };
  const window = {
    document,
    interseptorDesktop: host,
    matchMedia: (q) => ({ matches: (standalone && q === '(display-mode: standalone)') || (finePointer && q === '(pointer: fine)') }),
    addEventListener: (type, fn) => { listeners.push(type); (handlers[type] ||= []).push(fn); },
    dispatchEvent: (e) => { dispatched.push(e.type); return true; },
  };
  Object.assign(globalThis, {
    window, document, Element: FakeElement,
    getComputedStyle: (el) => ({ backgroundColor: el.bg || 'rgba(0, 0, 0, 0)' }),
    MutationObserver: class { constructor(fn) { this.fn = fn; observers.push(this); } observe() {} },
  });
  return { classes, listeners, handlers, dispatched, meta, bar, search, observers, fire: () => observers.forEach((o) => o.fn([])) };
}

test('plain browser tab: no classes, no guards, no bridge; theme-color still syncs', async () => {
  const dom = installDom();
  dom.bar.bg = 'rgb(247, 248, 250)';
  const m = await import('../js/desktop.js?case=tab');
  assert.equal(m.initDesktop({ actions: { panel: () => assert.fail('no command may run') } }), null);
  assert.equal(dom.classes.size, 0);
  assert.deepEqual(dom.listeners, []);
  assert.equal(m.isDesktopApp(), false);
  assert.equal(dom.meta.getAttribute('content'), '#f7f8fa');
  m.noteDesktopStatus(); m.desktopReady();
  assert.equal(m.runCommand('panel:proxy'), false, 'commands need a desktop host');
});

test('Chrome --app window: desktop classes and guards, no bridge', async () => {
  const dom = installDom({ standalone: true });
  const m = await import('../js/desktop.js?case=chromeapp');
  const d = m.initDesktop();
  assert.equal(d.platform, 'chromeapp');
  assert.ok(dom.classes.has('desktop-app') && dom.classes.has('desktop-chromeapp'));
  for (const type of ['keydown', 'wheel', 'dragstart', 'gesturestart', 'mousedown', 'mouseup', 'pointerdown']) assert.ok(dom.listeners.includes(type), type);
});

test('keyboard modality: navigation keys show the ring, a pointer press or a native command hides it', async () => {
  const host = { platform: 'macos', version: '0', post: () => {} };
  const dom = installDom({ host });
  const m = await import('../js/desktop.js?case=modality');
  m.initDesktop({ actions: { panel: () => true } });
  const fire = (type, e = {}) => dom.handlers[type].forEach((fn) => fn({ preventDefault() {}, target: null, ...e }));
  fire('keydown', { key: 'Enter' });
  assert.equal(dom.classes.has('dsk-kbd'), false, 'a non-navigation key leaves the ring off');
  fire('keydown', { key: 'Tab' });
  assert.equal(dom.classes.has('dsk-kbd'), true, 'Tab turns the ring on');
  assert.equal(host.run('panel:proxy'), true);
  assert.equal(dom.classes.has('dsk-kbd'), false, 'a menu command never leaves a ring on the focused control');
  fire('keydown', { key: 'ArrowDown' });
  assert.equal(dom.classes.has('dsk-kbd'), true);
  fire('pointerdown');
  assert.equal(dom.classes.has('dsk-kbd'), false, 'a pointer press clears keyboard mode');
});

test('native host: ready, theme and deduped status messages; run() routes commands', async () => {
  const posted = [];
  const host = { platform: 'macos', version: '0', post: (msg) => posted.push(msg) };
  const dom = installDom({ host });
  const m = await import('../js/desktop.js?case=macos');
  const calls = [];
  let status = { project: 'demo', live: 'ok', intercept: { enabled: false, queue: [] } };
  let active = 'findings';
  m.initDesktop({
    readStatus: () => status,
    activePanel: () => active,
    actions: {
      panel: (name) => { calls.push('panel:' + name); active = name; return true; },
      toggleRequestIntercept: () => { calls.push('req'); return true; },
      toggleResponseIntercept: () => false,
      openPalette: () => true,
      openProject: () => true,
      toggleTheme: () => { dom.bar.bg = 'rgb(247, 248, 250)'; dom.fire(); return true; },
    },
  });
  assert.ok(dom.classes.has('desktop-app') && dom.classes.has('desktop-macos'));
  assert.equal(typeof host.run, 'function');
  assert.deepEqual(posted.shift(), { type: 'theme', background: '#171a20', scheme: 'dark' });

  m.desktopReady(); m.desktopReady();
  assert.deepEqual(posted.shift(), { type: 'ready' });
  assert.deepEqual(posted.shift(), { type: 'status', project: 'demo', live: 'connected', intercept: { request: false, response: false }, held: 0, heldLatest: null });
  assert.equal(posted.length, 0, 'ready is sent once');

  assert.equal(host.run('panel:findings'), true);
  assert.equal(host.run('intercept:toggle-request'), true);
  assert.equal(host.run('intercept:toggle-response'), false, 'an unavailable action reports false');
  assert.equal(host.run('palette:open'), true);
  assert.equal(host.run('project:switch'), true);
  assert.equal(host.run('nope'), false);
  assert.equal(host.run('panel:discover'), false);
  assert.deepEqual(calls, ['panel:findings', 'req']);

  assert.equal(host.run('find'), true, 'Findings focuses its own search');
  assert.equal(globalThis.document.activeElement, dom.search);
  active = 'repeater';
  assert.equal(host.run('find'), true, 'Repeater falls back to History search');
  assert.equal(calls.at(-1), 'panel:proxy');

  assert.equal(host.run('theme:toggle'), true);
  assert.deepEqual(posted.shift(), { type: 'theme', background: '#f7f8fa', scheme: 'light' });
  assert.equal(dom.meta.getAttribute('content'), '#f7f8fa');
  dom.fire();
  assert.equal(posted.length, 0, 'an unchanged theme is not re-sent');

  status = { ...status, intercept: { enabled: true, queue: [{ id: 4, method: 'GET', host: 'example.com', path: '/' }] } };
  m.noteDesktopStatus(); m.noteDesktopStatus();
  await new Promise((r) => setTimeout(r, 200));
  assert.equal(posted.length, 1, 'a burst posts once');
  assert.deepEqual(posted.shift().heldLatest, { method: 'GET', host: 'example.com', path: '/' });
  m.noteDesktopStatus();
  await new Promise((r) => setTimeout(r, 200));
  assert.equal(posted.length, 0, 'no change, no message');
});

test('a throwing host post never breaks the page', async () => {
  installDom({ host: { platform: 'macos', post: () => { throw new Error('gone'); } } });
  const m = await import('../js/desktop.js?case=throws');
  assert.doesNotThrow(() => { m.initDesktop(); m.desktopReady(); });
});

test('unified title bar: the Flight Bar drags the window except over its controls; file inputs report accept', async () => {
  const posted = [];
  const host = { platform: 'macos', version: '0', titlebar: 'unified', post: (msg) => posted.push(msg) };
  const dom = installDom({ host });
  const rect = (x, y, w, h) => ({ left: x, top: y, right: x + w, bottom: y + h, width: w, height: h });
  const button = new FakeElement('button'); button.box = rect(400, 5, 360, 28);
  const hidden = new FakeElement('button'); hidden.box = rect(0, 0, 0, 0);
  const below = new FakeElement('button'); below.box = rect(900, 60, 40, 28); // an open popover's button, under the bar
  dom.bar.box = rect(0, 0, 1280, 38);
  for (const el of [button, hidden, below, dom.bar]) el.getBoundingClientRect = function () { return this.box; };
  dom.bar.querySelectorAll = (sel) => (sel === '*' ? [button, hidden, below] : [button, hidden, below]);
  globalThis.document.querySelectorAll = (sel) => (sel === '[data-window-drag]' && 'data-window-drag' in dom.bar.attrs ? [dom.bar] : []);
  // Hit testing: the bar (or its button) unless an overlay covers x in [cover0, cover1).
  let cover = null;
  const overlay = new FakeElement('div');
  dom.bar.contains = (el) => el === dom.bar || el === button;
  globalThis.document.elementFromPoint = (x, y) => (cover && x >= cover[0] && x < cover[1] ? overlay : x >= 400 && x < 760 && y >= 5 && y < 33 ? button : dom.bar);
  const frames = [];
  const observed = [];
  Object.assign(globalThis, {
    requestAnimationFrame: (fn) => { frames.push(fn); return frames.length; },
    ResizeObserver: class { constructor(fn) { this.fn = fn; } observe(el) { observed.push(el); } disconnect() {} },
  });
  class Input extends FakeElement { matches(s) { return s === 'input[type="file"]' && this.type === 'file'; } click() { return 'native'; } }
  globalThis.HTMLInputElement = Input;
  const m = await import('../js/desktop.js?case=unified');
  m.initDesktop();
  assert.equal(dom.bar.getAttribute('data-window-drag'), '', 'the Flight Bar is the drag handle');
  assert.ok(observed.includes(dom.bar) && observed.includes(button), 'layout changes of the bar and its controls re-report');
  posted.length = 0;
  frames.splice(0).forEach((fn) => fn());
  assert.deepEqual(posted, [{ type: 'drag-regions', rects: [{ x: 0, y: 0, w: 1280, h: 38 }], exclude: [{ x: 400, y: 5, w: 360, h: 28 }] }]);
  dom.handlers.resize.forEach((fn) => fn());
  frames.splice(0).forEach((fn) => fn());
  assert.equal(posted.length, 1, 'an unchanged layout is not re-sent');

  // A modal over the whole window: no drag region at all, so presses reach it.
  cover = [0, 1280];
  dom.handlers.resize.forEach((fn) => fn());
  frames.splice(0).forEach((fn) => fn());
  assert.deepEqual(posted.pop(), { type: 'drag-regions', rects: [], exclude: [] });
  // A popover over the right part: only the uncovered left part drags.
  cover = [1000, 1280];
  dom.handlers.resize.forEach((fn) => fn());
  frames.splice(0).forEach((fn) => fn());
  const partial = posted.pop();
  assert.deepEqual(partial.rects, [{ x: 0, y: 0, w: 992, h: 38 }]);
  assert.deepEqual(partial.exclude, [{ x: 400, y: 5, w: 360, h: 28 }]);
  cover = null;
  dom.handlers.resize.forEach((fn) => fn());
  frames.splice(0).forEach((fn) => fn());
  assert.deepEqual(posted.pop().rects, [{ x: 0, y: 0, w: 1280, h: 38 }], 'closing the overlay restores the region');

  const input = new Input('input'); input.type = 'file'; input.accept = '.har,application/json';
  assert.equal(input.click(), 'native', 'script-opened pickers still open');
  assert.deepEqual(posted.pop(), { type: 'file-accept', accept: ['.har', 'application/json'] });
  dom.handlers.click.forEach((fn) => fn({ target: input }));
  assert.deepEqual(posted.pop(), { type: 'file-accept', accept: ['.har', 'application/json'] });
  delete globalThis.HTMLInputElement;
});

test('a native host without a unified title bar reports no drag regions', async () => {
  const posted = [];
  const dom = installDom({ host: { platform: 'macos', post: (msg) => posted.push(msg) } });
  const m = await import('../js/desktop.js?case=classic-titlebar');
  m.initDesktop();
  assert.equal(dom.bar.getAttribute('data-window-drag'), null);
  assert.ok(!posted.some((p) => p.type === 'drag-regions'));
});

test('the desktop skin re-announces density: its 28px --row-h must reach lists that measured the web 30px', async () => {
  const tab = installDom();
  const t = await import('../js/desktop.js?case=density-tab');
  t.initDesktop();
  assert.deepEqual(tab.dispatched, [], 'a browser tab keeps its tokens');
  const dom = installDom({ host: { platform: 'macos', version: '0', post: () => {} } });
  const m = await import('../js/desktop.js?case=density');
  m.initDesktop();
  assert.ok(dom.classes.has('desktop-app'));
  assert.deepEqual(dom.dispatched, ['densitychange']);
});

test('keyboard modality only touches the <html> class when the state changes', async () => {
  const host = { platform: 'macos', version: '0', post: () => {} };
  const dom = installDom({ host });
  const root = globalThis.document.documentElement;
  const m = await import('../js/desktop.js?case=kbd-writes');
  m.initDesktop({ actions: { panel: () => true } });
  let writes = 0;
  const { add, remove } = root.classList;
  root.classList.add = (...c) => { writes++; add(...c); };
  root.classList.remove = (...c) => { writes++; remove(...c); };
  const fire = (type, e = {}) => dom.handlers[type].forEach((fn) => fn({ preventDefault() {}, target: null, ...e }));
  fire('pointerdown'); fire('pointerdown'); fire('pointerdown');
  assert.equal(writes, 0, 'clicks with the ring already off write nothing');
  fire('keydown', { key: 'ArrowDown' }); fire('keydown', { key: 'ArrowDown' }); fire('keydown', { key: 'Tab' });
  assert.equal(writes, 1, 'repeated navigation keys write once');
  fire('pointerdown');
  assert.equal(writes, 2);
});

test('Ctrl+wheel blocker is installed for the Chrome --app window only, not the native shell', async () => {
  const native = installDom({ host: { platform: 'macos', version: '0', post: () => {} } });
  const m1 = await import('../js/desktop.js?case=wheel-native');
  m1.initDesktop();
  assert.equal(native.listeners.includes('wheel'), false, 'WKWebView already has magnification off');
  const chrome = installDom({ standalone: true });
  const m2 = await import('../js/desktop.js?case=wheel-chrome');
  m2.initDesktop();
  assert.equal(chrome.listeners.includes('wheel'), true);
});
