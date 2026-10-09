// shell-hooks.js — extension points owned by the shell so later modules never
// need to edit app.js: command-palette entries, SSE event handlers, the
// tabchange event, a small shell API (activateTab and friends) and the guarded
// loader for optional feature modules. No DOM or core.js imports, so it runs
// under node.

const commands = [];
const sseHandlers = new Map();
let shellApi = {};

// registerCommand({t, kw, run, group}) adds a palette entry; a command with the
// same title replaces the earlier one. Returns an unregister function.
export function registerCommand(cmd) {
  if (!cmd || typeof cmd.t !== 'string' || typeof cmd.run !== 'function') throw new Error('registerCommand: needs {t, run}');
  const at = commands.findIndex((c) => c.t === cmd.t);
  if (at >= 0) commands.splice(at, 1, cmd); else commands.push(cmd);
  return () => { const i = commands.indexOf(cmd); if (i >= 0) commands.splice(i, 1); };
}
export const listCommands = () => commands.slice();

// registerSseHandler(type, fn) runs fn(message) after the built-in dispatcher
// for that event type. '*' receives every message. A throwing handler never
// affects the others or the built-in chain.
export function registerSseHandler(type, fn) {
  if (typeof type !== 'string' || typeof fn !== 'function') throw new Error('registerSseHandler: needs (type, fn)');
  const set = sseHandlers.get(type) || new Set();
  set.add(fn);
  sseHandlers.set(type, set);
  return () => set.delete(fn);
}
export function runSseHooks(message, onError = () => {}) {
  const type = message && message.type;
  for (const key of [type, '*']) {
    for (const fn of [...(sseHandlers.get(key) || [])]) {
      try { fn(message); } catch (e) { onError(e, key); }
    }
  }
}

export function setShellApi(api) { shellApi = { ...shellApi, ...api }; }
export const getShellApi = () => shellApi;

// Report-open signal: report-preflight publishes whether report work is on
// screen; the context bar reads it to show report-time readiness (blocker chip,
// Findings tab badge). One direction only: publisher -> subscribers.
let reportOpen = false;
const reportListeners = new Set();
export const isReportOpen = () => reportOpen;
export function setReportOpen(on) {
  const next = !!on;
  if (next === reportOpen) return;
  reportOpen = next;
  for (const fn of [...reportListeners]) { try { fn(next); } catch { /* one listener never blocks the rest */ } }
}
export function onReportOpenChange(fn) {
  if (typeof fn !== 'function') return () => {};
  reportListeners.add(fn);
  return () => reportListeners.delete(fn);
}

export const TAB_CHANGE_EVENT = 'interseptor:tabchange';
export function emitTabChange(tab, previous, target = globalThis.document) {
  if (!target || typeof globalThis.CustomEvent !== 'function') return false;
  target.dispatchEvent(new globalThis.CustomEvent(TAB_CHANGE_EVENT, { detail: { tab, previous } }));
  return true;
}

// Every module a later work package adds. Each import is guarded: a module that
// is missing or throws on load is reported and skipped, never blocking boot.
export const OPTIONAL_MODULES = [
  'flowbody', 'flowdrawer', 'evidence-attach', 'evidence-tray', 'readiness-meter', 'report-preflight',
  'checklist', 'dock', 'repeater', 'intruder', 'proxy-filters', 'proxy-selection',
];
export async function loadOptionalModules(importer, names = OPTIONAL_MODULES, onError = () => {}) {
  return Promise.all(names.map(async (name) => {
    try { await importer('./' + name + '.js'); return { name, ok: true }; }
    catch (e) { onError(e, name); return { name, ok: false }; }
  }));
}
