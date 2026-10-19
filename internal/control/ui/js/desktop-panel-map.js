// desktop-panel-map.js — the Map panel's desktop toolbar (docs/desktop-design.md,
// Panels). Runs only inside a desktop host (html.desktop-app); map.js calls it
// behind that check, so a browser tab keeps its two toolbar rows. It rebuilds the
// toolbar as ONE row: search, the view segmented control, the endpoint count, a
// Filters popover and a More menu. Every control is MOVED, never recreated, so ids,
// listeners, labels and the show/hide logic in map.js keep working:
//   Filters popover  domain, search scope, method, status, tag, and the three
//                    switches (hide 403/404-only, collapse identical, without evidence)
//   overflow menu    expand/collapse all, fit graph, rebuild from history, discovery
// The menu is the kit's overflow menu and the Filters popover closes through the
// kit's one outside-click / Escape handling, like every desktop popup.
import { icon } from './core.js';
import { activeFilterCount } from './desktop-panel-map-model.js';
import { isDesktop, registerPopup, createOverflow, iconNode, observe, move } from './desktop-panel-kit.js';
import { stripGlyph } from './desktop-panel-tools-model.js';

export const isDesktopSkin = isDesktop;

const q = (s) => document.querySelector(s);
function el(tag, cls, attrs) {
  const e = document.createElement(tag);
  if (cls) e.className = cls;
  for (const k of Object.keys(attrs || {})) e.setAttribute(k, attrs[k]);
  return e;
}
const holder = (sel) => { const s = q(sel); return s ? (s.closest('.ui-select') || s) : null; };

let inited = false;
function field(labelText, control, native) {
  const row = el('div', 'mapf-row');
  const label = el('span', 'mapf-label');
  label.textContent = labelText;
  row.append(label);
  move(control, row);
  // map.js hides a filter it has nothing for (no tags yet) with an inline
  // display:none on the native select; the row and its label go with it.
  if (native) {
    const sync = () => { row.hidden = native.style.display === 'none'; };
    new MutationObserver(sync).observe(native, { attributes: true, attributeFilter: ['style'] });
    sync();
  }
  return row;
}
function sw(labelText, help, button, extra) {
  const row = el('div', 'mapf-switch');
  const text = el('div', 'mapf-text');
  const l = el('span', 'mapf-label');
  l.textContent = labelText;
  const h = el('span', 'mapf-help');
  h.textContent = help;
  text.append(l, h);
  if (extra) move(extra, text);
  button.setAttribute('aria-label', labelText);
  row.append(text);
  move(button, row);
  return row;
}

export function initMapDesktop() {
  if (inited || !isDesktopSkin()) return;
  const bar = q('#panel-map > .toolbar:not(.toolbar-secondary)');
  const second = q('#panel-map > .toolbar-secondary');
  const search = q('#mapSearch');
  const seg = q('#mapViewSeg');
  const count = q('#mapCount');
  if (!bar || !second || !search || !seg || !count) return;
  inited = true;

  // ---- Filters popover ----
  const fWrap = el('div', 'map-pop-wrap');
  const fBtn = el('button', 'btn map-filters-btn', { type: 'button', id: 'mapFiltersBtn', 'aria-haspopup': 'true', 'aria-expanded': 'false', 'aria-controls': 'mapFiltersPop', 'aria-label': 'Filters' });
  fBtn.innerHTML = icon('filter') + '<span>Filters</span><span class="filters-count" hidden></span>';
  const fPop = el('div', 'map-pop map-filters-pop', { id: 'mapFiltersPop', role: 'group', 'aria-label': 'Map filters', hidden: '' });
  const domain = holder('#mapDomain'), scope = holder('#mapSearchScope'), method = holder('#mapMethod'), status = holder('#mapStatus'), tag = holder('#mapTag');
  [['Domain', domain, '#mapDomain'], ['Search in', scope, '#mapSearchScope'], ['Method', method, '#mapMethod'], ['Status', status, '#mapStatus'], ['Tag', tag, '#mapTag']].forEach(([t, c, sel]) => { if (c) fPop.append(field(t, c, q(sel))); });
  const noise = q('#mapHideNoise'), collapse = q('#mapCollapseIdentical'), unlinked = q('#mapUnlinked'), cov = q('#mapCoverage');
  const switches = el('div', 'mapf-switches');
  if (noise) switches.append(sw('Hide 403/404-only', 'Endpoints that only ever returned 403 or 404', noise));
  if (collapse) switches.append(sw('Collapse identical', 'Group endpoints that share a response or a soft-404', collapse));
  if (unlinked) switches.append(sw('Without evidence', 'Only endpoints with no flow attached to a finding', unlinked, cov));
  fPop.append(switches);
  fWrap.append(fBtn, fPop);

  // ---- overflow menu: the real buttons, each with an icon and a plain label ----
  const label = (b, name) => {
    if (!b) return b;
    observe(b, () => {
      const t = stripGlyph(b.textContent);
      if (!b.querySelector('*') && t !== b.textContent) b.textContent = t;
      if (!b.querySelector('.icon')) b.prepend(iconNode(name));
    }, { childList: true, characterData: true, subtree: true });
    return b;
  };
  const refresh = q('#mapRefresh');
  if (refresh && !refresh.querySelector('.mapm-text')) { const t = el('span', 'mapm-text'); t.textContent = 'Rebuild from history'; refresh.append(t); }
  const more = createOverflow({
    id: 'mapMoreBtn',
    tooltip: 'Expand all, fit, rebuild from history, discovery',
    items: [label(q('#mapExpand'), 'list'), q('#mapFit'), refresh, label(q('#mapDiscoveryHelp'), 'search')],
  });
  const mWrap = el('div', 'map-pop-wrap');
  mWrap.append(more.button);

  // ---- the one row ----
  const spacer = bar.querySelector('.spacer') || el('div', 'spacer');
  [search, seg, count, spacer].forEach((n) => move(n, bar));
  bar.append(fWrap, mWrap);
  second.hidden = true;

  const sync = () => {
    const pressed = (b, dflt) => (b ? b.getAttribute('aria-pressed') === 'true' : dflt);
    const n = activeFilterCount({
      domain: q('#mapDomain') && q('#mapDomain').value, method: q('#mapMethod') && q('#mapMethod').value,
      status: q('#mapStatus') && q('#mapStatus').value, tag: q('#mapTag') && q('#mapTag').value,
      searchScope: q('#mapSearchScope') && q('#mapSearchScope').value,
      hideNoise: pressed(noise, true), collapseIdentical: pressed(collapse, true), onlyUnlinked: pressed(unlinked, false),
    });
    const badge = fBtn.querySelector('.filters-count');
    badge.hidden = n === 0;
    badge.textContent = String(n);
    fBtn.setAttribute('aria-label', n ? 'Filters, ' + n + ' active' : 'Filters');
    fBtn.classList.toggle('has-active', n > 0);
  };
  // The Filters popover closes through the kit's one outside-click / Escape
  // handling; a select menu opened from inside it stays its own business.
  const isOpen = () => !fPop.hidden;
  const close = ({ focus = false } = {}) => {
    if (fPop.hidden) return;
    fPop.hidden = true;
    fBtn.setAttribute('aria-expanded', 'false');
    if (focus) fBtn.focus();
  };
  const popup = registerPopup({ isOpen, close, contains: (t) => fPop.contains(t) || fBtn.contains(t), ignore: (t) => !!(t && t.closest && t.closest('.ui-select-menu')) });
  fBtn.addEventListener('click', () => {
    sync();
    if (isOpen()) { close(); return; }
    popup.opened();
    fPop.hidden = false;
    fBtn.setAttribute('aria-expanded', 'true');
  });
  fPop.addEventListener('change', sync);
  fPop.addEventListener('click', () => setTimeout(sync, 0));
  // Code changes the filters too (search handoffs, prefill): the count follows the endpoint count.
  new MutationObserver(sync).observe(count, { childList: true, characterData: true, subtree: true });
  sync();
}
