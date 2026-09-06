import { $, esc, escAttr, state, toast, api, openModal, closeModal, renderMD, saveFile, uiPrompt, uiConfirm, methodColor, statusColor, renderLoadError } from './core.js';
import { flowPopup } from './flowmodal.js';
import { sendToRepeater } from './tools.js';

// Findings tab: the human reviews/curates the project's vulnerability findings.
// Each finding has a narrative body — an ordered sequence of text blocks (markdown)
// and flow-reference blocks (PoC request/response) interleaved freely, like a report.

const STATUSES = ['open', 'needs_verification', 'verified', 'false_positive', 'wont_fix', 'fixed'];
let findings = [], selFinding = null, findTagFilter = '', findTagCounts = [];
let findingsLoadStateEl = null;
let findingsLoadEpoch=0;
const findingAttachPending=new Set();

function findingsLoadState() {
  if (findingsLoadStateEl?.isConnected) return findingsLoadStateEl;
  const list = $('#findList');
  if (!list) return null;
  findingsLoadStateEl = document.createElement('div');
  findingsLoadStateEl.id = 'findingsLoadState';
  findingsLoadStateEl.className = 'tls-diag-banner';
  findingsLoadStateEl.setAttribute('role', 'status');
  findingsLoadStateEl.setAttribute('aria-live', 'polite');
  list.parentNode?.insertBefore(findingsLoadStateEl, list);
  return findingsLoadStateEl;
}
// Default Read/report view; Edit toggles the block editor.
let findEditMode = false;

// Body editor state for the active finding.
let bodyBlocks = [];
let bodyFindingId = null;
let bodySaveTimers = new Map();
let bodySaveSnapshots = new Map();
let bodySavesInFlight = 0;
let findingWritesInFlight = 0;
// PATCH requests for one finding are serialized. The API applies a PATCH as a
// whole document, so allowing an older body snapshot or blur value to finish
// after a newer one can silently restore stale operator intent. Pending writes
// coalesce by field while the current request is in flight.
const findingWriteQueues = new Map();
let findingDetailRefreshDeferred = false;
// True while a text-block textarea has focus. An SSE findings.update (e.g. a body
// save round-tripping, or the AI recording) would otherwise rebuild the detail
// pane mid-edit and discard the focused textarea + any unsaved keystrokes.
let bodyEditing = false;

const sevColor = s => ({ Critical: 'var(--red)', High: 'var(--red)', Medium: 'var(--amber)', Low: 'var(--blue)', Info: 'var(--fg3)' }[s] || 'var(--fg3)');
const statusLabel = s => ({ needs_verification: 'needs verification' }[s] || (s || '').replace(/_/g, ' '));
const statusBadgeColor = s => ({ verified: 'var(--accent)', fixed: 'var(--accent)', needs_verification: 'var(--amber)', false_positive: 'var(--fg3)', wont_fix: 'var(--fg3)' }[s] || 'var(--fg3)');

function textChainLabel(md) {
  return (md || '').replace(/```[\s\S]*?```/g, ' ').replace(/[#*_`~\[\]()]/g, '').replace(/\s+/g, ' ').trim();
}
const FINDING_ROLES = ['context','setup','baseline','action','result','control','retest','observation'];
function findingRoleOptions(selected) { return `<option value=""${selected ? '' : ' selected'}>Choose role…</option>` + FINDING_ROLES.map(r => `<option value="${r}"${r === selected ? ' selected' : ''}>${r}</option>`).join(''); }
function blockMetaEditor(b, i) {
  const evidence = b.type === 'flow' || b.type === 'image';
  return `<div class="find-block-meta"><label>Role <select class="find-block-role btn btn-field" data-i="${i}" aria-label="${evidence ? 'Evidence' : 'Reproduction step'} role">${findingRoleOptions(b.role)}</select></label>${evidence ? `<label class="find-proof-label">What this proves <input class="find-block-proof" data-i="${i}" aria-label="What this evidence proves" value="${escAttr(b.proof || '')}" placeholder="State the exact claim this evidence supports"></label>` : ''}${b.source ? `<span class="find-provenance">${esc(b.source)}${b.sourceFlowId ? ' · flow #' + esc(String(b.sourceFlowId)) : ''}</span>` : ''}</div>`;
}

const FINDING_OUTLINES = {
  'Differential proof': ['baseline', 'action', 'result'],
  'Input → Result': ['setup', 'action', 'result'],
  'Exposure proof': ['setup', 'result'],
  'Control failure': ['control', 'action', 'result'],
};

function findingStepPlaceholder(role) {
  return ({
    baseline: 'Describe the authorized or expected baseline…',
    setup: 'Describe the required state, identity, or input…',
    action: 'Describe the exact request or security-relevant action…',
    result: 'Describe the observed result and why it differs from secure behavior…',
    control: 'Describe the security control that should prevent this behavior…',
    retest: 'Describe the fixed behavior to verify…',
    context: 'Add context needed to understand the reproduction…',
    observation: 'Add a concise reproduction step…',
  })[role] || 'Add a concise reproduction step…';
}

function inferFindingOutline(blocks) {
  const roles = new Set((blocks || []).map(b => b.role).filter(Boolean));
  for (const name of ['Control failure', 'Differential proof', 'Input → Result', 'Exposure proof']) {
    if (FINDING_OUTLINES[name].every(role => roles.has(role))) return name;
  }
  return 'Custom';
}

function findingReadiness(f) {
  const structured = f?.readiness;
  if (structured && typeof structured.stage === 'string') {
    return { ...structured, gaps: Array.isArray(structured.gaps) ? structured.gaps : [], ready: structured.stage === 'report_ready' };
  }
  const legacy = (f?.missing || []).filter(gap => gap !== 'poc_before_after').map(gap => gap === 'poc' ? 'evidence' : gap);
  return { stage: f?.ready ? 'report_ready' : 'draft', gaps: [...new Set(legacy)], ready: !!f?.ready, visualProofRecommended: true };
}

function findingReadinessLabel(stage) {
  return ({ report_ready: 'Report ready', reproducible: 'Reproducible', evidence_attached: 'Evidence attached', draft: 'Draft' })[stage] || 'Draft';
}

function findingGapLabel(gap) {
  return ({ title: 'Title', summary: 'Claim', target: 'Target', impact: 'Impact', why: 'Why', evidence: 'Screenshot or flow', proof: 'Evidence proof statement', reproduction: 'Reproduction roles', fix: 'Remediation', retest: 'Retest', confidence: 'Confidence' })[gap] || gap;
}

function findingGapTarget(gap) {
  return ({ title: 'findTitleText', summary: 'find-sec-summary', target: 'find-sec-target', impact: 'find-sec-impact', why: 'find-sec-why', evidence: 'find-sec-poc', proof: 'find-sec-poc', reproduction: 'find-sec-poc', fix: 'find-sec-fix', retest: 'find-sec-retest', confidence: 'find-sec-review' })[gap] || 'find-sec-poc';
}

function findingPocCount(f) {
  return (f.blocks || []).filter(b => b.type === 'flow' || b.type === 'image').length || (f.flows || []).length || 0;
}

function findingStepCount(f) {
  const blocks = f.blocks || [];
  if (blocks.length) {
    return blocks.filter(b => (b.type === 'text' && textChainLabel(b.md)) || b.type === 'flow' || b.type === 'image').length;
  }
  let n = 0;
  if (f.detail) n++;
  if (f.evidence && f.evidence !== f.detail) n++;
  return n + findingPocCount(f);
}

function findingIsEmpty(f) {
  return !findingStepCount(f) && !textChainLabel(f.impact);
}

function findingListMeta(f) {
  const st = f.status === 'needs_verification'
    ? '<span class="find-needs-verif" title="Needs human verification"><svg class="icon" aria-hidden="true" focusable="false"><use href="#i-warning"/></svg> needs verification</span>'
    : esc(statusLabel(f.status));
  const readiness = findingReadiness(f);
  const ready = readiness.ready
    ? '<span class="find-ready">Report ready</span>'
    : `<span class="find-draft">${esc(findingReadinessLabel(readiness.stage))}</span>`;
  const parts = [ready, st];
  const tags = f.tags || [];
  if (tags.length) parts.push('<span class="find-tags-inline">' + tags.map(t => esc(t)).join(' · ') + '</span>');
  if (f.verification && f.verification.confidence != null) {
    parts.push('<span class="find-conf" title="External-agent verification confidence"><svg class="icon" aria-hidden="true" focusable="false"><use href="#i-gear"/></svg> ' + esc(String(f.verification.confidence)) + '%</span>');
  }
  const pocs = findingPocCount(f);
  if (pocs) parts.push(pocs + ' PoC');
  if (f.target) parts.push('<span class="hint">' + esc(f.target.length > 28 ? f.target.slice(0, 27) + '…' : f.target) + '</span>');
  else parts.push('<span class="hint">no target</span>');
  if (f.source === 'ai') parts.push('<span style="color:var(--accent)">AI</span>');
  return parts.join(' · ');
}

function parseFindTags(s) {
  return String(s || '').split(/[,;\s]+/).map(x => x.trim()).filter(Boolean);
}

function visibleFindings() {
  if (!findTagFilter) return findings;
  return findings.filter(f => (f.tags || []).includes(findTagFilter));
}

function renderFindTagFilter() {
  const box = $('#findTagFilter'); if (!box) return;
  const tags = findTagCounts.length ? findTagCounts : (() => {
    const m = {};
    for (const f of findings) for (const t of (f.tags || [])) m[t] = (m[t] || 0) + 1;
    return Object.keys(m).sort().map(tag => ({ tag, count: m[tag] }));
  })();
  if (!tags.length && !findTagFilter) { box.innerHTML = ''; return; }
  box.innerHTML = `<button type="button" class="btn xs find-tag-chip${!findTagFilter ? ' on' : ''}" data-tag="">All</button>` +
    tags.map(t => `<button type="button" class="btn xs find-tag-chip${findTagFilter === t.tag ? ' on' : ''}" data-tag="${escAttr(t.tag)}">${esc(t.tag)} <span class="hint">${t.count}</span></button>`).join('');
  box.querySelectorAll('[data-tag]').forEach(b => {
    b.onclick = () => {
      findTagFilter = b.dataset.tag || '';
      renderFindTagFilter();
      renderFindings();
    };
  });
}

export async function loadFindings() {
  const epoch=++findingsLoadEpoch;
  try {
    const q = findTagFilter ? '?tag=' + encodeURIComponent(findTagFilter) : '';
    // Always load the full set for the sidebar filter counts; filter client-side
    // so switching chips is instant. Server ?tag= still used by MCP/API.
    const [d, tags] = await Promise.all([
      api('/api/findings'),
      api('/api/findings/tags').catch(() => ({ tags: [] })),
    ]);
    if(epoch!==findingsLoadEpoch)return false;
    findings = d.findings || [];
    findTagCounts = tags.tags || [];
    renderFindTagFilter();
    renderFindings();
    const loadState = findingsLoadState();
    if (loadState) { loadState.style.display = 'none'; loadState.textContent = ''; }
    void q;
    return true;
  } catch (e) {
    if(epoch!==findingsLoadEpoch)return false;
    // Keep the last report visible. A toast alone disappears before a user can
    // diagnose a transient SSE/API failure, and an empty report is misleading.
    const loadState = findingsLoadState();
    if (loadState) {
      renderLoadError(loadState, 'Findings', e, loadFindings, findings.length > 0);
      const retry = loadState.querySelector('[data-load-retry]');
      if (retry) retry.setAttribute('data-findings-retry', '');
    } else toast(e.message);
    return false;
  }
}

function findingsEmptyHTML() {
  return `<div class="state-empty find-empty">
    <div class="state-empty-icon"><svg class="icon" aria-hidden="true" focusable="false"><use href="#i-search"/></svg></div>
    <div class="state-empty-title">No findings yet</div>
    <p class="state-empty-hint">File a vulnerability manually with PoC evidence, or attach captured flows from History.</p>
    <div class="find-empty-actions">
      <button type="button" class="btn btn-primary" id="findEmptyNew">＋ New finding</button>
       </div>
  </div>`;
}

function wireFindingsEmptyActions(root) {
  const box = root || $('#findList');
  if (!box) return;
  const neu = box.querySelector('#findEmptyNew');

  if (neu) neu.onclick = openFindCreate;

}

function setFindingsViewEmpty(empty) {
  const view = $('#scanFindingsView');
  if (view) view.classList.toggle('is-empty', !!empty);
}

function findingDetailEditPending() {
  const detail = $('#findDetail');
  const active = document.activeElement;
  return bodyEditing || bodySaveTimers.has(selFinding) || bodySavesInFlight > 0 || findingWritesInFlight > 0 ||
    !!(findEditMode && detail && (detail.querySelector('[role="combobox"][aria-expanded="true"]') ||
      (active && detail.contains(active) && active.matches('input,textarea,select,[contenteditable="true"],[role="combobox"]'))));
}

function refreshDeferredFindingDetail() {
  if (!findingDetailRefreshDeferred || findingDetailEditPending()) return;
  const focus = captureFindingFocus();
  findingDetailRefreshDeferred = false;
  renderFindingDetail();
  restoreFindingFocus(focus);
}

function captureFindingFocus() {
  const detail = $('#findDetail');
  const active = document.activeElement;
  if (!detail || !active || active === detail || !detail.contains(active) || active.tabIndex < 0) return null;
  if (!active.matches('button,a,input,textarea,select,[tabindex]')) return null;
  const attrs = {};
  for (const attr of active.attributes) {
    if (attr.name === 'id' || attr.name.startsWith('data-')) attrs[attr.name] = attr.value;
  }
  return {
    tag: active.tagName.toLowerCase(),
    className: typeof active.className === 'string' ? active.className : '',
    attrs,
  };
}

function restoreFindingFocus(focus) {
  if (!focus) return;
  const detail = $('#findDetail');
  if (!detail) return;
  const controls = detail.querySelectorAll('button,a,input,textarea,select,[tabindex]');
  for (const control of controls) {
    if (control.tagName.toLowerCase() !== focus.tag) continue;
    if (focus.className && control.className !== focus.className) continue;
    if (Object.entries(focus.attrs).some(([name, value]) => control.getAttribute(name) !== value)) continue;
    control.focus({ preventScroll: true });
    return;
  }
}

function renderFindings() {
  const box = $('#findList'); if (!box) return;
  const list = visibleFindings();
  const c = $('#findCount');
  if (c) {
    const n = list.length;
    const total = findings.length;
    c.textContent = total
      ? (findTagFilter ? `${n} of ${total} finding${total === 1 ? '' : 's'}` : `${total} finding${total === 1 ? '' : 's'}`)
      : '';
  }
  if (!findings.length) {
    setFindingsViewEmpty(true);
    box.innerHTML = findingsEmptyHTML();
    wireFindingsEmptyActions(box);
    selFinding = null;
    const detail = $('#findDetail');
    if (detail) detail.innerHTML = '';
    return;
  }
  setFindingsViewEmpty(false);
  if (!list.length) {
    box.innerHTML = `<div class="state-empty find-empty"><div class="state-empty-icon"><svg class="icon" aria-hidden="true" focusable="false"><use href="#i-tag"/></svg></div><div class="state-empty-title">No findings with tag “${esc(findTagFilter)}”</div><p class="state-empty-hint">Clear the tag filter or tag a finding in the detail pane.</p><div class="find-empty-actions"><button type="button" class="btn" id="findClearTagFilter">Show all findings</button></div></div>`;
    const clr = box.querySelector('#findClearTagFilter');
    if (clr) clr.onclick = () => { findTagFilter = ''; renderFindTagFilter(); renderFindings(); };
    selFinding = null; renderFindingDetail(); return;
  }
  if (!selFinding || !list.some(f => f.id === selFinding)) selFinding = list[0].id;
  box.innerHTML = list.map(f => `<div class="find-row${f.id === selFinding ? ' sel' : ''}${!findingReadiness(f).ready ? ' find-row-empty' : ''}${f.status === 'needs_verification' ? ' find-row-needs-verif' : ''}" data-id="${f.id}" role="option" tabindex="${f.id === selFinding ? '0' : '-1'}" aria-selected="${f.id === selFinding ? 'true' : 'false'}" aria-current="${f.id === selFinding ? 'true' : 'false'}">
    <span class="find-id">#${f.id}</span>
    <span class="sev" style="color:${sevColor(f.severity)}">${esc(f.severity)}</span>
    <span class="find-title">${esc(f.title)}</span>
    <span class="find-meta">${findingListMeta(f)}</span>
  </div>`).join('');
  const selectRow = (id, moveToDetail) => {
    if (id !== selFinding) findEditMode = false;
    selFinding = id;
    $('#findList')?.classList.remove('find-mobile-list-visible');
    $('#findDetail')?.classList.add('find-mobile-detail-visible');
    renderFindings();
    if (moveToDetail) setTimeout(() => {
      const target = window.matchMedia('(max-width:600px)').matches ? $('#findBackToList') : $('#findTitleText');
      target?.focus({ preventScroll: true });
    }, 0);
  };
  box.querySelectorAll('.find-row').forEach(el => {
    el.onclick = () => selectRow(Number(el.dataset.id), true);
    el.onkeydown = event => {
      if (event.key === 'Enter' || event.key === ' ') { event.preventDefault(); selectRow(Number(el.dataset.id), true); return; }
      const current = list.findIndex(f => f.id === Number(el.dataset.id));
      let next = current;
      if (event.key === 'ArrowDown') next = Math.min(list.length - 1, current + 1);
      else if (event.key === 'ArrowUp') next = Math.max(0, current - 1);
      else if (event.key === 'Home') next = 0;
      else if (event.key === 'End') next = list.length - 1;
      else return;
      event.preventDefault();
      selFinding = list[next].id;
      findEditMode = false;
      renderFindings();
      setTimeout(() => box.querySelector(`.find-row[data-id="${selFinding}"]`)?.focus({ preventScroll: true }), 0);
    };
  });
  if (findingDetailEditPending() && selFinding === bodyFindingId) findingDetailRefreshDeferred = true;
  else { findingDetailRefreshDeferred = false; renderFindingDetail(); }
}

// ---- block editor --------------------------------------------------------

function autoResizeTextarea(ta) {
  ta.style.height = 'auto';
  ta.style.height = ta.scrollHeight + 'px';
}

function startTextEdit(block, ta) {
  const i = Number(block.dataset.i);
  ta.value = bodyBlocks[i]?.md || '';
  bodyEditing = true;
  const view = block.querySelector('.find-text-view');
  block.classList.add('editing');
  const h = view.offsetHeight || 24;
  block.style.minHeight = h + 'px';
  view.style.visibility = 'hidden';
  view.style.position = 'absolute';
  ta.style.display = '';
  ta.style.minHeight = h + 'px';
  autoResizeTextarea(ta);
  ta.focus();
  ta.setSelectionRange(ta.value.length, ta.value.length);
}

function finishTextEdit(block, ta, fid) {
  bodyEditing = false;
  const i = Number(block.dataset.i);
  // If the user has switched to another finding since this edit began, the
  // module-level bodyBlocks now belong to a different finding — bail rather than
  // write this text into block[i] of the wrong finding.
  if (bodyFindingId !== fid || !bodyBlocks[i]) {
    return;
  }
  const md = ta.value;
  bodyBlocks[i].md = md;
  scheduleSave(fid);

  const view = block.querySelector('.find-text-view');
  block.classList.remove('editing');
  block.style.minHeight = '';
  view.style.visibility = '';
  view.style.position = '';
  ta.style.minHeight = '';

  if (md.trim()) {
    view.innerHTML = renderMD(md);
    view.style.display = '';
    ta.style.display = 'none';
    block.classList.remove('find-doc-text-empty');
  } else {
    view.innerHTML = '';
    view.style.display = 'none';
    ta.style.display = '';
    block.classList.add('find-doc-text-empty');
    autoResizeTextarea(ta);
  }
}

function renderBlockEl(b, i, total) {
  const isFirst = i === 0, isLast = i === total - 1;
  const upBtn = isFirst ? '' : `<button class="btn xs" data-mv="${i}" data-dir="-1" title="Move up" aria-label="Move evidence block ${i+1} up" style="padding:1px 5px;font-size:var(--fs-xs)">↑</button>`;
  const dnBtn = isLast ? '' : `<button class="btn xs" data-mv="${i}" data-dir="1" title="Move down" aria-label="Move evidence block ${i+1} down" style="padding:1px 5px;font-size:var(--fs-xs)">↓</button>`;
  const delBtn = `<button class="btn xs danger" data-del="${i}" title="Remove" aria-label="Remove evidence block ${i+1}" style="padding:1px 5px;font-size:var(--fs-xs)">✕</button>`;
  const controls = `<div class="find-block-controls">${upBtn}${dnBtn}${delBtn}</div>`;

  if (b.type === 'text') {
    const hasMd = !!(b.md && b.md.trim());
    return `<div class="find-block find-doc-text${hasMd ? '' : ' find-doc-text-empty'}" data-i="${i}">
      ${controls}<button type="button" class="btn xs find-edit-step" data-edit-step="${i}" aria-label="Edit ${escAttr(b.role || 'observation')} reproduction step ${i+1}">Edit step</button>${blockMetaEditor(b, i)}
      <div class="find-text-view md"${hasMd ? '' : ' style="display:none"'}>${hasMd ? renderMD(b.md) : ''}</div>
      <textarea class="find-text-edit block-text" data-i="${i}" rows="1" spellcheck="true" aria-label="Finding evidence step ${i+1}"
        ${hasMd ? 'style="display:none"' : ''}
        placeholder="${escAttr(findingStepPlaceholder(b.role || 'observation'))}">${esc(b.md || '')}</textarea>
    </div>`;
  }

  if (b.type === 'image') {
    if (b.missing) {
      return `<div class="find-block find-doc-image find-block-missing" data-i="${i}">
        ${controls}${blockMetaEditor(b, i)}
        <blockquote class="find-poc-callout find-poc-missing">
          <div><svg class="icon" aria-hidden="true" focusable="false"><use href="#i-warning"/></svg> Screenshot — evidence blob missing</div>
          <span class="hint">${esc(b.hash || '')}</span>
        </blockquote>
        <input class="find-poc-note-input block-caption" data-i="${i}" aria-label="Screenshot caption" value="${escAttr(b.caption || '')}" placeholder="Caption (optional)">
      </div>`;
    }
    const src = b.url || ('/api/findings/images/' + (b.hash || ''));
    return `<div class="find-block find-doc-image" data-i="${i}">
      ${controls}${blockMetaEditor(b, i)}
      <figure class="find-doc-figure">
        <img class="md-img find-doc-img" tabindex="0" role="button" aria-label="Open screenshot: ${escAttr(b.caption || 'screenshot')}" src="${escAttr(src)}" alt="${escAttr(b.caption || 'screenshot')}" title="Click to enlarge">
        <input class="find-poc-note-input block-caption" data-i="${i}" aria-label="Screenshot caption" value="${escAttr(b.caption || '')}"
          placeholder="Caption (optional)" onclick="event.stopPropagation()">
      </figure>
    </div>`;
  }

  // flow block. A missing flow (purged from history via prune_history / GC) is
  // rendered as a dimmed, non-clickable "evidence deleted" callout — the reference
  // and any annotation are preserved so the human knows the PoC is gone.
  if (b.missing) {
    return `<div class="find-block find-doc-flow find-block-missing" data-i="${i}">
      ${controls}
      <blockquote class="find-poc-callout find-poc-missing">
        <div><svg class="icon" aria-hidden="true" focusable="false"><use href="#i-warning"/></svg> PoC flow #${esc(String(b.flowId))} — evidence deleted from history</div>
        <span class="hint">Re-capture this endpoint to restore evidence</span>
      </blockquote>
      <input class="find-poc-note-input block-note" data-i="${i}" aria-label="Evidence annotation" value="${escAttr(b.note || '')}" placeholder="Annotation (optional)">
    </div>`;
  }
  const reqLine = b.method
    ? `<span class="m">${esc(b.method)}</span> <span class="p">${esc(b.host || '')}${esc(b.path || '')}</span>${b.status ? `<span class="sts">→ ${b.status}</span>` : ''}`
    : `<span class="hint">flow #${esc(String(b.flowId))}</span>`;
  return `<div class="find-block find-doc-flow" data-i="${i}" data-flow="${b.flowId}">
    ${controls}
     <div class="find-evidence-card">
       ${blockMetaEditor(b, i)}
       <blockquote class="find-poc-callout">${reqLine ? `<div class="find-poc-req">${reqLine}</div>` : ''}</blockquote>
       <div class="find-evidence-actions">
        <button type="button" class="btn xs find-open-flow" data-flow="${b.flowId}">Inspect request</button>
        <button type="button" class="btn xs find-flow-preview" data-flow="${b.flowId}">Generate report image</button>
         <button type="button" class="btn xs find-send-repeater" data-flow="${b.flowId}" aria-label="Send attached flow #${esc(String(b.flowId))} to Repeater">Send to Repeater →</button>
       </div>
     </div>
     <input class="find-poc-note-input block-note" data-i="${i}" aria-label="Evidence annotation" value="${escAttr(b.note || '')}"
       placeholder="Annotation (optional)" onclick="event.stopPropagation()">
   </div>`;
}

function wireSendToRepeaterButtons(container) {
  container.querySelectorAll('.find-send-repeater').forEach(btn => {
    btn.onclick = async event => {
      event.stopPropagation();
      const id = Number(btn.dataset.flow);
      const block = bodyBlocks.find(b => b.type === 'flow' && b.flowId === id);
      if (!(block && !block.missing && id)) return;
      const label = btn.textContent;
      btn.disabled = true;
      btn.textContent = 'Loading…';
      const ok = await sendToRepeater({ id });
      if (!ok && btn.isConnected) {
        btn.disabled = false;
        btn.textContent = label;
      }
    };
  });
}

function renderBodyEditor(container, fid) {
  if (!bodyBlocks.length) {
    container.innerHTML = '<div class="find-doc-empty">No evidence yet — add the shortest reproducible steps, prioritize a screenshot when visual state matters, and attach captured flows when traffic proves the issue.</div>';
    return;
  }
  container.innerHTML = bodyBlocks.map((b, i) => renderBlockEl(b, i, bodyBlocks.length)).join('');

  // Text blocks: rendered markdown at rest; overlay edit on click without layout jump.
  container.querySelectorAll('.find-doc-text').forEach(block => {
    const view = block.querySelector('.find-text-view');
    const ta = block.querySelector('.block-text');
    if (!view || !ta) return;

    if (ta.style.display !== 'none') autoResizeTextarea(ta);
    ta.addEventListener('input', () => autoResizeTextarea(ta));

    view.addEventListener('click', () => startTextEdit(block, ta));
    block.querySelector('[data-edit-step]')?.addEventListener('click', () => startTextEdit(block, ta));
    ta.addEventListener('blur', () => finishTextEdit(block, ta, fid));
  });

  // Evidence metadata updates the pending body snapshot while typing. The
  // debounced save remains cheap, and Done can flush the final focused value
  // without depending on browser blur/click ordering.
  container.querySelectorAll('.block-note').forEach(inp => {
    inp.addEventListener('input', () => {
      const i = Number(inp.dataset.i);
      if (bodyBlocks[i]) { bodyBlocks[i].note = inp.value; scheduleSave(fid); }
    });
    inp.addEventListener('click', e => e.stopPropagation());
  });
  container.querySelectorAll('.block-caption').forEach(inp => {
    inp.addEventListener('input', () => {
      const i = Number(inp.dataset.i);
      if (bodyBlocks[i]) { bodyBlocks[i].caption = inp.value; scheduleSave(fid); }
    });
    inp.addEventListener('click', e => e.stopPropagation());
  });
  container.querySelectorAll('.find-block-role').forEach(inp => inp.addEventListener('change', () => { const i=Number(inp.dataset.i); if (bodyBlocks[i]) { bodyBlocks[i].role=inp.value; scheduleSave(fid); } }));
  container.querySelectorAll('.find-block-proof').forEach(inp => inp.addEventListener('input', () => { const i=Number(inp.dataset.i); if (bodyBlocks[i]) { bodyBlocks[i].proof=inp.value; scheduleSave(fid); } }));

  // Move buttons.
  container.querySelectorAll('[data-mv]').forEach(btn => {
    btn.onclick = e => {
      e.stopPropagation();
      const i = Number(btn.dataset.mv), j = i + Number(btn.dataset.dir);
      if (j < 0 || j >= bodyBlocks.length) return;
      [bodyBlocks[i], bodyBlocks[j]] = [bodyBlocks[j], bodyBlocks[i]];
      renderFindBody(fid);
      scheduleSave(fid);
    };
  });

  // Delete buttons.
  container.querySelectorAll('[data-del]').forEach(btn => {
    btn.onclick = e => {
      e.stopPropagation();
      bodyBlocks.splice(Number(btn.dataset.del), 1);
      renderFindBody(fid);
      scheduleSave(fid);
    };
  });

  // Flow cards expose one explicit Inspect action. The request summary remains
  // presentational so keyboard and pointer users do not encounter two targets
  // with the same outcome.
  container.querySelectorAll('.find-open-flow').forEach(btn => {
    btn.onclick = event => {
      event.stopPropagation();
      const id = Number(btn.dataset.flow);
      if (id) openFindingFlow(id);
    };
  });
  wireFlowPreviewButtons(container);
  wireSendToRepeaterButtons(container);
}

function wireFlowPreviewButtons(container) {
  container.querySelectorAll('.find-flow-preview').forEach(btn => {
    btn.onclick = async event => {
      event.stopPropagation();
      const id = Number(btn.dataset.flow), fid = bodyFindingId;
      if (!id || !fid || btn.disabled) return;
      btn.disabled = true; const label = btn.textContent; btn.textContent = 'Generating…';
      try {
        await settleFindingBodyBeforeEvidence(fid);
        const sourceBlock = bodyBlocks.find(block => block.type === 'flow' && block.flowId === id);
        const updated = await api('/api/findings/' + fid + '/flow-preview', { method: 'POST', headers: {'content-type':'application/json'}, body: JSON.stringify({ flowId: id, caption: 'Generated HTTP report preview for flow #' + id, role: sourceBlock?.role || 'result', proof: sourceBlock?.proof || '', source: 'flow_preview', sourceFlowId: id }) });
        applyFindingEvidenceResponse(updated);
        toast('report image attached');
      } catch (err) { toast(err.message, 'error'); }
      finally { if (btn.isConnected) { btn.disabled = false; btn.textContent = label; } }
    };
  });
}

// ---- body editor ---------------------------------------------------------

function renderFindBody(fid) {
  const docEl = $('#findBody');
  if (docEl) renderBodyEditor(docEl, fid);
}

function scheduleSave(fid) {
  const previous = bodySaveTimers.get(fid);
  if (previous) clearTimeout(previous);
  const stateEl = $('#findSaveState');
  if (stateEl && bodyFindingId === fid) stateEl.textContent = 'Unsaved changes';
  // Snapshot the blocks now: switching findings before the 700 ms debounce fires
  // would otherwise make the deferred save read a module-level bodyBlocks that now
  // belongs to a different finding and PATCH it onto this one.
  const snap = bodyBlocks.map(b => {
    const r = { type: b.type };
    if (b.md !== undefined) r.md = b.md;
    if (b.flowId) r.flowId = b.flowId;
    if (b.note) r.note = b.note;
    if (b.hash) r.hash = b.hash;
    if (b.mime) r.mime = b.mime;
    if (b.caption) r.caption = b.caption;
    if (b.role) r.role = b.role;
    if (b.proof) r.proof = b.proof;
    if (b.source) r.source = b.source;
    if (b.sourceFlowId) r.sourceFlowId = b.sourceFlowId;
    return r;
  });
  bodySaveSnapshots.set(fid, snap);
  bodySaveTimers.set(fid, setTimeout(() => {
    bodySaveTimers.delete(fid);
    bodySaveSnapshots.delete(fid);
    void flushBodySave(fid, snap).catch(() => {});
  }, 700));
}

async function flushPendingBodySave(fid) {
  const timer = bodySaveTimers.get(fid);
  if (timer) { clearTimeout(timer); bodySaveTimers.delete(fid); }
  const snapshot = bodySaveSnapshots.get(fid);
  bodySaveSnapshots.delete(fid);
  if (snapshot) await flushBodySave(fid, snapshot);
}

// Paste/drop evidence can start while a reproduction textarea still owns
// focus, so its blur handler has not copied the latest text into bodyBlocks.
// Capture that focused value before an evidence endpoint returns an
// authoritative body and replaces the editor DOM.
function captureActiveFindingTextEditor(fid) {
  if (bodyFindingId !== fid) return;
  const ta = document.activeElement;
  if (!(ta instanceof HTMLTextAreaElement) || !ta.matches('#findBody .block-text')) return;
  const i = Number(ta.dataset.i);
  if (!Number.isInteger(i) || bodyBlocks[i]?.type !== 'text' || bodyBlocks[i].md === ta.value) return;
  bodyBlocks[i].md = ta.value;
  scheduleSave(fid);
}

// Evidence endpoints mutate the same ordered body as the debounced editor.
// Flush and settle older body snapshots first so a late PATCH cannot remove a
// screenshot, captured flow, or generated preview that just attached.
async function settleFindingBodyBeforeEvidence(fid) {
  captureActiveFindingTextEditor(fid);
  await flushPendingBodySave(fid);
  while (bodySavesInFlight > 0) await new Promise(resolve => setTimeout(resolve, 20));
  const stateEl = $('#findSaveState');
  if (bodyFindingId === fid && stateEl?.textContent === 'Save failed') {
    throw new Error('save pending finding changes before attaching evidence');
  }
}

function applyFindingEvidenceResponse(updated) {
  const id = Number(updated?.id);
  if (!id) return false;
  const focus = captureFindingFocus();
  const at = findings.findIndex(item => item.id === id);
  if (at >= 0) findings[at] = updated;
  else findings.unshift(updated);
  // Pre-seed the authoritative blocks before a focused editor node is replaced.
  // Any blur handler caused by that replacement will therefore snapshot the
  // attached evidence as well as the last typed value, never an older body.
  if (bodyFindingId === id) bodyBlocks = (updated.blocks || []).map(block => ({ ...block }));
  renderFindings();
  if (findingDetailRefreshDeferred) {
    findingDetailRefreshDeferred = false;
    renderFindingDetail();
  }
  restoreFindingFocus(focus);
  return true;
}

function findingWriteQueue(id) {
  let queue = findingWriteQueues.get(id);
  if (!queue) {
    queue = { running: false, pendingFields: null, pendingWaiters: [], latest: {}, latestValues: {} };
    findingWriteQueues.set(id, queue);
  }
  return queue;
}

function pendingFindingValue(id, key, fallback) {
  const queue = findingWriteQueues.get(id);
  return queue && Object.prototype.hasOwnProperty.call(queue.latestValues, key)
    ? queue.latestValues[key]
    : fallback;
}

function acknowledgedFindingValue(id, key, fallback) {
  const finding = findings.find(item => item.id === id);
  return finding && Object.prototype.hasOwnProperty.call(finding, key)
    ? finding[key]
    : fallback;
}

function enqueueFindingPatch(id, fields) {
  const queue = findingWriteQueue(id);
  const tokens = {};
  for (const key of Object.keys(fields)) {
    tokens[key] = Symbol(key);
    queue.latest[key] = tokens[key];
    queue.latestValues[key] = fields[key];
  }
  if (!queue.pendingFields) queue.pendingFields = {};
  Object.assign(queue.pendingFields, fields);
  const result = new Promise((resolve, reject) => queue.pendingWaiters.push({ resolve, reject, tokens }));
  void drainFindingWrites(id);
  return result;
}

async function drainFindingWrites(id) {
  const queue = findingWriteQueues.get(id);
  if (!queue || queue.running) return;
  queue.running = true;
  try {
    while (queue.pendingFields) {
      const fields = queue.pendingFields;
      queue.pendingFields = null;
      const waiters = queue.pendingWaiters.splice(0);
      try {
        await api('/api/findings/' + id, {
          method: 'PATCH', headers: { 'content-type': 'application/json' },
          body: JSON.stringify(fields),
        });
        Object.assign(findings.find(x => x.id === id) || {}, fields);
        for (const waiter of waiters) {
          const latest = Object.entries(waiter.tokens).every(([key, token]) => queue.latest[key] === token);
          waiter.resolve({ latest });
        }
      } catch (error) {
        for (const waiter of waiters) waiter.reject(error);
      }
    }
  } finally {
    queue.running = false;
    if (!queue.pendingFields && !queue.pendingWaiters.length) findingWriteQueues.delete(id);
    else void drainFindingWrites(id);
  }
}

async function flushBodySave(fid, snapshot) {
  if (!fid || !snapshot) return;
  bodySavesInFlight++;
  const stateEl = $('#findSaveState');
  if (stateEl && bodyFindingId === fid) stateEl.textContent = 'Saving…';
  try {
    const result = await enqueueFindingPatch(fid, { blocks: snapshot });
    if (result?.latest && !bodySaveTimers.has(fid) && !bodySaveSnapshots.has(fid)) {
      const current = $('#findSaveState');
      if (current && bodyFindingId === fid) current.textContent = 'Saved';
    }
  } catch (e) { const s = $('#findSaveState'); if (s && bodyFindingId === fid) s.textContent = 'Save failed'; toast('body save: ' + e.message, 'error'); throw e; }
  finally { bodySavesInFlight--; setTimeout(refreshDeferredFindingDetail, 0); }
}

// ---- detail pane ---------------------------------------------------------

async function patchFinding(id, fields) {
  findingWritesInFlight++;
  const stateEl = $('#findSaveState');
  if (stateEl && bodyFindingId === id) stateEl.textContent = 'Saving…';
  try {
    const result = await enqueueFindingPatch(id, fields);
    if (result?.latest) {
      const current = $('#findSaveState');
      if (current && bodyFindingId === id) current.textContent = 'Saved';
    }
    return result;
  } catch (error) {
    const current = $('#findSaveState');
    if (current && bodyFindingId === id) current.textContent = 'Save failed';
    throw error;
  } finally {
    findingWritesInFlight--;
    setTimeout(refreshDeferredFindingDetail, 0);
  }
}

function renderFindingDetail() {
  const box = $('#findDetail'); if (!box) return;
  findingDetailRefreshDeferred = false;
  const f = findings.find(x => x.id === selFinding);
  if (!f) { box.innerHTML = '<div class="state-empty"><div class="state-empty-icon"><svg class="icon" aria-hidden="true" focusable="false"><use href="#i-archive"/></svg></div><div class="state-empty-title">No finding selected</div><p class="state-empty-hint">Select a finding from the list to view its details.</p></div>'; return; }
  const edit = findEditMode;
  const narrativePresets = Object.keys(FINDING_OUTLINES);
  const preset = inferFindingOutline(f.blocks || []);
  const readiness = findingReadiness(f);

  const statusSel = STATUSES.map(s => `<option value="${s}"${s === f.status ? ' selected' : ''}>${esc(statusLabel(s))}</option>`).join('');
  const sevOpts = ['Critical', 'High', 'Medium', 'Low', 'Info'].map(s => `<option value="${s}"${s === f.severity ? ' selected' : ''}>${s}</option>`).join('');
  const envOpts = ['', 'prod', 'staging', 'local'].map(e => `<option value="${e}"${(f.environment || '') === e ? ' selected' : ''}>${e || 'env…'}</option>`).join('');
  const missBanner = (() => {
    const missFlow = (f.blocks || []).filter(b => b.type === 'flow' && b.missing).length;
    const missImg = (f.blocks || []).filter(b => b.type === 'image' && b.missing).length;
    const badType = (f.blocks || []).filter(b => b.type && !['text', 'flow', 'image'].includes(b.type)).length;
    const parts = [];
    if (missFlow) parts.push(`${missFlow} PoC flow${missFlow === 1 ? '' : 's'} deleted from history`);
    if (missImg) parts.push(`${missImg} screenshot${missImg === 1 ? '' : 's'} missing`);
    if (badType) parts.push(`${badType} unknown block type${badType === 1 ? '' : 's'} (edit & re-save to fix)`);
    return parts.length ? `<div class="find-missing-banner"><svg class="icon" aria-hidden="true" focusable="false"><use href="#i-warning"/></svg> ${parts.join(' · ')} — restore evidence if needed.</div>` : '';
  })();
  const completeBar = readiness.ready
    ? `<div class="find-complete find-complete-ready" role="status"><span class="find-ready">Report ready</span> — claim, evidence, remediation, retest, and review are complete.</div>`
    : `<div class="find-complete find-complete-draft" role="status"><span class="find-draft">${esc(findingReadinessLabel(readiness.stage))}</span> — still need: ${readiness.gaps.map(g => `<a href="#${escAttr(findingGapTarget(g))}" class="find-gap-link">${esc(findingGapLabel(g))}</a>`).join(', ') || 'content'}</div>`;
  const verifBanner = f.status === 'needs_verification'
    ? `<div class="find-verif-banner" role="status">
        <div class="find-verif-title"><svg class="icon" aria-hidden="true" focusable="false"><use href="#i-warning"/></svg> Needs human verification</div>
        ${edit
          ? `<textarea id="findVerifInstr" class="find-verif-text" rows="3" aria-label="Human verification instructions" placeholder="What should the human check? Exact steps…">${esc(f.verificationInstructions || '')}</textarea>`
          : `<div class="find-verif-read">${f.verificationInstructions ? esc(f.verificationInstructions) : '<span class="hint">No verification instructions recorded.</span>'}</div>`}
      </div>` : '';
  const machineProof = (() => {
    const v = f.verification;
    if (!v) return '';
    let gates = {};
    try { gates = typeof v.gates === 'string' ? JSON.parse(v.gates || '{}') : (v.gates || {}); } catch { gates = {}; }
    const gateKeys = Object.keys(gates);
    const gateRows = gateKeys.length
      ? gateKeys.map(k => {
          const g = gates[k] || {};
          let ok = false, detail = '';
          if (k === 'differential') { ok = !!g.reproduced; detail = g.detail || (g.reproN != null ? 'repro ×' + g.reproN : ''); }
          else if (k === 'agent') { ok = g.verdict === 'real'; detail = g.reasoning || g.verdict || ''; }
          else if (k === 'oob') { ok = !!g.confirmed; detail = g.detail || (g.token ? 'token present' : ''); }
          else if (k === 'human') { ok = !!g.confirmed; detail = g.note || g.answeredBy || ''; }
          else { ok = g.ok === true || g.passed === true || g.confirmed === true || g.verdict === 'real'; detail = g.detail || g.reasoning || g.note || ''; }
          return `<div class="find-gate-row"><span class="find-gate-name">${esc(k)}</span><span class="find-gate-ok" style="color:${ok ? 'var(--accent)' : 'var(--amber)'}">${ok ? 'pass' : 'fail'}</span>${detail ? `<span class="hint">${esc(String(detail).slice(0, 160))}</span>` : ''}</div>`;
        }).join('')
      : '<span class="hint">Gate detail unavailable</span>';
    return `<section class="find-machine-proof" aria-label="External-agent verification">
      <div class="find-machine-title"><svg class="icon" aria-hidden="true" focusable="false"><use href="#i-gear"/></svg> External-agent verification · confidence <b>${esc(String(v.confidence ?? 0))}%</b></div>
      <div class="hint">Class <b>${esc(v.vulnClass || '—')}</b>${v.runId ? ' · run #' + esc(String(v.runId)) : ''}${v.reproCount ? ' · repro ×' + esc(String(v.reproCount)) : ''}${v.oobToken ? ' · OOB' : ''}</div>
      <div class="find-gate-list">${gateRows}</div>
      ${(v.baselineFlow || v.payloadFlow) ? `<div class="hint">PoC flows: ${[v.baselineFlow && ('#' + v.baselineFlow), v.payloadFlow && ('#' + v.payloadFlow)].filter(Boolean).join(' · ')}</div>` : ''}
    </section>`;
  })();

  const impactRead = f.impact
    ? `<div class="find-sticky-impact">${esc(f.impact)}</div>`
    : `<div class="hint">No impact written yet.</div>`;
  const metaStrip = `<details class="find-meta-strip" open><summary>Technical context · target, classification, and tags</summary>
    <div class="find-meta-strip-body">
      ${edit
        ? `<section class="find-sec" id="find-sec-why"><h3>Why it's a finding</h3><textarea id="findWhy" class="find-field-text" rows="2" aria-label="Why this is a finding">${esc(f.why || '')}</textarea></section>
           <section class="find-sec" id="find-sec-target"><h3>Affected target</h3><input id="findTarget" class="btn btn-field find-target-input" type="text" aria-label="Affected target" value="${escAttr(f.target || '')}"></section>`
        : `<p><b>Why</b> — ${f.why ? esc(f.why) : '<span class="hint">—</span>'}</p>
           <p><b>Target</b> — ${f.target ? esc(f.target) : '<span class="hint">—</span>'}</p>`}
      <p class="hint">CVSS ${esc(f.cvss || '—')} · CWE ${esc(f.cwe || '—')} · env ${esc(f.environment || '—')}</p>
      <div class="find-tags-bar"><div class="find-tag-chips">${(f.tags || []).map(t => `<span class="find-tag-chip">${esc(t)}</span>`).join('') || '<span class="hint">no tags</span>'}</div>
        ${edit ? `<button class="btn xs" id="findEditTags"><svg class="icon" aria-hidden="true" focusable="false"><use href="#i-pencil"/></svg> Tags</button>` : ''}</div>
    </div></details>`;

  box.innerHTML = `<article class="find-article${edit ? ' find-editing' : ' find-reading'}">
    <header class="find-header find-header-sticky">
      <div class="find-header-top">
        <button class="btn find-mobile-back" id="findBackToList" type="button" aria-label="Back to findings">← Findings</button>
        <span class="find-id-badge">#${f.id}</span>
        ${edit
          ? `<select id="findSeverity" class="btn find-sev-select" aria-label="Severity" style="color:${sevColor(f.severity)}">${sevOpts}</select>
             <h2 class="find-title-text" id="findTitleText" tabindex="-1">${esc(f.title)}</h2>
             <button class="btn xs" id="findRename" title="Rename finding"><svg class="icon" aria-hidden="true" focusable="false"><use href="#i-pencil"/></svg></button>`
          : `<span class="sev" style="color:${sevColor(f.severity)}">${esc(f.severity)}</span>
             <h2 class="find-title-text" id="findTitleText" tabindex="-1">${esc(f.title)}</h2>
             <span class="sev find-status-badge" style="color:${statusBadgeColor(f.status)}">${esc(statusLabel(f.status))}</span>`}
        <div class="spacer"></div>
        <span id="findSaveState" class="find-save-state" role="status" aria-live="polite">${edit ? 'Saved' : ''}</span>
        <button class="btn ${edit ? '' : 'btn-primary'}" id="findToggleEdit">${edit ? 'Done' : 'Edit'}</button>
      </div>
      ${edit ? `<div class="find-meta-bar">
        <select id="findStatus" class="btn" style="background:var(--bg3)" aria-label="Finding status">${statusSel}</select>
        <select id="findEnv" class="btn" style="background:var(--bg3)" aria-label="Environment">${envOpts}</select>
        <select id="findConfidence" class="btn" style="background:var(--bg3)" aria-label="Finding confidence"><option value="">Confidence…</option><option value="tentative"${f.confidence === 'tentative' ? ' selected' : ''}>Tentative</option><option value="firm"${f.confidence === 'firm' ? ' selected' : ''}>Firm</option><option value="certain"${f.confidence === 'certain' ? ' selected' : ''}>Certain</option></select>
        <div class="find-cvss-field"><label for="findCvss">CVSS</label><input id="findCvss" class="find-cvss-inline" type="text" value="${escAttr(f.cvss || '')}"></div>
        <div class="find-cvss-field"><label for="findCwe">CWE</label><input id="findCwe" class="find-cvss-inline" type="text" value="${escAttr(f.cwe || '')}"></div>
        <div class="spacer"></div>

        <button class="btn danger xs" id="findDelete">Delete</button>
      </div>` : ''}
    </header>
    ${completeBar}
    ${missBanner}
    ${verifBanner}
    ${machineProof}

    <section class="find-sec find-sec-claim" id="find-sec-summary">
      <h3>Claim</h3>
      ${edit ? `<textarea id="findSummary" class="find-field-text find-claim-summary" rows="2" aria-label="Finding summary" placeholder="One sentence: who can do what, to which asset, and why it matters…">${esc(f.summary || '')}</textarea>` : `<p class="find-claim-summary">${f.summary ? esc(f.summary) : '<span class="hint">No concise claim yet.</span>'}</p>`}
    </section>
    <section class="find-sec find-sec-impact-sticky" id="find-sec-impact">
      <h3>Impact</h3>
      ${edit
        ? `<textarea id="findImpact" class="find-field-text" rows="2" aria-label="Finding impact" placeholder="What an attacker gains…">${esc(f.impact || '')}</textarea>`
        : impactRead}
    </section>
    ${metaStrip}

    <section class="find-sec" id="find-sec-poc">
      <div class="find-section-head"><h3>Reproduction &amp; evidence</h3>${edit ? `<div class="find-preset-label"><label for="findNarrativePreset">Step outline</label><select id="findNarrativePreset" class="btn btn-field" aria-label="Reproduction step outline"><option value="">Choose outline…</option>${narrativePresets.map(p => `<option value="${escAttr(p)}">${esc(p)}</option>`).join('')}</select><button type="button" class="btn xs" id="findApplyPreset" disabled>Add outline</button></div>` : `<span class="find-preset-read">${esc(preset)}</span>`}</div>
      <p class="hint find-poc-hint">Differential proof is available for Before → Action → After cases; choose the structure that matches the security claim.</p>
      <div class="find-evidence-rail" id="findEvidenceRail">
        <div class="find-doc" id="findBody"></div>
        <div class="find-doc-actions" id="findDocActions" ${edit ? '' : 'hidden'}>
          <button class="btn btn-primary find-evidence-screenshot" id="findAddImage">＋ Screenshot</button>
          <button class="btn find-attach-flow" id="findAddFlow">＋ Attach flow<span id="findPocReady" class="hint"></span></button>
          <button class="btn" id="findAddText">＋ Add step</button>
          <input type="file" id="findImageFile" accept="image/png,image/jpeg,image/gif,image/webp,image/bmp,image/avif" hidden>
          <span class="hint find-evidence-help">Paste or drop an image here. Flows can generate a report preview.</span>
        </div>
      </div>
    </section>

    <details class="find-more" id="find-sec-fix"${edit || f.fix || readiness.gaps.includes('fix') ? ' open' : ''}>
      <summary>Remediation</summary>
      ${edit
        ? `<textarea id="findFix" class="find-field-text" rows="2" aria-label="Finding remediation">${esc(f.fix || '')}</textarea>`
        : `<div class="hint" style="padding:8px 0">${f.fix ? esc(f.fix) : '—'}</div>`}
    </details>
    <section class="find-sec find-retest" id="find-sec-retest"><h3>Expected secure behavior / retest</h3>${edit ? `<textarea id="findRetest" class="find-field-text" rows="2" aria-label="Expected secure behavior and retest" placeholder="After the fix, what should the tester observe?…">${esc(f.retest || '')}</textarea>` : `<p>${f.retest ? esc(f.retest) : '<span class="hint">Not recorded.</span>'}</p>`}</section>
    <aside class="find-readiness" id="find-sec-review" aria-label="Finding report readiness"><strong>Review</strong><span>${f.confidence ? 'Confidence: ' + esc(f.confidence) : 'Confidence: not set'}</span><span>${esc(findingReadinessLabel(readiness.stage))}</span>${readiness.visualProofRecommended ? '<span>Screenshot recommended when it would make the claim easier to verify.</span>' : ''}</aside>
  </article>`;
  box.onfocusout = () => setTimeout(refreshDeferredFindingDetail, 0);
  $('#findBackToList')?.addEventListener('click', () => {
    $('#findList')?.classList.add('find-mobile-list-visible');
    box.classList.remove('find-mobile-detail-visible');
    setTimeout(() => document.querySelector(`.find-row[data-id="${selFinding}"]`)?.focus({ preventScroll: true }), 0);
  });

  bodyFindingId = f.id;
  bodyBlocks = (f.blocks || []).map(b => ({ ...b }));
  if (edit) renderFindBody(f.id);
  else renderFindReportBody(f.id);

  const te = $('#findToggleEdit');
  if (te) te.onclick = async () => {
    if (edit) { te.disabled = true; te.textContent = 'Saving…'; const s = $('#findSaveState'); if (s) s.textContent = 'Saving…'; try { await flushPendingBodySave(f.id); while (findingWritesInFlight || bodySavesInFlight) await new Promise(resolve => setTimeout(resolve, 20)); findEditMode = false; renderFindingDetail(); } catch (err) { if (s) s.textContent = 'Save failed'; toast(err.message, 'error'); te.disabled = false; te.textContent = 'Done'; } }
    else { findEditMode = true; renderFindingDetail(); }
  };

  const blurPatch = (id, key, getVal) => {
    const el = $(id); if (!el) return;
    const commit = async () => {
      const v = getVal(el);
      const previous = f[key] || '';
      const expected = pendingFindingValue(f.id, key, previous);
      if (v === expected) return;
      try {
        const result = await patchFinding(f.id, { [key]: v });
        // A newer edit for this same field may have been coalesced while the
        // request was in flight. Its completion owns the local model and reload.
        if (!result?.latest) return;
        f[key] = v;
      } catch (err) {
        const authoritative = acknowledgedFindingValue(f.id, key, previous);
        if (el.value === v) el.value = authoritative;
        toast(err.message); return;
      }
      await loadFindings();
    };
    el.addEventListener('blur', commit);
    if(el.tagName==='SELECT')el.addEventListener('change',commit);
  };
  if (edit) {
    blurPatch('#findImpact', 'impact', el => el.value);
    blurPatch('#findWhy', 'why', el => el.value);
    blurPatch('#findTarget', 'target', el => el.value);
    blurPatch('#findCvss', 'cvss', el => el.value);
    blurPatch('#findCwe', 'cwe', el => el.value);
    blurPatch('#findConfidence', 'confidence', el => el.value);
    blurPatch('#findFix', 'fix', el => el.value);
    blurPatch('#findSummary', 'summary', el => el.value);
    blurPatch('#findRetest', 'retest', el => el.value);
  }
  if (edit) blurPatch('#findVerifInstr', 'verificationInstructions', el => el.value);
  const presetEl = $('#findNarrativePreset');
  const applyPreset = $('#findApplyPreset');
  if (presetEl && applyPreset) {
    presetEl.onchange = () => { applyPreset.disabled = !presetEl.value; };
    applyPreset.onclick = () => {
      const roles = FINDING_OUTLINES[presetEl.value];
      if (!roles) return;
      const start = bodyBlocks.length;
      bodyBlocks.push(...roles.map(role => ({ type: 'text', md: '', role })));
      renderFindBody(f.id);
      scheduleSave(f.id);
      presetEl.value = '';
      applyPreset.disabled = true;
      document.querySelector(`#findBody .find-block[data-i="${start}"] .block-text`)?.focus();
    };
  }

  const renameBtn = $('#findRename');
  if (renameBtn) renameBtn.onclick = async () => {
    const t = await uiPrompt({ title: 'Rename finding', value: f.title, placeholder: 'Finding title' });
    if (t == null || t === pendingFindingValue(f.id, 'title', f.title)) return;
    try { const result = await patchFinding(f.id, { title: t }); if (!result?.latest) return; f.title = t; const el = $('#findTitleText'); if (el) el.textContent = t; toast('finding renamed'); renderFindings(); }
    catch (err) { toast(err.message); }
  };
  const stSel = $('#findStatus');
  if (stSel) stSel.onchange = async e => {
    const previous = f.status || '';
    const attempted = e.target.value;
    try {
      const result = await patchFinding(f.id, { status: attempted });
      if (!result?.latest) return;
    } catch (err) {
      const authoritative=acknowledgedFindingValue(f.id, 'status', previous);
      if (e.target.value === attempted) e.target.value = authoritative;
      toast(err.message); return;
    }
    f.status = attempted;
    toast('status: ' + statusLabel(f.status));
    await loadFindings();
  };
  const sevSel = $('#findSeverity');
  if (sevSel) sevSel.onchange = async e => {
    const previous = f.severity || '';
    const attempted = e.target.value;
    try {
      const result = await patchFinding(f.id, { severity: attempted });
      if (!result?.latest) return;
    } catch (err) {
      const authoritative=acknowledgedFindingValue(f.id, 'severity', previous);
      if (e.target.value === attempted) { e.target.value = authoritative; e.target.style.color = sevColor(authoritative); }
      toast(err.message); return;
    }
    f.severity = attempted;
    await loadFindings();
  };
  const envSel = $('#findEnv');
  if (envSel) envSel.onchange = async e => {
    const previous = f.environment || '';
    const attempted = e.target.value;
    try {
      const result = await patchFinding(f.id, { environment: attempted });
      if (!result?.latest) return;
    } catch (err) {
      const authoritative=acknowledgedFindingValue(f.id, 'environment', previous);
      if (e.target.value === attempted) e.target.value = authoritative;
      toast(err.message); return;
    }
    f.environment = attempted;
    await loadFindings();
  };
  const deleteBtn = $('#findDelete');
  if (deleteBtn) deleteBtn.onclick = async () => {
    const visible = visibleFindings();
    const at = visible.findIndex(x => x.id === f.id);
    const next = visible[at + 1] || visible[at - 1] || null;
    if (!await uiConfirm('Delete finding', `Delete <b>${esc(f.title)}</b>? This cannot be undone.`, 'Delete', 'btn danger', 'var(--red)')) return;
    deleteBtn.disabled = true;
    deleteBtn.setAttribute('aria-busy', 'true');
    try {
      await api('/api/findings/' + f.id, { method: 'DELETE' });
      selFinding = next?.id || null;
      toast('finding deleted');
      await loadFindings();
    } catch (err) {
      deleteBtn.disabled = false;
      deleteBtn.setAttribute('aria-busy', 'false');
      toast(err.message);
    }
  };
  $('#findEditTags') && ($('#findEditTags').onclick = async () => {
    const cur = (f.tags || []).join(' ');
    const v = await uiPrompt({ title: 'Finding tags (report scope)', value: cur, placeholder: 'cms website app api out-of-scope' });
    if (v == null) return;
    const tags = parseFindTags(v);
    try {
      const result = await patchFinding(f.id, { tags });
      if (!result?.latest) return;
      f.tags = tags;
      toast(tags.length ? 'tags: ' + tags.join(', ') : 'tags cleared');
      await loadFindings();
    } catch (err) { toast(err.message); }
  });
  if (edit) {
    $('#findAddText').onclick = () => {
      bodyBlocks.push({ type: 'text', md: '', role: 'observation' });
      renderFindBody(f.id);
      const tas = document.querySelectorAll('#findBody .block-text');
      if (tas.length) tas[tas.length - 1].focus();
    };
    $('#findAddFlow').onclick = () => addPoCFlowsToFinding(f.id);
    $('#findAddImage').onclick = () => $('#findImageFile')?.click();
    const attachScreenshot = async file => {
      if (!file || !file.type?.startsWith('image/')) { toast('choose an image file', 'error'); return; }
      try {
        const dataUrl = await new Promise((resolve, reject) => { const r = new FileReader(); r.onload = () => resolve(r.result); r.onerror = () => reject(new Error('failed to read image')); r.readAsDataURL(file); });
        await settleFindingBodyBeforeEvidence(f.id);
        const updated = await api('/api/findings/' + f.id + '/images', { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ data: dataUrl, mime: file.type, caption: file.name || 'Screenshot evidence', role: 'result', source: 'operator_upload' }) });
        applyFindingEvidenceResponse(updated);
        toast('screenshot attached');
      } catch (err) { toast(err.message, 'error'); }
    };
    $('#findImageFile').onchange = async e => { const file = e.target.files?.[0]; e.target.value = ''; await attachScreenshot(file); };
    const rail = $('#findEvidenceRail');
    if (rail) {
      rail.addEventListener('paste', e => { const file = [...(e.clipboardData?.files || [])].find(x => x.type.startsWith('image/')); if (file) { e.preventDefault(); void attachScreenshot(file); } });
      rail.addEventListener('dragover', e => { if ([...(e.dataTransfer?.items || [])].some(x => x.type.startsWith('image/'))) { e.preventDefault(); rail.classList.add('is-drop-target'); } });
      rail.addEventListener('dragleave', () => rail.classList.remove('is-drop-target'));
      rail.addEventListener('drop', e => { rail.classList.remove('is-drop-target'); const file = [...(e.dataTransfer?.files || [])].find(x => x.type.startsWith('image/')); if (file) { e.preventDefault(); void attachScreenshot(file); } });
    }
    updateFindPocBtn();
  }
}

function renderFindReportBody(fid) {
  const container = $('#findBody');
  if (!container) return;
  if (!bodyBlocks.length) {
    container.innerHTML = '<div class="find-doc-empty">No evidence yet — switch to Edit to add reproducible steps, screenshots, and captured flows.</div>';
    return;
  }
  let step = 0;
  container.innerHTML = bodyBlocks.map((b) => {
    if (b.type === 'text') {
      const md = (b.md || '').trim();
      if (!md) return '';
      step++;
      return `<div class="find-report-step"><div class="find-report-stepn">${step}</div><div class="find-report-stepbody"><div class="find-report-meta"><span class="find-role-badge">${esc(b.role || 'observation')}</span>${b.source ? `<span class="find-provenance">${esc(b.source)}</span>` : ''}</div>${renderMD(md)}${b.proof ? `<div class="find-proof-read"><b>Supports:</b> ${esc(b.proof)}</div>` : ''}</div></div>`;
    }
    if (b.type === 'image') {
      step++;
      const imageMeta = `<div class="find-report-meta"><span class="find-role-badge">${esc(b.role || 'observation')}</span>${b.source ? `<span class="find-provenance">${esc(b.source)}${b.sourceFlowId ? ' · flow #' + esc(String(b.sourceFlowId)) : ''}</span>` : ''}</div>`;
      if (b.missing) {
        return `<div class="find-report-step"><div class="find-report-stepn">${step}</div><div class="find-report-stepbody">${imageMeta}<div class="find-poc-missing"><svg class="icon" aria-hidden="true" focusable="false"><use href="#i-warning"/></svg> Screenshot missing</div></div></div>`;
      }
      const src = b.url || ('/api/findings/images/' + (b.hash || ''));
      return `<div class="find-report-step"><div class="find-report-stepn">${step}</div><div class="find-report-stepbody">${imageMeta}
        <figure class="find-doc-figure"><img class="md-img find-doc-img" tabindex="0" role="button" aria-label="Open screenshot: ${escAttr(b.caption || 'screenshot')}" src="${escAttr(src)}" alt="${escAttr(b.caption || 'screenshot')}">
        ${b.caption ? `<figcaption class="hint">${esc(b.caption)}</figcaption>` : ''}${b.proof ? `<div class="find-proof-read"><b>Proves:</b> ${esc(b.proof)}</div>` : '<div class="find-proof-needed">Proof annotation needed.</div>'}</figure></div></div>`;
    }
    if (b.type === 'flow') {
      step++;
      if (b.missing) {
        return `<div class="find-report-step"><div class="find-report-stepn">${step}</div>
          <div class="find-poc-missing"><svg class="icon" aria-hidden="true" focusable="false"><use href="#i-warning"/></svg> PoC flow #${esc(String(b.flowId))} — missing${b.note ? ' · ' + esc(b.note) : ''}</div></div>`;
      }
      const reqLine = b.method
        ? `<span class="m" style="color:${methodColor(b.method)}">${esc(b.method)}</span> <span class="p">${esc(b.host || '')}${esc(b.path || '')}</span>${b.status ? `<span class="sts" style="color:${statusColor(b.status)}">→ ${b.status}</span>` : ''}`
        : `flow #${esc(String(b.flowId))}`;
       return `<div class="find-report-step"><div class="find-report-stepn">${step}</div>
         <div class="find-report-stepbody">
           <div class="find-report-flow">
             <div class="find-report-meta"><span class="find-role-badge">${esc(b.role || 'observation')}</span>${b.source ? `<span class="find-provenance">${esc(b.source)}${b.sourceFlowId ? ' · flow #' + esc(String(b.sourceFlowId)) : ''}</span>` : ''}</div>${b.note ? `<div class="find-report-note">${esc(b.note)}</div>` : ''}
             <div class="find-poc-req">${reqLine}</div>
             ${b.proof ? `<div class="find-proof-read"><b>Proves:</b> ${esc(b.proof)}</div>` : '<div class="find-proof-needed">Proof annotation needed.</div>'}
           </div>
           <div class="find-evidence-actions">
             <button type="button" class="btn xs find-open-flow" data-flow="${b.flowId}">Inspect request</button>
             <button type="button" class="btn xs find-flow-preview" data-flow="${b.flowId}">Generate report image</button>
             <button type="button" class="btn xs find-send-repeater" data-flow="${b.flowId}" aria-label="Send attached flow #${esc(String(b.flowId))} to Repeater">Send to Repeater →</button>
           </div>
         </div></div>`;
    }
    return '';
  }).join('');
  container.querySelectorAll('.find-open-flow').forEach(btn => {
    btn.onclick = () => { const id = Number(btn.dataset.flow); if (id) flowPopup(id); };
  });
  wireSendToRepeaterButtons(container);
  wireFlowPreviewButtons(container);
}

function pocFlowIdsReady() {
  if (state.selected?.size) return [...state.selected];
  if (state.selId != null) return [state.selId];
  return [];
}

export function updateFindPocBtn() {
  const hint = $('#findPocReady');
  if (!hint) return;
  const n = pocFlowIdsReady().length;
  hint.textContent = n ? ` · ${n} ready` : '';
}

async function attachFlowsToFinding(findingId, ids) {
  if (!ids.length) return {attached:0,failed:[]};
  findingId=Number(findingId);
  if(findingAttachPending.has(findingId)){toast('flow attachment already in progress');return {attached:0,failed:ids.slice(),pending:true};}
  findingAttachPending.add(findingId);
  const failed=[];
  let attached=0;
  let latest=null;
  toast('attaching '+ids.length+' flow'+(ids.length===1?'':'s')+'…');
  try {
    await settleFindingBodyBeforeEvidence(findingId);
    for (const fid of ids) {
      try{
        latest = await api('/api/findings/' + findingId + '/flows', {
          method: 'POST', headers: { 'content-type': 'application/json' },
          body: JSON.stringify({ flowId: fid, role: 'result', source: 'captured_flow', sourceFlowId: fid }),
        });
        attached++;
      }catch(error){failed.push({id:fid,error});}
    }
    if (latest) applyFindingEvidenceResponse(latest);
    if(!failed.length)toast('attached '+attached+' flow'+(attached===1?'':'s'),'success');
    else if(attached)toast('attached '+attached+' of '+ids.length+' flows · '+failed.length+' failed','warn');
    else toast('could not attach flows: '+(failed[0]?.error?.message||'request failed'),'error');
    return {attached,failed};
  } finally { findingAttachPending.delete(findingId); }
}

async function addPoCFlowsToFinding(findingId) {
  const ids = pocFlowIdsReady();
  if (ids.length) await attachFlowsToFinding(findingId, ids);
  else openFlowPickForFinding(findingId);
}

let flowPickFindingId = null;
let flowPickFlows = [];
let flowPickSel = new Set();
// Flow-picker requests outlive the event that started them. These epochs keep
// a late response from a previous query or finding from repainting the active
// modal (especially across close/reopen).
let flowPickEpoch=0;
let flowPickSearchEpoch=0;

function flowPickModalOpen() {
  return $('#findFlowPickModal')?.style.display!=='none';
}

function flowPickSearchCurrent(q) {
  return flowPickModalOpen() && ($('#ffpSearch')?.value || '').trim() === String(q || '').trim();
}

function flowPickIdQuery(q) {
  const s = (q || '').trim();
  if (/^#\d+$/.test(s) || /^id:\d+$/i.test(s) || /^\d+$/.test(s)) return s;
  return '';
}

function flowPickFilter(q) {
  const s = (q || '').trim().toLowerCase();
  if (!s) return flowPickFlows;
  const idQ = flowPickIdQuery(q);
  if (idQ) {
    const raw = idQ.replace(/^#/i, '').replace(/^id:/i, '');
    const want = Number(raw);
    return flowPickFlows.filter(f => f.id === want);
  }
  return flowPickFlows.filter(f => `${f.method} ${f.host}${f.path} #${f.id}`.toLowerCase().includes(s));
}

let flowPickSearchTimer = null;

async function flowPickSearch(q) {
  const searchEpoch=++flowPickSearchEpoch;
  const ownerEpoch=flowPickEpoch;
  const ownerFindingId=flowPickFindingId;
  const idTerm = flowPickIdQuery(q);
  if (idTerm) {
    try {
      const d = await api('/api/flows?search=' + encodeURIComponent(idTerm) + '&searchScope=id&limit=20');
      if(searchEpoch!==flowPickSearchEpoch)return;
      if(ownerEpoch!==flowPickEpoch||ownerFindingId!==flowPickFindingId)return;
      if(!flowPickSearchCurrent(q))return;
      const extra = d.flows || [];
      const seen = new Set(flowPickFlows.map(f => f.id));
      for (const f of extra) {
        if (!seen.has(f.id)) { flowPickFlows.push(f); seen.add(f.id); }
      }
    } catch (e) {
      if(searchEpoch!==flowPickSearchEpoch)return;
      if(ownerEpoch!==flowPickEpoch||ownerFindingId!==flowPickFindingId)return;
      if(!flowPickSearchCurrent(q))return;
      toast(e.message);
      return;
    }
  }
  if(searchEpoch!==flowPickSearchEpoch)return;
  if(ownerEpoch!==flowPickEpoch||ownerFindingId!==flowPickFindingId)return;
  if(!flowPickSearchCurrent(q))return;
  renderFlowPickList(q);
}

function renderFlowPickList(filter = '') {
  const list = $('#findFlowPickList');
  const cnt = $('#ffpCount');
  const attach = $('#ffpAttach');
  if (!list) return;
  const rows = flowPickFilter(filter);
  if (!rows.length) {
    list.innerHTML = '<div class="hint" style="padding:12px">No flows match — capture traffic through the proxy first.</div>';
  } else {
    list.innerHTML = rows.map(f => {
      const on = flowPickSel.has(f.id);
      return `<label class="find-flow-pick${on ? ' on' : ''}" data-id="${f.id}">
        <input type="checkbox"${on ? ' checked' : ''} aria-label="Select flow #${f.id}">
        <span class="m" style="color:${methodColor(f.method)}">${esc(f.method)}</span>
        <span class="p">${esc(f.host)}${esc(f.path || '/')}</span>
        <span class="sts" style="color:${statusColor(f.status)}">${f.status || '—'}</span>
        <span class="hint">#${f.id}</span>
      </label>`;
    }).join('');
    list.querySelectorAll('.find-flow-pick').forEach(el => {
      const id = Number(el.dataset.id);
      const toggle = () => {
        flowPickSel.has(id) ? flowPickSel.delete(id) : flowPickSel.add(id);
        renderFlowPickList($('#ffpSearch')?.value || '');
      };
      el.querySelector('input').onchange = () => toggle();
    });
  }
  const n = flowPickSel.size;
  if (cnt) cnt.textContent = n + ' selected';
  if (attach) attach.disabled = !n;
}

async function openFlowPickForFinding(findingId) {
  const epoch=++flowPickEpoch;
  const initialSearchEpoch=++flowPickSearchEpoch;
  flowPickFindingId = findingId;
  flowPickSel = new Set();
  flowPickFlows = [];
  const list = $('#findFlowPickList');
  if (list) list.innerHTML = '<div class="hint" style="padding:12px">Loading…</div>';
  const count=$('#ffpCount');if(count)count.textContent='0 selected';
  const attach=$('#ffpAttach');if(attach)attach.disabled=true;
  const search = $('#ffpSearch');
  if (search) search.value = '';
  openModal($('#findFlowPickModal'));
  try {
    const d = await api('/api/flows?limit=200');
    if(epoch!==flowPickEpoch||flowPickFindingId!==findingId)return;
    if(!flowPickModalOpen())return;
    const incoming=d.flows||[];
    if(initialSearchEpoch===flowPickSearchEpoch)flowPickFlows=incoming;
    else{
      const seen=new Set(flowPickFlows.map(f=>f.id));
      for(const flow of incoming){if(!seen.has(flow.id)){flowPickFlows.push(flow);seen.add(flow.id);}}
    }
    renderFlowPickList(search?.value||'');
  } catch (e) {
    if(epoch!==flowPickEpoch||flowPickFindingId!==findingId)return;
    if(!flowPickModalOpen())return;
    toast(e.message);
    if (list) list.innerHTML = '';
  }
}

/* ---- create finding ---- */
let findingCreateEpoch=0;
let findingCreateBusy=false;
let findingCreateFocus=null;
function setFindingCreateStatus(message,kind='status'){
  const status=$('#fcStatus');if(!status)return;
  status.textContent=message||'';
  status.setAttribute('role',kind==='error'?'alert':'status');
  status.setAttribute('aria-live',kind==='error'?'assertive':'polite');
}
function setFindingCreateBusy(busy){
  const modal=$('#findCreateModal');
  if(busy&&!findingCreateBusy){
    const active=document.activeElement;
    findingCreateFocus=modal?.contains(active)?active:null;
  }
  const restore=!busy&&findingCreateBusy?findingCreateFocus:null;
  if(!busy)findingCreateFocus=null;
  findingCreateBusy=!!busy;
  ['#fcTitle','#fcSeverity','#fcSave','#fcClose'].forEach(sel=>{const control=$(sel);if(control)control.disabled=findingCreateBusy;});
  const button=$('#fcSave');if(!button)return;
  button.setAttribute('aria-busy',findingCreateBusy?'true':'false');
  button.textContent=findingCreateBusy?'Creating…':'Create finding';
  const status=$('#fcStatus');
  if(status)status.setAttribute('aria-busy',findingCreateBusy?'true':'false');
  if(findingCreateBusy&&findingCreateFocus&&status){
    setFindingCreateStatus('Creating finding…');
    status.focus({preventScroll:true});
  }else if(restore&&modal?.style.display==='flex'&&restore.isConnected&&!restore.disabled){
    restore.focus({preventScroll:true});
  }
}
function closeFindingCreate(){
  if(findingCreateBusy){$('#fcStatus')?.focus({preventScroll:true});return;}
  findingCreateEpoch++;
  closeModal($('#findCreateModal'));
}
function resetFindingCreateButton(){
  setFindingCreateBusy(false);
}
function openFindCreate(event) {
  findingCreateEpoch++;
  const trigger=event?.currentTarget;
  if(trigger?.focus)trigger.focus({preventScroll:true});
  $('#fcTitle').value = '';
  $('#fcSeverity').value = 'Medium';
  resetFindingCreateButton();
  setFindingCreateStatus('');
  openModal($('#findCreateModal'),{initialFocus:$('#fcTitle'),onEscape:closeFindingCreate,onDismiss:closeFindingCreate});
}
$('#findNew') && ($('#findNew').onclick = openFindCreate);
$('#findEmptyNew') && ($('#findEmptyNew').onclick = openFindCreate);
$('#fcClose') && ($('#fcClose').onclick = closeFindingCreate);
$('#fcSave') && ($('#fcSave').onclick = async () => {
  const button = $('#fcSave');
  const title = ($('#fcTitle')?.value || '').trim();
  if (!title) {
    toast('finding title is required', 'error');
    setFindingCreateStatus('Finding title is required.','error');
    $('#fcTitle')?.focus();
    return;
  }
  if (findingCreateBusy) return;
  const createEpoch=++findingCreateEpoch;
  const modal=$('#findCreateModal');
  setFindingCreateBusy(true);
  try {
    const created = await api('/api/findings', {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({
        title,
        severity: $('#fcSeverity')?.value || 'Medium',
        source: 'human',
      }),
    });
    if(createEpoch!==findingCreateEpoch||modal?.style.display!=='flex')return;
    closeModal(modal);
    selFinding = Number(created.id) || null;
    findEditMode = true;
    await loadFindings();
    if(createEpoch!==findingCreateEpoch)return;
    toast('finding created');
  } catch (err) {
    if(createEpoch!==findingCreateEpoch||modal?.style.display!=='flex')return;
    setFindingCreateStatus(err.message || 'Could not create finding.','error');
    toast(err.message || 'could not create finding', 'error');
  } finally {
    if (createEpoch===findingCreateEpoch&&button.isConnected) {
      setFindingCreateBusy(false);
    }
  }
});
$('#findGuide') && ($('#findGuide').onclick = () => openModal($('#findGuideModal')));
$('#findGuideClose') && ($('#findGuideClose').onclick = () => closeModal($('#findGuideModal')));

async function exportFindingsReport() {
  const fmt = $('#findExportFmt')?.value || 'md';
  const statuses = $('#findExportStatuses')?.value || 'open,verified,fixed';
  const group = $('#findExportGroupByTag')?.checked ? '&groupBy=tag' : '';
  const button = $('#findExport');
  if (button?.disabled) return;
  if (button) { button.disabled = true; button.setAttribute('aria-busy','true'); button.textContent = 'Exporting…'; }
  try {
    const res = await fetch('/api/findings/report?format=' + encodeURIComponent(fmt) + '&statuses=' + encodeURIComponent(statuses) + group, { credentials: 'same-origin' });
    if (!res.ok) throw new Error('export failed (' + res.status + ')');
    const blob = await res.blob();
    await saveFile(blob, 'interseptor-findings.' + fmt, blob.type);
    toast('findings exported');
  } catch (err) { if (err?.name !== 'AbortError') toast(err.message, 'error'); }
  finally { if (button) { button.disabled = false; button.removeAttribute('aria-busy'); button.textContent = '⤓ Export'; } }
}
$('#findExport') && ($('#findExport').onclick = exportFindingsReport);

export function flowFindings(flowId) {
  return findings.filter(f => (f.blocks || []).some(b => b.type === 'flow' && b.flowId === flowId) || (f.flows || []).some(x => x.flowId === flowId)).map(f => ({ id: f.id, title: f.title, severity: f.severity }));
}
export function openFinding(id) {
  selFinding = id;
  document.querySelector('.tab[data-tab="findings"]')?.click();
  loadFindings().then(() => {
    $('#findList')?.classList.remove('find-mobile-list-visible');
    $('#findDetail')?.classList.add('find-mobile-detail-visible');
  });
}

// Open a PoC flow from the current finding and keep a shareable compound hash.
function openFindingFlow(flowId) {
  if (!flowId) return;
  if (selFinding) {
    try { history.replaceState(null, '', `#finding-${selFinding}/flow-${flowId}`); } catch { /* ignore */ }
  } else {
    try { history.replaceState(null, '', `#flow-${flowId}`); } catch { /* ignore */ }
  }
  flowPopup(flowId);
}

// Deep-link: #finding-<id>, #finding-<id>/flow-<id>, #flow-<id>, #flow/<id>
export function handleAppHash() {
  const h = location.hash || '';
  let m = h.match(/^#finding-(\d+)(?:\/flow-(\d+))?$/i);
  if (m) {
    const fid = Number(m[1]);
    const flowId = m[2] ? Number(m[2]) : 0;
    if (!fid) return;
    selFinding = fid;
    document.querySelector('.tab[data-tab="findings"]')?.click();
    loadFindings().then(() => {
      $('#findList')?.classList.remove('find-mobile-list-visible');
      $('#findDetail')?.classList.add('find-mobile-detail-visible');
      if (flowId) flowPopup(flowId);
    });
    return;
  }
  m = h.match(/^#flow-(\d+)$/i) || h.match(/^#flow\/(\d+)$/i);
  if (m) {
    const id = Number(m[1]);
    if (id) flowPopup(id);
  }
}
window.addEventListener('hashchange', handleAppHash);
export function addFlowToFinding(flowId) {
  if (flowId) pickFindingForFlows([flowId]);
}

/* ---- "Add to finding" from the History selection bar ---- */
export function pickFindingForSelection() {
  pickFindingForFlows(state.selected ? [...state.selected] : []);
}
function pickFindingForFlows(ids) {
  if (!ids.length) { toast('select flows first'); return; }
  const list = $('#findPickList'); if (!list) return;
  const pocCount = findingPocCount;
  const rows = findings.map(f => `<button class="btn find-pick" data-id="${f.id}" style="width:100%;text-align:left;margin-bottom:4px">
    <span class="sev" style="color:${sevColor(f.severity)}">${esc(f.severity)}</span> ${esc(f.title)}
    <span class="hint" style="float:right">${esc(statusLabel(f.status))}${pocCount(f) ? ' · ' + pocCount(f) + ' PoC' : ''}</span></button>`).join('');
  list.innerHTML = `<div class="hint" style="margin-bottom:8px">Attach ${ids.length} selected flow${ids.length === 1 ? '' : 's'} to:</div>${rows || '<div class="hint">No findings yet.</div>'}
    <button class="btn accent find-pick-new" style="width:100%;margin-top:6px">＋ New finding from these flows</button>`;
  openModal($('#findPickModal'));
  list.querySelectorAll('.find-pick').forEach(b => b.onclick = async () => {
    closeModal($('#findPickModal'));
    await attachFlowsToFinding(Number(b.dataset.id),ids);
  });
  list.querySelector('.find-pick-new').onclick = async () => {
    closeModal($('#findPickModal'));
    const title = await uiPrompt({ title: 'Name the new finding', placeholder: 'e.g. IDOR on /api/user/{id}' });
    if (title == null) return;
    const f = await api('/api/findings', { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ title, severity: 'Medium', source: 'human', flowIds: ids }) }).catch(e => { toast(e.message); return null; });
    if (f) {
      const warnings=Array.isArray(f.warnings)?f.warnings:[];
      selFinding = f.id; document.querySelector('.tab[data-tab="findings"]')?.click(); loadFindings();
      toast(warnings.length?'finding created · '+warnings.length+' PoC attachment warning'+(warnings.length===1?'':'s')+': '+warnings.join(' · '):'finding created',warnings.length?'warn':'success');
    }
  };
}
$('#fpClose') && ($('#fpClose').onclick = () => closeModal($('#findPickModal')));
$('#ffpClose') && ($('#ffpClose').onclick = () => {
  flowPickEpoch++;
  flowPickSearchEpoch++;
  closeModal($('#findFlowPickModal'));
});
$('#ffpSearch') && ($('#ffpSearch').oninput = e => {
  clearTimeout(flowPickSearchTimer);
  const v = e.target.value;
  flowPickSearchTimer = setTimeout(() => flowPickSearch(v), 200);
});
$('#ffpAttach') && ($('#ffpAttach').onclick = async () => {
  const ids = [...flowPickSel];
  const fid = flowPickFindingId;
  closeModal($('#findFlowPickModal'));
  if (!fid || !ids.length) return;
  await attachFlowsToFinding(fid, ids);
});
$('#selAddFinding') && ($('#selAddFinding').onclick = pickFindingForSelection);
