// abilities.js — Copy Abilities manager (#panel-abilities) and public host API.
// Slot lifecycle and UI surface injection live in abilities-host.js.

import { $, $$, esc, api, toast, toastError, uiConfirm, wireRowKey, setSeg, openFlow, copyText } from './core.js';
import {
  ABILITY_GROUPS, isAbilityRunnable, abilityStatusBadge, abilitySummaryBadge,
  abilityTrustBanner, groupLabel, shortDigest, abilityCapabilities,
  surfaceSummaryCounts, buildSurfaceSections, filterAbilities,
  suggestAbilityId, validateScaffoldInput, extractAbilitiesList, collectInjectedMenus,
} from './abilities-model.js';
import { registerCommand, registerSseHandler, TAB_CHANGE_EVENT, getShellApi } from './shell-hooks.js';
import {
  h, svgEl, iconNode, btnWithIcon, postJson, openAbilityPanel, configureAbilityHost,
  mountAbilityPanel, syncInjectedMenus, syncInjectedCommands,
  abilityFlowContextItems, abilityInspectorTabs,
} from './abilities-host.js';

export { mountAbilityPanel, abilityFlowContextItems, abilityInspectorTabs };

const SENSITIVE_CAPS = new Set(['flow.hook', 'mcp.tool', 'ability.action']);

let abilities = [];
let loaded = false, loading = false, loadError = null, loadEpoch = 0, projectScopeAvailable = true;
let selectedId = '', searchQuery = '', statusFilter = 'all', scaffoldOpen = false, idManuallyEdited = false;
let sseWired = false, tabWired = false, domBuilt = false;

let hostCtx = {
  activateTab: (tabId) => getShellApi().activateTab?.(tabId),
  openFlow: (flowId, opts) => openFlow(flowId, opts),
  openInspectorTab: null,
  getSelectedFlow: () => null,
  onInjectedMenusChanged: null,
  registerCommand,
  registerSseHandler,
  handlesTabChange: false,
};

configureAbilityHost({
  getAbilities: () => abilities,
  isLoaded: () => loaded,
  isLoading: () => loading,
  loadAbilities: () => loadAbilities(),
  getHostCtx: () => hostCtx,
});

const createKirbyCrest = () => svgEl('svg', { class: 'ab-crest', viewBox: '0 0 24 24', 'aria-hidden': 'true', focusable: 'false' },
  svgEl('path', { class: 'ab-crest-fill', d: 'M5 17L4 8l5 3 3-6 3 6 5-3-1 9z' }),
  svgEl('path', { d: 'M5 17L4 8l5 3 3-6 3 6 5-3-1 9zM5 20h14' }),
  svgEl('path', { d: 'M12 11l2 2-2 2-2-2z' }));

const createStatusPill = (b) => h('span', { cls: 'ab-kirby-pill', dataset: { tone: b.tone } }, createKirbyCrest(), b.label);

/* ---- Configure -> Abilities Management UI ---- */

function renderListState(host, kind, opts = {}) {
  const isErr = kind === 'error';
  host.replaceChildren(h('div', isErr ? 'state-error state-panel' : 'state-empty state-panel',
    h('div', isErr ? 'state-error-icon' : 'state-empty-icon', iconNode(opts.icon || 'abilities')),
    h('h3', { cls: 'state-empty-title state-panel-title', text: opts.title || 'No abilities' }),
    opts.hint ? h('p', { cls: 'state-empty-hint', text: opts.hint }) : null,
    opts.actionLabel && opts.onAction ? btnWithIcon(opts.actionLabel, opts.actionIcon || '', 'btn', { on: { click: opts.onAction } }) : null,
  ));
}

function renderList() {
  const listHost = $('#abList');
  if (!listHost) return;
  if (loading && !loaded) { listHost.replaceChildren(h('p', { cls: 'ab-panel-loading', text: 'Loading Copy Abilities…' })); return; }
  if (loadError && !loaded) {
    renderListState(listHost, 'error', {
      icon: 'alert', title: 'Could not load Copy Abilities', hint: loadError.message || 'Request failed',
      actionLabel: 'Retry', actionIcon: 'refresh', onAction: () => void loadAbilities(),
    });
    return;
  }
  const filtered = filterAbilities(abilities, searchQuery, statusFilter);
  if (!abilities.length) {
    renderListState(listHost, 'empty-first', {
      icon: 'abilities', title: 'No Copy Abilities installed', actionLabel: 'Create ability', actionIcon: 'plus', onAction: () => toggleScaffold(true),
      hint: 'Install a .tar.gz or .zip bundle, or scaffold a starter ability to inject menus, inspector tabs, and Starlark hooks.',
    });
    return;
  }
  if (!filtered.length) {
    renderListState(listHost, 'empty-filtered', {
      icon: 'search', title: 'No matching abilities', hint: 'Clear your search or status filter to see all installed abilities.',
      actionLabel: 'Clear filters', actionIcon: 'close', onAction: () => { searchQuery = ''; statusFilter = 'all'; const i = $('#abSearch'); if (i) i.value = ''; renderPanel(); },
    });
    return;
  }

  listHost.replaceChildren(...filtered.map((ab) => {
    const m = ab.manifest || {};
    const card = h('div', {
      cls: 'ab-card', dataset: { id: ab.id }, attrs: { 'aria-selected': String(ab.id === selectedId) },
      on: { click: () => { selectedId = ab.id; renderPanel(); } },
    },
      h('div', 'ab-card-top', h('span', { cls: 'ab-card-name', text: m.name || ab.id }), createStatusPill(abilityStatusBadge(ab))),
      h('div', 'ab-card-meta', h('span', { text: ab.id }), m.version ? h('span', { text: 'v' + m.version }) : null, m.author ? h('span', { text: 'by ' + m.author }) : null),
      m.description ? h('p', { cls: 'ab-card-desc', text: m.description }) : null,
      h('div', 'ab-card-counts',
        h('span', { cls: ab.scope === 'project' ? 'ab-chip is-scope-project' : 'ab-chip', text: ab.scope || 'project' }),
        ...surfaceSummaryCounts(m).map((c) => h('span', { cls: 'ab-chip', text: c })),
      ),
    );
    wireRowKey(card, () => { selectedId = ab.id; renderPanel(); });
    return card;
  }));
}

function renderDetail() {
  const host = $('#abDetail');
  if (!host) return;
  const ab = abilities.find((a) => a.id === selectedId) || null;
  if (!ab) {
    renderListState(host, 'empty-first', {
      icon: 'abilities', title: abilities.length ? 'Select an ability' : 'Quarantine-first extension host',
      hint: 'Review manifest permissions, SHA-256 digests, and Starlark scripts before approving any ability.',
    });
    return;
  }

  const m = ab.manifest || {}, runnable = isAbilityRunnable(ab);
  const actions = h('div', 'ab-detail-actions');
  if ((!ab.trusted || ab.drift) && !ab.loadError) actions.appendChild(btnWithIcon('Approve & Enable', 'check', 'btn btn-primary', { on: { click: () => void approveAndEnable(ab) } }));
  if (ab.trusted && !ab.drift && !ab.loadError) actions.appendChild(btnWithIcon(ab.enabled ? 'Disable' : 'Enable', ab.enabled ? 'lock' : 'lock-open', ab.enabled ? 'btn' : 'btn btn-primary', { on: { click: () => void toggleAbility(ab) } }));
  if (runnable && m.menus?.length) actions.appendChild(btnWithIcon('Open panel', 'external', 'btn', { on: { click: () => openAbilityPanel(ab.id, m.menus[0].id) } }));
  actions.append(
    h('a', { cls: 'btn u-no-underline', href: '/api/abilities/' + encodeURIComponent(ab.id) + '/export', attrs: { download: ab.id + '.tar.gz' } }, iconNode('download'), 'Export .tar.gz'),
    btnWithIcon('Remove', 'trash', 'btn btn-danger', { on: { click: () => void removeAbility(ab) } }),
  );

  const ban = abilityTrustBanner(ab);
  const dl = h('dl', 'ab-facts');
  const addFact = (k, v) => dl.append(h('dt', { text: k }), typeof v === 'string' ? h('dd', { text: v }) : v);
  addFact('ID', ab.id); addFact('Version', m.version || '—'); addFact('Author', m.author || '—'); addFact('Scope', ab.scope || 'project');
  if (ab.dir) addFact('Directory', ab.dir);
  addFact('SHA-256', h('dd', null, h('code', { cls: 'ab-digest-code', text: ab.digest || '—' }),
    ab.digest ? btnWithIcon('Copy', 'copy', 'btn xs', { title: shortDigest(ab.digest), on: { click: () => copyText(ab.digest, 'SHA-256 digest copied') } }) : null));
  if (ab.trustedDigest && ab.trustedDigest !== ab.digest) addFact('Trusted SHA-256', h('dd', null, h('code', { cls: 'ab-digest-code', text: ab.trustedDigest })));

  const caps = abilityCapabilities(ab), sections = buildSurfaceSections(ab);
  host.replaceChildren(h('article', 'ab-detail',
    h('header', 'ab-detail-head',
      h('div', 'ab-detail-title-row',
        h('h3', { cls: 'ab-detail-title', text: m.name || ab.id }), createStatusPill(abilityStatusBadge(ab)),
        h('span', { cls: ab.scope === 'project' ? 'ab-chip is-scope-project' : 'ab-chip', text: ab.scope || 'project' }),
        ab.overridesGlobal ? h('span', { cls: 'ab-chip', text: 'overrides global' }) : null,
      ),
      m.description ? h('p', { cls: 'ab-detail-desc', text: m.description }) : null, actions,
    ),
    ban ? h('div', { cls: 'ab-banner', dataset: { kind: ban.kind }, attrs: ban.kind === 'err' ? { role: 'alert' } : {} }, iconNode(ban.icon), h('span', { text: ban.text })) : null,
    h('section', 'ab-section', h('h4', { cls: 'ab-section-title', text: 'Package & Integrity' }), dl),
    h('section', 'ab-section', h('h4', { cls: 'ab-section-title', text: 'Declared Capabilities' }),
      caps.length ? h('div', 'ab-chips', ...caps.map((c) => h('span', { cls: SENSITIVE_CAPS.has(c) ? 'ab-chip is-cap-sensitive' : 'ab-chip', text: c })))
        : h('p', { cls: 'ab-empty-inline', text: 'No capabilities declared.' })),
    h('section', 'ab-section', h('h4', { cls: 'ab-section-title', text: 'Injected Surfaces & Scripts' }),
      ...(sections.length ? sections.map((sec) => h('div', 'ab-surface-group',
        h('div', { cls: 'ab-surface-label', text: sec.heading }),
        h('ul', 'ab-surface-list', ...sec.rows.map((r) => h('li', 'ab-surface-item',
          h('div', 'ab-surface-main', h('span', { cls: 'ab-surface-title', text: r.title }), h('span', { cls: 'ab-surface-sub', text: r.sub })),
          r.badge ? h('span', { cls: r.badgeClass || 'ab-chip', text: r.badge }) : null,
          r.menuId && runnable ? btnWithIcon('Open', 'external', 'btn xs', { on: { click: () => openAbilityPanel(ab.id, r.menuId) } }) : null,
        ))),
      )) : [h('p', { cls: 'ab-empty-inline', text: 'No menus, tabs, hooks, or actions declared in ability.json.' })])),
  ));
}

function renderPanel() {
  const sum = $('#abCountSummary');
  if (sum) sum.replaceChildren(createStatusPill(abilitySummaryBadge(abilities)));
  $$('#abFilterSeg button[data-filter]').forEach((btn) => setSeg(btn, btn.dataset.filter === statusFilter));
  if (selectedId && !abilities.some((a) => a.id === selectedId)) selectedId = '';
  if (!selectedId && abilities.length) selectedId = (filterAbilities(abilities, searchQuery, statusFilter)[0] || abilities[0]).id;
  renderList();
  renderDetail();
}

/* ---- Mutating UI Actions ---- */

async function approveAndEnable(ab) {
  const name = ab.manifest?.name || ab.id, caps = abilityCapabilities(ab);
  const msg = 'Approve and enable <b>' + esc(name) + '</b> (<code>' + esc(ab.id) + '</code>)?<br>' +
    'Ability UI code runs with the same access as Interseptor itself, including captured data and control actions. Approve only authors and files you trust.<br>' +
    'Scope: <code>' + esc(ab.scope) + '</code><br>SHA-256: <code>' + esc(ab.digest) + '</code>' + (caps.length ? '<br>Declared features: <code>' + esc(caps.join(', ')) + '</code>' : '');
  if (!(await uiConfirm('Approve & Enable Copy Ability', msg, 'Approve & Enable', 'btn accent'))) return;
  try { await postJson('/api/abilities/' + encodeURIComponent(ab.id) + '/trust', { digest: ab.digest, scope: ab.scope, enabled: true }); toast(name + ' approved and enabled', 'success'); }
  catch (err) { toastError('Approve failed', err); }
  await loadAbilities();
}

async function toggleAbility(ab) {
  const next = !ab.enabled;
  try { await postJson('/api/abilities/' + encodeURIComponent(ab.id) + '/toggle', { enabled: next, digest: ab.digest, scope: ab.scope }); toast((ab.manifest?.name || ab.id) + (next ? ' enabled' : ' disabled'), 'success'); await loadAbilities(); }
  catch (err) { toastError('Toggle failed', err); }
}

async function removeAbility(ab) {
  const name = ab.manifest?.name || ab.id;
  const msg = 'Permanently delete <b>' + esc(name) + '</b> (<code>' + esc(ab.id) + '</code>) from ' + esc(ab.scope || 'project') + ' abilities?<br>SHA-256: <code>' + esc(shortDigest(ab.digest) || '—') + '</code>';
  if (!(await uiConfirm('Remove Copy Ability', msg, 'Remove', 'btn danger'))) return;
  try { await api('/api/abilities/' + encodeURIComponent(ab.id) + '?scope=' + encodeURIComponent(ab.scope || '') + '&digest=' + encodeURIComponent(ab.digest || ''), { method: 'DELETE' }); if (selectedId === ab.id) selectedId = ''; toast(name + ' removed', 'success'); await loadAbilities(); }
  catch (err) { toastError('Remove failed', err); }
}

async function installArchiveFile(file) {
  if (!file) return;
  const url = '/api/abilities/install?scope=' + encodeURIComponent($('#abInstallScope')?.value || (projectScopeAvailable ? 'project' : 'global'));
  try {
    let res;
    try { res = await api(url, { method: 'POST', body: file }); }
    catch (err) { if (!/multipart/i.test(err?.message || '')) throw err; const fd = new FormData(); fd.append('file', file); res = await api(url, { method: 'POST', body: fd }); }
    const inst = res?.ability || res;
    if (inst?.id) selectedId = inst.id;
    toast('Installed ' + (inst?.id || file.name) + ' (quarantined until approved)', 'success');
    await loadAbilities();
  } catch (err) { toastError('Install failed', err); }
}

async function reloadFromDisk() {
  const btn = $('#abReloadBtn'); if (btn) btn.disabled = true;
  try { await api('/api/abilities/reload', { method: 'POST' }); await loadAbilities(); toast('Abilities reloaded', 'success'); }
  catch (err) { toastError('Reload failed', err); }
  finally { if (btn) btn.disabled = false; }
}

function toggleScaffold(open) {
  scaffoldOpen = typeof open === 'boolean' ? open : !scaffoldOpen;
  const card = $('#abScaffoldCard'); if (!card) return;
  card.hidden = !scaffoldOpen;
  $('#abCreateBtn')?.setAttribute('aria-expanded', String(scaffoldOpen));
  if (scaffoldOpen) { const err = $('#abScafError'); if (err) err.textContent = ''; $('#abScafName')?.focus(); }
  else $('#abCreateBtn')?.focus();
}

async function submitScaffoldForm(e) {
  e.preventDefault();
  const errEl = $('#abScafError'), check = validateScaffoldInput({
    id: $('#abScafId')?.value, name: $('#abScafName')?.value, group: $('#abScafGroup')?.value, scope: $('#abScafScope')?.value, description: $('#abScafDesc')?.value,
  });
  if (!check.ok) { if (errEl) errEl.textContent = check.error; return; }
  if (errEl) errEl.textContent = '';
  const submitBtn = $('#abScafSubmit'); if (submitBtn) submitBtn.disabled = true;
  try {
    const res = await postJson('/api/abilities/scaffold', check.normalized), created = res?.ability || res;
    selectedId = created?.id || check.normalized.id;
    ['#abScafName', '#abScafId', '#abScafDesc'].forEach((s) => { const n = $(s); if (n) n.value = ''; });
    idManuallyEdited = false; toggleScaffold(false);
    toast('Created ' + check.normalized.name + ' (quarantined until approved)', 'success');
    await loadAbilities();
  } catch (err) { if (errEl) errEl.textContent = err?.message || 'Could not create ability'; }
  finally { if (submitBtn) submitBtn.disabled = false; }
}

/* ---- Build #abilitiesRoot inside #panel-abilities ---- */

function syncScopeSelectors() {
  for (const sel of [$('#abInstallScope'), $('#abScafScope')]) {
    if (!sel) continue;
    const projOpt = sel.querySelector('option[value="project"]');
    if (projOpt) projOpt.disabled = !projectScopeAvailable;
    if (!projectScopeAvailable && sel.value === 'project') sel.value = 'global';
  }
}

function buildShellDom() {
  const panel = $('#panel-abilities'); if (!panel) return false;
  let root = $('#abilitiesRoot');
  if (!root) { root = h('div', { cls: 'ab-shell', id: 'abilitiesRoot' }); panel.appendChild(root); } else root.classList.add('ab-shell');
  if (domBuilt && root.querySelector('#abList')) return true;

  const mkField = (id, label, node, cls = 'ab-field') => { node.id = id; return h('div', cls, h('label', { text: label, attrs: { for: id } }), node); };
  const nameInp = h('input', { placeholder: 'Header Audit', required: true }), idInp = h('input', { placeholder: 'header-audit', required: true });
  const grpSel = h('select', null, ...ABILITY_GROUPS.map((g) => h('option', { value: g, text: groupLabel(g), selected: g === 'abilities' })));
  const scafScope = h('select', null, ...[['project', 'Project'], ['global', 'Global']].map(([v, t]) => h('option', { value: v, text: t })));
  const descInp = h('textarea', { placeholder: 'Short summary of what this ability inspects or automates…' });
  nameInp.addEventListener('input', () => { if (!idManuallyEdited) idInp.value = suggestAbilityId(nameInp.value); });
  idInp.addEventListener('input', () => { idManuallyEdited = idInp.value.trim().length > 0; });

  const searchInp = h('input', { cls: 'ab-search', id: 'abSearch', type: 'search', placeholder: 'Filter by id, name, author…', attrs: { 'aria-label': 'Filter abilities' },
    on: { input: () => { searchQuery = searchInp.value; renderPanel(); }, keydown: (e) => { if (e.key === 'Escape' && searchInp.value) { e.preventDefault(); searchInp.value = ''; searchQuery = ''; renderPanel(); } } } });
  const installFile = h('input', { type: 'file', id: 'abInstallFile', cls: 'ab-file-input', accept: '.tar.gz,.tgz,.zip', attrs: { 'aria-label': 'Select ability archive' },
    on: { change: (e) => { const f = e.target.files?.[0]; e.target.value = ''; if (f) void installArchiveFile(f); } } });

  root.replaceChildren(
    h('header', 'ab-header',
      h('div', 'ab-header-brand', h('div', 'ab-emblem', createKirbyCrest()), h('div', 'ab-title-wrap',
        h('h2', { cls: 'ab-title', text: 'Copy Abilities' }),
        h('p', { cls: 'ab-subtitle', text: 'Extend Interseptor with menus, inspector tabs, context actions, Starlark hooks, and MCP actions. Installed bundles start quarantined until a human approves them.' }))),
      h('div', { id: 'abCountSummary' }),
    ),
    h('div', 'toolbar ab-toolbar',
      h('div', 'search ab-search-wrap', iconNode('search'), searchInp),
      h('div', { cls: 'seg ab-filters', id: 'abFilterSeg', attrs: { role: 'group', 'aria-label': 'Filter abilities by status' } },
        ...[['all', 'All'], ['active', 'Active'], ['quarantined', 'Quarantined'], ['drift', 'Drifted']].map(([val, label]) =>
          h('button', { type: 'button', cls: val === 'all' ? 'on' : '', text: label, dataset: { filter: val }, attrs: { 'aria-pressed': String(val === 'all') },
            on: { click: () => { statusFilter = val; renderPanel(); } } }))),
      h('div', 'spacer'),
      h('div', 'ab-actions-bar',
        btnWithIcon('Reload', 'refresh', 'btn', { id: 'abReloadBtn', on: { click: () => void reloadFromDisk() } }),
        h('select', { cls: 'btn btn-field ab-scope-select', id: 'abInstallScope', attrs: { 'aria-label': 'Install target scope' } },
          h('option', { value: 'project', text: 'Project scope' }), h('option', { value: 'global', text: 'Global scope' })),
        installFile,
        btnWithIcon('Install (.tar.gz / .zip)', 'archive', 'btn', { id: 'abInstallBtn', on: { click: () => installFile.click() } }),
        btnWithIcon('Create ability', 'plus', 'btn btn-primary', { id: 'abCreateBtn', attrs: { 'aria-expanded': 'false', 'aria-controls': 'abScaffoldCard' }, on: { click: () => toggleScaffold() } }),
      ),
    ),
    h('section', { cls: 'ab-scaffold-card', id: 'abScaffoldCard', hidden: true, attrs: { 'aria-label': 'Create starter Copy Ability' },
      on: { keydown: (e) => { if (e.key === 'Escape') { e.stopPropagation(); toggleScaffold(false); } } } },
      h('div', 'ab-scaffold-head', h('h3', { cls: 'ab-scaffold-title', text: 'Scaffold Starter Copy Ability' }), btnWithIcon('Cancel', 'close', 'btn xs', { on: { click: () => toggleScaffold(false) } })),
      h('form', { cls: 'ab-scaffold-form', on: { submit: submitScaffoldForm } },
        h('div', 'ab-scaffold-grid', mkField('abScafName', 'Display name', nameInp), mkField('abScafId', 'Ability ID', idInp), mkField('abScafGroup', 'Sidebar group', grpSel), mkField('abScafScope', 'Scope', scafScope), mkField('abScafDesc', 'Description (optional)', descInp, 'ab-field ab-field-wide')),
        h('div', 'ab-scaffold-foot', h('p', { cls: 'ab-form-error', id: 'abScafError', attrs: { role: 'alert' } }), h('button', { type: 'submit', cls: 'btn btn-primary', id: 'abScafSubmit', text: 'Create starter ability' })),
      ),
    ),
    h('div', 'ab-workspace', h('div', { cls: 'ab-list-col', id: 'abList', attrs: { role: 'listbox', 'aria-label': 'Installed Copy Abilities' } }), h('div', { cls: 'ab-detail-col', id: 'abDetail' })),
  );
  domBuilt = true;
  syncScopeSelectors();
  return true;
}

/* ---- Public Host API ---- */

/** Fetches `/api/abilities`, updates the management panel, and syncs all UI injection surfaces. */
export async function loadAbilities() {
  const epoch = ++loadEpoch; loading = true; loadError = null;
  buildShellDom(); if (!loaded) renderPanel();
  try {
    const data = await api('/api/abilities');
    if (epoch !== loadEpoch) return abilities;
    if (typeof data?.projectScopeAvailable === 'boolean') projectScopeAvailable = data.projectScopeAvailable;
    abilities = extractAbilitiesList(data); loaded = true; loadError = null;
  } catch (err) {
    if (epoch !== loadEpoch) return abilities;
    loadError = err; if (!loaded) abilities = [];
  } finally { if (epoch === loadEpoch) loading = false; }

  syncScopeSelectors();
  const menus = collectInjectedMenus(abilities);
  renderPanel(); syncInjectedMenus(menus); syncInjectedCommands(abilities, menus);
  hostCtx.onInjectedMenusChanged?.(menus);
  if (typeof document !== 'undefined' && typeof globalThis.CustomEvent === 'function') {
    document.dispatchEvent(new globalThis.CustomEvent('interseptor:abilitiesupdate', { detail: { abilities, menus } }));
  }
  return abilities;
}

/** Handles `ability.update` SSE notifications by reloading ability manifests and state. */
export function handleAbilitySSE(msg) {
  if (!msg || !msg.type || msg.type === 'ability.update') void loadAbilities();
}

/** Initializes the Copy Abilities frontend host and wires SSE + tab navigation hooks. */
export async function initAbilities(host = {}) {
  hostCtx = {
    activateTab: host?.activateTab || ((tabId) => getShellApi().activateTab?.(tabId)),
    openFlow: host?.openFlow || ((flowId, opts) => openFlow(flowId, opts)),
    openInspectorTab: host?.openInspectorTab || null,
    getSelectedFlow: host?.getSelectedFlow || (() => null),
    onInjectedMenusChanged: host?.onInjectedMenusChanged || null,
    registerCommand: host?.registerCommand || registerCommand,
    registerSseHandler: host?.registerSseHandler || registerSseHandler,
    handlesTabChange: Boolean(host?.handlesTabChange),
  };
  buildShellDom();
  if (!sseWired) { sseWired = true; hostCtx.registerSseHandler('ability.update', handleAbilitySSE); }
  if (!tabWired) {
    tabWired = true;
    document.addEventListener(TAB_CHANGE_EVENT, (e) => {
      const tab = e.detail?.tab;
      if (tab === 'abilities') {
        buildShellDom();
        if (!hostCtx.handlesTabChange && !loaded && !loading) void loadAbilities();
        else renderPanel();
      } else if (!hostCtx.handlesTabChange && typeof tab === 'string' && tab.startsWith('ability:')) {
        void mountAbilityPanel(tab);
      }
    });
  }
  return loadAbilities();
}
