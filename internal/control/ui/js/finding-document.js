// finding-document.js — the Findings reader as one scrolling document.
//
// The old workspace split a finding across four tab panels. This module holds the
// pure parts of its replacement: route-to-anchor mapping, the reproduction
// timeline model, per-target evidence, the stub state, and the read-only markup
// for the claim / impact / affected / properties sections.
//
// No imports and no DOM access: the file runs under `node --test`. Anything that
// needs the page (markdown, image copy buttons, fetching raw flows) is passed in
// through `ctx` by findings.js.

const esc = (s) => String(s == null ? '' : s).replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[c]);

// Where each anchor lives in the document.
export const ANCHOR_IDS = { claim: 'find-sec-summary', reproduction: 'find-sec-poc', 'report-prep': 'findReportPrep' };

// The old tab vocabulary stays valid as a route and gap identifier. Each one
// is now an anchor; remediation and review open the Report prep fold.
const SECTION_ANCHORS = {
  overview: { anchor: 'claim', openPrep: false },
  evidence: { anchor: 'reproduction', openPrep: false },
  remediation: { anchor: 'report-prep', openPrep: true },
  review: { anchor: 'report-prep', openPrep: true },
};

// findingRouteTarget maps a parseFindingRoute result ({section, flowId}) to the
// anchor to scroll to and whether Report prep must open. Unknown input is null.
export function findingRouteTarget(route) {
  if (!route) return null;
  const target = SECTION_ANCHORS[route.section];
  if (!target) return null;
  return { anchor: target.anchor, openPrep: target.openPrep, flowId: route.flowId == null ? null : route.flowId };
}

export const stepElementId = (n) => 'find-step-' + n;

const ROLE_LABELS = { setup: 'SETUP', action: 'ACTION', result: 'RESULT', control: 'CONTROL', baseline: 'BASELINE', retest: 'RETEST', context: 'CONTEXT', observation: 'NOTE' };
const roleLabel = (role) => ROLE_LABELS[role] || (role ? String(role).toUpperCase() : 'STEP');

const proofOf = (b) => String((b && (b.proof || b.note || b.caption)) || '').trim();

// buildReproductionSteps turns the ordered body blocks into timeline steps.
// A text block opens a step. A flow or screenshot attaches to the step before
// it, so evidence sits directly under the prose it proves. Evidence with no step
// before it, or one that plays a different role than a step that already has
// evidence, opens its own step and uses its proof text as the prose.
export function buildReproductionSteps(blocks) {
  const steps = [];
  let cur = null;
  const open = (role, md, derived) => {
    cur = { n: steps.length + 1, role: role || '', md, derived: !!derived, evidence: [] };
    steps.push(cur);
    return cur;
  };
  for (const b of blocks || []) {
    if (!b) continue;
    if (b.type === 'text') {
      const md = String(b.md || '').trim();
      if (!md) continue;
      open(b.role, md, false);
    } else if (b.type === 'flow' || b.type === 'image') {
      if (b.type === 'flow' && (b.missing || !b.flowId)) continue;
      const role = b.role || '';
      if (!cur || (role && cur.role && role !== cur.role && cur.evidence.length)) open(role, proofOf(b), true);
      else if (!cur.role && role) cur.role = role;
      cur.evidence.push(b);
    }
  }
  return steps;
}

const stepFlowIds = (step) => step.evidence.filter((b) => b.type === 'flow').map((b) => Number(b.flowId));
const stepHashes = (step) => step.evidence.filter((b) => b.type === 'image' && b.hash).map((b) => b.hash);

// timelineHTML draws the vertical timeline. ctx.evidenceHTML(block, step) draws
// one attached flow or screenshot; ctx.renderMD draws the prose.
export function timelineHTML(steps, ctx = {}) {
  if (!steps || !steps.length) return '';
  const md = ctx.renderMD || esc;
  const evidenceHTML = ctx.evidenceHTML || (() => '');
  const items = steps.map((s) => {
    const flows = stepFlowIds(s).join(' ');
    const hashes = stepHashes(s).join(' ');
    const ev = s.evidence.map((b, i) => `<div class="find-step-ev">${evidenceHTML(b, s, { skipProof: s.derived && i === 0 })}</div>`).join('');
    return `<li class="find-step${s.role === 'result' || s.role === 'action' ? ' is-key' : ''}" id="${stepElementId(s.n)}" tabindex="-1" data-step-flows="${esc(flows)}" data-step-images="${esc(hashes)}">
      <span class="find-node" aria-hidden="true">${s.n}</span>
      <div class="find-sbody">
        <div class="find-sline"><span class="find-role find-role-${esc(s.role || 'step')}">${esc(roleLabel(s.role))}</span><div class="find-stext md">${s.md ? md(s.md) : ''}</div></div>
        ${ev}
      </div></li>`;
  }).join('');
  return `<ol class="find-tl" aria-label="Reproduction steps">${items}</ol>`;
}

// affectedRows is the per-target view: which evidence proves each target, and
// which targets have none. Server-owned readiness gaps also mark a target.
export function affectedRows(f, steps) {
  const stepForFlow = new Map();
  const stepForHash = new Map();
  for (const s of steps || []) {
    for (const id of stepFlowIds(s)) if (!stepForFlow.has(id)) stepForFlow.set(id, s.n);
    for (const h of stepHashes(s)) if (!stepForHash.has(h)) stepForHash.set(h, s.n);
  }
  const gaps = new Set((f && f.readiness && f.readiness.targetEvidenceGaps) || []);
  return ((f && f.targets) || []).map((t, i) => {
    const missing = new Set((t.missingFlowIds || []).map(Number));
    const chips = [
      ...(t.flow_ids || []).map((id) => ({ kind: 'flow', flowId: Number(id), step: stepForFlow.get(Number(id)) ?? null, missing: missing.has(Number(id)) })),
      ...(t.image_hashes || []).map((hash) => ({ kind: 'image', hash, step: stepForHash.get(hash) ?? null, missing: false })),
    ];
    const proven = chips.length > 0 || !!t.evidenceException;
    return {
      i: i + 1,
      url: t.url || '',
      method: (t.methods && t.methods.length ? t.methods : (t.method ? [t.method] : [])).filter(Boolean).join(', '),
      relation: [t.relation, t.role].filter(Boolean).join(' · '),
      sub: [t.variant, t.note].filter(Boolean).join(' · '),
      chips,
      exception: t.evidenceException || '',
      needs: !proven || gaps.has(i),
    };
  });
}

function affectedHTML(f, steps) {
  const rows = affectedRows(f, steps);
  if (rows.length < 2) return '';
  const chip = (c) => {
    if (c.missing) return `<span class="find-evref is-missing">#${esc(c.flowId)} missing</span>`;
    if (c.kind === 'image') return c.step ? `<a class="find-evref" href="#${stepElementId(c.step)}" data-evref-step="${c.step}">shot</a>` : '<span class="find-evref">shot</span>';
    const href = c.step ? `#${stepElementId(c.step)}` : `#finding-${esc(f.id)}/flow-${esc(c.flowId)}`;
    return `<a class="find-evref" href="${href}" data-evref-flow="${esc(c.flowId)}"${c.step ? ` data-evref-step="${c.step}"` : ''} aria-label="Evidence flow ${esc(c.flowId)}${c.step ? `, step ${c.step}` : ''}">#${esc(c.flowId)}</a>`;
  };
  const items = rows.map((r) => `<li class="find-aff-row${r.needs ? ' is-gap' : ''}">
      <span class="find-aff-i">${r.i}</span><span class="find-aff-m">${esc(r.method)}</span><span class="find-aff-u" title="${esc(r.url)}">${esc(r.url)}</span><span class="find-aff-r">${esc(r.relation)}</span>
      <span class="find-aff-e">${r.chips.map(chip).join('')}${r.needs ? '<span class="find-need">needs evidence</span>' : ''}</span>
      ${r.sub || r.exception ? `<span class="find-aff-sub">${esc(r.sub)}${r.sub && r.exception ? ' · ' : ''}${r.exception ? 'Exception: ' + esc(r.exception) : ''}</span>` : ''}
    </li>`).join('');
  return `<section class="find-sec" id="find-sec-target" tabindex="-1" aria-label="Affected targets"><div class="find-sec-hd"><h3>Affected</h3><span class="find-count">${rows.length}</span></div><ul class="find-aff">${items}</ul></section>`;
}

// findingStub is non-null when a finding has neither a claim nor steps: the agent
// that opened it has not written it yet.
export function findingStub(f) {
  const hasClaim = !!String((f && f.summary) || '').trim();
  const hasSteps = buildReproductionSteps((f && f.blocks) || []).length > 0;
  if (hasClaim || hasSteps) return null;
  const source = (f && f.source) || '';
  const author = f && (f.authoredBy || f.agent);
  const who = author ? String(author) : source === 'ai' ? 'The authoring agent' : source === 'scanner' ? 'The scanner' : 'The author';
  return { who, agent: source === 'ai' || source === 'scanner' || !!author, missing: ['claim', 'reproduction steps'] };
}

function stubHTML(stub) {
  return `<div class="find-stub" role="status"><svg class="icon" aria-hidden="true" focusable="false"><use href="#i-agent"/></svg><p><b>${esc(stub.who)}</b> has not written the ${stub.missing.map(esc).join(' or ')} yet. This finding is a stub until it does.</p></div>`;
}

const SEV_CLASS = { critical: 'find-sv-critical', high: 'find-sv-high', medium: 'find-sv-medium', low: 'find-sv-low', info: 'find-sv-info' };
export const severityTone = (s) => SEV_CLASS[String(s || '').toLowerCase()] || 'find-sv-info';

// readDocumentHTML is the read-mode document body: claim, impact (+ root cause),
// affected targets, reproduction. Each section renders only when it has content.
// The reproduction host is filled by findings.js (renderFindReportBody) because
// its evidence needs the page.
export function readDocumentHTML(f, ctx = {}) {
  const md = ctx.renderMD || esc;
  const steps = buildReproductionSteps(f.blocks || []);
  const stub = findingStub(f);
  const parts = [];
  if (stub) parts.push(stubHTML(stub));
  else if (String(f.summary || '').trim()) parts.push(`<section class="find-claim" id="find-sec-summary" tabindex="-1" aria-label="Claim"><p class="find-claim-lead">${esc(f.summary)}</p></section>`);
  if (String(f.impact || '').trim()) {
    const why = String(f.why || '').trim() ? `<div class="find-why" id="find-sec-why"><span class="find-tag">Root cause</span><div class="md">${md(f.why)}</div></div>` : '';
    parts.push(`<section class="find-impact-block" id="find-sec-impact"><div class="find-impact ${severityTone(f.severity)}"><span class="find-tag find-impact-tag">IMPACT</span><div class="md">${md(f.impact)}</div></div>${why}</section>`);
  } else if (String(f.why || '').trim()) {
    parts.push(`<section class="find-impact-block" id="find-sec-impact"><div class="find-why" id="find-sec-why"><span class="find-tag">Root cause</span><div class="md">${md(f.why)}</div></div></section>`);
  }
  parts.push(affectedHTML(f, steps));
  if (steps.length) parts.push(`<section class="find-sec" id="find-sec-poc" tabindex="-1" aria-label="Reproduction"><div class="find-sec-hd"><h3>Reproduction</h3><span class="find-count">${steps.length}</span></div><div class="find-tl-host" id="findBody"></div></section>`);
  return parts.filter(Boolean).join('\n');
}

const cvssFill = (score) => Math.max(0, Math.min(8, Math.round((Number(score) || 0) / 10 * 8)));

// readPropertiesHTML is the read-only property list: plain values, no controls.
// Unset optional properties are omitted instead of rendered as "Not set".
export function readPropertiesHTML(f) {
  const rows = [];
  const row = (label, value) => rows.push(`<div class="find-prop"><dt>${label}</dt><dd>${value}</dd></div>`);
  row('Severity', `<span class="find-pval ${severityTone(f.severity)}"><span class="find-dot" aria-hidden="true"></span>${esc(f.severity)}</span>`);
  row('Status', `<span class="find-pval">${esc(String(f.status || '').replace(/_/g, ' '))}</span>`);
  if (f.confidence) row('Confidence', `<span class="find-pval">${esc(f.confidence)}</span>`);
  if (f.cwe) row('CWE', `<span class="find-pval">${esc(f.cwe)}</span>`);
  if (f.environment) row('Environment', `<span class="find-pval">${esc(f.environment)}</span>`);
  if (f.cvss || f.cvssScore != null) {
    const bar = f.cvssScore != null ? `<span class="find-cvssbar ${severityTone(f.severity)}" aria-hidden="true">${Array.from({ length: 8 }, (_, i) => `<b${i < cvssFill(f.cvssScore) ? ' class="on"' : ''}></b>`).join('')}</span>` : '';
    row('CVSS', `<span class="find-pval">${f.cvssScore != null ? esc(Number(f.cvssScore).toFixed(1)) : ''}</span>${bar}${f.cvss ? `<span class="find-vector">${esc(f.cvss)}</span>` : ''}`);
  }
  if ((f.tags || []).length) row('Tags', `<span class="find-tagrow">${f.tags.map((t) => `<span class="find-tag-chip">${esc(t)}</span>`).join('')}</span>`);
  return `<dl class="find-props">${rows.join('')}</dl>`;
}
