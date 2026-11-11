// codecs.js: Starlark typed into the codec editor must not be thrown away by
// opening another codec, pressing New, or closing and reopening the dialog.
// The real module runs against a stub DOM. The confirm dialog is absent there,
// so uiConfirm answers "no": the safe default, which must keep the draft.
import test from 'node:test';
import assert from 'node:assert/strict';

class Obs { observe() {} disconnect() {} unobserve() {} }
const node = () => ({
  children: [], dataset: {}, style: {}, textContent: '', className: '', innerHTML: '', hidden: false, tabIndex: 0, value: '',
  classList: { add() {}, remove() {}, toggle() {}, contains: () => false },
  addEventListener() {}, removeEventListener() {}, setAttribute() {}, getAttribute: () => null, removeAttribute() {},
  querySelector: () => null, querySelectorAll: () => [], closest: () => null, contains: () => false,
  append() {}, remove() {}, focus() {}, matches: () => false, getBoundingClientRect: () => ({}), appendChild(c) { return c; },
});
const els = {};
for (const id of ['codecId', 'codecSrc', 'codecOut', 'codecsList', 'codecsModal', 'codecsSearch', 'codecSave', 'codecTest', 'codecDelete', 'codecNew', 'codecsBtn', 'codecsClose', 'toast']) els['#' + id] = node();
globalThis.MutationObserver = Obs; globalThis.ResizeObserver = Obs; globalThis.IntersectionObserver = Obs;
globalThis.document = Object.assign(node(), {
  createElement: node, getElementById: () => node(), getElementsByClassName: () => [], getElementsByTagName: () => [], createDocumentFragment: node, createTextNode: node, documentElement: node(), body: node(), activeElement: null,
  querySelector: (sel) => els[sel] || null, querySelectorAll: () => [], readyState: 'complete',
});
globalThis.window = globalThis;
globalThis.addEventListener = () => {};
globalThis.location = { hash: '', search: '', pathname: '/' };
globalThis.matchMedia = () => ({ matches: false, addEventListener() {}, addListener() {} });
globalThis.requestAnimationFrame = (f) => f();
globalThis.localStorage = { getItem: () => null, setItem() {}, removeItem() {} };
globalThis.sessionStorage = globalThis.localStorage;

const stored = { alpha: 'def a(): pass', beta: 'def b(): pass' };
const puts = [];
const reply = (status, body) => ({ ok: status < 400, status, statusText: '', headers: { get: () => 'application/json' }, json: async () => body, text: async () => JSON.stringify(body) });
globalThis.fetch = async (path, init = {}) => {
  const method = (init.method || 'GET').toUpperCase();
  const m = /^\/api\/codecs\/([^/]+)$/.exec(String(path));
  if (String(path) === '/api/codecs') return reply(200, { codecs: Object.keys(stored).map((id) => ({ id })) });
  if (m && method === 'GET') return reply(200, { id: m[1], source: stored[m[1]] });
  if (m && method === 'PUT') { stored[m[1]] = JSON.parse(init.body).source; puts.push(m[1]); return reply(200, {}); }
  return reply(200, {});
};

const codecs = await import('../js/codecs.js');
const id = els['#codecId'], src = els['#codecSrc'];

test('the module exposes what the dialog buttons call', () => {
  for (const name of ['openCodecs', 'openCodec', 'codecNew', 'codecSave']) assert.equal(typeof codecs[name], 'function', name);
});

test('opening another codec keeps an unsaved draft unless the operator confirms', async () => {
  codecs.openCodecs();
  await codecs.openCodec('alpha');
  assert.equal(src.value, 'def a(): pass');
  src.value = 'def a():\n    return "hours of work"';
  await codecs.openCodec('beta');
  assert.equal(src.value, 'def a():\n    return "hours of work"', 'the draft is still in the editor');
  assert.equal(id.value, 'alpha');
});

test('New keeps an unsaved draft unless the operator confirms', async () => {
  await codecs.codecNew();
  assert.equal(src.value, 'def a():\n    return "hours of work"');
});

test('closing and reopening the dialog keeps an unsaved draft', () => {
  codecs.openCodecs();
  assert.equal(src.value, 'def a():\n    return "hours of work"');
  assert.equal(id.value, 'alpha');
});

test('after a save the editor is clean, so another codec opens without a prompt', async () => {
  await codecs.codecSave();
  assert.deepEqual(puts, ['alpha']);
  await codecs.openCodec('beta');
  assert.equal(src.value, 'def b(): pass');
  assert.equal(id.value, 'beta');
});

test('an untouched editor is replaced as before', async () => {
  await codecs.openCodec('alpha');
  assert.equal(src.value, 'def a():\n    return "hours of work"');
  await codecs.codecNew();
  assert.equal(id.value, 'aes-content-field');
  assert.match(src.value, /^meta = \{/);
});
