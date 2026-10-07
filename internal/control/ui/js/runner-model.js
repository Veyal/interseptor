// runner-model.js — pure model for the collection runner view (no DOM, no fetch).
// The view (runner.js) renders what these functions return; node tests pin them.
// Every state carries a text label, never colour alone (WCAG AA).

export const BAIL_MODES = ['none', 'on-failure', 'on-error'];
export const PERSIST_MODES = ['discard', 'keep'];
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

const count = (t, k) => (t && Number(t[k])) || 0;

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

export function normalizeRows(results) {
  return (Array.isArray(results) ? results : []).map((r, i) => {
    const state = rowState(r);
    const total = testsTotal(r && r.tests);
    return {
      n: i + 1,
      itemUid: (r && r.itemUid) || '',
      name: (r && r.name) || '(unnamed)',
      outcome: (r && r.outcome) || 'error',
      status: (r && r.status) || 0,
      flowId: (r && r.flowId) || 0,
      error: (r && r.error) || '',
      blockReason: (r && r.blockReason) || '',
      quarantined: (r && r.quarantinedScripts) || 0,
      tests: (r && r.tests) || {},
      state,
      label: stateLabel(state),
      testsText: total ? `${count(r.tests, 'pass')}/${total}` : '-',
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
// body. Unsupported persist modes fall back to the safe one (discard).
export function buildRunBody(o = {}) {
  if (!o.collectionUid) throw new Error('collection is required');
  const body = { collectionUid: o.collectionUid };
  if (o.folderUid) body.folderUid = o.folderUid;
  const ids = (Array.isArray(o.itemUids) ? o.itemUids : []).filter(Boolean);
  if (ids.length) body.itemUids = ids;
  if (o.envUid) body.envUid = o.envUid;
  body.persist = PERSIST_MODES.includes(o.persist) ? o.persist : 'discard';
  body.bail = BAIL_MODES.includes(o.bail) ? o.bail : 'none';
  body.delayMs = clampDelay(o.delayMs);
  body.noScripts = !!o.noScripts;
  return body;
}
