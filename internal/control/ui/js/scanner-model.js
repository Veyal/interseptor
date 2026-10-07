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

export function toggleSeverity(active, severity) {
  const next = new Set(active || []);
  if (next.has(severity)) next.delete(severity);
  else next.add(severity);
  return next;
}

export function summaryText(groups, issues) {
  const g = Array.isArray(groups) ? groups.length : 0;
  const t = Array.isArray(issues) ? issues.length : 0;
  if (!g) return '';
  return g + ' finding' + (g === 1 ? '' : 's') + ' · ' + t + ' target' + (t === 1 ? '' : 's');
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
