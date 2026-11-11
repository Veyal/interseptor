// playbook-view-model.js — pure logic for the Playbooks panel: which rows match
// a filter, how trust and coverage read, and the MCP invocation to copy.

// playbookRowModel(summary) -> the strings a row shows.
export function playbookRowModel(p) {
  const appliesTo = Array.isArray(p.appliesTo) ? p.appliesTo : [];
  return {
    id: p.id,
    title: p.title || p.id,
    // Trust is the boundary that matters: a project playbook is content dropped
    // into the workspace, not authority over the agent.
    trust: p.trusted ? 'bundled' : 'project · untrusted',
    untrusted: !p.trusted,
    severity: p.severityHint || '',
    appliesTo,
    invalid: p.invalid || '',
  };
}

// filterPlaybooks(list, query) matches id, title, severity and applies_to
// signals, so "/checkout", "graphql" and "ssrf" all narrow the catalogue.
export function filterPlaybooks(list, query) {
  const q = String(query || '').trim().toLowerCase();
  if (!q) return list || [];
  return (list || []).filter((p) => {
    const hay = [p.id, p.title, p.severityHint, (p.appliesTo || []).join(' ')].join(' ').toLowerCase();
    return hay.includes(q);
  });
}

// coverageModel(summary) -> the one-line rollup for a playbook.
export function coverageModel(c) {
  if (!c) return { text: 'not tested yet', tested: false };
  const parts = [];
  if (c.found) parts.push(c.found + ' found');
  if (c.run) parts.push(c.run + ' run');
  if (c.considered) parts.push(c.considered + ' considered');
  if (c.dismissed) parts.push(c.dismissed + ' dismissed');
  if (!parts.length) return { text: 'not tested yet', tested: false };
  return { text: parts.join(' · '), tested: true };
}

// mcpPrompt(id) is what the operator copies to run this playbook in a harness
// that exposes MCP prompts; getPlaybookCall is the tool equivalent everywhere.
export function mcpPrompt(id) {
  return '/playbook-' + id;
}

export function getPlaybookCall(id) {
  return 'get_playbook {"id":"' + id + '"}';
}

// severityClass(hint) -> the capitalised severity word the shared .sev chip
// styles ("High"), or '' when the hint is not one of the five known levels.
export function severityClass(hint) {
  const v = String(hint || '').trim().toLowerCase();
  const known = { critical: 'Critical', high: 'High', medium: 'Medium', low: 'Low', info: 'Info' };
  return known[v] || '';
}

// playbookFacts(summary) -> the frontmatter rows the detail shows as a
// definition list. kind 'chips' is a list of short tokens, 'text' is prose.
export function playbookFacts(p) {
  const arr = (v) => (Array.isArray(v) ? v.map((x) => String(x)).filter(Boolean) : []);
  const out = [];
  const add = (label, kind, value) => { if (kind === 'text' ? value : value.length) out.push({ label, kind, value }); };
  add('Applies to', 'chips', arr(p && p.appliesTo));
  add('Tools', 'chips', arr(p && p.tools));
  add('Evidence', 'text', String((p && p.evidence) || '').trim());
  add('References', 'chips', arr(p && p.references));
  return out;
}

const FENCE = /^\s*(```|~~~)/;
const HEADING = /^(#{1,6})\s+(.*?)\s*#*\s*$/;
const RULE = /^\s*([-*_])(?:\s*\1){2,}\s*$/;
const QUOTE = /^\s*>\s?/;
const ITEM = /^(\s*)([-*+]|\d{1,9}[.)])\s+(.*)$/;
const indentOf = (line) => line.length - line.replace(/^\s+/, '').length;
const startsBlock = (line) => FENCE.test(line) || HEADING.test(line) || RULE.test(line) || QUOTE.test(line) || ITEM.test(line);

// parseList reads one list starting at lines[i]. Indented text continues the
// item above it and a marker indented two or more columns opens a nested list.
// Returns [block, nextIndex]; depth bounds recursion on hostile nesting.
function parseList(lines, i, depth) {
  const first = ITEM.exec(lines[i]);
  const base = first[1].length;
  const ordered = /\d/.test(first[2]);
  const items = [];
  let prevBlank = false;
  while (i < lines.length) {
    const line = lines[i];
    if (!line.trim()) {
      // A blank line ends the list unless more of it follows.
      let j = i + 1;
      while (j < lines.length && !lines[j].trim()) j++;
      if (j >= lines.length) break;
      const next = ITEM.exec(lines[j]);
      const sameList = next && next[1].length - base < 2 && next[1].length >= base && /\d/.test(next[2]) === ordered;
      if (!sameList && indentOf(lines[j]) <= base) break;
      prevBlank = true;
      i = j;
      continue;
    }
    const m = ITEM.exec(line);
    if (m && !RULE.test(line)) {
      const ind = m[1].length;
      if (ind < base) break;
      if (ind - base >= 2 && items.length && depth < 8) {
        const [child, next] = parseList(lines, i, depth + 1);
        items[items.length - 1].children.push(child);
        i = next;
        prevBlank = false;
        continue;
      }
      if (/\d/.test(m[2]) !== ordered) break;
      items.push({ text: m[3].trim(), children: [] });
    } else {
      // Not a marker: text indented under the item, or a lazy continuation line.
      if (indentOf(line) <= base && (prevBlank || startsBlock(line))) break;
      const last = items[items.length - 1];
      last.text += (last.text ? ' ' : '') + line.trim();
    }
    prevBlank = false;
    i++;
  }
  return [{ kind: ordered ? 'ol' : 'ul', items }, i];
}

// markdownBlocks turns a playbook body into the blocks the panel renders with
// DOM nodes (never innerHTML): {kind:'h1'|'h2'|'h3'|'p'|'quote'|'code', text},
// {kind:'ul'|'ol', items:[{text, children:[list…]}]} and {kind:'hr'}. Wrapped
// source lines join into one paragraph, the way markdown reads them.
export function markdownBlocks(md) {
  const lines = String(md || '').replace(/\r\n?/g, '\n').split('\n').map((l) => l.replace(/\s+$/, ''));
  const out = [];
  let i = 0;
  while (i < lines.length) {
    const line = lines[i];
    if (!line.trim()) { i++; continue; }
    const fence = FENCE.exec(line);
    if (fence) {
      const body = [];
      i++;
      while (i < lines.length && !lines[i].trim().startsWith(fence[1])) body.push(lines[i++]);
      i++; // the closing fence, or the end of an unterminated block
      out.push({ kind: 'code', text: body.join('\n') });
      continue;
    }
    const h = HEADING.exec(line);
    if (h) { out.push({ kind: 'h' + Math.min(h[1].length, 3), text: h[2] }); i++; continue; }
    if (RULE.test(line)) { out.push({ kind: 'hr' }); i++; continue; }
    if (QUOTE.test(line)) {
      const body = [];
      while (i < lines.length && QUOTE.test(lines[i])) body.push(lines[i++].replace(QUOTE, '').trim());
      out.push({ kind: 'quote', text: body.filter(Boolean).join(' ') });
      continue;
    }
    if (ITEM.test(line)) {
      const [list, next] = parseList(lines, i, 0);
      out.push(list);
      i = next;
      continue;
    }
    const body = [line.trim()];
    i++;
    while (i < lines.length && lines[i].trim() && !startsBlock(lines[i])) body.push(lines[i++].trim());
    out.push({ kind: 'p', text: body.join(' ') });
  }
  return out;
}

// inlineTokens splits one line of prose into {t:'text'|'code'|'strong'|'em', v}
// tokens; strong and em carry nested tokens in `children` so **`role`** keeps
// its code styling. One left-to-right scan: a delimiter with no closer is
// remembered and never searched for again, so the cost stays linear on
// unbalanced input.
export function inlineTokens(text, depth = 0) {
  const src = String(text == null ? '' : text);
  const out = [];
  const missing = Object.create(null);
  let buf = '';
  const flush = () => { if (buf) { out.push({ t: 'text', v: buf }); buf = ''; } };
  const closer = (delim, from) => {
    if (missing[delim]) return -1;
    const at = src.indexOf(delim, from);
    if (at < 0) missing[delim] = true;
    return at;
  };
  let i = 0;
  while (i < src.length) {
    const ch = src[i];
    if (ch === '`') {
      let n = 1;
      while (src[i + n] === '`') n++;
      const fence = '`'.repeat(n);
      const end = closer(fence, i + n);
      if (end > i + n) {
        flush();
        out.push({ t: 'code', v: src.slice(i + n, end).trim() });
        i = end + n;
        continue;
      }
      buf += fence;
      i += n;
      continue;
    }
    if (ch === '*' && depth < 4) {
      const strong = src[i + 1] === '*';
      const delim = strong ? '**' : '*';
      const open = i + delim.length;
      const end = closer(delim, open);
      // Emphasis hugs its text: "2 * 3 * 4" and a bare list star stay literal.
      if (end > open && !/\s/.test(src[open]) && !/\s/.test(src[end - 1])) {
        flush();
        out.push({ t: strong ? 'strong' : 'em', children: inlineTokens(src.slice(open, end), depth + 1) });
        i = end + delim.length;
        continue;
      }
      buf += delim;
      i = open;
      continue;
    }
    buf += ch;
    i++;
  }
  flush();
  return out;
}
