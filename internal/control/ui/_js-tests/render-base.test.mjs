import test from 'node:test';
import assert from 'node:assert/strict';
import { withBaseHref } from '../js/render-base.js';

const U = 'https://example.com/a/b.html?x=1';

test('injects base right after <head>', () => {
  assert.equal(withBaseHref('<html><head><title>t</title></head><body>x</body></html>', U),
    '<html><head><base href="https://example.com/a/b.html?x=1"><title>t</title></head><body>x</body></html>');
});
test('without head, goes after <html> or doctype, else is prepended', () => {
  assert.equal(withBaseHref('<!DOCTYPE html><html lang="en"><body>x', U).includes('<html lang="en"><base href='), true);
  assert.equal(withBaseHref('<!DOCTYPE html><p>x', U), '<!DOCTYPE html><base href="https://example.com/a/b.html?x=1"><p>x');
  assert.equal(withBaseHref('<p>x</p>', U), '<base href="https://example.com/a/b.html?x=1"><p>x</p>');
});
test('page absolute base is kept', () => {
  const page = '<head><base href="https://cdn.example.net/"></head>';
  assert.equal(withBaseHref(page, U), page);
});
test('page relative base is resolved against the flow URL and placed first', () => {
  const out = withBaseHref('<head><base href="/static/"><link rel=stylesheet href=a.css></head>', U);
  assert.ok(out.startsWith('<head><base href="https://example.com/static/"><base href="/static/">'));
});
test('base inside a comment is ignored', () => {
  const out = withBaseHref('<head><!-- <base href="https://evil.test/"> --></head>', U);
  assert.ok(out.startsWith('<head><base href="https://example.com/a/b.html?x=1">'));
});
test('invalid or non-http baseURL leaves body untouched', () => {
  for (const bad of ['', null, undefined, 'javascript:alert(1)', 'data:text/html,x', 'file:///etc/passwd', '/relative', 'not a url', 'ftp://example.com/'])
    assert.equal(withBaseHref('<p>x</p>', bad), '<p>x</p>', String(bad));
});
test('URL with quotes is attribute-escaped', () => {
  const out = withBaseHref('<head></head>', "https://example.com/a'b\"c?q=<x>&y=1");
  const m = /<base href="([^"]*)">/.exec(out);
  assert.ok(m, out);
  assert.ok(!/["<>']/.test(m[1]), m[1]);
  assert.ok(m[1].includes('&amp;y=1'));
});
