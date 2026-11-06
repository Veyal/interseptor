// proxy.js wiring that a node test can pin without a DOM.
//
// The WebSocket replay URL and the Repeater URL built by "Send as identity" both
// hand-built `${scheme}://${host}:${port}${path}`. A captured IPv6 host is
// net/url's unbracketed Hostname() ("::1"), so both produced an authority no
// client can parse ("ws://::1:8080/…" / "http://::1:8080/…") — the sibling URL
// builders (copyas.flowURL, map-model.endpointUrl, flowbody.flowUrl) bracket it,
// these two did not. flowWsURL is sliced out of proxy.js the same way
// windowed-list.test.mjs slices core.js, so the wiring itself is under test.
import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import { urlHost, flowURL } from '../js/copyas.js';

const proxySrc = fs.readFileSync(new URL('../js/proxy.js', import.meta.url), 'utf8');

function sliceFunction(src, signature) {
  const start = src.indexOf(signature);
  assert.ok(start >= 0, 'proxy.js has ' + signature);
  let depth = 0;
  for (let i = src.indexOf('{', start); i < src.length; i++) {
    if (src[i] === '{') depth++;
    else if (src[i] === '}' && --depth === 0) return src.slice(start, i + 1);
  }
  throw new Error('unbalanced ' + signature);
}

const flowWsURL = new Function('urlHost', 'return ' + sliceFunction(proxySrc, 'function flowWsURL('))(urlHost);

test('the WS replay URL brackets an IPv6 host instead of emitting ws://::1:8080', () => {
  assert.equal(flowWsURL({ scheme: 'http', host: '::1', port: 8080, path: '/chat' }), 'ws://[::1]:8080/chat');
  assert.equal(flowWsURL({ scheme: 'https', host: '2001:db8::1', port: 443, path: '/chat' }), 'wss://[2001:db8::1]/chat');
  // The server parses this URL, so both forms must be real URLs.
  for (const d of [
    { scheme: 'http', host: '::1', port: 8080, path: '/chat?x=1' },
    { scheme: 'https', host: '::1', port: 443, path: '/' },
  ]) {
    const u = new URL(flowWsURL(d));
    assert.equal(u.protocol, d.scheme === 'https' ? 'wss:' : 'ws:');
    assert.equal(u.hostname, '[::1]');
    assert.equal(u.pathname, d.path.split('?')[0]);
  }
  // The old hand-built form is not even parseable; keep it out of the file.
  assert.ok(!/`\$\{s\}:\/\/\$\{d\.host\}/.test(proxySrc), 'flowWsURL goes through urlHost');
});

test('the WS replay URL keeps the flow’s default-port rule and empty path', () => {
  assert.equal(flowWsURL({ scheme: 'http', host: 'app.example.com', port: 80, path: '' }), 'ws://app.example.com/');
  assert.equal(flowWsURL({ scheme: 'http', host: 'app.example.com', port: 9000, path: '' }), 'ws://app.example.com:9000/');
  assert.equal(flowWsURL({ scheme: 'https', host: 'app.example.com', port: 443, path: '/x' }), 'wss://app.example.com/x');
  assert.equal(flowWsURL({ scheme: 'https', host: 'app.example.com', port: 80, path: '/x' }), 'wss://app.example.com:80/x');
});

test('Send as identity hands the Repeater the same IPv6-safe URL', () => {
  // The loader used to build `${scheme}://${host}:${port}${path}` inline.
  assert.ok(!/t\.url=`\$\{d\.scheme\}:\/\//.test(proxySrc), 'no hand-built authority in sendAsIdentity');
  assert.ok(/t\.url=flowURL\(d\);/.test(proxySrc), 'sendAsIdentity delegates to flowURL');
  // flowURL (flowbody.flowUrl) is the IPv6-aware builder, checked end to end.
  assert.equal(flowURL({ scheme: 'http', host: '::1', port: 8080, path: '/x' }), 'http://[::1]:8080/x');
  assert.equal(flowURL({ scheme: 'https', host: '2001:db8::1', port: 443, path: '/' }), 'https://[2001:db8::1]/');
});

test('the JSON tree fold is one pure function instead of a depth-only loop', () => {
  // The old loop unhid every row deeper than the toggled one, so expanding a
  // parent unfolded containers the user had closed inside it.
  assert.ok(proxySrc.includes('subtreeVisibility(model,0,!open)'), 'the fold handler uses the model');
  assert.ok(!/n\.hidden=open;/.test(proxySrc), 'no depth-only unhide loop');
});
