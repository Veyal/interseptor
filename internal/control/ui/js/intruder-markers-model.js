// intruder-markers-model.js — pure model for highlighting §…§ injection points in
// the Intruder request template. Mirrors intrMarkers() (/§[^§]+§/g): empty §§ and
// an unpaired § are not positions and are reported as `invalid`.

// splitMarkers(template) -> [{text, kind: plain|delim|marker|invalid, index?}]
// `index` is the zero-based position number (the payload-list / colour index).
// Concatenating every segment's text yields the input exactly.
export function splitMarkers(src) {
  const s = String(src ?? '');
  const out = [];
  let plainFrom = 0, n = 0, i = 0;
  const flushPlain = (to) => { if (to > plainFrom) out.push({ text: s.slice(plainFrom, to), kind: 'plain' }); };
  while ((i = s.indexOf('§', i)) !== -1) {
    const j = s.indexOf('§', i + 1);
    if (j === -1) { // no closer anywhere after: this and every later § is stray
      flushPlain(i);
      out.push({ text: '§', kind: 'invalid' });
      plainFrom = ++i;
      continue;
    }
    flushPlain(i);
    if (j === i + 1) { // empty §§: the first § is stray; the second may open a marker
      out.push({ text: '§', kind: 'invalid' });
      plainFrom = ++i;
      continue;
    }
    out.push({ text: '§', kind: 'delim', index: n },
      { text: s.slice(i + 1, j), kind: 'marker', index: n },
      { text: '§', kind: 'delim', index: n });
    n++;
    plainFrom = i = j + 1;
  }
  flushPlain(s.length);
  return out;
}

const ESC = { '&': '&amp;', '<': '&lt;', '>': '&gt;' };
const esc = (t) => t.replace(/[&<>]/g, (c) => ESC[c]);

// markersHighlightHTML builds the overlay markup. `colors` are trusted CSS colour
// values (theme tokens) cycled by position index; every piece of template text is
// HTML-escaped. With {enabled:false} (Race mode) it returns the plain escaped text.
export function markersHighlightHTML(src, colors, opts = {}) {
  const segs = splitMarkers(src);
  if (opts.enabled === false) return segs.map((g) => esc(g.text)).join('');
  const pal = colors && colors.length ? colors : ['var(--accent)'];
  let html = '';
  for (const g of segs) {
    const t = esc(g.text);
    if (g.kind === 'plain') html += t;
    else if (g.kind === 'invalid') html += `<span class="ih-bad">${t}</span>`;
    else html += `<span class="${g.kind === 'delim' ? 'ih-d' : 'ih-m'}" style="--ih:${pal[g.index % pal.length]}">${t}</span>`;
  }
  return html;
}
