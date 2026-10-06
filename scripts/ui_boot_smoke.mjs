#!/usr/bin/env node
// UI boot smoke test: load the control UI in a headless Chrome over CDP, click
// every [data-tab], and fail on any uncaught exception or a boot failure.
//
// Requires Node >= 22 (global fetch/WebSocket) and no npm dependencies.
//
//   "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" --headless=new \
//     --remote-debugging-port=9333 --user-data-dir="$(mktemp -d)" --no-first-run about:blank &
//   node scripts/ui_boot_smoke.mjs http://127.0.0.1:19966/ [--cdp http://127.0.0.1:9333]
//
// Point it at a throwaway instance (--data-dir on a temp dir), never at real
// engagement data. Exit 0 = clean, 1 = failure, 2 = usage/connection error.
const args = process.argv.slice(2);
let url = '';
let cdp = 'http://127.0.0.1:9333';
let settleMs = 6000;
let tabMs = 800;
for (let i = 0; i < args.length; i++) {
  if (args[i] === '--cdp') cdp = args[++i];
  else if (args[i] === '--settle-ms') settleMs = Number(args[++i]);
  else if (args[i] === '--tab-ms') tabMs = Number(args[++i]);
  else if (!url) url = args[i];
}
if (!url) {
  console.error('usage: ui_boot_smoke.mjs <url> [--cdp http://127.0.0.1:9333] [--settle-ms N] [--tab-ms N]');
  process.exit(2);
}
const sleep = ms => new Promise(r => setTimeout(r, ms));

let target;
try {
  target = await (await fetch(`${cdp}/json/new?${encodeURIComponent('about:blank')}`, { method: 'PUT' })).json();
} catch (err) {
  console.error(`cannot reach Chrome DevTools at ${cdp}: ${err.message}`);
  process.exit(2);
}
const ws = new WebSocket(target.webSocketDebuggerUrl);
let nextId = 0;
const pending = new Map();
const exceptions = [];
const warnings = [];
const send = (method, params = {}) => new Promise(resolve => {
  const id = ++nextId;
  pending.set(id, resolve);
  ws.send(JSON.stringify({ id, method, params }));
});
ws.onmessage = ev => {
  const m = JSON.parse(ev.data);
  if (m.id && pending.has(m.id)) { pending.get(m.id)(m.result); pending.delete(m.id); return; }
  const p = m.params || {};
  if (m.method === 'Runtime.exceptionThrown') {
    const d = p.exceptionDetails;
    exceptions.push(`EXCEPTION ${d.exception?.description || d.text} @ ${d.url}:${d.lineNumber}:${d.columnNumber}`);
  } else if (m.method === 'Runtime.consoleAPICalled' && p.type === 'error') {
    warnings.push(`CONSOLE.error ${(p.args || []).map(a => a.value ?? a.description).join(' ')}`);
  } else if (m.method === 'Network.responseReceived' && p.response.status >= 400) {
    warnings.push(`HTTP ${p.response.status} ${p.response.url}`);
  } else if (m.method === 'Network.loadingFailed') {
    warnings.push(`NETFAIL ${p.errorText} ${p.requestId}`);
  }
};
await new Promise((resolve, reject) => { ws.onopen = resolve; ws.onerror = () => reject(new Error('websocket error')); });
for (const domain of ['Runtime', 'Network', 'Page']) await send(`${domain}.enable`);
await send('Page.navigate', { url });
await sleep(settleMs);

const evalJS = async expression => (await send('Runtime.evaluate', { expression, returnByValue: true })).result?.value;
const tabs = await evalJS(`[...document.querySelectorAll('[data-tab]')].map(e => e.getAttribute('data-tab')).filter((v, i, a) => v && a.indexOf(v) === i)`) || [];
for (const tab of tabs) {
  await evalJS(`document.querySelector('[data-tab="${tab}"]')?.click()`);
  await sleep(tabMs);
}
await sleep(1000);
const status = await evalJS(`(() => { const el = document.getElementById('workspaceHydrationStatus'); return { text: el ? el.innerText : null, bootFailed: !!(el && el.hasAttribute('data-workspace-boot-failed')) }; })()`) || {};
await send('Page.close').catch(() => {});
ws.close();

console.log(`tabs clicked (${tabs.length}): ${tabs.join(', ')}`);
if (status.text) console.log(`workspaceHydrationStatus: ${JSON.stringify(status.text)}`);
for (const w of warnings) console.log(`warn: ${w}`);
const failures = [...exceptions];
if (tabs.length === 0) failures.push('no [data-tab] elements found: the UI did not boot');
if (status.bootFailed) failures.push('#workspaceHydrationStatus has data-workspace-boot-failed');
if (failures.length) {
  for (const f of failures) console.error(`FAIL ${f}`);
  process.exit(1);
}
console.log('ok: no uncaught exceptions, boot succeeded');
process.exit(0);
