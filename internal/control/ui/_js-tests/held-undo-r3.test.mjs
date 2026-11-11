// The Undo toast of a deferred Intercept drop pauses the countdown while it is hovered
// OR focused. The two are independent: the pointer leaving must not restart the timer
// while keyboard focus is still on Undo (the drop is irreversible), and the reverse.
import test from 'node:test';
import assert from 'node:assert/strict';
import { showDropToast } from '../js/held-undo.js';
import { createDropScheduler } from '../js/intercept-model.js';

function fakeDocument() {
  const mkNode = (tag) => ({
    tagName: tag, children: [], listeners: {}, attrs: {}, className: '', parent: null,
    setAttribute(k, v) { this.attrs[k] = v; },
    append(...n) { for (const c of n) { c.parent = this; this.children.push(c); } },
    appendChild(n) { n.parent = this; this.children.push(n); return n; },
    remove() { if (this.parent) this.parent.children = this.parent.children.filter((c) => c !== this); this.parent = null; },
    addEventListener(type, fn) { (this.listeners[type] ||= []).push(fn); },
    fire(type) { for (const fn of this.listeners[type] || []) fn({ type }); },
  });
  const host = mkNode('div');
  return { host, getElementById: (id) => (id === 'toast' ? host : null), createElement: mkNode, createTextNode: (t) => ({ text: t }) };
}

function setup() {
  const doc = fakeDocument();
  globalThis.document = doc;
  const log = [];
  const t = showDropToast('Dropping request', { onUndo: () => log.push('undo'), onPause: () => log.push('pause'), onResume: () => log.push('resume') });
  return { doc, log, node: t.node, t };
}

test('the pointer leaving does not restart the countdown while Undo still has focus', () => {
  const { log, node } = setup();
  node.fire('focusin');
  node.fire('mouseenter');
  node.fire('mouseleave');
  assert.ok(!log.includes('resume'), 'still focused: ' + log.join(','));
  node.fire('focusout');
  assert.equal(log.filter((x) => x === 'resume').length, 1);
});

test('focus leaving does not restart the countdown while the pointer is still over the toast', () => {
  const { log, node } = setup();
  node.fire('mouseenter');
  node.fire('focusin');
  node.fire('focusout');
  assert.ok(!log.includes('resume'), 'still hovered: ' + log.join(','));
  node.fire('mouseleave');
  assert.equal(log.filter((x) => x === 'resume').length, 1);
});

test('with the real scheduler a hovered-then-left toast keeps a focused drop pending', () => {
  let clock = 0; const timers = new Map(); let seq = 0;
  const committed = [];
  const sched = createDropScheduler({
    delay: 5000, now: () => clock, commit: (e) => committed.push(e),
    setTimeout: (fn, ms) => { timers.set(++seq, { fn, at: clock + ms }); return seq; },
    clearTimeout: (id) => timers.delete(id),
  });
  const advance = (ms) => { clock += ms; for (const [id, t] of [...timers]) if (t.at <= clock) { timers.delete(id); t.fn(); } };
  globalThis.document = fakeDocument();
  sched.schedule('req:1', { id: 1 });
  const t = showDropToast('Dropping request', { onUndo: () => sched.undo('req:1'), onPause: () => sched.pause('req:1'), onResume: () => sched.resume('req:1') });
  advance(1000);
  t.node.fire('focusin');   // Tab reached Undo
  t.node.fire('mouseenter');
  t.node.fire('mouseleave'); // the pointer only crossed the toast
  advance(60000);
  assert.deepEqual(committed, [], 'the drop must wait while Undo is focused');
  t.node.fire('focusout');
  advance(3999);
  assert.deepEqual(committed, []);
  advance(1);
  assert.deepEqual(committed, [{ id: 1 }]);
});
