import test from 'node:test';
import assert from 'node:assert/strict';
import { shouldShowStartupNotice, startupNoticeMessage, noticeKey } from '../js/startup-notice-model.js';

test('no notice means no banner', () => {
  assert.deepEqual(shouldShowStartupNotice('', ''), { show: false, key: '', text: '' });
  assert.deepEqual(shouldShowStartupNotice(null, 'x'), { show: false, key: '', text: '' });
  assert.deepEqual(shouldShowStartupNotice('   ', ''), { show: false, key: '', text: '' });
});

test('a notice shows until that same notice is dismissed', () => {
  const notice = 'the project folder /Volumes/client is unavailable; opened the default project instead';
  const first = shouldShowStartupNotice(notice, '');
  assert.equal(first.show, true);
  assert.equal(first.text, notice);
  assert.equal(shouldShowStartupNotice(notice, first.key).show, false);
  // A different reason is a different notice: it must not be swallowed by the
  // dismissal of the first one.
  assert.equal(shouldShowStartupNotice('the folder /Volumes/other is unavailable', first.key).show, true);
});

test('the key is stable for the same text and distinct for different text', () => {
  assert.equal(noticeKey('a folder went away'), noticeKey('  a folder went away  '));
  assert.notEqual(noticeKey('folder one'), noticeKey('folder two'));
  assert.match(noticeKey('x'), /^startupNotice:[0-9a-f]+:1$/);
});

test('the message reads as one sentence and names the way back', () => {
  const msg = startupNoticeMessage('the project folder /Volumes/client is unavailable; opened the default project instead.');
  assert.equal(msg, 'The project folder /Volumes/client is unavailable; opened the default project instead. Click the project name to switch back.');
  assert.match(startupNoticeMessage('lowercase reason'), /^Lowercase reason\./);
  assert.equal(startupNoticeMessage(''), '');
});
