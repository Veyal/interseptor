import test from 'node:test';
import assert from 'node:assert/strict';
import {
  SUBMENU_HOVER_MS, ROOT_ITEM_TARGET, ROOT_ITEM_MAX, PAYLOAD_MARKER, CODEC_OPS, INSERT_SNIPPETS,
  normalizeItems, normalizeSections, hasChildren, childSections, menuRows, rootItemCount,
  exceedsRootLimit, submenuPlacement, menuKeyAction, nextIndex, textFieldMenuModel,
  selectionMenuModel, shortLabel, dismissOnDocumentClick,
} from '../js/ctxmenu-model.js';

const VP = { width: 1000, height: 700, left: 0, top: 0 };

test('hover debounce is 120ms and the root limit is the 7-item rule', () => {
  assert.equal(SUBMENU_HOVER_MS, 120);
  assert.equal(ROOT_ITEM_TARGET, 7);
  assert.equal(ROOT_ITEM_MAX, 9);
});

test('normalizeItems drops empties and collapses separator runs', () => {
  const items = normalizeItems([null, { sep: true }, { label: 'a' }, { sep: true }, { sep: true }, { label: 'b' }, { sep: true }]);
  assert.deepEqual(items.map((i) => i.label || 'SEP'), ['a', 'SEP', 'b']);
});

test('normalizeSections drops sections with nothing to show', () => {
  const secs = normalizeSections([{ head: 'A', items: [] }, null, { head: 'B', items: [{ label: 'x' }] }]);
  assert.deepEqual(secs.map((s) => s.head), ['B']);
});

test('hasChildren ignores an empty or sparse children array', () => {
  assert.equal(hasChildren({ label: 'a', children: [] }), false);
  assert.equal(hasChildren({ label: 'a', children: [null, false] }), false);
  assert.equal(hasChildren({ label: 'a', children: [{ label: 'b' }] }), true);
});

test('childSections accepts a flat child list or pre-grouped sections', () => {
  const flat = childSections({ children: [{ label: 'a' }, { label: 'b' }] });
  assert.equal(flat.length, 1);
  assert.deepEqual(flat[0].items.map((i) => i.label), ['a', 'b']);
  const grouped = childSections({ children: [{ head: 'H1', items: [{ label: 'a' }] }, { head: 'H2', items: [{ label: 'b' }] }] });
  assert.deepEqual(grouped.map((s) => s.head), ['H1', 'H2']);
});

test('menuRows counts focusable rows only, never headings or separators', () => {
  const sections = [{ head: 'A', items: [{ label: 'one' }, { sep: true }, { label: 'two' }] }, { head: 'B', items: [{ label: 'three' }] }];
  assert.deepEqual(menuRows(sections).map((r) => r.label), ['one', 'two', 'three']);
  assert.equal(rootItemCount(sections), 3);
  assert.equal(exceedsRootLimit(sections), false);
});

test('exceedsRootLimit flags a menu that has stopped being categorised', () => {
  const flat = [{ items: Array.from({ length: 10 }, (_, i) => ({ label: 'item ' + i })) }];
  assert.equal(exceedsRootLimit(flat), true);
  const categorised = [{ items: [{ label: 'Copy as', children: Array.from({ length: 10 }, (_, i) => ({ label: 'k' + i })) }] }];
  assert.equal(exceedsRootLimit(categorised), false);
});

test('submenuPlacement opens to the right and overlaps the parent frame', () => {
  const p = submenuPlacement({ parent: { left: 100, right: 300, top: 50, bottom: 400 }, item: { top: 80 }, width: 220, height: 180, viewport: VP });
  assert.equal(p.side, 'right');
  assert.equal(p.left, 296);
  assert.equal(p.top, 75);
});

test('submenuPlacement flips left when the flyout would run off the right edge', () => {
  const p = submenuPlacement({ parent: { left: 700, right: 900, top: 50, bottom: 400 }, item: { top: 60 }, width: 220, height: 180, viewport: VP });
  assert.equal(p.side, 'left');
  assert.equal(p.left, 704 - 220);
});

test('submenuPlacement takes the roomier side and stays on screen when neither fits', () => {
  const narrow = { width: 300, height: 400, left: 0, top: 0 };
  const p = submenuPlacement({ parent: { left: 20, right: 250, top: 10, bottom: 300 }, item: { top: 10 }, width: 280, height: 200, viewport: narrow });
  assert.equal(p.side, 'right');
  assert.ok(p.left >= 8);
  assert.ok(p.left + p.width <= narrow.width - 8);
});

test('submenuPlacement slides a tall flyout up so its bottom stays in view', () => {
  const p = submenuPlacement({ parent: { left: 10, right: 200, top: 600, bottom: 690 }, item: { top: 650 }, width: 200, height: 300, viewport: VP });
  assert.ok(p.top + 300 <= VP.height - 8);
});

test('menuKeyAction drives the cascade: right enters, left and Escape leave', () => {
  assert.equal(menuKeyAction('ArrowRight', { children: true }), 'open');
  assert.equal(menuKeyAction('ArrowRight', { children: false }), '');
  assert.equal(menuKeyAction('ArrowLeft', { depth: 1 }), 'close');
  assert.equal(menuKeyAction('ArrowLeft', { depth: 0 }), '');
  assert.equal(menuKeyAction('Escape', { depth: 1 }), 'close');
  assert.equal(menuKeyAction('Escape', { depth: 0 }), 'dismiss');
  assert.equal(menuKeyAction('Enter', { children: false }), 'activate');
  assert.equal(menuKeyAction('Enter', { children: true }), 'open');
  assert.equal(menuKeyAction(' ', { children: false }), 'activate');
  assert.equal(menuKeyAction('Tab', {}), 'dismiss');
  assert.equal(menuKeyAction('ArrowDown', {}), 'next');
  assert.equal(menuKeyAction('ArrowUp', {}), 'prev');
  assert.equal(menuKeyAction('Home', {}), 'first');
  assert.equal(menuKeyAction('End', {}), 'last');
  assert.equal(menuKeyAction('x', {}), '');
});

test('nextIndex wraps at both ends', () => {
  assert.equal(nextIndex(2, 3, 'next'), 0);
  assert.equal(nextIndex(0, 3, 'prev'), 2);
  assert.equal(nextIndex(2, 3, 'first'), 0);
  assert.equal(nextIndex(0, 3, 'last'), 2);
  assert.equal(nextIndex(5, 0, 'next'), 0);
});

test('textFieldMenuModel categorises and never exceeds the root limit', () => {
  const m = textFieldMenuModel({ selection: 'abc', editable: true, empty: false, payloadMarker: true });
  assert.deepEqual(m.map((s) => s.head || ''), ['CLIPBOARD', '', '']);
  assert.ok(!exceedsRootLimit(m), 'text field menu must stay categorised');
  const labels = menuRows(m).map((r) => r.label);
  assert.deepEqual(labels.slice(0, 4), ['Cut', 'Copy', 'Paste', 'Select all']);
  assert.ok(labels.includes('Encode / decode'));
  assert.ok(labels.includes('Insert'));
  assert.ok(labels.includes('Clear field'));
});

test('textFieldMenuModel disables what cannot work right now', () => {
  const noSel = menuRows(textFieldMenuModel({ selection: '', editable: true, empty: false }));
  const by = (l) => noSel.find((r) => r.label === l);
  assert.equal(by('Cut').disabled, true);
  assert.equal(by('Copy').disabled, true);
  assert.equal(by('Paste').disabled, false);
  const readOnly = menuRows(textFieldMenuModel({ selection: 'x', editable: false, empty: false }));
  assert.equal(readOnly.find((r) => r.label === 'Paste').disabled, true);
  assert.equal(readOnly.find((r) => r.label === 'Copy').disabled, false);
  assert.equal(readOnly.find((r) => r.label === 'Clear field').disabled, true);
  const noClipRead = menuRows(textFieldMenuModel({ selection: 'x', editable: true, empty: false, clipboardRead: false }));
  assert.equal(noClipRead.find((r) => r.label === 'Paste').disabled, true);
});

test('the payload marker is offered only where a position means something', () => {
  const inserts = (payloadMarker) => childSections(menuRows(textFieldMenuModel({ payloadMarker })).find((r) => r.label === 'Insert'))[0].items.map((i) => i.label);
  assert.ok(inserts(true).some((l) => l.includes(PAYLOAD_MARKER)));
  assert.ok(!inserts(false).some((l) => l.includes(PAYLOAD_MARKER)));
});

test('encode/decode children cover every /api/decode op the engine offers', () => {
  const kids = childSections(menuRows(textFieldMenuModel({ selection: 'x', empty: false })).find((r) => r.label === 'Encode / decode'))[0].items;
  assert.equal(kids.length, CODEC_OPS.length);
  for (const c of CODEC_OPS) assert.ok(kids.some((k) => k.action === 'codec:' + c.op), c.op);
  for (const want of ['urldecode', 'base64decode', 'hexdecode', 'htmldecode', 'jwtdecode']) {
    assert.ok(CODEC_OPS.some((c) => c.op === want), want);
  }
});

test('insert snippets carry inert literal values, never generated traffic', () => {
  for (const s of INSERT_SNIPPETS) {
    if (s.value != null) assert.equal(typeof s.value, 'string');
  }
  assert.ok(INSERT_SNIPPETS.some((s) => s.id === 'uuid'));
  assert.ok(INSERT_SNIPPETS.some((s) => s.id === 'timestamp'));
  assert.ok(INSERT_SNIPPETS.filter((s) => s.probe).length >= 2);
});

test('selectionMenuModel is empty without a selection and categorised with one', () => {
  assert.deepEqual(selectionMenuModel({ selection: '' }), []);
  const m = selectionMenuModel({ selection: 'GET /a' });
  assert.equal(m[0].head, 'SELECTION');
  assert.deepEqual(menuRows(m).map((r) => r.label), ['Copy', 'Encode / decode', 'Open in Decoder', 'Search in History']);
  assert.ok(!exceedsRootLimit(m));
});

test('shortLabel collapses whitespace and ellipsises', () => {
  assert.equal(shortLabel('  a\n b '), 'a b');
  assert.equal(shortLabel('x'.repeat(50), 10), 'xxxxxxxxxx…');
  assert.equal(shortLabel(null), '');
});

// Regression: the click that opens a menu from a button (Copy as, Send to) bubbles
// to the document-level outside-click listener after the button's handler ran.
// Treating that click as "outside" closed the menu before it painted.
test('dismissOnDocumentClick keeps the menu open for clicks on its own trigger', () => {
  const trigger = { contains: (n) => n === trigger || n === icon };
  const icon = {};
  const elsewhere = {};
  const inMenu = () => false;
  assert.equal(dismissOnDocumentClick(trigger, inMenu, trigger), false);
  assert.equal(dismissOnDocumentClick(icon, inMenu, trigger), false, 'a click on an icon inside the trigger is still the trigger');
  assert.equal(dismissOnDocumentClick(elsewhere, inMenu, trigger), true);
  assert.equal(dismissOnDocumentClick(elsewhere, () => true, trigger), false, 'clicks inside the menu never dismiss');
  assert.equal(dismissOnDocumentClick(elsewhere, inMenu, null), true, 'a pointer-opened menu has no trigger');
  assert.equal(dismissOnDocumentClick(null, inMenu, trigger), true);
});

// Review regression: a right-click menu uses its row as the trigger. A later left
// click on that row is a new click and must close the menu; only the click that
// opened the menu (openingTrigger set for that dispatch) is spared.
test('dismissOnDocumentClick closes on a later click on a row trigger', () => {
  const row = { contains: (n) => n === row };
  const inMenu = () => false;
  assert.equal(dismissOnDocumentClick(row, inMenu, null), true, 'no opening trigger once the opening click is over');
  assert.equal(dismissOnDocumentClick(row, inMenu, row), false, 'the opening click itself is spared');
});
