// Review regressions for copyas.js: a captured request is hostile input, and the
// generated command is pasted into a shell.
import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import { buildCopyAs } from '../js/copyas.js';

const base = { id: 1, scheme: 'https', host: 'example.com', port: 443, path: '/', reqHeaders: { Accept: ['*/*'] } };

// An HTTP method is any RFC 9110 token, which includes ` $ & | ' ! * and more.
test('curl quotes a method that is not a plain word (bash)', () => {
  const { text } = buildCopyAs('curl', [{ ...base, method: 'X`id`$(id)&' }]);
  assert.ok(text.startsWith("curl -X 'X`ID`$(ID)&' 'https://example.com/'"), text);
  const quote = buildCopyAs('curl', [{ ...base, method: "A'B;C" }]).text;
  assert.ok(quote.startsWith("curl -X 'A'\\''B;C' 'https://example.com/'"), quote);
});

test('curl quotes a method that is not a plain word (PowerShell)', () => {
  const { text } = buildCopyAs('curl-ps', [{ ...base, method: "A;B'$(calc)" }]);
  assert.ok(text.startsWith("curl.exe -X 'A;B''$(CALC)' 'https://example.com/'"), text);
});

test('httpie quotes a method that is not a plain word', () => {
  const { text } = buildCopyAs('httpie', [{ ...base, method: 'A|B`x`' }]);
  assert.ok(text.startsWith("http 'A|B`X`' 'https://example.com/'"), text);
});

test('ordinary methods keep their unquoted form', () => {
  assert.ok(buildCopyAs('curl', [{ ...base, method: 'POST' }]).text.startsWith("curl -X POST 'https://example.com/'"));
  assert.ok(buildCopyAs('curl', [{ ...base, method: 'M-SEARCH' }]).text.startsWith("curl -X M-SEARCH '"));
  assert.ok(buildCopyAs('curl-ps', [{ ...base, method: 'PUT' }]).text.startsWith("curl.exe -X PUT '"));
  assert.ok(buildCopyAs('httpie', [{ ...base, method: 'DELETE' }]).text.startsWith("http DELETE '"));
  assert.ok(buildCopyAs('curl', [{ ...base, method: 'GET' }]).text.startsWith("curl 'https://example.com/'"));
});

test("History's Copy as cURL quotes the method too", () => {
  const proxy = fs.readFileSync(new URL('../js/proxy.js', import.meta.url), 'utf8');
  const start = proxy.indexOf('export async function copyCurl(f)');
  const fn = proxy.slice(start, proxy.indexOf('\n}\n', start));
  assert.ok(start > 0 && fn.length > 0);
  assert.ok(!fn.includes("'-X '+f.method"), 'the raw method is not concatenated into the command');
  assert.ok(fn.includes('shellWord(f.method)'), 'the method goes through the shell-word quoter');
  const helper = proxy.slice(proxy.indexOf('function shq(s)'), proxy.indexOf('\n', proxy.indexOf('function shellWord(')));
  const { shellWord } = new Function(helper + '\nreturn {shellWord};')();
  assert.equal(shellWord('POST'), 'POST');
  assert.equal(shellWord('X`id`'), "'X`id`'");
  assert.equal(shellWord("a'b"), "'a'\\''b'");
  assert.equal(shellWord(''), "''");
});

test('Go snippet keeps every value of a repeated header', () => {
  const { text } = buildCopyAs('go', [{ ...base, method: 'GET', reqHeaders: { 'X-Dup': ['one', 'two'], Accept: ['*/*'] } }]);
  assert.ok(text.includes('req.Header.Set("X-Dup", "one")'), text);
  assert.ok(text.includes('req.Header.Add("X-Dup", "two")'), text);
  assert.ok(text.includes('req.Header.Set("Accept", "*/*")'), text);
  // Names that differ only in case are the same header to net/http.
  const mixed = buildCopyAs('go', [{ ...base, method: 'GET', reqHeaders: { 'x-dup': ['one'], 'X-Dup': ['two'] } }]).text;
  assert.ok(mixed.includes('req.Header.Set("x-dup", "one")') && mixed.includes('req.Header.Add("X-Dup", "two")'), mixed);
});

test('raw HTTP Host keeps a port that is not the default for the scheme', () => {
  const host = (f) => buildCopyAs('raw', [{ ...base, method: 'GET', ...f }]).text.split('\r\n')[1];
  assert.equal(host({ scheme: 'https', port: 443 }), 'Host: example.com');
  assert.equal(host({ scheme: 'http', port: 80 }), 'Host: example.com');
  assert.equal(host({ scheme: 'https', port: 80 }), 'Host: example.com:80');
  assert.equal(host({ scheme: 'http', port: 443 }), 'Host: example.com:443');
  assert.equal(host({ scheme: 'http', port: 8080 }), 'Host: example.com:8080');
  assert.equal(host({ scheme: 'https', port: '443' }), 'Host: example.com');
  assert.equal(host({ scheme: 'http', host: '::1', port: 8080 }), 'Host: [::1]:8080');
});
