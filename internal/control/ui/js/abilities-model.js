// abilities-model.js — pure, DOM-free logic for Copy Abilities: trust/status
// badges, panel IDs, injected UI/command collection, filtering, and scaffold
// validation. Tested directly under node (`_js-tests/abilities-model.test.mjs`).

export const ABILITY_GROUPS = Object.freeze([
  'capture',
  'test',
  'recon',
  'report',
  'configure',
  'abilities',
]);

const GROUP_SET = new Set(ABILITY_GROUPS);
const VALID_ID_RE = /^[a-z0-9][a-z0-9-]{0,63}$/;
const VALID_SIDES = new Set(['both', 'req', 'res']);
const VALID_SCOPES = new Set(['project', 'global']);
const MAX_DESCRIPTION_LEN = 4096;

const GROUP_LABELS = Object.freeze({
  capture: 'Capture',
  test: 'Test',
  recon: 'Recon',
  report: 'Report',
  configure: 'Configure',
  abilities: 'Abilities',
});

function manifestOf(ability) {
  return (ability && ability.manifest && typeof ability.manifest === 'object') ? ability.manifest : {};
}

function abilityDisplayName(ability, manifest) {
  return String(manifest.name || (ability && ability.id) || '').trim();
}

export function isAbilityRunnable(ability) {
  return Boolean(
    ability &&
    ability.id &&
    ability.trusted &&
    ability.enabled &&
    !ability.quarantined &&
    !ability.drift &&
    !ability.loadError
  );
}

/** Returns { label, tone } for an ability's trust, drift, error, and enablement state. */
export function abilityStatusBadge(ability) {
  if (!ability || typeof ability !== 'object') {
    return { label: 'Quarantined', tone: 'warn' };
  }
  if (ability.drift) {
    return { label: 'Drifted', tone: 'warn' };
  }
  if (ability.loadError) {
    return { label: 'Error', tone: 'err' };
  }
  if (ability.quarantined) {
    return { label: 'Quarantined', tone: 'warn' };
  }
  if (ability.trusted && ability.enabled) {
    return { label: 'Active', tone: 'ok' };
  }
  if (ability.trusted && !ability.enabled) {
    return { label: 'Approved · Off', tone: 'neutral' };
  }
  return { label: 'Quarantined', tone: 'warn' };
}

/** Canonical canvas panel identifier for an ability menu slot. */
export function abilityPanelId(abilityId, menuId) {
  return 'ability:' + String(abilityId || '') + ':' + String(menuId || '');
}

/** Safe DOM element ID suffix for injected tab/panel elements. */
export function abilityDomId(abilityId, subId) {
  const clean = (v) => String(v || '').replace(/[^A-Za-z0-9_-]/g, '_');
  return 'ability-' + clean(abilityId) + '-' + clean(subId);
}

export function groupLabel(group) {
  const g = String(group || '').trim().toLowerCase();
  return GROUP_LABELS[g] || 'Abilities';
}

export function shortDigest(digest, hexChars = 12) {
  const s = String(digest || '').trim();
  if (!s) return '—';
  if (s.startsWith('sha256:')) {
    return 'sha256:' + s.slice(7, 7 + hexChars);
  }
  return s.slice(0, hexChars);
}

export function abilityCapabilities(ability) {
  const m = manifestOf(ability);
  const raw = Array.isArray(m.capabilities) ? m.capabilities : [];
  const seen = new Set();
  const out = [];
  for (const item of raw) {
    const cap = String(item || '').trim();
    if (cap && !seen.has(cap)) {
      seen.add(cap);
      out.push(cap);
    }
  }
  return out;
}

function runnableAbilities(abilities) {
  const list = Array.isArray(abilities) ? abilities : [];
  return list
    .filter(isAbilityRunnable)
    .slice()
    .sort((a, b) => String(a.id).localeCompare(String(b.id)));
}

/** Collects sorted sidebar menu descriptors from enabled, trusted, healthy abilities. */
export function collectInjectedMenus(abilities) {
  const out = [];
  for (const ability of runnableAbilities(abilities)) {
    const m = manifestOf(ability);
    const abilityName = abilityDisplayName(ability, m);
    const menus = Array.isArray(m.menus) ? m.menus : [];
    for (const menu of menus) {
      if (!menu || !menu.id || !menu.entry) continue;
      const rawGroup = String(menu.group || '').trim().toLowerCase();
      const group = GROUP_SET.has(rawGroup) ? rawGroup : 'abilities';
      out.push({
        abilityId: ability.id,
        abilityName,
        menuId: String(menu.id),
        panelKey: abilityPanelId(ability.id, menu.id),
        label: String(menu.label || abilityName || menu.id),
        group,
        icon: String(menu.icon || m.icon || 'abilities'),
        entry: String(menu.entry),
        style: String(menu.style || ''),
        description: String(menu.description || m.description || ''),
      });
    }
  }
  out.sort((a, b) => {
    const ga = ABILITY_GROUPS.indexOf(a.group);
    const gb = ABILITY_GROUPS.indexOf(b.group);
    if (ga !== gb) return ga - gb;
    const lc = a.label.localeCompare(b.label, undefined, { sensitivity: 'base' });
    if (lc !== 0) return lc;
    const ac = a.abilityId.localeCompare(b.abilityId);
    return ac !== 0 ? ac : a.menuId.localeCompare(b.menuId);
  });
  return out;
}

/** Collects context-menu items targeting `target` from enabled, trusted abilities. */
export function collectInjectedContextItems(abilities, target = 'flow') {
  const wantTarget = String(target || 'flow').trim().toLowerCase();
  const out = [];
  for (const ability of runnableAbilities(abilities)) {
    const m = manifestOf(ability);
    const abilityName = abilityDisplayName(ability, m);
    const items = Array.isArray(m.contextMenus) ? m.contextMenus : [];
    for (const cm of items) {
      if (!cm || !cm.id || !cm.label) continue;
      const cmTarget = String(cm.target || 'flow').trim().toLowerCase();
      if (wantTarget && cmTarget !== wantTarget) continue;
      out.push({
        abilityId: ability.id,
        abilityName,
        id: String(cm.id),
        label: String(cm.label),
        icon: String(cm.icon || m.icon || 'abilities'),
        panel: String(cm.panel || ''),
        action: String(cm.action || ''),
        entry: String(cm.entry || ''),
      });
    }
  }
  return out;
}

/** Collects flow inspector tab descriptors matching `side` ('both' returns all sides). */
export function collectInjectedInspectorTabs(abilities, side = 'both') {
  const wantSide = String(side || 'both').trim().toLowerCase();
  const out = [];
  for (const ability of runnableAbilities(abilities)) {
    const m = manifestOf(ability);
    const abilityName = abilityDisplayName(ability, m);
    const tabs = Array.isArray(m.inspectorTabs) ? m.inspectorTabs : [];
    for (const tab of tabs) {
      if (!tab || !tab.id || !tab.entry) continue;
      const rawSide = String(tab.side || 'both').trim().toLowerCase();
      const tabSide = VALID_SIDES.has(rawSide) ? rawSide : 'both';
      if (wantSide !== 'both' && wantSide !== 'all' && tabSide !== 'both' && tabSide !== wantSide) {
        continue;
      }
      out.push({
        abilityId: ability.id,
        abilityName,
        id: String(tab.id),
        label: String(tab.label || abilityName || tab.id),
        side: tabSide,
        entry: String(tab.entry),
        style: String(tab.style || ''),
      });
    }
  }
  return out;
}

function normalizeKeywords(kw) {
  if (Array.isArray(kw)) {
    return kw.map((v) => String(v || '').trim()).filter(Boolean).join(' ');
  }
  return String(kw || '').trim();
}

/** Collects command-palette entries from enabled, trusted abilities. */
export function collectInjectedCommands(abilities) {
  const out = [];
  for (const ability of runnableAbilities(abilities)) {
    const m = manifestOf(ability);
    const cmds = Array.isArray(m.commands) ? m.commands : [];
    for (const cmd of cmds) {
      if (!cmd || !cmd.id || !cmd.title) continue;
      const panel = String(cmd.panel || '');
      out.push({
        abilityId: ability.id,
        id: String(cmd.id),
        title: String(cmd.title),
        keywords: normalizeKeywords(cmd.keywords),
        panel,
        panelKey: panel ? abilityPanelId(ability.id, panel) : '',
        action: String(cmd.action || ''),
      });
    }
  }
  return out;
}

/** Filters abilities by search query (id/name/description/author) and status filter. */
export function filterAbilities(abilities, query = '', filter = 'all') {
  const list = Array.isArray(abilities) ? abilities : [];
  const q = String(query || '').trim().toLowerCase();
  const mode = String(filter || 'all').trim().toLowerCase();

  return list.filter((ability) => {
    if (!ability) return false;
    if (mode === 'active' && !isAbilityRunnable(ability)) return false;
    if (mode === 'quarantined' && !(ability.quarantined || !ability.trusted)) return false;
    if (mode === 'drift' && !ability.drift) return false;
    if (!q) return true;
    const m = manifestOf(ability);
    const haystack = [ability.id, m.id, m.name, m.description, m.author]
      .filter(Boolean)
      .join(' ')
      .toLowerCase();
    return haystack.includes(q);
  });
}

/** Suggests a valid ability ID slug from a human-readable display name. */
export function suggestAbilityId(name) {
  return String(name || '')
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9-]+/g, '-')
    .replace(/^-+|-+$/g, '')
    .slice(0, 64);
}

/** Validates and normalizes scaffold creation input against manifest.go rules. */
export function validateScaffoldInput({ id, name, group, description, scope } = {}) {
  const normId = String(id == null ? '' : id).trim().toLowerCase();
  const normName = String(name == null ? '' : name).trim();
  const normGroup = String(group == null ? '' : group).trim().toLowerCase() || 'abilities';
  const normDesc = String(description == null ? '' : description).trim();
  const normScope = String(scope == null ? '' : scope).trim().toLowerCase() || 'project';
  const normalized = {
    id: normId,
    name: normName,
    group: normGroup,
    description: normDesc,
    scope: normScope,
  };

  if (!normId) {
    return { ok: false, error: 'Ability ID is required.', normalized };
  }
  if (!VALID_ID_RE.test(normId)) {
    return {
      ok: false,
      error: 'ID must match ^[a-z0-9][a-z0-9-]{0,63}$ (lowercase letters, digits, hyphens).',
      normalized,
    };
  }
  if (!normName) {
    return { ok: false, error: 'Display name is required.', normalized };
  }
  if (!GROUP_SET.has(normGroup)) {
    return { ok: false, error: 'Invalid sidebar group: ' + normGroup, normalized };
  }
  if (!VALID_SCOPES.has(normScope)) {
    return { ok: false, error: 'Scope must be project or global.', normalized };
  }
  if (normDesc.length > MAX_DESCRIPTION_LEN || normDesc.includes('\0')) {
    return { ok: false, error: 'Description is invalid or exceeds 4096 characters.', normalized };
  }
  return { ok: true, error: '', normalized };
}

/** Returns summary badge descriptor { label, tone } for the manager header. */
export function abilitySummaryBadge(abilities) {
  const list = Array.isArray(abilities) ? abilities : [];
  const activeCount = list.filter(isAbilityRunnable).length;
  if (activeCount > 0) return { label: activeCount + ' active', tone: 'ok' };
  if (!list.length) return { label: '0 installed', tone: 'neutral' };
  const quarCount = list.filter((a) => !isAbilityRunnable(a) && !a?.trusted).length;
  return { label: quarCount + ' quarantined', tone: 'warn' };
}

/** Returns concise surface count chips for an ability list card. */
export function surfaceSummaryCounts(abilityOrManifest) {
  const m = abilityOrManifest?.manifest ? manifestOf(abilityOrManifest) : (abilityOrManifest || {});
  const noun = (n, s, p) => n + ' ' + (n === 1 ? s : p);
  const out = [];
  if (m.menus?.length) out.push(noun(m.menus.length, 'menu', 'menus'));
  if (m.inspectorTabs?.length) out.push(noun(m.inspectorTabs.length, 'tab', 'tabs'));
  if (m.contextMenus?.length) out.push(m.contextMenus.length + ' ctx');
  if (m.hooks?.length) out.push(noun(m.hooks.length, 'hook', 'hooks'));
  if (m.actions?.length) out.push(noun(m.actions.length, 'action', 'actions'));
  return out;
}

/** Returns banner metadata { kind, icon, text } for quarantined/drifted/broken abilities. */
export function abilityTrustBanner(ability) {
  if (!ability || typeof ability !== 'object') return null;
  if (ability.loadError) return { kind: 'err', icon: 'alert', text: 'Load error: ' + ability.loadError };
  if (ability.drift) {
    return {
      kind: 'warn',
      icon: 'alert',
      text: (ability.trustError || 'Files changed on disk since approval.') +
        ' Re-quarantined until a human reviews the changes and approves again.',
    };
  }
  if (ability.quarantined || !ability.trusted) {
    return {
      kind: 'warn',
      icon: 'lock',
      text: 'Quarantined: UI modules, flow hooks, and actions stay inert until you inspect this manifest and click Approve & Enable.',
    };
  }
  return null;
}

/** Builds structured surface sections for the ability detail inspector. */
export function buildSurfaceSections(ability) {
  const m = manifestOf(ability);
  const raw = [
    ['Sidebar Menus', (m.menus || []).map((menu) => ({
      title: menu.label || menu.id,
      sub: groupLabel(menu.group) + ' · ' + menu.entry + (menu.style ? ' · ' + menu.style : ''),
      menuId: menu.id,
    }))],
    ['Flow Inspector Tabs', (m.inspectorTabs || []).map((t) => ({
      title: t.label || t.id,
      sub: 'side=' + (t.side || 'both') + ' · ' + t.entry,
    }))],
    ['Context Menu Actions', (m.contextMenus || []).map((c) => ({
      title: c.label || c.id,
      sub: 'target=' + (c.target || 'flow') + (c.panel ? ' · panel=' + c.panel : '') + (c.action ? ' · action=' + c.action : ''),
    }))],
    ['Command Palette', (m.commands || []).map((c) => ({
      title: c.title || c.id,
      sub: [c.panel ? 'panel=' + c.panel : '', c.action ? 'action=' + c.action : ''].filter(Boolean).join(' '),
    }))],
    ['Starlark Flow Hooks', (m.hooks || []).map((h) => ({ title: h.event, sub: h.entry }))],
    ['Starlark Actions', (m.actions || []).map((a) => ({
      title: a.name || a.id,
      sub: a.id + ' · ' + a.entry + (a.description ? ' — ' + a.description : ''),
      badge: a.mcp ? 'MCP tool' : '',
      badgeClass: a.mcp ? 'ab-chip is-mcp' : '',
    }))],
  ];
  return raw.filter(([, rows]) => rows.length > 0).map(([heading, rows]) => ({ heading, rows }));
}

/** Keep relative imports and stylesheet URLs under the same approved revision. */
export function abilityAssetUrl(id, relPath, digest) {
  const segs = String(relPath || '').split('/').filter(Boolean).map(encodeURIComponent).join('/');
  return '/abilities/' + encodeURIComponent(id) + '/' + encodeURIComponent(digest) + '/' + segs;
}

/** Normalizes a manifest icon string to a safe lowercase symbol stem. */
export function sanitizeIconName(raw) {
  return String(raw || '').replace(/^i-/, '').replace(/[^a-z0-9-]/g, '');
}

/** Extracts an abilities array from `/api/abilities` responses. */
export function extractAbilitiesList(data) {
  if (Array.isArray(data)) return data;
  return Array.isArray(data?.abilities) ? data.abilities : [];
}

/** Builds standard flow input payload `{ flowId }` or `{ flow }` for ability actions. */
export function flowActionInput(flow, includeWhole = false) {
  if (flow && flow.id != null) return { flowId: flow.id };
  return includeWhole && flow ? { flow } : {};
}
