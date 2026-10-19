import test from 'node:test';
import assert from 'node:assert/strict';
import {
  normalizeThemeChoice, themeAttribute, themeStoragePlan, normalizeDensityChoice, readHintsPref, writeHintsPref,
  highlightRanges, matchSections, searchSummary, saveStatusText, validateTargetLines, sectionHealth, typedConfirmMatches,
  projectImportSummary, restoreNoticeText, upstreamPasswordDropWarning,
  readFlowDrawerLayoutPref, writeFlowDrawerLayoutPref, normalizeFlowDrawerLayoutChoice,
  THEME_CHOICES, DENSITY_CHOICES, FLOW_DRAWER_LAYOUT_CHOICES,
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

test('hints preference defaults off and survives a throwing storage', () => {
  const s = memory();
  assert.equal(readHintsPref(s), false);
  assert.equal(writeHintsPref(true, s), true);
  assert.equal(readHintsPref(s), true);
  assert.equal(writeHintsPref(false, s), true);
  assert.equal(readHintsPref(s), false);
  const bad = { getItem() { throw new Error('blocked'); }, setItem() { throw new Error('blocked'); } };
  assert.equal(readHintsPref(bad), false);
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

test('system proxy switch says what it is doing while the OS call is in flight', async () => {
  const { systemProxyView } = await import('../js/settings-model.js');
  const off = { supported: true, enabled: false, proxy: '127.0.0.1:8080' };
  const on = { ...off, enabled: true };
  assert.deepEqual(systemProxyView(off, null), { label: 'System proxy is off', pressed: false, hint: '' });
  assert.deepEqual(systemProxyView(on, null), { label: 'System proxy is on', pressed: true, hint: 'Traffic routes through 127.0.0.1:8080' });
  // A click must not leave the old "off" label up while networksetup runs.
  const turningOn = systemProxyView(off, true);
  assert.equal(turningOn.label, 'Turning system proxy on…');
  assert.equal(turningOn.pressed, false, 'aria-pressed keeps the confirmed state until the OS answers');
  assert.equal(systemProxyView(on, false).label, 'Turning system proxy off…');
  // Re-requesting the current state is not a transition.
  assert.equal(systemProxyView(on, true).label, 'System proxy is on');
});

test('the upstream summary describes the saved proxy, not unsaved edits', async () => {
  const { upstreamSummaryText } = await import('../js/settings-model.js');
  const saved = { scheme: 'http', host: 'proxy.example.com', port: '8080' };
  assert.equal(upstreamSummaryText(saved, saved), 'HTTP · proxy.example.com:8080');
  assert.equal(upstreamSummaryText({ scheme: 'direct' }, { scheme: 'direct' }), 'Direct connection · no upstream proxy');
  assert.equal(upstreamSummaryText({ scheme: 'socks5h', host: 'h', port: '' }, { scheme: 'socks5h', host: 'h', port: '' }), 'SOCKS5H · h:1080 · remote DNS');
  // QA: typing an invalid port updated the summary before Save.
  const draft = { scheme: 'http', host: '127.0.0.1', port: '99999' };
  assert.equal(upstreamSummaryText(saved, draft), 'Unsaved changes · saved: HTTP · proxy.example.com:8080');
  assert.equal(upstreamSummaryText({ scheme: 'direct' }, draft), 'Unsaved changes · saved: Direct connection · no upstream proxy');
});

// QA round 3: a restored backup leaves proxy auth on with no password (every
// client gets 407). The proxy chip must not read as a healthy green.
test('proxy health warns when proxy authentication has no password', () => {
  const readiness = { checks: [
    { id: 'proxy', ok: true, detail: 'listening 127.0.0.1:8080' },
    { id: 'proxy_auth', ok: false, detail: 'proxy authentication is on but has no password', fix: 're-enter the password' },
  ] };
  const h = sectionHealth('proxy', { readiness });
  assert.equal(h.state, 'warn');
  assert.equal(h.text, 'Proxy: auth password missing');
  assert.match(h.detail, /re-enter/);
});

test('portable import summary names skipped duplicates and settings kept local', () => {
  assert.deepEqual(projectImportSummary({ importedFlows: 3, importedRules: 1, importedScope: 2 }),
    { text: 'Imported 3 flows · 1 rule · 2 scope rules', tone: '' });
  const again = projectImportSummary({ importedFlows: 0, importedRules: 0, importedScope: 0, skippedFlows: 169, skippedRules: 0, skippedScope: 2,
    keptLocalSettings: ['upstream.proxy', 'upstream.proxyCA'] });
  assert.match(again.text, /^Imported 0 flows · 0 rules · 0 scope rules · 171 duplicates skipped/);
  assert.match(again.text, /Kept this machine's upstream proxy and upstream proxy CA/);
  assert.equal(again.tone, 'warn');
});

test('portable import summary reports what happened to the notes', () => {
  assert.match(projectImportSummary({ notes: 'merged' }).text, /notes merged/);
  assert.match(projectImportSummary({ notes: 'kept' }).text, /notes kept/);
  assert.match(projectImportSummary({ notes: 'imported' }).text, /notes imported/);
  assert.doesNotMatch(projectImportSummary({}).text, /notes/);
});

test('restore notice is the server notice, or empty', () => {
  assert.equal(restoreNoticeText({ name: 'x' }), '');
  assert.equal(restoreNoticeText({ credentialsNotRestored: ['proxyAuthPassword'], notice: 'Saved passwords were not included in the backup' }),
    'Saved passwords were not included in the backup');
  assert.match(restoreNoticeText({ credentialsNotRestored: ['proxyAuthPassword'] }), /not included in the backup/);
});

test('saving the upstream proxy warns whenever the saved password was dropped', () => {
  assert.equal(upstreamPasswordDropWarning({ keep: false, url: 'http://bob@p.example:8080', passwordSaved: false }), '');
  assert.equal(upstreamPasswordDropWarning({ keep: true, url: 'http://bob@p.example:8080', passwordSaved: true }), '');
  assert.equal(upstreamPasswordDropWarning({ keep: true, url: '', passwordSaved: false }), '');
  assert.match(upstreamPasswordDropWarning({ keep: true, url: 'http://bob@q.example:8080', passwordSaved: false }), /proxy host changed/);
  assert.match(upstreamPasswordDropWarning({ keep: true, url: 'http://p.example:8080/', passwordSaved: false }), /username was cleared/);
});

test('flow drawer layout preference defaults to bottom and persists valid choices', () => {
  assert.deepEqual(FLOW_DRAWER_LAYOUT_CHOICES.map((c) => c.id), ['bottom', 'popup', 'side']);
  assert.equal(normalizeFlowDrawerLayoutChoice(null), 'bottom');
  assert.equal(normalizeFlowDrawerLayoutChoice('other'), 'bottom');
  assert.equal(normalizeFlowDrawerLayoutChoice('popup'), 'popup');
  assert.equal(normalizeFlowDrawerLayoutChoice('side'), 'side');

  const mem = memory();
  assert.equal(readFlowDrawerLayoutPref(mem), 'bottom');
  writeFlowDrawerLayoutPref('popup', mem);
  assert.equal(readFlowDrawerLayoutPref(mem), 'popup');
  writeFlowDrawerLayoutPref('invalid', mem);
  assert.equal(readFlowDrawerLayoutPref(mem), 'bottom');
});
