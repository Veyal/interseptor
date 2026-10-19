// desktop-panel-proxy.js — the desktop layout of History (docs/desktop-design.md,
// Panels). It re-arranges the web markup in place; every control keeps its id,
// handlers and accessible name, so proxy.js is unchanged and a browser tab never
// runs this file's setup (whenDesktop waits for html.desktop-app).
//   toolbar   one row: search field (scope prefix + filter tokens), Method and
//             Status pills, Filters, then Pause, Views and the overflow menu (Columns,
//             Clear filters). In scope only + Edit scope live in Filters. When
//             the panel is narrow the tokens collapse to one count chip and the
//             pills drop their chrome (panel-proxy.css, container queries).
//   inspector one header row per side: Request/Response, Pretty/Raw/Hex plus a
//             More menu for the other views, and Repeater, Intruder and the overflow
//             menu (Attach as evidence, Copy as, Copy as PNG, More flow actions).
//   note      a compact "Add note" field that opens on focus or when a note exists.
// Menus are the kit's overflow menus (desktop-panel-kit.js), like every panel's.
import { whenDesktop, el, iconNode, createOverflow, menuSeparator, move } from './desktop-panel-kit.js';
import { splitViews, moreLabel, notePlaceholder, sentenceCase, activeTokenCount, isViewHidden } from './desktop-panel-proxy-model.js';

const $ = (sel, root = document) => root.querySelector(sel);

// press fires a source button's own click handler. The event does not bubble:
// History's app-wide click-away listener would otherwise close a menu the
// handler just opened (the web's Copy as button has that problem).
function press(node) {
  if (node && !node.disabled) node.dispatchEvent(new MouseEvent('click', { bubbles: false, cancelable: true, view: window }));
}

// park takes a source button out of the keyboard order and the accessibility tree
// (its action is reachable through `owner`'s menu) and sends any focus it
// receives, for instance when one of its menus closes, to `owner`.
function park(node, owner) {
  node.tabIndex = -1;
  node.setAttribute('aria-hidden', 'true');
  node.addEventListener('focus', () => owner.focus({ preventScroll: true }));
}

// ---- toolbar ----
function buildToolbar(panel) {
  const tb = $('.proxy-toolbar', panel);
  const search = $('.search', tb);
  const pop = $('#proxyFilters');
  if (!tb || !search || !pop) return;

  // Active filters render as removable tokens inside the search field. When the
  // field is narrow they collapse to one count chip that opens Filters.
  const chips = $('#chips');
  const input = $('#fSearch');
  if (chips && input) {
    move(chips, search, input);
    const count = el('button', 'dsk-chip-count');
    count.type = 'button';
    count.hidden = true;
    search.insertBefore(count, input);
    const sync = () => {
      const n = activeTokenCount([...chips.querySelectorAll('.chip')].map((c) => {
        const x = c.querySelector('[data-clear]');
        return x ? x.getAttribute('data-clear') : '';
      }));
      count.hidden = n === 0;
      count.textContent = n + (n === 1 ? ' filter' : ' filters');
      count.setAttribute('aria-label', n + ' active ' + (n === 1 ? 'filter' : 'filters') + '. Open Filters');
    };
    new MutationObserver(sync).observe(chips, { childList: true, subtree: true });
    sync();
    count.addEventListener('click', () => $('#filtersBtn')?.click());
  }

  // In scope only and Edit scope join the Filters popover as its first group.
  const scopeRow = el('div', 'proxy-filters-row dsk-filters-scope');
  scopeRow.setAttribute('role', 'group');
  scopeRow.setAttribute('aria-label', 'Scope');
  pop.insertBefore(scopeRow, pop.firstChild);
  for (const id of ['scopeToggle', 'scopeEdit']) { const b = $('#' + id); if (b) move(b, scopeRow); }
  const showRow = $('.proxy-filters-row:not(.dsk-filters-scope)', pop);
  if (showRow) showRow.classList.add('dsk-filters-show');
  $('#scopeEdit')?.addEventListener('click', () => closeFilters());
  // The one-time scope tip points at where the switch lives on desktop.
  const coach = $('#scopeCoach');
  if (coach && coach.firstChild && coach.firstChild.nodeType === 3) coach.firstChild.data = 'The scope switch moved to the Scope item in the status bar. ';

  // Method and Status become compact pills that tint when a value is set.
  for (const [id, name] of [['fMethod', 'Method'], ['fStatus', 'Status']]) {
    const native = $('#' + id);
    const wrap = native && native.closest('.ui-select');
    const value = wrap && $('.ui-select-value', wrap);
    if (!wrap || !value) continue;
    wrap.classList.add('dsk-pill');
    value.dataset.pill = name;
    const sync = () => wrap.classList.toggle('is-set', native.value !== '');
    sync();
    native.addEventListener('change', sync);
    new MutationObserver(sync).observe(value, { childList: true, characterData: true, subtree: true });
  }

  // Right side: Pause, Views, the overflow menu. The Columns button stays in the DOM as the
  // anchor of its own popover, stacked under the overflow button.
  const spacer = $('.spacer', tb);
  const views = $('#viewsBtn');
  const pause = $('#freezeBtn');
  if (spacer && views) move(spacer, tb, views);
  if (pause && views) move(pause, tb, views);
  const cols = $('#colPickerBtn');
  const wrap = el('div', 'dsk-more-wrap');
  const more = createOverflow({
    id: 'proxyMoreBtn',
    tooltip: 'Columns, clear filters',
    items: [
      { key: 'cols', label: 'Columns…', icon: 'columns', run: () => press(cols) },
      menuSeparator(),
      { key: 'clear', label: 'Clear all filters', icon: 'close', disabled: () => !$('#chipsClear'), run: () => $('#chipsClear')?.click() },
    ],
  });
  wrap.appendChild(more.button);
  if (cols) { park(cols, more.button); cols.classList.add('dsk-anchor'); move(cols, wrap); }
  tb.appendChild(wrap);

  // The Filters popover opens under its button instead of pushing the table.
  const btn = $('#filtersBtn');
  const place = () => {
    if (!btn || pop.hidden) return;
    const p = panel.getBoundingClientRect();
    const r = btn.getBoundingClientRect();
    const w = Math.min(560, p.width - 24);
    pop.style.width = w + 'px';
    pop.style.left = Math.max(12, Math.min(r.left - p.left, p.width - w - 12)) + 'px';
    pop.style.top = (r.bottom - p.top + 6) + 'px';
  };
  new MutationObserver(place).observe(pop, { attributes: true, attributeFilter: ['hidden'] });
  window.addEventListener('resize', place);
}

function closeFilters() {
  const pop = $('#proxyFilters');
  if (pop && !pop.hidden) { pop.hidden = true; $('#filtersBtn')?.setAttribute('aria-expanded', 'false'); }
}

// ---- inspector ----
// buildViewSeg keeps Pretty, Raw and Hex on the header and folds the other views
// into a "More" menu whose label names the folded view when it is the current one.
// proxy.js hides a view either with [hidden] or with an inline display:none
// (Render for a non-HTML response); both count as hidden.
function buildViewSeg(seg) {
  const buttons = [...seg.querySelectorAll('button[data-view]')];
  const hidden = (b) => isViewHidden({ hidden: b.hidden, display: b.style.display });
  const info = () => buttons.map((b) => ({ view: b.dataset.view, label: b.textContent.trim(), hidden: hidden(b), on: b.classList.contains('on') }));
  // Fold by name, not by what is visible right now: a view the page shows later
  // (Render once an HTML response arrives) still belongs to the menu.
  const { more: folded } = splitViews(buttons.map((b) => ({ view: b.dataset.view, label: b.textContent.trim(), hidden: false })));
  const foldedViews = new Set(folded.map((v) => v.view));
  buttons.forEach((b) => b.classList.toggle('dsk-folded', foldedViews.has(b.dataset.view)));
  // Visible order is Pretty, Raw, Hex; the DOM follows so Tab does too.
  const first = seg.querySelector('button');
  for (const view of ['pretty', 'raw']) {
    const b = buttons.find((x) => x.dataset.view === view);
    if (b && first && b !== first) move(b, seg, first);
  }
  const hex = buttons.find((x) => x.dataset.view === 'hex');
  const lastShown = buttons.find((x) => x.dataset.view === 'raw');
  if (hex && lastShown) move(hex, seg, lastShown.nextSibling);
  const btn = el('button', 'dsk-seg-more');
  btn.type = 'button';
  btn.setAttribute('aria-haspopup', 'menu');
  btn.setAttribute('aria-expanded', 'false');
  btn.title = 'Other views';
  const label = el('span', 'dsk-seg-more-label', 'More');
  btn.append(label, iconNode('chevron'));
  seg.appendChild(btn);
  const foldedButtons = buttons.filter((b) => foldedViews.has(b.dataset.view));
  createOverflow({
    id: 'dskViews' + (seg.dataset.side || ''),
    label: 'Other views',
    button: btn,
    items: foldedButtons.map((b) => ({
      key: b.dataset.view,
      label: () => b.textContent.trim(),
      hidden: () => hidden(b),
      checked: () => b.classList.contains('on'),
      run: () => b.click(),
    })),
  });
  const sync = () => {
    const now = info();
    const cur = now.find((v) => v.on);
    const inFolded = !!cur && foldedViews.has(cur.view);
    label.textContent = moreLabel(now.filter((v) => foldedViews.has(v.view)), cur && cur.view);
    btn.classList.toggle('on', inFolded);
    btn.setAttribute('aria-label', inFolded ? 'View: ' + label.textContent + '. Other views' : 'Other views');
    btn.hidden = !now.some((v) => foldedViews.has(v.view) && !v.hidden);
  };
  new MutationObserver(sync).observe(seg, { attributes: true, subtree: true, attributeFilter: ['class', 'hidden', 'style'] });
  sync();
}

function buildInspector() {
  const heads = document.querySelectorAll('#inspect .pane-head');
  heads.forEach((head) => {
    const lbl = $('.lbl', head);
    if (lbl) { const t = sentenceCase(lbl.textContent); lbl.textContent = t; }
    const seg = $('.seg[data-side]', head);
    if (seg) buildViewSeg(seg);
  });
  const reqHead = heads[0];
  if (!reqHead) return;
  // Repeater and Intruder carry their icons, shown when a narrow pane drops the words.
  [['inspectSendRepeater', 'repeater'], ['inspectSendIntruder', 'intruder']].forEach(([id, name]) => {
    const b = document.getElementById(id);
    if (b && !b.querySelector('.icon')) { b.prepend(iconNode(name)); b.title = b.getAttribute('aria-label') || ''; }
  });
  // Repeater and Intruder stay visible; everything else sits behind the overflow menu. The
  // source buttons remain in the DOM, stacked under that button, so their own menus and
  // popovers still anchor to it and proxy.js keeps its handlers.
  const src = el('div', 'dsk-anchor-stack');
  const wrap = el('div', 'dsk-more-wrap dsk-more-inspector');
  const png = () => src.querySelector('#inspectCopyPng button');
  const node = (id) => document.getElementById(id);
  const shown = (n) => !!n && getComputedStyle(n).display !== 'none' && !n.hidden;
  const later = (get) => () => press(get());
  const more = createOverflow({
    id: 'inspectActionsBtn',
    tooltip: 'Attach as evidence, Copy as, Copy as PNG, more flow actions',
    items: [
      { key: 'attach', label: 'Attach as evidence', icon: 'attach', hint: 'E', hidden: () => !shown(node('inspectAddFinding')), disabled: () => node('inspectAddFinding').disabled, run: later(() => node('inspectAddFinding')) },
      menuSeparator(),
      { key: 'copyas', label: 'Copy as…', icon: 'copy', hidden: () => !shown(node('inspectCopyAs')), disabled: () => node('inspectCopyAs').disabled, run: later(() => node('inspectCopyAs')) },
      { key: 'png', label: 'Copy as PNG', icon: 'image', hidden: () => !!png() && !shown(png()), disabled: () => !png() || png().disabled, run: later(png) },
      { key: 'pngopts', label: 'Copy as PNG options…', icon: 'settings', hidden: () => !shown(node('inspectCopyPngMore')), disabled: () => node('inspectCopyPngMore').disabled, run: later(() => node('inspectCopyPngMore')) },
      menuSeparator(),
      { key: 'more', label: 'More flow actions…', icon: 'more', hidden: () => !shown(node('inspectMoreActions')), disabled: () => node('inspectMoreActions').disabled, run: later(() => node('inspectMoreActions')) },
    ],
  });
  ['inspectAddFinding', 'inspectCopyPng', 'inspectCopyPngMore', 'inspectCopyAs', 'inspectMoreActions'].forEach((id) => { const n = node(id); if (n) move(n, src); });
  wrap.append(more.button, src);
  reqHead.appendChild(wrap);
  // The sources stay out of the tab order; a mounted Copy as PNG button is new
  // on every selection, so the lock is re-applied as the stack changes.
  src.setAttribute('aria-hidden', 'true');
  const lock = () => src.querySelectorAll('button').forEach((b) => { b.tabIndex = -1; });
  lock();
  new MutationObserver(lock).observe(src, { childList: true, subtree: true });
  src.addEventListener('focusin', () => more.button.focus({ preventScroll: true }));
}

// ---- note bar ----
function buildNote() {
  const bar = $('#noteBar');
  const input = $('#noteInput');
  if (!bar || !input) return;
  const full = input.getAttribute('placeholder') || '';
  const lbl = $('.lbl', bar);
  if (lbl) { const svg = $('svg', lbl); lbl.textContent = ' Note'; if (svg) lbl.insertBefore(svg, lbl.firstChild); }
  const set = (open) => { input.placeholder = notePlaceholder(open, full); };
  set(false);
  input.addEventListener('focus', () => set(true));
  input.addEventListener('blur', () => set(false));
  // Clicking the icon or label opens the field.
  if (lbl) lbl.addEventListener('click', () => input.focus());
}

whenDesktop(
  () => $('#fMethodUi') && $('#fStatusUi') && $('#inspectCopyAs') && $('#noteInput') && $('#proxyFilters'),
  () => {
    const panel = $('#panel-proxy');
    if (!panel || panel.dataset.dskProxy) return;
    panel.dataset.dskProxy = '1';
    buildToolbar(panel);
    buildInspector();
    buildNote();
  },
);
