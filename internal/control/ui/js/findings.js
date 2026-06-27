import { $, esc, escAttr, state, toast, api, openModal, closeModal, renderMD, wireRowKey, saveFile } from './core.js';
import { flowPopup } from './flowmodal.js';

// Findings tab: the human reviews/curates the project's vulnerability findings
// (recorded by the human or the AI) and the request/response flows attached to each
// as PoC evidence. Backed by /api/findings (+ /flows for PoC) and the SSE
// "findings.update" event.

const STATUSES = ['open', 'verified', 'false_positive', 'wont_fix', 'fixed'];
let findings = [], selFinding = null;

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
  box.innerHTML = findings.map(f => `<div class="find-row${f.id === selFinding ? ' sel' : ''}" data-id="${f.id}">
    <span class="sev" style="color:${sevColor(f.severity)}">${esc(f.severity)}</span>
    <span class="find-title">${esc(f.title)}</span>
    <span class="find-meta">${esc(statusLabel(f.status))}${f.flows && f.flows.length ? ' · ' + f.flows.length + ' PoC' : ''}${f.source === 'ai' ? ' · <span style="color:var(--accent)">AI</span>' : ''}</span>
  </div>`).join('');
  box.querySelectorAll('.find-row').forEach(el => { el.onclick = () => { selFinding = Number(el.dataset.id); renderFindings(); renderFindingDetail(); }; wireRowKey(el); });
  renderFindingDetail();
}

function renderFindingDetail() {
  const box = $('#findDetail'); if (!box) return;
  const f = findings.find(x => x.id === selFinding);
  if (!f) { box.innerHTML = '<div class="hint" style="padding:16px">Select a finding.</div>'; return; }
  const statusSel = STATUSES.map(s => `<option value="${s}"${s === f.status ? ' selected' : ''}>${esc(statusLabel(s))}</option>`).join('');
  const poc = (f.flows || []).map(fl => `<div class="find-poc" data-flow="${fl.flowId}">
    <span class="m" style="color:var(--accent)">${esc(fl.method || '?')}</span>
    <span class="p" title="${escAttr((fl.host || '') + (fl.path || ''))}">${esc(fl.host || '')}${esc(fl.path || '')}</span>
    <span class="s" style="color:var(--fg3)">${fl.status || ''}</span>
    ${fl.note ? '<span class="n">' + esc(fl.note) + '</span>' : ''}
    <button class="btn xs" data-detach="${fl.flowId}" title="Remove PoC" aria-label="Remove PoC">✕</button>
  </div>`).join('') || '<div class="hint">No PoC flows yet. Select request/responses in <b>Proxy History</b>, then use “➕ Add to finding” (selection bar) or the button below.</div>';
  box.innerHTML = `<div class="find-head">
      <span class="sev" style="color:${sevColor(f.severity)};font-weight:700">${esc(f.severity)}</span>
      <b style="font-size:14px">${esc(f.title)}</b>
      <div class="spacer"></div>
      <select id="findStatus" class="btn" style="background:var(--bg3)" aria-label="Finding status">${statusSel}</select>
      <button class="btn danger" id="findDelete">Delete</button>
    </div>
    ${f.target ? '<div class="hint" style="margin:2px 0 8px">Target: ' + esc(f.target) + '</div>' : ''}
    ${f.detail ? '<div class="md">' + renderMD(f.detail) + '</div>' : ''}
    ${f.evidence ? '<h4 class="find-sec">Evidence</h4><div class="md">' + renderMD(f.evidence) + '</div>' : ''}
    ${f.fix ? '<h4 class="find-sec">Fix</h4><div class="md">' + renderMD(f.fix) + '</div>' : ''}
    <h4 class="find-sec">PoC request / responses
      <button class="btn xs" id="findAddSel" title="Attach the flows currently selected in Proxy History">➕ Add selected (${state.selected ? state.selected.size : 0})</button>
    </h4>
    <div id="findPocList">${poc}</div>`;
  $('#findStatus').onchange = async e => { try { await api('/api/findings/' + f.id, { method: 'PATCH', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ status: e.target.value }) }); } catch (err) { toast(err.message); } };
  $('#findDelete').onclick = async () => { try { await api('/api/findings/' + f.id, { method: 'DELETE' }); selFinding = null; } catch (err) { toast(err.message); } };
  $('#findAddSel').onclick = () => attachFlowsToFinding(f.id, state.selected ? [...state.selected] : []);
  box.querySelectorAll('.find-poc').forEach(el => { el.onclick = ev => { if (ev.target.closest('[data-detach]')) return; flowPopup(Number(el.dataset.flow)); }; wireRowKey(el, () => flowPopup(Number(el.dataset.flow))); });
  box.querySelectorAll('[data-detach]').forEach(b => b.onclick = async ev => { ev.stopPropagation(); try { await api('/api/findings/' + f.id + '/flows/' + b.dataset.detach, { method: 'DELETE' }); } catch (err) { toast(err.message); } });
}

async function attachFlowsToFinding(findingId, ids) {
  if (!ids.length) { toast('select request/responses in Proxy History first'); return; }
  try {
    for (const fid of ids) await api('/api/findings/' + findingId + '/flows', { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ flowId: fid }) });
    toast('attached ' + ids.length + ' PoC flow' + (ids.length === 1 ? '' : 's'));
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
// flowFindings returns findings that have flowId attached as PoC evidence.
export function flowFindings(flowId) {
  return findings.filter(f => (f.flows || []).some(x => x.flowId === flowId)).map(f => ({ id: f.id, title: f.title, severity: f.severity }));
}
// openFinding jumps to the Findings tab and selects a finding.
export function openFinding(id) {
  selFinding = id;
  const tab = document.querySelector('.tab[data-tab="findings"]'); if (tab) tab.click();
  renderFindings();
}
// addFlowToFinding opens the picker to attach a single flow (e.g. from a flow's
// right-click menu) to a finding.
export function addFlowToFinding(flowId) {
  if (flowId) pickFindingForFlows([flowId]);
}

/* ---- "➕ Add to finding" from the History selection bar: pick a finding (or new) ---- */
export function pickFindingForSelection() {
  pickFindingForFlows(state.selected ? [...state.selected] : []);
}
function pickFindingForFlows(ids) {
  if (!ids.length) { toast('select flows first'); return; }
  const list = $('#findPickList'); if (!list) return;
  const rows = findings.map(f => `<button class="btn find-pick" data-id="${f.id}" style="width:100%;text-align:left;margin-bottom:4px">
    <span class="sev" style="color:${sevColor(f.severity)}">${esc(f.severity)}</span> ${esc(f.title)}
    <span class="hint" style="float:right">${esc(statusLabel(f.status))}${f.flows && f.flows.length ? ' · ' + f.flows.length + ' PoC' : ''}</span></button>`).join('');
  list.innerHTML = `<div class="hint" style="margin-bottom:8px">Attach ${ids.length} selected flow${ids.length === 1 ? '' : 's'} to:</div>${rows || '<div class="hint">No findings yet.</div>'}
    <button class="btn accent find-pick-new" style="width:100%;margin-top:6px">＋ New finding from these flows</button>`;
  openModal($('#findPickModal'));
  list.querySelectorAll('.find-pick').forEach(b => b.onclick = async () => { closeModal($('#findPickModal')); await attachFlowsToFinding(Number(b.dataset.id), ids); });
  list.querySelector('.find-pick-new').onclick = async () => {
    closeModal($('#findPickModal'));
    const f = await api('/api/findings', { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ title: 'New finding', severity: 'Medium', source: 'human', flowIds: ids }) }).catch(e => { toast(e.message); return null; });
    if (f) { document.querySelector('.tab[data-tab="findings"]').click(); selFinding = f.id; }
  };
}
$('#fpClose') && ($('#fpClose').onclick = () => closeModal($('#findPickModal')));
$('#selAddFinding') && ($('#selAddFinding').onclick = pickFindingForSelection);
