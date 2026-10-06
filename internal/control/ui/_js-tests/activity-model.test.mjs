import test from 'node:test';
import assert from 'node:assert/strict';
import { ACTIVITY_CATEGORIES, activityCategory, filterActivity, categoryCounts, dayKey, dayLabel, groupByDay, shouldDeferRender, pillLabel, activityAppendix, activityTarget } from '../js/activity-model.js';

const at = (y, mo, d, h = 12, mi = 0) => new Date(y, mo - 1, d, h, mi, 0).getTime();

test('categories are the documented five', () => {
  assert.deepEqual(ACTIVITY_CATEGORIES.map((c) => c.id), ['proxy', 'agent', 'findings', 'scope', 'settings']);
});

test('tools map to a category and unknown tools fall back to agent', () => {
  assert.equal(activityCategory('flows'), 'proxy');
  assert.equal(activityCategory('flow_as_curl'), 'proxy');
  assert.equal(activityCategory('finding_create'), 'findings');
  assert.equal(activityCategory('scope_from_url'), 'scope');
  assert.equal(activityCategory('settings_get'), 'settings');
  assert.equal(activityCategory('something_new'), 'agent');
  assert.equal(activityCategory(''), 'agent');
  assert.equal(activityCategory(undefined), 'agent');
});

test('filterActivity combines categories and the intent substring', () => {
  const items = [
    { id: 1, tool: 'flows', intent: 'Check login' },
    { id: 2, tool: 'finding_create', intent: 'Record IDOR' },
    { id: 3, tool: 'scope', intent: '' },
  ];
  assert.equal(filterActivity(items, {}).length, 3);
  assert.deepEqual(filterActivity(items, { cats: ['findings', 'scope'] }).map((i) => i.id), [2, 3]);
  assert.deepEqual(filterActivity(items, { cats: new Set(['proxy']), intent: 'login' }).map((i) => i.id), [1]);
  assert.deepEqual(filterActivity(items, { intent: 'zzz' }), []);
});

test('categoryCounts counts every category including zeros', () => {
  const c = categoryCounts([{ tool: 'flows' }, { tool: 'flows' }, { tool: 'scope' }]);
  assert.deepEqual(c, { proxy: 2, agent: 0, findings: 0, scope: 1, settings: 0 });
});

test('dayKey and dayLabel use local days', () => {
  const now = at(2026, 10, 6);
  assert.equal(dayKey(at(2026, 10, 6, 1)), '2026-10-06');
  assert.equal(dayLabel('2026-10-06', now), 'Today');
  assert.equal(dayLabel('2026-10-05', now), 'Yesterday');
  assert.equal(dayLabel('2026-09-30', now), '2026-09-30');
});

test('groupByDay keeps order and starts a group when the day changes', () => {
  const items = [{ ts: at(2026, 10, 6, 9) }, { ts: at(2026, 10, 6, 8) }, { ts: at(2026, 10, 5, 23) }];
  const g = groupByDay(items);
  assert.equal(g.length, 2);
  assert.equal(g[0].key, '2026-10-06');
  assert.equal(g[0].items.length, 2);
  assert.equal(g[1].items.length, 1);
  assert.deepEqual(groupByDay([]), []);
});

test('live tail defers rendering only when scrolled away from the newest row', () => {
  assert.equal(shouldDeferRender(0), false);
  assert.equal(shouldDeferRender(48), false);
  assert.equal(shouldDeferRender(49), true);
  assert.equal(shouldDeferRender(undefined), false);
  assert.equal(pillLabel(3), '3 new');
  assert.equal(pillLabel(1), '1 new');
  assert.equal(pillLabel(0), '');
});

test('appendix is deterministic Markdown with escaped cells', () => {
  const items = [
    { ts: Date.UTC(2026, 9, 6, 12, 4, 5), tool: 'flows', ok: true, summary: 'host=example.com | x', intent: 'Look', ms: 12 },
    { ts: Date.UTC(2026, 9, 6, 12, 5, 0), tool: 'scope', ok: false, summary: 'line1\nline2', ms: null },
  ];
  const md = activityAppendix(items);
  assert.match(md, /^## Agent activity appendix/);
  assert.match(md, /2 actions/);
  assert.match(md, /\| 2026-10-06 12:04:05Z \| AI \| flows \| Success \| host=example.com \\\| x \| Look \|/);
  assert.match(md, /\| Error \| line1 line2 \|/);
  assert.equal(activityAppendix([]).includes('No activity'), true);
});

test('appendix caps rows and says so', () => {
  const items = Array.from({ length: 5 }, (_, i) => ({ ts: i + 1, tool: 'flows', ok: true, summary: String(i) }));
  const md = activityAppendix(items, { max: 2 });
  assert.match(md, /showing the first 2 of 5/);
});

test('activityTarget finds a flow id only when one is stated', () => {
  assert.deepEqual(activityTarget({ result: 'ok: flow #42 captured' }), { kind: 'flow', id: 42 });
  assert.deepEqual(activityTarget({ summary: 'Flow #7' }), { kind: 'flow', id: 7 });
  assert.equal(activityTarget({ summary: 'nothing' }), null);
});
