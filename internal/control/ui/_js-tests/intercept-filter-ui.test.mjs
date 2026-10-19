import { test } from 'node:test';
import assert from 'node:assert/strict';
import { filterCheckboxChecked, filterErrorText } from '../js/intercept-model.js';

// Regression: clearing the pattern unchecked "Only hold when" (the server stores
// an empty filter as off), and typing a new pattern then saved it switched off.
test('an empty pattern keeps the operator\'s checkbox choice', () => {
  assert.equal(filterCheckboxChecked({ filterEnabled: false, filterPattern: '' }, true), true);
  assert.equal(filterCheckboxChecked({ filterEnabled: false, filterPattern: '' }, false), false);
  assert.equal(filterCheckboxChecked({ filterEnabled: false, filterPattern: 'x' }, true), false);
  assert.equal(filterCheckboxChecked({ filterEnabled: true, filterPattern: 'x' }, false), true);
});

// Regression: an invalid regex only toasted; the field said one thing and the
// server enforced another. The field keeps the text and says what is in force.
test('an invalid pattern names the rule still in force', () => {
  assert.equal(filterErrorText('invalid regex: missing closing )', { filterEnabled: true, filterPattern: 'admin' }), 'Not saved — invalid regex: missing closing ). Still holding only when it matches admin.');
  assert.equal(filterErrorText('invalid regex: x', { filterEnabled: false, filterPattern: '' }), 'Not saved — invalid regex: x. No hold filter is in force.');
});
