// varscope-model.js — pure logic for how the Collections UI explains variables.
// No imports and no DOM, so it runs under node. The server owns resolution
// (POST /api/variables/resolve, one Go resolver); this module only scans
// templates for {{name}} tokens and classifies each against the variable
// layers the UI already fetched, so the editor can colour tokens and the
// environment sheet can show which layer wins. It never substitutes a value.

// Narrow to wide. Matches internal/varstore: local > data > environment >
// folder (inner to outer) > collection > global.
export const LAYER_ORDER = ['local', 'data', 'environment', 'folder', 'collection', 'global'];
export const LAYER_LABEL = {
  local: 'Local', data: 'Data row', environment: 'Environment', folder: 'Folder', collection: 'Collection', global: 'Global',
};
export const SECRET_MASK = '[secret]';
export const MIN_SECRET_REVEAL_LEN = 6;

// Postman-compatible dynamic variables plus the Interseptor extras. They always
// resolve, so they never show as unresolved.
export const DYNAMIC_VARS = [
  '$guid', '$randomUUID', '$timestamp', '$isoTimestamp', '$randomInt', '$randomAlphaNumeric', '$randomBoolean',
  '$randomEmail', '$randomFirstName', '$randomLastName', '$randomIP', '$randomPassword', '$randomHexColor',
];
export const isDynamic = (name) => typeof name === 'string' && name.startsWith('$');

const TOKEN = /\\\{\{|\{\{\s*([^{}\s|][^{}|]*?)\s*(?:\|[^{}]*)?\}\}/g;

// scanVars splits a template into text and var tokens. An escaped \{{ stays
// literal text. Nested names ({{a{{b}}}}) are reported by their innermost token.
export function scanVars(text) {
  const src = String(text == null ? '' : text);
  const out = [];
  let last = 0;
  TOKEN.lastIndex = 0;
  let m;
  while ((m = TOKEN.exec(src))) {
    if (m[0] === '\\{{') continue;
    if (m.index > last) out.push({ kind: 'text', text: src.slice(last, m.index) });
    out.push({ kind: 'var', text: m[0], name: m[1].trim() });
    last = m.index + m[0].length;
  }
  if (last < src.length) out.push({ kind: 'text', text: src.slice(last) });
  return out;
}

export function varNames(text) {
  const seen = new Set();
  for (const t of scanVars(text)) if (t.kind === 'var') seen.add(t.name);
  return [...seen];
}

// buildScope merges layers into a lookup. layers is ordered by LAYER_ORDER
// priority (any order accepted; priority comes from LAYER_ORDER). Each layer is
// {scope, label?, vars:[{key,type,enabled,initialValue,current,hasCurrent}]}.
// The winner per key carries its scope, type and whether it has a usable value.
export function buildScope(layers) {
  const rank = (s) => { const i = LAYER_ORDER.indexOf(s); return i < 0 ? LAYER_ORDER.length : i; };
  const sorted = [...(layers || [])].sort((a, b) => rank(a.scope) - rank(b.scope));
  const map = new Map();
  for (const layer of sorted) {
    for (const v of layer.vars || []) {
      if (!v || !v.key || v.enabled === false) continue;
      const shadowed = map.get(v.key);
      const entry = {
        key: v.key, scope: layer.scope, label: layer.label || LAYER_LABEL[layer.scope] || layer.scope,
        type: v.type || 'default', hasValue: !!(v.hasCurrent || (v.initialValue != null && v.initialValue !== '')),
        shadows: [],
      };
      if (shadowed) shadowed.shadows.push(entry.scope);
      else map.set(v.key, entry);
    }
  }
  return map;
}

// classifyVar -> 'dynamic' | 'secret' | 'resolved' | 'empty' | 'unresolved'.
// 'empty' is declared but valueless: the server would still block it.
export function classifyVar(name, scope) {
  if (isDynamic(name)) return 'dynamic';
  const e = scope && scope.get ? scope.get(name) : null;
  if (!e) return 'unresolved';
  if (e.type === 'secret') return e.hasValue ? 'secret' : 'empty';
  return e.hasValue ? 'resolved' : 'empty';
}

// annotate turns a template into tokens with a status, for the overlay editor.
export function annotate(text, scope) {
  return scanVars(text).map((t) => (t.kind === 'var' ? { ...t, status: classifyVar(t.name, scope) } : t));
}

export function unresolvedIn(text, scope) {
  return varNames(text).filter((n) => { const s = classifyVar(n, scope); return s === 'unresolved' || s === 'empty'; });
}

// envDot is the state of the environment chip: green resolved, amber unresolved
// names in the request, red out-of-pin.
export function envDot({ hasEnv, unresolved = 0, pinBroken = false } = {}) {
  if (pinBroken) return { state: 'danger', label: 'Active environment does not match its target pin' };
  if (!hasEnv) return { state: 'none', label: 'No environment selected' };
  if (unresolved > 0) return { state: 'warn', label: unresolved + ' unresolved ' + (unresolved === 1 ? 'variable' : 'variables') };
  return { state: 'ok', label: 'All variables resolve' };
}

// maskValue never shows a secret: used for hover text and sheet cells.
export function maskValue(value, { secret = false, reveal = false } = {}) {
  if (!secret) return value == null ? '' : String(value);
  if (reveal && value != null) return String(value);
  return value ? SECRET_MASK : '';
}

// pinMatches: an environment's base target pin (host or host:port) against the
// host of the request URL, to catch "prod env left active".
export function pinMatches(pin, urlHost) {
  if (!pin) return true;
  const strip = (s) => String(s || '').toLowerCase().replace(/^[a-z]+:\/\//, '').replace(/\/.*$/, '');
  const p = strip(pin), h = strip(urlHost);
  if (!h) return true;
  return p === h || p.split(':')[0] === h.split(':')[0] && (!p.includes(':') || !h.includes(':') || p === h);
}

// sheetRows prepares environment-sheet rows from the API's variable views.
export function sheetRows(vars, { reveal = false } = {}) {
  return (vars || []).map((v) => {
    const secret = v.type === 'secret';
    return {
      key: v.key, type: v.type || 'default', enabled: v.enabled !== false,
      initial: secret ? '' : (v.initialValue || ''),
      current: v.hasCurrent ? maskValue(v.current, { secret, reveal }) : '',
      hasCurrent: !!v.hasCurrent, secret,
    };
  });
}

// diffRows reports which rows changed since the sheet loaded, for the Save
// action and the "unsaved" state.
export function changedKeys(before, after) {
  const a = new Map((before || []).map((r) => [r.key, JSON.stringify([r.type, r.enabled, r.initial])]));
  const out = [];
  for (const r of after || []) {
    const sig = JSON.stringify([r.type, r.enabled, r.initial]);
    if (!a.has(r.key) || a.get(r.key) !== sig) out.push(r.key);
  }
  for (const k of a.keys()) if (!(after || []).some((r) => r.key === k)) out.push(k);
  return out;
}

// toDeclared converts sheet rows back to the PUT /api/variables body. A secret's
// initial value is always blank: the server forces that too.
export function toDeclared(rows) {
  return (rows || []).filter((r) => r.key && r.key.trim()).map((r) => ({
    key: r.key.trim(), type: r.type || 'default', initialValue: r.type === 'secret' ? '' : (r.initial || ''), enabled: r.enabled !== false,
  }));
}
