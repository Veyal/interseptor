import test from 'node:test';
import assert from 'node:assert/strict';
import { FLOW_TABS, tabsForFlow, nextTab, isWebSocketFlow, flowUrl, wsOpcodeName, wsFramesHTML, timelineHTML, authHTML, renderFlowBody, MAX_RENDER_BYTES, escapeHTML, clampDrawerWidth, stepSibling } from '../js/flowbody.js';

const detail = { id: 7, method: 'GET', scheme: 'https', host: 'example.com', port: 443, path: '/a?b=1', status: 200, reqLen: 10, resLen: 20, flags: 0 };

test('tabsForFlow hides WS for non-WS flows and keeps order', () => {
  assert.deepEqual(tabsForFlow(detail).map((t) => t.id), ['request', 'response', 'timeline', 'auth']);
  assert.deepEqual(tabsForFlow({ ...detail, flags: 32 }).map((t) => t.id), ['request', 'response', 'timeline', 'auth', 'ws']);
  assert.equal(isWebSocketFlow({ flags: 32 }), true);
  assert.equal(isWebSocketFlow(null), false);
  assert.equal(FLOW_TABS.length, 5);
});

test('nextTab implements roving tabindex keys', () => {
  const tabs = tabsForFlow(detail);
  assert.equal(nextTab(tabs, 'request', 'ArrowRight'), 'response');
  assert.equal(nextTab(tabs, 'auth', 'ArrowRight'), 'request');
  assert.equal(nextTab(tabs, 'request', 'ArrowLeft'), 'auth');
  assert.equal(nextTab(tabs, 'response', 'Home'), 'request');
  assert.equal(nextTab(tabs, 'response', 'End'), 'auth');
  assert.equal(nextTab(tabs, 'response', 'x'), 'response');
});

test('flowUrl omits default ports and escapes nothing', () => {
  assert.equal(flowUrl(detail), 'https://example.com/a?b=1');
  assert.equal(flowUrl({ ...detail, port: 8443 }), 'https://example.com:8443/a?b=1');
  assert.equal(flowUrl(null), '');
});

test('escapeHTML neutralises markup', () => {
  assert.equal(escapeHTML('<img src=x onerror="a">&\''), '&lt;img src=x onerror=&quot;a&quot;&gt;&amp;&#39;');
});

test('wsOpcodeName and frames list are escaped and capped', () => {
  assert.equal(wsOpcodeName(1), 'text');
  assert.equal(wsOpcodeName(0x3), '0x3');
  const html = wsFramesHTML([{ dir: 'send', opcode: 1, length: 3, preview: '<b>x</b>' }, { dir: 'recv', opcode: 2, length: 9, preview: '' }]);
  assert.ok(html.includes('&lt;b&gt;x&lt;/b&gt;'));
  assert.ok(!html.includes('<b>x</b>'));
  assert.ok(html.includes('send') && html.includes('recv'));
  assert.ok(wsFramesHTML([]).includes('No frames'));
  const many = Array.from({ length: 600 }, () => ({ dir: 'send', opcode: 1, length: 1, preview: 'a' }));
  assert.ok(wsFramesHTML(many, 500).includes('500 of 600'));
});

test('timelineHTML renders the redirect chain and transitions, escaping values', () => {
  const html = timelineHTML({ flows: [{ id: 1, method: 'GET', scheme: 'https', host: 'example.com', path: '/<x>', status: 302, redirect: '/next' }], transitions: [{ flowId: 1, kind: 'cookie-set', message: 'Set <sid>', confidence: 'observed' }] });
  assert.ok(html.includes('302') && html.includes('/next'));
  assert.ok(html.includes('&lt;x&gt;') && html.includes('Set &lt;sid&gt;'));
  assert.ok(timelineHTML(null).includes('No timeline'));
  assert.ok(timelineHTML({ flows: [], transitions: [] }).includes('No timeline'));
});

test('authHTML marks hypotheses and the loss point', () => {
  const html = authHTML({ steps: [{ flowId: 3, method: 'POST', host: 'example.com', path: '/login', status: 401, events: [{ kind: 'csrf', detail: 'token', confidence: 'hypothesis' }] }], lostAt: { flowId: 3, confidence: 'hypothesis', reason: 'no cookie' }, mfaState: 'none', mfaConfidence: 'observed' });
  assert.ok(html.includes('hypothesis'));
  assert.ok(html.includes('no cookie'));
  assert.ok(authHTML({ steps: [] }).includes('No captured'));
});

test('renderFlowBody: raw tabs use the injected highlighter and truncate above 1 MB', () => {
  const seen = [];
  const deps = { highlight: (raw, side) => { seen.push(side); return escapeHTML(raw); }, downloadHref: (id, side) => `/api/flows/${id}/body?side=${side}` };
  const html = renderFlowBody({ id: 7, detail, raw: { req: 'GET / HTTP/1.1', res: 'HTTP/1.1 200 OK' } }, 'request', deps);
  assert.ok(html.includes('GET / HTTP/1.1'));
  assert.deepEqual(seen, ['req']);
  const big = 'a'.repeat(MAX_RENDER_BYTES + 10);
  const cut = renderFlowBody({ id: 7, detail, raw: { req: big } }, 'request', deps);
  assert.ok(cut.includes('Truncated at 1 MB'));
  assert.ok(!cut.includes('a'.repeat(MAX_RENDER_BYTES + 1)));
  assert.ok(cut.includes('/api/flows/7/body?side=req'));
});

test('renderFlowBody: oversized or binary bodies are never rendered, only offered for download', () => {
  const deps = { highlight: escapeHTML, isBinaryMime: (m) => /image/.test(m), downloadHref: (id, s) => `/b/${id}/${s}`, headerText: () => 'HTTP/1.1 200 OK' };
  const bin = renderFlowBody({ id: 7, detail: { ...detail, resMime: 'image/png' }, raw: {} }, 'response', { ...deps, mimeOf: () => 'image/png' });
  assert.ok(bin.includes('binary') && bin.includes('/b/7/res'));
  const huge = renderFlowBody({ id: 7, detail: { ...detail, resLen: MAX_RENDER_BYTES * 3 }, raw: {} }, 'response', deps);
  assert.ok(huge.includes('not rendered') && huge.includes('/b/7/res'));
});

test('renderFlowBody: missing data yields a loading placeholder, unknown tab yields empty', () => {
  assert.ok(renderFlowBody({ id: 7, detail, raw: {} }, 'request', { highlight: escapeHTML }).includes('Loading'));
  assert.equal(renderFlowBody({ id: 7, detail }, 'nope', {}), '');
  assert.equal(renderFlowBody(null, 'request', {}), '');
});

test('clampDrawerWidth honours min/max and falls back to the default', () => {
  assert.equal(clampDrawerWidth(100), 320);
  assert.equal(clampDrawerWidth(900), 640);
  assert.equal(clampDrawerWidth(500), 500);
  assert.equal(clampDrawerWidth('x'), 420);
  assert.equal(clampDrawerWidth(700, { max: 480 }), 480);
});

test('stepSibling walks the opener list without wrapping', () => {
  assert.equal(stepSibling([4, 5, 6], 5, 1), 6);
  assert.equal(stepSibling([4, 5, 6], 5, -1), 4);
  assert.equal(stepSibling([4, 5, 6], 6, 1), null);
  assert.equal(stepSibling([4, 5, 6], 4, -1), null);
  assert.equal(stepSibling([], 4, 1), null);
  assert.equal(stepSibling([4, 5], 9, 1), null);
});

test('linkedFindings finds findings referencing the flow through blocks or the flow list', async () => {
  const { linkedFindings } = await import('../js/flowbody.js');
  const out = linkedFindings([
    { id: 1, title: 'A', severity: 'High', blocks: [{ type: 'flow', flowId: 7 }] },
    { id: 2, title: 'B', flows: [{ id: 7 }] },
    { id: 3, title: 'C', blocks: [{ type: 'image', flowId: 7 }, { type: 'flow', flowId: 8 }] },
    null,
  ], 7);
  assert.deepEqual(out.map((f) => f.id), [1, 2]);
  assert.deepEqual(linkedFindings(null, 7), []);
});
