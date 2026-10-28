// identities-model.js — pure rules behind the Identities panel (identities.js):
// health and verdict derivation, the auth summary a list row shows, JWT expiry
// countdowns, header masking and "Key: Value" validation, run filtering, the
// access-matrix view model and the auth-flow timeline. No DOM and no imports,
// so it runs under node (_js-tests/identities-model.test.mjs). Nothing here
// returns a secret value except parseHeaderLines, which only splits what the
// operator typed.

// ---------------------------------------------------------------- statuses

// STATUS_LABELS names every health state a row or the detail can show. The
// words are always rendered next to the dot, so status is never colour-only.
export const STATUS_LABELS = Object.freeze({
  ok: 'Healthy',
  expired: 'Expired',
  denied: 'Denied',
  error: 'Check failed',
  broken: 'Broken',
  unknown: 'Not checked',
});

const VERDICTS = new Set(['ok', 'expired', 'denied', 'error']);

// identityStatus derives one health state. A manual "broken" mark wins, then
// the server's last check, then a JWT that has run out locally (the check may
// predate the expiry), else unknown.
export function identityStatus(identity, { jwtExp = 0, now = Date.now() } = {}) {
  if (!identity) return 'unknown';
  if (identity.broken) return 'broken';
  const v = identity.lastCheck && identity.lastCheck.verdict;
  if (v === 'expired') return 'expired';
  if (jwtExp && jwtExp * 1000 <= now) return 'expired';
  if (VERDICTS.has(v)) return v;
  return 'unknown';
}

export const statusLabel = (s) => STATUS_LABELS[s] || STATUS_LABELS.unknown;

// ---------------------------------------------------------------- time

// formatCountdown renders the time left until a unix-seconds expiry:
// "45s", "23m", "2h 5m", "3d 4h", or "expired".
export function formatCountdown(expSec, now = Date.now()) {
  const exp = Number(expSec);
  if (!Number.isFinite(exp) || exp <= 0) return '';
  const left = Math.floor(exp - now / 1000);
  if (left <= 0) return 'expired';
  if (left < 60) return left + 's';
  const m = Math.floor(left / 60);
  if (m < 60) return m + 'm';
  const h = Math.floor(m / 60);
  if (h < 24) return h + 'h' + (m % 60 ? ' ' + (m % 60) + 'm' : '');
  const d = Math.floor(h / 24);
  return d + 'd' + (h % 24 ? ' ' + (h % 24) + 'h' : '');
}

// toMs accepts RFC3339 strings, unix seconds and unix milliseconds.
export function toMs(ts) {
  if (ts == null || ts === '') return NaN;
  if (typeof ts === 'number') return ts < 1e12 ? ts * 1000 : ts;
  const n = Date.parse(String(ts));
  return Number.isFinite(n) ? n : NaN;
}

// relTime: "just now", "5m ago", "3h ago", "2d ago", or "in 4m" for the future.
export function relTime(ts, now = Date.now()) {
  const t = toMs(ts);
  if (!Number.isFinite(t)) return '';
  const diff = Math.round((now - t) / 1000);
  const future = diff < 0;
  const s = Math.abs(diff);
  if (s < 45) return future ? 'in a moment' : 'just now';
  let v;
  if (s < 3600) v = Math.max(1, Math.round(s / 60)) + 'm';
  else if (s < 86400) v = Math.round(s / 3600) + 'h';
  else v = Math.round(s / 86400) + 'd';
  return future ? 'in ' + v : v + ' ago';
}

// ---------------------------------------------------------------- headers

// parseHeaderLines splits the operator's header text into {name, value} pairs
// and reports every non-blank line that is not "Key: Value" (1-based line).
export function parseHeaderLines(text) {
  const headers = [];
  const errors = [];
  String(text || '').split(/\r?\n/).forEach((raw, i) => {
    const line = raw.trim();
    if (!line) return;
    const at = line.indexOf(':');
    const name = at > 0 ? line.slice(0, at).trim() : '';
    if (!name || !/^[!#$%&'*+.^_`|~0-9A-Za-z-]+$/.test(name)) {
      errors.push({ line: i + 1, text: line.length > 60 ? line.slice(0, 57) + '...' : line });
      return;
    }
    headers.push({ name, value: line.slice(at + 1).trim() });
  });
  return { headers, errors };
}

// headerValidationMessage is the one-line message the edit sheet shows.
export function headerValidationMessage(text) {
  const { errors } = parseHeaderLines(text);
  if (!errors.length) return '';
  const lines = errors.map((e) => e.line);
  return (lines.length === 1 ? 'Line ' + lines[0] : 'Lines ' + lines.slice(0, 5).join(', ') + (lines.length > 5 ? '…' : ''))
    + ' must look like "Key: Value".';
}

// maskValue hides a credential: it keeps only its length.
export function maskValue(value) {
  const n = String(value == null ? '' : value).length;
  return n ? '•'.repeat(Math.min(12, Math.max(4, n))) + ' (' + n + ' chars)' : '(empty)';
}

// maskedHeaders turns raw header text into display rows that never carry the
// value: [{name, masked, length}].
export function maskedHeaders(text) {
  return parseHeaderLines(text).headers.map((h) => ({ name: h.name, masked: maskValue(h.value), length: h.value.length }));
}

// ---------------------------------------------------------------- auth summary

function base64UrlDecode(part) {
  let s = String(part || '').replace(/-/g, '+').replace(/_/g, '/');
  while (s.length % 4) s += '=';
  try {
    if (typeof globalThis.atob === 'function') {
      const bin = globalThis.atob(s);
      const bytes = Uint8Array.from(bin, (c) => c.charCodeAt(0));
      return new TextDecoder().decode(bytes);
    }
  } catch (e) { return null; }
  return null;
}

// decodeJwtClaims returns the payload object of a compact JWT, or null.
export function decodeJwtClaims(token) {
  const parts = String(token || '').trim().split('.');
  if (parts.length !== 3 || !parts[1]) return null;
  const json = base64UrlDecode(parts[1]);
  if (!json) return null;
  try {
    const v = JSON.parse(json);
    return v && typeof v === 'object' && !Array.isArray(v) ? v : null;
  } catch (e) { return null; }
}

// authSummary reads the auth scheme (and a JWT expiry, when the bearer token
// or a cookie is one) from raw header text. It never returns a value.
export function authSummary(text) {
  const hs = parseHeaderLines(text).headers;
  if (!hs.length) return { type: 'none', jwtExp: 0, headerNames: [] };
  const names = hs.map((h) => h.name);
  const auth = hs.find((h) => h.name.toLowerCase() === 'authorization');
  let type = 'header';
  let jwtExp = 0;
  if (auth) {
    const [scheme, ...rest] = auth.value.split(/\s+/);
    const s = (scheme || '').toLowerCase();
    if (s === 'bearer') {
      type = 'bearer';
      const c = decodeJwtClaims(rest.join(' '));
      if (c && Number(c.exp) > 0) jwtExp = Number(c.exp);
    } else if (s === 'basic') type = 'basic';
  } else if (hs.some((h) => h.name.toLowerCase() === 'cookie')) {
    type = 'cookie';
  }
  if (!jwtExp) {
    const cookie = hs.find((h) => h.name.toLowerCase() === 'cookie');
    if (cookie) {
      for (const pair of cookie.value.split(';')) {
        const c = decodeJwtClaims(pair.slice(pair.indexOf('=') + 1));
        if (c && Number(c.exp) > 0) { jwtExp = Number(c.exp); break; }
      }
    }
  }
  return { type, jwtExp, headerNames: names };
}

export const AUTH_TYPE_LABELS = Object.freeze({ none: 'Anonymous', cookie: 'Cookie', bearer: 'Bearer', basic: 'Basic', header: 'Header' });
export const authTypeLabel = (t) => AUTH_TYPE_LABELS[t] || 'Header';

// authLine is the list row's second line: "Bearer · 23m", "Cookie",
// "Bearer · expired".
export function authLine(summary, now = Date.now()) {
  const s = summary || { type: 'none' };
  const label = authTypeLabel(s.type);
  const left = s.jwtExp ? formatCountdown(s.jwtExp, now) : '';
  return left ? label + ' · ' + left : label;
}

// ---------------------------------------------------------------- provenance

const HUMAN_ACTORS = new Set(['', 'ui', 'you', 'human', 'user', 'manual', 'operator']);

// sourceBadge says who created an identity: the operator ("you") or the
// agent that wrote it.
export function sourceBadge(identity) {
  const id = identity || {};
  const actor = String(id.createdBy || id.owner || '').trim();
  const kind = id.source && id.source.kind;
  if (kind === 'agent' || (actor && !HUMAN_ACTORS.has(actor.toLowerCase()))) {
    return { kind: 'agent', label: actor || 'agent' };
  }
  return { kind: 'human', label: 'you' };
}

// provenanceText: "Created by agent-x · 3h ago from flow #812 POST /api/login".
// The flow part is returned separately so the view can make it a link.
export function provenance(identity, now = Date.now()) {
  const id = identity || {};
  const who = sourceBadge(id);
  const when = relTime(id.createdAt || id.updatedAt, now);
  const src = id.source || {};
  const kindText = { manual: 'by hand', flow: 'from a captured flow', 'login-macro': 'by the login macro', agent: 'by an agent' }[src.kind] || '';
  const lead = 'Created by ' + who.label + (when ? ' · ' + when : '');
  const flowId = Number(src.flowId) > 0 ? Number(src.flowId) : 0;
  const target = [src.method, (src.host || '') + (src.path || '')].filter(Boolean).join(' ').trim();
  return { lead, kindText, flowId, target };
}

// ---------------------------------------------------------------- names

// duplicateName picks "<name> copy", "<name> copy 2", ... that is not taken.
export function duplicateName(name, existing) {
  const taken = new Set((existing || []).map((n) => String(n)));
  const base = String(name || 'identity') + ' copy';
  if (!taken.has(base)) return base;
  for (let i = 2; i < 1000; i++) if (!taken.has(base + ' ' + i)) return base + ' ' + i;
  return base + ' ' + Date.now();
}

// sortIdentities keeps the server's order for the baseline (first) identity
// meaning, so the list is shown as returned; filterIdentities narrows it.
export function filterIdentities(ids, query) {
  const q = String(query || '').trim().toLowerCase();
  if (!q) return ids.slice();
  return ids.filter((i) => [i.name, i.role, i.notes, i.createdBy, i.owner].some((v) => String(v || '').toLowerCase().includes(q)));
}

// identityPatch returns only the fields an edit changed, or null.
export function identityPatch(original, form) {
  const o = original || {};
  const out = {};
  for (const k of ['name', 'role', 'notes', 'headers']) {
    const next = String(form[k] == null ? '' : form[k]);
    const prev = String(o[k] == null ? '' : o[k]);
    if (k === 'name' ? next.trim() !== prev : next !== prev) out[k] = k === 'name' ? next.trim() : next;
  }
  return Object.keys(out).length ? out : null;
}

// isStale: the identity changed on the server since the edit form opened.
export function isStale(baseline, latest) {
  if (!baseline) return false;
  if (!latest) return true; // deleted or renamed elsewhere
  return String(baseline.updatedAt || '') !== String(latest.updatedAt || '')
    || String(baseline.headers || '') !== String(latest.headers || '');
}

// ---------------------------------------------------------------- runs

export const RUN_KIND_LABELS = Object.freeze({ single: 'Single flow', bulk: 'All in-scope', differential: 'Differential', crosshost: 'Cross-host' });
export const runKindLabel = (k) => RUN_KIND_LABELS[k] || 'Run';

// The store retains the newest 200 runs (maxAuthzRuns) and runsForIdentity
// filters them here, so fetch the whole retained history.
export const RUN_HISTORY_LIMIT = 200;
export const runsURL = () => '/api/authz/runs?limit=' + RUN_HISTORY_LIMIT;

export function runsForIdentity(runs, name) {
  return (runs || []).filter((r) => Array.isArray(r.identities) && r.identities.includes(name));
}

// runTitle: "POST example.com/api/users" or "12 endpoints".
export function runTitle(run) {
  const t = run && run.target;
  if (t && (t.method || t.path)) return [t.method, (t.host || '') + (t.path || '')].filter(Boolean).join(' ');
  const n = Number(run && run.endpoints) || 0;
  return n + ' endpoint' + (n === 1 ? '' : 's');
}

export const VERDICT_LABELS = Object.freeze({
  allowed: 'Allowed', denied: 'Denied', error: 'Error', same: 'Same as baseline', different: 'Differs', expired: 'Session expired',
});

// verdictTone maps a cell verdict onto a semantic tone; "same" is the suspicious one.
export function verdictTone(v) {
  return { allowed: 'ok', denied: 'muted', error: 'error', same: 'danger', different: 'ok', expired: 'warn' }[v] || 'muted';
}

// matrixView shapes GET /api/authz/runs/{id} into rows of labelled cells.
export function matrixView(detail) {
  const m = (detail && detail.matrix) || {};
  const columns = Array.isArray(m.identities) ? m.identities.slice() : [];
  let suspicious = 0;
  const rows = (m.rows || []).map((r, i) => {
    const byName = new Map((r.cells || []).map((c) => [c.identity, c]));
    const cells = columns.map((name) => {
      const c = byName.get(name);
      if (!c) return { identity: name, missing: true, label: 'Not run', tone: 'muted', status: 0, length: 0, flowId: 0, suspicious: false, note: '' };
      const verdict = VERDICT_LABELS[c.verdict] ? c.verdict : 'error';
      const isSus = verdict === 'same';
      if (isSus) suspicious++;
      return {
        identity: name, verdict, label: VERDICT_LABELS[verdict], tone: verdictTone(verdict), suspicious: isSus,
        status: Number(c.status) || 0, length: Number(c.length) || 0, flowId: Number(c.flowId) || 0, note: String(c.note || ''),
      };
    });
    return {
      key: (r.flowId || 'row') + ':' + i, flowId: Number(r.flowId) || 0, method: r.method || '', host: r.host || '', path: r.path || '/',
      baseline: { status: Number(r.baseline && r.baseline.status) || 0, length: Number(r.baseline && r.baseline.length) || 0 },
      cells, suspicious: cells.some((c) => c.suspicious),
    };
  });
  return { columns, rows, suspicious };
}

// ---------------------------------------------------------------- timeline

export const EVENT_LABELS = Object.freeze({ created: 'Created', updated: 'Updated', checked: 'Checked', refreshed: 'Refreshed', 'marked-broken': 'Marked broken' });

// timelineItems merges the source (login) flow and the identity's events into
// one oldest-first list: [{kind:'source', ...}|{kind:'event', ...}].
export function timelineItems(detail) {
  const tl = (detail && detail.timeline) || {};
  const items = [];
  if (tl.source && Number(tl.source.flowId) > 0) {
    items.push({ kind: 'source', ts: tl.source.ts, at: toMs(tl.source.ts), flowId: Number(tl.source.flowId), method: tl.source.method || '', host: tl.source.host || '', path: tl.source.path || '/', status: Number(tl.source.status) || 0 });
  }
  const events = (tl.events || []).map((e, i) => ({
    kind: 'event', event: e.kind, label: EVENT_LABELS[e.kind] || String(e.kind || 'Event'), ts: e.ts, at: toMs(e.ts), actor: String(e.actor || ''), detail: String(e.detail || ''), order: i,
  }));
  events.sort((a, b) => {
    const x = Number.isFinite(a.at) ? a.at : Infinity;
    const y = Number.isFinite(b.at) ? b.at : Infinity;
    return x - y || a.order - b.order;
  });
  return items.concat(events);
}

export function usageSummary(detail) {
  const u = (detail && detail.usage) || {};
  const n = Number(u.count) || 0;
  return { count: n, text: n ? 'Used in ' + n + ' request' + (n === 1 ? '' : 's') : 'Not used in any captured request yet', recent: Array.isArray(u.recent) ? u.recent : [] };
}

// jwtRows: the claims table rows in a fixed order, values as display text.
export function jwtRows(jwt, now = Date.now()) {
  if (!jwt) return [];
  const list = (v) => (Array.isArray(v) ? v.join(', ') : v == null ? '' : String(v));
  const date = (s) => { const t = toMs(s); return Number.isFinite(t) ? new Date(t).toISOString().replace('T', ' ').replace(/\.\d+Z$/, 'Z') : ''; };
  const rows = [
    ['Algorithm', list(jwt.alg)], ['Subject', list(jwt.sub)], ['Roles', list(jwt.roles)],
    ['Issuer', list(jwt.issuer)], ['Audience', list(jwt.audience)], ['Issued', date(jwt.iat)],
  ];
  const exp = date(jwt.exp);
  if (exp) rows.push(['Expires', exp + ' (' + (jwt.expired ? 'expired' : formatCountdown(toMs(jwt.exp) / 1000, now) || 'expired') + ')']);
  return rows.filter((r) => r[1]).map(([label, value]) => ({ label, value }));
}

// jwtExpSec reads a claims expiry (unix seconds or RFC3339) as unix seconds.
export function jwtExpSec(jwt) {
  const t = toMs(jwt && jwt.exp);
  return Number.isFinite(t) ? Math.floor(t / 1000) : 0;
}
