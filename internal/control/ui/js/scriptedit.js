// scriptedit.js — a dependency-free, textarea-based script editor: line numbers,
// lightweight syntax highlighting and unsupported-API hints. No build step, no
// Monaco. The highlighter and the hint scanner are pure functions (tested under
// node); createScriptEditor is the only DOM code and touches nothing outside
// the host element it is given.
//
// The overlay pattern: a transparent <textarea> sits over a <pre> that carries
// the highlighted copy, so native selection, IME, undo and mobile keyboards all
// keep working. Tab is NOT trapped (WCAG 2.1.2); Ctrl+] / Ctrl+[ indent instead.

const KEYWORDS = new Set(('async await break case catch class const continue debugger default delete do else export extends finally for function if import in instanceof let new of return static super switch this throw try typeof var void while with yield').split(' '));
const LITERALS = new Set(['true', 'false', 'null', 'undefined', 'NaN', 'Infinity']);
const GLOBALS = new Set(['pm', 'postman', 'console', 'isp', 'CryptoJS', 'Buffer', 'require', 'JSON', 'Math', 'Date', 'Promise', 'URL', 'URLSearchParams']);

export const esc = (s) => String(s).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');

// tokenize splits source into [{t, s}] covering every character (so joining the
// tokens reproduces the input exactly). t: com | str | num | kw | lit | api | id | ws | pun.
export function tokenize(src) {
  const s = String(src == null ? '' : src);
  const out = [];
  let i = 0;
  const push = (t, text) => { if (text) out.push({ t, s: text }); };
  while (i < s.length) {
    const c = s[i], n = s[i + 1];
    if (c === '/' && n === '/') { let j = s.indexOf('\n', i); if (j < 0) j = s.length; push('com', s.slice(i, j)); i = j; continue; }
    if (c === '/' && n === '*') { let j = s.indexOf('*/', i + 2); j = j < 0 ? s.length : j + 2; push('com', s.slice(i, j)); i = j; continue; }
    if (c === '"' || c === "'" || c === '`') {
      let j = i + 1;
      while (j < s.length && s[j] !== c) { if (s[j] === '\\') j++; if (c !== '`' && s[j] === '\n') break; j++; }
      j = Math.min(s.length, j + 1);
      push('str', s.slice(i, j)); i = j; continue;
    }
    if (/[0-9]/.test(c) || (c === '.' && /[0-9]/.test(n || ''))) {
      let j = i + 1;
      while (j < s.length && /[0-9a-fA-FxX._eE]/.test(s[j])) j++;
      push('num', s.slice(i, j)); i = j; continue;
    }
    if (/[A-Za-z_$]/.test(c)) {
      let j = i + 1;
      while (j < s.length && /[A-Za-z0-9_$]/.test(s[j])) j++;
      const w = s.slice(i, j);
      push(KEYWORDS.has(w) ? 'kw' : LITERALS.has(w) ? 'lit' : GLOBALS.has(w) ? 'api' : 'id', w); i = j; continue;
    }
    if (/\s/.test(c)) { let j = i + 1; while (j < s.length && /\s/.test(s[j])) j++; push('ws', s.slice(i, j)); i = j; continue; }
    push('pun', c); i++;
  }
  return out;
}

// highlightHTML returns escaped markup. A trailing newline gets a space so the
// <pre> keeps the same height as the textarea.
export function highlightHTML(src) {
  const html = tokenize(src).map((t) => (t.t === 'ws' || t.t === 'pun' || t.t === 'id' ? esc(t.s) : '<span class="se-' + t.t + '">' + esc(t.s) + '</span>')).join('');
  return html + (String(src || '').endsWith('\n') ? ' ' : '');
}

export function lineCount(src) { return String(src == null ? '' : src).split('\n').length; }

// Mirrors the server analyser's "would fail with status unsupported" set so the
// editor can warn while typing. The server analyser (script review sheet) is
// authoritative; this list is a preview and may lag it.
export const UNSUPPORTED_HINTS = [
  { re: /\bpm\.vault\b/, name: 'pm.vault', note: 'pm.vault is not shipped' },
  { re: /\bpm\.require\b/, name: 'pm.require', note: 'pm.require (package library) is not shipped' },
  { re: /\bpm\.execution\.runRequest\b/, name: 'pm.execution.runRequest', note: 'pm.execution.runRequest is not shipped' },
  { re: /\bpm\.response\.stream\b/, name: 'pm.response.stream', note: 'streamed response bodies are not shipped' },
  { re: /\bjsonSchema(Validate)?\b/, name: 'jsonSchema', note: 'JSON-schema assertions are not shipped' },
  { re: /\bpm\.visualizer\b/, name: 'pm.visualizer', note: 'pm.visualizer is not supported (renders nothing)' },
  { re: /(^|[^.\w$])_\.[a-zA-Z]+\(/, name: '_ (lodash)', note: 'lodash is not shipped' },
  { re: /\bmoment\s*\(/, name: 'moment', note: 'moment is not shipped' },
  { re: /\bcheerio\b/, name: 'cheerio', note: 'cheerio is not shipped' },
  { re: /\btv4\b|\bajv\b/, name: 'tv4/ajv', note: 'JSON-schema libraries are not shipped' },
  { re: /\bxml2Json\b/, name: 'xml2Json', note: 'xml2Json is not shipped' },
  { re: /(^|[^.\w$])fetch\s*\(/, name: 'fetch', note: 'network egress is only available through pm.sendRequest' },
  { re: /\bXMLHttpRequest\b/, name: 'XMLHttpRequest', note: 'network egress is only available through pm.sendRequest' },
  { re: /\beval\s*\(|\bnew\s+Function\s*\(/, name: 'eval', note: 'eval and Function are disabled in the sandbox' },
];

// scanUnsupported finds hint matches per line, ignoring comments and strings.
export function scanUnsupported(src) {
  const plain = tokenize(src).map((t) => (t.t === 'com' || t.t === 'str' ? t.s.replace(/[^\n]/g, ' ') : t.s)).join('');
  const hits = [];
  plain.split('\n').forEach((line, i) => {
    for (const h of UNSUPPORTED_HINTS) if (h.re.test(line)) hits.push({ line: i + 1, name: h.name, note: h.note });
  });
  return hits;
}

// SNIPPETS: the most common Postman script idioms, inserted at the caret.
export const SNIPPETS = {
  prerequest: [
    { label: 'Set a variable', code: "pm.variables.set('name', 'value');" },
    { label: 'Sign with HMAC', code: "const sig = CryptoJS.HmacSHA256(pm.request.url.toString(), pm.environment.get('secret')).toString();\npm.request.headers.add({ key: 'X-Signature', value: sig });" },
  ],
  tests: [
    { label: 'Status is 200', code: "pm.test('status is 200', () => pm.response.to.have.status(200));" },
    { label: 'JSON field equals', code: "pm.test('field matches', () => {\n  const j = pm.response.json();\n  pm.expect(j.id).to.eql(1);\n});" },
    { label: 'Save to environment', code: "pm.environment.set('token', pm.response.json().token);" },
  ],
};

// ---- DOM component ----------------------------------------------------------

const mk = (tag, cls, attrs = {}) => {
  const e = document.createElement(tag);
  if (cls) e.className = cls;
  for (const [k, v] of Object.entries(attrs)) e.setAttribute(k, v);
  return e;
};

// createScriptEditor(host, {label, value, readOnly, onChange}) -> api. The host
// is emptied. `label` becomes the textarea's accessible name.
export function createScriptEditor(host, { label = 'Script', value = '', readOnly = false, onChange } = {}) {
  host.textContent = '';
  const root = mk('div', 'se');
  const gutter = mk('div', 'se-gutter', { 'aria-hidden': 'true' });
  const stage = mk('div', 'se-stage');
  const pre = mk('pre', 'se-hl', { 'aria-hidden': 'true' });
  const ta = mk('textarea', 'se-ta', { 'aria-label': label, spellcheck: 'false', autocapitalize: 'off', autocomplete: 'off', wrap: 'off' });
  if (readOnly) ta.readOnly = true;
  const warnings = mk('ul', 'se-warnings');
  warnings.setAttribute('aria-label', label + ' warnings');
  stage.append(pre, ta);
  root.append(gutter, stage);
  host.append(root, warnings);

  let lastLines = 0;
  function paint() {
    const v = ta.value;
    pre.innerHTML = highlightHTML(v);
    const n = lineCount(v);
    if (n !== lastLines) {
      lastLines = n;
      gutter.textContent = Array.from({ length: n }, (_, i) => i + 1).join('\n');
    }
    renderWarnings(scanUnsupported(v));
  }
  function renderWarnings(list, extra = []) {
    warnings.textContent = '';
    for (const w of list.concat(extra)) {
      const li = mk('li', 'se-warn');
      li.textContent = (w.line ? 'Line ' + w.line + ': ' : '') + w.note;
      warnings.append(li);
    }
    warnings.hidden = !warnings.childNodes.length;
  }
  const syncScroll = () => {
    pre.scrollTop = ta.scrollTop; pre.scrollLeft = ta.scrollLeft; gutter.scrollTop = ta.scrollTop;
  };
  ta.addEventListener('scroll', syncScroll);
  ta.addEventListener('input', () => { paint(); if (onChange) onChange(ta.value); });
  ta.addEventListener('keydown', (e) => {
    if (!(e.ctrlKey || e.metaKey) || (e.key !== ']' && e.key !== '[')) return;
    e.preventDefault();
    indentSelection(ta, e.key === ']' ? 1 : -1);
    paint();
    if (onChange) onChange(ta.value);
  });
  ta.value = value;
  paint();

  return {
    el: root, textarea: ta,
    getValue: () => ta.value,
    setValue(v) { ta.value = v == null ? '' : v; paint(); },
    insert(code) {
      const p = ta.selectionStart || 0, q = ta.selectionEnd || 0;
      const pre0 = ta.value.slice(0, p), post = ta.value.slice(q);
      const lead = pre0 && !pre0.endsWith('\n') ? '\n' : '';
      ta.value = pre0 + lead + code + '\n' + post;
      ta.selectionStart = ta.selectionEnd = (pre0 + lead + code + '\n').length;
      paint();
      if (onChange) onChange(ta.value);
      ta.focus();
    },
    setReadOnly(on) { ta.readOnly = !!on; },
    // setNotes shows analyser findings from the server beside the local hints.
    setNotes(notes) { renderWarnings(scanUnsupported(ta.value), notes || []); },
    focus: () => ta.focus(),
  };
}

function indentSelection(ta, dir) {
  const v = ta.value;
  const start = v.lastIndexOf('\n', ta.selectionStart - 1) + 1;
  let end = v.indexOf('\n', ta.selectionEnd);
  if (end < 0) end = v.length;
  const block = v.slice(start, end).split('\n').map((l) => (dir > 0 ? '  ' + l : l.replace(/^ {1,2}/, ''))).join('\n');
  ta.value = v.slice(0, start) + block + v.slice(end);
  ta.selectionStart = start;
  ta.selectionEnd = start + block.length;
}
