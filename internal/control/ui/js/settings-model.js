// settings-model.js — pure logic behind Settings: the appearance choices, the
// section search, per-section health chips, live validation of authorised
// targets, autosave status text and the typed-confirm predicate. No imports and
// no DOM access, so every function runs under node --test.

export const THEME_CHOICES = [
  { id: 'system', label: 'System' },
  { id: 'dark', label: 'Dark' },
  { id: 'light', label: 'Light' },
  { id: 'hc', label: 'High contrast' },
];
export const DENSITY_CHOICES = [
  { id: 'compact', label: 'Compact' },
  { id: 'default', label: 'Default' },
  { id: 'comfortable', label: 'Comfortable' },
];
export const HINTS_PREF = 'interseptor.hints';
export const TYPED_CONFIRM_PHRASE = 'DELETE';

// A missing or unknown stored theme means "follow the system".
export function normalizeThemeChoice(raw) {
  return raw === 'dark' || raw === 'light' || raw === 'hc' ? raw : 'system';
}

// data-theme is light | hc; dark is the unmarked base (null removes the attribute).
export function themeAttribute(choice, prefersLight) {
  const c = normalizeThemeChoice(choice);
  if (c === 'system') return prefersLight ? 'light' : null;
  return c === 'dark' ? null : c;
}

// The pre-paint script treats a missing key as "system", so System removes it.
export function themeStoragePlan(choice) {
  const c = normalizeThemeChoice(choice);
  return c === 'system' ? { action: 'remove' } : { action: 'set', value: c };
}

export function normalizeDensityChoice(raw) {
  return raw === 'compact' || raw === 'comfortable' ? raw : 'default';
}

export function readHintsPref(storage) {
  try { return (storage || globalThis.localStorage).getItem(HINTS_PREF) !== 'off'; } catch (e) { return true; }
}
export function writeHintsPref(on, storage) {
  try { (storage || globalThis.localStorage).setItem(HINTS_PREF, on ? 'on' : 'off'); return true; } catch (e) { return false; }
}

/* ---- section search ---- */

// highlightRanges returns every [start, end) of a case-insensitive query in text.
export function highlightRanges(text, query) {
  const q = String(query || '').trim().toLowerCase();
  const t = String(text || '').toLowerCase();
  if (!q) return [];
  const out = [];
  for (let at = t.indexOf(q); at !== -1; at = t.indexOf(q, at + q.length)) out.push([at, at + q.length]);
  return out;
}

// entries: [{id, label, text}] (text is the section body). Returns one row per entry
// with `hit` and the label ranges to highlight; an empty query hits everything.
export function matchSections(entries, query) {
  const q = String(query || '').trim().toLowerCase();
  return (entries || []).map((e) => {
    const ranges = highlightRanges(e.label, q);
    const hit = !q || ranges.length > 0 || String(e.text || '').toLowerCase().includes(q);
    return { id: e.id, hit, ranges, inBody: !!q && ranges.length === 0 && hit };
  });
}

export function searchSummary(rows) {
  const n = rows.filter((r) => r.hit).length;
  if (n === rows.length) return '';
  return n === 0 ? 'No matching settings.' : n + (n === 1 ? ' section matches' : ' sections match');
}

/* ---- autosave status text ---- */

const pad2 = (n) => String(n).padStart(2, '0');
export function saveStatusText(kind, now = new Date()) {
  if (kind === 'saving') return 'Saving...';
  if (kind === 'saved') return 'Saved ' + pad2(now.getHours()) + ':' + pad2(now.getMinutes());
  if (kind === 'error') return 'Could not save. Retry';
  if (kind === 'invalid') return 'Fix the highlighted targets to save';
  if (kind === 'dirty') return 'Unsaved changes';
  return '';
}

/* ---- authorised targets (live validation) ---- */

const HOST_CHARS = /^[a-z0-9*._:\-\/\[\]@%?=&#+~,;]+$/i;
// Lines with whitespace are prose notes (the brief scope is free text); only a
// single-token line is a target pattern and is checked.
export function validateTargetLines(text) {
  const errors = [];
  const targets = [];
  String(text || '').split(/\r?\n/).forEach((raw, i) => {
    const line = raw.trim();
    if (!line || line.startsWith('#')) return;
    if (/\s/.test(line)) return;
    const err = targetLineError(line);
    if (err) errors.push({ line: i + 1, value: line, message: err });
    else targets.push(line);
  });
  return { ok: errors.length === 0, errors, targets, empty: String(text || '').trim() === '' };
}

function targetLineError(line) {
  if (line === '*' || line === '**' || line === '*.*') return 'A bare wildcard matches every host. Name the domain, for example *.example.com.';
  if (/^\*[^.]/.test(line)) return 'A wildcard needs a dot after it: use *.example.com, not ' + line + '.';
  if (/\*\*/.test(line)) return 'Use a single * for each wildcard label.';
  if (/\.\./.test(line)) return 'Empty label (two dots in a row).';
  if (/[<>"'`\\|{}^]/.test(line)) return 'Contains characters that are not valid in a host or URL.';
  if (!HOST_CHARS.test(line)) return 'Contains characters that are not valid in a host or URL.';
  const host = line.replace(/^[a-z][a-z0-9+.-]*:\/\//i, '').split(/[\/?#]/)[0];
  if (host === '') return 'Missing host name.';
  if (host.startsWith('.') && !host.startsWith('.*')) return 'A host cannot start with a dot.';
  if (/\*/.test(host.replace(/^\*\./, '')) && !/^\.\*/.test(host)) return 'A wildcard is only valid as the first label (*.example.com).';
  return '';
}

/* ---- section health chips ---- */

const CHECK = (ctx, id) => ((ctx && ctx.readiness && ctx.readiness.checks) || []).find((c) => c.id === id) || null;

// sectionHealth(sec, ctx) -> null (no chip) or {state:'ok'|'warn'|'unknown', text, detail}.
// ctx: {readiness:{checks}|null, scope:{enabled,inCount}, brief:{ok}, identities:n, stale:bool}.
export function sectionHealth(sec, ctx = {}) {
  const need = { proxy: 'proxy', tls: 'tls_intercept', session: 'auth_identities', scanner: 'oob' }[sec];
  if (need) {
    const c = CHECK(ctx, need);
    if (!c) return { state: 'unknown', text: 'Health unavailable', detail: ctx.stale ? 'Could not read the readiness report' : 'Checking...' };
    const detail = [c.detail, !c.ok && c.fix ? c.fix : ''].filter(Boolean).join('. ');
    if (sec === 'proxy') return c.ok ? { state: 'ok', text: 'Proxy: listening', detail } : { state: 'warn', text: 'Proxy: not listening', detail };
    if (sec === 'tls') return c.ok ? { state: 'ok', text: 'TLS: intercepting', detail } : { state: 'warn', text: 'TLS: CA not verified', detail };
    if (sec === 'session') return c.ok ? { state: 'ok', text: 'Auth: ' + (c.detail || 'identities set'), detail } : { state: 'warn', text: 'Auth: no identities', detail };
    return c.ok ? { state: 'ok', text: 'OOB: ready', detail } : { state: 'warn', text: 'OOB: not configured', detail };
  }
  if (sec === 'scope') {
    const brief = ctx.brief || { ok: false };
    const scope = ctx.scope || { enabled: false, inCount: 0 };
    if (brief.ok && scope.enabled) return { state: 'ok', text: 'Scope: target set, ' + scope.inCount + ' include', detail: '' };
    if (!brief.ok) return { state: 'warn', text: 'Scope: no target', detail: 'The engagement brief has no authorised target' };
    return { state: 'warn', text: 'Scope: no include rule', detail: 'Add an include rule to define scope' };
  }
  return null;
}

/* ---- typed confirm ---- */

export function typedConfirmMatches(input, phrase = TYPED_CONFIRM_PHRASE) {
  return String(input || '').trim().toLowerCase() === String(phrase).trim().toLowerCase();
}
