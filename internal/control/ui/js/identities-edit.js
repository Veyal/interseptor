// identities-edit.js — the New / Edit identity sheet: name, role, notes and the
// header lines (validated as "Key: Value"). An edit keeps the identity as it
// was when the sheet opened; when a live update changes that identity on the
// server, the sheet shows a stale banner with Reload (take the server copy) and
// Overwrite (save this form anyway) instead of silently replacing the form.
import { $, toastError } from './core.js';
import { openSheet, closeSheet } from './sheet.js';
import * as M from './identities-model.js';
import { S, X, jsend, enc, el, btn, byName, setStatus } from './identities-core.js';

const SHEET_ID = 'identEditSheet';

function field(label, control) {
  const wrap = el('label', 'ident-field');
  wrap.append(el('span', 'ident-field-label', label), control);
  return wrap;
}

function fill(values) {
  $('#identEdName').value = values.name || '';
  $('#identEdRole').value = values.role || '';
  $('#identEdNotes').value = values.notes || '';
  $('#identEdHeaders').value = values.headers || '';
  validate();
}

function readForm() {
  return {
    name: $('#identEdName')?.value || '',
    role: $('#identEdRole')?.value || '',
    notes: $('#identEdNotes')?.value || '',
    headers: $('#identEdHeaders')?.value || '',
  };
}

function validate() {
  const msg = M.headerValidationMessage($('#identEdHeaders')?.value || '');
  const box = $('#identEdHeadersMsg');
  const ta = $('#identEdHeaders');
  if (box) box.textContent = msg;
  if (ta) ta.setAttribute('aria-invalid', msg ? 'true' : 'false');
  return !msg;
}

function setMsg(text, kind = 'status') {
  const m = $('#identEdMsg');
  if (!m) return;
  m.textContent = text || '';
  m.dataset.kind = kind;
  m.setAttribute('role', kind === 'error' ? 'alert' : 'status');
}

function paintStale() {
  const banner = $('#identEdStale');
  const save = $('#identEdSave');
  const stale = !!(S.edit && S.edit.stale && !S.edit.overwrite);
  if (banner) banner.hidden = !stale;
  if (save) save.disabled = stale || !!(S.edit && S.edit.saving);
}

// checkEditStale is called after every identities reload while the sheet is open.
export function checkEditStale() {
  const e = S.edit;
  if (!e || e.mode !== 'edit' || e.stale) return;
  if (M.isStale(e.baseline, byName(e.name))) { e.stale = true; paintStale(); }
}

export function openEdit({ mode = 'create', name: editName = '', opener = null, values = null } = {}) {
  const base = mode === 'edit' ? byName(editName) : null;
  if (mode === 'edit' && !base) { setStatus('That identity no longer exists', 'error'); return; }
  S.edit = { mode, name: editName, baseline: base ? { ...base } : null, stale: false, overwrite: false, saving: false };
  const body = el('form', 'ident-edit');
  body.noValidate = true;
  const stale = el('div', 'ident-callout ident-stale');
  stale.id = 'identEdStale';
  stale.dataset.kind = 'warn';
  stale.setAttribute('role', 'alert');
  stale.hidden = true;
  stale.append(el('span', '', 'This identity changed on the server while you were editing.'));
  stale.append(btn('Reload', () => {
    const latest = byName(S.edit.name);
    if (!latest) { setMsg('It was deleted or renamed elsewhere. Cancel, or save to create it again.', 'error'); S.edit.overwrite = true; paintStale(); return; }
    S.edit.baseline = { ...latest }; S.edit.stale = false; S.edit.overwrite = false;
    fill(latest); paintStale(); setMsg('Reloaded the server copy');
  }, 'btn xs'));
  stale.append(btn('Overwrite', () => { S.edit.overwrite = true; paintStale(); save(); }, 'btn xs danger'));

  const name = el('input', 'btn btn-field');
  name.id = 'identEdName'; name.required = true; name.autocomplete = 'off'; name.spellcheck = false; name.placeholder = 'e.g. admin, user-b';
  const role = el('input', 'btn btn-field');
  role.id = 'identEdRole'; role.autocomplete = 'off'; role.placeholder = 'e.g. admin, tenant user, anonymous';
  const notes = el('textarea', 'ident-notes-input');
  notes.id = 'identEdNotes'; notes.rows = 3;
  const headers = el('textarea', 'ident-headers-input');
  headers.id = 'identEdHeaders'; headers.rows = 6; headers.spellcheck = false; headers.placeholder = 'Cookie: session=…\nAuthorization: Bearer …';
  headers.setAttribute('aria-describedby', 'identEdHeadersHint identEdHeadersMsg');
  headers.addEventListener('input', validate);
  const hint = el('p', 'hint ident-field-hint', 'One "Key: Value" per line. Leave empty for an anonymous identity.');
  hint.id = 'identEdHeadersHint';
  const err = el('p', 'ident-field-error');
  err.id = 'identEdHeadersMsg';
  err.setAttribute('aria-live', 'polite');

  const foot = el('div', 'ident-edit-foot');
  const msg = el('span', 'statusline');
  msg.id = 'identEdMsg';
  msg.setAttribute('aria-live', 'polite');
  const cancel = btn('Cancel', () => closeSheet(SHEET_ID), 'btn');
  cancel.id = 'identEdCancel';
  const ok = el('button', 'btn btn-primary', mode === 'edit' ? 'Save' : 'Create');
  ok.type = 'submit'; ok.id = 'identEdSave';
  foot.append(msg, el('div', 'spacer'), cancel, ok);

  body.append(stale, field('Name', name), field('Role', role), field('Notes', notes), field('Headers', headers), hint, err, foot);
  body.addEventListener('submit', (e) => { e.preventDefault(); save(); });

  openSheet({
    id: SHEET_ID, title: mode === 'edit' ? 'Edit identity' : 'New identity', detents: ['half', 'full'], detent: 'full', opener,
    content: (host) => { host.textContent = ''; host.append(body); return host; },
    onClose: () => { S.edit = null; },
  });
  fill(base || values || {});
  setTimeout(() => $('#identEdName')?.focus(), 50);
}

async function save() {
  const e = S.edit;
  if (!e || e.saving) return;
  const form = readForm();
  const name = form.name.trim();
  if (!name) { setMsg('Name is required', 'error'); $('#identEdName')?.focus(); return; }
  if (!validate()) { setMsg('Fix the header lines first', 'error'); $('#identEdHeaders')?.focus(); return; }
  if (e.stale && !e.overwrite) { paintStale(); return; }
  const taken = S.ids.some((i) => i.name === name && !(e.mode === 'edit' && i.name === e.name));
  if (taken) { setMsg('An identity named "' + name + '" already exists', 'error'); $('#identEdName')?.focus(); return; }
  e.saving = true; paintStale(); setMsg('Saving…');
  try {
    // An identity deleted elsewhere while this sheet was open is re-created.
    if (e.mode === 'create' || !byName(e.name)) {
      await jsend('POST', '/api/authz/identity', { name, headers: form.headers, role: form.role, notes: form.notes });
    } else {
      const patch = M.identityPatch(e.baseline, form);
      if (patch) await jsend('PATCH', '/api/authz/identities/' + enc(e.name), patch);
    }
    S.sel = name;
    closeSheet(SHEET_ID);
    setStatus((e.mode === 'create' ? 'Created ' : 'Saved ') + name);
    await X.loadIdentities?.();
  } catch (err) {
    e.saving = false; paintStale();
    if (err.status === 409) setMsg('An identity named "' + name + '" already exists', 'error');
    else { setMsg('Save failed: ' + err.message, 'error'); toastError('Save failed', err); }
  }
}
