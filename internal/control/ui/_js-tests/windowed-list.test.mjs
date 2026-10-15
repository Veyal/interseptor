// Behaviour of js/windowed-list.js on top of the REAL core.js createListView, under node.
//
// core.js touches the DOM at load, so the test slices createListView out of it (the same
// way the Go UI tests extract single functions) and runs it, the glue and the pure
// models against a very small fake DOM. That keeps the helper honest about what
// createListView actually does — including the two behaviours it does not own: it
// cannot tell "feature painted its own empty state" from "short list", and it re-windows
// on size changes only through whoever calls refresh().
import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { pathToFileURL } from 'node:url';

const uiJs = new URL('../js/', import.meta.url);
const read = (name) => fs.readFileSync(new URL(name, uiJs), 'utf8');

function sliceFunction(src, signature) {
  const start = src.indexOf(signature);
  assert.ok(start >= 0, 'core.js has ' + signature);
  let depth = 0;
  for (let i = src.indexOf('{', start); i < src.length; i++) {
    if (src[i] === '{') depth++;
    else if (src[i] === '}' && --depth === 0) return src.slice(start, i + 1);
  }
  throw new Error('unbalanced ' + signature);
}

// ---- a flat fake DOM: just enough for createListView and the glue ----------------------
const frames = [];
let observers = [];
const doc = { activeElement: null, body: null };
globalThis.document = doc;
globalThis.requestAnimationFrame = (fn) => { frames.push(fn); return frames.length; };
globalThis.cancelAnimationFrame = () => {};
globalThis.ResizeObserver = class { constructor(cb) { this.cb = cb; observers.push(this); } observe(el) { this.el = el; } disconnect() {} };
const flush = () => { while (frames.length) frames.shift()(); };
const fireResize = () => observers.forEach((o) => o.cb([], o));

const tokens = (el) => (el.attrs.class || '').split(/\s+/).filter(Boolean);
function matches(el, sel) {
  return sel.split(',').some((one) => {
    one = one.trim();
    const attr = one.match(/\[([\w-]+)="([^"]*)"\]/);
    if (attr && el.attrs[attr[1]] !== attr[2]) return false;
    const classes = [...one.replace(/\[[^\]]*\]/g, '').matchAll(/\.([\w-]+)/g)].map((m) => m[1]);
    return classes.every((c) => tokens(el).includes(c)) && (attr || classes.length > 0);
  });
}
class El {
  constructor(attrs = {}) {
    this.attrs = { ...attrs }; this.children = []; this.parent = null; this.style = {}; this.listeners = {};
    this.scrollTop = 0; this.clientHeight = 0; this.html = '';
  }
  get classList() {
    return {
      add: (c) => { if (!tokens(this).includes(c)) this.attrs.class = [...tokens(this), c].join(' '); },
      remove: (c) => { this.attrs.class = tokens(this).filter((t) => t !== c).join(' '); },
      contains: (c) => tokens(this).includes(c),
    };
  }
  get dataset() { return new Proxy({}, { get: (_, k) => this.attrs['data-' + String(k)] }); }
  set tabIndex(v) { this.attrs.tabindex = String(v); }
  hasAttribute(n) { return n in this.attrs; }
  getAttribute(n) { return n in this.attrs ? this.attrs[n] : null; }
  setAttribute(n, v) { this.attrs[n] = String(v); }
  get innerHTML() { return this.html; }
  set innerHTML(html) {
    this.html = html;
    for (const old of this.children) old.parent = null; // removed nodes are disconnected
    this.children = [...html.matchAll(/<(\w+)\b([^>]*)>([\s\S]*?)<\/\1>/g)].map((m) => {
      const child = new El(Object.fromEntries([...m[2].matchAll(/([\w-]+)="([^"]*)"/g)].map((a) => [a[1], a[2]])));
      child.parent = this; child.text = m[3]; return child;
    });
    if (doc.activeElement && !doc.activeElement.isConnected) doc.activeElement = doc.body; // like a browser
  }
  descendants() { return this.children.flatMap((c) => [c, ...c.descendants()]); }
  querySelectorAll(sel) {
    const scoped = sel.match(/^:scope\s*>\s*(.+)$/);
    return (scoped ? this.children : this.descendants()).filter((el) => matches(el, scoped ? scoped[1] : sel));
  }
  querySelector(sel) { return this.querySelectorAll(sel)[0] || null; }
  closest(sel) { for (let el = this; el; el = el.parent) if (matches(el, sel)) return el; return null; }
  contains(el) { for (; el; el = el.parent) if (el === this) return true; return false; }
  get isConnected() { for (let el = this; el; el = el.parent) if (el === doc.body) return true; return false; }
  focus() { if (this.isConnected) doc.activeElement = this; }
  addEventListener(type, fn) { (this.listeners[type] ||= []).push(fn); }
  removeEventListener(type, fn) { this.listeners[type] = (this.listeners[type] || []).filter((f) => f !== fn); }
  emit(type, extra = {}) { for (const fn of this.listeners[type] || []) fn({ target: this, ...extra }); }
  getBoundingClientRect() {
    boundsCalls++;
    const box = this.parent && this.parent.attrs.class?.includes('vlist-body') ? this.parent : this.parent;
    const index = box ? box.children.indexOf(this) : 0;
    return { top: index * ROW - (this.parent ? this.parent.scrollTop : 0), height: ROW };
  }
}
let ROW = 20;
let boundsCalls = 0;
doc.body = new El();

// ---- load the real createListView + the glue into a temp ES module graph ---------------------
const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'windowed-list-'));
fs.writeFileSync(path.join(tmp, 'package.json'), '{"type":"module"}');
fs.writeFileSync(path.join(tmp, 'virtual-list-model.js'), read('virtual-list-model.js'));
fs.writeFileSync(path.join(tmp, 'list-rows-model.js'), read('list-rows-model.js'));
fs.writeFileSync(path.join(tmp, 'core.js'),
  "import { computeVirtualWindow, sameWindow } from './virtual-list-model.js';\n" + sliceFunction(read('core.js'), 'export function createListView(opts){'));
fs.writeFileSync(path.join(tmp, 'windowed-list.js'), read('windowed-list.js'));
const { createWindowedList } = await import(pathToFileURL(path.join(tmp, 'windowed-list.js')).href);
test.after(() => fs.rmSync(tmp, { recursive: true, force: true }));

// ---- helpers --------------------------------------------------------------------------------
const rowHTML = (it, i) => `<div class="row" data-id="${it}" tabindex="${it === 'selected' ? '0' : '-1'}">${it}</div>`;
function mount({ count = 1000, height = 200, row = 20, threshold = 100, extra = {} } = {}) {
  ROW = row;
  const items = Array.from({ length: count }, (_, i) => String(i));
  const container = new El({ class: 'list' });
  container.parent = doc.body; doc.body.children.push(container);
  container.clientHeight = height;
  const win = createWindowedList({
    container, rowSelector: '.row', rowHeight: row, threshold, buffer: 8,
    getItems: () => items, renderRow: rowHTML,
    findRow: (key) => container.querySelector(`.row[data-id="${key}"]`),
    ...extra,
  });
  win.render(); flush();
  return { win, container, items };
}
const ids = (container) => container.querySelectorAll('.row').map((r) => Number(r.attrs['data-id']));
const reset = () => { frames.length = 0; observers = []; doc.activeElement = doc.body; boundsCalls = 0; doc.body.children = []; };

test('a long list mounts a bounded window and keeps exactly one tab stop', () => {
  reset();
  const { container } = mount();
  const rows = ids(container);
  assert.ok(rows.length <= Math.ceil(200 / 20) + 16, 'bounded: ' + rows.length);
  assert.ok(container.classList.contains('vlist'), 'virtualized containers carry the vlist class');
  assert.equal(container.attrs.tabindex, '-1', 'the container is a focus fallback, never a tab stop');
  const stops = container.querySelectorAll('.row').filter((r) => r.attrs.tabindex === '0');
  assert.equal(stops.length, 1, 'the open row is not mounted, so the first mounted row is the tab stop');
  assert.equal(stops[0], container.querySelector('.row'));
});

// Tab into a list whose open row is not mounted: the one tab stop must be a row the user can see.
// The first mounted row is one of up to 8 overscan rows above the viewport, and focusing it makes the
// browser scroll the list back to it.
test('with the open row unmounted the tab stop is the first row in the viewport, not an overscan row', () => {
  reset();
  const indexOf = (row) => Number(row.attrs['data-id']);
  const { container } = mount({ extra: { indexOf } });
  container.scrollTop = 6000; container.emit('scroll'); flush();
  const rows = ids(container);
  assert.ok(rows[0] < 300, 'overscan rows are mounted above the viewport: ' + rows[0]);
  const stops = container.querySelectorAll('.row').filter((r) => r.attrs.tabindex === '0');
  assert.equal(stops.length, 1);
  assert.equal(Number(stops[0].attrs['data-id']), 300, 'row 300 is the first one at scrollTop 6000 / 20px rows');
});

test('the tab stop clears a pinned header that covers the top of the viewport', () => {
  reset();
  const { container } = mount({ extra: { indexOf: (row) => Number(row.attrs['data-id']), topInset: () => 40 } });
  container.scrollTop = 6000; container.emit('scroll'); flush();
  const stop = container.querySelectorAll('.row').find((r) => r.attrs.tabindex === '0');
  assert.equal(Number(stop.attrs['data-id']), 302, '40px of header hides two 20px rows');
});

test('the open row, when mounted, keeps the tab stop wherever it is', () => {
  reset();
  const { container } = mount({ count: 1000, extra: { renderRow: (it) => `<div class="row" data-id="${it}" tabindex="${it === '305' ? '0' : '-1'}">${it}</div>`, indexOf: (row) => Number(row.attrs['data-id']) } });
  container.scrollTop = 6000; container.emit('scroll'); flush();
  const stops = container.querySelectorAll('.row').filter((r) => r.attrs.tabindex === '0');
  assert.deepEqual(stops.map((r) => r.attrs['data-id']), ['305']);
});

// createListView.refresh() answers a non-virtual list by re-rendering every row. A short list has no
// window to move, so a resize (dragging the split) or a focus helper must not repaint up to 149 rows.
test('a short list is not repainted by refresh() or a resize', () => {
  reset();
  const { win, container } = mount({ count: 12, threshold: 100 });
  const before = container.querySelectorAll('.row');
  win.refresh(); fireResize(); flush();
  const after = container.querySelectorAll('.row');
  assert.equal(after.length, 12);
  assert.ok(after.every((row, i) => row === before[i]), 'the same row elements: nothing was rebuilt');
});

test('a short list renders whole and still has one tab stop', () => {
  reset();
  const { container } = mount({ count: 12, threshold: 100 });
  assert.equal(ids(container).length, 12);
  assert.equal(container.classList.contains('vlist'), false);
  assert.equal(container.querySelectorAll('.row').filter((r) => r.attrs.tabindex === '0').length, 1);
});

// A short list keeps natural rows that may be taller than a virtualized row (a wrapped title);
// only the virtualized list's fixed rows are measured, and only once the list is virtualized.
test('a short list is not measured; the virtualized list adopts the laid-out row height once', () => {
  reset();
  const { win, container, items } = mount({ count: 12, threshold: 100, row: 20 });
  ROW = 44; // natural rows are taller than the stylesheet's fixed virtual row
  boundsCalls = 0;
  win.render(); flush();
  assert.equal(boundsCalls, 0, 'a short list is never measured');
  assert.equal(win.rowHeight(), 20, 'and keeps the guess');
  for (let i = 12; i < 1000; i++) items.push(String(i));
  ROW = 30; // what .vlist fixes the rows to
  win.render(); flush();
  assert.equal(win.rowHeight(), 30, 'the first mounted virtual row is measured');
  assert.equal(container.querySelector('.vlist-body').style.transform, 'translateY(0px)');
  assert.equal(container.querySelector('.vlist-spacer').style.height, 1000 * 30 + 'px', 'the spacer uses the measured height');
  const measuredOnce = boundsCalls;
  for (let top = 300; top <= 3000; top += 300) { container.scrollTop = top; container.emit('scroll'); flush(); }
  assert.equal(boundsCalls, measuredOnce, 'no further measurements while scrolling');
});

test('scrolling re-windows once per frame and does not read layout after the first paint', () => {
  reset();
  const { container } = mount();
  boundsCalls = 0;
  for (let top = 400; top <= 8000; top += 400) { container.scrollTop = top; container.emit('scroll'); flush(); }
  const rows = ids(container);
  assert.ok(rows[0] > 300 && rows.length <= 26, 'window followed the scroll: ' + rows[0] + ' x' + rows.length);
  assert.equal(boundsCalls, 0, 'no getBoundingClientRect on the scroll path');
});

test('a focused row that scrolls out of the window parks focus on the container', () => {
  reset();
  const { container } = mount({ extra: { findRow: () => null } });
  container.querySelector('.row[data-id="3"]').focus();
  assert.equal(doc.activeElement.attrs['data-id'], '3');
  container.scrollTop = 6000; container.emit('scroll'); flush();
  assert.equal(doc.activeElement, container, 'focus is kept in the list, not dropped to <body>');
});

test('a focused row that is rebuilt by a re-render gets its focus back', () => {
  reset();
  const { win, container } = mount();
  container.querySelector('.row[data-id="4"]').focus();
  win.render();
  assert.equal(doc.activeElement.attrs['data-id'], '4');
  assert.equal(doc.activeElement.isConnected, true);
});

// Roving tabindex: Tab lands on the list's one tab stop. A scroll then rebuilds every mounted row, and
// the focused row comes back as a fresh element. It must come back as the tab stop, or the list holds a
// second stop a few rows down and the next Tab walks to it instead of leaving the list.
test('the focused row stays the one tab stop when a scroll rebuilds the window under it', () => {
  reset();
  const { container } = mount({ extra: { indexOf: (row) => Number(row.attrs['data-id']) } });
  container.scrollTop = 6000; container.emit('scroll'); flush();
  const stop = container.querySelectorAll('.row').find((r) => r.attrs.tabindex === '0');
  assert.equal(stop.attrs['data-id'], '300', 'Tab lands on the first row in the viewport');
  stop.focus();
  container.scrollTop = 6100; container.emit('scroll'); flush(); // the wheel moves five rows
  assert.equal(doc.activeElement.attrs['data-id'], '300', 'focus is handed back to the same row');
  const stops = container.querySelectorAll('.row').filter((r) => r.attrs.tabindex === '0');
  assert.deepEqual(stops.map((r) => r.attrs['data-id']), ['300'], 'and that row is the tab stop');
});

// An owner's own controls inside the list (the empty state's "Clear filters") are replaced by rows when
// the list repaints. Focus on one of them used to drop to <body>.
test('focus on a control inside the list that is not a row lands on the open row when rows replace it', () => {
  reset();
  const open = (it) => `<div class="row" data-id="${it}" tabindex="${it === '3' ? '0' : '-1'}">${it}</div>`;
  const { win, container } = mount({ count: 12, threshold: 100, extra: { renderRow: open } });
  win.suspend();
  container.innerHTML = '<button class="clear" id="clear">Clear filters</button>'; // the owner's empty state
  const button = container.querySelector('.clear');
  button.focus();
  assert.equal(doc.activeElement, button);
  win.render(); flush();
  assert.ok(container.contains(doc.activeElement) && doc.activeElement !== container, 'focus stays on a row of the list');
  assert.equal(doc.activeElement.attrs['data-id'], '3', 'the open row, which carries the tab stop');
});

test('focus on a control inside the list that is not a row lands on the container when the open row is not mounted', () => {
  reset();
  const { win, container } = mount({ count: 1000 });
  win.suspend();
  container.innerHTML = '<button class="clear" id="clear">Clear filters</button>';
  container.querySelector('.clear').focus();
  win.render(); flush();
  assert.equal(doc.activeElement, container, 'not <body>: the keys keep working');
});

test('focus that moved elsewhere is not stolen back', () => {
  reset();
  const { win, container } = mount();
  const other = new El(); other.parent = doc.body; doc.body.children.push(other);
  container.querySelector('.row[data-id="4"]').focus();
  other.focus();
  win.render();
  assert.equal(doc.activeElement, other);
});

// Regression: after a feature paints its own empty state, createListView.refresh() sees
// "not virtual" and renders every row again, so a resize (the "open finding is outside
// these filters" bar appearing) repainted stale rows over "No matching findings".
test('suspend() keeps a resize or scroll from repainting rows over the feature’s own markup', () => {
  reset();
  const { win, container } = mount({ count: 11, threshold: 100 });
  win.suspend();
  container.innerHTML = '<div class="empty">No matching findings</div>';
  fireResize(); flush();
  container.scrollTop = 40; container.emit('scroll'); flush();
  win.refresh(); flush();
  assert.equal(ids(container).length, 0, 'no stale rows');
  assert.ok(container.querySelector('.empty'), 'the empty state is still there');
  win.render(); flush();
  assert.equal(ids(container).length, 11, 'the next render() repaints');
});

// Regression: a list in a hidden pane measures 0px; re-windowing against that replaced a
// good window with a 16-row one, so after the phone's Back button the open row was not
// mounted when it was focused.
test('a hidden list keeps the window it had and re-windows once it is shown again', () => {
  reset();
  const { container } = mount();
  container.scrollTop = 6000; container.emit('scroll'); flush();
  const before = ids(container);
  container.clientHeight = 0;
  fireResize(); flush();
  assert.deepEqual(ids(container), before, 'hidden: nothing is re-windowed against a 0px viewport');
  container.clientHeight = 400;
  fireResize(); flush();
  const after = ids(container);
  assert.ok(after.length >= Math.ceil(400 / 20), 'shown: the window covers the new viewport (' + after.length + ')');
  assert.equal(after[0], before[0], 'and still starts where the list was scrolled to');
});

test('a taller viewport mounts more rows (no blank band under the list)', () => {
  reset();
  const { container } = mount({ height: 200 });
  const small = ids(container).length;
  container.clientHeight = 600;
  fireResize(); flush();
  assert.ok(ids(container).length > small, 'grew from ' + small + ' to ' + ids(container).length);
});

test('reveal() scrolls a row that is not mounted into the viewport, and the next render mounts it', () => {
  reset();
  const { win, container } = mount();
  assert.equal(ids(container).includes(700), false);
  assert.equal(win.reveal(700, { align: 'center' }), true);
  win.render(); flush();
  const rows = ids(container);
  assert.ok(rows.includes(700), 'row 700 is mounted');
  const top = container.scrollTop;
  assert.ok(700 * 20 >= top && 700 * 20 + 20 <= top + 200, 'and inside the viewport');
  assert.equal(win.reveal(700, { align: 'center' }), false, 'already visible: no movement');
});

test('reveal() honours a pinned header on top of a virtualized list', () => {
  reset();
  const { win, container } = mount();
  container.scrollTop = 1000; container.emit('scroll'); flush();
  // Row 50 sits at y=1000, exactly at the top edge: under a 40px pinned header it is hidden.
  assert.equal(win.reveal(50, { inset: 40 }), true);
  assert.equal(container.scrollTop, 960);
});

test('reveal() on a fully mounted list uses the laid-out row, not index × height', () => {
  reset();
  const { win, container } = mount({ count: 12, height: 100, threshold: 100 });
  container.scrollHeight = 12 * 20;
  assert.equal(win.reveal(9), true, 'row 9 (y=180..200) is below a 100px viewport');
  assert.equal(container.scrollTop, 100);
});

test('destroy() detaches the scroll, resize and key listeners', () => {
  reset();
  const { win, container } = mount();
  win.destroy();
  const before = ids(container);
  container.scrollTop = 6000; container.emit('scroll'); fireResize(); flush();
  assert.deepEqual(ids(container), before);
});

test('keys that land on the container itself reach onContainerKey, row keys do not', () => {
  reset();
  const seen = [];
  const { container } = mount({ extra: { onContainerKey: (e) => seen.push(e.key) } });
  container.emit('keydown', { key: 'ArrowDown' });
  container.querySelector('.row').emit('keydown', { key: 'ArrowUp' });
  assert.deepEqual(seen, ['ArrowDown']);
});
