// activity-model.js — pure view-model for the Activity timeline: categories,
// filtering, day grouping, the live-tail rule and the Markdown report appendix.
// No imports and no DOM access, so it runs under node.

export const ACTIVITY_CATEGORIES = [
  { id: 'proxy', label: 'Proxy' },
  { id: 'agent', label: 'Agent/MCP' },
  { id: 'findings', label: 'Findings' },
  { id: 'scope', label: 'Scope' },
  { id: 'settings', label: 'Settings' },
];

// Activity rows are MCP tool calls, so the category is derived from the tool name.
// Anything unrecognised is a generic agent action, never dropped.
const RULES = [
  ['findings', /^(finding|report|evidence|note)/],
  ['scope', /^scope/],
  ['settings', /^(setting|config|project|upstream|device|ca_|tls|rule|match_replace)/],
  ['proxy', /^(flow|history|proxy|repeat|send|replay|intruder|scan|intercept|endpoint|map|ws_|websocket|oob|tag|authz|decode|encode)/],
];

export function activityCategory(tool) {
  const t = String(tool || '').toLowerCase();
  for (const [id, re] of RULES) if (re.test(t)) return id;
  return 'agent';
}

export function filterActivity(items, { cats, intent } = {}) {
  const set = cats ? new Set(cats) : null;
  const q = String(intent || '').trim().toLowerCase();
  return (items || []).filter((it) => {
    if (set && set.size && !set.has(activityCategory(it.tool))) return false;
    if (q && !String(it.intent || '').toLowerCase().includes(q)) return false;
    return true;
  });
}

export function categoryCounts(items) {
  const out = {};
  ACTIVITY_CATEGORIES.forEach((c) => { out[c.id] = 0; });
  (items || []).forEach((it) => { out[activityCategory(it.tool)]++; });
  return out;
}

const p2 = (n) => String(n).padStart(2, '0');
export function dayKey(ts) {
  const d = new Date(ts);
  return d.getFullYear() + '-' + p2(d.getMonth() + 1) + '-' + p2(d.getDate());
}
export function dayLabel(key, now = Date.now()) {
  if (key === dayKey(now)) return 'Today';
  const y = new Date(now);
  y.setDate(y.getDate() - 1);
  return key === dayKey(y.getTime()) ? 'Yesterday' : key;
}

// groupByDay keeps feed order and opens a new group whenever the local day changes.
export function groupByDay(items) {
  const groups = [];
  (items || []).forEach((it) => {
    const key = dayKey(it.ts || 0);
    const last = groups[groups.length - 1];
    if (last && last.key === key) last.items.push(it);
    else groups.push({ key, items: [it] });
  });
  return groups;
}

// The feed is newest-first: while the reader is scrolled below the top, new rows
// are counted into a pill instead of shifting the content under them.
export const DEFER_THRESHOLD = 48;
export const shouldDeferRender = (scrollTop) => Number(scrollTop) > DEFER_THRESHOLD;
export const pillLabel = (n) => (n > 0 ? n + ' new' : '');

export function activityTarget(it) {
  const m = String((it && (it.result || it.summary)) || '').match(/flow #(\d+)/i);
  return m ? { kind: 'flow', id: Number(m[1]) } : null;
}
// The first matching text is the result; fall back to the summary like the feed does.
export function activityTargetOf(it) {
  return activityTarget({ result: it && it.result }) || activityTarget({ summary: it && it.summary });
}

const cell = (s) => String(s == null ? '' : s).replace(/\s*\n\s*/g, ' ').replace(/\|/g, '\\|').trim();
const stamp = (ts) => new Date(ts).toISOString().replace('T', ' ').replace(/\.\d+Z$/, 'Z');

export function activityAppendix(items, { max = 300 } = {}) {
  const list = items || [];
  if (!list.length) return '## Agent activity appendix\n\nNo activity recorded.\n';
  const shown = list.slice(0, max);
  const head = '## Agent activity appendix\n\n' + list.length + (list.length === 1 ? ' action' : ' actions') +
    (shown.length < list.length ? ' (showing the first ' + shown.length + ' of ' + list.length + ')' : '') + '.\n\n';
  const rows = shown.map((it) => '| ' + [stamp(it.ts || 0), 'AI', cell(it.tool), it.ok ? 'Success' : 'Error', cell(it.summary), cell(it.intent)].join(' | ') + ' |');
  return head + '| Time (UTC) | Actor | Tool | Outcome | Summary | Intent |\n| --- | --- | --- | --- | --- | --- |\n' + rows.join('\n') + '\n';
}
