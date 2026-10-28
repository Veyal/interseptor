// js/core.js renderMD under node. core.js touches the DOM at load, so the test
// slices renderMD and the escape helpers out of it, as windowed-list.test does.
import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';

const src = fs.readFileSync(new URL('../js/core.js', import.meta.url), 'utf8');
function sliceFunction(signature) {
  const start = src.indexOf(signature);
  assert.ok(start >= 0, 'core.js has ' + signature);
  let depth = 0;
  for (let i = src.indexOf('{', start); i < src.length; i++) {
    if (src[i] === '{') depth++;
    else if (src[i] === '}' && --depth === 0) return src.slice(start, i + 1);
  }
  throw new Error('unbalanced ' + signature);
}
const line = (prefix) => src.slice(src.indexOf(prefix), src.indexOf('\n', src.indexOf(prefix)));
const body = [line('export const esc='), line('export const escAttr='), sliceFunction('export function renderMD(src)')]
  .join('\n').replace(/export /g, '');
const renderMD = new Function(body + '\nreturn renderMD;')();

// attrNames tokenizes start tags the way a browser does (quoted values may hold
// '>'), returning every attribute name, so a broken-out attribute is visible.
function attrNames(html) {
  const names = [];
  const re = /<([a-zA-Z][\w-]*)/g;
  let m;
  while ((m = re.exec(html))) {
    let i = m.index + m[0].length;
    for (;;) {
      while (i < html.length && /[\s/]/.test(html[i])) i++;
      if (i >= html.length || html[i] === '>') break;
      let n = '';
      while (i < html.length && !/[\s/>=]/.test(html[i])) n += html[i++];
      names.push(n.toLowerCase());
      while (/\s/.test(html[i] || '')) i++;
      if (html[i] === '=') {
        i++;
        while (/\s/.test(html[i] || '')) i++;
        const q = html[i];
        if (q === '"' || q === "'") { const end = html.indexOf(q, i + 1); i = end < 0 ? html.length : end + 1; }
        else while (i < html.length && !/[\s>]/.test(html[i])) i++;
      }
    }
    re.lastIndex = i;
  }
  return names;
}
const SAFE_ATTRS = new Set(['class', 'tabindex', 'role', 'aria-label', 'alt', 'src', 'href', 'target', 'rel', 'data-flow', 'data-lang', 'type', 'disabled', 'checked']);
const assertSafeAttrs = (html) => { for (const n of attrNames(html)) assert.ok(SAFE_ATTRS.has(n), 'unexpected attribute ' + n + ' in ' + html); };

test('blockquotes render after escaping (esc turns > into &gt;)', () => {
  const html = renderMD('intro\n\n> first line\n> second **bold**\n\nafter');
  assert.match(html, /<blockquote class="md-quote">first line<br>second <strong>bold<\/strong><\/blockquote>/);
  assert.doesNotMatch(html, /&gt; first/);
  assert.match(html, /<p>after<\/p>/);
});

test('a quoted tag stays escaped inside the blockquote', () => {
  const html = renderMD('> <img src=x onerror=alert(1)>');
  assert.match(html, /<blockquote class="md-quote">&lt;img src=x onerror=alert\(1\)&gt;<\/blockquote>/);
});

test('a > inside a line is not a quote', () => {
  const html = renderMD('a > b');
  assert.doesNotMatch(html, /blockquote/);
  assert.match(html, /a &gt; b/);
});

// Regression: an image's src could contain "](", letting the link pass match
// across the <img> the image pass produced and leave an attribute open.
test('nested image/link PoC stays inert (no injected attribute)', () => {
  const html = renderMD('[![a](https://h.invalid/](https://q/)X) " onerror=alert(document.domain) x=y');
  assertSafeAttrs(html);
  assert.match(html, /onerror=alert/); // still present, but only as text
});

test('a quote in a link URL cannot break out of href', () => {
  const html = renderMD('[x](https://a/"onmouseover=alert(1)//)');
  assertSafeAttrs(html);
  const html2 = renderMD("[x](https://a/'q)");
  assert.doesNotMatch(html2, /href="[^"]*'/);
});

test('a link wrapped around a flow link does not nest anchors in attributes', () => {
  const html = renderMD('[a [b](flow:7) c](https://example.com/)');
  assertSafeAttrs(html);
  assert.doesNotMatch(html, /<a [^>]*>[^]*<a [^]*<\/a>[^]*<\/a>/);
  assert.match(html, /data-flow="7"/);
});

test('control-character placeholders in input are not interpreted', () => {
  const html = renderMD('x \x03' + '0\x03 \x04' + '0\x04 y');
  assert.doesNotMatch(html, /<img|<a /);
});

test('normal links, images and flow refs still render', () => {
  const html = renderMD('See [docs](https://example.com/a?b=1&c=2) and ![shot](https://example.com/i.png) and flow 12 and [f](flow:3) and [**bold**](https://example.com/b)');
  assert.match(html, /<a href="https:\/\/example\.com\/a\?b=1&amp;c=2" target="_blank" rel="noopener">docs<\/a>/);
  assert.match(html, /<img class="md-img"[^>]* alt="shot" src="https:\/\/example\.com\/i\.png">/);
  assert.match(html, /data-flow="12"/);
  assert.match(html, /<a class="md-flow-link" data-flow="3" href="#flow-3">f<\/a>/);
  assert.match(html, /<a href="https:\/\/example\.com\/b"[^>]*><strong>bold<\/strong><\/a>/);
  const linked = renderMD('[![alt](https://example.com/i.png)](https://example.com/full)');
  assert.match(linked, /<a href="https:\/\/example\.com\/full"[^>]*><img class="md-img"[^>]*src="https:\/\/example\.com\/i\.png"><\/a>/);
  assert.match(renderMD('[m](mailto:a@example.com)'), /<a href="mailto:a@example\.com"/);
  assert.match(renderMD('![n](/api/notes/images/4)'), /src="\/api\/notes\/images\/4"/);
});
