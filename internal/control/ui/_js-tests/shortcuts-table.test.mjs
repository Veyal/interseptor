import test from 'node:test';
import assert from 'node:assert/strict';
import { LEGACY_SHORTCUTS, KNOWN_SCOPES, parseDisplayKeys, registryKeysToDisplay } from '../js/shortcuts-table.js';
import { createKeyRegistry } from '../js/keys.js';

test('every legacy entry names its source file, a probe, a group and a known scope', () => {
  for (const s of LEGACY_SHORTCUTS) {
    assert.ok(s.keys && s.label && s.group, JSON.stringify(s));
    assert.ok(['app.js', 'proxy.js', 'tools.js'].includes(s.source), s.keys);
    assert.ok(s.probe, s.keys);
    assert.ok(KNOWN_SCOPES.includes(s.scope), s.scope);
  }
});

test('no two legacy entries share keys within a scope', () => {
  const seen = new Set();
  for (const s of LEGACY_SHORTCUTS) {
    const k = s.scope + '|' + s.keys;
    assert.ok(!seen.has(k), 'duplicate ' + k);
    seen.add(k);
  }
});

test('parseDisplayKeys renders chords, combinations and alternatives', () => {
  assert.deepEqual(parseDisplayKeys('g p'), [[['g'], ['p']]]);
  assert.deepEqual(parseDisplayKeys('Ctrl+Shift+A'), [[['Ctrl/⌘', 'Shift', 'A']]]);
  assert.deepEqual(parseDisplayKeys('j / k'), [[['j']], [['k']]]);
});

test('registryKeysToDisplay maps Mod to Ctrl', () => {
  assert.equal(registryKeysToDisplay('Mod+Shift+K'), 'Ctrl+Shift+K');
  assert.equal(registryKeysToDisplay('y c'), 'y c');
});

test('cheatsheet merges legacy rows with registry bindings under one group list', () => {
  const reg = createKeyRegistry({ singleKeyEnabled: () => true });
  reg.register({ id: 'attach', keys: 'e', scope: 'proxy', label: 'Attach as evidence', group: 'History', run() {} });
  const sheet = reg.cheatsheet(LEGACY_SHORTCUTS.map((l) => ({ group: l.group, keys: l.keys, label: l.label, scope: l.scope })));
  const history = sheet.find((g) => g.group === 'History');
  assert.ok(history.items.some((i) => i.legacy && i.keys === 'j / k'));
  assert.ok(history.items.some((i) => !i.legacy && i.keys === 'e'));
});

test('a registered binding that reuses a legacy single key in the same scope is detectable', () => {
  const legacyProxy = new Set(LEGACY_SHORTCUTS.filter((l) => l.scope === 'proxy').map((l) => l.keys));
  assert.ok(legacyProxy.has('r') && legacyProxy.has('x'));
  assert.ok(!legacyProxy.has('e'), 'e is free for attach-as-evidence');
});

test('parseDisplayKeys renders platform-correct modifier labels', () => {
  assert.deepEqual(parseDisplayKeys('Ctrl+Alt+P / Space', true), [[['⌘', '⌥', 'P']], [['Space']]]);
  assert.deepEqual(parseDisplayKeys('Ctrl+Alt+P', false), [[['Ctrl', 'Alt', 'P']]]);
  assert.deepEqual(parseDisplayKeys('Ctrl+Alt+P'), [[['Ctrl/⌘', 'Alt', 'P']]], 'unknown platform keeps both');
});

test('the sheet does not list Ctrl+I (Cmd+I is the Intercept Requests menu item)', () => {
  assert.ok(!LEGACY_SHORTCUTS.some((s) => s.keys === 'Ctrl+I'));
});

test('Cmd+Space (Spotlight on macOS) is never offered as a Repeater send key on macOS', async () => {
  const { legacyShortcutsFor } = await import('../js/shortcuts-table.js');
  const rep = (mac) => legacyShortcutsFor(mac).filter((s) => s.scope === 'repeater').map((s) => s.keys);
  assert.deepEqual(rep(true), ['Ctrl+Enter']);
  assert.deepEqual(rep(false), ['Ctrl+Enter', 'Ctrl+Space']);
  assert.equal(legacyShortcutsFor(undefined).length, LEGACY_SHORTCUTS.length);
});

test('platform hints use the native modifier and a key a Mac keyboard has', async () => {
  const { platformHints } = await import('../js/shortcuts-table.js');
  assert.match(platformHints(true).singleKey, /without ⌘/);
  assert.doesNotMatch(platformHints(true).pickerKeys, /Shift\+F10/);
  assert.match(platformHints(true).pickerKeys, /right-click/i);
  assert.match(platformHints(false).singleKey, /without Ctrl/);
  assert.match(platformHints(false).pickerKeys, /Shift\+F10/);
});
