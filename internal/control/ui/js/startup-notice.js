// startup-notice.js — shows why the server opened a project other than the one
// the operator last had open (the remembered external folder was unavailable and
// the desktop app started with --resume-fallback default). The reason comes from
// GET /api/project as `startupNotice`; settings.js hands it here from the load it
// already makes, so this costs no request.
import { $, esc, escAttr } from './core.js';
import { shouldShowStartupNotice, startupNoticeMessage } from './startup-notice-model.js';

const DISMISSED_KEY = 'startupNoticeDismissed';
let shown = null;

function readDismissed() {
  try { return localStorage.getItem(DISMISSED_KEY) || ''; } catch (e) { return ''; }
}

function writeDismissed(key) {
  try { localStorage.setItem(DISMISSED_KEY, key); } catch (e) { /* private mode: shown again next time */ }
}

// syncStartupNotice renders the banner for a project payload, or hides it when
// there is nothing to say or the operator already dismissed this reason.
export function syncStartupNotice(payload) {
  const banner = $('#startupNotice');
  if (!banner) return;
  const notice = payload && payload.startupNotice;
  const decision = shouldShowStartupNotice(notice, readDismissed());
  if (!decision.show) {
    banner.hidden = true;
    banner.innerHTML = '';
    shown = null;
    return;
  }
  if (shown === decision.key) return; // already on screen
  shown = decision.key;
  banner.innerHTML = `<svg class="icon" aria-hidden="true" focusable="false"><use href="#i-folder-open"/></svg>
    <span class="startup-notice-text">${esc(startupNoticeMessage(decision.text))}</span>
    <button type="button" class="btn xs" data-startup-dismiss>Dismiss</button>`;
  banner.hidden = false;
  banner.querySelector('[data-startup-dismiss]')?.addEventListener('click', () => {
    writeDismissed(decision.key);
    banner.hidden = true;
    banner.innerHTML = '';
    shown = null;
  });
}

// dismissedStartupNoticeKey exposes the stored key for tests and for Settings.
export function dismissedStartupNoticeKey() { return readDismissed(); }
export { escAttr };
