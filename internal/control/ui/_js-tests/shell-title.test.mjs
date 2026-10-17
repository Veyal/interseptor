import { test } from 'node:test';
import assert from 'node:assert/strict';
import { documentTitle, tabLabel, APP_NAME } from '../js/shell-title.js';

test('documentTitle names the panel, then the project, then the app', () => {
  assert.equal(documentTitle('Proxy', 'demo'), 'Proxy · demo · Interseptor');
  assert.equal(documentTitle('Settings', ''), 'Settings · Interseptor');
  assert.equal(documentTitle('', 'demo'), 'demo · Interseptor');
  assert.equal(documentTitle(), APP_NAME);
  assert.equal(documentTitle('  Map ', ' acme '), 'Map · acme · Interseptor');
});

test('tabLabel drops the screen-reader note and squashes whitespace', () => {
  assert.equal(tabLabel('Scanner (new updates)', ' (new updates)'), 'Scanner');
  assert.equal(tabLabel('\n  Proxy\n ', ''), 'Proxy');
  assert.equal(tabLabel(null), '');
});
