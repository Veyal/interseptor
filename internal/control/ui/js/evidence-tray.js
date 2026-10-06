// evidence-tray.js — a compact tile strip of the evidence attached to a finding.
//
// Tiles mirror the finding's ordered `blocks` (flow and image blocks only; text
// steps stay in the step editor). Reordering and removal edit the same block list
// the step editor owns, through the callbacks the caller passes, so there is one
// source of truth and one save path. Reorder has a keyboard path (Alt+Arrow, or
// the move buttons) and every change is announced in a polite live region.
//
// Spec items the current API cannot honour are not faked: there is no per-flow
// "proof" flag (only the free-text `proof` statement, shown as an indicator), no
// WebSocket frame blocks, and no credential detection on stored blocks.
//
// No imports. The DOM is only touched through the elements passed in, so the
// pure helpers and the HTML builder run under `node --test`.

const EVIDENCE_TYPES = ['flow', 'image'];
const esc = (s) => String(s == null ? '' : s).replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[c]);

export const isEvidenceBlock = (b) => !!b && EVIDENCE_TYPES.includes(b.type);

// trayTiles returns one tile per evidence block, in block order. `index` is the
// block's position in the full list; `position` is 1-based among tiles.
export function trayTiles(blocks) {
  const tiles = [];
  (Array.isArray(blocks) ? blocks : []).forEach((b, index) => {
    if (!isEvidenceBlock(b)) return;
    const missing = !!b.missing;
    if (b.type === 'image') {
      tiles.push({ index, kind: 'shot', title: b.caption ? String(b.caption) : 'Screenshot', detail: missing ? 'blob missing' : String(b.mime || 'image'), role: b.role || '', missing, hasProof: !!b.proof, thumb: missing || !b.hash ? '' : (b.url || '/api/findings/images/' + b.hash), alt: String(b.caption || '') });
      return;
    }
    const line = b.method ? `${b.method} ${b.host || ''}${b.path || ''}` : '';
    tiles.push({ index, kind: 'flow', title: line || ('Flow #' + b.flowId), detail: missing ? 'deleted from history' : (b.status ? 'HTTP ' + b.status : 'flow #' + b.flowId), role: b.role || '', missing, hasProof: !!b.proof, flowId: Number(b.flowId) || 0, thumb: '', alt: '' });
  });
  tiles.forEach((t, i) => { t.position = i + 1; t.total = tiles.length; });
  return tiles;
}

// moveEvidence swaps the evidence block at `index` with the neighbouring
// evidence block in `dir` (-1 or 1), leaving text steps where they are. Returns
// {blocks, index} with the block's new position, or null when it cannot move.
export function moveEvidence(blocks, index, dir) {
  if (!Array.isArray(blocks) || !isEvidenceBlock(blocks[index]) || (dir !== -1 && dir !== 1)) return null;
  let j = index + dir;
  while (j >= 0 && j < blocks.length && !isEvidenceBlock(blocks[j])) j += dir;
  if (j < 0 || j >= blocks.length) return null;
  const next = blocks.slice();
  [next[index], next[j]] = [next[j], next[index]];
  return { blocks: next, index: j };
}

export function removeEvidence(blocks, index) {
  if (!Array.isArray(blocks) || !isEvidenceBlock(blocks[index])) return null;
  const next = blocks.slice();
  const [removed] = next.splice(index, 1);
  return { blocks: next, removed, index };
}

export function restoreEvidence(blocks, removed, index) {
  const next = blocks.slice();
  next.splice(Math.min(Math.max(index, 0), next.length), 0, removed);
  return next;
}

export function announceMove(tile, newPosition, total) {
  return `${tile.kind === 'shot' ? 'Screenshot' : 'Flow'} ${tile.title} moved to position ${newPosition} of ${total}`;
}

export function announceRemove(tile) {
  return `${tile.kind === 'shot' ? 'Screenshot' : 'Flow'} ${tile.title} removed`;
}

// Alt+ArrowUp/Left moves a tile earlier, Alt+ArrowDown/Right later.
export function keyMoveDir(e) {
  if (!e || !e.altKey || e.ctrlKey || e.metaKey) return 0;
  if (e.key === 'ArrowUp' || e.key === 'ArrowLeft') return -1;
  if (e.key === 'ArrowDown' || e.key === 'ArrowRight') return 1;
  return 0;
}

const icon = (name) => `<svg class="icon" aria-hidden="true" focusable="false"><use href="#${name}"/></svg>`;

function tileHTML(t, { editable }) {
  const label = `${t.kind === 'shot' ? 'Screenshot' : 'Flow'} ${t.position} of ${t.total}: ${t.title}${t.missing ? ' (' + t.detail + ')' : ''}`;
  const thumb = t.thumb ? `<img class="et-thumb" src="${esc(t.thumb)}" alt="${esc(t.alt)}" loading="lazy">` : `<span class="et-kind">${icon(t.kind === 'shot' ? 'i-evidence' : 'i-link')}</span>`;
  const controls = editable
    ? `<span class="et-actions"><button type="button" class="btn xs" data-et-move="-1" aria-label="Move earlier: ${esc(t.title)}"${t.position === 1 ? ' disabled' : ''}>${icon('i-chevron')}<span class="u-sr">Earlier</span></button><button type="button" class="btn xs" data-et-move="1" aria-label="Move later: ${esc(t.title)}"${t.position === t.total ? ' disabled' : ''}>${icon('i-chevron')}<span class="u-sr">Later</span></button><button type="button" class="btn xs danger" data-et-remove aria-label="Remove from evidence: ${esc(t.title)}">${icon('i-close')}<span class="u-sr">Remove</span></button></span>`
    : '';
  return `<li class="et-tile${t.missing ? ' is-missing' : ''}" role="group" aria-roledescription="evidence tile" aria-label="${esc(label)}" data-et-index="${t.index}" tabindex="${t.position === 1 ? '0' : '-1'}">${thumb}<span class="et-body"><span class="et-title">${esc(t.title)}</span><span class="et-meta">${esc(t.detail)}${t.role ? ' · ' + esc(t.role) : ''}${t.hasProof ? ' · proof statement' : ''}</span></span>${controls}</li>`;
}

// evidenceTrayHTML builds the strip. `findingId` makes the strip a drop target
// for the delegated flow drag handler in evidence-attach.js (drag is additive:
// every path also has a button).
export function evidenceTrayHTML(blocks, { editable = false, findingId = 0 } = {}) {
  const tiles = trayTiles(blocks);
  const head = `<div class="et-head"><strong>Evidence tray</strong><span class="count">${tiles.length}</span><span class="et-help hint">${editable ? 'Alt+Arrow moves the focused tile.' : 'Edit the finding to reorder.'}</span></div>`;
  const body = tiles.length
    ? `<ul class="et-list" role="list" aria-label="Attached evidence">${tiles.map((t) => tileHTML(t, { editable })).join('')}</ul>`
    : `<p class="et-empty hint">No evidence attached. Use Attach as evidence from History, or add a screenshot or flow below.</p>`;
  return `<div class="et" data-evidence-drop data-finding-id="${Number(findingId) || 0}">${head}${body}<div class="et-live u-sr" role="status" aria-live="polite"></div></div>`;
}

export const UNDO_MS = 5000;

// showUndoToast puts a five second toast with a keyboard-reachable Undo button
// in #toast. It pauses while hovered or focused. Returns the toast node.
export function showUndoToast(doc, message, onUndo, ms = UNDO_MS) {
  const host = doc && doc.getElementById ? doc.getElementById('toast') : null;
  if (!host) return null;
  const t = doc.createElement('div');
  t.className = 'toast-item info show';
  t.setAttribute('role', 'status');
  t.textContent = message + ' ';
  const btn = doc.createElement('button');
  btn.type = 'button';
  btn.className = 'btn xs toast-action';
  btn.textContent = 'Undo';
  t.appendChild(btn);
  host.appendChild(t);
  let timer = null;
  const dismiss = () => { clearTimeout(timer); t.remove(); };
  const arm = () => { clearTimeout(timer); timer = setTimeout(dismiss, ms); };
  t.addEventListener('mouseenter', () => clearTimeout(timer));
  t.addEventListener('mouseleave', arm);
  t.addEventListener('focusin', () => clearTimeout(timer));
  t.addEventListener('focusout', arm);
  btn.addEventListener('click', () => { dismiss(); onUndo(); });
  arm();
  return t;
}

// wireEvidenceTray attaches behaviour to a rendered tray. Callbacks:
//   getBlocks()          -> current block list
//   setBlocks(next, why) -> replace the list (caller re-renders and schedules the save)
//   focusIndex(i)        -> optional; called after re-render to refocus a tile
export function wireEvidenceTray(root, { getBlocks, setBlocks, focusIndex } = {}) {
  if (!root || !getBlocks || !setBlocks) return;
  const live = root.querySelector('.et-live');
  const say = (m) => { if (live) { live.textContent = ''; live.textContent = m; } };
  const tileOf = (blocks, index) => trayTiles(blocks).find((t) => t.index === index);
  const move = (index, dir) => {
    const blocks = getBlocks();
    const tile = tileOf(blocks, index);
    const r = moveEvidence(blocks, index, dir);
    if (!tile || !r) return;
    setBlocks(r.blocks, 'move');
    const after = trayTiles(r.blocks).find((t) => t.index === r.index);
    say(announceMove(tile, after ? after.position : tile.position, after ? after.total : tile.total));
    if (focusIndex) focusIndex(r.index);
  };
  const remove = (index) => {
    const blocks = getBlocks();
    const tile = tileOf(blocks, index);
    const r = removeEvidence(blocks, index);
    if (!tile || !r) return;
    setBlocks(r.blocks, 'remove');
    say(announceRemove(tile));
    showUndoToast(root.ownerDocument, announceRemove(tile), () => setBlocks(restoreEvidence(getBlocks(), r.removed, r.index), 'undo'));
  };
  root.addEventListener('click', (e) => {
    const tile = e.target.closest && e.target.closest('[data-et-index]');
    if (!tile) return;
    const index = Number(tile.dataset.etIndex);
    const mv = e.target.closest('[data-et-move]');
    if (mv) { e.preventDefault(); move(index, Number(mv.dataset.etMove)); return; }
    if (e.target.closest('[data-et-remove]')) { e.preventDefault(); remove(index); }
  });
  root.addEventListener('keydown', (e) => {
    const tile = e.target.closest && e.target.closest('[data-et-index]');
    if (!tile) return;
    const dir = keyMoveDir(e);
    if (dir) { e.preventDefault(); move(Number(tile.dataset.etIndex), dir); return; }
    if (e.target !== tile || e.altKey || e.ctrlKey || e.metaKey) return;
    const tiles = [...root.querySelectorAll('[data-et-index]')];
    const i = tiles.indexOf(tile);
    const step = e.key === 'ArrowRight' || e.key === 'ArrowDown' ? 1 : e.key === 'ArrowLeft' || e.key === 'ArrowUp' ? -1 : 0;
    if (!step) return;
    e.preventDefault();
    const next = tiles[Math.min(tiles.length - 1, Math.max(0, i + step))];
    tiles.forEach((t) => { t.tabIndex = t === next ? 0 : -1; });
    if (next) next.focus();
  });
}
