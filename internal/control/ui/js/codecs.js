import { $, api, toast, openModal, closeModal, esc, escAttr, state, wireRowKey, renderMD, uiConfirm } from './core.js';

const TEMPLATE = `meta = {
    "id": "aes-content-field",
    "title": "JSON content (prefix+AES-ECB)",
    "apply_on_send": False,
}

# Engagement secret from client JS — replace per target.
SECRET = "replace-me"

def _key(prefix):
    return hash("sha512", prefix + SECRET)[:32]

def match(flow, side):
    raw = flow.req_body if side == "req" else flow.res_body
    return '"content"' in raw

def decode(flow, side, raw):
    obj = json_decode(raw)
    blob = obj.get("content") or ""
    if len(blob) < 33:
        return {"plaintext": raw, "note": "no content field"}
    prefix = blob[:32]
    pt = aes_ecb_decrypt(_key(prefix), blob[32:])
    return {"plaintext": pt, "fields": {"content": pt}, "note": "prefix=" + prefix}

def encode(flow, side, plaintext):
    obj = json_decode(flow.req_body if side == "req" else flow.res_body)
    blob = obj.get("content") or ""
    prefix = blob[:32] if len(blob) >= 32 else "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
    obj["content"] = prefix + aes_ecb_encrypt(_key(prefix), plaintext)
    return json_encode(obj)
`;

let codecSel = '';
let codecMode = 'code';
let codecDocsLoaded = false;
let codecBusy = false;
let codecLoadEpoch = 0;

function codecEditorMatches(epoch, id, source) {
  return epoch === codecLoadEpoch
    && ($('#codecId').value || '').trim() === id
    && ($('#codecSrc').value || '') === source;
}

function setCodecBusy(busy) {
  codecBusy = !!busy;
  ['#codecSave', '#codecTest', '#codecDelete'].forEach(sel => {
    const b = $(sel); if (!b) return;
    b.disabled = codecBusy;
    b.setAttribute('aria-busy', codecBusy ? 'true' : 'false');
  });
}

function codecsDirLabel(dir) {
  if (!dir) return { text: 'No project codecs dir', title: '' };
  const parts = String(dir).replace(/\\/g, '/').split('/').filter(Boolean);
  const short = parts.length <= 2 ? dir : '…/' + parts.slice(-2).join('/');
  return { text: short, title: dir };
}

function updateCodecFlowHint() {
  const el = $('#codecFlowHint');
  if (!el) return;
  el.textContent = state.selId != null ? ('Test flow: #' + state.selId + ' (selected)') : 'Test uses latest captured flow';
}

function codecSetMode(mode) {
  codecMode = mode;
  const seg = $('#codecModeSeg');
  if (seg) seg.querySelectorAll('[data-mode]').forEach(b => {
    const on = b.dataset.mode === mode;
    b.classList.toggle('on', on);
    b.setAttribute('aria-selected', on ? 'true' : 'false');
    b.tabIndex = on ? 0 : -1;
  });
  const panes = { code: '#codecPaneCode', docs: '#codecPaneDocs' };
  Object.entries(panes).forEach(([m, sel]) => { const el = $(sel); if (el) { const on = m === mode; el.style.display = on ? '' : 'none'; el.hidden = !on; } });
  if (mode === 'docs') loadCodecDocs();
}
function wireCodecModeKeys(seg) {
  if (!seg) return;
  const tabs = [...seg.querySelectorAll('[role="tab"]')];
  tabs.forEach((tab, i) => tab.addEventListener('keydown', e => {
    let next = -1;
    if (e.key === 'ArrowRight' || e.key === 'ArrowDown') next = (i + 1) % tabs.length;
    else if (e.key === 'ArrowLeft' || e.key === 'ArrowUp') next = (i - 1 + tabs.length) % tabs.length;
    else if (e.key === 'Home') next = 0;
    else if (e.key === 'End') next = tabs.length - 1;
    else return;
    e.preventDefault(); tabs[next].focus(); tabs[next].click();
  }));
}

async function loadCodecDocs() {
  if (codecDocsLoaded) return;
  const box = $('#codecDocs');
  if (!box) return;
  try {
    const d = await api('/api/codecs/reference');
    box.innerHTML = renderMD(d.markdown || '');
    codecDocsLoaded = true;
  } catch (e) {
    box.innerHTML = '<div class="state-error"><div class="state-error-icon"><svg class="icon" aria-hidden="true" focusable="false"><use href="#i-warning"/></svg></div><p class="state-error-msg" role="alert">' + esc(e.message) + '</p><button type="button" class="btn" data-codec-docs-retry>Retry</button></div>';
    const retry=box.querySelector('[data-codec-docs-retry]');if(retry)retry.onclick=loadCodecDocs;
  }
}

function codecRow(c) {
  const id = c.id || '';
  const title = (c.meta && c.meta.title) || id;
  const err = !!c.error;
  const send = !!(c.meta && c.meta.applyOnSend);
  const badges = [
    err ? '<span class="checks-cat" style="color:var(--red);border-color:var(--red)">error</span>' : '',
    send ? '<span class="checks-cat" style="color:var(--accent);border-color:var(--accent)">re-encode on send</span>' : '<span class="checks-cat">display</span>',
  ].filter(Boolean).join('');
  return `<div class="checks-row checks-pick codecs-row${codecSel === id ? ' sel' : ''}" id="codec-option-${escAttr(id)}" data-id="${escAttr(id)}" role="option" tabindex="${codecSel === id ? '0' : '-1'}" aria-selected="${codecSel === id ? 'true' : 'false'}" title="${escAttr(err ? c.error : title)}" aria-label="codec ${escAttr(id)}">
    <div class="checks-body">
      <span class="checks-title" style="color:${err ? 'var(--red)' : 'var(--fg)'}">${esc(title)}${err ? ' <svg class="icon" aria-hidden="true" focusable="false"><use href="#i-warning"/></svg>' : ''}</span>
      <div class="checks-meta"><span class="checks-cat">${esc(id)}</span>${badges}</div>
    </div>
  </div>`;
}

// The codec list is a listbox. wireRowKey would promote each option to a
// button, which makes screen-reader semantics contradictory and breaks arrow
// navigation. Keep option semantics and provide the same Enter/Space affordance
// locally, while leaving controls inside a row alone.
function wireCodecRow(el, onActivate, list) {
  if (!el) return;
  // role=option is present in codecRow before this call, so the shared helper
  // adds Enter/Space activation without changing the listbox semantics.
  wireRowKey(el, onActivate);
  el.onclick = onActivate;
  el.onkeydown = e => {
    if (['ArrowDown','ArrowUp','Home','End'].includes(e.key)) {
      e.preventDefault();
      const options = [...list.querySelectorAll('.codecs-row[data-id]')].filter(option => option.style.display !== 'none');
      let i = options.indexOf(el);
      if (e.key === 'ArrowDown') i = (i + 1) % options.length;
      else if (e.key === 'ArrowUp') i = (i - 1 + options.length) % options.length;
      else if (e.key === 'Home') i = 0;
      else i = options.length - 1;
      options.forEach((option, index) => { option.tabIndex = index === i ? 0 : -1; });
      options[i]?.focus();
      return;
    }
  };
}

function codecsApplyFilter() {
  const q = (($('#codecsSearch') || {}).value || '').trim().toLowerCase();
  const box = $('#codecsList');
  if (!box) return;
  const rows = [...box.querySelectorAll('.codecs-row')];
  rows.forEach(row => {
    const hay = (row.querySelector('.checks-title')?.textContent || '') + ' ' + (row.dataset.id || '');
    row.style.display = !q || hay.toLowerCase().includes(q) ? '' : 'none';
  });
  const visible = rows.filter(row => row.style.display !== 'none');
  const entry = visible.find(row => row.dataset.id === codecSel) || visible[0];
  rows.forEach(row => { row.tabIndex = row === entry ? 0 : -1; });
}

export async function loadCodecsList() {
  const box = $('#codecsList');
  if (!box) return;
  try {
    const d = await api('/api/codecs');
    const list = d.codecs || [];
    const hint = $('#codecsDirHint');
    if (hint) {
      const lab = codecsDirLabel(d.dir || '');
      hint.textContent = lab.text;
      hint.title = lab.title;
    }
    if (!list.length) {
      box.innerHTML = '<div class="state-empty" style="padding:18px 14px"><div class="state-empty-title">No codecs yet</div><p class="state-empty-hint">New → edit Starlark on <b>Code</b> or consult <b>Docs</b> → Save. Files land under this project\'s <code>codecs/</code>.</p></div>';
      return;
    }
    box.innerHTML = list.map(codecRow).join('');
    const options = [...box.querySelectorAll('.codecs-row[data-id]')];
    if (!options.some(el => el.tabIndex === 0) && options[0]) options[0].tabIndex = 0;
    box.querySelectorAll('.codecs-row[data-id]').forEach(el => {
      const open = () => openCodec(el.dataset.id);
      wireCodecRow(el, open, box);
    });
    codecsApplyFilter();
  } catch (e) {
    box.innerHTML = `<div class="state-error"><div class="state-error-icon"><svg class="icon" aria-hidden="true" focusable="false"><use href="#i-warning"/></svg></div><p class="state-error-msg" role="alert">Couldn't load codecs: ${esc(e.message)}</p><button type="button" class="btn" data-codecs-list-retry>Retry</button></div>`;
    box.querySelector('[data-codecs-list-retry]')?.addEventListener('click', loadCodecsList);
  }
}

async function openCodec(id) {
  const epoch = ++codecLoadEpoch;
  codecSel = id;
  try {
    const d = await api('/api/codecs/' + encodeURIComponent(id));
    if (epoch!==codecLoadEpoch || codecSel!==id) return;
    $('#codecId').value = d.id || id;
    $('#codecSrc').value = d.source || '';
    const out = $('#codecOut');
    if (out) {
      out.innerHTML = d.error
        ? `<div class="check-status check-status-error">Loaded <b>${esc(id)}</b> with compile error<pre>${esc(d.error)}</pre></div>`
        : `<div class="check-status check-status-pending">Loaded <b>${esc(id)}</b>. Edit on <b>Code</b>, Test, then Save.</div>`;
    }
    codecSetMode('code');
    loadCodecsList();
  } catch (e) { if (epoch===codecLoadEpoch && codecSel===id) toast(e.message); }
}

export function openCodecs() {
  codecLoadEpoch++;
  openModal($('#codecsModal'));
  const s = $('#codecsSearch');
  if (s) s.value = '';
  codecSel = '';
  $('#codecId').value = '';
  $('#codecSrc').value = TEMPLATE;
  const out = $('#codecOut');
  if (out) out.innerHTML = '<div class="check-status check-status-pending">New codec — set an id, write Starlark on <b>Code</b> or consult <b>Docs</b>, Test, then Save.</div>';
  updateCodecFlowHint();
  codecSetMode('code');
  loadCodecsList();
}

function codecNew() {
  codecLoadEpoch++;
  codecSel = '';
  $('#codecId').value = 'aes-content-field';
  $('#codecSrc').value = TEMPLATE;
  const out = $('#codecOut');
  if (out) out.innerHTML = '<div class="check-status check-status-pending">New codec — set an id, write Starlark on <b>Code</b> or consult <b>Docs</b>, Test, then Save.</div>';
  codecSetMode('code');
  loadCodecsList();
  $('#codecId')?.focus();
}

async function codecSave() {
  if (codecBusy) return;
  const id = ($('#codecId').value || '').trim();
  const source = $('#codecSrc').value || '';
  const epoch = codecLoadEpoch;
  if (!id) { toast('enter a codec id'); return; }
  const out = $('#codecOut');
  if (out) out.innerHTML = '<div class="check-status check-status-pending">saving…</div>';
  const button = $('#codecSave');
  setCodecBusy(true);
  if (button) button.textContent = 'Saving…';
  try {
    await api('/api/codecs/' + encodeURIComponent(id), {
      method: 'PUT', headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ source }),
    });
    if (codecEditorMatches(epoch, id, source)) {
      codecSel = id;
      if (out) out.innerHTML = '<div class="check-status check-status-ok">Saved ✓ — available in History / Repeater <b>Decoded</b> views.</div>';
    }
    toast('codec saved');
    loadCodecsList();
  } catch (e) {
    if (out && codecEditorMatches(epoch, id, source)) out.innerHTML = '<div class="check-status check-status-error"><b>Save failed</b><pre>' + esc(e.message) + '</pre></div>';
    else toast(e.message);
  } finally {
    setCodecBusy(false);
    if (button) button.textContent = 'Save';
  }
}

async function codecDelete() {
  if (codecBusy) return;
  const id = ($('#codecId').value || codecSel || '').trim();
  const source = $('#codecSrc').value || '';
  const epoch = codecLoadEpoch;
  if (!id) return;
  if (!await uiConfirm('Delete codec', `Delete message codec <b>${esc(id)}</b>? Its Starlark source will be removed.`, 'Delete', 'btn danger', 'var(--red)')) return;
  if (!codecEditorMatches(epoch, id, source)) return;
  setCodecBusy(true);
  try {
    await api('/api/codecs/' + encodeURIComponent(id), { method: 'DELETE' });
    toast('deleted');
    if (codecEditorMatches(epoch, id, source)) codecNew();
    loadCodecsList();
  } catch (e) { toast(e.message); }
  finally { setCodecBusy(false); }
}

async function codecTest() {
  if (codecBusy) return;
  const out = $('#codecOut');
  if (out) out.innerHTML = '<div class="check-status check-status-pending">running…</div>';
  const button = $('#codecTest');
  const id = ($('#codecId').value || '').trim();
  const epoch = codecLoadEpoch;
  setCodecBusy(true);
  if (button) button.textContent = 'Testing…';
  const source = $('#codecSrc').value || '';
  const flowId = state.selId || 0;
  try {
    const d = await api('/api/codecs/test', {
      method: 'POST', headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ source, flowId, side: 'req' }),
    });
    if (!codecEditorMatches(epoch, id, source)) return;
    if (!out) return;
    if (d.error) {
      out.innerHTML = '<div class="check-status check-status-error"><b>Compile/runtime error</b><pre>' + esc(d.error) + '</pre></div>';
      return;
    }
    if (d.note && !d.matched) {
      out.innerHTML = `<div class="check-status check-status-pending"><div class="hint">${esc(d.note)}</div></div>`;
      return;
    }
    if (!d.matched) {
      out.innerHTML = `<div class="check-status check-status-ok"><div class="hint">no match on flow #${esc(String(d.flowId || flowId || '?'))}</div><div style="color:var(--accent);margin-top:4px">✓ Codec compiles — match() skipped this flow.</div></div>`;
      return;
    }
    const note = (d.title || d.codecId || 'matched') + ' · flow #' + (d.flowId || flowId || '?');
    const body = esc(d.plaintext || '').slice(0, 4000);
    out.innerHTML = `<div class="check-status check-status-ok"><div class="hint" style="margin-bottom:6px">${esc(note)}${d.note ? ' — ' + esc(d.note) : ''}</div><pre style="white-space:pre-wrap;margin:0;font-family:var(--mono);font-size:var(--fs-xs)">${body}</pre></div>`;
  } catch (e) {
    if (out && codecEditorMatches(epoch, id, source)) out.innerHTML = '<div class="check-status check-status-error"><b>Request failed</b><pre>' + esc(e.message) + '</pre></div>';
    else toast(e.message);
  } finally {
    setCodecBusy(false);
    if (button) button.textContent = 'Test ▸';
  }
}

if ($('#codecsBtn')) $('#codecsBtn').onclick = openCodecs;
if ($('#codecsClose')) $('#codecsClose').onclick = () => closeModal($('#codecsModal'));
if ($('#codecNew')) $('#codecNew').onclick = codecNew;
if ($('#codecSave')) $('#codecSave').onclick = codecSave;
if ($('#codecDelete')) $('#codecDelete').onclick = codecDelete;
if ($('#codecTest')) $('#codecTest').onclick = codecTest;
if ($('#codecModeSeg')) $('#codecModeSeg').querySelectorAll('[data-mode]').forEach(b => b.onclick = () => codecSetMode(b.dataset.mode));
wireCodecModeKeys($('#codecModeSeg'));
if ($('#codecsSearch')) $('#codecsSearch').addEventListener('input', codecsApplyFilter);
