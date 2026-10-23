// desktop-panel-intercept.js — the desktop layout of Intercept (docs/desktop-design.md,
// Panels). The web markup and intercept.js are unchanged; this moves two things
// and relabels a few, and only runs in a desktop host (whenDesktop):
//   - Beautify and Reset sit behind the kit's overflow menu in the editor header (their buttons
//     stay in the DOM, hidden, so intercept.js keeps its handlers and disabled
//     state);
//   - the action row is ordered Forward, Drop, then the quiet actions, with the
//     spacer before the quiet ones;
//   - "HOLD QUEUE" reads "Hold queue", the long scope sentence gets a tooltip,
//     and the hold rule's switch names the rule in its tooltip.
import { whenDesktop, el, createOverflow, move } from './desktop-panel-kit.js';
import { orderActions, sameOrder, holdRuleSummary } from './desktop-panel-intercept-model.js';

const $ = (sel, root = document) => root.querySelector(sel);

function buildEditorMenu() {
  const head = $('#heldEditor');
  const beautify = $('#heldBeautifyBtn');
  const reset = $('#heldResetBtn');
  if (!head || (!beautify && !reset)) return;
  const stash = el('div', 'dsk-src');
  stash.hidden = true;
  [beautify, reset].forEach((b) => { if (b) move(b, stash); });
  const wrap = el('div', 'dsk-more-wrap');
  const more = createOverflow({
    id: 'heldMoreBtn',
    tooltip: 'Beautify body, reset to original',
    items: [
      { key: 'beautify', label: 'Beautify body', icon: 'codec', hidden: () => !beautify, disabled: () => !beautify || beautify.disabled, run: () => beautify && beautify.click() },
      { key: 'reset', label: 'Reset to original', icon: 'refresh', hidden: () => !reset, disabled: () => !reset || reset.disabled, run: () => reset && reset.click() },
    ],
  });
  wrap.append(more.button, stash);
  head.appendChild(wrap);
}

function sortActions() {
  const bar = $('#heldActions');
  if (!bar) return;
  const spacer = bar.querySelector('.dsk-spacer') || el('span', 'dsk-spacer');
  spacer.setAttribute('aria-hidden', 'true');
  const buttons = [...bar.querySelectorAll(':scope > button')];
  const want = orderActions(buttons.map((b) => b.id));
  if (!sameOrder(buttons.map((b) => b.id), want)) {
    const byId = new Map(buttons.map((b) => [b.id, b]));
    want.forEach((id) => bar.appendChild(byId.get(id)));
  }
  const drop = $('#dropBtn');
  if (drop && drop.nextElementSibling !== spacer) bar.insertBefore(spacer, drop.nextSibling);
}

function buildActions() {
  const bar = $('#heldActions');
  if (!bar) return;
  sortActions();
  // intercept.js adds the Repeater button lazily, before Forward; re-sort then.
  new MutationObserver(() => { if (!bar.dataset.dskSorting) { bar.dataset.dskSorting = '1'; sortActions(); delete bar.dataset.dskSorting; } }).observe(bar, { childList: true });
}

function buildLabels() {
  const head = $('.icpt-queue-head');
  if (head) {
    for (const n of head.childNodes) {
      if (n.nodeType === 3 && /hold queue/i.test(n.textContent)) n.textContent = 'Hold queue ';
    }
    head.title = 'Intercept is session-only: it switches off after a restart.';
  }
  const note = $('#heldScopeNote');
  if (note) {
    const sync = () => { note.title = note.textContent; };
    new MutationObserver(sync).observe(note, { childList: true, characterData: true, subtree: true });
    sync();
  }
  const on = $('#interceptFilterOn');
  const target = $('#interceptFilterTarget');
  const pattern = $('#interceptFilterPattern');
  const label = on && on.closest('label');
  if (on && target && pattern && label) {
    const sync = () => { label.title = holdRuleSummary({ on: on.checked, target: target.value, pattern: pattern.value }); };
    ['change', 'input'].forEach((ev) => [on, target, pattern].forEach((n) => n.addEventListener(ev, sync)));
    // intercept.js writes checked/value programmatically (boot, SSE) with no event.
    document.addEventListener('intercept-filter-sync', sync);
    sync();
  }
}

whenDesktop(
  () => $('#heldEditor') && $('#heldActions') && $('#forwardBtn') && $('#interceptFilterTarget') && $('.icpt-queue-head'),
  () => {
    const panel = $('#panel-intercept');
    if (!panel || panel.dataset.dskIntercept) return;
    panel.dataset.dskIntercept = '1';
    buildEditorMenu();
    buildActions();
    buildLabels();
  },
);
