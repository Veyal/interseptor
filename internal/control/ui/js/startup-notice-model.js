// startup-notice-model.js — when the server started in a different project than
// the operator last had open (a remembered external folder was unavailable),
// GET /api/project carries `startupNotice` with the reason. The notice is an
// explanation, not an error: it must be shown once per distinct reason, and a
// dismissal must stick across reloads without hiding a later, different reason.

// noticeKey turns the notice text into a stable storage key, so a fixed bug or
// a different missing folder each get their own dismissal.
export function noticeKey(text) {
  const s = String(text || '').trim();
  if (!s) return '';
  // FNV-1a 32-bit: short, stable across builds, no crypto dependency.
  let h = 0x811c9dc5;
  for (let i = 0; i < s.length; i++) {
    h ^= s.charCodeAt(i);
    h = Math.imul(h, 0x01000193) >>> 0;
  }
  return 'startupNotice:' + h.toString(16) + ':' + s.length;
}

// shouldShowStartupNotice decides whether to render the notice. dismissed is the
// key that was last dismissed ('' when nothing was).
export function shouldShowStartupNotice(notice, dismissed) {
  const text = String(notice || '').trim();
  if (!text) return { show: false, key: '', text: '' };
  const key = noticeKey(text);
  return { show: key !== dismissed, key, text };
}

// startupNoticeMessage is the sentence a person reads: the server's reason, with
// its trailing period removed so the added clause does not read as two errors.
export function startupNoticeMessage(text) {
  const s = String(text || '').trim().replace(/\.$/, '');
  if (!s) return '';
  return s.charAt(0).toUpperCase() + s.slice(1) + '. Click the project name to switch back.';
}
