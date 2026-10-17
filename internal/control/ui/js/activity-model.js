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
// MCP tools are verb_noun (list_flows, create_finding, add_scope_rule), so every
// underscore-separated word is checked, not just the first. Rules are ordered:
// the first category with a matching word wins (record_finding_from_flow is a
// finding, add_scope_rule is scope). Anything unrecognised is a generic agent
// action, never dropped.
const RULES = [
  ['findings', /^(findings?|report|evidence|notes?|poc|cvss)$/],
  ['scope', /^scope$/],
  ['settings', /^(settings?|config|project|upstream|device|android|ios|ca|tls|ssl|rules?|match|interception|vault)$/],
  ['proxy', /^(flows?|history|proxy|repeat|repeater|send|replay|intruder|scan|scanner|issues?|intercept|forward|drop|endpoint|map|ws|websocket|oob|tags?|authz|decode|encode|codecs?)$/],
];

export function activityCategory(tool) {
  const words = String(tool || '').toLowerCase().split(/[^a-z0-9]+/).filter(Boolean);
  for (const [id, re] of RULES) if (words.some((w) => re.test(w))) return id;
  return 'agent';
}

// The free-text filter matches the tool name, the summary and the stated intent.
export function filterActivity(items, { cats, intent } = {}) {
  const set = cats ? new Set(cats) : null;
  const q = String(intent || '').trim().toLowerCase();
  return (items || []).filter((it) => {
    if (set && set.size && !set.has(activityCategory(it.tool))) return false;
    if (q && ![it.tool, it.summary, it.intent].some((v) => String(v || '').toLowerCase().includes(q))) return false;
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
