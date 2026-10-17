// scanner-model.js — pure logic for the Scanner panel (no imports, no DOM), so
// it runs under `node --test`.
//
// Scanner issues are finding candidates. The API exposes issue severity, title,
// target, detail, evidence, fix and an optional flowId; nothing here invents
// other data (no payload is attached to an issue, so "Verify in Repeater"
// preloads the affected flow only).

export const SEV_ORDER = ['Critical', 'High', 'Medium', 'Low', 'Info'];
export const sevRank = (s) => {
  const i = SEV_ORDER.indexOf(s);
  return i < 0 ? SEV_ORDER.length : i;
};

// Every severity is icon + text; colour only reinforces it.
const SEV_ICON = { Critical: 'sev-critical', High: 'alert', Medium: 'alert-circle', Low: 'sev-low', Info: 'info' };
export function severityMeta(sev) {
  const label = SEV_ORDER.includes(sev) ? sev : String(sev || 'Unknown');
  return { label, icon: SEV_ICON[sev] || 'info' };
}

export function groupIssues(issues) {
  const map = new Map();
  for (const i of Array.isArray(issues) ? issues : []) {
    let g = map.get(i.title);
    if (!g) {
      g = { title: i.title, severity: i.severity, items: [] };
      map.set(i.title, g);
    }
    g.items.push(i);
    if (sevRank(i.severity) < sevRank(g.severity)) g.severity = i.severity;
  }
  return [...map.values()].sort((a, b) => sevRank(a.severity) - sevRank(b.severity) || a.title.localeCompare(b.title));
}

// severityCounts counts groups (the unit the list shows) per severity, in
// severity order, omitting severities with no results.
export function severityCounts(groups) {
  const counts = new Map();
  for (const g of Array.isArray(groups) ? groups : []) counts.set(g.severity, (counts.get(g.severity) || 0) + 1);
  return [...counts.entries()].sort((a, b) => sevRank(a[0]) - sevRank(b[0])).map(([severity, count]) => ({ severity, count }));
}

// filterGroups keeps the groups whose severity is active. An empty active set
// means "no filter".
export function filterGroups(groups, active) {
  const set = active instanceof Set ? active : new Set(active || []);
  const list = Array.isArray(groups) ? groups : [];
  return set.size ? list.filter((g) => set.has(g.severity)) : list.slice();
}

// onlySeverity is the filter "Filter this severity" applies: exactly the issue's own
// severity, spelled as the chips and filterGroups spell it ("Medium", not "medium").
export function onlySeverity(severity) {
  return severity ? new Set([String(severity)]) : new Set();
}

export function toggleSeverity(active, severity) {
  const next = new Set(active || []);
  if (next.has(severity)) next.delete(severity);
  else next.add(severity);
  return next;
}

// Results are "issues": "findings" are the curated reports in the Findings tool.
export function summaryText(groups, issues) {
  const g = Array.isArray(groups) ? groups.length : 0;
  const t = Array.isArray(issues) ? issues.length : 0;
  if (!g) return '';
  return g + ' issue' + (g === 1 ? '' : 's') + ' · ' + t + ' target' + (t === 1 ? '' : 's');
}

// issueHost reads the host out of an issue target. Built-in checks write
// "METHOD host/path"; a custom check may write a URL or a bare host. It matches
// issueHost in internal/control/scanner_issues_query.go.
export function issueHost(target) {
  let t = String(target || '').trim();
  const sp = t.indexOf(' ');
  if (sp >= 0 && !t.slice(0, sp).includes('/')) t = t.slice(sp + 1).trim();
  if (t.includes('://')) {
    try { const h = new URL(t).hostname; if (h) return h.toLowerCase(); } catch { /* fall through */ }
  }
  const cut = t.search(/[/?# ]/);
  return (cut >= 0 ? t.slice(0, cut) : t).toLowerCase();
}

// hostCounts lists the hosts the issues fired on, most issues first, so the
// results can be narrowed to one target when browser background traffic (or any
// other host outside the engagement) fills the list.
export function hostCounts(issues) {
  const counts = new Map();
  for (const i of Array.isArray(issues) ? issues : []) {
    const h = issueHost(i && i.target);
    if (h) counts.set(h, (counts.get(h) || 0) + 1);
  }
  return [...counts.entries()].sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0])).map(([host, count]) => ({ host, count }));
}

// issuesForHost keeps one host's issues; '' keeps them all.
export function issuesForHost(issues, host) {
  const list = Array.isArray(issues) ? issues : [];
  return host ? list.filter((i) => issueHost(i && i.target) === host) : list;
}

// groupHostLabel is the list row's second line: how many targets, and on how
// many hosts when it is more than one.
export function groupHostLabel(group) {
  const items = (group && group.items) || [];
  const hosts = new Set(items.map((i) => issueHost(i && i.target)).filter(Boolean)).size;
  return items.length + ' target' + (items.length === 1 ? '' : 's') + (hosts > 1 ? ' · ' + hosts + ' hosts' : '');
}

// coverageNote explains a scan that stopped at its flow cap: the newest flows
// were scanned and older ones were not, so a clean result is not a clean history.
export function coverageNote(scan) {
  if (!scan || !scan.capped) return '';
  const n = Number(scan.flows) || 0;
  return 'Scanned the newest ' + n.toLocaleString('en-US') + ' matching flows; older traffic was not scanned. Pick a host or a path filter to reach it.';
}

// scopeLine describes what a scan covers. The server treats "no include rule"
// as everything in scope, so an empty scope does not disable Run; only the
// absence of any scannable traffic does.
export function scopeLine({ inCount = 0, hostCount = 0 } = {}) {
  const rules = Number(inCount) || 0;
  const hosts = Number(hostCount) || 0;
  if (rules > 0) return 'Scope: ' + hosts + ' in-scope host' + (hosts === 1 ? '' : 's') + ' (' + rules + ' include rule' + (rules === 1 ? '' : 's') + ')';
  return 'Scope: no include rule set, so all ' + hosts + ' captured host' + (hosts === 1 ? ' is' : 's are') + ' in scope';
}

export function runGate({ hostCount = 0, hostsLoaded = false } = {}) {
  if (hostsLoaded && Number(hostCount) === 0) return { disabled: true, reason: 'Nothing to scan: no in-scope traffic has been captured yet.' };
  return { disabled: false, reason: '' };
}

// promoteBody is the POST /api/findings body for "Promote to finding": every
// distinct PoC flow is attached through the existing flowIds field.
export function promoteBody(group) {
  const first = (group.items && group.items[0]) || {};
  const flowIds = [];
  for (const i of group.items || []) if (i.flowId && !flowIds.includes(i.flowId)) flowIds.push(i.flowId);
  return {
    title: group.title,
    severity: group.severity,
    source: 'scanner',
    detail: first.detail || '',
    evidence: first.evidence || '',
    fix: first.fix || '',
    flowIds,
  };
}

// verifyTarget picks the flow "Verify in Repeater" preloads: the issue's own flow.
export function verifyTarget(issue) {
  const id = Number(issue && issue.flowId);
  return Number.isSafeInteger(id) && id > 0 ? { id } : null;
}

export function unreviewedCount(groups, seen) {
  const s = seen instanceof Set ? seen : new Set(seen || []);
  return (Array.isArray(groups) ? groups : []).filter((g) => !s.has(g.title)).length;
}
