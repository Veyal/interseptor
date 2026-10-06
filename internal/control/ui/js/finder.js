// finder.js — in-pane find bar. `/` or Ctrl+F (bound by the owning panel through
// keys.js) opens a docked mini bar; Enter / Shift+Enter step through matches,
// Alt+R toggles regex, Alt+C toggles case. Matches are highlighted by wrapping
// text-node ranges in <mark>, so syntax highlighting is preserved and nothing is
// re-rendered through innerHTML. At most 5,000 matches are highlighted.

export const MAX_HIGHLIGHTS = 5000;

// findMatches is pure: literal or regex search over a string.
export function findMatches(text, query, { regex = false, caseSensitive = false, cap = MAX_HIGHLIGHTS } = {}) {
  const out = { matches: [], capped: false, error: '' };
  if (!query) return out;
  let re;
  try {
    const src = regex ? query : query.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
    re = new RegExp(src, caseSensitive ? 'g' : 'gi');
  } catch (e) {
    out.error = (e && e.message) || 'invalid pattern';
    return out;
  }
  let m;
  while ((m = re.exec(text)) !== null) {
    if (m[0] === '') { re.lastIndex++; continue; }
    if (out.matches.length >= cap) { out.capped = true; break; }
    out.matches.push({ start: m.index, end: m.index + m[0].length });
  }
  return out;
}

// createFinder(container, {root, getText, regex, caseSensitive, doc, label})
// container hosts the docked bar; root (default container) is the searched
// subtree; getText optionally supplies the searched string and must equal the
// concatenated text of root's text nodes.
export function createFinder(container, opts = {}) {
  const d = opts.doc || container.ownerDocument;
  const root = opts.root || container;
  const state = { regex: !!opts.regex, caseSensitive: !!opts.caseSensitive, query: '', marks: [], active: -1, bar: null, timer: null };
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
      if (e.key === 'Enter') { e.preventDefault(); step(e.shiftKey ? -1 : 1); }
      else if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); close_(); }
      else if (e.altKey && (e.key === 'r' || e.key === 'R')) { e.preventDefault(); toggle('regex'); }
      else if (e.altKey && (e.key === 'c' || e.key === 'C')) { e.preventDefault(); toggle('caseSensitive'); }
    });
    prev.addEventListener('click', () => step(-1));
    next.addEventListener('click', () => step(1));
    rx.addEventListener('click', () => toggle('regex'));
    cs.addEventListener('click', () => toggle('caseSensitive'));
    close.addEventListener('click', close_);
    container.insertBefore(bar, container.firstChild);
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
    state.marks = [];
    state.active = -1;
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
    if (!groups.length) return;
    if (state.active >= 0 && groups[state.active]) groups[state.active].forEach((m) => m.classList.remove('is-active'));
    state.active = (i + groups.length) % groups.length;
    groups[state.active].forEach((m) => m.classList.add('is-active'));
    const first = groups[state.active][0];
    if (first && first.scrollIntoView) first.scrollIntoView({ block: 'nearest' });
    state.bar.count.textContent = `${state.active + 1} / ${groups.length}`;
  }
  function step(delta) { if (state.matchMarks && state.matchMarks.length) activate(state.active + delta); }
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
    state.matchMarks = [];
    if (state.bar) { state.bar.el.hidden = true; state.bar.count.textContent = ''; }
    if (opts.onClose) opts.onClose();
  }
  return {
    open, close: close_, next: () => step(1), prev: () => step(-1),
    refresh: run, isOpen: () => !!state.bar && !state.bar.el.hidden,
    setQuery(q) { state.query = q; if (state.bar) state.bar.input.value = q; run(); },
    destroy() { clearMarks(); clearTimeout(state.timer); if (state.bar) state.bar.el.remove(); state.bar = null; },
  };
}
