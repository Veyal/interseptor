// desktop-panel-scanner.js — the Scanner panel's desktop layout (docs/desktop-design.md,
// Panels). Runs only inside a desktop host (html.desktop-app); scanner.js calls it
// behind that check, so a browser tab keeps the web markup. It:
//   - rearranges the toolbar into one row: target, path filter, Run (Stop takes its
//     place while a scan runs, with the progress bar inline), muted status, Clear, Tools;
//   - makes the Tools menu the panel's overflow menu (the ellipsis "More actions" button) and folds the
//     scope line's "Open target scope" action into it, every item with an icon;
//   - joins the host filter and the severity filters into one filter bar;
//   - renders an issue once (description, remediation) with its affected targets as
//     one compact row each; evidence and per-target notes expand on demand.
// Element ids, roles and the data-act / data-scan-more hooks are unchanged, so every
// existing handler in scanner.js keeps working.
import { esc, escAttr, icon } from './core.js';
import { splitScanTarget } from './scanner-model.js';
import { isDesktop, iconNode, observe } from './desktop-panel-kit.js';
import { stripGlyph } from './desktop-panel-tools-model.js';

export const isDesktopSkin = isDesktop;

let inited = false;
const q = (s) => document.querySelector(s);

export function initScannerDesktop() {
  if (inited || !isDesktopSkin()) return;
  const bar = q('#scanPassiveBar');
  if (!bar) return;
  inited = true;
  const run = q('#scanRun');
  const progress = q('#scanProgress');
  if (run && progress) run.after(progress);
  // The scope sentence is global state (the status bar carries it); its action lives in Tools.
  const link = q('#scanScopeLink');
  const menu = q('#scanToolsMenu');
  if (link && menu) { link.classList.remove('xs'); link.classList.add('scan-scope-action'); menu.prepend(link); }
  // One overflow vocabulary: the Tools disclosure is the ellipsis More actions menu.
  const tools = q('#scanToolsBtn');
  if (tools) {
    tools.textContent = '';
    tools.append(iconNode('more'));
    tools.classList.add('dsk-more');
    tools.setAttribute('aria-label', 'More actions');
    tools.setAttribute('data-tooltip', 'Checks, codecs, decoder, OOB, target scope');
  }
  // Every item in the menu carries an icon.
  [['#scanScopeLink', 'scope'], ['#oobBtn', 'oob']].forEach(([sel, name]) => {
    const b = q(sel);
    if (b) observe(b, () => { if (!b.querySelector('.icon')) b.prepend(iconNode(name)); }, { childList: true });
  });
  // "Run scan ▸" is plain "Run scan"; scanner.js re-writes the label per state.
  if (run) observe(run, () => { const t = stripGlyph(run.textContent); if (!run.querySelector('*') && t !== run.textContent) run.textContent = t; });
  const hostRow = q('#scanHostRow');
  const chips = q('#scanSevChips');
  if (hostRow && chips && !hostRow.parentElement.classList.contains('scan-filterbar')) {
    const fb = document.createElement('div');
    fb.className = 'scan-filterbar';
    hostRow.before(fb);
    fb.append(hostRow, chips);
  }
  // Per-target expand, delegated once: evidence and notes stay out of the way.
  const detail = q('#scanDetail');
  if (detail) detail.addEventListener('click', (e) => {
    const t = e.target.closest && e.target.closest('[data-scan-toggle]');
    if (!t) return;
    const body = document.getElementById(t.getAttribute('aria-controls') || '');
    if (!body) return;
    const open = t.getAttribute('aria-expanded') !== 'true';
    t.setAttribute('aria-expanded', open ? 'true' : 'false');
    body.hidden = !open;
    t.closest('.scan-tgt').classList.toggle('open', open);
  });
}

// One affected target: [toggle] METHOD host/path  #flow   (actions on hover or focus).
export function scanTargetRowDesktop(i, n, firstDetail) {
  const { method, host, path } = splitScanTarget(i.target);
  const name = i.target || 'this target';
  const note = i.detail && i.detail !== firstDetail ? i.detail : '';
  const hasBody = !!(note || i.evidence);
  const bodyId = 'scanTgtBody-' + n;
  const toggle = hasBody
    ? `<button type="button" class="scan-tgt-toggle" data-scan-toggle aria-expanded="false" aria-controls="${bodyId}" aria-label="Evidence for ${escAttr(name)}" title="Show evidence">${icon('chevron')}</button>`
    : '<span class="scan-tgt-toggle-gap" aria-hidden="true"></span>';
  const actions = i.flowId ? `<div class="scan-tgt-actions" role="group" aria-label="Actions for ${escAttr(name)}">
      <button type="button" class="btn xs" data-act="open" data-n="${n}" aria-label="Open flow #${i.flowId}" title="Open flow #${i.flowId}">${icon('link')}</button>
      <button type="button" class="btn xs" data-act="verify" data-n="${n}" aria-label="Verify in Repeater" title="Verify in Repeater">${icon('repeater')}</button>
      <button type="button" class="btn xs" data-act="attach" data-n="${n}" aria-label="Attach as evidence" title="Attach as evidence">${icon('attach')}</button></div>` : '';
  const body = hasBody ? `<div class="scan-tgt-body" id="${bodyId}" hidden>${note ? `<p class="scan-tgt-detail">${esc(note)}</p>` : ''}${i.evidence ? `<div class="evidence scan-tgt-evidence">${esc(i.evidence)}</div>` : ''}</div>` : '';
  return `<div class="scan-tgt"><div class="scan-tgt-row">${toggle}
    <span class="scan-tgt-m">${esc(method)}</span>
    <span class="scan-tgt-url" title="${escAttr(name)}">${esc(host || '(no target)')}<span class="scan-tgt-path">${esc(path)}</span></span>
    ${i.flowId ? `<span class="scan-tgt-flow">#${i.flowId}</span>` : ''}${actions}</div>${body}</div>`;
}

// The issue header and the sections above the target list. sev is the severity
// chip markup (dot plus word on desktop), meta the "10 targets · 2 hosts" text.
export function scanDetailDesktopHTML({ g, first, sev, meta, rows, more }) {
  return `<div class="scan-wrap">
    <div class="scan-head">
      <h1 class="scan-title" tabindex="-1" data-split-focus>${esc(g.title)}</h1>
      <button class="btn findings-write" id="scanPromote" title="Create a curated finding from this issue: title, detail, fix, and every PoC flow attached">${icon('plus')} Promote to Finding</button>
    </div>
    <div class="scan-meta">${sev}<span class="scan-meta-n">${esc(meta)}</span></div>
    ${first.detail ? `<p class="scan-detail-text">${esc(first.detail)}</p>` : ''}
    ${first.fix ? `<h2 class="scan-sec">Remediation</h2><div class="fixbox">${esc(first.fix)}</div>` : ''}
    <h2 class="scan-sec">Affected targets <span class="scan-sec-n">${g.items.length}</span></h2>
    ${rows}${more}</div>`;
}
