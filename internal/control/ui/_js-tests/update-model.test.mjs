// The one-click update dialog's pure state model (js/update-model.js).
import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  updateView, applyProgressEvent, progressText, progressPercent, phaseLabel,
  stripRemoteImages, handleBadgeClick, restartReached, RELEASES_URL,
} from '../js/update-model.js';

const self = { current: '2.0.0', latest: '2.1.0', available: true, method: 'self', notes: '## Fixed', url: 'https://github.com/Veyal/interseptor/releases/tag/v2.1.0' };

test('idle self-updatable install offers Update with the version step and notes', () => {
  const v = updateView(self, { phase: 'idle' });
  assert.equal(v.summary, 'v2.0.0 → v2.1.0');
  assert.deepEqual(v.action, { id: 'update', label: 'Update', busy: false });
  assert.equal(v.progress, null);
  assert.equal(v.notes, '## Fixed');
  assert.equal(v.releaseUrl, self.url);
});

test('download progress is determinate with MB text; later phases fill the bar', () => {
  const v = updateView(self, { phase: 'downloading', done: 35861709, total: 63963136 });
  assert.equal(v.progress.text, '34.2 of 61.0 MB');
  assert.equal(v.progress.percent, 56);
  assert.equal(v.progress.label, 'Downloading');
  assert.equal(v.action.busy, true);
  assert.equal(updateView(self, { phase: 'verifying', done: 10, total: 10 }).progress.label, 'Verifying checksum');
  assert.equal(updateView(self, { phase: 'installing', done: 10, total: 10 }).progress.percent, 100);
  assert.equal(updateView(self, { phase: 'downloading', done: 5, total: 0 }).progress.percent, null, 'unknown size is indeterminate');
});

test('ready offers Restart to Update; restarting is busy', () => {
  const ready = updateView(self, { phase: 'ready', done: 10, total: 10 });
  assert.deepEqual(ready.action, { id: 'restart', label: 'Restart to Update', busy: false });
  assert.match(ready.notice, /v2\.1\.0 is installed/);
  assert.equal(updateView(self, { phase: 'restarting', done: 10, total: 10 }).action.busy, true);
});

test('error shows the message, a Retry action and keeps the release link', () => {
  const v = updateView(self, { phase: 'error', error: 'checksum mismatch: got 00' });
  assert.equal(v.error, 'checksum mismatch: got 00');
  assert.deepEqual(v.action, { id: 'update', label: 'Retry', busy: false });
  assert.equal(v.releaseUrl, self.url);
});

test('brew and scoop show the package-manager command; dev/readonly/desktop explain', () => {
  const brew = updateView({ ...self, method: 'brew', command: 'brew upgrade interseptor', reason: 'This copy is managed by Homebrew.' });
  assert.equal(brew.mode, 'command');
  assert.equal(brew.command, 'brew upgrade interseptor');
  assert.equal(brew.action, null);
  assert.match(brew.notice, /Homebrew/);
  assert.equal(updateView({ ...self, method: 'scoop', command: 'scoop update interseptor' }).command, 'scoop update interseptor');
  for (const method of ['dev', 'readonly', 'desktop', 'unavailable']) {
    const v = updateView({ ...self, method, reason: 'why ' + method });
    assert.equal(v.mode, 'explain', method);
    assert.equal(v.action, null, method);
    assert.equal(v.command, '', method);
    assert.equal(v.notice, 'why ' + method);
  }
});

test('no newer release reports up to date', () => {
  const v = updateView({ current: '2.1.0', latest: '2.1.0', available: false, method: 'self' });
  assert.equal(v.mode, 'current');
  assert.equal(v.action, null);
});

test('a non-GitHub release URL falls back to the releases page', () => {
  assert.equal(updateView({ ...self, url: 'javascript:alert(1)' }).releaseUrl, RELEASES_URL);
});

test('SSE progress events fold into state; other events and late reports are ignored', () => {
  let s = { phase: 'idle', done: 0, total: 0, error: '' };
  s = applyProgressEvent(s, { type: 'flow.new' });
  assert.equal(s.phase, 'idle');
  s = applyProgressEvent(s, { type: 'update.progress', phase: 'downloading', done: 5, total: 10 });
  assert.deepEqual(s, { phase: 'downloading', done: 5, total: 10, error: '' });
  s = applyProgressEvent(s, { type: 'update.progress', phase: 'error', error: 'boom' });
  assert.equal(s.error, 'boom');
  const ready = { phase: 'ready', done: 10, total: 10, error: '' };
  assert.equal(applyProgressEvent(ready, { type: 'update.progress', phase: 'downloading', done: 1, total: 10 }), ready);
});

test('formatting helpers', () => {
  assert.equal(progressText(0, 0), '0.0 MB');
  assert.equal(progressPercent(20, 10), 100);
  assert.equal(phaseLabel('nope'), '');
});

test('release notes never load remote images', () => {
  const md = stripRemoteImages('Look ![shot](https://evil.example/x.png "t") here');
  assert.equal(md, 'Look [shot](https://evil.example/x.png) here');
  assert.ok(!md.includes('!['));
});

test('badge click: the native bridge gets check-updates instead of the web dialog', () => {
  const calls = [];
  const opts = (over) => ({
    retry: () => calls.push('retry'),
    requestNative: () => { calls.push('native'); return true; },
    openDialog: () => calls.push('dialog'),
    ...over,
  });
  assert.equal(handleBadgeClick(opts({ updateAvailable: true })), true);
  assert.deepEqual(calls, ['native'], 'native app handled it; the web dialog must stay closed');

  calls.length = 0;
  assert.equal(handleBadgeClick(opts({ updateAvailable: true, requestNative: () => { calls.push('native'); return false; } })), true);
  assert.deepEqual(calls, ['native', 'dialog'], 'browser tab / Chrome --app falls back to the web dialog');

  calls.length = 0;
  assert.equal(handleBadgeClick(opts({ failed: true, updateAvailable: true })), true);
  assert.deepEqual(calls, ['retry']);

  calls.length = 0;
  assert.equal(handleBadgeClick(opts({ updateAvailable: false })), false, 'no update: the releases link navigates');
  assert.deepEqual(calls, []);
});

test('restart completes only when the new version answers', () => {
  assert.equal(restartReached({ version: '2.0.0' }, '2.1.0'), false);
  assert.equal(restartReached({ version: '2.1.0' }, '2.1.0'), true);
  assert.equal(restartReached(null, '2.1.0'), false);
});

test('stripRemoteImages leaves no image behind for nested bangs', () => {
  const out = stripRemoteImages('!![x](https://e.example/p.png) and !!![y](https://e.example/q.png)');
  assert.equal(/!\[/.test(out), false, out);
  assert.match(out, /\[x\]\(https:\/\/e\.example\/p\.png\)/);
});
