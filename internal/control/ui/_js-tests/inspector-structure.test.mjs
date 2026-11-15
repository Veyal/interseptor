import test from 'node:test';
import assert from 'node:assert/strict';
import {
  splitHeadBody, headerRows, filterHeaderRows, parseCookies, parseSetCookie,
  queryOf, parseFormEncoded, isFormUrlEncoded, multipartBoundary, parseMultipart,
  flattenJson, jsonPathAppend, tryParseJson, detectGraphQL,
  isJsonInspectorBody, isDecodedAvailable, inspectorViewAvailability,
  needsBackgroundBodyProbe, fallbackInspectorView,
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

test('tryParseJson strips UTF-8 BOM and isJsonInspectorBody gates primitives by Content-Type', () => {
  assert.deepEqual(tryParseJson('\uFEFF{"ok":true}'), { ok: true, value: { ok: true } });
  assert.equal(isJsonInspectorBody('\uFEFF{"ok":true}', 'text/plain'), true);
  assert.equal(isJsonInspectorBody('[1,2]', ''), true);
  assert.equal(isJsonInspectorBody('true', 'application/json'), true);
  assert.equal(isJsonInspectorBody('null', 'application/problem+json'), true);
  assert.equal(isJsonInspectorBody('123', 'text/plain'), false);
  assert.equal(isJsonInspectorBody('   ', 'application/json'), false);
  assert.equal(isJsonInspectorBody('{broken', 'application/json'), false);
});

test('isDecodedAvailable requires a matched codec with non-empty output', () => {
  assert.equal(isDecodedAvailable(null), false);
  assert.equal(isDecodedAvailable({ matched: false, plaintext: 'x' }), false);
  assert.equal(isDecodedAvailable({ matched: true, plaintext: '', fields: [] }), false);
  assert.equal(isDecodedAvailable({ matched: true, plaintext: 'hello' }), true);
  assert.equal(isDecodedAvailable({ matched: true, fields: [{ name: 'k', value: 'v' }] }), true);
  assert.equal(isDecodedAvailable({ matched: true, note: 'hint' }), true);
  assert.equal(isDecodedAvailable({ matched: true, error: 'decode failed' }), true);
});

test('inspectorViewAvailability hides empty structured/decoded views on GET and empty headers', () => {
  const getAvail = inspectorViewAvailability({
    side: 'req',
    detail: { path: '/items', reqHeaders: { Host: ['example.com'] }, reqLen: 0 },
    raw: 'GET /items HTTP/1.1\r\nHost: example.com\r\n\r\n',
    mime: '',
    binary: false,
    renderCap: 2048 * 1024,
    decodedMatched: false,
  });
  assert.deepEqual(getAvail.visible, {
    raw: true,
    pretty: true,
    headers: true,
    json: false,
    params: false,
    gql: false,
    decoded: false,
    hex: true,
  });
  assert.equal(getAvail.anyVisible, true);

  const noHeaders = inspectorViewAvailability({
    side: 'res',
    detail: { resHeaders: {}, resLen: 4 },
    raw: 'HTTP/1.1 200 OK\r\n\r\ntest',
    mime: 'text/plain',
    binary: false,
    renderCap: 2048 * 1024,
    decodedMatched: false,
  });
  assert.equal(noHeaders.visible.headers, false);
  assert.equal(noHeaders.visible.json, false);
  assert.equal(noHeaders.visible.render, false);
  assert.equal(noHeaders.visible.decoded, false);
  assert.equal(noHeaders.visible.pretty, true);
});

test('inspectorViewAvailability enables params, json, gql, decoded, and render only when populated', () => {
  const queryReq = inspectorViewAvailability({
    side: 'req',
    detail: { path: '/search?q=1', reqHeaders: { Host: ['example.com'] }, reqLen: 0 },
    raw: null,
    mime: '',
    binary: false,
    renderCap: 2048 * 1024,
    decodedMatched: false,
  });
  assert.equal(queryReq.visible.params, true);
  assert.equal(queryReq.pending.params, false);
  assert.equal(queryReq.visible.json, false);
  assert.equal(queryReq.visible.gql, false);

  const formEmpty = inspectorViewAvailability({
    side: 'req',
    detail: { path: '/submit', reqHeaders: { Host: ['example.com'] }, reqLen: 0 },
    raw: 'POST /submit HTTP/1.1\r\nContent-Type: application/x-www-form-urlencoded\r\n\r\n',
    mime: 'application/x-www-form-urlencoded',
    binary: false,
    renderCap: 2048 * 1024,
    decodedMatched: false,
  });
  assert.equal(formEmpty.visible.params, false);

  const multipartPopulated = inspectorViewAvailability({
    side: 'req',
    detail: { path: '/upload', reqHeaders: { Host: ['example.com'] }, reqLen: 120 },
    raw: 'POST /upload HTTP/1.1\r\n\r\n--B\r\nContent-Disposition: form-data; name="a"\r\n\r\n1\r\n--B--',
    mime: 'multipart/form-data; boundary=B',
    binary: false,
    renderCap: 2048 * 1024,
    decodedMatched: false,
  });
  assert.equal(multipartPopulated.visible.params, true);

  const gqlJson = inspectorViewAvailability({
    side: 'req',
    detail: { path: '/graphql', reqHeaders: { Host: ['example.com'] }, reqLen: 40 },
    raw: 'POST /graphql HTTP/1.1\r\n\r\n{"query":"query{me{id}}"}',
    mime: 'application/json',
    binary: false,
    renderCap: 2048 * 1024,
    decodedMatched: true,
  });
  assert.equal(gqlJson.visible.json, true);
  assert.equal(gqlJson.visible.gql, true);
  assert.equal(gqlJson.visible.decoded, true);

  const gqlRaw = inspectorViewAvailability({
    side: 'req',
    detail: { path: '/graphql', reqHeaders: { Host: ['example.com'] }, reqLen: 15 },
    raw: 'POST /graphql HTTP/1.1\r\n\r\nquery { me { id } }',
    mime: 'application/graphql',
    binary: false,
    renderCap: 2048 * 1024,
    decodedMatched: false,
  });
  assert.equal(gqlRaw.visible.json, false);
  assert.equal(gqlRaw.visible.gql, true);

  const htmlEmpty = inspectorViewAvailability({
    side: 'res',
    detail: { resHeaders: { 'Content-Type': ['text/html'] }, resLen: 0 },
    raw: 'HTTP/1.1 304 Not Modified\r\nContent-Type: text/html\r\n\r\n',
    mime: 'text/html',
    binary: false,
    renderCap: 2048 * 1024,
    decodedMatched: false,
  });
  assert.equal(htmlEmpty.visible.render, false);

  const htmlNonEmpty = inspectorViewAvailability({
    side: 'res',
    detail: { resHeaders: { 'Content-Type': ['text/html'] }, resLen: 12 },
    raw: 'HTTP/1.1 200 OK\r\n\r\n<h1>hi</h1>',
    mime: 'text/html',
    binary: false,
    renderCap: 2048 * 1024,
    decodedMatched: false,
  });
  assert.equal(htmlNonEmpty.visible.render, true);
});

test('inspectorViewAvailability handles binary, over-cap, missing-message, pending, and fallback states', () => {
  const binReq = inspectorViewAvailability({
    side: 'req',
    detail: { path: '/img?size=2x', reqHeaders: { Host: ['example.com'] }, reqLen: 512 },
    raw: null,
    mime: 'image/png',
    binary: true,
    renderCap: 2048 * 1024,
    decodedMatched: null,
  });
  assert.equal(binReq.visible.json, false);
  assert.equal(binReq.visible.gql, false);
  assert.equal(binReq.visible.params, true);
  assert.equal(binReq.pending.decoded, true);

  const overCapJson = inspectorViewAvailability({
    side: 'res',
    detail: { resHeaders: { 'Content-Type': ['application/json'] }, resLen: 5000000 },
    raw: null,
    mime: 'application/json',
    binary: false,
    renderCap: 2048 * 1024,
    decodedMatched: false,
  });
  assert.equal(overCapJson.visible.json, true);
  assert.equal(overCapJson.pending.json, false);

  const noMsg = inspectorViewAvailability({
    side: 'res',
    detail: { resHeaders: {}, resLen: 0 },
    raw: null,
    mime: '',
    binary: false,
    renderCap: 2048 * 1024,
    decodedMatched: false,
    hasMessage: false,
  });
  assert.equal(noMsg.anyVisible, false);
  assert.equal(noMsg.visible.pretty, false);
  assert.equal(noMsg.visible.raw, false);

  assert.equal(fallbackInspectorView({ pretty: false, raw: true, headers: true }), 'raw');
  assert.equal(fallbackInspectorView({ visible: { pretty: true, raw: true } }), 'pretty');
});

test('needsBackgroundBodyProbe avoids fetching HTML/CSS/binary/empty bodies on Headers tab', () => {
  const cap = 2048 * 1024;
  assert.equal(needsBackgroundBodyProbe({ side: 'req', detail: { path: '/', reqLen: 0 }, mime: '', binary: false, renderCap: cap, rawKnown: false }), false);
  assert.equal(needsBackgroundBodyProbe({ side: 'res', detail: { resLen: 4000 }, mime: 'text/html', binary: false, renderCap: cap, rawKnown: false }), false);
  assert.equal(needsBackgroundBodyProbe({ side: 'res', detail: { resLen: 4000 }, mime: 'text/css', binary: false, renderCap: cap, rawKnown: false }), false);
  assert.equal(needsBackgroundBodyProbe({ side: 'res', detail: { resLen: 4000 }, mime: 'application/javascript', binary: false, renderCap: cap, rawKnown: false }), false);
  assert.equal(needsBackgroundBodyProbe({ side: 'res', detail: { resLen: 4000 }, mime: 'image/png', binary: true, renderCap: cap, rawKnown: false }), false);
  assert.equal(needsBackgroundBodyProbe({ side: 'req', detail: { path: '/api', reqLen: 40 }, mime: 'application/json', binary: false, renderCap: cap, rawKnown: true }), false);
  assert.equal(needsBackgroundBodyProbe({ side: 'req', detail: { path: '/api', reqLen: 40 }, mime: 'application/json', binary: false, renderCap: cap, rawKnown: false }), true);
  assert.equal(needsBackgroundBodyProbe({ side: 'res', detail: { resLen: 40 }, mime: 'text/plain', binary: false, renderCap: cap, rawKnown: false }), true);
  assert.equal(needsBackgroundBodyProbe({ side: 'req', detail: { path: '/post', reqLen: 40 }, mime: 'application/x-www-form-urlencoded', binary: false, renderCap: cap, rawKnown: false }), true);
});
