import test from 'node:test';
import assert from 'node:assert/strict';
import {
  REFRESH_DEBOUNCE_MS, FLOW_NEW_THROTTLE_MS, createProjectState, foldAggregate, foldFallback,
  buildBlockers, blockerLabel, humanizeCode, nextActionFrom, readinessValuetext,
  identityHue, scopeChipModel, evidenceSummary, createBlockerAnnouncer, SEGMENTS,
} from '../js/project-state.js';

function clock() {
  let t = 1000;
  const timers = [];
  return {
    now: () => t,
    advance(ms) {
      t += ms;
      for (const x of timers.slice()) if (x.at <= t && !x.done) { x.done = true; x.fn(); }
    },
    setTimeout(fn, ms) { const x = { at: t + ms, fn, done: false }; timers.push(x); return x; },
    clearTimeout(x) { if (x) x.done = true; },
  };
}
const flush = async () => { for (let i = 0; i < 12; i++) await Promise.resolve(); };

const AGG = {
  scope: { enabled: true, in: 12, out: 3 },
  brief: { target: 'app.example.com', ok: true },
  evidence: { flows: 41, shots: 6, ws: 2 },
  findings: { total: 3, ready: 1, truncated: false, items: [
    { id: 1, stage: 'report_ready', gaps: [] },
    { id: 2, stage: 'draft', gaps: ['evidence', 'cvss'] },
    { id: 3, stage: 'draft', gaps: ['cvss', 'mystery_code'] },
  ] },
  blockers: ['evidence', 'cvss', 'mystery_code'],
};

function make(fetchJson, extra = {}) {
  const c = clock();
  const ps = createProjectState({ fetchJson, now: c.now, setTimeout: c.setTimeout, clearTimeout: c.clearTimeout, ...extra });
  return { c, ps };
}

test('foldAggregate normalises the server aggregate and caps items', () => {
  const s = foldAggregate(AGG);
  assert.deepEqual(s.scope, { enabled: true, inCount: 12, outCount: 3 });
  assert.deepEqual(s.evidence, { flows: 41, shots: 6, ws: 2 });
  assert.equal(s.findings.total, 3);
  assert.equal(s.findings.items[1].readiness.stage, 'draft');
  const big = foldAggregate({ findings: { total: 500, ready: 0, items: Array.from({ length: 500 }, (_, i) => ({ id: i + 1, stage: 'draft', gaps: [] })) } });
  assert.equal(big.findings.items.length, 200);
  assert.equal(big.findings.truncated, true);
  const empty = foldAggregate(null);
  assert.deepEqual(empty.findings.items, []);
  assert.equal(empty.brief.ok, false);
});

test('buildBlockers uses server codes, links findings and never invents codes', () => {
  const s = foldAggregate(AGG);
  const b = buildBlockers(AGG.blockers, s.findings.items);
  assert.deepEqual(b.map((x) => x.code), ['evidence', 'cvss', 'mystery_code']);
  assert.deepEqual(b[1].findingIds, [2, 3]);
  assert.equal(b[0].href, '#finding-2/evidence');
  assert.equal(b[1].href, '#finding-2/review');
  assert.equal(b[2].label, 'Mystery code');
  const proj = buildBlockers(['brief_target', 'scope'], []);
  assert.equal(proj[0].scope, 'project');
  assert.equal(proj[0].fix, 'settings:scope');
  assert.equal(proj[1].label, blockerLabel('scope'));
  assert.equal(humanizeCode('capability:account_control'), 'Claim: account control');
});

test('foldFallback reproduces the aggregate codes from the individual endpoints', () => {
  const s = foldFallback({
    scope: { rules: [{ action: 'include', enabled: true }, { action: 'include', enabled: false }, { action: 'exclude', enabled: true }] },
    brief: { scope: '  app.example.com  ' },
    findings: { findings: [
      { id: 1, ready: true, readiness: { stage: 'report_ready', gaps: [], screenshotCount: 2 } },
      { id: 2, ready: false, readiness: { stage: 'draft', gaps: ['evidence'], screenshotCount: 1 } },
      { id: 3, ready: false, missing: ['poc', 'impact'] },
    ] },
  });
  assert.deepEqual(s.scope, { enabled: true, inCount: 1, outCount: 1 });
  assert.equal(s.brief.target, 'app.example.com');
  assert.equal(s.evidence.shots, 3);
  assert.equal(s.findings.ready, 1);
  assert.deepEqual(s.findings.items[2].gaps, ['evidence', 'impact']);
  assert.deepEqual(s.serverBlockers, ['evidence', 'impact']);
});

test('nextActionFrom follows the documented priority', () => {
  const base = foldAggregate(AGG);
  assert.equal(nextActionFrom({ ...base, brief: { target: '', ok: false } }).kind, 'set-target');
  assert.equal(nextActionFrom({ ...base, scope: { enabled: false, inCount: 0, outCount: 0 } }).kind, 'enable-scope');
  assert.equal(nextActionFrom({ ...base, evidence: { flows: 0, shots: 0, ws: 0 } }).kind, 'capture');
  const attach = nextActionFrom(base);
  assert.equal(attach.kind, 'attach-proof');
  assert.equal(attach.findingId, 2);
  assert.match(attach.label, /F-2/);
  const noProof = foldAggregate({ ...AGG, findings: { total: 1, ready: 0, items: [{ id: 9, stage: 'draft', gaps: ['cvss'] }] } });
  assert.equal(nextActionFrom(noProof).kind, 'fix-blockers');
  const none = foldAggregate({ ...AGG, findings: { total: 0, ready: 0, items: [] } });
  assert.equal(nextActionFrom(none).kind, 'new-finding');
  const ready = foldAggregate({ ...AGG, findings: { total: 1, ready: 1, items: [{ id: 1, stage: 'report_ready', gaps: [] }] } });
  assert.equal(nextActionFrom(ready).kind, 'export');
});

test('readinessValuetext covers none, some and all passing', () => {
  assert.equal(readinessValuetext({ total: 0, ready: 0 }, []), 'No findings yet');
  assert.equal(readinessValuetext({ total: 5, ready: 2 }, [1, 2, 3]), '2 of 5 findings ready; 3 blockers');
  assert.equal(readinessValuetext({ total: 5, ready: 5 }, []), '5 of 5 findings ready; no blockers');
  assert.equal(readinessValuetext({ total: 2, ready: 0 }, [1]), '0 of 2 findings ready; 1 blocker');
});

test('refresh fills every segment from the aggregate and the identities list', async () => {
  const calls = [];
  const { c, ps } = make(async (path) => {
    calls.push(path);
    if (path === '/api/project/readiness') return AGG;
    if (path === '/api/authz') return { identities: [{ name: 'admin' }, { name: '' }, { name: 'user-b' }] };
    throw new Error('unexpected ' + path);
  });
  const p = ps.refresh({ reason: 'boot' });
  c.advance(REFRESH_DEBOUNCE_MS);
  await p;
  const s = ps.get();
  assert.equal(s.loaded, true);
  assert.equal(s.scope.inCount, 12);
  assert.deepEqual(s.identities.map((i) => i.name), ['admin', 'user-b']);
  assert.equal(s.blockers.length, 3);
  assert.equal(s.nextAction.kind, 'attach-proof');
  assert.ok(SEGMENTS.every((k) => s.stale[k] === false));
  assert.deepEqual(calls.sort(), ['/api/authz', '/api/project/readiness']);
});

test('a rejected segment marks only that segment stale and keeps the last good data', async () => {
  let failIdentities = false;
  const { c, ps } = make(async (path) => {
    if (path === '/api/project/readiness') return AGG;
    if (path === '/api/authz') { if (failIdentities) throw new Error('boom'); return { identities: [{ name: 'admin' }] }; }
    throw new Error('unexpected');
  });
  let p = ps.refresh(); c.advance(REFRESH_DEBOUNCE_MS); await p;
  failIdentities = true;
  p = ps.refresh(); c.advance(REFRESH_DEBOUNCE_MS); await p;
  const s = ps.get();
  assert.equal(s.stale.identities, true);
  assert.equal(s.stale.scope, false);
  assert.equal(s.identities.length, 1, 'identities keep their last good value');
  assert.equal(s.scope.inCount, 12);
});

test('aggregate failure falls back to per-endpoint fetches and flags only what is missing', async () => {
  const { c, ps } = make(async (path) => {
    if (path === '/api/project/readiness') throw new Error('404');
    if (path === '/api/scope') return { rules: [{ action: 'include', enabled: true }] };
    if (path === '/api/engagement-brief') throw new Error('brief down');
    if (path === '/api/findings') return { findings: [{ id: 1, ready: false, readiness: { stage: 'draft', gaps: ['evidence'] } }] };
    if (path === '/api/authz') return { identities: [] };
    throw new Error('unexpected ' + path);
  });
  const p = ps.refresh(); c.advance(REFRESH_DEBOUNCE_MS); await p;
  const s = ps.get();
  assert.equal(s.stale.brief, true);
  assert.equal(s.stale.scope, false);
  assert.equal(s.stale.findings, false);
  assert.equal(s.stale.evidence, true, 'flow and websocket counts are only known from the aggregate');
  assert.equal(s.scope.enabled, true);
  assert.equal(s.findings.total, 1);
});

test('refresh calls are debounced to one trailing run per 250ms', async () => {
  let n = 0;
  const { c, ps } = make(async (path) => { if (path === '/api/project/readiness') { n++; return AGG; } return { identities: [] }; });
  const a = ps.refresh(); const b = ps.refresh(); ps.refresh();
  c.advance(REFRESH_DEBOUNCE_MS - 1);
  await flush();
  assert.equal(n, 0);
  c.advance(1);
  await Promise.all([a, b]);
  assert.equal(n, 1);
});

test('flow.new refreshes at most once per 2 seconds', async () => {
  let n = 0;
  const { c, ps } = make(async (path) => { if (path === '/api/project/readiness') { n++; return AGG; } return { identities: [] }; });
  ps.noteFlowNew();
  c.advance(REFRESH_DEBOUNCE_MS); await flush();
  assert.equal(n, 1);
  for (let i = 0; i < 20; i++) { c.advance(50); ps.noteFlowNew(); }
  c.advance(REFRESH_DEBOUNCE_MS); await flush();
  assert.equal(n, 1, 'a burst inside the throttle window does not refresh');
  c.advance(FLOW_NEW_THROTTLE_MS); c.advance(REFRESH_DEBOUNCE_MS); await flush();
  assert.equal(n, 2, 'one trailing refresh runs after the window');
});

test('an older in-flight refresh cannot overwrite a newer one', async () => {
  const gates = [];
  const { c, ps } = make((path) => {
    if (path !== '/api/project/readiness') return Promise.resolve({ identities: [] });
    return new Promise((res) => gates.push(res));
  });
  const first = ps.refresh(); c.advance(REFRESH_DEBOUNCE_MS); await flush();
  const second = ps.refresh(); c.advance(REFRESH_DEBOUNCE_MS); await flush();
  gates[1]({ ...AGG, scope: { enabled: true, in: 9, out: 0 } });
  await second;
  gates[0]({ ...AGG, scope: { enabled: true, in: 1, out: 0 } });
  await first;
  assert.equal(ps.get().scope.inCount, 9);
});

test('subscribers are notified and can unsubscribe', async () => {
  const { c, ps } = make(async (p) => (p === '/api/project/readiness' ? AGG : { identities: [] }));
  let seen = 0;
  const off = ps.subscribe(() => { seen++; });
  const p = ps.refresh(); c.advance(REFRESH_DEBOUNCE_MS); await p;
  assert.ok(seen >= 1);
  off();
  const before = seen;
  const q = ps.refresh(); c.advance(REFRESH_DEBOUNCE_MS); await q;
  assert.equal(seen, before);
});

test('the active identity is validated against the known identities', async () => {
  const { c, ps } = make(async (p) => (p === '/api/project/readiness' ? AGG : { identities: [{ name: 'admin' }, { name: 'user-b' }] }));
  const p = ps.refresh(); c.advance(REFRESH_DEBOUNCE_MS); await p;
  assert.equal(ps.setActiveIdentity('user-b'), true);
  assert.equal(ps.get().activeIdentity, 'user-b');
  assert.equal(ps.setActiveIdentity('nobody'), false);
  assert.equal(ps.get().activeIdentity, 'user-b');
  assert.equal(ps.setActiveIdentity(''), true);
  assert.equal(ps.get().activeIdentity, '');
});

test('identityHue is stable and within 1..6', () => {
  assert.equal(identityHue('admin'), identityHue('admin'));
  for (const n of ['admin', 'user-b', 'x', '']) assert.ok(identityHue(n) >= 1 && identityHue(n) <= 6);
});

test('scopeChipModel renders scope state with text, never colour alone', () => {
  const on = scopeChipModel({ scope: { enabled: true, inCount: 12, outCount: 3 } }, { filterOn: false });
  assert.equal(on.checked, false);
  assert.match(on.text, /12 in/);
  assert.match(on.text, /3 out/);
  assert.match(on.label, /in-scope filter/i);
  const filtering = scopeChipModel({ scope: { enabled: true, inCount: 1, outCount: 0 } }, { filterOn: true });
  assert.equal(filtering.checked, true);
  const none = scopeChipModel({ scope: { enabled: false, inCount: 0, outCount: 0 } }, { filterOn: false });
  assert.equal(none.disabled, true);
  assert.match(none.text, /No scope/i);
  const stale = scopeChipModel({ scope: { enabled: true, inCount: 1, outCount: 0 }, stale: { scope: true } }, {});
  assert.equal(stale.stale, true);
  assert.match(stale.title, /Could not refresh scope/);
});

test('evidenceSummary pluralises counts', () => {
  assert.equal(evidenceSummary({ flows: 1, shots: 0, ws: 2 }), '1 flow, 0 shots, 2 ws');
});

test('blocker announcements are limited to one per 5 seconds and only on change', () => {
  const c = clock();
  const said = [];
  const a = createBlockerAnnouncer({ now: c.now, setTimeout: c.setTimeout, clearTimeout: c.clearTimeout, emit: (m) => said.push(m) });
  a.update(3);
  assert.deepEqual(said, ['3 blockers']);
  a.update(3);
  c.advance(10);
  a.update(2);
  a.update(1);
  assert.deepEqual(said, ['3 blockers'], 'inside the window nothing is spoken yet');
  c.advance(5000);
  assert.deepEqual(said, ['3 blockers', '1 blocker'], 'the latest count is spoken once the window ends');
  a.update(0);
  c.advance(5000);
  assert.equal(said.at(-1), 'No blockers');
});

test('a throwing load never leaves an unhandled rejection and the next refresh recovers', async () => {
  const unhandled = [];
  const onUnhandled = (e) => unhandled.push(e);
  process.on('unhandledRejection', onUnhandled);
  try {
    let poisoned = true;
    const { c, ps } = make(async (path) => {
      if (path.includes('readiness')) {
        if (poisoned) return { get scope() { throw new Error('poisoned aggregate'); } };
        return AGG;
      }
      return { identities: [] };
    });
    const first = ps.refresh();
    c.advance(REFRESH_DEBOUNCE_MS + 1);
    await first; // waiters are released even though the load threw
    await new Promise((r) => setImmediate(r));
    assert.deepEqual(unhandled, [], 'the dropped run() promise must be handled');
    poisoned = false;
    const second = ps.refresh();
    c.advance(REFRESH_DEBOUNCE_MS + 1);
    await second;
    await flush();
    assert.equal(ps.get().loaded, true, 'the state recovers on the next refresh');
  } finally {
    process.off('unhandledRejection', onUnhandled);
  }
});
