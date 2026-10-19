// The pure rules behind the desktop History and Intercept layouts.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { splitViews, moreLabel, activeTokenCount, isViewHidden, notePlaceholder, NOTE_PLACEHOLDER_SHORT, sentenceCase, PRIMARY_VIEWS, toolbarWidthTier } from '../js/desktop-panel-proxy-model.js';
import { orderActions, sameOrder, holdRuleSummary, ACTION_ORDER } from '../js/desktop-panel-intercept-model.js';

const req = ['raw', 'pretty', 'headers', 'json', 'params', 'gql', 'decoded', 'hex'].map((view) => ({ view, label: view }));

test('splitViews keeps Pretty, Raw and Hex visible and folds the rest into More', () => {
  const { primary, more } = splitViews(req);
  assert.deepEqual(primary.map((v) => v.view), ['raw', 'pretty', 'hex']);
  assert.deepEqual(more.map((v) => v.view), ['headers', 'json', 'params', 'gql', 'decoded']);
  assert.deepEqual(PRIMARY_VIEWS, ['pretty', 'raw', 'hex']);
});

test('splitViews drops views the page hid and tolerates bad input', () => {
  const { more } = splitViews([...req, { view: 'render', label: 'Render', hidden: true }]);
  assert.ok(!more.some((v) => v.view === 'render'));
  assert.deepEqual(splitViews(null), { primary: [], more: [] });
  assert.deepEqual(splitViews([null, {}]), { primary: [], more: [] });
});

test('moreLabel names the folded view that is current', () => {
  const { more } = splitViews(req);
  assert.equal(moreLabel(more, 'pretty'), 'More');
  assert.equal(moreLabel(more, undefined), 'More');
  assert.equal(moreLabel([{ view: 'json', label: 'JSON' }], 'json'), 'JSON');
});

test('activeTokenCount counts the filter tokens the search field shows (method, status and text have their own controls)', () => {
  assert.equal(activeTokenCount(['tls', 'search', 'method', 'status']), 1);
  assert.equal(activeTokenCount(['tls', 'scope', 'tag:auth']), 3);
  assert.equal(activeTokenCount([]), 0);
  assert.equal(activeTokenCount(undefined), 0);
});

test('isViewHidden: a view is hidden by [hidden] or by an inline display:none (Render on a non-HTML response)', () => {
  assert.equal(isViewHidden({ hidden: true, display: '' }), true);
  assert.equal(isViewHidden({ hidden: false, display: 'none' }), true);
  assert.equal(isViewHidden({ hidden: false, display: '' }), false);
  assert.equal(isViewHidden({ hidden: false, display: 'inline-flex' }), false);
  assert.equal(isViewHidden(null), true);
});

test('note placeholder is short until the field opens', () => {
  assert.equal(notePlaceholder(false, 'Add a note for this flow'), NOTE_PLACEHOLDER_SHORT);
  assert.equal(notePlaceholder(true, 'Add a note for this flow'), 'Add a note for this flow');
});

test('sentenceCase lowercases the web labels', () => {
  assert.equal(sentenceCase('REQUEST'), 'Request');
  assert.equal(sentenceCase(' RESPONSE '), 'Response');
  assert.equal(sentenceCase(''), '');
  assert.equal(sentenceCase(null), '');
});

test('orderActions puts Forward first, then Drop, then the quiet actions; unknown ids stay last', () => {
  const dom = ['heldRepeaterBtn', 'forwardBtn', 'forwardRepeaterBtn', 'dropBtn', 'forwardAllBtn'];
  assert.deepEqual(orderActions(dom), ACTION_ORDER);
  assert.deepEqual(orderActions(['zzz', 'dropBtn', 'forwardBtn', 'aaa']), ['forwardBtn', 'dropBtn', 'zzz', 'aaa']);
  assert.deepEqual(orderActions(undefined), []);
});

test('sameOrder compares id lists', () => {
  assert.equal(sameOrder(['a', 'b'], ['a', 'b']), true);
  assert.equal(sameOrder(['a', 'b'], ['b', 'a']), false);
  assert.equal(sameOrder(['a'], ['a', 'b']), false);
});

test('holdRuleSummary names the rule', () => {
  assert.match(holdRuleSummary({ on: false }), /everything/);
  assert.equal(holdRuleSummary({ on: true, target: 'url', pattern: '/admin' }), 'Holding when URL matches /admin');
  assert.match(holdRuleSummary({ on: true, target: 'nope', pattern: '' }), /anything matches \(add a pattern\)/);
});

test('pressBubbles: only copy-image buttons need their synthetic click to bubble', async () => {
  const { pressBubbles } = await import('../js/desktop-panel-proxy-model.js');
  const node = (...c) => ({ classList: { contains: (x) => c.includes(x) } });
  assert.equal(pressBubbles(node('btn', 'js-copy-image')), true);
  assert.equal(pressBubbles(node('btn')), false);
  assert.equal(pressBubbles(null), false);
});

test('toolbarWidthTier replaces the History size container: narrow at 880px, compact at 760px', () => {
  assert.deepEqual(toolbarWidthTier(1200), { narrow: false, compact: false });
  assert.deepEqual(toolbarWidthTier(881), { narrow: false, compact: false });
  assert.deepEqual(toolbarWidthTier(880), { narrow: true, compact: false });
  assert.deepEqual(toolbarWidthTier(761), { narrow: true, compact: false });
  assert.deepEqual(toolbarWidthTier(760), { narrow: true, compact: true });
  // A parked (display:none) panel measures 0: keep the wide layout rather than flap.
  assert.deepEqual(toolbarWidthTier(0), { narrow: false, compact: false });
  assert.deepEqual(toolbarWidthTier(NaN), { narrow: false, compact: false });
});
