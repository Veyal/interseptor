import test from 'node:test';
import assert from 'node:assert/strict';
import { sheetRows, retypeRow, SECRET_MASK } from '../js/varscope-model.js';

test('switching a secret variable to plain does not expose the mask as its value', () => {
  const [row] = sheetRows([{ key: 'token', type: 'secret', initialValue: '', hasCurrent: true, current: 'hunter22' }]);
  assert.equal(row.current, SECRET_MASK, 'the sheet only ever holds a mask for a secret');
  assert.equal(row.secret, true);
  retypeRow(row, 'default');
  assert.equal(row.secret, false);
  assert.equal(row.type, 'default');
  // The mask was a label, not the value: the Current value cell must start empty
  // again instead of offering "[secret]" for editing (and for saving back).
  assert.equal(row.current, '');
  assert.equal(row.hasCurrent, true, 'the placeholder can still say a value is stored');
});

test('retypeRow keeps a plain current value when the row stays plain or becomes secret', () => {
  const plain = { key: 'a', type: 'default', secret: false, current: 'v', hasCurrent: true };
  retypeRow(plain, 'default');
  assert.equal(plain.current, 'v');
  retypeRow(plain, 'secret');
  assert.equal(plain.secret, true);
  assert.equal(plain.current, 'v', 'nothing is lost when the direction is the harmless one');
});

test('retypeRow treats anything other than "secret" as the default type', () => {
  const row = { key: 'a', type: 'secret', secret: true, current: '', hasCurrent: false };
  retypeRow(row, 'bogus');
  assert.equal(row.type, 'default');
  assert.equal(row.secret, false);
});
