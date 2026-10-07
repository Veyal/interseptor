import test from 'node:test';
import assert from 'node:assert/strict';
import {
  rankBetween, buildTree, filterTree, flattenVisible, treeKey, planMove, planNudge, descendantUids,
  splitURL, parseQuery, buildURL, syncParamsFromURL, itemToEditor, editorToItem, editorSignature,
  eventsToScripts, scriptsToEvents, authToEditor, editorToAuth, stepSummary, testCounts, testsLabel,
  decideSend, needsAddHost, hostOf, runSummary, groupReport, scriptReview, trustBody, firstFlowId, commandEntries,
} from '../js/collections-model.js';

const mk = (uid, parentUid, kind, rank, name = uid, extra = {}) => ({ uid, parentUid, kind, rank, name, ...extra });
const ITEMS = [
  mk('f1', '', 'folder', 'a', 'Auth'),
  mk('r1', 'f1', 'request', 'a', 'login', { method: 'POST', url: 'https://example.com/login' }),
  mk('r2', 'f1', 'request', 'b', 'logout', { method: 'POST', url: { raw: 'https://example.com/logout' } }),
  mk('f2', '', 'folder', 'b', 'Users'),
  mk('r3', 'f2', 'request', 'a', 'get user', { method: 'GET', url: 'https://example.com/users/1' }),
  mk('r4', '', 'request', 'c', 'health', { method: 'GET', url: 'https://example.com/health' }),
];

test('rankBetween mirrors the Go fractional index', () => {
  assert.equal(rankBetween('', ''), 'i');
  const m = rankBetween('a', 'b');
  assert.ok(m > 'a' && m < 'b');
  assert.ok(rankBetween('a', '') > 'a');
  assert.ok(rankBetween('', 'a') < 'a');
  assert.ok(rankBetween('b', 'a') > 'b'); // a >= b opens the end
});

test('buildTree orders by rank and nests children', () => {
  const t = buildTree(ITEMS);
  assert.deepEqual(t.map((n) => n.item.uid), ['f1', 'f2', 'r4']);
  assert.deepEqual(t[0].children.map((n) => n.item.uid), ['r1', 'r2']);
});

test('buildTree keeps orphans as roots', () => {
  const t = buildTree([mk('x', 'missing', 'request', 'a')]);
  assert.equal(t.length, 1);
});

test('filterTree keeps matches with their ancestors; folder match keeps subtree', () => {
  const t = buildTree(ITEMS);
  assert.deepEqual(filterTree(t, 'logout').map((n) => [n.item.uid, n.children.map((c) => c.item.uid)]), [['f1', ['r2']]]);
  assert.deepEqual(filterTree(t, 'users')[0].children.map((c) => c.item.uid), ['r3']);
  assert.deepEqual(filterTree(t, 'POST').map((n) => n.item.uid), ['f1']);
  assert.equal(filterTree(t, '').length, 3);
  assert.equal(filterTree(t, 'zzz').length, 0);
});

test('flattenVisible honours expansion and forceOpen', () => {
  const t = buildTree(ITEMS);
  assert.deepEqual(flattenVisible(t, new Set()).map((r) => r.uid), ['f1', 'f2', 'r4']);
  assert.deepEqual(flattenVisible(t, new Set(['f1'])).map((r) => r.uid), ['f1', 'r1', 'r2', 'f2', 'r4']);
  assert.equal(flattenVisible(t, new Set(), { forceOpen: true }).length, 6);
  const rows = flattenVisible(t, new Set(['f1']));
  assert.equal(rows[1].depth, 1);
  assert.equal(rows[1].setsize, 2);
});

test('treeKey follows the ARIA tree pattern', () => {
  const rows = flattenVisible(buildTree(ITEMS), new Set(['f1']));
  assert.deepEqual(treeKey(rows, 'f1', 'ArrowDown'), { focus: 'r1' });
  assert.deepEqual(treeKey(rows, 'f2', 'ArrowRight'), { expand: 'f2' });
  assert.deepEqual(treeKey(rows, 'f1', 'ArrowRight'), { focus: 'r1' });
  assert.deepEqual(treeKey(rows, 'f1', 'ArrowLeft'), { collapse: 'f1' });
  assert.deepEqual(treeKey(rows, 'r1', 'ArrowLeft'), { focus: 'f1' });
  assert.equal(treeKey(rows, 'f1', 'ArrowUp'), null);
  assert.deepEqual(treeKey(rows, 'f1', 'End'), { focus: 'r4' });
});

test('planMove: before/after/into and invalid drops', () => {
  const into = planMove(ITEMS, 'r4', 'f1', 'into');
  assert.equal(into.parentUid, 'f1');
  assert.ok(into.rank > 'b');
  const before = planMove(ITEMS, 'r4', 'r1', 'before');
  assert.equal(before.parentUid, 'f1');
  assert.ok(before.rank < 'a');
  const after = planMove(ITEMS, 'r3', 'r1', 'after');
  assert.equal(after.parentUid, 'f1');
  assert.ok(after.rank > 'a' && after.rank < 'b');
  assert.equal(planMove(ITEMS, 'f1', 'r1', 'into'), null, 'cannot drop a folder into its own subtree');
  assert.equal(planMove(ITEMS, 'r1', 'r1', 'before'), null);
  assert.equal(planMove(ITEMS, 'r1', 'r2', 'into').parentUid, 'f1', 'into a request falls back to after/before its parent');
  assert.deepEqual([...descendantUids(ITEMS, 'f1')].sort(), ['r1', 'r2']);
});

test('planNudge moves one place and stops at the ends', () => {
  const down = planNudge(ITEMS, 'r1', 1);
  assert.ok(down.rank > 'b');
  assert.equal(planNudge(ITEMS, 'r1', -1), null);
  assert.equal(planNudge(ITEMS, 'r2', 1), null);
});

test('URL and params stay in step', () => {
  assert.deepEqual(splitURL('https://e.com/a?x=1&y=2#h'), { base: 'https://e.com/a', query: 'x=1&y=2', hash: '#h' });
  assert.deepEqual(parseQuery('a=1&b=%20x&c'), [
    { key: 'a', value: '1', disabled: false }, { key: 'b', value: ' x', disabled: false }, { key: 'c', value: '', disabled: false }]);
  assert.deepEqual(parseQuery('id={{userId}}')[0].value, '{{userId}}');
  assert.equal(buildURL('https://e.com/a', [{ key: 'a', value: '1 2' }, { key: 'off', value: 'x', disabled: true }, { key: 'v', value: '{{x}}' }]), 'https://e.com/a?a=1%202&v={{x}}');
  const synced = syncParamsFromURL('https://e.com/a?q=1', [{ key: 'old', value: 'v', disabled: true }, { key: 'gone', value: '1' }]);
  assert.deepEqual(synced.map((p) => [p.key, p.disabled]), [['q', false], ['old', true]]);
});

const POSTMAN_ITEM = {
  uid: 'r1', rev: 3, kind: 'request', name: 'login', method: 'POST',
  url: { raw: 'https://example.com/login?a=1', host: ['example', 'com'] },
  headers: [{ key: 'X-A', value: '1' }, { key: 'X-B', value: '2', disabled: true, description: 'off' }],
  body: { mode: 'raw', raw: '{"a":1}', options: { raw: { language: 'json' } } },
  auth: { type: 'bearer', bearer: [{ key: 'token', value: '{{token}}', type: 'string' }] },
  events: [{ listen: 'test', script: { id: 's1', type: 'text/javascript', exec: ['pm.test("x", () => {});'] } }, { listen: 'custom', script: { exec: ['x'] } }],
  sidecar: { keep: true },
};

test('editor round-trips an imported item without dropping fields', () => {
  const ed = itemToEditor(POSTMAN_ITEM);
  assert.equal(ed.method, 'POST');
  assert.equal(ed.url, 'https://example.com/login?a=1');
  assert.deepEqual(ed.params.map((p) => p.key), ['a']);
  assert.equal(ed.headers[1].disabled, true);
  assert.equal(ed.auth.type, 'bearer');
  assert.equal(ed.auth.fields.token, '{{token}}');
  assert.equal(ed.scripts.tests, 'pm.test("x", () => {});');
  const out = editorToItem(ed, POSTMAN_ITEM);
  assert.equal(out.rev, 3);
  assert.deepEqual(out.sidecar, { keep: true });
  assert.equal(out.url.host.length, 2, 'url object fields survive');
  assert.equal(out.events.find((e) => e.listen === 'test').script.id, 's1');
  assert.ok(out.events.find((e) => e.listen === 'custom'));
  assert.deepEqual(out.auth.bearer[0].value, '{{token}}');
  assert.equal(out.headers[1].disabled, true);
  assert.equal(out.headers[1].description, 'off');
  assert.equal(out.body.options.raw.language, 'json');
});

test('editing params rewrites the URL query; disabled params leave it', () => {
  const ed = itemToEditor(POSTMAN_ITEM);
  ed.params.push({ key: 'b', value: '2', disabled: false });
  ed.params[0].disabled = true;
  const out = editorToItem(ed, POSTMAN_ITEM);
  assert.equal(out.url.raw, 'https://example.com/login?b=2');
  assert.equal(out.params.length, 2);
  assert.equal(out.params[0].disabled, true);
});

test('clearing a script removes its event; blank auth inherits', () => {
  const ed = itemToEditor(POSTMAN_ITEM);
  ed.scripts.tests = '  ';
  ed.auth.type = 'inherit';
  const out = editorToItem(ed, POSTMAN_ITEM);
  assert.equal(out.events.some((e) => e.listen === 'test'), false);
  assert.equal('auth' in out, false);
});

test('new request events and auth shapes', () => {
  assert.deepEqual(scriptsToEvents([], { prerequest: 'a\nb', tests: '' }), [{ listen: 'prerequest', script: { type: 'text/javascript', exec: ['a', 'b'] } }]);
  assert.deepEqual(eventsToScripts([{ listen: 'prerequest', script: { exec: 'one' } }]), { prerequest: 'one', tests: '' });
  assert.deepEqual(authToEditor({ type: 'noauth' }), { type: 'none', fields: {} });
  assert.deepEqual(authToEditor({ type: 'basic', basic: { username: 'u', password: 'p' } }), { type: 'basic', fields: { username: 'u', password: 'p' } });
  assert.deepEqual(editorToAuth({ type: 'none', fields: {} }), { type: 'noauth' });
  assert.equal(editorToAuth({ type: 'inherit', fields: {} }), undefined);
  assert.equal(editorToAuth({ type: 'basic', fields: { username: 'u', password: 'p' } }).basic[1].value, 'p');
});

test('editorSignature changes only on edits', () => {
  const ed = itemToEditor(POSTMAN_ITEM);
  const s0 = editorSignature(ed);
  assert.equal(editorSignature(itemToEditor(POSTMAN_ITEM)), s0);
  ed.headers[0].value = '9';
  assert.notEqual(editorSignature(ed), s0);
});

test('stepSummary names blocks, errors and sends', () => {
  assert.equal(stepSummary({ outcome: 'blocked', blockReason: 'unresolved_variables' }).kind, 'blocked');
  assert.match(stepSummary({ outcome: 'blocked', blockReason: 'out_of_scope' }).label, /out of scope/);
  assert.equal(stepSummary({ outcome: 'error', error: 'bad url' }).label, 'bad url');
  assert.equal(stepSummary({ outcome: 'skipped' }).kind, 'skipped');
  assert.deepEqual(stepSummary({ outcome: 'sent', response: { status: 200, statusText: 'OK', size: 5, timeMs: 7 } }),
    { kind: 'sent', label: '200', status: 200, statusText: 'OK', size: 5, timeMs: 7 });
  assert.equal(stepSummary({ outcome: 'sent', response: { status: 502, error: 'dial' } }).kind, 'error');
  assert.equal(needsAddHost({ outcome: 'blocked', blockReason: 'out_of_scope' }), true);
  assert.equal(needsAddHost({ outcome: 'blocked', blockReason: 'base_target_pin' }), false);
});

test('unsupported tests never count as passes', () => {
  const tests = [{ status: 'pass' }, { status: 'unsupported' }, { status: 'fail' }, { status: 'error' }, { status: 'skip' }];
  const c = testCounts(tests);
  assert.deepEqual([c.pass, c.unsupported, c.fail, c.error, c.skip, c.total], [1, 1, 1, 1, 1, 5]);
  assert.match(testsLabel(tests), /1\/5 passed, 2 failed, 1 unsupported/);
  assert.equal(testsLabel([]), 'No tests');
});

test('decideSend', () => {
  assert.equal(decideSend({ url: '', kind: 'request' }).ok, false);
  assert.equal(decideSend({ url: 'x', kind: 'folder' }).ok, false);
  assert.equal(decideSend({ url: 'x', busy: true }).ok, false);
  assert.equal(decideSend({ url: 'https://example.com', kind: 'request' }).ok, true);
  assert.equal(hostOf('https://u:p@example.com:8443/a'), 'example.com:8443');
  assert.equal(hostOf('{{baseUrl}}/a'), '');
});

test('runSummary buckets rows without double counting', () => {
  const rows = [
    { status: 'sent', resultJson: JSON.stringify({ outcome: 'sent', tests: [{ status: 'pass' }] }) },
    { status: 'sent', resultJson: JSON.stringify({ outcome: 'sent', tests: [{ status: 'fail' }, { status: 'pass' }] }) },
    { status: 'blocked', resultJson: JSON.stringify({ outcome: 'blocked' }) },
    { status: 'error', resultJson: 'not json' },
  ];
  const s = runSummary(rows);
  assert.deepEqual([s.requests, s.passed, s.failed, s.blocked, s.errors], [4, 1, 1, 1, 1]);
  assert.deepEqual(s.tests, { pass: 2, fail: 1, total: 3 });
});

test('import report groups by level in severity order', () => {
  const g = groupReport({ entries: [{ level: 'converted', feature: 'a' }, { level: 'unsupported', feature: 'b' }, { level: 'weird', feature: 'c' }] });
  assert.deepEqual(g.map((x) => x.level), ['unsupported', 'needs-review', 'converted']);
});

test('script review and trust body', () => {
  const v = scriptReview({ scripts: [{ hash: 'abcdef0123456789', listen: 'test', owners: ['', 'r1'], trusted: false, status: 'partial', apis: ['pm.test'] }, { hash: 'z', trusted: true }], capabilities: ['vars.read'], grantable: ['vars.read', 'net.send'] }, (u) => (u === 'r1' ? 'login' : ''));
  assert.equal(v.untrusted, 1);
  assert.deepEqual(v.rows[0].owners, ['Collection', 'login']);
  assert.equal(v.rows[0].shortHash, 'abcdef012345');
  assert.deepEqual(trustBody({ hashes: ['h'] }), { confirm: true, hashes: ['h'] });
  assert.deepEqual(trustBody({ all: true, capabilities: ['vars.read'] }), { confirm: true, all: true, capabilities: ['vars.read'] });
});

test('firstFlowId and command entries', () => {
  assert.equal(firstFlowId({ flowId: 5, flowIds: [5, 6] }), 5);
  assert.equal(firstFlowId({ flowIds: [5, 6] }), 6);
  assert.equal(firstFlowId(null), 0);
  const cmds = commandEntries({ import() {}, send() {} });
  assert.deepEqual(cmds.map((c) => c.t), ['Collections: Import collection', 'Collections: Send open request']);
  assert.ok(cmds.every((c) => c.group === 'Actions'));
});
