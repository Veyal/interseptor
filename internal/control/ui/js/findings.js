import { $, esc, escAttr, state, toast, api, openModal, closeModal, renderMD, wireRowKey, saveFile, uiPrompt } from './core.js';
import { flowPopup } from './flowmodal.js';

// Findings tab: the human reviews/curates the project's vulnerability findings.
// Each finding has a narrative body — an ordered sequence of text blocks (markdown)
// and flow-reference blocks (PoC request/response) interleaved freely, like a report.

const STATUSES = ['open', 'verified', 'false_positive', 'wont_fix', 'fixed'];
let findings = [], selFinding = null;

// Body editor state for the active finding.
let bodyBlocks = [];
let bodyFindingId = null;
let bodySaveTimer = null;

const sevColor = s => ({ High: 'var(--red)', Medium: 'var(--amber)', Low: 'var(--blue)', Info: 'var(--fg3)' }[s] || 'var(--fg3)');
const statusLabel = s => (s || '').replace(/_/g, ' ');

export async function loadFindings() {
  try { const d = await api('/api/findings'); findings = d.findings || []; renderFindings(); }
  catch (e) { toast(e.message); }
}

function renderFindings() {
  const box = $('#findList'); if (!box) return;
  const c = $('#findCount'); if (c) c.textContent = findings.length ? findings.length + ' finding' + (findings.length === 1 ? '' : 's') : '';
  if (!findings.length) {
    box.innerHTML = '<div class="hint" style="padding:12px">No findings yet — create one, or the AI records them as it tests.</div>';
    selFinding = null; renderFindingDetail(); return;
  }
  const pocCount = f => (f.blocks || []).filter(b => b.type === 'flow').length || (f.flows && f.flows.length) || 0;
  box.innerHTML = findings.map(f => `<div class="find-row${f.id === selFinding ? ' sel' : ''}" data-id="${f.id}">
    <span class="sev" style="color:${sevColor(f.severity)}">${esc(f.severity)}</span>
    <span class="find-title">${esc(f.title)}</span>
    <span class="find-meta">${esc(statusLabel(f.status))}${pocCount(f) ? ' · ' + pocCount(f) + ' PoC' : ''}${f.source === 'ai' ? ' · <span style="color:var(--accent)">AI</span>' : ''}</span>
  </div>`).join('');
  box.querySelectorAll('.find-row').forEach(el => { el.onclick = () => { selFinding = Number(el.dataset.id); renderFindings(); renderFindingDetail(); }; wireRowKey(el); });
  renderFindingDetail();
}

// ---- block editor --------------------------------------------------------

function renderBlockEl(b, i, total) {
  const isFirst = i === 0, isLast = i === total - 1;
  const upBtn = isFirst ? '' : `<button class="btn xs" data-mv="${i}" data-dir="-1" title="Move up" style="padding:1px 5px;font-size:11px">↑</button>`;
  const dnBtn = isLast ? '' : `<button class="btn xs" data-mv="${i}" data-dir="1" title="Move down" style="padding:1px 5px;font-size:11px">↓</button>`;
  const delBtn = `<button class="btn xs danger" data-del="${i}" title="Remove" style="padding:1px 5px;font-size:11px">✕</button>`;

  if (b.type === 'text') {
    const preview = b.md ? `<div class="md block-preview" style="padding:6px 8px;min-height:28px;cursor:text;border-radius:6px 6px 0 0" data-preview="${i}">${renderMD(b.md)}</div>` : '';
    const taStyle = b.md ? 'display:none' : '';
    return `<div class="find-block find-block-text" data-i="${i}" style="border:1px solid var(--line);border-radius:6px;margin-bottom:8px;overflow:hidden">
      ${preview}
      <textarea class="rep-edit block-text" data-i="${i}" rows="3" style="border-radius:${b.md ? '0' : '6px 6px 0 0'};border:none;border-bottom:1px solid var(--line2);resize:vertical;${taStyle}" placeholder="Write markdown…">${esc(b.md || '')}</textarea>
      <div class="row" style="padding:3px 6px;gap:3px;background:var(--bg3)">${upBtn}${dnBtn}<div class="spacer"></div>${delBtn}</div>
    </div>`;
  }

  // flow block
  const flowLabel = b.method
    ? `<span style="color:var(--accent);font-weight:700">${esc(b.method)}</span> <span style="font-family:var(--mono);font-size:11px;color:var(--fg2)">${esc(b.host || '')}${esc(b.path || '')}</span> <span class="hint">${b.status ? '→ ' + b.status : ''}</span>`
    : `<span class="hint">flow #${b.flowId} (deleted?)</span>`;
  return `<div class="find-block find-block-flow" data-i="${i}" data-flow="${b.flowId}"
    style="border:1px solid var(--line);border-radius:6px;padding:8px 10px;margin-bottom:8px;cursor:pointer">
    <div class="row" style="gap:8px;align-items:flex-start">
      <span style="font-size:9px;font-weight:700;letter-spacing:.6px;color:var(--fg3);padding-top:2px;white-space:nowrap">FLOW</span>
      <div style="flex:1;min-width:0">
        <div style="margin-bottom:4px">${flowLabel}</div>
        <input class="btn block-note" data-i="${i}" value="${escAttr(b.note || '')}"
          placeholder="annotation (optional)" style="width:100%;font-size:11px;background:var(--bg3)" onclick="event.stopPropagation()">
      </div>
      <div class="row" style="gap:3px;flex-shrink:0">${upBtn}${dnBtn}${delBtn}</div>
    </div>
  </div>`;
}

function renderBodyEditor(container, fid) {
  if (!bodyBlocks.length) {
    container.innerHTML = '<div class="hint" style="padding:6px 0">No content yet — add a text block or attach flows from Proxy History.</div>';
    return;
  }
  container.innerHTML = bodyBlocks.map((b, i) => renderBlockEl(b, i, bodyBlocks.length)).join('');

  // Preview → edit toggle for text blocks.
  container.querySelectorAll('[data-preview]').forEach(div => {
    div.onclick = () => {
      const i = Number(div.dataset.preview);
      div.style.display = 'none';
      const ta = container.querySelector(`.block-text[data-i="${i}"]`);
      if (ta) { ta.style.display = ''; ta.focus(); ta.style.borderRadius = '0'; }
    };
  });

  // Text block: save on blur, restore preview.
  container.querySelectorAll('.block-text').forEach(ta => {
    ta.addEventListener('blur', () => {
      const i = Number(ta.dataset.i);
      if (!bodyBlocks[i]) return;
      bodyBlocks[i].md = ta.value;
      scheduleSave(fid);
      // Refresh the block to show new preview.
      const blockEl = container.querySelector(`.find-block[data-i="${i}"]`);
      if (blockEl) blockEl.outerHTML = renderBlockEl(bodyBlocks[i], i, bodyBlocks.length);
      // Re-wire after DOM replacement (simple: re-render whole editor).
      renderBodyEditor(container, fid);
    });
  });

  // Flow note: save on blur.
  container.querySelectorAll('.block-note').forEach(inp => {
    inp.addEventListener('blur', () => {
      const i = Number(inp.dataset.i);
      if (bodyBlocks[i]) { bodyBlocks[i].note = inp.value; scheduleSave(fid); }
    });
    inp.addEventListener('click', e => e.stopPropagation());
  });

  // Move buttons.
  container.querySelectorAll('[data-mv]').forEach(btn => {
    btn.onclick = e => {
      e.stopPropagation();
      const i = Number(btn.dataset.mv), j = i + Number(btn.dataset.dir);
      if (j < 0 || j >= bodyBlocks.length) return;
      [bodyBlocks[i], bodyBlocks[j]] = [bodyBlocks[j], bodyBlocks[i]];
      renderBodyEditor(container, fid);
      scheduleSave(fid);
    };
  });

  // Delete buttons.
  container.querySelectorAll('[data-del]').forEach(btn => {
    btn.onclick = e => {
      e.stopPropagation();
      bodyBlocks.splice(Number(btn.dataset.del), 1);
      renderBodyEditor(container, fid);
      scheduleSave(fid);
    };
  });

  // Flow click → open flow modal.
  container.querySelectorAll('.find-block-flow').forEach(el => {
    el.onclick = ev => {
      if (ev.target.closest('[data-del],[data-mv],.block-note')) return;
      flowPopup(Number(el.dataset.flow));
    };
  });
}

function scheduleSave(fid) {
  clearTimeout(bodySaveTimer);
  bodySaveTimer = setTimeout(() => flushBodySave(fid), 700);
}

async function flushBodySave(fid) {
  if (!fid) return;
  // Strip enriched metadata before sending; store only type/md/flowId/note.
  const minimal = bodyBlocks.map(b => {
    const r = { type: b.type };
    if (b.md !== undefined) r.md = b.md;
    if (b.flowId) r.flowId = b.flowId;
    if (b.note) r.note = b.note;
    return r;
  });
  try {
    await api('/api/findings/' + fid, {
      method: 'PATCH',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ body: JSON.stringify(minimal) }),
    });
  } catch (e) { toast('body save: ' + e.message); }
}

// ---- detail pane ---------------------------------------------------------

function renderFindingDetail() {
  const box = $('#findDetail'); if (!box) return;
  const f = findings.find(x => x.id === selFinding);
  if (!f) { box.innerHTML = '<div class="hint" style="padding:16px">Select a finding.</div>'; return; }

  const statusSel = STATUSES.map(s => `<option value="${s}"${s === f.status ? ' selected' : ''}>${esc(statusLabel(s))}</option>`).join('');
  box.innerHTML = `<div class="find-head">
      <span class="sev" style="color:${sevColor(f.severity)};font-weight:700">${esc(f.severity)}</span>
      <b style="font-size:14px" id="findTitleText">${esc(f.title)}</b>
      <button class="btn xs" id="findRename" title="Rename finding" aria-label="Rename finding">✎</button>
      <div class="spacer"></div>
      <select id="findStatus" class="btn" style="background:var(--bg3)" aria-label="Finding status">${statusSel}</select>
      <button class="btn danger" id="findDelete">Delete</button>
    </div>
    ${f.target ? `<div class="hint" style="margin:2px 0 10px">Target: ${esc(f.target)}</div>` : ''}
    <div id="findBody" style="margin-bottom:4px"></div>
    <div class="row" style="gap:6px;margin-bottom:12px">
      <button class="btn" id="findAddText" style="font-size:11px;padding:3px 8px">＋ Text</button>
      <button class="btn" id="findAddFlow" style="font-size:11px;padding:3px 8px">＋ Flow (<span id="findSelCount">${state.selected ? state.selected.size : 0}</span> selected)</button>
    </div>
    ${f.fix !== undefined ? `<div style="margin-bottom:4px">
      <div class="find-sec" style="margin-bottom:4px">Remediation</div>
      <textarea id="findFix" class="rep-edit" rows="2" placeholder="Fix / remediation…" style="border-radius:6px">${esc(f.fix || '')}</textarea>
    </div>` : ''}`;

  // Load body blocks for this finding.
  bodyFindingId = f.id;
  bodyBlocks = (f.blocks || []).map(b => ({ ...b }));
  renderBodyEditor($('#findBody'), f.id);

  // Wire controls.
  $('#findRename').onclick = async () => {
    const t = await uiPrompt({ title: 'Rename finding', value: f.title, placeholder: 'Finding title' });
    if (t == null || t === f.title) return;
    try { await api('/api/findings/' + f.id, { method: 'PATCH', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ title: t }) }); }
    catch (err) { toast(err.message); }
  };
  $('#findStatus').onchange = async e => {
    try { await api('/api/findings/' + f.id, { method: 'PATCH', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ status: e.target.value }) }); }
    catch (err) { toast(err.message); }
  };
  $('#findDelete').onclick = async () => {
    try { await api('/api/findings/' + f.id, { method: 'DELETE' }); selFinding = null; }
    catch (err) { toast(err.message); }
  };
  $('#findAddText').onclick = () => {
    bodyBlocks.push({ type: 'text', md: '' });
    renderBodyEditor($('#findBody'), f.id);
    // Auto-focus the new textarea.
    const tas = document.querySelectorAll('#findBody .block-text');
    if (tas.length) tas[tas.length - 1].focus();
  };
  $('#findAddFlow').onclick = () => attachSelectedFlowsAsBlocks(f.id);
  $('#findFix')?.addEventListener('blur', async () => {
    const fix = $('#findFix').value;
    try { await api('/api/findings/' + f.id, { method: 'PATCH', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ fix }) }); }
    catch (err) { toast(err.message); }
  });
}

async function attachSelectedFlowsAsBlocks(findingId) {
  const ids = state.selected ? [...state.selected] : [];
  if (!ids.length) { toast('select flows in Proxy History first'); return; }
  try {
    for (const fid of ids) {
      await api('/api/findings/' + findingId + '/flows', {
        method: 'POST', headers: { 'content-type': 'application/json' },
        body: JSON.stringify({ flowId: fid }),
      });
    }
    toast('attached ' + ids.length + ' flow' + (ids.length === 1 ? '' : 's'));
  } catch (e) { toast(e.message); }
}

/* ---- create finding ---- */
$('#findNew') && ($('#findNew').onclick = () => { $('#fcTitle').value = ''; $('#fcSeverity').value = 'Medium'; $('#fcDetail').value = ''; openModal($('#findCreateModal')); $('#fcTitle').focus(); });
$('#fcClose') && ($('#fcClose').onclick = () => closeModal($('#findCreateModal')));
$('#findExport') && ($('#findExport').onclick = async () => {
  try {
    const md = await api('/api/findings/report');
    await saveFile(new Blob([md], { type: 'text/markdown' }), 'interceptor-report.md', 'text/markdown');
    toast('Downloading engagement report…');
  } catch (e) { if (!(e && e.name === 'AbortError')) toast(e.message); }
});
$('#fcSave') && ($('#fcSave').onclick = async () => {
  const title = $('#fcTitle').value.trim(); if (!title) { toast('title required'); return; }
  try {
    const f = await api('/api/findings', { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ title, severity: $('#fcSeverity').value, detail: $('#fcDetail').value, source: 'human' }) });
    closeModal($('#findCreateModal')); selFinding = f && f.id;
  } catch (e) { toast(e.message); }
});

/* ---- cross-linking: which findings reference a flow, and jump to one ---- */
export function flowFindings(flowId) {
  return findings.filter(f => (f.blocks || []).some(b => b.type === 'flow' && b.flowId === flowId) || (f.flows || []).some(x => x.flowId === flowId)).map(f => ({ id: f.id, title: f.title, severity: f.severity }));
}
export function openFinding(id) {
  selFinding = id;
  import('./scanner.js').then(m => {
    document.querySelector('.tab[data-tab="scanner"]')?.click();
    m.setScanSub('findings');
    renderFindings();
  });
}
export function addFlowToFinding(flowId) {
  if (flowId) pickFindingForFlows([flowId]);
}

/* ---- "➕ Add to finding" from the History selection bar ---- */
export function pickFindingForSelection() {
  pickFindingForFlows(state.selected ? [...state.selected] : []);
}
function pickFindingForFlows(ids) {
  if (!ids.length) { toast('select flows first'); return; }
  const list = $('#findPickList'); if (!list) return;
  const pocCount = f => (f.blocks || []).filter(b => b.type === 'flow').length || (f.flows && f.flows.length) || 0;
  const rows = findings.map(f => `<button class="btn find-pick" data-id="${f.id}" style="width:100%;text-align:left;margin-bottom:4px">
    <span class="sev" style="color:${sevColor(f.severity)}">${esc(f.severity)}</span> ${esc(f.title)}
    <span class="hint" style="float:right">${esc(statusLabel(f.status))}${pocCount(f) ? ' · ' + pocCount(f) + ' PoC' : ''}</span></button>`).join('');
  list.innerHTML = `<div class="hint" style="margin-bottom:8px">Attach ${ids.length} selected flow${ids.length === 1 ? '' : 's'} to:</div>${rows || '<div class="hint">No findings yet.</div>'}
    <button class="btn accent find-pick-new" style="width:100%;margin-top:6px">＋ New finding from these flows</button>`;
  openModal($('#findPickModal'));
  list.querySelectorAll('.find-pick').forEach(b => b.onclick = async () => {
    closeModal($('#findPickModal'));
    for (const fid of ids) {
      await api('/api/findings/' + b.dataset.id + '/flows', { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ flowId: fid }) }).catch(e => toast(e.message));
    }
    toast('attached ' + ids.length + ' flow' + (ids.length === 1 ? '' : 's'));
  });
  list.querySelector('.find-pick-new').onclick = async () => {
    closeModal($('#findPickModal'));
    const title = await uiPrompt({ title: 'Name the new finding', placeholder: 'e.g. IDOR on /api/user/{id}' });
    if (title == null) return;
    const f = await api('/api/findings', { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ title, severity: 'Medium', source: 'human', flowIds: ids }) }).catch(e => { toast(e.message); return null; });
    if (f) { import('./scanner.js').then(m => { document.querySelector('.tab[data-tab="scanner"]')?.click(); m.setScanSub('findings'); }); selFinding = f.id; toast('finding created'); }
  };
}
$('#fpClose') && ($('#fpClose').onclick = () => closeModal($('#findPickModal')));
$('#selAddFinding') && ($('#selAddFinding').onclick = pickFindingForSelection);
