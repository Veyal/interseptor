import { $, esc, escAttr, state, toast, api, openModal, closeModal, renderMD, wireRowKey, saveFile, uiPrompt, methodColor, statusColor } from './core.js';
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
let findDetailView = 'report';
try { findDetailView = sessionStorage.getItem('findView') || 'report'; } catch { /* private mode */ }
// True while a text-block textarea has focus. An SSE findings.update (e.g. a body
// save round-tripping, or the AI recording) would otherwise rebuild the detail
// pane mid-edit and discard the focused textarea + any unsaved keystrokes.
let bodyEditing = false;

const sevColor = s => ({ Critical: 'var(--red)', High: 'var(--red)', Medium: 'var(--amber)', Low: 'var(--blue)', Info: 'var(--fg3)' }[s] || 'var(--fg3)');
const statusLabel = s => (s || '').replace(/_/g, ' ');

function textChainLabel(md) {
  return (md || '').replace(/```[\s\S]*?```/g, ' ').replace(/[#*_`~\[\]()]/g, '').replace(/\s+/g, ' ').trim();
}

function findingPocCount(f) {
  return (f.blocks || []).filter(b => b.type === 'flow').length || (f.flows || []).length || 0;
}

function findingStepCount(f) {
  const blocks = f.blocks || [];
  if (blocks.length) {
    return blocks.filter(b => (b.type === 'text' && textChainLabel(b.md)) || b.type === 'flow').length;
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
  const parts = [esc(statusLabel(f.status))];
  const steps = findingStepCount(f);
  const pocs = findingPocCount(f);
  if (steps) parts.push(steps + ' step' + (steps === 1 ? '' : 's'));
  else if (pocs) parts.push(pocs + ' PoC');
  if (findingIsEmpty(f)) parts.push('<span class="hint">needs content</span>');
  if (f.target) parts.push('<span class="hint">' + esc(f.target.length > 28 ? f.target.slice(0, 27) + '…' : f.target) + '</span>');
  if (f.source === 'ai') parts.push('<span style="color:var(--accent)">AI</span>');
  return parts.join(' · ');
}

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
  if (!selFinding || !findings.some(f => f.id === selFinding)) selFinding = findings[0].id;
  box.innerHTML = findings.map(f => `<div class="find-row${f.id === selFinding ? ' sel' : ''}${findingIsEmpty(f) ? ' find-row-empty' : ''}" data-id="${f.id}">
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
  if (findDetailView === 'chain') renderFindingChain($('#findChain'), bodyBlocks, $('#findImpact')?.value || '');
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
    container.innerHTML = '<div class="find-doc-empty">No description yet — write the finding narrative below, or attach PoC flows from Proxy History.</div>';
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
      renderFindBody(fid, $('#findImpact')?.value || '');
      scheduleSave(fid);
    };
  });

  // Delete buttons.
  container.querySelectorAll('[data-del]').forEach(btn => {
    btn.onclick = e => {
      e.stopPropagation();
      bodyBlocks.splice(Number(btn.dataset.del), 1);
      renderFindBody(fid, $('#findImpact')?.value || '');
      scheduleSave(fid);
    };
  });

  // Flow click → open flow modal. Missing (purged) flow blocks aren't clickable.
  container.querySelectorAll('.find-doc-flow:not(.find-block-missing) .find-poc-callout').forEach(el => {
    el.onclick = ev => {
      if (ev.target.closest('[data-del],[data-mv],.block-note,.find-poc-note-input')) return;
      const block = el.closest('.find-doc-flow');
      if (block) flowPopup(Number(block.dataset.flow));
    };
  });
}

// ---- attack-chain timeline (ordered blocks → vertical step flow) ------------

/** Visible steps for the chain: non-empty text, all flows, optional impact tail. */
export function chainSteps(blocks, impact) {
  const steps = [];
  (blocks || []).forEach((b, i) => {
    if (b.type === 'flow') steps.push({ b, i });
    else if (b.type === 'text' && textChainLabel(b.md)) steps.push({ b, i });
  });
  if (impact && impact.trim()) steps.push({ b: { type: 'impact', md: impact.trim() }, i: -1 });
  return steps;
}

/** Step list + edge indices; kept for testability. */
export function chainLayout(blocks, impact) {
  const steps = chainSteps(blocks, impact);
  const edges = [];
  for (let i = 0; i < steps.length - 1; i++) edges.push([i, i + 1]);
  return { nodes: steps, edges, w: 0, h: steps.length };
}

function chainFlowCard(b, i) {
  if (b.missing) {
    return `<blockquote class="find-poc-callout find-poc-missing">
      <div>⚠ PoC flow #${esc(String(b.flowId))} — evidence deleted from history</div>
      <span class="hint">Re-capture this endpoint to restore evidence</span>
    </blockquote>
    ${b.note ? `<div class="fc-step-note">${esc(b.note)}</div>` : ''}`;
  }
  const reqLine = b.method
    ? `<span class="m">${esc(b.method)}</span> <span class="p">${esc(b.host || '')}${esc(b.path || '')}</span>${b.status ? `<span class="sts">→ ${b.status}</span>` : ''}`
    : `<span class="hint">flow #${esc(String(b.flowId))}</span>`;
  return `<blockquote class="find-poc-callout"><div class="find-poc-req">${reqLine}</div></blockquote>
    ${b.note ? `<div class="fc-step-note">${esc(b.note)}</div>` : ''}`;
}

function chainStepHtml(item, num, isLast) {
  const { b, i } = item;
  const kind = b.type === 'impact' ? 'impact' : b.type === 'flow' ? (b.missing ? 'missing' : 'flow') : 'text';
  const badgeLabel = b.type === 'impact' ? '!' : String(num);
  let card = '', attrs = `data-kind="${kind}" data-i="${i}"`;
  if (b.type === 'text') {
    card = `<div class="fc-card fc-card-text md">${renderMD(b.md)}</div>`;
  } else if (b.type === 'impact') {
    card = `<div class="fc-card fc-card-impact"><div class="fc-card-label">Impact</div><div class="fc-card-body">${esc(b.md)}</div></div>`;
  } else {
    attrs += b.missing ? '' : ` data-flow="${b.flowId}"`;
    card = `<div class="fc-card fc-card-flow">${chainFlowCard(b, i)}</div>`;
  }
  const vline = isLast ? '' : '<div class="fc-vline" aria-hidden="true"></div>';
  return `<div class="fc-step" ${attrs} role="listitem">
    <div class="fc-rail"><div class="fc-badge fc-badge-${kind}">${badgeLabel}</div>${vline}</div>
    <div class="fc-content">${card}</div>
  </div>`;
}

function renderFindingChain(wrap, blocks, impact) {
  if (!wrap) return;
  const steps = chainSteps(blocks, impact);
  if (!steps.length) {
    wrap.innerHTML = '<div class="find-chain-empty hint">No steps yet — add narrative paragraphs and PoC flows in Report view.</div>';
    return;
  }
  wrap.innerHTML = `<div class="find-chain" role="list" aria-label="Attack chain">${steps.map((item, n) => chainStepHtml(item, n + 1, n === steps.length - 1)).join('')}</div>`;

  wrap.querySelectorAll('.fc-step[data-kind="flow"]').forEach(el => {
    el.onclick = () => {
      const fid = el.dataset.flow;
      if (fid) flowPopup(Number(fid));
    };
  });
  wrap.querySelectorAll('.fc-step[data-kind="text"]').forEach(el => {
    el.title = 'Open in Report view';
    el.onclick = () => {
      const idx = Number(el.dataset.i);
      setFindDetailView('report');
      requestAnimationFrame(() => {
        const block = document.querySelector(`#findBody .find-block[data-i="${idx}"]`);
        block?.scrollIntoView({ behavior: 'smooth', block: 'center' });
        block?.classList.add('find-block-flash');
        setTimeout(() => block?.classList.remove('find-block-flash'), 1200);
      });
    };
  });
}

function setFindDetailView(view) {
  findDetailView = view;
  try { sessionStorage.setItem('findView', view); } catch { /* private mode */ }
  const seg = $('#findViewSeg');
  if (seg) seg.querySelectorAll('button').forEach(b => b.classList.toggle('on', b.dataset.view === view));
  const hint = document.querySelector('.find-view-hint');
  if (hint) hint.textContent = view === 'chain' ? 'Attack steps top-to-bottom · click a step to inspect' : 'Narrative report · markdown + PoC flows';
  const article = document.querySelector('.find-article');
  if (article) article.classList.toggle('find-chain-active', view === 'chain');
  renderFindBody(bodyFindingId, $('#findImpact')?.value || '');
}

function renderFindBody(fid, impactText) {
  const docEl = $('#findBody');
  const chainEl = $('#findChain');
  if (findDetailView === 'chain') {
    if (chainEl) renderFindingChain(chainEl, bodyBlocks, impactText);
  } else {
    renderBodyEditor(docEl, fid);
  }
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

function renderFindingDetail() {
  const box = $('#findDetail'); if (!box) return;
  const f = findings.find(x => x.id === selFinding);
  if (!f) { box.innerHTML = '<div class="hint" style="padding:16px">Select a finding.</div>'; return; }

  const statusSel = STATUSES.map(s => `<option value="${s}"${s === f.status ? ' selected' : ''}>${esc(statusLabel(s))}</option>`).join('');
  const missBanner = (() => {
    const miss = (f.blocks || []).filter(b => b.type === 'flow' && b.missing).length;
    return miss ? `<div class="find-missing-banner">⚠ ${miss} PoC flow${miss === 1 ? '' : 's'} deleted from history — re-capture the endpoint${miss === 1 ? '' : 's'} to restore evidence.</div>` : '';
  })();
  box.innerHTML = `<article class="find-article${findDetailView === 'chain' ? ' find-chain-active' : ''}">
    <header class="find-header">
      <div class="find-header-top">
        <span class="find-sev-badge" style="color:${sevColor(f.severity)}">${esc(f.severity)}</span>
        <h2 class="find-title-text" id="findTitleText">${esc(f.title)}</h2>
        <button class="btn xs" id="findRename" title="Rename finding" aria-label="Rename finding">✎</button>
      </div>
      <div class="find-meta-bar">
        <select id="findStatus" class="btn" style="background:var(--bg3)" aria-label="Finding status">${statusSel}</select>
        ${f.target ? `<span class="find-target hint">${esc(f.target)}</span>` : ''}
        <div class="find-cvss-field">
          <label for="findCvss">CVSS</label>
          <input id="findCvss" class="find-cvss-inline" type="text" value="${escAttr(f.cvss || '')}" placeholder="e.g. 7.5">
        </div>
        <div class="spacer"></div>
        <button class="btn danger xs" id="findDelete">Delete</button>
      </div>
    </header>
    ${missBanner}
    <div class="find-view-bar">
      <div class="seg find-view-seg" id="findViewSeg" role="tablist" aria-label="Finding view">
        <button type="button" data-view="report"${findDetailView === 'report' ? ' class="on"' : ''}>Edit</button>
        <button type="button" data-view="chain"${findDetailView === 'chain' ? ' class="on"' : ''}>Timeline</button>
      </div>
      <span class="hint find-view-hint">${findDetailView === 'chain' ? 'Read-only attack timeline · click a step to inspect' : 'Editable narrative · add paragraphs & PoC flows'}</span>
    </div>
    <div class="find-body-wrap">
      <div class="find-doc" id="findBody"></div>
      <div class="find-chain-wrap" id="findChain"></div>
    </div>
    <div class="find-doc-actions" id="findDocActions">
      <button class="btn" id="findAddText">＋ Paragraph</button>
      <button class="btn" id="findAddFlow" title="Attach request/response flows as PoC evidence">＋ PoC flow<span id="findPocReady" class="hint"></span></button>
    </div>
    <aside class="find-impact">
      <h3>Impact</h3>
      <textarea id="findImpact" class="find-impact-text" rows="3" placeholder="What an attacker gains — business consequence, data exposed, privilege escalation…">${esc(f.impact || '')}</textarea>
    </aside>
  </article>`;

  // Load body blocks for this finding.
  bodyFindingId = f.id;
  bodyBlocks = (f.blocks || []).map(b => ({ ...b }));
  renderFindBody(f.id, f.impact || '');

  $('#findViewSeg')?.querySelectorAll('button').forEach(btn => {
    btn.onclick = () => setFindDetailView(btn.dataset.view);
  });

  // Wire controls.
  $('#findRename').onclick = async () => {
    const t = await uiPrompt({ title: 'Rename finding', value: f.title, placeholder: 'Finding title' });
    if (t == null || t === f.title) return;
    try { await api('/api/findings/' + f.id, { method: 'PATCH', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ title: t }) }); f.title = t; const el = $('#findTitleText'); if (el) el.textContent = t; toast('finding renamed'); }
    catch (err) { toast(err.message); }
  };
  $('#findStatus').onchange = async e => {
    try { await api('/api/findings/' + f.id, { method: 'PATCH', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ status: e.target.value }) }); f.status = e.target.value; toast('status: ' + statusLabel(e.target.value)); }
    catch (err) { toast(err.message); }
  };
  $('#findDelete').onclick = async () => {
    try { await api('/api/findings/' + f.id, { method: 'DELETE' }); selFinding = null; toast('finding deleted'); loadFindings(); }
    catch (err) { toast(err.message); }
  };
  $('#findAddText').onclick = () => {
    bodyBlocks.push({ type: 'text', md: '' });
    setFindDetailView('report');
    renderFindBody(f.id, $('#findImpact')?.value || '');
    const tas = document.querySelectorAll('#findBody .block-text');
    if (tas.length) tas[tas.length - 1].focus();
  };
  $('#findAddFlow').onclick = () => addPoCFlowsToFinding(f.id);
  updateFindPocBtn();
  $('#findImpact')?.addEventListener('blur', async () => {
    const impact = $('#findImpact').value;
    if (findDetailView === 'chain') renderFindingChain($('#findChain'), bodyBlocks, impact);
    try { await api('/api/findings/' + f.id, { method: 'PATCH', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ impact }) }); }
    catch (err) { toast(err.message); }
  });
  $('#findCvss')?.addEventListener('blur', async () => {
    const cvss = $('#findCvss').value;
    try { await api('/api/findings/' + f.id, { method: 'PATCH', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ cvss }) }); }
    catch (err) { toast(err.message); }
  });
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
$('#findNew') && ($('#findNew').onclick = () => { $('#fcTitle').value = ''; $('#fcSeverity').value = 'Medium'; $('#fcDetail').value = ''; openModal($('#findCreateModal')); $('#fcTitle').focus(); });
$('#fcClose') && ($('#fcClose').onclick = () => closeModal($('#findCreateModal')));
$('#findExport') && ($('#findExport').onclick = async () => {
  const fmt = ($('#findExportFmt') || {}).value || 'md';
  try {
    if (fmt === 'pdf') {
      const html = await api('/api/findings/report?format=html');
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
    const isHtml = fmt === 'html';
    const body = await api('/api/findings/report' + (isHtml ? '?format=html' : ''));
    const mime = isHtml ? 'text/html' : 'text/markdown';
    await saveFile(new Blob([body], { type: mime }), 'interceptor-report.' + (isHtml ? 'html' : 'md'), mime);
    toast('Report downloaded');
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
  document.querySelector('.tab[data-tab="findings"]')?.click();
  loadFindings();
}

// Deep-link: if the URL hash is #finding-<id>, activate the Findings tab and
// select that finding. Handles both initial page load and hashchange events.
function handleFindingHash() {
  const m = location.hash.match(/^#finding-(\d+)$/);
  if (!m) return;
  const id = Number(m[1]);
  if (!id) return;
  selFinding = id;
  document.querySelector('.tab[data-tab="findings"]')?.click();
  loadFindings();
}
window.addEventListener('hashchange', handleFindingHash);
// Run on module load so a direct URL like /#finding-3 opens the right finding.
handleFindingHash();
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
