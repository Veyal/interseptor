import { wireRowKey } from './core.js';

/* ---- shared single-select list semantics ----
   A JS-rendered list where exactly one row is "current" is a listbox, not a
   pile of buttons: the container owns role="listbox", every row is a
   role="option" carrying aria-selected, and the whole group is ONE Tab stop
   (roving tabindex) with Arrow/Home/End moving between options. Rows stay
   plain <div>s, so click behaviour, CSS classes and ids are unchanged.

   wireRowKey() still owns Enter/Space activation (including its "let a real
   control inside the row handle its own key" rule); this module only adds the
   option/selection semantics and the roving Tab stop on top of it.

   Pick exactly one keyboard model per widget via `selectionFollowsFocus`:
     true  — arrows select as they move. Correct when moving *is* the
             selection and it is cheap (Intercept's hold queue, mirroring
             Scanner's issue list).
     false — arrows only move focus, Enter/Space commits. Correct when
             committing is expensive or destructive (Repeater/Intruder
             history reload the editor and hit the network).
*/

const NAV_KEYS = ['ArrowDown', 'ArrowUp', 'Home', 'End'];

// focusOption moves the single Tab stop to `index` and focuses that option.
export function focusOption(els, index) {
  if (!els.length) return null;
  const i = Math.max(0, Math.min(index, els.length - 1));
  els.forEach((el, n) => { el.tabIndex = n === i ? 0 : -1; });
  els[i].focus({ preventScroll: true });
  return els[i];
}

// wireListbox promotes `rows` (already rendered inside `container`) to options.
// `selected(el)` reports the current row when the caller does not render
// aria-selected in its own template; `activate(el,event)` performs the row's
// action. Returns the option elements in order.
export function wireListbox(container, rows, { label = '', activate, selected, selectionFollowsFocus = false } = {}) {
  if (!container) return [];
  container.setAttribute('role', 'listbox');
  if (label) container.setAttribute('aria-label', label);
  const els = [...rows];
  if (!els.length) return els;
  els.forEach(el => {
    el.setAttribute('role', 'option');
    if (selected) el.setAttribute('aria-selected', selected(el) ? 'true' : 'false');
    else if (!el.hasAttribute('aria-selected')) el.setAttribute('aria-selected', 'false');
  });
  // Exactly one Tab stop: the selected option, otherwise the first one.
  let stop = els.findIndex(el => el.getAttribute('aria-selected') === 'true');
  if (stop < 0) stop = 0;
  els.forEach((el, i) => { el.tabIndex = i === stop ? 0 : -1; });
  els.forEach(el => {
    if (activate) el.onclick = e => activate(el, e);
    wireRowKey(el, activate ? (e => activate(el, e)) : undefined);
    el.addEventListener('keydown', e => {
      if (!NAV_KEYS.includes(e.key)) return;
      // A real control inside the option keeps its own caret/arrow handling.
      const t = e.target;
      if (t !== el && t.closest && t.closest('button,input,textarea,select,a,[role="button"]')) return;
      e.preventDefault();
      const from = els.indexOf(el);
      const to = e.key === 'ArrowDown' ? (from + 1) % els.length
        : e.key === 'ArrowUp' ? (from - 1 + els.length) % els.length
          : e.key === 'Home' ? 0 : els.length - 1;
      focusOption(els, to);
      if (selectionFollowsFocus && to !== from && activate) activate(els[to], e);
    });
  });
  return els;
}

// setListboxSelection re-points aria-selected (and the roving Tab stop) without
// re-rendering, for panels that update a live list in place.
export function setListboxSelection(rows, isSelected) {
  const els = [...rows];
  let stop = -1;
  els.forEach((el, i) => {
    const on = !!isSelected(el);
    el.setAttribute('aria-selected', on ? 'true' : 'false');
    if (on && stop < 0) stop = i;
  });
  if (stop < 0) stop = 0;
  // Never steal focus here: only move the Tab stop when focus is elsewhere.
  const focusedInside = els.some(el => el === document.activeElement);
  if (!focusedInside) els.forEach((el, i) => { el.tabIndex = i === stop ? 0 : -1; });
}
