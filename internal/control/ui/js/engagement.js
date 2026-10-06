import { $, api, toast, renderLoadError } from './core.js';

/* ---- engagement brief (Settings > Target scope) ---- */
// The project's authorisation/conduct statement. Agents read it over MCP and
// reports cite its version; the version bumps only when content changes.
const FIELDS = ['scope', 'authorisation', 'conductRules', 'rateLimits', 'doNotTouch', 'credentialPolicy'];
const fieldEl = name => $('#brief-' + name);
let loaded = {};
let saving = false;

function readForm() {
  const out = {};
  FIELDS.forEach(f => { out[f] = fieldEl(f)?.value || ''; });
  return out;
}

function isDirty() {
  const cur = readForm();
  return FIELDS.some(f => cur[f] !== (loaded[f] || ''));
}

function renderBrief(brief) {
  loaded = brief || {};
  FIELDS.forEach(f => { const el = fieldEl(f); if (el) el.value = loaded[f] || ''; });
  const v = $('#briefVersion');
  if (v) v.textContent = loaded.version > 0 ? 'Version ' + loaded.version : 'No brief recorded yet';
}

export async function loadEngagementBrief() {
  const state = $('#briefLoadState');
  if (isDirty()) return; // never clobber an in-progress edit with a live update
  try {
    renderBrief(await api('/api/engagement-brief'));
    if (state) { state.textContent = ''; state.classList.add('u-hidden'); }
  } catch (e) {
    if (state) { state.classList.remove('u-hidden'); renderLoadError(state, 'Engagement brief', e, loadEngagementBrief, false); }
  }
}

async function saveBrief() {
  if (saving) return;
  const btn = $('#briefSaveBtn');
  saving = true;
  if (btn) { btn.disabled = true; btn.setAttribute('aria-busy', 'true'); }
  try {
    const saved = await api('/api/engagement-brief', {
      method: 'PUT', headers: { 'content-type': 'application/json' }, body: JSON.stringify(readForm()),
    });
    renderBrief(saved);
    toast('Engagement brief saved (version ' + saved.version + ')', 'success');
  } catch (e) {
    toast('Engagement brief not saved: ' + e.message, 'error');
  } finally {
    saving = false;
    if (btn) { btn.disabled = false; btn.setAttribute('aria-busy', 'false'); }
  }
}

const saveBtn = $('#briefSaveBtn');
if (saveBtn) saveBtn.onclick = saveBrief;
