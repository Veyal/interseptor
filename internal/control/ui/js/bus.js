// bus.js — a tiny synchronous publish/subscribe event bus so feature modules can
// coordinate without importing each other. Direct cross-module imports (e.g.
// settings.js -> proxy.js -> findings.js -> settings.js) formed ESM dependency
// cycles whose evaluation order risked `ReferenceError: Cannot access 'X' before
// initialization` under the Temporal Dead Zone. A module now publishes an intent
// ("proxy should reload its flows") and whichever module owns that behaviour
// listens for it; neither needs a static import of the other.
//
// No DOM and no core.js import, so this runs under node and stays in the pure
// tier (TestUIPureModulesStayDOMFree). It sits one level below the existing
// shell-hooks.js SSE dispatcher and core.js registerHook mediator: use a hook
// when one module answers a question (a single implementor returning a value),
// use the bus when zero-or-more modules react to something that happened.

const listeners = new Map(); // event -> Set<fn>
const errorSinks = new Set();

// EVENTS is the canonical list of bus topics. Publishers and subscribers import
// from here so a typo is a load error, not a silently dropped message. Grouped
// by the module that owns the reaction.
export const EVENTS = Object.freeze({
  // proxy.js owns the flow store + history table.
  PROXY_RELOAD_FLOWS: 'proxy:reload-flows',
  PROXY_RELOAD_SCOPE: 'proxy:reload-scope',
  PROXY_REFRESH_ROWS: 'proxy:refresh-rows',
  // intercept.js owns the intercept rule list.
  INTERCEPT_RELOAD_RULES: 'intercept:reload-rules',
  // settings.js owns the Settings panel + its section router.
  SETTINGS_OPEN_SECTION: 'settings:open-section',
  SETTINGS_OPEN_PROJECT: 'settings:open-project',
  // setup.js owns the first-run setup flow.
  SETUP_OPEN: 'setup:open',
  // tlsdiag.js owns the traffic-diagnosis view.
  TLS_RELOAD_DIAGNOSIS: 'tls:reload-diagnosis',
  // flow inspection (unified <flow-inspector>): any surface can ask to open a
  // flow or broadcast the current selection to popout windows.
  FLOW_SELECT: 'flow:select',
  FLOW_OPEN: 'flow:open',
});

const EMPTY = new Set();

// on(event, fn) subscribes fn to event and returns an unsubscribe function.
export function on(event, fn) {
  if (typeof event !== 'string' || typeof fn !== 'function') throw new Error('bus.on: needs (event, fn)');
  let set = listeners.get(event);
  if (!set) { set = new Set(); listeners.set(event, set); }
  set.add(fn);
  return () => off(event, fn);
}

// once(event, fn) runs fn for the next emit of event only.
export function once(event, fn) {
  if (typeof fn !== 'function') throw new Error('bus.once: needs (event, fn)');
  const dispose = on(event, (payload) => { dispose(); fn(payload); });
  return dispose;
}

// off(event, fn) removes one subscription; harmless if it was never registered.
export function off(event, fn) {
  const set = listeners.get(event);
  if (!set) return;
  set.delete(fn);
  if (!set.size) listeners.delete(event);
}

// emit(event, payload) delivers payload to every subscriber of event and of the
// '*' wildcard, and returns how many ran. A throwing listener never blocks the
// others: its error goes to the error sinks (or the console) instead. Listeners
// are snapshotted first so a handler may subscribe or unsubscribe during delivery.
export function emit(event, payload) {
  let count = 0;
  for (const key of (event === '*' ? ['*'] : [event, '*'])) {
    const set = listeners.get(key);
    if (!set || !set.size) continue;
    for (const fn of [...set]) {
      count++;
      try { fn(payload, event); } catch (e) { reportBusError(e, event); }
    }
  }
  return count;
}

// onBusError(fn) observes listener exceptions (fn receives (error, event)). With
// no sink registered, errors fall back to console.error so they are never silent.
export function onBusError(fn) {
  if (typeof fn !== 'function') throw new Error('bus.onBusError: needs a function');
  errorSinks.add(fn);
  return () => errorSinks.delete(fn);
}
function reportBusError(error, event) {
  if (errorSinks.size) { for (const sink of [...errorSinks]) { try { sink(error, event); } catch (e) { /* a sink never breaks delivery */ } } return; }
  try { globalThis.console && globalThis.console.error && globalThis.console.error('bus listener for "' + event + '" threw', error); } catch (e) { /* no console */ }
}

// listenerCount(event) is the number of direct subscribers (ignores '*').
export function listenerCount(event) { return (listeners.get(event) || EMPTY).size; }

// resetBus() drops every subscription and error sink. Tests only — the running
// app never tears the bus down.
export function resetBus() { listeners.clear(); errorSinks.clear(); }
