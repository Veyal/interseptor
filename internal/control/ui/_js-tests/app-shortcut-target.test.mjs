import { test } from 'node:test';
import assert from 'node:assert/strict';
import { isShortcutTypingTarget } from '../js/keys.js';

// A fake element: tag, and the ancestor selectors closest() should match.
const el = (tagName, matches = []) => ({
  tagName,
  closest: (sel) => (sel.split(',').some((s) => matches.includes(s.trim())) ? {} : null),
});

test('editable controls block the plain-letter workflow shortcuts', () => {
  assert.equal(isShortcutTypingTarget(el('TEXTAREA')), true);
  assert.equal(isShortcutTypingTarget(el('INPUT')), true);
  assert.equal(isShortcutTypingTarget(el('SELECT')), true);
  assert.equal(isShortcutTypingTarget({ tagName: 'DIV', isContentEditable: true, closest: () => null }), true);
});

test('a listbox with typeahead or a combobox blocks them', () => {
  assert.equal(isShortcutTypingTarget(el('DIV', ['[role="combobox"]'])), true);
  assert.equal(isShortcutTypingTarget(el('DIV', ['[role="listbox"]'])), true);
});

// Regression: F and D did nothing right after clicking a held item, because the
// hold queue is a [role=listbox] and only History's #rows was exempt.
test('the hold queue and History rows take the workflow shortcuts', () => {
  assert.equal(isShortcutTypingTarget(el('DIV', ['[role="listbox"]', '#heldList'])), false);
  assert.equal(isShortcutTypingTarget(el('DIV', ['[role="listbox"]', '#rows'])), false);
  assert.equal(isShortcutTypingTarget(null), false);
  assert.equal(isShortcutTypingTarget(el('BUTTON')), false);
});
