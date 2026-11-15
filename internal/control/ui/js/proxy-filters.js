// proxy-filters.js: pure view-model helpers for the Proxy panel (filter counts,
// empty states, bulk verbs). No imports and no DOM access so
// every function runs under `node --test`.

// activeFilterCount mirrors the legacy anyFilter(): each simple filter counts
// once and every exclusion counts individually. Hiding TLS failures is the
// default and is not counted.
export function activeFilterCount(st) {
  const f = st.filters || {};
  let n = 0;
  for (const k of ['scheme', 'method', 'status', 'host', 'tag']) if (f[k]) n++;
  if (hasEffectiveSearch(f)) n++;
  n += (f.exclude || []).length;
  if (st.notesOnly) n++;
  if (st.inScopeOnly) n++;
  if (st.showManual === false) n++;
  if (st.showAI === false) n++;
  if (st.showCollection === false) n++;
  if (st.hideBrowserNoise === true) n++;
  return n;
}

// popoverFilterCount counts only the controls that live inside the Filters
// popover (the badge on the Filters button).
export function popoverFilterCount(st) {
  const f = st.filters || {};
  let n = 0;
  if (st.notesOnly) n++;
  if (st.showManual === false) n++;
  if (st.showAI === false) n++;
  if (st.showCollection === false) n++;
  if (f.tag) n++;
  if (st.hideTlsFailed === false) n++;
  if (st.hideBrowserNoise === true) n++;
  return n;
}

// rawSearchTokens splits a History search into its terms on unquoted whitespace,
// keeping the quotes so a term can be moved in and out of the box unchanged.
function rawSearchTokens(input) {
  const tokens = [];
  let current = '';
  let quoted = false;
  for (const ch of String(input == null ? '' : input)) {
    if (ch === '"') { quoted = !quoted; current += ch; continue; }
    if (!quoted && /\s/.test(ch)) {
      if (current) { tokens.push(current); current = ''; }
      continue;
    }
    current += ch;
  }
  if (current) tokens.push(current);
  return tokens;
}

function parseRawToken(raw) {
  const canExclude = !raw.startsWith('"');
  const hadQuote = raw.includes('"');
  const text = raw.replaceAll('"', '');
  if (canExclude && text.startsWith('-') && (text.length > 1 || hadQuote)) {
    const term = text.slice(1).trim().toLowerCase();
    return term ? { kind: 'exclude', term } : null;
  }
  if (!text.trim()) return null;
  return { kind: 'positive', text };
}

// splitSearchTerms mirrors internal/store/flows_search_terms.go: a token prefixed
// with unquoted "-" excludes matching flows; a token beginning with a quote
// (such as `"-admin"`) stays literal positive text. Quotes group spaces with or
// without the "-" prefix. Only the server decides which rows match; this copy labels
// the exclusion chips and edits the box, so keep the two rules in step.
export function splitSearchTerms(input) {
  const positive = [];
  const exclude = [];
  for (const raw of rawSearchTokens(input)) {
    const tok = parseRawToken(raw);
    if (!tok) continue;
    if (tok.kind === 'exclude') {
      if (!exclude.includes(tok.term)) exclude.push(tok.term);
    } else {
      positive.push(tok.text);
    }
  }
  return { positive: positive.join(' '), exclude };
}

// hasEffectiveSearch reports whether a search filter has a query, exclusion,
// or saved script name (ignoring whitespace-only inputs or empty -"").
export function hasEffectiveSearch(f) {
  if (!f || !f.search) return false;
  if (f.searchScope === 'script') return String(f.search).trim().length > 0;
  const { positive, exclude } = splitSearchTerms(f.search);
  return Boolean(positive || exclude.length);
}

// formatSearchLiteral wraps values starting with "-" (other than a bare "-")
// in quotes so context menu and selection actions run positive searches.
export function formatSearchLiteral(value) {
  const s = String(value == null ? '' : value);
  const trimmed = s.trim();
  if (!trimmed) return '';
  if (trimmed.startsWith('-') && trimmed.length > 1) return '"' + trimmed + '"';
  return s;
}

// withoutSearchTerm removes the index-th exclusion (every copy of that term) and
// leaves the rest of the box exactly as typed.
export function withoutSearchTerm(input, index) {
  const { exclude } = splitSearchTerms(input);
  const target = exclude[index];
  if (target == null) return String(input == null ? '' : input);
  return rawSearchTokens(input).filter((raw) => {
    const tok = parseRawToken(raw);
    return !tok || tok.kind !== 'exclude' || tok.term !== target;
  }).join(' ');
}

// onlyExcludedTerms keeps the exclusions of a search and drops the positive query,
// so clearing the search chip does not also un-hide the noise the operator excluded.
export function onlyExcludedTerms(input) {
  return rawSearchTokens(input).filter((raw) => {
    const tok = parseRawToken(raw);
    return Boolean(tok && tok.kind === 'exclude');
  }).join(' ');
}

// BROWSER_NOISE mirrors internal/store/flows_noise.go (the server applies it
// before the page limit; this copy decides live rows). Keep the two in step:
// TestBrowserNoiseRulesMatchTheUIMirror compares them.
export const BROWSER_NOISE = {
  hosts: [
    'clients2.google.com',
    'clients2.googleusercontent.com',
    'update.googleapis.com',
    'chromewebstore.googleapis.com',
    'edgedl.me.gvt1.com',
    'redirector.gvt1.com',
    'clientservices.googleapis.com',
    'chrome-variations.googleapis.com',
    'content-autofill.googleapis.com',
    'optimizationguide-pa.googleapis.com',
    'safebrowsing.googleapis.com',
    'safebrowsing.google.com',
    'sb-ssl.google.com',
    'chromecrashreports-pa.googleapis.com',
    'crash.chromium.org',
    'connectivity.gstatic.com',
  ],
  suffixes: ['.gvt1.com'],
  paths: [
    ['accounts.google.com', '/ListAccounts'],
    ['www.google.com', '/async/folae'],
    ['www.googleapis.com', '/chromewebstore/'],
  ],
};

// isBrowserNoise reports whether a flow is the browser's own background traffic.
export function isBrowserNoise(host, path) {
  const h = String(host || '').toLowerCase();
  const p = String(path || '');
  return BROWSER_NOISE.hosts.includes(h)
    || BROWSER_NOISE.suffixes.some((s) => h.endsWith(s))
    || BROWSER_NOISE.paths.some(([ph, pp]) => h === ph && p.startsWith(pp));
}

export function emptyStateModel(st, { proxyAddr = '' } = {}) {
  const n = activeFilterCount(st);
  if (n > 0) {
    return { kind: 'empty-filtered', title: `No flows match ${n} filter${n === 1 ? '' : 's'}`, hint: 'Clear the filters to see everything captured so far.', count: n };
  }
  return {
    kind: 'empty-first',
    title: 'Point your browser or device at the proxy',
    hint: proxyAddr ? `Set the HTTP and HTTPS proxy to ${proxyAddr}. Requests appear here as they arrive.` : 'Requests appear here as they arrive.',
    count: 0,
  };
}

export function attachedLabel(findings) {
  const list = findings || [];
  if (!list.length) return '';
  return `Attached to finding${list.length === 1 ? '' : 's'} ${list.map((f) => '#' + f.id).join(', ')}`;
}

// bulkVerbs lists the bulk-bar verbs and whether each is enabled for a selection
// of `n` flows, with the reason shown when it is not.
export function bulkVerbs(n) {
  const any = n > 0;
  return [
    { id: 'repeater', label: 'Repeater', enabled: any, reason: 'Select at least one flow' },
    { id: 'intruder', label: 'Intruder', enabled: n === 1, reason: 'Intruder takes exactly one flow' },
    { id: 'tag', label: 'Tag', enabled: any, reason: 'Select at least one flow' },
    { id: 'finding', label: 'Add to finding', enabled: any, reason: 'Select at least one flow' },
    { id: 'copyas', label: 'Copy as', enabled: any, reason: 'Select at least one flow' },
    { id: 'diff', label: 'Diff', enabled: n === 2, reason: 'Select exactly two flows to diff' },
    { id: 'render', label: 'Render image', enabled: any, reason: 'Select at least one flow' },
    { id: 'delete', label: 'Delete', enabled: any, reason: 'Select at least one flow' },
  ];
}

// middleEllipsis shortens a long path to `max` characters by eliding the middle,
// keeping the start (route) and the end (resource name) visible on phone cards.
export function middleEllipsis(text, max = 44) {
  const t = String(text == null ? '' : text);
  if (max < 5 || t.length <= max) return t;
  const keep = max - 1;
  const head = Math.ceil(keep * 0.6);
  return t.slice(0, head) + '\u2026' + t.slice(t.length - (keep - head));
}

// methodChoices: the Method filter's options. The common verbs are always
// offered (a filter you can only narrow to what the loaded pages happened to
// contain hides POST until a POST scrolls in); any other method seen is added.
export const COMMON_METHODS = ['GET', 'POST', 'PUT', 'PATCH', 'DELETE', 'HEAD', 'OPTIONS'];
const METHOD_ORDER = [...COMMON_METHODS, 'CONNECT', 'TRACE'];
export function methodChoices(seen = [], current = '') {
  const all = new Set([...COMMON_METHODS, ...seen, current].filter(Boolean));
  const rank = (m) => { const i = METHOD_ORDER.indexOf(m); return i < 0 ? 99 : i; };
  return [...all].sort((a, b) => rank(a) - rank(b) || a.localeCompare(b));
}
