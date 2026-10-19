import test from 'node:test';
import assert from 'node:assert/strict';
import {
  DROP_DELAY_MS, createDropScheduler, forwardAllPlan, forwardPath, dropPath, dropPendingPath,
  scopeNote, scopeBadgeText, identityText, rulesSummary, heldCountText, dropToastText, forwardAllProgress, heldKey,
} from '../js/intercept-model.js';

function fakeTimers() {
  let seq = 0;
  const q = new Map();
  let now = 0;
  return {
    setTimeout(fn, ms) { const id = ++seq; q.set(id, { fn, at: now + ms }); return id; },
    clearTimeout(id) { q.delete(id); },
    now: () => now,
    advance(ms) {
      now += ms;
      for (const [id, t] of [...q]) if (t.at <= now) { q.delete(id); t.fn(); }
    },
    size: () => q.size,
  };
}

test('drop is deferred for 5s and then committed exactly once', () => {
  const t = fakeTimers();
  const committed = [];
  const s = createDropScheduler({ setTimeout: t.setTimeout, clearTimeout: t.clearTimeout, commit: (e) => committed.push(e) });
  assert.equal(DROP_DELAY_MS, 5000);
  assert.equal(s.schedule('req:1', { side: 'req', id: 1 }), true);
  assert.equal(s.schedule('req:1', { side: 'req', id: 1 }), false, 'a second drop of the same item is ignored');
  t.advance(4999);
  assert.deepEqual(committed, []);
  assert.equal(s.has('req:1'), true);
  t.advance(1);
  assert.deepEqual(committed, [{ side: 'req', id: 1 }]);
  assert.equal(s.has('req:1'), false);
  t.advance(10000);
  assert.equal(committed.length, 1);
});

test('undo cancels the pending drop and nothing is ever sent', () => {
  const t = fakeTimers();
  const committed = [];
  const events = [];
  const s = createDropScheduler({ setTimeout: t.setTimeout, clearTimeout: t.clearTimeout, commit: (e) => committed.push(e), onChange: (k, w) => events.push(k + ':' + w) });
  s.schedule('resp:7', { side: 'resp', id: 7 });
  assert.equal(s.undo('resp:7'), true);
  assert.equal(s.undo('resp:7'), false, 'undo after undo is a no-op');
  t.advance(60000);
  assert.deepEqual(committed, []);
  assert.equal(t.size(), 0);
  assert.deepEqual(events, ['resp:7:pending', 'resp:7:undone']);
});

test('undo after the drop was committed reports false (never claims it was undone)', () => {
  const t = fakeTimers();
  const s = createDropScheduler({ setTimeout: t.setTimeout, clearTimeout: t.clearTimeout, commit: () => {} });
  s.schedule('req:2', { side: 'req', id: 2 });
  t.advance(DROP_DELAY_MS);
  assert.equal(s.undo('req:2'), false);
});

test('forward-all skips dropping items and sends edits only when modified', () => {
  const items = [{ side: 'req', id: 1 }, { side: 'req', id: 2 }, { side: 'resp', id: 3 }];
  const rawCache = new Map([[heldKey('req', 1), 'edited'], [heldKey('resp', 3), 'same']]);
  const originalCache = new Map([[heldKey('req', 1), 'orig'], [heldKey('resp', 3), 'same']]);
  const plan = forwardAllPlan(items, { dropping: new Set([heldKey('req', 2)]), rawCache, originalCache });
  assert.deepEqual(plan, [{ side: 'req', id: 1, raw: 'edited' }, { side: 'resp', id: 3 }]);
  assert.deepEqual(forwardAllPlan(null), []);
});

test('endpoints use the existing intercept API paths only', () => {
  assert.equal(forwardPath('req', 4), '/api/intercept/4/forward');
  assert.equal(forwardPath('resp', 4), '/api/intercept/response/4/forward');
  assert.equal(dropPath('req', 4), '/api/intercept/4/drop');
  assert.equal(dropPath('resp', 4), '/api/intercept/response/4/drop');
  assert.equal(dropPendingPath('req', 4), '/api/intercept/4/drop-pending');
  assert.equal(dropPendingPath('resp', 4), '/api/intercept/response/4/drop-pending');
});

// A bulk release (intercept off, closing the window) must not outlive a pending
// drop: takeAll hands the pending drops of one side to the caller to commit now,
// cancels their timers, and leaves the other side's Undo windows running.
test('takeAll commits pending drops of one side immediately and cancels their timers', () => {
  const t = fakeTimers();
  const committed = [];
  const events = [];
  const s = createDropScheduler({ setTimeout: t.setTimeout, clearTimeout: t.clearTimeout, now: t.now, commit: (e) => committed.push(e), onChange: (k, w) => events.push(k + ':' + w) });
  s.schedule('req:1', { side: 'req', id: 1 });
  s.schedule('req:2', { side: 'req', id: 2 });
  s.pause('req:2');
  s.schedule('resp:3', { side: 'resp', id: 3 });
  assert.deepEqual(s.takeAll((e) => e.side === 'req'), [{ side: 'req', id: 1 }, { side: 'req', id: 2 }]);
  assert.equal(s.has('req:1'), false);
  assert.equal(s.has('resp:3'), true);
  assert.equal(s.undo('req:1'), false, 'a taken drop can no longer be undone');
  assert.ok(events.includes('req:1:committed') && events.includes('req:2:committed'));
  t.advance(60000);
  assert.deepEqual(committed, [{ side: 'resp', id: 3 }], 'taken drops never fire again from the timer');
  assert.deepEqual(s.takeAll(), []);
});

test('scope note states real server behaviour and invents no counter', () => {
  assert.equal(scopeNote({ inCount: 2 }).scoped, true);
  assert.match(scopeNote({ inCount: 2 }).text, /out-of-scope traffic is forwarded without a hold/);
  assert.equal(scopeNote({ inCount: 0 }).scoped, false);
  assert.doesNotMatch(scopeNote({ inCount: 2 }).text, /\d+ (request|forwarded)/i);
  assert.equal(scopeBadgeText({ inCount: 3, outCount: 1 }), 'Scope: 3 include / 1 exclude');
  assert.equal(scopeBadgeText(null), 'Scope: all traffic');
});

test('identity, rules and count text', () => {
  assert.equal(identityText('user-b'), 'As user-b');
  assert.equal(identityText(''), 'No identity selected');
  assert.deepEqual(rulesSummary([{ enabled: true }, { enabled: false }]), { total: 2, enabled: 1, label: '1 of 2 on' });
  assert.equal(rulesSummary(undefined).label, 'none');
  assert.equal(heldCountText(2), '2 held');
  assert.equal(heldCountText(0), 'Nothing held');
  assert.equal(dropToastText('req'), 'Dropping request in 5s');
  assert.equal(dropToastText('resp'), 'Dropping response in 5s');
});

test('forward-all progress text', () => {
  assert.equal(forwardAllProgress(2, 5), 'Forwarded 2 of 5');
  assert.equal(forwardAllProgress(5, 5), 'Forwarded 5 of 5');
  assert.equal(forwardAllProgress(2, 5, 'boom'), 'Forward all stopped after 2 of 5: boom');
});

test('pausing the timer (toast hover or focus) keeps the remaining time', () => {
  const t = fakeTimers();
  const committed = [];
  const s = createDropScheduler({ setTimeout: t.setTimeout, clearTimeout: t.clearTimeout, now: t.now, commit: (e) => committed.push(e) });
  s.schedule('req:5', { side: 'req', id: 5 });
  t.advance(3000);
  assert.equal(s.pause('req:5'), true);
  assert.equal(s.pause('req:5'), false, 'already paused');
  t.advance(60000);
  assert.deepEqual(committed, [], 'a paused drop never fires');
  assert.equal(s.resume('req:5'), true);
  t.advance(1999);
  assert.deepEqual(committed, []);
  t.advance(1);
  assert.deepEqual(committed, [{ side: 'req', id: 5 }]);
  assert.equal(s.resume('req:5'), false);
});

test('undo works while paused', () => {
  const t = fakeTimers();
  const committed = [];
  const s = createDropScheduler({ setTimeout: t.setTimeout, clearTimeout: t.clearTimeout, now: t.now, commit: (e) => committed.push(e) });
  s.schedule('req:6', { side: 'req', id: 6 });
  s.pause('req:6');
  assert.equal(s.undo('req:6'), true);
  t.advance(60000);
  assert.deepEqual(committed, []);
});

test('a due drop waits while another held action is in flight, then commits once', () => {
  const t = fakeTimers();
  const committed = [];
  const events = [];
  let busy = true;
  const s = createDropScheduler({ setTimeout: t.setTimeout, clearTimeout: t.clearTimeout, commit: (e) => committed.push(e), onChange: (k, w) => events.push(k + ':' + w), busy: () => busy, retryDelay: 100 });
  s.schedule('req:3', { side: 'req', id: 3 });
  t.advance(5000);
  assert.deepEqual(committed, [], 'not committed while a forward is in flight');
  assert.equal(s.has('req:3'), true, 'still pending so Undo works');
  t.advance(1000);
  assert.deepEqual(committed, []);
  busy = false;
  t.advance(100);
  assert.deepEqual(committed, [{ side: 'req', id: 3 }]);
  assert.equal(s.has('req:3'), false);
  assert.deepEqual(events, ['req:3:pending', 'req:3:committed']);
  t.advance(10000);
  assert.equal(committed.length, 1);
});

test('undo still cancels a drop that is waiting for a busy forward', () => {
  const t = fakeTimers();
  const committed = [];
  const s = createDropScheduler({ setTimeout: t.setTimeout, clearTimeout: t.clearTimeout, commit: (e) => committed.push(e), busy: () => true, retryDelay: 100 });
  s.schedule('req:4', { side: 'req', id: 4 });
  t.advance(5200);
  assert.equal(s.undo('req:4'), true);
  t.advance(60000);
  assert.deepEqual(committed, []);
});

test('auto-forward tally: only proxy-captured out-of-scope requests count while Requests interception is on', async () => {
  const { isAutoForwarded, autoForwardText, createAutoForwardTally } = await import('../js/intercept-model.js');
  const compiled = { evaluable: true, hasInclude: true, rules: [] };
  const inScope = (f) => f.host === 'app.example.com';
  const opts = (over) => ({ interceptOn: true, compiled, flowInScope: inScope, ...over });
  assert.equal(isAutoForwarded({ host: 'cdn.example.net', flags: 0 }, opts()), true);
  assert.equal(isAutoForwarded({ host: 'app.example.com', flags: 0 }, opts()), false, 'in scope is held, not auto-forwarded');
  assert.equal(isAutoForwarded({ host: 'cdn.example.net', flags: 0 }, opts({ interceptOn: false })), false);
  assert.equal(isAutoForwarded({ host: 'cdn.example.net', flags: 0 }, opts({ compiled: { evaluable: false, hasInclude: true, rules: [] } })), false, 'a regex rule is undecidable here: never guess');
  assert.equal(isAutoForwarded({ host: 'cdn.example.net', flags: 0 }, opts({ compiled: { evaluable: true, hasInclude: false, rules: [] } })), false, 'with no include rule everything is in scope');
  for (const flags of [64, 128, 256, 1024, 2048, 4096, 8192]) {
    assert.equal(isAutoForwarded({ host: 'cdn.example.net', flags }, opts()), false, 'non-proxy flag ' + flags);
  }
  assert.equal(autoForwardText(3), 'Out of scope, auto-forwarded: 3');
  const t = createAutoForwardTally();
  t.sync(true); t.add(); t.add();
  assert.equal(t.count(), 2);
  t.sync(true);
  assert.equal(t.count(), 2, 'staying on keeps the count');
  t.sync(false); t.sync(true);
  assert.equal(t.count(), 0, 'turning interception back on restarts the tally');
});

test('forward all treats a line-ending-only difference as untouched, like Forward', async () => {
  const { forwardAllPlan: plan } = await import('../js/intercept-model.js');
  // The textarea LF-normalizes the CRLF original; typing then undoing leaves an
  // LF copy in the cache. Forward omits raw for it, so Forward all must too, or
  // the body's CRLF bytes are silently rewritten to LF.
  const original = 'POST /a HTTP/1.1\r\nHost: example.com\r\n\r\nline1\r\nline2';
  const rawCache = new Map([[heldKey('req', 1), original.replace(/\r\n/g, '\n')]]);
  const originalCache = new Map([[heldKey('req', 1), original]]);
  assert.deepEqual(plan([{ side: 'req', id: 1 }], { rawCache, originalCache }), [{ side: 'req', id: 1 }]);
});

test('contentLengthNote reports the length the server will send after an edit', async () => {
  const { contentLengthNote } = await import('../js/intercept-model.js');
  // untouched: header matches body
  assert.equal(contentLengthNote('POST / HTTP/1.1\nHost: example.com\nContent-Length: 3\n\nabc'), null);
  // edited body: stale header
  assert.deepEqual(contentLengthNote('POST / HTTP/1.1\nHost: example.com\nContent-Length: 3\n\nabcdef'), { declared: 3, actual: 6 });
  // UTF-8 bytes, not UTF-16 code units
  assert.deepEqual(contentLengthNote('POST / HTTP/1.1\nContent-Length: 1\n\né'), { declared: 1, actual: 2 });
  // CRLF framing (server splits on the first CRLFCRLF)
  assert.deepEqual(contentLengthNote('POST / HTTP/1.1\r\nContent-Length: 1\r\n\r\nab'), { declared: 1, actual: 2 });
  // chunked bodies keep their own framing; the server leaves them alone
  assert.equal(contentLengthNote('POST / HTTP/1.1\nTransfer-Encoding: chunked\nContent-Length: 1\n\n5\nhello\n0\n\n'), null);
  // no Content-Length and no body: nothing to say
  assert.equal(contentLengthNote('GET / HTTP/1.1\nHost: example.com\n\n'), null);
  // body added where no Content-Length was declared
  assert.deepEqual(contentLengthNote('POST / HTTP/1.1\nHost: example.com\n\nxy'), { declared: null, actual: 2 });
  // no blank line yet (mid-edit): nothing to say
  assert.equal(contentLengthNote('POST / HTTP/1.1\nContent-Length: 9'), null);
  // header names are case-insensitive
  assert.deepEqual(contentLengthNote('HTTP/1.1 200 OK\ncontent-length: 2\n\nabc'), { declared: 2, actual: 3 });
});

test('contentLengthText is short, states the change and when it happens', async () => {
  const { contentLengthText } = await import('../js/intercept-model.js');
  assert.equal(contentLengthText({ declared: 22, actual: 26 }), 'Content-Length 22 → 26 on Forward');
  assert.equal(contentLengthText({ declared: null, actual: 2 }), 'Content-Length 2 added on Forward');
  assert.equal(contentLengthText(null), '');
});
