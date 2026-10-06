import test from 'node:test';
import assert from 'node:assert/strict';
import { fuzzyScore, fuzzyRank, createKeyRegistry, parseKeys, isTypingTarget, eventStep } from '../js/keys.js';

const ev = (key, extra = {}) => ({ key, ctrlKey: false, metaKey: false, altKey: false, shiftKey: false, target: { tagName: 'DIV' }, preventDefault() { this.prevented = true; }, ...extra });

function clock() {
  let t = 1000;
  const timers = [];
  return {
    now: () => t,
    advance(ms) { t += ms; for (const x of timers.slice()) if (x.at <= t && !x.done) { x.done = true; x.fn(); } },
    setTimeout(fn, ms) { const x = { at: t + ms, fn, done: false }; timers.push(x); return x; },
    clearTimeout(x) { if (x) x.done = true; },
  };
}
function make(extra = {}) {
  const c = clock();
  const calls = [];
  const reg = createKeyRegistry({ now: c.now, setTimeout: c.setTimeout, clearTimeout: c.clearTimeout, singleKeyEnabled: () => true, isModalOpen: () => false, ...extra });
  const bind = (id, keys, scope = 'global', more = {}) => reg.register({ id, keys, scope, label: id, group: 'Test', run: () => calls.push(id), ...more });
  return { c, calls, reg, bind };
}

test('fuzzyScore: subsequence match, no match, empty query', () => {
  assert.ok(fuzzyScore('gp', 'Go to Proxy') > 0);
  assert.equal(fuzzyScore('zzz', 'Go to Proxy'), -1);
  assert.equal(fuzzyScore('', 'anything'), 0);
  assert.equal(fuzzyScore('abc', 'ab'), -1);
});

test('fuzzyScore: word-boundary and contiguous matches outrank scattered ones', () => {
  const boundary = fuzzyScore('gp', 'Go Proxy');
  const scattered = fuzzyScore('gp', 'xxgxxxxpxx');
  assert.ok(boundary > scattered, `${boundary} should beat ${scattered}`);
  assert.ok(fuzzyScore('prox', 'Proxy history') > fuzzyScore('prox', 'p r o x scattered'));
  assert.ok(fuzzyScore('find', 'Findings') > fuzzyScore('find', 'Intruder: finish daily'));
});

test('fuzzyRank: orders by score, stable on ties, honours limit and empty query', () => {
  const items = ['Settings', 'Scanner', 'Proxy', 'Repeater', 'Proxy history'];
  assert.deepEqual(fuzzyRank('prox', items).slice(0, 2), ['Proxy', 'Proxy history']);
  assert.deepEqual(fuzzyRank('', items, { limit: 2 }), ['Settings', 'Scanner']);
  assert.deepEqual(fuzzyRank('qqq', items), []);
  const objs = [{ t: 'beta' }, { t: 'alpha' }];
  assert.deepEqual(fuzzyRank('a', objs, { getText: (o) => o.t }).map((o) => o.t), ['alpha', 'beta']);
});

test('parseKeys normalises chords and modifiers', () => {
  assert.deepEqual(parseKeys('g p'), ['g', 'p']);
  assert.deepEqual(parseKeys('Ctrl+Enter'), ['Mod+Enter']);
  assert.deepEqual(parseKeys('Cmd+K'), ['Mod+k']);
  assert.deepEqual(parseKeys('Shift+Enter'), ['Shift+Enter']);
  assert.deepEqual(parseKeys('N'), ['N']);
  assert.deepEqual(parseKeys('Esc'), ['Escape']);
  assert.throws(() => parseKeys('  '));
});

test('eventStep maps keyboard events to canonical steps', () => {
  assert.equal(eventStep(ev('k', { ctrlKey: true })), 'Mod+k');
  assert.equal(eventStep(ev('k', { metaKey: true })), 'Mod+k');
  assert.equal(eventStep(ev('Enter', { shiftKey: true })), 'Shift+Enter');
  assert.equal(eventStep(ev('N', { shiftKey: true })), 'N');
  assert.equal(eventStep(ev('e')), 'e');
});

test('register refuses duplicate bindings in the same scope but allows other scopes', () => {
  const { bind } = make();
  bind('a', 'e', 'proxy');
  assert.throws(() => bind('b', 'e', 'proxy'), /already bound/);
  assert.doesNotThrow(() => bind('c', 'e', 'drawer'));
  assert.throws(() => bind('a', 'x', 'proxy'), /duplicate id/);
});

test('register refuses a binding that shadows or is shadowed by a chord prefix', () => {
  const { bind } = make();
  bind('copy', 'y c', 'proxy');
  assert.throws(() => bind('yank', 'y', 'proxy'), /prefix/);
  const m = make();
  m.bind('yank', 'y', 'proxy');
  assert.throws(() => m.bind('copy', 'y c', 'proxy'), /prefix/);
});

test('single key fires in scope and ignores inactive scopes', () => {
  const { reg, bind, calls } = make();
  bind('attach', 'e', 'proxy');
  assert.equal(reg.handle(ev('e'), { scopes: ['global'] }), false);
  assert.equal(reg.handle(ev('e'), { scopes: ['global', 'proxy'] }), true);
  assert.deepEqual(calls, ['attach']);
});

test('single keys are gated by typing targets, role=textbox, contenteditable, modals and the settings switch', () => {
  let modal = false, enabled = true;
  const { reg, bind, calls } = make({ isModalOpen: () => modal, singleKeyEnabled: () => enabled });
  bind('attach', 'e', 'proxy');
  bind('send', 'Ctrl+Enter', 'proxy');
  const ctx = { scopes: ['proxy'] };
  for (const target of [{ tagName: 'INPUT', type: 'text' }, { tagName: 'TEXTAREA' }, { tagName: 'DIV', isContentEditable: true }, { tagName: 'DIV', getAttribute: (n) => (n === 'role' ? 'textbox' : null) }]) {
    assert.equal(reg.handle(ev('e', { target }), ctx), false);
  }
  assert.equal(reg.handle(ev('e', { target: { tagName: 'INPUT', type: 'checkbox' } }), ctx), true);
  modal = true;
  assert.equal(reg.handle(ev('e'), ctx), false);
  modal = false; enabled = false;
  assert.equal(reg.handle(ev('e'), ctx), false);
  // modified shortcuts remain available when the switch is off and inside inputs
  assert.equal(reg.handle(ev('Enter', { ctrlKey: true, target: { tagName: 'TEXTAREA' } }), ctx), true);
  assert.deepEqual(calls, ['attach', 'send']);
});

test('isTypingTarget classifies controls', () => {
  assert.equal(isTypingTarget(null), false);
  assert.equal(isTypingTarget({ tagName: 'BUTTON' }), false);
  assert.equal(isTypingTarget({ tagName: 'INPUT', type: 'search' }), true);
  assert.equal(isTypingTarget({ tagName: 'INPUT', type: 'radio' }), false);
  assert.equal(isTypingTarget({ tagName: 'SELECT' }), true);
});

test('chord resolves in sequence, exposes continuations and clears', () => {
  const { reg, bind, calls } = make();
  bind('curl', 'y c', 'proxy', { label: 'Copy as curl' });
  bind('fetch', 'y f', 'proxy', { label: 'Copy as fetch' });
  const ctx = { scopes: ['proxy'] };
  assert.equal(reg.handle(ev('y'), ctx), true);
  const p = reg.pending();
  assert.deepEqual(p.typed, ['y']);
  assert.deepEqual(p.continuations.map((x) => x.keys).sort(), ['y c', 'y f']);
  assert.equal(reg.handle(ev('c'), ctx), true);
  assert.deepEqual(calls, ['curl']);
  assert.equal(reg.pending(), null);
});

test('chord expires after 1200ms and Escape cancels', () => {
  const { reg, bind, calls, c } = make();
  bind('curl', 'y c', 'proxy');
  const ctx = { scopes: ['proxy'] };
  reg.handle(ev('y'), ctx);
  c.advance(1201);
  assert.equal(reg.pending(), null);
  assert.equal(reg.handle(ev('c'), ctx), false);
  reg.handle(ev('y'), ctx);
  const esc = ev('Escape');
  assert.equal(reg.handle(esc, ctx), true);
  assert.equal(esc.prevented, true);
  assert.equal(reg.pending(), null);
  assert.deepEqual(calls, []);
});

test('a wrong second key cancels the chord and is treated as a fresh key', () => {
  const { reg, bind, calls } = make();
  bind('curl', 'y c', 'proxy');
  bind('attach', 'e', 'proxy');
  const ctx = { scopes: ['proxy'] };
  reg.handle(ev('y'), ctx);
  assert.equal(reg.handle(ev('e'), ctx), true);
  assert.deepEqual(calls, ['attach']);
});

test('when() predicates and unregister', () => {
  let ok = false;
  const { reg, bind, calls } = make();
  bind('diff', 'd', 'proxy', { when: () => ok });
  const ctx = { scopes: ['proxy'] };
  assert.equal(reg.handle(ev('d'), ctx), false);
  ok = true;
  assert.equal(reg.handle(ev('d'), ctx), true);
  reg.unregister('diff');
  assert.equal(reg.handle(ev('d'), ctx), false);
  assert.deepEqual(calls, ['diff']);
});

test('cheatsheet merges registry bindings with the static legacy table', () => {
  const { reg, bind } = make();
  bind('attach', 'e', 'proxy', { label: 'Attach as evidence', group: 'Proxy' });
  const sheet = reg.cheatsheet([{ keys: 'g p', label: 'Go to Proxy', group: 'Navigation' }]);
  const groups = sheet.map((g) => g.group);
  assert.deepEqual(groups, ['Navigation', 'Proxy']);
  assert.deepEqual(sheet[1].items[0], { keys: 'e', label: 'Attach as evidence', scope: 'proxy' });
  assert.equal(sheet[0].items[0].legacy, true);
});
