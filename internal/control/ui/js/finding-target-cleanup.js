import { api, esc, escAttr, toast } from './core.js';
export function bindTargetCleanup(root, collect, apply) {
  const button = root.querySelector('#findTargetCleanup');
  if (!button) return;
  let epoch = 0;
  button.onclick = async () => {
    const owner = ++epoch;
    button.disabled = true;
    let panel = root.querySelector('.find-target-cleanup');
    if (!panel) { panel = document.createElement('div'); panel.className = 'find-target-cleanup'; root.querySelector('#find-sec-target')?.appendChild(panel); }
    panel.textContent = 'Preparing preview…';
    const original = collect();
    try {
      const preview = await api('/api/finding-targets/preview', { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ targets: original }) });
      if (owner !== epoch || !panel.isConnected) return;
      const next = structuredClone(preview.targets);
      panel.innerHTML = `<h4>Review target cleanup</h4><p class="hint">${preview.removed} duplicate${preview.removed === 1 ? '' : 's'} combined. Evidence links stay attached. Path templates need your selection.</p><ol>${next.map((t, i) => { const suggestion = preview.suggestions.find(s => s.index === i); return `<li><strong>${esc(t.url)}</strong><small>${esc([...(t.methods || []), t.role, t.variant].filter(Boolean).join(' · '))} · ${(t.flow_ids || []).length} flow links · ${(t.image_hashes || []).length} images</small>${suggestion ? `<button type="button" class="btn xs" data-template="${i}" aria-pressed="false" data-url="${escAttr(suggestion.template)}">Use ${esc(suggestion.template)}</button>` : ''}</li>`; }).join('')}</ol><div class="find-target-actions"><button type="button" class="btn accent" data-apply>Apply cleaned targets</button><button type="button" class="btn" data-cancel>Cancel</button></div>`;
      panel.querySelectorAll('[data-template]').forEach(toggle => { toggle.onclick = () => { const selected = toggle.getAttribute('aria-pressed') !== 'true'; toggle.setAttribute('aria-pressed', String(selected)); const i = Number(toggle.dataset.template); next[i].url = selected ? toggle.dataset.url : preview.targets[i].url; }; });
      panel.querySelector('[data-cancel]').onclick = () => { epoch++; panel.remove(); };
      panel.querySelector('[data-apply]').onclick = async () => {
        if (JSON.stringify(collect()) !== JSON.stringify(original)) { toast('Targets changed. Preview cleanup again.', 'error'); panel.remove(); return; }
        const applyButton = panel.querySelector('[data-apply]'); applyButton.disabled = true;
        try {
          const cleaned = await api('/api/finding-targets/preview', { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ targets: next }) });
          if (!panel.isConnected || owner !== epoch) return;
          if (JSON.stringify(collect()) !== JSON.stringify(original)) { toast('Targets changed. Preview cleanup again.', 'error'); panel.remove(); return; }
          const ok = await apply(cleaned.targets);
          if (ok) panel.remove();
        } catch(error) { if(panel.isConnected)toast(error.message,'error'); }
        finally { if(applyButton.isConnected)applyButton.disabled=false; }
      };
    } catch (error) { if (panel.isConnected && owner === epoch) panel.innerHTML = `<div class="state-error"><div class="state-error-icon"><svg class="icon" aria-hidden="true" focusable="false"><use href="#i-warning"/></svg></div><span class="state-error-msg" role="alert">${esc(error.message)} Preview again to retry.</span></div>`; }
    finally { if (button.isConnected && owner === epoch) button.disabled = false; }
  };
}
