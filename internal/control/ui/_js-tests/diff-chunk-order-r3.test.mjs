// A diff is rendered in chunks of RENDER_CHUNK rows per animation frame. Every chunk
// has to land where its hunk is, so a long change block followed by more of the
// document cannot end up below the lines that come after it.
import test from 'node:test';
import assert from 'node:assert/strict';
import { createDiffView, diffLines, RENDER_CHUNK } from '../js/diff.js';

// A minimal DOM: enough for createDiffView (elements, text, fragments, insertBefore).
function fakeDoc() {
  class Node {
    constructor(tag) { this.tagName = tag; this.children = []; this.parentNode = null; this.dataset = {}; this.className = ''; this._text = null; this.listeners = {}; this.classList = { add() {} }; }
    get textContent() { return this._text != null ? this._text : this.children.map((c) => c.textContent).join(''); }
    set textContent(v) { for (const c of this.children) c.parentNode = null; this.children = []; this._text = v === '' ? null : String(v); }
    get nextSibling() { const p = this.parentNode; if (!p) return null; return p.children[p.children.indexOf(this) + 1] || null; }
    setAttribute() {}
    addEventListener(type, fn) { this.listeners[type] = fn; }
    appendChild(n) { return this.insertBefore(n, null); }
    querySelectorAll(sel) {
      if (sel !== '[data-change-start]') throw new Error('unsupported selector ' + sel);
      const out = [];
      const walk = (e) => { for (const c of e.children) { if (c.dataset.changeStart) out.push(c); walk(c); } };
      walk(this);
      return out;
    }
    removeChild(n) { const i = this.children.indexOf(n); if (i < 0) throw new Error('NotFoundError'); this.children.splice(i, 1); n.parentNode = null; return n; }
    remove() { if (this.parentNode) this.parentNode.removeChild(this); }
    insertBefore(n, before) {
      const list = n.tagName === '#fragment' ? n.children.splice(0) : [n];
      if (n.tagName === '#fragment') for (const c of list) c.parentNode = null;
      let at = this.children.length;
      if (before) { at = this.children.indexOf(before); if (at < 0) throw new Error('NotFoundError'); }
      for (const c of list) { if (c.parentNode) c.parentNode.removeChild(c); c.parentNode = this; }
      this.children.splice(at, 0, ...list);
      return n;
    }
  }
  return {
    createElement: (t) => new Node(t),
    createTextNode: (t) => { const n = new Node('#text'); n._text = String(t); return n; },
    createDocumentFragment: () => new Node('#fragment'),
    createComment: () => new Node('#comment'),
  };
}

function withFrames(fn) {
  const queue = [];
  const prev = globalThis.requestAnimationFrame;
  globalThis.requestAnimationFrame = (cb) => { queue.push(cb); return queue.length; };
  const flush = () => { let guard = 0; while (queue.length && guard++ < 10000) queue.shift()(); };
  try { return fn(flush, queue); } finally { globalThis.requestAnimationFrame = prev; }
}

// What a reader sees, top to bottom: the text of every row, an expander as "…".
const rowsOf = (container) => {
  const body = container.children.find((c) => c.className === 'diff-body');
  return body.children.map((r) => (r.tagName === 'button' ? '…' : r.children[r.children.length - 1].textContent));
};

test('a change block longer than one render chunk stays above the lines that follow it', () => {
  const n = RENDER_CHUNK + 150;
  const a = ['head'];
  const b = ['head'];
  for (let i = 0; i < n; i++) b.push('new ' + i);
  for (let i = 0; i < 12; i++) { a.push('same ' + i); b.push('same ' + i); }
  a.push('old tail'); b.push('new tail');
  withFrames((flush) => {
    const d = fakeDoc();
    const container = d.createElement('div');
    const view = createDiffView(container, { doc: d });
    view.setResult(diffLines(a, b));
    flush();
    const rows = rowsOf(container);
    const want = ['head'];
    for (let i = 0; i < n; i++) want.push('new ' + i);
    want.push('same 0', 'same 1', 'same 2', '…', 'same 9', 'same 10', 'same 11', 'old tail', 'new tail');
    assert.equal(rows.length, want.length);
    assert.equal(rows.indexOf('new ' + (n - 1)) + 1, rows.indexOf('same 0'), 'the last added line is directly above the unchanged line after it');
    assert.deepEqual(rows, want);
    const body = container.children.find((c) => c.className === 'diff-body');
    assert.ok(body.children.every((c) => c.tagName !== '#comment'), 'the position marker is removed once the run is complete');
  });
});

test('next/prev visit change blocks in document order when rendering took several frames', () => {
  const a = ['x'];
  const b = ['x'];
  for (let i = 0; i < RENDER_CHUNK + 10; i++) b.push('ins ' + i);
  for (let i = 0; i < 3; i++) { a.push('near ' + i); b.push('near ' + i); }
  a.push('mid'); // a second change block in the same hunk, past the first chunk
  for (let i = 0; i < 10; i++) { a.push('keep ' + i); b.push('keep ' + i); }
  a.push('gone');
  withFrames((flush) => {
    const d = fakeDoc();
    const container = d.createElement('div');
    const view = createDiffView(container, { doc: d });
    view.setResult(diffLines(a, b));
    flush();
    const focused = [];
    const body = container.children.find((c) => c.className === 'diff-body');
    for (const r of body.children) { r.focus = () => focused.push(r.children[r.children.length - 1].textContent); }
    view.next(); view.next(); view.next();
    assert.deepEqual(focused, ['ins 0', 'mid', 'gone']);
  });
});

test('expanding a long unchanged region while the next expander is opened keeps every line in place', () => {
  const a = [], b = [];
  a.push('A0'); b.push('B0');
  for (let i = 0; i < RENDER_CHUNK + 40; i++) { a.push('u' + i); b.push('u' + i); }
  a.push('A1'); b.push('B1');
  for (let i = 0; i < 30; i++) { a.push('v' + i); b.push('v' + i); }
  a.push('A2'); b.push('B2');
  withFrames((flush) => {
    const d = fakeDoc();
    const container = d.createElement('div');
    const view = createDiffView(container, { doc: d });
    view.setResult(diffLines(a, b));
    flush();
    const body = container.children.find((c) => c.className === 'diff-body');
    const expanders = body.children.filter((c) => c.tagName === 'button');
    assert.equal(expanders.length, 2);
    expanders[0].listeners.click(); // first chunk now, the rest next frame
    expanders[1].listeners.click(); // the second region is opened before that frame
    assert.doesNotThrow(flush);
    const rows = rowsOf(container).filter((t) => /^[uv]\d+$/.test(t));
    const want = [];
    for (let i = 0; i < RENDER_CHUNK + 40; i++) want.push('u' + i);
    for (let i = 0; i < 30; i++) want.push('v' + i);
    assert.deepEqual(rows, want);
  });
});
