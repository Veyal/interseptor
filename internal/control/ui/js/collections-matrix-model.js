// collections-matrix-model.js — pure helpers for the Collections identity
// matrix, OpenAPI coverage and timing views (internal/collmatrix). No DOM or
// browser API; node-tested in _js-tests/collections-matrix-model.test.mjs.
// Server strings reach the DOM renderer untouched; it is the renderer's job
// to use textContent/esc(), never this module's.

// ---- identity matrix --------------------------------------------------------

// cellVerdict returns a short glyph+word for one matrix cell, in the same
// spirit as the authz-matrix evidence render: priority blocked/error, then the
// flag (a hypothesis), then same-as-baseline, then "differs".
export function cellVerdict(cell) {
  if (!cell || cell.class === 'not_run') return { glyph: '·', word: 'not run' };
  if (cell.class === 'blocked') return { glyph: '!', word: 'blocked' };
  if (cell.class === 'error') return { glyph: '!', word: 'error' };
  if (cell.flag === 'violation') return { glyph: 'V', word: 'unexpected access' };
  if (cell.flag === 'unexpected_denial') return { glyph: '?', word: 'unexpected denial' };
  if (cell.sameAsBaseline) return { glyph: '=', word: 'same as baseline' };
  return { glyph: '~', word: 'differs' };
}

export const CLASS_LABEL = {
  success: 'success', auth_failure: 'auth failure', authz_failure: 'authz failure',
  validation_failure: 'validation', other: 'other', error: 'error', blocked: 'blocked', not_run: 'not run',
};

export function classLabel(c) { return CLASS_LABEL[c] || c || ''; }

// matrixCaption summarises a matrix for the sheet header / alt text.
export function matrixCaption(m) {
  if (!m) return '';
  const s = m.summary || {};
  const bits = [`${s.rows || 0} request${s.rows === 1 ? '' : 's'}`, `${s.identities || 0} identit${s.identities === 1 ? 'y' : 'ies'}`];
  if (s.violations) bits.push(`${s.violations} unexpected access`);
  if (s.unexpectedDenials) bits.push(`${s.unexpectedDenials} unexpected denial${s.unexpectedDenials === 1 ? '' : 's'}`);
  if (s.blocked) bits.push(`${s.blocked} blocked`);
  if (s.errors) bits.push(`${s.errors} errored`);
  return bits.join(' · ');
}

// flaggedRowCount: rows with at least one flagged cell.
export function flaggedRowCount(m) {
  return (m?.rows || []).filter((r) => (r.cells || []).some((c) => c.flag)).length;
}

// sortIdentityCommand builds the registerCommand entries for picking an
// identity from a known list (used by the run sheet's quick-pick).
export function identityChoices(known, skipped) {
  const skip = new Set(skipped || []);
  return (known || []).filter((n) => !skip.has(n));
}

// MATRIX_MAX_IDENTITIES mirrors collmatrix.MaxIdentities: the server truncates
// beyond it, so the run plan must count the same.
export const MATRIX_MAX_IDENTITIES = 12;

// matrixRunIdentities lists the identities the server will actually send as:
// every saved identity that is not broken and carries headers, then
// anonymous (see collmatrix resolveIdentities). The run plan multiplies the
// collection by this list, so it has to match what the server does.
export function matrixRunIdentities(saved) {
  const out = [];
  const seen = new Set();
  for (const id of Array.isArray(saved) ? saved : []) {
    const name = String((id && id.name) || '').trim();
    const key = name.toLowerCase();
    if (!name || seen.has(key) || key === 'anonymous') continue;
    if (id.broken || !String(id.headers || '').trim()) continue;
    seen.add(key);
    out.push(name);
  }
  out.push('anonymous');
  return out.slice(0, MATRIX_MAX_IDENTITIES);
}

// ---- coverage ----------------------------------------------------------------

export const COVERAGE_RANK = { untested: 0, blocked: 1, failing: 2, passing: 3 };
export const COVERAGE_LABEL = { untested: 'Untested', blocked: 'Blocked', failing: 'Failing', passing: 'Passing' };

export function coverageLabel(state) { return COVERAGE_LABEL[state] || state; }

export function coverageSummaryText(rep) {
  if (!rep) return '';
  const pct = typeof rep.percent === 'number' ? rep.percent : 0;
  let s = `${rep.exercised || 0} of ${rep.total || 0} operations exercised (${pct}%)`;
  if (rep.nonSpecItems) s += ` · ${rep.nonSpecItems} request${rep.nonSpecItems === 1 ? '' : 's'} not from the spec`;
  if (rep.runsFolded) s += ` · across ${rep.runsFolded} run${rep.runsFolded === 1 ? '' : 's'}`;
  return s;
}

// groupBar: a [0,1] fraction for a simple bar-chart cell, clamped.
export function groupFraction(g) {
  if (!g || !g.total) return 0;
  return Math.max(0, Math.min(1, g.exercised / g.total));
}

// filterOperations: by state (falsy = all) and a case-insensitive text match
// over path/operationId/item name.
export function filterOperations(ops, state, q) {
  const needle = (q || '').trim().toLowerCase();
  return (ops || []).filter((o) => {
    if (state && o.state !== state) return false;
    if (!needle) return true;
    return [o.path, o.operationId, o.itemName, o.key].some((s) => (s || '').toLowerCase().includes(needle));
  });
}

// ---- timing ------------------------------------------------------------------

export function fmtMs(ms) {
  if (ms == null) return '';
  if (ms < 1000) return Math.round(ms) + ' ms';
  return (ms / 1000).toFixed(ms < 10000 ? 2 : 1) + ' s';
}

// phaseBar turns TimingReport.phases into {name,pct,ms} rows with a
// rounding-safe total (adds any missing remainder to the largest phase so the
// bar always visually sums to 100%).
export function phaseBars(phases) {
  const rows = (phases || []).map((p) => ({ name: p.name, ms: p.ms, pct: p.percent || 0 }));
  const sum = rows.reduce((a, r) => a + r.pct, 0);
  if (rows.length && Math.abs(sum - 100) > 1e-6) {
    const biggest = rows.reduce((a, r) => (r.pct > a.pct ? r : a), rows[0]);
    biggest.pct += 100 - sum;
  }
  return rows;
}

export function timingSummaryText(tr) {
  if (!tr) return '';
  return `${tr.requests || 0} requests · wall ${fmtMs(tr.wallMs)} · requests ${fmtMs(tr.requestMs)} · tests ${fmtMs(tr.testMs)} · other ${fmtMs(tr.otherMs)}`;
}

// outlierText: one line per outlier, slowest first.
export function sortedOutliers(outliers) {
  return (outliers || []).slice().sort((a, b) => b.durationMs - a.durationMs);
}

// ---- example diff --------------------------------------------------------------

export function diffHasChanges(d) { return !!d && !d.equal; }

export function diffSummaryText(d) {
  if (!d) return '';
  if (d.equal) return 'Matches the saved example.';
  const bits = [];
  if (d.status?.changed) bits.push(`status ${d.status.expected} → ${d.status.actual}`);
  if (d.headers?.length) bits.push(`${d.headers.length} header change${d.headers.length === 1 ? '' : 's'}`);
  if (d.json?.length) bits.push(`${d.json.length} field change${d.json.length === 1 ? '' : 's'}`);
  else if (d.bodyChanged) bits.push('body changed');
  if (d.changesCapped) bits.push('more changes not shown');
  if (d.truncated) bits.push('body truncated for the diff');
  return bits.join(' · ') || 'Response differs.';
}

// ---- intruder handoff ----------------------------------------------------------

export function handoffSummaryText(h) {
  if (!h) return '';
  const n = (h.positions || []).length;
  return `${h.attackType || 'sniper'} · ${n} position${n === 1 ? '' : 's'} (${(h.positions || []).join(', ')})`;
}
