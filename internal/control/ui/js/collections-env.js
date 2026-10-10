// collections-env.js — environment selector, variable scope for highlighting, the env dot and the scripts chip.
import { $, api, esc, initUiSelects, toast, toastError, uiConfirm, uiPrompt } from './core.js';
import { openSheet } from './sheet.js';
import * as M from './collections-model.js';
import * as V from './varscope-model.js';
import * as EM from './collections-env-model.js';
import { S, btn, el, itemByUid, jget, jsend, lsGet, lsSet } from './collections-core.js';

export function expandTo(uid) {
  let it = itemByUid(uid);
  while (it && it.parentUid) { S.expanded.add(it.parentUid); it = itemByUid(it.parentUid); }
}

export function updateBadge() {
  const dot = $('#collBadge');
  if (!dot) return;
  const n = S.scripts && S.scripts.untrusted ? S.scripts.untrusted : 0;
  dot.classList.toggle('on', n > 0);
}

/* ------------------------------------------------------------------ picker, env */

export function envsForCollection() {
  return S.envs.filter((e) => e.kind === 'env' && (!e.collectionUid || e.collectionUid === S.colUid));
}
export const globalsEnv = () => S.envs.find((e) => e.kind === 'globals') || null;

export function renderEnvSelect() {
  const sel = $('#collEnv');
  const list = envsForCollection();
  sel.textContent = '';
  sel.append(new Option('No environment', ''));
  list.forEach((e) => sel.append(new Option(e.name, e.uid)));
  const saved = S.envUid || lsGet('env');
  S.envUid = list.some((e) => e.uid === saved) ? saved : '';
  sel.value = S.envUid;
  $('#collEnvEdit').disabled = !S.collection;
  // A collection with no environment must not dead-end: offer to create one right here.
  const create = $('#collEnvNew');
  const manage = $('#collEnvManage');
  if (create) { create.hidden = !(S.collection && !list.length); create.onclick = () => createEnv(); }
  if (manage) { manage.disabled = !S.collection; manage.onclick = () => openEnvManager(); }
  initUiSelects($('#collEnvWrap'));
}

/* ------------------------------------------------------------------ environment CRUD */

// select makes uid the active environment through the picker's own change handler, so the
// variable scope, the dot and the resolve preview all refresh the way a manual pick does.
function selectEnv(uid) {
  S.envUid = uid || '';
  lsSet('env', S.envUid);
  renderEnvSelect();
  $('#collEnv').dispatchEvent(new Event('change'));
}

async function afterEnvChange(uid) {
  await reloadEnvs();
  selectEnv(uid === undefined ? S.envUid : uid);
  paintEnvManager();
}

export async function createEnv() {
  if (!S.collection) { toast('Create or import a collection first'); return; }
  const name = await uiPrompt({ title: 'New environment', placeholder: 'e.g. Staging' });
  if (name == null) return;
  const bad = EM.envNameError(name, envsForCollection());
  if (bad) { toast(bad, 'error'); return; }
  try {
    const e = await jsend('POST', '/api/environments', EM.createBody(name, S.colUid));
    await afterEnvChange(e.uid);
    toast('Created environment "' + e.name + '"');
  } catch (err) { toastError('Could not create the environment', err); }
}

async function renameEnv(env) {
  const name = await uiPrompt({ title: 'Rename environment', value: env.name });
  if (name == null || name === env.name) return;
  const bad = EM.envNameError(name, envsForCollection().filter((e) => e.uid !== env.uid));
  if (bad) { toast(bad, 'error'); return; }
  try {
    await jsend('PUT', '/api/environments/' + encodeURIComponent(env.uid), EM.renameBody(env, name));
    await afterEnvChange();
    toast('Renamed');
  } catch (err) { toastError('Could not rename the environment', err); }
}

async function duplicateEnv(env) {
  const name = await uiPrompt({ title: 'Duplicate environment', value: EM.copyName(env.name, envsForCollection()) });
  if (name == null) return;
  const bad = EM.envNameError(name, envsForCollection());
  if (bad) { toast(bad, 'error'); return; }
  try {
    const e = await jsend('POST', '/api/environments', EM.duplicateBody(env, name, S.colUid));
    await afterEnvChange(e.uid);
    toast('Duplicated. Secret and current values are not copied.');
  } catch (err) { toastError('Could not duplicate the environment', err); }
}

async function deleteEnv(env) {
  const w = EM.deleteWarning(env);
  const ok = await uiConfirm('Delete environment', 'Environment <b>' + esc(w.name) + '</b>' + esc(w.tail), 'Delete', 'btn danger');
  if (!ok) return;
  try {
    await jsend('DELETE', '/api/environments/' + encodeURIComponent(env.uid));
    await afterEnvChange(S.envUid === env.uid ? '' : undefined);
    toast('Deleted "' + w.name + '"');
  } catch (err) { toastError('Could not delete the environment', err); }
}

let managerBody = null;

export function openEnvManager() {
  if (!S.collection) { toast('Create or import a collection first'); return; }
  openSheet({ id: 'collEnvSheet', title: 'Environments', detents: ['half', 'full'], detent: 'half', opener: $('#collEnvManage'), onClose: () => { managerBody = null; }, content: (body) => { managerBody = body; paintEnvManager(); return body; } });
}

function paintEnvManager() {
  const body = managerBody;
  if (!body || !body.isConnected) return;
  body.textContent = '';
  const wrap = el('div', 'coll-sheet coll-envmgr');
  body.append(wrap);
  wrap.append(el('h3', '', 'Environments for "' + (S.collection.name || 'this collection') + '"'));
  wrap.append(el('p', 'coll-note', 'An environment holds the values that {{variables}} in requests resolve to, such as {{baseUrl}}. Duplicating copies variable names and shared initial values, never secrets or current values.'));
  const list = envsForCollection();
  if (!list.length) wrap.append(el('p', 'coll-note', 'No environments yet. Create one, then add its variables with the Variables button.'));
  const ul = el('div', 'coll-envmgr-list');
  ul.setAttribute('role', 'list');
  list.forEach((e) => {
    const row = el('div', 'coll-envmgr-row');
    row.setAttribute('role', 'listitem');
    const active = e.uid === S.envUid;
    row.append(el('span', 'coll-name', e.name), el('span', 'coll-meta', (active ? 'Active · ' : '') + (e.variables || []).length + ' variable' + ((e.variables || []).length === 1 ? '' : 's')));
    const acts = el('span', 'coll-envmgr-acts');
    if (!active) acts.append(btn('Use', () => { selectEnv(e.uid); paintEnvManager(); }, 'btn xs'));
    acts.append(btn('Rename', () => renameEnv(e), 'btn xs', 'edit'), btn('Duplicate', () => duplicateEnv(e), 'btn xs', 'copy'), btn('Delete', () => deleteEnv(e), 'btn xs', 'trash'));
    row.append(acts);
    ul.append(row);
  });
  wrap.append(ul);
  wrap.append(btn('New environment', () => createEnv(), 'btn accent', 'plus'));
}

export async function refreshScope() {
  const layers = [];
  const env = S.envs.find((e) => e.uid === S.envUid);
  if (env) layers.push({ scope: 'environment', label: env.name, vars: env.variables || [] });
  const g = globalsEnv();
  if (g) layers.push({ scope: 'global', vars: g.variables || [] });
  if (S.colUid) {
    const cv = await jget('/api/variables/collection/' + encodeURIComponent(S.colUid)).catch(() => null);
    if (cv) layers.push({ scope: 'collection', vars: cv.variables || [] });
  }
  S.scope = V.buildScope(layers);
  paintEnvDot();
}

export function paintEnvDot(unresolved = null) {
  const dot = $('#collEnvDot');
  if (!dot) return;
  const env = S.envs.find((e) => e.uid === S.envUid);
  const host = S.ed ? M.hostOf(S.ed.url) : '';
  const pinBroken = !!(env && env.baseTargetPin && host && !V.pinMatches(env.baseTargetPin, host));
  const n = unresolved == null ? (S.ed ? V.unresolvedIn(S.ed.url, S.scope).length : 0) : unresolved;
  const st = V.envDot({ hasEnv: !!env, unresolved: n, pinBroken });
  dot.dataset.state = st.state;
  dot.setAttribute('aria-label', st.label);
}


export function renderScriptsChip() {
  const chip = $('#collScripts');
  const text = $('#collScriptsText');
  const n = S.scripts && S.scripts.untrusted ? S.scripts.untrusted : 0;
  const total = S.scripts && S.scripts.scripts ? S.scripts.scripts.length : 0;
  chip.hidden = !total;
  chip.dataset.state = n ? 'warn' : 'ok';
  const count = n || total;
  const word = (count === 1 ? 'script' : 'scripts') + (n ? ' quarantined' : ' approved');
  // The long wording hides on phones (shared .lbl-long rule); the count and the accessible name stay.
  text.textContent = '';
  const num = document.createElement('span');
  num.textContent = String(count);
  const long = document.createElement('span');
  long.className = 'lbl-long';
  long.textContent = ' ' + word;
  text.append(num, long);
  chip.setAttribute('aria-label', count + ' ' + word + '. Review scripts');
}

/* ------------------------------------------------------------------ send */

export async function reloadEnvs() {
  try { const envs = await jget('/api/environments'); S.envs = envs.environments || []; await refreshScope(); } catch (e) { /* ignore */ }
}


export function setPhone(v) {
  S.phone = v;
  $('#collMain').dataset.view = v;
  $('#collPhoneSeg').querySelectorAll('button').forEach((b) => { const on = b.dataset.v === v; b.classList.toggle('on', on); b.setAttribute('aria-pressed', on ? 'true' : 'false'); });
}

/* ------------------------------------------------------------------ boot */

export function selectedFolder() {
  const it = itemByUid(S.selUid);
  if (!it) return '';
  return it.kind === 'folder' ? it.uid : it.parentUid || '';
}

