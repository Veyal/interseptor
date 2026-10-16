import test from 'node:test';
import assert from 'node:assert/strict';
import { ringModel, meterModel, blockerChecklist, compactIntercept, RING_BADGE_CAP, POPOVER_ROW_CAP } from '../js/ctxbar-model.js';
import { foldAggregate, buildBlockers, nextActionFrom } from '../js/project-state.js';

// stateFrom builds the store shape the Flight Bar renders, from an aggregate
// payload shaped like GET /api/project/readiness, the way project-state.js does.
function stateFrom(agg, { loaded = true, stale = {} } = {}) {
  const f = foldAggregate(agg);
  const s = {
    loaded, scope: f.scope, brief: f.brief, identities: [], activeIdentity: '', evidence: f.evidence, findings: f.findings,
    blockers: buildBlockers(f.serverBlockers, f.findings.items), stale: { scope: false, brief: false, identities: false, findings: false, evidence: false, ...stale },
    nextAction: null,
  };
  s.nextAction = nextActionFrom(s);
  return s;
}

const READY_ALL = { scope: { enabled: true, in: 1, out: 0 }, brief: { target: 'app.example.com', ok: true }, evidence: { flows: 9, shots: 1, ws: 0 },
  findings: { total: 2, ready: 2, items: [{ id: 1, stage: 'report_ready', gaps: [] }, { id: 2, stage: 'report_ready', gaps: [] }] }, blockers: [] };
const BLOCKED = { scope: { enabled: true, in: 1, out: 0 }, brief: { target: 'app.example.com', ok: true }, evidence: { flows: 9, shots: 0, ws: 0 },
  findings: { total: 5, ready: 2, items: [{ id: 1, stage: 'report_ready', gaps: [] }, { id: 2, stage: 'draft', gaps: ['evidence', 'cvss'] }, { id: 3, stage: 'draft', gaps: ['cvss'] }] },
  blockers: ['evidence', 'cvss'] };
const NO_SETUP = { scope: { enabled: false, in: 0, out: 0 }, brief: { target: '', ok: false }, evidence: { flows: 0, shots: 0, ws: 0 },
  findings: { total: 0, ready: 0, items: [] }, blockers: ['brief_target', 'scope'] };

test('the ring is neutral and says nothing about findings before the project state loads', () => {
  const m = ringModel(stateFrom(NO_SETUP, { loaded: false }), { reportOpen: true });
  assert.equal(m.state, 'loading');
  assert.equal(m.mark, 'none');
  assert.equal(m.reportTime, false);
  assert.equal(m.text, '');
  assert.equal(m.badge, '');
  assert.equal(m.pct, 0);
  assert.match(m.label, /loading/i);
});

test('outside the Report view the ring carries no readiness signal, only setup attention', () => {
  const idle = ringModel(stateFrom(BLOCKED), { reportOpen: false });
  assert.equal(idle.state, 'idle');
  assert.equal(idle.mark, 'none');
  assert.equal(idle.reportTime, false);
  assert.equal(idle.text, '', 'the ready fraction is report-time information');
  assert.equal(idle.badge, '', 'the blocker count is report-time information');
  assert.equal(idle.pct, 0);
  assert.doesNotMatch(idle.label, /blocker/i);

  const setup = ringModel(stateFrom(NO_SETUP), { reportOpen: false });
  assert.equal(setup.state, 'setup');
  assert.equal(setup.mark, 'bang', 'a missing target or scope is flagged with a shape, never colour alone');
  assert.match(setup.label, /Setup incomplete/);
  assert.match(setup.label, /Next: Set target/);
  assert.equal(setup.badge, '');
});

test('inside the Report view the ring shows the ready fraction, an arc and the blocker count', () => {
  const m = ringModel(stateFrom(BLOCKED), { reportOpen: true });
  assert.equal(m.reportTime, true);
  assert.equal(m.state, 'blocked');
  assert.equal(m.mark, 'bang');
  assert.equal(m.text, '2/5');
  assert.equal(m.pct, 40);
  assert.equal(m.badge, '2');
  assert.match(m.label, /^Report readiness 2\/5\./);
  assert.match(m.label, /2 blockers/);
  assert.match(m.title, /2 of 5 findings ready; 2 blockers/);
});

test('a ring with every finding ready and no blocker reads ready, with a tick', () => {
  const m = ringModel(stateFrom(READY_ALL), { reportOpen: true });
  assert.equal(m.state, 'ready');
  assert.equal(m.mark, 'check');
  assert.equal(m.pct, 100);
  assert.equal(m.text, '2/2');
  assert.equal(m.badge, '');
  assert.match(m.label, /No blockers/);
});

test('with no findings the Report ring shows no fraction and no arc', () => {
  const noFindings = { ...READY_ALL, findings: { total: 0, ready: 0, items: [] } };
  const m = ringModel(stateFrom(noFindings), { reportOpen: true });
  assert.equal(m.text, '');
  assert.equal(m.pct, 0);
  assert.equal(m.state, 'idle');
  assert.match(m.label, /no findings yet/i);
  assert.match(m.title, /No findings yet/);
});

test('the blocker badge is capped so it never outgrows the bar', () => {
  const many = { ...BLOCKED, blockers: Array.from({ length: 140 }, (_, i) => 'gap' + i) };
  const m = ringModel(stateFrom(many), { reportOpen: true });
  assert.equal(m.badge, RING_BADGE_CAP + '+');
  const one = ringModel(stateFrom({ ...BLOCKED, blockers: ['cvss'] }), { reportOpen: true });
  assert.equal(one.badge, '1');
  assert.match(one.label, /1 blocker\b/);
});

test('a stale findings segment is named in the tooltip instead of showing old numbers as fresh', () => {
  const m = ringModel(stateFrom(BLOCKED, { stale: { findings: true } }), { reportOpen: true });
  assert.equal(m.title, 'Could not refresh findings');
});

test('the visible ring text is part of its accessible name (WCAG 2.5.3), including a capped badge', () => {
  const capped = { ...BLOCKED, blockers: Array.from({ length: 140 }, (_, i) => 'gap' + i) };
  const cases = { blocked: BLOCKED, ready: READY_ALL, 'project setup only': NO_SETUP, capped };
  for (const [name, agg] of Object.entries(cases)) {
    const m = ringModel(stateFrom(agg), { reportOpen: true });
    if (m.text) assert.ok(m.label.includes(m.text), name + ': name must contain the visible fraction ' + m.text + ' in ' + m.label);
    if (m.badge) assert.ok(m.label.includes(m.badge + ' blocker'), name + ': name must contain the visible blocker count ' + m.badge + ' in ' + m.label);
  }
  const m = ringModel(stateFrom(capped), { reportOpen: true });
  assert.equal(m.badge, '99+');
  assert.match(m.label, /99\+ blockers/);
  assert.match(m.title, /140 blockers/, 'the exact count stays in the tooltip');
});

test('some findings ready and nothing blocking reads as progress, with no mark', () => {
  const partial = { ...READY_ALL, findings: { total: 3, ready: 2, items: [{ id: 1, stage: 'report_ready', gaps: [] }, { id: 2, stage: 'report_ready', gaps: [] }, { id: 3, stage: 'draft', gaps: [] }] } };
  const m = ringModel(stateFrom(partial), { reportOpen: true });
  assert.equal(m.state, 'progress');
  assert.equal(m.mark, 'none');
  assert.equal(m.text, '2/3');
  assert.equal(m.pct, 67);
  assert.equal(m.badge, '');
  assert.match(m.label, /No blockers/);
});

test('project blockers are counted on the ring while the Report view is open, even with no findings', () => {
  const m = ringModel(stateFrom(NO_SETUP), { reportOpen: true });
  assert.equal(m.reportTime, true);
  assert.equal(m.state, 'blocked');
  assert.equal(m.mark, 'bang');
  assert.equal(m.text, '');
  assert.equal(m.badge, '2');
  assert.match(m.label, /^Report readiness: no findings yet\. 2 blockers\./);
});

test('the popover meter keeps the role=meter range and the segment cap', () => {
  const m = meterModel(stateFrom(BLOCKED));
  assert.equal(m.max, 5);
  assert.equal(m.now, 2);
  assert.equal(m.valuetext, '2 of 5 findings ready; 2 blockers');
  assert.equal(m.hidden, false);
  assert.deepEqual(m.segments, { n: 5, filled: 2 });

  const big = meterModel(stateFrom({ ...BLOCKED, findings: { ...BLOCKED.findings, total: 40, ready: 10 } }));
  assert.deepEqual(big.segments, { n: 10, filled: 3 }, 'segments are capped at ten and scaled');

  const empty = meterModel(stateFrom(NO_SETUP));
  assert.equal(empty.max, 1);
  assert.equal(empty.now, 0);
  assert.equal(empty.hidden, true);
  assert.equal(empty.valuetext, 'No findings yet');
});

test('the readiness popover checklist lists project blockers first, then one row per blocked finding', () => {
  const list = blockerChecklist(stateFrom({ ...BLOCKED, blockers: ['brief_target', 'evidence', 'cvss'] }));
  assert.equal(list.empty, false);
  assert.deepEqual(list.project.map((b) => b.code), ['brief_target']);
  assert.equal(list.project[0].label, 'No authorised target in the engagement brief');
  assert.deepEqual(list.findings.map((f) => f.id), [2, 3]);
  assert.deepEqual(list.findings[0].gaps.map((g) => g.label), ['Proof evidence', 'CVSS vector']);
  assert.equal(list.findings[0].gaps[0].href, '#finding-2/evidence');
  assert.equal(list.more, 0);
  assert.equal(list.moreText, '');
});

test('the checklist says so when nothing blocks the report, and when it cannot tell', () => {
  const clear = blockerChecklist(stateFrom(READY_ALL));
  assert.equal(clear.empty, true);
  assert.equal(clear.emptyText, 'Nothing blocks the report.');
  const stale = blockerChecklist(stateFrom(READY_ALL, { stale: { findings: true } }));
  assert.equal(stale.emptyText, 'Could not refresh findings. Showing the last known state.');
});

test('before the project state loads the checklist says it is loading instead of claiming nothing blocks the report', () => {
  const list = blockerChecklist(stateFrom(READY_ALL, { loaded: false }));
  assert.equal(list.empty, true);
  assert.equal(list.emptyText, 'Loading readiness\u2026');
});

test('the checklist is capped and says how many findings it left out', () => {
  const items = Array.from({ length: POPOVER_ROW_CAP + 7 }, (_, i) => ({ id: i + 1, stage: 'draft', gaps: ['cvss'] }));
  const list = blockerChecklist(stateFrom({ ...BLOCKED, findings: { total: items.length, ready: 0, items }, blockers: ['cvss'] }));
  assert.equal(list.findings.length, POPOVER_ROW_CAP);
  assert.equal(list.more, 7);
  assert.equal(list.moreText, '7 more findings. Open Findings to see all.');
});

test('the narrow-tier intercept label keeps what matters and drops the filler words', () => {
  assert.equal(compactIntercept('\u00b7 REQ intercept ON'), 'REQ');
  assert.equal(compactIntercept('\u00b7 RESP intercept ON'), 'RESP');
  assert.equal(compactIntercept('\u00b7 REQ intercept ON \u00b7 RESP intercept ON \u00b7 3 held'), 'REQ \u00b7 RESP \u00b7 3 held');
  assert.equal(compactIntercept('\u00b7 2 held'), '2 held');
  assert.equal(compactIntercept('  \u00b7   req   intercept   on  '), 'REQ');
});

test('the narrow-tier intercept label is empty when there is nothing to say, and keeps an unknown part verbatim', () => {
  assert.equal(compactIntercept(''), '');
  assert.equal(compactIntercept(null), '');
  assert.equal(compactIntercept(undefined), '');
  assert.equal(compactIntercept('\u00b7 \u00b7 '), '');
  assert.equal(compactIntercept('\u00b7 Something new'), 'Something new');
});
