// desktop-panel-kit.js behaviour against a minimal fake DOM (no browser): the
// overflow menu's open/close, Escape and outside press, focus return, focus that
// lands in a closed menu, Tab order, descriptor items, the reveal-before-focus
// hook, and the restore of every moved control when html.desktop-app goes away.
import { test } from 'node:test';
import assert from 'node:assert/strict';

class FakeNode {
  constructor(tag) {
    this.tagName = String(tag).toUpperCase();
    this.nodeType = 1;
    this.attrs = {};
    this.children = [];
    this.parentNode = null;
    this.listeners = {};
    this.dataset = {};
    this.style = {};
    this.hidden = false;
    this.disabled = false;
    this.open = false;
    this._text = '';
    const self = this;
    this.classList = {
      contains: (c) => (self.className || '').split(/\s+/).includes(c),
      add: (c) => { if (!self.classList.contains(c)) self.className = ((self.className || '') + ' ' + c).trim(); },
      remove: (c) => { self.className = (self.className || '').split(/\s+/).filter((x) => x !== c).join(' '); },
    };
  }
  get id() { return this.attrs.id || ''; }
  set id(v) { this.attrs.id = String(v); }
  get className() { return this.attrs.class || ''; }
  set className(v) { this.attrs.class = String(v); }
  get tabIndex() { return this.attrs.tabindex !== undefined ? Number(this.attrs.tabindex) : (this.tagName === 'BUTTON' ? 0 : -1); }
  set tabIndex(v) { this.attrs.tabindex = String(v); }
  get childNodes() { return this.children; }
  get nextSibling() { const p = this.parentNode; if (!p) return null; return p.children[p.children.indexOf(this) + 1] || null; }
  get textContent() { return this._text + this.children.map((c) => c.textContent).join(''); }
  set textContent(v) { this._text = String(v); this.children.forEach((c) => { c.parentNode = null; }); this.children = []; }
  get offsetWidth() { return 200; }
  get offsetHeight() { return 100; }
  setAttribute(n, v) { this.attrs[n] = String(v); }
  getAttribute(n) { return n in this.attrs ? this.attrs[n] : null; }
  hasAttribute(n) { return n in this.attrs; }
  removeAttribute(n) { delete this.attrs[n]; }
  appendChild(c) { return this.insertBefore(c, null); }
  insertBefore(c, ref) {
    if (c.parentNode) c.parentNode.children.splice(c.parentNode.children.indexOf(c), 1);
    const at = ref ? this.children.indexOf(ref) : -1;
    if (at < 0) this.children.push(c); else this.children.splice(at, 0, c);
    c.parentNode = this;
    return c;
  }
  contains(n) { for (let x = n; x; x = x.parentNode) if (x === this) return true; return false; }
  matches(sel) { return sel.split(',').some((s) => { s = s.trim(); return s.startsWith('.') ? this.classList.contains(s.slice(1)) : this.tagName === s.toUpperCase(); }); }
  closest(sel) { for (let x = this; x && x.matches; x = x.parentNode) if (x.matches(sel)) return x; return null; }
  querySelector(sel) { return this.querySelectorAll(sel)[0] || null; }
  querySelectorAll(sel) { const out = []; const walk = (n) => n.children.forEach((c) => { if (c.matches(sel)) out.push(c); walk(c); }); walk(this); return out; }
  getBoundingClientRect() { return { left: 10, top: 10, right: 38, bottom: 38, width: 28, height: 28 }; }
  getClientRects() { return this.hidden ? [] : [1]; }
  addEventListener(type, fn, capture) { (this.listeners[type] ||= []).push({ fn, capture: !!(capture === true || (capture && capture.capture)) }); }
  dispatch(type, init = {}) {
    const path = [];
    for (let x = this; x; x = x.parentNode) path.push(x);
    path.push(globalThis.document);
    const e = { type, target: this, key: init.key, defaultPrevented: false, stopped: false, preventDefault() { this.defaultPrevented = true; }, stopPropagation() { this.stopped = true; } };
    for (const n of [...path].reverse()) { for (const l of (n.listeners[type] || [])) if (l.capture && !e.stopped) l.fn(e); }
    for (const n of path) { if (e.stopped) break; for (const l of (n.listeners[type] || [])) if (!l.capture) l.fn(e); }
    return e;
  }
  click() { if (!this.disabled) this.dispatch('click'); }
  focus() {
    const doc = globalThis.document;
    doc.activeElement = this;
    this.dispatch('focusin');
  }
}

function installDom({ desktop = true } = {}) {
  const observers = [];
  const html = new FakeNode('html');
  const body = new FakeNode('body');
  html.appendChild(body);
  if (desktop) html.className = 'desktop-app';
  const document = new FakeNode('#document');
  Object.assign(document, {
    documentElement: html, body, activeElement: null,
    createElement: (t) => new FakeNode(t),
    createElementNS: (_ns, t) => new FakeNode(t),
  });
  document.querySelector = (sel) => (sel.includes('aria-expanded="true"') ? null : body.querySelector(sel));
  const win = new FakeNode('#window');
  Object.assign(win, { innerWidth: 1280, innerHeight: 800 });
  Object.assign(globalThis, {
    document, window: win,
    MutationObserver: class { constructor(fn) { this.fn = fn; observers.push(this); } observe(target) { this.target = target; } disconnect() { this.off = true; } },
  });
  globalThis.window.addEventListener = win.addEventListener.bind(win);
  return { html, body, document, win, fireObservers: () => observers.filter((o) => !o.off).forEach((o) => o.fn([])) };
}

const button = (id, text) => { const b = new FakeNode('button'); b.id = id; b.textContent = text; return b; };
let seq = 0;
const load = () => import('../js/desktop-panel-kit.js?case=' + (++seq));

test('overflow: re-parents the real controls, opens on click, Escape closes and returns focus', async () => {
  const dom = installDom();
  const kit = await load();
  const bar = new FakeNode('div');
  dom.body.appendChild(bar);
  const imp = button('collImport', 'Import');
  const exp = button('collExport', 'Export');
  bar.appendChild(imp); bar.appendChild(exp);
  const more = kit.createOverflow({ id: 'collMoreTools', items: [imp, kit.menuSeparator(), exp] });
  bar.appendChild(more.button);
  assert.equal(imp.parentNode, more.menu, 'Import moved into the menu, the same element');
  assert.equal(more.button.getAttribute('aria-label'), 'More actions', 'one overflow vocabulary');
  assert.equal(imp.tabIndex, -1, 'menu items are out of the Tab order');
  assert.equal(more.menu.getAttribute('aria-hidden'), 'true');

  more.button.click();
  assert.equal(more.isOpen(), true);
  assert.equal(more.button.getAttribute('aria-expanded'), 'true');
  assert.equal(dom.document.activeElement, imp, 'focus goes to the first item');
  assert.equal(more.menu.getAttribute('aria-hidden'), null);

  const esc = imp.dispatch('keydown', { key: 'Escape' });
  assert.equal(esc.defaultPrevented, true);
  assert.equal(more.isOpen(), false);
  assert.equal(dom.document.activeElement, more.button, 'Escape returns focus to the trigger');
});

test('overflow: arrows move inside the menu, Tab closes it onto the trigger', async () => {
  const dom = installDom();
  const kit = await load();
  const a = button('a', 'A'), b = button('b', 'B');
  dom.body.appendChild(a); dom.body.appendChild(b);
  const more = kit.createOverflow({ id: 'm', items: [a, b] });
  dom.body.appendChild(more.button);
  more.open();
  a.dispatch('keydown', { key: 'ArrowDown' });
  assert.equal(dom.document.activeElement, b);
  b.dispatch('keydown', { key: 'ArrowDown' });
  assert.equal(dom.document.activeElement, a, 'arrows wrap');
  const tab = a.dispatch('keydown', { key: 'Tab' });
  assert.equal(tab.defaultPrevented, true);
  assert.equal(more.isOpen(), false);
  assert.equal(dom.document.activeElement, more.button);
});

test('overflow: focus returned to a moved control while closed lands on the trigger, not on an invisible item', async () => {
  const dom = installDom();
  const kit = await load();
  const imp = button('collImport', 'Import');
  dom.body.appendChild(imp);
  const more = kit.createOverflow({ id: 'c', items: [imp] });
  dom.body.appendChild(more.button);
  // A sheet opened from Import closes and gives focus back to Import.
  imp.focus();
  assert.equal(dom.document.activeElement, more.button);
});

test('overflow: an outside press closes, a press inside does not; opening one menu closes the other', async () => {
  const dom = installDom();
  const kit = await load();
  const x = button('x', 'X'), y = button('y', 'Y');
  const m1 = kit.createOverflow({ id: 'm1', items: [x] });
  const m2 = kit.createOverflow({ id: 'm2', items: [y] });
  dom.body.appendChild(m1.button); dom.body.appendChild(m2.button);
  const outside = new FakeNode('div'); dom.body.appendChild(outside);
  m1.open();
  x.dispatch('pointerdown');
  assert.equal(m1.isOpen(), true, 'a press on an item keeps the menu for its click');
  m2.open();
  assert.equal(m1.isOpen(), false, 'one desktop popup at a time');
  outside.dispatch('pointerdown');
  assert.equal(m2.isOpen(), false);
  m1.open();
  y.click(); // a click inside another (closed) menu is not this menu's business
  x.click();
  assert.equal(m1.isOpen(), false, 'choosing an item closes the menu');
});

test('overflow: descriptor items re-read their state on open and run after the menu closed', async () => {
  const dom = installDom();
  const kit = await load();
  let canClear = false, ran = 0;
  const more = kit.createOverflow({ id: 'p', items: [
    { key: 'cols', label: 'Columns…', icon: 'columns', run: () => {} },
    kit.menuSeparator(),
    { key: 'clear', label: 'Clear all filters', icon: 'close', disabled: () => !canClear, run: () => { ran++; } },
    kit.menuSeparator(),
    { key: 'gone', label: 'Hidden', hidden: true, run: () => {} },
  ] });
  dom.body.appendChild(more.button);
  const [cols, sep1, clear, sep2, gone] = more.menu.children;
  more.open();
  assert.equal(clear.disabled, true);
  assert.equal(gone.hidden, true);
  assert.equal(sep1.hidden, false, 'a separator between two visible groups shows');
  assert.equal(sep2.hidden, true, 'a trailing separator before only hidden items does not');
  assert.equal(cols.querySelector('.dsk-menu-label').textContent, 'Columns…');
  more.close();
  canClear = true;
  more.open();
  assert.equal(clear.disabled, false, 'state is re-read on every open');
  await new Promise((r) => {
    clear.click();
    assert.equal(more.isOpen(), false, 'the menu closes first');
    assert.equal(ran, 0, 'the action waits for the menu to close');
    setTimeout(r, 5);
  });
  assert.equal(ran, 1);
});

test('restore: removing html.desktop-app puts every moved control back and hides the skin chrome', async () => {
  const dom = installDom();
  const kit = await load();
  const bar = new FakeNode('div'); dom.body.appendChild(bar);
  const a = button('a', 'A'), b = button('b', 'B'), c = button('c', 'C');
  bar.appendChild(a); bar.appendChild(b); bar.appendChild(c);
  b.setAttribute('tabindex', '0');
  const more = kit.createOverflow({ id: 'r', items: [a, b] });
  bar.appendChild(more.button);
  more.open();
  assert.deepEqual(bar.children.map((n) => n.id), ['c', 'r']);
  dom.html.className = '';
  dom.fireObservers();
  assert.deepEqual(bar.children.slice(0, 3).map((n) => n.id), ['a', 'b', 'c'], 'web order restored');
  assert.equal(a.getAttribute('tabindex'), null, 'a moved control gets its own tabindex back');
  assert.equal(b.getAttribute('tabindex'), '0');
  assert.equal(more.isOpen(), false);
  assert.equal(more.button.hidden, true);
  assert.equal(more.menu.hidden, true);
});

test('revealBeforeFocus: focusing a field inside a closed section opens it first', async () => {
  installDom();
  const kit = await load();
  const details = new FakeNode('details');
  const threads = new FakeNode('input');
  details.appendChild(threads);
  kit.revealBeforeFocus(details);
  assert.equal(details.open, false);
  threads.focus();
  assert.equal(details.open, true);
  assert.equal(globalThis.document.activeElement, threads);
});

test('host checks: isDesktop reads html.desktop-app; nothing runs in a browser tab', async () => {
  const dom = installDom({ desktop: false });
  const kit = await load();
  assert.equal(kit.isDesktop(), false);
  let ran = false;
  kit.whenDesktop(() => true, () => { ran = true; });
  assert.equal(ran, false);
  dom.html.className = 'desktop-app';
  dom.fireObservers();
  assert.equal(ran, true, 'setup runs once the class arrives');
});
