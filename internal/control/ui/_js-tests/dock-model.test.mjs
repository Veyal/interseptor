import test from 'node:test';
import assert from 'node:assert/strict';
import {
  DOCK_DESTINATIONS, destinationForPanel, resolvePanel, rememberPanel, parseStoredLast,
  badgeCount, badgeText, badgeAnnouncement, dockKeyTarget,
} from '../js/dock-model.js';

test('five destinations map to the rail groups and cover every panel once', () => {
  assert.deepEqual(DOCK_DESTINATIONS.map((d) => d.id), ['capture', 'test', 'recon', 'report', 'more']);
  const panels = DOCK_DESTINATIONS.flatMap((d) => d.panels);
  assert.equal(new Set(panels).size, panels.length);
  assert.deepEqual([...panels].sort(), ['activity', 'findings', 'intercept', 'intruder', 'map', 'notes', 'proxy', 'repeater', 'scanner', 'settings']);
});

test('destinationForPanel resolves panels and rejects unknown names', () => {
  assert.equal(destinationForPanel('intruder'), 'test');
  assert.equal(destinationForPanel('activity'), 'report');
  assert.equal(destinationForPanel('settings'), 'more');
  assert.equal(destinationForPanel('nope'), null);
});

test('resolvePanel opens the last-used panel of a destination, else its first', () => {
  assert.equal(resolvePanel('capture', {}), 'proxy');
  assert.equal(resolvePanel('capture', { capture: 'intercept' }), 'intercept');
  assert.equal(resolvePanel('capture', { capture: 'findings' }), 'proxy');
  assert.equal(resolvePanel('report', { report: 'notes' }), 'notes');
  assert.equal(resolvePanel('bogus', {}), null);
});

test('rememberPanel is immutable and ignores unknown panels', () => {
  const a = { capture: 'proxy' };
  const b = rememberPanel(a, 'intercept');
  assert.deepEqual(a, { capture: 'proxy' });
  assert.deepEqual(b, { capture: 'intercept' });
  assert.equal(rememberPanel(a, 'zzz'), a);
});

test('parseStoredLast tolerates corrupt storage and drops invalid entries', () => {
  assert.deepEqual(parseStoredLast(null), {});
  assert.deepEqual(parseStoredLast('{not json'), {});
  assert.deepEqual(parseStoredLast('[1,2]'), {});
  assert.deepEqual(parseStoredLast(JSON.stringify({ test: 'intruder', recon: 'proxy', x: 'map' })), { test: 'intruder' });
});

test('badgeCount reads rail badges and honours hidden', () => {
  assert.equal(badgeCount('3', false), 3);
  assert.equal(badgeCount(' 12 ', false), 12);
  assert.equal(badgeCount('3', true), 0);
  assert.equal(badgeCount('', false), 0);
  assert.equal(badgeCount('abc', false), 0);
  assert.equal(badgeCount('-4', false), 0);
  assert.equal(badgeCount(undefined, false), 0);
});

test('badgeText caps at 99+ and badgeAnnouncement states the meaning in words', () => {
  assert.equal(badgeText(0), '');
  assert.equal(badgeText(7), '7');
  assert.equal(badgeText(120), '99+');
  assert.equal(badgeAnnouncement('capture', 2), '2 requests held');
  assert.equal(badgeAnnouncement('capture', 1), '1 request held');
  assert.equal(badgeAnnouncement('report', 3), '3 blockers');
  assert.equal(badgeAnnouncement('report', 1), '1 blocker');
  assert.equal(badgeAnnouncement('test', 5), '');
  assert.equal(badgeAnnouncement('capture', 0), '');
});

test('dockKeyTarget moves focus with arrows, wraps, and supports Home/End', () => {
  assert.equal(dockKeyTarget('ArrowRight', 0, 5), 1);
  assert.equal(dockKeyTarget('ArrowRight', 4, 5), 0);
  assert.equal(dockKeyTarget('ArrowLeft', 0, 5), 4);
  assert.equal(dockKeyTarget('Home', 3, 5), 0);
  assert.equal(dockKeyTarget('End', 1, 5), 4);
  assert.equal(dockKeyTarget('ArrowUp', 1, 5), -1);
  assert.equal(dockKeyTarget('ArrowRight', 0, 0), -1);
});
