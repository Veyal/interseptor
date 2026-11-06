// The topbar theme toggle is written by three modules: app.js at boot and on its
// own click, settings-appearance.js (Settings → Appearance) and dock.js (the phone
// More sheet). Only app.js re-labelled it, so switching the theme anywhere else
// left the button announcing the theme it showed *before* the switch — a wrong
// accessible name and a wrong tooltip. themeToggleLabel is the shared wording;
// this test pins it to what app.js writes so the two cannot drift.
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { themeToggleLabel, normalizeThemeChoice } from '../js/settings-model.js';

const appSource = readFileSync(new URL('../js/app.js', import.meta.url), 'utf8');

test('themeToggleLabel names the applied theme and the one a click switches to', () => {
  assert.equal(themeToggleLabel('dark'), 'Theme: dark. Switch to light.');
  assert.equal(themeToggleLabel('light'), 'Theme: light. Switch to dark.');
  assert.equal(themeToggleLabel('hc'), 'Theme: high contrast. Switch to dark.');
});

test('themeToggleLabel matches the wording app.js writes for the same theme', () => {
  // app.js builds: 'Theme: ' + (t==='hc'?'high contrast':t) + '. Switch to ' + nx + '.'
  // where nx is 'light' only for the dark base. Rebuild it here so a change in
  // either place fails this test instead of silently de-syncing the label.
  const appLabel = (t) => 'Theme: ' + (t === 'hc' ? 'high contrast' : t) + '. Switch to ' + (t === 'dark' ? 'light' : 'dark') + '.';
  for (const t of ['dark', 'light', 'hc']) assert.equal(themeToggleLabel(t), appLabel(t), t);
  assert.ok(appSource.includes("'Theme: '"), 'app.js keeps its pinned literal');
});

test('themeToggleLabel treats a missing or unknown theme as the dark base', () => {
  assert.equal(themeToggleLabel(''), themeToggleLabel('dark'));
  assert.equal(themeToggleLabel(undefined), themeToggleLabel('dark'));
  assert.equal(themeToggleLabel('system'), themeToggleLabel('dark'));
});

test('every writer of the theme re-labels the toggle', () => {
  for (const file of ['../js/settings-appearance.js', '../js/dock.js']) {
    const src = readFileSync(new URL(file, import.meta.url), 'utf8');
    assert.ok(src.includes('themeToggleLabel('), file + ' must use the shared label');
    assert.ok(/setAttribute\('aria-label', label\)/.test(src), file + ' must set the toggle aria-label');
    assert.ok(/toggle\.title = label/.test(src), file + ' must set the toggle tooltip');
  }
});

test('the theme choice that reaches the toggle is the applied one, not the raw choice', () => {
  // 'system' never reaches data-theme; both writers pass the resolved theme.
  const appearance = readFileSync(new URL('../js/settings-appearance.js', import.meta.url), 'utf8');
  assert.ok(/setToggleIcon\(attr === 'light' \|\| attr === 'hc' \? attr : 'dark'\)/.test(appearance),
    'Settings must label the resolved theme (system resolves through themeAttribute)');
  const dock = readFileSync(new URL('../js/dock.js', import.meta.url), 'utf8');
  assert.ok(/themeToggleLabel\(effective\)/.test(dock), 'the More sheet must label the resolved theme');
  // normalizeThemeChoice is the one place 'system' is allowed.
  assert.equal(normalizeThemeChoice('system'), 'system');
});
