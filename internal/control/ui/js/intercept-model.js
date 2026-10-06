// intercept-model.js — pure logic for the Intercept panel (no imports, no DOM),
// so it runs under `node --test`.
//
// The control API has no "undo drop": POST /api/intercept/{id}/drop is final.
// Drop therefore stays a client-side deferral. The request keeps being held on
// the server for DROP_DELAY_MS; Undo cancels the timer and the request is simply
// still held. Nothing here ever claims a drop was undone after it was sent.

export const DROP_DELAY_MS = 5000;

export const heldKey = (side, id) => side + ':' + id;

// createDropScheduler defers an irreversible drop. `commit(entry)` performs the
// real API call once the delay has elapsed without an Undo. The timer can be
// paused (hover or focus on the undo toast) so the operator is never rushed.
//
// `busy()` reports that another held action (a Forward or Drop) is mid-flight.
// A due drop then re-arms after `retryDelay` instead of committing, so it never
// overwrites the in-flight action's state; Undo stays valid while it waits.
export function createDropScheduler({ delay = DROP_DELAY_MS, setTimeout: st = globalThis.setTimeout, clearTimeout: ct = globalThis.clearTimeout, now = Date.now, commit, onChange = () => {}, busy = () => false, retryDelay = 250 } = {}) {
  const pending = new Map();
  const arm = (key, item, ms) => {
    item.startedAt = now();
    item.remaining = ms;
    item.timer = st(() => fire(key), ms);
  };
  const fire = (key) => {
    const item = pending.get(key);
    if (!item) return;
    if (busy()) { arm(key, item, retryDelay); return; }
    pending.delete(key);
    onChange(key, 'committed');
    commit(item.entry);
  };
  return {
    schedule(key, entry) {
      if (pending.has(key)) return false;
      const item = { entry, timer: null, startedAt: 0, remaining: delay, paused: false };
      pending.set(key, item);
      arm(key, item, delay);
      onChange(key, 'pending');
      return true;
    },
    undo(key) {
      const item = pending.get(key);
      if (!item) return false;
      ct(item.timer);
      pending.delete(key);
      onChange(key, 'undone');
      return true;
    },
    pause(key) {
      const item = pending.get(key);
      if (!item || item.paused) return false;
      ct(item.timer);
      item.remaining = Math.max(0, item.remaining - (now() - item.startedAt));
      item.paused = true;
      return true;
    },
    resume(key) {
      const item = pending.get(key);
      if (!item || !item.paused) return false;
      item.paused = false;
      arm(key, item, item.remaining);
      return true;
    },
    has: (key) => pending.has(key),
    size: () => pending.size,
    keys: () => [...pending.keys()],
  };
}

// forwardAllPlan lists what "Forward all" sends: every held item that is not
// waiting on a deferred drop, in queue order. An item the operator edited keeps
// the edit (raw is sent only when it differs from the original bytes);
// otherwise the server forwards the held bytes unmodified.
export function forwardAllPlan(items, { dropping = new Set(), rawCache = new Map(), originalCache = new Map() } = {}) {
  const plan = [];
  for (const h of Array.isArray(items) ? items : []) {
    const key = heldKey(h.side, h.id);
    if (dropping.has(key)) continue;
    const step = { side: h.side, id: h.id };
    const raw = rawCache.get(key);
    if (typeof raw === 'string' && raw !== (originalCache.get(key) ?? raw)) step.raw = raw;
    plan.push(step);
  }
  return plan;
}

export const forwardPath = (side, id) => (side === 'resp' ? '/api/intercept/response/' : '/api/intercept/') + id + '/forward';
export const dropPath = (side, id) => (side === 'resp' ? '/api/intercept/response/' : '/api/intercept/') + id + '/drop';

// scopeNote states what the server really does. The proxy holds only in-scope
// requests (and everything is in scope while no include rule exists); there is
// no counter for requests it let through, so none is invented here.
export function scopeNote(scope) {
  const s = scope || {};
  const rules = Number(s.inCount) || 0;
  if (rules > 0) return { scoped: true, text: 'Scope on: only in-scope requests are held; out-of-scope traffic is forwarded without a hold.' };
  return { scoped: false, text: 'No include rule set: every request is in scope and can be held.' };
}

export function scopeBadgeText(scope) {
  const s = scope || {};
  const inCount = Number(s.inCount) || 0;
  const outCount = Number(s.outCount) || 0;
  if (inCount > 0) return 'Scope: ' + inCount + ' include / ' + outCount + ' exclude';
  return 'Scope: all traffic';
}

export function identityText(name) {
  const n = String(name || '').trim();
  return n ? 'As ' + n : 'No identity selected';
}

// rulesSummary feeds the count badge on the Match & Replace disclosure.
export function rulesSummary(rules) {
  const list = Array.isArray(rules) ? rules : [];
  const enabled = list.filter((r) => r && r.enabled).length;
  return { total: list.length, enabled, label: list.length ? enabled + ' of ' + list.length + ' on' : 'none' };
}

export function heldCountText(total) {
  const n = Number(total) || 0;
  return n ? n + ' held' : 'Nothing held';
}

export function dropToastText(side) {
  return (side === 'resp' ? 'Dropping response' : 'Dropping request') + ' in ' + Math.round(DROP_DELAY_MS / 1000) + 's';
}

// forwardAllProgress is the polite live-region text while "Forward all" runs.
export function forwardAllProgress(done, total, failed) {
  if (failed) return 'Forward all stopped after ' + done + ' of ' + total + ': ' + failed;
  if (done >= total) return 'Forwarded ' + total + ' of ' + total;
  return 'Forwarded ' + done + ' of ' + total;
}

// ---- out-of-scope auto-forward tally -------------------------------------
// The server keeps no counter for requests it let through without a hold, so the
// panel tallies them from the live flow stream: a request flow that arrives
// while Requests interception is on and a scope with include rules is defined,
// and that the client-side scope mirror says is out of scope. Flows the proxy did
// not capture (Repeater, Intruder, import, AI, authz, discovery, TLS bypass) never
// count. A regex scope rule makes the mirror undecidable (Go RE2 versus JS), in
// which case `evaluable` is false and the line is hidden rather than guessed.
export const NON_PROXY_FLAGS = 64 | 128 | 256 | 1024 | 2048 | 4096 | 8192;

export function isAutoForwarded(flow, { interceptOn, compiled, flowInScope }) {
  if (!interceptOn || !flow || !compiled || !compiled.evaluable || !compiled.hasInclude) return false;
  if ((Number(flow.flags) || 0) & NON_PROXY_FLAGS) return false;
  return !flowInScope(flow, compiled);
}

export function autoForwardText(count) {
  return 'Out of scope, auto-forwarded: ' + (Number(count) || 0);
}

// createAutoForwardTally counts since Requests interception was last switched on.
export function createAutoForwardTally() {
  let n = 0;
  let wasOn = false;
  return {
    sync(interceptOn) { if (interceptOn && !wasOn) n = 0; wasOn = !!interceptOn; },
    add() { n++; return n; },
    count: () => n,
  };
}
