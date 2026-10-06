// keys.js — additive keyboard registry for NEW features (scoped single keys,
// chords), the shortcut cheatsheet generator and the fuzzy scorer used by the
// command palette. It does not replace the existing document handlers in app.js
// and proxy.js; legacy bindings are listed for the cheatsheet via a static table.
//
// WCAG 2.1.4: bindings whose first step is a bare character key are single-key
// shortcuts. They never fire inside editable controls, while a modal is open, or
// when the Settings switch "Single-key shortcuts" is off. Modified keys
// (Ctrl+Enter, Escape, arrows) are not gated.
//
// Pure: no imports and no DOM access outside functions, so it runs under node.

export const SINGLE_KEY_PREF = 'interseptor.singleKeyShortcuts';
export const CHORD_TIMEOUT_MS = 1200;

export function readSingleKeyPref(storage) {
  try { return (storage || globalThis.localStorage).getItem(SINGLE_KEY_PREF) !== 'off'; } catch (e) { return true; }
}
export function writeSingleKeyPref(on, storage) {
  try { (storage || globalThis.localStorage).setItem(SINGLE_KEY_PREF, on ? 'on' : 'off'); return true; } catch (e) { return false; }
}

const NON_TEXT_INPUTS = new Set(['button', 'checkbox', 'radio', 'submit', 'reset', 'range', 'color', 'file', 'image']);
export function isTypingTarget(el) {
  if (!el) return false;
  const tag = String(el.tagName || '').toLowerCase();
  if (tag === 'textarea' || tag === 'select') return true;
  if (tag === 'input') return !NON_TEXT_INPUTS.has(String(el.type || 'text').toLowerCase());
  if (el.isContentEditable) return true;
  const role = typeof el.getAttribute === 'function' ? el.getAttribute('role') : null;
  return role === 'textbox' || role === 'searchbox' || role === 'combobox';
}

const KEY_ALIASES = { esc: 'Escape', escape: 'Escape', return: 'Enter', enter: 'Enter', space: ' ', spacebar: ' ', up: 'ArrowUp', down: 'ArrowDown', left: 'ArrowLeft', right: 'ArrowRight' };

function canonicalStep(mods, key) {
  const parts = [];
  if (mods.mod) parts.push('Mod');
  if (mods.alt) parts.push('Alt');
  const named = key.length > 1;
  // Shift is only part of the identity for named keys and modified letters;
  // for a bare character the case (n vs N) already carries it.
  if (mods.shift && (named || mods.mod || mods.alt)) parts.push('Shift');
  let k = key;
  if (!named && (mods.mod || mods.alt)) k = key.toLowerCase();
  parts.push(k);
  return parts.join('+');
}

function parseStep(spec) {
  const raw = String(spec).trim();
  if (!raw) throw new Error('keys: empty key step');
  const pieces = raw.endsWith('+') && raw.length > 1 ? [...raw.slice(0, -2).split('+').filter(Boolean), '+'] : raw.split('+');
  const mods = { mod: false, alt: false, shift: false };
  let key = pieces.pop();
  for (const m of pieces) {
    const n = m.toLowerCase();
    if (n === 'ctrl' || n === 'control' || n === 'cmd' || n === 'meta' || n === 'mod') mods.mod = true;
    else if (n === 'alt' || n === 'option') mods.alt = true;
    else if (n === 'shift') mods.shift = true;
    else throw new Error('keys: unknown modifier ' + m);
  }
  if (!key) throw new Error('keys: missing key in ' + raw);
  key = KEY_ALIASES[key.toLowerCase()] || key;
  return canonicalStep(mods, key);
}

// parseKeys("g p") -> ['g','p']; "Ctrl+Enter" -> ['Mod+Enter'].
export function parseKeys(spec) {
  const steps = String(spec).trim().split(/\s+/).filter(Boolean);
  if (!steps.length) throw new Error('keys: empty binding');
  return steps.map(parseStep);
}

export function eventStep(e) {
  const mods = { mod: !!(e.ctrlKey || e.metaKey), alt: !!e.altKey, shift: !!e.shiftKey };
  return canonicalStep(mods, e.key === 'Spacebar' ? ' ' : e.key);
}

const isCharStep = (step) => [...step].length === 1;

export function createKeyRegistry(opts = {}) {
  const now = opts.now || Date.now;
  const setT = opts.setTimeout || ((fn, ms) => setTimeout(fn, ms));
  const clearT = opts.clearTimeout || ((t) => clearTimeout(t));
  const isTyping = opts.isTyping || isTypingTarget;
  const isModalOpen = opts.isModalOpen || (() => false);
  const singleKeyEnabled = opts.singleKeyEnabled || (() => readSingleKeyPref());
  const timeoutMs = opts.timeoutMs || CHORD_TIMEOUT_MS;
  const bindings = [];
  let pendingState = null;

  const same = (a, b) => a.length === b.length && a.every((s, i) => s === b[i]);
  const isPrefix = (a, b) => a.length < b.length && a.every((s, i) => s === b[i]);

  function register(def) {
    if (!def || typeof def.id !== 'string' || !def.id) throw new Error('keys.register: id required');
    if (typeof def.run !== 'function') throw new Error('keys.register: run required for ' + def.id);
    if (bindings.some((b) => b.id === def.id)) throw new Error('keys.register: duplicate id ' + def.id);
    const steps = parseKeys(def.keys);
    const scope = def.scope || 'global';
    for (const b of bindings) {
      if (b.scope !== scope) continue;
      if (same(b.steps, steps)) throw new Error(`keys.register: ${def.keys} already bound in scope ${scope} by ${b.id}`);
      if (isPrefix(b.steps, steps) || isPrefix(steps, b.steps)) throw new Error(`keys.register: ${def.keys} conflicts with chord prefix of ${b.id} in scope ${scope}`);
    }
    bindings.push({ id: def.id, keys: def.keys, steps, scope, when: def.when || null, run: def.run, label: def.label || def.id, group: def.group || 'General' });
    return def.id;
  }
  function unregister(id) {
    const i = bindings.findIndex((b) => b.id === id);
    if (i >= 0) bindings.splice(i, 1);
    return i >= 0;
  }
  function clearPending() {
    if (pendingState) clearT(pendingState.timer);
    const had = !!pendingState;
    pendingState = null;
    if (had && opts.onPendingChange) opts.onPendingChange(null);
  }
  function pending() {
    if (!pendingState) return null;
    if (now() > pendingState.expires) { clearPending(); return null; }
    const idx = pendingState.typed.length;
    return {
      typed: pendingState.typed.slice(),
      continuations: pendingState.candidates.map((b) => ({ keys: b.keys, label: b.label, id: b.id, next: b.steps[idx] })),
    };
  }
  function gated(b, e) {
    if (!isCharStep(b.steps[0])) return false;
    if (!singleKeyEnabled()) return true;
    if (isModalOpen()) return true;
    return !!isTyping(e.target);
  }
  function candidatesFor(scopes, e, idx, step, from) {
    return (from || bindings).filter((b) => scopes.includes(b.scope) && b.steps.length > idx && b.steps[idx] === step && (!b.when || b.when()) && !gated(b, e));
  }
  function handle(e, ctx = {}) {
    const scopes = ctx.scopes || ['global'];
    if (e.repeat) return false;
    const step = eventStep(e);
    let idx = 0;
    let pool = null;
    if (pendingState) {
      if (now() > pendingState.expires) clearPending();
      else if (step === 'Escape') { clearPending(); if (e.preventDefault) e.preventDefault(); return true; }
      else {
        idx = pendingState.typed.length;
        const matches = candidatesFor(scopes, e, idx, step, pendingState.candidates);
        if (!matches.length) clearPending();
        else { pool = matches; }
      }
    }
    if (!pool) { idx = 0; pool = candidatesFor(scopes, e, 0, step); }
    if (!pool.length) return false;
    const full = pool.find((b) => b.steps.length === idx + 1);
    if (full) {
      clearPending();
      if (e.preventDefault) e.preventDefault();
      full.run(e);
      return true;
    }
    const typed = (pendingState ? pendingState.typed : []).concat(step);
    if (pendingState) clearT(pendingState.timer);
    pendingState = { typed, candidates: pool, expires: now() + timeoutMs, timer: setT(() => { clearPending(); }, timeoutMs) };
    if (opts.onPendingChange) opts.onPendingChange(pending());
    if (e.preventDefault) e.preventDefault();
    return true;
  }
  function list(scope) {
    return bindings.filter((b) => !scope || b.scope === scope).map((b) => ({ id: b.id, keys: b.keys, scope: b.scope, label: b.label, group: b.group }));
  }
  // cheatsheet merges registered bindings with the static legacy table so the
  // shortcut documentation cannot drift from the keys that actually exist.
  function cheatsheet(legacy = []) {
    const groups = new Map();
    const add = (group, item) => { if (!groups.has(group)) groups.set(group, []); groups.get(group).push(item); };
    for (const l of legacy) add(l.group || 'General', { keys: l.keys, label: l.label, scope: l.scope || 'global', legacy: true });
    for (const b of bindings) add(b.group, { keys: b.keys, label: b.label, scope: b.scope });
    return [...groups.entries()].map(([group, items]) => ({ group, items }));
  }
  return { register, unregister, handle, pending, cancel: clearPending, list, cheatsheet };
}

// ---- fuzzy scorer (command palette) ----
const isBoundary = (text, i) => i === 0 || /[^a-z0-9]/i.test(text[i - 1]) || (text[i - 1] === text[i - 1].toLowerCase() && text[i] !== text[i].toLowerCase());

// fuzzyScore: -1 when `query` is not a subsequence of `text`, 0 for an empty
// query, otherwise a score where word-boundary hits, contiguous runs and
// substring/prefix matches rank higher. Best alignment via O(n*m) DP.
export function fuzzyScore(query, text) {
  const q = String(query || '').trim().toLowerCase();
  if (!q) return 0;
  const orig = String(text || '');
  const t = orig.toLowerCase();
  const n = t.length, m = q.length;
  if (m > n) return -1;
  const NEG = -1e9;
  let prev = null;
  for (let i = 0; i < m; i++) {
    const cur = new Array(n).fill(NEG);
    let runMax = NEG; // best prev[k] for k <= j-5 (gap penalty saturates at 3)
    for (let j = 0; j < n; j++) {
      if (i > 0 && j - 5 >= 0 && prev[j - 5] > runMax) runMax = prev[j - 5];
      if (t[j] !== q[i]) continue;
      const base = 1 + (isBoundary(orig, j) ? 8 : 0);
      if (i === 0) { cur[j] = base - Math.min(j, 6) * 0.25; continue; }
      let best = NEG;
      if (j >= 1 && prev[j - 1] > NEG) best = prev[j - 1] + 6;
      for (let k = Math.max(0, j - 4); k <= j - 2; k++) if (prev[k] > NEG) best = Math.max(best, prev[k] - (j - k - 1));
      if (runMax > NEG) best = Math.max(best, runMax - 3);
      if (best > NEG) cur[j] = base + best;
    }
    prev = cur;
  }
  let score = NEG;
  for (let j = 0; j < n; j++) if (prev[j] > score) score = prev[j];
  if (score <= NEG / 2) return -1;
  const at = t.indexOf(q);
  if (at >= 0) score += 12 + (at === 0 ? 8 : 0);
  return score;
}

// fuzzyRank returns matching items best first (stable on ties). An empty query
// keeps the input order.
export function fuzzyRank(query, items, { getText = (x) => x, limit = Infinity } = {}) {
  const q = String(query || '').trim();
  if (!q) return items.slice(0, limit);
  const scored = [];
  items.forEach((item, i) => {
    const s = fuzzyScore(q, getText(item));
    if (s >= 0) scored.push({ item, s, i });
  });
  scored.sort((a, b) => b.s - a.s || a.i - b.i);
  return scored.slice(0, limit).map((x) => x.item);
}
