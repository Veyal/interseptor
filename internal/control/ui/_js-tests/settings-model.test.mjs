import test from 'node:test';
import assert from 'node:assert/strict';
import {
  normalizeThemeChoice, themeAttribute, themeStoragePlan, normalizeDensityChoice, readHintsPref, writeHintsPref,
  highlightRanges, matchSections, searchSummary, saveStatusText, validateTargetLines, sectionHealth, typedConfirmMatches,
  THEME_CHOICES, DENSITY_CHOICES,
} from '../js/settings-model.js';

const memory = () => { const m = new Map(); return { getItem: (k) => (m.has(k) ? m.get(k) : null), setItem: (k, v) => m.set(k, v), m }; };

test('theme has four choices and System resolves through the OS preference', () => {
  assert.deepEqual(THEME_CHOICES.map((c) => c.id), ['system', 'dark', 'light', 'hc']);
  assert.equal(normalizeThemeChoice(null), 'system');
  assert.equal(normalizeThemeChoice('purple'), 'system');
  assert.equal(normalizeThemeChoice('hc'), 'hc');
  assert.equal(themeAttribute('system', true), 'light');
  assert.equal(themeAttribute('system', false), null);
  assert.equal(themeAttribute('dark', true), null);
  assert.equal(themeAttribute('light', false), 'light');
  assert.equal(themeAttribute('hc', false), 'hc');
});

test('System removes the stored theme so the pre-paint script follows the OS', () => {
  assert.deepEqual(themeStoragePlan('system'), { action: 'remove' });
  assert.deepEqual(themeStoragePlan('hc'), { action: 'set', value: 'hc' });
  assert.deepEqual(themeStoragePlan('bogus'), { action: 'remove' });
});

test('density choices normalise to the three tiers', () => {
  assert.deepEqual(DENSITY_CHOICES.map((c) => c.id), ['compact', 'default', 'comfortable']);
  assert.equal(normalizeDensityChoice('comfortable'), 'comfortable');
  assert.equal(normalizeDensityChoice('x'), 'default');
});

test('hints preference defaults on and survives a throwing storage', () => {
  const s = memory();
  assert.equal(readHintsPref(s), true);
  assert.equal(writeHintsPref(false, s), true);
  assert.equal(readHintsPref(s), false);
  const bad = { getItem() { throw new Error('blocked'); }, setItem() { throw new Error('blocked'); } };
  assert.equal(readHintsPref(bad), true);
  assert.equal(writeHintsPref(false, bad), false);
});

test('search highlights label matches and falls back to body text', () => {
  assert.deepEqual(highlightRanges('TLS / CA and tls', 'tls'), [[0, 3], [13, 16]]);
  assert.deepEqual(highlightRanges('abc', ''), []);
  const rows = matchSections([
    { id: 'proxy', label: 'Proxy & network', text: 'listener address' },
    { id: 'tls', label: 'TLS / CA', text: 'certificate trust' },
    { id: 'api', label: 'API & MCP', text: 'tokens' },
  ], 'trust');
  assert.deepEqual(rows.map((r) => r.hit), [false, true, false]);
  assert.equal(rows[1].inBody, true);
  assert.equal(searchSummary(rows), '1 section matches');
  assert.equal(searchSummary(matchSections([{ id: 'a', label: 'A', text: '' }], 'zzz')), 'No matching settings.');
  assert.equal(searchSummary(matchSections([{ id: 'a', label: 'A', text: '' }], '')), '');
});

test('autosave status text follows the documented states', () => {
  const at = new Date(2026, 0, 2, 12, 4);
  assert.equal(saveStatusText('saving'), 'Saving...');
  assert.equal(saveStatusText('saved', at), 'Saved 12:04');
  assert.equal(saveStatusText('error'), 'Could not save. Retry');
  assert.equal(saveStatusText('dirty'), 'Unsaved changes');
  assert.equal(saveStatusText('invalid'), 'Fix the highlighted targets to save');
  assert.equal(saveStatusText('idle'), '');
});

test('target validation flags single-token mistakes and ignores prose lines', () => {
  const ok = validateTargetLines('*.example.com\napi.example.com:8443\nhttps://app.example.com/v1\n10.0.0.0/24\n# a comment\nAll staging APIs under example.com except billing');
  assert.equal(ok.ok, true);
  assert.deepEqual(ok.targets, ['*.example.com', 'api.example.com:8443', 'https://app.example.com/v1', '10.0.0.0/24']);
  const bad = validateTargetLines('*\n*example.com\nfoo..example.com\nexa<mple>.com\n*.ex*.com\nhttps://');
  assert.equal(bad.ok, false);
  assert.deepEqual(bad.errors.map((e) => e.line), [1, 2, 3, 4, 5, 6]);
  assert.match(bad.errors[0].message, /bare wildcard/);
  assert.match(bad.errors[1].message, /\*\.example\.com/);
  assert.equal(validateTargetLines('').empty, true);
  assert.equal(validateTargetLines('').ok, true);
});

test('section health shows text and degrades per segment', () => {
  const readiness = { checks: [
    { id: 'proxy', ok: true, detail: '127.0.0.1:8080' },
    { id: 'tls_intercept', ok: false, detail: 'no HTTPS flows', fix: 'trust the CA' },
    { id: 'auth_identities', ok: true, detail: '2 identities' },
    { id: 'oob', ok: false, detail: 'off' },
  ] };
  assert.deepEqual(sectionHealth('proxy', { readiness }), { state: 'ok', text: 'Proxy: listening', detail: '127.0.0.1:8080' });
  const tls = sectionHealth('tls', { readiness });
  assert.equal(tls.state, 'warn');
  assert.equal(tls.text, 'TLS: CA not verified');
  assert.match(tls.detail, /trust the CA/);
  assert.equal(sectionHealth('session', { readiness }).text, 'Auth: 2 identities');
  assert.equal(sectionHealth('scanner', { readiness }).state, 'warn');
  assert.equal(sectionHealth('tls', { readiness: null, stale: true }).state, 'unknown');
  assert.equal(sectionHealth('devices', { readiness }), null);
  assert.equal(sectionHealth('project', { readiness }), null);
  assert.equal(sectionHealth('scope', { brief: { ok: true }, scope: { enabled: true, inCount: 3 } }).state, 'ok');
  assert.equal(sectionHealth('scope', { brief: { ok: false }, scope: { enabled: true, inCount: 3 } }).text, 'Scope: no target');
  assert.equal(sectionHealth('scope', { brief: { ok: true }, scope: { enabled: false, inCount: 0 } }).text, 'Scope: no include rule');
});

test('typed confirm needs the phrase', () => {
  assert.equal(typedConfirmMatches('delete'), true);
  assert.equal(typedConfirmMatches('  DELETE '), true);
  assert.equal(typedConfirmMatches('del'), false);
  assert.equal(typedConfirmMatches('', 'wipe'), false);
  assert.equal(typedConfirmMatches('wipe', 'WIPE'), true);
});
