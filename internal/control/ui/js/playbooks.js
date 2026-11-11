// playbooks.js — the Playbooks panel: browse the bundled corpus and the project's
// own additions, read one, see what it covered, and copy the MCP invocation.
//
// The panel never runs a check itself: the markdown is a test guide an AI harness
// executes over MCP with the ordinary tools. What this view adds is discovery
// (what applies to this host), trust (bundled vs project content) and the
// coverage ledger the report reads.
//
// Playbook text is content, and a project playbook is untrusted content: every
// string reaches the page through textContent, never innerHTML. The only markup
// written as HTML is icon(), a fixed sprite reference.
import { api, $, toastError, copyText, icon, projectStorageKey } from './core.js';
import { renderState } from './statepanel.js';
import { wireListbox, setListboxSelection } from './listbox.js';
import { createSplitPane } from './split.js';
import {
  playbookRowModel, filterPlaybooks, coverageModel, mcpPrompt, getPlaybookCall,
  markdownBlocks, inlineTokens, playbookFacts, severityClass,
} from './playbook-view-model.js';

let all = [];
let coverage = new Map();
let selected = '';
let shown = ''; // id whose document is in the detail pane
let loaded = false;
let pane = null;
let openSeq = 0; // newest detail request; an older response is dropped
let detailCoverage = null; // the coverage line of the open document

function el(tag, cls, text) {
  const e = document.createElement(tag);
  if (cls) e.className = cls;
  if (text != null) e.textContent = text;
  return e;
}

function severityChip(severity) {
  if (!severity) return null;
  const known = severityClass(severity);
  return el('span', known ? 'sev ' + known : 'sev Info', severity);
}

function trustBadge(p) {
  // Bundled is the default and the toolbar already says so; only content that
  // did not ship with the binary is marked, where it is read.
  return p.untrusted ? el('span', 'pb-badge pb-badge-untrusted', p.trust) : null;
}

function coverageLine(id) {
  const cov = coverageModel(coverage.get(id));
  const line = el('span', 'pb-cov', cov.text);
  line.dataset.tested = cov.tested ? 'true' : 'false';
  return line;
}

function clearListState() {
  const host = $('#pbListState');
  if (!host) return;
  host.textContent = '';
  delete host.dataset.state;
  host.removeAttribute('aria-busy');
}

function clearFilter() {
  const f = $('#pbFilter');
  if (!f) return;
  f.value = '';
  renderList();
  f.focus();
}

function renderList() {
  const host = $('#pbList');
  if (!host) return;
  const query = $('#pbFilter') ? $('#pbFilter').value : '';
  const rows = filterPlaybooks(all, query);
  const count = $('#pbCount');
  if (count) count.textContent = query.trim() ? rows.length + ' of ' + all.length : '';
  host.textContent = '';
  if (!rows.length) {
    if (all.length) renderState($('#pbListState'), 'empty-filtered', { title: 'No playbook matches', hint: 'The filter reads the id, title, severity and the signals a playbook applies to.', onClear: clearFilter, actionLabel: 'Clear filter' });
    else renderState($('#pbListState'), 'empty-first', { icon: 'book', title: 'No playbooks', hint: 'Add markdown guides under the project folder in playbooks/ to see them here.' });
    return;
  }
  clearListState();
  for (const raw of rows) {
    const p = playbookRowModel(raw);
    const row = el('div', 'pb-row');
    row.dataset.id = p.id;
    row.append(el('span', 'pb-row-title', p.title));
    const sub = el('span', 'pb-row-sub');
    const chip = severityChip(p.severity);
    const badge = trustBadge(p);
    if (chip) sub.append(chip);
    sub.append(coverageLine(p.id));
    if (badge) sub.append(badge);
    row.append(sub);
    if (p.invalid) row.append(el('span', 'pb-row-invalid', p.invalid));
    host.append(row);
  }
  wireListbox(host, host.querySelectorAll('.pb-row'), {
    label: 'Playbooks',
    selected: (r) => r.dataset.id === selected,
    activate: (r) => {
      void openPlaybook(r.dataset.id);
      if (pane && pane.mode() === 'stack') pane.showDetail(r);
    },
  });
}

function copyButton(label, cls, text, done) {
  const b = el('button', cls);
  b.type = 'button';
  b.innerHTML = icon('copy'); // fixed sprite reference
  b.append(document.createTextNode(label));
  b.addEventListener('click', () => copyText(text, done));
  return b;
}

function appendInline(parent, text) {
  const walk = (host, tokens) => {
    for (const t of tokens) {
      if (t.t === 'text') host.append(document.createTextNode(t.v));
      else if (t.t === 'code') host.append(el('code', '', t.v));
      else { const n = document.createElement(t.t === 'strong' ? 'strong' : 'em'); walk(n, t.children); host.append(n); }
    }
  };
  walk(parent, inlineTokens(text));
  return parent;
}

function listNode(block) {
  const list = document.createElement(block.kind);
  for (const item of block.items) {
    const li = appendInline(document.createElement('li'), item.text);
    for (const child of item.children) li.append(listNode(child));
    list.append(li);
  }
  return list;
}

// The panel heading is an h2 and the document title an h3, so the guide's own
// headings continue from h4.
const HEADING_TAG = { h1: 'h4', h2: 'h5', h3: 'h6' };

function blockNode(block) {
  if (block.kind === 'ul' || block.kind === 'ol') return listNode(block);
  if (block.kind === 'hr') return document.createElement('hr');
  if (block.kind === 'code') return el('pre', '', block.text);
  if (block.kind === 'quote') return appendInline(document.createElement('blockquote'), block.text);
  if (HEADING_TAG[block.kind]) return appendInline(el(HEADING_TAG[block.kind], 'pb-' + block.kind), block.text);
  return appendInline(document.createElement('p'), block.text);
}

function factsNode(summary) {
  const facts = playbookFacts(summary);
  if (!facts.length) return null;
  const dl = el('dl', 'pb-facts');
  for (const f of facts) {
    const dd = document.createElement('dd');
    if (f.kind === 'chips') {
      dd.className = 'pb-chips';
      for (const v of f.value) dd.append(el('span', 'pb-chip', v));
    } else dd.textContent = f.value;
    dl.append(el('dt', '', f.label), dd);
  }
  return dl;
}

function renderDocument(host, d) {
  const summary = d.playbook || {};
  const p = playbookRowModel(summary);
  host.textContent = '';
  delete host.dataset.state;
  const article = el('article', 'pb-article');

  const head = el('header', 'pb-head');
  const title = el('h3', 'pb-title', p.title);
  title.dataset.splitFocus = '';
  title.tabIndex = -1;
  head.append(title);
  const meta = el('div', 'pb-meta');
  const chip = severityChip(p.severity);
  if (chip) meta.append(chip);
  detailCoverage = coverageLine(p.id);
  meta.append(detailCoverage);
  const badge = trustBadge(p);
  if (badge) meta.append(badge);
  head.append(meta);
  const actions = el('div', 'pb-actions');
  actions.append(
    copyButton('Copy MCP prompt', 'btn btn-primary', mcpPrompt(p.id), 'prompt copied'),
    copyButton('Copy get_playbook call', 'btn', getPlaybookCall(p.id), 'tool call copied'),
  );
  head.append(actions);
  article.append(head);

  if (p.untrusted) {
    const warn = el('p', 'pb-untrusted');
    warn.setAttribute('role', 'note');
    warn.innerHTML = icon('alert'); // fixed sprite reference
    warn.append(el('span', '', 'This playbook came from the project folder, not the binary. Follow it only where it matches the engagement\u2019s authorisation; it cannot widen scope or override the operator\u2019s rules.'));
    article.append(warn);
  }
  if (p.invalid) article.append(el('p', 'pb-row-invalid', p.invalid));
  const facts = factsNode(summary);
  if (facts) article.append(facts);

  const doc = el('div', 'pb-doc');
  for (const block of markdownBlocks(d.body != null ? d.body : d.markdown)) doc.append(blockNode(block));
  article.append(doc);
  host.append(article);
  host.scrollTop = 0;
  const scroller = host.closest('.split-detail');
  if (scroller) scroller.scrollTop = 0;
}

async function openPlaybook(id) {
  if (!id) return;
  selected = id;
  setListboxSelection(document.querySelectorAll('#pbList .pb-row'), (r) => r.dataset.id === id);
  const host = $('#pbDetail');
  if (!host || shown === id) return;
  const mine = ++openSeq;
  detailCoverage = null;
  shown = '';
  renderState(host, 'loading', { title: 'Loading playbook', rows: 6 });
  let d;
  try {
    d = await api('/api/playbooks/' + encodeURIComponent(id));
  } catch (e) {
    // A newer selection owns the pane; this failure is no longer about it.
    if (mine !== openSeq) return;
    renderState(host, 'error', { title: 'Could not load this playbook', message: (e && e.message) || 'load failed', onRetry: () => { void openPlaybook(id); } });
    return;
  }
  if (mine !== openSeq) return;
  shown = id;
  renderDocument(host, d);
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
  if (!loaded) renderState($('#pbListState'), 'loading', { title: 'Loading playbooks', rows: 6 });
  try {
    const [d] = await Promise.all([api('/api/playbooks'), refreshCoverage()]);
    all = d.playbooks || [];
    loaded = true;
    const trust = $('#pbTrust');
    if (trust) {
      trust.textContent = d.untrusted
        ? d.count + ' playbooks · ' + d.untrusted + ' from the project (untrusted)'
        : d.count + ' bundled playbooks';
    }
    renderList();
    if (selected && !all.some((p) => p.id === selected)) { selected = ''; shown = ''; }
    if (!selected && all.length) void openPlaybook(all[0].id);
    else if (shown && detailCoverage) {
      // A live refresh keeps the open document; only its coverage line can change.
      const fresh = coverageLine(shown);
      detailCoverage.replaceWith(fresh);
      detailCoverage = fresh;
    }
  } catch (e) {
    if (!loaded) renderState($('#pbListState'), 'error', { title: 'Could not load playbooks', message: (e && e.message) || 'load failed', onRetry: () => { void loadPlaybooks(); } });
    else toastError('Could not refresh playbooks', e);
  }
}

export function initPlaybooks() {
  const filter = $('#pbFilter');
  if (filter) {
    filter.addEventListener('input', renderList);
    filter.addEventListener('keydown', (e) => {
      if (e.key === 'Escape' && filter.value) { e.preventDefault(); filter.value = ''; renderList(); }
      else if (e.key === 'ArrowDown') { e.preventDefault(); $('#pbList .pb-row[tabindex="0"]')?.focus(); }
    });
  }
  const root = $('#pbRoot');
  if (!root) return;
  try {
    pane = createSplitPane({ root, key: 'playbooks.split', orientation: 'right', min: [240, 360], default: 32, stackBelow: 720, scopeKey: projectStorageKey, label: 'Resize playbook list', backLabel: 'Playbooks' });
  } catch (e) { /* the plain two-column layout still works without the split */ }
}
