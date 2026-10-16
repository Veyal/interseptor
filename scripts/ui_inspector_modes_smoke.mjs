#!/usr/bin/env node
// History inspector placement smoke test: the Side drawer / Bottom inspector
// control must put the inspector where its label says (drawer to the right of
// the list, bottom inspector below it), keep aria-pressed in step, and restore
// the saved choice after a reload. Needs >= 1 flow in History (e.g. import a HAR).
//
//   "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" --headless=new \
//     --remote-debugging-port=9333 --user-data-dir="$(mktemp -d)" --no-first-run about:blank &
//   node scripts/ui_inspector_modes_smoke.mjs http://127.0.0.1:19966/ [--cdp http://127.0.0.1:9333]
//
// Requires Node >= 22, no dependencies. Use a throwaway instance. Exit 0 = pass, 1 = fail, 2 = usage.
const args = process.argv.slice(2);
let url = '', cdp = 'http://127.0.0.1:9333';
for (let i = 0; i < args.length; i++) { if (args[i] === '--cdp') cdp = args[++i]; else if (!url) url = args[i]; }
if (!url) { console.error('usage: ui_inspector_modes_smoke.mjs <url> [--cdp http://127.0.0.1:9333]'); process.exit(2); }
const sleep = ms => new Promise(r => setTimeout(r, ms));
let target;
try { target = await (await fetch(`${cdp}/json/new?about:blank`, { method: 'PUT' })).json(); }
catch (err) { console.error(`cannot reach Chrome DevTools at ${cdp}: ${err.message}`); process.exit(2); }
const ws = new WebSocket(target.webSocketDebuggerUrl);
let nextId = 0; const pending = new Map();
ws.onmessage = ev => { const m = JSON.parse(ev.data); if (m.id && pending.has(m.id)) { pending.get(m.id)(m.result); pending.delete(m.id); } };
await new Promise(r => { ws.onopen = r; });
const send = (method, params = {}) => new Promise(r => { const id = ++nextId; pending.set(id, r); ws.send(JSON.stringify({ id, method, params })); });
const ev = async expr => (await send('Runtime.evaluate', { expression: expr, returnByValue: true })).result.value;
const failures = [];
const check = (ok, msg) => { if (!ok) failures.push(msg); console.log(`${ok ? 'ok  ' : 'FAIL'} ${msg}`); };

const state = () => ev(`(() => {
  const box = id => { const e = document.getElementById(id); if (!e || getComputedStyle(e).display === 'none') return null; const b = e.getBoundingClientRect(); return { x: b.x, y: b.y, w: b.width, h: b.height }; };
  const g = document.getElementById('inspectDock');
  const pressed = g && [...g.querySelectorAll('button[data-dock]')].filter(b => b.getAttribute('aria-pressed') === 'true').map(b => b.dataset.dock);
  return { hidden: !g || g.hidden, pressed, rows: box('rows'), inspect: box('inspect'), drawer: box('flowDrawer'), saved: localStorage.getItem('proxy.dock') };
})()`);
const load = async (w, h, saved) => {
  await send('Emulation.setDeviceMetricsOverride', { width: w, height: h, deviceScaleFactor: 1, mobile: false });
  await send('Page.enable'); await send('Page.navigate', { url }); await sleep(2500);
  await ev(`localStorage.${saved ? `setItem('proxy.dock','${saved}')` : `removeItem('proxy.dock')`}`);
  await send('Page.navigate', { url }); await sleep(3000);
  await ev(`document.querySelector('[data-tab="proxy"]')?.click()`); await sleep(600);
};
const pick = async sel => { await ev(`document.querySelector(${JSON.stringify(sel)}).click()`); await sleep(900); };
const below = s => s.inspect && s.rows && s.inspect.y >= s.rows.y + s.rows.h - 1 && !s.drawer;
const beside = s => s.drawer && s.rows && s.drawer.x >= s.rows.x + s.rows.w - 1 && !s.inspect;

await load(1440, 900, '');
await pick('#rows .trow');
let s = await state();
check(!s.hidden && s.pressed.join() === 'bottom', 'wide default: control visible, only Bottom inspector pressed');
check(below(s), 'wide default: selected flow opens in the bottom inspector, below the list');
await pick('#inspectDock [data-dock="drawer"]'); s = await state();
check(s.pressed.join() === 'drawer' && beside(s) && s.saved === 'drawer', 'Side drawer: pressed, opens to the right of the list, saved as drawer');
await pick('#inspectDock [data-dock="drawer"]'); s = await state();
check(s.pressed.join() === 'drawer' && beside(s), 'Side drawer pressed again stays on the side drawer');
await pick('#rows .trow:nth-child(3)'); s = await state();
check(beside(s), 'selecting another row reveals the side drawer in the current mode');
await pick('#inspectDock [data-dock="bottom"]'); s = await state();
check(s.pressed.join() === 'bottom' && below(s) && s.saved === 'bottom', 'Bottom inspector: pressed, opens below the list, saved as bottom');

await load(1440, 900, 'drawer'); await pick('#rows .trow'); s = await state();
check(s.pressed.join() === 'drawer' && beside(s), 'saved "drawer" restores the side drawer after reload');
await load(1440, 900, 'bottom'); await pick('#rows .trow'); s = await state();
check(s.pressed.join() === 'bottom' && below(s), 'saved "bottom" restores the bottom inspector after reload');

await load(390, 844, 'drawer'); await pick('#rows .trow'); s = await state();
check(s.hidden && below(s), 'phone: control hidden and the inspector stays at the bottom, even with a saved "drawer"');

ws.close();
if (failures.length) { console.error(`\n${failures.length} check(s) failed`); process.exit(1); }
console.log('\ninspector placement smoke: pass'); process.exit(0);
