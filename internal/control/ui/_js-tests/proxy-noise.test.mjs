import test from 'node:test';
import assert from 'node:assert/strict';
import { isBrowserNoise, activeFilterCount, popoverFilterCount } from '../js/proxy-filters.js';

test('isBrowserNoise matches Chrome background hosts, CDN mirrors and browser-only paths', () => {
  assert.equal(isBrowserNoise('clients2.google.com', '/service/update2/crx'), true);
  assert.equal(isBrowserNoise('CONTENT-AUTOFILL.googleapis.com', '/v1/forms:vote'), true, 'case-insensitive host');
  assert.equal(isBrowserNoise('rr7---sn-example.gvt1.com', '/edgedl/x'), true);
  assert.equal(isBrowserNoise('accounts.google.com', '/ListAccounts?gpsia=1'), true);
  assert.equal(isBrowserNoise('www.googleapis.com', '/chromewebstore/v1.1/item'), true);
});

test('isBrowserNoise keeps target traffic on shared hosts visible', () => {
  assert.equal(isBrowserNoise('accounts.google.com', '/o/oauth2/auth'), false, 'a sign-in flow under test');
  assert.equal(isBrowserNoise('www.googleapis.com', '/oauth2/v3/userinfo'), false);
  assert.equal(isBrowserNoise('notgvt1.com', '/'), false, 'the suffix needs its dot');
  assert.equal(isBrowserNoise('app.example.com', '/ListAccounts'), false);
  assert.equal(isBrowserNoise('', ''), false);
});

test('hiding browser noise counts as an active and a popover filter', () => {
  const st = { filters: {}, hideBrowserNoise: true };
  assert.equal(activeFilterCount(st), 1);
  assert.equal(popoverFilterCount(st), 1);
  st.hideBrowserNoise = false;
  assert.equal(activeFilterCount(st), 0);
});
