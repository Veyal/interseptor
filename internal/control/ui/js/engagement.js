import { $, api, toast, renderLoadError, createAutosave, registerHook, state as appState } from './core.js';
import { openSheet } from './sheet.js';
import { projectState, scopeChipModel } from './project-state.js';
import { getShellApi } from './shell-hooks.js';
import { validateTargetLines, saveStatusText } from './settings-model.js';

/* ---- engagement brief (Settings > Scope, and the Engagement sheet) ---- */
// The project's authorisation/conduct statement. Agents read it over MCP and
// reports cite its version; the version bumps only when content changes.
//
// One renderer builds the form. Settings mounts it in #briefFormMount; the
// Engagement sheet moves that same node into the sheet while it is open and puts
// it back on close, so ids stay unique and both surfaces always look identical.
const FIELDS = [
  { name: 'scope', label: 'Authorised targets', placeholder: 'One target per line, e.g. *.example.com',
    help: 'In-scope hosts and APIs, one per line: api.example.com, *.example.com or 10.0.0.0/24. Lines with spaces are kept as notes.' },
  { name: 'authorisation', label: 'Authorisation', placeholder: 'Who authorised what, and for how long',
    help: 'Who authorised the work and the testing window (start and end dates).' },
  { name: 'conductRules', label: 'Rules of engagement', placeholder: 'e.g. own account only; destructive actions noted, not executed',
    help: 'What the test may and may not do.' },
  { name: 'rateLimits', label: 'Rate limits', placeholder: 'e.g. at most 1 req/s, request budget', help: '' },
  { name: 'doNotTouch', label: 'Do not touch', placeholder: 'Hosts, endpoints or data that are off limits', help: '' },
  { name: 'credentialPolicy', label: 'Credential policy', placeholder: 'e.g. never print credential values', help: '' },
];
const NAMES = FIELDS.map(f => f.name);
const AUTOSAVE_MS = 1500;
// The form node is held here (not looked up by id) so it survives being detached
// when another surface reuses the shared sheet.
let formNode = null;
let sheetOpen = false;
const q = id => (formNode ? formNode.querySelector('#' + id) : null);
const fieldEl = name => q('brief-' + name);
let loaded = {};
let lastError = false;
let unsubscribeSheet = null;

const el = (tag, cls, text) => {
  const n = document.createElement(tag);
  if (cls) n.className = cls;
  if (text != null) n.textContent = text;
  return n;
};

function readForm() {
  const out = {};
  NAMES.forEach(f => { out[f] = fieldEl(f)?.value || ''; });
  return out;
}
const serialize = () => JSON.stringify(readForm());

function setStatus(kind) {
  const status = q('briefSaveStatus');
  const btn = q('briefSaveBtn');
  if (status) status.textContent = saveStatusText(kind === 'dirty' && lastError ? 'error' : kind);
  if (btn) { const busy = kind === 'saving'; btn.disabled = busy; btn.setAttribute('aria-busy', busy ? 'true' : 'false'); }
}

async function persistBrief(json) {
  lastError = false;
  try {
    const saved = await api('/api/engagement-brief', {
      method: 'PUT', headers: { 'content-type': 'application/json' }, body: json,
    });
    loaded = saved || {};
    renderVersion();
    projectState.refresh({ reason: 'engagement.save' });
    toast('Engagement brief saved (version ' + loaded.version + ')', 'success');
  } catch (e) {
    lastError = true;
    toast('Engagement brief not saved: ' + e.message, 'error');
    throw e;
  }
}
const autosave = createAutosave({ delay: AUTOSAVE_MS, save: persistBrief, onStatus: setStatus });

function renderVersion() {
  const v = q('briefVersion');
  if (v) v.textContent = loaded.version > 0 ? 'Version ' + loaded.version : 'No brief recorded yet';
}

// validateScope paints the live error list under the targets field and returns ok.
function validateScope() {
  const area = fieldEl('scope');
  const box = q('brief-scope-err');
  if (!area || !box) return true;
  const res = validateTargetLines(area.value);
  box.textContent = '';
  box.hidden = res.ok;
  res.errors.forEach(e => box.appendChild(el('span', 'field-error-line', 'Line ' + e.line + ': ' + e.message)));
  area.setAttribute('aria-invalid', res.ok ? 'false' : 'true');
  return res.ok;
}

function onInput() {
  if (!validateScope()) { setStatus('invalid'); return; }
  autosave.schedule(serialize());
}

async function onSaveClick() {
  if (!validateScope()) { setStatus('invalid'); fieldEl('scope')?.focus(); toast('Fix the highlighted targets first', 'error'); return; }
  autosave.schedule(serialize());
  try {
    if (!autosave.isDirty()) { toast('Engagement brief is up to date'); return; }
    await autosave.flush();
  } catch (e) { /* persistBrief already reported it; the status text offers Retry */ }
}

function fieldBlock(f) {
  const wrap = el('div', 'brief-field');
  const label = el('label', 'hint', f.label);
  label.setAttribute('for', 'brief-' + f.name);
  const area = el('textarea', 'raw u-mb-2');
  area.id = 'brief-' + f.name;
  area.maxLength = 16384;
  area.spellcheck = false;
  area.placeholder = f.placeholder;
  const describedBy = [];
  if (f.help) {
    const help = el('p', 'hint brief-help', f.help);
    help.id = 'brief-' + f.name + '-help';
    describedBy.push(help.id);
    wrap.append(label, help, area);
  } else wrap.append(label, area);
  if (f.name === 'scope') {
    const err = el('p', 'field-error');
    err.id = 'brief-scope-err';
    err.hidden = true;
    err.setAttribute('aria-live', 'polite');
    describedBy.push(err.id);
    wrap.appendChild(err);
  }
  if (describedBy.length) area.setAttribute('aria-describedby', describedBy.join(' '));
  return wrap;
}

// renderBriefForm builds the single brief form once and (re)parents it into host.
function renderBriefForm(host) {
  let form = formNode;
  if (!form) {
    form = el('div', 'brief-form');
    form.id = 'briefForm';
    const meta = el('p', 'hint brief-meta', 'Recorded: ');
    const version = el('b', null, 'No brief recorded yet');
    version.id = 'briefVersion';
    meta.appendChild(version);
    form.appendChild(meta);
    FIELDS.forEach(f => form.appendChild(fieldBlock(f)));
    const row = el('div', 'settings-save-row');
    const save = el('button', 'btn btn-primary', 'Save brief');
    save.type = 'button';
    save.id = 'briefSaveBtn';
    save.addEventListener('click', onSaveClick);
    const status = el('span', 'settings-status');
    status.id = 'briefSaveStatus';
    status.setAttribute('role', 'status');
    status.setAttribute('aria-live', 'polite');
    row.append(save, status);
    form.appendChild(row);
    form.addEventListener('input', onInput);
    formNode = form;
  }
  if (host && form.parentNode !== host) host.appendChild(form);
  return form;
}

// goHome puts the form back in Settings unless the sheet currently shows it.
function goHome() {
  const live = sheetOpen && formNode && formNode.isConnected;
  const home = $('#briefFormMount');
  if (home && !live) renderBriefForm(home);
}

function renderBrief(brief) {
  loaded = brief || {};
  NAMES.forEach(f => { const area = fieldEl(f); if (area) area.value = loaded[f] || ''; });
  renderVersion();
  validateScope();
  autosave.setBaseline(serialize());
}

export async function loadEngagementBrief() {
  const state = $('#briefLoadState');
  goHome();
  if (autosave.isDirty()) return; // never clobber an in-progress edit with a live update
  try {
    renderBrief(await api('/api/engagement-brief'));
    if (state) { state.textContent = ''; state.classList.add('u-hidden'); }
  } catch (e) {
    if (state) { state.classList.remove('u-hidden'); renderLoadError(state, 'Engagement brief', e, loadEngagementBrief, false); }
  }
}

/* ---- the Engagement sheet ---- */

function sheetRow(key, ...nodes) {
  const row = el('div', 'eng-row');
  row.appendChild(el('span', 'eng-key', key));
  const val = el('span', 'eng-val');
  val.append(...nodes);
  row.appendChild(val);
  return row;
}

function actionButton(text, onClick) {
  const b = el('button', 'btn', text);
  b.type = 'button';
  b.addEventListener('click', onClick);
  return b;
}

// The strip owns scope: this switch mirrors it and clicks it, so there is one path.
function scopeSwitch() {
  const b = el('button', 'uisw eng-scope');
  b.type = 'button';
  b.id = 'engScopeToggle';
  const sync = () => {
    const m = scopeChipModel(projectState.get(), { filterOn: !!appState.inScopeOnly });
    b.textContent = m.text;
    b.setAttribute('aria-pressed', m.checked ? 'true' : 'false');
    b.setAttribute('aria-disabled', m.disabled ? 'true' : 'false');
    b.classList.toggle('on', m.checked);
    b.title = m.title;
  };
  b.addEventListener('click', () => {
    if (b.getAttribute('aria-disabled') === 'true') { getShellApi().openSettings?.('scope'); return; }
    document.getElementById('ctxScope')?.click();
    sync();
  });
  sync();
  return { node: b, sync };
}

function sheetContent(form) {
  return (body) => {
    const wrap = el('div', 'eng-sheet');
    const name = document.getElementById('projNameHint')?.textContent || 'default';
    wrap.appendChild(sheetRow('Engagement', el('b', null, name), actionButton('Change project', () => getShellApi().openProject?.())));
    const sw = scopeSwitch();
    wrap.appendChild(sheetRow('Scope capture', sw.node));
    const ids = el('span', 'eng-identities');
    const syncIds = () => {
      const list = projectState.get().identities;
      ids.textContent = list.length ? list.length + (list.length === 1 ? ' identity: ' : ' identities: ') + list.map(i => i.name).join(', ') : 'No identities yet';
    };
    syncIds();
    wrap.appendChild(sheetRow('Identities', ids, actionButton('Manage identities', () => { import('./authz.js').then(m => m.openAuthz()); })));
    wrap.appendChild(form);
    body.appendChild(wrap);
    if (unsubscribeSheet) unsubscribeSheet();
    unsubscribeSheet = projectState.subscribe(() => { sw.sync(); syncIds(); });
  };
}

function closeSheetCleanup() {
  sheetOpen = false;
  if (unsubscribeSheet) { unsubscribeSheet(); unsubscribeSheet = null; }
  goHome();
}

export function openEngagementSheet() {
  const form = renderBriefForm();
  sheetOpen = true;
  openSheet({ id: 'engagementSheet', title: 'Engagement', detents: ['half', 'full'], content: sheetContent(form), onClose: closeSheetCleanup });
  if (!autosave.isDirty()) loadEngagementBrief();
}

registerHook('openEngagementSheet', openEngagementSheet);
renderBriefForm($('#briefFormMount'));
