// diff.js — line/word diff (Myers O(ND)), body normalizers and a chunked DOM
// renderer. The algorithm and normalizers are pure (no imports, no DOM access
// outside functions) so they run under `node --test`. The renderer builds rows
// with textContent only, never innerHTML, so flow bodies cannot inject markup.

export const MAX_DIFF_LINES = 20000; // changed region above this -> "too different"
export const MAX_EDIT_DISTANCE = 2500; // bounds trace memory and runtime
export const RENDER_CHUNK = 500;

function toLines(x) {
  if (Array.isArray(x)) return x;
  const s = String(x == null ? '' : x);
  return s === '' ? [] : s.split(/\r?\n/);
}

// myers returns the edit script as [{type,ai,bi}] over int arrays, or null when
// the edit distance exceeds maxD.
function myers(A, B, maxD) {
  const n = A.length, m = B.length;
  if (n === 0) return B.map((_, bi) => ({ type: 'add', bi }));
  if (m === 0) return A.map((_, ai) => ({ type: 'del', ai }));
  const max = Math.min(n + m, maxD);
  const off = max + 1;
  const v = new Int32Array(2 * max + 3);
  const trace = [];
  let found = -1;
  for (let d = 0; d <= max && found < 0; d++) {
    trace.push(v.slice(off - d, off + d + 1));
    for (let k = -d; k <= d; k += 2) {
      let x;
      if (k === -d || (k !== d && v[off + k - 1] < v[off + k + 1])) x = v[off + k + 1];
      else x = v[off + k - 1] + 1;
      let y = x - k;
      while (x < n && y < m && A[x] === B[y]) { x++; y++; }
      v[off + k] = x;
      if (x >= n && y >= m) { found = d; break; }
    }
  }
  if (found < 0) return null;
  const ops = [];
  let x = n, y = m;
  for (let d = found; d > 0; d--) {
    const snap = trace[d];
    const get = (kk) => snap[kk + d];
    const k = x - y;
    const down = k === -d || (k !== d && get(k - 1) < get(k + 1));
    const prevK = down ? k + 1 : k - 1;
    const prevX = get(prevK);
    const prevY = prevX - prevK;
    const startX = down ? prevX : prevX + 1;
    const startY = down ? prevY + 1 : prevY;
    while (x > startX && y > startY) { x--; y--; ops.push({ type: 'eq', ai: x, bi: y }); }
    ops.push(down ? { type: 'add', bi: prevY } : { type: 'del', ai: prevX });
    x = prevX; y = prevY;
  }
  while (x > 0 && y > 0) { x--; y--; ops.push({ type: 'eq', ai: x, bi: y }); }
  return ops.reverse();
}

// diffLines(a, b) -> {tooDifferent, ops:[{type:'eq'|'add'|'del', text, a, b}], stats}
// a and b are strings or line arrays. Lines are hashed to ints and the common
// prefix/suffix are trimmed first, so near-identical large bodies stay cheap.
export function diffLines(a, b, { maxLines = MAX_DIFF_LINES, maxD = MAX_EDIT_DISTANCE } = {}) {
  const A = toLines(a), B = toLines(b);
  const ids = new Map();
  const hash = (l) => { let id = ids.get(l); if (id === undefined) { id = ids.size; ids.set(l, id); } return id; };
  const HA = A.map(hash), HB = B.map(hash);
  let pre = 0;
  while (pre < HA.length && pre < HB.length && HA[pre] === HB[pre]) pre++;
  let suf = 0;
  while (suf < HA.length - pre && suf < HB.length - pre && HA[HA.length - 1 - suf] === HB[HB.length - 1 - suf]) suf++;
  const ma = HA.slice(pre, HA.length - suf), mb = HB.slice(pre, HB.length - suf);
  const tooDifferent = { tooDifferent: true, ops: [], stats: { added: 0, removed: 0 } };
  if (ma.length > maxLines || mb.length > maxLines) return tooDifferent;
  const mid = myers(ma, mb, maxD);
  if (!mid) return tooDifferent;
  const ops = [];
  for (let i = 0; i < pre; i++) ops.push({ type: 'eq', text: A[i], a: i, b: i });
  for (const o of mid) {
    if (o.type === 'eq') ops.push({ type: 'eq', text: A[pre + o.ai], a: pre + o.ai, b: pre + o.bi });
    else if (o.type === 'del') ops.push({ type: 'del', text: A[pre + o.ai], a: pre + o.ai, b: -1 });
    else ops.push({ type: 'add', text: B[pre + o.bi], a: -1, b: pre + o.bi });
  }
  for (let i = 0; i < suf; i++) { const ai = A.length - suf + i, bi = B.length - suf + i; ops.push({ type: 'eq', text: A[ai], a: ai, b: bi }); }
  let added = 0, removed = 0;
  for (const o of ops) { if (o.type === 'add') added++; else if (o.type === 'del') removed++; }
  return { tooDifferent: false, ops, stats: { added, removed } };
}

// diffWords(a, b) -> {a:[{text,changed}], b:[...]} for highlighting inside a
// changed line pair. Adjacent segments with the same state are merged.
export function diffWords(a, b) {
  const tok = (s) => String(s).match(/\s+|[A-Za-z0-9_]+|[^\sA-Za-z0-9_]/g) || [];
  const TA = tok(a), TB = tok(b);
  const whole = () => ({ a: TA.length ? [{ text: String(a), changed: true }] : [], b: TB.length ? [{ text: String(b), changed: true }] : [] });
  if (TA.length > 2000 || TB.length > 2000) return whole();
  const ids = new Map();
  const hash = (t) => { let id = ids.get(t); if (id === undefined) { id = ids.size; ids.set(t, id); } return id; };
  const script = myers(TA.map(hash), TB.map(hash), 800);
  if (!script) return whole();
  const outA = [], outB = [];
  const push = (arr, text, changed) => {
    const last = arr[arr.length - 1];
    if (last && last.changed === changed) last.text += text; else arr.push({ text, changed });
  };
  for (const o of script) {
    if (o.type === 'eq') { push(outA, TA[o.ai], false); push(outB, TB[o.bi], false); }
    else if (o.type === 'del') push(outA, TA[o.ai], true);
    else push(outB, TB[o.bi], true);
  }
  return { a: outA, b: outB };
}

// hunks(ops, context) groups ops into visible runs and collapsed unchanged
// regions (only collapsed when more than two lines would hide).
export function hunks(ops, context = 3) {
  const keep = new Array(ops.length).fill(false);
  ops.forEach((o, i) => {
    if (o.type === 'eq') return;
    for (let j = Math.max(0, i - context); j <= Math.min(ops.length - 1, i + context); j++) keep[j] = true;
  });
  const out = [];
  let i = 0;
  while (i < ops.length) {
    let j = i;
    if (keep[i]) {
      while (j < ops.length && keep[j]) j++;
      out.push({ kind: 'ops', ops: ops.slice(i, j) });
    } else {
      while (j < ops.length && !keep[j]) j++;
      const hidden = ops.slice(i, j);
      if (hidden.length > 2) out.push({ kind: 'collapsed', count: hidden.length, ops: hidden });
      else out.push({ kind: 'ops', ops: hidden });
    }
    i = j;
  }
  return out;
}

// changeStarts: index of the first op of every contiguous change block.
export function changeStarts(ops) {
  const starts = [];
  ops.forEach((o, i) => { if (o.type !== 'eq' && (i === 0 || ops[i - 1].type === 'eq')) starts.push(i); });
  return starts;
}

// splitRows pairs deletions with additions for side-by-side rendering.
export function splitRows(ops) {
  const rows = [];
  let i = 0;
  while (i < ops.length) {
    if (ops[i].type === 'eq') { rows.push({ left: ops[i], right: ops[i] }); i++; continue; }
    const dels = [], adds = [];
    while (i < ops.length && ops[i].type === 'del') dels.push(ops[i++]);
    while (i < ops.length && ops[i].type === 'add') adds.push(ops[i++]);
    const n = Math.max(dels.length, adds.length);
    for (let k = 0; k < n; k++) rows.push({ left: dels[k] || null, right: adds[k] || null });
  }
  return rows;
}

// ---- normalizers: ignore volatile values so the real difference stands out ----
export const DEFAULT_NORMALIZE_RULES = [
  { name: 'date headers', re: /^(Date|Expires|Last-Modified):.*$/gim, to: '$1: <date>' },
  { name: 'cookie header', re: /^(Cookie):.*$/gim, to: '$1: <cookie>' },
  { name: 'set-cookie value', re: /^(Set-Cookie):\s*([^=;\s]+)=[^;\r\n]*/gim, to: '$1: $2=<cookie>' },
  { name: 'request ids', re: /^(X-Request-Id|X-Correlation-Id|X-Trace-Id|Request-Id|X-Amzn-Trace-Id|CF-Ray):.*$/gim, to: '$1: <id>' },
  { name: 'csrf headers', re: /^(X-CSRF-Token|X-XSRF-Token):.*$/gim, to: '$1: <csrf>' },
  { name: 'csrf json', re: /("(?:csrf|xsrf)[\w-]*"\s*:\s*")[^"]*(")/gi, to: '$1<csrf>$2' },
  { name: 'csrf form', re: /((?:csrf|xsrf)[\w-]*=)[^&\s]*/gi, to: '$1<csrf>' },
  { name: 'iso timestamps', re: /\b\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:?\d{2})?/g, to: '<timestamp>' },
];

export function normalizeText(text, rules = DEFAULT_NORMALIZE_RULES) {
  let out = String(text == null ? '' : text);
  for (const r of rules) out = out.replace(r.re, r.to);
  return out;
}

function sortKeys(v) {
  if (Array.isArray(v)) return v.map(sortKeys);
  if (v && typeof v === 'object') {
    const o = {};
    for (const k of Object.keys(v).sort()) o[k] = sortKeys(v[k]);
    return o;
  }
  return v;
}
export function normalizeJSON(text) {
  try { return JSON.stringify(sortKeys(JSON.parse(text)), null, 2); } catch (e) { return text; }
}
export function normalizeBody(text, { json = false, ignore = false, rules } = {}) {
  let out = String(text == null ? '' : text);
  if (json) out = normalizeJSON(out);
  if (ignore) out = normalizeText(out, rules || DEFAULT_NORMALIZE_RULES);
  return out;
}

// ---- DOM view ----
// createDiffView(container, {mode, context}) renders a diffLines() result.
// Rows carry both a +/- glyph and a colour; collapsed regions expose "Expand N
// lines" buttons; next()/prev() move between change blocks (bound to n/N by the
// owning panel). Rendering is chunked through requestAnimationFrame.
export function createDiffView(container, { mode = 'unified', context = 3, doc } = {}) {
  const d = doc || container.ownerDocument;
  const state = { result: null, mode, token: 0, changeEls: [], cursor: -1 };
  const live = d.createElement('div');
  live.className = 'u-sr';
  live.setAttribute('role', 'status');
  live.setAttribute('aria-live', 'polite');

  const el = (tag, cls, text) => { const e = d.createElement(tag); if (cls) e.className = cls; if (text != null) e.textContent = text; return e; };
  const glyphOf = { add: '+', del: '-', eq: ' ' };
  const nameOf = { add: 'added', del: 'removed', eq: '' };

  function segments(parent, segs) {
    for (const s of segs) {
      if (s.changed) { const m = el('mark', 'diff-word', s.text); parent.appendChild(m); }
      else parent.appendChild(d.createTextNode(s.text));
    }
  }
  function unifiedRow(op, words) {
    const row = el('div', 'diff-row diff-' + op.type);
    row.appendChild(el('span', 'diff-no', op.a >= 0 ? String(op.a + 1) : ''));
    row.appendChild(el('span', 'diff-no', op.b >= 0 ? String(op.b + 1) : ''));
    const g = el('span', 'diff-glyph', glyphOf[op.type]);
    row.appendChild(g);
    if (nameOf[op.type]) row.appendChild(el('span', 'u-sr', nameOf[op.type] + ': '));
    const t = el('span', 'diff-text');
    if (words) segments(t, words); else t.textContent = op.text;
    row.appendChild(t);
    return row;
  }
  function splitCell(op, words, side) {
    const cell = el('div', 'diff-cell' + (op ? ' diff-' + op.type : ' diff-empty'));
    if (!op) return cell;
    cell.appendChild(el('span', 'diff-no', String((side === 'left' ? op.a : op.b) + 1)));
    cell.appendChild(el('span', 'diff-glyph', glyphOf[op.type]));
    if (nameOf[op.type]) cell.appendChild(el('span', 'u-sr', nameOf[op.type] + ': '));
    const t = el('span', 'diff-text');
    if (words) segments(t, words); else t.textContent = op.text;
    cell.appendChild(t);
    return cell;
  }
  // buildItems flattens hunks into render jobs; word-level highlighting is only
  // computed for a lone del/add pair so a huge change block stays cheap.
  function buildNodes(ops) {
    const nodes = [];
    if (state.mode === 'split') {
      for (const r of splitRows(ops)) {
        let wl = null, wr = null;
        if (r.left && r.right && r.left.type === 'del' && r.right.type === 'add') { const w = diffWords(r.left.text, r.right.text); wl = w.a; wr = w.b; }
        const row = el('div', 'diff-split-row');
        row.appendChild(splitCell(r.left, wl, 'left'));
        row.appendChild(splitCell(r.right, wr, 'right'));
        if ((r.left && r.left.type !== 'eq') || (r.right && r.right.type !== 'eq')) row.dataset.change = '1';
        nodes.push({ node: row, change: row.dataset.change === '1' });
      }
      return nodes;
    }
    for (let i = 0; i < ops.length; i++) {
      const o = ops[i];
      let words = null;
      if (o.type === 'del' && ops[i + 1] && ops[i + 1].type === 'add' && (i === 0 || ops[i - 1].type !== 'del') && (!ops[i + 2] || ops[i + 2].type !== 'add')) {
        const w = diffWords(o.text, ops[i + 1].text);
        nodes.push({ node: unifiedRow(o, w.a), change: true });
        nodes.push({ node: unifiedRow(ops[i + 1], w.b), change: false });
        i++;
        continue;
      }
      nodes.push({ node: unifiedRow(o, words), change: o.type !== 'eq' && (i === 0 || ops[i - 1].type === 'eq') });
    }
    return nodes;
  }
  function appendChunked(parent, nodes, token, before) {
    let i = 0;
    const step = () => {
      if (token !== state.token) return;
      const frag = d.createDocumentFragment();
      const end = Math.min(nodes.length, i + RENDER_CHUNK);
      for (; i < end; i++) { frag.appendChild(nodes[i].node); if (nodes[i].change) state.changeEls.push(nodes[i].node); }
      parent.insertBefore(frag, before || null);
      if (i < nodes.length) requestAnimationFrame(step);
    };
    step();
  }
  function expander(hidden, token) {
    const b = el('button', 'diff-expand btn xs', `Expand ${hidden.count} unchanged ${hidden.count === 1 ? 'line' : 'lines'}`);
    b.type = 'button';
    b.addEventListener('click', () => {
      const parent = b.parentNode, next = b.nextSibling;
      parent.removeChild(b);
      appendChunked(parent, buildNodes(hidden.ops), token, next);
    });
    return b;
  }
  function render() {
    state.token++;
    state.changeEls = [];
    state.cursor = -1;
    container.textContent = '';
    container.classList.add('diff');
    container.dataset.mode = state.mode;
    container.appendChild(live);
    const r = state.result;
    if (!r) return;
    if (r.tooDifferent) { container.appendChild(el('p', 'diff-note', 'These messages are too different to compare line by line.')); return; }
    if (!r.stats.added && !r.stats.removed) { container.appendChild(el('p', 'diff-note', 'No differences.')); return; }
    const body = el('div', 'diff-body');
    container.appendChild(body);
    const token = state.token;
    for (const h of hunks(r.ops, context)) {
      if (h.kind === 'collapsed') body.appendChild(expander(h, token));
      else appendChunked(body, buildNodes(h.ops), token);
    }
  }
  function go(delta) {
    if (!state.changeEls.length) return;
    state.cursor = (state.cursor + delta + state.changeEls.length) % state.changeEls.length;
    const target = state.changeEls[state.cursor];
    target.tabIndex = -1;
    target.focus({ preventScroll: true });
    if (target.scrollIntoView) target.scrollIntoView({ block: 'center' });
    live.textContent = `Change ${state.cursor + 1} of ${state.changeEls.length}`;
  }
  return {
    setResult(r) { state.result = r; render(); },
    setMode(m) { state.mode = m === 'split' ? 'split' : 'unified'; render(); },
    next: () => go(1),
    prev: () => go(-1),
    destroy() { state.token++; container.textContent = ''; },
  };
}
