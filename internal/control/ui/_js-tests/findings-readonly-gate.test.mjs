// Behavioural contract for findings.uiEditing = false. The REAL modules run
// against a stub DOM; fetch is the observation point: with the gate off, no
// finding write may reach the network no matter which entry point is called.
import test from 'node:test';
import assert from 'node:assert/strict';

class Obs { observe() {} disconnect() {} unobserve() {} }
const toasts = [];
const node = () => {
  const n = {
    children: [], dataset: {}, style: {}, textContent: '', className: '', innerHTML: '', hidden: false, tabIndex: 0,
    classList: { add() {}, remove() {}, toggle() {}, contains: () => false },
    addEventListener() {}, removeEventListener() {}, setAttribute() {}, getAttribute: () => null, removeAttribute() {},
    querySelector: () => null, querySelectorAll: () => [], closest: () => null, contains: () => false,
    append() {}, remove() {}, focus() {}, matches: () => false, getBoundingClientRect: () => ({}),
    appendChild(c) { if (n.isToast) toasts.push(c); return c; },
  };
  return n;
};
const toastHost = node(); toastHost.isToast = true;
globalThis.MutationObserver = Obs; globalThis.ResizeObserver = Obs; globalThis.IntersectionObserver = Obs;
globalThis.document = Object.assign(node(), {
  createElement: node, getElementById: () => node(), getElementsByClassName: () => [], getElementsByTagName: () => [], createDocumentFragment: node, createTextNode: node, documentElement: node(), body: node(), activeElement: null,
  querySelector: (sel) => (sel === '#toast' ? toastHost : null), querySelectorAll: () => [], readyState: 'complete',
});
globalThis.window = globalThis;
globalThis.addEventListener = () => {};
globalThis.location = { hash: '', search: '', pathname: '/' };
globalThis.matchMedia = () => ({ matches: false, addEventListener() {}, addListener() {} });
globalThis.requestAnimationFrame = (f) => f();
globalThis.localStorage = { getItem: () => null, setItem() {}, removeItem() {} };
globalThis.sessionStorage = globalThis.localStorage;

const calls = [];
globalThis.fetch = async (path, init = {}) => {
  calls.push({ path: String(path), method: (init.method || 'GET').toUpperCase() });
  return { ok: true, status: 200, headers: { get: () => 'application/json' }, json: async () => ({ id: 7, findings: [], revisions: [], revision: {}, diff: [] }), text: async () => '' };
};
const writes = () => calls.filter((c) => c.method !== 'GET' && c.method !== 'HEAD');
const reset = () => { calls.length = 0; toasts.length = 0; };
const OFF_MSG = 'Findings editing is off. Enable it in Settings.';

const core = await import('../js/core.js');
const evidence = await import('../js/evidence-attach.js');
const revisions = await import('../js/finding-revisions.js');

test('core exports the single findingsEditable chokepoint', () => {
  assert.equal(typeof core.findingsEditable, 'function');
  core.state.findingsUIEditing = false;
  assert.equal(core.findingsEditable(), false);
  core.state.findingsUIEditing = true;
  assert.equal(core.findingsEditable(), true);
});

test('attachEvidence performs no write when editing is off, and toasts once', async () => {
  core.state.findingsUIEditing = false; reset();
  const r = await evidence.attachEvidence({ kind: 'flow', refs: [3] }, { findingId: 5 });
  assert.equal(writes().length, 0, JSON.stringify(calls));
  assert.equal(calls.length, 0, 'not even a picker read');
  assert.ok(!r.attached);
  assert.deepEqual(toasts.map((t) => t.textContent), [OFF_MSG]);
});

test('attachEvidence still writes when editing is on', async () => {
  core.state.findingsUIEditing = true; reset();
  await evidence.attachEvidence({ kind: 'flow', refs: [3] }, { findingId: 5 });
  assert.ok(writes().some((c) => c.method === 'POST' && c.path === '/api/findings/5/flows'), JSON.stringify(calls));
});

test('attachEvidence for a new finding and for screenshots is also blocked when off', async () => {
  core.state.findingsUIEditing = false; reset();
  await evidence.attachEvidence({ kind: 'shot', refs: [{ data: 'data:image/png;base64,AA', mime: 'image/png', alt: 'x' }] }, { findingId: 5 });
  await evidence.attachEvidence({ kind: 'note', refs: [1] });
  assert.equal(calls.length, 0, JSON.stringify(calls));
});

test('revision restore never reaches the network when off', async () => {
  core.state.findingsUIEditing = false; reset();
  assert.equal(typeof revisions.restoreFindingRevision, 'function');
  await revisions.restoreFindingRevision(5, 2, { isConnected: true }, () => true, async () => {});
  await revisions.openDeletedFindings({ canRestore: () => true, restored: async () => {} });
  assert.equal(calls.length, 0, JSON.stringify(calls));
  assert.ok(toasts.some((t) => t.textContent === OFF_MSG));
});
