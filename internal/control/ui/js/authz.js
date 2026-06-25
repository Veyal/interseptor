// authz.js — authorization (access-control) testing. Replays one captured request
// under each saved identity (role) and diffs the responses to surface IDOR /
// broken access control. Launched from the History right-click menu.
import { $, esc, escAttr, api, toast, openModal, closeModal, statusColor, fmtSize } from './core.js';
import { selectFlow } from './proxy.js';

let authzFlowId = null;

export function openAuthz(flowId) {
  authzFlowId = flowId || null;
  openModal($('#authzModal'));
  $('#authzFlow').textContent = flowId ? ('#' + flowId) : '(none)';
  $('#authzResults').innerHTML = '<div class="hint">Run to compare access across roles.</div>';
  loadAuthzIdentities();
}

async function loadAuthzIdentities() {
  try { const d = await api('/api/authz'); renderIdentities(d.identities || []); } catch (e) { renderIdentities([]); }
}
function renderIdentities(ids) {
  if (!ids.length) ids = [{ name: '', headers: '' }];
  $('#authzIds').innerHTML = ids.map((id, i) => `<div class="authz-id" data-i="${i}">
    <input class="authz-name btn" style="background:var(--bg3)" placeholder="role name e.g. ${i === 0 ? 'admin (baseline)' : 'user'}" value="${escAttr(id.name || '')}">
    <textarea class="authz-hdr rep-edit" rows="2" placeholder="Cookie: session=…   (leave blank = anonymous)">${esc(id.headers || '')}</textarea>
    <button class="btn danger authz-del" data-i="${i}" title="remove">✕</button></div>`).join('');
  document.querySelectorAll('#authzIds .authz-del').forEach(b => b.onclick = () => {
    const ids = collectIds(); ids.splice(Number(b.dataset.i), 1);
    renderIdentities(ids.length ? ids : [{ name: '', headers: '' }]);
  });
}
function collectIds() {
  return [...document.querySelectorAll('#authzIds .authz-id')].map(el => ({
    name: el.querySelector('.authz-name').value, headers: el.querySelector('.authz-hdr').value,
  })).filter(x => x.name || x.headers);
}
async function saveIds() { await api('/api/authz', { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ identities: collectIds() }) }); }

$('#authzAdd') && ($('#authzAdd').onclick = () => renderIdentities([...collectIds(), { name: '', headers: '' }]));
$('#authzSave') && ($('#authzSave').onclick = async () => { try { await saveIds(); toast('identities saved'); } catch (e) { toast(e.message); } });
$('#authzClose') && ($('#authzClose').onclick = () => closeModal($('#authzModal')));
$('#authzRun') && ($('#authzRun').onclick = async () => {
  if (!authzFlowId) { toast('no flow — right-click a flow → Authz test'); return; }
  if (collectIds().length < 1) { toast('add at least one identity'); return; }
  $('#authzResults').innerHTML = '<div class="hint">replaying…</div>';
  try {
    await saveIds(); // persist current edits, then run against them
    const d = await api('/api/authz/run', { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ flowId: authzFlowId }) });
    renderAuthzResults(d);
  } catch (e) { $('#authzResults').innerHTML = '<div class="hint" style="color:var(--red)">' + esc(e.message) + '</div>'; }
});

function renderAuthzResults(d) {
  const res = d.results || [], box = $('#authzResults');
  if (!res.length) { box.innerHTML = '<div class="hint">no results</div>'; return; }
  box.innerHTML = '<div class="authz-row authz-head"><span>identity</span><span>status</span><span>length</span><span>verdict</span></div>'
    + res.map((r, i) => `<div class="authz-row${r.sameAsBaseline ? ' flag' : ''}"${r.flowId ? ` data-flow="${r.flowId}"` : ''} title="${r.flowId ? 'open flow #' + r.flowId : ''}">
      <span>${esc(r.name || '(unnamed)')}</span>
      <span style="color:${statusColor(r.status)};font-weight:700">${r.error ? 'ERR' : (r.status || '—')}</span>
      <span>${fmtSize(r.length)}</span>
      <span>${i === 0 ? '<span class="hint">baseline</span>' : (r.sameAsBaseline ? '<span style="color:var(--red);font-weight:700">⚠ same access</span>' : '<span class="hint">differs ✓</span>')}</span></div>`).join('');
  box.querySelectorAll('[data-flow]').forEach(el => el.onclick = () => { closeModal($('#authzModal')); selectFlow(Number(el.dataset.flow)); });
}
