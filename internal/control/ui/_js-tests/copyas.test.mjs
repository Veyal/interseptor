import test from 'node:test';
import assert from 'node:assert/strict';
import { buildCopyAs, COPY_AS_KINDS, flowURL, truncateBody, copyAs, MAX_BODY_BYTES, redactHeaders } from '../js/copyas.js';

const flow = {
  id: 7, method: 'POST', scheme: 'https', host: 'api.example.com', port: 443, path: '/v1/items?x=1', httpVersion: 'HTTP/1.1', status: 201,
  reqHeaders: { 'Content-Type': ['application/json'], Authorization: ['Bearer secret-token'], Cookie: ['sid=abc'], 'Content-Length': ['13'] },
  resHeaders: { 'Content-Type': ['application/json'], 'Set-Cookie': ['sid=new'] },
  reqBody: '{"name":"it\'s"}', resBody: '{"ok":true}',
};

test('flowURL omits default ports and keeps custom ones', () => {
  assert.equal(flowURL(flow), 'https://api.example.com/v1/items?x=1');
  assert.equal(flowURL({ ...flow, scheme: 'http', port: 8080 }), 'http://api.example.com:8080/v1/items?x=1');
});

test('every documented kind is produced', () => {
  const kinds = COPY_AS_KINDS.map((k) => k.kind);
  assert.deepEqual(kinds, ['curl', 'curl-ps', 'fetch', 'python', 'go', 'httpie', 'raw', 'har', 'har-bundle', 'url', 'headers', 'body', 'markdown', 'repro']);
  for (const k of kinds) {
    const out = buildCopyAs(k, [flow]);
    assert.ok(out.text.length > 0, k);
  }
});

test('curl escapes single quotes, includes method, headers and body, redaction off by default', () => {
  const { text } = buildCopyAs('curl', [flow]);
  assert.ok(text.startsWith("curl -X POST 'https://api.example.com/v1/items?x=1'"));
  assert.ok(text.includes("-H 'Authorization: Bearer secret-token'"));
  assert.ok(text.includes("--data-raw '{\"name\":\"it'\\''s\"}'"));
  assert.ok(!text.includes('Content-Length'));
});

test('curl with redaction masks Authorization and Cookie', () => {
  const { text } = buildCopyAs('curl', [flow], { redact: true });
  assert.ok(!text.includes('secret-token') && !text.includes('sid=abc'));
  assert.ok(text.includes('Authorization: <redacted>'));
});

test('powershell curl uses Invoke-WebRequest-safe quoting', () => {
  const { text } = buildCopyAs('curl-ps', [flow]);
  assert.ok(text.startsWith('curl.exe -X POST'));
  assert.ok(text.includes("it''s"));
});

test('fetch, python, go, httpie carry method url headers and body', () => {
  const f = buildCopyAs('fetch', [flow]).text;
  assert.ok(f.includes('fetch("https://api.example.com/v1/items?x=1"') && f.includes('method: "POST"') && f.includes('"Authorization": "Bearer secret-token"'));
  const p = buildCopyAs('python', [flow]).text;
  assert.ok(p.includes('import requests') && p.includes('requests.request(') && p.includes('"POST"') && p.includes('data='));
  const g = buildCopyAs('go', [flow]).text;
  assert.ok(g.includes('http.NewRequest("POST"') && g.includes('strings.NewReader(') && g.includes('req.Header.Set("Authorization"'));
  const h = buildCopyAs('httpie', [flow]).text;
  assert.ok(h.startsWith('http POST ') && h.includes('Authorization:'));
});

test('raw http renders request line, headers and body', () => {
  const { text } = buildCopyAs('raw', [flow]);
  assert.ok(text.startsWith('POST /v1/items?x=1 HTTP/1.1\r\nHost: api.example.com\r\n'));
  assert.ok(text.endsWith('\r\n\r\n{"name":"it\'s"}'));
});

test('har produces a valid HAR 1.2 entry and a bundle for several flows', () => {
  const entry = JSON.parse(buildCopyAs('har', [flow]).text);
  assert.equal(entry.request.method, 'POST');
  assert.equal(entry.request.url, 'https://api.example.com/v1/items?x=1');
  assert.equal(entry.response.status, 201);
  assert.equal(entry.request.postData.text, flow.reqBody);
  const bundle = JSON.parse(buildCopyAs('har-bundle', [flow, { ...flow, id: 8 }]).text);
  assert.equal(bundle.log.version, '1.2');
  assert.equal(bundle.log.entries.length, 2);
});

test('url, headers and body kinds and multi-flow joins', () => {
  assert.equal(buildCopyAs('url', [flow, flow]).text, 'https://api.example.com/v1/items?x=1\nhttps://api.example.com/v1/items?x=1');
  assert.ok(buildCopyAs('headers', [flow]).text.startsWith('Content-Type: application/json'));
  assert.equal(buildCopyAs('body', [flow]).text, flow.reqBody);
  assert.equal(buildCopyAs('body', [flow], { side: 'res' }).text, flow.resBody);
});

test('markdown redacts secrets by default and can be switched off', () => {
  const md = buildCopyAs('markdown', [flow]).text;
  assert.ok(md.includes('```http') && md.includes('POST https://api.example.com/v1/items?x=1'));
  assert.ok(!md.includes('secret-token') && md.includes('<redacted>'));
  assert.ok(buildCopyAs('markdown', [flow], { redact: false }).text.includes('secret-token'));
});

test('repro is a replayable json document', () => {
  const doc = JSON.parse(buildCopyAs('repro', [flow]).text);
  assert.equal(doc.interseptor_repro, 1);
  assert.equal(doc.requests[0].method, 'POST');
  assert.equal(doc.requests[0].url, 'https://api.example.com/v1/items?x=1');
});

test('redactHeaders only masks sensitive headers', () => {
  const out = redactHeaders({ Authorization: ['x'], 'Proxy-Authorization': ['y'], Cookie: ['c'], Accept: ['*/*'] });
  assert.deepEqual(out, { Authorization: ['<redacted>'], 'Proxy-Authorization': ['<redacted>'], Cookie: ['<redacted>'], Accept: ['*/*'] });
});

test('bodies over 1MB are truncated with an explicit notice', () => {
  const big = 'a'.repeat(MAX_BODY_BYTES + 10);
  const t = truncateBody(big);
  assert.equal(t.truncated, true);
  assert.equal(t.text.length, MAX_BODY_BYTES);
  assert.equal(truncateBody('small').truncated, false);
  const out = buildCopyAs('curl', [{ ...flow, reqBody: big, reqBodyTruncated: true }]);
  assert.ok(out.notices.some((n) => /truncated/i.test(n)));
  assert.ok(out.text.includes('# body truncated at 1 MB'));
});

test('copyAs fetches bodies lazily only for kinds that need them and reports size', async () => {
  const fetched = [];
  const copied = [];
  const toasts = [];
  const deps = {
    fetchFlow: async (id) => ({ ...flow, id, reqBody: undefined, resBody: undefined }),
    fetchBody: async (id, side) => { fetched.push(`${id}:${side}`); return { text: side === 'req' ? 'REQ' : 'RES', truncated: false }; },
    copyText: (t, m) => copied.push([t, m]),
    toast: (m) => toasts.push(m),
  };
  await copyAs('url', [flow], deps);
  assert.deepEqual(fetched, []);
  await copyAs('curl', [{ id: 7 }], deps);
  assert.deepEqual(fetched, ['7:req']);
  assert.ok(copied[1][0].includes("--data-raw 'REQ'"));
  assert.match(copied[1][1], /copied .*B/);
  fetched.length = 0;
  await copyAs('har', [{ id: 7 }], deps);
  assert.deepEqual(fetched.sort(), ['7:req', '7:res']);
});

test('copyAs reports failure through toast and never throws', async () => {
  const toasts = [];
  await copyAs('curl', [{ id: 1 }], { fetchFlow: async () => { throw new Error('boom'); }, fetchBody: async () => ({ text: '' }), copyText() {}, toast: (m) => toasts.push(m) });
  assert.ok(toasts.some((m) => /boom/.test(m)));
});
