import test, { after } from 'node:test';
import assert from 'node:assert/strict';
import { registerHooks } from 'node:module';
import { abilityAssetUrl, collectInjectedMenus } from '../js/abilities-model.js';

// Exercise real host/core modules with just the DOM operations their slots use.
class Element extends EventTarget {
  constructor(tag) { super(); this.tag = tag; }
  children = [];
  dataset = {};
  parent = null;
  text = '';
  setAttribute() {}
  get isConnected() { return this === root || !!this.parent?.isConnected; }
  get childElementCount() { return this.children.length; }
  get textContent() { return this.text + this.children.map(c => c.textContent).join(''); }
  set textContent(text) { this.replaceChildren(); this.text = text; }
  appendChild(child) { this.children.push(child); child.parent = this; return child; }
  append(...children) { children.forEach(c => this.appendChild(c)); }
  remove() { if (this.parent) this.parent.children.splice(this.parent.children.indexOf(this), 1); this.parent = null; }
  replaceChildren(...children) { for (const c of [...this.children]) c.remove(); this.text = ''; this.append(...children); }
  querySelector() { return null; }
  querySelectorAll() { return []; }
}
const root = new Element('body');
const panel = root.appendChild(new Element('div'));
const selectors = new Map([['.panel[data-panel="ability:fixture:panel"]', panel]]);
globalThis.document = Object.assign(new EventTarget(), {
  readyState: 'loading', documentElement: root, body: root, head: new Element('head'),
  createElement: tag => new Element(tag), createElementNS: (_, tag) => new Element(tag),
  createTextNode: text => Object.assign(new Element('#text'), { text }),
  getElementById: () => null, querySelector: s => selectors.get(s) ?? null, querySelectorAll: () => [],
});
globalThis.window = new EventTarget();
globalThis.MutationObserver = class { observe() {} };

const fixture = {
  id: 'fixture', digest: 'sha256:' + '1'.repeat(64), enabled: true, trusted: true,
  manifest: {
    menus: [{ id: 'panel', label: 'Fixture panel', entry: 'ui/main.js' }],
    inspectorTabs: ['one', 'two'].map(id => ({ id, label: id, side: 'both', entry: 'ui/main.js' })),
    contextMenus: [{ id: 'entry', label: 'Fixture menu', target: 'flow', entry: 'ui/main.js' }],
  },
};
const localModule = new URL('./fixtures/ability-host-fixture.mjs', import.meta.url);
const mockAssets = registerHooks({
  resolve(specifier, context, nextResolve) {
    if (specifier === abilityAssetUrl(fixture.id, 'ui/main.js', fixture.digest)) return nextResolve(localModule.href, context);
    return nextResolve(specifier, context);
  },
});
after(() => mockAssets.deregister());

const { configureAbilityHost, mountAbilityPanel, abilityInspectorTabs, abilityFlowContextItems, syncInjectedMenus } = await import('../js/abilities-host.js');
const listeners = new Set();
let abilities = [fixture];
configureAbilityHost({
  isLoaded: () => true,
  getAbilities: () => abilities,
  getHostCtx: () => ({
    getSelectedFlow: () => ({ id: 1, label: 'Selected & ready' }),
    registerSseHandler: (_, fn) => { listeners.add(fn); return () => listeners.delete(fn); },
  }),
});

const inspector = (id, flow) => abilityInspectorTabs(flow, 'req').find(t => t.id === `ab:fixture:${id}`);
const newHost = () => root.appendChild(new Element('div'));
function gatedFlow(id, label) {
  const entered = Promise.withResolvers();
  const proceed = Promise.withResolvers();
  const events = [];
  return {
    flow: { id, label, events, wait: () => { entered.resolve(); return proceed.promise; } },
    waitForEntry: pending => Promise.race([entered.promise, pending.then(() => assert.fail('fixture never entered mount or update'))]),
    proceed: proceed.resolve, events,
  };
}

test('sidebar panel renders SDK-escaped selected-flow content and updates it', async () => {
  assert.equal(await mountAbilityPanel('ability:fixture:panel'), true, panel.textContent);
  assert.equal(panel.textContent, 'mount:panel/panel:&lt;Selected &amp; ready&gt;');
  assert.equal(await mountAbilityPanel('ability:fixture:panel', { flow: { id: 2, label: 'New' } }), true);
  assert.equal(panel.textContent, 'update:panel/panel:&lt;New&gt;');
});

test('inspector supersession leaves the latest tab content and unmounts the old module', async () => {
  const host = newHost();
  const first = gatedFlow(1, 'Old');
  const pending = inspector('one', first.flow).mount(host);
  await first.waitForEntry(pending);
  const oldSlot = [...listeners][0];
  const dispose = await inspector('two', { id: 2, label: 'New' }).mount(host);
  assert.equal(host.textContent, 'mount:two/req:&lt;New&gt;');
  first.proceed();
  const staleDispose = await pending;
  staleDispose(); // Proxy and Drawer discard stale completions after an awaited mount.
  assert.equal(host.textContent, 'mount:two/req:&lt;New&gt;');
  assert.deepEqual(first.events, ['unmount']);
  assert.equal(listeners.has(oldSlot), false);
  dispose();
  assert.equal(host.textContent, '');
});

test('closing an inspector immediately drops in-flight subscriptions and late DOM', async () => {
  const host = newHost();
  const step = gatedFlow(1, 'Closing');
  const pending = inspector('one', step.flow).mount(host);
  await step.waitForEntry(pending);
  assert.equal(listeners.size, 1);
  host._abilityUnmount();
  assert.equal(listeners.size, 0);
  step.proceed();
  await pending;
  assert.equal(host.textContent, '');
  assert.deepEqual(step.events, ['unmount']);
  assert.equal(listeners.size, 0);
});

test('inspector updates finish in context order and stale cleanup leaves the newest result', async () => {
  const host = newHost();
  await inspector('one', { id: 1, label: 'Open' }).mount(host);
  const step = gatedFlow(2, 'Slow');
  const pending = inspector('one', step.flow).mount(host);
  await step.waitForEntry(pending);
  const latest = inspector('one', { id: 3, label: 'Latest' }).mount(host);
  step.proceed();
  const [staleDispose, dispose] = await Promise.all([pending, latest]);
  staleDispose();
  assert.equal(host.textContent, 'update:one/req:&lt;Latest&gt;');
  dispose();
});

test('context entry runs with real SDK and cleans up even when its module rejects', async () => {
  const ran = Promise.withResolvers();
  const flow = { id: 7, label: 'Menu & flow', record: ran.resolve };
  abilityFlowContextItems(flow)[0].act();
  // The menu action is intentionally fire-and-forget; flush local module jobs, then assert the effect.
  for (let attempts = 0; attempts < 20 && !flow.rendered; attempts++) await new Promise(setImmediate);
  assert.equal(flow.rendered, 'entry:&lt;Menu &amp; flow&gt;');
  await ran.promise;
  assert.equal(listeners.size, 0);
});

after(() => { abilities = []; syncInjectedMenus(collectInjectedMenus(abilities)); });
