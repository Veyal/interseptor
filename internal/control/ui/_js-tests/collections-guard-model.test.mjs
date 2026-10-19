import test from 'node:test';
import assert from 'node:assert/strict';
import { restrictedHost, addableBlock } from '../js/collections-guard-model.js';

// Round 4 (a5) B1: the run plan predicts the IP guard, so it needs the same
// private/loopback test the server applies to a literal destination.
test('restrictedHost mirrors the IP guard for literal loopback, private and link-local hosts', () => {
  for (const h of ['127.0.0.1', 'localhost', 'api.localhost', '10.1.2.3', '172.16.0.9', '172.31.255.1', '192.168.1.5', '169.254.169.254', '100.64.0.1', '0.0.0.0', '[::1]', '::1', 'fe80::1', 'fd00::5']) {
    assert.equal(restrictedHost(h), true, h);
  }
  for (const h of ['example.com', '8.8.8.8', '172.32.0.1', '100.128.0.1', '192.169.0.1', '2606:4700::1111', '', undefined]) {
    assert.equal(restrictedHost(h), false, String(h));
  }
});


test('only scope and IP guard blocks are fixed by adding the host', () => {
  assert.equal(addableBlock('out_of_scope'), true);
  assert.equal(addableBlock('ip_guard'), true);
  assert.equal(addableBlock('base_target_pin'), false);
  assert.equal(addableBlock(''), false);
});
