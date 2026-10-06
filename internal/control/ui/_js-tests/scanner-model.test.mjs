import test from 'node:test';
import assert from 'node:assert/strict';
import {
  SEV_ORDER, sevRank, severityMeta, groupIssues, severityCounts, filterGroups, toggleSeverity,
  summaryText, scopeLine, runGate, promoteBody, verifyTarget, unreviewedCount,
} from '../js/scanner-model.js';

const issues = [
  { title: 'Missing HSTS', severity: 'Low', target: 'https://example.com/a', flowId: 3, detail: 'd', fix: 'f' },
  { title: 'Missing HSTS', severity: 'Medium', target: 'https://example.com/b', flowId: 4 },
  { title: 'Reflected input', severity: 'High', target: 'https://example.com/q', flowId: 9, evidence: 'e' },
  { title: 'Server banner', severity: 'Info', target: 'https://example.com/' },
];

test('severity meta is always icon plus text', () => {
  for (const s of SEV_ORDER) {
    const m = severityMeta(s);
    assert.equal(m.label, s);
    assert.ok(m.icon, s + ' has an icon');
  }
  assert.equal(severityMeta('Weird').label, 'Weird');
  assert.equal(sevRank('Critical') < sevRank('High'), true);
});

test('groups keep the most severe member and sort by severity', () => {
  const g = groupIssues(issues);
  assert.deepEqual(g.map((x) => x.title), ['Reflected input', 'Missing HSTS', 'Server banner']);
  assert.equal(g[1].severity, 'Medium');
  assert.equal(g[1].items.length, 2);
  assert.deepEqual(groupIssues(null), []);
});

test('count chips filter the list and toggle', () => {
  const g = groupIssues(issues);
  const counts = severityCounts(g);
  assert.deepEqual(counts, [{ severity: 'High', count: 1 }, { severity: 'Medium', count: 1 }, { severity: 'Info', count: 1 }]);
  assert.equal(filterGroups(g, new Set()).length, 3, 'empty set is no filter');
  assert.deepEqual(filterGroups(g, new Set(['Medium', 'Info'])).map((x) => x.title), ['Missing HSTS', 'Server banner']);
  let active = toggleSeverity(new Set(), 'High');
  assert.equal(active.has('High'), true);
  active = toggleSeverity(active, 'High');
  assert.equal(active.size, 0);
});

test('summary text', () => {
  const g = groupIssues(issues);
  assert.equal(summaryText(g, issues), '3 findings · 4 targets');
  assert.equal(summaryText([], []), '');
});

test('empty scope does not disable Run (the server treats it as all traffic)', () => {
  assert.match(scopeLine({ inCount: 0, hostCount: 2 }), /no include rule set, so all 2 captured hosts are in scope/);
  assert.match(scopeLine({ inCount: 2, hostCount: 1 }), /Scope: 1 in-scope host \(2 include rules\)/);
  assert.deepEqual(runGate({ hostCount: 0, hostsLoaded: false }), { disabled: false, reason: '' });
  assert.equal(runGate({ hostCount: 0, hostsLoaded: true }).disabled, true);
  assert.match(runGate({ hostCount: 0, hostsLoaded: true }).reason, /no in-scope traffic/);
  assert.equal(runGate({ hostCount: 3, hostsLoaded: true }).disabled, false);
});

test('promote attaches each distinct PoC flow through flowIds', () => {
  const g = groupIssues(issues).find((x) => x.title === 'Missing HSTS');
  g.items.push({ ...g.items[0] });
  const b = promoteBody(g);
  assert.equal(b.source, 'scanner');
  assert.deepEqual(b.flowIds, [3, 4]);
  assert.equal(b.title, 'Missing HSTS');
  assert.equal(b.severity, 'Medium');
  assert.equal(b.detail, 'd');
});

test('verify targets only a real flow id', () => {
  assert.deepEqual(verifyTarget({ flowId: 9 }), { id: 9 });
  assert.equal(verifyTarget({}), null);
  assert.equal(verifyTarget({ flowId: -1 }), null);
  assert.equal(verifyTarget(null), null);
});

test('unreviewed count', () => {
  const g = groupIssues(issues);
  assert.equal(unreviewedCount(g, new Set(['Missing HSTS'])), 2);
  assert.equal(unreviewedCount(g, null), 3);
});
