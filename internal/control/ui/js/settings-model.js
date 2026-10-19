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
export const FLOW_DRAWER_LAYOUT_PREF = 'interseptor.flowDrawerLayout';
export const FLOW_DRAWER_LAYOUT_CHOICES = [
  { id: 'bottom', label: 'Bottom panel' },
  { id: 'popup', label: 'Popup dialog' },
  { id: 'side', label: 'Side drawer' },
];
export const TYPED_CONFIRM_PHRASE = 'DELETE';

export function normalizeFlowDrawerLayoutChoice(raw) {
  return raw === 'popup' || raw === 'side' ? raw : 'bottom';
}

export function readFlowDrawerLayoutPref(storage) {
  try { return normalizeFlowDrawerLayoutChoice((storage || globalThis.localStorage).getItem(FLOW_DRAWER_LAYOUT_PREF)); } catch (e) { return 'bottom'; }
}

export function writeFlowDrawerLayoutPref(val, storage) {
  const norm = normalizeFlowDrawerLayoutChoice(val);
  try { (storage || globalThis.localStorage).setItem(FLOW_DRAWER_LAYOUT_PREF, norm); return true; } catch (e) { return false; }
}

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
  try { return (storage || globalThis.localStorage).getItem(HINTS_PREF) === 'on'; } catch (e) { return false; }
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
    if (sec === 'proxy') {
      // Auth on with no password (a restored backup never carries it) answers
      // every client with 407: listening, but not healthy.
      const auth = CHECK(ctx, 'proxy_auth');
      if (c.ok && auth && !auth.ok) return { state: 'warn', text: 'Proxy: auth password missing', detail: [auth.detail, auth.fix].filter(Boolean).join('. ') };
      return c.ok ? { state: 'ok', text: 'Proxy: listening', detail } : { state: 'warn', text: 'Proxy: not listening', detail };
    }
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

// systemProxyView is the System proxy switch's text and pressed state. pending is
// the state a click asked for while the OS call (networksetup can take seconds)
// is still running, or null. aria-pressed keeps the confirmed state; only the
// label announces the transition so the switch never reads as unchanged.
export function systemProxyView(s, pending = null) {
  const enabled = !!(s && s.enabled);
  const hint = enabled ? 'Traffic routes through ' + s.proxy : '';
  if (pending !== null && !!pending !== enabled) {
    return { label: pending ? 'Turning system proxy on…' : 'Turning system proxy off…', pressed: enabled, hint };
  }
  return { label: enabled ? 'System proxy is on' : 'System proxy is off', pressed: enabled, hint };
}

const UPSTREAM_DEFAULT_PORTS = { http: '80', https: '443', socks5: '1080', socks5h: '1080' };
function describeUpstream(v) {
  const scheme = (v && v.scheme) || 'direct';
  if (scheme === 'direct') return 'Direct connection · no upstream proxy';
  const host = String(v.host || '').trim() || 'host required';
  const port = String(v.port || '').trim() || UPSTREAM_DEFAULT_PORTS[scheme] || 'port required';
  const dns = scheme === 'socks5h' ? ' · remote DNS' : scheme === 'socks5' ? ' · local DNS' : '';
  return scheme.toUpperCase() + ' · ' + host + ':' + port + dns;
}
const sameUpstream = (a, b) => describeUpstream(a) === describeUpstream(b);

// upstreamSummaryText is the line beside "Save upstream proxy". It states the
// saved connection; while the form holds different values it says so instead
// of presenting the unsaved (possibly invalid) draft as the active proxy.
export function upstreamSummaryText(saved, draft) {
  const text = describeUpstream(saved);
  return !draft || sameUpstream(saved, draft) ? text : 'Unsaved changes · saved: ' + text;
}

/* ---- project import / restore results ---- */

const plural = (n, one, many) => (Number(n) || 0) + ' ' + ((Number(n) || 0) === 1 ? one : many);
const LOCAL_SETTING_LABELS = { 'upstream.proxy': 'upstream proxy', 'upstream.proxyCA': 'upstream proxy CA' };

// projectImportSummary turns POST /api/import/project's result into one toast:
// counts, duplicates skipped (import is idempotent) and the network settings
// the import kept local instead of taking the bundle's.
export function projectImportSummary(r = {}) {
  const parts = ['Imported ' + plural(r.importedFlows, 'flow', 'flows'), plural(r.importedRules, 'rule', 'rules'), plural(r.importedScope, 'scope rule', 'scope rules')];
  const skipped = (Number(r.skippedFlows) || 0) + (Number(r.skippedRules) || 0) + (Number(r.skippedScope) || 0);
  if (skipped) parts.push(plural(skipped, 'duplicate', 'duplicates') + ' skipped');
  const notes = { imported: 'notes imported', merged: 'notes merged (imported text appended, local notes kept)', kept: 'notes kept (local notes unchanged)' }[r.notes];
  if (notes) parts.push(notes);
  let text = parts.join(' · ');
  const kept = (Array.isArray(r.keptLocalSettings) ? r.keptLocalSettings : []).map((k) => LOCAL_SETTING_LABELS[k] || k);
  if (kept.length) text += ". Kept this machine's " + kept.join(' and ') + ' (network settings are never imported)';
  return { text, tone: kept.length ? 'warn' : '' };
}

// restoreNoticeText is the "passwords not included, re-enter them" message a
// full-archive or vault restore returns, or '' when nothing is missing.
export function restoreNoticeText(r = {}) {
  if (!Array.isArray(r.credentialsNotRestored) || !r.credentialsNotRestored.length) return '';
  return r.notice || 'Saved passwords were not included in the backup: re-enter them in Settings → Proxy & network after switching to this project.';
}

// upstreamPasswordDropWarning explains why saving the upstream proxy lost the
// saved password (keep was requested but none is saved now), or ''.
export function upstreamPasswordDropWarning({ keep, url, passwordSaved }) {
  if (!keep || !url || passwordSaved) return '';
  let user = '';
  try { user = new URL(url).username; } catch (_) { /* treated as no user */ }
  return user
    ? 'Upstream proxy saved without the saved password: the proxy host changed, so enter the password again'
    : 'Upstream proxy saved without the saved password: the username was cleared, so the password was removed too';
}
