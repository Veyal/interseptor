import test from 'node:test';
import assert from 'node:assert/strict';
import {
  parseQuery, groupOfCommand, scoreCommand, rankCommands, dedupeCommands, rankFlows, rankFindings, rankIdentities,
  flattenCheatsheet, rankShortcuts, pushRecent, loadRecents, saveRecents, RECENTS_MAX, RECENTS_KEY, MAX_ROWS,
  buildResults, flattenResults, resultCountText, findingSub, LEGACY_MODALS,
} from '../js/cmdk-logic.js';

const cmds = [
  { t: 'Go to Proxy', kw: 'history flows traffic' },
  { t: 'Go to Findings', kw: 'findings poc' },
  { t: 'Go to Repeater', kw: 'resend craft' },
  { t: 'New finding', kw: 'create record' },
  { t: 'Export findings', kw: 'report download' },
  { t: 'Settings: Target scope', kw: 'include exclude host' },
  { t: 'Shortcuts', kw: 'help keys' },
];
const flows = [
  { id: 1, method: 'GET', host: 'app.example.com', path: '/login', status: 200 },
  { id: 285, method: 'POST', host: 'api.example.com', path: '/v1/orders', status: 201 },
  { id: 3, method: 'GET', host: 'app.example.com', path: '/orders/9', status: 404 },
];
const findings = [
  { id: 4, title: 'IDOR on orders', readiness: { stage: 'draft', gaps: ['proof', 'cvss'] } },
  { id: 5, title: 'Reflected XSS in search', readiness: { stage: 'report_ready', gaps: [] } },
];

test('parseQuery: prefix modes and the default', () => {
  assert.deepEqual(parseQuery('> send'), { mode: 'actions', prefix: '>', query: 'send' });
  assert.deepEqual(parseQuery('f:orders'), { mode: 'flows', prefix: 'f:', query: 'orders' });
  assert.deepEqual(parseQuery('F: orders'), { mode: 'flows', prefix: 'f:', query: 'orders' });
  assert.deepEqual(parseQuery('#idor'), { mode: 'findings', prefix: '#', query: 'idor' });
  assert.deepEqual(parseQuery('@admin'), { mode: 'identity', prefix: '@', query: 'admin' });
  assert.deepEqual(parseQuery('?'), { mode: 'shortcuts', prefix: '?', query: '' });
  assert.deepEqual(parseQuery('  proxy '), { mode: 'all', prefix: '', query: 'proxy' });
  assert.deepEqual(parseQuery(null), { mode: 'all', prefix: '', query: '' });
  assert.equal(parseQuery('foo f:bar').mode, 'all');
});

test('groupOfCommand: derived from the title unless set', () => {
  assert.equal(groupOfCommand({ t: 'Go to Proxy' }), 'Go to');
  assert.equal(groupOfCommand({ t: 'Settings: TLS' }), 'Settings');
  assert.equal(groupOfCommand({ t: 'Shortcuts' }), 'Shortcuts');
  assert.equal(groupOfCommand({ t: 'New finding' }), 'Actions');
  assert.equal(groupOfCommand({ t: 'X', group: 'Dialogs' }), 'Dialogs');
});

test('scoreCommand keeps every legacy substring match matching', () => {
  // legacy: (title + kw).includes(q)
  for (const c of cmds) for (const q of ['history', 'flows tr', 'proxy hi', 'poc', 'target scope', 'help']) {
    const legacy = (c.t + ' ' + c.kw).toLowerCase().includes(q);
    if (legacy) assert.ok(scoreCommand(q, c) >= 0, `${q} should still match ${c.t}`);
  }
  assert.equal(scoreCommand('zzzz', cmds[0]), -1);
  assert.equal(scoreCommand('', cmds[0]), 0);
});

test('rankCommands: title hits outrank keyword-only hits; empty query keeps order', () => {
  const r = rankCommands('proxy', cmds).map((x) => x.cmd.t);
  assert.equal(r[0], 'Go to Proxy');
  const kw = rankCommands('poc', cmds).map((x) => x.cmd.t);
  assert.deepEqual(kw, ['Go to Findings']);
  assert.deepEqual(rankCommands('', cmds).map((x) => x.cmd.t), cmds.map((c) => c.t));
  const fz = rankCommands('gtf', cmds).map((x) => x.cmd.t);
  assert.equal(fz[0], 'Go to Findings');
});

test('dedupeCommands: first title wins and bad entries are dropped', () => {
  const out = dedupeCommands([{ t: 'A', n: 1 }], [{ t: 'A', n: 2 }, { t: 'B' }, null, { t: 5 }]);
  assert.deepEqual(out.map((c) => c.t), ['A', 'B']);
  assert.equal(out[0].n, 1);
});

test('rankFlows: id lookups (285, #285, id:285) come first; fuzzy otherwise', () => {
  for (const q of ['285', '#285', 'id:285']) assert.equal(rankFlows(q, flows)[0].id, 285);
  assert.deepEqual(rankFlows('orders', flows).map((f) => f.id).sort(), [285, 3]);
  assert.equal(rankFlows('post api', flows)[0].id, 285);
  assert.equal(rankFlows('', flows, { limit: 2 }).length, 2);
  assert.deepEqual(rankFlows('zzzz', flows), []);
});

test('rankFindings and findingSub display server state only', () => {
  assert.equal(rankFindings('idor', findings)[0].id, 4);
  assert.equal(rankFindings('F-5', findings)[0].id, 5);
  assert.equal(rankFindings('5', findings)[0].id, 5);
  assert.equal(findingSub(findings[0]), 'draft · 2 to fix');
  assert.equal(findingSub(findings[1]), 'report ready');
  assert.equal(findingSub({ id: 9, ready: true }), 'ready');
  assert.equal(findingSub({ id: 9 }), '');
});

test('rankIdentities and rankShortcuts', () => {
  const ids = [{ name: 'admin' }, { name: 'user-b' }, { name: '' }, null];
  assert.deepEqual(rankIdentities('', ids).map((i) => i.name), ['admin', 'user-b']);
  assert.deepEqual(rankIdentities('usr', ids).map((i) => i.name), ['user-b']);
  const rows = flattenCheatsheet([{ group: 'Nav', items: [{ keys: 'g p', label: 'Go to Proxy', scope: 'global' }, { keys: 'x', label: 'Toggle selection', scope: 'proxy' }] }]);
  assert.equal(rows.length, 2);
  assert.equal(rankShortcuts('toggle', rows)[0].keys, 'x');
  assert.equal(rankShortcuts('', rows).length, 2);
});

test('recents: most recent first, deduped, capped, storage failures are safe', () => {
  let l = [];
  for (let i = 0; i < RECENTS_MAX + 5; i++) l = pushRecent(l, 'cmd' + i);
  assert.equal(l.length, RECENTS_MAX);
  assert.equal(l[0], 'cmd' + (RECENTS_MAX + 4));
  l = pushRecent(l, 'cmd10');
  assert.equal(l[0], 'cmd10');
  assert.equal(l.filter((t) => t === 'cmd10').length, 1);
  assert.deepEqual(pushRecent(['a'], ''), ['a']);
  const store = new Map();
  const storage = { getItem: (k) => store.get(k) ?? null, setItem: (k, v) => store.set(k, v) };
  assert.equal(saveRecents(['a', 'b'], storage), true);
  assert.ok(store.has(RECENTS_KEY));
  assert.deepEqual(loadRecents(storage), ['a', 'b']);
  assert.deepEqual(loadRecents({ getItem() { throw new Error('blocked'); } }), []);
  assert.equal(saveRecents(['a'], { setItem() { throw new Error('blocked'); } }), false);
  assert.deepEqual(loadRecents({ getItem: () => '{not json' }), []);
  assert.deepEqual(loadRecents({ getItem: () => '{"a":1}' }), []);
});

test('buildResults default mode: empty query lists Recent then groups without duplicates', () => {
  const res = buildResults({ raw: '', commands: cmds, recents: ['New finding', 'Gone'] });
  assert.equal(res.groups[0].group, 'Recent');
  assert.deepEqual(res.groups[0].items.map((i) => i.label), ['New finding']);
  const labels = flattenResults(res).map((i) => i.label);
  assert.equal(new Set(labels).size, labels.length);
  assert.deepEqual(res.groups.map((g) => g.group), ['Recent', 'Go to', 'Actions', 'Settings', 'Shortcuts']);
});

test('buildResults default mode: query mixes commands, flows and findings', () => {
  const res = buildResults({ raw: 'orders', commands: cmds, flows, findings });
  const groups = res.groups.map((g) => g.group);
  assert.ok(groups.includes('Flows') && groups.includes('Findings'));
  assert.equal(res.groups.find((g) => g.group === 'Flows').items[0].kind, 'flow');
  assert.match(res.groups.find((g) => g.group === 'Findings').items[0].label, /^F-4 /);
  const top = buildResults({ raw: 'proxy', commands: cmds, flows: [], findings: [] });
  assert.equal(top.groups[0].items[0].label, 'Go to Proxy');
});

test('buildResults prefix modes', () => {
  assert.deepEqual(buildResults({ raw: 'f:orders', commands: cmds, flows, findings }).groups.map((g) => g.group), ['Flows']);
  assert.deepEqual(buildResults({ raw: '#xss', commands: cmds, flows, findings }).groups.map((g) => g.group), ['Findings']);
  const idn = buildResults({ raw: '@', identities: [{ name: 'admin' }, { name: 'user-b' }], activeIdentity: 'admin' });
  assert.equal(idn.groups[0].items[0].sub, 'active');
  const sel = buildResults({ raw: '> copy', selectionActions: [{ t: 'Copy as curl', kw: '' }, { t: 'Send to Repeater', kw: '' }] });
  assert.deepEqual(sel.groups[0].items.map((i) => i.label), ['Copy as curl']);
  const keys = buildResults({ raw: '?', shortcuts: [{ group: 'Nav', keys: 'g p', label: 'Go to Proxy' }] });
  assert.equal(keys.groups[0].items[0].kind, 'shortcut');
  assert.equal(buildResults({ raw: '#', findings: [] }).total, 0);
});

test('buildResults caps rows at MAX_ROWS across groups', () => {
  const many = Array.from({ length: 200 }, (_, i) => ({ t: 'Action ' + i, kw: '' }));
  const manyFlows = Array.from({ length: 200 }, (_, i) => ({ id: i + 1, method: 'GET', host: 'example.com', path: '/a' + i }));
  assert.ok(buildResults({ raw: '', commands: many }).total <= MAX_ROWS);
  assert.ok(buildResults({ raw: 'f:', commands: many, flows: manyFlows }).total <= MAX_ROWS);
  const mixed = buildResults({ raw: 'a', commands: many, flows: manyFlows, findings });
  assert.ok(mixed.total <= MAX_ROWS);
});

test('resultCountText', () => {
  assert.equal(resultCountText(0, 'all'), 'No matches');
  assert.equal(resultCountText(1, 'all'), '1 result');
  assert.equal(resultCountText(3, 'flows'), '3 flows');
  assert.equal(resultCountText(2, 'findings'), '2 findings');
});

test('LEGACY_MODALS: unique ids and every dialog has a way in or a stated reason', () => {
  const ids = LEGACY_MODALS.map((m) => m.id);
  assert.equal(new Set(ids).size, ids.length);
  for (const m of LEGACY_MODALS) {
    const how = m.how || {};
    assert.ok(how.cmd || how.click || how.open || how.transient, `${m.id} has no route`);
    if (!how.transient) assert.ok(m.title, `${m.id} needs a palette title`);
  }
});
