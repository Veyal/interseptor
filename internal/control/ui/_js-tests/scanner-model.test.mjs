import test from 'node:test';
import assert from 'node:assert/strict';
import {
  SEV_ORDER, sevRank, severityMeta, groupIssues, severityCounts, filterGroups, toggleSeverity,
  onlySeverity, summaryText, issueHost, hostCounts, issuesForHost, groupHostLabel, coverageNote, scopeLine, runGate, promoteBody, verifyTarget, unreviewedCount,
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

test('"Filter this severity" keeps the issue severity as the chips spell it', () => {
  const g = groupIssues(issues);
  // The context menu used to lower-case the severity ("medium"), which matched no
  // group and no chip, so the list emptied with no chip pressed.
  assert.deepEqual(filterGroups(g, onlySeverity('Medium')).map((x) => x.title), ['Missing HSTS']);
  assert.deepEqual([...onlySeverity('High')], ['High']);
  assert.equal(onlySeverity('').size, 0);
});

test('summary text', () => {
  const g = groupIssues(issues);
  assert.equal(summaryText(g, issues), '3 issues · 4 targets');
  assert.equal(summaryText([], []), '');
});

// The header counts what the list shows once a severity chip narrows it.
test('summary text follows an active severity chip', () => {
  const g = groupIssues(issues);
  const shown = g.slice(0, 1);
  const targets = shown[0].items.length;
  assert.equal(summaryText(g, issues, shown), '1 of 3 issues · ' + targets + ' target' + (targets === 1 ? '' : 's'));
  assert.equal(summaryText(g, issues, g), '3 issues · 4 targets');
  assert.equal(summaryText(g, issues, []), '0 of 3 issues · 0 targets');
});

test('issue hosts: parsed from every target shape, counted and filterable', () => {
  assert.equal(issueHost('GET app.example.com/p?q=1'), 'app.example.com');
  assert.equal(issueHost('POST API.example.com'), 'api.example.com');
  assert.equal(issueHost('https://www.example.com:8443/x'), 'www.example.com');
  assert.equal(issueHost('cdn.example.com/a.js'), 'cdn.example.com');
  assert.equal(issueHost(''), '');
  const mixed = [
    { title: 'Missing headers', severity: 'Medium', target: 'GET app.example.com/a' },
    { title: 'Missing headers', severity: 'Medium', target: 'GET app.example.com/b' },
    { title: 'Missing headers', severity: 'Medium', target: 'GET accounts.example.net/' },
    { title: 'CORS', severity: 'High', target: 'GET accounts.example.net/o' },
  ];
  assert.deepEqual(hostCounts(mixed), [{ host: 'accounts.example.net', count: 2 }, { host: 'app.example.com', count: 2 }]);
  const onlyApp = groupIssues(issuesForHost(mixed, 'app.example.com'));
  assert.deepEqual(onlyApp.map((g) => [g.title, g.items.length]), [['Missing headers', 2]]);
  assert.equal(issuesForHost(mixed, '').length, 4);
  assert.equal(groupHostLabel(groupIssues(mixed).find((g) => g.title === 'Missing headers')), '3 targets · 2 hosts');
  assert.equal(groupHostLabel(onlyApp[0]), '2 targets');
});

test('a capped scan says older traffic was not scanned', () => {
  assert.equal(coverageNote({ flows: 5000, capped: false }), '');
  assert.equal(coverageNote(null), '');
  assert.match(coverageNote({ flows: 5000, limit: 5000, capped: true }), /newest 5,000 matching flows; older traffic was not scanned/);
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

import { splitScanTarget } from '../js/scanner-model.js';

test('splitScanTarget separates method, host and path', () => {
  assert.deepEqual(splitScanTarget('GET api.example.com/boom'), { method: 'GET', host: 'api.example.com', path: '/boom' });
  assert.deepEqual(splitScanTarget('POST shop.example.com/search?q=1'), { method: 'POST', host: 'shop.example.com', path: '/search?q=1' });
  assert.deepEqual(splitScanTarget('https://example.com/a/b'), { method: '', host: 'example.com', path: '/a/b' });
  assert.deepEqual(splitScanTarget('GET example.com'), { method: 'GET', host: 'example.com', path: '' });
  assert.deepEqual(splitScanTarget(''), { method: '', host: '', path: '' });
  assert.deepEqual(splitScanTarget(null), { method: '', host: '', path: '' });
});

test('completion toast counts grouped issues, not target rows', async () => {
  const { scanToastText } = await import('../js/scanner-model.js');
  const issues = [
    { title: 'Missing HSTS', severity: 'Low', target: 'GET a.test/x' },
    { title: 'Missing HSTS', severity: 'Low', target: 'GET a.test/y' },
    { title: 'Missing HSTS', severity: 'Low', target: 'GET a.test/z' },
    { title: 'Server banner', severity: 'Info', target: 'GET a.test/' },
  ];
  assert.equal(scanToastText(issues), '2 issues');
  assert.equal(scanToastText(issues.slice(0, 1), 'a.test', 'x'), '1 issue · a.test · "x"');
  assert.equal(scanToastText([]), '0 issues');
});
