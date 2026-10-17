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
    // Same test as a single Forward: a line-ending-only difference (the textarea
    // LF-normalizes the CRLF original) is not an edit, so the held bytes go out.
    if (typeof raw === 'string' && heldIsModified(raw, originalCache.get(key) ?? raw)) step.raw = raw;
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

/* ---- Pretty view editing ----
   #heldRaw stays the single source of truth for the bytes that get forwarded.
   Pretty is an editable presentation of it: merely viewing derives indented
   text from the raw text without touching it; typing in Pretty is the only act
   that writes the pretty text back into the raw editor. */

// A <textarea>'s API value normalizes CRLF to LF, so comparisons against the
// server's CRLF original must ignore line-ending style.
export function normalizeEOL(s) { return String(s == null ? '' : s).replace(/\r\n/g, '\n'); }

// heldIsModified: does the editor text differ from the held original?
export function heldIsModified(current, original) {
  return normalizeEOL(current) !== normalizeEOL(original);
}

// heldForwardRaw is the `raw` field to POST on Forward, or null to omit it. An
// untouched message omits it so the server forwards the held original bytes
// exactly (no LF-normalized copy replaces them); an edited one sends the text.
export function heldForwardRaw(current, original) {
  return heldIsModified(current, original) ? String(current == null ? '' : current) : null;
}

// createPrettyBridge maps raw text <-> the text shown in the Pretty editor.
// show(raw) never mutates anything; it returns what Pretty should display. Once
// the user types, edit(text) records that pretty text IS the new raw, so a
// Raw -> Pretty round trip shows the user's own text back instead of
// re-indenting it.
export function createPrettyBridge({ beautify }) {
  let synced = null; // { raw, pretty } — the last text the user typed in Pretty
  return {
    show(raw) {
      const r = String(raw == null ? '' : raw);
      if (synced && synced.raw === r) return synced.pretty;
      return beautify(r);
    },
    edit(text) {
      const t = String(text == null ? '' : text);
      synced = { raw: t, pretty: t };
      return t; // the raw text to store in the editor
    },
    reset() { synced = null; },
  };
}

// squashJSONish drops whitespace outside double-quoted strings, so two texts that
// differ only in indentation compare equal.
const squashJSONish = s => s.replace(/"(?:\\.|[^"\\])*"|\s+/g, m => (m[0] === '"' ? m : ''));
// losslessBeautify returns beautify(body) only when the reformat changes nothing
// but whitespace; otherwise (big integers, duplicate or reordered keys, 1.0,
// \u escapes, a throwing formatter) it returns body unchanged. Pretty edits are
// forwarded as shown, so a lossy reformat would silently rewrite the payload.
export function losslessBeautify(body, beautify) {
  try {
    const out = beautify(body);
    return squashJSONish(out) === squashJSONish(body.trim()) ? out : body;
  } catch (e) {
    return body;
  }
}

/* ---- Content-Length after an edit ----
   The server re-derives Content-Length from the edited body on Forward
   (intercept.parseEditedRequest, proxy response edits), so the header the
   operator sees in the editor goes stale as soon as the body changes. These
   mirror that rule so the panel can say what will actually be sent. */

// contentLengthNote returns {declared, actual} when the forwarded
// Content-Length will differ from the one written in the editor, else null.
// `declared` is null when the message has no Content-Length but gains one.
// Chunked messages keep their own framing and are left alone, as on the server.
export function contentLengthNote(raw) {
  const s = String(raw == null ? '' : raw);
  let i = s.indexOf('\r\n\r\n'), sep = 4;
  if (i < 0) { i = s.indexOf('\n\n'); sep = 2; }
  if (i < 0) return null;
  const head = s.slice(0, i);
  const body = s.slice(i + sep);
  const lines = head.split(/\r?\n/).slice(1);
  let declared = null;
  for (const line of lines) {
    const c = line.indexOf(':');
    if (c < 0) continue;
    const name = line.slice(0, c).trim().toLowerCase();
    const value = line.slice(c + 1).trim();
    if (name === 'transfer-encoding' && /chunked/i.test(value)) return null;
    if (name === 'content-length' && declared === null && /^\d+$/.test(value)) declared = Number(value);
  }
  const actual = new TextEncoder().encode(body).length;
  if (declared === actual) return null;
  if (declared === null && actual === 0) return null;
  return { declared, actual };
}

export function contentLengthText(note) {
  if (!note) return '';
  return note.declared === null
    ? 'Content-Length ' + note.actual + ' added on Forward'
    : 'Content-Length ' + note.declared + ' \u2192 ' + note.actual + ' on Forward';
}
