// desktop-shell.js and the desktop-skin rules in desktop-model.js: the status bar
// model, the palette chord, keyboard modality, and the chrome being built only
// in a desktop host (a browser tab gets nothing).
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { statusBarModel, paletteShortcut, isNavigationKey } from '../js/desktop-model.js';

test('statusBarModel: idle, intercepting, scope and connection states', () => {
  const idle = statusBarModel({ proxyAddr: '127.0.0.1:8080', intercept: {}, scope: { loaded: true, inCount: 0, outCount: 0 }, flows: 0, live: 'ok', project: 'demo', version: 'v2.16.0' });
  assert.equal(idle.proxy.text, '127.0.0.1:8080');
  assert.equal(idle.intercept.text, 'Intercept off');
  assert.equal(idle.intercept.tone, '');
  assert.equal(idle.scope.text, 'No scope');
  assert.equal(idle.scope.tone, 'warn');
  assert.equal(idle.flows.text, '0 flows');
  assert.equal(idle.warnings.hidden, true);
  assert.deepEqual([idle.connection.text, idle.connection.dot], ['Live', 'ok']);
  assert.equal(idle.version.hidden, false);

  const busy = statusBarModel({ intercept: { enabled: true, responseEnabled: true, held: 2 }, scope: { loaded: true, inCount: 1, outCount: 1 }, flows: 1, live: 'offline', warnings: [{ title: 'No HTTPS', detail: 'x' }, { title: '' }] });
  assert.equal(busy.intercept.text, 'Intercept on · 2 held');
  assert.equal(busy.intercept.tone, 'warn');
  assert.match(busy.intercept.label, /requests and responses, 2 messages held/);
  assert.equal(busy.scope.text, 'Scope 1 in · 1 out');
  assert.equal(busy.flows.text, '1 flow');
  assert.equal(busy.warnings.hidden, false);
  assert.equal(busy.warnings.items.length, 1, 'untitled warnings are dropped');
  assert.deepEqual([busy.connection.text, busy.connection.dot], ['Offline', 'bad']);
  assert.equal(busy.proxy.tone, 'muted', 'no address yet');
  assert.equal(busy.version.hidden, true);

  const held = statusBarModel({ intercept: { held: 1 }, scope: { loaded: false }, live: 'reconnecting' });
  assert.equal(held.intercept.text, '1 held', 'intercept off but a message is still held');
  assert.equal(held.scope.text, 'Scope …');
  assert.equal(held.connection.dot, 'warn');
});

test('paletteShortcut names the platform chord', () => {
  assert.deepEqual(paletteShortcut(true), ['⌘', 'K']);
  assert.deepEqual(paletteShortcut(false), ['Ctrl', 'K']);
});

test('isNavigationKey: only focus-moving keys switch the ring on', () => {
  for (const key of ['Tab', 'ArrowDown', 'Home', 'PageUp', 'F6']) assert.equal(isNavigationKey({ key }), true, key);
  for (const key of ['Enter', 'a', 'Escape', ' ']) assert.equal(isNavigationKey({ key }), false, key);
  assert.equal(isNavigationKey({ key: 'Tab', metaKey: true }), false, 'Cmd+Tab is the app switcher');
  assert.equal(isNavigationKey(null), false);
});

// ---- a minimal DOM for initDesktopShell ----
class Node {
  constructor(tag) { this.tagName = String(tag).toUpperCase(); this.children = []; this.attrs = {}; this.dataset = {}; this.hidden = false; this.listeners = {}; this._text = ''; this.parentNode = null; this.className = ''; this.style = {}; this.title = ''; }
  get id() { return this.attrs.id || ''; }
  set id(v) { this.attrs.id = v; }
  get textContent() { return this._text + this.children.map((c) => c.textContent).join(''); }
  set textContent(v) { this._text = String(v); this.children = []; }
  setAttribute(n, v) { this.attrs[n] = String(v); if (n === 'class') this.className = String(v); }
  getAttribute(n) { return n in this.attrs ? this.attrs[n] : null; }
  hasAttribute(n) { return n in this.attrs; }
  appendChild(c) { if (c.parentNode) c.parentNode.children = c.parentNode.children.filter((x) => x !== c); c.parentNode = this; this.children.push(c); return c; }
  append(...cs) { cs.forEach((c) => this.appendChild(c)); }
  insertBefore(c, ref) { this.appendChild(c); this.children.pop(); const i = this.children.indexOf(ref); this.children.splice(i < 0 ? this.children.length : i, 0, c); return c; }
  addEventListener(type, fn) { (this.listeners[type] ||= []).push(fn); }
  fire(type) { (this.listeners[type] || []).forEach((fn) => fn({ target: this, preventDefault() {} })); }
  contains(n) { for (let x = n; x; x = x.parentNode) if (x === this) return true; return false; }
  querySelector(sel) { return this.querySelectorAll(sel)[0] || null; }
  querySelectorAll(sel) { const out = []; const walk = (n) => { n.children.forEach((c) => { if (match(c, sel)) out.push(c); walk(c); }); }; walk(this); return out; }
  focus() {}
  getBoundingClientRect() { return { right: 1200, left: 1100, top: 880, bottom: 900 }; }
}
function match(n, sel) {
  const last = sel.trim().split(/\s+/).pop();
  if (last.startsWith('#')) return n.id === last.slice(1);
  if (last.startsWith('.')) return n.className.split(/\s+/).includes(last.slice(1)) || (n.attrs.class || '').split(/\s+/).includes(last.slice(1));
  if (last.startsWith('[data-sb=')) return n.dataset.sb === last.slice(9, -1);
  return n.tagName === last.toUpperCase();
}

function installDom({ desktop }) {
  const classes = new Set(desktop ? ['desktop-app', 'desktop-macos'] : []);
  const html = new Node('html');
  html.classList = { contains: (c) => classes.has(c), add: (c) => classes.add(c), remove: (c) => classes.delete(c) };
  const body = new Node('body'); html.appendChild(body);
  const bar = new Node('header'); bar.id = 'bar'; body.appendChild(bar);
  const start = new Node('div'); start.className = 'fb-zone fb-start'; bar.appendChild(start);
  const cmdk = new Node('button'); cmdk.id = 'cmdkBtn'; bar.appendChild(cmdk);
  const k1 = new Node('kbd'); k1.textContent = 'Ctrl/⌘'; const k2 = new Node('kbd'); k2.textContent = 'K'; cmdk.append(k1, k2);
  const end = new Node('div'); end.className = 'fb-zone fb-end'; bar.appendChild(end);
  const chip = new Node('button'); chip.id = 'mobileProjectChip'; end.appendChild(chip);
  const ring = new Node('button'); ring.id = 'ctxRing'; end.appendChild(ring);
  const document = {
    documentElement: html, body, hidden: false, listeners: {},
    createElement: (t) => new Node(t), createElementNS: (_, t) => new Node(t),
    getElementById: (id) => html.querySelector('#' + id),
    querySelector: (s) => html.querySelector(s), querySelectorAll: (s) => html.querySelectorAll(s),
    addEventListener(type, fn) { (this.listeners[type] ||= []).push(fn); },
  };
  globalThis.document = document;
  globalThis.window = { innerWidth: 1440, addEventListener() {} };
  return { html, body, bar, start, end, chip, ring, k1, k2 };
}

test('a browser tab gets no desktop chrome', async () => {
  const dom = installDom({ desktop: false });
  const { initDesktopShell } = await import('../js/desktop-shell.js?case=tab');
  assert.equal(initDesktopShell({ read: () => assert.fail('nothing reads in a tab') }), false);
  assert.equal(dom.body.querySelector('#dskStatus'), null);
  assert.equal(dom.k1.textContent, 'Ctrl/⌘', 'the web markup is untouched');
  assert.equal(dom.end.contains(dom.chip), true);
});

test('a desktop host gets the status bar, the pill and the macOS chord', async () => {
  const dom = installDom({ desktop: true });
  const realSetInterval = globalThis.setInterval;
  globalThis.setInterval = () => 0;
  try {
    const { initDesktopShell, refresh } = await import('../js/desktop-shell.js?case=desktop');
    const calls = [];
    let snap = { proxyAddr: '127.0.0.1:8080', intercept: { enabled: false, held: 0 }, scope: { loaded: true, inCount: 1, outCount: 1 }, flows: 12, live: 'ok', project: 'demo', version: 'v2.16.0', warnings: [] };
    const registered = [];
    assert.equal(initDesktopShell({
      mac: true,
      read: () => snap,
      actions: {
        openPanel: (n) => calls.push('panel:' + n), openSettings: (s) => calls.push('settings:' + s),
        openConnection: () => calls.push('conn'), closeConnection: () => calls.push('conn-close'), connectionOpen: () => false,
        openProject: () => calls.push('project'), openReleases: () => calls.push('releases'), toggleIntercept: () => calls.push('toggle'),
      },
      registerCommand: (c) => registered.push(c.t),
      commands: [{ t: 'View: Split left and right', run() {} }],
    }), true);
    const status = dom.body.querySelector('#dskStatus');
    assert.ok(status, 'status bar built');
    const item = (k) => status.querySelector('[data-sb=' + k + ']');
    assert.equal(item('proxy').textContent, '127.0.0.1:8080');
    assert.equal(item('scope').textContent, 'Scope 1 in · 1 out');
    assert.equal(item('flows').textContent, '12 flows');
    assert.equal(item('warnings').hidden, true);
    assert.equal(dom.start.contains(dom.chip), true, 'project switcher moved to the left');
    const pill = dom.end.querySelector('#dskIcptPill');
    assert.ok(pill, 'intercept pill added');
    assert.equal(dom.end.children.indexOf(pill), dom.end.children.indexOf(dom.ring) - 1, 'pill sits before the readiness ring');
    assert.equal(pill.getAttribute('aria-pressed'), 'false');
    assert.equal(dom.k1.textContent + dom.k2.textContent, '⌘K');
    assert.deepEqual(registered, ['View: Split left and right']);

    item('proxy').fire('click'); item('intercept').fire('click'); item('scope').fire('click'); item('flows').fire('click');
    item('connection').fire('click'); item('project').fire('click'); item('version').fire('click'); pill.fire('click');
    assert.deepEqual(calls, ['settings:proxy', 'panel:intercept', 'settings:scope', 'panel:proxy', 'conn', 'project', 'releases', 'toggle']);

    snap = { ...snap, intercept: { enabled: true, held: 3 }, warnings: [{ title: 'No HTTPS traffic intercepted yet' }] };
    refresh();
    assert.equal(pill.getAttribute('aria-pressed'), 'true');
    assert.equal(pill.textContent, 'Intercept on · 3 held');
    assert.equal(item('intercept').dataset.tone, 'warn');
    assert.equal(item('warnings').hidden, false);
    item('warnings').fire('click');
    const pop = dom.body.querySelector('#dskWarnPop');
    assert.equal(pop.hidden, false);
    assert.match(pop.textContent, /No HTTPS traffic intercepted yet/);
  } finally {
    globalThis.setInterval = realSetInterval;
  }
});
