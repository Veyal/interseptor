// connection.js — the Connection chip and popover that replace the topbar's
// proxy/control addresses and SSE label, plus the offline banner. Pure helpers
// (connectionModel, createOfflineWatcher) run under node; the DOM wiring uses
// the injected helpers only, so this module imports nothing from core.js.
//
// The popover keeps the ids other modules already write to: proxyAddr,
// controlAddr, deviceProxyChip, deviceProxyAddr, sseStatus (role=status),
// sseDot, sseLabel, sseRetry, capDot and capStat.

export const OFFLINE_BANNER_MS = 5000;

// connectionModel maps the stream status ('ok' | 'reconnecting' | 'offline')
// to the words, state name and tone the chip and popover show.
export function connectionModel(status) {
  const offline = status === 'offline';
  const reconnecting = status !== 'ok';
  const word = offline ? 'offline' : reconnecting ? 'reconnecting' : 'live';
  const state = offline ? 'offline' : reconnecting ? 'reconnecting' : 'connected';
  return {
    word, state, offline, reconnecting,
    chipText: offline ? 'Offline' : reconnecting ? 'Reconnecting' : 'Live',
    chipLabel: 'Connection: ' + (offline ? 'offline' : reconnecting ? 'reconnecting' : 'live'),
    title: 'Live updates: ' + state + (offline ? ' — use Reconnect to retry' : reconnecting ? '…' : ''),
    dotClass: 'sse-dot ' + (status || 'ok'),
  };
}

// createOfflineWatcher shows the banner only after the stream has been down for
// delay ms and hides it as soon as it is live again.
export function createOfflineWatcher({ setTimeout: st = globalThis.setTimeout, clearTimeout: ct = globalThis.clearTimeout, onChange = () => {}, delay = OFFLINE_BANNER_MS } = {}) {
  let timer = null, shown = false;
  return {
    update(status) {
      if (status === 'ok') {
        if (timer !== null) { ct(timer); timer = null; }
        if (shown) { shown = false; onChange(false); }
        return;
      }
      if (timer !== null || shown) return;
      timer = st(() => { timer = null; shown = true; onChange(true); }, delay);
    },
    get shown() { return shown; },
  };
}

let deps = {};
let watcher = null;
let popOpen = false;

const $ = (id) => document.getElementById(id);

export function setConnectionStatus(status) {
  const m = connectionModel(status);
  const dot = $('sseDot'), label = $('sseLabel'), wrap = $('sseStatus'), retry = $('sseRetry');
  if (dot) dot.className = m.dotClass;
  if (label) label.textContent = m.word;
  if (retry) retry.hidden = !m.offline;
  if (wrap) {
    wrap.classList.toggle('reconnecting', m.reconnecting);
    wrap.setAttribute('aria-label', 'Live updates: ' + m.state);
    wrap.title = m.title;
  }
  const chip = $('connChip'), text = $('connText'), cdot = $('connDot');
  if (chip) { chip.dataset.state = m.state; chip.setAttribute('aria-label', m.chipLabel + '. Open connection details.'); }
  if (text) text.textContent = m.chipText;
  if (cdot) cdot.className = m.dotClass;
  if (watcher) watcher.update(status);
}

function setBanner(visible) {
  const b = $('offlineBanner');
  if (b) b.hidden = !visible;
}

export function closeConnectionPopover({ restoreFocus = false } = {}) {
  const pop = $('connPop'), chip = $('connChip');
  if (!pop || !popOpen) return;
  popOpen = false;
  pop.hidden = true;
  if (chip) chip.setAttribute('aria-expanded', 'false');
  if (restoreFocus && chip) chip.focus({ preventScroll: true });
}

export function openConnectionPopover() {
  const pop = $('connPop'), chip = $('connChip');
  if (!pop || popOpen) return;
  popOpen = true;
  pop.hidden = false;
  if (chip) chip.setAttribute('aria-expanded', 'true');
  const first = pop.querySelector('button:not([hidden]):not([disabled])');
  if (first) first.focus({ preventScroll: true });
}

function wireCopyButtons() {
  document.querySelectorAll('[data-conn-copy]').forEach((btn) => {
    btn.addEventListener('click', () => {
      const src = $(btn.dataset.connCopy);
      if (src && deps.copyText) deps.copyText(src.textContent.trim(), 'address copied');
    });
  });
}

// initConnection wires the chip. deps: {copyText, hasOpenModal}.
export function initConnection(d = {}) {
  deps = d;
  watcher = createOfflineWatcher({ onChange: setBanner });
  const chip = $('connChip'), pop = $('connPop');
  if (!chip || !pop) return;
  chip.addEventListener('click', () => (popOpen ? closeConnectionPopover({ restoreFocus: true }) : openConnectionPopover()));
  document.addEventListener('keydown', (e) => {
    if (e.key !== 'Escape' || !popOpen) return;
    if (deps.hasOpenModal && deps.hasOpenModal()) return;
    e.preventDefault();
    closeConnectionPopover({ restoreFocus: true });
  });
  document.addEventListener('click', (e) => {
    if (popOpen && !pop.contains(e.target) && !chip.contains(e.target)) closeConnectionPopover();
  });
  pop.addEventListener('focusout', (e) => {
    if (popOpen && e.relatedTarget && !pop.contains(e.relatedTarget) && e.relatedTarget !== chip) closeConnectionPopover();
  });
  wireCopyButtons();
}
