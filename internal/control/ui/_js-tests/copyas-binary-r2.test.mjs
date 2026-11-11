// copyas.js: a request body that is not UTF-8 text must be copied byte for byte.
// Each generated command is executed (or its body literal evaluated) and the
// bytes it would send are compared with the captured ones.
import test from 'node:test';
import assert from 'node:assert/strict';
import http from 'node:http';
import { execFile, execFileSync } from 'node:child_process';
import { promisify } from 'node:util';
import * as copyas from '../js/copyas.js';

const { buildCopyAs, copyAs } = copyas;
const run = promisify(execFile);
const have = (cmd) => { try { execFileSync('/bin/sh', ['-c', 'command -v ' + cmd], { stdio: 'ignore' }); return true; } catch (e) { return false; } };

// Every byte value, a NUL, an invalid UTF-8 run, quotes and a backslash.
const BYTES = Uint8Array.from([...Array(256).keys(), 0x00, 0xff, 0xfe, 0x27, 0x22, 0x5c, 0x0a, 0x24, 0x60]);
const lossy = new TextDecoder().decode(BYTES);
const flow = (extra = {}) => ({
  id: 9, method: 'POST', scheme: 'http', host: '127.0.0.1', port: 80, path: '/upload', httpVersion: 'HTTP/1.1', status: 200,
  reqHeaders: { 'Content-Type': ['application/octet-stream'], 'Content-Length': [String(BYTES.length)] },
  resHeaders: { 'Content-Type': ['image/png'] }, reqBody: lossy, reqBodyBytes: BYTES, resBody: 'ok', ...extra,
});
const hex = (u8) => Buffer.from(u8).toString('hex');

test('decodeBody keeps text as text and flags bytes that are not UTF-8 text', () => {
  assert.equal(typeof copyas.decodeBody, 'function', 'copyas.js exports decodeBody');
  const enc = new TextEncoder();
  assert.deepEqual(copyas.decodeBody(enc.encode('héllo €')), { text: 'héllo €' });
  assert.deepEqual(copyas.decodeBody(new Uint8Array(0)), { text: '' });
  const bad = copyas.decodeBody(Uint8Array.from([0x61, 0xff, 0x62]));
  assert.equal(hex(bad.bytes), '61ff62');
  assert.equal(bad.text, 'a\ufffdb');
  // A NUL cannot travel in a shell argument, so it is binary too.
  assert.equal(hex(copyas.decodeBody(Uint8Array.from([0x61, 0x00])).bytes), '6100');
  // A 1 MB cut inside a multibyte character is still text: the partial character is dropped.
  const cut = enc.encode('ab€').slice(0, 4);
  assert.deepEqual(copyas.decodeBody(cut, true), { text: 'ab' });
  assert.equal(hex(copyas.decodeBody(cut, false).bytes), hex(cut), 'an untruncated partial sequence is invalid');
});

test('a text body is generated exactly as before', () => {
  const f = flow({ reqBody: 'a=1', reqBodyBytes: undefined });
  assert.ok(buildCopyAs('curl', [f]).text.includes("--data-raw 'a=1'"));
  assert.ok(buildCopyAs('python', [f]).text.includes('data="a=1".encode("utf-8"),'));
  assert.ok(buildCopyAs('go', [f]).text.includes('strings.NewReader("a=1")'));
  assert.deepEqual(buildCopyAs('curl', [f]).notices, []);
});

async function withServer(fn) {
  let got = null;
  const srv = http.createServer((req, res) => {
    const chunks = [];
    req.on('data', (c) => chunks.push(c));
    req.on('end', () => { got = { body: Buffer.concat(chunks), type: req.headers['content-type'], method: req.method }; res.end('ok'); });
  });
  await new Promise((r) => srv.listen(0, '127.0.0.1', r));
  try { await fn(srv.address().port); return got; } finally { srv.close(); }
}

test('curl (bash) sends the captured bytes', { skip: !have('curl') || !have('base64') }, async () => {
  const got = await withServer(async (port) => {
    const { text } = buildCopyAs('curl', [flow({ port })]);
    assert.ok(!text.includes('\ufffd'), 'no replacement characters in the command');
    assert.ok(text.includes('--data-binary @-'));
    await run('/bin/bash', ['-c', text]);
  });
  assert.equal(got.method, 'POST');
  assert.equal(got.type, 'application/octet-stream');
  assert.equal(hex(got.body), hex(BYTES));
});

test('httpie reads the same bytes from stdin', { skip: !have('base64') }, async () => {
  const { text } = buildCopyAs('httpie', [flow()]);
  assert.ok(!text.includes('--raw') && !text.includes('\ufffd'), text.slice(0, 200));
  // `http` is replaced by a function that dumps its stdin: the pipeline is what is under test.
  const { stdout } = await run('/bin/bash', ['-c', 'http() { od -An -v -tx1 | tr -d " \\n"; }\n' + text]);
  assert.equal(stdout, hex(BYTES));
});

test('fetch body evaluates to the captured bytes', () => {
  const { text } = buildCopyAs('fetch', [flow()]);
  const m = /\n  body: (.*),\n/.exec(text);
  assert.ok(m, text);
  assert.ok(!text.includes('\ufffd'));
  const body = new Function('return (' + m[1] + ')')();
  assert.ok(body instanceof Uint8Array);
  assert.equal(hex(body), hex(BYTES));
});

test('Python data literal evaluates to the captured bytes', { skip: !have('python3') }, async () => {
  const { text } = buildCopyAs('python', [flow()]);
  const m = /\n    data=(.*),\n/.exec(text);
  assert.ok(m, text);
  assert.ok(!m[1].includes('.encode('), 'bytes are not re-encoded as UTF-8');
  const { stdout } = await run('python3', ['-c', 'import sys\nv=' + m[1] + '\nassert isinstance(v, bytes)\nsys.stdout.write(v.hex())']);
  assert.equal(stdout, hex(BYTES));
});

test('Go body literal holds the captured bytes', () => {
  const { text } = buildCopyAs('go', [flow()]);
  const m = /strings\.NewReader\("((?:[^"\\]|\\.)*)"\)\)\n/.exec(text);
  assert.ok(m, text.slice(0, 400));
  // A Go interpreted string literal: \xNN, \\, \", \n, \r, \t, otherwise printable ASCII only.
  assert.match(m[1], /^(?:[\x20\x21\x23-\x5b\x5d-\x7e]|\\x[0-9a-f]{2}|\\[\\"nrt])*$/);
  const out = [];
  for (let i = 0; i < m[1].length; i++) {
    if (m[1][i] !== '\\') { out.push(m[1].charCodeAt(i)); continue; }
    const c = m[1][++i];
    if (c === 'x') { out.push(parseInt(m[1].slice(i + 1, i + 3), 16)); i += 2; } else out.push({ n: 10, r: 13, t: 9, '\\': 92, '"': 34 }[c]);
  }
  assert.equal(hex(Uint8Array.from(out)), hex(BYTES));
});

test('curl (PowerShell) writes the bytes to a file instead of a text argument', () => {
  const { text } = buildCopyAs('curl-ps', [flow()]);
  assert.ok(!text.includes('\ufffd') && !text.includes('--data-raw'));
  const m = /FromBase64String\('([A-Za-z0-9+/=]+)'\)/.exec(text);
  assert.ok(m, text.slice(0, 300));
  assert.equal(hex(Buffer.from(m[1], 'base64')), hex(BYTES));
  assert.match(text, /WriteAllBytes\(/);
  assert.match(text, /--data-binary "@\$/);
});

test('formats that are text only say that the body is not faithful', () => {
  for (const kind of ['raw', 'body', 'markdown']) {
    const { notices } = buildCopyAs(kind, [flow()]);
    assert.ok(notices.some((n) => /flow 9/.test(n) && /binary/i.test(n)), kind + ': ' + JSON.stringify(notices));
  }
  for (const kind of ['curl', 'curl-ps', 'fetch', 'python', 'go', 'httpie']) {
    assert.deepEqual(buildCopyAs(kind, [flow()]).notices, [], kind);
  }
});

test('HAR carries a binary response as base64 and counts sizes in bytes', () => {
  const png = Uint8Array.from([0x89, 0x50, 0x4e, 0x47, 0x00, 0xff]);
  const e = JSON.parse(buildCopyAs('har', [flow({ resBody: new TextDecoder().decode(png), resBodyBytes: png, reqBody: 'é€', reqBodyBytes: undefined })]).text);
  assert.equal(e.response.content.encoding, 'base64');
  assert.equal(hex(Buffer.from(e.response.content.text, 'base64')), hex(png));
  assert.equal(e.response.content.size, 6);
  assert.equal(e.response.bodySize, 6);
  assert.equal(e.request.bodySize, 5, 'UTF-8 bytes, not UTF-16 units');
  const t = JSON.parse(buildCopyAs('har', [flow({ reqBody: 'x', reqBodyBytes: undefined, resBody: 'é' })]).text);
  assert.equal(t.response.content.encoding, undefined);
  assert.equal(t.response.content.size, 2);
});

test('the repro document keeps the bytes next to the readable body', () => {
  const d = JSON.parse(buildCopyAs('repro', [flow()]).text);
  assert.equal(hex(Buffer.from(d.requests[0].bodyBase64, 'base64')), hex(BYTES));
});

test('copyAs carries fetched bytes into the generator', async () => {
  const copied = [];
  const deps = {
    fetchFlow: async (id) => ({ ...flow(), id, reqBody: undefined, reqBodyBytes: undefined }),
    fetchBody: async () => ({ ...copyas.decodeBody(BYTES), truncated: false }),
    copyText: (t) => copied.push(t), toast() {},
  };
  await copyAs('python', [{ id: 9 }], deps);
  assert.ok(copied[0] && !copied[0].includes('\ufffd'), String(copied[0]).slice(0, 200));
  assert.match(copied[0], /data=b"/);
});
