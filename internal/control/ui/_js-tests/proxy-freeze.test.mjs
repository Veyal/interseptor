// History freeze (Pause live updates) must actually freeze.
//
// proxy.js touches the DOM at load, so the reload policy is sliced out of it the
// same way windowed-list.test.mjs slices core.js: armReload / scheduleReload /
// suspendReload / takeDeferredReload / noteFrozenArrival are evaluated against a
// fake timer pool and a fake flow predicate. The old code armed the debounce from
// inside scheduleReload unconditionally, so a reload triggered while frozen
// (a non-live-default sort in handleFlowNew, a sort-key change in
// handleFlowUpdate) re-sorted and re-rendered the list the operator had pinned —
// the first test below is the one that fails on that shape.
import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';

const proxySrc = fs.readFileSync(new URL('../js/proxy.js', import.meta.url), 'utf8');

function sliceFunction(src, signature) {
  const start = src.indexOf(signature);
  assert.ok(start >= 0, 'proxy.js has ' + signature);
  let depth = 0;
  for (let i = src.indexOf('{', start); i < src.length; i++) {
    if (src[i] === '{') depth++;
    else if (src[i] === '}' && --depth === 0) return src.slice(start, i + 1);
  }
  throw new Error('unbalanced ' + signature);
}

const sliceExported = (signature) => sliceFunction(proxySrc, signature).replace(/^export /, '');

// The pre-fix body, verbatim. It armed the debounce unconditionally, so a reload
// triggered while frozen fired ~150 ms after the pause. It lives here as a fixture
// so the harness below is provably able to tell the defect from the fix instead of
// asserting a shape nothing could violate.
const LEGACY_SCHEDULE_RELOAD = `function scheduleReload(){
  const now=Date.now();
  if(!reloadFirstAt)reloadFirstAt=now;
  clearTimeout(reloadTimer);
  const wait=Math.max(0,Math.min(RELOAD_DEBOUNCE_MS,RELOAD_MAX_WAIT_MS-(now-reloadFirstAt)));
  reloadTimer=setTimeout(()=>{reloadFirstAt=0;loadFlows();},wait);
}`;

// The live-reload gate, taken verbatim from proxy.js and run against stub state.
function makeGate(scheduleSrc = sliceExported('export function scheduleReload(){')) {
  return new Function(`
let liveFrozen=false,frozenNew=0;
let reloadTimer=null,reloadFirstAt=0,reloadPendingWhileFrozen=false;
const RELOAD_DEBOUNCE_MS=150,RELOAD_MAX_WAIT_MS=1000;
const loads=[];
const loadFlows=()=>loads.push(1);
const renderRows=()=>{};
const timers=new Map();let nextId=1;
const setTimeout=(fn,ms)=>{const id=nextId++;timers.set(id,{fn,ms});return id;};
const clearTimeout=(id)=>{timers.delete(id);};
const badges=[];
const updateFreezeBadge=()=>badges.push(frozenNew);
let incremental=true;
const canIncremental=()=>incremental;
const flowMatchesFilters=(f)=>f.matched!==false;
${sliceFunction(proxySrc, 'function armReload(')}
${scheduleSrc}
${sliceFunction(proxySrc, 'function suspendReload(')}
${sliceFunction(proxySrc, 'function takeDeferredReload(')}
${sliceFunction(proxySrc, 'function noteFrozenArrival(')}
return {
  scheduleReload,suspendReload,takeDeferredReload,noteFrozenArrival,
  freeze(on){liveFrozen=on;},
  setIncremental(on){incremental=on;},
  pending(){return reloadPendingWhileFrozen;},
  frozenCount(){return frozenNew;},
  badges(){return badges;},
  timerCount(){return timers.size;},
  fireTimers(){for(const [id,t] of [...timers]){timers.delete(id);t.fn();}},
  loads(){return loads;},
};
`)();
}

test('the harness catches the pre-fix scheduleReload, so the green cases are not vacuous', () => {
  const legacy = makeGate(LEGACY_SCHEDULE_RELOAD);
  legacy.freeze(true);
  legacy.scheduleReload();
  assert.equal(legacy.timerCount(), 1, 'the old body armed a reload onto the frozen list');
  assert.equal(legacy.pending(), false, 'and forgot it, so Pause could not be trusted');
});


test('a reload trigger while frozen arms no timer and is remembered instead', () => {
  const g = makeGate();
  g.freeze(true);
  g.scheduleReload();
  // The old scheduleReload armed the debounce here, so the frozen list was
  // re-sorted/re-rendered 150 ms after the pause.
  assert.equal(g.timerCount(), 0, 'no reload may be armed while frozen');
  assert.equal(g.pending(), true, 'the trigger is deferred, not dropped');
  g.scheduleReload();
  g.scheduleReload();
  assert.equal(g.timerCount(), 0);
  assert.equal(g.pending(), true);
  assert.deepEqual(g.loads(), [], 'nothing reloaded');
});

test('unfreezing honours exactly one deferred reload, then reloads normally again', () => {
  const g = makeGate();
  g.freeze(true);
  g.scheduleReload();
  g.freeze(false);
  // setLiveFrozen's resume path: takeDeferredReload() once, then a normal reload.
  assert.equal(g.takeDeferredReload(), true, 'resume reloads the state the freeze held back');
  assert.equal(g.takeDeferredReload(), false, 'the deferral is one-shot');
  assert.equal(g.pending(), false);
  g.scheduleReload();
  assert.equal(g.timerCount(), 1, 'unfrozen triggers debounce again');
});

test('an armed reload is cancelled when the operator pauses mid-debounce', () => {
  const g = makeGate();
  g.scheduleReload();
  assert.equal(g.timerCount(), 1);
  g.suspendReload();
  assert.equal(g.timerCount(), 0, 'the pending timer must not fire onto a frozen list');
  assert.equal(g.pending(), true, 'but it is not forgotten: resuming reloads');
  g.suspendReload();
  assert.equal(g.pending(), true, 'suspending twice is harmless');
});

test('a trigger with no freeze still debounces and fires loadFlows once', () => {
  const g = makeGate();
  g.scheduleReload();
  g.scheduleReload();
  assert.equal(g.timerCount(), 1, 'bursts collapse into one timer');
  g.fireTimers();
  assert.deepEqual(g.loads(), [1]);
  g.scheduleReload();
  assert.equal(g.timerCount(), 1);
  g.fireTimers();
  assert.deepEqual(g.loads(), [1, 1], 'and the debounce re-arms after it fires');
  assert.equal(g.pending(), false);
});

test('the frozen badge counts the arrivals the freeze is holding back', () => {
  const g = makeGate();
  g.noteFrozenArrival({ matched: true });
  assert.equal(g.frozenCount(), 0, 'not frozen: nothing is held back');
  g.freeze(true);
  assert.equal(g.noteFrozenArrival({ matched: true }), true);
  assert.equal(g.frozenCount(), 1);
  assert.equal(g.noteFrozenArrival({ matched: false }), false, 'a flow the filters rule out is not "new"');
  assert.equal(g.frozenCount(), 1);
  g.setIncremental(false); // a text search / regex scope rule: only the server can decide
  assert.equal(g.noteFrozenArrival({ matched: false }), true);
  assert.equal(g.frozenCount(), 2, 'undecidable arrivals still count rather than going unreported');
  assert.deepEqual(g.badges(), [1, 2], 'the badge is repainted on every count');
});

test('scheduleReload keeps the freeze gate as the only path to the debounce', () => {
  const body = sliceFunction(proxySrc, 'export function scheduleReload(){');
  assert.ok(body.includes('if(liveFrozen){reloadPendingWhileFrozen=true;return;}'),
    'frozen triggers are deferred before anything can be armed');
  assert.ok(!body.includes('setTimeout('), 'arming lives in armReload so the gate cannot be bypassed');
  assert.ok(body.indexOf('liveFrozen') < body.indexOf('armReload()'),
    'the freeze check comes first');
});

test('freeze/resume wiring: pause suspends, resume reloads the held-back state', () => {
  const freeze = sliceFunction(proxySrc, 'export function setLiveFrozen(on){');
  assert.ok(freeze.includes('suspendReload();'), 'pausing cancels a debouncing reload');
  assert.ok(freeze.includes('const deferred=takeDeferredReload();'));
  assert.ok(freeze.includes('if(deferred)loadFlows();else renderRows();'),
    'resume must fetch the latest list when a reload was held back');
  const badge = sliceFunction(proxySrc, 'function updateFreezeBadge(){');
  assert.ok(badge.includes("FREEZE_RESUME_LABEL"), 'the accessible name follows the state');
  assert.ok(badge.includes('held while paused'), 'the badge tooltip says what "+N" means');
});

test('live handlers do not paint the frozen list', () => {
  const newFlow = sliceFunction(proxySrc, 'export function handleFlowNew(f){');
  assert.ok(newFlow.includes('if(active)noteFrozenArrival(f);'),
    'a flow that needs a reload is still counted while frozen');
  const deferredAt = newFlow.indexOf('noteFrozenArrival(f)');
  const reloadAt = newFlow.indexOf('scheduleReload()');
  assert.ok(deferredAt > 0 && deferredAt < reloadAt, 'counted before the deferred trigger');
  // The Go contract (TestUILiveStreamFreeze) pins this exact line and the upsert
  // that must precede it.
  assert.ok(newFlow.includes('if(liveFrozen){frozenNew++;updateFreezeBadge();return;}'));
  assert.ok(newFlow.indexOf('upsertFlow(f);') < newFlow.indexOf('if(liveFrozen){frozenNew++'),
    'capture still happens before the freeze holds the viewport');

  const update = sliceFunction(proxySrc, 'export function handleFlowUpdate(f){');
  assert.ok(update.includes('const paint=active&&!liveFrozen;'), 'one gate for every row write');
  assert.ok(!/if\(active\)flowRowLiveUpdate/.test(update), 'no row write outside the gate');
  assert.ok(update.includes('if(!flowMatchesFilters(f)){') && update.includes('if(paint){const row='),
    'the store is still corrected while frozen; only the DOM is held');
  const rebuild = sliceFunction(proxySrc, 'function queueFullWindowRebuild(){');
  assert.ok(rebuild.includes('if(liveFrozen)return;'), 'a rebuild queued just before Pause is dropped');
  // The visibility catch-up renders from state.flows, which keeps growing while
  // frozen: ungated, returning to the tab would reveal every held-back row.
  const catchUp = proxySrc.match(/document\.addEventListener\('visibilitychange',\(\)=>\{if\(!document\.hidden&&flowVirt\.isActive\(\)[^}]*\)renderRows\(\);\}\);/);
  assert.ok(catchUp, 'the virtualized catch-up render is still wired');
  assert.ok(catchUp[0].includes('!liveFrozen'), 'a frozen list is never repainted by the catch-up');
});
