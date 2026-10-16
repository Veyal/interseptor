import test from 'node:test';
import assert from 'node:assert/strict';
import {
  relativeTime, openedLabel, rowMeta, normalizeFolder, folderOptions, projectKey,
  projectPathFlavorFor, projectPathKey, isCurrentProject,
  matchProject, highlightHTML, buildPickerModel, nextIndex, pickerSummary,
  validateNewProject, RECENT_LIMIT, RECENT_THRESHOLD,
} from '../js/project-picker-model.js';

const NOW = Date.UTC(2026, 5, 15, 12, 0, 0);
const ago = (ms) => Math.floor((NOW - ms) / 1000);
const MIN = 60e3, HOUR = 60 * MIN, DAY = 24 * HOUR;
const esc = (s) => String(s).replace(/[&<>]/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;' }[c]));

test('relative times are short and stable', () => {
  assert.equal(relativeTime(0, NOW), '');
  assert.equal(relativeTime(ago(20e3), NOW), 'just now');
  assert.equal(relativeTime(ago(5 * MIN), NOW), '5m ago');
  assert.equal(relativeTime(ago(2 * HOUR), NOW), '2h ago');
  assert.equal(relativeTime(ago(3 * DAY), NOW), '3d ago');
  assert.match(relativeTime(ago(40 * DAY), NOW), /^\d{1,2} [A-Z][a-z]{2}$/);
  assert.match(relativeTime(ago(500 * DAY), NOW), /^\d{1,2} [A-Z][a-z]{2} \d{4}$/);
  // A clock skew into the future never prints a negative age.
  assert.equal(relativeTime(Math.floor((NOW + HOUR) / 1000), NOW), 'just now');
});

test('row time label says when it was opened and never shows creation noise', () => {
  assert.equal(openedLabel({ openedAt: ago(2 * HOUR), createdAt: ago(9 * DAY) }, NOW), 'Opened 2h ago');
  assert.equal(openedLabel({ createdAt: ago(20e3) }, NOW), 'Never opened');
  assert.equal(openedLabel({}, NOW), 'Never opened');
});

test('row meta adds the folder only where the section does not already say it', () => {
  const p = { name: 'web', category: 'Clients/Acme', openedAt: ago(2 * HOUR) };
  assert.equal(rowMeta(p, { showFolder: true, nowMs: NOW }), 'Opened 2h ago · Clients/Acme');
  assert.equal(rowMeta(p, { showFolder: false, nowMs: NOW }), 'Opened 2h ago');
  assert.equal(rowMeta({ name: 'x' }, { showFolder: true, nowMs: NOW }), 'Never opened');
});

test('folders normalise separators and whitespace', () => {
  assert.equal(normalizeFolder('  Clients \\ Acme / '), 'Clients/Acme');
  assert.equal(normalizeFolder(''), '');
  assert.equal(normalizeFolder(undefined), '');
  assert.deepEqual(folderOptions([{ category: 'Clients/Acme' }, { category: 'Internal' }, { category: ' Clients / Acme' }, {}]), ['Clients/Acme', 'Internal']);
});

test('path keys normalise posix and windows paths', () => {
  assert.equal(projectPathFlavorFor('C:\\work'), 'windows');
  assert.equal(projectPathFlavorFor('\\\\server\\share'), 'windows');
  assert.equal(projectPathFlavorFor('/srv/work'), 'posix');
  const cases = [
    ['/tmp/engagement/../acme/', 'posix', '/tmp/acme'],
    ['/tmp//acme/./', 'posix', '/tmp/acme'],
    ['//tmp/../acme', 'posix', '/acme'],
    ['\\tmp\\acme', 'posix', ''],
    ['C:\\engagements\\draft\\..\\acme', 'windows', 'C:/engagements/acme'],
    ['\\\\server\\share\\draft\\..\\acme', 'windows', '//server/share/acme'],
    ['\\\\server\\share\\..\\..\\acme', 'windows', '//server/share/acme'],
    ['relative/acme', 'posix', ''],
    ['../acme', 'posix', ''],
  ];
  for (const [input, flavor, want] of cases) assert.equal(projectPathKey(input, flavor), want, input);
});

test('current project matches by path for folders and by name otherwise', () => {
  const projects = [{ name: 'acme', path: '' }, { name: 'acme', path: '/srv/acme' }];
  assert.equal(isCurrentProject(projects[0], 'acme', '/home/u/.interseptor/projects/acme', projects, 'posix'), true);
  assert.equal(isCurrentProject(projects[1], 'acme', '/home/u/.interseptor/projects/acme', projects, 'posix'), false);
  // When the open directory is the external folder, the named twin is not current.
  assert.equal(isCurrentProject(projects[0], 'acme', '/srv/acme/', projects, 'posix'), false);
  assert.equal(isCurrentProject(projects[1], 'acme', '/srv/acme/', projects, 'posix'), true);
  assert.notEqual(projectKey(projects[0], 'posix'), projectKey(projects[1], 'posix'));
});

test('filter matches name, folder and path with every term', () => {
  const p = { name: 'acme-web', category: 'Clients/Acme', path: '/srv/engagements/acme' };
  const all = matchProject(p, 'acme');
  assert.deepEqual(all.name, [[0, 4]]);
  assert.deepEqual(all.folder, [[8, 12]]);
  assert.deepEqual(all.path, [[17, 21]]);
  assert.ok(matchProject(p, 'CLIENTS web'), 'case-insensitive, terms may hit different fields');
  assert.equal(matchProject(p, 'acme mobile'), null, 'every term must match somewhere');
  assert.ok(matchProject({ name: 'x', path: '/srv/engagements/acme' }, 'engagements'), 'path is searchable');
  assert.deepEqual(matchProject(p, '  '), { name: [], folder: [], path: [] });
});

test('highlight escapes text and wraps merged ranges', () => {
  assert.equal(highlightHTML('a<b>acme', [[3, 5], [4, 8]], esc), 'a&lt;b<mark class="pm-hit">&gt;acme</mark>');
  assert.equal(highlightHTML('plain', [], esc), 'plain');
  assert.equal(highlightHTML('', [[0, 2]], esc), '');
});

function fixture(count) {
  const out = [];
  for (let i = 0; i < count; i++) {
    out.push({ name: 'p' + i, path: '', category: i % 3 === 0 ? 'Clients/Acme' : i % 3 === 1 ? 'Internal' : '', openedAt: ago((i + 1) * HOUR), createdAt: ago(30 * DAY) });
  }
  return out;
}

test('the current project is pinned once and never listed as a row', () => {
  const model = buildPickerModel(fixture(4), { current: 'p0', dir: '/h/.interseptor/projects/p0', nowMs: NOW });
  assert.equal(model.current.project.name, 'p0');
  assert.ok(model.items.every((i) => i.project.name !== 'p0'));
  assert.equal(model.total, 3);
  assert.equal(model.matched, 3);
});

test('small lists group by folder only, folders A-Z then unfiled last, all open', () => {
  const model = buildPickerModel(fixture(4), { current: 'p0', dir: '/h/.interseptor/projects/p0', nowMs: NOW });
  assert.deepEqual(model.sections.map((s) => s.label), ['Clients/Acme', 'Internal', 'No folder']);
  assert.ok(model.sections.every((s) => s.open));
  assert.deepEqual(model.sections[0].items.map((i) => i.project.name), ['p3']);
  assert.equal(model.sections[0].containsCurrent, true);
  assert.equal(model.items[model.enterIndex].project.name, 'p3');
  model.items.forEach((item, index) => assert.equal(item.index, index));
});

test('rows sort by latest activity, then creation, then name', () => {
  const projects = [
    { name: 'b', createdAt: ago(1 * DAY) },
    { name: 'a', createdAt: ago(1 * DAY) },
    { name: 'old', openedAt: ago(9 * DAY) },
    { name: 'new', openedAt: ago(1 * HOUR) },
    { name: 'fresh', createdAt: ago(1 * MIN) },
  ];
  const model = buildPickerModel(projects, { current: 'zzz', dir: '/x', nowMs: NOW });
  assert.deepEqual(model.items.map((i) => i.project.name), ['new', 'old', 'fresh', 'a', 'b']);
});

test('a single unfiled group is labelled as all projects', () => {
  const model = buildPickerModel([{ name: 'a' }, { name: 'b' }], { current: 'a', dir: '/x', nowMs: NOW });
  assert.deepEqual(model.sections.map((s) => s.label), ['All projects']);
});

test('large lists lead with Recent; folders keep every member', () => {
  const projects = fixture(RECENT_THRESHOLD + 2);
  const model = buildPickerModel(projects, { current: 'p0', dir: '/h/.interseptor/projects/p0', nowMs: NOW });
  assert.equal(model.sections[0].id, 'recent');
  assert.equal(model.sections[0].label, 'Recent');
  const recent = model.sections[0].items.map((i) => i.project.name);
  assert.equal(recent.length, RECENT_LIMIT);
  assert.deepEqual(recent, ['p1', 'p2', 'p3', 'p4', 'p5']);
  assert.ok(model.sections[0].items.every((i) => i.showFolder), 'Recent rows name their folder');
  const foldered = model.sections.slice(1).flatMap((s) => s.items.map((i) => i.project.name));
  assert.equal(foldered.length, projects.length - 1, 'every non-current project stays in its folder');
  assert.equal(new Set(model.items.map((i) => i.id)).size, model.items.length, 'row ids are unique');
});

test('large lists collapse folders unless remembered open or holding the current project', () => {
  const projects = fixture(RECENT_THRESHOLD + 2);
  const model = buildPickerModel(projects, { current: 'p0', dir: '/x/p0', nowMs: NOW });
  const byLabel = Object.fromEntries(model.sections.map((s) => [s.label, s.open]));
  assert.deepEqual(byLabel, { Recent: true, 'Clients/Acme': true, Internal: false, 'No folder': false });
  const remembered = buildPickerModel(projects, { current: 'p0', dir: '/x/p0', nowMs: NOW, open: { 'folder:Internal': true, 'folder:Clients/Acme': false, recent: false } });
  const again = Object.fromEntries(remembered.sections.map((s) => [s.label, s.open]));
  assert.deepEqual(again, { Recent: false, 'Clients/Acme': false, Internal: true, 'No folder': false });
});

test('never-opened projects do not fill the Recent group', () => {
  const projects = fixture(RECENT_THRESHOLD + 2).map((p, i) => (i > 1 ? { ...p, openedAt: 0 } : p));
  const model = buildPickerModel(projects, { current: 'zzz', dir: '/x', nowMs: NOW });
  assert.deepEqual(model.sections[0].items.map((i) => i.project.name), ['p0', 'p1']);
});

test('a query drops Recent, opens every group and keeps folder grouping of matches', () => {
  const projects = fixture(RECENT_THRESHOLD + 2);
  const model = buildPickerModel(projects, { query: 'acme', current: 'p0', dir: '/x', nowMs: NOW, open: { 'folder:Clients/Acme': false } });
  assert.ok(model.sections.every((s) => s.id !== 'recent'));
  assert.deepEqual(model.sections.map((s) => s.label), ['Clients/Acme']);
  assert.ok(model.sections.every((s) => s.open));
  assert.equal(model.matched, model.items.length);
  assert.ok(model.items.every((i) => i.match.folder.length));
  const none = buildPickerModel(projects, { query: 'nothing-here', current: 'p0', dir: '/x', nowMs: NOW });
  assert.equal(none.matched, 0);
  assert.equal(none.enterIndex, -1);
});

test('keyboard index math clamps and returns to the filter above the first row', () => {
  assert.equal(nextIndex(-1, 5, 'ArrowDown'), 0);
  assert.equal(nextIndex(0, 5, 'ArrowDown'), 1);
  assert.equal(nextIndex(4, 5, 'ArrowDown'), 4);
  assert.equal(nextIndex(0, 5, 'ArrowUp'), -1);
  assert.equal(nextIndex(3, 5, 'ArrowUp'), 2);
  assert.equal(nextIndex(3, 5, 'Home'), 0);
  assert.equal(nextIndex(1, 5, 'End'), 4);
  assert.equal(nextIndex(1, 20, 'PageDown'), 6);
  assert.equal(nextIndex(18, 20, 'PageDown'), 19);
  assert.equal(nextIndex(3, 20, 'PageUp'), 0);
  assert.equal(nextIndex(0, 0, 'ArrowDown'), -1);
  assert.equal(nextIndex(2, 5, 'x'), 2);
});

test('summary counts results for the live region', () => {
  assert.equal(pickerSummary({ total: 1, matched: 1, query: '' }), '1 other project');
  assert.equal(pickerSummary({ total: 12, matched: 12, query: '' }), '12 other projects');
  assert.equal(pickerSummary({ total: 0, matched: 0, query: '' }), 'No other projects');
  assert.equal(pickerSummary({ total: 12, matched: 3, query: 'acme' }), '3 of 12 match');
  assert.equal(pickerSummary({ total: 12, matched: 0, query: 'acme' }), 'No matches');
});

test('new project validation explains each problem inline', () => {
  const projects = [{ name: 'acme', path: '' }, { name: 'ext', path: '/srv/ext' }];
  const ok = (r) => r.ok;
  assert.equal(validateNewProject({ name: '', path: '' }, projects).field, 'name');
  assert.ok(ok(validateNewProject({ name: 'beta' }, projects)));
  const dup = validateNewProject({ name: ' acme ' }, projects);
  assert.equal(dup.ok, false);
  assert.equal(dup.field, 'name');
  assert.equal(dup.existing.name, 'acme');
  assert.match(dup.error, /already exists/);
  // A custom save location is its own project, even with a familiar name.
  assert.ok(ok(validateNewProject({ name: 'acme', path: '/srv/acme-2' }, projects)));
  assert.ok(ok(validateNewProject({ name: 'ext' }, projects)), 'an external folder name does not block a named project');
  for (const bad of ['a/b', 'a\\b', '~x', '-x', '.', '..']) {
    assert.equal(validateNewProject({ name: bad }, projects).field, 'name', bad);
  }
  assert.equal(validateNewProject({ name: '', path: 'relative/x' }, projects, 'posix').field, 'path');
  assert.ok(ok(validateNewProject({ name: '', path: '/srv/new' }, projects, 'posix')));
  assert.equal(validateNewProject({ name: 'x', folder: 'a/b/c/d' }, projects).field, 'folder');
  assert.equal(validateNewProject({ name: 'x', folder: 'a/../b' }, projects).field, 'folder');
  assert.equal(validateNewProject({ name: 'x', folder: 'a'.repeat(49) }, projects).field, 'folder');
  assert.ok(ok(validateNewProject({ name: 'x', folder: ' Clients / Acme ' }, projects)));
});
