import { renderCapabilityClaims, bindCapabilityClaims } from './finding-claims.js';
import { bindTargetCleanup } from './finding-target-cleanup.js';
import { esc, escAttr, initUiSelects, uiPrompt, toast } from './core.js';

const executionLabels = { '': 'Not reviewed', demonstrated: 'Impact demonstrated', prerequisite_only: 'Prerequisites only', not_executed: 'Not executed' };
export const evidenceSourceLabel = source => ({ device_screenshot: 'Device capture · reviewer declared', browser_screenshot: 'Browser capture · reviewer declared', operator_upload: 'Uploaded image · origin unconfirmed', flow_preview: 'Generated HTTP preview', generated_image: 'Generated image · not browser proof', tool_output: 'Tool output', captured_flow: 'Captured traffic' })[source] || source || 'Origin unconfirmed';

// Action/result/control/visual proof are reported separately, with uploaded
// and generated images counted apart so a preview never reads as a capture.
export function renderEvidenceCapabilities(readiness) {
  const c = readiness?.capabilities;
  if (!c) return '';
  const chip = (ok, label) => `<span class="find-cap ${ok ? 'find-cap-ok' : 'find-cap-miss'}">${esc(label)}: ${ok ? 'present' : 'missing'}</span>`;
  const exec = c.execution ? `<span class="find-cap">${esc(executionLabels[c.execution] || c.execution)}</span>` : '';
  return `<div class="find-caps" role="group" aria-label="Evidence capabilities">${chip(c.action, 'Action')}${chip(c.result, 'Result')}${chip(c.control, 'Control')}${chip(c.visual, 'Visual capture')}${exec}<span class="hint">${Number(readiness.screenshotCount) || 0} verified capture(s) · ${Number(readiness.uploadedImageCount) || 0} uploaded · ${Number(readiness.generatedImageCount) || 0} generated preview(s)</span></div>`;
}

function field(index, key, label, value, extra = '') {
  return `<label class="find-target-field">${label}<input class="find-field-text" id="${index === 0 && key === 'url' ? 'findTarget' : `findTarget-${index}-${key}`}" data-target-field="${key}" value="${escAttr(value || '')}" ${extra}></label>`;
}

export function renderAffectedTargets(f, edit) {
  const targets = f.targets || (f.target ? [{ url: f.target }] : []);
  return `<section class="find-sec" id="find-sec-target"><div class="find-section-head"><h3>Affected targets <span class="hint">${targets.length}</span></h3>${edit ? '<div class="find-target-actions"><button type="button" class="btn xs" id="findTargetCleanup">Clean up</button><button type="button" class="btn xs" id="findAddTarget">Add target</button></div>' : ''}</div>
  <div class="find-target-list">${targets.map((t, i) => {
    const gap = (f.readiness?.targetEvidenceGaps || []).includes(i);
    const ids = t.flow_ids || [];
    const methods = (t.methods || (t.method ? [t.method] : [])).join(', ');
    const evidence = ids.map(id => (t.missingFlowIds || []).includes(id) ? `<span class="find-target-gap">#${id} · missing</span>` : `<a href="#finding-${f.id}/flow-${id}" data-target-flow="${id}" class="find-target-flow">#${id}${(t.missingFlowIds || []).includes(id) ? ' · missing' : ''}</a>`).join(' ');
    const heading = `<span class="find-target-number">${i + 1}</span><span class="find-target-address"><strong>${esc(t.url)}</strong><small>${esc([methods, t.relation, t.role, i === 0 ? 'Primary target' : ''].filter(Boolean).join(' · '))}</small></span>`;
    if (!edit) return `<article class="find-target-card"><div class="find-target-heading">${heading}</div>${(t.image_hashes || []).length ? `<p class="hint">${t.image_hashes.length} linked browser capture${t.image_hashes.length === 1 ? '' : 's'}</p>` : ''}${t.variant ? `<p>${esc(t.variant)}</p>` : ''}${t.note ? `<p>${esc(t.note)}</p>` : ''}<div class="find-target-evidence">${evidence}${t.evidenceException ? `<p class="hint">Evidence exception: ${esc(t.evidenceException)}</p>` : ''}${gap ? '<span class="find-target-gap">Link evidence for this target</span>' : ''}</div></article>`;
    const flows = (f.blocks || []).filter(b => b.type === 'flow' && !b.missing);
    const images = (f.blocks || []).filter(b => b.type === 'image' && !b.missing && ['browser_screenshot','device_screenshot'].includes(b.source));
    return `<details class="find-target-card" id="findTargetCard-${i}" data-target-index="${i}"${i === 0 ? ' open' : ''}><summary class="find-target-heading">${heading}${gap ? '<span class="find-target-gap">Needs evidence</span>' : ''}</summary><div class="find-target-editor">
      <div class="find-target-actions"><button type="button" class="btn xs" data-target-move="-1" aria-label="Move target ${i + 1} up"${i === 0 ? ' disabled' : ''}>↑ Move up</button><button type="button" class="btn xs" data-target-move="1" aria-label="Move target ${i + 1} down"${i === targets.length - 1 ? ' disabled' : ''}>↓ Move down</button><button type="button" class="btn xs" data-target-remove aria-label="Remove target ${i + 1}">Remove</button></div>
      ${field(i, 'url', 'URL or app', t.url)}
      <div class="find-target-grid">${field(i, 'methods', 'Methods', methods, 'placeholder="GET, POST"')}${field(i, 'relation', 'Relation', t.relation, 'placeholder="affected, source, sink, setup, chain"')}${field(i, 'role', 'Role / prerequisite', t.role)}${field(i, 'variant', 'Parameter / variant', t.variant)}</div>
      ${field(i, 'note', 'Note', t.note)}
      <div class="find-target-evidence"><span class="find-target-field-label">Evidence flows</span><div class="find-target-flow-choices">${flows.map(b => `<button type="button" class="btn xs" data-target-evidence="${b.flowId}" aria-pressed="${ids.includes(b.flowId)}" title="${escAttr([b.method, b.host, b.path].filter(Boolean).join(' '))}">#${b.flowId} ${esc(b.method || '')} ${esc(b.path || '')}</button>`).join('')}${images.map(b=>`<button type="button" class="btn xs" data-target-image="${escAttr(b.hash)}" aria-pressed="${(t.image_hashes || []).includes(b.hash)}">▧ ${esc(b.caption || 'Browser capture')}</button>`).join('')}${!flows.length && !images.length ? '<span class="hint">Attach flows or classify browser captures in Evidence, then link them here.</span>' : ''}</div>${evidence}</div>
      ${field(i, 'evidenceException', 'Setup / chain exception', t.evidenceException, 'placeholder="Why this setup or chain target needs no separate evidence"')}
    </div></details>`;
  }).join('') || '<p class="hint">No affected targets recorded.</p>'}</div></section>`;
}

function renderEvidenceMapping(f) {
 const options=(f.blocks || []).filter(b=>!b.missing && (b.type==='flow' || (b.type==='image' && ['browser_screenshot','device_screenshot'].includes(b.source))));
 return `<details class="find-proof-mapping" id="findProofMapping"><summary title="One captured request and response may establish more than one check.">Map evidence to checks</summary><div class="find-properties">${['action','result','control'].map(role=>{
   const ref=f.proofReview?.evidence?.[role],selected=ref?.flowId ? `flow:${ref.flowId}` : ref?.hash ? `image:${ref.hash}` : '';
   const items=options.map(b=>({value:b.type==='flow'?`flow:${b.flowId}`:`image:${b.hash}`,label:b.type==='flow'?`#${b.flowId} ${b.method || ''} ${b.path || ''}`:(b.caption || 'Browser capture')}));
   if(selected && !items.some(x=>x.value===selected))items.push({value:selected,label:'Missing evidence reference'});
   return `<label for="findEvidenceMap-${role}">${{action:'Action / request',result:'Observed result',control:'Negative / control'}[role]}</label><select id="findEvidenceMap-${role}" data-review-evidence="${role}"><option value="">Use artifact roles</option>${items.map(x=>`<option value="${escAttr(x.value)}"${x.value===selected?' selected':''}>${esc(x.label)}</option>`).join('')}</select>`;
 }).join('')}</div></details>`;
}

export function renderProofReview(f, edit) {
  const r = f.proofReview || {};
  const score = f.cvssScore == null ? 'CVSS v4.0 vector needed' : `${f.cvssScore.toFixed(1)} · ${f.cvssRating} · ${f.cvssNomenclature}`;
  return `<section class="find-sec find-proof-review" id="find-sec-proof-review"><h3>Impact verification</h3>${edit ? `<div class="find-properties"><label for="findExecution">Observed impact</label><select id="findExecution">${Object.entries(executionLabels).map(([v, label]) => `<option value="${v}"${(r.execution || '') === v ? ' selected' : ''}>${label}</option>`).join('')}</select><label for="findVisualProof">Visual claim</label><button type="button" id="findVisualProof" class="btn" aria-pressed="${!!r.visual}">${r.visual ? 'Browser screenshot required' : 'No visual claim'}</button></div><label class="find-target-field" for="findExecutionReason">Verification limit / reason<textarea class="find-field-text" id="findExecutionReason" rows="2" placeholder="Required when impact has not been demonstrated" aria-describedby="findExecutionReasonError">${esc(r.reason || '')}</textarea></label><p class="find-proof-needed" id="findExecutionReasonError" hidden>A reason is required when impact has not been demonstrated.</p><label class="find-target-field" for="findSeverityOverride">Severity override reason<textarea class="find-field-text" id="findSeverityOverride" rows="2" placeholder="Only when severity deliberately differs from the calculated CVSS rating">${esc(r.severityOverride || '')}</textarea></label>` : `<p>${esc(executionLabels[r.execution || ''] || r.execution)}</p>${r.reason ? `<p>${esc(r.reason)}</p>` : ''}${r.severityOverride ? `<p class="hint">Severity override: ${esc(r.severityOverride)}</p>` : ''}${r.visual ? '<p class="hint">Requires a real browser capture of the observed result.</p>' : ''}`}
  ${edit ? renderEvidenceMapping(f) : ''}${renderCapabilityClaims(f,edit)}<p class="find-cvss-score" id="findCvssScore">${esc(score)}</p></section>`;
}

export function bindFindingAssessment(root, f, { stage, save, refresh, openFlow }) {
  let targets = structuredClone(f.targets || []), review = { ...(f.proofReview || {}) };
  let targetWritePending = false;
  const persist = async (fields, targetsChanged = false) => {
    try { const result = await save(fields); if (result?.latest) await refresh({ targetsChanged }); return true; }
    catch (err) { toast(err.message, 'error'); return false; }
  };
  const collect = () => {
    root.querySelectorAll('[data-target-index]').forEach(card => {
      const t = targets[Number(card.dataset.targetIndex)]; if (!t) return;
      card.querySelectorAll('[data-target-field]').forEach(el => {
        t[el.dataset.targetField] = el.dataset.targetField === 'methods' ? el.value.split(',').map(x => x.trim()).filter(Boolean) : el.value;
      });
    });
    return structuredClone(targets);
  };
  const mutateTargets = async change => {
    if (targetWritePending) return false;
    // Keep the binder aligned with the visible cards until a successful refresh.
    // Failed changes remain in the shared draft store for Retry.
    const next = collect();
    change(next);
    targetWritePending = true;
    const controls = [...root.querySelectorAll('#find-sec-target input, #find-sec-target button')].map(el => [el, el.disabled]);
    controls.forEach(([el]) => { el.disabled = true; });
    try { return await persist({ targets: next }, true); }
    finally { targetWritePending = false; controls.forEach(([el, disabled]) => { el.disabled = disabled; }); }
  };
  root.querySelectorAll('[data-target-flow]').forEach(link => link.addEventListener('click', event => {
    if (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
    event.preventDefault(); openFlow(Number(link.dataset.targetFlow));
  }));
  bindTargetCleanup(root, collect, next => mutateTargets(items => items.splice(0, items.length, ...next)));
  root.querySelectorAll('[data-target-field]').forEach(input => {
    let savedValue = input.value;
    input.addEventListener('input', () => stage({ targets: collect() }));
    input.addEventListener('blur', async () => {
      if (targetWritePending || input.value === savedValue) return;
      const value = input.value;
      if (await persist({ targets: collect() })) savedValue = value;
    });
  });
  root.querySelectorAll('[data-target-index]').forEach(card => {
    const i = Number(card.dataset.targetIndex);
    card.querySelectorAll('[data-target-move]').forEach(button => button.onclick = async () => {
      const j = i + Number(button.dataset.targetMove);
      await mutateTargets(next => { if (j >= 0 && j < next.length) [next[i], next[j]] = [next[j], next[i]]; });
    });
    card.querySelector('[data-target-remove]').onclick = () => mutateTargets(next => { next.splice(i, 1); });
    card.querySelectorAll('[data-target-image]').forEach(button => button.onclick = () => mutateTargets(next => {
      const hash = button.dataset.targetImage, hashes = new Set(next[i].image_hashes || []);
      if (hashes.has(hash)) hashes.delete(hash); else hashes.add(hash);
      next[i].image_hashes = [...hashes];
    }));
    card.querySelectorAll('[data-target-evidence]').forEach(button => button.onclick = async () => {
      await mutateTargets(next => {
        const id = Number(button.dataset.targetEvidence), ids = new Set(next[i].flow_ids || []);
        if (ids.has(id)) ids.delete(id); else ids.add(id);
        next[i].flow_ids = [...ids];
      });
    });
  });
  const add = root.querySelector('#findAddTarget');
  if (add) add.onclick = async () => {
    if (targetWritePending) return;
    const url = await uiPrompt({ title: 'Add affected target', placeholder: 'https://example.com/path or app identifier' });
    if (!url?.trim()) return;
    const added = await mutateTargets(next => { next.push({ url: url.trim(), relation: 'affected', methods: [], flow_ids: [] }); });
    if (!added) return;
    const card = root.querySelector(`#findTargetCard-${targets.length}`);
    if (card) { card.open = true; card.querySelector('input')?.focus(); }
  };
  const execution = root.querySelector('#findExecution'), reason = root.querySelector('#findExecutionReason'), visual = root.querySelector('#findVisualProof'), override = root.querySelector('#findSeverityOverride');
  const reviewFields = () => { review.execution = execution.value; review.reason = reason.value; if (override) review.severityOverride = override.value.trim(); return { proofReview: { ...review } }; };
  const saveReview = () => {
    const fields = reviewFields(); stage(fields);
    const needsReason = ['prerequisite_only', 'not_executed'].includes(review.execution) && !review.reason.trim();
    reason.setAttribute('aria-invalid', String(needsReason));
    const reasonError = root.querySelector('#findExecutionReasonError');
    if (reasonError) reasonError.hidden = !needsReason;
    if (needsReason) { reason.focus(); return; }
    void persist(fields);
  };
  bindCapabilityClaims(root,()=>review,claims=>{review.claims=claims;stage(reviewFields());},claims=>{review.claims=claims;saveReview();});
  root.querySelectorAll('[data-review-evidence]').forEach(select=>select.addEventListener('change',()=>{review.evidence={...(review.evidence || {})};const [type,id]=select.value.split(':');if(!id)delete review.evidence[select.dataset.reviewEvidence];else review.evidence[select.dataset.reviewEvidence]=type==='flow'?{flowId:Number(id)}:{hash:id};saveReview();}));
  if (execution) execution.addEventListener('change', saveReview);
  if (reason) { reason.addEventListener('input', () => stage(reviewFields())); reason.addEventListener('blur', saveReview); }
  if (override) { override.addEventListener('input', () => stage(reviewFields())); override.addEventListener('blur', saveReview); }
  if (visual) visual.onclick = () => { review.visual = !review.visual; visual.setAttribute('aria-pressed', String(review.visual)); visual.textContent = review.visual ? 'Browser screenshot required' : 'No visual claim'; saveReview(); };
  initUiSelects(root);
}
