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

// markdownSections splits a rendered playbook into its heading and paragraphs so
// the panel can show a readable document without a markdown parser: each block
// is {kind, text}, kind one of h1|h2|h3|list|code|quote|p.
export function markdownSections(md) {
  const out = [];
  const lines = String(md || '').split('\n');
  let code = null;
  for (const raw of lines) {
    const line = raw.replace(/\s+$/, '');
    if (line.trim().startsWith('```')) {
      if (code === null) {
        code = [];
      } else {
        out.push({ kind: 'code', text: code.join('\n') });
        code = null;
      }
      continue;
    }
    if (code !== null) {
      code.push(line);
      continue;
    }
    if (!line.trim()) continue;
    const h = /^(#{1,3})\s+(.*)$/.exec(line);
    if (h) {
      out.push({ kind: 'h' + h[1].length, text: h[2] });
      continue;
    }
    if (/^\s*[-*]\s+/.test(line) || /^\s*\d+\.\s+/.test(line)) {
      out.push({ kind: 'list', text: line.replace(/^\s*(?:[-*]|\d+\.)\s+/, '') });
      continue;
    }
    if (/^\s*>\s?/.test(line)) {
      out.push({ kind: 'quote', text: line.replace(/^\s*>\s?/, '') });
      continue;
    }
    out.push({ kind: 'p', text: line });
  }
  if (code !== null) out.push({ kind: 'code', text: code.join('\n') });
  return out;
}
