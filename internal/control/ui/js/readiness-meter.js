// readiness-meter.js — the one place a finding's server-owned readiness is drawn.
//
// P1 (display, never derive): the server decides the stage and the gaps
// (`f.readiness.stage`, `f.readiness.gaps`). This module only maps that stage to
// a fixed milestone ladder and words it. It never inspects finding content and
// never decides that a check passed or failed.
//
// The spec assumed `readiness.checks` lists every check with an `ok` flag. The
// real payload lists only the failing checks (`code`, `message`), so the meter
// has one segment per STAGE milestone the server can report, not one per check.
//
// No imports and no DOM access: the file runs under `node --test`.

export const STAGES = ['draft', 'evidence_attached', 'reproducible', 'report_ready'];

// Milestones after "draft". A segment is reached when the server stage is at or
// past it. `section` is the finding workspace section the segment links to.
export const MILESTONES = [
  { stage: 'evidence_attached', label: 'Evidence', section: 'evidence' },
  { stage: 'reproducible', label: 'Reproducible', section: 'evidence' },
  { stage: 'report_ready', label: 'Report ready', section: 'review' },
];

export const STAGE_LABELS = {
  draft: 'Draft', evidence_attached: 'Evidence attached', reproducible: 'Reproducible', report_ready: 'Report ready',
};

const esc = (s) => String(s == null ? '' : s).replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[c]);

export function humanizeGap(code) {
  const raw = String(code == null ? '' : code).replace(/^capability:/, 'claim: ').replace(/[_-]+/g, ' ').trim();
  return raw ? raw.charAt(0).toUpperCase() + raw.slice(1) : 'Unknown gap';
}

// stageIndex returns the number of milestones reached (0..3), or -1 when the
// readiness object is missing or carries a stage this client does not know.
export function stageIndex(readiness) {
  const stage = readiness && typeof readiness.stage === 'string' ? readiness.stage : '';
  const i = STAGES.indexOf(stage);
  return i;
}

export function readinessGaps(readiness) {
  return readiness && Array.isArray(readiness.gaps) ? readiness.gaps.filter((g) => typeof g === 'string' && g) : [];
}

// readinessSegments describes one segment per milestone with an icon name, a
// state (pass or todo) and a pattern class so colour is never the only signal.
export function readinessSegments(readiness) {
  const reached = stageIndex(readiness);
  return MILESTONES.map((m, i) => {
    const pass = reached >= i + 1;
    return { stage: m.stage, label: m.label, section: m.section, state: pass ? 'pass' : 'todo', icon: pass ? 'i-status-done' : 'i-status-todo', status: pass ? 'reached' : 'not reached' };
  });
}

// readinessValuetext is the accessible text of the meter, for example
// "1 of 3 stages reached, Evidence attached; missing: Title, CVSS vector".
export function readinessValuetext(readiness, labelFor = humanizeGap) {
  const reached = stageIndex(readiness);
  if (reached < 0) return 'Readiness unknown';
  const gaps = readinessGaps(readiness);
  let text = `${reached} of ${MILESTONES.length} stages reached, ${STAGE_LABELS[readiness.stage]}`;
  if (readiness.stage === 'report_ready') return text;
  if (gaps.length) text += '; missing: ' + gaps.map((g) => labelFor(g)).join(', ');
  return text;
}

export function readinessMeterAttrs(readiness, labelFor) {
  const reached = stageIndex(readiness);
  return { role: 'meter', 'aria-label': 'Report readiness', 'aria-valuemin': '0', 'aria-valuemax': String(MILESTONES.length), 'aria-valuenow': String(Math.max(0, reached)), 'aria-valuetext': readinessValuetext(readiness, labelFor) };
}

function iconSvg(name) {
  return `<svg class="icon" aria-hidden="true" focusable="false"><use href="#${name}"/></svg>`;
}

// readinessMeterHTML returns trusted markup (every dynamic value is escaped).
//   size:      'compact' (icons only, label text hidden visually) | 'full' (labels visible)
//   hrefFor:   (section) => href, or omitted for plain segments (list rows are links already)
//   labelFor:  gap code => label
export function readinessMeterHTML(readiness, { size = 'compact', hrefFor, labelFor = humanizeGap, id } = {}) {
  const known = stageIndex(readiness) >= 0;
  const linked = !!(hrefFor && known);
  // Children of role=meter are presentational in ARIA 1.2, so the interactive
  // variant is a labelled group (links keep their semantics) with the value
  // spoken through a screen-reader summary instead.
  const attrs = linked ? { role: 'group', 'aria-label': 'Report readiness' } : readinessMeterAttrs(readiness, labelFor);
  const attrText = Object.entries(attrs).map(([k, v]) => `${k}="${esc(v)}"`).join(' ');
  const summary = linked ? `<span class="u-sr">${esc(readinessValuetext(readiness, labelFor))}. </span>` : '';
  const segs = readinessSegments(readiness).map((s) => {
    const body = `${iconSvg(s.icon)}<span class="rm-text">${esc(s.label)}<span class="u-sr">: ${esc(s.status)}</span></span>`;
    const cls = `rm-seg is-${s.state}`;
    if (linked) return `<a class="${cls}" href="${esc(hrefFor(s.section))}" data-find-section="${esc(s.section)}">${body}</a>`;
    return `<span class="${cls}">${body}</span>`;
  }).join('');
  return `<span class="rm rm-${esc(size)}"${id ? ` id="${esc(id)}"` : ''} ${attrText}${known ? '' : ' data-unknown="true"'}>${summary}${segs}</span>`;
}
