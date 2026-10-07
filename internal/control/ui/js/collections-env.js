// collections-env.js — environment selector, variable scope for highlighting, the env dot and the scripts chip.
import { $, api, initUiSelects } from './core.js';
import * as M from './collections-model.js';
import * as V from './varscope-model.js';
import { S, itemByUid, jget, lsGet } from './collections-core.js';

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
  initUiSelects($('#collEnvWrap'));
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
  text.textContent = n ? n + ' quarantined ' + (n === 1 ? 'script' : 'scripts') : total + ' ' + (total === 1 ? 'script' : 'scripts') + ' approved';
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

