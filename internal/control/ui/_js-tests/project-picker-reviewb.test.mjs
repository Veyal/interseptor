// project-picker-model.js — review finding (agent ui-js-b).
//
// The server treats the project name "default" case-insensitively as the root
// project (cmd/interseptor/project.go resolveProject, internal/control/project.go
// namedProjectDir / availableProjects all use strings.EqualFold(name, "default")).
// The picker compared names exactly, so "Default" or "DEFAULT" passed inline
// validation as a brand-new project; the restart then silently reopened the
// existing default project instead of creating one.
import test from 'node:test';
import assert from 'node:assert/strict';
import { validateNewProject } from '../js/project-picker-model.js';

const projects = [{ name: 'default', path: '' }, { name: 'acme', path: '' }];

test('a new project named like the reserved default project is reported as existing', () => {
  for (const name of ['Default', 'DEFAULT', ' dEfAuLt ']) {
    const r = validateNewProject({ name }, projects);
    assert.equal(r.ok, false, name);
    assert.equal(r.field, 'name');
    assert.equal(r.existing.name, 'default');
    assert.match(r.error, /already exists/);
  }
});

test('the reserved name is refused even when the list does not carry the default row', () => {
  const r = validateNewProject({ name: 'Default' }, [{ name: 'acme', path: '' }]);
  assert.equal(r.ok, false);
  assert.equal(r.field, 'name');
});

test('other names stay case-sensitive, and a custom location is unaffected', () => {
  assert.equal(validateNewProject({ name: 'Acme' }, projects).ok, true);
  assert.equal(validateNewProject({ name: 'defaults' }, projects).ok, true);
  assert.equal(validateNewProject({ name: 'Default', path: '/srv/engagements/default' }, projects).ok, true);
});
