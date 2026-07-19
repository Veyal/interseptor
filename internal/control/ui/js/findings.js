import { $, esc, escAttr, state, toast, api, openModal, closeModal, renderMD, wireRowKey, saveFile, uiPrompt, methodColor, statusColor } from './core.js';
import { openAi } from './ai.js';
import { flowPopup } from './flowmodal.js';

// Findings tab: the human reviews/curates the project's vulnerability findings.
// Each finding has a narrative body — an ordered sequence of text blocks (markdown)
// and flow-reference blocks (PoC request/response) interleaved freely, like a report.

const STATUSES = ['open', 'needs_verification', 'verified', 'false_positive', 'wont_fix', 'fixed'];
let findings = [], selFinding = null, findTagFilter = '', findTagCounts = [];

// Body editor state for the active finding.
let bodyBlocks = [];
let bodyFindingId = null;
let bodySaveTimer = null;
// True while a text-block textarea has focus. An SSE findings.update (e.g. a body
// save round-tripping, or the AI recording) would otherwise rebuild the detail
// pane mid-edit and discard the focused textarea + any unsaved keystrokes.
let bodyEditing = false;

const sevColor = s => ({ Critical: 'var(--red)', High: 'var(--red)', Medium: 'var(--amber)', Low: 'var(--blue)', Info: 'var(--fg3)' }[s] || 'var(--fg3)');
const statusLabel = s => ({ needs_verification: 'needs verification' }[s] || (s || '').replace(/_/g, ' '));

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
    ? '<span class="find-needs-verif" title="Needs human verification">⚠ needs verification</span>'
    : esc(statusLabel(f.status));
  const ready = f.ready
    ? '<span class="find-ready">Ready</span>'
    : '<span class="find-draft">Draft</span>';
  const parts = [ready, st];
  const tags = f.tags || [];
  if (tags.length) parts.push('<span class="find-tags-inline">' + tags.map(t => esc(t)).join(' · ') + '</span>');
  if (f.verification && f.verification.confidence != null) {
    parts.push('<span class="find-conf" title="Autopilot verifier confidence">⚙ ' + esc(String(f.verification.confidence)) + '%</span>');
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
  try {
    const q = findTagFilter ? '?tag=' + encodeURIComponent(findTagFilter) : '';
    // Always load the full set for the sidebar filter counts; filter client-side
    // so switching chips is instant. Server ?tag= still used by MCP/API.
    const [d, tags] = await Promise.all([
      api('/api/findings'),
      api('/api/findings/tags').catch(() => ({ tags: [] })),
    ]);
    findings = d.findings || [];
    findTagCounts = tags.tags || [];
    renderFindTagFilter();
    renderFindings();
    void q;
  } catch (e) { toast(e.message); }
}

function findingsEmptyHTML() {
  return `<div class="state-empty find-empty">
    <div class="state-empty-icon">🔎</div>
    <div class="state-empty-title">No findings yet</div>
    <p class="state-empty-hint">File a vulnerability with PoC evidence — manually, from Autopilot, or by asking AI to triage history.</p>
    <div class="find-empty-actions">
      <button type="button" class="btn btn-primary" id="findEmptyNew">＋ New finding</button>
      <button type="button" class="btn accent" id="findEmptyAskAi" data-ai-ui>✨ Ask AI for findings</button>
    </div>
    <p class="state-empty-hint state-empty-cmdk">Later: <b>Export report</b> · <a href="https://github.com/Veyal/interseptor/blob/main/docs/engagement-closeout.md" target="_blank" rel="noopener">engagement close-out checklist</a></p>
  </div>`;
}

function wireFindingsEmptyActions(root) {
  const box = root || $('#findList');
  if (!box) return;
  const neu = box.querySelector('#findEmptyNew');
  const ask = box.querySelector('#findEmptyAskAi');
  if (neu) neu.onclick = openFindCreate;
  if (ask) ask.onclick = openFindAskAi;
}

function setFindingsViewEmpty(empty) {
  const view = $('#scanFindingsView');
  if (view) view.classList.toggle('is-empty', !!empty);
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
    box.innerHTML = `<div class="state-empty find-empty"><div class="state-empty-icon">🏷️</div><div class="state-empty-title">No findings with tag “${esc(findTagFilter)}”</div><p class="state-empty-hint">Clear the tag filter or tag a finding in the detail pane.</p><div class="find-empty-actions"><button type="button" class="btn" id="findClearTagFilter">Show all findings</button></div></div>`;
    const clr = box.querySelector('#findClearTagFilter');
    if (clr) clr.onclick = () => { findTagFilter = ''; renderFindTagFilter(); renderFindings(); };
    selFinding = null; renderFindingDetail(); return;
  }
  if (!selFinding || !list.some(f => f.id === selFinding)) selFinding = list[0].id;
  box.innerHTML = list.map(f => `<div class="find-row${f.id === selFinding ? ' sel' : ''}${!(f.ready) ? ' find-row-empty' : ''}${f.status === 'needs_verification' ? ' find-row-needs-verif' : ''}" data-id="${f.id}">
    <span class="sev" style="color:${sevColor(f.severity)}">${esc(f.severity)}</span>
    <span class="find-title">${esc(f.title)}</span>
    <span class="find-meta">${findingListMeta(f)}</span>
  </div>`).join('');
  box.querySelectorAll('.find-row').forEach(el => { el.onclick = () => { selFinding = Number(el.dataset.id); renderFindings(); renderFindingDetail(); }; wireRowKey(el); });
  // Skip the detail rebuild while a text block is open for this same finding —
  // otherwise an SSE findings.update (e.g. a body save round-tripping) wipes the
  // focused textarea. An explicit row click still calls renderFindingDetail().
  if (!(bodyEditing && selFinding === bodyFindingId)) renderFindingDetail();
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
  const upBtn = isFirst ? '' : `<button class="btn xs" data-mv="${i}" data-dir="-1" title="Move up" style="padding:1px 5px;font-size:11px">↑</button>`;
  const dnBtn = isLast ? '' : `<button class="btn xs" data-mv="${i}" data-dir="1" title="Move down" style="padding:1px 5px;font-size:11px">↓</button>`;
  const delBtn = `<button class="btn xs danger" data-del="${i}" title="Remove" style="padding:1px 5px;font-size:11px">✕</button>`;
  const controls = `<div class="find-block-controls">${upBtn}${dnBtn}${delBtn}</div>`;

  if (b.type === 'text') {
    const hasMd = !!(b.md && b.md.trim());
    return `<div class="find-block find-doc-text${hasMd ? '' : ' find-doc-text-empty'}" data-i="${i}">
      ${controls}
      <div class="find-text-view md"${hasMd ? '' : ' style="display:none"'}>${hasMd ? renderMD(b.md) : ''}</div>
      <textarea class="find-text-edit block-text" data-i="${i}" rows="1" spellcheck="true"
        ${hasMd ? 'style="display:none"' : ''}
        placeholder="Describe the vulnerability, steps to reproduce, and what you observed…">${esc(b.md || '')}</textarea>
    </div>`;
  }

  if (b.type === 'image') {
    if (b.missing) {
      return `<div class="find-block find-doc-image find-block-missing" data-i="${i}">
        ${controls}
        <blockquote class="find-poc-callout find-poc-missing">
          <div>⚠ Screenshot — evidence blob missing</div>
          <span class="hint">${esc(b.hash || '')}</span>
        </blockquote>
        <input class="find-poc-note-input block-caption" data-i="${i}" value="${escAttr(b.caption || '')}" placeholder="Caption (optional)">
      </div>`;
    }
    const src = b.url || ('/api/findings/images/' + (b.hash || ''));
    return `<div class="find-block find-doc-image" data-i="${i}">
      ${controls}
      <figure class="find-doc-figure">
        <img class="md-img find-doc-img" src="${escAttr(src)}" alt="${escAttr(b.caption || 'screenshot')}" title="Click to enlarge">
        <input class="find-poc-note-input block-caption" data-i="${i}" value="${escAttr(b.caption || '')}"
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
        <div>⚠ PoC flow #${esc(String(b.flowId))} — evidence deleted from history</div>
        <span class="hint">Re-capture this endpoint to restore evidence</span>
      </blockquote>
      <input class="find-poc-note-input block-note" data-i="${i}" value="${escAttr(b.note || '')}" placeholder="Annotation (optional)">
    </div>`;
  }
  const reqLine = b.method
    ? `<span class="m">${esc(b.method)}</span> <span class="p">${esc(b.host || '')}${esc(b.path || '')}</span>${b.status ? `<span class="sts">→ ${b.status}</span>` : ''}`
    : `<span class="hint">flow #${esc(String(b.flowId))}</span>`;
  return `<div class="find-block find-doc-flow" data-i="${i}" data-flow="${b.flowId}">
    ${controls}
    <blockquote class="find-poc-callout">${reqLine ? `<div class="find-poc-req">${reqLine}</div>` : ''}</blockquote>
    <input class="find-poc-note-input block-note" data-i="${i}" value="${escAttr(b.note || '')}"
      placeholder="Annotation (optional)" onclick="event.stopPropagation()">
  </div>`;
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
      if (ev.target.closest('[data-del],[data-mv],.block-note,.find-poc-note-input')) return;
      const block = el.closest('.find-doc-flow');
      if (block) openFindingFlow(Number(block.dataset.flow));
    };
  });
}

// ---- body editor ---------------------------------------------------------

function renderFindBody(fid) {
  const docEl = $('#findBody');
  if (docEl) renderBodyEditor(docEl, fid);
}

function scheduleSave(fid) {
  clearTimeout(bodySaveTimer);
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
  bodySaveTimer = setTimeout(() => flushBodySave(fid, snap), 700);
}

async function flushBodySave(fid, snapshot) {
  if (!fid || !snapshot) return;
  // Strip enriched metadata before sending; store only type/md/flowId/note.
  try {
    await api('/api/findings/' + fid, {
      method: 'PATCH',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ body: JSON.stringify(snapshot) }),
    });
  } catch (e) { toast('body save: ' + e.message); }
}

// ---- detail pane ---------------------------------------------------------

function missingLabel(k) {
  return ({ impact: 'Impact', why: 'Why', target: 'Target', poc: 'PoC evidence', poc_before_after: 'Before+After flows (High/Critical)' }[k] || k);
}

async function patchFinding(id, fields) {
  await api('/api/findings/' + id, {
    method: 'PATCH', headers: { 'content-type': 'application/json' },
    body: JSON.stringify(fields),
  });
  Object.assign(findings.find(x => x.id === id) || {}, fields);
}

function renderFindingDetail() {
  const box = $('#findDetail'); if (!box) return;
  const f = findings.find(x => x.id === selFinding);
  if (!f) { box.innerHTML = '<div class="state-empty"><div class="state-empty-icon">🗂️</div><div class="state-empty-title">No finding selected</div><p class="state-empty-hint">Select a finding from the list to view its details.</p></div>'; return; }

  const statusSel = STATUSES.map(s => `<option value="${s}"${s === f.status ? ' selected' : ''}>${esc(statusLabel(s))}</option>`).join('');
  const sevOpts = ['Critical', 'High', 'Medium', 'Low', 'Info'].map(s => `<option value="${s}"${s === f.severity ? ' selected' : ''}>${s}</option>`).join('');
  const envOpts = ['', 'prod', 'staging', 'local'].map(e => `<option value="${e}"${(f.environment || '') === e ? ' selected' : ''}>${e || 'env…'}</option>`).join('');
  const missBanner = (() => {
    const missFlow = (f.blocks || []).filter(b => b.type === 'flow' && b.missing).length;
    const missImg = (f.blocks || []).filter(b => b.type === 'image' && b.missing).length;
    const parts = [];
    if (missFlow) parts.push(`${missFlow} PoC flow${missFlow === 1 ? '' : 's'} deleted from history`);
    if (missImg) parts.push(`${missImg} screenshot${missImg === 1 ? '' : 's'} missing`);
    return parts.length ? `<div class="find-missing-banner">⚠ ${parts.join(' · ')} — restore evidence if needed.</div>` : '';
  })();
  const gaps = f.missing || [];
  const completeBar = f.ready
    ? `<div class="find-complete find-complete-ready" role="status"><span class="find-ready">Ready</span> — Impact, Why, Target, and PoC are filled.</div>`
    : `<div class="find-complete find-complete-draft" role="status"><span class="find-draft">Draft</span> — still need: ${gaps.map(g => `<a href="#find-sec-${escAttr(g === 'poc_before_after' ? 'poc' : g)}" class="find-gap-link">${esc(missingLabel(g))}</a>`).join(', ') || 'content'}</div>`;
  const verifBanner = (f.status === 'needs_verification' || f.verificationInstructions)
    ? `<div class="find-verif-banner" role="status">
        <div class="find-verif-title">⚠ Needs human verification</div>
        <textarea id="findVerifInstr" class="find-verif-text" rows="3" placeholder="What should the human check? Exact steps…">${esc(f.verificationInstructions || '')}</textarea>
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
      <div class="find-machine-title">⚙ Autopilot trust · confidence <b>${esc(String(v.confidence ?? 0))}%</b></div>
      <div class="hint">Class <b>${esc(v.vulnClass || '—')}</b>${v.runId ? ' · run #' + esc(String(v.runId)) : ''}${v.reproCount ? ' · repro ×' + esc(String(v.reproCount)) : ''}${v.oobToken ? ' · OOB' : ''}</div>
      <div class="find-gate-list">${gateRows}</div>
      ${(v.baselineFlow || v.payloadFlow) ? `<div class="hint">PoC flows: ${[v.baselineFlow && ('#' + v.baselineFlow), v.payloadFlow && ('#' + v.payloadFlow)].filter(Boolean).join(' · ')}</div>` : ''}
    </div>`;
  })();

  box.innerHTML = `<article class="find-article">
    <header class="find-header">
      <div class="find-header-top">
        <select id="findSeverity" class="btn find-sev-select" aria-label="Severity" style="color:${sevColor(f.severity)}">${sevOpts}</select>
        <h2 class="find-title-text" id="findTitleText">${esc(f.title)}</h2>
        <button class="btn xs" id="findRename" title="Rename finding" aria-label="Rename finding">✎</button>
      </div>
      <div class="find-meta-bar">
        <select id="findStatus" class="btn" style="background:var(--bg3)" aria-label="Finding status">${statusSel}</select>
        <select id="findEnv" class="btn" style="background:var(--bg3)" aria-label="Environment">${envOpts}</select>
        <div class="find-cvss-field">
          <label for="findCvss">CVSS</label>
          <input id="findCvss" class="find-cvss-inline" type="text" value="${escAttr(f.cvss || '')}" placeholder="e.g. 7.5">
        </div>
        <div class="find-cvss-field">
          <label for="findCwe">CWE</label>
          <input id="findCwe" class="find-cvss-inline" type="text" value="${escAttr(f.cwe || '')}" placeholder="CWE-639">
        </div>
        <div class="spacer"></div>
        <button class="btn accent" id="findAskAi" data-ai-ui title="Ask AI about this finding">✨ Ask AI</button>
        <button class="btn danger xs" id="findDelete">Delete</button>
      </div>
      <div class="find-tags-bar">
        <span class="hint">Tags</span>
        <div class="find-tag-chips" id="findTagsChips">${(f.tags || []).map(t => `<span class="find-tag-chip">${esc(t)}</span>`).join('') || '<span class="hint">none — e.g. cms, api, out-of-scope</span>'}</div>
        <button class="btn xs" id="findEditTags" title="Edit report-scope tags">✎ Tags</button>
      </div>
    </header>
    ${completeBar}
    ${missBanner}
    ${verifBanner}
    ${machineProof}

    <section class="find-sec" id="find-sec-impact">
      <h3>Impact</h3>
      <textarea id="findImpact" class="find-field-text" rows="2" placeholder="What an attacker gains — business consequence, data exposed, privilege…">${esc(f.impact || '')}</textarea>
    </section>
    <section class="find-sec" id="find-sec-why">
      <h3>Why it's a finding</h3>
      <textarea id="findWhy" class="find-field-text" rows="2" placeholder="Which security property breaks (authz, authn, integrity, secrets handling…)?">${esc(f.why || '')}</textarea>
    </section>
    <section class="find-sec" id="find-sec-target">
      <h3>Affected target</h3>
      <input id="findTarget" class="btn btn-field find-target-input" type="text" value="${escAttr(f.target || '')}" placeholder="Host / app / endpoint (e.g. GET api.example.com/users/{id})">
    </section>

    <section class="find-sec" id="find-sec-poc">
      <h3>PoC / Evidence</h3>
      <p class="hint find-poc-hint">Ordered exploit chain — Before → Action → After. Attach flows and screenshots; step notes must say what changed and why it proves Impact.</p>
      <div class="find-doc" id="findBody"></div>
      <div class="find-doc-actions" id="findDocActions">
        <button class="btn" id="findAddText" title="Add a short step note">＋ Step note</button>
        <button class="btn" id="findAddFlow" title="Attach request/response flows as PoC evidence">＋ PoC flow<span id="findPocReady" class="hint"></span></button>
        <button class="btn" id="findAddImage" title="Attach a screenshot as evidence">＋ Screenshot</button>
        <input type="file" id="findImageFile" accept="image/png,image/jpeg,image/gif,image/webp,image/bmp,image/avif" hidden>
      </div>
    </section>

    <details class="find-more">
      <summary>Remediation (optional)</summary>
      <textarea id="findFix" class="find-field-text" rows="2" placeholder="How to fix…">${esc(f.fix || '')}</textarea>
    </details>
  </article>`;

  bodyFindingId = f.id;
  bodyBlocks = (f.blocks || []).map(b => ({ ...b }));
  renderFindBody(f.id);

  const blurPatch = (id, key, getVal) => {
    const el = $(id); if (!el) return;
    el.addEventListener('blur', async () => {
      const v = getVal(el);
      if (v === (f[key] || '')) return;
      try {
        await patchFinding(f.id, { [key]: v });
        f[key] = v;
        await loadFindings();
      } catch (err) { toast(err.message); }
    });
  };
  blurPatch('#findImpact', 'impact', el => el.value);
  blurPatch('#findWhy', 'why', el => el.value);
  blurPatch('#findTarget', 'target', el => el.value);
  blurPatch('#findCvss', 'cvss', el => el.value);
  blurPatch('#findCwe', 'cwe', el => el.value);
  blurPatch('#findFix', 'fix', el => el.value);
  blurPatch('#findVerifInstr', 'verificationInstructions', el => el.value);

  $('#findRename').onclick = async () => {
    const t = await uiPrompt({ title: 'Rename finding', value: f.title, placeholder: 'Finding title' });
    if (t == null || t === f.title) return;
    try { await patchFinding(f.id, { title: t }); f.title = t; const el = $('#findTitleText'); if (el) el.textContent = t; toast('finding renamed'); renderFindings(); }
    catch (err) { toast(err.message); }
  };
  $('#findStatus').onchange = async e => {
    try {
      await patchFinding(f.id, { status: e.target.value });
      f.status = e.target.value;
      toast('status: ' + statusLabel(f.status));
      await loadFindings();
    } catch (err) { toast(err.message); }
  };
  $('#findSeverity').onchange = async e => {
    try {
      await patchFinding(f.id, { severity: e.target.value });
      f.severity = e.target.value;
      await loadFindings();
    } catch (err) { toast(err.message); }
  };
  $('#findEnv').onchange = async e => {
    try {
      await patchFinding(f.id, { environment: e.target.value });
      f.environment = e.target.value;
      await loadFindings();
    } catch (err) { toast(err.message); }
  };
  $('#findDelete').onclick = async () => {
    try { await api('/api/findings/' + f.id, { method: 'DELETE' }); selFinding = null; toast('finding deleted'); loadFindings(); }
    catch (err) { toast(err.message); }
  };
  $('#findEditTags') && ($('#findEditTags').onclick = async () => {
    const cur = (f.tags || []).join(' ');
    const v = await uiPrompt({ title: 'Finding tags (report scope)', value: cur, placeholder: 'cms website app api out-of-scope' });
    if (v == null) return;
    const tags = parseFindTags(v);
    try {
      await patchFinding(f.id, { tags });
      f.tags = tags;
      toast(tags.length ? 'tags: ' + tags.join(', ') : 'tags cleared');
      await loadFindings();
    } catch (err) { toast(err.message); }
  });
  $('#findAskAi') && ($('#findAskAi').onclick = () => openAi({ findingId: f.id }));
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
  const idTerm = flowPickIdQuery(q);
  if (idTerm) {
    try {
      const d = await api('/api/flows?search=' + encodeURIComponent(idTerm) + '&searchScope=id&limit=20');
      const extra = d.flows || [];
      const seen = new Set(flowPickFlows.map(f => f.id));
      for (const f of extra) {
        if (!seen.has(f.id)) { flowPickFlows.push(f); seen.add(f.id); }
      }
    } catch (e) { toast(e.message); }
  }
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
  flowPickFindingId = findingId;
  flowPickSel = new Set();
  flowPickFlows = [];
  const list = $('#findFlowPickList');
  if (list) list.innerHTML = '<div class="hint" style="padding:12px">Loading…</div>';
  const search = $('#ffpSearch');
  if (search) search.value = '';
  openModal($('#findFlowPickModal'));
  try {
    const d = await api('/api/flows?limit=200');
    flowPickFlows = d.flows || [];
    renderFlowPickList();
  } catch (e) { toast(e.message); if (list) list.innerHTML = ''; }
}

/* ---- create finding ---- */
function openFindCreate() {
  $('#fcTitle').value = '';
  $('#fcSeverity').value = 'Medium';
  openModal($('#findCreateModal'));
  $('#fcTitle').focus();
}
$('#findNew') && ($('#findNew').onclick = openFindCreate);
$('#findEmptyNew') && ($('#findEmptyNew').onclick = openFindCreate);
$('#fcClose') && ($('#fcClose').onclick = () => closeModal($('#findCreateModal')));

/* ---- Ask AI for findings (triage) ---- */
let triageAbort = null;
let triageSeq = 0;
function setTriageStatus(s) { const el = $('#ftStatus'); if (el) el.textContent = s || ''; }
function closeTriage() { if (triageAbort) triageAbort.abort(); closeModal($('#findTriageModal')); }
function openFindAskAi() {
  if (state.aiDisabled) { toast('AI features are disabled — enable in Settings → AI assist'); return; }
  const out = $('#ftOut'); if (out) out.innerHTML = '<div class="hint">Ready — click Run triage.</div>';
  setTriageStatus('');
  const stop = $('#ftStop'); if (stop) stop.style.display = 'none';
  openModal($('#findTriageModal'), { onEscape: closeTriage, onDismiss: closeTriage });
  setTimeout(() => { const s = $('#ftSteer'); if (s) s.focus(); }, 30);
}
$('#findAskAiFindings') && ($('#findAskAiFindings').onclick = openFindAskAi);
$('#findEmptyAskAi') && ($('#findEmptyAskAi').onclick = openFindAskAi);
$('#ftClose') && ($('#ftClose').onclick = closeTriage);
$('#ftStop') && ($('#ftStop').onclick = () => { if (triageAbort) triageAbort.abort(); });
$('#ftRun') && ($('#ftRun').onclick = async () => {
  const seq = ++triageSeq;
  if (triageAbort) triageAbort.abort();
  const ctrl = new AbortController();
  triageAbort = ctrl;
  const out = $('#ftOut');
  const stop = $('#ftStop');
  if (out) out.innerHTML = '<div class="hint">Starting…</div>';
  if (stop) stop.style.display = '';
  setTriageStatus('Building context…');
  let log = '';
  const render = () => { if (seq === triageSeq && out) out.innerHTML = renderMD(log || '…'); };
  try {
    const r = await fetch('/api/ai/findings/triage', {
      method: 'POST',
      headers: { 'content-type': 'application/json', 'X-Interseptor-CSRF': '1' },
      body: JSON.stringify({ steer: ($('#ftSteer') || {}).value || '' }),
      signal: ctrl.signal,
    });
    if (r.status === 401) {
      if (location.pathname !== '/login') location.href = '/login';
      throw new Error('unauthorized');
    }
    if (!r.ok || !r.body) {
      const t = await r.text().catch(() => '');
      let msg = t;
      try { msg = (JSON.parse(t).error) || t; } catch { /* keep */ }
      throw new Error(msg || ('HTTP ' + r.status));
    }
    const reader = r.body.getReader(), dec = new TextDecoder();
    let buf = '', event = 'message';
    for (;;) {
      const { value, done } = await reader.read();
      if (done) break;
      if (seq !== triageSeq) return;
      buf += dec.decode(value, { stream: true });
      let idx;
      while ((idx = buf.indexOf('\n\n')) >= 0) {
        const chunk = buf.slice(0, idx); buf = buf.slice(idx + 2);
        let data = '';
        for (const line of chunk.split('\n')) {
          if (line.startsWith('event:')) event = line.slice(6).trim();
          else if (line.startsWith('data:')) data += line.slice(5).trim();
        }
        if (!data) continue;
        let payload = {};
        try { payload = JSON.parse(data); } catch { continue; }
        if (event === 'status') {
          setTriageStatus(payload.message || '');
          log += (log ? '\n' : '') + '_'+ (payload.message || '') + '_';
          render();
        } else if (event === 'tool') {
          log += '\n- tool `' + (payload.name || '?') + '`';
          render();
        } else if (event === 'text') {
          log += '\n\n' + (payload.text || '');
          render();
        } else if (event === 'error') {
          throw new Error(payload.message || 'triage failed');
        } else if (event === 'done') {
          setTriageStatus('Done · ' + (payload.toolCalls || 0) + ' tools · ' + (payload.steps || 0) + ' steps');
        }
        event = 'message';
      }
    }
    if (seq !== triageSeq) return;
    await loadFindings();
    toast('Triage finished — findings refreshed');
  } catch (e) {
    if (seq !== triageSeq) return;
    if (ctrl.signal.aborted) setTriageStatus('stopped');
    else {
      setTriageStatus('');
      if (out) out.innerHTML = '<div class="hint" style="color:var(--red)">Error: ' + esc(e.message) + '</div>';
      toast('triage: ' + e.message);
    }
  } finally {
    if (seq === triageSeq) {
      if (triageAbort === ctrl) triageAbort = null;
      if (stop) stop.style.display = 'none';
    }
  }
});

function findReportQuery(extra) {
  const q = new URLSearchParams(extra || {});
  const statuses=($('#findExportStatuses')||{}).value||'open,verified,fixed';
  q.set('statuses',statuses);
  if (findTagFilter) q.set('tag', findTagFilter);
  if ($('#findExportGroupByTag')?.checked) {
    q.set('groupBy', 'tag');
    q.set('omitTags', 'out-of-scope');
    q.set('tagOrder', 'cms,website,app,api');
  }
  const s = q.toString();
  return s ? '?' + s : '';
}
$('#findExport') && ($('#findExport').onclick = async () => {
  const fmt = ($('#findExportFmt') || {}).value || 'md';
  try {
    if (fmt === 'pdf') {
      const html = await api('/api/findings/report' + findReportQuery({ format: 'html' }));
      // Note: 'noopener' makes window.open return null per spec, which would
      // always trip the pop-up blocker branch and skip the print entirely.
      const w = window.open('', '_blank');
      if (!w) { toast('Allow pop-ups to export PDF'); return; }
      w.document.write(html);
      w.document.close();
      setTimeout(() => { w.focus(); w.print(); }, 250);
      toast('Print dialog — choose Save as PDF');
      return;
    }
    if (fmt === 'json') {
      const body = await api('/api/findings/report' + findReportQuery({ format: 'json' }));
      const text = typeof body === 'string' ? body : JSON.stringify(body, null, 2);
      await saveFile(new Blob([text], { type: 'application/json' }), 'interseptor-report.json', 'application/json');
      toast('Report downloaded');
      return;
    }
    const isHtml = fmt === 'html';
    const body = await api('/api/findings/report' + findReportQuery(isHtml ? { format: 'html' } : {}));
    const mime = isHtml ? 'text/html' : 'text/markdown';
    await saveFile(new Blob([body], { type: mime }), 'interseptor-report.' + (isHtml ? 'html' : 'md'), mime);
    toast('Report downloaded');
  } catch (e) { if (!(e && e.name === 'AbortError')) toast(e.message); }
});
$('#fcSave') && ($('#fcSave').onclick = async () => {
  const title = $('#fcTitle').value.trim(); if (!title) { toast('title required'); return; }
  try {
    const f = await api('/api/findings', { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ title, severity: $('#fcSeverity').value, source: 'human' }) });
    closeModal($('#findCreateModal')); selFinding = f && f.id;
  } catch (e) { toast(e.message); }
});

/* ---- cross-linking: which findings reference a flow, and jump to one ---- */
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

/* ---- "➕ Add to finding" from the History selection bar ---- */
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
$('#ffpClose') && ($('#ffpClose').onclick = () => closeModal($('#findFlowPickModal')));
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
