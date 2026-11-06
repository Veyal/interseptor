import test from 'node:test';
import assert from 'node:assert/strict';
import { suppressedBannerModel, suppressedReasonText } from '../js/suppressed-model.js';

test('an empty report hides the banner', () => {
  const m = suppressedBannerModel({ total: 0, hosts: [] });
  assert.equal(m.show, false);
  assert.equal(m.key, '');
});

test('an out-of-scope drop names the host and offers scope widening', () => {
  const m = suppressedBannerModel({
    total: 12,
    hosts: [{ host: 'pos.example', reason: 'out-of-scope', count: 12 }],
  });
  assert.equal(m.show, true);
  assert.equal(m.total, 12);
  assert.equal(m.host, 'pos.example');
  assert.equal(m.reason, 'out-of-scope');
  assert.match(m.text, /12 requests suppressed/);
  assert.match(m.text, /pos\.example/);
  assert.match(m.hint, /scope/i);
});

test('a telemetry drop points at the suppression setting, not at scope', () => {
  const m = suppressedBannerModel({
    total: 3,
    hosts: [{ host: 'incoming.telemetry.mozilla.org', reason: 'browser-telemetry', count: 3 }],
  });
  assert.match(m.hint, /Settings → Proxy/);
  assert.equal(suppressedReasonText('browser-telemetry'), 'browser telemetry');
  assert.equal(suppressedReasonText('android-telemetry'), 'Android telemetry');
});

test('a report without host detail still shows the total', () => {
  const m = suppressedBannerModel({ total: 2, hosts: [] });
  assert.equal(m.show, true);
  assert.match(m.text, /2 requests suppressed/);
  assert.match(m.text, /never captured/);
});

test('the key changes when the count or host changes, and not otherwise', () => {
  const a = suppressedBannerModel({ total: 1, hosts: [{ host: 'a.example', reason: 'out-of-scope', count: 1 }] });
  const b = suppressedBannerModel({ total: 1, hosts: [{ host: 'a.example', reason: 'out-of-scope', count: 1 }] });
  const c = suppressedBannerModel({ total: 2, hosts: [{ host: 'a.example', reason: 'out-of-scope', count: 2 }] });
  assert.equal(a.key, b.key);
  assert.notEqual(a.key, c.key);
});
