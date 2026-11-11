// notes.js + core.js createAutosave: "Saved" must mean the server acknowledged
// exactly the text in the editor. The REAL modules run against a stub DOM; fetch
// plays the server (conditional PUT with baseRev, 409 when the notebook moved).
import test from 'node:test';
import assert from 'node:assert/strict';

class Obs { observe() {} disconnect() {} unobserve() {} }
const node = () => {
  const n = {
    children: [], dataset: {}, style: {}, textContent: '', className: '', innerHTML: '', hidden: false, tabIndex: 0, value: '',
    selectionStart: 0, selectionEnd: 0,
    classList: { add() {}, remove() {}, toggle() {}, contains: () => false },
    addEventListener() {}, removeEventListener() {}, setAttribute() {}, getAttribute: () => null, removeAttribute() {},
    querySelector: () => null, querySelectorAll: () => [], closest: () => null, contains: () => false,
    append() {}, remove() {}, focus() {}, matches: () => false, getBoundingClientRect: () => ({}),
    appendChild(c) { return c; }, insertBefore(c) { return c; },
  };
  return n;
};
const els = { '#notesEdit': node(), '#notesStatus': node(), '#notesSaveRetry': node(), '#notesPreview': node(), '#toast': node() };
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

// ---- the server: one notebook, conditional saves ----
const server = { notes: '', puts: [], beforePut: null, failPut: null };
const rev = (t) => 'rev:' + t.length + ':' + t;
const reply = (status, body) => ({ ok: status < 400, status, statusText: '', headers: { get: () => 'application/json' }, json: async () => body, text: async () => JSON.stringify(body) });
globalThis.fetch = async (path, init = {}) => {
  const method = (init.method || 'GET').toUpperCase();
  if (String(path) !== '/api/notes') return reply(200, {});
  if (method === 'GET') return reply(200, { notes: server.notes, rev: rev(server.notes) });
  const body = JSON.parse(init.body);
  if (server.beforePut) { const hook = server.beforePut; server.beforePut = null; hook(body); }
  if (server.failPut && server.failPut(body)) return reply(500, { error: 'disk full' });
  if (body.baseRev && body.baseRev !== rev(server.notes)) return reply(409, { error: 'notes changed' });
  server.notes = body.notes;
  server.puts.push(body.notes);
  return reply(200, { rev: rev(server.notes) });
};

const notes = await import('../js/notes.js');
const ta = els['#notesEdit'], status = els['#notesStatus'];
const type = (text) => { ta.value = text; notes.scheduleNotesSave(); };
async function open(text) {
  // Leave the previous case clean: whatever it left unsaved is saved or dropped first.
  server.beforePut = null; server.failPut = null;
  await notes.flushNotesSave();
  server.notes = text; server.puts.length = 0;
  // Edit mode, pinned: the rendered preview needs a real DOM and is not under test.
  Object.assign(notes.notesState, { loaded: '', rev: '', mode: 'edit', chosen: true });
  ta.value = '';
  await notes.loadNotes();
  assert.equal(ta.value, text, 'fixture: the notebook is loaded');
}

test('a plain save reports Saved once the server holds the draft', async () => {
  await open('A');
  type('A and more');
  await notes.flushNotesSave();
  assert.equal(status.textContent, 'Saved');
  assert.equal(server.notes, 'A and more');
});

test('409 whose merge equals the submitted draft is still PUT before "Saved"', async () => {
  await open('A');
  // The operator's draft already ends with exactly what an agent appends meanwhile.
  type('A edited\nB');
  server.beforePut = () => { server.notes = 'A\nB'; };
  await notes.flushNotesSave();
  assert.equal(ta.value, 'A edited\nB', 'the merge keeps the draft as typed');
  assert.equal(status.textContent, 'Saved');
  assert.equal(server.notes, ta.value, '"Saved" while the server still holds ' + JSON.stringify(server.notes));
});

test('409 whose merge changes the draft saves the merged text', async () => {
  await open('A');
  type('A edited');
  server.beforePut = () => { server.notes = 'A\nfrom agent'; };
  await notes.flushNotesSave();
  assert.equal(ta.value, 'A edited\nfrom agent');
  assert.equal(status.textContent, 'Saved');
  assert.equal(server.notes, ta.value);
});

test('after a merge whose follow-up save fails, editing back to the refused draft is not "Saved"', async () => {
  await open('A');
  type('A edited');
  server.beforePut = () => { server.notes = 'A\nfrom agent'; };
  server.failPut = (body) => body.notes === 'A edited\nfrom agent';
  await notes.flushNotesSave();
  assert.equal(server.notes, 'A\nfrom agent', 'fixture: the merged text never reached the server');
  assert.match(status.textContent, /^Save failed/);
  // The refused draft was never acknowledged, so typing it again is an unsaved change.
  type('A edited');
  assert.notEqual(status.textContent, 'Saved', 'the server holds ' + JSON.stringify(server.notes) + ', not the editor text');
  server.failPut = null;
  await notes.flushNotesSave();
  assert.equal(status.textContent, 'Saved');
  assert.equal(server.notes, ta.value);
});

test('a notebook that keeps moving ends in an actionable error, never a false "Saved"', async () => {
  await open('A');
  type('A edited\nB');
  // Every PUT finds one more "\nB" appended elsewhere: each merge keeps the draft as typed.
  const append = () => { server.notes += '\nB'; server.beforePut = append; };
  server.beforePut = append;
  await notes.flushNotesSave();
  assert.equal(ta.value, 'A edited\nB');
  assert.notEqual(server.notes, ta.value, 'fixture: the draft never reached the server');
  assert.match(status.textContent, /^Save failed: Notes keep changing elsewhere/);
  assert.equal(els['#notesSaveRetry'].hidden, false, 'Retry is offered');
  assert.ok(server.puts.length === 0, 'no draft was accepted');
  server.beforePut = null;
  await notes.flushNotesSave();
  assert.equal(status.textContent, 'Saved');
  assert.equal(server.notes, ta.value);
});
