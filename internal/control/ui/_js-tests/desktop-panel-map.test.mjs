import test from 'node:test';
import assert from 'node:assert/strict';
import { activeFilterCount } from '../js/desktop-panel-map-model.js';

const defaults = { domain: '', method: '', status: '', tag: '', searchScope: 'path', hideNoise: true, collapseIdentical: true, onlyUnlinked: false };

test('the defaults are zero active filters', () => {
  assert.equal(activeFilterCount(defaults), 0);
  assert.equal(activeFilterCount({}), 0);
});

test('every filter that differs from its default counts once', () => {
  assert.equal(activeFilterCount({ ...defaults, domain: 'api.example.com' }), 1);
  assert.equal(activeFilterCount({ ...defaults, method: 'GET', status: '4' }), 2);
  assert.equal(activeFilterCount({ ...defaults, tag: 'auth', searchScope: 'body' }), 2);
  assert.equal(activeFilterCount({ ...defaults, hideNoise: false, collapseIdentical: false }), 2);
  assert.equal(activeFilterCount({ ...defaults, onlyUnlinked: true }), 1);
  assert.equal(activeFilterCount({ domain: 'a', method: 'GET', status: '2', tag: 't', searchScope: 'all', hideNoise: false, collapseIdentical: false, onlyUnlinked: true }), 8);
});
