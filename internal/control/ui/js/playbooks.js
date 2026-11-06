// playbooks.js — the Playbooks panel: browse the bundled corpus and the project's
// own additions, read one, see what it covered, and copy the MCP invocation.
//
// The panel never runs a check itself: the markdown is a test guide an AI harness
// executes over MCP with the ordinary tools. What this view adds is discovery
// (what applies to this host), trust (bundled vs project content) and the
// coverage ledger the report reads.
import { api, $, esc, toast, toastError, copyText } from './core.js';
import {
  playbookRowModel, filterPlaybooks, coverageModel, mcpPrompt, getPlaybookCall, markdownSections,
} from './playbook-view-model.js';

let all = [];
let coverage = new Map();
let selected = '';
let loaded = false;

function renderList() {
  const host = $('#pbList');
  if (!host) return;
  const rows = filterPlaybooks(all, $('#pbFilter') ? $('#pbFilter').value : '');
  $('#pbCount').textContent = rows.length + ' of ' + all.length;
  if (!rows.length) {
    host.innerHTML = '<div class="hint u-p-3">No playbook matches that filter.</div>';
    return;
  }
  host.textContent = '';
  for (const raw of rows) {
    const p = playbookRowModel(raw);
    const btn = document.createElement('button');
    btn.type = 'button';
    btn.className = 'pb-row' + (p.id === selected ? ' sel' : '');
    btn.dataset.id = p.id;
    btn.setAttribute('aria-pressed', p.id === selected ? 'true' : 'false');
    const cov = coverageModel(coverage.get(p.id));
    btn.innerHTML =
      '<span class="pb-row-title">' + esc(p.title) + '</span>' +
      '<span class="pb-row-sub">' +
        '<span class="pb-badge' + (p.untrusted ? ' pb-badge-untrusted' : '') + '">' + esc(p.trust) + '</span>' +
        (p.severity ? '<span class="pb-sev">' + esc(p.severity) + '</span>' : '') +
        '<span class="hint">' + esc(cov.text) + '</span>' +
      '</span>' +
      (p.invalid ? '<span class="hint u-danger">' + esc(p.invalid) + '</span>' : '');
    btn.addEventListener('click', () => openPlaybook(p.id));
    host.appendChild(btn);
  }
}

async function openPlaybook(id) {
  selected = id;
  renderList();
  const host = $('#pbDetail');
  if (!host) return;
  host.innerHTML = '<div class="hint u-p-3">Loading…</div>';
  let d;
  try {
    d = await api('/api/playbooks/' + encodeURIComponent(id));
  } catch (e) {
    host.innerHTML = '<div class="hint u-danger u-p-3">' + esc(e.message || 'load failed') + '</div>';
    return;
  }
  const p = playbookRowModel(d.playbook || {});
  host.textContent = '';

  const head = document.createElement('div');
  head.className = 'pb-head';
  head.innerHTML =
    '<div class="pb-head-title">' + esc(p.title) + '</div>' +
    '<div class="pb-head-meta">' +
      '<span class="pb-badge' + (p.untrusted ? ' pb-badge-untrusted' : '') + '">' + esc(p.trust) + '</span>' +
      (p.severity ? '<span class="pb-sev">' + esc(p.severity) + '</span>' : '') +
      '<span class="hint">' + esc(coverageModel(coverage.get(p.id)).text) + '</span>' +
    '</div>';
  const actions = document.createElement('div');
  actions.className = 'row u-gap-2 u-mt-2 u-wrap';
  const copyPrompt = document.createElement('button');
  copyPrompt.type = 'button';
  copyPrompt.className = 'btn btn-sm';
  copyPrompt.textContent = 'Copy MCP prompt';
  copyPrompt.addEventListener('click', () => copyText(mcpPrompt(p.id), 'prompt copied'));
  const copyTool = document.createElement('button');
  copyTool.type = 'button';
  copyTool.className = 'btn btn-sm';
  copyTool.textContent = 'Copy get_playbook call';
  copyTool.addEventListener('click', () => copyText(getPlaybookCall(p.id), 'tool call copied'));
  actions.append(copyPrompt, copyTool);
  head.appendChild(actions);
  host.appendChild(head);

  const doc = document.createElement('div');
  doc.className = 'pb-doc';
  for (const block of markdownSections(d.markdown || '')) {
    const el = document.createElement(block.kind === 'list' ? 'div' : block.kind === 'code' ? 'pre' : block.kind);
    el.textContent = (block.kind === 'list' ? '• ' : '') + block.text;
    doc.appendChild(el);
  }
  host.appendChild(doc);
}

async function refreshCoverage() {
  try {
    const d = await api('/api/playbooks/coverage');
    coverage = new Map((d.summary || []).map((c) => [c.playbookId, c]));
  } catch (e) {
    coverage = new Map();
  }
}

export async function loadPlaybooks() {
  const host = $('#pbList');
  if (!host) return;
  if (!loaded) host.innerHTML = '<div class="hint u-p-3">Loading playbooks…</div>';
  try {
    const [d] = await Promise.all([api('/api/playbooks'), refreshCoverage()]);
    all = d.playbooks || [];
    loaded = true;
    const trust = $('#pbTrust');
    if (trust) {
      trust.textContent = d.untrusted
        ? d.count + ' playbooks · ' + d.untrusted + ' project (untrusted content)'
        : d.count + ' bundled playbooks';
    }
    renderList();
    if (!selected && all.length) void openPlaybook(all[0].id);
    else if (selected) renderList();
  } catch (e) {
    host.innerHTML = '';
    toastError('Could not load playbooks', e);
  }
}

export function initPlaybooks() {
  const filter = $('#pbFilter');
  if (filter) filter.addEventListener('input', renderList);
}
