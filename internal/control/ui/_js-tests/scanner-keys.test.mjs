// Keyboard and focus behaviour of the Scanner issue list, running the REAL scanItemHTML,
// ensureScanListWin, chooseScanIssue, moveScanSelection and renderScan out of scanner.js on
// top of the REAL createListView and windowed-list glue, against a small fake DOM.
//
// Why it exists. renderScan hands keyboard focus back to the issue that had it (by title),
// revealing that issue first when a re-sort moved it out of the mounted window. A key move
// that jumps past the window (End, Home, a wrap-around arrow) goes through the same render,
// and the focus hand-back used to drag the list straight back to the row the key started
// from: the selection moved, the list did not, and focus stayed on the old row. Nothing but
// a browser run showed it, so these tests pin the behaviour.
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
  assert.ok(start >= 0, 'source has ' + signature);
  let depth = 0;
  for (let i = src.indexOf('{', start); i < src.length; i++) {
    if (src[i] === '{') depth++;
    else if (src[i] === '}' && --depth === 0) return src.slice(start, i + 1);
  }
  throw new Error('unbalanced ' + signature);
}

// ---- a small fake DOM: nested markup, #id / .class / [attr] selectors, focus ---------------
const frames = [];
const doc = { activeElement: null, body: null };
globalThis.document = doc;
globalThis.requestAnimationFrame = (fn) => { frames.push(fn); return frames.length; };
globalThis.cancelAnimationFrame = () => {};
globalThis.ResizeObserver = class { observe() {} disconnect() {} };
const flush = () => { while (frames.length) frames.shift()(); };

const tokens = (el) => (el.attrs.class || '').split(/\s+/).filter(Boolean);
function matchesCompound(el, compound) {
  const id = compound.match(/#([\w-]+)/);
  if (id && el.attrs.id !== id[1]) return false;
  for (const m of compound.replace(/\[[^\]]*\]/g, '').matchAll(/\.([\w-]+)/g)) if (!tokens(el).includes(m[1])) return false;
  for (const m of compound.matchAll(/\[([\w-]+)(?:="([^"]*)")?\]/g)) {
    if (!(m[1] in el.attrs) || (m[2] !== undefined && el.attrs[m[1]] !== m[2])) return false;
  }
  return true;
}
function matches(el, selector) {
  return selector.split(',').some((one) => {
    const parts = one.trim().split(/\s+/);
    if (!matchesCompound(el, parts.pop())) return false;
    let up = el.parent;
    while (parts.length) {
      while (up && !matchesCompound(up, parts[parts.length - 1])) up = up.parent;
      if (!up) return false;
      parts.pop(); up = up.parent;
    }
    return true;
  });
}
function parse(html, parent) {
  const root = { children: [] };
  const stack = [root];
  for (const m of html.matchAll(/<(\/?)([a-zA-Z][\w-]*)([^>]*?)(\/?)>|[^<]+/g)) {
    if (m[2] === undefined) continue; // text
    if (m[1]) { if (stack.length > 1) stack.pop(); continue; }
    const el = new El(Object.fromEntries([...m[3].matchAll(/([\w-]+)="([^"]*)"/g)].map((a) => [a[1], a[2]])));
    const top = stack[stack.length - 1];
    el.parent = top === root ? parent : top;
    top.children.push(el);
    if (!m[4]) stack.push(el);
  }
  return root.children;
}
class El {
  constructor(attrs = {}) {
    this.attrs = { ...attrs }; this.children = []; this.parent = null; this.style = {}; this.listeners = {};
    this.scrollTop = 0; this.clientHeight = 0; this.html = ''; this.textContent = '';
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
  removeAttribute(n) { delete this.attrs[n]; }
  get innerHTML() { return this.html; }
  set innerHTML(html) {
    this.html = html;
    for (const old of this.children) old.parent = null; // removed nodes are disconnected
    this.children = parse(html, this);
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
  getBoundingClientRect() { return { top: 0, height: ROW_H }; }
}
const ROW_H = 74;
doc.body = new El();

// ---- load the real scanner functions + createListView + glue into a temp ES module graph -----
const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'scanner-keys-'));
fs.writeFileSync(path.join(tmp, 'package.json'), '{"type":"module"}');
for (const name of ['virtual-list-model.js', 'list-rows-model.js', 'windowed-list.js']) fs.writeFileSync(path.join(tmp, name), read(name));
fs.writeFileSync(path.join(tmp, 'core.js'),
  "import { computeVirtualWindow, sameWindow } from './virtual-list-model.js';\n" + sliceFunction(read('core.js'), 'export function createListView(opts){'));
const scanner = read('scanner.js');
fs.writeFileSync(path.join(tmp, 'scanner-slice.js'), `
import { createWindowedList } from './windowed-list.js';
import { stepIndex } from './list-rows-model.js';
export const scanState = { issues: [], groups: [], sel: null };
export const scanUI = { sevActive: new Set(), pane: null };
const $ = (sel) => globalThis.__scanDom(sel);
const esc = (s) => String(s ?? ''), escAttr = (s) => String(s ?? '');
const sevChip = () => '';
const groupIssues = (issues) => issues.slice(); // ready-made groups; a copy, as the real grouping makes one per render
const filterGroups = (groups) => groups;
const renderScanChips = () => {};
const summaryText = () => '';
const renderScanDetail = () => {};
const setScanDetailHTML = () => {};
const renderState = () => {};
const showScanIssueCtx = () => {};
const SCAN_NO_SELECTION = '';
const SCAN_VIRT_MIN = 150;
let scanListWin = null;
${sliceFunction(scanner, 'function scanItemHTML(g,idx){')}
${sliceFunction(scanner, 'function ensureScanListWin(list){')}
${sliceFunction(scanner, 'function chooseScanIssue(i){')}
${sliceFunction(scanner, 'function moveScanSelection(e,from){')}
${sliceFunction(scanner, 'export function renderScan(opts){')}
export { chooseScanIssue, moveScanSelection };
`);
const { scanState, renderScan } = await import(pathToFileURL(path.join(tmp, 'scanner-slice.js')).href);
test.after(() => fs.rmSync(tmp, { recursive: true, force: true }));

// ---- helpers ------------------------------------------------------------------------------
const title = (i) => 'Issue ' + String(i).padStart(4, '0');
let list = null;
function setup(count = 300) {
  frames.length = 0;
  doc.body.children = [];
  doc.activeElement = doc.body;
  list = new El({ id: 'scanList', class: 'scan-list' });
  list.parent = doc.body; doc.body.children.push(list);
  list.clientHeight = 600; // 8 rows fit; 300 rows are far past the 150-row window threshold
  const countEl = new El();
  globalThis.__scanDom = (sel) => (sel === '#scanList' ? list : sel === '#scanCount' ? countEl : new El());
  scanState.issues = Array.from({ length: count }, (_, i) => ({ title: title(i), severity: 'low', items: [{}] }));
  scanState.groups = []; scanState.sel = null;
  renderScan(); flush();
}
const row = (i) => list.querySelector('#scan-issue-' + i);
const activeIndex = () => {
  const a = doc.activeElement;
  if (a === list) return 'container';
  const r = a && a.closest('.scan-item');
  return r ? Number(r.attrs['data-i']) : null;
};
// A key event delivered the way the browser does: to the container, with the row as the target.
function press(keyName, target) {
  const ev = { key: keyName, target, defaultPrevented: false, preventDefault: () => { ev.defaultPrevented = true; } };
  list.emit('keydown', ev); flush();
  return ev;
}
const scrollTo = (top) => { list.scrollTop = top; list.emit('scroll'); flush(); };

test('the list is windowed: far fewer rows than issues are mounted, with one tab stop', () => {
  setup(300);
  const mounted = list.querySelectorAll('.scan-item').length;
  assert.ok(mounted > 0 && mounted < 40, 'bounded window: ' + mounted);
  assert.equal(list.querySelectorAll('.scan-item[tabindex="0"]').length, 1);
  assert.equal(row(0).attrs['aria-posinset'], '1');
  assert.equal(row(0).attrs['aria-setsize'], '300');
});

test('End jumps past the mounted window and focus lands on the last issue', () => {
  setup(300);
  row(1).focus();
  const ev = press('End', row(1));
  assert.equal(ev.defaultPrevented, true, 'the key is claimed');
  assert.equal(scanState.sel, 299);
  assert.equal(activeIndex(), 299, 'focus follows the selection, not the row the key started from');
  assert.ok(row(299), 'the last issue is mounted');
  assert.ok(list.scrollTop >= 300 * ROW_H - list.clientHeight - 1, 'the list scrolled to the end: ' + list.scrollTop);
});

test('Home from the end jumps back to the first issue', () => {
  setup(300);
  row(1).focus(); press('End', row(1));
  press('Home', row(299));
  assert.equal(scanState.sel, 0);
  assert.equal(activeIndex(), 0);
  assert.equal(list.scrollTop, 0);
});

test('arrows wrap around the ends and focus follows across the whole list', () => {
  setup(300);
  row(0).focus();
  press('ArrowUp', row(0));
  assert.equal(scanState.sel, 299, 'ArrowUp from the first issue wraps to the last');
  assert.equal(activeIndex(), 299);
  press('ArrowDown', row(299));
  assert.equal(scanState.sel, 0, 'ArrowDown from the last issue wraps to the first');
  assert.equal(activeIndex(), 0);
  assert.equal(list.scrollTop, 0);
});

test('a short step stays inside the window and keeps one tab stop on the open issue', () => {
  setup(300);
  row(0).focus();
  press('ArrowDown', row(0));
  assert.equal(scanState.sel, 1);
  assert.equal(activeIndex(), 1);
  const stops = list.querySelectorAll('.scan-item[tabindex="0"]');
  assert.deepEqual(stops.map((r) => r.attrs['data-i']), ['1']);
});

test('keys that land on the container itself (focus parked there) still move the selection', () => {
  setup(300);
  scrollTo(150 * ROW_H);
  assert.equal(row(0), null, 'issue 0 is out of the window');
  list.focus();
  assert.equal(activeIndex(), 'container');
  press('End', list);
  assert.equal(scanState.sel, 299);
  assert.equal(activeIndex(), 299);
});

test('clicking an issue far from the previous one selects it and focuses its row', () => {
  setup(300);
  scrollTo(120 * ROW_H);
  const target = row(124);
  assert.ok(target, 'issue 124 is mounted at this scroll offset');
  list.emit('click', { target }); flush();
  assert.equal(scanState.sel, 124);
  assert.equal(activeIndex(), 124);
});

test('a scan that re-sorts the list hands focus back to the same issue by title, even out of the window', () => {
  setup(300);
  scrollTo(100 * ROW_H - 100);
  const focused = row(100);
  assert.ok(focused, 'issue 100 is mounted');
  focused.focus();
  const [moved] = scanState.issues.splice(100, 1); // the issue now sorts to the very end
  scanState.issues.push(moved);
  renderScan(); flush();
  assert.equal(scanState.groups[299].title, title(100));
  assert.equal(activeIndex(), 299, 'focus follows the issue, not the index it used to have');
  assert.ok(row(299), 'the issue was scrolled back into the window to be focused');
});

test('renderScan({focusTitle}) overrides which issue ends up focused', () => {
  setup(300);
  row(1).focus();
  scanState.sel = 7;
  renderScan({ focusTitle: title(7) }); flush();
  assert.equal(activeIndex(), 7);
});

test('a render with no focus inside the list leaves focus alone', () => {
  setup(300);
  const outside = new El({ id: 'toolbar', tabindex: '0' });
  outside.parent = doc.body; doc.body.children.push(outside);
  outside.focus();
  renderScan(); flush();
  assert.equal(doc.activeElement, outside);
});
