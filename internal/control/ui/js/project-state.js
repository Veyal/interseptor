// project-state.js — the engagement state store behind the strip and the rail
// badge: scope, brief target, identities, evidence counts and server-computed
// finding readiness. Pure logic: it imports no DOM and no core.js so every rule
// (blockers, next action, debounce, throttle, stale segments) runs under
// `node --test`. The client displays readiness; it never decides pass or fail,
// and it never invents a blocker code the server did not send.
//
//   projectState.get() / .subscribe(fn) -> off / .refresh({reason}) -> Promise
//   projectState.noteFlowNew()  throttled counter refresh for live traffic
//   projectState.attach({fetchJson}) wires the real API (app.js does this once)

import { findingSectionForGap } from './finding-workspace.js';

export const REFRESH_DEBOUNCE_MS = 250;
export const FLOW_NEW_THROTTLE_MS = 2000;
export const ITEMS_CAP = 200;
export const ANNOUNCE_GAP_MS = 5000;
export const SEGMENTS = ['scope', 'brief', 'identities', 'findings', 'evidence'];

const MAX_TARGET = 256;
const AGGREGATE_PATH = '/api/project/readiness';

const PROJECT_CODES = {
  brief_target: { label: 'No authorised target in the engagement brief', fix: 'settings:scope' },
  scope: { label: 'Scope is not set: add an include rule', fix: 'settings:scope' },
};
const GAP_LABELS = {
  title: 'Title', summary: 'Claim', target: 'Target', target_evidence: 'Evidence per target',
  action: 'Triggering action', result: 'Observed result', control: 'Negative / control case',
  execution: 'Impact verification', execution_reason: 'Verification reason', visual: 'Real browser capture',
  cvss: 'CVSS vector', severity: 'Severity matches score', impact: 'Impact', why: 'Why',
  evidence: 'Proof evidence', evidence_missing: 'Proof evidence', proof: 'Proof', reproduction: 'Reproduction',
  fix: 'Fix guidance', retest: 'Retest', confidence: 'Confidence', verification: 'Verification',
};

const int = (n) => (Number.isFinite(Number(n)) && Number(n) > 0 ? Math.floor(Number(n)) : 0);
const text = (s) => (typeof s === 'string' ? s : '');
const list = (a) => (Array.isArray(a) ? a : []);

export function humanizeCode(code) {
  const raw = String(code == null ? '' : code).replace(/^capability:/, 'Claim: ').replace(/[_-]+/g, ' ').trim();
  return raw ? raw.charAt(0).toUpperCase() + raw.slice(1) : 'Unknown blocker';
}

export function blockerLabel(code) {
  if (PROJECT_CODES[code]) return PROJECT_CODES[code].label;
  if (GAP_LABELS[code]) return GAP_LABELS[code];
  return humanizeCode(code);
}

export const findingHref = (id, section = 'overview') => '#finding-' + id + '/' + section;

function foldItems(items) {
  const out = [];
  for (const it of list(items)) {
    if (out.length >= ITEMS_CAP) break;
    const id = int(it && it.id);
    if (!id) continue;
    const gaps = list(it.gaps).filter((g) => typeof g === 'string');
    const stage = text(it.stage) || 'draft';
    out.push({ id, stage, gaps, readiness: { stage, gaps } });
  }
  return out;
}

// foldAggregate maps GET /api/project/readiness onto the store shape.
export function foldAggregate(agg) {
  const a = agg && typeof agg === 'object' ? agg : {};
  const scope = a.scope || {}, brief = a.brief || {}, ev = a.evidence || {}, f = a.findings || {};
  const items = foldItems(f.items);
  return {
    scope: { enabled: !!scope.enabled, inCount: int(scope.in), outCount: int(scope.out) },
    brief: { target: text(brief.target).slice(0, MAX_TARGET), ok: !!brief.ok },
    evidence: { flows: int(ev.flows), shots: int(ev.shots), ws: int(ev.ws) },
    findings: { total: int(f.total), ready: int(f.ready), items, truncated: !!f.truncated || int(f.total) > items.length },
    serverBlockers: list(a.blockers).filter((c) => typeof c === 'string'),
  };
}

// foldFallback rebuilds the same shape from the individual endpoints when the
// aggregate is unavailable. Codes follow the aggregate: project codes first,
// then each distinct finding gap in first-seen order.
export function foldFallback({ scope, brief, findings } = {}) {
  const rules = list(scope && scope.rules).filter((r) => r && r.enabled);
  const inCount = rules.filter((r) => r.action === 'include').length;
  const outCount = rules.filter((r) => r.action === 'exclude').length;
  const target = text(brief && brief.scope).trim().slice(0, MAX_TARGET);
  const records = list(findings && findings.findings);
  let ready = 0, shots = 0;
  const raw = [];
  for (const f of records) {
    if (f && f.ready) ready++;
    const r = f && f.readiness;
    if (r) shots += int(r.screenshotCount);
    const legacy = list(f && f.missing).map((g) => (g === 'poc' ? 'evidence' : g)).filter((g) => g !== 'poc_before_after');
    raw.push({ id: f && f.id, stage: r ? r.stage : (f && f.ready ? 'report_ready' : 'draft'), gaps: r ? list(r.gaps) : [...new Set(legacy)] });
  }
  const items = foldItems(raw);
  const serverBlockers = [];
  for (const it of items) for (const g of it.gaps) if (!serverBlockers.includes(g)) serverBlockers.push(g);
  return {
    scope: { enabled: inCount > 0, inCount, outCount },
    brief: { target, ok: target !== '' },
    evidence: { flows: 0, shots, ws: 0 },
    findings: { total: records.length, ready, items, truncated: records.length > items.length },
    serverBlockers,
  };
}

// buildBlockers turns server codes into rows. Project codes keep their own fix
// target; finding gaps link to the first finding that carries the gap.
export function buildBlockers(codes, items) {
  const rows = [];
  for (const code of list(codes)) {
    const project = PROJECT_CODES[code];
    const ids = project ? [] : list(items).filter((it) => it.gaps.includes(code)).map((it) => it.id);
    const section = findingSectionForGap(code);
    rows.push({
      code, label: blockerLabel(code), scope: project ? 'project' : 'finding',
      findingIds: ids, section, fix: project ? project.fix : '',
      href: ids.length ? findingHref(ids[0], section) : '',
    });
  }
  return rows;
}

// Rows for the popover: project blockers first, then one row per blocked finding.
export function blockersByFinding(items) {
  return list(items).filter((it) => it.gaps.length).map((it) => ({
    id: it.id, stage: it.stage,
    gaps: it.gaps.map((g) => ({ code: g, label: blockerLabel(g), href: findingHref(it.id, findingSectionForGap(g)) })),
  }));
}

export function nextActionFrom(s) {
  if (!s.brief.ok) return { kind: 'set-target', label: 'Set target' };
  if (!s.scope.enabled) return { kind: 'enable-scope', label: 'Enable scope' };
  if (!s.evidence.flows) return { kind: 'capture', label: 'Capture traffic' };
  const proof = s.findings.items.find((it) => it.gaps.some((g) => findingSectionForGap(g) === 'evidence'));
  if (proof) return { kind: 'attach-proof', label: 'Attach evidence to F-' + proof.id, findingId: proof.id, href: findingHref(proof.id, 'evidence') };
  const blocked = s.findings.items.find((it) => it.gaps.length);
  if (blocked) return { kind: 'fix-blockers', label: 'Fix blockers in F-' + blocked.id, findingId: blocked.id, href: findingHref(blocked.id, findingSectionForGap(blocked.gaps[0])) };
  if (!s.findings.total) return { kind: 'new-finding', label: 'Create a finding' };
  return { kind: 'export', label: 'Export report' };
}

export function readinessValuetext(findings, blockers) {
  const total = int(findings && findings.total), ready = int(findings && findings.ready);
  if (!total) return 'No findings yet';
  const n = list(blockers).length;
  return ready + ' of ' + total + ' findings ready; ' + (n ? n + (n === 1 ? ' blocker' : ' blockers') : 'no blockers');
}

export function identityHue(name) {
  let h = 0;
  for (const ch of String(name || '')) h = (h * 31 + ch.charCodeAt(0)) >>> 0;
  return (h % 6) + 1;
}

export function identityInitials(name) {
  const parts = String(name || '').trim().split(/[\s._-]+/).filter(Boolean);
  if (!parts.length) return '?';
  return (parts.length > 1 ? parts[0][0] + parts[1][0] : parts[0].slice(0, 2)).toUpperCase();
}

const plural = (n, one, many) => n + ' ' + (n === 1 ? one : many);
export function evidenceSummary(ev) {
  const e = ev || {};
  return plural(int(e.flows), 'flow', 'flows') + ', ' + plural(int(e.shots), 'shot', 'shots') + ', ' + int(e.ws) + ' ws';
}

// The chip shows scope state as text (never colour alone). The server has no
// global scope switch: scope is "on" when an enabled include rule exists. The
// switch semantic therefore drives the existing In-scope-only history filter.
export function scopeChipModel(s, { filterOn = false } = {}) {
  const scope = (s && s.scope) || { enabled: false, inCount: 0, outCount: 0 };
  const stale = !!(s && s.stale && s.stale.scope);
  const counts = scope.inCount + ' in / ' + scope.outCount + ' out';
  if (!scope.enabled) {
    return { checked: false, disabled: true, stale, text: 'No scope', label: 'Scope: no include rule set. Open Scope settings.', title: stale ? 'Could not refresh scope' : 'Add an include rule to define scope' };
  }
  return {
    checked: !!filterOn, disabled: false, stale,
    text: (filterOn ? 'Filter ON ' : 'Filter OFF ') + counts,
    label: 'In-scope filter ' + (filterOn ? 'on' : 'off') + ', ' + counts,
    title: stale ? 'Could not refresh scope' : 'Toggle the in-scope history filter. Shift-click opens scope settings.',
  };
}

// createBlockerAnnouncer limits the polite live region to one message per 5s,
// speaking the latest count once the window closes.
export function createBlockerAnnouncer({ now = Date.now, setTimeout: st = globalThis.setTimeout, clearTimeout: ct = globalThis.clearTimeout, emit = () => {}, gap = ANNOUNCE_GAP_MS } = {}) {
  let lastCount = null, lastAt = -Infinity, timer = null, pending = null;
  const say = (n) => { lastCount = n; lastAt = now(); emit(n ? plural(n, 'blocker', 'blockers') : 'No blockers'); };
  return {
    update(n) {
      if (n === lastCount && timer === null) return;
      const wait = lastAt + gap - now();
      if (wait <= 0) { if (timer !== null) { ct(timer); timer = null; } say(n); return; }
      pending = n;
      if (timer === null) timer = st(() => { timer = null; if (pending !== lastCount) say(pending); }, wait);
    },
  };
}

const blankStale = () => Object.fromEntries(SEGMENTS.map((k) => [k, false]));
const blankState = () => ({
  loaded: false, scope: { enabled: false, inCount: 0, outCount: 0 }, brief: { target: '', ok: false },
  identities: [], activeIdentity: '', evidence: { flows: 0, shots: 0, ws: 0 },
  findings: { total: 0, ready: 0, items: [], truncated: false }, blockers: [],
  nextAction: { kind: 'set-target', label: 'Set target' }, stale: blankStale(),
});

export function createProjectState(opts = {}) {
  const timers = {
    now: opts.now || Date.now,
    setTimeout: opts.setTimeout || ((fn, ms) => globalThis.setTimeout(fn, ms)),
    clearTimeout: opts.clearTimeout || ((t) => globalThis.clearTimeout(t)),
  };
  let fetchJson = opts.fetchJson || null;
  let state = blankState();
  const listeners = new Set();
  let debounceTimer = null, waiters = [], epoch = 0;
  let lastFlowRefresh = -Infinity, flowTimer = null;

  const notify = () => { for (const fn of [...listeners]) { try { fn(state); } catch (e) { /* a view must not break the store */ } } };
  const set = (next) => { state = next; notify(); };

  async function settle(path) {
    if (!fetchJson) throw new Error('project state is not attached');
    return fetchJson(path);
  }

  function applyIdentities(prev, result) {
    if (result.status !== 'fulfilled') return { identities: prev.identities, stale: true };
    const names = list(result.value && result.value.identities).filter((i) => i && text(i.name).trim());
    return { identities: names.map((i) => ({ name: i.name.trim(), broken: !!i.broken })), stale: false };
  }

  async function load() {
    const [agg, ids] = await Promise.allSettled([settle(AGGREGATE_PATH), settle('/api/authz')]);
    if (agg.status === 'fulfilled') return { folded: foldAggregate(agg.value), ids, fallback: null };
    const [scope, brief, findings] = await Promise.allSettled([settle('/api/scope'), settle('/api/engagement-brief'), settle('/api/findings')]);
    return { folded: null, ids, fallback: { scope, brief, findings } };
  }

  function merge(prev, res) {
    const stale = blankStale();
    const idn = applyIdentities(prev, res.ids);
    stale.identities = idn.stale;
    let part;
    if (res.folded) part = res.folded;
    else {
      const f = res.fallback, ok = (r) => r.status === 'fulfilled';
      const folded = foldFallback({ scope: ok(f.scope) ? f.scope.value : null, brief: ok(f.brief) ? f.brief.value : null, findings: ok(f.findings) ? f.findings.value : null });
      stale.scope = !ok(f.scope); stale.brief = !ok(f.brief); stale.findings = !ok(f.findings); stale.evidence = true;
      part = {
        scope: stale.scope ? prev.scope : folded.scope,
        brief: stale.brief ? prev.brief : folded.brief,
        evidence: { flows: prev.evidence.flows, shots: stale.findings ? prev.evidence.shots : folded.evidence.shots, ws: prev.evidence.ws },
        findings: stale.findings ? prev.findings : folded.findings,
        serverBlockers: null,
      };
      if (stale.findings || stale.scope || stale.brief) part.serverBlockers = prev.blockers.map((b) => b.code);
      else part.serverBlockers = projectCodes(part).concat(folded.serverBlockers);
    }
    const codes = part.serverBlockers;
    const blockers = buildBlockers(codes, part.findings.items);
    const active = idn.identities.some((i) => i.name === prev.activeIdentity) ? prev.activeIdentity : '';
    const next = { loaded: true, scope: part.scope, brief: part.brief, identities: idn.identities, activeIdentity: active, evidence: part.evidence, findings: part.findings, blockers, stale, nextAction: null };
    next.nextAction = nextActionFrom(next);
    return next;
  }

  async function run() {
    const mine = ++epoch;
    const done = waiters; waiters = [];
    try {
      const res = await load();
      if (mine === epoch) set(merge(state, res));
    } finally { for (const w of done) w(); }
  }

  const api = {
    attach(o = {}) { if (o.fetchJson) fetchJson = o.fetchJson; return api; },
    get: () => state,
    subscribe(fn) { listeners.add(fn); return () => listeners.delete(fn); },
    refresh() {
      return new Promise((resolve) => {
        waiters.push(resolve);
        if (debounceTimer !== null) timers.clearTimeout(debounceTimer);
        // run() releases its waiters in finally; the rejection itself has no owner here.
        debounceTimer = timers.setTimeout(() => { debounceTimer = null; run().catch(() => {}); }, REFRESH_DEBOUNCE_MS);
      });
    },
    // Live traffic only needs fresh counters: one refresh per 2 seconds.
    noteFlowNew() {
      const wait = lastFlowRefresh + FLOW_NEW_THROTTLE_MS - timers.now();
      if (wait <= 0) { lastFlowRefresh = timers.now(); api.refresh(); return; }
      if (flowTimer !== null) return;
      flowTimer = timers.setTimeout(() => { flowTimer = null; lastFlowRefresh = timers.now(); api.refresh(); }, wait);
    },
    setActiveIdentity(name) {
      const n = text(name);
      if (n && !state.identities.some((i) => i.name === n)) return false;
      set({ ...state, activeIdentity: n });
      return true;
    },
    reset() { epoch++; state = blankState(); notify(); },
  };
  return api;
}

function projectCodes(part) {
  const out = [];
  if (!part.brief.ok) out.push('brief_target');
  if (!part.scope.enabled) out.push('scope');
  return out;
}

export const projectState = createProjectState();
