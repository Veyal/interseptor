// project-picker.js — the Projects dialog (#projModal). One searchable tree:
// the current project is pinned in its own card, every other project is a
// two-line row grouped by folder (Recent first on long lists), and creating a
// project is a separate footer step. Pure rules live in project-picker-model.js;
// switching itself stays in settings.js (doSwitchProject), injected through
// configureProjectPicker() so this module never imports settings.js.
//
// Keyboard: the filter has focus on open. ArrowDown enters the tree (one Tab
// stop, roving tabindex); arrows/Home/End/PageUp/PageDown move, Right/Left
// expand or collapse a folder, Enter opens a project, Shift+F10 / F2 moves it
// to another folder, typing returns to the filter. Enter in the filter opens a
// single match, otherwise moves to the first match, or offers Create on none.

import { $, esc, escAttr, api, toast, openModal, closeModal, renderLoadError, copyText } from './core.js';
import {
  buildPickerModel, folderOptions, highlightHTML, nextIndex, pickerSummary, projectPathFlavorFor,
  validateNewProject, folderError, normalizeFolder,
} from './project-picker-model.js';

const OPEN_KEY = 'interseptor.projectPicker.open';
const NAV_KEYS = ['ArrowDown', 'ArrowUp', 'Home', 'End', 'PageDown', 'PageUp'];

let cfg = {
  switchProject: async () => {}, isSwitchPending: () => false,
  setPathFlavor: () => {}, clearFeedback: () => {}, onProjectsChanged: () => {},
};
export function configureProjectPicker(next) { cfg = { ...cfg, ...next }; }

let projectModalLoadEpoch = 0;
let projectModalDataLoaded = false;
let cache = null; // {projects, current, dir, canSwitch, flavor}
let model = null;
let rowsById = new Map();
let activeNode = ''; // data-node of the roving tab stop
let moveTarget = null; // project being moved to a folder
let opener = null;

const list = () => $('#pmList');
const filter = () => $('#pmFilter');

function readOpenState() {
  try { const v = JSON.parse(localStorage.getItem(OPEN_KEY) || '{}'); return v && typeof v === 'object' ? v : {}; } catch { return {}; }
}
function writeOpenState(id, open) {
  try { const v = readOpenState(); v[id] = !!open; localStorage.setItem(OPEN_KEY, JSON.stringify(v)); } catch { /* storage is a convenience */ }
}

const icon = (name, cls = 'icon') => `<svg class="${cls}" aria-hidden="true" focusable="false"><use href="#i-${name}"/></svg>`;

function rowHTML(item) {
  const p = item.project;
  const path = p.path ? `<span class="pm-row-path font-mono">${highlightHTML(p.path, item.match.path, esc)}</span>` : '';
  const meta = item.showFolder && p.category
    ? esc(item.meta.slice(0, item.meta.length - normalizeFolder(p.category).length)) + highlightHTML(normalizeFolder(p.category), item.match.folder, esc)
    : esc(item.meta);
  return `<div role="treeitem" class="pm-row" data-node="r:${escAttr(item.id)}" tabindex="-1" aria-keyshortcuts="Shift+F10">`
    + `<span class="pm-row-main"><span class="pm-name">${highlightHTML(p.name, item.match.name, esc)}</span>`
    + `<span class="pm-meta">${meta}</span>${path}</span>`
    + `<button type="button" class="pm-row-more" tabindex="-1" aria-hidden="true" title="Move to folder…">${icon('folder')}</button></div>`;
}

function sectionHTML(section, n) {
  const count = section.items.length;
  const label = section.label;
  const body = section.items.map((item) => { const html = rowHTML(item); rowsById.set('r:' + item.id, item); return html; }).join('');
  return `<div role="treeitem" class="pm-group" data-node="g:${escAttr(section.id)}" data-section="${escAttr(section.id)}" tabindex="-1" aria-expanded="${section.open}" aria-label="${escAttr(label + ', ' + count + (count === 1 ? ' project' : ' projects'))}">`
    + `<div class="pm-group-head" id="pm-g-${n}">${icon('chevron', 'icon pm-chevron')}<span class="pm-group-label">${esc(label)}</span><span class="pm-count">${count}</span></div>`
    + `<div role="group" class="pm-group-body"${section.open ? '' : ' hidden'}>${body}</div></div>`;
}

function visibleNodes() {
  const el = list();
  if (!el) return [];
  return [...el.querySelectorAll('[role="treeitem"]')].filter((n) => !n.parentElement.closest('[role="group"][hidden]'));
}

function setTabStop(node) {
  const el = list();
  if (!el) return;
  el.querySelectorAll('[role="treeitem"]').forEach((n) => { n.tabIndex = n === node ? 0 : -1; });
  activeNode = node ? node.dataset.node : '';
}
function focusNode(node) {
  if (!node) return;
  setTabStop(node);
  node.focus({ preventScroll: true });
  node.scrollIntoView({ block: 'nearest' });
}

function paintCurrent() {
  const name = $('#pmCurrent'), dir = $('#pmDir');
  if (name) name.textContent = cache ? cache.current || 'default' : '';
  if (dir) { dir.textContent = cache ? cache.dir || '' : ''; dir.title = dir.textContent; }
  const copy = $('#pmCopyDir');
  if (copy) copy.hidden = !(cache && cache.dir);
}

function paintEmpty(query) {
  const empty = $('#pmEmpty');
  if (!empty) return;
  const none = !!model && !model.items.length;
  empty.hidden = !none;
  if (!none) { empty.innerHTML = ''; return; }
  if (query) {
    empty.innerHTML = `<p class="pm-empty-text">No projects match “${esc(query)}”.</p>`
      + `<div class="pm-empty-actions"><button type="button" class="btn btn-primary" id="pmCreateFromQuery">${icon('plus')} Create “${esc(query)}”</button>`
      + `<button type="button" class="btn" id="pmEmptyClear">Clear filter</button></div>`;
    $('#pmCreateFromQuery').onclick = () => openCreate(query);
    $('#pmEmptyClear').onclick = () => clearFilter();
  } else {
    empty.innerHTML = '<p class="pm-empty-text">No other projects yet. Create one to keep an engagement’s history separate.</p>';
  }
}

function paintFolderList() {
  const dl = $('#pmFolderList');
  if (dl) dl.innerHTML = folderOptions(cache ? cache.projects : []).map((f) => `<option value="${escAttr(f)}"></option>`).join('');
}

export function paintProjectModal() {
  const el = list();
  if (!el || !cache) return;
  const f = filter();
  const query = (f && f.value || '').trim();
  const clear = $('#pmFilterClear');
  if (clear) clear.hidden = !(f && f.value);
  paintCurrent();
  if (!cache.canSwitch) {
    el.innerHTML = '';
    el.hidden = true;
    model = null;
    const empty = $('#pmEmpty');
    if (empty) { empty.hidden = false; empty.innerHTML = '<p class="pm-empty-text">Project switching is unavailable in this build.</p>'; }
    return;
  }
  const hadFocus = el.contains(document.activeElement);
  model = buildPickerModel(cache.projects, { query, current: cache.current, dir: cache.dir, flavor: cache.flavor, open: readOpenState() });
  rowsById = new Map();
  el.innerHTML = model.sections.map(sectionHTML).join('');
  el.hidden = !model.items.length;
  const summary = $('#pmSummary');
  if (summary) summary.textContent = pickerSummary(model);
  paintEmpty(query);
  const nodes = visibleNodes();
  const keep = nodes.find((n) => n.dataset.node === activeNode) || nodes.find((n) => n.classList.contains('pm-row')) || nodes[0] || null;
  setTabStop(keep);
  if (hadFocus && keep) keep.focus({ preventScroll: true });
}

function toggleGroup(node, open) {
  const next = open === undefined ? node.getAttribute('aria-expanded') !== 'true' : open;
  node.setAttribute('aria-expanded', String(next));
  const body = node.querySelector('[role="group"]');
  if (body) body.hidden = !next;
  if (!(model && model.query)) writeOpenState(node.dataset.section, next);
}

async function openRow(node) {
  const item = rowsById.get(node.dataset.node);
  if (!item || node.getAttribute('aria-disabled') === 'true' || cfg.isSwitchPending()) return;
  node.setAttribute('aria-busy', 'true');
  await cfg.switchProject(item.project.name, item.project.path || '');
  if (!cfg.isSwitchPending()) node.removeAttribute('aria-busy');
}

/* ---- move to folder ---- */
function openMoveEditor(node) {
  const item = rowsById.get(node.dataset.node);
  const form = $('#pmMove');
  if (!item || !form || cfg.isSwitchPending()) return;
  moveTarget = { project: item.project, key: item.key, node: node.dataset.node };
  $('#pmMoveName').textContent = item.project.name;
  const input = $('#pmMoveFolder');
  input.value = normalizeFolder(item.project.category);
  setFieldError(input, $('#pmMoveError'), '');
  form.hidden = false;
  input.focus();
  input.select();
}
function closeMoveEditor({ restore = true } = {}) {
  const form = $('#pmMove');
  if (!form || form.hidden) return;
  form.hidden = true;
  const back = moveTarget && moveTarget.node;
  moveTarget = null;
  if (restore) {
    const node = visibleNodes().find((n) => n.dataset.node === back);
    if (node) focusNode(node); else filter()?.focus();
  }
}
async function saveMove(e) {
  e.preventDefault();
  if (!moveTarget) return;
  const input = $('#pmMoveFolder'), err = $('#pmMoveError'), save = $('#pmMoveSave');
  const problem = folderError(input.value);
  if (problem) { setFieldError(input, err, problem); input.focus(); return; }
  const { project } = moveTarget;
  const category = normalizeFolder(input.value);
  save.disabled = true;
  try {
    await api('/api/project/folder', { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify(project.path ? { path: project.path, category } : { target: project.name, category }) });
    const { key } = moveTarget;
    moveTarget = null;
    $('#pmMove').hidden = true;
    // Show the project where it landed: its destination folder opens.
    writeOpenState(category ? 'folder:' + category : 'unfiled', true);
    await renderProjModal();
    const nodes = visibleNodes();
    const back = nodes.find((n) => !n.dataset.node.startsWith('r:recent|') && rowsById.get(n.dataset.node)?.key === key)
      || nodes.find((n) => rowsById.get(n.dataset.node)?.key === key);
    if (back) focusNode(back); else filter()?.focus();
    toast(category ? `Moved “${project.name}” to ${category}` : `Removed “${project.name}” from its folder`);
    cfg.onProjectsChanged();
  } catch (ex) {
    setFieldError(input, err, ex.message || 'Could not move the project.');
    input.focus();
  } finally { save.disabled = false; }
}

/* ---- create ---- */
function setFieldError(input, out, message) {
  if (!input || !out) return;
  if (message) { input.setAttribute('aria-invalid', 'true'); out.hidden = false; out.textContent = message; } else { input.removeAttribute('aria-invalid'); out.hidden = true; out.textContent = ''; }
}
function clearCreateErrors() {
  setFieldError($('#pmNew'), $('#pmNewError'), '');
  setFieldError($('#pmNewFolder'), $('#pmNewFolderError'), '');
  setFieldError($('#pmNewPath'), $('#pmNewPathError'), '');
  const openExisting = $('#pmOpenExisting'); if (openExisting) openExisting.hidden = true;
}
function createValues() {
  return { name: $('#pmNew')?.value || '', folder: $('#pmNewFolder')?.value || '', path: $('#pmNewPath')?.value || '' };
}
function validateCreate({ show = true } = {}) {
  const result = validateNewProject(createValues(), cache ? cache.projects : [], cache ? cache.flavor : 'posix');
  if (!show) return result;
  const fields = { name: ['#pmNew', '#pmNewError'], folder: ['#pmNewFolder', '#pmNewFolderError'], path: ['#pmNewPath', '#pmNewPathError'] };
  for (const [field, [inputSel, errSel]] of Object.entries(fields)) setFieldError($(inputSel), $(errSel), result.field === field ? result.error : '');
  const openExisting = $('#pmOpenExisting');
  if (openExisting) {
    openExisting.hidden = !result.existing;
    if (result.existing) openExisting.textContent = `Open “${result.existing.name}” instead`;
  }
  if (!result.ok && (result.field === 'folder' || result.field === 'path')) { const more = $('#pmCreateMore'); if (more) more.open = true; }
  return result;
}
export function openCreate(prefill = '') {
  const form = $('#pmCreate'), toggle = $('#pmNewToggle');
  if (!form || cfg.isSwitchPending()) return;
  closeMoveEditor({ restore: false });
  form.hidden = false;
  if (toggle) { toggle.setAttribute('aria-expanded', 'true'); toggle.hidden = true; }
  const name = $('#pmNew');
  if (prefill && name) name.value = prefill;
  if (name && name.value) validateCreate();
  name?.focus();
}
function closeCreate({ restore = true } = {}) {
  const form = $('#pmCreate'), toggle = $('#pmNewToggle');
  if (!form) return;
  form.hidden = true;
  ['#pmNew', '#pmNewFolder', '#pmNewPath'].forEach((s) => { const f = $(s); if (f) f.value = ''; });
  clearCreateErrors();
  const more = $('#pmCreateMore'); if (more) more.open = false;
  if (toggle) { toggle.hidden = false; toggle.setAttribute('aria-expanded', 'false'); if (restore) toggle.focus(); }
}
async function submitCreate(e) {
  e.preventDefault();
  if (cfg.isSwitchPending()) return;
  const result = validateCreate();
  if (!result.ok) { const sel = { name: '#pmNew', folder: '#pmNewFolder', path: '#pmNewPath' }[result.field]; $(sel)?.focus(); return; }
  const btn = $('#pmNewBtn');
  if (btn) { btn.dataset.label = btn.innerHTML; btn.setAttribute('aria-busy', 'true'); btn.textContent = 'Opening… Interseptor restarts'; }
  try {
    if (result.folder) {
      try {
        await api('/api/project/folder', { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify(result.path ? { path: result.path, category: result.folder } : { target: result.name, category: result.folder }) });
      } catch (ex) { setFieldError($('#pmNewFolder'), $('#pmNewFolderError'), ex.message); $('#pmCreateMore').open = true; return; }
    }
    await cfg.switchProject(result.name, result.path);
  } finally {
    if (!cfg.isSwitchPending()) restoreCreateButton();
  }
}

/* ---- load / open ---- */
export async function renderProjModal() {
  const epoch = ++projectModalLoadEpoch;
  const el = list();
  if (el) {
    el.setAttribute('aria-busy', 'true');
    el.querySelectorAll('.pm-row').forEach((row) => row.setAttribute('aria-disabled', 'true'));
  }
  setProjectModalActionsDisabled(true);
  try {
    const d = await api('/api/project');
    if (epoch !== projectModalLoadEpoch) return false;
    const flavor = projectPathFlavorFor(d.dir);
    cfg.setPathFlavor(flavor);
    if (!el) return false;
    cache = { projects: Array.isArray(d.projects) ? d.projects : [], current: d.current || 'default', dir: d.dir || '', canSwitch: !!d.canSwitch, flavor };
    paintFolderList();
    paintProjectModal();
    el.removeAttribute('aria-busy'); projectModalDataLoaded = true; setProjectModalActionsDisabled(!d.canSwitch);
    return true;
  } catch (e) {
    if (epoch !== projectModalLoadEpoch) return false;
    setProjectModalActionsDisabled(true);
    if (el) el.hidden = false;
    renderLoadError(el, 'Projects', e, renderProjModal, projectModalDataLoaded);
    if (el) el.removeAttribute('aria-busy');
    return false;
  }
}

function restoreCreateButton() {
  const btn = $('#pmNewBtn');
  if (!btn || btn.dataset.label === undefined) return;
  btn.innerHTML = btn.dataset.label;
  delete btn.dataset.label;
  btn.removeAttribute('aria-busy');
}

function setProjectModalActionsDisabled(disabled) {
  for (const sel of ['#pmNewBtn', '#pmNewToggle']) {
    const b = $(sel);
    if (!b) continue;
    b.disabled = !!disabled;
    if (disabled) b.title = 'Project data is not loaded yet — wait or retry before creating a project';
    else b.removeAttribute('title');
  }
}

function visible(el) { return !!(el && el.isConnected && el.getClientRects().length); }

// openProjectPicker opens the dialog. `from` is the control that asked; it
// takes focus first so closing the dialog returns there even in browsers that
// do not focus a clicked button (Safari).
export async function openProjectPicker(from) {
  if (cfg.isSwitchPending()) return;
  const m = $('#projModal');
  if (!m) return;
  const candidate = from instanceof Element ? from : null;
  opener = visible(candidate) ? candidate : [$('#mobileProjectChip'), $('#projBadge')].find(visible) || null;
  if (opener && document.activeElement !== opener && !m.contains(document.activeElement)) opener.focus({ preventScroll: true });
  cfg.clearFeedback();
  closeMoveEditor({ restore: false });
  closeCreate({ restore: false });
  const f = filter();
  if (f) f.value = '';
  activeNode = '';
  openModal(m, { initialFocus: f });
  if (cache) paintProjectModal();
  const rendered = await renderProjModal();
  if (rendered && f && m.style.display === 'flex' && !f.disabled && !m.contains(document.activeElement)) f.focus();
}

function clearFilter() {
  const f = filter();
  if (!f) return;
  f.value = '';
  paintProjectModal();
  f.focus();
}

function closePicker() {
  if (cfg.isSwitchPending()) return;
  closeModal($('#projModal'));
}

// showProjectSwitchError ties a failed switch to the Save location field it came
// from, so the reason sits next to the value to fix (the shared note below the
// dialog still carries it for every other entry point).
export function showProjectSwitchError(message, path) {
  const input = $('#pmNewPath'), form = $('#pmCreate');
  if (!path || !input || !form || form.hidden) return;
  setFieldError(input, $('#pmNewPathError'), message);
  const more = $('#pmCreateMore'); if (more) more.open = true;
}

/* ---- pending switch lock (called by settings.js doSwitchProject) ---- */
const projectSwitchDisabledControls = new Map();
export function setProjectSwitchModalBusy(busy) {
  const modal = $('#projModal'); if (!modal) return;
  if (busy) {
    // Invalidate an earlier list load before it can enable replacement rows.
    projectModalLoadEpoch++;
    modal.querySelectorAll('button,input,textarea,select').forEach((control) => {
      if (!projectSwitchDisabledControls.has(control)) projectSwitchDisabledControls.set(control, control.disabled);
      control.disabled = true;
    });
    modal.setAttribute('aria-busy', 'true');
    const note = $('#pmSwitchNote'); if (note) note.tabIndex = -1;
    openModal(modal, { initialFocus: note, onEscape: () => {}, onDismiss: () => {} });
  } else {
    projectSwitchDisabledControls.forEach((disabled, control) => { control.disabled = disabled; });
    projectSwitchDisabledControls.clear();
    modal.removeAttribute('aria-busy');
    modal.querySelectorAll('[aria-busy="true"]').forEach((el) => el.removeAttribute('aria-busy'));
    restoreCreateButton();
    if (modal.style.display === 'flex') openModal(modal, { initialFocus: $('#pmFilter') });
  }
}

/* ---- wiring ---- */
function onListKey(e) {
  const node = e.target.closest('[role="treeitem"]');
  if (!node) return;
  const isGroup = node.classList.contains('pm-group');
  if (e.target !== node) return; // keys inside an inner control stay there
  if (NAV_KEYS.includes(e.key)) {
    e.preventDefault();
    const nodes = visibleNodes();
    const j = nextIndex(nodes.indexOf(node), nodes.length, e.key);
    if (j < 0) filter()?.focus(); else focusNode(nodes[j]);
    return;
  }
  if (e.key === 'ArrowRight' && isGroup) {
    e.preventDefault();
    if (node.getAttribute('aria-expanded') !== 'true') toggleGroup(node, true);
    else focusNode(node.querySelector('[role="group"] [role="treeitem"]'));
    return;
  }
  if (e.key === 'ArrowLeft') {
    e.preventDefault();
    if (isGroup) { if (node.getAttribute('aria-expanded') === 'true') toggleGroup(node, false); } else focusNode(node.closest('.pm-group'));
    return;
  }
  if (e.key === 'Enter' || e.key === ' ') {
    e.preventDefault();
    if (isGroup) toggleGroup(node); else openRow(node);
    return;
  }
  if (!isGroup && ((e.key === 'F10' && e.shiftKey) || e.key === 'ContextMenu' || e.key === 'F2')) {
    e.preventDefault();
    openMoveEditor(node);
    return;
  }
  if (e.key.length === 1 && !e.ctrlKey && !e.metaKey && !e.altKey) {
    const f = filter();
    if (f) { f.focus(); const end = f.value.length; f.setSelectionRange(end, end); }
  }
}

function onListClick(e) {
  const more = e.target.closest('.pm-row-more');
  const node = e.target.closest('[role="treeitem"]');
  if (!node) return;
  if (more) { e.stopPropagation(); setTabStop(node); openMoveEditor(node); return; }
  if (e.target.closest('.pm-group-head')) { setTabStop(node); toggleGroup(node); return; }
  if (node.classList.contains('pm-row')) { setTabStop(node); openRow(node); }
}

function onFilterKey(e) {
  if (e.key === 'ArrowDown' || e.key === 'PageDown') {
    e.preventDefault();
    const nodes = visibleNodes();
    focusNode(nodes.find((n) => n.dataset.node === activeNode) || nodes[0]);
    return;
  }
  if (e.key === 'Enter') {
    e.preventDefault();
    if (!model) return;
    const query = model.query;
    if (query && !model.items.length) { openCreate(query); return; }
    const rows = visibleNodes().filter((n) => n.classList.contains('pm-row'));
    if (query && model.items.length === 1 && rows[0]) { openRow(rows[0]); return; }
    if (rows[0]) focusNode(rows[0]);
    return;
  }
  if (e.key === 'Escape' && e.currentTarget.value) {
    e.preventDefault(); e.stopPropagation();
    clearFilter();
  }
}

function wire() {
  const el = list();
  if (!el) return;
  el.addEventListener('keydown', onListKey);
  el.addEventListener('click', onListClick);
  el.addEventListener('contextmenu', (e) => {
    const node = e.target.closest('.pm-row');
    if (!node) return;
    e.preventDefault();
    setTabStop(node); openMoveEditor(node);
  });
  const f = filter();
  if (f) {
    f.addEventListener('input', () => { activeNode = ''; paintProjectModal(); });
    f.addEventListener('keydown', onFilterKey);
  }
  $('#pmFilterClear')?.addEventListener('click', clearFilter);
  $('#pmClose')?.addEventListener('click', closePicker);
  $('#pmCopyDir')?.addEventListener('click', () => { if (cache && cache.dir) copyText(cache.dir, 'Project folder path copied'); });
  $('#pmNewToggle')?.addEventListener('click', () => openCreate((filter()?.value || '').trim() && model && !model.items.length ? filter().value.trim() : ''));
  const create = $('#pmCreate');
  if (create) {
    create.addEventListener('submit', submitCreate);
    create.addEventListener('keydown', (e) => {
      if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); closeCreate(); }
    });
    ['#pmNew', '#pmNewFolder', '#pmNewPath'].forEach((sel) => $(sel)?.addEventListener('input', () => { cfg.clearFeedback(); validateCreate(); }));
  }
  $('#pmCreateCancel')?.addEventListener('click', () => closeCreate());
  $('#pmOpenExisting')?.addEventListener('click', () => {
    const r = validateCreate({ show: false });
    if (r.existing) cfg.switchProject(r.existing.name, r.existing.path || '');
  });
  const move = $('#pmMove');
  if (move) {
    move.addEventListener('submit', saveMove);
    move.addEventListener('keydown', (e) => { if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); closeMoveEditor(); } });
    $('#pmMoveFolder')?.addEventListener('input', () => setFieldError($('#pmMoveFolder'), $('#pmMoveError'), ''));
  }
  $('#pmMoveCancel')?.addEventListener('click', () => closeMoveEditor());
}
wire();
