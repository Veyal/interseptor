import { $, api, esc, escAttr, openModal, closeModal, uiConfirm, toast, saveFile, registerProjectSwitchGuard } from './core.js';
let restoring = false;
registerProjectSwitchGuard(() => restoring ? 'Wait for the finding restoration to finish.' : '');
const when = ts => new Date(ts).toLocaleString();
const value = v => v == null ? '—' : typeof v === 'string' ? v : JSON.stringify(v, null, 2);
// Shared failure component (app.css .state-error): an alerted message plus an
// optional Retry, instead of a bare sentence assigned as textContent.
function showErrorState(host, message, retry) {
  host.innerHTML = `<div class="state-error"><div class="state-error-icon"><svg class="icon" aria-hidden="true" focusable="false"><use href="#i-warning"/></svg></div><span class="state-error-msg" role="alert">${esc(message)}</span>${retry ? '<button type="button" class="btn xs" data-revision-retry>Retry</button>' : ''}</div>`;
  if (retry) host.querySelector('[data-revision-retry]').onclick = retry;
}
export function renderFindingRevisions() {
  return '<details id="findRevisionHistory" class="find-sec find-revisions"><summary>Revision history</summary><button class="btn xs" type="button" data-export-audit>Export audit summary</button><div class="find-revisions-list" aria-live="polite"></div></details>';
}
export function bindFindingRevisions(root, findingId, { canRestore, restored }) {
  const disclosure = root.querySelector('.find-revisions');
  const list = disclosure?.querySelector('.find-revisions-list');
  if (!list) return;
  let epoch = 0;
  const exportButton=disclosure.querySelector('[data-export-audit]');
  exportButton.onclick=async()=>{
    if(exportButton.disabled)return;exportButton.disabled=true;
    try{
      const revisions=[];let before=0;
      do{const page=await api(`/api/finding-revisions/${findingId}?before=${before}`);if(!exportButton.isConnected)return;revisions.push(...page.revisions.map(r=>({id:r.id,ts:r.ts,action:r.action,fields:r.fields})));before=page.nextBefore||0;}while(before);
      await saveFile(new Blob([JSON.stringify({findingId,revisions},null,2)],{type:'application/json'}),`finding-${findingId}-audit.json`,'application/json');
    }catch(error){if(error.name!=='AbortError')toast(error.message,'error');}
    finally{if(exportButton.isConnected)exportButton.disabled=false;}
  };
  disclosure.addEventListener('toggle', async () => {
    if (!disclosure.open) { epoch++; return; }
    const owner = ++epoch;
    list.textContent = 'Loading revisions…';
    const all=[];
    const loadPage=async before=>{
    try {
      const data = await api(`/api/finding-revisions/${findingId}?before=${before}`);
      all.push(...data.revisions);
      if (owner !== epoch || !list.isConnected) return;
      list.innerHTML = all.length ? all.map(rev => `<details class="find-revision" data-revision="${rev.id}"><summary><strong>${esc(rev.action)}</strong><time>${esc(when(rev.ts))}</time><span>${esc(rev.fields.join(', '))}</span></summary><div class="find-revision-detail"></div></details>`).join('') : '<p class="hint">History starts with the next edit.</p>';
      list.querySelectorAll('[data-revision]').forEach(item => {
        let loaded = false;
        item.addEventListener('toggle', async () => {
          if (!item.open || loaded) return;
          const detail = item.querySelector('.find-revision-detail');
          detail.textContent = 'Loading changes…';
          try {
            const { revision, diff } = await api(`/api/finding-revisions/${findingId}/${item.dataset.revision}`);
            if (!item.isConnected || owner !== epoch) return;
            loaded = true;
            detail.innerHTML = `<p class="hint">${esc(revision.actor)} · ${esc(revision.source)}${revision.reason ? ' · ' + esc(revision.reason) : ''}</p>${diff.map(d => `<details class="find-revision-field"><summary>${esc(d.field)}</summary><div class="find-revision-diff"><section><h4>Before</h4><pre>${esc(value(d.before).slice(0, 12000))}</pre></section><section><h4>After</h4><pre>${esc(value(d.after).slice(0, 12000))}</pre></section></div></details>`).join('')}<button class="btn" type="button" data-restore aria-label="Restore this version — ${escAttr(revision.action)} from ${escAttr(when(revision.ts))}">Restore this version</button>`;
            detail.querySelector('[data-restore]').onclick = () => restore(findingId, revision.id, detail.querySelector('[data-restore]'), canRestore, restored);
          } catch (error) { if (item.isConnected) showErrorState(detail, error.message + ' Close and reopen to retry.'); }
        });
      });
      if(data.nextBefore){const more=document.createElement('button');more.type='button';more.className='btn';more.textContent='Load older revisions';more.onclick=()=>{more.disabled=true;loadPage(data.nextBefore);};list.appendChild(more);}
    } catch (error) { if (owner === epoch && list.isConnected) showErrorState(list, error.message, () => loadPage(before)); }
    };
    await loadPage(0);
  });
}
async function restore(findingId, revisionId, button, canRestore, restored) {
  if (restoring) return;
  if (!canRestore()) { toast('Save or retry your current changes before restoring.', 'error'); return; }
  if (!await uiConfirm('Restore finding', 'Restore this version? Current content stays in revision history.', 'Restore')) return;
  if (!button.isConnected || !canRestore() || restoring) return;
  restoring = true; button.disabled = true; button.textContent = 'Restoring…';
  try {
    await api(`/api/finding-revisions/${findingId}/${revisionId}/restore`, { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ reason: 'Restored from revision history' }) });
    await restored(findingId);
    toast('Finding restored');
  } catch (error) { toast(error.message, 'error'); }
  finally { restoring = false; if (button.isConnected) { button.disabled = false; button.textContent = 'Restore this version'; } }
}
export async function openDeletedFindings({ canRestore, restored }) {
  let modal = $('#findDeletedModal');
  if (!modal) {
    modal = document.createElement('div'); modal.id = 'findDeletedModal'; modal.className = 'modal-overlay'; modal.style.display = 'none';
    modal.innerHTML = '<div class="modal-shell find-export-dialog" role="dialog" aria-modal="true" aria-labelledby="findDeletedTitle"><div class="modal-shell-head"><h3 id="findDeletedTitle">Deleted findings</h3><button type="button" class="btn" data-close>Close</button></div><div class="modal-shell-body find-deleted-list" aria-live="polite"></div></div>';
    document.body.appendChild(modal);
  }
  const dismiss = () => { if (restoring) return false; closeModal(modal); return true; };
  modal.querySelector('[data-close]').onclick = dismiss;
  openModal(modal, { onDismiss: dismiss, onEscape: dismiss });
  const list = modal.querySelector('.find-deleted-list'); list.textContent = 'Loading deleted findings…';
  try {
    const { revisions } = await api('/api/findings/deleted');
    if (modal.style.display !== 'flex') return;
    list.innerHTML = revisions.length ? revisions.map(r => `<article class="find-deleted-row"><div><strong>${esc(r.snapshot?.title || 'Untitled')}</strong><p class="hint">Deleted ${esc(when(r.ts))}</p></div><button class="btn" data-finding="${r.findingId}" data-revision="${r.id}" aria-label="Restore deleted finding ${escAttr(r.snapshot?.title || 'Untitled')}">Restore</button></article>`).join('') : '<p class="hint">No deleted findings to restore.</p>';
    list.querySelectorAll('[data-revision]').forEach(button => { button.onclick = () => restore(Number(button.dataset.finding), Number(button.dataset.revision), button, canRestore, async id => { closeModal(modal); await restored(id); }); });
  } catch (error) { showErrorState(list, error.message + ' Reopen to retry.'); }
}
