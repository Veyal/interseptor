import test from 'node:test';
import assert from 'node:assert/strict';
import { selectionToFocus, parseFocusInput, normalizeLines, clampContext, readFocusHeaders, focusResultText, focusOptions, focusAttachBody, checkRegex, attachErrorText, decodedBodyText } from '../js/flow-focus-model.js';
import { flowPreviewPngURL, focusQuery } from '../js/copy-image.js';
import { renderRequest, attachRequest, flowFocusPreview, optionControls, KINDS } from '../js/evidence-render.js';

test('selectionToFocus keeps the first non-empty line, trimmed, at most 200 characters', () => {
  assert.equal(selectionToFocus('\n   "isAdmin": true,  \n  "id": 4'), '"isAdmin": true,');
  assert.equal(selectionToFocus('a\r\nb'), 'a');
  assert.equal(selectionToFocus('x'.repeat(250)).length, 200);
  assert.equal(selectionToFocus('é'.repeat(250)), 'é'.repeat(200));
  assert.equal(selectionToFocus('  \n\t'), '');
  assert.equal(selectionToFocus(null), '');
});

test('parseFocusInput treats /pattern/ as a regex and plain text as a literal', () => {
  assert.deepEqual(parseFocusInput('/role":\\s*"admin/'), { focus: 'role":\\s*"admin', regex: true });
  assert.deepEqual(parseFocusInput('  a.b  '), { focus: 'a.b', regex: false });
  assert.deepEqual(parseFocusInput('a.b', true), { focus: 'a.b', regex: true });
  assert.deepEqual(parseFocusInput(''), { focus: '', regex: false });
  assert.match(checkRegex('('), /^invalid regex/);
  assert.equal(checkRegex('a+'), '');
});

test('normalizeLines mirrors the server range grammar', () => {
  assert.deepEqual(normalizeLines('res:400 - 440, request:1-5, 12'), { value: 'res:400-440,req:1-5,12', error: '' });
  assert.deepEqual(normalizeLines(''), { value: '', error: '' });
  for (const bad of ['body:1-2', '5-3', '0-2', 'res:a', 'res:']) assert.notEqual(normalizeLines(bad).error, '', bad);
  assert.match(normalizeLines(Array.from({ length: 9 }, (_, i) => String(i + 1)).join(',')).error, /at most 8/);
});

test('clampContext bounds context lines to 0..40 with default 6', () => {
  assert.equal(clampContext(''), 6);
  assert.equal(clampContext('abc'), 6);
  assert.equal(clampContext(0), 0);
  assert.equal(clampContext(99), 40);
  assert.equal(clampContext(-3), 0);
});

test('readFocusHeaders and focusResultText report matches and not-found notes', () => {
  const h = { 'X-Interseptor-Focus-Mode': 'focus', 'X-Interseptor-Focus-Matches': 'req=0,res=2' };
  const r = readFocusHeaders((k) => h[k] || null);
  assert.deepEqual(r, { mode: 'focus', matches: { req: 0, res: 2 }, notFound: [], masked: false, notShown: '', bodyTruncated: '' });
  assert.equal(focusResultText(r), 'Highlighted 2 matches (request 0, response 2).');
  const nf = { 'X-Interseptor-Focus-Mode': 'top', 'X-Interseptor-Focus-Matches': 'res=0', 'X-Interseptor-Focus-Notfound': 'a%2C%20b,res:9-10' };
  const r2 = readFocusHeaders((k) => nf[k] || null);
  assert.deepEqual(r2.notFound, ['a, b', 'res:9-10']);
  assert.equal(focusResultText(r2), 'Not found: “a, b”, “res:9-10”. Showing the first lines instead.');
  const masked = readFocusHeaders((k) => ({ ...nf, 'X-Interseptor-Focus-Masked': '1' })[k] || null);
  assert.match(focusResultText(masked), /masked header value/);
  assert.equal(readFocusHeaders(() => null), null);
  assert.equal(focusResultText(null), '');
});

test('flowPreviewPngURL encodes focus options with encodeURIComponent and repeats focus', () => {
  const url = flowPreviewPngURL(5, { side: 'res', focus: ['"isAdmin":true', 'a&b=c'], focusSide: 'res', regex: false, lines: 'res:400-440', context: 0, caption: 'Highlighted: role escalation' });
  assert.equal(url, '/api/flows/5/preview.png?side=res&pretty=1&layout=vertical&theme=light'
    + '&focus=%22isAdmin%22%3Atrue&focus=a%26b%3Dc&focusSide=res&lines=res%3A400-440&context=0&caption=Highlighted%3A%20role%20escalation');
  assert.equal(flowPreviewPngURL(5, { focus: '/x', regex: true }), '/api/flows/5/preview.png?side=both&pretty=1&layout=vertical&theme=light&focus=%2Fx&regex=1');
  assert.equal(focusQuery({ focus: '  ', regex: true, focusSide: 'res', context: 99 }), '', 'no focus, no regex/side; bad context dropped');
  assert.equal(focusQuery({ focus: Array.from({ length: 10 }, (_, i) => 't' + i) }).split('&focus=').length - 1, 8);
});

test('focusOptions validates before rendering', () => {
  assert.equal(focusOptions({ text: '/(/' }).error.startsWith('invalid regex'), true);
  assert.match(focusOptions({ lines: '9-3' }).error, /from ≤ to/);
  const { error, opts } = focusOptions({ text: 'token', side: 'req', context: '3', lines: 'req:1-2', caption: ' c ' });
  assert.equal(error, '');
  assert.deepEqual(opts, { focus: ['token'], regex: false, focusSide: 'req', lines: 'req:1-2', context: 3, caption: 'c', decode: 'auto' });
});

test('flow-focus kind renders preview.png with focus and attaches to flow-preview', () => {
  assert.ok(KINDS['flow-focus']);
  assert.equal(optionControls('flow-focus').focus, true);
  const { kind, params } = flowFocusPreview(12, { selection: '\n "isAdmin": true \nnext', focusSide: 'res' });
  assert.equal(kind, 'flow-focus');
  assert.equal(params.seed.text, '"isAdmin": true');
  const url = renderRequest(kind, params, { focus: params.focus }).url;
  assert.equal(url, '/api/flows/12/preview.png?side=both&pretty=1&layout=vertical&theme=light&focus=%22isAdmin%22%3A%20true&focusSide=res&context=6');
  const a = attachRequest(3, kind, params, 'ignored alt', { focus: params.focus });
  assert.equal(a.path, '/api/findings/3/flow-preview');
  assert.deepEqual(a.body, { flowId: 12, side: 'both', theme: 'light', role: 'result', focus: ['"isAdmin": true'], focusSide: 'res', context: 6 });
  assert.throws(() => flowFocusPreview(0), /flow id/);
  assert.deepEqual(focusAttachBody(4, { focus: [], regex: true }), { flowId: 4, side: 'both', theme: 'light', role: 'result' });
});

test('focusResultText never echoes a masked focus and explains truncation and undrawn matches', () => {
  const masked = readFocusHeaders((k) => ({ 'X-Interseptor-Focus-Mode': 'top', 'X-Interseptor-Focus-Matches': 'req=0,res=0', 'X-Interseptor-Focus-Masked': '1' })[k] || null);
  assert.equal(focusResultText(masked), 'A focus text matched only a masked header value (hidden). Showing the first lines instead.');
  const cut = readFocusHeaders((k) => ({ 'X-Interseptor-Focus-Mode': 'top', 'X-Interseptor-Focus-Matches': 'res=0', 'X-Interseptor-Focus-Notfound': 'isAdmin', 'X-Interseptor-Body-Truncated': 'res' })[k] || null);
  assert.equal(cut.bodyTruncated, 'res');
  assert.match(focusResultText(cut), /cut at 64 KiB for the image/);
  const hidden = readFocusHeaders((k) => ({ 'X-Interseptor-Focus-Mode': 'focus', 'X-Interseptor-Focus-Matches': 'res=3', 'X-Interseptor-Focus-Notshown': 'res' })[k] || null);
  assert.doesNotMatch(focusResultText(hidden), /^Highlighted/);
  assert.match(focusResultText(hidden), /did not fit in the image/);
});

test('attachErrorText explains a refused focused attach (422)', () => {
  const e = Object.assign(new Error('focus not found: isAdmin'), { status: 422 });
  assert.equal(attachErrorText(e), 'Not attached: focus not found: isAdmin. Change the Focus text or lines, or clear them to attach the first lines.');
  assert.equal(attachErrorText(new Error('boom')), 'boom');
  assert.equal(attachErrorText(null), 'request failed');
});

test('decode option: auto is the default and stays out of requests; raw is sent', () => {
  assert.equal(focusOptions({ text: 'x' }).opts.decode, 'auto');
  assert.equal(focusOptions({ text: 'x', decode: 'raw' }).opts.decode, 'raw');
  assert.equal(focusOptions({ text: 'x', decode: 'RAW ' }).opts.decode, 'raw');
  assert.equal(focusOptions({ text: 'x', decode: 'bogus' }).opts.decode, 'auto');
  assert.equal(focusAttachBody(5, focusOptions({ text: 'x' }).opts).decode, undefined);
  assert.equal(focusAttachBody(5, focusOptions({ text: 'x', decode: 'raw' }).opts).decode, 'raw');
  assert.equal(focusQuery({ decode: 'auto' }), '');
  assert.equal(focusQuery({ decode: 'raw' }), '&decode=raw');
  assert.ok(flowPreviewPngURL(5, { decode: 'raw', focus: 'x' }).endsWith('&focus=x&decode=raw'));
  assert.ok(!flowPreviewPngURL(5, { focus: 'x' }).includes('decode'));
});

test('decode option flows through renderRequest and attachRequest for the dialog', () => {
  const opts = { focus: focusOptions({ text: 'role', decode: 'raw' }).opts };
  assert.match(renderRequest('flow-focus', { flowId: 7, side: 'res' }, opts).url, /&decode=raw$/);
  assert.equal(attachRequest(3, 'flow-focus', { flowId: 7, side: 'res' }, '', opts).body.decode, 'raw');
  const auto = { focus: focusOptions({ text: 'role' }).opts };
  assert.ok(!renderRequest('flow-focus', { flowId: 7 }, auto).url.includes('decode'));
  assert.equal(attachRequest(3, 'flow-focus', { flowId: 7 }, '', auto).body.decode, undefined);
});

test('decodedBodyText explains X-Interseptor-Decoded-Body', () => {
  assert.equal(decodedBodyText(null), '');
  assert.equal(decodedBodyText(''), '');
  assert.equal(decodedBodyText('req=form,res=codec:example-envelope'), 'Decoded body: request form, response codec:example-envelope. Choose Raw to see the original bytes.');
  assert.equal(decodedBodyText('res=base64'), 'Decoded body: response base64. Choose Raw to see the original bytes.');
});
