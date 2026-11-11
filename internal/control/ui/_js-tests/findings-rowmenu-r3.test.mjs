// findings.js row context menu: "Delete finding" must confirm like the header's
// More > Delete does, and a refused or failed write must reach the operator
// instead of becoming an unhandled rejection. The REAL functions are cut from
// findings.js and run against stubs for their collaborators.
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

const src = readFileSync(new URL('../js/findings.js', import.meta.url), 'utf8');
const cut = (a, b) => {
  const i = src.indexOf(a); assert.ok(i >= 0, 'missing ' + a);
  const j = src.indexOf(b, i); assert.ok(j > i, 'missing ' + b);
  return src.slice(i, j);
};
const unhandled = [];
process.on('unhandledRejection', (e) => unhandled.push(e));
const settle = () => new Promise((r) => setTimeout(r, 15));

function harness({ confirmAnswer = true, failPatch = null, failDelete = null } = {}) {
  const log = { confirms: [], deletes: [], patches: [], toasts: [], renders: 0, menu: null };
  const env = {
    findings: [{ id: 7, title: 'Open one', severity: 'High', status: 'open' }, { id: 8, title: 'Row <b>two</b>', severity: 'Low', status: 'open' }],
    findingsEditable: () => true,
    visibleFindings: () => env.findings,
    statusLabel: (s) => String(s), findingStatusIcon: () => 'x', severityKey: (s) => String(s).toLowerCase(),
    STATUSES: ['open', 'verified'], SEVERITY_ORDER: ['critical', 'high', 'medium', 'low', 'info'],
    FORMAT_CHOICES: [], TRACKER_CHOICES: [],
    findingHref: (id) => '#finding-' + id, findingSummaryLine: () => '', copyText() {}, exportOneFinding() {}, openIgnoreReasonMenu() {},
    location: { origin: 'http://ui.example.com', hash: '' },
    esc: (s) => String(s).replace(/[&<>]/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;' }[c])),
    uiConfirm: async (title, html) => { log.confirms.push({ title, html }); return confirmAnswer; },
    deleteFinding: async (id) => { if (failDelete) throw failDelete; log.deletes.push(id); },
    patchFinding: async (id, fields) => { log.patches.push({ id, fields }); if (failPatch) throw failPatch; return { latest: true }; },
    statusSaved: async () => true,
    loadFindings: async () => true,
    renderFindings: () => { log.renders++; },
    toast: (message, kind) => { log.toasts.push({ message, kind }); },
    openCtxMenu: (x, y, sections) => { log.menu = sections; },
  };
  const names = Object.keys(env);
  const make = new Function(...names, `let selFinding = 7;
${cut('function showFindingRowCtx(event, id) {', '// exportOneFinding downloads')}
${cut('async function confirmDeleteFinding(f) {', 'function findingMoreSections(f)')}
return { showFindingRowCtx, selected: () => selFinding };`);
  const api = make(...names.map((n) => env[n]));
  const open = (id) => { api.showFindingRowCtx({ clientX: 1, clientY: 1 }, id); return log.menu[0].items; };
  return { log, open, selected: api.selected };
}
const item = (items, label) => items.find((i) => i && i.label === label);

test('row menu Delete asks first; declining deletes nothing', async () => {
  const h = harness({ confirmAnswer: false });
  await item(h.open(8), 'Delete finding').act();
  assert.equal(h.log.confirms.length, 1, 'no confirmation was shown');
  assert.match(h.log.confirms[0].html, /Row &lt;b&gt;two&lt;\/b&gt;/, 'the confirmation names the row, escaped');
  assert.deepEqual(h.log.deletes, []);
});

test('row menu Delete, confirmed, deletes the row it was opened on and keeps the open finding', async () => {
  const h = harness();
  await item(h.open(8), 'Delete finding').act();
  assert.deepEqual(h.log.deletes, [8]);
  assert.equal(h.selected(), 7);
  assert.ok(h.log.renders > 0, 'the list was not re-rendered');
});

test('row menu Delete refused for unsaved edits tells the operator', async () => {
  const h = harness({ failDelete: new Error('Use Retry to save finding changes before deleting.') });
  await item(h.open(7), 'Delete finding').act();
  await settle();
  assert.ok(h.log.toasts.some((t) => /Use Retry/.test(t.message)), 'the refusal was not shown');
  assert.equal(h.selected(), 7, 'selection moved although nothing was deleted');
  assert.deepEqual(unhandled, []);
});

test('row menu Set severity failure is reported', async () => {
  const h = harness({ failPatch: new Error('network down') });
  const sev = item(h.open(8), 'Set severity');
  sev.children.find((c) => c.label === 'medium').act();
  await settle();
  assert.deepEqual(h.log.patches, [{ id: 8, fields: { severity: 'medium' } }]);
  assert.ok(h.log.toasts.some((t) => t.message === 'network down' && t.kind === 'error'), 'the failure was not shown');
  assert.deepEqual(unhandled, []);
});
