import test from 'node:test';
import assert from 'node:assert/strict';
import { KINDS, familyKinds, renderRequest, downloadName, attachRequest, toolbarState, hasTiming, decodeHeader, DEFAULT_OPTS, optionControls, chainPreview, waterfallPreview, diffPreview, authzPreview } from '../js/evidence-render.js';

test('intruder kinds map to the attack render endpoint and keep tab order', () => {
  assert.deepEqual(familyKinds('intruder-race'), ['intruder-timeline', 'intruder-distribution', 'intruder-race', 'intruder-strip']);
  const r = renderRequest('intruder-timeline', { runId: 'r_1' });
  assert.equal(r.url, '/api/intruder/attacks/r_1/render.png?kind=timeline');
  assert.equal(renderRequest('intruder-strip', { runId: 'a b/c' }).url, '/api/intruder/attacks/a%20b%2Fc/render.png?kind=strip');
});

test('unknown kinds and missing run ids are rejected, not guessed', () => {
  assert.throws(() => renderRequest('nope', {}), /unknown render kind/);
  assert.throws(() => renderRequest('intruder-race', {}), /run id/);
  assert.throws(() => renderRequest('flow-diff', {}), /url/);
  assert.equal(renderRequest('flow-diff', { url: '/api/x.png' }).url, '/api/x.png');
});

test('download name follows interseptor-intruder-<runId>-<kind>.png and is sanitised', () => {
  assert.equal(downloadName('intruder-race', { runId: 'abc123' }), 'interseptor-intruder-abc123-race.png');
  assert.equal(downloadName('intruder-timeline', { runId: '../x y' }), 'interseptor-intruder-x-y-timeline.png');
  assert.equal(downloadName('authz-matrix', { id: 7 }), 'interseptor-authz-matrix-7.png');
});

test('attach request posts the kind and run to the finding evidence-render endpoint', () => {
  const r = attachRequest(12, 'intruder-race', { runId: 'r9' }, 'Race window');
  assert.equal(r.path, '/api/findings/12/evidence-render');
  assert.equal(r.method, 'POST');
  assert.deepEqual(r.body, { kind: 'intruder-race', runId: 'r9', caption: 'Race window', role: 'result' });
  assert.throws(() => attachRequest(0, 'intruder-race', { runId: 'r9' }, 'x'), /finding/);
});

test('toolbar is disabled with a reason until a finished run with an id exists', () => {
  const rows = [{ status: 200, startUs: 10, endUs: 900 }];
  assert.equal(toolbarState({ runId: '', running: false, total: 3, results: rows }).enabled, false);
  assert.match(toolbarState({ runId: '', running: false, total: 3, results: rows }).reason, /run ID/);
  assert.match(toolbarState({ runId: 'r1', running: true, total: 3, results: rows }).reason, /finish/);
  assert.match(toolbarState({ runId: 'r1', running: false, total: 0, results: [] }).reason, /No results/);
  const ok = toolbarState({ runId: 'r1', running: false, total: 3, results: rows });
  assert.equal(ok.enabled, true);
  assert.equal(ok.timingRecorded, true);
  assert.equal(ok.reason, '');
});

test('legacy runs without timing stay enabled but say timing was not recorded', () => {
  const legacy = toolbarState({ runId: 'r1', running: false, total: 2, results: [{ status: 200, timeMs: 12 }] });
  assert.equal(legacy.enabled, true);
  assert.equal(legacy.timingRecorded, false);
  assert.match(legacy.note, /timing not recorded/);
  assert.equal(hasTiming([{ startUs: 0, endUs: 0 }]), false);
  assert.equal(hasTiming([{ startUs: 0, endUs: 5 }]), true);
});

test('decodeHeader tolerates percent-encoding and bad input', () => {
  assert.equal(decodeHeader('a%20b'), 'a b');
  assert.equal(decodeHeader('100% sure'), '100% sure');
  assert.equal(decodeHeader(null), '');
});

test('every kind has a label and a family', () => {
  for (const [k, m] of Object.entries(KINDS)) { assert.ok(m.label, k); assert.ok(m.family, k); }
});

test('masking is on by default and unmasking is an explicit opt-in for the kinds that show values', () => {
  assert.equal(DEFAULT_OPTS.mask, true);
  assert.equal(DEFAULT_OPTS.includeBody, false);
  assert.equal(renderRequest('intruder-strip', { runId: 'r1' }).url, '/api/intruder/attacks/r1/render.png?kind=strip');
  assert.equal(renderRequest('intruder-strip', { runId: 'r1' }, { mask: false }).url, '/api/intruder/attacks/r1/render.png?kind=strip&unmask=1');
  assert.equal(renderRequest('intruder-race', { runId: 'r1' }, { mask: false }).url.endsWith('&unmask=1'), true);
  // timeline and distribution never show payloads, so the option is not sent
  assert.equal(renderRequest('intruder-timeline', { runId: 'r1' }, { mask: false }).url, '/api/intruder/attacks/r1/render.png?kind=timeline');
  assert.deepEqual(optionControls('intruder-strip'), { mask: true, body: false });
  assert.deepEqual(optionControls('flow-diff'), { mask: false, body: true });
  assert.deepEqual(optionControls('finding-chain'), { mask: false, body: false });
});

test('flow diff body lines are opt-in on both the GET and the attach body', () => {
  const p = diffPreview(4, 9).params;
  assert.equal(renderRequest('flow-diff', p).url, '/api/render/flow-diff.png?a=4&b=9');
  assert.equal(renderRequest('flow-diff', p, { includeBody: true }).url, '/api/render/flow-diff.png?a=4&b=9&includeBody=1');
  assert.equal(attachRequest(3, 'flow-diff', p, 'c').body.includeBody, undefined);
  assert.equal(attachRequest(3, 'flow-diff', p, 'c', { includeBody: true }).body.includeBody, true);
  assert.equal(attachRequest(3, 'intruder-strip', { runId: 'r' }, 'c', { mask: false }).body.unmask, true);
  assert.equal(attachRequest(3, 'intruder-strip', { runId: 'r' }, 'c').body.unmask, undefined);
});

test('entry points for chain, waterfall and diff build the REST urls and attach bodies', () => {
  const c = chainPreview(12);
  assert.equal(c.kind, 'finding-chain');
  assert.equal(c.params.url, '/api/evidence-render?kind=finding_chain&findingId=12');
  assert.deepEqual(attachRequest(12, c.kind, c.params, 'x').body, { findingId: 12, kind: 'finding-chain', caption: 'x', role: 'result' });
  const w = waterfallPreview([5, '6', 5, 0, -1, 'x']);
  assert.equal(w.params.url, '/api/evidence-render?kind=flow_waterfall&flowIds=5,6');
  assert.deepEqual(w.params.attach.flowIds, [5, 6]);
  assert.equal(waterfallPreview(Array.from({ length: 80 }, (_, i) => i + 1)).params.attach.flowIds.length, 50);
  assert.throws(() => waterfallPreview([]), /at least one/);
  assert.throws(() => diffPreview(3, 3), /two different/);
  assert.throws(() => chainPreview(0), /finding id/);
  for (const k of [c.kind, w.kind, diffPreview(1, 2).kind]) assert.ok(KINDS[k], k + ' is a defined kind');
});

test('authz runs render through the captured run id', () => {
  const a = authzPreview('abc123def4567890');
  assert.equal(a.kind, 'authz-matrix');
  assert.equal(a.params.url, '/api/render/authz/abc123def4567890.png');
  assert.equal(attachRequest(5, a.kind, a.params, 'm').body.runId, 'abc123def4567890');
  assert.throws(() => authzPreview(''), /no id/);
});
