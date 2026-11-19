// abilities-host.js — Copy Abilities slot lifecycle, SDK context, error
// boundaries, and dynamic UI injection (menus, canvas panels, command palette,
// flow context menus, and inspector tabs). Imports only core.js,
// abilities-model.js, and shell-hooks.js to keep the module graph acyclic.

import { $, $$, api, esc, toast, toastError, icon, openFlow } from './core.js';
import {
  isAbilityRunnable, abilityPanelId, abilityDomId, groupLabel, abilityAssetUrl,
  sanitizeIconName, flowActionInput, collectInjectedMenus,
  collectInjectedContextItems, collectInjectedInspectorTabs, collectInjectedCommands,
} from './abilities-model.js';
import { registerCommand, registerSseHandler, getShellApi } from './shell-hooks.js';

const SVG_NS = 'http://www.w3.org/2000/svg';
const FALLBACK_PANEL_TAB = 'proxy';
const VALID_SIDES = new Set(['both', 'req', 'res']);

let hostBridge = {
  getAbilities: () => [],
  isLoaded: () => false,
  isLoading: () => false,
  loadAbilities: async () => [],
  getHostCtx: () => ({
    activateTab: (tabId) => getShellApi().activateTab?.(tabId),
    openFlow: (flowId, opts) => openFlow(flowId, opts),
    openInspectorTab: null,
    getSelectedFlow: () => null,
    onInjectedMenusChanged: null,
    registerCommand,
    registerSseHandler,
  }),
};

const mountedPanels = new Map();
const pendingPanelContext = new Map();
const mountedInspectors = new Map();
let cmdUnregisters = [];

/** Connects the host runtime to the manager module's live ability state. */
export function configureAbilityHost(bridge = {}) {
  hostBridge = { ...hostBridge, ...bridge };
}

const getAbilities = () => (typeof hostBridge.getAbilities === 'function' ? hostBridge.getAbilities() : []);
const getCtx = () => (typeof hostBridge.getHostCtx === 'function' ? hostBridge.getHostCtx() : {});
const sameRevision = (rec, ab) => Boolean(rec && ab && rec.abilityId === ab.id && rec.digest === ab.digest && rec.scope === ab.scope && rec.dir === ab.dir);
const isLiveAbility = (ab) => getAbilities().some((a) => isAbilityRunnable(a) && a.id === ab.id && a.digest === ab.digest && a.scope === ab.scope && a.dir === ab.dir);

export function h(tag, props, ...kids) {
  const n = document.createElement(tag);
  if (typeof props === 'string') { if (props) n.className = props; }
  else if (props) {
    if (props.cls) n.className = props.cls;
    if (props.id) n.id = props.id;
    if (props.text != null) n.textContent = String(props.text);
    for (const k of ['type', 'role', 'href', 'accept', 'placeholder', 'title', 'value', 'disabled', 'hidden', 'required', 'selected']) {
      if (props[k] !== undefined) n[k] = props[k];
    }
    if (props.dataset) Object.assign(n.dataset, props.dataset);
    if (props.attrs) for (const [k, v] of Object.entries(props.attrs)) if (v != null) n.setAttribute(k, String(v));
    if (props.on) for (const [ev, fn] of Object.entries(props.on)) if (typeof fn === 'function') n.addEventListener(ev, fn);
  }
  for (const k of kids) if (k != null && k !== false) n.appendChild(typeof k === 'object' ? k : document.createTextNode(String(k)));
  return n;
}

export function svgEl(tag, attrs = {}, ...kids) {
  const n = document.createElementNS(SVG_NS, tag);
  for (const [k, v] of Object.entries(attrs)) if (v != null) n.setAttribute(k, String(v));
  if (kids.length) n.append(...kids);
  return n;
}

export const cssAttr = (v) => (typeof globalThis.CSS?.escape === 'function' ? globalThis.CSS.escape(String(v)) : String(v).replace(/["\\]/g, '\\$&'));
export const resolveSprite = (raw) => {
  const c = sanitizeIconName(raw);
  return c && document.getElementById('i-' + c) ? c : (document.getElementById('i-abilities') ? 'abilities' : 'codec');
};
export const iconNode = (name, cls = '') => svgEl('svg', { class: cls ? 'icon ' + cls : 'icon', 'aria-hidden': 'true', focusable: 'false' }, svgEl('use', { href: '#i-' + name }));
export const btnWithIcon = (label, ico, cls = 'btn', props = {}) => h('button', { type: 'button', cls, ...props }, ico ? iconNode(ico) : null, label);
export const postJson = (path, body) => api(path, { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify(body || {}) });
export const runAbilityAction = (id, actionId, input) => postJson('/api/abilities/' + encodeURIComponent(id) + '/actions/' + encodeURIComponent(actionId), input);
export const openAbilityPanel = (id, menuId) => { const pk = abilityPanelId(id, menuId); navigateToPanel(pk); void mountAbilityPanel(pk); };

export function navigateToPanel(panelKey) {
  if (!panelKey) return;
  const ctx = getCtx();
  if (typeof ctx.activateTab === 'function') { ctx.activateTab(panelKey); return; }
  const tab = document.querySelector('.tab[data-tab="' + cssAttr(panelKey) + '"]');
  if (tab && !tab.disabled) tab.click();
}

function trackSse(cleanups, type, fn, isActive) {
  if (typeof type !== 'string' || typeof fn !== 'function') return () => {};
  if (typeof isActive === 'function' && !isActive()) return () => {};
  const ctx = getCtx();
  const unsub = (ctx.registerSseHandler || registerSseHandler)(type, fn);
  const entry = { type, fn, unsub: typeof unsub === 'function' ? unsub : () => {} };
  cleanups.add(entry);
  return () => { if (cleanups.delete(entry)) { try { entry.unsub(); } catch { /* ignore */ } } };
}

function drainSse(cleanups, typeFilter = null) {
  for (const e of [...cleanups]) {
    if (!typeFilter || e.type === typeFilter) { cleanups.delete(e); try { e.unsub(); } catch { /* ignore */ } }
  }
}

function ensureAbilityStyle(abilityId, relStyle, digest) {
  if (!relStyle) return Promise.resolve();
  const sel = 'link[data-ability-css="' + cssAttr(abilityId) + '"][data-ability-style="' + cssAttr(relStyle) + '"]';
  const existing = document.head.querySelector(sel);
  if (existing) { if (existing.dataset.abilityDigest === String(digest || '')) return Promise.resolve(); existing.remove(); }
  return new Promise((resolve) => {
    const link = h('link', { dataset: { abilityCss: abilityId, abilityStyle: relStyle, abilityDigest: String(digest || '') }, on: { load: resolve, error: resolve } });
    link.rel = 'stylesheet'; link.href = abilityAssetUrl(abilityId, relStyle, digest);
    document.head.appendChild(link);
  });
}

function pruneAbilityStyles(runnableMap) {
  document.head.querySelectorAll('link[data-ability-css]').forEach((link) => {
    const ab = runnableMap.get(link.dataset.abilityCss);
    if (!ab || link.dataset.abilityDigest !== String(ab.digest || '')) link.remove();
  });
}

/* ---- SDK Context, error boundaries & slot lifecycle ---- */

function renderErrorBoundary(container, title, err, onRetry) {
  if (!container) return;
  const retry = typeof onRetry === 'function' ? btnWithIcon('Retry', 'refresh', 'btn', { on: { click: onRetry } }) : null;
  const openMgr = document.querySelector('.tab[data-tab="abilities"]')
    ? btnWithIcon('Open Abilities', 'settings', 'btn', { on: { click: () => navigateToPanel('abilities') } }) : null;
  container.replaceChildren(h('section', { cls: 'ab-error-boundary', attrs: { role: 'alert' } },
    h('h3', null, iconNode('alert'), title),
    h('p', { text: 'The ability UI module failed to load or render.' }),
    h('pre', { text: (err && (err.stack || err.message)) || String(err || 'Unknown error') }),
    h('div', 'ab-error-actions', retry, openMgr),
  ));
}

function buildSdkCtx(ability, slotId, extraCtx, sseCleanups, sideOverride, isActive = () => true) {
  const hostCtx = getCtx();
  let pendingContextFlow = (extraCtx && extraCtx.flow) || null;
  const base = {
    abilityId: ability.id,
    menuId: slotId,
    tabId: slotId,
    api, toast, toastError, esc, icon,
    openFlow: (flowId, opts) => (hostCtx.openFlow ? hostCtx.openFlow(flowId, opts) : openFlow(flowId, opts)),
    activateTab: (tabId) => navigateToPanel(tabId),
    getSelectedFlow: () => {
      if (pendingContextFlow) { const f = pendingContextFlow; pendingContextFlow = null; return f; }
      return hostCtx.getSelectedFlow ? hostCtx.getSelectedFlow() : null;
    },
    runAction: (actionId, input) => runAbilityAction(ability.id, actionId, input),
    onSse: (type, fn) => trackSse(sseCleanups, type, fn, isActive),
    offSse: (type) => drainSse(sseCleanups, type),
  };
  if (sideOverride) base.side = sideOverride;
  return extraCtx ? Object.assign(base, extraCtx) : base;
}

function teardownRecord(map, key, expectedRec = null) {
  const rec = map.get(key);
  if (!rec || (expectedRec && rec !== expectedRec)) return;
  rec.disposed = true;
  map.delete(key);
  drainSse(rec.sseCleanups);
  rec.slotEl?.remove?.();
  if (rec.mod && typeof rec.mod.unmount === 'function' && !rec.unmounted) {
    rec.unmounted = true;
    try { rec.mod.unmount(rec.slotEl || rec.container); } catch { /* isolate teardown errors */ }
  }
  if (rec.container?._abilityUnmount === rec.unmountFn) rec.container._abilityUnmount = null;
  if (rec.container?.isConnected && !map.has(key)) rec.container.replaceChildren();
}

async function tryUpdateMounted(rec, ab, slotId, container, extraCtx, sideOverride, label, onRetry, isActive) {
  try {
    await rec.mod.update(rec.slotEl, buildSdkCtx(ab, slotId, extraCtx, rec.sseCleanups, sideOverride, isActive));
    return true;
  } catch (err) {
    if (isActive()) renderErrorBoundary(container, label + ' failed', err, onRetry);
    return false;
  }
}

async function loadAndMountSlot({ ability, slotId, entry, style, label, container, rec, extraCtx, sideOverride, isCurrent }) {
  if (isCurrent()) container.replaceChildren(h('p', { cls: 'ab-panel-loading', text: 'Loading ' + label + '…' }));
  if (style) await ensureAbilityStyle(ability.id, style, ability.digest);
  const mod = await import(abilityAssetUrl(ability.id, entry, ability.digest));
  if (!isCurrent()) { drainSse(rec.sseCleanups); return null; }
  if (typeof mod.mount !== 'function') throw new Error('Module "' + entry + '" does not export mount(container, ctx)');
  container.replaceChildren(rec.slotEl);
  await mod.mount(rec.slotEl, buildSdkCtx(ability, slotId, extraCtx, rec.sseCleanups, sideOverride, isCurrent));
  if (!isCurrent()) {
    drainSse(rec.sseCleanups);
    rec.slotEl?.remove?.();
    if (!rec.unmounted && typeof mod.unmount === 'function') {
      rec.unmounted = true;
      try { mod.unmount(rec.slotEl); } catch { /* isolate teardown errors */ }
    }
    return null;
  }
  return mod;
}

/** Dynamically imports and mounts an ability sidebar panel slot (`ability:<id>:<menuId>`). */
export async function mountAbilityPanel(panelKey, explicitCtx = null) {
  if (!panelKey || !String(panelKey).startsWith('ability:')) return false;
  if (!hostBridge.isLoaded() && !hostBridge.isLoading()) await hostBridge.loadAbilities();
  const extraCtx = explicitCtx || pendingPanelContext.get(panelKey) || null;
  if (extraCtx) pendingPanelContext.delete(panelKey);

  const list = getAbilities();
  const menu = collectInjectedMenus(list).find((m) => m.panelKey === panelKey);
  const ability = menu ? list.find((a) => a.id === menu.abilityId && isAbilityRunnable(a)) : null;
  const container = document.querySelector('.panel[data-panel="' + cssAttr(panelKey) + '"]');
  if (!menu || !ability || !container) return false;

  const existing = mountedPanels.get(panelKey);
  if (existing && !existing.disposed && sameRevision(existing, ability)) {
    const isCurrent = () => !existing.disposed && mountedPanels.get(panelKey) === existing && container.isConnected && isLiveAbility(ability);
    if (!extraCtx && existing.mounting) return existing.queue.then(() => existing.mounted);
    if (existing.mounted && existing.slotEl?.isConnected) {
      if (!extraCtx) return true;
      if (typeof existing.mod?.update === 'function') {
        const run = existing.queue.then(async () => {
          if (!isCurrent()) return false;
          return tryUpdateMounted(existing, ability, menu.menuId, container, extraCtx, null, menu.label,
            () => void mountAbilityPanel(panelKey, extraCtx), isCurrent);
        });
        existing.queue = run.catch(() => false);
        return run;
      }
    }
  }

  teardownRecord(mountedPanels, panelKey);
  const sseCleanups = new Set();
  const slotEl = h('div', 'ab-slot-body');
  const rec = {
    abilityId: ability.id, menuId: menu.menuId, digest: ability.digest, scope: ability.scope, dir: ability.dir,
    container, slotEl, mod: null, sseCleanups, mounted: false, mounting: true, disposed: false, unmounted: false, token: 1,
  };
  const isCurrent = () => !rec.disposed && mountedPanels.get(panelKey) === rec && container.isConnected && isLiveAbility(ability);
  mountedPanels.set(panelKey, rec);
  rec.queue = (async () => {
    try {
      const mod = await loadAndMountSlot({
        ability, slotId: menu.menuId, entry: menu.entry, style: menu.style, label: menu.label, container, rec, extraCtx, isCurrent,
      });
      if (!mod) return false;
      rec.mod = mod; rec.mounted = true;
      return true;
    } catch (err) {
      drainSse(sseCleanups);
      if (isCurrent()) {
        mountedPanels.delete(panelKey);
        rec.disposed = true;
        renderErrorBoundary(container, menu.label + ' crashed', err, () => void mountAbilityPanel(panelKey, extraCtx));
      }
      return false;
    } finally { rec.mounting = false; }
  })();
  return rec.queue;
}

/* ---- Sidebar, Canvas Pool, Mobile Select, Command Palette, Context & Inspector ---- */

export function syncInjectedMenus(menus) {
  const nav = $('#tabs'), pool = $('#canvasPool') || $('#main'), mobileSel = $('#mobileToolSelect');
  const list = getAbilities();
  const wantedKeys = new Set(menus.map((m) => m.panelKey));
  const runnableMap = new Map(list.filter(isAbilityRunnable).map((a) => [a.id, a]));

  for (const [pk, rec] of [...mountedPanels.entries()]) {
    const ab = runnableMap.get(rec.abilityId);
    if (!wantedKeys.has(pk) || !ab || !sameRevision(rec, ab)) teardownRecord(mountedPanels, pk, rec);
  }
  for (const [c, rec] of [...mountedInspectors.entries()]) {
    const ab = runnableMap.get(rec.abilityId);
    if (!c.isConnected || !ab || !sameRevision(rec, ab)) teardownRecord(mountedInspectors, c, rec);
  }
  pruneAbilityStyles(runnableMap);

  $$('.panel.ability-injected-panel').forEach((p) => {
    const pk = p.dataset.panel;
    if (wantedKeys.has(pk)) return;
    const wasFocused = p.classList.contains('is-focused');
    const wasActive = p.classList.contains('active');
    if (wasFocused) navigateToPanel(document.querySelector('.tab[data-tab="abilities"]') ? 'abilities' : FALLBACK_PANEL_TAB);
    if (wasActive || wasFocused) getShellApi().closeDoc?.(pk);
    p.remove();
  });

  if (pool) {
    for (const m of menus) {
      if (document.querySelector('.panel[data-panel="' + cssAttr(m.panelKey) + '"]')) continue;
      const domSuffix = abilityDomId(m.abilityId, m.menuId);
      pool.appendChild(h('div', {
        cls: 'panel ability-injected-panel', id: 'panel-' + domSuffix, dataset: { panel: m.panelKey },
        attrs: { role: 'tabpanel', 'aria-labelledby': 'tab-' + domSuffix },
      }));
    }
  }

  if (nav) {
    nav.querySelectorAll('.ability-injected-tab,[data-ability-injected="true"]').forEach((n) => n.remove());
    const builtinTabs = $$('#tabs .tab:not(.ability-injected-tab)');
    const tabsDisabled = builtinTabs.length > 0 && builtinTabs.every((t) => t.disabled);
    const focused = $('.panel.is-focused')?.dataset.panel || $('.panel.active')?.dataset.panel || '';
    for (const m of menus) {
      let groupEl = nav.querySelector('.nav-rail-group[data-nav-group="' + cssAttr(m.group) + '"]');
      if (!groupEl && m.group === 'abilities') {
        groupEl = h('div', { cls: 'nav-rail-group', dataset: { navGroup: 'abilities', abilityInjected: 'true' } },
          h('div', { cls: 'nav-rail-group-label', text: 'Abilities' }));
        const cfg = nav.querySelector('.nav-rail-group[data-nav-group="configure"]');
        if (cfg) nav.insertBefore(groupEl, cfg); else nav.appendChild(groupEl);
      }
      const domSuffix = abilityDomId(m.abilityId, m.menuId), isActive = focused === m.panelKey;
      const tab = h('button', {
        type: 'button', cls: isActive ? 'tab ability-injected-tab active' : 'tab ability-injected-tab',
        id: 'tab-' + domSuffix, title: m.description || m.label, disabled: tabsDisabled,
        dataset: { tab: m.panelKey, group: groupLabel(m.group) },
        attrs: { role: 'tab', 'aria-controls': 'panel-' + domSuffix, 'aria-selected': String(isActive), tabindex: isActive ? '0' : '-1' },
        on: { click: () => navigateToPanel(m.panelKey) },
      }, iconNode(resolveSprite(m.icon), 'nav-icon'), m.label);
      (groupEl || nav).appendChild(tab);
    }
  }

  if (mobileSel) {
    mobileSel.querySelectorAll('[data-ability-injected="true"]').forEach((n) => n.remove());
    if (menus.length) {
      mobileSel.appendChild(h('optgroup', { dataset: { abilityInjected: 'true' }, attrs: { label: 'Abilities' } },
        ...menus.map((m) => h('option', { value: m.panelKey, text: m.label, dataset: { abilityInjected: 'true' } }))));
    }
  }
  $$('.panel.ability-injected-panel.active').forEach((p) => { if (p.dataset.panel && !mountedPanels.has(p.dataset.panel)) void mountAbilityPanel(p.dataset.panel); });
}

async function invokeAbilityCommand(cmd) {
  try {
    const hostCtx = getCtx();
    const selFlow = hostCtx.getSelectedFlow ? hostCtx.getSelectedFlow() : null;
    const input = flowActionInput(selFlow, false);
    if (cmd.action && cmd.panelKey) {
      const res = await runAbilityAction(cmd.abilityId, cmd.action, input);
      pendingPanelContext.set(cmd.panelKey, { flow: selFlow, actionResult: res, result: res });
      navigateToPanel(cmd.panelKey);
      await mountAbilityPanel(cmd.panelKey);
    } else if (cmd.action) {
      await runAbilityAction(cmd.abilityId, cmd.action, input);
      toast(cmd.title + ' completed', 'success');
    } else if (cmd.panelKey) {
      navigateToPanel(cmd.panelKey);
      await mountAbilityPanel(cmd.panelKey);
    }
  } catch (err) { toastError(cmd.title || 'Ability command failed', err); }
}

export function syncInjectedCommands(abilitiesList, menus) {
  while (cmdUnregisters.length) { try { cmdUnregisters.pop()(); } catch { /* ignore */ } }
  const reg = getCtx().registerCommand || registerCommand;
  const covered = new Set();
  for (const cmd of collectInjectedCommands(abilitiesList)) {
    if (cmd.panelKey) covered.add(cmd.panelKey);
    const u = reg({ t: cmd.title, kw: cmd.keywords || cmd.abilityId, group: 'Abilities', run: () => void invokeAbilityCommand(cmd) });
    if (typeof u === 'function') cmdUnregisters.push(u);
  }
  for (const m of menus) {
    if (covered.has(m.panelKey)) continue;
    const u = reg({
      t: 'Go to ' + m.label, kw: [m.abilityName, m.description, m.group, 'ability'].filter(Boolean).join(' '),
      group: 'Abilities', run: () => { navigateToPanel(m.panelKey); void mountAbilityPanel(m.panelKey); },
    });
    if (typeof u === 'function') cmdUnregisters.push(u);
  }
}

async function invokeContextItem(item, flow) {
  const cleanups = new Set();
  let active = true;
  try {
    let actionRes;
    if (item.action) actionRes = await runAbilityAction(item.abilityId, item.action, flowActionInput(flow, true));
    if (item.panel) {
      const panelKey = abilityPanelId(item.abilityId, item.panel);
      const ctx = { flow: flow || null };
      if (actionRes !== undefined) { ctx.actionResult = actionRes; ctx.result = actionRes; }
      pendingPanelContext.set(panelKey, ctx);
      navigateToPanel(panelKey);
      await mountAbilityPanel(panelKey);
      return;
    }
    if (item.action) {
      toast(item.label + (typeof actionRes?.result?.summary === 'string' ? ': ' + actionRes.result.summary : ' completed'), 'success');
      return;
    }
    if (item.entry) {
      const ab = getAbilities().find((a) => a.id === item.abilityId && isAbilityRunnable(a));
      if (!ab) return;
      const mod = await import(abilityAssetUrl(ab.id, item.entry, ab.digest));
      const ctx = buildSdkCtx(ab, item.id, { flow: flow || null }, cleanups, null, () => active && isLiveAbility(ab));
      if (typeof mod.run === 'function') await mod.run(ctx);
      else if (typeof mod.default === 'function') await mod.default(ctx);
    }
  } catch (err) {
    toastError(item.label || 'Ability action failed', err);
  } finally {
    active = false;
    drainSse(cleanups);
  }
}

/** Returns ctxMenu items for ability contextMenus targeting `'flow'`. */
export function abilityFlowContextItems(flow) {
  return collectInjectedContextItems(getAbilities(), 'flow').map((item) => ({
    label: item.label, icon: resolveSprite(item.icon), act: () => void invokeContextItem(item, flow),
  }));
}

async function mountInspectorTab(tab, container, flowData, sideHint) {
  if (!container) return () => {};
  const ab = getAbilities().find((a) => a.id === tab.abilityId && isAbilityRunnable(a));
  if (!ab) { renderErrorBoundary(container, tab.label + ' unavailable', new Error('Ability is quarantined or disabled.')); return () => {}; }
  const rawSide = flowData?.side;
  const resolvedSide = VALID_SIDES.has(rawSide) ? rawSide : (sideHint && sideHint !== 'both' ? sideHint : tab.side);
  const flowObj = flowData?.flow && !flowData.method ? flowData.flow : flowData;

  const existing = mountedInspectors.get(container);
  if (existing && !existing.disposed && sameRevision(existing, ab) && existing.tabId === tab.id &&
      (existing.mounting || typeof existing.mod?.update === 'function')) {
    const myToken = ++existing.token;
    const isActive = () => !existing.disposed && mountedInspectors.get(container) === existing && isLiveAbility(ab);
    const run = existing.queue.then(async () => {
      if (!isActive() || !existing.mounted || typeof existing.mod?.update !== 'function') return;
      await tryUpdateMounted(existing, ab, tab.id, container, { flow: flowObj || null }, resolvedSide, tab.label,
        () => void mountInspectorTab(tab, container, flowData, sideHint), isActive);
    });
    existing.queue = run.catch(() => {});
    await run;
    return () => { if (!existing.disposed && existing.token === myToken) teardownRecord(mountedInspectors, container, existing); };
  }

  teardownRecord(mountedInspectors, container);
  const sseCleanups = new Set();
  const slotEl = h('div', 'ab-slot-body');
  const rec = {
    abilityId: tab.abilityId, tabId: tab.id, digest: ab.digest, scope: ab.scope, dir: ab.dir,
    container, slotEl, mod: null, sseCleanups, mounted: false, mounting: true, disposed: false, unmounted: false, token: 1,
  };
  rec.unmountFn = () => teardownRecord(mountedInspectors, container, rec);
  container._abilityUnmount = rec.unmountFn;
  mountedInspectors.set(container, rec);

  const isCurrent = () => !rec.disposed && mountedInspectors.get(container) === rec && isLiveAbility(ab);
  rec.queue = (async () => {
    try {
      const mod = await loadAndMountSlot({
        ability: ab, slotId: tab.id, entry: tab.entry, style: tab.style, label: tab.label, container, rec,
        extraCtx: { flow: flowObj || null }, sideOverride: resolvedSide, isCurrent,
      });
      if (mod) { rec.mod = mod; rec.mounted = true; }
    } catch (err) {
      drainSse(sseCleanups);
      slotEl.remove();
      if (isCurrent()) {
        mountedInspectors.delete(container);
        rec.disposed = true;
        if (container._abilityUnmount === rec.unmountFn) container._abilityUnmount = null;
        renderErrorBoundary(container, tab.label + ' failed', err, () => void mountInspectorTab(tab, container, flowData, sideHint));
      }
    } finally { rec.mounting = false; }
  })();
  await rec.queue;
  return () => { if (!rec.disposed && rec.token === 1) teardownRecord(mountedInspectors, container, rec); };
}

/** Returns inspector tab descriptors filtered by side (`'both'`, `'req'`, `'res'`). */
export function abilityInspectorTabs(flowDetail, side = 'both') {
  const effectiveSide = side && side !== 'both' ? side : (typeof flowDetail?.side === 'string' && VALID_SIDES.has(flowDetail.side) ? flowDetail.side : 'both');
  return collectInjectedInspectorTabs(getAbilities(), effectiveSide).map((t) => ({
    id: 'ab:' + t.abilityId + ':' + t.id, label: t.label, side: t.side, abilityId: t.abilityId, tab: t,
    mount: (container, flowData) => mountInspectorTab(t, container, flowData !== undefined ? flowData : flowDetail, effectiveSide),
  }));
}
