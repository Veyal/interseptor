import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  ABILITY_GROUPS,
  isAbilityRunnable,
  abilityStatusBadge,
  abilitySummaryBadge,
  abilityTrustBanner,
  abilityPanelId,
  abilityDomId,
  groupLabel,
  shortDigest,
  abilityCapabilities,
  surfaceSummaryCounts,
  buildSurfaceSections,
  collectInjectedMenus,
  collectInjectedContextItems,
  collectInjectedInspectorTabs,
  collectInjectedCommands,
  filterAbilities,
  suggestAbilityId,
  validateScaffoldInput,
  abilityAssetUrl,
  sanitizeIconName,
  extractAbilitiesList,
  flowActionInput,
} from '../js/abilities-model.js';

function makeAbility(overrides = {}) {
  const manifest = {
    schema: 'interseptor.ability/v1',
    id: overrides.id || 'jwt-xray',
    name: 'JWT X-Ray',
    version: '1.0.0',
    description: 'Inspect JWT claims and alg=none risks',
    author: 'Veyal',
    icon: 'key',
    capabilities: ['ui.menu', 'ui.inspectorTab', 'ui.contextMenu', 'ui.command', 'flow.hook', 'mcp.tool'],
    menus: [
      { id: 'jwt-panel', label: 'JWT X-Ray', group: 'recon', icon: 'key', entry: 'ui/panel.js', style: 'ui/style.css', description: 'Inspect JWTs' },
    ],
    inspectorTabs: [
      { id: 'jwt-req', label: 'JWT Req', side: 'req', entry: 'ui/req.js' },
      { id: 'jwt-res', label: 'JWT Res', side: 'res', entry: 'ui/res.js', style: 'ui/res.css' },
      { id: 'jwt-both', label: 'JWT', side: 'both', entry: 'ui/inspector.js', style: 'ui/style.css' },
    ],
    contextMenus: [
      { id: 'xray-flow', label: 'Inspect JWTs', target: 'flow', icon: 'key', panel: 'jwt-panel', action: 'analyze_flow' },
      { id: 'xray-sel', label: 'Decode Selection JWT', target: 'selection', entry: 'ui/sel.js' },
      { id: 'xray-default-target', label: 'Quick Scan', action: 'analyze_flow' },
    ],
    commands: [
      { id: 'open-jwt', title: 'JWT X-Ray: Open', keywords: ['jwt', 'bearer'], panel: 'jwt-panel' },
      { id: 'run-jwt', title: 'JWT X-Ray: Analyze', keywords: 'token claims', action: 'analyze_flow' },
    ],
    hooks: [{ event: 'flow.captured', entry: 'hooks/on_flow.star' }],
    actions: [{ id: 'analyze_flow', name: 'Analyze Flow', entry: 'hooks/analyze.star', mcp: true }],
    ...(overrides.manifest || {}),
  };
  return {
    id: manifest.id,
    scope: 'project',
    digest: 'sha256:0123456789abcdef0123456789abcdef',
    trusted: true,
    enabled: true,
    quarantined: false,
    drift: false,
    loadError: '',
    ...overrides,
    manifest,
  };
}

test('ABILITY_GROUPS matches the six canonical sidebar groups', () => {
  assert.deepEqual(ABILITY_GROUPS, ['capture', 'test', 'recon', 'report', 'configure', 'abilities']);
  assert.ok(Object.isFrozen(ABILITY_GROUPS));
});

test('abilityStatusBadge classifies every trust, drift, error, and enablement state', () => {
  assert.deepEqual(abilityStatusBadge(null), { label: 'Quarantined', tone: 'warn' });
  assert.deepEqual(abilityStatusBadge({ trusted: false, enabled: false, quarantined: true }), { label: 'Quarantined', tone: 'warn' });
  assert.deepEqual(abilityStatusBadge({ trusted: true, enabled: true, quarantined: false }), { label: 'Active', tone: 'ok' });
  assert.deepEqual(abilityStatusBadge({ trusted: true, enabled: false, quarantined: false }), { label: 'Approved · Off', tone: 'neutral' });
  assert.deepEqual(abilityStatusBadge({ trusted: false, enabled: false, quarantined: true, drift: true }), { label: 'Drifted', tone: 'warn' });
  assert.deepEqual(abilityStatusBadge({ trusted: false, enabled: false, quarantined: true, loadError: 'bad manifest' }), { label: 'Error', tone: 'err' });
  // Drift or loadError wins even if stale flags are true
  assert.deepEqual(abilityStatusBadge({ trusted: true, enabled: true, quarantined: false, drift: true }), { label: 'Drifted', tone: 'warn' });
  assert.deepEqual(abilityStatusBadge({ trusted: true, enabled: true, quarantined: false, loadError: 'compile error' }), { label: 'Error', tone: 'err' });
  assert.deepEqual(abilityStatusBadge({ trusted: true, enabled: true, quarantined: true }), { label: 'Quarantined', tone: 'warn' });
});

test('abilityPanelId, abilityDomId, groupLabel, shortDigest, and abilityCapabilities format consistently', () => {
  assert.equal(abilityPanelId('jwt-xray', 'jwt-panel'), 'ability:jwt-xray:jwt-panel');
  assert.equal(abilityDomId('jwt-xray', 'jwt:panel.1'), 'ability-jwt-xray-jwt_panel_1');
  assert.equal(groupLabel('recon'), 'Recon');
  assert.equal(groupLabel('abilities'), 'Abilities');
  assert.equal(groupLabel('unknown'), 'Abilities');
  assert.equal(shortDigest('sha256:0123456789abcdef9999'), 'sha256:0123456789ab');
  assert.equal(shortDigest(''), '—');
  assert.deepEqual(
    abilityCapabilities(makeAbility({ manifest: { capabilities: ['ui.menu', 'ui.menu', '  mcp.tool '] } })),
    ['ui.menu', 'mcp.tool'],
  );
});

test('collectInjectedMenus returns sorted runnable menus and omits quarantined/disabled/drifted/errored', () => {
  const activeRecon = makeAbility({
    id: 'jwt-xray',
    manifest: {
      name: 'JWT X-Ray',
      menus: [{ id: 'jwt', label: 'JWT X-Ray', group: 'recon', icon: 'key', entry: 'ui/panel.js', style: 'ui/style.css', description: 'Inspect JWTs' }],
    },
  });
  const activeAbilities = makeAbility({
    id: 'alpha-tool',
    manifest: {
      name: 'Alpha Tool',
      icon: 'codec',
      description: 'Default description',
      menus: [
        { id: 'zeta', label: 'Zeta Panel', group: 'unknown-group', entry: 'ui/z.js' },
        { id: 'alpha', label: 'Alpha Panel', group: 'capture', entry: 'ui/a.js' },
        { id: 'broken-menu', label: 'No Entry', group: 'abilities', entry: '' },
      ],
    },
  });
  const quarantined = makeAbility({ id: 'q-tool', trusted: false, enabled: false, quarantined: true });
  const disabled = makeAbility({ id: 'off-tool', trusted: true, enabled: false, quarantined: false });
  const drifted = makeAbility({ id: 'drift-tool', trusted: false, enabled: false, quarantined: true, drift: true });
  const errored = makeAbility({ id: 'err-tool', trusted: true, enabled: true, quarantined: false, loadError: 'broken' });

  const menus = collectInjectedMenus([activeAbilities, activeRecon, quarantined, disabled, drifted, errored]);
  assert.deepEqual(menus, [
    {
      abilityId: 'alpha-tool',
      abilityName: 'Alpha Tool',
      menuId: 'alpha',
      panelKey: 'ability:alpha-tool:alpha',
      label: 'Alpha Panel',
      group: 'capture',
      icon: 'codec',
      entry: 'ui/a.js',
      style: '',
      description: 'Default description',
    },
    {
      abilityId: 'jwt-xray',
      abilityName: 'JWT X-Ray',
      menuId: 'jwt',
      panelKey: 'ability:jwt-xray:jwt',
      label: 'JWT X-Ray',
      group: 'recon',
      icon: 'key',
      entry: 'ui/panel.js',
      style: 'ui/style.css',
      description: 'Inspect JWTs',
    },
    {
      abilityId: 'alpha-tool',
      abilityName: 'Alpha Tool',
      menuId: 'zeta',
      panelKey: 'ability:alpha-tool:zeta',
      label: 'Zeta Panel',
      group: 'abilities',
      icon: 'codec',
      entry: 'ui/z.js',
      style: '',
      description: 'Default description',
    },
  ]);
});

test('collectInjectedContextItems filters by target and runnable status', () => {
  const active = makeAbility();
  const inactive = makeAbility({ id: 'off', enabled: false });

  const flowItems = collectInjectedContextItems([active, inactive]);
  assert.deepEqual(flowItems, [
    {
      abilityId: 'jwt-xray',
      abilityName: 'JWT X-Ray',
      id: 'xray-flow',
      label: 'Inspect JWTs',
      icon: 'key',
      panel: 'jwt-panel',
      action: 'analyze_flow',
      entry: '',
    },
    {
      abilityId: 'jwt-xray',
      abilityName: 'JWT X-Ray',
      id: 'xray-default-target',
      label: 'Quick Scan',
      icon: 'key',
      panel: '',
      action: 'analyze_flow',
      entry: '',
    },
  ]);

  const selItems = collectInjectedContextItems([active, inactive], 'selection');
  assert.deepEqual(selItems, [
    {
      abilityId: 'jwt-xray',
      abilityName: 'JWT X-Ray',
      id: 'xray-sel',
      label: 'Decode Selection JWT',
      icon: 'key',
      panel: '',
      action: '',
      entry: 'ui/sel.js',
    },
  ]);
});

test('collectInjectedInspectorTabs filters by side and runnable status', () => {
  const active = makeAbility();
  const quarantined = makeAbility({ id: 'q', trusted: false, enabled: false, quarantined: true });

  const allTabs = collectInjectedInspectorTabs([active, quarantined]);
  assert.equal(allTabs.length, 3);
  assert.deepEqual(allTabs.map((t) => t.id), ['jwt-req', 'jwt-res', 'jwt-both']);

  const reqTabs = collectInjectedInspectorTabs([active, quarantined], 'req');
  assert.deepEqual(reqTabs.map((t) => t.id), ['jwt-req', 'jwt-both']);

  const resTabs = collectInjectedInspectorTabs([active, quarantined], 'res');
  assert.deepEqual(resTabs.map((t) => t.id), ['jwt-res', 'jwt-both']);
  assert.equal(collectInjectedInspectorTabs([active, quarantined], 'all').length, 3);
  assert.deepEqual(resTabs[0], {
    abilityId: 'jwt-xray',
    abilityName: 'JWT X-Ray',
    id: 'jwt-res',
    label: 'JWT Res',
    side: 'res',
    entry: 'ui/res.js',
    style: 'ui/res.css',
  });
});

test('collectInjectedCommands normalizes keywords and derives panelKey', () => {
  const active = makeAbility();
  const disabled = makeAbility({ id: 'off', enabled: false });
  assert.deepEqual(collectInjectedCommands([active, disabled]), [
    {
      abilityId: 'jwt-xray',
      id: 'open-jwt',
      title: 'JWT X-Ray: Open',
      keywords: 'jwt bearer',
      panel: 'jwt-panel',
      panelKey: 'ability:jwt-xray:jwt-panel',
      action: '',
    },
    {
      abilityId: 'jwt-xray',
      id: 'run-jwt',
      title: 'JWT X-Ray: Analyze',
      keywords: 'token claims',
      panel: '',
      panelKey: '',
      action: 'analyze_flow',
    },
  ]);
});

test('filterAbilities matches query across id/name/description/author and filters by status', () => {
  const list = [
    makeAbility({ id: 'jwt-xray', manifest: { name: 'JWT X-Ray', author: 'Veyal', description: 'Token tool' } }),
    makeAbility({ id: 'csp-check', trusted: false, enabled: false, quarantined: true, manifest: { name: 'CSP Check', author: 'Alice', description: 'Header audit' } }),
    makeAbility({ id: 'drift-one', trusted: false, enabled: false, quarantined: true, drift: true, manifest: { name: 'Drift One', author: 'Bob', description: 'Changed files' } }),
    makeAbility({ id: 'off-one', trusted: true, enabled: false, quarantined: false, manifest: { name: 'Paused One', author: 'Cara', description: 'Off' } }),
  ];

  assert.equal(filterAbilities(list, '', 'all').length, 4);
  assert.deepEqual(filterAbilities(list, '', 'active').map((a) => a.id), ['jwt-xray']);
  assert.deepEqual(filterAbilities(list, '', 'quarantined').map((a) => a.id), ['csp-check', 'drift-one']);
  assert.deepEqual(filterAbilities(list, '', 'drift').map((a) => a.id), ['drift-one']);
  assert.deepEqual(filterAbilities(list, 'alice', 'all').map((a) => a.id), ['csp-check']);
  assert.deepEqual(filterAbilities(list, 'header audit', 'quarantined').map((a) => a.id), ['csp-check']);
  assert.deepEqual(filterAbilities(list, 'header audit', 'active').map((a) => a.id), []);
});

test('validateScaffoldInput normalizes valid inputs and rejects invalid id/name/group/scope', () => {
  const ok = validateScaffoldInput({
    id: '  My-Scanner ',
    name: '  My Scanner  ',
    group: 'RECON',
    description: '  Checks headers  ',
    scope: 'GLOBAL',
  });
  assert.equal(ok.ok, true);
  assert.equal(ok.error, '');
  assert.deepEqual(ok.normalized, {
    id: 'my-scanner',
    name: 'My Scanner',
    group: 'recon',
    description: 'Checks headers',
    scope: 'global',
  });

  const defaults = validateScaffoldInput({ id: 'a1', name: 'Starter' });
  assert.equal(defaults.ok, true);
  assert.equal(defaults.normalized.group, 'abilities');
  assert.equal(defaults.normalized.scope, 'project');

  assert.equal(validateScaffoldInput({ id: '', name: 'X' }).ok, false);
  assert.equal(validateScaffoldInput({ id: '-bad', name: 'X' }).ok, false);
  assert.equal(validateScaffoldInput({ id: 'bad_id', name: 'X' }).ok, false);
  assert.equal(validateScaffoldInput({ id: 'a'.repeat(65), name: 'X' }).ok, false);
  assert.equal(validateScaffoldInput({ id: 'good-id', name: '   ' }).ok, false);
  assert.equal(validateScaffoldInput({ id: 'good-id', name: 'Good', group: 'nope' }).ok, false);
  assert.equal(validateScaffoldInput({ id: 'good-id', name: 'Good', scope: 'workspace' }).ok, false);
  assert.equal(validateScaffoldInput({ id: 'good-id', name: 'Good', description: 'x'.repeat(4097) }).ok, false);
});

test('isAbilityRunnable, suggestAbilityId, abilitySummaryBadge, surfaceSummaryCounts, abilityTrustBanner, and buildSurfaceSections work cleanly', () => {
  const active = makeAbility();
  const quar = makeAbility({ id: 'q', trusted: false, enabled: false, quarantined: true });
  const drift = makeAbility({ id: 'd', trusted: false, enabled: false, quarantined: true, drift: true, trustError: 'Hash mismatch' });
  const broken = makeAbility({ id: 'e', loadError: 'syntax error' });

  assert.equal(isAbilityRunnable(active), true);
  assert.equal(isAbilityRunnable(quar), false);
  assert.equal(isAbilityRunnable(broken), false);
  assert.equal(suggestAbilityId('  JWT X-Ray v2!! '), 'jwt-x-ray-v2');
  assert.deepEqual(abilitySummaryBadge([]), { label: '0 installed', tone: 'neutral' });
  assert.deepEqual(abilitySummaryBadge([quar]), { label: '1 quarantined', tone: 'warn' });
  assert.deepEqual(abilitySummaryBadge([active, quar]), { label: '1 active', tone: 'ok' });

  assert.deepEqual(surfaceSummaryCounts(active), ['1 menu', '3 tabs', '3 ctx', '1 hook', '1 action']);
  assert.equal(abilityTrustBanner(active), null);
  assert.equal(abilityTrustBanner(broken).kind, 'err');
  assert.equal(abilityTrustBanner(drift).icon, 'alert');
  assert.match(abilityTrustBanner(drift).text, /Hash mismatch/);
  assert.equal(abilityTrustBanner(quar).icon, 'lock');

  const secs = buildSurfaceSections(active);
  assert.equal(secs.length, 6);
  assert.equal(secs[0].heading, 'Sidebar Menus');
  assert.equal(secs[0].rows[0].menuId, 'jwt-panel');
  assert.equal(secs[5].heading, 'Starlark Actions');
  assert.equal(secs[5].rows[0].badge, 'MCP tool');

  const url = abilityAssetUrl('jwt-xray', 'ui/panel.js', 'sha256:abc');
  assert.equal(url, '/abilities/jwt-xray/sha256%3Aabc/ui/panel.js');
  assert.equal(new URL('./shared.js', 'http://localhost' + url).pathname, '/abilities/jwt-xray/sha256%3Aabc/ui/shared.js');
  assert.equal(abilityAssetUrl('a/b', '/ui//p.js', 'sha256:abc'), '/abilities/a%2Fb/sha256%3Aabc/ui/p.js');
  assert.equal(sanitizeIconName('i-key!@#'), 'key');
  assert.deepEqual(extractAbilitiesList([{ id: 'x' }]), [{ id: 'x' }]);
  assert.deepEqual(extractAbilitiesList({ abilities: [{ id: 'y' }] }), [{ id: 'y' }]);
  assert.deepEqual(extractAbilitiesList(null), []);
  assert.deepEqual(flowActionInput({ id: 42 }), { flowId: 42 });
  assert.deepEqual(flowActionInput({ method: 'GET' }, true), { flow: { method: 'GET' } });
  assert.deepEqual(flowActionInput(null, true), {});
});
