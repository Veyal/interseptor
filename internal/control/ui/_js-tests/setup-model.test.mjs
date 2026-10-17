import test from 'node:test';
import assert from 'node:assert/strict';
import { setupBootAction } from '../js/setup-model.js';

test('a finished wizard never reopens', () => {
  assert.equal(setupBootAction({ done: true, hasFlows: false, scopeRules: 0 }), 'skip');
});

test('an empty project opens the wizard', () => {
  assert.equal(setupBootAction({ done: false, hasFlows: false, scopeRules: null }), 'open');
});

test('traffic without scope still opens the wizard', () => {
  assert.equal(setupBootAction({ done: false, hasFlows: true, scopeRules: 0 }), 'open');
});

test('traffic with scope rules counts as already set up', () => {
  assert.equal(setupBootAction({ done: false, hasFlows: true, scopeRules: 2 }), 'mark-done');
});

test('unreadable scope with traffic stays out of the way', () => {
  assert.equal(setupBootAction({ done: false, hasFlows: true, scopeRules: null }), 'skip');
});
