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
  });
  const panes = { code: '#codecPaneCode', describe: '#codecPaneDescribe', docs: '#codecPaneDocs' };
  Object.entries(panes).forEach(([m, sel]) => { const el = $(sel); if (el) el.style.display = m === mode ? '' : 'none'; });
  if (mode === 'docs') loadCodecDocs();
  if (mode === 'describe') setTimeout(() => $('#codecDescribe')?.focus(), 0);
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
    box.innerHTML = '<div class="state-error"><div class="state-error-icon">⚠</div><p class="state-error-msg">' + esc(e.message) + '</p></div>';
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
  return `<div class="checks-row checks-pick codecs-row${codecSel === id ? ' sel' : ''}" data-id="${escAttr(id)}" title="${escAttr(err ? c.error : title)}" aria-label="codec ${escAttr(id)}">
    <div class="checks-body">
      <span class="checks-title" style="color:${err ? 'var(--red)' : 'var(--fg)'}">${esc(title)}${err ? ' ⚠' : ''}</span>
      <div class="checks-meta"><span class="checks-cat">${esc(id)}</span>${badges}</div>
    </div>
  </div>`;
}

function codecsApplyFilter() {
  const q = (($('#codecsSearch') || {}).value || '').trim().toLowerCase();
  const box = $('#codecsList');
  if (!box) return;
  box.querySelectorAll('.codecs-row').forEach(row => {
    const hay = (row.querySelector('.checks-title')?.textContent || '') + ' ' + (row.dataset.id || '');
    row.style.display = !q || hay.toLowerCase().includes(q) ? '' : 'none';
  });
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
      box.innerHTML = '<div class="state-empty" style="padding:18px 14px"><div class="state-empty-title">No codecs yet</div><p class="state-empty-hint">New → edit Starlark on <b>Code</b> (or use <b>Describe</b>) → Save. Files land under this project\'s <code>codecs/</code>.</p></div>';
      return;
    }
    box.innerHTML = list.map(codecRow).join('');
    box.querySelectorAll('.codecs-row[data-id]').forEach(el => {
      const open = () => openCodec(el.dataset.id);
      el.onclick = open;
      wireRowKey(el, open);
    });
    codecsApplyFilter();
  } catch (e) {
    box.innerHTML = `<div class="state-error"><div class="state-error-icon">⚠</div><p class="state-error-msg">Couldn't load codecs: ${esc(e.message)}</p></div>`;
  }
}

async function openCodec(id) {
  codecSel = id;
  try {
    const d = await api('/api/codecs/' + encodeURIComponent(id));
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
  } catch (e) { toast(e.message); }
}

export function openCodecs() {
  openModal($('#codecsModal'));
  const s = $('#codecsSearch');
  if (s) s.value = '';
  codecSel = '';
  $('#codecId').value = '';
  $('#codecSrc').value = TEMPLATE;
  const out = $('#codecOut');
  if (out) out.innerHTML = '<div class="check-status check-status-pending">New codec — set an id, write Starlark on <b>Code</b> (or use <b>Describe</b>), Test, then Save.</div>';
  updateCodecFlowHint();
  codecSetMode('code');
  loadCodecsList();
}

function codecNew() {
  codecSel = '';
  $('#codecId').value = 'aes-content-field';
  $('#codecSrc').value = TEMPLATE;
  const out = $('#codecOut');
  if (out) out.innerHTML = '<div class="check-status check-status-pending">New codec — set an id, write Starlark on <b>Code</b> (or use <b>Describe</b>), Test, then Save.</div>';
  codecSetMode('code');
  loadCodecsList();
  $('#codecId')?.focus();
}

async function codecSave() {
  const id = ($('#codecId').value || '').trim();
  const source = $('#codecSrc').value || '';
  if (!id) { toast('enter a codec id'); return; }
  const out = $('#codecOut');
  if (out) out.innerHTML = '<div class="check-status check-status-pending">saving…</div>';
  try {
    await api('/api/codecs/' + encodeURIComponent(id), {
      method: 'PUT', headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ source }),
    });
    codecSel = id;
    if (out) out.innerHTML = '<div class="check-status check-status-ok">Saved ✓ — available in History / Repeater <b>Decoded</b> views.</div>';
    toast('codec saved');
    loadCodecsList();
  } catch (e) {
    if (out) out.innerHTML = '<div class="check-status check-status-error"><b>Save failed</b><pre>' + esc(e.message) + '</pre></div>';
    else toast(e.message);
  }
}

async function codecDelete() {
  const id = ($('#codecId').value || codecSel || '').trim();
  if (!id) return;
  if (!await uiConfirm('Delete codec', `Delete message codec <b>${esc(id)}</b>? Its Starlark source will be removed.`, 'Delete', 'btn danger', 'var(--red)')) return;
  try {
    await api('/api/codecs/' + encodeURIComponent(id), { method: 'DELETE' });
    toast('deleted');
    codecNew();
    loadCodecsList();
  } catch (e) { toast(e.message); }
}

async function codecTest() {
  const out = $('#codecOut');
  if (out) out.innerHTML = '<div class="check-status check-status-pending">running…</div>';
  const source = $('#codecSrc').value || '';
  const flowId = state.selId || 0;
  try {
    const d = await api('/api/codecs/test', {
      method: 'POST', headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ source, flowId, side: 'req' }),
    });
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
    out.innerHTML = `<div class="check-status check-status-ok"><div class="hint" style="margin-bottom:6px">${esc(note)}${d.note ? ' — ' + esc(d.note) : ''}</div><pre style="white-space:pre-wrap;margin:0;font-family:var(--mono);font-size:11.5px">${body}</pre></div>`;
  } catch (e) {
    if (out) out.innerHTML = '<div class="check-status check-status-error"><b>Request failed</b><pre>' + esc(e.message) + '</pre></div>';
    else toast(e.message);
  }
}

async function codecAiGenerate() {
  if (state.aiDisabled) { toast('AI features are disabled — enable in Settings → AI assist'); return; }
  const desc = ($('#codecDescribe') || {}).value?.trim();
  if (!desc) { toast('describe the wire format / crypto to unwrap'); $('#codecDescribe')?.focus(); return; }
  const status = $('#codecAiStatus'), btn = $('#codecAiGen');
  if (status) status.textContent = 'generating…';
  if (btn) btn.disabled = true;
  try {
    const r = await api('/api/ai/codecs/generate', {
      method: 'POST', headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ description: desc, source: $('#codecSrc').value || '', flowId: state.selId || 0 }),
    });
    if (r.error && !r.source) {
      if (status) status.innerHTML = '<span style="color:var(--red)">' + esc(r.error) + '</span>';
      return;
    }
    if (r.source) $('#codecSrc').value = r.source;
    if (r.suggestedId && !$('#codecId').value.trim()) $('#codecId').value = r.suggestedId;
    codecSetMode('code');
    if (status) status.textContent = 'generated — running test…';
    await codecTest();
    if (status) {
      if (r.error) status.innerHTML = '<span style="color:var(--amber)">compiled after retry; review output</span>';
      else status.textContent = 'done — review code, set id, Save';
    }
  } catch (e) {
    if (status) status.innerHTML = '<span style="color:var(--red)">' + esc(e.message) + '</span>';
  } finally { if (btn) btn.disabled = false; }
}

if ($('#codecsBtn')) $('#codecsBtn').onclick = openCodecs;
if ($('#codecsClose')) $('#codecsClose').onclick = () => closeModal($('#codecsModal'));
if ($('#codecNew')) $('#codecNew').onclick = codecNew;
if ($('#codecSave')) $('#codecSave').onclick = codecSave;
if ($('#codecDelete')) $('#codecDelete').onclick = codecDelete;
if ($('#codecTest')) $('#codecTest').onclick = codecTest;
if ($('#codecModeSeg')) $('#codecModeSeg').querySelectorAll('[data-mode]').forEach(b => b.onclick = () => codecSetMode(b.dataset.mode));
if ($('#codecAiGen')) $('#codecAiGen').onclick = codecAiGenerate;
if ($('#codecsSearch')) $('#codecsSearch').addEventListener('input', codecsApplyFilter);
