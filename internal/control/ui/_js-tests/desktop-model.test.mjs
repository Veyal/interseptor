import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  PANELS, parseCommand, findTargetFor, detectDesktop, parseColor, toHex, firstOpaqueHex, schemeFor,
  themeMessage, statusMessage, statusKey, latestHeld, createStatusReporter, blockedKey,
} from '../js/desktop-model.js';

test('parseCommand routes every panel and command in the contract', () => {
  for (const p of PANELS) assert.deepEqual(parseCommand('panel:' + p), { action: 'panel', arg: p });
  assert.equal(PANELS.length, 12);
  assert.deepEqual(parseCommand('intercept:toggle-request'), { action: 'toggleRequestIntercept', arg: null });
  assert.deepEqual(parseCommand('intercept:toggle-response'), { action: 'toggleResponseIntercept', arg: null });
  assert.deepEqual(parseCommand('palette:open'), { action: 'openPalette', arg: null });
  assert.deepEqual(parseCommand('project:switch'), { action: 'openProject', arg: null });
  assert.deepEqual(parseCommand('find'), { action: 'find', arg: null });
  assert.deepEqual(parseCommand('theme:toggle'), { action: 'toggleTheme', arg: null });
});

test('parseCommand rejects anything outside the contract', () => {
  for (const bad of ['panel:', 'panel:discover', 'panel:Proxy', 'reload', '', 'toString', 'constructor', '__proto__', null, 42, {}, 'intercept:toggle']) {
    assert.equal(parseCommand(bad), null, String(bad));
  }
});

test('findTargetFor uses the panel search or falls back to History search', () => {
  assert.deepEqual(findTargetFor('findings'), { panel: 'findings', selector: '#findSearch' });
  assert.deepEqual(findTargetFor('settings'), { panel: 'settings', selector: '#setSearch' });
  for (const p of ['repeater', 'intruder', 'notes', 'intercept', '']) assert.deepEqual(findTargetFor(p), { panel: 'proxy', selector: '#fSearch' });
});

test('detectDesktop: native host, Chrome app window, plain tab', () => {
  const host = { platform: 'macos', version: '1', post() {} };
  assert.deepEqual(detectDesktop({ host, standalone: true }), { platform: 'macos', classes: ['desktop-app', 'desktop-macos'], bridge: true });
  assert.deepEqual(detectDesktop({ standalone: true, finePointer: true }), { platform: 'chromeapp', classes: ['desktop-app', 'desktop-chromeapp'], bridge: false });
  // Review regression: a phone that installs the UI (Add to Home Screen) also runs
  // standalone, but has a touch pointer and must keep the web layout.
  assert.equal(detectDesktop({ standalone: true, finePointer: false }), null);
  assert.equal(detectDesktop({ standalone: true }), null, 'pointer unknown is not a desktop window');
  assert.equal(detectDesktop({}), null);
  assert.equal(detectDesktop({ host: { platform: 'macos' } }), null, 'a host without post is not a bridge');
  assert.equal(detectDesktop({ host: { platform: '<x y>', post() {} } }).platform, 'native');
});

test('colour conversion handles computed colour forms', () => {
  assert.equal(toHex('rgb(23, 26, 32)'), '#171a20');
  assert.equal(toHex('rgb(23 26 32)'), '#171a20');
  assert.equal(toHex('rgba(255, 255, 255, 1)'), '#ffffff');
  assert.equal(toHex('rgba(0, 0, 0, 0)'), null);
  assert.equal(toHex('rgb(0 0 0 / 50%)'), null);
  assert.equal(toHex('#FFF'), '#ffffff');
  assert.equal(toHex('#0a0B0c'), '#0a0b0c');
  assert.equal(toHex('#0a0b0c80'), null);
  assert.equal(toHex('color(srgb 1 0.5 0)'), '#ff8000');
  assert.equal(toHex('transparent'), null);
  assert.equal(toHex(''), null);
  assert.deepEqual(parseColor('rgba(1,2,3,0.5)'), { r: 1, g: 2, b: 3, a: 0.5 });
  assert.equal(firstOpaqueHex(['rgba(0, 0, 0, 0)', 'rgb(247, 248, 250)']), '#f7f8fa');
  assert.equal(firstOpaqueHex(['transparent'], '#000000'), '#000000');
});

test('schemeFor and themeMessage classify by luminance', () => {
  assert.equal(schemeFor('#171a20'), 'dark');
  assert.equal(schemeFor('#000000'), 'dark');
  assert.equal(schemeFor('#f7f8fa'), 'light');
  assert.deepEqual(themeMessage('rgb(247, 248, 250)'), { type: 'theme', background: '#f7f8fa', scheme: 'light' });
  assert.deepEqual(themeMessage('rgb(23, 26, 32)'), { type: 'theme', background: '#171a20', scheme: 'dark' });
});

test('statusMessage maps shell sources to the contract shape', () => {
  assert.deepEqual(statusMessage(), { type: 'status', project: '', live: 'connected', intercept: { request: false, response: false }, held: 0, heldLatest: null });
  const ic = {
    enabled: true, responseEnabled: false,
    queue: [{ id: 3, method: 'GET', host: 'example.com', path: '/a', extra: 'x' }, { id: 7, method: 'POST', host: 'example.com', path: '/login' }],
    responseQueue: [{ id: 5, method: 'GET', host: 'api.example.com', path: '/r' }],
  };
  assert.deepEqual(statusMessage({ project: 'demo', live: 'reconnecting', intercept: ic }), {
    type: 'status', project: 'demo', live: 'reconnecting', intercept: { request: true, response: false }, held: 3,
    heldLatest: { method: 'POST', host: 'example.com', path: '/login' },
  });
  assert.equal(statusMessage({ live: 'offline' }).live, 'offline');
  assert.equal(statusMessage({ live: 'weird' }).live, 'reconnecting');
});

test('latestHeld prefers the newer tail of either queue', () => {
  assert.equal(latestHeld([], []), null);
  assert.deepEqual(latestHeld([], [{ id: 1, method: 'GET', host: 'h', path: '/' }]), { method: 'GET', host: 'h', path: '/' });
  assert.deepEqual(latestHeld([{ id: 2, method: 'GET', host: 'a', path: '/' }], [{ id: 9, method: 'PUT', host: 'b', path: '/x' }]).host, 'b');
});

test('statusKey changes with every field the native side shows', () => {
  const base = statusMessage({ project: 'p', intercept: { enabled: false } });
  const k = statusKey(base);
  assert.equal(statusKey(statusMessage({ project: 'p', intercept: { enabled: false } })), k);
  assert.notEqual(statusKey(statusMessage({ project: 'q' })), k);
  assert.notEqual(statusKey(statusMessage({ project: 'p', live: 'offline' })), k);
  assert.notEqual(statusKey(statusMessage({ project: 'p', intercept: { enabled: true } })), k);
  assert.notEqual(statusKey(statusMessage({ project: 'p', intercept: { responseEnabled: true } })), k);
  assert.notEqual(statusKey(statusMessage({ project: 'p', intercept: { queue: [{ id: 1, method: 'GET', host: 'h', path: '/' }] } })), k);
});

function fakeTimers() {
  let now = 0, seq = 0; const timers = new Map();
  return {
    setTimeout(fn, ms) { const id = ++seq; timers.set(id, { fn, at: now + ms }); return id; },
    clearTimeout(id) { timers.delete(id); },
    advance(ms) {
      now += ms;
      for (const [id, t] of [...timers]) if (t.at <= now) { timers.delete(id); t.fn(); }
    },
    get pending() { return timers.size; },
  };
}

test('createStatusReporter debounces and posts only on change', () => {
  const t = fakeTimers();
  let src = { project: 'demo', live: 'ok', intercept: { enabled: false } };
  const posted = [];
  const r = createStatusReporter({ read: () => src, post: (m) => posted.push(m), delay: 150, setTimeout: t.setTimeout, clearTimeout: t.clearTimeout });
  r.note(); r.note(); r.note();
  assert.equal(t.pending, 1, 'a burst keeps one timer');
  t.advance(149); assert.equal(posted.length, 0);
  t.advance(1); assert.equal(posted.length, 1);
  r.note(); t.advance(150); assert.equal(posted.length, 1, 'unchanged snapshot is deduped');
  src = { ...src, intercept: { enabled: true } };
  r.note(); t.advance(150); assert.equal(posted.length, 2);
  assert.equal(posted[1].intercept.request, true);
  src = { ...src, live: 'offline' };
  assert.equal(r.flush(), true, 'flush posts at once');
  assert.equal(posted[2].live, 'offline');
  const quiet = createStatusReporter({ read: () => { throw new Error('boom'); }, post: () => assert.fail('must not post'), setTimeout: t.setTimeout, clearTimeout: t.clearTimeout });
  assert.equal(quiet.flush(), false);
});

test('blockedKey swallows reload and history keys but keeps text editing', () => {
  const k = (key, mods = {}) => ({ key, ctrlKey: false, metaKey: false, altKey: false, shiftKey: false, ...mods });
  const mac = { mac: true }, win = { mac: false };
  assert.equal(blockedKey(k('r', { metaKey: true }), mac), 'reload');
  assert.equal(blockedKey(k('R', { metaKey: true, shiftKey: true }), mac), 'reload');
  assert.equal(blockedKey(k('r', { ctrlKey: true }), win), 'reload');
  assert.equal(blockedKey(k('r', { ctrlKey: true }), mac), null, 'Ctrl+R is not reload on macOS');
  assert.equal(blockedKey(k('F5'), win), 'reload');
  assert.equal(blockedKey(k('[', { metaKey: true }), mac), 'history');
  assert.equal(blockedKey(k(']', { metaKey: true }), mac), 'history');
  assert.equal(blockedKey(k('ArrowLeft', { altKey: true }), win), 'history');
  assert.equal(blockedKey(k('ArrowLeft', { altKey: true }), { mac: true, editable: true }), null, 'Option+Left moves by word');
  assert.equal(blockedKey(k('ArrowLeft', { metaKey: true }), mac), 'history');
  assert.equal(blockedKey(k('ArrowLeft', { metaKey: true }), { mac: true, editable: true }), null, 'Cmd+Left goes to line start');
  assert.equal(blockedKey(k('Backspace'), win), 'history');
  assert.equal(blockedKey(k('Backspace'), { editable: true }), null);
  assert.equal(blockedKey(k('BrowserBack'), win), 'history');
  for (const ok of [k('k', { metaKey: true }), k('f', { metaKey: true }), k('ArrowLeft'), k('r'), k('Enter', { metaKey: true })]) {
    assert.equal(blockedKey(ok, mac), null, JSON.stringify(ok));
  }
});

test('dragRegionsMessage: whole CSS pixels, snapped outward, empties dropped, capped', async () => {
  const { dragRegionsMessage, dragRegionsKey, MAX_DRAG_RECTS } = await import('../js/desktop-model.js');
  const m = dragRegionsMessage([{ left: 0, top: 0, width: 1280.4, height: 38 }], [{ x: 99.6, y: 5.2, w: 80.1, h: 28 }, { x: 1, y: 1, w: 0, h: 5 }, null, { x: NaN, y: 0, w: 1, h: 1 }]);
  assert.equal(m.type, 'drag-regions');
  assert.deepEqual(m.rects, [{ x: 0, y: 0, w: 1281, h: 38 }]);
  assert.deepEqual(m.exclude, [{ x: 99, y: 5, w: 81, h: 29 }], 'a control keeps its edges');
  assert.equal(dragRegionsKey(m), dragRegionsKey(dragRegionsMessage([{ x: 0, y: 0, w: 1280.4, h: 38 }], [{ x: 99.6, y: 5.2, w: 80.1, h: 28 }])));
  const many = Array.from({ length: 100 }, (_, i) => ({ x: i, y: 0, w: 1, h: 1 }));
  assert.equal(dragRegionsMessage(many, many).exclude.length, MAX_DRAG_RECTS);
  assert.deepEqual(dragRegionsMessage(null, undefined), { type: 'drag-regions', rects: [], exclude: [] });
});

test('fileAcceptMessage: lower-case trimmed tokens; no accept means any file', async () => {
  const { fileAcceptMessage } = await import('../js/desktop-model.js');
  assert.deepEqual(fileAcceptMessage('.har, application/JSON,,'), { type: 'file-accept', accept: ['.har', 'application/json'] });
  assert.deepEqual(fileAcceptMessage('.tar.gz,application/gzip').accept, ['.tar.gz', 'application/gzip']);
  assert.deepEqual(fileAcceptMessage('').accept, []);
  assert.deepEqual(fileAcceptMessage(undefined).accept, []);
});

test('uncoveredDragRects: clear runs become rects; covered samples never drag', async () => {
  const { uncoveredDragRects } = await import('../js/desktop-model.js');
  const area = { left: 0, top: 0, width: 40, height: 38 }; // samples at 0, 8, 16, 24, 32
  assert.deepEqual(uncoveredDragRects(area, [true, true, true, true, true]), [{ x: 0, y: 0, w: 40, h: 38 }]);
  assert.deepEqual(uncoveredDragRects(area, [false, false, false, false, false]), [], 'a full-window modal');
  assert.deepEqual(uncoveredDragRects(area, [true, true, false, true, true]), [{ x: 0, y: 0, w: 8, h: 38 }, { x: 24, y: 0, w: 16, h: 38 }]);
  assert.deepEqual(uncoveredDragRects(area, [false, true, true, true, false]), [{ x: 8, y: 0, w: 16, h: 38 }]);
  assert.deepEqual(uncoveredDragRects(area, [false, true, false, false, false]), [], 'a lone clear sample has no width');
  assert.deepEqual(uncoveredDragRects({ x: 10, y: 2, w: 16, h: 30 }, [true, true], 8), [{ x: 10, y: 2, w: 16, h: 30 }]);
  assert.deepEqual(uncoveredDragRects(area, null), []);
});
