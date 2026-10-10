import test from 'node:test';
import assert from 'node:assert/strict';
import { envNameError, copyName, createBody, duplicateBody, renameBody, deleteWarning } from '../js/collections-env-model.js';

const others = [{ name: 'Staging' }, { name: 'Staging copy' }];

test('names must be non-empty, short and unique ignoring case', () => {
  assert.match(envNameError('  ', others), /name/);
  assert.match(envNameError('x'.repeat(81), others), /80/);
  assert.match(envNameError('staging', others), /already exists/);
  assert.equal(envNameError('Production', others), '');
});

test('copyName skips taken copies', () => {
  assert.equal(copyName('Staging', others), 'Staging copy 2');
  assert.equal(copyName('Prod', others), 'Prod copy');
});

test('createBody scopes to the collection and trims', () => {
  assert.deepEqual(createBody('  Dev ', 'c1'), { name: 'Dev', kind: 'env', collectionUid: 'c1' });
});

test('duplicateBody never carries secret initial values or current values', () => {
  const b = duplicateBody({ boundIdentity: 'alice', baseTargetPin: 'api.example.com', variables: [
    { key: 'baseUrl', type: 'default', initialValue: 'https://api.example.com', enabled: true, current: 'x' },
    { key: 'token', type: 'secret', initialValue: 'leak', enabled: true, current: 'cur-s3cret' },
  ] }, 'Dev copy', 'c1');
  assert.equal(b.variables[1].initialValue, '');
  assert.equal(b.variables[0].initialValue, 'https://api.example.com');
  assert.ok(!JSON.stringify(b).includes('cur-s3cret'));
  assert.equal(b.boundIdentity, 'alice');
});

test('renameBody keeps pin, binding and rev so the PUT does not clear them', () => {
  const b = renameBody({ kind: 'env', collectionUid: 'c1', boundIdentity: 'a', baseTargetPin: 'p', rev: 4 }, ' New ');
  assert.deepEqual(b, { name: 'New', kind: 'env', collectionUid: 'c1', boundIdentity: 'a', baseTargetPin: 'p', rev: 4 });
});

test('deleteWarning names the env and says variables go with it', () => {
  const w = deleteWarning({ name: 'Dev', variables: [{}, {}] });
  assert.equal(w.name, 'Dev');
  assert.match(w.tail, /2 variables/);
});
