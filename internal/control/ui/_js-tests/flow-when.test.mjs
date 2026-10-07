import test from 'node:test';
import assert from 'node:assert/strict';
import { formatFlowWhen, formatFlowWhenFull, msUntilNextMidnight } from '../js/flow-when.js';

// Local-time constructors keep these assertions valid under any TZ.
const L = (y, mo, d, h = 0, mi = 0, s = 0, ms = 0) => new Date(y, mo - 1, d, h, mi, s, ms).getTime();
const NOW = L(2026, 10, 7, 15, 0, 0);

test('today shows time only, 24h, with seconds', () => {
  const r = formatFlowWhen(L(2026, 10, 7, 9, 5, 3, 120), NOW);
  assert.deepEqual({ date: r.date, time: r.time, kind: r.kind }, { date: '', time: '09:05:03', kind: 'today' });
});

test('yesterday is labelled Yest and keeps seconds', () => {
  const r = formatFlowWhen(L(2026, 10, 6, 23, 59, 59), NOW);
  assert.deepEqual({ date: r.date, time: r.time, kind: r.kind }, { date: 'Yest', time: '23:59:59', kind: 'yesterday' });
});

test('same calendar year uses a fixed English month table', () => {
  const r = formatFlowWhen(L(2026, 1, 5, 14, 32, 5), NOW);
  assert.deepEqual({ date: r.date, time: r.time, kind: r.kind }, { date: 'Jan 5', time: '14:32:05', kind: 'year' });
  assert.equal(formatFlowWhen(L(2026, 9, 30, 0, 0, 0), NOW).date, 'Sep 30');
});

test('another year uses ISO date and drops seconds', () => {
  const r = formatFlowWhen(L(2025, 10, 5, 14, 32, 59), NOW);
  assert.deepEqual({ date: r.date, time: r.time, kind: r.kind }, { date: '2025-10-05', time: '14:32', kind: 'other' });
});

test('midnight boundary: 23:59:59 vs 00:00:00 are different days', () => {
  const now = L(2026, 10, 7, 0, 0, 0);
  assert.equal(formatFlowWhen(L(2026, 10, 6, 23, 59, 59), now).date, 'Yest');
  assert.equal(formatFlowWhen(L(2026, 10, 7, 0, 0, 0), now).date, '');
  const justBefore = L(2026, 10, 6, 23, 59, 59);
  assert.equal(formatFlowWhen(L(2026, 10, 6, 0, 0, 0), justBefore).date, '');
  assert.equal(formatFlowWhen(L(2026, 10, 5, 23, 59, 59), justBefore).date, 'Yest');
});

test('a 24h delta that crosses midnight is yesterday, a 23h delta on the same day is today', () => {
  assert.equal(formatFlowWhen(L(2026, 10, 6, 23, 0, 0), L(2026, 10, 7, 1, 0, 0)).date, 'Yest');
  assert.equal(formatFlowWhen(L(2026, 10, 7, 0, 30, 0), L(2026, 10, 7, 23, 30, 0)).date, '');
});

test('Dec 31 -> Jan 1 and month boundaries', () => {
  const jan1 = L(2027, 1, 1, 8, 0, 0);
  assert.equal(formatFlowWhen(L(2026, 12, 31, 23, 59, 59), jan1).date, 'Yest');
  assert.equal(formatFlowWhen(L(2026, 12, 30, 12, 0, 0), jan1).date, '2026-12-30');
  const mar1 = L(2026, 3, 1, 8, 0, 0);
  assert.equal(formatFlowWhen(L(2026, 2, 28, 23, 0, 0), mar1).date, 'Yest');
  const leapMar1 = L(2028, 3, 1, 8, 0, 0);
  assert.equal(formatFlowWhen(L(2028, 2, 29, 23, 0, 0), leapMar1).date, 'Yest');
});

test('invalid, zero and missing timestamps render a dash', () => {
  for (const bad of [undefined, null, '', 0, -5, NaN, Infinity, 'abc', {}]) {
    const r = formatFlowWhen(bad, NOW);
    assert.deepEqual({ date: r.date, time: r.time, title: r.title }, { date: '', time: '—', title: '' }, String(bad));
  }
});

test('a timestamp later than now (clock skew) never shows Yest', () => {
  assert.equal(formatFlowWhen(L(2026, 10, 7, 23, 59, 0), NOW).date, '');
  assert.equal(formatFlowWhen(L(2026, 10, 8, 0, 1, 0), NOW).date, 'Oct 8');
});

test('title is the full local datetime with milliseconds and UTC offset', () => {
  const t = L(2026, 10, 7, 9, 5, 3, 7);
  const r = formatFlowWhen(t, NOW);
  assert.match(r.title, /^2026-10-07 09:05:03\.007 UTC[+-]\d\d:\d\d$/);
  assert.equal(r.title, formatFlowWhenFull(t));
  assert.equal(formatFlowWhenFull(0), '');
});

test('msUntilNextMidnight lands on the next local midnight', () => {
  const now = L(2026, 10, 7, 23, 59, 30);
  assert.equal(msUntilNextMidnight(now), 30000);
  assert.equal(msUntilNextMidnight(L(2026, 10, 7, 0, 0, 0)), 24 * 3600 * 1000);
  assert.equal(msUntilNextMidnight(L(2026, 10, 7, 23, 59, 59, 999)), 1000); // floor
});

// Zone-specific behaviour is asserted in child processes so TZ is honoured at startup.
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
const mod = fileURLToPath(new URL('../js/flow-when.js', import.meta.url));
function inZone(tz, body) {
  const code = `import(${JSON.stringify(mod)}).then(m=>{const L=(y,mo,d,h=0,mi=0,s=0)=>new Date(y,mo-1,d,h,mi,s).getTime();${body}})`;
  const r = spawnSync(process.execPath, ['-e', code], { env: { ...process.env, TZ: tz }, encoding: 'utf8' });
  assert.equal(r.status, 0, r.stderr);
  return JSON.parse(r.stdout);
}

test('Asia/Jakarta: fixed +07:00 offset in the title', () => {
  const out = inZone('Asia/Jakarta', `console.log(JSON.stringify(m.formatFlowWhen(L(2026,10,7,9,0,0),L(2026,10,7,10,0,0)).title))`);
  assert.match(out, /^2026-10-07 09:00:00\.000 UTC\+07:00$/);
});

test('Europe/London: spring-forward and fall-back days keep calendar-day semantics', () => {
  // 2026-03-29 has 23 local hours; 2026-10-25 has 25.
  const out = inZone('Europe/London', `
    const a = m.formatFlowWhen(L(2026,3,28,12,0,0), L(2026,3,29,12,0,0)).date;     // Yest although 23h apart
    const b = m.formatFlowWhen(L(2026,3,27,0,30,0), L(2026,3,29,23,30,0)).date;    // two days back
    const c = m.formatFlowWhen(L(2026,10,24,23,30,0), L(2026,10,25,23,30,0)).date; // previous calendar day on the 25h day
    const d = m.formatFlowWhen(L(2026,10,25,0,10,0), L(2026,10,25,23,50,0)).date;  // today on a 25h day
    const t1 = m.formatFlowWhen(L(2026,3,29,12,0,0), L(2026,3,29,13,0,0)).title;
    const t2 = m.formatFlowWhen(L(2026,7,1,12,0,0), L(2026,7,1,13,0,0)).title;
    const ms = m.msUntilNextMidnight(L(2026,3,29,0,30,0)); // 22.5h left on a 23h day
    console.log(JSON.stringify({a,b,c,d,t1:t1.slice(-9),t2:t2.slice(-9),ms}))`);
  assert.deepEqual(out, { a: 'Yest', b: 'Mar 27', c: 'Yest', d: '', t1: 'UTC+01:00', t2: 'UTC+01:00', ms: 22.5 * 3600 * 1000 });
});

test('America/New_York: DST days and negative offsets', () => {
  const out = inZone('America/New_York', `
    const a = m.formatFlowWhen(L(2026,3,7,23,0,0), L(2026,3,8,23,0,0)).date;  // spring-forward day (23h)
    const b = m.formatFlowWhen(L(2026,11,1,0,5,0), L(2026,11,1,23,55,0)).date; // fall-back day (25h)
    const c = m.formatFlowWhen(L(2026,10,31,23,59,0), L(2026,11,1,0,1,0)).date;
    const t = m.formatFlowWhen(L(2026,1,15,12,0,0), L(2026,1,15,13,0,0)).title.slice(-9);
    const t2 = m.formatFlowWhen(L(2026,7,15,12,0,0), L(2026,7,15,13,0,0)).title.slice(-9);
    console.log(JSON.stringify({a,b,c,t,t2}))`);
  assert.deepEqual(out, { a: 'Yest', b: '', c: 'Yest', t: 'UTC-05:00', t2: 'UTC-04:00' });
});
