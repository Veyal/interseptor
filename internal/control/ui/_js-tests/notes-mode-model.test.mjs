import test from 'node:test';
import assert from 'node:assert/strict';
import { NOTES_MODES, normalizeMode, toggleMode, initialMode, resolveLoadMode, caretForWord } from '../js/notes-mode-model.js';

test('modes are exactly view and edit', () => {
  assert.deepEqual(NOTES_MODES, ['view', 'edit']);
  assert.equal(normalizeMode('preview'), 'view');
  assert.equal(normalizeMode('edit'), 'edit');
  assert.equal(normalizeMode('nonsense'), 'view');
});

test('toggle flips between view and edit', () => {
  assert.equal(toggleMode('view'), 'edit');
  assert.equal(toggleMode('edit'), 'view');
  assert.equal(toggleMode('x'), 'edit');
});

test('empty or blank notes start in edit, non-empty in view', () => {
  assert.equal(initialMode(''), 'edit');
  assert.equal(initialMode(' \n\t'), 'edit');
  assert.equal(initialMode(null), 'edit');
  assert.equal(initialMode('# Notes'), 'view');
});

test('load picks the default only until the user chose a mode', () => {
  assert.equal(resolveLoadMode({ current: 'edit', userChosen: false, text: 'hello' }), 'view');
  assert.equal(resolveLoadMode({ current: 'view', userChosen: false, text: '' }), 'edit');
  assert.equal(resolveLoadMode({ current: 'edit', userChosen: true, text: 'hello' }), 'edit');
  assert.equal(resolveLoadMode({ current: 'view', userChosen: true, text: '' }), 'view');
});

test('caretForWord locates a double-clicked word in the source', () => {
  const src = '# Title\n\nSome **bold** text and more text';
  assert.equal(caretForWord(src, 'bold'), src.indexOf('bold'));
  assert.equal(caretForWord(src, ' text '), src.indexOf('text'));
  assert.equal(caretForWord(src, ''), null);
  assert.equal(caretForWord(src, 'missing'), null);
  assert.equal(caretForWord(src, 'x'.repeat(500)), null);
});
