// update.js — the one-click update dialog (#updateModal). Opened from the
// version badge when a newer release exists (browser tab and Chrome --app; the
// native macOS app runs its own updater instead). GET /api/update describes the
// release and install method, POST /api/update stages it with progress over SSE
// (update.progress), POST /api/update/restart relaunches on the same project and
// ports; the page reloads once the new version answers. State lives in
// js/update-model.js.
import { $, api, openModal, closeModal, renderMD, copyText } from './core.js';
import { updateView, applyProgressEvent, normalizeState, restartReached } from './update-model.js';

const RESTART_POLL_MS = 700;
const RESTART_DEADLINE_MS = 60000;

const ui = { info: null, state: normalizeState(null), gen: 0, notes: null, pollTimer: 0 };

const modal = () => $('#updateModal');
const isOpen = () => { const m = modal(); return !!m && m.style.display === 'flex'; };

function show(el, on) { if (el) el.hidden = !on; }

function renderNotes(md) {
  if (md === ui.notes) return; // progress repaints must not rebuild the notes
  ui.notes = md;
  const box = $('#updNotes');
  if (!box) return;
  box.innerHTML = md.trim() ? renderMD(md) : '<p class="hint">No release notes were published for this version.</p>';
}

function renderProgress(p) {
  show($('#updProgress'), !!p);
  if (!p) return;
  $('#updPhase').textContent = p.label;
  $('#updBytes').textContent = p.text;
  const bar = $('#updBar');
  if (p.percent == null) {
    bar.removeAttribute('aria-valuenow');
    bar.classList.add('is-indeterminate');
  } else {
    bar.setAttribute('aria-valuenow', String(p.percent));
    bar.classList.remove('is-indeterminate');
  }
  bar.setAttribute('aria-valuetext', p.label + (p.text ? ', ' + p.text : ''));
  $('#updBarFill').style.transform = `scaleX(${(p.percent ?? 0) / 100})`;
}

function renderAction(a) {
  const btn = $('#updAction');
  show(btn, !!a);
  if (!a) return;
  btn.textContent = a.label;
  btn.dataset.action = a.id;
  btn.disabled = !!a.busy;
  btn.setAttribute('aria-busy', a.busy ? 'true' : 'false');
}

function render() {
  if (!ui.info) return;
  const v = updateView(ui.info, ui.state);
  $('#updSummary').textContent = v.summary;
  $('#updNotice').textContent = v.notice;
  show($('#updNotice'), !!v.notice);
  $('#updCommandText').textContent = v.command;
  show($('#updCommand'), !!v.command);
  $('#updError').textContent = v.error;
  show($('#updError'), !!v.error);
  $('#updRelease').href = v.releaseUrl;
  renderProgress(v.progress);
  renderAction(v.action);
  renderNotes(v.notes);
}

function renderLoading(text) {
  $('#updSummary').textContent = text;
  for (const id of ['#updNotice', '#updCommand', '#updError', '#updProgress', '#updAction']) show($(id), false);
}

async function loadInfo(gen) {
  try {
    const info = await api('/api/update');
    if (gen !== ui.gen) return;
    ui.info = info;
    if (ui.state.phase !== 'restarting') ui.state = normalizeState(info.state);
    render();
  } catch (e) {
    if (gen !== ui.gen) return;
    renderLoading('Could not read update details: ' + e.message);
  }
}

// openUpdateDialog opens the dialog (focus returns to the invoking badge on
// close) and refreshes the release details.
export function openUpdateDialog() {
  const m = modal();
  if (!m) return false;
  const gen = ++ui.gen;
  if (ui.info) render(); else renderLoading('Checking for updates…');
  openModal(m, { initialFocus: '#updAction' });
  loadInfo(gen);
  return true;
}

// handleUpdateProgress folds an update.progress SSE event into the dialog.
export function handleUpdateProgress(ev) {
  ui.state = applyProgressEvent(ui.state, ev);
  if (isOpen()) render();
}

async function startUpdate() {
  ui.state = { phase: 'downloading', done: 0, total: 0, error: '' };
  render();
  try {
    const st = await api('/api/update', { method: 'POST' });
    // An SSE event may already have moved the state past the 202 snapshot.
    if (ui.state.phase === 'downloading' && !ui.state.total) ui.state = normalizeState(st);
  } catch (e) {
    ui.state = { phase: 'error', done: 0, total: 0, error: e.message };
    // A conflict (another tab staged it, or it is now up to date) is settled
    // server-side: re-read the state rather than offering a Retry that loops.
    if (e.status === 409) { loadInfo(ui.gen); return; }
  }
  render();
}

function pollRestart(expected, deadline) {
  clearTimeout(ui.pollTimer);
  ui.pollTimer = setTimeout(async () => {
    try {
      const d = await api('/api/version?_t=' + Date.now());
      if (restartReached(d, expected)) { location.reload(); return; }
    } catch (e) { /* the server is between processes */ }
    if (Date.now() >= deadline) {
      ui.state = { phase: 'error', done: 0, total: 0, error: 'Interseptor did not come back on the new version within a minute. Start it again, then reload this page.' };
      render();
      return;
    }
    pollRestart(expected, deadline);
  }, RESTART_POLL_MS);
}

async function restart() {
  const expected = ui.info && ui.info.latest;
  ui.state = { ...ui.state, phase: 'restarting' };
  render();
  try {
    await api('/api/update/restart', { method: 'POST' });
  } catch (e) {
    ui.state = { phase: 'ready', done: ui.state.total, total: ui.state.total, error: '' };
    render();
    $('#updError').textContent = 'Restart failed: ' + e.message;
    show($('#updError'), true);
    return;
  }
  pollRestart(expected, Date.now() + RESTART_DEADLINE_MS);
}

{
  const btn = $('#updAction');
  if (btn) btn.addEventListener('click', () => {
    if (btn.disabled) return;
    if (btn.dataset.action === 'restart') restart(); else startUpdate();
  });
  $('#updClose')?.addEventListener('click', () => closeModal(modal()));
  $('#updCopy')?.addEventListener('click', () => copyText($('#updCommandText').textContent, 'command copied'));
}
