// cmdk-logic.js — pure logic behind the command palette v2: prefix-mode parsing,
// fuzzy ranking, grouping, the recents list and the legacy-dialog table. No DOM
// and no core.js import, so it runs under `node --test` (ui/_js-tests).
//
// The palette NAVIGATES and opens dialogs; the few actions it performs are
// reversible toggles. Findings readiness is displayed as the server reports it
// (stage and gap count); nothing here recomputes pass/fail.
import { fuzzyScore } from './keys.js';

export const MAX_ROWS = 50;
export const RECENTS_KEY = 'interseptor.cmdkRecents.v1';
export const RECENTS_MAX = 20;
export const RECENTS_SHOWN = 5;
export const DEFAULT_LIMITS = { commands: 20, flows: 8, findings: 6 };
export const GROUP_ORDER = ['Recent', 'Selection', 'Go to', 'Actions', 'Flows', 'Findings', 'Identities', 'Dialogs', 'Settings', 'Shortcuts'];

// ---- prefix modes ----
// `>` actions on the current selection, `f:` flows, `#` findings, `@` identity,
// `?` shortcuts. Anything else is the default mixed search.
export function parseQuery(raw) {
  const s = String(raw == null ? '' : raw).replace(/^\s+/, '');
  if (s.startsWith('>')) return { mode: 'actions', prefix: '>', query: s.slice(1).trim() };
  if (s.startsWith('?')) return { mode: 'shortcuts', prefix: '?', query: s.slice(1).trim() };
  if (s.startsWith('#')) return { mode: 'findings', prefix: '#', query: s.slice(1).trim() };
  if (s.startsWith('@')) return { mode: 'identity', prefix: '@', query: s.slice(1).trim() };
  if (/^f:/i.test(s)) return { mode: 'flows', prefix: 'f:', query: s.slice(2).trim() };
  return { mode: 'all', prefix: '', query: s.trim() };
}

export const MODE_LABEL = { all: 'Search', actions: 'Selection actions', flows: 'Flows', findings: 'Findings', identity: 'Identities', shortcuts: 'Shortcuts' };

// ---- commands ----
export function groupOfCommand(c) {
  if (c.group) return c.group;
  const t = String(c.t || '');
  if (/^Go to /.test(t)) return 'Go to';
  if (/^Settings:/.test(t)) return 'Settings';
  if (t === 'Shortcuts') return 'Shortcuts';
  return 'Actions';
}

// scoreCommand: fuzzy on the title; a keyword (or title+keyword substring) hit
// is a low positive score so every legacy substring match still matches.
export function scoreCommand(q, c) {
  const query = String(q || '').trim().toLowerCase();
  if (!query) return 0;
  const title = String(c.t || '');
  const s = fuzzyScore(query, title);
  if (s >= 0) return s + 20;
  return (title + ' ' + String(c.kw || '')).toLowerCase().includes(query) ? 1 : -1;
}

export function rankCommands(q, commands) {
  const scored = [];
  commands.forEach((c, i) => { const s = scoreCommand(q, c); if (s >= 0) scored.push({ c, s, i }); });
  if (String(q || '').trim()) scored.sort((a, b) => b.s - a.s || a.i - b.i);
  return scored.map((x) => ({ cmd: x.c, score: x.s }));
}

// Same title appears once (first wins): registered commands never duplicate the base list.
export function dedupeCommands(...lists) {
  const seen = new Set();
  const out = [];
  for (const list of lists) for (const c of list || []) {
    if (!c || typeof c.t !== 'string' || seen.has(c.t)) continue;
    seen.add(c.t);
    out.push(c);
  }
  return out;
}

// ---- flows / findings / identities / shortcuts ----
export const flowLabel = (f) => `${f.method || '?'}  ${(f.host || '') + (f.path || '')}`;

export function rankFlows(q, flows, { limit = DEFAULT_LIMITS.flows } = {}) {
  const query = String(q || '').trim();
  const list = Array.isArray(flows) ? flows : [];
  if (!query) return list.slice(0, limit);
  const idMatch = /^(?:id:|#)?(\d+)$/i.exec(query);
  const idWant = idMatch ? Number(idMatch[1]) : 0;
  const scored = [];
  list.forEach((f, i) => {
    if (!f) return;
    if (idWant && f.id === idWant) { scored.push({ f, s: 1e6, i }); return; }
    const s = fuzzyScore(query.replace(/^id:/i, ''), `${f.method || ''} ${f.host || ''}${f.path || ''} #${f.id}`);
    if (s >= 0) scored.push({ f, s, i });
  });
  scored.sort((a, b) => b.s - a.s || a.i - b.i);
  return scored.slice(0, limit).map((x) => x.f);
}

export function findingSub(f) {
  const r = f && f.readiness;
  if (!r) return f && f.ready ? 'ready' : '';
  const gaps = Array.isArray(r.gaps) ? r.gaps.length : 0;
  const stage = String(r.stage || '').replace(/_/g, ' ');
  return gaps ? `${stage} · ${gaps} to fix` : (stage || 'ready');
}

export function rankFindings(q, findings, { limit = DEFAULT_LIMITS.findings } = {}) {
  const query = String(q || '').trim();
  const list = Array.isArray(findings) ? findings : [];
  if (!query) return list.slice(0, limit);
  const idMatch = /^(?:f-?)?(\d+)$/i.exec(query);
  const idWant = idMatch ? Number(idMatch[1]) : 0;
  const scored = [];
  list.forEach((f, i) => {
    if (!f) return;
    if (idWant && f.id === idWant) { scored.push({ f, s: 1e6, i }); return; }
    const s = fuzzyScore(query, `F-${f.id} ${f.title || ''} ${f.target || ''}`);
    if (s >= 0) scored.push({ f, s, i });
  });
  scored.sort((a, b) => b.s - a.s || a.i - b.i);
  return scored.slice(0, limit).map((x) => x.f);
}

export function rankIdentities(q, identities) {
  const list = (Array.isArray(identities) ? identities : []).filter((i) => i && i.name);
  const query = String(q || '').trim();
  if (!query) return list;
  return list.map((it, i) => ({ it, i, s: fuzzyScore(query, it.name) })).filter((x) => x.s >= 0).sort((a, b) => b.s - a.s || a.i - b.i).map((x) => x.it);
}

// flattenCheatsheet turns keys.js cheatsheet() groups into searchable rows.
export function flattenCheatsheet(sheet) {
  const rows = [];
  for (const g of sheet || []) for (const it of g.items || []) rows.push({ group: g.group, keys: it.keys, label: it.label, scope: it.scope });
  return rows;
}
export function rankShortcuts(q, rows) {
  const query = String(q || '').trim();
  if (!query) return rows;
  return rows.map((r, i) => ({ r, i, s: Math.max(fuzzyScore(query, r.label), fuzzyScore(query, r.keys), fuzzyScore(query, r.group)) })).filter((x) => x.s >= 0).sort((a, b) => b.s - a.s || a.i - b.i).map((x) => x.r);
}

// ---- recents (localStorage, always inside try/catch) ----
export function pushRecent(list, title, max = RECENTS_MAX) {
  if (typeof title !== 'string' || !title) return list.slice(0, max);
  return [title, ...list.filter((t) => t !== title)].slice(0, max);
}
export function loadRecents(storage) {
  try {
    const raw = (storage || globalThis.localStorage).getItem(RECENTS_KEY);
    const arr = raw ? JSON.parse(raw) : [];
    return Array.isArray(arr) ? arr.filter((t) => typeof t === 'string').slice(0, RECENTS_MAX) : [];
  } catch (e) { return []; }
}
export function saveRecents(list, storage) {
  try { (storage || globalThis.localStorage).setItem(RECENTS_KEY, JSON.stringify(list.slice(0, RECENTS_MAX))); return true; } catch (e) { return false; }
}

// ---- result assembly ----
const cmdItem = (c, group) => ({ key: 'c:' + c.t, kind: 'command', group, label: c.t, sub: c.sub || group, ref: c });

// buildResults returns {mode, query, groups:[{group, items}], total}. Rows are
// capped at MAX_ROWS across groups; the first group is never starved.
export function buildResults(input) {
  const { mode, query } = parseQuery(input.raw);
  const groups = [];
  const push = (group, items) => { if (items.length) groups.push({ group, items }); };

  if (mode === 'all') {
    const commands = dedupeCommands(input.commands);
    if (!query) {
      const byTitle = new Map(commands.map((c) => [c.t, c]));
      const recent = (input.recents || []).map((t) => byTitle.get(t)).filter(Boolean).slice(0, RECENTS_SHOWN);
      push('Recent', recent.map((c) => cmdItem(c, 'Recent')));
      const rest = commands.filter((c) => !recent.includes(c));
      const by = new Map();
      for (const c of rest) { const g = groupOfCommand(c); if (!by.has(g)) by.set(g, []); by.get(g).push(c); }
      for (const g of GROUP_ORDER) if (by.has(g)) push(g, by.get(g).map((c) => cmdItem(c, g)));
      for (const [g, list] of by) if (!GROUP_ORDER.includes(g)) push(g, list.map((c) => cmdItem(c, g)));
    } else {
      const ranked = rankCommands(query, commands).slice(0, DEFAULT_LIMITS.commands);
      const by = new Map();
      for (const r of ranked) {
        const g = groupOfCommand(r.cmd);
        if (!by.has(g)) by.set(g, { best: r.score, items: [] });
        by.get(g).items.push(cmdItem(r.cmd, g));
      }
      const order = (g) => { const i = GROUP_ORDER.indexOf(g); return i < 0 ? GROUP_ORDER.length : i; };
      [...by.entries()].sort((a, b) => b[1].best - a[1].best || order(a[0]) - order(b[0])).forEach(([g, v]) => push(g, v.items));
      push('Flows', rankFlows(query, input.flows).map((f) => ({ key: 'f:' + f.id, kind: 'flow', group: 'Flows', label: flowLabel(f), sub: String(f.status || '—'), ref: f })));
      push('Findings', rankFindings(query, input.findings).map((f) => ({ key: 'g:' + f.id, kind: 'finding', group: 'Findings', label: `F-${f.id}  ${f.title || 'Untitled finding'}`, sub: findingSub(f), ref: f })));
    }
  } else if (mode === 'actions') {
    const ranked = rankCommands(query, input.selectionActions || []);
    push('Selection', ranked.map((r) => cmdItem(r.cmd, 'Selection')));
  } else if (mode === 'flows') {
    push('Flows', rankFlows(query, input.flows, { limit: 20 }).map((f) => ({ key: 'f:' + f.id, kind: 'flow', group: 'Flows', label: flowLabel(f), sub: String(f.status || '—'), ref: f })));
  } else if (mode === 'findings') {
    push('Findings', rankFindings(query, input.findings, { limit: 20 }).map((f) => ({ key: 'g:' + f.id, kind: 'finding', group: 'Findings', label: `F-${f.id}  ${f.title || 'Untitled finding'}`, sub: findingSub(f), ref: f })));
  } else if (mode === 'identity') {
    const active = input.activeIdentity || '';
    push('Identities', rankIdentities(query, input.identities).map((i) => ({ key: 'i:' + i.name, kind: 'identity', group: 'Identities', label: i.name, sub: i.name === active ? 'active' : (i.broken ? 'broken' : 'identity'), ref: i })));
  } else if (mode === 'shortcuts') {
    push('Shortcuts', rankShortcuts(query, input.shortcuts || []).map((r, n) => ({ key: 's:' + n + r.keys, kind: 'shortcut', group: 'Shortcuts', label: r.label, sub: r.keys, ref: r })));
  }

  let left = MAX_ROWS;
  const capped = [];
  for (const g of groups) {
    if (left <= 0) break;
    const items = g.items.slice(0, left);
    left -= items.length;
    capped.push({ group: g.group, items });
  }
  return { mode, query, groups: capped, total: capped.reduce((n, g) => n + g.items.length, 0) };
}

export const flattenResults = (res) => res.groups.flatMap((g) => g.items);

export function resultCountText(total, mode) {
  if (!total) return 'No matches';
  const noun = mode === 'flows' ? 'flow' : mode === 'findings' ? 'finding' : mode === 'identity' ? 'identity' : mode === 'shortcuts' ? 'shortcut' : 'result';
  return `${total} ${noun}${total === 1 ? '' : 's'}`;
}

// Hint line under the field: what the active mode does and how to leave it.
export function modeHint(mode, { hasSelection = false } = {}) {
  if (mode === 'actions') return hasSelection ? 'Actions on the selected flows' : 'Select flows in History first';
  if (mode === 'flows') return 'Enter opens the flow, Shift+Enter sends it to Repeater';
  if (mode === 'findings') return 'Enter opens the finding';
  if (mode === 'identity') return 'Enter sets the active identity';
  if (mode === 'shortcuts') return 'Enter opens the full shortcut sheet';
  return '';
}

// ---- legacy dialogs ----
// Every id in core.js MODAL_IDS (the first literal block) is listed so a dialog
// that is being replaced stays reachable from the palette until its cleanup
// commit. `how`: {cmd: base command title already offered} | {click: selector,
// tab} | {open: 'shortcuts'|'selected-flow'|'authz'|'session'|'auth-timeline'}
// | {transient: reason} for dialogs that only exist while another action runs.
export const LEGACY_MODALS = [
  { id: 'flowModal', title: 'Open selected flow in the inspector dialog', kw: 'flow inspector popup raw request response', how: { open: 'selected-flow' } },
  { id: 'shortcutsModal', title: 'Keyboard shortcut sheet', kw: 'help cheatsheet keys hotkeys', how: { open: 'shortcuts' } },
  { id: 'checksModal', title: 'Edit custom scanner checks', kw: 'starlark checks passive custom rules', how: { cmd: 'Edit custom scanner checks' } },
  { id: 'codecsModal', title: 'Edit message codecs', kw: 'encrypt decrypt aes codec', how: { cmd: 'Edit message codecs' } },
  { id: 'oobModal', title: 'Open OOB catcher', kw: 'out of band blind ssrf callback', how: { cmd: 'Open OOB catcher' } },
  { id: 'projModal', title: 'Switch or create project', kw: 'projects workspace engagement', how: { cmd: 'Switch or create project' } },
  { id: 'authzModal', title: 'Open Authz test', kw: 'authorization access control roles', how: { cmd: 'Open Authz test' } },
  { id: 'findGuideModal', title: 'Findings guide (legacy dialog)', kw: 'findings guide help writing', how: { click: '#findGuide', tab: 'findings' } },
  { id: 'findCreateModal', title: 'Create finding dialog (legacy)', kw: 'new finding create record', how: { click: '#findNew', tab: 'findings' } },
  { id: 'findPickModal', title: 'Pick a finding for the selected flow (legacy dialog)', kw: 'add attach evidence finding picker', how: { cmd: 'Add selected flow to finding' } },
  { id: 'findFlowPickModal', title: 'Attach flows to the open finding (legacy dialog)', kw: 'attach flow picker proof evidence', how: { click: '#findAddFlow', tab: 'findings' } },
  { id: 'findExportModal', title: 'Export findings dialog (legacy)', kw: 'export report markdown html json', how: { click: '#findExportOpen', tab: 'findings' } },
  { id: 'findDeletedModal', title: 'Deleted findings dialog (legacy)', kw: 'deleted findings restore trash', how: { click: '#findDeletedOpen', tab: 'findings' } },
  { id: 'sessionInspectModal', title: 'Inspect session timeline of selected flows', kw: 'session inspection timeline roles', how: { open: 'session' } },
  { id: 'authTimelineModal', title: 'Auth timeline from selected flow', kw: 'authentication timeline login token', how: { open: 'auth-timeline' } },
  { id: 'compareModal', title: 'Compare selected flows (diff)', kw: 'compare diff two flows', how: { cmd: 'Compare selected flows (diff)' } },
  { id: 'decModal', title: 'Open Decoder (base64 / url / jwt / hex…)', kw: 'encode decode smart', how: { cmd: 'Open Decoder (base64 / url / jwt / hex…)' } },
  { id: 'confirmModal', how: { transient: 'confirmation prompt raised by another action' } },
  { id: 'promptModal', how: { transient: 'text prompt raised by another action' } },
  { id: 'setupModal', title: 'Run setup wizard', kw: 'setup wizard onboarding', how: { cmd: 'Run setup wizard' } },
  { id: 'imgLightbox', how: { transient: 'image zoom opened from an evidence thumbnail' } },
];
