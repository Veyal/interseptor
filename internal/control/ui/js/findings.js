import { $, esc, escAttr, state, toast, api, openModal, closeModal, renderMD, wireRowKey, saveFile, uiPrompt, uiConfirm, methodColor, statusColor, renderLoadError } from './core.js';
import { flowPopup } from './flowmodal.js';
import { sendToRepeater } from './tools.js';

// Findings tab: the human reviews/curates the project's vulnerability findings.
// Each finding has a narrative body — an ordered sequence of text blocks (markdown)
// and flow-reference blocks (PoC request/response) interleaved freely, like a report.

const STATUSES = ['open', 'needs_verification', 'verified', 'false_positive', 'wont_fix', 'fixed'];
let findings = [], selFinding = null, findTagFilter = '', findTagCounts = [];
let findingsLoadStateEl = null;
let findingsLoadEpoch=0;

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
  const ready = f.ready
    ? '<span class="find-ready">Ready</span>'
    : '<span class="find-draft">Draft</span>';
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
    <p class="state-empty-hint state-empty-cmdk find-closeout-hint"><span class="find-closeout-lead">Ready to wrap up?</span><span>Use <b>Export report</b> when your findings are ready.</span><a href="https://github.com/Veyal/interseptor/blob/main/docs/engagement-closeout.md" target="_blank" rel="noopener">Open engagement close-out checklist <span aria-hidden="true">↗</span></a></p>
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
    !!(findEditMode && detail && active && detail.contains(active) && active.matches('input,textarea,select,[contenteditable="true"]'));
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
  box.innerHTML = list.map(f => `<div class="find-row${f.id === selFinding ? ' sel' : ''}${!(f.ready) ? ' find-row-empty' : ''}${f.status === 'needs_verification' ? ' find-row-needs-verif' : ''}" data-id="${f.id}">
    <span class="find-id">#${f.id}</span>
    <span class="sev" style="color:${sevColor(f.severity)}">${esc(f.severity)}</span>
    <span class="find-title">${esc(f.title)}</span>
    <span class="find-meta">${findingListMeta(f)}</span>
  </div>`).join('');
  box.querySelectorAll('.find-row').forEach(el => { el.onclick = () => { const id=Number(el.dataset.id); if(id!==selFinding)findEditMode=false; selFinding = id; renderFindings(); renderFindingDetail(); }; wireRowKey(el); });
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
  const upBtn = isFirst ? '' : `<button class="btn xs" data-mv="${i}" data-dir="-1" title="Move up" style="padding:1px 5px;font-size:var(--fs-xs)">↑</button>`;
  const dnBtn = isLast ? '' : `<button class="btn xs" data-mv="${i}" data-dir="1" title="Move down" style="padding:1px 5px;font-size:var(--fs-xs)">↓</button>`;
  const delBtn = `<button class="btn xs danger" data-del="${i}" title="Remove" style="padding:1px 5px;font-size:var(--fs-xs)">✕</button>`;
  const controls = `<div class="find-block-controls">${upBtn}${dnBtn}${delBtn}</div>`;

  if (b.type === 'text') {
    const hasMd = !!(b.md && b.md.trim());
    return `<div class="find-block find-doc-text${hasMd ? '' : ' find-doc-text-empty'}" data-i="${i}">
      ${controls}
      <div class="find-text-view md"${hasMd ? '' : ' style="display:none"'}>${hasMd ? renderMD(b.md) : ''}</div>
      <textarea class="find-text-edit block-text" data-i="${i}" rows="1" spellcheck="true" aria-label="Finding evidence step ${i+1}"
        ${hasMd ? 'style="display:none"' : ''}
        placeholder="Describe the vulnerability, steps to reproduce, and what you observed…">${esc(b.md || '')}</textarea>
    </div>`;
  }

  if (b.type === 'image') {
    if (b.missing) {
      return `<div class="find-block find-doc-image find-block-missing" data-i="${i}">
        ${controls}
        <blockquote class="find-poc-callout find-poc-missing">
          <div><svg class="icon" aria-hidden="true" focusable="false"><use href="#i-warning"/></svg> Screenshot — evidence blob missing</div>
          <span class="hint">${esc(b.hash || '')}</span>
        </blockquote>
        <input class="find-poc-note-input block-caption" data-i="${i}" aria-label="Screenshot caption" value="${escAttr(b.caption || '')}" placeholder="Caption (optional)">
      </div>`;
    }
    const src = b.url || ('/api/findings/images/' + (b.hash || ''));
    return `<div class="find-block find-doc-image" data-i="${i}">
      ${controls}
      <figure class="find-doc-figure">
        <img class="md-img find-doc-img" src="${escAttr(src)}" alt="${escAttr(b.caption || 'screenshot')}" title="Click to enlarge">
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
       <blockquote class="find-poc-callout">${reqLine ? `<div class="find-poc-req">${reqLine}</div>` : ''}</blockquote>
       <div class="find-evidence-actions">
         <button type="button" class="btn xs find-open-flow" data-flow="${b.flowId}">Inspect request</button>
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
    container.innerHTML = '<div class="find-doc-empty">No PoC yet — add step notes, attach flows from History, or upload screenshots. Label Before → Action → After.</div>';
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
    ta.addEventListener('blur', () => finishTextEdit(block, ta, fid));
  });

  // Flow note / image caption: save on blur.
  container.querySelectorAll('.block-note').forEach(inp => {
    inp.addEventListener('blur', () => {
      const i = Number(inp.dataset.i);
      if (bodyBlocks[i]) { bodyBlocks[i].note = inp.value; scheduleSave(fid); }
    });
    inp.addEventListener('click', e => e.stopPropagation());
  });
  container.querySelectorAll('.block-caption').forEach(inp => {
    inp.addEventListener('blur', () => {
      const i = Number(inp.dataset.i);
      if (bodyBlocks[i]) { bodyBlocks[i].caption = inp.value; scheduleSave(fid); }
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

  // Flow click → open flow modal. Missing (purged) flow blocks aren't clickable.
  container.querySelectorAll('.find-doc-flow:not(.find-block-missing) .find-poc-callout').forEach(el => {
     el.onclick = ev => {
       if (ev.target.closest('[data-del],[data-mv],.block-note,.find-poc-note-input,.find-send-repeater')) return;
       const block = el.closest('.find-doc-flow');
       if (block) openFindingFlow(Number(block.dataset.flow));
     };
   });
  container.querySelectorAll('.find-open-flow').forEach(btn => {
    btn.onclick = event => {
      event.stopPropagation();
      const id = Number(btn.dataset.flow);
      if (id) openFindingFlow(id);
    };
  });
  wireSendToRepeaterButtons(container);
}

// ---- body editor ---------------------------------------------------------

function renderFindBody(fid) {
  const docEl = $('#findBody');
  if (docEl) renderBodyEditor(docEl, fid);
}

function scheduleSave(fid) {
  const previous = bodySaveTimers.get(fid);
  if (previous) clearTimeout(previous);
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
    return r;
  });
  bodySaveTimers.set(fid, setTimeout(() => {
    bodySaveTimers.delete(fid);
    flushBodySave(fid, snap);
  }, 700));
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
  // Strip enriched metadata before sending; store only type/md/flowId/note.
  try {
    await enqueueFindingPatch(fid, { body: JSON.stringify(snapshot) });
  } catch (e) { toast('body save: ' + e.message); }
  finally { bodySavesInFlight--; setTimeout(refreshDeferredFindingDetail, 0); }
}

// ---- detail pane ---------------------------------------------------------

function missingLabel(k) {
  return ({ impact: 'Impact', why: 'Why', target: 'Target', poc: 'PoC evidence', poc_before_after: 'Before+After flows (High/Critical)' }[k] || k);
}

async function patchFinding(id, fields) {
  findingWritesInFlight++;
  try {
    return await enqueueFindingPatch(id, fields);
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
  const gaps = f.missing || [];
  const completeBar = f.ready
    ? `<div class="find-complete find-complete-ready" role="status"><span class="find-ready">Ready</span> — Impact, Why, Target, and PoC are filled.</div>`
    : `<div class="find-complete find-complete-draft" role="status"><span class="find-draft">Draft</span> — still need: ${gaps.map(g => `<a href="#find-sec-${escAttr(g === 'poc_before_after' ? 'poc' : g)}" class="find-gap-link">${esc(missingLabel(g))}</a>`).join(', ') || 'content'}</div>`;
  const verifBanner = (f.status === 'needs_verification' || f.verificationInstructions)
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
    return `<div class="find-machine-proof" role="status">
      <div class="find-machine-title"><svg class="icon" aria-hidden="true" focusable="false"><use href="#i-gear"/></svg> External-agent verification · confidence <b>${esc(String(v.confidence ?? 0))}%</b></div>
      <div class="hint">Class <b>${esc(v.vulnClass || '—')}</b>${v.runId ? ' · run #' + esc(String(v.runId)) : ''}${v.reproCount ? ' · repro ×' + esc(String(v.reproCount)) : ''}${v.oobToken ? ' · OOB' : ''}</div>
      <div class="find-gate-list">${gateRows}</div>
      ${(v.baselineFlow || v.payloadFlow) ? `<div class="hint">PoC flows: ${[v.baselineFlow && ('#' + v.baselineFlow), v.payloadFlow && ('#' + v.payloadFlow)].filter(Boolean).join(' · ')}</div>` : ''}
    </div>`;
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
        <span class="find-id-badge">#${f.id}</span>
        ${edit
          ? `<select id="findSeverity" class="btn find-sev-select" aria-label="Severity" style="color:${sevColor(f.severity)}">${sevOpts}</select>
             <h2 class="find-title-text" id="findTitleText">${esc(f.title)}</h2>
             <button class="btn xs" id="findRename" title="Rename finding"><svg class="icon" aria-hidden="true" focusable="false"><use href="#i-pencil"/></svg></button>`
          : `<span class="sev" style="color:${sevColor(f.severity)}">${esc(f.severity)}</span>
             <h2 class="find-title-text" id="findTitleText">${esc(f.title)}</h2>
             <span class="sev find-status-badge" style="color:${statusBadgeColor(f.status)}">${esc(statusLabel(f.status))}</span>`}
        <div class="spacer"></div>
        <button class="btn ${edit ? '' : 'btn-primary'}" id="findToggleEdit">${edit ? 'Done' : 'Edit'}</button>
      </div>
      ${edit ? `<div class="find-meta-bar">
        <select id="findStatus" class="btn" style="background:var(--bg3)" aria-label="Finding status">${statusSel}</select>
        <select id="findEnv" class="btn" style="background:var(--bg3)" aria-label="Environment">${envOpts}</select>
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

    <section class="find-sec find-sec-impact-sticky" id="find-sec-impact">
      <h3>Impact</h3>
      ${edit
        ? `<textarea id="findImpact" class="find-field-text" rows="2" aria-label="Finding impact" placeholder="What an attacker gains…">${esc(f.impact || '')}</textarea>`
        : impactRead}
    </section>
    ${metaStrip}

    <section class="find-sec" id="find-sec-poc">
      <h3>PoC / Evidence</h3>
      ${edit ? `<p class="hint find-poc-hint">Ordered exploit chain — Before → Action → After.</p>` : ''}
      <div class="find-doc" id="findBody"></div>
      <div class="find-doc-actions" id="findDocActions" ${edit ? '' : 'hidden'}>
        <button class="btn" id="findAddText">＋ Step note</button>
        <button class="btn" id="findAddFlow">＋ PoC flow<span id="findPocReady" class="hint"></span></button>
        <button class="btn" id="findAddImage">＋ Screenshot</button>
        <input type="file" id="findImageFile" accept="image/png,image/jpeg,image/gif,image/webp,image/bmp,image/avif" hidden>
      </div>
    </section>

    <details class="find-more"${f.fix ? ' open' : ''}>
      <summary>Remediation (optional)</summary>
      ${edit
        ? `<textarea id="findFix" class="find-field-text" rows="2" aria-label="Finding remediation">${esc(f.fix || '')}</textarea>`
        : `<div class="hint" style="padding:8px 0">${f.fix ? esc(f.fix) : '—'}</div>`}
    </details>
  </article>`;
  box.onfocusout = () => setTimeout(refreshDeferredFindingDetail, 0);

  bodyFindingId = f.id;
  bodyBlocks = (f.blocks || []).map(b => ({ ...b }));
  if (edit) renderFindBody(f.id);
  else renderFindReportBody(f.id);

  const te = $('#findToggleEdit');
  if (te) te.onclick = () => { findEditMode = !findEditMode; renderFindingDetail(); };

  const blurPatch = (id, key, getVal) => {
    const el = $(id); if (!el) return;
    el.addEventListener('blur', async () => {
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
    });
  };
  if (edit) {
    blurPatch('#findImpact', 'impact', el => el.value);
    blurPatch('#findWhy', 'why', el => el.value);
    blurPatch('#findTarget', 'target', el => el.value);
    blurPatch('#findCvss', 'cvss', el => el.value);
    blurPatch('#findCwe', 'cwe', el => el.value);
    blurPatch('#findFix', 'fix', el => el.value);
  }
  if (edit) blurPatch('#findVerifInstr', 'verificationInstructions', el => el.value);

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
      bodyBlocks.push({ type: 'text', md: '' });
      renderFindBody(f.id);
      const tas = document.querySelectorAll('#findBody .block-text');
      if (tas.length) tas[tas.length - 1].focus();
    };
    $('#findAddFlow').onclick = () => addPoCFlowsToFinding(f.id);
    $('#findAddImage').onclick = () => $('#findImageFile')?.click();
    $('#findImageFile').onchange = async e => {
      const file = e.target.files?.[0];
      e.target.value = '';
      if (!file) return;
      try {
        const dataUrl = await new Promise((resolve, reject) => {
          const r = new FileReader();
          r.onload = () => resolve(r.result);
          r.onerror = () => reject(new Error('failed to read image'));
          r.readAsDataURL(file);
        });
        await api('/api/findings/' + f.id + '/images', {
          method: 'POST', headers: { 'content-type': 'application/json' },
          body: JSON.stringify({ data: dataUrl, mime: file.type, caption: file.name }),
        });
        toast('screenshot attached');
        await loadFindings();
      } catch (err) { toast(err.message); }
    };
    updateFindPocBtn();
  }
}

function renderFindReportBody(fid) {
  const container = $('#findBody');
  if (!container) return;
  if (!bodyBlocks.length) {
    container.innerHTML = '<div class="find-doc-empty">No PoC yet — switch to Edit to add steps and flows.</div>';
    return;
  }
  let step = 0;
  container.innerHTML = bodyBlocks.map((b) => {
    if (b.type === 'text') {
      const md = (b.md || '').trim();
      if (!md) return '';
      step++;
      return `<div class="find-report-step"><div class="find-report-stepn">${step}</div><div class="find-report-stepbody">${renderMD(md)}</div></div>`;
    }
    if (b.type === 'image') {
      step++;
      if (b.missing) {
        return `<div class="find-report-step"><div class="find-report-stepn">${step}</div><div class="find-poc-missing"><svg class="icon" aria-hidden="true" focusable="false"><use href="#i-warning"/></svg> Screenshot missing</div></div>`;
      }
      const src = b.url || ('/api/findings/images/' + (b.hash || ''));
      return `<div class="find-report-step"><div class="find-report-stepn">${step}</div>
        <figure class="find-doc-figure"><img class="md-img find-doc-img" src="${escAttr(src)}" alt="${escAttr(b.caption || 'screenshot')}">
        ${b.caption ? `<figcaption class="hint">${esc(b.caption)}</figcaption>` : ''}</figure></div>`;
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
           <button type="button" class="find-report-flow" data-flow="${b.flowId}">
             ${b.note ? `<div class="find-report-note">${esc(b.note)}</div>` : ''}
             <div class="find-poc-req">${reqLine}</div>
           </button>
           <div class="find-evidence-actions">
             <button type="button" class="btn xs find-open-flow" data-flow="${b.flowId}">Inspect request</button>
             <button type="button" class="btn xs find-send-repeater" data-flow="${b.flowId}" aria-label="Send attached flow #${esc(String(b.flowId))} to Repeater">Send to Repeater →</button>
           </div>
         </div></div>`;
    }
    return '';
  }).join('');
  container.querySelectorAll('.find-report-flow').forEach(btn => {
     btn.onclick = () => { const id = Number(btn.dataset.flow); if (id) flowPopup(id); };
   });
  container.querySelectorAll('.find-open-flow').forEach(btn => {
    btn.onclick = () => { const id = Number(btn.dataset.flow); if (id) flowPopup(id); };
  });
  wireSendToRepeaterButtons(container);
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
  if (!ids.length) return;
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
      el.onclick = e => { if (e.target.tagName !== 'INPUT') toggle(); };
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
function openFindCreate(event) {
  const trigger=event?.currentTarget;
  if(trigger?.focus)trigger.focus({preventScroll:true});
  $('#fcTitle').value = '';
  $('#fcSeverity').value = 'Medium';
  openModal($('#findCreateModal'),{initialFocus:$('#fcTitle')});
}
$('#findNew') && ($('#findNew').onclick = openFindCreate);
$('#findEmptyNew') && ($('#findEmptyNew').onclick = openFindCreate);
$('#fcClose') && ($('#fcClose').onclick = () => closeModal($('#findCreateModal')));
$('#fcSave') && ($('#fcSave').onclick = async () => {
  const button = $('#fcSave');
  const title = ($('#fcTitle')?.value || '').trim();
  if (!title) {
    toast('finding title is required', 'error');
    $('#fcTitle')?.focus();
    return;
  }
  if (button.disabled) return;
  const label = button.textContent;
  button.disabled = true;
  button.setAttribute('aria-busy', 'true');
  button.textContent = 'Creating…';
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
    closeModal($('#findCreateModal'));
    selFinding = Number(created.id) || null;
    findEditMode = true;
    await loadFindings();
    toast('finding created');
  } catch (err) {
    toast(err.message || 'could not create finding', 'error');
  } finally {
    if (button.isConnected) {
      button.disabled = false;
      button.setAttribute('aria-busy', 'false');
      button.textContent = label;
    }
  }
});
$('#findGuide') && ($('#findGuide').onclick = () => openModal($('#findGuideModal')));
$('#findGuideClose') && ($('#findGuideClose').onclick = () => closeModal($('#findGuideModal')));

export function flowFindings(flowId) {
  return findings.filter(f => (f.blocks || []).some(b => b.type === 'flow' && b.flowId === flowId) || (f.flows || []).some(x => x.flowId === flowId)).map(f => ({ id: f.id, title: f.title, severity: f.severity }));
}
export function openFinding(id) {
  selFinding = id;
  document.querySelector('.tab[data-tab="findings"]')?.click();
  loadFindings();
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
function handleAppHash() {
  const h = location.hash || '';
  let m = h.match(/^#finding-(\d+)(?:\/flow-(\d+))?$/i);
  if (m) {
    const fid = Number(m[1]);
    const flowId = m[2] ? Number(m[2]) : 0;
    if (!fid) return;
    selFinding = fid;
    document.querySelector('.tab[data-tab="findings"]')?.click();
    loadFindings().then(() => { if (flowId) flowPopup(flowId); });
    return;
  }
  m = h.match(/^#flow-(\d+)$/i) || h.match(/^#flow\/(\d+)$/i);
  if (m) {
    const id = Number(m[1]);
    if (id) flowPopup(id);
  }
}
window.addEventListener('hashchange', handleAppHash);
// Run on module load so a direct URL like /#finding-3 or /#flow-6010 opens.
handleAppHash();
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
    if (f) { selFinding = f.id; document.querySelector('.tab[data-tab="findings"]')?.click(); loadFindings(); toast('finding created'); }
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
