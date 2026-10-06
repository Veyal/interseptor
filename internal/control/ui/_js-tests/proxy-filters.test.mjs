import test from 'node:test';
import assert from 'node:assert/strict';
import {
  activeFilterCount, popoverFilterCount, emptyStateModel, attachedLabel, bulkVerbs, parseDockPref, resolveDock, DRAWER_MIN_WIDTH, middleEllipsis,
} from '../js/proxy-filters.js';

const base = () => ({
  filters: { scheme: '', search: '', method: '', status: '', host: '', tag: '', exclude: [] },
  notesOnly: false, inScopeOnly: false, showManual: true, showAI: true, hideTlsFailed: true,
});

test('activeFilterCount mirrors the legacy anyFilter semantics', () => {
  assert.equal(activeFilterCount(base()), 0, 'default state, including hiding TLS failures, is zero');
  const s = base();
  s.filters.method = 'POST'; s.filters.exclude = [{ field: 'host', value: 'example.com' }, { field: 'path', value: '/x' }];
  s.inScopeOnly = true; s.showAI = false;
  assert.equal(activeFilterCount(s), 5);
});

test('popoverFilterCount counts only controls that live inside the Filters popover', () => {
  const s = base();
  s.filters.method = 'GET'; s.inScopeOnly = true;
  assert.equal(popoverFilterCount(s), 0);
  s.notesOnly = true; s.showManual = false; s.hideTlsFailed = false; s.filters.tag = 'auth';
  assert.equal(popoverFilterCount(s), 4);
});

test('emptyStateModel distinguishes first run from filtered-empty with a count', () => {
  const first = emptyStateModel(base(), { proxyAddr: '127.0.0.1:8080' });
  assert.equal(first.kind, 'empty-first');
  assert.match(first.title, /point your browser or device/i);
  assert.match(first.hint, /127\.0\.0\.1:8080/);
  const s = base(); s.filters.method = 'GET'; s.filters.host = 'example.com'; s.inScopeOnly = true;
  const filtered = emptyStateModel(s, { proxyAddr: 'x' });
  assert.equal(filtered.kind, 'empty-filtered');
  assert.equal(filtered.title, 'No flows match 3 filters');
  s.inScopeOnly = false; s.filters.host = '';
  assert.equal(emptyStateModel(s, {}).title, 'No flows match 1 filter');
});

test('attachedLabel names the findings, singular and plural', () => {
  assert.equal(attachedLabel([]), '');
  assert.equal(attachedLabel([{ id: 2, title: 'IDOR' }]), 'Attached to finding #2');
  assert.equal(attachedLabel([{ id: 2 }, { id: 5 }]), 'Attached to findings #2, #5');
});

test('bulkVerbs enables verbs by selection size', () => {
  const none = Object.fromEntries(bulkVerbs(0).map((v) => [v.id, v.enabled]));
  assert.ok(Object.values(none).every((e) => !e));
  const one = Object.fromEntries(bulkVerbs(1).map((v) => [v.id, v.enabled]));
  assert.equal(one.diff, false);
  assert.equal(one.intruder, true);
  assert.equal(one.repeater, true);
  const two = Object.fromEntries(bulkVerbs(2).map((v) => [v.id, v.enabled]));
  assert.equal(two.diff, true);
  assert.equal(two.intruder, false, 'Intruder takes exactly one flow');
  assert.equal(two.delete, true);
  const three = Object.fromEntries(bulkVerbs(3).map((v) => [v.id, v.enabled]));
  assert.equal(three.diff, false);
  assert.ok(bulkVerbs(2).every((v) => typeof v.reason === 'string'));
});

test('dock preference defaults to the drawer on wide screens only', () => {
  assert.equal(DRAWER_MIN_WIDTH, 1100);
  assert.equal(parseDockPref('bottom'), 'bottom');
  assert.equal(parseDockPref('drawer'), 'drawer');
  assert.equal(parseDockPref('garbage'), 'drawer');
  assert.equal(parseDockPref(null), 'drawer');
  assert.equal(resolveDock('drawer', 1400), 'drawer');
  assert.equal(resolveDock('drawer', 1099), 'bottom');
  assert.equal(resolveDock('bottom', 1600), 'bottom');
});

test('middleEllipsis keeps both ends of a long path and leaves short ones alone', () => {
  assert.equal(middleEllipsis('/api/v1/users', 44), '/api/v1/users');
  const long = '/api/v1/organizations/1234567890/projects/abcdefghij/resources/export.json';
  const out = middleEllipsis(long, 30);
  assert.equal(out.length, 30);
  assert.ok(out.startsWith('/api/v1/organi'));
  assert.ok(out.endsWith('export.json'));
  assert.ok(out.includes('\u2026'));
  assert.equal(middleEllipsis(null, 10), '');
  assert.equal(middleEllipsis('abcdefghij', 3), 'abcdefghij', 'a nonsensical max never mangles text');
});
