// report-preflight-model.js — pure logic behind the Report sub-view.
//
// P1 (display, never derive): the server owns readiness. `GET
// /api/findings/readiness` (and the 409 body of a final export) says which
// findings are blocked and why; this module only groups, words and gates on that
// answer. It never inspects finding content and never decides that a check
// passed, so a code the client does not know renders generically.
//
// No DOM access: the file runs under `node --test`.

import { findingSectionForGap } from './finding-workspace.js';
import { blockerLabel, findingHref } from './project-state.js';

export const OVERRIDE_PHRASE = 'DRAFT';
export const DEFAULT_STATUSES = 'open,verified,fixed';
export const STATUS_CHOICES = [
  { value: 'open,verified,fixed', label: 'Open, verified and fixed' },
  { value: 'all', label: 'All statuses' },
  { value: 'verified', label: 'Verified only' },
  { value: 'open', label: 'Open only' },
  { value: 'needs_verification', label: 'Needs verification' },
];
export const FORMAT_CHOICES = [
  { value: 'md', label: 'Markdown' },
  { value: 'html', label: 'HTML with images' },
  { value: 'json', label: 'JSON' },
];

const list = (a) => (Array.isArray(a) ? a : []);
const text = (s) => (typeof s === 'string' ? s : '');
const posInt = (n) => (Number.isFinite(Number(n)) && Number(n) > 0 ? Math.floor(Number(n)) : 0);
const plural = (n, one, many) => n + ' ' + (n === 1 ? one : many);

// Gaps that are closed by reproducing the issue: the pipeline has no Verify tab,
// so the blocker row says where verification happens.
export const VERIFY_RULES = new Set(['verification', 'reproduction', 'retest', 'proof']);
export const VERIFY_HINT = 'Reproduce it in Repeater, then attach the response as evidence.';

function issueRow(id, rule, message) {
  const section = findingSectionForGap(rule);
  const row = { rule, label: blockerLabel(rule), message, section, href: findingHref(id, section) };
  if (VERIFY_RULES.has(rule)) row.hint = VERIFY_HINT;
  return row;
}

// groupBlockers returns one group per blocked finding, in the server's board
// order (severity first). Issue messages come from `findings[].issues`; when the
// payload carries only the board, the gap codes are shown without a message.
export function groupBlockers(quality) {
  const q = quality && typeof quality === 'object' ? quality : {};
  const detail = new Map();
  for (const f of list(q.findings)) {
    const id = posInt(f && f.id);
    if (id) detail.set(id, f);
  }
  const rows = list(q.board).length ? list(q.board) : list(q.findings);
  const groups = [];
  for (const r of rows) {
    const id = posInt(r && r.id);
    if (!id || r.ready === true) continue;
    const d = detail.get(id);
    const issues = list(d && d.issues).filter((i) => i && typeof i.rule === 'string' && i.rule).map((i) => issueRow(id, i.rule, text(i.message)));
    const fromGaps = issues.length ? issues : list(r.gaps).filter((g) => typeof g === 'string' && g).map((g) => issueRow(id, g, ''));
    if (!fromGaps.length && d && d.ready !== false && r.ready !== false) continue;
    groups.push({ id, title: text(r.title) || text(d && d.title) || 'Untitled', severity: text(r.severity), status: text(r.status), issues: fromGaps });
  }
  return groups;
}

// summarize reads the server's counts. It counts blocked board rows only when
// the server sent no summary.
export function summarize(quality) {
  const q = quality && typeof quality === 'object' ? quality : null;
  if (!q) return { total: 0, ready: 0, blocked: 0, allReady: false };
  const total = posInt(q.total) || list(q.board).length;
  const s = q.summary && typeof q.summary === 'object' ? q.summary : null;
  const blocked = s ? posInt(s.blocked) : list(q.board).filter((r) => r && r.ready !== true).length;
  const ready = s ? posInt(s.ready) : Math.max(0, total - blocked);
  return { total, ready, blocked, allReady: q.ready === true && total > 0 && blocked === 0 };
}

// exportGate decides whether the final export button is usable. A disabled
// button always comes with a reason the page shows as text.
export function exportGate({ quality = null, loading = false, error = '', busy = false } = {}) {
  if (busy) return { allowed: false, code: 'busy', reason: 'Exporting…' };
  if (loading) return { allowed: false, code: 'loading', reason: 'Checking readiness…' };
  if (error) return { allowed: false, code: 'error', reason: 'Readiness could not be checked: ' + error };
  if (!quality) return { allowed: false, code: 'unknown', reason: 'Readiness has not been checked yet.' };
  const s = summarize(quality);
  if (s.total === 0) return { allowed: false, code: 'empty', reason: text(quality.message) || 'Select at least one finding for a final report.' };
  if (s.blocked > 0) {
    const verb = s.blocked === 1 ? 'has a blocker' : 'have blockers';
    return { allowed: false, code: 'blocked', reason: `${s.blocked} of ${s.total} ${s.total === 1 ? 'finding' : 'findings'} ${verb}. Resolve them or export a draft.` };
  }
  if (!s.allReady) return { allowed: false, code: 'not-ready', reason: 'The server does not report this set as ready for a final report.' };
  return { allowed: true, code: 'ok', reason: 'All ' + plural(s.total, 'finding is', 'findings are') + ' ready for a final report.' };
}

// draftGate offers "Export draft anyway" only while blockers exist; with no
// blockers the normal export is the right action.
export function draftGate({ quality = null, loading = false, error = '', busy = false } = {}) {
  if (busy || loading || error || !quality) return { available: false };
  const s = summarize(quality);
  return { available: s.total > 0 && s.blocked > 0 };
}

export function overrideConfirmed(typed) {
  return typeof typed === 'string' && typed.trim() === OVERRIDE_PHRASE;
}

export function sanitizeOptions(o) {
  const x = o && typeof o === 'object' ? o : {};
  return {
    format: FORMAT_CHOICES.some((f) => f.value === x.format) ? x.format : 'md',
    statuses: STATUS_CHOICES.some((s) => s.value === x.statuses) ? x.statuses : DEFAULT_STATUSES,
    groupByTag: !!x.groupByTag,
    includeEvidence: o && typeof o === 'object' && 'includeEvidence' in x ? !!x.includeEvidence : true,
  };
}

// exportURL builds the same request the legacy export modal sends, plus the
// existing includeBodies switch for "include evidence".
export function exportURL(options, mode) {
  const o = sanitizeOptions(options);
  const m = mode === 'final' ? 'final' : 'draft';
  let url = '/api/findings/report?format=' + encodeURIComponent(o.format) + '&statuses=' + encodeURIComponent(o.statuses) + '&mode=' + m;
  if (o.groupByTag) url += '&groupBy=tag';
  if (!o.includeEvidence) url += '&includeBodies=0';
  return url;
}

export function readinessURL(statuses) {
  return '/api/findings/readiness?statuses=' + encodeURIComponent(sanitizeOptions({ statuses }).statuses);
}

export function exportFilename(format) {
  return 'interseptor-findings.' + sanitizeOptions({ format }).format;
}

// previewRows is the skeleton preview: the findings the export would contain,
// in report order, each with a text status.
export function previewRows(quality) {
  const out = [];
  for (const r of list(quality && quality.board)) {
    const id = posInt(r && r.id);
    if (!id) continue;
    out.push({ id, title: text(r.title) || 'Untitled', severity: text(r.severity), ready: r.ready === true, status: r.ready === true ? 'Ready' : 'Blocked' });
  }
  return out;
}
