import test from 'node:test';
import assert from 'node:assert/strict';
import {
  scanVars, varNames, buildScope, classifyVar, annotate, unresolvedIn, envDot, maskValue, pinMatches,
  sheetRows, changedKeys, toDeclared, SECRET_MASK,
} from '../js/varscope-model.js';

test('scanVars finds tokens, ignores escapes', () => {
  const t = scanVars('{{baseUrl}}/users/{{ id }}?x=\\{{literal}}&y={{$guid}}');
  assert.deepEqual(t.filter((x) => x.kind === 'var').map((x) => x.name), ['baseUrl', 'id', '$guid']);
  assert.equal(t.map((x) => x.text).join(''), '{{baseUrl}}/users/{{ id }}?x=\\{{literal}}&y={{$guid}}');
  assert.deepEqual(varNames('{{a}}{{a}}{{b|b64}}'), ['a', 'b']);
  assert.deepEqual(scanVars('').length, 0);
});

const SCOPE = buildScope([
  { scope: 'global', vars: [{ key: 'baseUrl', initialValue: 'https://global.example.com' }, { key: 'only_global', initialValue: 'x' }] },
  { scope: 'environment', vars: [{ key: 'baseUrl', initialValue: 'https://stg.example.com' }, { key: 'token', type: 'secret', hasCurrent: true }, { key: 'empty', initialValue: '' }, { key: 'off', initialValue: 'x', enabled: false }] },
  { scope: 'collection', vars: [{ key: 'baseUrl', initialValue: 'https://coll.example.com' }] },
]);

test('narrow layers win and record what they shadow', () => {
  const e = SCOPE.get('baseUrl');
  assert.equal(e.scope, 'environment');
  assert.deepEqual(e.shadows.sort(), ['collection', 'global']);
});

test('classification', () => {
  assert.equal(classifyVar('baseUrl', SCOPE), 'resolved');
  assert.equal(classifyVar('token', SCOPE), 'secret');
  assert.equal(classifyVar('empty', SCOPE), 'empty');
  assert.equal(classifyVar('off', SCOPE), 'unresolved');
  assert.equal(classifyVar('nope', SCOPE), 'unresolved');
  assert.equal(classifyVar('$randomInt', SCOPE), 'dynamic');
  assert.deepEqual(unresolvedIn('{{baseUrl}}/{{nope}}/{{empty}}/{{$guid}}', SCOPE), ['nope', 'empty']);
  assert.deepEqual(annotate('a {{token}}', SCOPE).map((t) => t.status || t.kind), ['text', 'secret']);
});

test('env dot priority: pin beats unresolved beats ok', () => {
  assert.equal(envDot({ hasEnv: true, pinBroken: true, unresolved: 2 }).state, 'danger');
  assert.equal(envDot({ hasEnv: true, unresolved: 2 }).state, 'warn');
  assert.equal(envDot({ hasEnv: true }).state, 'ok');
  assert.equal(envDot({ hasEnv: false }).state, 'none');
});

test('secrets are masked unless a human reveals them', () => {
  assert.equal(maskValue('hunter22', { secret: true }), SECRET_MASK);
  assert.equal(maskValue('hunter22', { secret: true, reveal: true }), 'hunter22');
  assert.equal(maskValue('plain'), 'plain');
  assert.equal(maskValue('', { secret: true }), '');
});

test('pin matching ignores scheme and path', () => {
  assert.equal(pinMatches('stg.example.com', 'stg.example.com'), true);
  assert.equal(pinMatches('https://stg.example.com/', 'stg.example.com'), true);
  assert.equal(pinMatches('stg.example.com', 'prod.example.com'), false);
  assert.equal(pinMatches('', 'anything'), true);
  assert.equal(pinMatches('stg.example.com', ''), true);
});

test('sheet rows never carry a secret value without reveal; declared body blanks secret initials', () => {
  const rows = sheetRows([{ key: 'token', type: 'secret', initialValue: 'leak', hasCurrent: true, current: 'hunter22' }, { key: 'a', initialValue: '1', hasCurrent: true, current: '2' }]);
  assert.equal(rows[0].initial, '');
  assert.equal(rows[0].current, SECRET_MASK);
  assert.equal(rows[1].current, '2');
  const body = toDeclared([{ key: ' token ', type: 'secret', initial: 'x', enabled: true }, { key: '', type: 'default' }]);
  assert.deepEqual(body, [{ key: 'token', type: 'secret', initialValue: '', enabled: true }]);
  assert.deepEqual(changedKeys([{ key: 'a', type: 'default', enabled: true, initial: '1' }], [{ key: 'a', type: 'default', enabled: true, initial: '2' }, { key: 'b', type: 'default', enabled: true, initial: '' }]), ['a', 'b']);
});
