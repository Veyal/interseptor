import test from 'node:test';
import assert from 'node:assert/strict';
import { TITLE_MAX, titleLimitMessage, carryToFullForm } from '../js/finding-title-model.js';

test('a title at the cap says it was cut; shorter titles say nothing', () => {
  assert.equal(titleLimitMessage(TITLE_MAX - 1), '');
  assert.match(titleLimitMessage(TITLE_MAX), /limited to 300 characters/);
});

test('Full form receives the typed title and severity', () => {
  assert.deepEqual(carryToFullForm('  IDOR  ', 'High'), { title: 'IDOR', severity: 'High' });
  assert.deepEqual(carryToFullForm(undefined, ''), { title: '', severity: 'Medium' });
});
