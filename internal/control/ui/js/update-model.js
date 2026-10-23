// update-model.js — pure state for the one-click update dialog (js/update.js).
// No DOM: GET /api/update + update.progress SSE events in, a view description
// out, so node tests cover every state.

export const RELEASES_URL = 'https://github.com/Veyal/interseptor/releases';

const RUNNING = new Set(['downloading', 'verifying', 'installing']);
const PHASE_LABELS = {
  downloading: 'Downloading',
  verifying: 'Verifying checksum',
  installing: 'Installing',
  ready: 'Ready to restart',
  error: 'Update failed',
  restarting: 'Restarting',
};

export const isRunning = (phase) => RUNNING.has(phase);
export const phaseLabel = (phase) => PHASE_LABELS[phase] || '';

// formatMB renders bytes as megabytes with one decimal ("34.2").
export function formatMB(bytes) {
  const n = Number(bytes);
  return (Number.isFinite(n) && n > 0 ? n / 1048576 : 0).toFixed(1);
}

// progressText is "34.2 of 61.0 MB", or "34.2 MB" when the size is unknown.
export function progressText(done, total) {
  return total > 0 ? `${formatMB(done)} of ${formatMB(total)} MB` : `${formatMB(done)} MB`;
}

// progressPercent is 0–100 for the progressbar, null while indeterminate.
export function progressPercent(done, total) {
  if (!(total > 0)) return null;
  return Math.max(0, Math.min(100, Math.round((done / total) * 100)));
}

const emptyState = () => ({ phase: 'idle', done: 0, total: 0, error: '' });

// normalizeState copies the server's {phase, done, total, error} defensively.
export function normalizeState(s) {
  const out = emptyState();
  if (!s || typeof s !== 'object') return out;
  if (typeof s.phase === 'string' && s.phase) out.phase = s.phase;
  out.done = Number(s.done) || 0;
  out.total = Number(s.total) || 0;
  out.error = typeof s.error === 'string' ? s.error : '';
  return out;
}

// applyProgressEvent folds one SSE message into the state; other events and a
// late progress report after a settled phase are ignored.
export function applyProgressEvent(state, ev) {
  if (!ev || ev.type !== 'update.progress') return state;
  const next = normalizeState(ev);
  if (state && (state.phase === 'ready' || state.phase === 'restarting') && isRunning(next.phase)) return state;
  return next;
}

// stripRemoteImages turns markdown images into plain links so release notes
// never load a remote image into the (offline-capable) control UI.
export function stripRemoteImages(md) {
  // Repeat until stable: one pass over "!![x](u)" leaves "![x](u)" behind.
  let out = String(md || ''), prev;
  do {
    prev = out;
    out = out.replace(/!\[([^\]]*)\]\(([^)\s]+)[^)]*\)/g, (m, alt, src) => `[${alt || 'image'}](${src})`);
  } while (out !== prev);
  return out;
}

const MANAGED = { brew: 'Homebrew', scoop: 'Scoop' };

// updateView describes the dialog for the GET /api/update payload and the
// current progress state.
export function updateView(info = {}, state = emptyState()) {
  const current = String(info.current || '');
  const latest = String(info.latest || '');
  const method = String(info.method || 'unavailable');
  const st = normalizeState(state);
  const view = {
    summary: latest ? `v${current} → v${latest}` : `v${current}`,
    releaseUrl: /^https:\/\/github\.com\//.test(info.url || '') ? info.url : RELEASES_URL,
    notes: stripRemoteImages(info.notes),
    mode: 'self',
    notice: '',
    command: '',
    action: null, // {id, label, busy}
    progress: null, // {label, text, percent}
    error: '',
  };
  if (!info.available || !latest) {
    view.mode = 'current';
    view.notice = `Interseptor v${current} is up to date.`;
    return view;
  }
  if (method !== 'self') {
    view.mode = MANAGED[method] ? 'command' : 'explain';
    view.command = MANAGED[method] ? String(info.command || '') : '';
    view.notice = String(info.reason || 'This copy of Interseptor cannot update itself.');
    if (MANAGED[method]) view.notice += ` Update it with ${MANAGED[method]}:`;
    return view;
  }
  if (isRunning(st.phase)) {
    view.action = { id: 'update', label: 'Updating…', busy: true };
    view.progress = {
      label: phaseLabel(st.phase),
      text: progressText(st.done, st.total),
      percent: st.phase === 'downloading' ? progressPercent(st.done, st.total) : 100,
    };
    return view;
  }
  if (st.phase === 'ready' || st.phase === 'restarting') {
    const restarting = st.phase === 'restarting';
    view.action = { id: 'restart', label: restarting ? 'Restarting…' : 'Restart to Update', busy: restarting };
    view.progress = { label: phaseLabel(st.phase), text: progressText(st.total, st.total), percent: 100 };
    view.notice = restarting
      ? 'Interseptor is restarting on the same project and ports; this page reloads when it is back.'
      : `v${latest} is installed. Restart Interseptor to start using it.`;
    return view;
  }
  if (st.phase === 'error') {
    view.error = st.error || 'The update failed.';
    view.action = { id: 'update', label: 'Retry', busy: false };
    return view;
  }
  view.action = { id: 'update', label: 'Update', busy: false };
  return view;
}

// handleBadgeClick runs what a click on the version badge does: retry a failed
// version read, hand an available update to the native macOS app
// (requestNative returns false without a native bridge), or open the web
// dialog. Returns true when it acted; false lets the badge's releases link
// navigate.
export function handleBadgeClick({ failed = false, updateAvailable = false, retry, requestNative, openDialog } = {}) {
  if (failed) { retry(); return true; }
  if (!updateAvailable) return false;
  if (!requestNative()) openDialog();
  return true;
}

// restartReached reports whether the relaunched server answers with the
// version the update installed.
export function restartReached(versionInfo, expected) {
  return !!versionInfo && !!expected && String(versionInfo.version || '') === String(expected);
}
