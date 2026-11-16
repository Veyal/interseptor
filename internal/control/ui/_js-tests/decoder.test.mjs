// decoder.js UI state & grouped operation bar behavior.
import test from 'node:test';
import assert from 'node:assert/strict';

class Obs { observe() {} disconnect() {} unobserve() {} }
const makeNode = (tag = 'div') => {
  const el = {
    tagName: tag.toUpperCase(),
    children: [],
    dataset: {},
    style: {
      display: 'none',
      getPropertyValue: () => '',
      getPropertyPriority: () => '',
      setProperty() {},
      removeProperty() {},
    },
    textContent: '',
    className: '',
    _innerHTML: '',
    hidden: false,
    disabled: false,
    tabIndex: 0,
    value: '',
    attrs: {},
    listeners: {},
    classList: {
      set: new Set(),
      add(...c) { c.forEach((x) => this.set.add(x)); },
      remove(...c) { c.forEach((x) => this.set.delete(x)); },
      toggle(c, on) { if (on === undefined) { if (this.set.has(c)) this.set.delete(c); else this.set.add(c); } else if (on) this.set.add(c); else this.set.delete(c); },
      contains(c) { return this.set.has(c); },
    },
    addEventListener(ev, fn) { (el.listeners[ev] ||= []).push(fn); },
    removeEventListener() {},
    setAttribute(k, v) { el.attrs[k] = String(v); },
    getAttribute(k) { return Object.prototype.hasOwnProperty.call(el.attrs, k) ? el.attrs[k] : null; },
    hasAttribute(k) { return Object.prototype.hasOwnProperty.call(el.attrs, k); },
    removeAttribute(k) { delete el.attrs[k]; },
    querySelector: () => null,
    querySelectorAll: () => [],
    closest: () => null,
    contains: () => false,
    append(...items) { el.children.push(...items); },
    appendChild(c) { el.children.push(c); return c; },
    remove() {},
    focus() {},
    matches: () => false,
    getBoundingClientRect: () => ({}),
  };
  return el;
};

const els = {};
const ids = ['decModal', 'decIn', 'decOut', 'decOps', 'decErr', 'decLoad', 'decClose', 'decUp', 'decCopy', 'decClear', 'decInMeta', 'decOutMeta', 'decChain', 'toast'];
for (const id of ids) els['#' + id] = makeNode();
const decDialog = makeNode();
els['#decModal'].querySelector = (sel) => (sel === '[role="dialog"]' ? decDialog : null);
const opButtons = [];
els['#decOps'] = makeNode();
Object.defineProperty(els['#decOps'], 'innerHTML', {
  get() { return this._innerHTML; },
  set(html) {
    this._innerHTML = html;
    opButtons.length = 0;
    const re = /data-op="([^"]+)"[^>]*>([^<]*)</g;
    let m;
    while ((m = re.exec(html)) !== null) {
      const btn = makeNode('button');
      btn.dataset.op = m[1];
      btn.textContent = m[2];
      opButtons.push(btn);
    }
  },
});
els['#decOps'].querySelectorAll = (sel) => (sel === '[data-op]' ? opButtons : []);

globalThis.MutationObserver = Obs; globalThis.ResizeObserver = Obs; globalThis.IntersectionObserver = Obs;
globalThis.document = Object.assign(makeNode(), {
  createElement: makeNode,
  getElementById: (id) => els['#' + id] || makeNode(),
  getElementsByClassName: () => [],
  getElementsByTagName: () => [],
  createDocumentFragment: makeNode,
  createTextNode: makeNode,
  documentElement: makeNode(),
  body: makeNode(),
  activeElement: null,
  querySelector: (sel) => els[sel] || null,
  querySelectorAll: () => [],
  readyState: 'complete',
});
globalThis.window = globalThis;
globalThis.addEventListener = () => {};
globalThis.location = { hash: '', search: '', pathname: '/' };
globalThis.matchMedia = () => ({ matches: false, addEventListener() {}, addListener() {} });
globalThis.requestAnimationFrame = (f) => f();
globalThis.localStorage = { getItem: () => null, setItem() {}, removeItem() {} };
globalThis.sessionStorage = globalThis.localStorage;

let nextReply = { output: 'hello' };
globalThis.fetch = async (url, init) => {
  const body = JSON.parse(init.body);
  const res = typeof nextReply === 'function' ? nextReply(body) : nextReply;
  return {
    ok: true,
    status: 200,
    statusText: 'OK',
    headers: { get: () => 'application/json' },
    json: async () => res,
    text: async () => JSON.stringify(res),
  };
};

const { decBuildOps, decApply, openDecoder } = await import('../js/decoder.js');

test('decBuildOps renders grouped Decode, Encode, and Inspect operations with base64decode first', () => {
  decBuildOps();
  assert.match(els['#decOps'].innerHTML, /Decode/);
  assert.match(els['#decOps'].innerHTML, /Encode/);
  assert.match(els['#decOps'].innerHTML, /Inspect/);
  assert.equal(opButtons[0]?.dataset.op, 'base64decode');
  assert.ok(opButtons.some((b) => b.dataset.op === 'smart'));
});

test('openDecoder seeds input metadata, applies transform, updates chain, and clear resets', async () => {
  nextReply = { output: 'decoded-value' };
  openDecoder('aGVsbG8=');
  assert.equal(els['#decIn'].value, 'aGVsbG8=');
  assert.match(els['#decInMeta'].textContent, /8 chars/);

  await decApply('base64decode');
  assert.equal(els['#decOut'].value, 'decoded-value');
  assert.match(els['#decOutMeta'].textContent, /13 chars/);
  assert.equal(opButtons.find((b) => b.dataset.op === 'base64decode')?.getAttribute('aria-pressed'), 'true');
  assert.match(els['#decChain'].textContent, /Base64 ↓/);

  els['#decUp'].onclick();
  assert.equal(els['#decIn'].value, 'decoded-value');
  assert.equal(els['#decOut'].value, '');

  nextReply = { error: 'invalid hex' };
  await decApply('hexdecode');
  assert.equal(els['#decErr'].dataset.tone, 'error');
  assert.equal(els['#decErr'].textContent, 'invalid hex');

  els['#decClear'].onclick();
  assert.equal(els['#decIn'].value, '');
  assert.equal(els['#decOut'].value, '');
  assert.equal(els['#decChain'].textContent, '');
});
