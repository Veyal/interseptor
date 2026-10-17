import test from 'node:test';
import assert from 'node:assert/strict';
import {
  groupBlockers, summarize, exportGate, draftGate, overrideConfirmed, OVERRIDE_PHRASE,
  exportURL, readinessURL, exportFilename, sanitizeOptions, previewRows, findingReportURL, findingReportName,
  hiddenNotice, findingExportURL, findingExportName, TRACKER_CHOICES, FORMAT_CHOICES, DEFAULT_STATUSES,
} from '../js/report-preflight-model.js';

const quality = (over = {}) => ({
  ready: false, total: 3, summary: { ready: 1, blocked: 2 },
  board: [
    { id: 7, title: 'IDOR on /api/user/{id}', severity: 'High', status: 'open', ready: false, gaps: ['evidence', 'cvss'] },
    { id: 2, title: 'Verbose errors', severity: 'Low', status: 'open', ready: false, gaps: ['fix'] },
    { id: 4, title: 'Open redirect', severity: 'Medium', status: 'verified', ready: true, gaps: [] },
  ],
  findings: [
    { id: 7, title: 'IDOR on /api/user/{id}', ready: false, issues: [{ rule: 'evidence', field: 'blocks', message: 'Attach a proof request.' }, { rule: 'cvss', field: 'cvss', message: 'Add a CVSS v4.0 vector.' }] },
    { id: 2, title: 'Verbose errors', ready: false, issues: [{ rule: 'fix', field: 'fix', message: 'Describe the fix.' }] },
    { id: 4, title: 'Open redirect', ready: true, issues: [] },
  ],
  ...over,
});

test('groupBlockers lists only blocked findings in server order with section links', () => {
  const g = groupBlockers(quality());
  assert.deepEqual(g.map((x) => x.id), [7, 2]);
  assert.equal(g[0].severity, 'High');
  assert.equal(g[0].issues[0].message, 'Attach a proof request.');
  assert.equal(g[0].issues[0].href, '#finding-7/evidence');
  assert.equal(g[0].issues[1].href, '#finding-7/review');
  assert.equal(g[1].issues[0].href, '#finding-2/remediation');
});

test('groupBlockers falls back to board gaps and never invents codes', () => {
  const q = quality({ findings: [] });
  const g = groupBlockers(q);
  assert.deepEqual(g[0].issues.map((i) => i.rule), ['evidence', 'cvss']);
  assert.equal(g[0].issues[0].message, '');
  const odd = groupBlockers({ board: [{ id: 1, title: 'x', ready: false, gaps: ['brand_new_code'] }], findings: [] });
  assert.equal(odd[0].issues[0].rule, 'brand_new_code');
  assert.equal(odd[0].issues[0].label, 'Brand new code');
});

test('groupBlockers tolerates missing or malformed payloads', () => {
  assert.deepEqual(groupBlockers(null), []);
  assert.deepEqual(groupBlockers({}), []);
  assert.deepEqual(groupBlockers({ board: [null, { id: 'x' }, { id: 0 }] }), []);
});

test('summarize reads the server counts and never re-derives pass or fail', () => {
  assert.deepEqual(summarize(quality()), { total: 3, ready: 1, blocked: 2, allReady: false });
  assert.deepEqual(summarize(quality({ ready: true, summary: { ready: 3, blocked: 0 } })), { total: 3, ready: 3, blocked: 0, allReady: true });
  assert.deepEqual(summarize(null), { total: 0, ready: 0, blocked: 0, allReady: false });
});

test('exportGate blocks while loading, on error, when empty and when blockers exist', () => {
  assert.equal(exportGate({ loading: true }).allowed, false);
  assert.match(exportGate({ loading: true }).reason, /Checking/);
  const err = exportGate({ error: 'boom' });
  assert.equal(err.allowed, false);
  assert.match(err.reason, /boom/);
  assert.equal(exportGate({}).allowed, false);
  const empty = exportGate({ quality: { ready: false, total: 0, summary: { ready: 0, blocked: 0 }, board: [], findings: [], message: 'No findings to report yet.' } });
  assert.equal(empty.allowed, false);
  assert.match(empty.reason, /No findings to report yet/);
  const filtered = exportGate({ quality: { ready: false, total: 0, board: [], findings: [], hidden: { needs_verification: 1 } } });
  assert.match(filtered.reason, /Include/);
  const blocked = exportGate({ quality: quality() });
  assert.equal(blocked.allowed, false);
  assert.match(blocked.reason, /2 of 3 findings have blockers/);
  assert.equal(exportGate({ quality: quality({ ready: true, summary: { ready: 3, blocked: 0 } }) }).allowed, true);
  assert.equal(exportGate({ quality: quality({ ready: true, summary: { ready: 3, blocked: 0 } }), busy: true }).allowed, false);
});

test('exportGate singular wording', () => {
  const q = quality({ total: 1, summary: { ready: 0, blocked: 1 } });
  assert.match(exportGate({ quality: q }).reason, /1 of 1 finding has a blocker/);
});

test('draftGate only offers the override when blockers exist', () => {
  assert.equal(draftGate({ quality: quality() }).available, true);
  assert.equal(draftGate({ quality: quality({ ready: true, summary: { ready: 3, blocked: 0 } }) }).available, false);
  assert.equal(draftGate({ loading: true, quality: quality() }).available, false);
  assert.equal(draftGate({ quality: null }).available, false);
  assert.equal(draftGate({ quality: quality(), busy: true }).available, false);
});

test('override requires the typed phrase exactly', () => {
  assert.equal(OVERRIDE_PHRASE, 'DRAFT');
  assert.equal(overrideConfirmed('DRAFT'), true);
  assert.equal(overrideConfirmed('  DRAFT '), true);
  assert.equal(overrideConfirmed('draft'), false);
  assert.equal(overrideConfirmed('DRAF'), false);
  assert.equal(overrideConfirmed(''), false);
  assert.equal(overrideConfirmed(null), false);
});

test('exportURL targets the report endpoint and masks credentials unless turned off', () => {
  const u = exportURL({ format: 'html', statuses: 'all', groupByTag: true, includeEvidence: true }, 'final');
  assert.equal(u, '/api/findings/report?format=html&statuses=all&mode=final&groupBy=tag&maskHeaders=1');
  const d = exportURL({ format: 'md', statuses: 'open,verified,fixed', groupByTag: false, includeEvidence: false, maskHeaders: false }, 'draft');
  assert.equal(d, '/api/findings/report?format=md&statuses=open%2Cverified%2Cfixed&mode=draft&includeBodies=0');
  assert.equal(exportURL({ format: 'pdf' }, 'final'), '/api/findings/report?format=pdf&statuses=all&mode=final&maskHeaders=1');
  assert.equal(exportURL({ format: 'sarif' }, 'draft'), '/api/findings/report?format=sarif&statuses=all&mode=draft&maskHeaders=1');
});

test('sanitizeOptions rejects unknown formats and statuses, defaulting to every status', () => {
  assert.equal(DEFAULT_STATUSES, 'all');
  assert.deepEqual(sanitizeOptions({ format: 'exe', statuses: 'x', groupByTag: 1, includeEvidence: 0, maskHeaders: 0 }), { format: 'md', statuses: 'all', groupByTag: true, includeEvidence: false, maskHeaders: false });
  assert.deepEqual(sanitizeOptions(undefined), { format: 'md', statuses: 'all', groupByTag: false, includeEvidence: true, maskHeaders: true });
  assert.ok(['md', 'html', 'pdf', 'json', 'sarif'].every((f) => FORMAT_CHOICES.some((c) => c.value === f)));
});

test('readinessURL and exportFilename', () => {
  assert.equal(readinessURL('verified'), '/api/findings/readiness?statuses=verified');
  assert.equal(readinessURL('bogus'), '/api/findings/readiness?statuses=all');
  assert.equal(exportFilename('html'), 'interseptor-findings.html');
  assert.equal(exportFilename('pdf'), 'interseptor-findings.pdf');
  assert.equal(exportFilename('weird'), 'interseptor-findings.md');
});

test('hiddenNotice says which statuses the filter left out', () => {
  assert.equal(hiddenNotice(null), null);
  assert.equal(hiddenNotice({ hidden: {} }), null);
  const n = hiddenNotice({ hidden: { needs_verification: 1, false_positive: 2 } });
  assert.equal(n.count, 3);
  assert.equal(n.text, '3 findings hidden by this filter: 1 needs verification, 2 false positive.');
});

test('previewRows lists the server board in order and flags blocked rows with text', () => {
  const rows = previewRows(quality());
  assert.equal(rows.length, 3);
  assert.deepEqual(rows[0], { id: 7, title: 'IDOR on /api/user/{id}', severity: 'High', ready: false, status: 'Blocked' });
  assert.equal(rows[2].status, 'Ready');
  assert.deepEqual(previewRows(null), []);
});

test('blocker rows closed by reproduction say where to verify', async () => {
  const { groupBlockers, VERIFY_HINT } = await import('../js/report-preflight-model.js');
  const g = groupBlockers({ board: [{ id: 4, title: 'IDOR', ready: false, gaps: ['verification', 'cvss'] }], findings: [] });
  const byRule = Object.fromEntries(g[0].issues.map((i) => [i.rule, i]));
  assert.equal(byRule.verification.hint, VERIFY_HINT);
  assert.match(VERIFY_HINT, /Repeater/);
  assert.equal(byRule.cvss.hint, undefined, 'other gaps carry no verify hint');
});

test('findingReportURL selects one finding of any status in a valid format, default md', () => {
  assert.equal(findingReportURL(41, 'json'), '/api/findings/report?ids=41&format=json&mode=draft&statuses=all&maskHeaders=1');
  assert.equal(findingReportURL(41, 'pdf'), '/api/findings/report?ids=41&format=pdf&mode=draft&statuses=all&maskHeaders=1');
  assert.equal(findingReportURL(41, 'exe'), '/api/findings/report?ids=41&format=md&mode=draft&statuses=all&maskHeaders=1');
  assert.equal(findingReportURL('x', 'html'), '/api/findings/report?ids=0&format=html&mode=draft&statuses=all&maskHeaders=1');
  assert.equal(findingReportName(41, 'json'), 'finding-41.json');
  assert.equal(findingReportName(41, 'bogus'), 'finding-41.md');
});

test('tracker exports target the per-finding endpoint', () => {
  assert.deepEqual(TRACKER_CHOICES.map((t) => t.value), ['hackerone', 'bugcrowd', 'jira']);
  assert.equal(findingExportURL(41, 'jira'), '/api/finding-export/41?format=jira');
  assert.equal(findingExportURL(41, 'csv'), '');
  assert.equal(findingExportName(41, 'hackerone'), 'finding-41-hackerone.json');
});
