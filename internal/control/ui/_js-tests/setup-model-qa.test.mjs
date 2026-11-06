import test from 'node:test';
import assert from 'node:assert/strict';
import { setupBootAction, setupEnterAction, SETUP_LAST } from '../js/setup-model.js';

test('setupBootAction', () => {
  assert.equal(setupBootAction({ done: true, hasFlows: true, scopeRules: 3 }), 'skip');
  assert.equal(setupBootAction({ done: false, hasFlows: false, scopeRules: null }), 'open');
  assert.equal(setupBootAction({ done: false, hasFlows: true, scopeRules: 2 }), 'mark-done');
  assert.equal(setupBootAction({ done: false, hasFlows: true, scopeRules: 0 }), 'open');
  assert.equal(setupBootAction({ done: false, hasFlows: true, scopeRules: null }), 'skip');
});

test('Enter presses the step action that owns the typed input, never Next', () => {
  // Steps 2 (scope host) and 3 (authorised target) each have a text input with
  // its own button. Advancing to Next instead dropped what was typed, and the
  // last step still reported the configuration as saved.
  assert.equal(setupEnterAction(2), 'setupScopeAdd');
  assert.equal(setupEnterAction(3), 'setupTargetSave');
  // The CA step's checkbox gates Next; Enter must not bypass it.
  assert.equal(setupEnterAction(1), '');
  // The steps without an input keep plain advance.
  assert.equal(setupEnterAction(0), 'setupNext');
  assert.equal(setupEnterAction(SETUP_LAST), 'setupNext');
  assert.equal(SETUP_LAST, 4, 'the wizard shows five steps');
});
