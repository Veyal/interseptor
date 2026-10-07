// runner-model.js — pure model for the collection runner view (no DOM, no fetch).
// The view (runner.js) renders what these functions return; node tests pin them.
// Every state carries a text label, never colour alone (WCAG AA).

export const BAIL_MODES = ['none', 'on-failure', 'on-error'];
export const PERSIST_MODES = ['ask', 'discard', 'keep'];
export const MAX_DELAY_MS = 60000;

const LABELS = {
  pass: 'Passed', fail: 'Failed', error: 'Error', blocked: 'Blocked', skipped: 'Skipped', unsupported: 'Unsupported',
};
const ICONS = { pass: 'check', fail: 'alert', error: 'alert-circle', blocked: 'stop', skipped: 'info', unsupported: 'alert' };
export const stateLabel = (s) => LABELS[s] || 'Error';
export const stateIcon = (s) => ICONS[s] || 'alert';

export function clampDelay(v) {
  const n = Math.floor(Number(v));
  if (!Number.isFinite(n) || n < 0) return 0;
  return Math.min(n, MAX_DELAY_MS);
}

// tests arrive either as a count map ({pass: 2}, the synchronous API rows) or
// as an array of {status} results (live item results); both fold to a map.
function testCounts(t) {
  if (Array.isArray(t)) {
    const m = {};
    for (const x of t) { const k = (x && x.status) || 'error'; m[k] = (m[k] || 0) + 1; }
    return m;
  }
  return t && typeof t === 'object' ? t : {};
}
const count = (t, k) => Number(testCounts(t)[k]) || 0;

// rowState folds a run row into one state. A failing test beats everything
// sent; an unsupported API is reported as such and is never a pass.
export function rowState(row) {
  if (!row) return 'error';
  switch (row.outcome) {
    case 'blocked': return 'blocked';
    case 'skipped': return 'skipped';
    case 'sent': break;
    default: return 'error';
  }
  const t = row.tests;
  if (count(t, 'fail') > 0) return 'fail';
  if (count(t, 'error') > 0) return 'error';
  if (count(t, 'unsupported') > 0) return 'unsupported';
  return 'pass';
}

export function testsTotal(t) {
  return ['pass', 'fail', 'skip', 'error', 'unsupported'].reduce((a, k) => a + count(t, k), 0);
}

const quarantinedCount = (r) => (Array.isArray(r && r.scripts) ? r.scripts.filter((s) => /^quarantined/.test((s && s.reason) || '')).length : 0);

export function normalizeRows(results) {
  return (Array.isArray(results) ? results : []).map((r, i) => {
    const state = rowState(r && { ...r, tests: testCounts(r.tests) });
    const total = testsTotal(r && r.tests);
    return {
      n: i + 1,
      seq: (r && r.seq) || 0,
      itemUid: (r && r.itemUid) || '',
      name: (r && r.name) || '(unnamed)',
      outcome: (r && r.outcome) || 'error',
      status: (r && r.status) || 0,
      flowId: (r && r.flowId) || 0,
      error: (r && r.error) || '',
      blockReason: (r && r.blockReason) || '',
      quarantined: (r && r.quarantinedScripts) || quarantinedCount(r),
      tests: testCounts(r && r.tests),
      state,
      label: stateLabel(state),
      testsText: total ? `${count(r.tests, 'pass')}/${total}` : '-',
      // Live results carry their own detail; no second request is needed.
      detail: {
        tests: Array.isArray(r && r.tests) ? r.tests : [],
        console: (r && r.console) || [],
        varChanges: (r && r.varChanges) || [],
      },
    };
  });
}

export function summarize(rows) {
  const s = { total: 0, pass: 0, fail: 0, error: 0, blocked: 0, skipped: 0, unsupported: 0, testsPass: 0, testsFail: 0 };
  for (const r of rows || []) {
    s.total++;
    s[r.state] = (s[r.state] || 0) + 1;
    s.testsPass += count(r.tests, 'pass');
    s.testsFail += count(r.tests, 'fail');
  }
  return s;
}

export function summaryText(s) {
  if (!s || !s.total) return 'No results yet';
  const parts = [`${s.total} request${s.total === 1 ? '' : 's'}`, `${s.pass} passed`, `${s.fail + s.error} failed`];
  if (s.blocked) parts.push(`${s.blocked} blocked`);
  if (s.unsupported) parts.push(`${s.unsupported} unsupported`);
  if (s.skipped) parts.push(`${s.skipped} skipped`);
  parts.push(`tests ${s.testsPass} passed, ${s.testsFail} failed`);
  return parts.join(', ');
}

const NOT_OK = new Set(['fail', 'error', 'blocked', 'unsupported']);

// failedItemUids: unique request uids that did not pass, in run order, for
// "rerun failed".
export function failedItemUids(rows) {
  const out = [];
  const seen = new Set();
  for (const r of rows || []) {
    if (NOT_OK.has(r.state) && r.itemUid && !seen.has(r.itemUid)) { seen.add(r.itemUid); out.push(r.itemUid); }
  }
  return out;
}

export function filterRows(rows, filter) {
  if (filter === 'failed' || filter === 'problems') return (rows || []).filter((r) => NOT_OK.has(r.state));
  return rows || [];
}

// buildRunBody validates the operator's choices into the POST /api/collections/run
// body. Unsupported persist modes fall back to ask: nothing is written unless
// the operator answers the prompt.
export function buildRunBody(o = {}) {
  if (!o.collectionUid) throw new Error('collection is required');
  const body = { collectionUid: o.collectionUid };
  if (o.folderUid) body.folderUid = o.folderUid;
  const ids = (Array.isArray(o.itemUids) ? o.itemUids : []).filter(Boolean);
  if (ids.length) body.itemUids = ids;
  if (o.envUid) body.envUid = o.envUid;
  body.persist = PERSIST_MODES.includes(o.persist) ? o.persist : 'ask';
  body.bail = BAIL_MODES.includes(o.bail) ? o.bail : 'none';
  body.delayMs = clampDelay(o.delayMs);
  body.noScripts = !!o.noScripts;
  return body;
}

// ---- live runs ---------------------------------------------------------------
// The server owns a run; the view follows it through progress snapshots
// (GET /api/runner/runs/{uid}?since=N) and events (SSE). Both feed the same
// pure reducers, so a replayed or overlapping update never duplicates a row.

const FINISHED = new Set(['done', 'bailed', 'stopped', 'aborted', 'loop_guard', 'scripts_not_approved', 'bad_next_request']);
const PHASE = {
  starting: 'Starting', running: 'Running', paused: 'Paused', awaiting_persist: 'Waiting for your decision on variable changes',
  done: 'Finished', bailed: 'Stopped early (bail rule)', stopped: 'Stopped by a script', aborted: 'Aborted',
  loop_guard: 'Stopped by the loop guard', scripts_not_approved: 'Scripts not approved', bad_next_request: 'Stopped: unknown next request',
};
export const phaseLabel = (s) => PHASE[s] || 'Status: ' + (s || 'unknown');
export const isFinishedStatus = (s) => FINISHED.has(s);

export function newLive() {
  return { runUid: '', status: 'starting', planned: 0, items: [], rows: [], since: 0, finished: false, pending: [], totals: null, report: null, error: '' };
}

function addItems(live, incoming) {
  const items = live.items.slice();
  for (const it of incoming || []) {
    if (!it || !it.seq || items.some((x) => x.seq === it.seq)) continue;
    items.push(it);
  }
  items.sort((a, b) => a.seq - b.seq);
  return { ...live, items, rows: normalizeRows(items) };
}

export function applyProgress(live, p) {
  if (!p) return live;
  let next = { ...live };
  if (p.runUid) next.runUid = p.runUid;
  if (p.plannedSteps) next.planned = p.plannedSteps;
  if (p.status) next.status = p.status;
  if (p.totals) next.totals = p.totals;
  if (p.error) next.error = p.error;
  next.pending = p.pendingWrites || [];
  if (p.report) next.report = p.report;
  next.finished = !!p.finished || (next.finished && !p.status);
  next = addItems(next, p.items);
  next.since = Math.max(live.since, Number(p.count) || next.rows.length);
  return next;
}

export function applyEvent(live, e) {
  if (!e || !e.type) return live;
  if (e.type !== 'start' && live.runUid && e.runUid && e.runUid !== live.runUid) return live;
  switch (e.type) {
    case 'start':
      return { ...live, runUid: e.runUid || live.runUid, planned: e.plannedSteps || live.planned, status: e.status || 'running' };
    case 'item': {
      const n = addItems(live, e.item ? [e.item] : []);
      return { ...n, totals: e.totals || n.totals, since: Math.max(n.since, n.rows.length) };
    }
    case 'paused': return { ...live, status: 'paused' };
    case 'resumed': return { ...live, status: 'running' };
    case 'awaiting_persist': return { ...live, status: 'awaiting_persist', pending: e.pendingWrites || [] };
    case 'done': return { ...live, status: e.status || 'done', finished: true, pending: [], totals: e.totals || live.totals };
    default: return live;
  }
}

// runControls says which run controls are usable now.
export function runControls(live) {
  const none = { pause: false, resume: false, abort: false, decide: false };
  if (!live || live.finished || isFinishedStatus(live.status)) return none;
  switch (live.status) {
    case 'running': return { pause: true, resume: false, abort: true, decide: false };
    case 'paused': return { pause: false, resume: true, abort: true, decide: false };
    case 'awaiting_persist': return { pause: false, resume: false, abort: true, decide: true };
    default: return none;
  }
}
