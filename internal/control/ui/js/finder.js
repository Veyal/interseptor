// finder.js — in-pane find bar. `/` or Ctrl+F (bound by the owning panel through
// keys.js) opens a docked mini bar; Enter / Shift+Enter step through matches,
// Alt+R toggles regex, Alt+C toggles case. Matches are highlighted by wrapping
// text-node ranges in <mark>, so syntax highlighting is preserved and nothing is
// re-rendered through innerHTML. At most 5,000 matches are highlighted.

import { isImeKeyEvent } from './keys.js';

export const MAX_HIGHLIGHTS = 5000;

// finderKeyAction maps a keydown in the find field to 'next' | 'prev' | 'close' |
// 'regex' | 'case' | null. The Alt shortcuts match the physical key: on macOS
// Option+R types "®" and Option+C types "ç", so e.key never equals the letter.
// Ctrl+Alt is AltGr on Windows (it types a character) and is left alone, and so
// is every key of an IME composition.
export function finderKeyAction(e) {
  if (!e || isImeKeyEvent(e)) return null;
  if (e.key === 'Enter') return e.shiftKey ? 'prev' : 'next';
  if (e.key === 'Escape') return 'close';
  if (e.altKey && !e.ctrlKey && !e.metaKey) {
    const letter = /^Key[A-Z]$/.test(e.code || '') ? e.code.slice(3).toLowerCase() : String(e.key || '').toLowerCase();
    if (letter === 'r') return 'regex';
    if (letter === 'c') return 'case';
  }
  return null;
}

const isHighSurrogate = (c) => c >= 0xd800 && c <= 0xdbff;
const isLowSurrogate = (c) => c >= 0xdc00 && c <= 0xdfff;

// widenOutOfSurrogate pulls a match boundary back onto a code-point boundary. A
// pattern without the `u` flag matches UTF-16 code units, so "." over an emoji
// reports two one-unit matches; wrapping either half in a <mark> splits the pair
// into two lone surrogates, which render as U+FFFD and make the bar count one
// character as two matches.
function widenOutOfSurrogate(text, start, end) {
  let s = start;
  let e = end;
  if (s > 0 && isLowSurrogate(text.charCodeAt(s)) && isHighSurrogate(text.charCodeAt(s - 1))) s--;
  if (e < text.length && isHighSurrogate(text.charCodeAt(e - 1)) && isLowSurrogate(text.charCodeAt(e))) e++;
  return { start: s, end: e };
}

// findMatches is pure: literal or regex search over a string. Every reported
// range starts and ends on a code-point boundary, and a later match never starts
// inside the previous one (widening two adjacent halves can collide).
export function findMatches(text, query, { regex = false, caseSensitive = false, cap = MAX_HIGHLIGHTS } = {}) {
  const out = { matches: [], capped: false, error: '' };
  if (!query) return out;
  const haystack = String(text == null ? '' : text);
  let re;
  try {
    const src = regex ? query : query.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
    re = new RegExp(src, caseSensitive ? 'g' : 'gi');
  } catch (e) {
    out.error = (e && e.message) || 'invalid pattern';
    return out;
  }
  let m;
  while ((m = re.exec(haystack)) !== null) {
    if (m[0] === '') { re.lastIndex++; continue; }
    const wide = widenOutOfSurrogate(haystack, m.index, m.index + m[0].length);
    const prev = out.matches[out.matches.length - 1];
    const start = prev && wide.start < prev.end ? prev.end : wide.start;
    if (wide.end <= start) continue;
    if (out.matches.length >= cap) { out.capped = true; break; }
    out.matches.push({ start, end: wide.end });
  }
  return out;
}

// nextMarkIndex is the list advance next()/prev() perform: wrap around a non-empty match
// list, and report -1 for an empty one so the caller does nothing at all. An empty list
// means there is no match to activate — the bar keeps the count text the last run() wrote
// instead of blanking it with "1 / 0".
export function nextMarkIndex(current, count, dir) {
  const n = Number(count) > 0 ? Math.floor(Number(count)) : 0;
  if (!n) return -1;
  const from = Number.isFinite(current) ? Math.floor(current) : -1;
  const step = Number.isFinite(dir) ? Math.sign(dir) : 0;
  return ((from + step) % n + n) % n;
}

// clearMarkState drops every mark reference and the active index together, which is what
// clearMarks() owes the caller. Keeping the two lists in one place matters: state.marks is
// the flat list of <mark> nodes clearMarks() unwraps, while state.matchMarks groups them
// per match and is what next()/prev() walk. A reset that emptied only state.marks left the
// grouped list pointing at detached nodes, so a step after clearing the query re-activated
// a mark that was no longer in the document (the bar wrote "1 / N" and scrolled a detached
// node). Exported so the reset is unit-testable without a DOM.
export function clearMarkState(state) {
  state.marks = [];
  state.matchMarks = [];
  state.active = -1;
  return state;
}

// createFinder(container, {root, getText, regex, caseSensitive, doc, label})
// container hosts the docked bar (placed before insertBefore() when given, else
// first); root (default container) is the searched subtree; getText optionally supplies the searched string and must equal the
// concatenated text of root's text nodes.
export function createFinder(container, opts = {}) {
  const d = opts.doc || container.ownerDocument;
  const root = opts.root || container;
  const state = { regex: !!opts.regex, caseSensitive: !!opts.caseSensitive, query: '', marks: [], matchMarks: [], active: -1, bar: null, timer: null };
  const SVG = 'http://www.w3.org/2000/svg';
  const sprite = (name) => {
    const svg = d.createElementNS(SVG, 'svg');
    svg.setAttribute('class', 'icon');
    svg.setAttribute('aria-hidden', 'true');
    svg.setAttribute('focusable', 'false');
    const use = d.createElementNS(SVG, 'use');
    use.setAttribute('href', '#i-' + name);
    svg.appendChild(use);
    return svg;
  };
  // content is a sprite name ({icon}) or visible text.
  const btn = (cls, label, content) => {
    const b = d.createElement('button');
    b.type = 'button';
    b.className = 'btn xs finder-btn ' + cls;
    b.setAttribute('aria-label', label);
    b.title = label;
    if (content.icon) b.appendChild(sprite(content.icon)); else b.textContent = content.text;
    return b;
  };

  function build() {
    const bar = d.createElement('div');
    bar.className = 'finder';
    bar.setAttribute('role', 'search');
    bar.hidden = true;
    const input = d.createElement('input');
    input.type = 'search';
    input.className = 'finder-input';
    input.setAttribute('aria-label', opts.label || 'Find in message');
    input.placeholder = 'Find';
    input.autocomplete = 'off';
    input.spellcheck = false;
    const count = d.createElement('span');
    count.className = 'finder-count';
    count.setAttribute('role', 'status');
    count.setAttribute('aria-live', 'polite');
    const prev = btn('finder-prev', 'Previous match (Shift+Enter)', { icon: 'chevron' });
    const next = btn('finder-next', 'Next match (Enter)', { icon: 'chevron' });
    const rx = btn('finder-regex', 'Use regular expression (Alt+R)', { text: '.*' });
    const cs = btn('finder-case', 'Match case (Alt+C)', { text: 'Aa' });
    const close = btn('finder-close', 'Close find bar (Escape)', { icon: 'close' });
    rx.setAttribute('aria-pressed', String(state.regex));
    cs.setAttribute('aria-pressed', String(state.caseSensitive));
    bar.append(input, count, prev, next, rx, cs, close);
    input.addEventListener('input', () => { state.query = input.value; clearTimeout(state.timer); state.timer = setTimeout(run, 120); });
    input.addEventListener('keydown', (e) => {
      const action = finderKeyAction(e);
      if (!action) return;
      // Escape closes only the bar and never reaches the dialog or drawer under it.
      if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); close_(); }
      else if (action === 'next' || action === 'prev') { e.preventDefault(); step(action === 'prev' ? -1 : 1); }
      else { e.preventDefault(); toggle(action === 'regex' ? 'regex' : 'caseSensitive'); }
    });
    prev.addEventListener('click', () => step(-1));
    next.addEventListener('click', () => step(1));
    rx.addEventListener('click', () => toggle('regex'));
    cs.addEventListener('click', () => toggle('caseSensitive'));
    close.addEventListener('click', close_);
    const ref = opts.insertBefore ? opts.insertBefore() : null;
    container.insertBefore(bar, ref && ref.parentNode === container ? ref : container.firstChild);
    state.bar = { el: bar, input, count, rx, cs };
  }
  function toggle(which) {
    state[which] = !state[which];
    state.bar.rx.setAttribute('aria-pressed', String(state.regex));
    state.bar.cs.setAttribute('aria-pressed', String(state.caseSensitive));
    run();
  }
  function textNodes() {
    const walker = d.createTreeWalker(root, 4 /* NodeFilter.SHOW_TEXT */, {
      acceptNode: (n) => (n.parentNode && n.parentNode.closest && n.parentNode.closest('.finder,script,style') ? 2 : 1),
    });
    const nodes = [];
    for (let n = walker.nextNode(); n; n = walker.nextNode()) nodes.push(n);
    return nodes;
  }
  function clearMarks() {
    for (const m of state.marks) {
      const p = m.parentNode;
      if (!p) continue;
      while (m.firstChild) p.insertBefore(m.firstChild, m);
      p.removeChild(m);
      p.normalize();
    }
    clearMarkState(state);
  }
  function run() {
    clearMarks();
    const ui = state.bar;
    if (!ui) return;
    ui.input.removeAttribute('aria-invalid');
    if (!state.query) { ui.count.textContent = ''; return; }
    const nodes = textNodes();
    const offsets = [];
    let text = '';
    for (const n of nodes) { offsets.push(text.length); text += n.nodeValue; }
    const found = findMatches(opts.getText ? opts.getText() : text, state.query, { regex: state.regex, caseSensitive: state.caseSensitive });
    if (found.error) { ui.input.setAttribute('aria-invalid', 'true'); ui.count.textContent = 'Invalid pattern'; return; }
    // Wrap from the last match backwards so earlier offsets stay valid.
    const marksByMatch = [];
    for (let mi = found.matches.length - 1; mi >= 0; mi--) {
      const { start, end } = found.matches[mi];
      const segs = [];
      for (let ni = 0; ni < nodes.length; ni++) {
        const ns = offsets[ni], ne = ns + nodes[ni].nodeValue.length;
        if (ne <= start || ns >= end) continue;
        segs.push({ node: nodes[ni], from: Math.max(start, ns) - ns, to: Math.min(end, ne) - ns });
      }
      const made = [];
      for (let si = segs.length - 1; si >= 0; si--) {
        const range = d.createRange();
        range.setStart(segs[si].node, segs[si].from);
        range.setEnd(segs[si].node, segs[si].to);
        const mark = d.createElement('mark');
        mark.className = 'finder-hit';
        range.surroundContents(mark);
        made.unshift(mark);
      }
      marksByMatch[mi] = made;
    }
    state.marks = marksByMatch.flat();
    state.matchMarks = marksByMatch;
    if (!found.matches.length) { ui.count.textContent = 'No matches'; return; }
    ui.count.textContent = found.capped ? `${found.matches.length}+ matches, refine search` : `${found.matches.length} ${found.matches.length === 1 ? 'match' : 'matches'}`;
    activate(0);
  }
  function activate(i) {
    const groups = state.matchMarks || [];
    const next = nextMarkIndex(i, groups.length, 0);
    if (next < 0) { state.active = -1; return; }
    if (state.active >= 0 && groups[state.active]) groups[state.active].forEach((m) => m.classList.remove('is-active'));
    state.active = next;
    groups[state.active].forEach((m) => m.classList.add('is-active'));
    const first = groups[state.active][0];
    if (first && first.scrollIntoView) first.scrollIntoView({ block: 'nearest' });
    state.bar.count.textContent = `${state.active + 1} / ${groups.length}`;
  }
  function step(delta) {
    // An empty list is the "cleared the query" case: nothing to activate, so nothing runs.
    const next = nextMarkIndex(state.active, (state.matchMarks || []).length, delta);
    if (next < 0) return;
    activate(next);
  }
  function open(initial) {
    if (!state.bar) build();
    state.bar.el.hidden = false;
    if (typeof initial === 'string' && initial) { state.bar.input.value = initial; state.query = initial; }
    state.bar.input.focus();
    state.bar.input.select();
    run();
  }
  function close_() {
    clearMarks();
    if (state.bar) { state.bar.el.hidden = true; state.bar.count.textContent = ''; }
    if (opts.onClose) opts.onClose();
  }
  return {
    container, open, close: close_, next: () => step(1), prev: () => step(-1),
    refresh: run, isOpen: () => !!state.bar && !state.bar.el.hidden,
    setQuery(q) { state.query = q; if (state.bar) state.bar.input.value = q; run(); },
    destroy() { clearMarks(); clearTimeout(state.timer); if (state.bar) state.bar.el.remove(); state.bar = null; },
  };
}
