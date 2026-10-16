import test from 'node:test';
import assert from 'node:assert/strict';
import {
  splitHeadBody, headerRows, filterHeaderRows, parseCookies, parseSetCookie,
  queryOf, parseFormEncoded, isFormUrlEncoded, multipartBoundary, parseMultipart,
  flattenJson, jsonPathAppend, tryParseJson, detectGraphQL,
} from '../js/inspector-structure.js';

test('splitHeadBody splits on the first blank line (CRLF or LF)', () => {
  assert.deepEqual(splitHeadBody('GET / HTTP/1.1\r\nHost: x\r\n\r\nbody=1'), { head: 'GET / HTTP/1.1\r\nHost: x', body: 'body=1' });
  assert.deepEqual(splitHeadBody('H: 1\n\n{"a":1}'), { head: 'H: 1', body: '{"a":1}' });
  assert.deepEqual(splitHeadBody('no blank line'), { head: 'no blank line', body: '' });
});

test('headerRows flattens {name:[values]} preserving order and duplicates', () => {
  assert.deepEqual(headerRows({ 'Content-Type': ['application/json'], 'Set-Cookie': ['a=1', 'b=2'] }), [
    { name: 'Content-Type', value: 'application/json' },
    { name: 'Set-Cookie', value: 'a=1' },
    { name: 'Set-Cookie', value: 'b=2' },
  ]);
  assert.deepEqual(headerRows(null), []);
});

test('filterHeaderRows matches name or value, case-insensitive', () => {
  const rows = [{ name: 'Content-Type', value: 'application/json' }, { name: 'X-Id', value: 'ABC' }];
  assert.equal(filterHeaderRows(rows, 'json').length, 1);
  assert.equal(filterHeaderRows(rows, 'x-id').length, 1);
  assert.equal(filterHeaderRows(rows, '').length, 2);
});

test('parseCookies splits a Cookie header into ordered pairs', () => {
  assert.deepEqual(parseCookies('sid=abc; theme=dark; flag'), [
    { name: 'sid', value: 'abc' }, { name: 'theme', value: 'dark' }, { name: 'flag', value: '' },
  ]);
  assert.deepEqual(parseCookies(''), []);
});

test('parseSetCookie separates the cookie from its attributes', () => {
  const c = parseSetCookie('sid=abc; Path=/; HttpOnly; SameSite=Lax');
  assert.equal(c.name, 'sid');
  assert.equal(c.value, 'abc');
  assert.deepEqual(c.attrs, [{ k: 'Path', v: '/' }, { k: 'HttpOnly', v: '' }, { k: 'SameSite', v: 'Lax' }]);
});

test('queryOf extracts the query string, and parseFormEncoded url-decodes pairs', () => {
  assert.equal(queryOf('/search?q=a+b&x=1'), 'q=a+b&x=1');
  assert.equal(queryOf('/no-query'), '');
  assert.deepEqual(parseFormEncoded('q=a+b&x=1&x=2&empty'), [
    { key: 'q', value: 'a b' }, { key: 'x', value: '1' }, { key: 'x', value: '2' }, { key: 'empty', value: '' },
  ]);
  assert.deepEqual(parseFormEncoded('name=%E2%9C%93'), [{ key: 'name', value: '✓' }]);
});

test('content-type classifiers', () => {
  assert.equal(isFormUrlEncoded('application/x-www-form-urlencoded; charset=utf-8'), true);
  assert.equal(isFormUrlEncoded('application/json'), false);
  assert.equal(multipartBoundary('multipart/form-data; boundary=----WebKitFormBoundaryX'), '----WebKitFormBoundaryX');
  assert.equal(multipartBoundary('application/json'), '');
});

test('parseMultipart extracts named parts and file metadata', () => {
  const b = '----B';
  const body = [
    '--' + b, 'Content-Disposition: form-data; name="field1"', '', 'value1',
    '--' + b, 'Content-Disposition: form-data; name="file"; filename="a.txt"', 'Content-Type: text/plain', '', 'FILEDATA',
    '--' + b + '--', '',
  ].join('\r\n');
  const parts = parseMultipart(body, b);
  assert.equal(parts.length, 2);
  assert.deepEqual({ name: parts[0].name, value: parts[0].value }, { name: 'field1', value: 'value1' });
  assert.equal(parts[1].filename, 'a.txt');
  assert.equal(parts[1].contentType, 'text/plain');
  assert.equal(parts[1].value, 'FILEDATA');
});

test('jsonPathAppend uses dot for identifiers and brackets otherwise', () => {
  assert.equal(jsonPathAppend('$', 'users'), '$.users');
  assert.equal(jsonPathAppend('$.users', 'first-name'), '$.users["first-name"]');
  assert.equal(jsonPathAppend('$', '0abc'), '$["0abc"]');
});

test('flattenJson walks in document order with correct paths and types', () => {
  const { rows } = flattenJson({ data: { users: [{ id: 7 }], ok: true }, note: null });
  // root object first
  assert.deepEqual({ depth: rows[0].depth, type: rows[0].type, path: rows[0].path }, { depth: 0, type: 'object', path: '$' });
  const byPath = Object.fromEntries(rows.map((r) => [r.path, r]));
  assert.equal(byPath['$.data.users'].type, 'array');
  assert.equal(byPath['$.data.users[0].id'].type, 'number');
  assert.equal(byPath['$.data.users[0].id'].value, 7);
  assert.equal(byPath['$.data.ok'].value, true);
  assert.equal(byPath['$.note'].type, 'null');
  // depth increases into nested containers
  assert.equal(byPath['$.data.users[0].id'].depth, 4);
});

test('flattenJson truncates at maxNodes', () => {
  const big = Array.from({ length: 50 }, (_, i) => i);
  const { rows, truncated } = flattenJson(big, { maxNodes: 10 });
  assert.equal(truncated, true);
  assert.ok(rows.length <= 10);
});

test('tryParseJson never throws', () => {
  assert.deepEqual(tryParseJson('{"a":1}'), { ok: true, value: { a: 1 } });
  assert.equal(tryParseJson('not json').ok, false);
});

test('detectGraphQL recognizes JSON GraphQL POSTs and splits variables', () => {
  const r = detectGraphQL(JSON.stringify({ query: 'query Me { me { id } }', variables: { x: 1 }, operationName: 'Me' }), 'application/json');
  assert.equal(r.isGraphQL, true);
  assert.equal(r.query, 'query Me { me { id } }');
  assert.equal(r.operationName, 'Me');
  assert.equal(r.variables, JSON.stringify({ x: 1 }, null, 2));
});

test('detectGraphQL handles application/graphql raw bodies and rejects plain JSON', () => {
  assert.equal(detectGraphQL('query { me { id } }', 'application/graphql').isGraphQL, true);
  assert.equal(detectGraphQL(JSON.stringify({ hello: 'world' }), 'application/json').isGraphQL, false);
  assert.equal(detectGraphQL('<html>', 'text/html').isGraphQL, false);
  // batched queries report the first operation
  const batch = detectGraphQL(JSON.stringify([{ query: 'query A { a }' }, { query: 'query B { b }' }]), 'application/json');
  assert.equal(batch.isGraphQL, true);
  assert.equal(batch.batched, true);
  assert.equal(batch.query, 'query A { a }');
});
