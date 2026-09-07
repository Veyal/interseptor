import { esc, escAttr } from './core.js';
const CAPABILITIES = {
 authenticated_without_required_factor: 'Authenticated without the required factor',
 browser_execution: 'Browser execution',
 account_control: 'Account control',
 state_change: 'State change',
};
const referenceKey = ref => ref.flowId ? `flow:${ref.flowId}` : `image:${ref.hash}`;
export function renderCapabilityClaims(f, edit) {
 const claims = f.proofReview?.claims || {};
 const required = new Set((f.readiness?.gaps || []).filter(g => g.startsWith('capability:')).map(g => g.slice(11)));
 const choices = (f.blocks || []).filter(b => !b.missing && (b.type === 'flow' || b.type === 'image' && ['browser_screenshot','device_screenshot'].includes(b.source)));
 return `<section id="findCapabilityClaims" class="find-capability-claims"><h4>Claim evidence <span class="hint" title="These are reviewer declarations linked to captured evidence. The app checks completeness; it does not independently verify exploitation.">ⓘ</span></h4>${Object.entries(CAPABILITIES).filter(([key]) => edit || claims[key] || required.has(key)).map(([key,label]) => {
  const claim = claims[key] || {}, refs = new Set((claim.evidence || []).map(referenceKey));
  return `<details id="findCapability-${key}" class="find-capability" data-capability="${key}"${required.has(key) ? ' open' : ''}><summary>${esc(label)}${required.has(key) ? '<span class="find-target-gap">Needs evidence</span>' : ''}</summary>${edit ? `<label class="find-target-field">Observed capability<textarea id="findCapabilityNote-${key}" class="find-field-text" data-claim-note rows="2" placeholder="What did the linked evidence establish?">${esc(claim.note || '')}</textarea></label><div class="find-target-flow-choices">${choices.map(b => { const ref = b.type === 'flow' ? `flow:${b.flowId}` : `image:${b.hash}`; return `<button type="button" class="btn xs" data-claim-evidence="${escAttr(ref)}" aria-pressed="${refs.has(ref)}">${esc(b.type === 'flow' ? `#${b.flowId} · ${b.role || 'No role'}` : `${b.caption || 'Capture'} · ${b.role || 'No role'}`)}</button>`; }).join('') || '<p class="hint">Attach and label evidence first.</p>'}</div>${claims[key] ? '<button type="button" class="btn xs" data-claim-remove>Remove declaration</button>' : ''}` : `<p>${esc(claim.note || 'Link evidence for this claim in Edit.')}</p>`}</details>`;
 }).join('')}</section>`;
}
export function bindCapabilityClaims(root, getReview, stage, save) {
 root.querySelectorAll('[data-capability]').forEach(card => {
  const note=card.querySelector('[data-claim-note]');if(!note)return;
  const collect=()=>{const claims=structuredClone(getReview().claims||{}),key=card.dataset.capability;claims[key]={note:note.value,evidence:[...card.querySelectorAll('[data-claim-evidence][aria-pressed="true"]')].map(b=>{const [kind,id]=b.dataset.claimEvidence.split(':');return kind==='flow'?{flowId:Number(id)}:{hash:id};})};const basis=card.querySelector('[data-claim-basis]');if(basis)claims[key].basis=basis.value;return claims;};
  note.addEventListener('input',()=>stage(collect()));note.addEventListener('blur',()=>save(collect()));
  card.querySelector('[data-claim-basis]')?.addEventListener('change',()=>save(collect()));
  card.querySelectorAll('[data-claim-evidence]').forEach(button=>button.onclick=()=>{button.setAttribute('aria-pressed',String(button.getAttribute('aria-pressed')!=='true'));save(collect());});
  card.querySelector('[data-claim-remove]')?.addEventListener('click',()=>{const claims=structuredClone(getReview().claims||{});delete claims[card.dataset.capability];save(claims);});
 });
}
